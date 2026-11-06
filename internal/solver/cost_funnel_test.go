package solver

import (
	"context"
	"fmt"
	"testing"

	"educoder-cli/internal/answer"
)

func TestCostFunnelCapsProgrammingLifetimeCallsAndEvaluations(t *testing.T) {
	store := &fakeStore{}
	p := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, req answer.Request, n int) (answer.Result, error) {
		if req.Profile.Model != answer.DefaultProfile().Model {
			t.Fatalf("automatic expensive model switch: %+v", req.Profile)
		}
		return solved(fmt.Sprintf("bad-%d", n), 1), nil
	}}
	req := recoveryRequest()
	req.MaxModelCalls, req.MaxEvaluations, req.BudgetMicroUSD = 40, 40, 1000
	a := &fakeAdapter{snap: testSnapshot()}
	r, err := New(store, p, Config{MaxModelCalls: 40, MaxEvaluations: 40}).Run(context.Background(), a, req)
	if err != nil || r.ModelCalls != 3 || a.selfTests != 3 || len(store.usages) != 3 {
		t.Fatalf("hard ceiling bypassed: status=%s calls=%d evaluations=%d usage=%d err=%v", r.Status, r.ModelCalls, a.selfTests, len(store.usages), err)
	}
	r, err = New(store, p, Config{}).ContinueRun(context.Background(), a, req, r.RunID, Continuation{AdditionalModelCalls: 40, AdditionalEvaluations: 40})
	if err != nil || r.ModelCalls != 3 || a.selfTests != 3 || len(store.usages) != 3 {
		t.Fatalf("continuation bypassed lifetime ceiling: result=%+v evaluations=%d usage=%d err=%v", r, a.selfTests, len(store.usages), err)
	}
	t.Logf("calls=%d evaluations=%d usage=%d; continuation adds zero calls", r.ModelCalls, a.selfTests, len(store.usages))
}

func TestCostFunnelStopsAtFirstRepeatedFailedCandidate(t *testing.T) {
	store := &fakeStore{}
	p := &scriptedProvider{estimateCost: 10, generate: func(context.Context, answer.Request, int) (answer.Result, error) { return solved("bad", 2), nil }}
	a := &fakeAdapter{snap: testSnapshot()}
	req := recoveryRequest()
	req.MaxModelCalls, req.MaxEvaluations = 40, 40
	r, err := New(store, p, Config{}).Run(context.Background(), a, req)
	if err != nil || r.Status != "generation_stalled" || r.ModelCalls != 2 || a.selfTests != 1 || len(store.usages) != 2 {
		t.Fatalf("repeat was paid for again: result=%+v evaluations=%d usage=%d err=%v", r, a.selfTests, len(store.usages), err)
	}
	t.Logf("status=%s calls=%d evaluations=%d cost=%d", r.Status, r.ModelCalls, a.selfTests, r.CostMicroUSD)
}
