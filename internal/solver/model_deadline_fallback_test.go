package solver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

func proBodyDeadlineError() *answer.Error {
	return &answer.Error{Kind: "response_read_failed", HTTPStatus: 200, BilledUnknown: true, ResponsePhase: "body", ReadCause: "deadline", BytesRead: 3, ExpectedBytes: -1}
}

func deadlineFallbackRequest() Request {
	req := recoveryRequest()
	req.Mode = ModeSubmit
	req.MaxModelCalls, req.MaxEvaluations, req.BudgetMicroUSD = 6, 6, 100
	return req
}

// Two distinct, confirmed selftest failures select PRO through normal policy.
// Tests then script its response without a real provider or long timeout.
func deadlineFallbackProvider(generate func(context.Context, answer.Request, int) (answer.Result, error)) *scriptedProvider {
	return &scriptedProvider{estimateCost: 10, generate: func(ctx context.Context, req answer.Request, n int) (answer.Result, error) {
		if n <= 2 {
			result := solved(fmt.Sprintf("bad%d", n), 1)
			result.Model = req.Profile.Model
			return result, nil
		}
		return generate(ctx, req, n)
	}}
}

func assertDeadlineFallbackProfile(t *testing.T, req answer.Request) {
	t.Helper()
	if req.Profile.Model != answer.DefaultProfile().Model || req.Profile.Thinking != "disabled" || req.Profile.ReasoningEffort != "" || req.Profile.MaxTokens != 8192 {
		t.Fatalf("fallback did not use distinct compact baseline profile: %+v", req.Profile)
	}
}

func TestProBodyDeadlineFallsBackAndRetainsUnknownReservation(t *testing.T) {
	store := &fakeStore{}
	adapter := &fakeAdapter{snap: testSnapshot()}
	p := deadlineFallbackProvider(func(_ context.Context, req answer.Request, n int) (answer.Result, error) {
		if n == 3 {
			if req.Profile.Model != answer.ProProfile().Model || adapter.selfTests != 2 || adapter.submits != 0 {
				t.Fatal("PRO was not reached after confirmed failures and before formal submission")
			}
			return answer.Result{}, proBodyDeadlineError()
		}
		assertDeadlineFallbackProfile(t, req)
		if req.Previous == nil || req.Previous.Files[0].Content != "bad2" || len(req.RepairHistory) != 2 || adapter.selfTests != 2 || adapter.submits != 0 {
			t.Fatal("fallback lost bound source history or replayed a platform operation")
		}
		for _, record := range store.runs {
			var saved runState
			if json.Unmarshal(record.Data, &saved) != nil || !saved.GenerationRecovery.ProBodyDeadlineFallbackUsed || saved.Result.ModelCalls != 4 || saved.Result.ReservedMicroUSD != 20 {
				t.Fatalf("fallback dispatch did not durably reserve its own call and retain the PRO reservation: %+v", saved)
			}
		}
		result := solved("good", 3)
		result.Model = req.Profile.Model
		return result, nil
	})
	r, err := New(store, p, Config{}).Run(context.Background(), adapter, deadlineFallbackRequest())
	if err != nil || r.Status != "passed" || r.ModelCalls != 4 || r.CostMicroUSD != 5 || r.ReservedMicroUSD != 10 || adapter.selfTests != 3 || adapter.submits != 1 {
		t.Fatalf("result=%+v err=%v tests=%d submits=%d", r, err, adapter.selfTests, adapter.submits)
	}
	if len(store.usages) != 4 || store.usages[2].Status != "unknown" || store.usages[2].ReservedMicroUSD != 10 || store.usages[2].Model != answer.ProProfile().Model || store.usages[3].Status != "settled" || store.usages[3].Model != answer.DefaultProfile().Model {
		t.Fatalf("unknown PRO usage was lost or settled by the fallback: %+v", store.usages)
	}
}

