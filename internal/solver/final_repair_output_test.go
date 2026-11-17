package solver

import (
	"context"
	"testing"

	"educoder-cli/internal/answer"
)

func TestFinalProgrammingRepairReservesCompleteOutputBeforeFirstTruncation(t *testing.T) {
	for _, tc := range []struct {
		name        string
		calls       int
		evaluations int
	}{
		{name: "last model call", calls: 2, evaluations: 3},
		{name: "last evaluation", calls: 3, evaluations: 2},
		{name: "third call after successful reasoning output", calls: 3, evaluations: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{}
			last := min(tc.calls, tc.evaluations)
			provider := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, req answer.Request, n int) (answer.Result, error) {
				if n < last {
					if n > 1 && req.Profile != answer.RepairProfile() {
						t.Fatalf("non-final repair lost selected reasoning profile: %+v", req.Profile)
					}
					return solved([]string{"bad-a", "bad-b"}[n-1], 2), nil
				}
				if req.Previous == nil || len(req.RepairHistory) == 0 || len(req.Feedback) == 0 {
					t.Fatal("complete-output guard discarded source-bound repair evidence")
				}
				if req.Profile.Thinking != "disabled" || req.Profile.ReasoningEffort != "" || req.Profile.MaxTokens != 8192 {
					// Simulate a first truncation on the final allowed repair. The
					// old strategy cannot recover because no fourth call exists.
					return solved("partial", 2), &answer.Error{Kind: "truncated_output", Retryable: true}
				}
				return solved("good", 3), nil
			}}
			req := recoveryRequest()
			req.MaxModelCalls, req.MaxEvaluations, req.BudgetMicroUSD = tc.calls, tc.evaluations, 100
			adapter := &fakeAdapter{snap: testSnapshot()}
			result, err := New(store, provider, Config{}).Run(context.Background(), adapter, req)
			if err != nil || result.Status != "selftest_passed" || result.ModelCalls != last || result.CostMicroUSD != int64((last-1)*2+3) || result.ReservedMicroUSD != 0 || len(store.usages) != last || adapter.selfTests != last {
				t.Fatalf("final repair failed or changed budgets/charges: result=%+v err=%v usages=%d tests=%d", result, err, len(store.usages), adapter.selfTests)
			}
			for i, request := range provider.requests {
				if provider.estimates[i].Profile != request.Profile {
					t.Fatal("dispatch used a different profile from its charged estimate")
				}
			}
		})
	}
}

func TestFinalProgrammingRepairOutputGuardSurvivesContinuation(t *testing.T) {
	store := &fakeStore{}
	provider := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, req answer.Request, n int) (answer.Result, error) {
		if n == 1 {
			return solved("bad", 2), nil
		}
		if req.Profile.Thinking != "disabled" || req.Profile.ReasoningEffort != "" || req.Profile.MaxTokens != 8192 || req.Previous == nil || len(req.RepairHistory) == 0 {
			t.Fatalf("continued final repair lost output window or bound evidence: %+v", req)
		}
		return solved("good", 3), nil
	}}
	req := recoveryRequest()
	req.MaxModelCalls, req.MaxEvaluations, req.BudgetMicroUSD = 1, 1, 100
	result, err := New(store, provider, Config{}).Run(context.Background(), &fakeAdapter{snap: testSnapshot()}, req)
	if err != nil || result.Status != "selftest_failed" || result.ModelCalls != 1 {
		t.Fatalf("first=%+v err=%v", result, err)
	}
	result, err = New(store, provider, Config{}).ContinueRun(context.Background(), &fakeAdapter{snap: testSnapshot()}, req, result.RunID, Continuation{AdditionalModelCalls: 1, AdditionalEvaluations: 1})
	if err != nil || result.Status != "selftest_passed" || result.ModelCalls != 2 || result.CostMicroUSD != 5 || len(store.usages) != 2 {
		t.Fatalf("continued=%+v err=%v usage=%+v", result, err, store.usages)
	}
}

func TestFinalProgrammingOutputGuardKeepsInitialProfileWithoutRepairEvidence(t *testing.T) {
	profile := answer.RepairProfile()
	provider := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, req answer.Request, _ int) (answer.Result, error) {
		if req.Profile != profile || req.Previous != nil || len(req.Feedback) != 0 || len(req.RepairHistory) != 0 {
			t.Fatalf("initial generation was mistaken for a repair: %+v", req)
		}
		return solved("good", 3), nil
	}}
	req := recoveryRequest()
	req.MaxModelCalls, req.MaxEvaluations = 1, 1
	result, err := New(&fakeStore{}, provider, Config{Profile: profile}).Run(context.Background(), &fakeAdapter{snap: testSnapshot()}, req)
	if err != nil || result.Status != "selftest_passed" || result.ModelCalls != 1 {
		t.Fatalf("initial=%+v err=%v", result, err)
	}
}
