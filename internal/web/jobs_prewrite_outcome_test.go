package web

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"educoder-cli/internal/jobs"
	"educoder-cli/internal/taskservice"
)

func TestKnownPreFormalHomeworkFailureAfterSubmissionRemainsRetryable(t *testing.T) {
	for _, status := range []string{"prewrite_error", "saved_not_submitted", "save_unverified", "save_error", "editor_changed"} {
		t.Run(status, func(t *testing.T) {
			batch := &taskservice.BatchResult{
				Status: status, ReasonCode: status, TotalLevels: 2, CompletedLevels: 1, FailedLevels: 1,
				Levels: []taskservice.BatchLevel{{ChallengeID: 1, Status: "passed"}, {ChallengeID: 2, Status: status, ReasonCode: status}},
			}
			outcome, err := homeworkOutcome(batch, errors.New("graphic mutation stopped before formal evaluation"))
			if err != nil || outcome.Status != jobs.Failed {
				t.Fatalf("outcome=%+v err=%v", outcome, err)
			}
			_, _, manager := jobsHTTP(t, jobs.Config{Workers: 1}, nil)
			job, _, err := manager.Submit(context.Background(), "owner", jobs.Request{Course: "C", HomeworkID: 1, Scope: "homework", Mode: "submit"}, func(_ context.Context, _ jobs.Request, report func(jobs.Event)) (jobs.Outcome, error) {
				report(jobs.Event{Phase: "submitting", Message: "第一关已提交，第二关尚未提交"})
				return homeworkOutcome(batch, errors.New("graphic mutation stopped before formal evaluation"))
			})
			if err != nil {
				t.Fatal(err)
			}
			for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
				got, err := manager.Get(context.Background(), "owner", job.ID)
				if err != nil {
					t.Fatal(err)
				}
				if got.Status != jobs.Queued && got.Status != jobs.Running {
					var recorded taskservice.BatchResult
					if err := json.Unmarshal(got.Result, &recorded); err != nil {
						t.Fatal(err)
					}
					if got.Status != jobs.Failed || got.ErrorCode != "HOMEWORK_BATCH_FAILED" || recorded.Status != status || recorded.ReasonCode != status || recorded.CompletedLevels != 1 {
						t.Fatalf("known pre-formal failure was overwritten: job=%+v batch=%+v", got, recorded)
					}
					return
				}
				time.Sleep(time.Millisecond)
			}
			t.Fatal("job did not finish")
		})
	}
}

func TestHomeworkOutcomeKeepsFormalBuildUnknownUncertain(t *testing.T) {
	outcome, err := homeworkOutcome(&taskservice.BatchResult{Status: "uncertain", ReasonCode: "submission_uncertain", TotalLevels: 2, CompletedLevels: 1, FailedLevels: 1}, errors.New("formal build response lost"))
	if err != nil || outcome.Status != jobs.Uncertain || outcome.ErrorCode != "RESULT_UNCONFIRMED" {
		t.Fatalf("outcome=%+v err=%v", outcome, err)
	}
}
