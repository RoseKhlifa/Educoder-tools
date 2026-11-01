package solver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

type repairHistoryAdapter struct {
	fakeAdapter
	seen           []string
	compileFailure bool
	unknown        string
}

func (a *repairHistoryAdapter) SelfTest(_ context.Context, _ Target, file problem.SolutionFile, input, expected string) (SelfTest, error) {
	a.selfTests++
	a.seen = append(a.seen, file.Content+":"+input)
	hash, _ := problem.HashFiles([]problem.SolutionFile{file})
	if a.unknown == "network" {
		return SelfTest{}, errors.New("runtime disconnected")
	}
	if a.unknown == "incomplete" {
		return SelfTest{Output: "waiting", SavedFilesHash: hash}, nil
	}
	if a.unknown == "changed_source" {
		return SelfTest{Completed: true, Output: expected, Matches: true, SavedFilesHash: "different"}, nil
	}
	compiled := !a.compileFailure
	if !compiled {
		return SelfTest{Completed: true, CompileOK: &compiled, Output: "incompatible helper parameter types", SavedFilesHash: hash}, nil
	}
	matched := file.Content == "C" || (file.Content == "A" && input == "2") || (file.Content == "B" && input == "1")
	actual := expected
	if !matched {
		actual = "wrong-" + input
	}
	return SelfTest{Completed: true, Matches: matched, CompileOK: &compiled, Output: actual, SavedFilesHash: hash}, nil
}

func twoSampleSnapshot() problem.Snapshot {
	snap := testSnapshot()
	snap.PublicTests = []problem.PublicTest{{Stdin: "1", ExpectedStdout: "one"}, {Stdin: "2", ExpectedStdout: "two"}}
	return snap
}

func TestRepairHistoryBlocksRepeatedFailedSourceBeforeAnyNewSelfTest(t *testing.T) {
	store := &fakeStore{}
	provider := &scriptedProvider{estimateCost: 1, generate: func(_ context.Context, req answer.Request, n int) (answer.Result, error) {
		if n == 1 {
			return solved("A", 1), nil
		}
		if len(req.RepairHistory) != 1 || req.RepairHistory[0].Candidate == nil || req.RepairHistory[0].Candidate.Files[0].Content != "A" || len(req.RepairHistory[0].Feedback) != 2 {
			t.Fatalf("source-bound sample history missing: %+v", req.RepairHistory)
		}
		if n == 2 {
			return solved("A", 1), nil
		}
		return solved("C", 1), nil
	}}
	adapter := &repairHistoryAdapter{fakeAdapter: fakeAdapter{snap: twoSampleSnapshot()}}
	req := recoveryRequest()
	req.Mode, req.MaxModelCalls, req.MaxEvaluations = ModeSubmit, 3, 3
	r, err := New(store, provider, Config{}).Run(context.Background(), adapter, req)
	if err != nil || r.Status != "passed" || adapter.selfTests != 4 || adapter.submits != 1 || r.ModelCalls != 3 || r.CostMicroUSD != 3 || len(store.candidates) != 2 {
		t.Fatalf("result=%+v err=%v tested=%v candidates=%d", r, err, adapter.seen, len(store.candidates))
	}
	if strings.Join(adapter.seen, ",") != "A:1,A:2,C:1,C:2" {
		t.Fatalf("failed source was saved/tested again: %v", adapter.seen)
	}
}

func TestRepairHistoryKeepsDifferentSampleFailuresAcrossCandidates(t *testing.T) {
	store := &fakeStore{}
	provider := &scriptedProvider{estimateCost: 1, generate: func(_ context.Context, req answer.Request, n int) (answer.Result, error) {
		if n == 3 {
			if len(req.RepairHistory) != 2 || len(req.Feedback) != 2 || req.Feedback[0].Stdin != "1" || req.Feedback[1].Stdin != "2" {
				t.Fatalf("repair forgot a prior sample failure: feedback=%+v history=%+v", req.Feedback, req.RepairHistory)
			}
			for i, content := range []string{"A", "B"} {
				entry := req.RepairHistory[i]
				if entry.Candidate == nil || entry.Candidate.Files[0].Content != content || len(entry.Feedback) != 2 || entry.Feedback[i].Kind != "selftest" {
					t.Fatalf("feedback no longer bound to exact source: %+v", entry)
				}
			}
		}
		return solved([]string{"A", "B", "C"}[n-1], 1), nil
	}}
	adapter := &repairHistoryAdapter{fakeAdapter: fakeAdapter{snap: twoSampleSnapshot()}}
	r, err := New(store, provider, Config{}).Run(context.Background(), adapter, recoveryRequest())
	if err != nil || r.Status != "selftest_passed" || adapter.selfTests != 6 {
		t.Fatalf("result=%+v err=%v tested=%v", r, err, adapter.seen)
	}
}

