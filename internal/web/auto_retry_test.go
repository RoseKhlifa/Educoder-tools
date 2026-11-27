package web

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"educoder-cli/internal/jobs"
)

func TestPlatformOnlyStopRecognizesOnlyPlatformFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		raw  string
		want bool
	}{
		"homework refused then planned": {`{"status":"needs_service","levels":[{"status":"passed"},{"status":"resource_unavailable"},{"status":"planned"}]}`, true},
		"homework refused via reason":   {`{"status":"needs_service","levels":[{"status":"needs_service","reason_code":"resource_unavailable"},{"status":"precheck_failed","reason_code":"runtime_initialization_failed"}]}`, true},
		"homework code failure":         {`{"status":"needs_service","levels":[{"status":"resource_unavailable"},{"status":"selftest_failed"}]}`, false},
		"homework uncertain":            {`{"status":"uncertain","levels":[{"status":"uncertain"}]}`, true},
		"homework save timed out":       {`{"status":"needs_service","levels":[{"status":"passed"},{"status":"uncertain","reason_code":"selftest_stream_unconfirmed"},{"status":"planned"}]}`, true},
		"homework model gave up":        {`{"status":"needs_service","levels":[{"status":"attempts_exhausted","reason_code":"attempts_exhausted"}]}`, false},
		"homework nothing refused":      {`{"status":"needs_service","levels":[{"status":"passed"},{"status":"planned"}]}`, false},
		"course one refused homework": {`{"status":"needs_service","homeworks":[{"status":"passed"},{"status":"skipped"},
			{"status":"needs_service","reason_code":"service_required","result":{"levels":[{"status":"passed"},{"status":"resource_unavailable"}]}}]}`, true},
		"course mixed failure": {`{"status":"needs_service","homeworks":[
			{"status":"needs_service","result":{"levels":[{"status":"resource_unavailable"}]}},
			{"status":"needs_service","result":{"levels":[{"status":"attempts_exhausted"}]}}]}`, false},
		"course progress unavailable": {`{"status":"needs_service","homeworks":[{"status":"needs_service","reason_code":"progress_unavailable"}]}`, true},
		"course homework hand-off":    {`{"status":"needs_service","homeworks":[{"status":"needs_service","reason_code":"unsupported_homework"}]}`, false},
		"course interrupted":          {`{"status":"needs_service","homeworks":[{"status":"needs_service","reason_code":"interrupted","result":{"levels":[{"status":"resource_unavailable"}]}},{"status":"planned"}]}`, false},
		"malformed":                   {`{`, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := platformOnlyStop(json.RawMessage(tc.raw)); got != tc.want {
				t.Fatalf("platformOnlyStop=%v want %v", got, tc.want)
			}
		})
	}
}

const refusedHomeworkResult = `{"status":"needs_service","reason_code":"service_required","total_levels":2,"completed_levels":1,"levels":[{"position":1,"status":"passed"},{"position":2,"status":"resource_unavailable","reason_code":"resource_unavailable"}]}`

// A job the platform refused carries a retry plan; the scheduler launches it
// once when due through the owner's live session, numbers the chain, and
// stops after the last attempt.
func TestPlatformRefusedJobIsRetriedAutomaticallyAndBounded(t *testing.T) {
	now := time.Date(2026, 11, 20, 4, 0, 0, 0, time.UTC)
	old := autoRetryNow
	autoRetryNow = func() time.Time { return now }
	t.Cleanup(func() { autoRetryNow = old })
	var runs atomic.Int32
	f := newPlatformHTTPFixture(t, func(_ context.Context, _ EducoderClient, _ string, req jobs.Request, _ func(jobs.Event)) (jobs.Outcome, error) {
		runs.Add(1)
		return jobs.Outcome{Status: jobs.Failed, ErrorCode: "HOMEWORK_BATCH_FAILED", Result: json.RawMessage(refusedHomeworkResult)}, nil
	})
	got := f.request(t, f.aliceClient, "POST", "/api/jobs", `{"course":"COURSE_A","homework_id":43,"mode":"submit"}`)
	if got.StatusCode != 202 {
		t.Fatal(got.Body)
	}
	job := f.waitJob(t, decodeJob(t, got).ID)
	planOf := func(j jobs.Job) *autoRetryPlan {
		var r struct {
			AutoRetry *autoRetryPlan `json:"auto_retry"`
		}
		_ = json.Unmarshal(j.Result, &r)
		return r.AutoRetry
	}
	plan := planOf(job)
	if plan == nil || plan.Attempt != 1 || plan.Max != autoRetryMax || !plan.NextAt.Equal(now.Add(autoRetryDelays[0])) {
		t.Fatalf("first plan=%+v result=%s", plan, job.Result)
	}
	if n := f.app.AutoRetryTick(context.Background()); n != 0 {
		t.Fatalf("launched %d retries before they were due", n)
	}
	for attempt := 1; attempt <= autoRetryMax; attempt++ {
		now = now.Add(autoRetryDelays[attempt-1] + time.Second)
		if n := f.app.AutoRetryTick(context.Background()); n != 1 {
			t.Fatalf("attempt %d: launched %d retries", attempt, n)
		}
		if n := f.app.AutoRetryTick(context.Background()); n != 0 {
			t.Fatalf("attempt %d: the same retry launched twice", attempt)
		}
		// Find the child by its retry key.
		records, err := f.manager.AdminList(context.Background(), 50)
		if err != nil {
			t.Fatal(err)
		}
		var next jobs.Job
		for _, r := range records {
			if r.Request.IdempotencyKey == "retry-job:"+job.ID {
				next = r.Job
			}
		}
		if next.ID == "" {
			t.Fatalf("attempt %d: retry job not found", attempt)
		}
		job = f.waitJob(t, next.ID)
		plan = planOf(job)
		if attempt < autoRetryMax {
			if plan == nil || plan.Attempt != attempt+1 {
				t.Fatalf("attempt %d child plan=%+v", attempt, plan)
			}
		} else if plan != nil {
			t.Fatalf("retry chain exceeded its bound: %+v", plan)
		}
	}
	if n := int(runs.Load()); n != 1+autoRetryMax {
		t.Fatalf("runs=%d want %d", n, 1+autoRetryMax)
	}
	// Nothing more to launch once the chain is exhausted.
	now = now.Add(time.Hour)
	if n := f.app.AutoRetryTick(context.Background()); n != 0 {
		t.Fatalf("launched %d after the last attempt", n)
	}
}

