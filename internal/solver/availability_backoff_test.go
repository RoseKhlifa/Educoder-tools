package solver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"educoder-cli/internal/answer"
)

func skipProviderRetryWait(context.Context, time.Duration) error { return nil }

func TestAvailabilityBackoffFollowsDurableUsageAndStopsAfterSuccess(t *testing.T) {
	store := &fakeStore{}
	waits := 0
	wait := func(_ context.Context, delay time.Duration) error {
		waits++
		base := time.Second << (waits - 1)
		if delay < base/2 || delay > base || len(store.usages) != waits {
			t.Fatalf("wait %d delay=%s usages=%d", waits, delay, len(store.usages))
		}
		for _, record := range store.runs {
			var saved runState
			if json.Unmarshal(record.Data, &saved) != nil || saved.GenerationRecovery.ProviderRetry.Streak != waits || saved.Result.ModelCalls != waits || saved.Result.ReservedMicroUSD != int64(10*waits) {
				t.Fatalf("wait preceded durable accounting: %+v", saved)
			}
		}
		return nil
	}
	p := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, _ answer.Request, n int) (answer.Result, error) {
		if n <= 2 {
			return answer.Result{}, availabilityError(503)
		}
		return solved("good", 3), nil
	}}
	req := recoveryRequest()
	req.MaxModelCalls, req.MaxEvaluations = 5, 5
	r, err := New(store, p, Config{ProviderRetryWait: wait}).Run(context.Background(), &fakeAdapter{snap: testSnapshot()}, req)
	if err != nil || r.Status != "selftest_passed" || waits != 2 || r.ModelCalls != 3 || r.ReservedMicroUSD != 20 || r.CostMicroUSD != 3 {
		t.Fatalf("result=%+v err=%v waits=%d", r, err, waits)
	}
	var saved runState
	if json.Unmarshal(store.runs[r.RunID].Data, &saved) != nil || saved.GenerationRecovery.ProviderRetry.Streak != 0 || saved.GenerationRecovery.ProviderAvailabilityFailures != 2 {
		t.Fatalf("success did not reset streak independently of total failures: %+v", saved.GenerationRecovery)
	}
}

func TestAvailabilityBackoffCancellationKeepsCheckpointAndUnknownCharge(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &fakeStore{}
	p := &scriptedProvider{estimateCost: 10, generate: func(context.Context, answer.Request, int) (answer.Result, error) {
		return answer.Result{}, availabilityError(429)
	}}
	waits := 0
	wait := func(ctx context.Context, _ time.Duration) error {
		waits++
		cancel()
		return ctx.Err()
	}
	req := recoveryRequest()
	req.MaxModelCalls, req.MaxEvaluations = 5, 5
	a := &fakeAdapter{snap: testSnapshot()}
	r, err := New(store, p, Config{ProviderRetryWait: wait}).Run(ctx, a, req)
	var saved runState
	if !errors.Is(err, context.Canceled) || r.Status != "canceled" || waits != 1 || r.ModelCalls != 1 || r.ReservedMicroUSD != 10 || len(store.usages) != 1 || store.usages[0].Status != "unknown" || a.selfTests != 0 || a.submits != 0 {
		t.Fatalf("canceled wait lost usage or dispatched again: result=%+v err=%v waits=%d usage=%+v", r, err, waits, store.usages)
	}
	if json.Unmarshal(store.runs[r.RunID].Data, &saved) != nil || saved.GenerationRecovery.ProviderRetry.Streak != 1 || saved.Result.ReservedMicroUSD != 10 || saved.Result.Status != "canceled" {
		t.Fatalf("cancellation lost checkpoint: %+v", saved)
	}
}

func TestAvailabilityBackoffSurvivesContinuationAndResetsOnOutputDefect(t *testing.T) {
	store := &fakeStore{}
	waits := 0
	wait := func(_ context.Context, delay time.Duration) error {
		waits++
		// Historical first failure survives continuation. A real truncated
		// response resets the streak before a later independent outage.
		if delay < 500*time.Millisecond || delay > time.Second {
			t.Fatalf("output defect failed to reset consecutive outage streak: %s", delay)
		}
		return nil
	}
	p := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, _ answer.Request, n int) (answer.Result, error) {
		switch n {
		case 1:
			return answer.Result{}, availabilityError(503)
		case 2:
			return solved("partial", 2), &answer.Error{Kind: "truncated_output"}
		default:
			return solved("good", 3), nil
		}
	}}
	req := recoveryRequest()
	req.MaxModelCalls, req.MaxEvaluations, req.BudgetMicroUSD = 1, 1, 100
	r, err := New(store, p, Config{ProviderRetryWait: wait}).Run(context.Background(), &fakeAdapter{snap: testSnapshot()}, req)
	if err == nil || r.Status != "model_error" || waits != 0 {
		t.Fatalf("initial budget should stop without waiting: result=%+v err=%v waits=%d", r, err, waits)
	}
	r, err = New(store, p, Config{ProviderRetryWait: wait}).ContinueRun(context.Background(), &fakeAdapter{snap: testSnapshot()}, req, r.RunID, Continuation{AdditionalModelCalls: 4, AdditionalEvaluations: 4})
	if err != nil || r.Status != "selftest_passed" || waits != 1 || r.ModelCalls != 3 || r.ReservedMicroUSD != 10 || r.CostMicroUSD != 5 {
		t.Fatalf("continued=%+v err=%v waits=%d", r, err, waits)
	}
}

func TestAvailabilityBackoffSkipsWaitWhenNextDispatchCannotBeFunded(t *testing.T) {
	for _, tc := range []struct {
		name               string
		calls, evaluations int
		budget             int64
	}{
		{"calls", 1, 5, 100},
		{"evaluations", 5, 1, 100},
		{"money", 5, 5, 15},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &scriptedProvider{estimateCost: 10, generate: func(context.Context, answer.Request, int) (answer.Result, error) {
				return answer.Result{}, availabilityError(503)
			}}
			req := recoveryRequest()
			req.MaxModelCalls, req.MaxEvaluations, req.BudgetMicroUSD = tc.calls, tc.evaluations, tc.budget
			r, _ := New(&fakeStore{}, p, Config{ProviderRetryWait: func(context.Context, time.Duration) error {
				t.Fatal("wait occurred without budget for another dispatch")
				return nil
			}}).Run(context.Background(), &fakeAdapter{snap: testSnapshot()}, req)
			if r.ModelCalls != 1 || r.ReservedMicroUSD != 10 {
				t.Fatalf("budget was reset: %+v", r)
			}
		})
	}
}
