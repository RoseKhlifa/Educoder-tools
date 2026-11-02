package solver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

type emptyObservationAdapter struct {
	fakeAdapter
	outputs []string
}

func (a *emptyObservationAdapter) SelfTest(_ context.Context, _ Target, f problem.SolutionFile, _, expected string) (SelfTest, error) {
	i := min(a.selfTests, len(a.outputs)-1)
	a.selfTests++
	hash, _ := problem.HashFiles([]problem.SolutionFile{f})
	return SelfTest{Completed: true, Output: a.outputs[i], Matches: a.outputs[i] == expected, SavedFilesHash: hash, ProcessFinishedSeen: true, OutputBytes: int64(len(a.outputs[i]))}, nil
}

func observationFixture() (problem.Snapshot, questionbank.Candidate) {
	snap := testSnapshot()
	snap.EditMode = "single_file"
	files := candidate("good").Files
	hash, _ := problem.HashFiles(files)
	ref := questionbank.Ref{RevisionHash: "rev", ContractHash: "contract"}
	c := questionbank.Candidate{ID: 9, Ref: ref, FilesHash: hash, CandidateInput: questionbank.CandidateInput{Files: files, Source: "verified"}, Status: "upstream_verified"}
	base := questionbank.Validation{CandidateID: c.ID, FilesHash: hash, SavedFilesHash: hash, RevisionHash: ref.RevisionHash, ContractHash: ref.ContractHash, Result: "passed", Complete: true, Terminal: true, TestCount: 1, PassedCount: 1}
	sample := base
	sample.Kind = "selftest"
	sample.Feedback = feedbackString([]answer.Feedback{{Kind: "selftest_passed", Stdin: "1", ExpectedStdout: "ok", ActualStdout: "ok\r\n"}})
	formal := base
	formal.Kind, formal.SubmittedFilesHash = "formal", hash
	c.Validations = []questionbank.Validation{sample, formal}
	return snap, c
}

func TestBoundEmptySelfTestObservationRecovery(t *testing.T) {
	for _, tc := range []struct {
		name        string
		outputs     []string
		status      string
		wantErr     bool
		wantSubmits int
		wantModels  int
	}{
		{"recovers", []string{"", "ok"}, "passed", false, 1, 0},
		{"persistent_conflict", []string{"", ""}, "uncertain", true, 0, 0},
		{"real_mismatch_after_retry", []string{"", "wrong", "ok"}, "passed", false, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snap, c := observationFixture()
			store := &fakeStore{candidates: []questionbank.Candidate{c}}
			provider := &fakeProvider{candidates: []answer.Candidate{candidate("new-good")}}
			adapter := &emptyObservationAdapter{fakeAdapter: fakeAdapter{snap: snap}, outputs: tc.outputs}
			req := formalRuntimeRecoveryRequest()
			req.MaxModelCalls = 1
			r, err := New(store, provider, Config{}).Run(context.Background(), adapter, req)
			if (err != nil) != tc.wantErr || r.Status != tc.status || adapter.selfTests != 2+tc.wantModels || adapter.submits != tc.wantSubmits || provider.calls != tc.wantModels || r.CostMicroUSD != int64(tc.wantModels) {
				t.Fatalf("result=%+v err=%v selftests=%d submits=%d model=%d", r, err, adapter.selfTests, adapter.submits, provider.calls)
			}
			if len(store.validations) < 2 || store.validations[0].Result != "unknown" || store.validations[0].Terminal {
				t.Fatalf("empty observation became rejection: %+v", store.validations)
			}
			var state runState
			if e := json.Unmarshal(store.runs[r.RunID].Data, &state); e != nil {
				t.Fatal(e)
			}
			if tc.wantModels == 0 && (len(state.RejectedFilesHashes) != 0 || len(state.RepairHistory) != 0) {
				t.Fatalf("uncertain observation entered model repair: %+v", state)
			}
		})
	}
}

