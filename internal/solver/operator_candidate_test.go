package solver

import (
	"context"
	"path/filepath"
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

func operatorFixture(t *testing.T, source, content string) (*questionbank.Store, questionbank.Candidate) {
	t.Helper()
	bank, err := questionbank.Open(filepath.Join(t.TempDir(), "operator.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bank.Close() })
	snap, err := problem.Prepare(testSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	ref, err := bank.UpsertSnapshot(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	c, err := bank.AddCandidate(context.Background(), ref, questionbank.CandidateInput{Source: source, Files: candidate(content).Files})
	if err != nil {
		t.Fatal(err)
	}
	return bank, c
}

func operatorValidation(c questionbank.Candidate, kind, result string) questionbank.Validation {
	return questionbank.Validation{CandidateID: c.ID, Kind: kind, Result: result, FilesHash: c.FilesHash, SavedFilesHash: c.FilesHash, SubmittedFilesHash: c.FilesHash, RevisionHash: c.RevisionHash, ContractHash: c.ContractHash, Terminal: true, Complete: true, TestCount: 1, PassedCount: 1}
}

func TestOperatorProposalRequiresFreshSelfTestAndFormalEvidence(t *testing.T) {
	for _, priorSelfTest := range []bool{false, true} {
		name := "unvalidated"
		if priorSelfTest {
			name = "selftest passed"
		}
		t.Run(name, func(t *testing.T) {
			bank, c := operatorFixture(t, "manual_public_statement", "good")
			if priorSelfTest {
				if err := bank.RecordValidation(context.Background(), operatorValidation(c, "selftest", "passed")); err != nil {
					t.Fatal(err)
				}
			}
			adapter := &importedAdapter{fakeAdapter: fakeAdapter{snap: testSnapshot()}}
			req := recoveryRequest()
			req.Mode = ModeSubmit
			r, err := New(bank, nil, Config{}).Run(context.Background(), adapter, req)
			if err != nil || r.Status != "passed" || r.ModelCalls != 0 || r.CostMicroUSD != 0 || r.ReservedMicroUSD != 0 || r.CandidateID != c.ID || adapter.selfTests != 1 || adapter.submits != 1 {
				t.Fatalf("proposal did not use fresh evaluation gates: result=%+v err=%v tests=%d submits=%d", r, err, adapter.selfTests, adapter.submits)
			}
			stored, err := bank.Candidate(context.Background(), c.ID)
			want := 2
			if priorSelfTest {
				want++
			}
			if err != nil || stored.Status != "upstream_verified" || len(stored.Validations) != want {
				t.Fatalf("proposal trust did not come from new evidence: %+v err=%v", stored, err)
			}
			latest := stored.Validations[len(stored.Validations)-1]
			if latest.Kind != "formal" || latest.Result != "passed" || latest.FilesHash != c.FilesHash || latest.SavedFilesHash != c.FilesHash || latest.SubmittedFilesHash != c.FilesHash || latest.TestCount != latest.PassedCount {
				t.Fatalf("formal evidence was not bound to evaluated source: %+v", latest)
			}
		})
	}
}

func TestFailedOperatorProposalKeepsRejectionAndRepairsNormally(t *testing.T) {
	bank, c := operatorFixture(t, "manual_public_statement", "bad")
	provider := &scriptedProvider{estimateCost: 1, generate: func(_ context.Context, req answer.Request, _ int) (answer.Result, error) {
		if req.Previous == nil || req.Previous.Files[0].Content != "bad" || len(req.Feedback) == 0 || req.Feedback[0].Kind != "selftest" {
			t.Fatalf("proposal failure did not enter normal repair: %+v", req)
		}
		return solved("good", 1), nil
	}}
	adapter := &importedAdapter{fakeAdapter: fakeAdapter{snap: testSnapshot()}}
	req := recoveryRequest()
	req.Mode = ModeSubmit
	r, err := New(bank, provider, Config{}).Run(context.Background(), adapter, req)
	if err != nil || r.Status != "passed" || r.ModelCalls != 1 || r.CostMicroUSD != 1 || adapter.selfTests != 2 || adapter.submits != 1 || r.CandidateID == c.ID {
		t.Fatalf("failed proposal did not repair normally: result=%+v err=%v tests=%d submits=%d", r, err, adapter.selfTests, adapter.submits)
	}
	stored, err := bank.Candidate(context.Background(), c.ID)
	if err != nil || stored.Status != "rejected" || len(stored.Validations) != 1 || stored.Validations[0].Result != "failed" {
		t.Fatalf("failed proposal was promoted or replayed: %+v err=%v", stored, err)
	}
}

func TestUnmarkedAndFailedOperatorCandidatesAreNotSelected(t *testing.T) {
	for _, tc := range []struct {
		name, source, validation string
	}{
		{"model", "model", ""},
		{"partial model", "model_error", ""},
		{"unmarked", "", ""},
		{"similar label", "manual_public_statement ", ""},
		{"rejected", "manual_public_statement", "failed"},
		{"unknown", "manual_public_statement", "unknown"},
		{"provider error", "manual_public_statement", "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bank, c := operatorFixture(t, tc.source, "bad")
			if tc.validation != "" {
				v := operatorValidation(c, "selftest", tc.validation)
				v.PassedCount = 0
				if err := bank.RecordValidation(context.Background(), v); err != nil {
					t.Fatal(err)
				}
			}
			provider := &fakeProvider{candidates: []answer.Candidate{candidate("good")}}
			adapter := &importedAdapter{fakeAdapter: fakeAdapter{snap: testSnapshot()}}
			r, err := New(bank, provider, Config{}).Run(context.Background(), adapter, recoveryRequest())
			if err != nil || r.Status != "selftest_passed" || r.ModelCalls != 1 || adapter.selfTests != 1 || adapter.submits != 0 || r.CandidateID == c.ID {
				t.Fatalf("ineligible source was selected or replayed: result=%+v err=%v tests=%d", r, err, adapter.selfTests)
			}
			stored, err := bank.Candidate(context.Background(), c.ID)
			want := 0
			if tc.validation != "" {
				want = 1
			}
			if err != nil || len(stored.Validations) != want {
				t.Fatalf("ineligible candidate gained new execution evidence: %+v err=%v", stored, err)
			}
		})
	}
}

func TestOperatorProposalPrecedesUnverifiedExternalAnswer(t *testing.T) {
	bank := importedFixture(t, "bad")
	snap, _ := problem.Prepare(testSnapshot())
	ref, err := bank.UpsertSnapshot(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	c, err := bank.AddCandidate(context.Background(), ref, questionbank.CandidateInput{Source: "manual_public_statement", Files: candidate("good").Files})
	if err != nil {
		t.Fatal(err)
	}
	adapter := &importedAdapter{fakeAdapter: fakeAdapter{snap: testSnapshot()}, identity: questionbank.ExternalIdentity{Experiment: "Imported experiment", Title: "stack"}}
	req := recoveryRequest()
	req.Mode = ModeSubmit
	r, err := New(bank, nil, Config{}).Run(context.Background(), adapter, req)
	if err != nil || r.Status != "passed" || r.CandidateID != c.ID || r.ModelCalls != 0 || adapter.selfTests != 1 || adapter.submits != 1 {
		t.Fatalf("external proposal displaced explicit operator proposal: result=%+v err=%v", r, err)
	}
}

func TestOperatorProposalSavedHashMismatchStaysUncertain(t *testing.T) {
	bank, c := operatorFixture(t, "manual_public_statement", "good")
	adapter := &repairHistoryAdapter{fakeAdapter: fakeAdapter{snap: testSnapshot()}, unknown: "changed_source"}
	req := recoveryRequest()
	req.Mode = ModeSubmit
	r, err := New(bank, nil, Config{}).Run(context.Background(), adapter, req)
	if err == nil || r.Status != "uncertain" || r.ModelCalls != 0 || adapter.selfTests != 1 || adapter.submits != 0 {
		t.Fatalf("proposal bypassed readback mismatch: result=%+v err=%v", r, err)
	}
	stored, err := bank.Candidate(context.Background(), c.ID)
	if err != nil || stored.Status == "upstream_verified" || len(stored.Validations) != 1 || stored.Validations[0].Result != "unknown" || stored.Validations[0].SavedFilesHash != "different" {
		t.Fatalf("unknown source was promoted or evidence overwritten: %+v err=%v", stored, err)
	}
}

func TestVerifiedCandidateKeepsPriorityOverOperatorProposal(t *testing.T) {
	bank, manual := operatorFixture(t, "manual_public_statement", "bad")
	verified, err := bank.AddCandidate(context.Background(), manual.Ref, questionbank.CandidateInput{Source: "model", Files: candidate("good").Files})
	if err != nil {
		t.Fatal(err)
	}
	if err := bank.RecordValidation(context.Background(), operatorValidation(verified, "formal", "passed")); err != nil {
		t.Fatal(err)
	}
	adapter := &importedAdapter{fakeAdapter: fakeAdapter{snap: testSnapshot()}}
	r, err := New(bank, nil, Config{}).Run(context.Background(), adapter, recoveryRequest())
	if err != nil || r.Status != "selftest_passed" || r.CandidateID != verified.ID || r.ModelCalls != 0 || adapter.selfTests != 1 {
		t.Fatalf("operator displaced verified candidate: result=%+v err=%v", r, err)
	}
	manual, err = bank.Candidate(context.Background(), manual.ID)
	if err != nil || len(manual.Validations) != 0 {
		t.Fatalf("unused operator proposal was evaluated: %+v err=%v", manual, err)
	}
}

func TestOperatorProposalDoesNotReuseTerminalWorkspace(t *testing.T) {
	files := candidate("good").Files
	hash, _ := problem.HashFiles(files)
	store := &fakeStore{candidates: []questionbank.Candidate{{ID: 1, CandidateInput: questionbank.CandidateInput{Source: "manual_public_statement", Files: files}, FilesHash: hash, Status: "candidate"}}}
	snap := testSnapshot()
	snap.EditMode = "terminal"
	adapter := &fakeAdapter{snap: snap}
	r, err := New(store, nil, Config{}).Run(context.Background(), adapter, recoveryRequest())
	if err == nil || r == nil || r.Status != "needs_provider" || r.ModelCalls != 0 || adapter.selfTests != 0 || adapter.submits != 0 {
		t.Fatalf("operator proposal reused terminal workspace: result=%+v err=%v tests=%d submits=%d", r, err, adapter.selfTests, adapter.submits)
	}
}
