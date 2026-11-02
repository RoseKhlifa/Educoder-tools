package questionbank

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"educoder-cli/internal/problem"
)

func receiptTestAnswer(t *testing.T, s *Store) int64 {
	t.Helper()
	ctx := context.Background()
	key, err := s.UpsertObjectiveQuestion(ctx, objectiveTestQuestion())
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.AddObjectiveAnswer(ctx, key, "A", "collection", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecordObjectiveValidation(ctx, id, objectiveTestValidation("A")); err != nil {
		t.Fatal(err)
	}
	return id
}

func receiptTestRecord(answerID int64) CollectionReceipt {
	return CollectionReceipt{
		Fingerprint:        problem.HashContent("verified challenge metadata"),
		VerifiedAt:         time.Date(2026, 11, 3, 8, 0, 0, 123456789, time.UTC),
		Questions:          1,
		ObjectiveAnswerIDs: []int64{answerID},
	}
}

func TestCollectionReceiptsDurableOverwriteAndOwnerScope(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "bank.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	r := receiptTestRecord(receiptTestAnswer(t, s))
	owner := "platform:" + strings.Repeat("a", 32)
	otherOwner := strings.Repeat("b", 64)
	for _, scope := range []struct {
		owner, course string
		homework      int64
	}{
		{owner, "course-a", 10},
		{otherOwner, "course-a", 10},
		{owner, "course-b", 10},
		{owner, "course-a", 11},
	} {
		if err = s.PutCollectionReceipt(ctx, scope.owner, scope.course, scope.homework, 100, r); err != nil {
			t.Fatal(err)
		}
	}
	updated := r
	updated.Fingerprint = problem.HashContent("changed observed metadata")
	updated.VerifiedAt = updated.VerifiedAt.Add(time.Minute)
	if err = s.PutCollectionReceipt(ctx, owner, "course-a", 10, 100, updated); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.CollectionReceipts(ctx, owner, "course-a", 10)
	if err != nil || len(got) != 1 {
		t.Fatalf("persisted receipt missing: %+v %v", got, err)
	}
	if got[100].Fingerprint != updated.Fingerprint || !got[100].VerifiedAt.Equal(updated.VerifiedAt) || got[100].Questions != 1 || len(got[100].ObjectiveAnswerIDs) != 1 || got[100].ObjectiveAnswerIDs[0] != r.ObjectiveAnswerIDs[0] {
		t.Fatalf("receipt was not replaced durably: %+v", got[100])
	}
	other, err := s.CollectionReceipts(ctx, otherOwner, "course-a", 10)
	if err != nil || len(other) != 1 || other[100].Fingerprint != r.Fingerprint {
		t.Fatalf("another owner's receipt changed: %+v %v", other, err)
	}
	if err = s.DeleteCollectionReceipt(ctx, owner, "course-a", 10, 100); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteCollectionReceipt(ctx, owner, "course-a", 10, 100); err != nil {
		t.Fatal(err)
	}
	got, err = s.CollectionReceipts(ctx, owner, "course-a", 10)
	if err != nil || len(got) != 0 {
		t.Fatalf("deleted receipt remains: %+v %v", got, err)
	}
	for _, scope := range []struct {
		owner, course string
		homework      int64
	}{{otherOwner, "course-a", 10}, {owner, "course-b", 10}, {owner, "course-a", 11}} {
		got, err = s.CollectionReceipts(ctx, scope.owner, scope.course, scope.homework)
		if err != nil || len(got) != 1 || got[100].Fingerprint != r.Fingerprint {
			t.Fatalf("deletion escaped requested scope: %+v %v", got, err)
		}
	}
}

func TestCollectionReceiptsRejectInvalidAndIncompleteCheckpoints(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	owner := "platform:" + strings.Repeat("a", 32)
	r := receiptTestRecord(receiptTestAnswer(t, s))
	if err = s.PutCollectionReceipt(ctx, owner, "course", 1, 2, r); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*CollectionReceipt)
	}{
		{"missing fingerprint", func(r *CollectionReceipt) { r.Fingerprint = "" }},
		{"uppercase fingerprint", func(r *CollectionReceipt) { r.Fingerprint = strings.Repeat("A", 64) }},
		{"negative questions", func(r *CollectionReceipt) { r.Questions = -1 }},
		{"zero questions", func(r *CollectionReceipt) { r.Questions = 0 }},
		{"partial evidence", func(r *CollectionReceipt) { r.Questions = 2 }},
		{"missing evidence", func(r *CollectionReceipt) { r.ObjectiveAnswerIDs = nil }},
		{"invalid evidence ID", func(r *CollectionReceipt) { r.ObjectiveAnswerIDs = []int64{0} }},
		{"absent evidence record", func(r *CollectionReceipt) { r.ObjectiveAnswerIDs = []int64{99999} }},
		{"mixed evidence", func(r *CollectionReceipt) { r.CodeCandidateID = 1 }},
		{"negative code ID", func(r *CollectionReceipt) { r.CodeCandidateID = -1 }},
		{"zero time", func(r *CollectionReceipt) { r.VerifiedAt = time.Time{} }},
		{"epoch time", func(r *CollectionReceipt) { r.VerifiedAt = time.Unix(0, 0) }},
		{"overflowing time", func(r *CollectionReceipt) { r.VerifiedAt = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bad := r
			test.mutate(&bad)
			if err := s.PutCollectionReceipt(ctx, owner, "course", 1, 2, bad); err == nil {
				t.Fatal("invalid checkpoint accepted")
			}
		})
	}
	for _, scope := range []struct {
		owner, course       string
		homework, challenge int64
	}{{"user@example.test", "course", 1, 2}, {owner, "", 1, 2}, {owner, "../course", 1, 2}, {owner, "course", 0, 2}, {owner, "course", 1, 0}} {
		if err = s.PutCollectionReceipt(ctx, scope.owner, scope.course, scope.homework, scope.challenge, r); err == nil {
			t.Fatalf("invalid write scope accepted: %+v", scope)
		}
		if err = s.DeleteCollectionReceipt(ctx, scope.owner, scope.course, scope.homework, scope.challenge); err == nil {
			t.Fatalf("invalid delete scope accepted: %+v", scope)
		}
		if scope.challenge > 0 {
			if _, err = s.CollectionReceipts(ctx, scope.owner, scope.course, scope.homework); err == nil {
				t.Fatalf("invalid query scope accepted: %+v", scope)
			}
		}
	}
	got, err := s.CollectionReceipts(ctx, owner, "course", 1)
	if err != nil || len(got) != 1 || got[2].Fingerprint != r.Fingerprint {
		t.Fatalf("invalid writes changed the existing checkpoint: %+v %v", got, err)
	}
}

