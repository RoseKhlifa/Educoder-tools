package solver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

func TestTruncationRecoverySurvivesExplicitContinuation(t *testing.T) {
	store := &fakeStore{}
	p := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, req answer.Request, n int) (answer.Result, error) {
		if n == 1 {
			return solved("partial", 2), &answer.Error{Kind: "truncated_output"}
		}
		if req.Profile.MaxTokens != 8192 || req.Profile.Thinking != "disabled" || req.Profile.ReasoningEffort != "" {
			t.Fatalf("continuation lost complete output profile: %+v", req.Profile)
		}
		return solved("good", 3), nil
	}}
	req := recoveryRequest()
	req.MaxModelCalls, req.MaxEvaluations, req.BudgetMicroUSD = 1, 1, 100
	r, err := New(store, p, Config{}).Run(context.Background(), &fakeAdapter{snap: testSnapshot()}, req)
	if err == nil || r.Status != "model_error" || r.ModelCalls != 1 {
		t.Fatalf("first=%+v err=%v", r, err)
	}
	r, err = New(store, p, Config{}).ContinueRun(context.Background(), &fakeAdapter{snap: testSnapshot()}, req, r.RunID, Continuation{AdditionalModelCalls: 1, AdditionalEvaluations: 1})
	if err != nil || r.Status != "selftest_passed" || r.ModelCalls != 2 || r.CostMicroUSD != 5 || len(store.usages) != 2 {
		t.Fatalf("continued=%+v err=%v usage=%+v", r, err, store.usages)
	}
}

func TestRepeatedRejectedOutputEscalatesAndStopsBeforeSpendingAllCalls(t *testing.T) {
	store := &fakeStore{}
	a := &fakeAdapter{snap: testSnapshot()}
	p := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, req answer.Request, n int) (answer.Result, error) {
		if n >= 3 && (req.Profile.Model != answer.ProProfile().Model || req.Profile.MaxTokens != 8192 || req.Profile.Thinking != "disabled") {
			t.Fatalf("unchanged failed answer did not escalate: %+v", req.Profile)
		}
		return solved("bad", 2), nil
	}}
	req := recoveryRequest()
	req.MaxModelCalls, req.MaxEvaluations, req.BudgetMicroUSD = 40, 40, 1000
	r, err := New(store, p, Config{}).Run(context.Background(), a, req)
	if err != nil || r.Status != "generation_stalled" || r.ModelCalls != 4 || r.CostMicroUSD != 8 || a.selfTests != 1 || a.submits != 0 {
		t.Fatalf("result=%+v err=%v selftests=%d submits=%d", r, err, a.selfTests, a.submits)
	}
	if len(store.usages) != 4 || len(store.candidates) != 1 {
		t.Fatalf("usage or source duplication: usage=%d candidates=%d", len(store.usages), len(store.candidates))
	}
	var state runState
	if err := json.Unmarshal(store.runs[r.RunID].Data, &state); err != nil || state.GenerationRecovery.RepeatedCandidates != 3 || !state.GenerationRecovery.CompactOutput {
		t.Fatalf("recovery checkpoint=%+v err=%v", state.GenerationRecovery, err)
	}
}

func TestRepeatedOutputRecoveryCanProduceNewPassingSource(t *testing.T) {
	store := &fakeStore{}
	p := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, _ answer.Request, n int) (answer.Result, error) {
		if n <= 3 {
			return solved("bad", 1), nil
		}
		return solved("good", 1), nil
	}}
	req := recoveryRequest()
	req.MaxModelCalls, req.MaxEvaluations = 6, 6
	r, err := New(store, p, Config{}).Run(context.Background(), &fakeAdapter{snap: testSnapshot()}, req)
	if err != nil || r.Status != "selftest_passed" || r.ModelCalls != 4 {
		t.Fatalf("result=%+v err=%v", r, err)
	}
	var state runState
	json.Unmarshal(store.runs[r.RunID].Data, &state)
	if state.GenerationRecovery.RepeatedCandidates != 0 {
		t.Fatalf("new evaluated source retained duplicate streak: %+v", state.GenerationRecovery)
	}
}

