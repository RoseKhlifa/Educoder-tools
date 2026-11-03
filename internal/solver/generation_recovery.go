package solver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

const maxUnproductiveGenerations = 3

// These controls belong to the durable run, just like charged usage and failed
// hashes. Continuation grants more capacity without resetting an ineffective
// profile or endlessly repeating an identical failed answer.
type generationRecovery struct {
	CompactOutput               bool `json:"compact_output,omitempty"`
	RepeatedCandidates          int  `json:"repeated_candidates,omitempty"`
	OutputFailures              int  `json:"output_failures,omitempty"`
	ProBodyDeadlineFallbackUsed bool `json:"pro_body_deadline_fallback_used,omitempty"`
}

func (g *generationRecovery) restore(feedback []answer.Feedback) {
	for _, entry := range feedback {
		if entry.Kind == "model" && entry.Message == (&answer.Error{Kind: "truncated_output"}).Error() {
			g.CompactOutput = true
		}
	}
}

func (g generationRecovery) profile(cfg Config, calls int) answer.Profile {
	p := cfg.Profile
	if calls > 0 {
		p = cfg.RepairProfile
	}
	if calls >= 2 || g.RepeatedCandidates > 0 {
		p = cfg.ProProfile
	}
	if g.CompactOutput {
		// Optional reasoning shares the model's output window with source code.
		// Once a response is cut off, reserve that window for complete JSON.
		p.Thinking, p.ReasoningEffort, p.MaxTokens = "disabled", "", 8192
	}
	if g.ProBodyDeadlineFallbackUsed {
		// A complete PRO response did not arrive within its body deadline.
		// Keep this run on the distinct baseline model after the one-way switch;
		// call count or repeated code must not select that same stalled PRO again.
		p = cfg.Profile
		p.Thinking, p.ReasoningEffort, p.MaxTokens = "disabled", "", 8192
	}
	return p
}

func (g *generationRecovery) fallbackAfterProBodyDeadline(cfg Config, profile answer.Profile, err error) bool {
	if g.ProBodyDeadlineFallbackUsed || profile.Model != cfg.ProProfile.Model || strings.TrimSpace(cfg.Profile.Model) == "" || cfg.Profile.Model == cfg.ProProfile.Model {
		return false
	}
	var providerErr *answer.Error
	if !errors.As(err, &providerErr) || providerErr.Kind != "response_read_failed" || providerErr.HTTPStatus != 200 || providerErr.ResponsePhase != "body" || providerErr.ReadCause != "deadline" || !providerErr.BilledUnknown {
		return false
	}
	// This changes model selection only. The solver retains the unknown usage
	// reservation and enforces its original call, evaluation, and cost limits
	// before another dispatch. Other transport errors stay non-retryable.
	g.ProBodyDeadlineFallbackUsed = true
	return true
}

func (g *generationRecovery) failedOutput(err error) bool {
	var providerErr *answer.Error
	if !errors.As(err, &providerErr) {
		return false
	}
	switch providerErr.Kind {
	case "truncated_output", "invalid_output_json", "candidate_contract":
		g.CompactOutput = true
		g.OutputFailures++
		return g.OutputFailures >= maxUnproductiveGenerations
	default:
		// Rate limits, authentication and transport errors do not describe the
		// quality of generated source and retain their existing retry rules.
		return false
	}
}

const runtimeStartupFailureMessage = "启动环境失败，请稍后重试，如问题持续存在，请联系平台人员解决。"

func runtimeStartupFailure(output, expected string) bool {
	// Match only the observed complete runner response, never an arbitrary
	// program output containing the same words or a sample expecting this text.
	return strings.TrimSpace(output) == runtimeStartupFailureMessage && strings.TrimSpace(expected) != runtimeStartupFailureMessage
}

func legacyRuntimeFeedback(f answer.Feedback) bool {
	return f.Kind == "selftest" && strings.TrimSpace(f.Message) == runtimeStartupFailureMessage && runtimeStartupFailure(f.ActualStdout, f.ExpectedStdout)
}

func codeRepairFeedback(feedback []answer.Feedback) []answer.Feedback {
	out := make([]answer.Feedback, 0, len(feedback))
	for _, entry := range feedback {
		if !legacyRuntimeFeedback(entry) && !(entry.Kind == "formal" && formalRuntimeStartupFeedback(entry.Message)) {
			out = append(out, entry)
		}
	}
	return out
}

// Historical formal verifications did not persist the raw upstream status.
// Their exact, source-bound environment diagnostic permits a fresh retest,
// never a trust promotion or replacement of the historical failure.
func legacyFormalRuntimeOnlyRejection(c questionbank.Candidate) bool {
	if c.Status != "rejected" || c.FilesHash == "" {
		return false
	}
	hash, err := problem.HashFiles(c.Files)
	if err != nil || hash != c.FilesHash {
		return false
	}
	found := false
	for _, v := range c.Validations {
		if v.Result == "unknown" {
			return false
		}
		if v.Result != "failed" {
			continue
		}
		if v.Kind == "selftest" {
			one := c
			one.Validations = []questionbank.Validation{v}
			if !legacyRuntimeOnlyRejection(one) {
				return false
			}
			continue
		}
		if v.Kind != "formal" || !v.Terminal || !v.Complete || v.TestCount < 1 || v.PassedCount != 0 || v.FilesHash != c.FilesHash || v.SavedFilesHash != c.FilesHash || v.SubmittedFilesHash != c.FilesHash || v.RevisionHash != c.RevisionHash || v.ContractHash != c.ContractHash || !formalRuntimeStartupFeedback(v.Feedback) {
			return false
		}
		found = true
	}
	return found
}

func legacyRuntimeOnlyRejection(c questionbank.Candidate) bool {
	if c.Status != "rejected" || c.FilesHash == "" {
		return false
	}
	hash, err := problem.HashFiles(c.Files)
	if err != nil || hash != c.FilesHash {
		return false
	}
	found := false
	for _, validation := range c.Validations {
		if validation.Result == "unknown" {
			return false
		}
		if validation.Result != "failed" {
			continue
		}
		if validation.Kind != "selftest" || !validation.Terminal || !validation.Complete || validation.TestCount != 1 || validation.PassedCount != 0 || validation.FilesHash != c.FilesHash || validation.SavedFilesHash != c.FilesHash || validation.RevisionHash != c.RevisionHash || validation.ContractHash != c.ContractHash {
			return false
		}
		var feedback []answer.Feedback
		if json.Unmarshal([]byte(validation.Feedback), &feedback) != nil || len(feedback) == 0 {
			return false
		}
		sawRuntime := false
		for _, entry := range feedback {
			if legacyRuntimeFeedback(entry) {
				sawRuntime = true
			} else if entry.Kind != "selftest_passed" || !educoder.PublicOutputMatches(entry.ActualStdout, entry.ExpectedStdout) {
				return false
			}
		}
		if !sawRuntime {
			return false
		}
		found = true
	}
	return found
}

func waitRuntimeRetry(ctx context.Context) error {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
