package answer

import (
	"encoding/json"
	"testing"
)

func TestObjectiveFocusedReviewRetainsExactOriginalContext(t *testing.T) {
	full := objectiveFixture()
	selected := full
	selected.Questions = []ObjectiveQuestion{full.Questions[1]}
	wire, err := buildObjectiveRequest(ObjectiveRequest{Problem: selected, Context: &full, Profile: RepairProfile()})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Questions []ObjectiveQuestion `json:"questions"`
		Context   []ObjectiveQuestion `json:"context_questions"`
	}
	if err := json.Unmarshal([]byte(wire.Messages[1].Content.textOnly()), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Questions) != 1 || payload.Questions[0].Position != 2 || len(payload.Context) != 2 || payload.Context[0].Question != full.Questions[0].Question {
		t.Fatalf("original roster or focused target lost: %+v", payload)
	}
	// Only the requested slot is part of the strict response contract.
	if _, err := parseObjectiveCandidate(selected, `{"kind":"objective","answers":["AB"]}`); err != nil {
		t.Fatal(err)
	}
	if _, err := parseObjectiveCandidate(selected, `{"kind":"objective","answers":["A","AB"]}`); err == nil {
		t.Fatal("accepted extra sibling answer")
	}
}

func TestObjectiveFocusedReviewRejectsChangedContext(t *testing.T) {
	for _, change := range []string{"title", "statement", "question", "option", "position", "duplicate"} {
		t.Run(change, func(t *testing.T) {
			full := objectiveFixture()
			selected := full
			selected.Questions = []ObjectiveQuestion{full.Questions[0]}
			selected.Questions[0].Options = append([]ObjectiveOption(nil), full.Questions[0].Options...)
			switch change {
			case "title":
				selected.Title += "changed"
			case "statement":
				selected.Statement += "changed"
			case "question":
				selected.Questions[0].Question += "changed"
			case "option":
				selected.Questions[0].Options[0].Text += "changed"
			case "position":
				selected.Questions[0].Position = 3
			case "duplicate":
				full.Questions[1].Position = 1
			}
			if _, err := buildObjectiveRequest(ObjectiveRequest{Problem: selected, Context: &full, Profile: RepairProfile()}); err == nil {
				t.Fatal("accepted mismatched original context")
			}
		})
	}
}
