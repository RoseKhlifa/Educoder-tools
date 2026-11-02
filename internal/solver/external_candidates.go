package solver

import (
	"context"

	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

// Import capabilities are optional so custom stores keep the same solver API.
// Provenance comes from database bindings, never a caller-controlled source label.
type externalCandidateStore interface {
	BindExternalCandidates(context.Context, questionbank.Ref, questionbank.ExternalIdentity, problem.Snapshot) ([]questionbank.Candidate, error)
	IsExternalCandidate(context.Context, int64) (bool, error)
}

func (s *Service) bindExternalCandidates(ctx context.Context, ref questionbank.Ref, loaded LoadedProblem) error {
	store, ok := s.store.(externalCandidateStore)
	if !ok || loaded.ExternalIdentity.Experiment == "" || loaded.ExternalIdentity.Title == "" {
		return nil
	}
	_, err := store.BindExternalCandidates(ctx, ref, loaded.ExternalIdentity, loaded.Snapshot)
	return err
}

func (s *Service) isExternalCandidate(ctx context.Context, id int64) (bool, error) {
	if store, ok := s.store.(externalCandidateStore); ok {
		return store.IsExternalCandidate(ctx, id)
	}
	return false, nil
}
