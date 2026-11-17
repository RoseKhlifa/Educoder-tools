package solver

import (
	"context"
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

func headerContextSnapshot(status, stage, reason string) problem.Snapshot {
	snap := testSnapshot()
	snap.Files[0].Path = "answer.h"
	snap.Files[0].Filename = "answer.h"
	snap.Files[0].Extension = ".h"
	snap.ReadOnlyContext = &problem.ReadOnlyContextDiagnostic{Status: status, Stage: stage, Reason: reason}
	return snap
}

func TestTechnicalHeaderContextFailureDoesNotSpendModelBudget(t *testing.T) {
	for _, reason := range []string{"unavailable_timeout", "unavailable_network", "missing_task_identifier", "filename_mismatch", "invalid_response"} {
		t.Run(reason, func(t *testing.T) {
			store := &fakeStore{}
			provider := &fakeProvider{candidates: []answer.Candidate{{Kind: "programming", Language: "cpp", Files: []problem.SolutionFile{{Path: "answer.h", Content: "good"}}}}}
			adapter := &fakeAdapter{snap: headerContextSnapshot("unavailable", "driver", reason)}
			result, err := New(store, provider, Config{}).Run(context.Background(), adapter, recoveryRequest())
			if err != nil || result.Status != "context_unavailable" || result.ModelCalls != 0 || provider.calls != 0 || result.CostMicroUSD != 0 || len(store.usages) != 0 || adapter.selfTests != 0 {
				t.Fatalf("technical context failure triggered blind generation: result=%+v err=%v calls=%d", result, err, provider.calls)
			}
		})
	}
}

func TestTechnicalHeaderContextFailureKeepsBankRevalidation(t *testing.T) {
	source := []problem.SolutionFile{{Path: "answer.h", Content: "good"}}
	hash, _ := problem.HashFiles(source)
	store := &fakeStore{candidates: []questionbank.Candidate{{ID: 9, Ref: questionbank.Ref{RevisionHash: "rev", ContractHash: "contract"}, CandidateInput: questionbank.CandidateInput{Files: source, Source: "verified"}, FilesHash: hash, Status: "upstream_verified"}}}
	provider := &fakeProvider{}
	adapter := &fakeAdapter{snap: headerContextSnapshot("unavailable", "root", "unavailable_network")}
	result, err := New(store, provider, Config{}).Run(context.Background(), adapter, recoveryRequest())
	if err != nil || result.Status != "selftest_passed" || provider.calls != 0 || adapter.selfTests != 1 {
		t.Fatalf("optional context invalidated a reusable answer: result=%+v err=%v calls=%d tests=%d", result, err, provider.calls, adapter.selfTests)
	}
}

func TestMissingOptionalHeaderDriverStillAllowsGeneration(t *testing.T) {
	provider := &fakeProvider{candidates: []answer.Candidate{{Kind: "programming", Language: "cpp", Files: []problem.SolutionFile{{Path: "answer.h", Content: "good"}}}}}
	adapter := &fakeAdapter{snap: headerContextSnapshot("missing", "association", "no_matching_driver")}
	result, err := New(&fakeStore{}, provider, Config{}).Run(context.Background(), adapter, recoveryRequest())
	if err != nil || result.Status != "selftest_passed" || provider.calls != 1 {
		t.Fatalf("a legitimately absent optional driver blocked generation: result=%+v err=%v calls=%d", result, err, provider.calls)
	}
}
