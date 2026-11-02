package solver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
)

const formalRuntimeFailureFeedback = runtimeStartupFailureMessage + "\nPublic test: input=\"\" expected=\"通关成功\" actual=\"编译失败，请在测试结果中查看具体的错误信息\""

type formalRuntimeRecoveryAdapter struct {
	fakeAdapter
	evaluations []Evaluation
	submitErr   error
	submitted   []problem.SolutionFile
	skipHashes  bool
}

func (a *formalRuntimeRecoveryAdapter) SelfTest(_ context.Context, _ Target, file problem.SolutionFile, _, expected string) (SelfTest, error) {
	a.selfTests++
	hash, _ := problem.HashFiles([]problem.SolutionFile{file})
	return SelfTest{Completed: true, Matches: true, Output: expected, SavedFilesHash: hash}, nil
}

func (a *formalRuntimeRecoveryAdapter) Submit(_ context.Context, _ Target, file problem.SolutionFile) (Evaluation, error) {
	i := a.submits
	a.submits++
	a.submitted = append(a.submitted, file)
	if i >= len(a.evaluations) {
		i = len(a.evaluations) - 1
	}
	eval := a.evaluations[i]
	hash, _ := problem.HashFiles([]problem.SolutionFile{file})
	if eval.SavedFilesHash == "" && !a.skipHashes {
		eval.SavedFilesHash = hash
	}
	if eval.SubmittedFilesHash == "" && !a.skipHashes {
		eval.SubmittedFilesHash = hash
	}
	return eval, a.submitErr
}

func formalRuntimeFailureEvaluation() Evaluation {
	return Evaluation{Completed: true, Status: "0", Feedback: formalRuntimeFailureFeedback, TestCount: 1}
}

func formalRuntimeRecoveryRequest() Request {
	req := recoveryRequest()
	req.Mode, req.MaxModelCalls, req.MaxEvaluations = ModeSubmit, 3, 3
	return req
}

func formalRuntimeRecoverySnapshot() problem.Snapshot {
	snap := testSnapshot()
	snap.EditMode = "single_file"
	return snap
}

func TestFormalRuntimeStartupFailureRetriesSameCandidate(t *testing.T) {
	store := &fakeStore{}
	provider := &fakeProvider{candidates: []answer.Candidate{candidate("good"), candidate("good"), candidate("good")}}
	adapter := &formalRuntimeRecoveryAdapter{fakeAdapter: fakeAdapter{snap: formalRuntimeRecoverySnapshot()}, evaluations: []Evaluation{
		formalRuntimeFailureEvaluation(),
		{Completed: true, Passed: true, Status: "2", TestCount: 1, PassedCount: 1},
	}}
	r, err := New(store, provider, Config{}).Run(context.Background(), adapter, formalRuntimeRecoveryRequest())
	if err != nil || r.Status != "passed" || r.ModelCalls != 1 || r.CostMicroUSD != 1 || r.ReservedMicroUSD != 0 || len(store.usages) != 1 || adapter.submits != 2 || adapter.selfTests != 1 {
		t.Fatalf("result=%+v err=%v model_calls=%d submits=%d selftests=%d", r, err, provider.calls, adapter.submits, adapter.selfTests)
	}
	if adapter.submitted[0] != adapter.submitted[1] || len(store.candidates) != 1 || len(store.validations) != 3 {
		t.Fatalf("environment retry changed the candidate or lost evidence: files=%+v candidates=%+v validations=%+v", adapter.submitted, store.candidates, store.validations)
	}
	unknown, passed := store.validations[1], store.validations[2]
	if unknown.Kind != "formal" || unknown.Result != "unknown" || unknown.Terminal || !unknown.Complete || unknown.Feedback != formalRuntimeFailureFeedback || passed.Result != "passed" || !passed.Terminal || !passed.Complete {
		t.Fatalf("formal evidence=%+v", store.validations)
	}
	var state runState
	if err := json.Unmarshal(store.runs[r.RunID].Data, &state); err != nil || state.Attempts != 2 || len(state.RejectedFilesHashes) != 0 || len(state.RepairHistory) != 0 {
		t.Fatalf("environment failure entered code repair state: %+v err=%v", state, err)
	}
}

