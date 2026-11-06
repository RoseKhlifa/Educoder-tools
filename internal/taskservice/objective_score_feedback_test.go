package taskservice

import (
	"testing"

	"educoder-cli/internal/answer"
)

func scoreFeedbackSnapshot() answer.ObjectiveSnapshot {
	return answer.ObjectiveSnapshot{Kind: "objective", Title: "t", Questions: []answer.ObjectiveQuestion{
		{Position: 1, Question: "one", Category: 1, Options: []answer.ObjectiveOption{{Label: "A", Text: "a"}, {Label: "B", Text: "b"}}},
		{Position: 2, Question: "two", Category: 1, Options: []answer.ObjectiveOption{{Label: "A", Text: "a"}, {Label: "B", Text: "b"}, {Label: "C", Text: "c"}}},
	}}
}

func TestObjectiveScoreFeedbackUsesOneQuestionAndLocksByDelta(t *testing.T) {
	snapshot := scoreFeedbackSnapshot()
	candidate := answer.ObjectiveCandidate{Kind: "objective", Answers: []string{"A", "A"}, RankedAnswers: []answer.ObjectiveRankedSlot{
		{QuestionPosition: 1, Choices: []string{"A", "B"}, Confidence: []float64{0.9, 0.1}},
		{QuestionPosition: 2, Choices: []string{"A", "B", "C"}, Confidence: []float64{0.2, 0.5, 0.3}},
	}}
	search, ok := newObjectiveScoreFeedback(snapshot, candidate, nil)
	if !ok {
		t.Fatal("ranked candidate was rejected")
	}
	search.observe("1", "2")
	first, ok := search.next(snapshot)
	if !ok || first.Answers[0] != "A" || first.Answers[1] != "B" {
		t.Fatalf("weakest question was not probed first: %+v ok=%v", first, ok)
	}
	search.observe("2", "2")
	second, ok := search.next(snapshot)
	if !ok || second.Answers[0] != "B" || second.Answers[1] != "B" {
		t.Fatalf("score increase did not lock changed answer: %+v ok=%v", second, ok)
	}
	search.observe("1", "2")
	if _, ok := search.next(snapshot); ok {
		t.Fatal("search emitted a candidate after all questions were classified")
	}
}

func TestObjectiveScoreFeedbackEqualScoreAdvancesAlternative(t *testing.T) {
	snapshot := scoreFeedbackSnapshot()
	candidate := answer.ObjectiveCandidate{Kind: "objective", Answers: []string{"A", "A"}, RankedAnswers: []answer.ObjectiveRankedSlot{
		{QuestionPosition: 1, Choices: []string{"A", "B"}},
		{QuestionPosition: 2, Choices: []string{"A", "B", "C"}},
	}}
	search, ok := newObjectiveScoreFeedback(snapshot, candidate, nil)
	if !ok {
		t.Fatal("ranked candidate was rejected")
	}
	search.observe("1", "2")
	first, _ := search.next(snapshot)
	search.observe("1", "2") // neither first answer nor probe improved score
	second, ok := search.next(snapshot)
	if !ok || second.Answers[0] != "A" || second.Answers[1] != "B" {
		t.Fatalf("equal score did not advance same question: %+v ok=%v", second, ok)
	}
	if first.Answers[0] != "B" || first.Answers[1] != "A" {
		t.Fatalf("unexpected first probe: %+v", first)
	}
}

func TestObjectiveScoreFeedbackAbandonsUnknownOrChangedMaximum(t *testing.T) {
	snapshot := scoreFeedbackSnapshot()
	candidate := answer.ObjectiveCandidate{Kind: "objective", Answers: []string{"A", "A"}, RankedAnswers: []answer.ObjectiveRankedSlot{
		{QuestionPosition: 1, Choices: []string{"A", "B"}},
		{QuestionPosition: 2, Choices: []string{"A", "B"}},
	}}
	search, ok := newObjectiveScoreFeedback(snapshot, candidate, nil)
	if !ok {
		t.Fatal("ranked candidate was rejected")
	}
	search.observe("?", "2")
	if _, ok := search.next(snapshot); ok {
		t.Fatal("unknown score must not drive a probe")
	}
	search, _ = newObjectiveScoreFeedback(snapshot, candidate, nil)
	search.observe("1", "2")
	_, _ = search.next(snapshot)
	search.observe("1", "3")
	if _, ok := search.next(snapshot); ok {
		t.Fatal("changed maximum must stop score inference")
	}
}
