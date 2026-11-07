package questionbank

import (
	"context"
	"testing"
	"time"
)

func TestFormalUnknownRunRequiresFormalWriteEvidence(t *testing.T) {
	tests := []struct {
		name, status, raw string
		want              bool
	}{
		{
			name:   "objective transport uncertainty",
			status: "uncertain",
			raw:    `{"result":{"kind":"objective","status":"uncertain"}}`,
			want:   true,
		},
		{
			name:   "objective changed before submit",
			status: "verification_unconfirmed",
			raw:    `{"result":{"kind":"objective","status":"verification_unconfirmed","score_check":""}}`,
			want:   false,
		},
		{
			name:   "objective score readback uncertainty",
			status: "verification_unconfirmed",
			raw:    `{"result":{"kind":"objective","status":"verification_unconfirmed","score_check":"unknown"}}`,
			want:   true,
		},
		{
			name:   "programming formal evaluation uncertainty",
			status: "uncertain",
			raw:    `{"result":{"kind":"programming","status":"uncertain","programming":{"evaluation":{"status":"error"}}}}`,
			want:   true,
		},
		{
			name:   "graphic prewrite failure",
			status: "uncertain",
			raw:    `{"result":{"kind":"programming","status":"uncertain","programming":{"evaluation":{"status":"saved_not_submitted"}}}}`,
			want:   false,
		},
		{
			name:   "self test has no formal evaluation",
			status: "uncertain",
			raw:    `{"result":{"kind":"programming","status":"uncertain","programming":{"self_test_status":"unknown"}}}`,
			want:   false,
		},
		{
			name:   "malformed historical record",
			status: "uncertain",
			raw:    `{`,
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formalUnknownRun(tt.status, []byte(tt.raw)); got != tt.want {
				t.Fatalf("formalUnknownRun()=%v want %v", got, tt.want)
			}
		})
	}
}

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
