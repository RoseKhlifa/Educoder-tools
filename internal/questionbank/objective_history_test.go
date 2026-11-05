package questionbank

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"reflect"
	"testing"

	"educoder-cli/internal/answer"
)

func failureHistorySnapshot() answer.ObjectiveSnapshot {
	q := objectiveTestQuestion()
	q.Options = append(q.Options, answer.ObjectiveOption{Label: "C", Text: "Inorder", Position: 2}, answer.ObjectiveOption{Label: "D", Text: "Level order", Position: 3})
	q2 := q
	q2.Position, q2.Question = 8, "Which traversal visits the root last?"
	return answer.ObjectiveSnapshot{Kind: "objective", Questions: []answer.ObjectiveQuestion{q, q2}}
}

func addFailedVector(t *testing.T, store *Store, key string, snapshot answer.ObjectiveSnapshot, vector []string, score, maximum, status string) ObjectiveCandidate {
	t.Helper()
	candidate, err := store.AddObjectiveCandidate(context.Background(), key, snapshot, answer.ObjectiveCandidate{Kind: "objective", Answers: vector}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordObjectiveVerification(context.Background(), candidate.ID, "fixture-run", status, score, maximum); err != nil {
		t.Fatal(err)
	}
	return candidate
}

func TestObjectiveFailedAttemptsSurviveReopenAndUseLatestExactSnapshotEvidence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "history.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := failureHistorySnapshot()
	key, err := store.PutObjectiveSnapshot(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	older := addFailedVector(t, store, key, snapshot, []string{"A", "A"}, "1", "2", "not_full_score")
	latest := addFailedVector(t, store, key, snapshot, []string{"B", "A"}, "0", "2", "not_full_score")
	// A later observation of the same vector replaces its old score and place
	// in chronology; score changes never turn into per-question evidence.
	if err := store.RecordObjectiveVerification(ctx, older.ID, "later-run", "not_full_score", "1.5", "2"); err != nil {
		t.Fatal(err)
	}
	other := snapshot
	other.Questions = append([]answer.ObjectiveQuestion(nil), snapshot.Questions...)
	other.Questions[0].Question = "Changed question with the same positions"
	otherKey, err := store.PutObjectiveSnapshot(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	addFailedVector(t, store, otherKey, other, []string{"C", "D"}, "0", "2", "not_full_score")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	history, err := store.ObjectiveFailedAttempts(ctx, key, 10)
	if err != nil || len(history) != 2 || history[0].Candidate.ID != latest.ID || history[1].Candidate.ID != older.ID || history[1].Score != "1.5" || history[1].MaxScore != "2" {
		t.Fatalf("wrong durable order, score or snapshot: %+v err=%v", history, err)
	}
	one, err := store.ObjectiveFailedAttempts(ctx, key, 1)
	if err != nil || len(one) != 1 || one[0].Candidate.ID != older.ID {
		t.Fatalf("latest bound lost: %+v err=%v", one, err)
	}
}

func TestObjectiveFailedAttemptsExcludeUnknownFullScoreAndMalformedEvidence(t *testing.T) {
	ctx := context.Background()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot := failureHistorySnapshot()
	key, err := store.PutObjectiveSnapshot(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	good := addFailedVector(t, store, key, snapshot, []string{"A", "A"}, "1.5", "2", "not_full_score")
	addFailedVector(t, store, key, snapshot, []string{"A", "B"}, "1", "2", "uncertain")
	addFailedVector(t, store, key, snapshot, []string{"A", "C"}, "1", "2", "unconfirmed")
	passed := addFailedVector(t, store, key, snapshot, []string{"A", "D"}, "1", "2", "not_full_score")
	if err := store.RecordObjectiveVerification(ctx, passed.ID, "verified-run", "full_score", "2", "2"); err != nil {
		t.Fatal(err)
	}
	for i, score := range []string{"2", "3", "-1", "unknown", "NaN"} {
		addFailedVector(t, store, key, snapshot, []string{string(rune('B' + i/4)), string(rune('A' + i%4))}, score, "2", "not_full_score")
	}
	badHash := addFailedVector(t, store, key, snapshot, []string{"C", "B"}, "1", "2", "not_full_score")
	if _, err := store.db.Exec(`UPDATE objective_candidates SET candidate_hash='wrong' WHERE id=?`, badHash.ID); err != nil {
		t.Fatal(err)
	}
	badVector := addFailedVector(t, store, key, snapshot, []string{"C", "C"}, "1", "2", "not_full_score")
	raw := `{"kind":"objective","answers":["Z","A"]}`
	h := sha256.Sum256([]byte(raw))
	if _, err := store.db.Exec(`UPDATE objective_candidates SET candidate_json=?,candidate_hash=? WHERE id=?`, raw, hex.EncodeToString(h[:]), badVector.ID); err != nil {
		t.Fatal(err)
	}
	history, err := store.ObjectiveFailedAttempts(ctx, key, 10)
	if err != nil || len(history) != 1 || history[0].Candidate.ID != good.ID {
		t.Fatalf("nonfailure or invalid evidence restored: %+v err=%v", history, err)
	}
}

func TestObjectiveFailedAttemptsBoundDistinctVectorsWithoutChangingVerifiedLookup(t *testing.T) {
	ctx := context.Background()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot := failureHistorySnapshot()
	key, err := store.PutObjectiveSnapshot(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		addFailedVector(t, store, key, snapshot, []string{string(rune('A' + i/4)), string(rune('A' + i%4))}, "1", "2", "not_full_score")
	}
	// Cosmetic differences in accepted legacy candidate JSON do not consume
	// another slot or make a known failed vector eligible for resubmission.
	addFailedVector(t, store, key, snapshot, []string{" C ", "D"}, "1.25", "2", "not_full_score")
	history, err := store.ObjectiveFailedAttempts(ctx, key, 999)
	if err != nil || len(history) != 10 || !reflect.DeepEqual(history[len(history)-1].Candidate.Candidate.Answers, []string{" C ", "D"}) || history[len(history)-1].Score != "1.25" {
		t.Fatalf("history not bounded to latest distinct vectors: %+v err=%v", history, err)
	}
	passed := addFailedVector(t, store, key, snapshot, []string{"D", "D"}, "2", "2", "full_score")
	verified, err := store.FindObjectiveVerified(ctx, key)
	if err != nil || verified.ID != passed.ID {
		t.Fatalf("failure history changed verified cache: %+v err=%v", verified, err)
	}
}
