package solver

import (
	"context"
	"errors"
	"testing"
	"time"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/problem"
)

// busyAdapter refuses the first `busy` self-tests the way the platform does
// under resource pressure, then behaves normally.
type busyAdapter struct {
	fakeAdapter
	busy    int
	refusal *educoder.SelfTestRefusedError
	calls   int
}

func (a *busyAdapter) SelfTest(ctx context.Context, target Target, file problem.SolutionFile, input, expected string) (SelfTest, error) {
	a.calls++
	if a.calls <= a.busy {
		return SelfTest{}, a.refusal
	}
	return a.fakeAdapter.SelfTest(ctx, target, file, input, expected)
}

func fastResourceBackoff(t *testing.T) {
	t.Helper()
	old := selfTestResourceBackoff
	selfTestResourceBackoff = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { selfTestResourceBackoff = old })
}

func TestSelfTestResourcePressureRetriesSameCandidate(t *testing.T) {
	fastResourceBackoff(t)
	adapter := &busyAdapter{fakeAdapter: fakeAdapter{snap: testSnapshot()}, busy: 2, refusal: &educoder.SelfTestRefusedError{Kind: "resource_busy"}}
	req := recoveryRequest()
	req.Mode = ModeSubmit
	r, err := New(verifiedGoodStore(), nil, Config{}).Run(context.Background(), adapter, req)
	if err != nil || r.Status != "passed" || adapter.calls != 3 || adapter.submits != 1 {
		t.Fatalf("result=%+v err=%v calls=%d submits=%d", r, err, adapter.calls, adapter.submits)
	}
}

func TestPersistentResourcePressureIsUnavailableNotUncertain(t *testing.T) {
	fastResourceBackoff(t)
	for name, refusal := range map[string]*educoder.SelfTestRefusedError{
		"busy":    {Kind: "resource_busy"},
		"refused": {Kind: "refused"},
	} {
		t.Run(name, func(t *testing.T) {
			adapter := &busyAdapter{fakeAdapter: fakeAdapter{snap: testSnapshot()}, busy: 99, refusal: refusal}
			provider := &scriptedProvider{estimateCost: 10, generate: func(context.Context, answer.Request, int) (answer.Result, error) {
				t.Fatal("a platform refusal is not code feedback and must not call the model")
				return answer.Result{}, nil
			}}
			req := recoveryRequest()
			req.Mode = ModeSubmit
			r, err := New(verifiedGoodStore(), provider, Config{}).Run(context.Background(), adapter, req)
			var typed *educoder.SelfTestRefusedError
			if !errors.As(err, &typed) || r.Status != "resource_unavailable" || r.SelfTestStatus != "unavailable" || adapter.submits != 0 || r.ModelCalls != 0 {
				t.Fatalf("result=%+v err=%v submits=%d", r, err, adapter.submits)
			}
			wantCalls := 1 + len(selfTestResourceBackoff)
			if name == "refused" {
				wantCalls = 1
			}
			if adapter.calls != wantCalls {
				t.Fatalf("self-test attempts=%d want %d", adapter.calls, wantCalls)
			}
		})
	}
}