func TestFallbackBodyDeadlineStopsWithoutAnotherDispatch(t *testing.T) {
	store := &fakeStore{}
	adapter := &fakeAdapter{snap: testSnapshot()}
	p := deadlineFallbackProvider(func(_ context.Context, req answer.Request, n int) (answer.Result, error) {
		if n >= 4 {
			assertDeadlineFallbackProfile(t, req)
		}
		return answer.Result{}, proBodyDeadlineError()
	})
	r, err := New(store, p, Config{}).Run(context.Background(), adapter, deadlineFallbackRequest())
	if err == nil || r.Status != "model_error" || r.ModelCalls != 4 || r.CostMicroUSD != 2 || r.ReservedMicroUSD != 20 || adapter.selfTests != 2 || adapter.submits != 0 || len(store.usages) != 4 {
		t.Fatalf("repeated an unknown billed response: result=%+v err=%v tests=%d submits=%d usage=%+v", r, err, adapter.selfTests, adapter.submits, store.usages)
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
			store := &fakeStore{}
			providerErr := proBodyDeadlineError()
			tc.alter(providerErr)
			p := deadlineFallbackProvider(func(context.Context, answer.Request, int) (answer.Result, error) {
				return answer.Result{}, providerErr
			})
			r, err := New(store, p, Config{}).Run(context.Background(), &fakeAdapter{snap: testSnapshot()}, deadlineFallbackRequest())
			var saved runState
			json.Unmarshal(store.runs[r.RunID].Data, &saved)
			if err == nil || r.Status != "model_error" || r.ModelCalls != 3 || saved.GenerationRecovery.ProBodyDeadlineFallbackUsed || r.ReservedMicroUSD != 10 {
				t.Fatalf("nonmatching response enabled fallback: result=%+v err=%v state=%+v", r, err, saved.GenerationRecovery)
			}
		})
	}
	for _, tc := range []struct {
		name    string
		profile answer.Profile
		cfg     Config
	}{
		{"baseline failed", answer.DefaultProfile(), defaultConfig()},
		{"no distinct fallback model", answer.ProProfile(), Config{Profile: answer.ProProfile(), ProProfile: answer.ProProfile()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var recovery generationRecovery
			if recovery.fallbackAfterProBodyDeadline(tc.cfg, tc.profile, proBodyDeadlineError()) || recovery.ProBodyDeadlineFallbackUsed {
				t.Fatal("unmatched or identical model selected for fallback")
			}
		})
	}
}

func TestProBodyDeadlineFallbackRespectsRemainingLimits(t *testing.T) {
	for _, tc := range []struct {
		name   string
		alter  func(*Request)
		status string
	}{
		{"call limit", func(r *Request) { r.MaxModelCalls = 3 }, "model_error"},
		{"evaluation limit", func(r *Request) { r.MaxEvaluations = 3 }, "model_error"},
		{"money includes unknown reservation", func(r *Request) { r.BudgetMicroUSD = 21 }, "budget_exhausted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{}
			adapter := &fakeAdapter{snap: testSnapshot()}
			p := deadlineFallbackProvider(func(context.Context, answer.Request, int) (answer.Result, error) {
				return answer.Result{}, proBodyDeadlineError()
			})
			req := deadlineFallbackRequest()
			tc.alter(&req)
			r, err := New(store, p, Config{}).Run(context.Background(), adapter, req)
			if r.Status != tc.status || r.ModelCalls != 3 || r.ReservedMicroUSD != 10 || r.CostMicroUSD != 2 || len(store.usages) != 3 || adapter.selfTests != 2 || adapter.submits != 0 {
				t.Fatalf("exceeded remaining limit: result=%+v err=%v usage=%+v", r, err, store.usages)
			}
		})
	}
	for _, canceled := range []bool{true, false} {
		t.Run(fmt.Sprintf("inactive_context_canceled_%v", canceled), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req := deadlineFallbackRequest()
			if !canceled {
				req.Timeout = 10 * time.Millisecond
			}
			store := &fakeStore{}
			p := deadlineFallbackProvider(func(callCtx context.Context, _ answer.Request, _ int) (answer.Result, error) {
				if canceled {
					cancel()
				} else {
					<-callCtx.Done()
				}
				return answer.Result{}, proBodyDeadlineError()
			})
			r, err := New(store, p, Config{}).Run(ctx, &fakeAdapter{snap: testSnapshot()}, req)
			var saved runState
			json.Unmarshal(store.runs[r.RunID].Data, &saved)
			if err == nil || r.ModelCalls != 3 || r.ReservedMicroUSD != 10 || saved.GenerationRecovery.ProBodyDeadlineFallbackUsed || len(store.usages) != 3 {
				t.Fatalf("inactive context selected fallback: result=%+v err=%v recovery=%+v", r, err, saved.GenerationRecovery)
			}
		})
	}
}

func TestProBodyDeadlineFallbackSurvivesExplicitContinuation(t *testing.T) {
	store := &fakeStore{}
	p := deadlineFallbackProvider(func(_ context.Context, req answer.Request, n int) (answer.Result, error) {
		if n == 3 {
			return answer.Result{}, proBodyDeadlineError()
		}
		assertDeadlineFallbackProfile(t, req)
		return solved("good", 3), nil
	})
	req := deadlineFallbackRequest()
	req.MaxModelCalls, req.MaxEvaluations = 3, 3
	r, err := New(store, p, Config{}).Run(context.Background(), &fakeAdapter{snap: testSnapshot()}, req)
	if err == nil || r.Status != "model_error" || r.ReservedMicroUSD != 10 {
		t.Fatalf("first run=%+v err=%v", r, err)
	}
	var saved runState
	if json.Unmarshal(store.runs[r.RunID].Data, &saved) != nil || !saved.GenerationRecovery.ProBodyDeadlineFallbackUsed {
		t.Fatalf("model selection was not durably checkpointed: %+v", saved)
	}
	r, err = New(store, p, Config{}).ContinueRun(context.Background(), &fakeAdapter{snap: testSnapshot()}, recoveryRequest(), r.RunID, Continuation{AdditionalModelCalls: 1, AdditionalEvaluations: 1})
	if err != nil || r.Status != "selftest_passed" || r.ModelCalls != 4 || r.CostMicroUSD != 5 || r.ReservedMicroUSD != 10 || len(store.usages) != 4 {
		t.Fatalf("continuation reset selection or cost: result=%+v err=%v usage=%+v", r, err, store.usages)
	}
}

