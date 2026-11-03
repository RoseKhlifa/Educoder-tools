package taskservice

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/solver"
)

func availabilityFailure(status int) *answer.Error {
	return &answer.Error{Kind: "upstream_error", HTTPStatus: status, Retryable: true, BilledUnknown: true}
}

func TestObjectiveAvailabilityBackoffPersistsUsageBeforeWaiting(t *testing.T) {
	store := bank(t)
	client, submits := objectiveServer(t, `"13.30"`, false)
	request := req(solver.ModeSubmit)
	request.IdempotencyKey = "objective-backoff"
	calls, waits := 0, 0
	p := &fakeProvider{generate: func(context.Context) error {
		calls++
		if calls <= 3 {
			return availabilityFailure(503)
		}
		return nil
	}}
	wait := func(_ context.Context, delay time.Duration) error {
		waits++
		base := time.Second << (waits - 1)
		if delay < base/2 || delay > base {
			t.Fatalf("objective delay %d=%s", waits, delay)
		}
		record, err := store.FindRunByIdempotency(context.Background(), ownerHash(request.Owner), request.IdempotencyKey)
		var state objectiveState
		if err != nil || json.Unmarshal(record.Data, &state) != nil || state.ProviderRetry.Streak != waits || state.Result.ModelCalls != waits || state.Result.CostMicroUSD != int64(waits*10) {
			t.Fatalf("wait before objective checkpoint/accounting: state=%+v err=%v", state, err)
		}
		usage, err := store.UsageForRun(context.Background(), record.ID)
		if err != nil || len(usage) != waits || usage[waits-1].Status != "unknown" {
			t.Fatalf("wait before durable unknown usage: %+v err=%v", usage, err)
		}
		return nil
	}
	r, err := New(store, p, Config{ProviderRetryWait: wait}).Run(context.Background(), client, request, nil)
	if err != nil || r.Status != "passed" || r.ModelCalls != 4 || r.CostMicroUSD != 32 || waits != 3 || submits.Load() != 1 {
		t.Fatalf("result=%+v err=%v waits=%d submits=%d", r, err, waits, submits.Load())
	}
	record, err := store.GetRun(context.Background(), r.RunID)
	var state objectiveState
	if err != nil || json.Unmarshal(record.Data, &state) != nil || state.ProviderRetry.Streak != 0 {
		t.Fatalf("success retained outage streak: %+v err=%v", state, err)
	}
	public, _ := json.Marshal(r)
	if strings.Contains(string(public), "provider_retry") || strings.Contains(string(public), "streak") {
		t.Fatal("operational retry metadata escaped the private checkpoint")
	}
}

