package solver

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"educoder-cli/internal/answer"
)

func TestProgressModeAndIdempotencySurviveIntegration(t *testing.T) {
	store := &fakeStore{}
	provider := &fakeProvider{candidates: []answer.Candidate{candidate("good")}}
	adapter := &fakeAdapter{snap: testSnapshot()}
	service := New(store, provider, Config{})
	var phases []string
	req := Request{Owner: "account", Course: "c", HomeworkID: 1, Game: "g", Mode: ModeSubmit, IdempotencyKey: "job-key", OnProgress: func(st Stage) { phases = append(phases, st.Phase) }}
	r, err := service.Run(context.Background(), adapter, req)
	if err != nil || r.Status != "passed" || r.SelfTestStatus != "passed" {
		t.Fatalf("result=%+v err=%v", r, err)
	}
	want := []string{"loading", "bank_lookup", "generating", "validating", "saving", "selftesting", "submitting", "evaluating", "bank_saved", "verifying"}
	if !reflect.DeepEqual(phases, want) {
		t.Fatalf("phases=%v want=%v", phases, want)
	}
	var state runState
	if err := json.Unmarshal(store.runs[r.RunID].Data, &state); err != nil {
		t.Fatal(err)
	}
	if state.Mode != ModeSubmit || state.Limits.MaxModelCalls != 3 || state.Result.CostMicroUSD != 1 {
		t.Fatalf("checkpoint lost mode or accounting: %+v", state)
	}
	if _, err := service.Run(context.Background(), adapter, req); err != nil || provider.calls != 1 || adapter.submits != 1 {
		t.Fatalf("replay repeated work: err=%v model=%d submits=%d", err, provider.calls, adapter.submits)
	}
	req.Mode = ModeGenerateOnly
	if _, err := service.Run(context.Background(), adapter, req); err == nil {
		t.Fatal("same idempotency key accepted another mode")
	}
}

func TestExplicitCancellationPreservesLatestAccountingAndCandidate(t *testing.T) {
	store := &fakeStore{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &scriptedProvider{estimateCost: 10, generate: func(context.Context, answer.Request, int) (answer.Result, error) {
		cancel()
		return solved("partial", 3), context.Canceled
	}}
	r, err := New(store, p, Config{}).Run(ctx, &fakeAdapter{snap: testSnapshot()}, recoveryRequest())
	if !errors.Is(err, context.Canceled) || r == nil || r.Status != "canceled" || r.ModelCalls != 1 || r.CostMicroUSD != 3 || r.ReservedMicroUSD != 0 || r.CandidateID == 0 {
		t.Fatalf("result=%+v err=%v", r, err)
	}
	var state runState
	record := store.runs[r.RunID]
	if err := json.Unmarshal(record.Data, &state); err != nil {
		t.Fatal(err)
	}
	if record.Status != "canceled" || state.Result.CostMicroUSD != 3 || state.Result.CandidateID != r.CandidateID || state.LastCandidate == nil || state.LastCandidate.Files[0].Content != "partial" || len(state.Feedback) == 0 {
		t.Fatalf("cancellation overwrote latest checkpoint: %+v", state)
	}
	if len(store.usages) != 1 || store.usages[0].Status != "settled" || store.usages[0].CostMicroUSD != 3 {
		t.Fatalf("usage=%+v", store.usages)
	}
}

func TestCancellationAtGeneratingStageDoesNotDispatchOrReserve(t *testing.T) {
	store := &fakeStore{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &scriptedProvider{estimateCost: 10, generate: func(context.Context, answer.Request, int) (answer.Result, error) {
		t.Fatal("provider called after explicit cancellation")
		return answer.Result{}, nil
	}}
	req := recoveryRequest()
	req.OnProgress = func(st Stage) {
		if st.Phase == "generating" {
			cancel()
		}
	}
	r, err := New(store, p, Config{}).Run(ctx, &fakeAdapter{snap: testSnapshot()}, req)
	if !errors.Is(err, context.Canceled) || r.Status != "canceled" || r.ModelCalls != 0 || r.ReservedMicroUSD != 0 || len(store.usages) != 0 {
		t.Fatalf("result=%+v err=%v usage=%+v", r, err, store.usages)
	}
}

func TestRuntimeLockWaitCancellationPersistsAndCleansUp(t *testing.T) {
	store := &fakeStore{}
	service := New(store, nil, Config{})
	unlock, err := service.lock(context.Background(), "myshixun")
	if err != nil {
		t.Fatal(err)
	}
	req := recoveryRequest()
	req.Timeout = 15 * time.Millisecond
	r, err := service.Run(context.Background(), &fakeAdapter{snap: testSnapshot()}, req)
	if !errors.Is(err, context.DeadlineExceeded) || r == nil || r.Status != "canceled" || store.runs[r.RunID].Status != "canceled" {
		t.Fatalf("result=%+v err=%v", r, err)
	}
	service.lockMu.Lock()
	refs := service.locks["myshixun"].refs
	service.lockMu.Unlock()
	if refs != 1 {
		t.Fatalf("canceled waiter retained reference: %d", refs)
	}
	unlock()
	service.lockMu.Lock()
	remaining := len(service.locks)
	service.lockMu.Unlock()
	if remaining != 0 {
		t.Fatalf("runtime locks leaked: %d", remaining)
	}
}

type refreshedSnapshotAdapter struct {
	fakeAdapter
	loads int
}

func (a *refreshedSnapshotAdapter) Load(context.Context, Target) (LoadedProblem, error) {
	a.loads++
	snapshot := testSnapshot()
	if a.loads == 1 {
		snapshot.Files[0].CurrentContent = "before waiting for runtime lock"
	} else {
		snapshot.Files[0].CurrentContent = "after acquiring runtime lock"
	}
	return LoadedProblem{Snapshot: snapshot, Key: "myshixun"}, nil
}

func TestModelUsesSnapshotRefreshedAfterRuntimeLock(t *testing.T) {
	p := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, req answer.Request, _ int) (answer.Result, error) {
		if req.Problem.Files[0].CurrentContent != "after acquiring runtime lock" {
			t.Fatal("model received stale editor source")
		}
		return solved("good", 3), nil
	}}
	a := &refreshedSnapshotAdapter{}
	req := recoveryRequest()
	req.Mode = ModeGenerateOnly
	r, err := New(&fakeStore{}, p, Config{}).Run(context.Background(), a, req)
	if err != nil || r.Status != "generated" || a.loads != 2 {
		t.Fatalf("result=%+v err=%v loads=%d", r, err, a.loads)
	}
}

func TestSelfTestWithoutPublicExamplesKeepsSkippedStatus(t *testing.T) {
	snap := testSnapshot()
	snap.PublicTests = nil
	p := &fakeProvider{candidates: []answer.Candidate{candidate("good")}}
	a := &fakeAdapter{snap: snap}
	r, err := New(&fakeStore{}, p, Config{}).Run(context.Background(), a, recoveryRequest())
	if err != nil || r.Status != "selftest_skipped" || r.SelfTestStatus != "skipped" || a.selfTests != 0 || a.submits != 0 {
		t.Fatalf("result=%+v err=%v", r, err)
	}
}

var _ Adapter = (*refreshedSnapshotAdapter)(nil)
