package answer

import (
	"encoding/json"
	"testing"

	"educoder-cli/internal/problem"
)

func TestFeedbackPreservesObservedEmptyStdout(t *testing.T) {
	feedback := Feedback{Kind: "selftest", Stdin: "input\n", ExpectedStdout: "expected\n", ActualStdout: ""}
	encoded, err := json.Marshal(feedback)
	if err != nil {
		t.Fatal(err)
	}
	assertEmptyStdoutField(t, encoded)

	snapshot := testSnapshot()
	previous := Candidate{Kind: "programming", Language: "cpp", Files: []problem.SolutionFile{{Path: snapshot.Files[0].Path, Content: snapshot.Files[0].CurrentContent}}}
	hash, err := problem.HashFiles(previous.Files)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := buildRequest(Request{Problem: snapshot, Previous: &previous, Feedback: []Feedback{feedback}, RepairHistory: []RepairAttempt{{FilesHash: hash, Candidate: &previous, Feedback: []Feedback{feedback}}}, Profile: RepairProfile()})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Feedback []json.RawMessage `json:"feedback"`
		History  []struct {
			Feedback []json.RawMessage `json:"feedback"`
		} `json:"repair_history"`
	}
	if err := json.Unmarshal([]byte(wire.Messages[1].Content.textOnly()), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Feedback) != 1 || len(payload.History) != 1 || len(payload.History[0].Feedback) != 1 {
		t.Fatalf("feedback missing from repair request: %+v", payload)
	}
	assertEmptyStdoutField(t, payload.Feedback[0])
	assertEmptyStdoutField(t, payload.History[0].Feedback[0])
}

func assertEmptyStdoutField(t *testing.T, encoded []byte) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	actual, present := fields["actual_stdout"]
	if !present || string(actual) != `""` {
		t.Fatalf("observed empty stdout became missing or unknown: %s", encoded)
	}
}
