package solver

import (
	"context"
	"strings"
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

func legacyRejectedCandidate(t *testing.T, actual, expected string) questionbank.Candidate {
	t.Helper()
	files := candidate("good").Files
	hash, err := problem.HashFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	ref := questionbank.Ref{RevisionHash: "rev", ContractHash: "contract"}
	return questionbank.Candidate{
		ID: 9, Ref: ref, CandidateInput: questionbank.CandidateInput{Files: files, Source: "model"}, FilesHash: hash, Status: "rejected",
		Validations: []questionbank.Validation{{
			CandidateID: 9, Kind: "selftest", Result: "failed", FilesHash: hash, SavedFilesHash: hash,
			RevisionHash: ref.RevisionHash, ContractHash: ref.ContractHash, Terminal: true, Complete: true, TestCount: 1,
			Feedback: feedbackString([]answer.Feedback{{Kind: "selftest", Message: actual, Stdin: "1", ExpectedStdout: expected, ActualStdout: actual}}),
		}},
	}
}

type outputRetestAdapter struct {
	fakeAdapter
	store                     *fakeStore
	tested                    []string
	wrongOutput               bool
	formalFailure             bool
	verifiedBeforeFormalCheck bool
}

func (a *outputRetestAdapter) SelfTest(_ context.Context, _ Target, file problem.SolutionFile, input, expected string) (SelfTest, error) {
	a.selfTests++
	a.tested = append(a.tested, input)
	output := strings.TrimSuffix(expected, "\n") + "\r\n"
	if a.wrongOutput {
		output = "wrong\n"
	}
	hash, _ := problem.HashFiles([]problem.SolutionFile{file})
	return SelfTest{Completed: true, Output: output, Matches: educoder.PublicOutputMatches(output, expected), SavedFilesHash: hash}, nil
}

func (a *outputRetestAdapter) Submit(_ context.Context, _ Target, file problem.SolutionFile) (Evaluation, error) {
	a.submits++
	for _, stored := range a.store.candidates {
		if stored.Status == "upstream_verified" {
			a.verifiedBeforeFormalCheck = true
		}
	}
	hash, _ := problem.HashFiles([]problem.SolutionFile{file})
	passed := !a.formalFailure
	return Evaluation{Completed: true, Passed: passed, TestCount: 1, PassedCount: boolInt(passed), SavedFilesHash: hash, SubmittedFilesHash: hash, Feedback: "formal result"}, nil
}

func TestLegacyNewlineRejectionRerunsAllCurrentSamplesAndFormalBeforePromotion(t *testing.T) {
	store := &fakeStore{candidates: []questionbank.Candidate{legacyRejectedCandidate(t, "ok\r\n", "ok")}}
	snapshot := testSnapshot()
	snapshot.PublicTests = append(snapshot.PublicTests, problem.PublicTest{Stdin: "2", ExpectedStdout: "second\n"})
	adapter := &outputRetestAdapter{fakeAdapter: fakeAdapter{snap: snapshot}, store: store}
	req := recoveryRequest()
	req.Mode = ModeSubmit
	r, err := New(store, nil, Config{}).Run(context.Background(), adapter, req)
	if err != nil || r.Status != "passed" || r.ModelCalls != 0 || adapter.selfTests != 2 || adapter.submits != 1 || strings.Join(adapter.tested, ",") != "1,2" {
		t.Fatalf("result=%+v err=%v tested=%v submits=%d", r, err, adapter.tested, adapter.submits)
	}
	if adapter.verifiedBeforeFormalCheck || store.candidates[0].Status != "upstream_verified" || len(store.validations) != 3 {
		t.Fatalf("legacy candidate promoted without fresh formal evidence: candidate=%+v validations=%+v", store.candidates[0], store.validations)
	}
}

func TestLegacyNewlineRejectionNeverPromotesWhenFreshFormalFails(t *testing.T) {
	store := &fakeStore{candidates: []questionbank.Candidate{legacyRejectedCandidate(t, "ok\n", "ok")}}
	adapter := &outputRetestAdapter{fakeAdapter: fakeAdapter{snap: testSnapshot()}, store: store, formalFailure: true}
	req := recoveryRequest()
	req.Mode, req.MaxEvaluations = ModeSubmit, 1
	r, err := New(store, nil, Config{}).Run(context.Background(), adapter, req)
	if err != nil || r.Status != "attempts_exhausted" || adapter.selfTests != 1 || adapter.submits != 1 || store.candidates[0].Status == "upstream_verified" {
		t.Fatalf("result=%+v err=%v candidate=%+v", r, err, store.candidates[0])
	}
}

func TestLegacyOutputCompatibilityLeavesRealAndUncertainFailuresRejected(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*questionbank.Candidate)
	}{
		{name: "empty output for long input", change: func(c *questionbank.Candidate) {
			c.Validations[0].Feedback = feedbackString([]answer.Feedback{{Kind: "selftest", Stdin: strings.Repeat("input", 1000), ExpectedStdout: "42\n", ActualStdout: ""}})
		}},
		{name: "spaces differ", change: func(c *questionbank.Candidate) {
			c.Validations[0].Feedback = feedbackString([]answer.Feedback{{Kind: "selftest", ExpectedStdout: "ok", ActualStdout: "ok \n"}})
		}},
		{name: "formal failure", change: func(c *questionbank.Candidate) { c.Validations[0].Kind = "formal" }},
		{name: "incomplete selftest", change: func(c *questionbank.Candidate) { c.Validations[0].Complete = false }},
		{name: "saved source differs", change: func(c *questionbank.Candidate) { c.Validations[0].SavedFilesHash = "other" }},
		{name: "uncertain evaluation", change: func(c *questionbank.Candidate) {
			c.Validations = append(c.Validations, questionbank.Validation{Kind: "formal", Result: "unknown"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cached := legacyRejectedCandidate(t, "ok\n", "ok")
			tc.change(&cached)
			store := &fakeStore{candidates: []questionbank.Candidate{cached}}
			adapter := &outputRetestAdapter{fakeAdapter: fakeAdapter{snap: testSnapshot()}, store: store}
			req := recoveryRequest()
			req.Mode = ModeSubmit
			r, err := New(store, nil, Config{}).Run(context.Background(), adapter, req)
			if err == nil || r.Status != "needs_provider" || adapter.selfTests != 0 || adapter.submits != 0 || store.candidates[0].Status != "rejected" {
				t.Fatalf("result=%+v err=%v selftests=%d submits=%d candidate=%+v", r, err, adapter.selfTests, adapter.submits, store.candidates[0])
			}
		})
	}
}

func TestLegacyRetestDoesNotBypassRepeatedSourceGuardAfterNewFailure(t *testing.T) {
	store := &fakeStore{candidates: []questionbank.Candidate{legacyRejectedCandidate(t, "ok\n", "ok")}}
	adapter := &outputRetestAdapter{fakeAdapter: fakeAdapter{snap: testSnapshot()}, store: store, wrongOutput: true}
	provider := &scriptedProvider{estimateCost: 1, generate: func(context.Context, answer.Request, int) (answer.Result, error) {
		return solved("good", 1), nil
	}}
	req := recoveryRequest()
	req.Mode, req.MaxModelCalls, req.MaxEvaluations = ModeSubmit, 1, 2
	r, err := New(store, provider, Config{}).Run(context.Background(), adapter, req)
	if err != nil || r.Status != "attempts_exhausted" || r.ModelCalls != 1 || adapter.selfTests != 1 || adapter.submits != 0 {
		t.Fatalf("result=%+v err=%v selftests=%d submits=%d", r, err, adapter.selfTests, adapter.submits)
	}
}
