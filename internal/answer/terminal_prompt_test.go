package answer

import (
	"encoding/json"
	"strings"
	"testing"

	"educoder-cli/internal/problem"
)

func TestTerminalPromptSeparatesWorkspaceScriptFromPlaceholder(t *testing.T) {
	snapshot := problem.Snapshot{Kind: "programming", Title: "Create experiment files", StatementMarkdown: "Create the requested directory and file.", Files: []problem.File{{Path: "placeholder.py", Language: "shell", CurrentContent: ""}}, EditMode: "terminal", EnvironmentContract: []string{"terminal:posix-shell/v1"}, PublicTests: []problem.PublicTest{{}}}
	wire, err := buildRequest(Request{Problem: snapshot, Profile: DefaultProfile()})
	if err != nil {
		t.Fatal(err)
	}
	system := wire.Messages[0].Content.textOnly()
	for _, requirement := range []string{"complete POSIX shell script", "never written into the named placeholder editor file", "working directory", "exit-status check only", "formal evaluation checks workspace state", "Printing expected output with echo is not evidence", "Do not read hidden tests", "conceal an unknown or failed operation"} {
		if !strings.Contains(system, requirement) {
			t.Errorf("terminal prompt missing %q", requirement)
		}
	}
	var payload struct {
		EditMode    string               `json:"edit_mode"`
		Files       []problem.File       `json:"files"`
		PublicTests []problem.PublicTest `json:"public_tests"`
	}
	if err := json.Unmarshal([]byte(wire.Messages[1].Content.textOnly()), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.EditMode != "terminal" || len(payload.Files) != 1 || payload.Files[0].Path != "placeholder.py" || payload.Files[0].Language != "shell" || payload.Files[0].CurrentContent != "" || len(payload.PublicTests) != 1 || payload.PublicTests[0].ExpectedStdout != "" {
		t.Fatalf("terminal payload contract lost: %+v", payload)
	}
	code, err := buildRequest(Request{Problem: testSnapshot(), Profile: DefaultProfile()})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(code.Messages[0].Content.textOnly(), "Terminal mode overrides") {
		t.Fatal("terminal semantics leaked into ordinary editor tasks")
	}
}