func TestConsecutiveTruncatedOutputsAreBoundedAndCharged(t *testing.T) {
	store := &fakeStore{}
	p := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, req answer.Request, n int) (answer.Result, error) {
		if n > 1 && (req.Profile.MaxTokens != 8192 || req.Profile.Thinking != "disabled") {
			t.Fatalf("retry failed to reserve source output: %+v", req.Profile)
		}
		return solved("partial", 2), &answer.Error{Kind: "truncated_output"}
	}}
	req := recoveryRequest()
	req.MaxModelCalls, req.MaxEvaluations, req.BudgetMicroUSD = 40, 40, 1000
	a := &fakeAdapter{snap: testSnapshot()}
	r, err := New(store, p, Config{}).Run(context.Background(), a, req)
	if err == nil || r.Status != "generation_stalled" || r.ModelCalls != 3 || r.CostMicroUSD != 6 || len(store.usages) != 3 || a.selfTests != 0 || a.submits != 0 {
		t.Fatalf("result=%+v err=%v selftests=%d submits=%d usage=%d", r, err, a.selfTests, a.submits, len(store.usages))
	}
}

func TestRepeatedCandidateCounterSurvivesContinuation(t *testing.T) {
	store := &fakeStore{}
	p := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, _ answer.Request, _ int) (answer.Result, error) {
		return solved("bad", 2), nil
	}}
	req := recoveryRequest()
	req.MaxModelCalls, req.MaxEvaluations, req.BudgetMicroUSD = 2, 2, 100
	r, err := New(store, p, Config{}).Run(context.Background(), &fakeAdapter{snap: testSnapshot()}, req)
	if err != nil || r.Status != "attempts_exhausted" || r.ModelCalls != 2 {
		t.Fatalf("first=%+v err=%v", r, err)
	}
	r, err = New(store, p, Config{}).ContinueRun(context.Background(), &fakeAdapter{snap: testSnapshot()}, req, r.RunID, Continuation{AdditionalModelCalls: 20, AdditionalEvaluations: 20})
	if err != nil || r.Status != "generation_stalled" || r.ModelCalls != 4 || r.CostMicroUSD != 8 || len(store.usages) != 4 {
		t.Fatalf("continuation reset ineffective generation count: %+v err=%v", r, err)
	}
}

func TestLegacyTruncationCheckpointRestoresOutputProfile(t *testing.T) {
	store := &fakeStore{}
	p := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, req answer.Request, n int) (answer.Result, error) {
		if n == 1 {
			return solved("partial", 2), &answer.Error{Kind: "truncated_output"}
		}
		if req.Profile.MaxTokens != 8192 || req.Profile.Thinking != "disabled" {
			t.Fatalf("legacy feedback failed to restore output profile: %+v", req.Profile)
		}
		return solved("good", 3), nil
	}}
	req := recoveryRequest()
	req.MaxModelCalls, req.MaxEvaluations, req.BudgetMicroUSD = 1, 1, 100
	r, _ := New(store, p, Config{}).Run(context.Background(), &fakeAdapter{snap: testSnapshot()}, req)
	record := store.runs[r.RunID]
	var old map[string]json.RawMessage
	json.Unmarshal(record.Data, &old)
	delete(old, "generation_recovery")
	record.Data, _ = json.Marshal(old)
	store.runs[r.RunID] = record
	r, err := New(store, p, Config{}).ContinueRun(context.Background(), &fakeAdapter{snap: testSnapshot()}, req, r.RunID, Continuation{AdditionalModelCalls: 1, AdditionalEvaluations: 1})
	if err != nil || r.Status != "selftest_passed" || r.CostMicroUSD != 5 {
		t.Fatalf("legacy continuation=%+v err=%v", r, err)
	}
}

type startupFailureAdapter struct {
	fakeAdapter
	failures int
	files    []string
}

func (a *startupFailureAdapter) SelfTest(ctx context.Context, target Target, file problem.SolutionFile, input, expected string) (SelfTest, error) {
	a.files = append(a.files, file.Content)
	if len(a.files) <= a.failures {
		return SelfTest{Completed: true, Output: runtimeStartupFailureMessage}, nil
	}
	return a.fakeAdapter.SelfTest(ctx, target, file, input, expected)
}

