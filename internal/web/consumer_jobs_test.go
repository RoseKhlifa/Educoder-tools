package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/jobs"
)

func assertConsumerBoundary(t *testing.T, body string) {
	t.Helper()
	var decoded any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatal(err)
	}
	banned := map[string]bool{"error_code": true, "events": true, "diagnostics": true, "model_calls": true, "cost_micro_usd": true, "reserved_micro_usd": true, "usage": true, "capacity": true, "owner_id": true, "idempotency_key": true, "input_tokens": true, "output_tokens": true, "model_configured": true, "model_concurrency": true, "candidate": true, "answer": true, "answers": true}
	var inspect func(any)
	inspect = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			for key, child := range v {
				if banned[key] {
					t.Fatalf("operator-only field %s leaked to consumer: %s", key, body)
				}
				inspect(child)
			}
		case []any:
			for _, child := range v {
				inspect(child)
			}
		}
	}
	inspect(decoded)
	for _, marker := range []string{"operator-diagnostic-marker", "operator-event-marker", "operator-message-marker", "provider-truncated-marker", "model_error"} {
		if strings.Contains(body, marker) {
			t.Fatalf("raw operator detail %q leaked: %s", marker, body)
		}
	}
}

func TestConsumerCreateListDetailAndCancelUseAllowlistAndIsolateTenants(t *testing.T) {
	f := newPlatformHTTPFixture(t, func(_ context.Context, _ EducoderClient, _ string, _ jobs.Request, report func(jobs.Event)) (jobs.Outcome, error) {
		report(jobs.Event{Phase: "repairing", Message: "operator-event-marker", Progress: &jobs.BatchProgress{TotalLevels: 6, CompletedLevels: 3, FailedLevels: 1, CurrentTitle: "第四关"}})
		return jobs.Outcome{Status: jobs.Failed, ErrorCode: "model_error", Message: "operator-message-marker", Result: json.RawMessage(`{"total_levels":6,"completed_levels":3,"diagnostics":["operator-diagnostic-marker"],"reason":"provider-truncated-marker","cost_micro_usd":99999,"model_calls":5}`)}, nil
	})
	created := f.request(t, f.aliceClient, "POST", "/api/jobs", `{"scope":"homework","mode":"submit","course":"COURSE_A","homework_id":42,"idempotency_key":"fixture-idempotency"}`)
	if created.StatusCode != http.StatusAccepted {
		t.Fatal(created.Body)
	}
	assertConsumerBoundary(t, created.Body)
	id := decodeJob(t, created).ID
	f.waitJob(t, id)
	for _, route := range []struct{ method, path string }{{"GET", "/api/jobs"}, {"GET", "/api/jobs/" + id}, {"POST", "/api/jobs/" + id + "/cancel"}} {
		got := f.request(t, f.aliceClient, route.method, route.path, "")
		if got.StatusCode != 200 {
			t.Fatal(got.Body)
		}
		assertConsumerBoundary(t, got.Body)
		if !strings.Contains(got.Body, `"status":"support"`) || !strings.Contains(got.Body, "管理员将协助完成") {
			t.Fatalf("failed job not rendered as service support: %s", got.Body)
		}
	}
	for _, route := range []struct{ method, path string }{{"GET", "/api/jobs/" + id}, {"POST", "/api/jobs/" + id + "/cancel"}} {
		got := f.request(t, f.bobClient, route.method, route.path, "")
		if got.StatusCode != 404 {
			t.Fatalf("other tenant accessed %s: %d", route.path, got.StatusCode)
		}
	}
	other := f.request(t, f.bobClient, "GET", "/api/jobs", "")
	if other.StatusCode != 200 || strings.Contains(other.Body, id) {
		t.Fatal("tenant A job leaked into tenant B list")
	}
	assertConsumerBoundary(t, other.Body)
	// Legacy login hashes are deliberately not auto-claimed by verified users.
	legacy, _, err := f.manager.Submit(context.Background(), accountJobOwner(f.alice.Login), jobs.Request{Course: "LEGACY", HomeworkID: 99, Mode: "submit", Scope: "homework"}, func(context.Context, jobs.Request, func(jobs.Event)) (jobs.Outcome, error) {
		return jobs.Outcome{Status: jobs.Succeeded}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	f.waitJob(t, legacy.ID)
	got := f.request(t, f.aliceClient, "GET", "/api/jobs/"+legacy.ID, "")
	if got.StatusCode != 404 {
		t.Fatal("current display login automatically claimed historical legacy owner")
	}
}

func TestConsumerProjectionNeverCopiesArbitraryExecutorFields(t *testing.T) {
	for _, status := range []string{jobs.Failed, jobs.Uncertain, jobs.Interrupted} {
		public := toConsumerJob(jobs.Job{ID: "fixture", Status: status, ErrorCode: "model_error", Message: "operator-message-marker", Events: []jobs.Event{{Message: "operator-event-marker"}}, Result: json.RawMessage(`{"total_levels":2,"completed_levels":1,"model_calls":999,"cost_micro_usd":99999,"nested":{"diagnostics":["operator-diagnostic-marker"]},"answers":["private candidate"]}`)})
		body, err := json.Marshal(public)
		if err != nil {
			t.Fatal(err)
		}
		assertConsumerBoundary(t, string(body))
		if public.Status != "support" || public.Result.TotalLevels != 2 || public.Result.CompletedLevels != 1 {
			t.Fatalf("unexpected service projection: %+v", public)
		}
	}
}

// The paid service surface must not report generation or a standalone self-test
// as completion of a student's assignment. Those modes remain CLI operations.
func TestPlatformConsumerRejectsTechnicalModesAndExpandsLegacyHomeworkSubmit(t *testing.T) {
	f := newPlatformHTTPFixture(t, func(_ context.Context, _ EducoderClient, _ string, req jobs.Request, _ func(jobs.Event)) (jobs.Outcome, error) {
		return jobs.Outcome{Status: jobs.Succeeded, Result: json.RawMessage(`{"status":"generated"}`)}, nil
	})
	for _, mode := range []string{"generate_only", "selftest"} {
		got := f.request(t, f.aliceClient, "POST", "/api/jobs", `{"course":"COURSE_A","homework_id":42,"game":"fixture_game","mode":"`+mode+`"}`)
		if got.StatusCode != http.StatusBadRequest {
			t.Errorf("technical mode %s admitted to consumer completion service: %d %s", mode, got.StatusCode, got.Body)
		}
	}
	got := f.request(t, f.aliceClient, "POST", "/api/jobs", `{"course":"COURSE_A","homework_id":43,"mode":"submit"}`)
	if got.StatusCode != http.StatusAccepted {
		t.Fatal(got.Body)
	}
	job, err := f.manager.Get(context.Background(), f.alice.Owner(), decodeJob(t, got).ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Request.Scope != "homework" || job.Request.Game != "" {
		t.Errorf("legacy homework submit still executes only the current level: %+v", job.Request)
	}
}

func TestBusyBackgroundCollectionDoesNotStarveAnswerWorkers(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(entered) })
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":0,"courses":[]}`))
	}))
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.JobWorkers = 2
	cfg.JobPerOwnerRunning = 1
	cfg.ModelAPIKeyFile = filepath.Join(cfg.DataDir, "not-configured")
	runtime, err := NewJobRuntime(cfg)
	if err != nil {
		upstream.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		close(release)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := runtime.Close(ctx); err != nil {
			t.Error(err)
		}
		upstream.Close()
	})
	protocol := educoder.New(upstream.URL)
	protocol.LoginName = "fixture-user"
	collect := func(ctx context.Context, _ jobs.Request, report func(jobs.Event)) (jobs.Outcome, error) {
		return runtime.collect(ctx, protocol, accountJobOwner("fixture-owner"), report)
	}
	if _, _, err = runtime.manager.Submit(context.Background(), "collector-a", jobs.Request{Mode: "collect_verified"}, collect); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first collection did not occupy local upstream")
	}
	if _, _, err = runtime.manager.Submit(context.Background(), "collector-b", jobs.Request{Mode: "collect_verified"}, collect); err != nil {
		t.Fatal(err)
	}
	answer, _, err := runtime.manager.Submit(context.Background(), "answer-owner", jobs.Request{Scope: "homework", Mode: "submit", Course: "COURSE_A", HomeworkID: 42}, func(context.Context, jobs.Request, func(jobs.Event)) (jobs.Outcome, error) {
		return jobs.Outcome{Status: jobs.Succeeded}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		job, err := runtime.manager.Get(context.Background(), "answer-owner", answer.ID)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status == jobs.Succeeded {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("an additional background scan occupied the free answer worker while another scan waited on upstream")
}
