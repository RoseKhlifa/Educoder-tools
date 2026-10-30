package solver

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

type scriptedProvider struct {
	requests     []answer.Request
	estimates    []answer.Request
	estimateCost int64
	estimateErr  error
	generate     func(context.Context, answer.Request, int) (answer.Result, error)
}

func (p *scriptedProvider) Estimate(req answer.Request) (answer.Estimate, error) {
	p.estimates = append(p.estimates, req)
	return answer.Estimate{CostMicroUSD: p.estimateCost, Model: req.Profile.Model, PriceVersion: "test-pricing"}, p.estimateErr
}
func (p *scriptedProvider) Generate(ctx context.Context, req answer.Request) (answer.Result, error) {
	p.requests = append(p.requests, req)
	return p.generate(ctx, req, len(p.requests))
}
func solved(content string, cost int64) answer.Result {
	return answer.Result{Candidate: candidate(content), CostKnown: true, CostMicroUSD: cost, Model: "fake", Usage: answer.Usage{Known: true, PromptTokens: 11, CompletionTokens: 7}, FinishReason: "stop"}
}
func recoveryRequest() Request {
	return Request{Owner: "account", Course: "c", HomeworkID: 1, Game: "g", Mode: ModeSelfTest}
}

func TestAllModelProfilesAreEstimatedAndCharged(t *testing.T) {
	store := &fakeStore{}
	p := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, _ answer.Request, n int) (answer.Result, error) {
		if n < 3 {
			return solved("bad", 3), nil
		}
		return solved("good", 4), nil
	}}
	svc := New(store, p, Config{})
	r, err := svc.Run(context.Background(), &fakeAdapter{snap: testSnapshot()}, recoveryRequest())
	if err != nil || r.Status != "selftest_passed" || r.ModelCalls != 3 || r.CostMicroUSD != 10 || r.ReservedMicroUSD != 0 {
		t.Fatalf("result=%+v err=%v", r, err)
	}
	want := []answer.Profile{answer.DefaultProfile(), answer.RepairProfile(), answer.ProProfile()}
	for i, profile := range want {
		if p.estimates[i].Profile != profile || p.requests[i].Profile != profile {
			t.Fatalf("profile %d was not estimated before dispatch", i)
		}
	}
	if len(store.usages) != 3 {
		t.Fatalf("usage rows=%d", len(store.usages))
	}
	for _, u := range store.usages {
		if u.Status != "settled" || u.InputTokens != 11 || u.OutputTokens != 7 || u.PricingVersion != "test-pricing" {
			t.Fatalf("usage=%+v", u)
		}
	}
}

func TestUnknownPricingNeverDispatches(t *testing.T) {
	for _, estimateErr := range []error{errors.New("unknown pricing"), nil} {
		store := &fakeStore{}
		p := &scriptedProvider{estimateErr: estimateErr, generate: func(context.Context, answer.Request, int) (answer.Result, error) {
			t.Fatal("dispatched without positive known price")
			return answer.Result{}, nil
		}}
		r, err := New(store, p, Config{}).Run(context.Background(), &fakeAdapter{snap: testSnapshot()}, recoveryRequest())
		if err == nil || r.Status != "budget_unavailable" || len(store.usages) != 0 {
			t.Fatalf("result=%+v err=%v", r, err)
		}
	}
}

