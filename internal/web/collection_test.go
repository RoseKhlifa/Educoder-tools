package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"educoder-cli/internal/jobs"
)

func TestLoginEnqueuesReadOnlyCollectionWithoutModelAndDoesNotWait(t *testing.T) {
	started := make(chan struct{}, 2)
	var calls atomic.Int32
	server, app, manager := jobsHTTP(t, jobs.Config{Workers: 2, QueueSize: 8, PerOwnerPending: 4}, nil)
	app.jobs.modelConfigured = false
	app.jobs.collect = func(ctx context.Context, _ EducoderClient, _ string, report func(jobs.Event)) (jobs.Outcome, error) {
		calls.Add(1)
		report(jobs.Event{Phase: "collecting", Kind: "collection", Message: "Read-only scan"})
		started <- struct{}{}
		<-ctx.Done()
		return jobs.Outcome{Result: json.RawMessage(`{"collected":0,"unconfirmed":1}`)}, ctx.Err()
	}
	start := time.Now()
	alice := jobAccount(t, server, "alice")
	if time.Since(start) > time.Second {
		t.Fatal("login blocked on scan")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("scan not started")
	}
	list := doJSON(t, alice, http.MethodGet, server.URL+"/api/collections", "")
	if list.StatusCode != 200 {
		t.Fatal(list.Body)
	}
	var parsed struct {
		Data struct {
			Jobs []jobs.Job `json:"jobs"`
		} `json:"data"`
	}
	json.Unmarshal([]byte(list.Body), &parsed)
	if len(parsed.Data.Jobs) != 1 || parsed.Data.Jobs[0].Kind != "collection" {
		t.Fatal(list.Body)
	}
	id := parsed.Data.Jobs[0].ID
	// Repeated login reuses active work instead of enqueuing another scan.
	jobAccount(t, server, "alice")
	again, _ := manager.List(context.Background(), accountJobOwner("alice"), 100)
	if len(again) != 1 || again[0].ID != id {
		t.Fatal(again)
	}
	bob := jobAccount(t, server, "bob")
	other := doJSON(t, bob, http.MethodGet, server.URL+"/api/jobs/"+id, "")
	if other.StatusCode != 404 {
		t.Fatal("cross owner read", other.Body)
	}
	other = doJSON(t, bob, http.MethodPost, server.URL+"/api/jobs/"+id+"/cancel", "{}")
	if other.StatusCode != 404 {
		t.Fatal("cross owner cancel", other.Body)
	}
	canceled := doJSON(t, alice, http.MethodPost, server.URL+"/api/jobs/"+id+"/cancel", "{}")
	if canceled.StatusCode != 200 {
		t.Fatal(canceled.Body)
	}
	deadline := time.Now().Add(time.Second)
	for {
		j, e := manager.Get(context.Background(), accountJobOwner("alice"), id)
		if e != nil {
			t.Fatal(e)
		}
		if j.Status == jobs.Canceled {
			if !strings.Contains(string(j.Result), "unconfirmed") {
				t.Fatal("lost summary")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(j)
		}
		time.Sleep(time.Millisecond)
	}
	if calls.Load() > 2 {
		t.Fatal("duplicate scan")
	}
	bad := doJSON(t, alice, http.MethodPost, server.URL+"/api/collections", `{"game":"injected"}`)
	if bad.StatusCode != 400 {
		t.Fatal(bad.Body)
	}
}

func TestLoginRetriesTerminalCollectionWithCurrentSessionAndKeepsActiveDedupe(t *testing.T) {
	server, app, manager := jobsHTTP(t, jobs.Config{Workers: 1, QueueSize: 8, PerOwnerPending: 4}, nil)
	started := make(chan EducoderClient, 3)
	release := make(chan struct{})
	var calls atomic.Int32
	app.jobs.collect = func(ctx context.Context, client EducoderClient, _ string, _ func(jobs.Event)) (jobs.Outcome, error) {
		attempt := calls.Add(1)
		started <- client
		if attempt == 1 {
			return jobs.Outcome{Status: jobs.Failed, ErrorCode: "collection_stopped", Result: json.RawMessage(`{"scan_complete":false,"failed":1}`)}, nil
		}
		select {
		case <-release:
			return jobs.Outcome{Status: jobs.Succeeded, Result: json.RawMessage(`{"scan_complete":true,"failed":0}`)}, nil
		case <-ctx.Done():
			return jobs.Outcome{}, ctx.Err()
		}
	}
	jobAccount(t, server, "alice")
	var firstClient EducoderClient
	select {
	case firstClient = <-started:
	case <-time.After(time.Second):
		t.Fatal("initial login scan did not start")
	}
	first := waitCollectionTerminal(t, manager, "alice", 1)
	if first.Status != jobs.Failed {
		t.Fatalf("fixture did not stop: %+v", first)
	}
	secondAccount := jobAccount(t, server, "alice")
	var secondClient EducoderClient
	select {
	case secondClient = <-started:
	case <-time.After(time.Second):
		t.Fatal("terminal same-day scan suppressed a fresh authenticated scan")
	}
	if firstClient == secondClient {
		t.Fatal("collection reused the stopped session's client")
	}
	jobAccount(t, server, "alice")
	list, err := manager.List(context.Background(), accountJobOwner("alice"), 100)
	if err != nil || len(list) != 2 || list[0].ID == first.ID || calls.Load() != 2 {
		t.Fatalf("active scan duplicate or old scan replay: %+v %d %v", list, calls.Load(), err)
	}
	secondID := list[0].ID
	// An explicit idempotency key binds to the active scan and keeps its normal
	// durable semantics even when the scan later finishes.
	post := func(key string) struct {
		JobID  string `json:"job_id"`
		Reused bool   `json:"reused"`
	} {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, server.URL+"/api/collections", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		response, err := secondAccount.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var result struct {
			Data struct {
				JobID  string `json:"job_id"`
				Reused bool   `json:"reused"`
			} `json:"data"`
		}
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil || response.StatusCode != http.StatusAccepted {
			t.Fatalf("collection POST %d: %+v %v", response.StatusCode, result, err)
		}
		return result.Data
	}
	if result := post("explicit-client-key"); result.JobID != secondID || !result.Reused {
		t.Fatalf("explicit active dedupe: %+v", result)
	}
	close(release)
	second := waitCollectionTerminal(t, manager, "alice", 2)
	if second.ID != secondID || second.Status != jobs.Succeeded {
		t.Fatalf("fresh scan did not finish: %+v", second)
	}
	if result := post("explicit-client-key"); result.JobID != secondID || !result.Reused || calls.Load() != 2 {
		t.Fatalf("explicit terminal replay changed: %+v calls=%d", result, calls.Load())
	}
	if result := post(""); result.JobID == secondID || result.Reused {
		t.Fatalf("unkeyed explicit POST did not schedule fresh scan: %+v", result)
	}
	third := waitCollectionTerminal(t, manager, "alice", 3)
	if third.Status != jobs.Succeeded || calls.Load() != 3 {
		t.Fatalf("fresh explicit scan: %+v calls=%d", third, calls.Load())
	}
}

func waitCollectionTerminal(t *testing.T, manager *jobs.Manager, login string, count int) jobs.Job {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		list, err := manager.List(context.Background(), accountJobOwner(login), 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(list) == count && list[0].Status != jobs.Queued && list[0].Status != jobs.Running {
			return list[0]
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("collection did not reach terminal state")
	return jobs.Job{}
}

func TestLoginStillSucceedsWhenCollectionQueueIsFull(t *testing.T) {
	server, app, manager := jobsHTTP(t, jobs.Config{Workers: 1, QueueSize: 1, PerOwnerPending: 3}, nil)
	app.jobs.collect = func(ctx context.Context, _ EducoderClient, _ string, _ func(jobs.Event)) (jobs.Outcome, error) {
		<-ctx.Done()
		return jobs.Outcome{}, ctx.Err()
	}
	jobAccount(t, server, "a")
	deadline := time.Now().Add(time.Second)
	for manager.Stats().Running == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no worker")
		}
		time.Sleep(time.Millisecond)
	}
	jobAccount(t, server, "b")
	client := jobAccount(t, server, "c")
	r := doJSON(t, client, http.MethodGet, server.URL+"/api/auth/me", "")
	if r.StatusCode != 200 {
		t.Fatal(r.Body)
	}
	list, e := manager.List(context.Background(), accountJobOwner("c"), 100)
	if e != nil || len(list) != 0 {
		t.Fatal(list, e)
	}
}
