package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"educoder-cli/internal/jobs"
)

// Keep this endpoint regression self-contained so it can run unchanged against
// the previous release: the old endpoint fails with HTTP 404, not a build error.
func TestHTTPJobRetryContinuation(t *testing.T) {
	var calls atomic.Int64
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	var expectedClient EducoderClient
	originalRequest := jobs.Request{Course: "COURSE_A", HomeworkID: 42, Mode: "submit", Scope: "homework", IdempotencyKey: "original-click"}
	server, app, manager := jobsHTTP(t, jobs.Config{Workers: 4, PerOwnerRunning: 2}, func(ctx context.Context, client EducoderClient, owner string, req jobs.Request, report func(jobs.Event)) (jobs.Outcome, error) {
		calls.Add(1)
		if owner != accountJobOwner("alice") || client != expectedClient || req.Course != originalRequest.Course || req.HomeworkID != originalRequest.HomeworkID || req.Scope != originalRequest.Scope || req.Mode != originalRequest.Mode || req.Game != "" {
			return jobs.Outcome{}, fmt.Errorf("retry changed owner, client or original target: %+v", req)
		}
		entered <- struct{}{}
		report(jobs.Event{Phase: "loading", Progress: &jobs.BatchProgress{TotalLevels: 2, CompletedLevels: 1}})
		select {
		case <-release:
			return jobs.Outcome{Status: jobs.Succeeded, Result: json.RawMessage(`{"status":"passed","total_levels":2,"completed_levels":2}`)}, nil
		case <-ctx.Done():
			return jobs.Outcome{}, ctx.Err()
		}
	})
	alice := jobAccount(t, server, "alice")
	parsed, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	for _, cookie := range alice.Jar.Cookies(parsed.URL) {
		if cookie.Name == sessionCookieName {
			session, ok := app.Store().Get(cookie.Value)
			if !ok {
				t.Fatal("missing authenticated session")
			}
			expectedClient = session.Client
		}
	}
	original, _, err := manager.Submit(context.Background(), accountJobOwner("alice"), originalRequest, func(context.Context, jobs.Request, func(jobs.Event)) (jobs.Outcome, error) {
		return jobs.Outcome{Status: jobs.Failed, ErrorCode: "HOMEWORK_BATCH_FAILED", Result: json.RawMessage(`{"status":"attempts_exhausted","total_levels":2,"completed_levels":1}`)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(3 * time.Second); ; {
		job, err := manager.Get(context.Background(), accountJobOwner("alice"), original.ID)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status == jobs.Failed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("original did not fail")
		}
		time.Sleep(time.Millisecond)
	}
	retryURL := server.URL + "/api/jobs/" + original.ID + "/retry"
	first, err := http.NewRequest(http.MethodPost, retryURL, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	first.Header.Set("Idempotency-Key", "browser-cannot-override-retry-key")
	response, err := alice.Do(first)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("retry status=%d body=%s", response.StatusCode, body)
	}
	var envelope struct {
		Data jobs.Job `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Data.ID == "" || envelope.Data.ID == original.ID {
		t.Fatalf("retry did not create child: %s", body)
	}
	childID := envelope.Data.ID
	if response.Header.Get("Location") != "/api/jobs/"+childID {
		t.Fatal("missing retry location")
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("retry did not use current client and original scope")
	}
	var wait sync.WaitGroup
	failures := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			request, _ := http.NewRequest(http.MethodPost, retryURL, strings.NewReader("{}"))
			result, err := alice.Do(request)
			if err != nil {
				failures <- err.Error()
				return
			}
			defer result.Body.Close()
			var payload struct {
				Data jobs.Job `json:"data"`
			}
			if err := json.NewDecoder(result.Body).Decode(&payload); err != nil || result.StatusCode != http.StatusAccepted || payload.Data.ID != childID || result.Header.Get("X-Job-Reused") != "true" {
				failures <- fmt.Sprintf("duplicate status=%d child=%s reused=%s", result.StatusCode, payload.Data.ID, result.Header.Get("X-Job-Reused"))
			}
		}()
	}
	wait.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
	releaseOnce.Do(func() { close(release) })
	for deadline := time.Now().Add(3 * time.Second); ; {
		job, err := manager.Get(context.Background(), accountJobOwner("alice"), childID)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status == jobs.Succeeded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("retry did not complete: status=%s", job.Status)
		}
		time.Sleep(time.Millisecond)
	}
	again := doJSON(t, alice, http.MethodPost, retryURL, "")
	if again.StatusCode != http.StatusAccepted || json.Unmarshal([]byte(again.Body), &envelope) != nil || envelope.Data.ID != childID || calls.Load() != 1 {
		t.Fatalf("completed retry was duplicated: response=%s executions=%d", again.Body, calls.Load())
	}
	stored, err := manager.Get(context.Background(), accountJobOwner("alice"), childID)
	if err != nil || stored.Request.IdempotencyKey != "retry-job:"+original.ID {
		t.Fatalf("retry key not owned by server: %+v err=%v", stored.Request, err)
	}
	t.Log("RETRY=202; ORIGINAL_SCOPE=homework; CURRENT_CLIENT=PASS; CONCURRENT_REUSE=PASS; REUSE_AFTER_COMPLETION=PASS; EXECUTIONS=1")
}
