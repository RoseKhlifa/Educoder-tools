package solver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"educoder-cli/internal/answer"
)

func availabilityError(status int) *answer.Error {
	return &answer.Error{Kind: "upstream_error", HTTPStatus: status, Retryable: true, BilledUnknown: true}
}

func TestAvailabilityFailuresKeepSelectedProfileAndUnknownCharges(t *testing.T) {
	store := &fakeStore{}
	adapter := &fakeAdapter{snap: testSnapshot()}
	p := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, req answer.Request, n int) (answer.Result, error) {
		if req.Profile != answer.DefaultProfile() || len(req.Feedback) != 0 || req.Previous != nil {
			t.Fatalf("provider outage was treated as a code repair: %+v", req)
		}
		if n <= 2 {
			return answer.Result{}, availabilityError([]int{503, 429}[n-1])
		}
		return solved("good", 3), nil
	}}
	req := recoveryRequest()
	req.MaxModelCalls, req.MaxEvaluations = 5, 5
	r, err := New(store, p, Config{ProviderRetryWait: skipProviderRetryWait}).Run(context.Background(), adapter, req)
	if err != nil || r.Status != "selftest_passed" || r.ModelCalls != 3 || r.ReservedMicroUSD != 20 || r.CostMicroUSD != 3 || adapter.selfTests != 1 || adapter.submits != 0 {
		t.Fatalf("result=%+v err=%v tests=%d submits=%d", r, err, adapter.selfTests, adapter.submits)
	}
	if len(store.usages) != 3 || store.usages[0].Status != "unknown" || store.usages[1].Status != "unknown" || store.usages[2].Status != "settled" {
		t.Fatalf("availability failure usage was refunded or omitted: %+v", store.usages)
	}
	var saved runState
	if err := json.Unmarshal(store.runs[r.RunID].Data, &saved); err != nil || saved.GenerationRecovery.ProviderAvailabilityFailures != 2 {
		t.Fatalf("availability distinction was not persisted: %+v err=%v", saved.GenerationRecovery, err)
	}
}

func TestAvailabilityFailuresDoNotSuppressRealRepairEscalation(t *testing.T) {
	want := []answer.Profile{answer.DefaultProfile(), answer.DefaultProfile(), answer.RepairProfile(), answer.ProProfile()}
	store := &fakeStore{}
	p := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, req answer.Request, n int) (answer.Result, error) {
		if n > len(want) || req.Profile != want[n-1] {
			t.Fatalf("call %d selected incorrect repair profile: %+v", n, req.Profile)
		}
		if n == 1 {
			return answer.Result{}, availabilityError(503)
		}
		if n < 4 {
			return solved(fmt.Sprintf("bad%d", n), 3), nil
		}
		return solved("good", 3), nil
	}}
	req := recoveryRequest()
	req.MaxModelCalls, req.MaxEvaluations = 5, 5
	a := &fakeAdapter{snap: testSnapshot()}
	r, err := New(store, p, Config{ProviderRetryWait: skipProviderRetryWait}).Run(context.Background(), a, req)
	if err != nil || r.Status != "selftest_passed" || r.ModelCalls != 4 || a.selfTests != 3 || r.ReservedMicroUSD != 10 || r.CostMicroUSD != 9 {
		t.Fatalf("result=%+v err=%v tests=%d", r, err, a.selfTests)
	}
}

func TestAvailabilityFailureKeepsTruncationRecovery(t *testing.T) {
	store := &fakeStore{}
	p := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, req answer.Request, n int) (answer.Result, error) {
		switch n {
		case 1:
			return answer.Result{}, availabilityError(503)
		case 2:
			if req.Profile != answer.DefaultProfile() {
				t.Fatalf("availability failure changed profile: %+v", req.Profile)
			}
			return solved("partial", 2), &answer.Error{Kind: "truncated_output"}
		default:
			if req.Profile.Model != answer.RepairProfile().Model || req.Profile.MaxTokens != 8192 || req.Profile.Thinking != "disabled" || req.Profile.ReasoningEffort != "" || req.Previous == nil {
				t.Fatalf("availability distinction lost output repair: %+v", req)
			}
			return solved("good", 3), nil
		}
	}}
	req := recoveryRequest()
	req.MaxModelCalls, req.MaxEvaluations = 5, 5
	r, err := New(store, p, Config{ProviderRetryWait: skipProviderRetryWait}).Run(context.Background(), &fakeAdapter{snap: testSnapshot()}, req)
	if err != nil || r.Status != "selftest_passed" || r.ModelCalls != 3 || r.ReservedMicroUSD != 10 || r.CostMicroUSD != 5 {
		t.Fatalf("result=%+v err=%v", r, err)
	}
}