func TestContinuationRetainsRejectionHistoryEvenWithoutBankStatus(t *testing.T) {
	store := &fakeStore{}
	provider := &scriptedProvider{estimateCost: 1, generate: func(_ context.Context, req answer.Request, n int) (answer.Result, error) {
		if n > 1 && (len(req.RepairHistory) != 1 || req.RepairHistory[0].Candidate == nil || req.RepairHistory[0].Candidate.Files[0].Content != "A") {
			t.Fatalf("continuation lost durable source history: %+v", req.RepairHistory)
		}
		return solved([]string{"A", "A", "C"}[n-1], 2), nil
	}}
	adapter := &repairHistoryAdapter{fakeAdapter: fakeAdapter{snap: twoSampleSnapshot()}}
	req := recoveryRequest()
	req.MaxModelCalls, req.MaxEvaluations = 1, 1
	r, err := New(store, provider, Config{}).Run(context.Background(), adapter, req)
	if err != nil || r.Status != "selftest_failed" {
		t.Fatalf("first=%+v err=%v", r, err)
	}
	var state runState
	if err := json.Unmarshal(store.runs[r.RunID].Data, &state); err != nil || len(state.RejectedFilesHashes) != 1 || len(state.RepairHistory) != 1 {
		t.Fatalf("durable history=%+v err=%v", state, err)
	}
	// The checkpoint itself must protect against replays, without relying on
	// a fresh Lookup happening to reproduce the candidate's rejected status.
	store.candidates[0].Status, store.candidates[0].Validations = "candidate", nil
	continued, err := New(store, provider, Config{}).ContinueRun(context.Background(), adapter, recoveryRequest(), r.RunID, Continuation{AdditionalModelCalls: 2, AdditionalEvaluations: 2})
	if err != nil || continued.Status != "selftest_passed" || continued.ModelCalls != 3 || continued.CostMicroUSD != 6 || continued.ReservedMicroUSD != 0 || adapter.selfTests != 4 || len(store.usages) != 3 {
		t.Fatalf("continued=%+v err=%v tested=%v usage=%d", continued, err, adapter.seen, len(store.usages))
	}
}

func TestCompileFailureIsRepairedWithoutRecompilingEverySample(t *testing.T) {
	store := &fakeStore{}
	provider := &fakeProvider{candidates: []answer.Candidate{candidate("A")}}
	adapter := &repairHistoryAdapter{fakeAdapter: fakeAdapter{snap: twoSampleSnapshot()}, compileFailure: true}
	req := recoveryRequest()
	req.MaxModelCalls, req.MaxEvaluations = 1, 1
	r, err := New(store, provider, Config{}).Run(context.Background(), adapter, req)
	if err != nil || r.Status != "selftest_failed" || adapter.selfTests != 1 {
		t.Fatalf("result=%+v err=%v selftests=%d", r, err, adapter.selfTests)
	}
}

func TestUnknownSelfTestNeverBecomesConfirmedFailureOrAutomaticReplay(t *testing.T) {
	for _, unknown := range []string{"network", "incomplete", "changed_source"} {
		t.Run(unknown, func(t *testing.T) {
			store := &fakeStore{}
			provider := &fakeProvider{candidates: []answer.Candidate{candidate("C"), candidate("C")}}
			adapter := &repairHistoryAdapter{fakeAdapter: fakeAdapter{snap: twoSampleSnapshot()}, unknown: unknown}
			req := recoveryRequest()
			req.Mode = ModeSubmit
			svc := New(store, provider, Config{})
			r, err := svc.Run(context.Background(), adapter, req)
			if err == nil || r.Status != "uncertain" || provider.calls != 1 || adapter.selfTests != 1 || adapter.submits != 0 {
				t.Fatalf("result=%+v err=%v tests=%d submits=%d", r, err, adapter.selfTests, adapter.submits)
			}
			var state runState
			json.Unmarshal(store.runs[r.RunID].Data, &state)
			if len(state.RejectedFilesHashes) != 0 || len(state.RepairHistory) != 0 || len(store.validations) != 1 || store.validations[0].Result != "unknown" {
				t.Fatalf("unknown host result became wrong-code evidence: state=%+v validation=%+v", state, store.validations)
			}
			if _, err := svc.ContinueRun(context.Background(), adapter, req, r.RunID, Continuation{AdditionalModelCalls: 2, AdditionalEvaluations: 2}); err == nil || adapter.selfTests != 1 || adapter.submits != 0 {
				t.Fatal("uncertain mutation was automatically replayed")
			}
		})
	}
}

