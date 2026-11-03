package web

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"educoder-cli/internal/jobs"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

func seedPreformalRecovery(t *testing.T, f *platformHTTPFixture, original jobs.Job, challengeID int64, formal bool) {
	t.Helper()
	ctx := context.Background()
	ref, err := f.bank.UpsertSnapshot(ctx, problem.Snapshot{Kind: "programming", Title: "test", StatementMarkdown: "test", EditMode: "single_file", Files: []problem.File{{Path: "main.py", Language: "python", CurrentContent: "starter", StarterProvenance: "unknown"}}})
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.bank.AddCandidate(ctx, ref, questionbank.CandidateInput{Files: []problem.SolutionFile{{Path: "main.py", Content: "print(1)"}}, Source: "model"})
	if err != nil {
		t.Fatal(err)
	}
	key := original.Request.IdempotencyKey
	if original.Request.Scope == "course" {
		hash := sha256.Sum256([]byte(fmt.Sprintf("%s:C:42", key)))
		key = fmt.Sprintf("course-%x", hash)
	}
	key = fmt.Sprintf("%s:%d", key, challengeID)
	id := "preformal-" + original.ID
	raw, _ := json.Marshal(map[string]any{"owner": problem.HashContent(f.alice.Owner()), "mode": "submit", "target": map[string]any{"Course": "C", "HomeworkID": 42, "Game": "G"}, "snapshot_ref": ref, "result": map[string]any{"run_id": id, "status": "uncertain", "candidate_id": c.ID, "code_hash": c.FilesHash, "self_test_status": "unknown"}})
	if err = f.bank.PutRun(ctx, questionbank.RunRecord{ID: id, Owner: problem.HashContent(f.alice.Owner()), IdempotencyKey: key, Status: "uncertain", Data: raw, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err = f.bank.RecordValidation(ctx, questionbank.Validation{CandidateID: c.ID, Kind: "selftest", Result: "unknown", FilesHash: c.FilesHash, RevisionHash: ref.RevisionHash, ContractHash: ref.ContractHash, RunID: id}); err != nil {
		t.Fatal(err)
	}
	if formal {
		if err = f.bank.RecordValidation(ctx, questionbank.Validation{CandidateID: c.ID, Kind: "formal", Result: "unknown", FilesHash: c.FilesHash, RevisionHash: ref.RevisionHash, ContractHash: ref.ContractHash, RunID: id}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRecoveryCanRestartProvenPreformalFailureWithoutReplayingUnknownBuild(t *testing.T) {
	for _, formal := range []bool{false, true} {
		t.Run(fmt.Sprint(formal), func(t *testing.T) {
			var runs atomic.Int32
			f := newPlatformHTTPFixture(t, func(context.Context, EducoderClient, string, jobs.Request, func(jobs.Event)) (jobs.Outcome, error) {
				runs.Add(1)
				return jobs.Outcome{Status: jobs.Succeeded}, nil
			})
			installRecoveryClient(t, f, f.aliceClient, newRecoveryFixtureClient())
			original := recoveryOriginal(t, f, jobs.Request{Mode: "submit", Scope: "course", Course: "C", IdempotencyKey: "original-course"}, jobs.Failed, `{"status":"needs_service","total_homeworks":19,"completed_homeworks":13,"homeworks":[{"course":"C","homework_id":42,"result":{"status":"uncertain","levels":[{"challenge_id":2,"status":"uncertain","reason_code":"editor_changed"}]}}]}`)
			seedPreformalRecovery(t, f, original, 2, formal)
			response := f.request(t, f.aliceClient, "POST", "/api/jobs/"+original.ID+"/recover", "{}")
			if response.StatusCode != 202 {
				t.Fatal(response.Body)
			}
			child := f.waitJob(t, decodeJob(t, response).ID)
			if formal {
				if runs.Load() != 0 || child.ErrorCode != "RECOVERY_RECHECK_REQUIRED" {
					t.Fatalf("unknown formal build replayed: %+v", child)
				}
			} else if runs.Load() != 1 || child.Status != jobs.Succeeded {
				t.Fatalf("preformal failure remained blocked: %+v", child)
			}
		})
	}
}

func TestLegacyRecoveryProjectionAndMissingChallengeCannotUsePreformalEvidence(t *testing.T) {
	legacy := jobs.Job{Status: jobs.Failed, Request: jobs.Request{IdempotencyKey: "recover-job:" + strings.Repeat("a", 32)}, Result: json.RawMessage(`{"reason_code":"recovery_recheck_required"}`)}
	public := toConsumerJob(legacy)
	if public.Recovery == nil || public.Recovery.State != "blocked" {
		t.Fatal("old recovery lost its status")
	}
	called := false
	err := recoveryPreflightWithEvidence(context.Background(), newRecoveryFixtureClient(), jobs.Job{Status: jobs.Uncertain, Request: jobs.Request{Scope: "homework", Course: "C", HomeworkID: 42}, Result: json.RawMessage(`{"levels":[{"challenge_id":999,"status":"uncertain"}]}`)}, func(context.Context, string, int64, int64) (bool, error) { called = true; return true, nil })
	if err == nil || called {
		t.Fatal("unknown roster identity used unrelated local proof")
	}
}