func TestAvailabilityDistinctionSurvivesExplicitContinuation(t *testing.T) {
	store := &fakeStore{}
	p := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, req answer.Request, n int) (answer.Result, error) {
		if req.Profile != answer.DefaultProfile() {
			t.Fatalf("continuation escalated for historical availability failure: %+v", req.Profile)
		}
		if n == 1 {
			return answer.Result{}, availabilityError(503)
		}
		return solved("good", 3), nil
	}}
	req := recoveryRequest()
	req.MaxModelCalls, req.MaxEvaluations, req.BudgetMicroUSD = 1, 1, 100
	r, err := New(store, p, Config{ProviderRetryWait: skipProviderRetryWait}).Run(context.Background(), &fakeAdapter{snap: testSnapshot()}, req)
	if err == nil || r.Status != "model_error" || r.ModelCalls != 1 || r.ReservedMicroUSD != 10 {
		t.Fatalf("first=%+v err=%v", r, err)
	}
	r, err = New(store, p, Config{ProviderRetryWait: skipProviderRetryWait}).ContinueRun(context.Background(), &fakeAdapter{snap: testSnapshot()}, req, r.RunID, Continuation{AdditionalModelCalls: 1, AdditionalEvaluations: 1})
	if err != nil || r.Status != "selftest_passed" || r.ModelCalls != 2 || r.ReservedMicroUSD != 10 || r.CostMicroUSD != 3 || len(store.usages) != 2 {
		t.Fatalf("continued=%+v err=%v usage=%+v", r, err, store.usages)
	}
}

func TestAvailabilityFailuresStillExhaustCallAndMoneyBudgets(t *testing.T) {
	for _, tc := range []struct {
		name   string
		calls  int
		budget int64
		want   int
		status string
	}{
		{"calls", 2, 100, 2, "model_error"},
		{"money", 5, 15, 1, "budget_exhausted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{}
			p := &scriptedProvider{estimateCost: 10, generate: func(context.Context, answer.Request, int) (answer.Result, error) {
				return answer.Result{}, availabilityError(503)
			}}
			req := recoveryRequest()
			req.MaxModelCalls, req.MaxEvaluations, req.BudgetMicroUSD = tc.calls, 5, tc.budget
			a := &fakeAdapter{snap: testSnapshot()}
			r, _ := New(store, p, Config{ProviderRetryWait: skipProviderRetryWait}).Run(context.Background(), a, req)
			if r.Status != tc.status || r.ModelCalls != tc.want || len(p.requests) != tc.want || r.ReservedMicroUSD != int64(tc.want*10) || a.selfTests != 0 || a.submits != 0 {
				t.Fatalf("result=%+v calls=%d tests=%d submits=%d", r, len(p.requests), a.selfTests, a.submits)
			}
		})
	}
}

func TestAvailabilityClassificationDoesNotBroadenResponseRecovery(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"rate limit", availabilityError(429), true},
		{"server failure", availabilityError(503), true},
		{"wrapped server failure", fmt.Errorf("wrapped: %w", availabilityError(503)), true},
		{"server body interrupted", &answer.Error{Kind: "response_read_failed", HTTPStatus: 503, Retryable: true, ReadCause: "unexpected_eof"}, true},
		{"network", &answer.Error{Kind: "network_error", Retryable: true, BilledUnknown: true}, true},
		{"no response timeout", &answer.Error{Kind: "timeout", Retryable: true, BilledUnknown: true}, true},
		{"unauthorized", availabilityError(401), false},
		{"nonretryable", &answer.Error{Kind: "upstream_error", HTTPStatus: 503}, false},
		{"successful body deadline", proBodyDeadlineError(), false},
		{"successful body interrupted", &answer.Error{Kind: "response_read_failed", HTTPStatus: 200, Retryable: true, ReadCause: "unexpected_eof"}, false},
		{"canceled body", &answer.Error{Kind: "response_read_failed", HTTPStatus: 503, Retryable: true, ReadCause: "canceled"}, false},
		{"truncated", &answer.Error{Kind: "truncated_output", Retryable: true}, false},
		{"invalid output", &answer.Error{Kind: "invalid_output_json", Retryable: true}, false},
		{"candidate contract", &answer.Error{Kind: "candidate_contract", Retryable: true}, false},
		{"canceled", context.Canceled, false},
		{"unclassified", errors.New("503"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := providerAvailabilityFailure(answer.Result{}, tc.err); got != tc.want {
				t.Fatalf("classified %v as availability=%v", tc.err, got)
			}
		})
	}
	if providerAvailabilityFailure(solved("partial", 3), availabilityError(503)) {
		t.Fatal("partial parsed candidate was reclassified as no generated output")
	}
}
