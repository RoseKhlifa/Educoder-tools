package solver

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

// The runner response the HBase Java levels printed on 2026-11-27 when HBase
// had not been started in their command line.
const hbaseStartPrompt = "Hbase未启动，请先启动Hbase。\r\n"

func TestRuntimeStartRequiredMatchesOnlyAWholeObservedPrompt(t *testing.T) {
	for _, test := range []struct {
		output, expected string
		want             educoder.RuntimeService
	}{
		{runtimeStartRequiredMessage, "ok", educoder.RuntimeHadoop},
		{hbaseStartPrompt, "dept表创建成功\n", educoder.RuntimeHBase},
		{hbaseStartPrompt, "Hbase未启动，请先启动Hbase。", ""},
		{"Hbase未启动", "ok", ""},
		{"row1\n" + hbaseStartPrompt, "ok", ""},
	} {
		got, ok := runtimeStartRequired(test.output, test.expected)
		if got != test.want || ok != (test.want != "") {
			t.Errorf("runtimeStartRequired(%q, %q) = %q %v", test.output, test.expected, got, ok)
		}
	}
}

func TestHBasePromptStartsHBaseOnceAndRetestsTheSameCandidate(t *testing.T) {
	filesHash, err := problem.HashFiles(candidate("good").Files)
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{}
	first := SelfTest{Completed: true, ProcessFinishedSeen: true, SavedFilesHash: filesHash, Output: hbaseStartPrompt}
	adapter := &hadoopStartTestAdapter{fakeAdapter: fakeAdapter{snap: hadoopStartSnapshot()}, first: first}
	provider := &fakeProvider{candidates: []answer.Candidate{candidate("good")}}
	result, runErr := New(store, provider, Config{}).Run(context.Background(), adapter, recoveryRequest())
	if runErr != nil || result.Status != "selftest_passed" || provider.calls != 1 || adapter.selfTestCalls != 2 || !reflect.DeepEqual(adapter.started, []educoder.RuntimeService{educoder.RuntimeHBase}) {
		t.Fatalf("result=%+v err=%v model=%d started=%v selftests=%d", result, runErr, provider.calls, adapter.started, adapter.selfTestCalls)
	}
	if len(store.validations) != 2 || store.validations[0].Result != "unknown" || store.validations[1].Result != "passed" {
		t.Fatalf("the HBase prompt became code evidence: %+v", store.validations)
	}
}

// formalStartAdapter passes the public self-test, then reports the HBase
// prompt from the first formal evaluation and passes the next one.
type formalStartAdapter struct {
	fakeAdapter
	started []educoder.RuntimeService
}

func (a *formalStartAdapter) StartRuntime(_ context.Context, _ Target, service educoder.RuntimeService) error {
	a.started = append(a.started, service)
	return nil
}

func (a *formalStartAdapter) Submit(_ context.Context, _ Target, file problem.SolutionFile) (Evaluation, error) {
	a.submits++
	hash, _ := problem.HashFiles([]problem.SolutionFile{file})
	if a.submits == 1 {
		feedback := "\n" + fmt.Sprintf("Public test: input=%q expected=%q actual=%q", "1", "ok", hbaseStartPrompt)
		return Evaluation{Completed: true, Status: "0", Feedback: feedback, TestCount: 1, SavedFilesHash: hash, SubmittedFilesHash: hash}, nil
	}
	return Evaluation{Completed: true, Passed: true, Status: "2", TestCount: 1, PassedCount: 1, SavedFilesHash: hash, SubmittedFilesHash: hash}, nil
}

func TestFormalHBasePromptStartsHBaseAndEvaluatesTheSameCodeAgain(t *testing.T) {
	store := &fakeStore{}
	adapter := &formalStartAdapter{fakeAdapter: fakeAdapter{snap: hadoopStartSnapshot()}}
	provider := &fakeProvider{candidates: []answer.Candidate{candidate("good")}}
	req := recoveryRequest()
	req.Mode = ModeSubmit
	result, err := New(store, provider, Config{}).Run(context.Background(), adapter, req)
	if err != nil || result.Status != "passed" || provider.calls != 1 || adapter.submits != 2 || !reflect.DeepEqual(adapter.started, []educoder.RuntimeService{educoder.RuntimeHBase}) {
		t.Fatalf("result=%+v err=%v model=%d submits=%d started=%v", result, err, provider.calls, adapter.submits, adapter.started)
	}
	var formal []string
	for _, v := range store.validations {
		if v.Kind == "formal" {
			formal = append(formal, v.Result)
		}
	}
	if !reflect.DeepEqual(formal, []string{"unknown", "passed"}) {
		t.Fatalf("the formal HBase prompt became code evidence: %v", formal)
	}
}

func TestFormalStartPromptNeedsEveryFailingPublicTestToBeThePrompt(t *testing.T) {
	line := func(actual string) string {
		return fmt.Sprintf("Public test: input=%q expected=%q actual=%q", "", "ok", actual)
	}
	for _, test := range []struct {
		name string
		eval Evaluation
		want bool
	}{
		{"prompt", Evaluation{Completed: true, Feedback: "\n" + line(hbaseStartPrompt), TestCount: 2}, true},
		{"compile output first", Evaluation{Completed: true, Feedback: "compile successfully\n" + line(hbaseStartPrompt), TestCount: 1}, true},
		{"one other failure", Evaluation{Completed: true, Feedback: "\n" + line(hbaseStartPrompt) + "\n" + line("wrong"), TestCount: 2}, false},
		{"some passed", Evaluation{Completed: true, Feedback: "\n" + line(hbaseStartPrompt), TestCount: 2, PassedCount: 1}, false},
		{"no public test", Evaluation{Completed: true, Feedback: "evaluation reported 1 failing test sets", TestCount: 1}, false},
		{"incomplete", Evaluation{Feedback: "\n" + line(hbaseStartPrompt), TestCount: 1}, false},
	} {
		if _, got := formalRuntimeStartRequired(test.eval); got != test.want {
			t.Errorf("%s: formalRuntimeStartRequired = %v", test.name, got)
		}
	}
}

// The HBase Java runs of 2026-11-27 recorded the prompt as an ordinary failed
// self-test. Those candidates are re-validated, not treated as wrong code.
func TestHistoricalHBasePromptRejectionIsRetestable(t *testing.T) {
	bank, err := questionbank.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer bank.Close()
	snapshot := testSnapshot()
	snapshot.EditMode = "single_file"
	ref, err := bank.UpsertSnapshot(context.Background(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	record, err := bank.AddCandidate(context.Background(), ref, questionbank.CandidateInput{Files: candidate("good").Files})
	if err != nil {
		t.Fatal(err)
	}
	feedback := feedbackString([]answer.Feedback{{Kind: "selftest", Message: hbaseStartPrompt, ActualStdout: hbaseStartPrompt, ExpectedStdout: "dept表创建成功\n"}})
	if err := bank.RecordValidation(context.Background(), questionbank.Validation{CandidateID: record.ID, Kind: "selftest", Result: "failed", FilesHash: record.FilesHash, SavedFilesHash: record.FilesHash, RevisionHash: record.RevisionHash, ContractHash: record.ContractHash, Feedback: feedback, Terminal: true, Complete: true, TestCount: 1}); err != nil {
		t.Fatal(err)
	}
	if record, err = bank.Candidate(context.Background(), record.ID); err != nil {
		t.Fatal(err)
	}
	if record.Status != "rejected" || !hadoopPrerequisiteOnlyRejection(record) {
		t.Fatalf("historical HBase prompt rejection is not retestable: %+v", record)
	}
}
