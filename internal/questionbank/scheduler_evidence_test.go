package questionbank

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"educoder-cli/internal/problem"
)

func TestReusableEvidenceRevisionForHomeworkIgnoresFailedRunUpdates(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	owner := "owner-hash"
	target := `{"course":"C","homework_id":7,"game":"G"}`
	now := time.Now().UTC()
	if _, err = store.db.ExecContext(ctx, `INSERT INTO solver_runs(id,owner,idempotency_key,status,data_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, "failed-run", owner, "k", "uncertain", `{"target":`+target+`}`, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	first, err := store.ReusableEvidenceRevisionForHomework(ctx, owner, "C", 7)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.ExecContext(ctx, `UPDATE solver_runs SET updated_at=? WHERE id=?`, now.Add(time.Hour).Format(time.RFC3339Nano), "failed-run"); err != nil {
		t.Fatal(err)
	}
	second, err := store.ReusableEvidenceRevisionForHomework(ctx, owner, "C", 7)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("failed/running run update reopened fuse: first=%s second=%s", first, second)
	}
}

// An untried classmate's full-score answer on a level the owner attempted
// reopens the no-progress fuse once; after it has been evaluated the revision
// returns to exactly what it was without it, so it never reopens again for the
// same answer, and homeworks without such answers keep their old revision.
func TestReusableEvidenceRevisionReopensOnceForUntriedObservedAnswer(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, attempted := externalSnapshot(t, store, "attempted.sh")
	_, untouched := externalSnapshot(t, store, "untouched.sh")
	run := fmt.Sprintf(`{"target":{"Course":"C","HomeworkID":7,"Game":"G"},"snapshot_ref":{"revision_hash":%q,"contract_hash":%q}}`, attempted.RevisionHash, attempted.ContractHash)
	if _, err = store.db.ExecContext(ctx, `INSERT INTO solver_runs(id,owner,idempotency_key,status,data_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, "stalled", "owner-hash", "k", "generation_stalled", run, now, now); err != nil {
		t.Fatal(err)
	}
	base, err := store.ReusableEvidenceRevisionForHomework(ctx, "owner-hash", "C", 7)
	if err != nil {
		t.Fatal(err)
	}
	if want := sha256.Sum256([]byte(fmt.Sprint([3]int64{}))); base != hex.EncodeToString(want[:]) {
		t.Fatal("a homework without observed answers changed its revision")
	}
	observe := func(ref Ref, content string) Candidate {
		c, err := store.AddCandidate(ctx, ref, CandidateInput{Source: "collection:platform_observed", Files: []problem.SolutionFile{{Path: "attempted.sh", Content: content}}})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	_, _ = store.AddCandidate(ctx, untouched, CandidateInput{Source: "collection:platform_observed", Files: []problem.SolutionFile{{Path: "untouched.sh", Content: "echo other"}}})
	if got, _ := store.ReusableEvidenceRevisionForHomework(ctx, "owner-hash", "C", 7); got != base {
		t.Fatal("an observed answer for a level this homework never attempted reopened the fuse")
	}
	c := observe(attempted, "echo observed")
	reopened, err := store.ReusableEvidenceRevisionForHomework(ctx, "owner-hash", "C", 7)
	if err != nil || reopened == base {
		t.Fatalf("an untried observed answer did not reopen the fuse: %v", err)
	}
	if _, err = store.db.ExecContext(ctx, `INSERT INTO verifications(solution_id,evidence_json,created_at) VALUES(?,?,?)`, c.ID, `{"result":"failed","run_id":"x"}`, now); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.ReusableEvidenceRevisionForHomework(ctx, "owner-hash", "C", 7); got != base {
		t.Fatal("an evaluated observed answer kept the fuse open")
	}
}
