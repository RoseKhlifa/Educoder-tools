package answer

import (
	"encoding/json"
	"strings"
	"testing"

	"educoder-cli/internal/problem"
)

func TestRepairPromptAllowsBrokenEditorRepairAndBindsSourceHistory(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.Files[0] = problem.File{Path: "include/stack.h", Filename: "stack.h", Extension: ".h", Language: "cpp", CurrentContent: "struct StackA {}; struct StackB {}; void f(StackA); void g(StackB b){f(b);}", StarterProvenance: "unknown", EntrySignature: "void solve()"}
	previous := Candidate{Kind: "programming", Language: "cpp", Files: []problem.SolutionFile{{Path: "include/stack.h", Content: snapshot.Files[0].CurrentContent}}}
	hash, err := problem.HashFiles(previous.Files)
	if err != nil {
		t.Fatal(err)
	}
	feedback := []Feedback{{Kind: "selftest", Message: "cannot convert StackB to StackA", Stdin: "1", ExpectedStdout: "3", ActualStdout: "compiler error"}}
	wire, err := buildRequest(Request{Problem: snapshot, Previous: &previous, Feedback: feedback, RepairHistory: []RepairAttempt{{FilesHash: hash, Candidate: &previous, Feedback: feedback}}, Profile: RepairProfile()})
	if err != nil {
		t.Fatal(err)
	}
	system := wire.Messages[0].Content.textOnly()
	for _, required := range []string{"unknown starter_provenance", "may already contain broken code", "Repair any necessary content inside allowed files", "explicitly declared fixed regions", "function call signatures", "Do not add a main function to a header", "do not switch between unchanged previously failed candidates", "Recheck every public sample"} {
		if !strings.Contains(system, required) {
			t.Errorf("repair instruction missing: %s", required)
		}
	}
	var payload struct {
		History   []RepairAttempt `json:"repair_history"`
		Files     []problem.File  `json:"files"`
		Contracts []fileContract  `json:"file_contract"`
	}
	if err := json.Unmarshal([]byte(wire.Messages[1].Content.textOnly()), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.History) != 1 || payload.History[0].FilesHash != hash || payload.History[0].Candidate.Files[0].Content != previous.Files[0].Content || payload.History[0].Feedback[0].Message != feedback[0].Message {
		t.Fatalf("compiler evidence was detached from failed source: %+v", payload.History)
	}
	if payload.Files[0].StarterProvenance != "unknown" || payload.Files[0].EntrySignature != "void solve()" || payload.Contracts[0].Extension != ".h" || payload.Contracts[0].Path != "include/stack.h" {
		t.Fatalf("file contract lost while repairing editor: %+v", payload)
	}
}

func TestRepairPromptRejectsHistorySourceHashMismatch(t *testing.T) {
	snapshot := testSnapshot()
	previous := Candidate{Kind: "programming", Language: "cpp", Files: []problem.SolutionFile{{Path: snapshot.Files[0].Path, Content: "int main(){return 1;}"}}}
	_, err := buildRequest(Request{Problem: snapshot, RepairHistory: []RepairAttempt{{FilesHash: "other", Candidate: &previous, Feedback: []Feedback{{Kind: "selftest", Message: "compile error"}}}}, Profile: RepairProfile()})
	if err == nil || !strings.Contains(err.Error(), "invalid_repair_history") {
		t.Fatalf("mismatched source was accepted as bound history: %v", err)
	}
}

func TestHTMLRepairKeepsConflictingRequirementsAndBoundPassingAttributeFeedback(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.StatementMarkdown = "## Task\nUse src=\"https://example.invalid/a\".\n## Programming requirement\nUse src=\"https://example.invalid/b\".\n## Target effect\n![Example](/attachments/sample/example.png)\n```html\n<img src=\"/attachments/sample/example.png\" alt=\"sample\">\n```"
	snapshot.Files = []problem.File{{Path: "step/test.html", Filename: "test.html", Extension: ".html", Language: "html", CurrentContent: "<!DOCTYPE html>\n<body>\n<!-- Begin -->\n<!-- End -->\n</body>", StarterProvenance: "unknown"}}
	previous := Candidate{Kind: "programming", Language: "html", Files: []problem.SolutionFile{{Path: "step/test.html", Content: `<body><img src="https://example.invalid/a" alt="sample"/></body>`}}}
	hash, _ := problem.HashFiles(previous.Files)
	feedback := []Feedback{{Kind: "selftest", ExpectedStdout: "alt passed\nsrc passed\n", ActualStdout: "alt passed\n"}}
	wire, err := buildRequest(Request{Problem: snapshot, Previous: &previous, Feedback: feedback, RepairHistory: []RepairAttempt{{FilesHash: hash, Candidate: &previous, Feedback: feedback}}, Profile: DefaultProfile()})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Statement string          `json:"statement_markdown"`
		Files     []problem.File  `json:"files"`
		Previous  *Candidate      `json:"previous_candidate"`
		Feedback  []Feedback      `json:"feedback"`
		History   []RepairAttempt `json:"repair_history"`
	}
	if err := json.Unmarshal([]byte(wire.Messages[1].Content.textOnly()), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Statement != snapshot.StatementMarkdown || len(payload.Files) != 1 || payload.Files[0].CurrentContent != snapshot.Files[0].CurrentContent || payload.Previous == nil || payload.Previous.Files[0] != previous.Files[0] || len(payload.History) != 1 || payload.History[0].FilesHash != hash || payload.History[0].Candidate.Files[0] != previous.Files[0] || payload.History[0].Feedback[0] != feedback[0] || len(payload.Feedback) != 1 || payload.Feedback[0] != feedback[0] {
		t.Fatal("conflicting literals, current source or source-bound attribute feedback lost")
	}
	for _, reference := range []string{`src="https://example.invalid/a"`, `src="https://example.invalid/b"`, `src="/attachments/sample/example.png"`} {
		if !strings.Contains(payload.Statement, reference) {
			t.Errorf("supplied reference was normalized or removed: %s", reference)
		}
	}
	system := wire.Messages[0].Content.textOnly()
	for _, instruction := range []string{"task prose, programming requirements, existing allowed-file content, target-effect examples, and code samples together", "Preserve exact supplied relative or absolute literal forms", "do not silently resolve relative URLs, change schemes, hosts or routes", "Keep attributes whose checks already passed", "source-bound repair_history", "Do not alternate between attribute values already disproved"} {
		if !strings.Contains(system, instruction) {
			t.Errorf("HTML repair guidance missing: %s", instruction)
		}
	}
	if wire.ResponseFormat.Type != "json_object" || !strings.Contains(system, "The object fields must be kind, language, and files only.") {
		t.Fatal("HTML evidence guidance changed the JSON output contract")
	}
}

func TestHTMLAttributeGuidanceDoesNotChangeNonHTMLPrompt(t *testing.T) {
	snapshot := testSnapshot()
	baseline, err := buildRequest(Request{Problem: snapshot, Profile: DefaultProfile()})
	if err != nil {
		t.Fatal(err)
	}
	snapshot.StatementMarkdown += "\nA quoted example contains <img src=\"/attachments/sample/example.png\">."
	withExample, err := buildRequest(Request{Problem: snapshot, Profile: DefaultProfile()})
	if err != nil {
		t.Fatal(err)
	}
	if baseline.Messages[0].Content.textOnly() != withExample.Messages[0].Content.textOnly() || strings.Contains(withExample.Messages[0].Content.textOnly(), "For HTML attribute tasks") || baseline.ResponseFormat != withExample.ResponseFormat {
		t.Fatal("HTML example text changed a non-HTML system prompt or output contract")
	}
}