type repairFormalAdapter struct {
	repairHistoryAdapter
	unknownFormal bool
}

func (a *repairFormalAdapter) Submit(_ context.Context, _ Target, file problem.SolutionFile) (Evaluation, error) {
	a.submits++
	if a.unknownFormal {
		return Evaluation{Status: "processing"}, errors.New("formal result disconnected")
	}
	passed := file.Content == "C"
	return Evaluation{Completed: true, Passed: passed, TestCount: 2, PassedCount: boolInt(passed) * 2, Feedback: "formal sample assertion failed"}, nil
}

func TestConfirmedFormalFailureAlsoBlocksIdenticalResubmission(t *testing.T) {
	store := &fakeStore{}
	provider := &fakeProvider{candidates: []answer.Candidate{candidate("A"), candidate("A"), candidate("C")}}
	snap := testSnapshot()
	snap.PublicTests = nil
	adapter := &repairFormalAdapter{repairHistoryAdapter: repairHistoryAdapter{fakeAdapter: fakeAdapter{snap: snap}}}
	req := recoveryRequest()
	req.Mode, req.MaxModelCalls, req.MaxEvaluations = ModeSubmit, 3, 3
	r, err := New(store, provider, Config{}).Run(context.Background(), adapter, req)
	if err != nil || r.Status != "passed" || adapter.submits != 2 || provider.calls != 3 || len(store.validations) != 2 {
		t.Fatalf("same formal failure was submitted again: result=%+v err=%v submits=%d", r, err, adapter.submits)
	}
}

func TestUnknownFormalSubmissionCannotEnterRepairOrReplay(t *testing.T) {
	store := &fakeStore{}
	provider := &fakeProvider{candidates: []answer.Candidate{candidate("C"), candidate("C")}}
	adapter := &repairFormalAdapter{repairHistoryAdapter: repairHistoryAdapter{fakeAdapter: fakeAdapter{snap: twoSampleSnapshot()}}, unknownFormal: true}
	req := recoveryRequest()
	req.Mode = ModeSubmit
	svc := New(store, provider, Config{})
	r, err := svc.Run(context.Background(), adapter, req)
	if err == nil || r.Status != "uncertain" || adapter.submits != 1 || provider.calls != 1 {
		t.Fatalf("unknown formal submission retried: result=%+v err=%v submits=%d", r, err, adapter.submits)
	}
	var state runState
	json.Unmarshal(store.runs[r.RunID].Data, &state)
	if len(state.RejectedFilesHashes) != 0 || len(state.RepairHistory) != 0 || store.validations[len(store.validations)-1].Result != "unknown" {
		t.Fatalf("unknown formal verdict became confirmed code failure: %+v", state)
	}
	if _, err := New(store, provider, Config{}).ContinueRun(context.Background(), adapter, req, r.RunID, Continuation{AdditionalModelCalls: 2, AdditionalEvaluations: 2}); err == nil || adapter.submits != 1 {
		t.Fatal("unknown formal submission became a repair continuation")
	}
}

func TestRepairHistoryBoundsSourcesAndFeedbackWithoutForgettingFailedHashes(t *testing.T) {
	var state runState
	for i := 0; i < maxRepairHistory+2; i++ {
		c := candidate(strings.Repeat(string(rune('a'+i)), maxRepairSourceBytes/2))
		state.rememberRejection(c, []answer.Feedback{{Kind: "selftest", Message: strings.Repeat("x", maxRepairFeedbackTextBytes*2)}})
	}
	sourceBytes := 0
	for _, entry := range state.RepairHistory {
		if entry.Candidate != nil {
			for _, file := range entry.Candidate.Files {
				sourceBytes += len(file.Path) + len(file.Content)
			}
		}
		if len(entry.Feedback[0].Message) > maxRepairFeedbackTextBytes+len("\n[feedback truncated]") {
			t.Fatal("feedback text was not bounded")
		}
	}
	if len(state.RepairHistory) != maxRepairHistory || len(state.RejectedFilesHashes) != maxRepairHistory+2 || sourceBytes > maxRepairSourceBytes || state.RepairHistory[len(state.RepairHistory)-1].Candidate == nil {
		t.Fatalf("history unbounded or latest source lost: sources=%d history=%d hashes=%d", sourceBytes, len(state.RepairHistory), len(state.RejectedFilesHashes))
	}
}