func TestKnownRuntimeStartupFailureRetriesSameCandidateWithoutRejection(t *testing.T) {
	store := &fakeStore{}
	a := &startupFailureAdapter{fakeAdapter: fakeAdapter{snap: testSnapshot()}, failures: 1}
	p := &fakeProvider{candidates: []answer.Candidate{candidate("good")}}
	r, err := New(store, p, Config{}).Run(context.Background(), a, recoveryRequest())
	if err != nil || r.Status != "selftest_passed" || p.calls != 1 || len(a.files) != 2 || a.files[0] != a.files[1] {
		t.Fatalf("result=%+v err=%v calls=%d files=%v", r, err, p.calls, a.files)
	}
	if len(store.validations) != 2 || store.validations[0].Result != "unknown" || store.validations[1].Result != "passed" || store.candidates[0].Status == "rejected" {
		t.Fatalf("host failure rejected source: %+v", store.validations)
	}
	var state runState
	json.Unmarshal(store.runs[r.RunID].Data, &state)
	if len(state.RepairHistory) != 0 || len(state.RejectedFilesHashes) != 0 || strings.Contains(feedbackString(state.Feedback), runtimeStartupFailureMessage) {
		t.Fatalf("host failure polluted code repair state: %+v", state)
	}
}

func TestRepeatedRuntimeStartupFailureStopsAsUnknownWithoutModelRepair(t *testing.T) {
	store := &fakeStore{}
	a := &startupFailureAdapter{fakeAdapter: fakeAdapter{snap: testSnapshot()}, failures: 5}
	p := &fakeProvider{candidates: []answer.Candidate{candidate("good")}}
	r, err := New(store, p, Config{}).Run(context.Background(), a, recoveryRequest())
	if err == nil || r.Status != "uncertain" || r.SelfTestStatus != "unknown" || p.calls != 1 || len(a.files) != 2 || a.submits != 0 {
		t.Fatalf("result=%+v err=%v calls=%d retries=%d submits=%d", r, err, p.calls, len(a.files), a.submits)
	}
	for _, v := range store.validations {
		if v.Result != "unknown" {
			t.Fatalf("environment failure became code evidence: %+v", v)
		}
	}
}

func TestRuntimeStartupRetryKeepsGenuineCodeFailureFeedback(t *testing.T) {
	store := &fakeStore{}
	a := &startupFailureAdapter{fakeAdapter: fakeAdapter{snap: testSnapshot()}, failures: 1}
	p := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, req answer.Request, n int) (answer.Result, error) {
		if n == 1 {
			return solved("bad", 1), nil
		}
		if len(req.Feedback) != 1 || req.Feedback[0].ActualStdout != "wrong" || strings.Contains(feedbackString(req.Feedback), runtimeStartupFailureMessage) {
			t.Fatalf("environment failure hid or polluted real code feedback: %+v", req.Feedback)
		}
		return solved("good", 1), nil
	}}
	r, err := New(store, p, Config{}).Run(context.Background(), a, recoveryRequest())
	if err != nil || r.Status != "selftest_passed" || r.ModelCalls != 2 || len(a.files) != 3 || a.files[0] != a.files[1] {
		t.Fatalf("result=%+v err=%v files=%v", r, err, a.files)
	}
}

func TestRuntimeFailureClassifierRequiresCompleteObservedMessage(t *testing.T) {
	for _, tc := range []struct {
		output   string
		expected string
		want     bool
	}{
		{runtimeStartupFailureMessage, "ok", true},
		{" \n" + runtimeStartupFailureMessage + "\r\n", "ok", true},
		{runtimeStartupFailureMessage, runtimeStartupFailureMessage, false},
		{"程序输出：" + runtimeStartupFailureMessage, "ok", false},
		{"启动环境失败", "ok", false},
	} {
		if got := runtimeStartupFailure(tc.output, tc.expected); got != tc.want {
			t.Errorf("classification of %q expected=%q: got=%v want=%v", tc.output, tc.expected, got, tc.want)
		}
	}
}

func legacyEnvironmentCandidate(t *testing.T, content string) questionbank.Candidate {
	t.Helper()
	hash, err := problem.HashFiles(candidate(content).Files)
	if err != nil {
		t.Fatal(err)
	}
	ref := questionbank.Ref{RevisionHash: "rev", ContractHash: "contract"}
	return questionbank.Candidate{
		ID: 1, Ref: ref, FilesHash: hash, Status: "rejected",
		CandidateInput: questionbank.CandidateInput{Files: candidate(content).Files},
		Validations: []questionbank.Validation{{
			Kind: "selftest", Result: "failed", Terminal: true, Complete: true,
			TestCount: 1, FilesHash: hash, SavedFilesHash: hash,
			RevisionHash: ref.RevisionHash, ContractHash: ref.ContractHash,
			Feedback: feedbackString([]answer.Feedback{{Kind: "selftest", Message: runtimeStartupFailureMessage, ActualStdout: runtimeStartupFailureMessage, ExpectedStdout: "ok"}}),
		}},
	}
}

