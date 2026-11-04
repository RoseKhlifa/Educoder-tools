package solver

import (
	"context"
	"errors"
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

type hadoopLegacyAdapter struct {
	fakeAdapter
	marker     bool
	startErr   error
	startCalls int
	selfTests  int
}

func (a *hadoopLegacyAdapter) SelfTest(ctx context.Context, target Target, file problem.SolutionFile, input, expected string) (SelfTest, error) {
	a.selfTests++
	if a.marker {
		a.marker = false
		hash, _ := problem.HashFiles([]problem.SolutionFile{file})
		return SelfTest{Completed: true, ProcessFinishedSeen: true, SavedFilesHash: hash, Output: runtimeStartRequiredMessage}, nil
	}
	return a.fakeAdapter.SelfTest(ctx, target, file, input, expected)
}

func (a *hadoopLegacyAdapter) StartHadoop(context.Context, Target) error {
	a.startCalls++
	return a.startErr
}

func hadoopRejectedCandidate(t *testing.T, bank *questionbank.Store) questionbank.Candidate {
	t.Helper()
	snapshot := testSnapshot()
	snapshot.EditMode = "single_file"
	ref, err := bank.UpsertSnapshot(context.Background(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	candidateRecord, err := bank.AddCandidate(context.Background(), ref, questionbank.CandidateInput{Files: candidate("good").Files})
	if err != nil {
		t.Fatal(err)
	}
	feedback := feedbackString([]answer.Feedback{{Kind: "selftest", Message: runtimeStartRequiredMessage, ActualStdout: runtimeStartRequiredMessage, ExpectedStdout: "ok"}})
	validation := questionbank.Validation{CandidateID: candidateRecord.ID, Kind: "selftest", Result: "failed", FilesHash: candidateRecord.FilesHash, SavedFilesHash: candidateRecord.FilesHash, RevisionHash: candidateRecord.RevisionHash, ContractHash: candidateRecord.ContractHash, Feedback: feedback, Terminal: true, Complete: true, TestCount: 1, PassedCount: 0}
	if err := bank.RecordValidation(context.Background(), validation); err != nil {
		t.Fatal(err)
	}
	candidateRecord, err = bank.Candidate(context.Background(), candidateRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if candidateRecord.Status != "rejected" || !hadoopPrerequisiteOnlyRejection(candidateRecord) {
		t.Fatalf("seed candidate is not an eligible historical Hadoop rejection: %+v", candidateRecord)
	}
	return candidateRecord
}

func TestHistoricalHadoopRejectionRetainsUnknownThenRetestsWithZeroModel(t *testing.T) {
	bank, err := questionbank.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer bank.Close()
	seed := hadoopRejectedCandidate(t, bank)
	snapshot := testSnapshot()
	snapshot.EditMode = "single_file"
	failedAdapter := &hadoopLegacyAdapter{fakeAdapter: fakeAdapter{snap: snapshot}, marker: true, startErr: errors.New("Hadoop readiness unavailable")}
	failedReq := recoveryRequest()
	failedReq.IdempotencyKey = "hadoop-bootstrap-failed"
	failed, err := New(bank, nil, Config{}).Run(context.Background(), failedAdapter, failedReq)
	if err == nil || failed.Status != "uncertain" || failed.SelfTestStatus != "unknown" || failedAdapter.startCalls != 1 || failedAdapter.selfTests != 1 || failedAdapter.submits != 0 {
		t.Fatalf("failed Hadoop bootstrap was not fail-closed: result=%+v err=%v adapter=%+v", failed, err, failedAdapter)
	}
	stored, err := bank.Candidate(context.Background(), seed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "rejected" || len(stored.Validations) != 2 || stored.Validations[0].Result != "failed" || stored.Validations[1].Result != "unknown" {
		t.Fatalf("original rejection/unknown evidence changed unexpectedly: %+v", stored.Validations)
	}

	readyAdapter := &hadoopLegacyAdapter{fakeAdapter: fakeAdapter{snap: snapshot}}
	readyReq := failedReq
	readyReq.IdempotencyKey = "hadoop-bootstrap-ready"
	readyReq.Mode = ModeSubmit
	ready, err := New(bank, nil, Config{}).Run(context.Background(), readyAdapter, readyReq)
	if err != nil || ready.Status != "passed" || ready.ModelCalls != 0 || ready.CandidateID != seed.ID || readyAdapter.selfTests != 1 || readyAdapter.submits != 1 || readyAdapter.startCalls != 0 {
		t.Fatalf("historical Hadoop candidate was not freshly verified without model: result=%+v err=%v adapter=%+v", ready, err, readyAdapter)
	}
	stored, err = bank.Candidate(context.Background(), seed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "upstream_verified" || len(stored.Validations) != 4 || stored.Validations[0].Result != "failed" || stored.Validations[1].Result != "unknown" || stored.Validations[2].Result != "passed" || stored.Validations[3].Result != "passed" || stored.Validations[3].Kind != "formal" {
		t.Fatalf("candidate was promoted without preserving historical evidence: %+v", stored)
	}
}

func TestHadoopPrerequisiteOnlyRejectionRejectsUnrelatedEvidence(t *testing.T) {
	bank, err := questionbank.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer bank.Close()
	base := hadoopRejectedCandidate(t, bank)
	cases := []struct {
		name   string
		mutate func(*questionbank.Candidate)
	}{
		{name: "non-hadoop marker", mutate: func(c *questionbank.Candidate) {
			c.Validations[0].Feedback = feedbackString([]answer.Feedback{{Kind: "selftest", Message: "wrong", ActualStdout: "wrong", ExpectedStdout: "ok"}})
		}},
		{name: "formal unknown", mutate: func(c *questionbank.Candidate) {
			c.Validations = append(c.Validations, questionbank.Validation{Kind: "formal", Result: "unknown", FilesHash: c.FilesHash, SavedFilesHash: c.FilesHash, SubmittedFilesHash: c.FilesHash, RevisionHash: c.RevisionHash, ContractHash: c.ContractHash, Complete: true, TestCount: 1})
		}},
		{name: "files hash mismatch", mutate: func(c *questionbank.Candidate) { c.FilesHash = "other" }},
		{name: "revision mismatch", mutate: func(c *questionbank.Candidate) { c.Validations[0].RevisionHash = "other" }},
		{name: "formal passed", mutate: func(c *questionbank.Candidate) {
			c.Validations = append(c.Validations, questionbank.Validation{Kind: "formal", Result: "passed", FilesHash: c.FilesHash, SavedFilesHash: c.FilesHash, RevisionHash: c.RevisionHash, ContractHash: c.ContractHash})
		}},
		{name: "foreign passed selftest", mutate: func(c *questionbank.Candidate) {
			c.Validations = append(c.Validations, questionbank.Validation{Kind: "selftest", Result: "passed", FilesHash: "foreign", SavedFilesHash: "foreign", RevisionHash: c.RevisionHash, ContractHash: c.ContractHash})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			copy := base
			copy.Validations = append([]questionbank.Validation(nil), base.Validations...)
			tc.mutate(&copy)
			if hadoopPrerequisiteOnlyRejection(copy) {
				t.Fatalf("invalid historical evidence was accepted: %+v", copy)
			}
		})
	}
}
