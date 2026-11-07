package taskservice

import (
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/questionbank"
)

func scoreFeedbackSnapshot() answer.ObjectiveSnapshot {
	return answer.ObjectiveSnapshot{Kind: "objective", Title: "t", Questions: []answer.ObjectiveQuestion{
		{Position: 1, Question: "one", Category: 1, Options: []answer.ObjectiveOption{{Label: "A", Text: "a", Position: 0}, {Label: "B", Text: "b", Position: 1}}},
		{Position: 2, Question: "two", Category: 1, Options: []answer.ObjectiveOption{{Label: "A", Text: "a", Position: 0}, {Label: "B", Text: "b", Position: 1}, {Label: "C", Text: "c", Position: 2}}},
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
	search.observe(candidate, "1", "2")
	first, ok := search.next(snapshot)
	if !ok || first.Answers[0] != "A" || first.Answers[1] != "B" {
		t.Fatalf("weakest question was not probed first: %+v ok=%v", first, ok)
	}
	search.observe(first, "2", "2")
	second, ok := search.next(snapshot)
	if !ok || second.Answers[0] != "B" || second.Answers[1] != "B" {
		t.Fatalf("score increase did not lock changed answer: %+v ok=%v", second, ok)
	}
	search.observe(second, "1", "2")
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
	search.observe(candidate, "1", "2")
	first, _ := search.next(snapshot)
	search.observe(first, "1", "2") // neither first answer nor probe improved score
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
	search.observe(candidate, "?", "2")
	if _, ok := search.next(snapshot); ok {
		t.Fatal("unknown score must not drive a probe")
	}
	search, _ = newObjectiveScoreFeedback(snapshot, candidate, nil)
	search.observe(candidate, "1", "2")
	probe, _ := search.next(snapshot)
	search.observe(probe, "1", "3")
	if _, ok := search.next(snapshot); ok {
		t.Fatal("changed maximum must stop score inference")
	}
}

func TestObjectiveScoreFeedbackRejectsUnsubmittedBaselineAndProbe(t *testing.T) {
	snapshot := scoreFeedbackSnapshot()
	candidate := answer.ObjectiveCandidate{Kind: "objective", Answers: []string{"A", "A"}, RankedAnswers: []answer.ObjectiveRankedSlot{
		{QuestionPosition: 1, Choices: []string{"A", "B"}},
		{QuestionPosition: 2, Choices: []string{"A", "B"}},
	}}
	search, ok := newObjectiveScoreFeedback(snapshot, candidate, nil)
	if !ok {
		t.Fatal("ranked candidate was rejected")
	}
	// A stale model candidate must not establish a baseline for this search.
	stale := candidate
	stale.Answers = []string{"B", "A"}
	search.observe(stale, "1", "2")
	if !search.done {
		t.Fatal("stale baseline was accepted")
	}

	search, _ = newObjectiveScoreFeedback(snapshot, candidate, nil)
	search.observe(candidate, "1", "2")
	probe, ok := search.next(snapshot)
	if !ok {
		t.Fatal("expected probe")
	}
	// A score for another vector is incomparable and must stop inference.
	wrong := probe
	wrong.Answers = append([]string(nil), probe.Answers...)
	wrong.Answers[0] = "A"
	if objectiveAnswersEqual(wrong, probe) {
		t.Fatal("test setup did not change probe")
	}
	search.observe(wrong, "2", "2")
	if !search.done {
		t.Fatal("unsubmitted probe was accepted")
	}
}

func TestObjectiveScoreFeedbackSeedsFromConfirmedHistoricalFailure(t *testing.T) {
	snapshot := scoreFeedbackSnapshot()
	candidate := answer.ObjectiveCandidate{Kind: "objective", Answers: []string{"A", "A"}, RankedAnswers: []answer.ObjectiveRankedSlot{
		{QuestionPosition: 1, Choices: []string{"A", "B"}},
		{QuestionPosition: 2, Choices: []string{"A", "B", "C"}},
	}}
	history := newObjectiveAttemptHistory(15)
	history.restore([]questionbank.ObjectiveFailedAttempt{{
		Candidate: questionbank.ObjectiveCandidate{Candidate: candidate},
		Score:     "1", MaxScore: "2",
	}})
	search, ok := history.scoreFeedback(snapshot, nil)
	if !ok || search.baseline == nil {
		t.Fatal("confirmed ranked history was not accepted as a baseline")
	}
	probe, ok := search.next(snapshot)
	if !ok || probe.Answers[0] != "B" || probe.Answers[1] != "A" || len(probe.RankedAnswers) != 0 {
		t.Fatalf("historical baseline did not produce a clean one-question probe: %+v ok=%v", probe, ok)
	}
	if _, ok := history.scoreFeedback(snapshot, []string{"B", ""}); ok {
		t.Fatal("historical score was reused after a conflicting verified lock")
	}
	if _, ok := history.scoreFeedback(snapshot, []string{"A", ""}); !ok {
		t.Fatal("matching verified lock rejected a comparable history baseline")
	}
}
