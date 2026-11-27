package solver

import (
	"context"
	"testing"

	"educoder-cli/internal/answer"
)

// A classmate's full-score answer is evaluated before any model call and, on
// a pass, promoted so the next student reuses it as verified.
func TestObservedFullScoreAnswerIsTriedBeforeTheModel(t *testing.T) {
	bank, observed := operatorFixture(t, "collection:platform_observed", "good")
	provider := &fakeProvider{candidates: []answer.Candidate{candidate("bad")}}
	adapter := &importedAdapter{fakeAdapter: fakeAdapter{snap: testSnapshot()}}
	req := recoveryRequest()
	req.Mode = ModeSubmit
	r, err := New(bank, provider, Config{}).Run(context.Background(), adapter, req)
	if err != nil || r.Status != "passed" || r.CandidateID != observed.ID || r.ModelCalls != 0 || provider.calls != 0 {
		t.Fatalf("observed answer was not tried first: result=%+v err=%v provider calls=%d", r, err, provider.calls)
	}
	stored, err := bank.Candidate(context.Background(), observed.ID)
	if err != nil || stored.Status != "upstream_verified" {
		t.Fatalf("a passing observed answer must become verified: %+v err=%v", stored, err)
	}
}

// A wrong observed answer costs one evaluation; the model then repairs it with
// the concrete feedback, as for any bank candidate.
func TestWrongObservedAnswerFallsBackToTheModel(t *testing.T) {
	bank, observed := operatorFixture(t, "collection:platform_observed", "bad")
	provider := &fakeProvider{candidates: []answer.Candidate{candidate("good")}}
	adapter := &importedAdapter{fakeAdapter: fakeAdapter{snap: testSnapshot()}}
	req := recoveryRequest()
	req.Mode = ModeSubmit
	r, err := New(bank, provider, Config{}).Run(context.Background(), adapter, req)
	if err != nil || r.Status != "passed" || r.CandidateID == observed.ID || provider.calls != 1 {
		t.Fatalf("fallback after a wrong observed answer: result=%+v err=%v provider calls=%d", r, err, provider.calls)
	}
}