func TestConfirmedCodeRepairRemainsOnFallbackModel(t *testing.T) {
	adapter := &fakeAdapter{snap: testSnapshot()}
	p := deadlineFallbackProvider(func(_ context.Context, req answer.Request, n int) (answer.Result, error) {
		if n == 3 {
			return answer.Result{}, proBodyDeadlineError()
		}
		assertDeadlineFallbackProfile(t, req)
		if n == 4 {
			return solved("bad4", 2), nil
		}
		if req.Previous == nil || req.Previous.Files[0].Content != "bad4" {
			t.Fatal("fallback repair lost the most recent rejected source")
		}
		return solved("good", 3), nil
	})
	r, err := New(&fakeStore{}, p, Config{}).Run(context.Background(), adapter, deadlineFallbackRequest())
	if err != nil || r.Status != "passed" || r.ModelCalls != 5 || r.ReservedMicroUSD != 10 || r.CostMicroUSD != 7 || adapter.selfTests != 4 || adapter.submits != 1 {
		t.Fatalf("repair returned to stalled model: result=%+v err=%v", r, err)
	}
}

type fallbackUnknownPlatformAdapter struct {
	fakeAdapter
	formal bool
}

func (a *fallbackUnknownPlatformAdapter) SelfTest(ctx context.Context, target Target, file problem.SolutionFile, input, expected string) (SelfTest, error) {
	if !a.formal && a.selfTests == 2 {
		a.selfTests++
		return SelfTest{}, errors.New("selftest response disconnected")
	}
	return a.fakeAdapter.SelfTest(ctx, target, file, input, expected)
}

func (a *fallbackUnknownPlatformAdapter) Submit(context.Context, Target, problem.SolutionFile) (Evaluation, error) {
	a.submits++
	return Evaluation{}, errors.New("formal response disconnected")
}

func TestFallbackDoesNotReplayUnknownPlatformOperations(t *testing.T) {
	for _, formal := range []bool{false, true} {
		t.Run(fmt.Sprintf("formal_%v", formal), func(t *testing.T) {
			store := &fakeStore{}
			adapter := &fallbackUnknownPlatformAdapter{fakeAdapter: fakeAdapter{snap: testSnapshot()}, formal: formal}
			p := deadlineFallbackProvider(func(_ context.Context, req answer.Request, n int) (answer.Result, error) {
				if n == 3 {
					return answer.Result{}, proBodyDeadlineError()
				}
				assertDeadlineFallbackProfile(t, req)
				return solved("good", 3), nil
			})
			r, err := New(store, p, Config{}).Run(context.Background(), adapter, deadlineFallbackRequest())
			if err == nil || r.Status != "uncertain" || r.ModelCalls != 4 || r.ReservedMicroUSD != 10 || adapter.selfTests != 3 || adapter.submits != boolInt(formal) {
				t.Fatalf("unknown platform result replayed: result=%+v err=%v tests=%d submits=%d", r, err, adapter.selfTests, adapter.submits)
			}
			_, err = New(store, p, Config{}).ContinueRun(context.Background(), adapter, recoveryRequest(), r.RunID, Continuation{AdditionalModelCalls: 2, AdditionalEvaluations: 2})
			if err == nil || len(p.requests) != 4 || adapter.selfTests != 3 || adapter.submits != boolInt(formal) {
				t.Fatal("fallback allowed continuation of an unknown platform mutation")
			}
		})
	}
}

type fallbackCheckpointFailureStore struct{ fakeStore }

func (s *fallbackCheckpointFailureStore) PutRun(ctx context.Context, record questionbank.RunRecord) error {
	var saved runState
	if json.Unmarshal(record.Data, &saved) == nil && record.Status == "running" && saved.GenerationRecovery.ProBodyDeadlineFallbackUsed {
		return errors.New("fallback checkpoint unavailable")
	}
	return s.fakeStore.PutRun(ctx, record)
}

func TestFallbackCheckpointFailurePreventsPaidDispatch(t *testing.T) {
	store := &fallbackCheckpointFailureStore{}
	p := deadlineFallbackProvider(func(context.Context, answer.Request, int) (answer.Result, error) {
		return answer.Result{}, proBodyDeadlineError()
	})
	r, err := New(store, p, Config{}).Run(context.Background(), &fakeAdapter{snap: testSnapshot()}, deadlineFallbackRequest())
	if err == nil || r.Status != "checkpoint_persistence_failed" || r.ModelCalls != 3 || r.ReservedMicroUSD != 10 || len(store.usages) != 3 {
		t.Fatalf("paid dispatch occurred before fallback checkpoint: result=%+v err=%v usage=%+v", r, err, store.usages)
	}
}
