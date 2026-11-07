package taskservice

import (
	"context"
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/solver"
)

func TestCostFunnelNeverAutomaticallyGrantsContinuationBudget(t *testing.T) {
	f := &recoveryFixture{t: t, initial: &solver.Result{RunID: "same-run", Status: "attempts_exhausted", ModelCalls: 3, CostMicroUSD: 3}}
	r, err := runProgrammingRecovery(context.Background(), f, nil, solver.Request{Owner: "owner", Game: "game", Mode: solver.ModeSubmit, MaxModelCalls: 3, MaxEvaluations: 3, BudgetMicroUSD: 100})
	if err != nil || r.ModelCalls != 3 || f.continues != 0 {
		t.Fatalf("automatic continuation expanded budget: result=%+v continues=%d err=%v", r, f.continues, err)
	}
	t.Logf("calls=%d continues=%d", r.ModelCalls, f.continues)
}

func TestCostFunnelObjectiveCallsAndSubmissionsAreIndependent(t *testing.T) {
	client, submits := objectiveAttemptServer(t, 1, false)
	p := &objectiveSequenceProvider{answers: []string{"", "A"}, errors: map[int]error{0: &answer.Error{Kind: "truncated_output"}}}
	r, err := New(bank(t), p, Config{MaxModelCalls: 99, MaxEvaluations: 99, ObjectiveMaxSubmissions: 1}).Run(context.Background(), client, req(solver.ModeSubmit), nil)
	if err != nil || r.Status != "passed" || r.ModelCalls != 2 || submits.Load() != 1 {
		t.Fatalf("generation consumed submission allowance: result=%+v submits=%d err=%v", r, submits.Load(), err)
	}
	for _, request := range p.requests {
		if request.Profile.Model != answer.DefaultProfile().Model {
			t.Fatalf("implicit pro dispatch: %+v", request.Profile)
		}
	}
	s := New(nil, nil, Config{MaxModelCalls: 99, MaxEvaluations: 99, ObjectiveMaxSubmissions: 99})
	if s.cfg.MaxModelCalls != 3 || s.cfg.MaxEvaluations != 3 || s.cfg.ObjectiveMaxSubmissions != 15 {
		t.Fatalf("hard caps=%+v", s.cfg)
	}
}
