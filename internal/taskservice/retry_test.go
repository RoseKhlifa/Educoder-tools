package taskservice

import (
	"context"
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/solver"
)

type objectiveRetryProvider struct {
	fakeProvider
	requests []answer.ObjectiveRequest
}

func (p *objectiveRetryProvider) GenerateObjective(_ context.Context, req answer.ObjectiveRequest) (answer.ObjectiveResult, error) {
	p.requests = append(p.requests, req)
	result := answer.ObjectiveResult{Model: req.Profile.Model, CostKnown: true, CostMicroUSD: 2}
	switch len(p.requests) {
	case 1:
		return result, &answer.Error{Kind: "truncated_output"}
	case 2:
		return result, &answer.Error{Kind: "invalid_output_json"}
	default:
		result.Candidate = answer.ObjectiveCandidate{Kind: "objective", Answers: []string{"A"}}
		return result, nil
	}
}

func TestObjectiveTruncationRetryChangesOutputProfileAndRecordsEveryCharge(t *testing.T) {
	store := bank(t)
	client, submits := objectiveServer(t, `"13.30"`, false)
	provider := &objectiveRetryProvider{}
	service := New(store, provider, Config{})
	if service.cfg.MaxModelCalls != 10 || service.cfg.MaxEvaluations != 10 {
		t.Fatalf("web task retry defaults=%+v", service.cfg)
	}
	r, err := service.Run(context.Background(), client, req(solver.ModeSubmit), nil)
	if err != nil || r.Status != "passed" || r.ModelCalls != 3 || r.CostMicroUSD != 6 || submits.Load() != 1 {
		t.Fatalf("result=%+v err=%v submits=%d", r, err, submits.Load())
	}
	for _, request := range provider.requests[1:] {
		if request.Profile.MaxTokens != 8192 || request.Profile.Thinking != "disabled" || request.Profile.ReasoningEffort != "" || len(request.Feedback) == 0 {
			t.Fatalf("retry profile/feedback=%+v", request)
		}
	}
	usage, err := store.UsageForRun(context.Background(), r.RunID)
	if err != nil || len(usage) != 3 {
		t.Fatalf("usage=%+v err=%v", usage, err)
	}
	for _, entry := range usage {
		if entry.Status != "settled" || entry.CostMicroUSD != 2 {
			t.Fatalf("charge lost during retry: %+v", entry)
		}
	}
}
