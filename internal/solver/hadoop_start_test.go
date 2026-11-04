package solver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
)

type hadoopStartTestAdapter struct {
	fakeAdapter
	first         SelfTest
	startErr      error
	startCalls    int
	selfTestCalls int
}

func (a *hadoopStartTestAdapter) SelfTest(ctx context.Context, target Target, file problem.SolutionFile, input, expected string) (SelfTest, error) {
	a.selfTestCalls++
	if a.selfTestCalls == 1 {
		return a.first, nil
	}
	return a.fakeAdapter.SelfTest(ctx, target, file, input, expected)
}

func (a *hadoopStartTestAdapter) StartHadoop(context.Context, Target) error {
	a.startCalls++
	return a.startErr
}

func hadoopStartMarkerSelfTest(t *testing.T, completed, finished bool, savedHash string) SelfTest {
	t.Helper()
	return SelfTest{Completed: completed, ProcessFinishedSeen: finished, SavedFilesHash: savedHash, Output: runtimeStartRequiredMessage}
}

func hadoopStartSnapshot() problem.Snapshot {
	snapshot := testSnapshot()
	snapshot.EditMode = "single_file"
	return snapshot
}

func TestHadoopStartPromptStartsOnceAndRetriesSameCandidate(t *testing.T) {
	filesHash, err := problem.HashFiles(candidate("good").Files)
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{}
	adapter := &hadoopStartTestAdapter{fakeAdapter: fakeAdapter{snap: hadoopStartSnapshot()}, first: hadoopStartMarkerSelfTest(t, true, true, filesHash)}
	provider := &fakeProvider{candidates: []answer.Candidate{candidate("good")}}
	result, runErr := New(store, provider, Config{}).Run(context.Background(), adapter, recoveryRequest())
	if runErr != nil || result.Status != "selftest_passed" || provider.calls != 1 || adapter.startCalls != 1 || adapter.selfTestCalls != 2 {
		t.Fatalf("result=%+v err=%v model=%d starts=%d selftests=%d", result, runErr, provider.calls, adapter.startCalls, adapter.selfTestCalls)
	}
	if len(store.validations) != 2 || store.validations[0].Result != "unknown" || store.validations[1].Result != "passed" {
		t.Fatalf("Hadoop startup was not isolated from code evidence: %+v", store.validations)
	}
}

func TestHadoopReadinessFailureStopsUnknownWithoutReplay(t *testing.T) {
	filesHash, err := problem.HashFiles(candidate("good").Files)
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{}
	adapter := &hadoopStartTestAdapter{fakeAdapter: fakeAdapter{snap: hadoopStartSnapshot()}, first: hadoopStartMarkerSelfTest(t, true, true, filesHash), startErr: errors.New("private runtime details")}
	provider := &fakeProvider{candidates: []answer.Candidate{candidate("good")}}
	result, runErr := New(store, provider, Config{}).Run(context.Background(), adapter, recoveryRequest())
	if runErr == nil || result.Status != "uncertain" || result.SelfTestStatus != "unknown" || provider.calls != 1 || adapter.startCalls != 1 || adapter.selfTestCalls != 1 {
		t.Fatalf("result=%+v err=%v model=%d starts=%d selftests=%d", result, runErr, provider.calls, adapter.startCalls, adapter.selfTestCalls)
	}
	if strings.Contains(runErr.Error(), "private runtime details") {
		t.Fatalf("readiness failure leaked upstream detail: %v", runErr)
	}
	for _, validation := range store.validations {
		if validation.Result != "unknown" {
			t.Fatalf("readiness failure became code evidence: %+v", store.validations)
		}
	}
}

func TestHadoopMarkerRequiresCompleteBoundSelfTestEvidence(t *testing.T) {
	filesHash, err := problem.HashFiles(candidate("good").Files)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		first SelfTest
	}{
		{name: "incomplete", first: hadoopStartMarkerSelfTest(t, false, true, filesHash)},
		{name: "missing-finished-marker", first: hadoopStartMarkerSelfTest(t, true, false, filesHash)},
		{name: "hash-mismatch", first: hadoopStartMarkerSelfTest(t, true, true, "other")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{}
			adapter := &hadoopStartTestAdapter{fakeAdapter: fakeAdapter{snap: hadoopStartSnapshot()}, first: tc.first}
			provider := &fakeProvider{candidates: []answer.Candidate{candidate("good")}}
			req := recoveryRequest()
			req.MaxModelCalls = 1
			_, _ = New(store, provider, Config{}).Run(context.Background(), adapter, req)
			if adapter.startCalls != 0 {
				t.Fatalf("invalid marker triggered Hadoop start: %d", adapter.startCalls)
			}
		})
	}
}
