package questionbank

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"educoder-cli/internal/answer"
)

func objectiveCompatSnapshot() answer.ObjectiveSnapshot {
	return answer.ObjectiveSnapshot{Kind: "objective", Title: "Traversal", Questions: []answer.ObjectiveQuestion{objectiveTestQuestion()}}
}

func openObjectiveFixture(t *testing.T, path, schema string) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(initialMigration + schema); err != nil {
		db.Close()
		t.Fatal(err)
	}
	return &Store{db: db}
}

func objectiveFixtureAnswer(t *testing.T, s *Store, value string) (string, int64) {
	t.Helper()
	ctx := context.Background()
	key, err := s.UpsertObjectiveQuestion(ctx, objectiveTestQuestion())
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.AddObjectiveAnswer(ctx, key, value, "fixture", "model")
	if err != nil {
		t.Fatal(err)
	}
	return key, id
}

func TestPublishedObjectiveLayoutUpgradesAndCoexistsWithSingleQuestions(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "published.sqlite")
	s := openObjectiveFixture(t, path, objectiveSchema)
	snapshot := objectiveCompatSnapshot()
	key, err := s.PutObjectiveSnapshot(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := s.AddObjectiveCandidate(ctx, key, snapshot, answer.ObjectiveCandidate{Kind: "objective", Answers: []string{"A"}}, "published")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecordObjectiveVerification(ctx, candidate.ID, "published-run", "full_score", "13.30", "13.3"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		s, err = Open(path)
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.FindObjectiveVerified(ctx, key)
		if err != nil || got.ID != candidate.ID || got.Source != "published" {
			t.Fatalf("published candidate lost after upgrade: %+v %v", got, err)
		}
		_, id := objectiveFixtureAnswer(t, s, "A")
		if err = s.RecordObjectiveValidation(ctx, id, objectiveTestValidation("A")); err != nil {
			t.Fatal(err)
		}
		var groupCount, questionCount int
		if err = s.db.QueryRow(`SELECT count(*) FROM objective_verifications`).Scan(&groupCount); err != nil {
			t.Fatal(err)
		}
		if err = s.db.QueryRow(`SELECT count(*) FROM objective_answer_verifications`).Scan(&questionCount); err != nil {
			t.Fatal(err)
		}
		if groupCount != 1 || questionCount != i+1 {
			t.Fatalf("independent verification records changed: group=%d single=%d", groupCount, questionCount)
		}
		if err = s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLegacyIndividualObjectiveLayoutPreservesVerifiedAndFailedHistory(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "individual.sqlite")
	legacySchema := strings.ReplaceAll(objectiveMigration, "objective_answer_verifications", "objective_verifications")
	s := openObjectiveFixture(t, path, legacySchema)
	key, id := objectiveFixtureAnswer(t, s, "A")
	_, wrongID := objectiveFixtureAnswer(t, s, "B")
	for _, item := range []struct {
		id       int64
		answer   string
		verified bool
	}{{id, "A", true}, {wrongID, "B", false}} {
		v := objectiveTestValidation(item.answer)
		v.Passed, v.FullScore = item.verified, item.verified
		v.Feedback = "legacy evidence for " + item.answer
		if _, err := s.db.Exec(`INSERT INTO objective_verifications(answer_id,verified,evidence_json,created_at) VALUES(?,?,?,?)`, item.id, item.verified, marshal(v), now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.LookupObjectiveAnswers(ctx, key)
		if err != nil || len(got) != 2 || !got[0].Reusable || got[0].ID != id || got[1].Reusable || got[1].ID != wrongID {
			t.Fatalf("individual history changed during migration: %+v %v", got, err)
		}
		if len(got[0].Validations) != 1 || got[0].Validations[0].Feedback != "legacy evidence for A" || len(got[1].Validations) != 1 || got[1].Validations[0].Feedback != "legacy evidence for B" {
			t.Fatalf("individual evidence lost during migration: %+v", got)
		}
		var count int
		if err = s.db.QueryRow(`SELECT count(*) FROM objective_verifications`).Scan(&count); err != nil || count != 0 {
			t.Fatalf("published verification schema not created: %d %v", count, err)
		}
		if err = s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLegacyIndividualMigrationMergesBothTablesWithoutDroppingCollidingIDs(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mixed.sqlite")
	s := openObjectiveFixture(t, path, objectiveMigration)
	if _, err := s.db.Exec(`CREATE TABLE objective_verifications (id INTEGER PRIMARY KEY,answer_id INTEGER NOT NULL REFERENCES objective_answers(id),verified INTEGER NOT NULL,evidence_json TEXT NOT NULL,created_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	key, id := objectiveFixtureAnswer(t, s, "A")
	for _, table := range []string{"objective_verifications", "objective_answer_verifications"} {
		v := objectiveTestValidation("A")
		v.Feedback = table
		if _, err := s.db.Exec(`INSERT INTO `+table+`(id,answer_id,verified,evidence_json,created_at) VALUES(7,?,?,?,?)`, id, true, marshal(v), now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.LookupObjectiveAnswers(ctx, key)
	if err != nil || len(got) != 1 || len(got[0].Validations) != 2 || !got[0].Reusable {
		t.Fatalf("colliding evidence IDs discarded records: %+v %v", got, err)
	}
	if got[0].Validations[0].Feedback == got[0].Validations[1].Feedback {
		t.Fatal("one evidence row was copied over the other")
	}
}

func TestObjectiveWholeTaskLookupBridgesVerifiedSingleQuestionAnswers(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snapshot := objectiveCompatSnapshot()
	second := objectiveTestQuestion()
	second.Question = "Which traversal visits the root last?"
	second.Position = 19
	snapshot.Questions = append(snapshot.Questions, second)
	key, err := s.PutObjectiveSnapshot(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	_, firstID := objectiveFixtureAnswer(t, s, "A")
	if err = s.RecordObjectiveValidation(ctx, firstID, objectiveTestValidation("A")); err != nil {
		t.Fatal(err)
	}
	secondKey, err := s.UpsertObjectiveQuestion(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := s.AddObjectiveAnswer(ctx, secondKey, "B", "model", "flash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.FindObjectiveVerified(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("incomplete verified set became a cache hit: %v", err)
	}
	if err = s.RecordObjectiveValidation(ctx, secondID, objectiveTestValidation("B")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		got, err := s.FindObjectiveVerified(ctx, key)
		if err != nil || got.ID == 0 || got.Source != "question_bank" || strings.Join(got.Candidate.Answers, ",") != "A,B" {
			t.Fatalf("single-question answers were not bridged in order: %+v %v", got, err)
		}
	}
	var count int
	if err = s.db.QueryRow(`SELECT count(*) FROM objective_verifications`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("bridge fabricated whole-task score evidence: %d %v", count, err)
	}
}

func TestObjectiveWholeTaskLookupDoesNotBridgeImagesWithoutContentHashes(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snapshot := objectiveCompatSnapshot()
	snapshot.Questions[0].Question += ` ![tree](https://example.org/tree.png)`
	key, err := s.PutObjectiveSnapshot(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	questionKey, err := s.UpsertObjectiveQuestion(ctx, snapshot.Questions[0])
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.AddObjectiveAnswer(ctx, questionKey, "A", "fixture", "model")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecordObjectiveValidation(ctx, id, objectiveTestValidation("A")); err != nil {
		t.Fatal(err)
	}
	candidate, err := s.AddObjectiveCandidate(ctx, key, snapshot, answer.ObjectiveCandidate{Kind: "objective", Answers: []string{"A"}}, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecordObjectiveVerification(ctx, candidate.ID, "legacy-image", "full_score", "1", "1"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.FindObjectiveVerified(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a URL-only image snapshot was reused without a content hash: %v", err)
	}
}
