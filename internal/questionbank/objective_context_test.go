package questionbank

import (
	"context"
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
)

func completeObjectiveInput() ObjectiveQuestionInput {
	return ObjectiveQuestionInput{
		Question: objectiveTestQuestion(),
		Context: ObjectiveQuestionContext{
			Kind: "objective", Title: "Traversal", Statement: "Choose the correct traversal.", Complete: true,
		},
	}
}

func TestObjectiveContextFingerprintFailsClosedForIncompleteAndChangedContext(t *testing.T) {
	input := completeObjectiveInput()
	completeKey, reusable, err := ObjectiveQuestionKeyWithContext(input)
	if err != nil || !reusable || completeKey == "" {
		t.Fatalf("complete context rejected: key=%q reusable=%v err=%v", completeKey, reusable, err)
	}

	missing := input
	missing.Context.Complete = false
	missingKey, reusable, err := ObjectiveQuestionKeyWithContext(missing)
	if err != nil || reusable || missingKey == "" || missingKey == completeKey {
		t.Fatalf("incomplete context was reusable or collided: key=%q reusable=%v err=%v", missingKey, reusable, err)
	}

	changed := input
	changed.Context.Statement = "Choose the incorrect traversal."
	changedKey, reusable, err := ObjectiveQuestionKeyWithContext(changed)
	if err != nil || !reusable || changedKey == completeKey {
		t.Fatalf("shared context change reused old identity: key=%q reusable=%v err=%v", changedKey, reusable, err)
	}

	mediaMissing := input
	mediaMissing.Context.Statement += ` ![diagram](https://example.org/diagram.png)`
	mediaKey, reusable, err := ObjectiveQuestionKeyWithContext(mediaMissing)
	if err != nil || reusable || mediaKey == completeKey {
		t.Fatalf("unread shared media was reusable or collided: key=%q reusable=%v err=%v", mediaKey, reusable, err)
	}
}

func TestObjectiveContextLookupRequiresCompleteContextAndLatestEvidence(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	input := completeObjectiveInput()
	key, reusable, err := s.UpsertObjectiveQuestionWithContext(ctx, input)
	if err != nil || !reusable {
		t.Fatalf("context question not stored as reusable: key=%q reusable=%v err=%v", key, reusable, err)
	}
	id, err := s.AddObjectiveAnswer(ctx, key, "A", "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	full := objectiveTestValidation("A")
	full.ScopeHash = problem.HashContent("complete context scope")
	if err = s.RecordObjectiveValidation(ctx, id, full); err != nil {
		t.Fatal(err)
	}
	got, err := s.LookupObjectiveAnswersWithContext(ctx, key)
	if err != nil || len(got) != 1 || !got[0].Reusable {
		t.Fatalf("exact complete context was not reusable: %+v err=%v", got, err)
	}

	failed := full
	failed.Passed = false
	failed.Confirmed = true
	failed.FullScore = false
	failed.PerQuestionCorrect = false
	failed.Score = "0"
	failed.MaxScore = full.MaxScore
	failed.Feedback = "later partial verification"
	if err = s.RecordObjectiveValidation(ctx, id, failed); err != nil {
		t.Fatal(err)
	}
	got, err = s.LookupObjectiveAnswersWithContext(ctx, key)
	if err != nil || len(got) != 1 || got[0].Reusable || got[0].Verified {
		t.Fatalf("later failed evidence did not invalidate full score: %+v err=%v", got, err)
	}

	incomplete := input
	incomplete.Context.Complete = false
	incompleteKey, incompleteReusable, err := s.UpsertObjectiveQuestionWithContext(ctx, incomplete)
	if err != nil || incompleteReusable || incompleteKey == key {
		t.Fatalf("incomplete context storage contract broken: key=%q reusable=%v err=%v", incompleteKey, incompleteReusable, err)
	}
	incompleteID, err := s.AddObjectiveAnswer(ctx, incompleteKey, "A", "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecordObjectiveValidation(ctx, incompleteID, full); err != nil {
		t.Fatal(err)
	}
	got, err = s.LookupObjectiveAnswersWithContext(ctx, incompleteKey)
	if err != nil || len(got) != 0 {
		t.Fatalf("incomplete context exposed answers: %+v err=%v", got, err)
	}
}

func TestObjectiveContextFingerprintRejectsMissingQuestionData(t *testing.T) {
	input := completeObjectiveInput()
	input.Question.Question = ""
	if key, reusable, err := ObjectiveQuestionKeyWithContext(input); err == nil || key != "" || reusable {
		t.Fatalf("missing question data was accepted: key=%q reusable=%v err=%v", key, reusable, err)
	}

	input = completeObjectiveInput()
	input.Question.Options = []answer.ObjectiveOption{{Label: "A", Text: "", Position: 0}}
	if key, reusable, err := ObjectiveQuestionKeyWithContext(input); err != nil || key == "" || reusable {
		t.Fatalf("missing option data was not retained as non-reusable: key=%q reusable=%v err=%v", key, reusable, err)
	}
}
