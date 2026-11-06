package taskservice

import (
	"context"
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/solver"
)

type objectiveFormatRepairProvider struct {
	fakeProvider
	requests []answer.ObjectiveRequest
}

func (p *objectiveFormatRepairProvider) GenerateObjective(_ context.Context, request answer.ObjectiveRequest) (answer.ObjectiveResult, error) {
	p.requests = append(p.requests, request)
	result := answer.ObjectiveResult{Model: request.Profile.Model, CostKnown: true, CostMicroUSD: 2}
	if len(request.Problem.Questions) > 1 {
		result.Candidate = answer.ObjectiveCandidate{Kind: "objective", Answers: []string{"A", "A"}}
		return result, nil
	}
	if request.Problem.Questions[0].Position == 1 {
		if len(request.Feedback) == 0 {
			// A provider confusing the complete context roster with the selected
			// output repeats the same valid-JSON, wrong-count response until it
			// receives specific structural correction. The rejected values never
			// enter a retry's prompt.
			invalid := answer.ObjectiveCandidate{Kind: "objective", Answers: []string{"B", "A"}}
			return result, answer.ValidateObjectiveCandidate(request.Problem, invalid)
		}
		result.Candidate = answer.ObjectiveCandidate{Kind: "objective", Answers: []string{"B"}}
	} else {
		result.Candidate = answer.ObjectiveCandidate{Kind: "objective", Answers: []string{"A"}}
	}
	return result, nil
}

func TestObjectiveIndependentFormatRepairConverges(t *testing.T) {
	client, submits := independentConvergenceServer(t)
	provider := &objectiveFormatRepairProvider{}
	store := bank(t)
	result, err := New(store, provider, Config{MaxModelCalls: 4, MaxEvaluations: 4}).Run(context.Background(), client, req(solver.ModeSubmit), nil)
	if err != nil || result.Status != "passed" || result.ModelCalls != 4 || result.CostMicroUSD != 8 || submits.Load() != 2 {
		t.Fatalf("format repair: status=%s calls=%d submits=%d cost=%d err=%v", result.Status, result.ModelCalls, submits.Load(), result.CostMicroUSD, err)
	}
	if len(provider.requests[1].Feedback) != 0 || len(provider.requests[2].Feedback) != 1 || provider.requests[2].Feedback[0].Kind != "objective_output_contract" || len(provider.requests[3].Feedback) != 0 {
		t.Fatal("structural correction missing or leaked into another question")
	}
	for _, request := range provider.requests[1:] {
		if request.Previous != nil || request.FixedAnswers != nil {
			t.Fatal("format correction reintroduced scored-history anchoring")
		}
	}
	usage, err := store.UsageForRun(context.Background(), result.RunID)
	if err != nil || len(usage) != 4 {
		t.Fatalf("usage not preserved: entries=%d err=%v", len(usage), err)
	}
	t.Log("status=passed calls=4 submits=2 usage=4 cost=8 score=2/2")
}