func TestRepeatedFormalRuntimeStartupFailureStopsWithoutModelRepair(t *testing.T) {
	for _, limit := range []int{1, 3} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			store := &fakeStore{}
			provider := &fakeProvider{candidates: []answer.Candidate{candidate("good")}}
			adapter := &formalRuntimeRecoveryAdapter{fakeAdapter: fakeAdapter{snap: formalRuntimeRecoverySnapshot()}, evaluations: []Evaluation{formalRuntimeFailureEvaluation()}}
			req := formalRuntimeRecoveryRequest()
			req.MaxEvaluations = limit
			r, err := New(store, provider, Config{}).Run(context.Background(), adapter, req)
			wantSubmits := min(limit, 2)
			if err == nil || r.Status != "uncertain" || r.ModelCalls != 1 || adapter.submits != wantSubmits || len(store.validations) != wantSubmits+1 {
				t.Fatalf("result=%+v err=%v submits=%d validations=%+v", r, err, adapter.submits, store.validations)
			}
			for _, v := range store.validations[1:] {
				if v.Result != "unknown" || v.Terminal || !v.Complete {
					t.Fatalf("environment fault became code failure: %+v", v)
				}
			}
			var state runState
			if err := json.Unmarshal(store.runs[r.RunID].Data, &state); err != nil || state.Attempts != wantSubmits || len(state.RejectedFilesHashes) != 0 || len(state.RepairHistory) != 0 || state.GenerationRecovery.RepeatedCandidates != 0 {
				t.Fatalf("durable state=%+v err=%v", state, err)
			}
			if _, err := New(store, provider, Config{}).ContinueRun(context.Background(), adapter, req, r.RunID, Continuation{AdditionalEvaluations: 2}); err == nil || adapter.submits != wantSubmits {
				t.Fatal("uncertain build was replayed by continuation")
			}
		})
	}
}

func TestFormalRuntimeFailureDoesNotReplayUncertainOrDifferentBuild(t *testing.T) {
	for _, tc := range []struct {
		name       string
		change     func(*Evaluation)
		err        error
		skipHashes bool
	}{
		{name: "submit error", err: errors.New("evaluation polling disconnected")},
		{name: "incomplete", change: func(e *Evaluation) { e.Completed = false }},
		{name: "different saved source", change: func(e *Evaluation) { e.SavedFilesHash = "other" }},
		{name: "different submitted source", change: func(e *Evaluation) { e.SubmittedFilesHash = "other" }},
		{name: "source read-back evidence missing", skipHashes: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{}
			eval := formalRuntimeFailureEvaluation()
			if tc.change != nil {
				tc.change(&eval)
			}
			adapter := &formalRuntimeRecoveryAdapter{fakeAdapter: fakeAdapter{snap: formalRuntimeRecoverySnapshot()}, evaluations: []Evaluation{eval}, submitErr: tc.err, skipHashes: tc.skipHashes}
			provider := &fakeProvider{candidates: []answer.Candidate{candidate("good")}}
			r, err := New(store, provider, Config{}).Run(context.Background(), adapter, formalRuntimeRecoveryRequest())
			if err == nil || r.Status != "uncertain" || provider.calls != 1 || adapter.submits != 1 || len(store.validations) != 2 || store.validations[1].Result != "unknown" {
				t.Fatalf("result=%+v err=%v calls=%d submits=%d validations=%+v", r, err, provider.calls, adapter.submits, store.validations)
			}
		})
	}
}

func TestFormalRuntimeRetryKeepsEvaluationLimitAndPassRequirements(t *testing.T) {
	for _, tc := range []struct {
		name       string
		eval       Evaluation
		status     string
		validation string
	}{
		{name: "code failure consumes final slot", eval: Evaluation{Completed: true, Status: "0", Feedback: "SyntaxError: invalid syntax", TestCount: 1}, status: "attempts_exhausted", validation: "failed"},
		{name: "empty pass evidence", eval: Evaluation{Completed: true, Passed: true, Status: "2"}, status: "uncertain", validation: "unknown"},
		{name: "partial pass evidence", eval: Evaluation{Completed: true, Passed: true, Status: "2", TestCount: 2, PassedCount: 1}, status: "uncertain", validation: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{}
			provider := &fakeProvider{candidates: []answer.Candidate{candidate("good"), candidate("corrected")}}
			adapter := &formalRuntimeRecoveryAdapter{fakeAdapter: fakeAdapter{snap: formalRuntimeRecoverySnapshot()}, evaluations: []Evaluation{formalRuntimeFailureEvaluation(), tc.eval}}
			req := formalRuntimeRecoveryRequest()
			req.MaxEvaluations = 2
			r, err := New(store, provider, Config{}).Run(context.Background(), adapter, req)
			if r.Status != tc.status || (err != nil) != (tc.status == "uncertain") || provider.calls != 1 || adapter.submits != 2 || len(store.validations) != 3 || store.validations[1].Result != "unknown" || store.validations[2].Result != tc.validation {
				t.Fatalf("result=%+v err=%v calls=%d submits=%d validations=%+v", r, err, provider.calls, adapter.submits, store.validations)
			}
			var state runState
			if err := json.Unmarshal(store.runs[r.RunID].Data, &state); err != nil || state.Attempts != 2 || state.Limits.MaxEvaluations != 2 {
				t.Fatalf("retry changed evaluation capacity: state=%+v err=%v", state, err)
			}
		})
	}
}