func TestCollectionReceiptsRecheckObjectiveBankEvidence(t *testing.T) {
	for _, invalidation := range []struct {
		name string
		sql  string
	}{
		{"validation removed", `DELETE FROM objective_answer_verifications`},
		{"resource unavailable", `UPDATE objective_questions SET resource_complete=0`},
		{"corrupt checkpoint references", `UPDATE collection_receipts SET objective_answer_ids='invalid json'`},
		{"checkpoint references missing record", `UPDATE collection_receipts SET objective_answer_ids='[99999]'`},
	} {
		t.Run(invalidation.name, func(t *testing.T) {
			ctx := context.Background()
			s, err := Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			owner := strings.Repeat("a", 64)
			r := receiptTestRecord(receiptTestAnswer(t, s))
			if err = s.PutCollectionReceipt(ctx, owner, "course", 1, 2, r); err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.ExecContext(ctx, invalidation.sql); err != nil {
				t.Fatal(err)
			}
			got, err := s.CollectionReceipts(ctx, owner, "course", 1)
			if err != nil || len(got) != 0 {
				t.Fatalf("invalidated bank proof still skipped collection: %+v %v", got, err)
			}
		})
	}
}

func TestCollectionReceiptsRequirePortableFormalCodeEvidence(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ref, err := s.UpsertSnapshot(ctx, bankSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.AddCandidate(ctx, ref, CandidateInput{Files: []problem.SolutionFile{{Path: "src/main.cpp", Content: "answer"}}, Source: "collection"})
	if err != nil {
		t.Fatal(err)
	}
	owner := strings.Repeat("c", 64)
	r := receiptTestRecord(0)
	r.ObjectiveAnswerIDs = nil
	r.CodeCandidateID = c.ID
	if err = s.PutCollectionReceipt(ctx, owner, "course", 1, 2, r); err == nil {
		t.Fatal("unverified code accepted")
	}
	v := Validation{CandidateID: c.ID, Kind: "formal_workspace", Result: "passed", FilesHash: c.FilesHash, SavedFilesHash: c.FilesHash, SubmittedFilesHash: c.FilesHash, RevisionHash: ref.RevisionHash, ContractHash: ref.ContractHash, Terminal: true, Complete: true, TestCount: 1, PassedCount: 1}
	if err = s.RecordValidation(ctx, v); err != nil {
		t.Fatal(err)
	}
	if err = s.PutCollectionReceipt(ctx, owner, "course", 1, 2, r); err == nil {
		t.Fatal("workspace-only code accepted")
	}
	v.Kind = "formal"
	if err = s.RecordValidation(ctx, v); err != nil {
		t.Fatal(err)
	}
	if err = s.PutCollectionReceipt(ctx, owner, "course", 1, 2, r); err != nil {
		t.Fatal(err)
	}
	got, err := s.CollectionReceipts(ctx, owner, "course", 1)
	if err != nil || len(got) != 1 || got[2].CodeCandidateID != c.ID {
		t.Fatalf("formally verified code missing: %+v %v", got, err)
	}
	v.Result, v.PassedCount = "failed", 0
	if err = s.RecordValidation(ctx, v); err != nil {
		t.Fatal(err)
	}
	got, err = s.CollectionReceipts(ctx, owner, "course", 1)
	if err != nil || len(got) != 0 {
		t.Fatalf("rejected candidate still skipped collection: %+v %v", got, err)
	}
}
