package solver

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"educoder-cli/internal/answer"
)

// With a runtime key hint the solver locks first and loads exactly once; that
// single locked load is the snapshot the model sees.
func TestRuntimeKeyHintLocksFirstAndLoadsOnce(t *testing.T) {
	p := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, req answer.Request, _ int) (answer.Result, error) {
		if req.Problem.Files[0].CurrentContent != "before waiting for runtime lock" {
			t.Fatalf("model received %q, not the locked load", req.Problem.Files[0].CurrentContent)
		}
		return solved("good", 3), nil
	}}
	a := &refreshedSnapshotAdapter{}
	req := recoveryRequest()
	req.Mode = ModeGenerateOnly
	req.RuntimeKeyHint = "myshixun"
	r, err := New(&fakeStore{}, p, Config{}).Run(context.Background(), a, req)
	if err != nil || r.Status != "generated" || a.loads != 1 {
		t.Fatalf("result=%+v err=%v loads=%d", r, err, a.loads)
	}
}

// A loaded runtime that differs from the hint is not covered by the lock that
// is held: the run stops before any model call or write.
func TestRuntimeKeyHintMismatchFailsClosed(t *testing.T) {
	p := &scriptedProvider{estimateCost: 10, generate: func(context.Context, answer.Request, int) (answer.Result, error) {
		t.Fatal("model called for a runtime the lock does not cover")
		return answer.Result{}, nil
	}}
	a := &fakeAdapter{snap: testSnapshot()}
	req := recoveryRequest()
	req.Mode = ModeSubmit
	req.RuntimeKeyHint = "another-runtime"
	store := &fakeStore{}
	r, err := New(store, p, Config{}).Run(context.Background(), a, req)
	if err == nil || r != nil || a.selfTests != 0 || a.submits != 0 || len(store.runs) != 0 {
		t.Fatalf("mismatched hint ran: result=%+v err=%v selftests=%d submits=%d runs=%d", r, err, a.selfTests, a.submits, len(store.runs))
	}
}

// blockingLoadAdapter blocks inside Load until released, so a test can observe
// whether a second run with the same runtime key enters Load concurrently.
type blockingLoadAdapter struct {
	fakeAdapter
	entered chan struct{}
	release chan struct{}
	loads   atomic.Int32
}

func (a *blockingLoadAdapter) Load(ctx context.Context, target Target) (LoadedProblem, error) {
	a.loads.Add(1)
	a.entered <- struct{}{}
	<-a.release
	return a.fakeAdapter.Load(ctx, target)
}

// The hinted load happens under the runtime lock: a second run for the same
// runtime cannot read the editor while the first one holds it.
func TestRuntimeKeyHintHoldsTheLockAcrossTheLoad(t *testing.T) {
	a := &blockingLoadAdapter{fakeAdapter: fakeAdapter{snap: testSnapshot()}, entered: make(chan struct{}, 2), release: make(chan struct{})}
	svc := New(&fakeStore{}, &scriptedProvider{estimateCost: 10, generate: func(context.Context, answer.Request, int) (answer.Result, error) {
		return solved("good", 3), nil
	}}, Config{})
	run := func(done chan<- error) {
		req := recoveryRequest()
		req.Mode = ModeGenerateOnly
		req.RuntimeKeyHint = "myshixun"
		_, err := svc.Run(context.Background(), a, req)
		done <- err
	}
	first, second := make(chan error, 1), make(chan error, 1)
	go run(first)
	<-a.entered
	go run(second)
	select {
	case <-a.entered:
		t.Fatal("second run loaded the editor while the first held the runtime lock")
	case <-time.After(100 * time.Millisecond):
	}
	a.release <- struct{}{}
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("second run never loaded after the lock was released")
	}
	a.release <- struct{}{}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	if n := a.loads.Load(); n != 2 {
		t.Fatalf("loads=%d, want one per run", n)
	}
}
