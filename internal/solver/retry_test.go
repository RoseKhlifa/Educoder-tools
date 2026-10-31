package solver

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"educoder-cli/internal/answer"
)

func TestModelOutputFailuresRetryBeforeAnyPlatformMutation(t *testing.T) {
	store := &fakeStore{}
	adapter := &fakeAdapter{snap: testSnapshot()}
	provider := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, req answer.Request, n int) (answer.Result, error) {
		if adapter.selfTests != 0 || adapter.submits != 0 {
			t.Fatal("model retry happened after a platform mutation")
		}
		if n > 1 {
			if len(req.Feedback) == 0 || req.Feedback[0].Kind != "model" {
				t.Fatal("retry did not retain model failure feedback")
			}
			if req.Profile.MaxTokens != 8192 || req.Profile.Thinking != "disabled" || req.Profile.ReasoningEffort != "" {
				t.Fatalf("truncated output repeated the same profile: %+v", req.Profile)
			}
		}
		if n == 1 {
			return solved("partial", 2), &answer.Error{Kind: "truncated_output"}
		}
		if n == 2 {
			return answer.Result{Model: "fake", CostKnown: true, CostMicroUSD: 3}, &answer.Error{Kind: "invalid_output_json"}
		}
		return solved("good", 4), nil
	}}
	req := recoveryRequest()
	req.Mode = ModeSubmit
	req.MaxModelCalls, req.MaxEvaluations = 5, 5
	r, err := New(store, provider, Config{}).Run(context.Background(), adapter, req)
	if err != nil || r.Status != "passed" || r.ModelCalls != 3 || r.CostMicroUSD != 9 || r.ReservedMicroUSD != 0 {
		t.Fatalf("result=%+v err=%v", r, err)
	}
	if len(provider.requests) != 3 || adapter.selfTests != 1 || adapter.submits != 1 || len(store.usages) != 3 {
		t.Fatalf("calls=%d selftests=%d submits=%d usage=%+v", len(provider.requests), adapter.selfTests, adapter.submits, store.usages)
	}
}

func TestModelOutputRetriesRespectCallAndMoneyBudgets(t *testing.T) {
	for _, tc := range []struct {
		name   string
		calls  int
		budget int64
		want   int
		status string
	}{
		{name: "call limit", calls: 2, budget: 100, want: 2, status: "model_error"},
		{name: "money limit", calls: 5, budget: 15, want: 1, status: "budget_exhausted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{}
			adapter := &fakeAdapter{snap: testSnapshot()}
			provider := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, _ answer.Request, _ int) (answer.Result, error) {
				return answer.Result{Model: "fake", CostKnown: true, CostMicroUSD: 10}, &answer.Error{Kind: "truncated_output"}
			}}
			req := recoveryRequest()
			req.MaxModelCalls, req.MaxEvaluations, req.BudgetMicroUSD = tc.calls, 5, tc.budget
			r, err := New(store, provider, Config{}).Run(context.Background(), adapter, req)
			if r == nil || r.Status != tc.status || r.ModelCalls != tc.want || adapter.selfTests != 0 || adapter.submits != 0 || len(store.usages) != tc.want {
				t.Fatalf("result=%+v err=%v selftests=%d submits=%d usage=%+v", r, err, adapter.selfTests, adapter.submits, store.usages)
			}
		})
	}
}

func TestConfirmedSelfTestFailuresRepairWithinFiveAttempts(t *testing.T) {
	store := &fakeStore{}
	adapter := &fakeAdapter{snap: testSnapshot()}
	provider := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, req answer.Request, n int) (answer.Result, error) {
		if n > 1 && (req.Previous == nil || len(req.Feedback) == 0 || req.Feedback[0].Kind != "selftest") {
			t.Fatal("confirmed failing selftest was not supplied as repair feedback")
		}
		if n < 5 {
			return solved(fmt.Sprintf("bad%d", n), 1), nil
		}
		return solved("good", 1), nil
	}}
	req := recoveryRequest()
	req.Mode = ModeSubmit
	req.MaxModelCalls, req.MaxEvaluations = 5, 5
	r, err := New(store, provider, Config{}).Run(context.Background(), adapter, req)
	if err != nil || r.Status != "passed" || r.ModelCalls != 5 || adapter.selfTests != 5 || adapter.submits != 1 {
		t.Fatalf("result=%+v err=%v selftests=%d submits=%d", r, err, adapter.selfTests, adapter.submits)
	}
	if len(store.candidates) != 5 || len(store.validations) != 6 {
		t.Fatalf("candidates=%d validations=%+v", len(store.candidates), store.validations)
	}
}

func TestModelAuthFailureDoesNotConsumeAllRetryBudget(t *testing.T) {
	adapter := &fakeAdapter{snap: testSnapshot()}
	provider := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, _ answer.Request, _ int) (answer.Result, error) {
		return answer.Result{}, &answer.Error{Kind: "upstream_error", HTTPStatus: 401, BilledUnknown: true}
	}}
	req := recoveryRequest()
	req.MaxModelCalls, req.MaxEvaluations = 5, 5
	r, err := New(&fakeStore{}, provider, Config{}).Run(context.Background(), adapter, req)
	if err == nil || r.Status != "model_error" || len(provider.requests) != 1 || r.ReservedMicroUSD != 10 || adapter.selfTests != 0 || adapter.submits != 0 {
		t.Fatalf("result=%+v err=%v calls=%d", r, err, len(provider.requests))
	}
}

func TestTransientModelRetryRetainsUnresolvedUsageReservation(t *testing.T) {
	store := &fakeStore{}
	adapter := &fakeAdapter{snap: testSnapshot()}
	provider := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, _ answer.Request, n int) (answer.Result, error) {
		if n == 1 {
			return answer.Result{}, &answer.Error{Kind: "upstream_error", HTTPStatus: 429, Retryable: true, BilledUnknown: true}
		}
		return solved("good", 3), nil
	}}
	req := recoveryRequest()
	req.MaxModelCalls, req.MaxEvaluations = 5, 5
	r, err := New(store, provider, Config{}).Run(context.Background(), adapter, req)
	if err != nil || r.Status != "selftest_passed" || r.ModelCalls != 2 || r.CostMicroUSD != 3 || r.ReservedMicroUSD != 10 {
		t.Fatalf("result=%+v err=%v", r, err)
	}
	if len(store.usages) != 2 || store.usages[0].Status != "unknown" || store.usages[0].ReservedMicroUSD != 10 || store.usages[1].Status != "settled" {
		t.Fatalf("retry lost or cleared unknown billed usage: %+v", store.usages)
	}
}

func TestSuccessfulModelRetryDoesNotReplayUncertainSelfTest(t *testing.T) {
	adapter := &brokenHostAdapter{fakeAdapter{snap: testSnapshot()}}
	provider := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, _ answer.Request, n int) (answer.Result, error) {
		if n == 1 {
			return solved("partial", 1), &answer.Error{Kind: "truncated_output"}
		}
		return solved("good", 1), nil
	}}
	req := recoveryRequest()
	req.MaxModelCalls, req.MaxEvaluations = 5, 5
	r, err := New(&fakeStore{}, provider, Config{}).Run(context.Background(), adapter, req)
	if err == nil || errors.Is(err, context.Canceled) || r.Status != "uncertain" || r.ModelCalls != 2 || adapter.selfTests != 1 || adapter.submits != 0 {
		t.Fatalf("result=%+v err=%v selftests=%d submits=%d", r, err, adapter.selfTests, adapter.submits)
	}
}