func TestFormalRuntimeStartupFailureRequiresExactPlatformDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name     string
		feedback string
		want     bool
	}{
		{name: "exact marker", feedback: runtimeStartupFailureMessage, want: true},
		{name: "observed public placeholder", feedback: formalRuntimeFailureFeedback, want: true},
		{name: "CRLF", feedback: runtimeStartupFailureMessage + "\r\n", want: true},
		{name: "quoted sample data", feedback: runtimeStartupFailureMessage + "\n" + fmt.Sprintf("Public test: input=%q expected=%q actual=%q", "first\n\"quoted\"", "ok\n", "编译失败，请在测试结果中查看具体的错误信息"), want: true},
		{name: "short marker", feedback: "启动环境失败"},
		{name: "leading empty line", feedback: "\n" + runtimeStartupFailureMessage},
		{name: "indented marker", feedback: "  " + runtimeStartupFailureMessage},
		{name: "trailing whitespace line", feedback: runtimeStartupFailureMessage + "\n\t"},
		{name: "actual compiler diagnostic", feedback: runtimeStartupFailureMessage + "\nPublic test: input=\"\" expected=\"ok\" actual=\"SyntaxError: invalid syntax\""},
		{name: "trailing compiler diagnostic", feedback: formalRuntimeFailureFeedback + " SyntaxError"},
		{name: "missing input field", feedback: runtimeStartupFailureMessage + "\nPublic test: expected=\"ok\" actual=\"编译失败，请在测试结果中查看具体的错误信息\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eval := formalRuntimeFailureEvaluation()
			eval.Feedback = tc.feedback
			if got := formalRuntimeStartupFailure(eval); got != tc.want {
				t.Fatalf("feedback=%q classified=%t want=%t", tc.feedback, got, tc.want)
			}
		})
	}
}

func TestFormalCodeFailuresStillRepairInsteadOfEnvironmentReplay(t *testing.T) {
	for _, tc := range []struct {
		name     string
		feedback string
		status   string
		terminal bool
	}{
		{name: "syntax error", feedback: "SyntaxError: invalid syntax", status: "0"},
		{name: "generic compiler placeholder", feedback: "编译失败，请在测试结果中查看具体的错误信息", status: "0"},
		{name: "mixed compiler diagnostic", feedback: formalRuntimeFailureFeedback + "\nSyntaxError: invalid syntax", status: "0"},
		{name: "embedded environment marker", feedback: "program output: " + formalRuntimeFailureFeedback, status: "0"},
		{name: "different lifecycle status", feedback: formalRuntimeFailureFeedback, status: "1"},
		{name: "terminal workspace", feedback: formalRuntimeFailureFeedback, status: "0", terminal: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{}
			snap := formalRuntimeRecoverySnapshot()
			if tc.terminal {
				snap.EditMode = "terminal"
			}
			adapter := &formalRuntimeRecoveryAdapter{fakeAdapter: fakeAdapter{snap: snap}, evaluations: []Evaluation{
				{Completed: true, Status: tc.status, Feedback: tc.feedback, TestCount: 1},
				{Completed: true, Passed: true, Status: "2", TestCount: 1, PassedCount: 1},
			}}
			provider := &fakeProvider{candidates: []answer.Candidate{candidate("good"), candidate("corrected")}}
			r, err := New(store, provider, Config{}).Run(context.Background(), adapter, formalRuntimeRecoveryRequest())
			if err != nil || r.Status != "passed" || provider.calls != 2 || adapter.submits != 2 || adapter.submitted[0] == adapter.submitted[1] || store.validations[1].Result != "failed" {
				t.Fatalf("code failure used environment recovery: result=%+v err=%v calls=%d submitted=%+v validations=%+v", r, err, provider.calls, adapter.submitted, store.validations)
			}
		})
	}
}
