package jobs

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestCollectionLaneQueuesOtherOwnersAndLeavesAnswerWorkersFree(t *testing.T) {
	m := testManager(t, Config{Workers: 1, CollectionWorkers: 1, QueueSize: 8, PerOwnerPending: 4, PerOwnerRunning: 1})
	started := make(chan string, 2)
	gate := make(chan struct{})
	var calls atomic.Int32
	collect := func(owner string) Executor {
		return func(ctx context.Context, _ Request, _ func(Event)) (Outcome, error) {
			calls.Add(1)
			started <- owner
			select {
			case <-gate:
				return Outcome{Status: Succeeded}, nil
			case <-ctx.Done():
				return Outcome{}, ctx.Err()
			}
		}
	}
	req := Request{Mode: "collect_verified", IdempotencyKey: "shared-client-key"}
	first := submit(t, m, "alice", req, collect("alice"))
	if owner := receive(t, started); owner != "alice" {
		t.Fatal(owner)
	}
	second := submit(t, m, "bob", req, collect("bob"))
	reused, duplicate, err := m.Submit(context.Background(), "bob", req, collect("unexpected"))
	if err != nil || !duplicate || reused.ID != second.ID {
		t.Fatalf("queued collection dedupe: %+v %t %v", reused, duplicate, err)
	}
	if _, err = m.Get(context.Background(), "alice", second.ID); !IsCode(err, "not_found") {
		t.Fatalf("collection owner isolation lost: %v", err)
	}
	// Even this same account's answer can start, despite PerOwnerRunning=1.
	// Read-only collection has its own bounded lane and execution allowance.
	answer := submit(t, m, "alice", request("answer"), func(context.Context, Request, func(Event)) (Outcome, error) {
		return Outcome{Status: Succeeded}, nil
	})
	waitJob(t, m, "alice", answer.ID, Succeeded)
	if s := m.Stats(); s.Workers != 1 || s.CollectionWorkers != 1 || s.Running != 1 || s.Queued != 1 || s.CollectionRunning != 1 || s.CollectionQueued != 1 || s.AnswerRunning != 0 || s.AnswerQueued != 0 {
		t.Fatalf("lane accounting: %+v", s)
	}
	queued, err := m.Get(context.Background(), "bob", second.ID)
	if err != nil || queued.Status != Queued || calls.Load() != 1 {
		t.Fatalf("second scan was not waiting: %+v calls=%d err=%v", queued, calls.Load(), err)
	}
	close(gate)
	waitJob(t, m, "alice", first.ID, Succeeded)
	if owner := receive(t, started); owner != "bob" {
		t.Fatal(owner)
	}
	waitJob(t, m, "bob", second.ID, Succeeded)
	if calls.Load() != 2 {
		t.Fatalf("collection skipped or repeated: %d", calls.Load())
	}
	if s := m.Stats(); s.Running != 0 || s.Queued != 0 || s.CollectionRunning != 0 || s.CollectionQueued != 0 {
		t.Fatalf("terminal lane accounting: %+v", s)
	}
}

func TestCollectionLaneCancellationAndDisabledOwnerReleaseQueuedWork(t *testing.T) {
	m := testManager(t, Config{Workers: 1, CollectionWorkers: 1, QueueSize: 4})
	started := make(chan string, 3)
	collect := func(owner string) Executor {
		return func(ctx context.Context, _ Request, _ func(Event)) (Outcome, error) {
			started <- owner
			<-ctx.Done()
			return Outcome{}, ctx.Err()
		}
	}
	first := submit(t, m, "alice", Request{Mode: "collect_verified"}, collect("alice"))
	receive(t, started)
	second := submit(t, m, "bob", Request{Mode: "collect_verified"}, collect("bob"))
	third := submit(t, m, "carol", Request{Mode: "collect_verified"}, collect("carol"))
	if j, err := m.Cancel(context.Background(), "bob", second.ID); err != nil || j.Status != Canceled {
		t.Fatalf("queued collection cancel: %+v %v", j, err)
	}
	if err := m.CancelOwner(context.Background(), "alice"); err != nil {
		t.Fatal(err)
	}
	waitJob(t, m, "alice", first.ID, Canceled)
	if owner := receive(t, started); owner != "carol" {
		t.Fatalf("canceled queued scan ran: %s", owner)
	}
	if _, err := m.Cancel(context.Background(), "carol", third.ID); err != nil {
		t.Fatal(err)
	}
	waitJob(t, m, "carol", third.ID, Canceled)
	if s := m.Stats(); s.Running != 0 || s.Queued != 0 || s.CollectionRunning != 0 || s.CollectionQueued != 0 {
		t.Fatalf("cancel lane accounting: %+v", s)
	}
}

func TestCollectionLaneShutdownCancelsRunningAndQueuedThenRecoversStoredResults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.sqlite")
	m := testManager(t, Config{Path: path, Workers: 1, CollectionWorkers: 1})
	started := make(chan struct{})
	first := submit(t, m, "alice", Request{Mode: "collect_verified"}, func(ctx context.Context, _ Request, _ func(Event)) (Outcome, error) {
		close(started)
		<-ctx.Done()
		return Outcome{}, ctx.Err()
	})
	receive(t, started)
	var queuedCalls atomic.Int32
	second := submit(t, m, "bob", Request{Mode: "collect_verified"}, func(context.Context, Request, func(Event)) (Outcome, error) {
		queuedCalls.Add(1)
		return Outcome{}, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := m.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if queuedCalls.Load() != 0 {
		t.Fatal("queued collection ran during shutdown")
	}
	if s := m.Stats(); s.Running != 0 || s.Queued != 0 || s.Accepting {
		t.Fatalf("shutdown lane accounting: %+v", s)
	}
	reopened := testManager(t, Config{Path: path})
	for owner, id := range map[string]string{"alice": first.ID, "bob": second.ID} {
		j, err := reopened.Get(context.Background(), owner, id)
		if err != nil || j.Status != Canceled {
			t.Fatalf("canceled collection not preserved: %+v %v", j, err)
		}
	}
}

func TestCollectionLaneConfigRemainsBounded(t *testing.T) {
	cfg, err := (Config{Path: "unused"}).defaults()
	if err != nil || cfg.CollectionWorkers != 1 {
		t.Fatalf("collection lane default: %+v %v", cfg, err)
	}
	for _, workers := range []int{-1, 17} {
		if _, err := (Config{Path: "unused", CollectionWorkers: workers}).defaults(); err == nil {
			t.Fatalf("unbounded collection workers accepted: %d", workers)
		}
	}
}
