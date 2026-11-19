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

// selfTestFirstAdapter rejects a formal build unless this candidate's public
// self-test ran first, as the platform does: log_output starts the game
// runtime, and game_build without it returns API status -2 (seen on
// production 2026-11-20 for every level that skipped the self-test).
type selfTestFirstAdapter struct {
	fakeAdapter
	selfTested map[string]bool
	rejected   int
}

func (a *selfTestFirstAdapter) SelfTest(ctx context.Context, target Target, file problem.SolutionFile, input, expected string) (SelfTest, error) {
	if a.selfTested == nil {
		a.selfTested = map[string]bool{}
	}
	a.selfTested[file.Content] = true
	return a.fakeAdapter.SelfTest(ctx, target, file, input, expected)
}

func (a *selfTestFirstAdapter) Submit(ctx context.Context, target Target, file problem.SolutionFile) (Evaluation, error) {
	if !a.selfTested[file.Content] {
		a.rejected++
		return Evaluation{Status: "build_error", Feedback: "game_build.json: API status -2"}, errAPIStatusMinus2
	}
	return a.fakeAdapter.Submit(ctx, target, file)
}

var errAPIStatusMinus2 = &apiStatusFixtureError{}

type apiStatusFixtureError struct{}

func (*apiStatusFixtureError) Error() string { return "POST game_build.json: API status -2" }

func TestVerifiedCandidateStillRunsPublicSelfTestBeforeFormal(t *testing.T) {
	adapter := &selfTestFirstAdapter{fakeAdapter: fakeAdapter{snap: testSnapshot()}}
	req := recoveryRequest()
	req.Mode = ModeSubmit
	r, err := New(verifiedGoodStore(), nil, Config{}).Run(context.Background(), adapter, req)
	if err != nil || r.Status != "passed" || r.SelfTestStatus != "passed" || adapter.selfTests != 1 || adapter.submits != 1 || adapter.rejected != 0 {
		t.Fatalf("verified candidate must self-test before formal: result=%+v err=%v selftests=%d submits=%d rejected=%d", r, err, adapter.selfTests, adapter.submits, adapter.rejected)
	}
}

func TestVerifiedCandidateStillSelfTestsInSelfTestMode(t *testing.T) {
	adapter := &fakeAdapter{snap: testSnapshot()}
	r, err := New(verifiedGoodStore(), nil, Config{}).Run(context.Background(), adapter, recoveryRequest())
	if err != nil || r.Status != "selftest_passed" || adapter.selfTests != 1 || adapter.submits != 0 {
		t.Fatalf("explicit self-test skipped: result=%+v err=%v selftests=%d", r, err, adapter.selfTests)
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
	r, err := New(verifiedGoodStore(), p, Config{}).Run(context.Background(), adapter, req)
	// Both the verified and the repaired candidate self-test before formal.
	if err != nil || r.Status != "passed" || r.ModelCalls != 1 || adapter.submits != 2 || adapter.selfTests != 2 {
		t.Fatalf("result=%+v err=%v submits=%d selftests=%d", r, err, adapter.submits, adapter.selfTests)
	}
}
