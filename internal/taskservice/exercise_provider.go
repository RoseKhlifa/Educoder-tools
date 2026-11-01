package taskservice

import (
	"context"
	"errors"

	"educoder-cli/internal/answer"
)

func (p *limitedProvider) EstimateExercise(req answer.ExerciseRequest) (answer.Estimate, error) {
	provider, ok := p.provider.(answer.ExerciseProvider)
	if !ok {
		return answer.Estimate{}, errors.New("exercise model provider unavailable")
	}
	return provider.EstimateExercise(req)
}

func (p *limitedProvider) GenerateExercise(ctx context.Context, req answer.ExerciseRequest) (answer.ExerciseResult, error) {
	provider, ok := p.provider.(answer.ExerciseProvider)
	if !ok {
		return answer.ExerciseResult{}, errors.New("exercise model provider unavailable")
	}
	release, err := p.acquire(ctx)
	if err != nil {
		return answer.ExerciseResult{}, err
	}
	defer release()
	return provider.GenerateExercise(ctx, req)
}