func TestUnknownBillingReservationAndPartialOutputSurviveRestart(t *testing.T) {
	ctx := context.Background()
	dbpath := filepath.Join(t.TempDir(), "bank.sqlite")
	db, err := questionbank.Open(dbpath)
	if err != nil {
		t.Fatal(err)
	}
	p := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, _ answer.Request, _ int) (answer.Result, error) {
		return answer.Result{Candidate: candidate("partial"), Model: "fake", Usage: answer.Usage{PromptTokens: 11}}, &answer.Error{Kind: "missing_usage", BilledUnknown: true}
	}}
	r, err := New(db, p, Config{}).Run(ctx, &fakeAdapter{snap: testSnapshot()}, recoveryRequest())
	if err == nil || r.ReservedMicroUSD != 10 || r.ModelCalls != 1 || r.CandidateID == 0 {
		t.Fatalf("result=%+v err=%v", r, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = questionbank.Open(dbpath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	read, err := New(db, nil, Config{}).Resume(ctx, r.RunID)
	if err != nil || read.ReservedMicroUSD != 10 || read.CandidateID != r.CandidateID {
		t.Fatalf("checkpoint=%+v err=%v", read, err)
	}
	ledger, err := db.UsageForRun(ctx, r.RunID)
	if err != nil || len(ledger) != 1 || ledger[0].Status != "unknown" || ledger[0].ReservedMicroUSD != 10 || ledger[0].Model != "fake" {
		t.Fatalf("ledger=%+v err=%v", ledger, err)
	}
	c, err := db.Candidate(ctx, r.CandidateID)
	if err != nil || c.Files[0].Content != "partial" {
		t.Fatalf("partial=%+v err=%v", c, err)
	}
}

func TestDeadlineStillPersistsChargedUsageAndCandidate(t *testing.T) {
	store := &fakeStore{}
	p := &scriptedProvider{estimateCost: 10, generate: func(ctx context.Context, _ answer.Request, _ int) (answer.Result, error) {
		<-ctx.Done()
		return solved("partial", 3), ctx.Err()
	}}
	req := recoveryRequest()
	req.Timeout = 15 * time.Millisecond
	r, err := New(store, p, Config{}).Run(context.Background(), &fakeAdapter{snap: testSnapshot()}, req)
	if !errors.Is(err, context.DeadlineExceeded) || r.CostMicroUSD != 3 || r.ReservedMicroUSD != 0 || len(store.usages) != 1 || r.CandidateID == 0 {
		t.Fatalf("result=%+v err=%v usage=%+v", r, err, store.usages)
	}
	saved, err := New(store, nil, Config{}).Resume(context.Background(), r.RunID)
	if err != nil || saved.Status != "model_error" || saved.CostMicroUSD != 3 {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}
}

type brokenHostAdapter struct{ fakeAdapter }

func (a *brokenHostAdapter) SelfTest(context.Context, Target, problem.SolutionFile, string, string) (SelfTest, error) {
	a.selfTests++
	return SelfTest{}, errors.New("runtime host disconnected")
}
func TestRuntimeFailureIsUncertainAndDoesNotAskModelToRepair(t *testing.T) {
	store := &fakeStore{}
	p := &fakeProvider{candidates: []answer.Candidate{candidate("good"), candidate("good")}}
	a := &brokenHostAdapter{fakeAdapter{snap: testSnapshot()}}
	r, err := New(store, p, Config{}).Run(context.Background(), a, recoveryRequest())
	if err == nil || r.Status != "uncertain" || p.calls != 1 || a.selfTests != 1 {
		t.Fatalf("result=%+v err=%v", r, err)
	}
	if len(store.validations) != 1 || store.validations[0].Result != "unknown" || store.candidates[0].Status == "rejected" {
		t.Fatalf("host error became wrong-code evidence: %+v", store.validations)
	}
}

func TestExplicitContinuationRetainsCostAndRepairsRejectedCache(t *testing.T) {
	store := &fakeStore{}
	p := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, req answer.Request, n int) (answer.Result, error) {
		if n == 1 {
			return solved("bad", 3), nil
		}
		if req.Previous == nil || req.Previous.Files[0].Content != "bad" || len(req.Feedback) == 0 {
			t.Fatal("failed candidate and feedback missing from continuation")
		}
		return solved("good", 4), nil
	}}
	svc := New(store, p, Config{})
	req := recoveryRequest()
	req.MaxModelCalls, req.MaxEvaluations, req.BudgetMicroUSD = 1, 1, 10
	r, err := svc.Run(context.Background(), &fakeAdapter{snap: testSnapshot()}, req)
	if err != nil || r.Status != "selftest_failed" || r.CostMicroUSD != 3 {
		t.Fatalf("first=%+v err=%v", r, err)
	}
	continued, err := New(store, p, Config{}).ContinueRun(context.Background(), &fakeAdapter{snap: testSnapshot()}, recoveryRequest(), r.RunID, Continuation{AdditionalModelCalls: 1, AdditionalEvaluations: 1, AdditionalBudgetMicroUSD: 10})
	if err != nil || continued.Status != "selftest_passed" || continued.ModelCalls != 2 || continued.CostMicroUSD != 7 {
		t.Fatalf("continued=%+v err=%v", continued, err)
	}
	var saved runState
	json.Unmarshal(store.runs[r.RunID].Data, &saved)
	if saved.Attempts != 2 || saved.Limits.MaxModelCalls != 2 || saved.Limits.BudgetMicroUSD != 20 {
		t.Fatalf("bad cumulative checkpoint: %+v", saved)
	}
}

func TestNewRunRepairsRejectedCacheWithoutRepeatingSelfTest(t *testing.T) {
	store := &fakeStore{}
	h, _ := problem.HashFiles(candidate("bad").Files)
	store.candidates = []questionbank.Candidate{{ID: 1, FilesHash: h, Status: "rejected", CandidateInput: questionbank.CandidateInput{Files: candidate("bad").Files}, Validations: []questionbank.Validation{{Kind: "selftest", Result: "failed", Feedback: `[{"kind":"selftest","message":"wrong","expected_stdout":"ok","actual_stdout":"wrong"}]`}}}}
	p := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, req answer.Request, _ int) (answer.Result, error) {
		if req.Previous == nil || len(req.Feedback) == 0 || req.Feedback[0].ActualStdout != "wrong" {
			t.Fatal("rejected cache feedback was discarded")
		}
		return solved("good", 3), nil
	}}
	a := &fakeAdapter{snap: testSnapshot()}
	r, err := New(store, p, Config{}).Run(context.Background(), a, recoveryRequest())
	if err != nil || r.Status != "selftest_passed" || a.selfTests != 1 {
		t.Fatalf("result=%+v err=%v selftests=%d", r, err, a.selfTests)
	}
}
