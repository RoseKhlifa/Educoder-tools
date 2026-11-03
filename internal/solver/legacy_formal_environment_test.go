package solver

import (
	"context"
	"strings"
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/questionbank"
)

func legacyFormalEnvironmentCandidate(t *testing.T) questionbank.Candidate {
	c := legacyEnvironmentCandidate(t, "good")
	c.Validations[0].Kind = "formal"
	c.Validations[0].SubmittedFilesHash = c.FilesHash
	c.Validations[0].Feedback = formalRuntimeFailureFeedback
	return c
}

func TestHistoricalFormalEnvironmentFailureUsesFreshSelftestAndFormalWithoutModel(t *testing.T) {
	c := legacyFormalEnvironmentCandidate(t)
	store := &fakeStore{candidates: []questionbank.Candidate{c}}
	adapter := &formalRuntimeRecoveryAdapter{fakeAdapter: fakeAdapter{snap: formalRuntimeRecoverySnapshot()}, evaluations: []Evaluation{{Completed: true, Passed: true, Status: "2", TestCount: 1, PassedCount: 1}}}
	result, err := New(store, nil, Config{}).Run(context.Background(), adapter, formalRuntimeRecoveryRequest())
	if err != nil || result.Status != "passed" || result.ModelCalls != 0 || adapter.selfTests != 1 || adapter.submits != 1 {
		t.Fatalf("historical environment failure triggered model repair: %+v err=%v selftests=%d submits=%d", result, err, adapter.selfTests, adapter.submits)
	}
	if store.candidates[0].Validations[0] != c.Validations[0] || store.candidates[0].Status != "upstream_verified" {
		t.Fatal("original failure lost or candidate not proven by new validation")
	}
}

func TestHistoricalFormalEnvironmentCompatibilityRejectsMixedUnboundAndUnknownEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*questionbank.Candidate)
	}{
		{"unknown", func(c *questionbank.Candidate) { c.Validations[0].Result = "unknown" }},
		{"missing submitted source", func(c *questionbank.Candidate) { c.Validations[0].SubmittedFilesHash = "" }},
		{"mismatched submitted source", func(c *questionbank.Candidate) { c.Validations[0].SubmittedFilesHash = "other" }},
		{"mismatched revision", func(c *questionbank.Candidate) { c.Validations[0].RevisionHash = "other" }},
		{"not terminal", func(c *questionbank.Candidate) { c.Validations[0].Terminal = false }},
		{"incomplete", func(c *questionbank.Candidate) { c.Validations[0].Complete = false }},
		{"no tests", func(c *questionbank.Candidate) { c.Validations[0].TestCount = 0 }},
		{"workspace", func(c *questionbank.Candidate) { c.Validations[0].Kind = "formal_workspace" }},
		{"actual compiler failure", func(c *questionbank.Candidate) {
			c.Validations[0].Feedback = runtimeStartupFailureMessage + "\nSyntaxError"
		}},
		{"mixed code failure", func(c *questionbank.Candidate) {
			v := c.Validations[0]
			v.Feedback = "wrong answer"
			c.Validations = append(c.Validations, v)
		}},
		{"mixed selftest failure", func(c *questionbank.Candidate) {
			v := c.Validations[0]
			v.Kind = "selftest"
			v.Feedback = feedbackString([]answer.Feedback{{Kind: "selftest", ActualStdout: "wrong", ExpectedStdout: "ok"}})
			c.Validations = append(c.Validations, v)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := legacyFormalEnvironmentCandidate(t)
			tc.change(&c)
			if legacyFormalRuntimeOnlyRejection(c) {
				t.Fatal("invalid historical proof was retested")
			}
		})
	}
	feedback := codeRepairFeedback([]answer.Feedback{{Kind: "formal", Message: formalRuntimeFailureFeedback}, {Kind: "formal", Message: "SyntaxError"}})
	if len(feedback) != 1 || !strings.Contains(feedback[0].Message, "SyntaxError") {
		t.Fatal("genuine code feedback lost")
	}
}

func TestHistoricalFormalEnvironmentFreshFailureStillBlocksPromotion(t *testing.T) {
	c := legacyFormalEnvironmentCandidate(t)
	store := &fakeStore{candidates: []questionbank.Candidate{c}}
	adapter := &formalRuntimeRecoveryAdapter{fakeAdapter: fakeAdapter{snap: formalRuntimeRecoverySnapshot()}, evaluations: []Evaluation{formalRuntimeFailureEvaluation()}}
	result, err := New(store, nil, Config{}).Run(context.Background(), adapter, formalRuntimeRecoveryRequest())
	if err == nil || result.Status != "uncertain" || result.ModelCalls != 0 || adapter.submits != 2 || store.candidates[0].Status == "upstream_verified" {
		t.Fatalf("fresh environment failure promoted old code: %+v err=%v", result, err)
	}
}

func TestHistoricalFormalEnvironmentRetestExcludesTerminalAndMissingPublicSamples(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		c := legacyFormalEnvironmentCandidate(t)
		store := &fakeStore{candidates: []questionbank.Candidate{c}}
		snapshot := formalRuntimeRecoverySnapshot()
		if terminal {
			snapshot.EditMode = "terminal"
		} else {
			snapshot.PublicTests = nil
		}
		adapter := &fakeAdapter{snap: snapshot}
		result, _ := New(store, nil, Config{}).Run(context.Background(), adapter, formalRuntimeRecoveryRequest())
		if result != nil && result.Status == "passed" || adapter.submits != 0 || adapter.selfTests != 0 || store.candidates[0].Status == "upstream_verified" {
			t.Fatal("historical environment bypassed full editor validation")
		}
	}
}
