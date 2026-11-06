package solver

import (
	"context"
	"educoder-cli/internal/answer"
	"encoding/json"
	"testing"
)

func proBodyDeadlineError() *answer.Error {
	return &answer.Error{Kind: "response_read_failed", HTTPStatus: 200, BilledUnknown: true, ResponsePhase: "body", ReadCause: "deadline", BytesRead: 3, ExpectedBytes: -1}
}

// A deliberately configured initial Pro call can still downgrade within the
// same three-call budget. Default runs never select Pro automatically.
func TestConfiguredProDeadlineDoesNotRetryIdenticalFallbackModel(t *testing.T) {
	store := &fakeStore{}
	p := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, req answer.Request, n int) (answer.Result, error) {
		if n == 1 {
			return answer.Result{}, proBodyDeadlineError()
		}
		if req.Profile.Model != answer.DefaultProfile().Model || req.Profile.Thinking != "disabled" || req.Profile.MaxTokens != 8192 {
			t.Fatalf("fallback profile=%+v", req.Profile)
		}
		return solved("good", 3), nil
	}}
	req := recoveryRequest()
	req.MaxModelCalls, req.MaxEvaluations = 40, 40
	r, err := New(store, p, Config{Profile: answer.ProProfile()}).Run(context.Background(), &fakeAdapter{snap: testSnapshot()}, req)
	if err == nil || r.Status != "model_error" || r.ModelCalls != 1 || r.ReservedMicroUSD != 10 || r.CostMicroUSD != 0 || len(store.usages) != 1 || store.usages[0].Status != "unknown" {
		t.Fatalf("fallback reset usage or cap: result=%+v err=%v usage=%+v", r, err, store.usages)
	}
}

func TestOptionalFreshProHasNoRepairHistoryAndNoFourthCall(t *testing.T) {
	store := &fakeStore{}
	p := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, req answer.Request, n int) (answer.Result, error) {
		if n < 3 {
			return solved([]string{"bad-a", "bad-b"}[n-1], 2), nil
		}
		if req.Profile.Model != answer.ProProfile().Model || req.Previous != nil || len(req.Feedback) != 0 || len(req.RepairHistory) != 0 {
			t.Fatalf("Pro was a repetitive repair: %+v", req)
		}
		return answer.Result{}, proBodyDeadlineError()
	}}
	req := recoveryRequest()
	req.MaxModelCalls, req.MaxEvaluations = 40, 40
	a := &fakeAdapter{snap: testSnapshot()}
	r, err := New(store, p, Config{EnableProFreshSolve: true}).Run(context.Background(), a, req)
	if err == nil || r.Status != "model_error" || r.ModelCalls != 3 || r.CostMicroUSD != 4 || r.ReservedMicroUSD != 10 || len(store.usages) != 3 || a.selfTests != 2 {
		t.Fatalf("optional Pro exceeded cap: result=%+v err=%v", r, err)
	}
	var saved runState
	if json.Unmarshal(store.runs[r.RunID].Data, &saved) != nil || !saved.GenerationRecovery.ProFreshSolveUsed {
		t.Fatal("one-shot Pro selection not durable")
	}
	r, err = New(store, p, Config{EnableProFreshSolve: true}).ContinueRun(context.Background(), a, req, r.RunID, Continuation{AdditionalModelCalls: 40, AdditionalEvaluations: 40})
	if err != nil || r.ModelCalls != 3 || r.ReservedMicroUSD != 10 || len(p.requests) != 3 {
		t.Fatalf("continuation replayed Pro: result=%+v err=%v", r, err)
	}
}

func TestProBodyDeadlineFallbackRequiresExactEvidence(t *testing.T) {
	for _, tc := range []struct {
		name  string
		alter func(*answer.Error)
	}{
		{"generic read", func(e *answer.Error) { e.ReadCause = "read" }},
		{"unexpected EOF", func(e *answer.Error) { e.ReadCause = "unexpected_eof" }},
		{"canceled", func(e *answer.Error) { e.ReadCause = "canceled" }},
		{"other successful HTTP status", func(e *answer.Error) { e.HTTPStatus = 201 }},
		{"server HTTP status", func(e *answer.Error) { e.HTTPStatus = 500 }},
		{"legacy unclassified response", func(e *answer.Error) { e.ResponsePhase, e.ReadCause = "", "" }},
		{"wrong response phase", func(e *answer.Error) { e.ResponsePhase = "headers" }},
		{"unknown billing not indicated", func(e *answer.Error) { e.BilledUnknown = false }},
		{"other provider failure", func(e *answer.Error) { e.Kind = "timeout" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var g generationRecovery
			e := proBodyDeadlineError()
			tc.alter(e)
			if g.fallbackAfterProBodyDeadline(defaultConfig(), answer.ProProfile(), e) {
				t.Fatal("unrelated error enabled fallback")
			}
		})
	}
	for _, cfg := range []Config{defaultConfig(), {Profile: answer.ProProfile(), ProProfile: answer.ProProfile()}} {
		var g generationRecovery
		if g.fallbackAfterProBodyDeadline(cfg, cfg.Profile, proBodyDeadlineError()) {
			t.Fatal("baseline or identical-model fallback")
		}
	}
}
