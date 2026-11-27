package answer

import (
	"strings"
	"testing"
)

// The compact shape a model used in production: one list of choices per
// question, in question order. It is read as the slot form and every choice is
// still validated; any other length or shape stays invalid.
func TestObjectivePositionalRankedAnswersAreAccepted(t *testing.T) {
	s := objectiveFixture()
	candidate, err := parseObjectiveCandidate(s, `{"kind":"objective","ranked_answers":[["B","A"],["AB","A"]]}`)
	if err != nil || strings.Join(candidate.Answers, ",") != "B,AB" || len(candidate.RankedAnswers) != 2 || candidate.RankedAnswers[1].QuestionPosition != 2 || strings.Join(candidate.RankedAnswers[1].Choices, ",") != "AB,A" {
		t.Fatalf("positional ranked answers: candidate=%+v err=%v", candidate, err)
	}
	for _, raw := range []string{
		`{"kind":"objective","ranked_answers":[["B","A"]]}`,
		`{"kind":"objective","ranked_answers":[["B","A"],[]]}`,
		`{"kind":"objective","ranked_answers":[["B","Z"],["AB"]]}`,
		`{"kind":"objective","ranked_answers":[["B"],["AB"]],"note":"x"}`,
	} {
		if _, err := parseObjectiveCandidate(s, raw); err == nil {
			t.Fatalf("accepted an inexact positional shape: %s", raw)
		}
	}
}
