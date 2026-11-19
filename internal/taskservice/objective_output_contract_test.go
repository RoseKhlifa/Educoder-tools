package taskservice

import (
	"context"
	"testing"
	"time"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/solver"
)

func TestObjectiveOutputFailureSwitchesToAnswersOnlyContract(t *testing.T) {
	cases := []struct {
		name         string
		err          error
		wantThinking string
	}{
		// A length failure also disables reasoning for the rest of the run.
		{"truncated", &answer.Error{Kind: "truncated_output", Retryable: true}, "disabled"},
		// Malformed JSON keeps reasoning but drops the ranked structure.
		{"malformed", &answer.Error{Kind: "invalid_output_json"}, "enabled"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, submits := objectiveServer(t, `"13.30"`, false)
			provider := &objectiveSequenceProvider{answers: []string{"", "A"}, errors: map[int]error{0: tc.err}, ranked: true}
			r, err := New(bank(t), provider, Config{MaxModelCalls: 3, MaxEvaluations: 3}).Run(context.Background(), client, req(solver.ModeSubmit), nil)
			if err != nil || r.Status != "passed" || r.ModelCalls != 2 || submits.Load() != 1 || len(provider.requests) != 2 {
				t.Fatalf("result=%+v err=%v submits=%d requests=%d", r, err, submits.Load(), len(provider.requests))
			}
			first, retry := provider.requests[0], provider.requests[1]
			if !first.RequireRankedAnswers || first.AnswersOnly {
				t.Fatalf("first request contract=%v/%v", first.RequireRankedAnswers, first.AnswersOnly)
			}
			if retry.RequireRankedAnswers || !retry.AnswersOnly || retry.Profile.Thinking != tc.wantThinking || retry.Profile.MaxTokens != answer.OutputTokenLimit(retry.Profile.Thinking) {
				t.Fatalf("retry did not use the compact answers contract: ranked=%v answersOnly=%v profile=%+v", retry.RequireRankedAnswers, retry.AnswersOnly, retry.Profile)
			}
		})
	}
}

func TestObjectiveProviderOutageKeepsRankedContract(t *testing.T) {
	client, _ := objectiveServer(t, `"13.30"`, false)
	outage := &answer.Error{Kind: "upstream_error", HTTPStatus: 503, Retryable: true}
	provider := &objectiveSequenceProvider{answers: []string{"", "A"}, errors: map[int]error{0: outage}, ranked: true}
	r, err := New(bank(t), provider, Config{MaxModelCalls: 3, MaxEvaluations: 3, ProviderRetryWait: func(context.Context, time.Duration) error { return nil }}).Run(context.Background(), client, req(solver.ModeSubmit), nil)
	if err != nil || r.Status != "passed" || len(provider.requests) != 2 {
		t.Fatalf("result=%+v err=%v requests=%d", r, err, len(provider.requests))
	}
	if retry := provider.requests[1]; !retry.RequireRankedAnswers || retry.AnswersOnly {
		t.Fatalf("an availability failure changed the output contract: ranked=%v answersOnly=%v", retry.RequireRankedAnswers, retry.AnswersOnly)
	}
}
