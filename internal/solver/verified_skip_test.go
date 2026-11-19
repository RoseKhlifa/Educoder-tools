package solver

import (
	"context"
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

func verifiedGoodStore() *fakeStore {
	files := []problem.SolutionFile{{Path: "main.cpp", Content: "good"}}
	hash, _ := problem.HashFiles(files)
	return &fakeStore{candidates: []questionbank.Candidate{verifiedCandidateForTest(1, files, hash)}}
}

func TestVerifiedCandidateSkipsPublicSelfTestInSubmitMode(t *testing.T) {
	adapter := &fakeAdapter{snap: testSnapshot()}
	req := recoveryRequest()
	req.Mode = ModeSubmit
	r, err := New(verifiedGoodStore(), nil, Config{}).Run(context.Background(), adapter, req)
	if err != nil || r.Status != "passed" || r.SelfTestStatus != "skipped" || adapter.selfTests != 0 || adapter.submits != 1 || r.ModelCalls != 0 {
		t.Fatalf("result=%+v err=%v selftests=%d submits=%d", r, err, adapter.selfTests, adapter.submits)
	}
	if len(r.Diagnostics) == 0 || r.Diagnostics[0] != "题库候选已在相同题目修订下正式评测满分通过，跳过公开自测，直接正式评测" {
		t.Fatalf("skip not explained: %v", r.Diagnostics)
	}
}

func TestVerifiedCandidateStillSelfTestsInSelfTestMode(t *testing.T) {
	adapter := &fakeAdapter{snap: testSnapshot()}
	r, err := New(verifiedGoodStore(), nil, Config{}).Run(context.Background(), adapter, recoveryRequest())
	if err != nil || r.Status != "selftest_passed" || adapter.selfTests != 1 || adapter.submits != 0 {
		t.Fatalf("explicit self-test skipped: result=%+v err=%v selftests=%d", r, err, adapter.selfTests)
	}
}

func TestUnverifiedProposalStillRunsPublicSelfTest(t *testing.T) {
	files := []problem.SolutionFile{{Path: "main.cpp", Content: "good"}}
	hash, _ := problem.HashFiles(files)
	store := &fakeStore{candidates: []questionbank.Candidate{{ID: 1, Ref: questionbank.Ref{RevisionHash: "rev", ContractHash: "contract"}, CandidateInput: questionbank.CandidateInput{Files: files, Source: "manual_public_statement"}, FilesHash: hash, Status: "candidate"}}}
	adapter := &fakeAdapter{snap: testSnapshot()}
	req := recoveryRequest()
	req.Mode = ModeSubmit
	r, err := New(store, nil, Config{}).Run(context.Background(), adapter, req)
	if err != nil || r.Status != "passed" || adapter.selfTests != 1 || adapter.submits != 1 || r.SelfTestStatus != "passed" {
		t.Fatalf("unverified proposal bypassed self-test: result=%+v err=%v selftests=%d", r, err, adapter.selfTests)
	}
}

// formalFailOnceAdapter passes every public sample but fails the first formal
// evaluation, modelling a verified answer that no longer satisfies hidden tests.
type formalFailOnceAdapter struct{ fakeAdapter }

func (a *formalFailOnceAdapter) SelfTest(_ context.Context, _ Target, _ problem.SolutionFile, _, expected string) (SelfTest, error) {
	a.selfTests++
	return SelfTest{Completed: true, Output: expected, Matches: true}, nil
}

func (a *formalFailOnceAdapter) Submit(ctx context.Context, target Target, file problem.SolutionFile) (Evaluation, error) {
	a.submits++
	if a.submits == 1 {
		return Evaluation{Completed: true, Passed: false, Status: "0", Feedback: "hidden test 2 failed", TestCount: 2, PassedCount: 1}, nil
	}
	return Evaluation{Completed: true, Passed: true, Status: "2", TestCount: 2, PassedCount: 2}, nil
}

func TestVerifiedCandidateFormalFailureReturnsToRepair(t *testing.T) {
	adapter := &formalFailOnceAdapter{fakeAdapter: fakeAdapter{snap: testSnapshot()}}
	store := verifiedGoodStore()
	p := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, req answer.Request, n int) (answer.Result, error) {
		found := false
		for _, f := range req.Feedback {
			found = found || (f.Kind == "formal" && f.Message == "hidden test 2 failed")
		}
		if !found || req.Previous == nil {
			t.Fatalf("repair lost the formal feedback: %+v", req.Feedback)
		}
		return solved("fixed", 2), nil
	}}
	req := recoveryRequest()
	req.Mode = ModeSubmit
	r, err := New(store, p, Config{}).Run(context.Background(), adapter, req)
	// The verified candidate goes straight to formal; the repaired candidate is
	// new source and therefore runs its public sample before formal again.
	if err != nil || r.Status != "passed" || r.ModelCalls != 1 || adapter.submits != 2 || adapter.selfTests != 1 {
		t.Fatalf("result=%+v err=%v submits=%d selftests=%d", r, err, adapter.submits, adapter.selfTests)
	}
}