func TestObjectiveAvailabilityWaitCancellationDoesNotSubmitOrDispatchAgain(t *testing.T) {
	store := bank(t)
	client, submits := objectiveServer(t, `"13.30"`, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &fakeProvider{generate: func(context.Context) error { return availabilityFailure(429) }}
	r, err := New(store, p, Config{ProviderRetryWait: func(ctx context.Context, _ time.Duration) error { cancel(); return ctx.Err() }}).Run(ctx, client, req(solver.ModeSubmit), nil)
	if !errors.Is(err, context.Canceled) || r.Status != "canceled" || r.ModelCalls != 1 || r.CostMicroUSD != 10 || p.objectiveCalls.Load() != 1 || submits.Load() != 0 {
		t.Fatalf("cancellation dispatched or submitted again: result=%+v err=%v", r, err)
	}
	record, err := store.GetRun(context.Background(), r.RunID)
	var state objectiveState
	usage, usageErr := store.UsageForRun(context.Background(), r.RunID)
	if err != nil || json.Unmarshal(record.Data, &state) != nil || state.ProviderRetry.Streak != 1 || state.Result.CostMicroUSD != 10 || usageErr != nil || len(usage) != 1 || usage[0].Status != "unknown" || usage[0].ReservedMicroUSD != 10 {
		t.Fatalf("cancellation lost usage/checkpoint: state=%+v usage=%+v err=%v/%v", state, usage, err, usageErr)
	}
}

func TestObjectiveAvailabilityBackoffResetsForRealOutputAndHonorsBudget(t *testing.T) {
	t.Run("output reset", func(t *testing.T) {
		client, _ := objectiveServer(t, `"13.30"`, false)
		p := &objectiveSequenceProvider{answers: []string{"", "", "", "A"}, errors: map[int]error{0: availabilityFailure(503), 1: &answer.Error{Kind: "truncated_output"}, 2: availabilityFailure(503)}}
		waits := 0
		r, err := New(bank(t), p, Config{ProviderRetryWait: func(_ context.Context, delay time.Duration) error {
			waits++
			if delay < 500*time.Millisecond || delay > time.Second {
				t.Fatalf("output failure did not reset objective streak: %s", delay)
			}
			return nil
		}}).Run(context.Background(), client, req(solver.ModeSubmit), nil)
		if err != nil || r.Status != "passed" || r.ModelCalls != 4 || waits != 2 {
			t.Fatalf("result=%+v err=%v waits=%d", r, err, waits)
		}
	})
	t.Run("cost budget", func(t *testing.T) {
		client, submits := objectiveServer(t, `"13.30"`, false)
		p := &fakeProvider{generate: func(context.Context) error { return availabilityFailure(503) }}
		r, err := New(bank(t), p, Config{BudgetMicroUSD: 15, ProviderRetryWait: func(context.Context, time.Duration) error {
			t.Fatal("objective waited without enough budget")
			return nil
		}}).Run(context.Background(), client, req(solver.ModeSubmit), nil)
		if err != nil || r.Status != "budget_exhausted" || r.ModelCalls != 1 || r.CostMicroUSD != 10 || submits.Load() != 0 {
			t.Fatalf("result=%+v err=%v", r, err)
		}
	})
}

type availabilityExerciseModel struct {
	exerciseModel
	attempts int
	failures int
}

func (p *availabilityExerciseModel) GenerateExercise(ctx context.Context, request answer.ExerciseRequest) (answer.ExerciseResult, error) {
	p.attempts++
	if p.attempts <= p.failures {
		return answer.ExerciseResult{}, availabilityFailure(503)
	}
	return p.exerciseModel.GenerateExercise(ctx, request)
}

func TestExerciseAvailabilityBackoffPreservesUnknownReservationsAndWriteBoundary(t *testing.T) {
	store := bank(t)
	p := &availabilityExerciseModel{failures: 2}
	x := &exerciseFake{paper: syntheticExercisePaper()}
	waits := 0
	wait := func(_ context.Context, delay time.Duration) error {
		waits++
		base := time.Second << (waits - 1)
		record, err := store.FindRunByIdempotency(context.Background(), ownerHash(exerciseReq().Owner), exerciseReq().IdempotencyKey)
		var state exerciseState
		if err != nil || json.Unmarshal(record.Data, &state) != nil || state.ProviderRetry.Streak != waits || state.Result.ModelCalls != waits || state.Result.ReservedMicroUSD != int64(waits*10) || state.MutationStarted || x.saves != 0 || delay < base/2 || delay > base {
			t.Fatalf("exercise wait crossed mutation/accounting boundary: state=%+v err=%v delay=%s", state, err, delay)
		}
		return nil
	}
	r, err := New(store, p, Config{ProviderRetryWait: wait}).runExercise(context.Background(), x, exerciseReq(), nil)
	if err != nil || !r.Submitted || r.ModelCalls != 3 || r.CostMicroUSD != 2 || r.ReservedMicroUSD != 20 || waits != 2 || p.attempts != 3 || x.saves != 1 || x.commits != 1 {
		t.Fatalf("result=%+v err=%v waits=%d saves=%d commits=%d", r, err, waits, x.saves, x.commits)
	}
	usage, err := store.UsageForRun(context.Background(), r.RunID)
	if err != nil || len(usage) != 3 || usage[0].Status != "unknown" || usage[1].Status != "unknown" || usage[2].Status != "settled" {
		t.Fatalf("usage=%+v err=%v", usage, err)
	}
}

func TestExerciseAvailabilityWaitCancellationNeverStartsWrites(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := bank(t)
	p := &availabilityExerciseModel{failures: 2}
	x := &exerciseFake{paper: syntheticExercisePaper()}
	r, err := New(store, p, Config{ProviderRetryWait: func(ctx context.Context, _ time.Duration) error { cancel(); return ctx.Err() }}).runExercise(ctx, x, exerciseReq(), nil)
	if !errors.Is(err, context.Canceled) || r.Status != "canceled" || r.ModelCalls != 1 || r.ReservedMicroUSD != 10 || p.attempts != 1 || x.saves != 0 || x.commits != 0 {
		t.Fatalf("result=%+v err=%v saves=%d commits=%d", r, err, x.saves, x.commits)
	}
	record, err := store.GetRun(context.Background(), r.RunID)
	var state exerciseState
	if err != nil || json.Unmarshal(record.Data, &state) != nil || state.ProviderRetry.Streak != 1 || state.MutationStarted {
		t.Fatalf("cancellation lost safe checkpoint: %+v err=%v", state, err)
	}
}
