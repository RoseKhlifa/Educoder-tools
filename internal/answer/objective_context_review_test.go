package answer

import (
	"encoding/json"
	"reflect"
	"strings"
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

func TestObjectiveFocusedReviewMakesOutputSlotsExplicit(t *testing.T) {
	full := objectiveFixture()
	full.Questions[1].Position = 7
	selected := full
	selected.Questions = []ObjectiveQuestion{full.Questions[1]}
	wire, err := buildObjectiveRequest(ObjectiveRequest{Problem: selected, Context: &full, Profile: RepairProfile()})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Output objectiveOutputContract `json:"output_contract"`
	}
	if err := json.Unmarshal([]byte(wire.Messages[1].Content.textOnly()), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Output.AnswerCount != 1 || !reflect.DeepEqual(payload.Output.QuestionPositions, []int{7}) || len(payload.Output.AnswerSlots) != 1 {
		t.Fatalf("ambiguous output count or positions: %+v", payload.Output)
	}
	slot := payload.Output.AnswerSlots[0]
	if slot.ArrayIndex != 0 || slot.QuestionPosition != 7 || slot.Shape != "concatenated_labels_in_one_string" || !reflect.DeepEqual(slot.AllowedLabels, []string{"A", "B"}) {
		t.Fatalf("multiple-choice output is not one explicit slot: %+v", slot)
	}
	if !strings.HasPrefix(wire.Messages[0].Content.textOnly(), "REQUEST-SPECIFIC OUTPUT: answers must contain exactly 1 strings") || !strings.Contains(wire.Messages[0].Content.textOnly(), "not indexed by original position") {
		t.Fatal("focused output count is not salient")
	}
	fullWire, err := buildObjectiveRequest(ObjectiveRequest{Problem: full, Profile: DefaultProfile()})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(fullWire.Messages[1].Content.textOnly()), &payload); err != nil || payload.Output.AnswerCount != 2 || payload.Output.AnswerSlots[0].Shape != "one_label_in_one_string" {
		t.Fatalf("full roster output shape incorrect: %+v err=%v", payload, err)
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
