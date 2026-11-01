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