// A manual retry and the scheduler use the same key, so only one runs.
func TestAutomaticAndManualRetryNeverBothRun(t *testing.T) {
	now := time.Date(2026, 11, 20, 4, 0, 0, 0, time.UTC)
	old := autoRetryNow
	autoRetryNow = func() time.Time { return now }
	t.Cleanup(func() { autoRetryNow = old })
	var runs atomic.Int32
	f := newPlatformHTTPFixture(t, func(_ context.Context, _ EducoderClient, _ string, req jobs.Request, _ func(jobs.Event)) (jobs.Outcome, error) {
		runs.Add(1)
		if strings.HasPrefix(req.IdempotencyKey, "retry-job:") {
			return jobs.Outcome{Status: jobs.Succeeded, Result: json.RawMessage(`{"status":"passed"}`)}, nil
		}
		return jobs.Outcome{Status: jobs.Failed, ErrorCode: "HOMEWORK_BATCH_FAILED", Result: json.RawMessage(refusedHomeworkResult)}, nil
	})
	got := f.request(t, f.aliceClient, "POST", "/api/jobs", `{"course":"COURSE_A","homework_id":43,"mode":"submit"}`)
	job := f.waitJob(t, decodeJob(t, got).ID)
	manual := f.request(t, f.aliceClient, "POST", "/api/jobs/"+job.ID+"/retry", "")
	if manual.StatusCode != 202 {
		t.Fatalf("manual retry: %d %s", manual.StatusCode, manual.Body)
	}
	f.waitJob(t, decodeJob(t, manual).ID)
	now = now.Add(autoRetryDelays[0] + time.Second)
	f.app.AutoRetryTick(context.Background())
	time.Sleep(50 * time.Millisecond)
	if n := runs.Load(); n != 2 {
		t.Fatalf("runs=%d; a manual and an automatic retry both ran", n)
	}
}

// Code failures and owners without a live session are never retried
// automatically.
func TestAutoRetryRequiresAResourceOnlyStopAndALiveSession(t *testing.T) {
	now := time.Date(2026, 11, 20, 4, 0, 0, 0, time.UTC)
	old := autoRetryNow
	autoRetryNow = func() time.Time { return now }
	t.Cleanup(func() { autoRetryNow = old })
	f := newPlatformHTTPFixture(t, func(_ context.Context, _ EducoderClient, _ string, req jobs.Request, _ func(jobs.Event)) (jobs.Outcome, error) {
		if req.HomeworkID == 44 {
			return jobs.Outcome{Status: jobs.Failed, ErrorCode: "HOMEWORK_BATCH_FAILED", Result: json.RawMessage(`{"status":"selftest_failed","levels":[{"status":"selftest_failed"}]}`)}, nil
		}
		return jobs.Outcome{Status: jobs.Failed, ErrorCode: "HOMEWORK_BATCH_FAILED", Result: json.RawMessage(refusedHomeworkResult)}, nil
	})
	codeFail := f.waitJob(t, decodeJob(t, f.request(t, f.aliceClient, "POST", "/api/jobs", `{"course":"COURSE_A","homework_id":44,"mode":"submit"}`)).ID)
	if strings.Contains(string(codeFail.Result), "auto_retry") {
		t.Fatalf("code failure scheduled a retry: %s", codeFail.Result)
	}
	refused := f.waitJob(t, decodeJob(t, f.request(t, f.bobClient, "POST", "/api/jobs", `{"course":"COURSE_A","homework_id":43,"mode":"submit"}`)).ID)
	if !strings.Contains(string(refused.Result), "auto_retry") {
		t.Fatalf("refused job has no plan: %s", refused.Result)
	}
	// Bob logs out: no live session can run his retry.
	f.request(t, f.bobClient, "POST", "/api/auth/logout", "")
	now = now.Add(autoRetryDelays[0] + time.Second)
	if n := f.app.AutoRetryTick(context.Background()); n != 0 {
		t.Fatalf("launched %d retries without a live session", n)
	}
}

func TestAutoRetryPlanStopsAfterTheLastAttempt(t *testing.T) {
	failed := jobs.Outcome{Status: jobs.Failed, Result: json.RawMessage(refusedHomeworkResult)}
	for attempt := 1; attempt <= autoRetryMax; attempt++ {
		if !strings.Contains(string(withAutoRetryPlan(failed, attempt).Result), `"attempt":`) {
			t.Fatalf("attempt %d has no plan", attempt)
		}
	}
	for _, attempt := range []int{0, autoRetryMax + 1, autoRetryMax + 5} {
		if out := withAutoRetryPlan(failed, attempt); string(out.Result) != refusedHomeworkResult {
			t.Fatalf("attempt %d planned beyond the bound: %s", attempt, out.Result)
		}
	}
}
