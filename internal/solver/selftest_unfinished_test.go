package solver

import (
	"context"
	"errors"
	"testing"
	"time"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
)

func init() {
	// No real 15 s pauses in tests; a test that cares sets its own value.
	unfinishedSelfTestRetryDelay = time.Millisecond
}

// flakySelfTestAdapter fails its first self-tests the way a save to Educoder
// did on 2026-11-27: the request hung until the client timed out.
type flakySelfTestAdapter struct {
	fakeAdapter
	failures, calls int
}

func (a *flakySelfTestAdapter) SelfTest(ctx context.Context, target Target, file problem.SolutionFile, input, expected string) (SelfTest, error) {
	a.calls++
	if a.calls <= a.failures {
		return SelfTest{}, errors.New("POST /api/myshixuns/x/update_file.json: request failed")
	}
	return a.fakeAdapter.SelfTest(ctx, target, file, input, expected)
}

func TestUnfinishedEditorSelftestIsRunOnceMoreWithTheSameCode(t *testing.T) {
	adapter := &flakySelfTestAdapter{fakeAdapter: fakeAdapter{snap: hadoopStartSnapshot()}, failures: 1}
	provider := &fakeProvider{candidates: []answer.Candidate{candidate("good")}}
	result, err := New(&fakeStore{}, provider, Config{}).Run(context.Background(), adapter, recoveryRequest())
	if err != nil || result.Status != "selftest_passed" || provider.calls != 1 || adapter.calls != 2 {
		t.Fatalf("result=%+v err=%v model=%d selftests=%d", result, err, provider.calls, adapter.calls)
	}
}

func TestSelftestThatNeverFinishesStopsAfterOneRetry(t *testing.T) {
	adapter := &flakySelfTestAdapter{fakeAdapter: fakeAdapter{snap: hadoopStartSnapshot()}, failures: 10}
	provider := &fakeProvider{candidates: []answer.Candidate{candidate("good")}}
	result, err := New(&fakeStore{}, provider, Config{}).Run(context.Background(), adapter, recoveryRequest())
	if err == nil || result.Status != "uncertain" || provider.calls != 1 || adapter.calls != 2 {
		t.Fatalf("result=%+v err=%v model=%d selftests=%d", result, err, provider.calls, adapter.calls)
	}
}
