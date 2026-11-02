package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"educoder-cli/internal/jobs"
)

func TestConsumerJobRetryCapabilities(t *testing.T) {
	homework := jobs.Request{Mode: "submit", Scope: "homework", Course: "C", HomeworkID: 42}
	for _, tc := range []struct {
		name, status, errorCode, result string
		request                         jobs.Request
		retry, cancel                   bool
	}{
		{name: "failed homework", status: jobs.Failed, request: homework, retry: true},
		{name: "stopped homework", status: jobs.Canceled, request: homework, retry: true},
		{name: "interrupted homework", status: jobs.Interrupted, request: homework, retry: true},
		{name: "running homework", status: jobs.Running, request: homework, cancel: true},
		{name: "queued homework", status: jobs.Queued, request: homework, cancel: true},
		{name: "completed homework", status: jobs.Succeeded, request: homework},
		{name: "unknown outcome", status: jobs.Uncertain, request: homework},
		{name: "failed course", status: jobs.Failed, request: jobs.Request{Mode: "submit", Scope: "course", Course: "C"}, retry: true},
		{name: "failed all courses", status: jobs.Failed, request: jobs.Request{Mode: "submit", Scope: "all"}, retry: true},
		{name: "exam", status: jobs.Failed, request: jobs.Request{Mode: "submit", Scope: "exercise", Course: "C", ExerciseID: 42}},
		{name: "collection", status: jobs.Failed, request: jobs.Request{Mode: "collect_verified"}},
		{name: "generation", status: jobs.Failed, request: jobs.Request{Mode: "generate_only", Course: "C", HomeworkID: 42}},
		{name: "selftest", status: jobs.Failed, request: jobs.Request{Mode: "selftest", Course: "C", HomeworkID: 42}},
		{name: "legacy scope", status: jobs.Failed, request: jobs.Request{Mode: "submit", Course: "C", HomeworkID: 42}},
		{name: "uncertain code", status: jobs.Failed, request: homework, errorCode: "RESULT_UNCONFIRMED"},
		{name: "uncertain result", status: jobs.Failed, request: homework, result: `{"status":"verification_unconfirmed"}`},
		{name: "uncertain child", status: jobs.Failed, request: jobs.Request{Mode: "submit", Scope: "course", Course: "C"}, result: `{"status":"needs_service","homeworks":[{"status":"needs_service","result":{"status":"uncertain"}}]}`},
		{name: "uncertain level", status: jobs.Failed, request: homework, result: `{"status":"attempts_exhausted","levels":[{"status":"verification_unconfirmed"}]}`},
		{name: "uncertain reason", status: jobs.Canceled, request: homework, result: `{"status":"canceled","levels":[{"status":"failed","reason_code":"submission_uncertain"}]}`},
		{name: "invalid result", status: jobs.Failed, request: homework, result: `{"status":`},
		{name: "known failure", status: jobs.Failed, request: homework, result: `{"status":"attempts_exhausted","levels":[{"status":"selftest_failed"}]}`, retry: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			public := toConsumerJob(jobs.Job{ID: "fixture", Request: tc.request, Status: tc.status, ErrorCode: tc.errorCode, Result: json.RawMessage(tc.result)})
			if public.CanRetry != tc.retry || public.CanCancel != tc.cancel {
				t.Fatalf("capabilities retry=%v cancel=%v, want retry=%v cancel=%v", public.CanRetry, public.CanCancel, tc.retry, tc.cancel)
			}
			body, err := json.Marshal(public)
			if err != nil {
				t.Fatal(err)
			}
			assertConsumerBoundary(t, string(body))
			if !strings.Contains(string(body), `"can_retry":`) || !strings.Contains(string(body), `"can_cancel":`) {
				t.Fatal("capability booleans must be present even when false")
			}
		})
	}
}