func TestEmptyObservationRequiresEveryProofBinding(t *testing.T) {
	for _, name := range []string{"terminal", "graphic", "missing_marker", "incomplete", "matches", "nonempty", "observed_bytes", "empty_expected", "compile_status", "exit_status", "wrong_saved_hash", "protocol_error", "wrong_ref", "wrong_files_hash", "wrong_formal_hash", "partial_formal", "workspace_pass", "wrong_sample_input", "missing_sample_actual", "wrong_candidate_id"} {
		t.Run(name, func(t *testing.T) {
			snap, c := observationFixture()
			ref, test := c.Ref, snap.PublicTests[0]
			st := SelfTest{Completed: true, ProcessFinishedSeen: true, SavedFilesHash: c.FilesHash}
			var err error
			yes, zero := true, 0
			switch name {
			case "terminal":
				snap.EditMode = "terminal"
			case "graphic":
				snap.EditMode = "graphic"
			case "missing_marker":
				st.ProcessFinishedSeen = false
			case "incomplete":
				st.Completed = false
			case "matches":
				st.Matches = true
			case "nonempty":
				st.Output = "wrong"
			case "observed_bytes":
				st.OutputBytes = 1
			case "empty_expected":
				test.ExpectedStdout = ""
			case "compile_status":
				st.CompileOK = &yes
			case "exit_status":
				st.ExitCode = &zero
			case "wrong_saved_hash":
				st.SavedFilesHash = "other"
			case "protocol_error":
				err = errors.New("stream failed")
			case "wrong_ref":
				ref.ContractHash = "other"
			case "wrong_files_hash":
				c.FilesHash = "other"
				st.SavedFilesHash = "other"
			case "wrong_formal_hash":
				c.Validations[1].SubmittedFilesHash = "other"
			case "partial_formal":
				c.Validations[1].TestCount = 2
			case "workspace_pass":
				c.Validations[1].Kind = "formal_workspace"
			case "wrong_sample_input":
				test.Stdin = "different"
			case "missing_sample_actual":
				c.Validations[0].Feedback = `[{"kind":"selftest_passed","stdin":"1","expected_stdout":"ok"}]`
			case "wrong_candidate_id":
				c.Validations[1].CandidateID++
			}
			if conflictingEmptySelfTest(snap, c, ref, test, st, err) {
				t.Fatal("unbound or effectful observation allowed replay")
			}
		})
	}
}

func TestLegacyEmptyObservationRetestsWithoutChangingHistory(t *testing.T) {
	snap, c := observationFixture()
	c.Status = "rejected"
	v := c.Validations[0]
	v.Result, v.PassedCount = "failed", 0
	v.Feedback = `[{"kind":"selftest","stdin":"1","expected_stdout":"ok"}]`
	c.Validations = append(c.Validations, v)
	if !legacyEmptyOutputRejection(snap, c, c.Ref) {
		t.Fatal("expected narrow legacy re-test")
	}
	store := &fakeStore{candidates: []questionbank.Candidate{c}}
	adapter := &emptyObservationAdapter{fakeAdapter: fakeAdapter{snap: snap}, outputs: []string{"ok"}}
	r, err := New(store, &fakeProvider{}, Config{}).Run(context.Background(), adapter, formalRuntimeRecoveryRequest())
	if err != nil || r.Status != "passed" || r.ModelCalls != 0 || adapter.selfTests != 1 || adapter.submits != 1 {
		t.Fatalf("result=%+v err=%v", r, err)
	}
	if store.candidates[0].Validations[2] != v {
		t.Fatal("historical failed row was rewritten")
	}
	for _, alter := range []func(*questionbank.Candidate){
		func(c *questionbank.Candidate) {
			c.Validations[2].Feedback = `[{"kind":"selftest","stdin":"1","expected_stdout":"ok","actual_stdout":"wrong"}]`
		},
		func(c *questionbank.Candidate) {
			c.Validations[2].Feedback = `[{"kind":"selftest","message":"readback mismatch","stdin":"1","expected_stdout":"ok"}]`
		},
		func(c *questionbank.Candidate) { c.Validations[2].Kind = "formal" },
		func(c *questionbank.Candidate) { c.Validations[1].SubmittedFilesHash = "other" },
	} {
		copy := c
		copy.Validations = append([]questionbank.Validation(nil), c.Validations...)
		alter(&copy)
		if legacyEmptyOutputRejection(snap, copy, copy.Ref) {
			t.Fatal("real failure or missing proof reclassified as observation")
		}
	}
}

type observationCheckpointFailureStore struct{ fakeStore }

func (s *observationCheckpointFailureStore) PutRun(ctx context.Context, r questionbank.RunRecord) error {
	if r.Status == "running" && len(s.validations) == 1 && s.validations[0].Result == "unknown" {
		return errors.New("checkpoint storage unavailable")
	}
	return s.fakeStore.PutRun(ctx, r)
}

func TestEmptyObservationCheckpointFailurePreventsReplay(t *testing.T) {
	snap, c := observationFixture()
	store := &observationCheckpointFailureStore{fakeStore: fakeStore{candidates: []questionbank.Candidate{c}}}
	adapter := &emptyObservationAdapter{fakeAdapter: fakeAdapter{snap: snap}, outputs: []string{"", "ok"}}
	r, err := New(store, &fakeProvider{}, Config{}).Run(context.Background(), adapter, formalRuntimeRecoveryRequest())
	if err == nil || r.Status != "checkpoint_persistence_failed" || adapter.selfTests != 1 || adapter.submits != 0 || r.ModelCalls != 0 {
		t.Fatalf("replayed without checkpoint: result=%+v err=%v tests=%d submits=%d", r, err, adapter.selfTests, adapter.submits)
	}
	if len(store.validations) != 1 || store.validations[0].Result != "unknown" {
		t.Fatalf("evidence=%+v", store.validations)
	}
}