func TestLegacyEnvironmentOnlyRejectionIsRetestedWithoutGeneratingNewCode(t *testing.T) {
	store := &fakeStore{candidates: []questionbank.Candidate{legacyEnvironmentCandidate(t, "good")}}
	a := &fakeAdapter{snap: testSnapshot()}
	req := recoveryRequest()
	req.Mode = ModeSubmit
	r, err := New(store, nil, Config{}).Run(context.Background(), a, req)
	if err != nil || r.Status != "passed" || r.ModelCalls != 0 || a.selfTests != 1 || a.submits != 1 {
		t.Fatalf("legacy runtime error prevented fresh validation: %+v err=%v selftests=%d submits=%d", r, err, a.selfTests, a.submits)
	}
	if store.candidates[0].Status != "upstream_verified" {
		t.Fatalf("candidate was not promoted by actual formal evidence: %+v", store.candidates[0])
	}
}

func TestLegacyEnvironmentCompatibilityRetainsMixedRealCodeFailure(t *testing.T) {
	c := legacyEnvironmentCandidate(t, "bad")
	codeFailure := c.Validations[0]
	codeFailure.Feedback = feedbackString([]answer.Feedback{{Kind: "selftest", Message: "wrong", ActualStdout: "wrong", ExpectedStdout: "ok"}})
	c.Validations = append([]questionbank.Validation{codeFailure}, c.Validations...)
	if legacyRuntimeOnlyRejection(c) {
		t.Fatal("real earlier code failure was erased by a later environment error")
	}
	store := &fakeStore{candidates: []questionbank.Candidate{c}}
	p := &scriptedProvider{estimateCost: 10, generate: func(_ context.Context, req answer.Request, _ int) (answer.Result, error) {
		if req.Previous == nil || len(req.Feedback) != 1 || req.Feedback[0].ActualStdout != "wrong" || strings.Contains(feedbackString(req.Feedback), runtimeStartupFailureMessage) {
			t.Fatalf("mixed legacy evidence was discarded or polluted: %+v", req.Feedback)
		}
		return solved("good", 1), nil
	}}
	a := &fakeAdapter{snap: testSnapshot()}
	r, err := New(store, p, Config{}).Run(context.Background(), a, recoveryRequest())
	if err != nil || r.Status != "selftest_passed" || r.ModelCalls != 1 || a.selfTests != 1 {
		t.Fatalf("old truly failing source was retested: %+v err=%v selftests=%d", r, err, a.selfTests)
	}
}

func TestLegacyEnvironmentCompatibilityRequiresBoundTerminalEvidence(t *testing.T) {
	for _, mutate := range []func(*questionbank.Candidate){
		func(c *questionbank.Candidate) { c.Validations[0].SavedFilesHash = "other" },
		func(c *questionbank.Candidate) { c.Validations[0].Terminal = false },
		func(c *questionbank.Candidate) { c.Validations[0].Result = "unknown" },
		func(c *questionbank.Candidate) { c.Validations[0].Kind = "formal" },
		func(c *questionbank.Candidate) {
			c.Validations[0].Feedback = `[{"kind":"selftest","message":"启动环境失败"}]`
		},
	} {
		c := legacyEnvironmentCandidate(t, "good")
		mutate(&c)
		if legacyRuntimeOnlyRejection(c) {
			t.Fatalf("unbound or uncertain evidence received legacy compatibility: %+v", c.Validations)
		}
	}
}

func TestLegacyEnvironmentCompatibilityAllowsEarlierPassingSamples(t *testing.T) {
	c := legacyEnvironmentCandidate(t, "good")
	c.Validations[0].Feedback = feedbackString([]answer.Feedback{
		{Kind: "selftest_passed", ActualStdout: "ok", ExpectedStdout: "ok"},
		{Kind: "selftest", Message: runtimeStartupFailureMessage, ActualStdout: runtimeStartupFailureMessage, ExpectedStdout: "ok"},
	})
	if !legacyRuntimeOnlyRejection(c) {
		t.Fatal("earlier passing sample was treated as a real code failure")
	}
}
