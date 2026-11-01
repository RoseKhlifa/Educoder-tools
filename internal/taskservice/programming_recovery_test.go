package taskservice

import (
	"context"
	"errors"
	"testing"

	"educoder-cli/internal/solver"
)

type recoveryFixture struct {
	t           *testing.T
	continues   int
	initial     *solver.Result
	initialErr  error
	finalStatus string
	continueErr error
}

func (f *recoveryFixture) Run(context.Context, solver.Adapter, solver.Request) (*solver.Result, error) {
	return f.initial, f.initialErr
}

func (f *recoveryFixture) ContinueRun(_ context.Context, _ solver.Adapter, req solver.Request, runID string, grant solver.Continuation) (*solver.Result, error) {
	f.continues++
	if f.continueErr != nil {
		return nil, f.continueErr
	}
	if runID != "same-run" || req.Owner != "owner" || req.Game != "game" || grant.AdditionalModelCalls != 2 || grant.AdditionalEvaluations != 2 || grant.AdditionalBudgetMicroUSD != 0 {
		f.t.Fatalf("continuation lost its target or reset money budget: req=%+v run=%s grant=%+v", req, runID, grant)
	}
	status := f.finalStatus
	if status == "" {
		status = "passed"
	}
	return &solver.Result{RunID: runID, Status: status, ModelCalls: 2 * (f.continues + 1), CostMicroUSD: int64(3 * (f.continues + 1))}, nil
}

func TestProgrammingRecoveryContinuesSameCheckpointWithoutResettingUsage(t *testing.T) {
	f := &recoveryFixture{t: t, initial: &solver.Result{RunID: "same-run", Status: "selftest_failed", ModelCalls: 2, CostMicroUSD: 3}}
	req := solver.Request{Owner: "owner", Course: "course", HomeworkID: 11, Game: "game", Mode: solver.ModeSubmit, MaxModelCalls: 2, MaxEvaluations: 2, BudgetMicroUSD: 20}
	var phases []string
	req.OnProgress = func(st solver.Stage) { phases = append(phases, st.Phase) }
	r, err := runProgrammingRecovery(context.Background(), f, nil, req, 4, func() (bool, error) { return true, nil })
	if err != nil || r.Status != "passed" || f.continues != 1 || r.ModelCalls != 4 || r.CostMicroUSD != 6 || len(phases) != 1 || phases[0] != "repairing" {
		t.Fatalf("confirmed code failure was not automatically continued: %+v continues=%d phases=%v err=%v", r, f.continues, phases, err)
	}
}

func TestProgrammingRecoveryStopsAtPassAndPreservesAdmissionAndUnknownResults(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result *solver.Result
		mode   solver.Mode
		err    error
		admit  bool
		want   string
	}{
		{"already passed", &solver.Result{RunID: "same-run", Status: "passed"}, solver.ModeSubmit, nil, true, "passed"},
		{"unknown formal evaluation", &solver.Result{RunID: "same-run", Status: "uncertain", Evaluation: &solver.Evaluation{}}, solver.ModeSubmit, nil, true, "uncertain"},
		{"unknown selftest", &solver.Result{RunID: "same-run", Status: "uncertain", SelfTestStatus: "unknown"}, solver.ModeSubmit, nil, true, "uncertain"},
		{"real cost budget spent", &solver.Result{RunID: "same-run", Status: "selftest_failed", CostMicroUSD: 19, ReservedMicroUSD: 1}, solver.ModeSubmit, nil, true, "selftest_failed"},
		{"editor conflict", &solver.Result{RunID: "same-run", Status: "selftest_failed"}, solver.ModeSubmit, errors.New("editor changed"), true, "selftest_failed"},
		{"generate only", &solver.Result{RunID: "same-run", Status: "selftest_failed"}, solver.ModeGenerateOnly, nil, true, "selftest_failed"},
		{"closed before continuation", &solver.Result{RunID: "same-run", Status: "selftest_failed"}, solver.ModeSubmit, nil, false, "deadline_passed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &recoveryFixture{t: t, initial: tc.result, initialErr: tc.err}
			r, err := runProgrammingRecovery(context.Background(), f, nil, solver.Request{Mode: tc.mode, BudgetMicroUSD: 20}, 4, func() (bool, error) { return tc.admit, nil })
			if f.continues != 0 || r.Status != tc.want || !errors.Is(err, tc.err) {
				t.Fatalf("unexpected replay or status: %+v continues=%d err=%v", r, f.continues, err)
			}
		})
	}
}

func TestProgrammingRecoveryBoundedRoundsAndCancellation(t *testing.T) {
	req := solver.Request{Owner: "owner", Game: "game", Mode: solver.ModeSubmit, MaxModelCalls: 2, MaxEvaluations: 2, BudgetMicroUSD: 100}
	f := &recoveryFixture{t: t, initial: &solver.Result{RunID: "same-run", Status: "attempts_exhausted"}, finalStatus: "attempts_exhausted"}
	r, err := runProgrammingRecovery(context.Background(), f, nil, req, 3, func() (bool, error) { return true, nil })
	if err != nil || f.continues != 2 || r.ModelCalls != 6 || r.CostMicroUSD != 9 {
		t.Fatalf("repair round cap or lifetime usage wrong: %+v continues=%d err=%v", r, f.continues, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.continues = 0
	_, err = runProgrammingRecovery(ctx, f, nil, req, 3, func() (bool, error) { t.Fatal("canceled run checked admission"); return true, nil })
	if !errors.Is(err, context.Canceled) || f.continues != 0 {
		t.Fatalf("cancellation ignored: continues=%d err=%v", f.continues, err)
	}
}

func TestProgrammingRecoveryKeepsCheckpointWhenContinuationReadFails(t *testing.T) {
	readErr := errors.New("synthetic continuation task read failed")
	last := &solver.Result{RunID: "same-run", Status: "selftest_failed", ModelCalls: 10, CostMicroUSD: 17, ReservedMicroUSD: 5}
	f := &recoveryFixture{t: t, initial: last, continueErr: readErr}
	r, err := runProgrammingRecovery(context.Background(), f, nil, solver.Request{Mode: solver.ModeSubmit, BudgetMicroUSD: 100}, 4, func() (bool, error) { return true, nil })
	if !errors.Is(err, readErr) || r != last || r.RunID != "same-run" || r.ModelCalls != 10 || r.CostMicroUSD != 17 || r.ReservedMicroUSD != 5 || f.continues != 1 {
		t.Fatalf("continuation read error discarded charged work: result=%+v continues=%d err=%v", r, f.continues, err)
	}
}
