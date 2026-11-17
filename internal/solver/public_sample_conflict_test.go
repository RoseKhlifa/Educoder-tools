package solver

import (
	"context"
	"strings"
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

func conflictingPublicMatrixSnapshot() problem.Snapshot {
	s := testSnapshot()
	s.StatementMarkdown = "删除顶点后输出邻接矩阵。\n### 输出\n每组数据输出n-1行，每行n-1个数。"
	s.PublicTests = []problem.PublicTest{{Stdin: "4 2\n1 3\n2 4\n3\n0 0\n", ExpectedStdout: "0 0 1 0\n0 0 0 1\n1 0 0 0\n0 1 0 0\n"}}
	return s
}

type sampleConflictAdapter struct{ fakeAdapter }

func (a *sampleConflictAdapter) SelfTest(_ context.Context, _ Target, file problem.SolutionFile, _, expected string) (SelfTest, error) {
	a.selfTests++
	return SelfTest{Completed: true, Output: expected, Matches: file.Content == "good"}, nil
}

func TestPublicContractConflictStopsCacheMissBeforeModelOrSubmission(t *testing.T) {
	store := &fakeStore{}
	provider := &fakeProvider{candidates: []answer.Candidate{candidate("good")}}
	adapter := &sampleConflictAdapter{fakeAdapter: fakeAdapter{snap: conflictingPublicMatrixSnapshot()}}
	req := recoveryRequest()
	req.Mode = ModeSubmit
	result, err := New(store, provider, Config{}).Run(context.Background(), adapter, req)
	if err != nil || result.Status != "public_contract_conflict" || result.ModelCalls != 0 || provider.calls != 0 || adapter.selfTests != 0 || adapter.submits != 0 || len(store.usages) != 0 {
		t.Fatalf("conflicting cache miss spent calls or submitted: result=%+v err=%v calls=%d tests=%d submits=%d", result, err, provider.calls, adapter.selfTests, adapter.submits)
	}
	message := strings.Join(result.Diagnostics, " ")
	if !strings.Contains(message, "sample=1") || !strings.Contains(message, "expected=4x4") || !strings.Contains(message, "required=3x3") || strings.Contains(message, adapter.snap.StatementMarkdown) || strings.Contains(message, "0 0 1 0") {
		t.Fatalf("missing fixed summary or leaked public material: %q", message)
	}
}

func TestPublicContractConflictKeepsVerifiedBankCandidateEligibleForNormalValidation(t *testing.T) {
	store := &fakeStore{candidates: []questionbank.Candidate{{ID: 1, Status: "upstream_verified", CandidateInput: questionbank.CandidateInput{Files: candidate("good").Files, Source: "verified"}}}}
	provider := &fakeProvider{}
	adapter := &sampleConflictAdapter{fakeAdapter: fakeAdapter{snap: conflictingPublicMatrixSnapshot()}}
	req := recoveryRequest()
	req.Mode = ModeSubmit
	result, err := New(store, provider, Config{}).Run(context.Background(), adapter, req)
	if err != nil || result.Status != "passed" || provider.calls != 0 || adapter.selfTests != 1 || adapter.submits != 1 {
		t.Fatalf("verified candidate was invalidated or skipped: result=%+v err=%v tests=%d submits=%d", result, err, adapter.selfTests, adapter.submits)
	}
}

func TestCorrectedPublicContractAutomaticallyAllowsNormalGeneration(t *testing.T) {
	snap := conflictingPublicMatrixSnapshot()
	snap.PublicTests[0].ExpectedStdout = "0 0 0\n0 0 1\n0 1 0\n"
	provider := &fakeProvider{candidates: []answer.Candidate{candidate("good")}}
	adapter := &sampleConflictAdapter{fakeAdapter: fakeAdapter{snap: snap}}
	req := recoveryRequest()
	req.Mode = ModeSubmit
	result, err := New(&fakeStore{}, provider, Config{}).Run(context.Background(), adapter, req)
	if err != nil || result.Status != "passed" || result.ModelCalls != 1 || adapter.selfTests != 1 || adapter.submits != 1 {
		t.Fatalf("corrected public data remained blocked: result=%+v err=%v", result, err)
	}
}
