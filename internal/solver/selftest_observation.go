package solver

import (
	"encoding/json"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

// A prior pass is a reason to investigate conflicting observations, never a
// substitute for a new selftest or formal result. Every source/contract binding
// is checked explicitly, including the prior sample's exact input and output.
func priorPassedSample(c questionbank.Candidate, ref questionbank.Ref, test problem.PublicTest) bool {
	hash, err := problem.HashFiles(c.Files)
	if err != nil || hash == "" || hash != c.FilesHash || c.Ref != ref {
		return false
	}
	formal, sample := false, false
	for _, v := range c.Validations {
		if v.CandidateID != c.ID || v.RevisionHash != ref.RevisionHash || v.ContractHash != ref.ContractHash || v.FilesHash != hash || v.SavedFilesHash != hash || !v.Terminal || !v.Complete || v.Result != "passed" || v.TestCount <= 0 || v.PassedCount != v.TestCount {
			continue
		}
		if v.Kind == "formal" && v.SubmittedFilesHash == hash {
			formal = true
		}
		if v.Kind == "selftest" && v.TestCount == 1 {
			var feedback []answer.Feedback
			if json.Unmarshal([]byte(v.Feedback), &feedback) != nil {
				continue
			}
			for _, f := range feedback {
				if f.Kind == "selftest_passed" && f.Stdin == test.Stdin && f.ExpectedStdout == test.ExpectedStdout && educoder.PublicOutputMatches(f.ActualStdout, test.ExpectedStdout) {
					sample = true
				}
			}
		}
	}
	return formal && sample
}

// Old empty observations remain immutable failed rows. A new run may re-test
// their source only when every rejection has this narrow shape and the same
// sample and candidate already have bound passing evidence. This grants no trust.
func legacyEmptyOutputRejection(snap problem.Snapshot, c questionbank.Candidate, ref questionbank.Ref) bool {
	if snap.EditMode != "single_file" || c.Status != "rejected" {
		return false
	}
	found := false
	for _, v := range c.Validations {
		if v.Result != "failed" {
			continue
		}
		if v.Kind != "selftest" || v.CandidateID != c.ID || v.FilesHash != c.FilesHash || v.SavedFilesHash != c.FilesHash || v.RevisionHash != ref.RevisionHash || v.ContractHash != ref.ContractHash || !v.Terminal || !v.Complete || v.TestCount != 1 || v.PassedCount != 0 {
			return false
		}
		var feedback []answer.Feedback
		if json.Unmarshal([]byte(v.Feedback), &feedback) != nil || len(feedback) != 1 {
			return false
		}
		f := feedback[0]
		if f.Kind != "selftest" || f.Message != "" || f.ActualStdout != "" || f.ExpectedStdout == "" {
			return false
		}
		test := problem.PublicTest{Stdin: f.Stdin, ExpectedStdout: f.ExpectedStdout}
		inSnapshot := false
		for _, current := range snap.PublicTests {
			inSnapshot = inSnapshot || current == test
		}
		if !inSnapshot || !priorPassedSample(c, ref, test) {
			return false
		}
		found = true
	}
	return found
}

// Do not replay terminal scripts, incomplete/failed builds, or arbitrary empty
// output. This branch is only for a normal editor's status-less stream that
// contradicts this exact candidate's prior full formal and public-sample pass.
func conflictingEmptySelfTest(snap problem.Snapshot, c questionbank.Candidate, ref questionbank.Ref, test problem.PublicTest, st SelfTest, executionErr error) bool {
	return snap.EditMode == "single_file" && executionErr == nil && st.Completed && st.ProcessFinishedSeen && !st.Matches && st.Output == "" && st.OutputBytes == 0 && test.ExpectedStdout != "" && st.CompileOK == nil && st.ExitCode == nil && st.SavedFilesHash == c.FilesHash && priorPassedSample(c, ref, test)
}
