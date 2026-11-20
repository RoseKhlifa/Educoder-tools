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

// selfTestingGoodStore holds a bank answer whose formal proof is incomplete,
// so it still runs its public samples before the formal evaluation.
func selfTestingGoodStore() *fakeStore {
	store := verifiedGoodStore()
	store.candidates[0].Validations[0].Complete = false
	return store
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
	r, err := New(selfTestingGoodStore(), nil, Config{}).Run(context.Background(), adapter, req)
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
			r, err := New(selfTestingGoodStore(), provider, Config{}).Run(context.Background(), adapter, req)
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

// refusedBuildAdapter passes self-tests and has every formal build refused
// with status -2, as the platform does under resource pressure.
type refusedBuildAdapter struct{ fakeAdapter }

func (a *refusedBuildAdapter) Submit(context.Context, Target, problem.SolutionFile) (Evaluation, error) {
	a.submits++
	return Evaluation{Status: "build_refused", Feedback: "refused"}, &educoder.EvaluationRefusedError{Code: -2}
}

func TestRefusedFormalBuildIsUnavailableNotUncertain(t *testing.T) {
	store := verifiedGoodStore()
	adapter := &refusedBuildAdapter{fakeAdapter: fakeAdapter{snap: testSnapshot()}}
	provider := &scriptedProvider{estimateCost: 10, generate: func(context.Context, answer.Request, int) (answer.Result, error) {
		t.Fatal("a refused build is not code feedback and must not call the model")
		return answer.Result{}, nil
	}}
	req := recoveryRequest()
	req.Mode = ModeSubmit
	r, err := New(store, provider, Config{}).Run(context.Background(), adapter, req)
	var refused *educoder.EvaluationRefusedError
	if !errors.As(err, &refused) || r.Status != "resource_unavailable" || adapter.submits != 1 || r.ModelCalls != 0 {
		t.Fatalf("result=%+v err=%v submits=%d", r, err, adapter.submits)
	}
	formal := 0
	for _, v := range store.validations {
		if v.Kind == "formal" {
			formal++
			if v.Result != "error" {
				t.Fatalf("refused build recorded as %q, want error (no evaluation started)", v.Result)
			}
		}
	}
	if formal != 1 {
		t.Fatalf("formal validations=%d", formal)
	}
}
