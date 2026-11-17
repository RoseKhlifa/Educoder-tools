package answer

import (
	"encoding/json"
	"strings"
	"testing"

	"educoder-cli/internal/problem"
)

func TestProgrammingPromptIncludesDriverWithoutGrantingWriteAccess(t *testing.T) {
	s := testSnapshot()
	s.Files[0] = problem.File{Path: "src/answer.h", Language: "cpp", CurrentContent: "void solve();"}
	s.ReadOnlyFiles = []problem.ReadOnlyFile{{Path: "src/main.cpp", Language: "cpp", Content: "#include \"answer.h\"\nint main(){solve();}"}}
	wire, err := buildRequest(Request{Problem: s, Profile: DefaultProfile()})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Files       []problem.File         `json:"files"`
		ReadOnly    []problem.ReadOnlyFile `json:"read_only_files"`
		Allowed     []string               `json:"allowed_files"`
		ContextHash string                 `json:"context_hash"`
	}
	if err := json.Unmarshal([]byte(wire.Messages[1].Content.textOnly()), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Files) != 1 || len(payload.Allowed) != 1 || payload.Allowed[0] != "src/answer.h" || len(payload.ReadOnly) != 1 || payload.ReadOnly[0].Content != s.ReadOnlyFiles[0].Content || payload.ContextHash == "" {
		t.Fatalf("payload=%+v", payload)
	}
	if !strings.Contains(wire.Messages[0].Content.textOnly(), "read_only_files") || !strings.Contains(wire.Messages[0].Content.textOnly(), "never return or modify") {
		t.Fatal("read-only contract not explicit")
	}
	c := Candidate{Kind: "programming", Language: "cpp", Files: []problem.SolutionFile{{Path: "src/main.cpp", Content: "int main(){return 0;}"}}}
	if ValidateCandidate(s, c) == nil {
		t.Fatal("read-only driver granted submission access")
	}
}