func TestHTTPJobRetryOwnershipBodyAndModelGate(t *testing.T) {
	var calls atomic.Int64
	f := newPlatformHTTPFixture(t, func(context.Context, EducoderClient, string, jobs.Request, func(jobs.Event)) (jobs.Outcome, error) {
		calls.Add(1)
		return jobs.Outcome{Status: jobs.Failed, ErrorCode: "model_error", Message: "operator-message-marker", Result: json.RawMessage(`{"status":"attempts_exhausted","diagnostics":["operator-diagnostic-marker"],"answers":["private candidate"],"model_calls":9}`)}, nil
	})
	original, _, err := f.manager.Submit(context.Background(), f.alice.Owner(), jobs.Request{Mode: "submit", Scope: "homework", Course: "C", HomeworkID: 42}, func(context.Context, jobs.Request, func(jobs.Event)) (jobs.Outcome, error) {
		return jobs.Outcome{Status: jobs.Failed}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	f.waitJob(t, original.ID)
	path := "/api/jobs/" + original.ID + "/retry"
	for _, body := range []string{"", `{}`, `{"owner":"other"}`} {
		if got := f.request(t, f.bobClient, "POST", path, body); got.StatusCode != http.StatusNotFound {
			t.Fatalf("cross-owner retry=%d %s", got.StatusCode, got.Body)
		}
	}
	if got := f.request(t, f.server.Client(), "POST", path, "{}"); got.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated retry=%d", got.StatusCode)
	}
	for _, body := range []string{`{"course":"OTHER"}`, `{"homework_id":99}`, `{"owner":"other"}`, `{"mode":"selftest"}`, `{"idempotency_key":"browser-key"}`, `{"scope":"all"}`, `null`, `[]`, `{"unknown":true}`, `{} {}`, strings.Repeat(" ", 1025) + `{}`} {
		got := f.request(t, f.aliceClient, "POST", path, body)
		if got.StatusCode != http.StatusBadRequest || !strings.Contains(got.Body, "INVALID_REQUEST") {
			t.Errorf("retry body %q accepted: %d %s", body, got.StatusCode, got.Body)
		}
	}
	f.app.jobs.modelConfigured = false
	if got := f.request(t, f.aliceClient, "POST", path, "{}"); got.StatusCode != http.StatusServiceUnavailable || !strings.Contains(got.Body, "SERVICE_UNAVAILABLE") {
		t.Fatalf("missing model configuration accepted: %d %s", got.StatusCode, got.Body)
	}
	if calls.Load() != 0 {
		t.Fatal("invalid retries started an executor")
	}
	f.app.jobs.modelConfigured = true
	created := f.request(t, f.aliceClient, "POST", path, " { } ")
	if created.StatusCode != http.StatusAccepted {
		t.Fatal(created.Body)
	}
	assertConsumerBoundary(t, created.Body)
	child := f.waitJob(t, decodeJob(t, created).ID)
	if child.Request.IdempotencyKey != "retry-job:"+original.ID {
		t.Fatal("deterministic key was not persisted")
	}
	for _, route := range []string{"/api/jobs", "/api/jobs/" + child.ID} {
		got := f.request(t, f.aliceClient, "GET", route, "")
		if got.StatusCode != http.StatusOK {
			t.Fatal(got.Body)
		}
		assertConsumerBoundary(t, got.Body)
		if !strings.Contains(got.Body, `"can_retry":true`) {
			t.Fatal("failed retry is not independently restartable")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("retry ran more than once")
	}
}

func TestHTTPJobRetryPreservesAllSupportedScopesAndRejectsOthers(t *testing.T) {
	for _, req := range []jobs.Request{
		{Mode: "submit", Scope: "homework", Course: "C", HomeworkID: 42},
		{Mode: "submit", Scope: "course", Course: "C"},
		{Mode: "submit", Scope: "all"},
	} {
		t.Run(req.Scope, func(t *testing.T) {
			observed := make(chan jobs.Request, 1)
			f := newPlatformHTTPFixture(t, func(_ context.Context, _ EducoderClient, owner string, request jobs.Request, _ func(jobs.Event)) (jobs.Outcome, error) {
				observed <- request
				return jobs.Outcome{Status: jobs.Succeeded}, nil
			})
			original, _, err := f.manager.Submit(context.Background(), f.alice.Owner(), req, func(context.Context, jobs.Request, func(jobs.Event)) (jobs.Outcome, error) {
				return jobs.Outcome{Status: jobs.Failed}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			f.waitJob(t, original.ID)
			response := f.request(t, f.aliceClient, "POST", "/api/jobs/"+original.ID+"/retry", "")
			if response.StatusCode != http.StatusAccepted {
				t.Fatal(response.Body)
			}
			f.waitJob(t, decodeJob(t, response).ID)
			got := <-observed
			got.IdempotencyKey = ""
			if got != req {
				t.Fatalf("target changed: got=%+v want=%+v", got, req)
			}
		})
	}
	for i, tc := range []struct {
		name, status, result string
		request              jobs.Request
	}{
		{name: "succeeded", status: jobs.Succeeded, request: jobs.Request{Mode: "submit", Scope: "homework", Course: "C", HomeworkID: 42}},
		{name: "uncertain", status: jobs.Uncertain, request: jobs.Request{Mode: "submit", Scope: "homework", Course: "C", HomeworkID: 42}},
		{name: "course uncertain child", status: jobs.Failed, request: jobs.Request{Mode: "submit", Scope: "course", Course: "C"}, result: `{"status":"needs_service","homeworks":[{"status":"needs_service","result":{"status":"uncertain"}}]}`},
		{name: "exercise", status: jobs.Failed, request: jobs.Request{Mode: "submit", Scope: "exercise", Course: "C", ExerciseID: 42}},
		{name: "collection", status: jobs.Failed, request: jobs.Request{Mode: "collect_verified"}},
		{name: "selftest", status: jobs.Failed, request: jobs.Request{Mode: "selftest", Course: "C", HomeworkID: 42}},
	} {
		t.Run(fmt.Sprintf("reject-%d-%s", i, tc.name), func(t *testing.T) {
			f := newPlatformHTTPFixture(t, nil)
			original, _, err := f.manager.Submit(context.Background(), f.alice.Owner(), tc.request, func(context.Context, jobs.Request, func(jobs.Event)) (jobs.Outcome, error) {
				return jobs.Outcome{Status: tc.status, Result: json.RawMessage(tc.result)}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			f.waitJob(t, original.ID)
			got := f.request(t, f.aliceClient, "POST", "/api/jobs/"+original.ID+"/retry", "{}")
			if got.StatusCode != http.StatusConflict || !strings.Contains(got.Body, "JOB_NOT_RETRYABLE") {
				t.Fatalf("ineligible job admitted: %d %s", got.StatusCode, got.Body)
			}
		})
	}
}

func TestHTTPJobRetryWaitsForStopBeforeRestarting(t *testing.T) {
	entered := make(chan struct{}, 1)
	var executions atomic.Int64
	f := newPlatformHTTPFixture(t, func(ctx context.Context, _ EducoderClient, _ string, _ jobs.Request, report func(jobs.Event)) (jobs.Outcome, error) {
		if executions.Add(1) == 1 {
			report(jobs.Event{Phase: "loading"})
			entered <- struct{}{}
			<-ctx.Done()
			return jobs.Outcome{}, ctx.Err()
		}
		return jobs.Outcome{Status: jobs.Succeeded}, nil
	})
	created := f.request(t, f.aliceClient, "POST", "/api/jobs", `{"mode":"submit","scope":"homework","course":"C","homework_id":42}`)
	if created.StatusCode != http.StatusAccepted {
		t.Fatal(created.Body)
	}
	id := decodeJob(t, created).ID
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("original did not start")
	}
	detail := f.request(t, f.aliceClient, "GET", "/api/jobs/"+id, "")
	if !strings.Contains(detail.Body, `"can_cancel":true`) || !strings.Contains(detail.Body, `"can_retry":false`) {
		t.Fatalf("running capabilities=%s", detail.Body)
	}
	if got := f.request(t, f.aliceClient, "POST", "/api/jobs/"+id+"/retry", "{}"); got.StatusCode != http.StatusConflict {
		t.Fatalf("running original was restarted: %d %s", got.StatusCode, got.Body)
	}
	if executions.Load() != 1 {
		t.Fatal("retry started a duplicate before original stopped")
	}
	stopped := f.request(t, f.aliceClient, "POST", "/api/jobs/"+id+"/cancel", "{}")
	if stopped.StatusCode != http.StatusOK {
		t.Fatal(stopped.Body)
	}
	if job := f.waitJob(t, id); job.Status != jobs.Canceled {
		t.Fatalf("stop outcome=%s", job.Status)
	}
	restarted := f.request(t, f.aliceClient, "POST", "/api/jobs/"+id+"/retry", "{}")
	if restarted.StatusCode != http.StatusAccepted {
		t.Fatal(restarted.Body)
	}
	if job := f.waitJob(t, decodeJob(t, restarted).ID); job.Status != jobs.Succeeded || executions.Load() != 2 {
		t.Fatalf("restart outcome=%s executions=%d", job.Status, executions.Load())
	}
}
