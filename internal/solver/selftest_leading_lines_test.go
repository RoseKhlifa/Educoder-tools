package solver

import (
	"context"
	"strings"
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

// jiebaLog is what jieba printed in front of a correct program's output on
// 2026-11-27 ("Matplotlib绘制二维图", expected "词云图绘制成功").
const jiebaLog = "Building prefix dict from the default dictionary ...\r\nDumping model to file cache /tmp/jieba.cache\r\nLoading model cost 1.014 seconds.\r\nPrefix dict has been built successfully.\r\n"

func TestLeadingLinesOnly(t *testing.T) {
	for _, tc := range []struct {
		actual, expected string
		want             bool
	}{
		{jiebaLog + "词云图绘制成功\r\n", "词云图绘制成功", true},
		{"Note: src/step2/Task.java uses or overrides a deprecated API.\nNote: Recompile with -Xlint:deprecation for details.\nrow1:张三丰\nrow2:张无忌\n", "row1:张三丰\nrow2:张无忌\n", true},
		{"词云图绘制成功\n", "词云图绘制成功", false},
		{"词云图绘制成功\nextra\n", "词云图绘制成功", false},
		{"x词云图绘制成功", "词云图绘制成功", false},
		{"log\n", "", false},
	} {
		if got := leadingLinesOnly(tc.actual, tc.expected); got != tc.want {
			t.Errorf("leadingLinesOnly(%q, %q) = %v", tc.actual, tc.expected, got)
		}
	}
}

// noisySelfTestAdapter's runner prints a library log in front of the output,
// or after it when trailing is set.
type noisySelfTestAdapter struct {
	fakeAdapter
	trailing bool
}

func (a *noisySelfTestAdapter) SelfTest(_ context.Context, _ Target, file problem.SolutionFile, _, expected string) (SelfTest, error) {
	a.selfTests++
	out := "wrong\n"
	if file.Content == "good" {
		out = jiebaLog + expected + "\r\n"
		if a.trailing {
			out = expected + "\r\n" + strings.TrimSpace(jiebaLog) + "\r\n"
		}
	}
	return SelfTest{Completed: true, ProcessFinishedSeen: true, Output: out}, nil
}

func noisySnapshot() problem.Snapshot {
	snap := testSnapshot()
	snap.EditMode = "single_file"
	return snap
}

func TestSelftestWithALibraryLogInFrontGoesToFormalEvaluation(t *testing.T) {
	adapter := &noisySelfTestAdapter{fakeAdapter: fakeAdapter{snap: noisySnapshot()}}
	provider := &fakeProvider{candidates: []answer.Candidate{candidate("good")}}
	req := recoveryRequest()
	req.Mode = ModeSubmit
	r, err := New(&fakeStore{}, provider, Config{}).Run(context.Background(), adapter, req)
	if err != nil || r.Status != "passed" || provider.calls != 1 || adapter.submits != 1 || !strings.Contains(strings.Join(r.Diagnostics, "|"), "交给正式评测判定") {
		t.Fatalf("result=%+v err=%v model=%d submits=%d", r, err, provider.calls, adapter.submits)
	}
}

func TestExtraLinesAfterTheOutputStillFailTheSelftest(t *testing.T) {
	adapter := &noisySelfTestAdapter{fakeAdapter: fakeAdapter{snap: noisySnapshot()}, trailing: true}
	provider := &fakeProvider{candidates: []answer.Candidate{candidate("good")}}
	req := recoveryRequest()
	req.MaxModelCalls = 1
	r, _ := New(&fakeStore{}, provider, Config{}).Run(context.Background(), adapter, req)
	if r.Status == "selftest_passed" || adapter.submits != 0 {
		t.Fatalf("trailing output was accepted: %+v", r)
	}
}

// The answer the jieba level rejected on its log lines is tried again before
// the model, and passes formally.
func TestAnswerRejectedOnlyForLeadingLogLinesIsRetried(t *testing.T) {
	bank, err := questionbank.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer bank.Close()
	ref, err := bank.UpsertSnapshot(context.Background(), noisySnapshot())
	if err != nil {
		t.Fatal(err)
	}
	stored, err := bank.AddCandidate(context.Background(), ref, questionbank.CandidateInput{Source: "model", Files: candidate("good").Files})
	if err != nil {
		t.Fatal(err)
	}
	feedback := feedbackString([]answer.Feedback{{Kind: "selftest", Message: jiebaLog + "ok\r\n", ExpectedStdout: "ok", ActualStdout: jiebaLog + "ok\r\n"}})
	if err := bank.RecordValidation(context.Background(), questionbank.Validation{CandidateID: stored.ID, Kind: "selftest", Result: "failed", FilesHash: stored.FilesHash, SavedFilesHash: stored.FilesHash, RevisionHash: stored.RevisionHash, ContractHash: stored.ContractHash, Feedback: feedback, Terminal: true, Complete: true, TestCount: 1}); err != nil {
		t.Fatal(err)
	}
	adapter := &noisySelfTestAdapter{fakeAdapter: fakeAdapter{snap: noisySnapshot()}}
	req := recoveryRequest()
	req.Mode = ModeSubmit
	r, err := New(bank, nil, Config{}).Run(context.Background(), adapter, req)
	if err != nil || r.Status != "passed" || r.ModelCalls != 0 || r.CandidateID != stored.ID {
		t.Fatalf("rejected-on-log answer was not retried: result=%+v err=%v", r, err)
	}
}
