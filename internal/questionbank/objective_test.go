package questionbank

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
)

func objectiveTestQuestion() answer.ObjectiveQuestion {
	return answer.ObjectiveQuestion{Position: 7, Question: "Which traversal visits the root first?", QuestionType: 0, Category: 1, Options: []answer.ObjectiveOption{{Label: "A", Text: "Preorder", Position: 0}, {Label: "B", Text: "Postorder", Position: 1}}}
}

func objectiveTestValidation(answerText string) ObjectiveValidation {
	return ObjectiveValidation{Passed: true, Confirmed: true, FullScore: true, Score: "13.30", MaxScore: "13.3", AnswerHash: problem.HashContent(answerText), ScopeHash: problem.HashContent("observed submission scope")}
}

func TestObjectiveQuestionIdentityAndImmutableAnswerHistory(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "bank.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	q := objectiveTestQuestion()
	key, err := s.UpsertObjectiveQuestion(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	wrongID, err := s.AddObjectiveAnswer(ctx, key, "B", "model", "flash")
	if err != nil {
		t.Fatal(err)
	}
	failure := objectiveTestValidation("B")
	failure.Score = "0"
	if err = s.RecordObjectiveValidation(ctx, wrongID, failure); err != nil {
		t.Fatal(err)
	}
	id, err := s.AddObjectiveAnswer(ctx, key, "a", "model", "flash")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecordObjectiveValidation(ctx, id, objectiveTestValidation("A")); err != nil {
		t.Fatal(err)
	}
	duplicate, err := s.AddObjectiveAnswer(ctx, key, " A ", "another source", "pro")
	if err != nil || duplicate != id {
		t.Fatalf("candidate mutated or duplicated: %d %v", duplicate, err)
	}
	q.Position = 42
	q.Options[0], q.Options[1] = q.Options[1], q.Options[0]
	otherKey, err := s.UpsertObjectiveQuestion(ctx, q)
	if err != nil || otherKey != key {
		t.Fatalf("local order or question position changed identity: %s %v", otherKey, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.LookupObjectiveAnswers(ctx, otherKey)
	if err != nil || len(got) != 2 {
		t.Fatalf("answers not persisted: %+v %v", got, err)
	}
	if got[0].Answer != "A" || !got[0].Verified || !got[0].Reusable || got[0].Source != "model" || got[0].Model != "flash" || len(got[0].Validations) != 1 {
		t.Fatalf("verified candidate provenance was changed: %+v", got[0])
	}
	if got[1].Verified || got[1].Reusable || len(got[1].Validations) != 1 {
		t.Fatalf("failed candidate was lost or promoted: %+v", got[1])
	}
	q.Options[0].Text = "Different option text"
	changedKey, err := s.UpsertObjectiveQuestion(ctx, q)
	if err != nil || changedKey == key {
		t.Fatalf("option text change reused old identity: %s %v", changedKey, err)
	}
	q = objectiveTestQuestion()
	q.Category = 2
	kindKey, err := s.UpsertObjectiveQuestion(ctx, q)
	if err != nil || kindKey == key {
		t.Fatalf("single/multiple change reused identity: %s %v", kindKey, err)
	}
	q = objectiveTestQuestion()
	q.Options[0].Text, q.Options[1].Text = q.Options[1].Text, q.Options[0].Text
	orderKey, err := s.UpsertObjectiveQuestion(ctx, q)
	if err != nil || orderKey == key {
		t.Fatalf("semantic option reorder reused answer letters: %s %v", orderKey, err)
	}
}

func TestObjectiveValidationDoesNotPromotePartialUnknownOrMismatchedEvidence(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	key, err := s.UpsertObjectiveQuestion(ctx, objectiveTestQuestion())
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.AddObjectiveAnswer(ctx, key, "A", "model", "flash")
	if err != nil {
		t.Fatal(err)
	}
	for _, score := range []string{"13.29999999999999999", "", "NaN", "0"} {
		v := objectiveTestValidation("A")
		v.Score = score
		if err = s.RecordObjectiveValidation(ctx, id, v); err != nil {
			t.Fatal(err)
		}
	}
	v := objectiveTestValidation("A")
	v.Confirmed = false
	if err = s.RecordObjectiveValidation(ctx, id, v); err != nil {
		t.Fatal(err)
	}
	v = objectiveTestValidation("A")
	v.Score, v.MaxScore = "0", "0"
	if err = s.RecordObjectiveValidation(ctx, id, v); err != nil {
		t.Fatal(err)
	}
	got, err := s.LookupObjectiveAnswers(ctx, key)
	if err != nil || len(got) != 1 || got[0].Verified || len(got[0].Validations) != 6 {
		t.Fatalf("unproven answer promoted or history lost: %+v %v", got, err)
	}
	v = objectiveTestValidation("B")
	if err = s.RecordObjectiveValidation(ctx, id, v); err == nil {
		t.Fatal("wrong answer hash accepted")
	}
	v = objectiveTestValidation("A")
	v.ScopeHash = ""
	if err = s.RecordObjectiveValidation(ctx, id, v); err == nil {
		t.Fatal("missing submission scope accepted")
	}
	v = objectiveTestValidation("A")
	v.FullScore, v.PerQuestionCorrect = false, true
	v.Score, v.MaxScore = "", ""
	if err = s.RecordObjectiveValidation(ctx, id, v); err != nil {
		t.Fatal(err)
	}
	got, err = s.LookupObjectiveAnswers(ctx, key)
	if err != nil || !got[0].Verified {
		t.Fatalf("explicit individual correct result not promoted: %+v %v", got, err)
	}
}

func TestObjectiveImagesNeedContentHashesBeforeAutomaticReuse(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	q := objectiveTestQuestion()
	q.Question += ` ![tree](https://example.org/tree.png)`
	key, err := s.UpsertObjectiveQuestion(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.AddObjectiveAnswer(ctx, key, "A", "model", "flash")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecordObjectiveValidation(ctx, id, objectiveTestValidation("A")); err != nil {
		t.Fatal(err)
	}
	got, err := s.LookupObjectiveAnswers(ctx, key)
	if err != nil || !got[0].Verified || got[0].Reusable || got[0].ResourceComplete {
		t.Fatalf("unresolved image treated as complete: %+v %v", got, err)
	}
	resources := []ObjectiveResource{{Locator: "https://example.org/tree.png", SHA256: problem.HashContent("actual image bytes")}}
	completeKey, err := s.UpsertObjectiveQuestionWithResources(ctx, q, resources)
	if err != nil || completeKey == key {
		t.Fatalf("image bytes not included in key: %s %v", completeKey, err)
	}
	completeID, err := s.AddObjectiveAnswer(ctx, completeKey, "A", "model", "flash")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecordObjectiveValidation(ctx, completeID, objectiveTestValidation("A")); err != nil {
		t.Fatal(err)
	}
	got, err = s.LookupObjectiveAnswers(ctx, completeKey)
	if err != nil || !got[0].Reusable {
		t.Fatalf("inspected image answer not reusable: %+v %v", got, err)
	}
	resources[0].SHA256 = problem.HashContent("changed image bytes")
	changedKey, err := s.UpsertObjectiveQuestionWithResources(ctx, q, resources)
	if err != nil || changedKey == completeKey {
		t.Fatal("changed image content reused previous identity")
	}
}

func TestObjectiveMigrationUpgradesExistingProgrammingBank(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "existing.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(initialMigration); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO problems(kind,title,created_at) VALUES('programming','legacy',?)", now()); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var count int
	if err = s.db.QueryRowContext(ctx, "SELECT count(*) FROM problems WHERE title='legacy'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("legacy data lost: %d %v", count, err)
	}
	if err = s.db.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations WHERE version IN (1,2)").Scan(&count); err != nil || count != 2 {
		t.Fatalf("migration versions missing: %d %v", count, err)
	}
	if _, err = s.UpsertObjectiveQuestion(ctx, objectiveTestQuestion()); err != nil {
		t.Fatal(err)
	}
}

func TestUsageForRunSurvivesReopenAndPreservesUnknownReservation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "usage.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PutRun(ctx, RunRecord{ID: "usage-run", Owner: "opaque-owner", Status: "running", Data: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	for _, u := range []Usage{{RunID: "usage-run", Provider: "test", Model: "flash", PricingVersion: "v1", Status: "settled", InputTokens: 20, OutputTokens: 10, CostMicroUSD: 23}, {RunID: "usage-run", Provider: "test", Model: "flash", PricingVersion: "v1", Status: "billed_unknown", ReservedMicroUSD: 100}} {
		if err = s.RecordUsage(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.UsageForRun(ctx, "usage-run")
	if err != nil || len(got) != 2 || got[0].CostMicroUSD != 23 || got[1].ReservedMicroUSD != 100 || got[1].Status != "billed_unknown" {
		t.Fatalf("durable usage lost: %+v %v", got, err)
	}
}