func TestFeedbackWindowRetainsFailureWhenLaterSamplesPass(t *testing.T) {
	feedback := []answer.Feedback{{Kind: "selftest", Stdin: "first", ExpectedStdout: "one", ActualStdout: "wrong"}}
	for i := 0; i < maxRepairFeedback+5; i++ {
		feedback = boundedFeedback(feedback, answer.Feedback{Kind: "selftest_passed", Stdin: strings.Repeat("x", i+1), ExpectedStdout: "ok", ActualStdout: "ok"})
	}
	if len(feedback) != maxRepairFeedback || feedback[0].Kind != "selftest" || feedback[0].Stdin != "first" {
		t.Fatalf("successful samples evicted the failing case: %+v", feedback)
	}
}

func TestLongRepairEvidenceFitsSQLiteCheckpointAndKeepsActualFailure(t *testing.T) {
	db, err := questionbank.Open(filepath.Join(t.TempDir(), "bank.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	longText := strings.Repeat("<&\"\n", 4096)
	state := runState{Owner: ownerKey("synthetic-account"), Target: Target{"course", 1, "game"}, Result: Result{RunID: "long-repair-run", Status: "selftest_failed"}}
	for round := 0; round < maxRepairHistory+2; round++ {
		c := candidate(fmt.Sprintf("// source %d\n", round) + strings.Repeat("<&\"\n", 4096))
		var feedback []answer.Feedback
		for sample := 0; sample < maxRepairFeedback; sample++ {
			feedback = append(feedback, answer.Feedback{Kind: "selftest", Message: fmt.Sprintf("compiler failure %d/%d: ", round, sample) + longText, Stdin: fmt.Sprintf("sample %d: ", sample) + longText, ExpectedStdout: longText, ActualStdout: "failed: " + longText})
		}
		state.rememberRejection(c, feedback)
		state.LastCandidate = &c
		state.Feedback = boundedFeedback(state.Feedback, feedback...)
	}
	// The sole failure must survive even if a large number of later samples
	// pass. Both feedback and stored output deliberately expand when encoded.
	state.Feedback = []answer.Feedback{{Kind: "selftest", Message: "unique compiler failure: " + longText, Stdin: "failing input " + longText, ExpectedStdout: longText, ActualStdout: longText}}
	for i := 0; i < maxRepairFeedback+10; i++ {
		state.Feedback = boundedFeedback(state.Feedback, answer.Feedback{Kind: "selftest_passed", Stdin: fmt.Sprintf("passed %d: ", i) + longText, ExpectedStdout: longText, ActualStdout: longText})
		state.Result.SelfTests = append(state.Result.SelfTests, SelfTest{Completed: true, Matches: i > 0, Output: longText})
	}
	state = sanitizeRunState(state)
	historyJSON, _ := json.Marshal(state.RepairHistory)
	feedbackJSON, _ := json.Marshal(state.Feedback)
	resultEvidenceJSON, _ := json.Marshal(state.Result.SelfTests)
	raw, err := json.Marshal(state)
	if err != nil || len(historyJSON) > maxRepairHistoryBytes || len(feedbackJSON) > maxRepairFeedbackBytes || len(resultEvidenceJSON) > maxRepairResultEvidenceBytes || len(raw) >= 1<<20 {
		t.Fatalf("checkpoint exceeds encoded budget: history=%d feedback=%d outputs=%d total=%d err=%v", len(historyJSON), len(feedbackJSON), len(resultEvidenceJSON), len(raw), err)
	}
	if len(state.RepairHistory) != maxRepairHistory || len(state.RejectedFilesHashes) != maxRepairHistory+2 || !strings.Contains(state.Feedback[0].Message, "unique compiler failure") || state.Feedback[0].Kind != "selftest" || state.RepairHistory[len(state.RepairHistory)-1].Candidate == nil {
		t.Fatalf("size bound discarded failure/source identity: history=%d hashes=%d feedback=%+v", len(state.RepairHistory), len(state.RejectedFilesHashes), state.Feedback)
	}
	for _, entry := range state.RepairHistory {
		if entry.FilesHash == "" || len(entry.Feedback) == 0 || !strings.Contains(entry.Feedback[0].Message, "compiler failure") {
			t.Fatal("history budget detached a failure from its source hash")
		}
	}
	if err := db.PutRun(context.Background(), questionbank.RunRecord{ID: state.Result.RunID, Owner: state.Owner, Status: state.Result.Status, Data: raw, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("bounded checkpoint rejected by actual SQLite store: %v", err)
	}
	stored, err := db.GetRun(context.Background(), state.Result.RunID)
	if err != nil || len(stored.Data) != len(raw) {
		t.Fatalf("checkpoint failed round-trip: stored=%d err=%v", len(stored.Data), err)
	}
	t.Logf("encoded checkpoint: history=%d feedback=%d outputs=%d total=%d", len(historyJSON), len(feedbackJSON), len(resultEvidenceJSON), len(raw))
}
