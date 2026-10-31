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
	// Repeated login reuses the same daily job instead of enqueuing another scan.
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
