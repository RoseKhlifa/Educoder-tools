package questionbank

import (
	"context"
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
)

func TestSummaryCountsReusableQuestionsInsteadOfCandidatesOrAttempts(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, err := s.Summary(ctx); err != nil || got != (Summary{}) {
		t.Fatalf("empty bank = %+v, %v", got, err)
	}
	objective := func(title, text string, resources []ObjectiveResource, validations ...ObjectiveValidation) string {
		t.Helper()
		q := objectiveTestQuestion()
		q.Question = title
		key, err := s.UpsertObjectiveQuestionWithResources(ctx, q, resources)
		if err != nil {
			t.Fatal(err)
		}
		id, err := s.AddObjectiveAnswer(ctx, key, text, "fixture", "")
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range validations {
			if err := s.RecordObjectiveValidation(ctx, id, v); err != nil {
				t.Fatal(err)
			}
		}
		return key
	}
	full := objectiveTestValidation("A")
	key := objective("Same normalized question", "A", nil, full, full)
	// Multiple eligible answers and repeated observations remain one question.
	objective("Same normalized question", "B", nil, objectiveTestValidation("B"))
	q := objectiveTestQuestion()
	q.Question, q.Position = "Same normalized question", 99
	if duplicate, err := s.UpsertObjectiveQuestion(ctx, q); err != nil || duplicate != key {
		t.Fatal("different local position did not retain question identity")
	}
	individual := full
	individual.FullScore, individual.PerQuestionCorrect = false, true
	individual.Score, individual.MaxScore = "", ""
	objective("Explicit correct individual question", "A", nil, individual)
	objective("Unverified question", "A", nil)
	failed := full
	failed.Passed = false
	objective("Failed question", "A", nil, failed)
	partial := full
	partial.Score = "1"
	objective("Partial score question", "A", nil, partial)
	unknown := full
	unknown.Confirmed = false
	objective("Unconfirmed question", "A", nil, unknown)
	imageQuestion := "Image question ![tree](https://example.org/tree.png)"
	objective(imageQuestion, "A", nil, full)
	objective(imageQuestion, "A", []ObjectiveResource{{Locator: "https://example.org/tree.png", SHA256: problem.HashContent("inspected image bytes")}}, full)
	// Legacy whole-task evidence is not counted as an extra individual question.
	snapshot := answer.ObjectiveSnapshot{Kind: "objective", Questions: []answer.ObjectiveQuestion{q}}
	legacyKey, err := s.PutObjectiveSnapshot(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := s.AddObjectiveCandidate(ctx, legacyKey, snapshot, answer.ObjectiveCandidate{Kind: "objective", Answers: []string{"A"}}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordObjectiveVerification(ctx, legacy.ID, "fixture", "full_score", "1", "1"); err != nil {
		t.Fatal(err)
	}

	addProgramming := func(statement, code string) Candidate {
		t.Helper()
		p := bankSnapshot()
		p.StatementMarkdown = statement
		ref, err := s.UpsertSnapshot(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		c, err := s.AddCandidate(ctx, ref, CandidateInput{Files: []problem.SolutionFile{{Path: "src/main.cpp", Content: code}}, Source: "fixture"})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	validate := func(c Candidate, kind, result string) {
		t.Helper()
		passed := 0
		if result == "passed" {
			passed = 2
		}
		v := Validation{CandidateID: c.ID, Kind: kind, Result: result, FilesHash: c.FilesHash, SavedFilesHash: c.FilesHash, SubmittedFilesHash: c.FilesHash, RevisionHash: c.RevisionHash, ContractHash: c.ContractHash, Terminal: true, Complete: true, TestCount: 2, PassedCount: passed}
		if err := s.RecordValidation(ctx, v); err != nil {
			t.Fatal(err)
		}
	}
	addProgramming("Unverified", "candidate")
	validate(addProgramming("Self-test only", "candidate"), "selftest", "passed")
	validate(addProgramming("Failed formal", "candidate"), "formal", "failed")
	rejected := addProgramming("Withdrawn formal pass", "candidate")
	validate(rejected, "formal", "passed")
	validate(rejected, "selftest", "failed")
	validate(rejected, "selftest", "passed")
	if c, err := s.FindVerified(ctx, rejected.Ref); err != ErrNotFound {
		t.Fatalf("fixture should remain rejected: %+v %v", c, err)
	}
	first := addProgramming("Verified first semantic revision", "candidate-1")
	validate(first, "selftest", "failed")
	validate(first, "formal", "passed")
	validate(first, "formal", "passed")
	validate(first, "formal", "unknown")
	validate(addProgramming("Verified first semantic revision", "candidate-2"), "formal", "passed")
	// A changed execution contract with the same problem revision is not a new
	// question; it is another environment in which the source was validated.
	p := bankSnapshot()
	p.StatementMarkdown = "Verified first semantic revision"
	p.Files[0].Language = "c"
	otherRef, err := s.UpsertSnapshot(ctx, p)
	if err != nil || otherRef.RevisionHash != first.RevisionHash || otherRef.ContractHash == first.ContractHash {
		t.Fatalf("alternate contract fixture = %+v, %v", otherRef, err)
	}
	other, err := s.AddCandidate(ctx, otherRef, CandidateInput{Files: []problem.SolutionFile{{Path: "src/main.cpp", Content: "candidate-3"}}, Source: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	validate(other, "formal", "passed")
	second := addProgramming("Verified second semantic revision", "candidate")
	validate(second, "formal", "passed")
	// Incomplete or mismatched proof cannot enter the verified count.
	invalid := addProgramming("Incomplete proof", "candidate")
	for _, mutate := range []func(*Validation){
		func(v *Validation) { v.Complete = false },
		func(v *Validation) { v.Terminal = false },
		func(v *Validation) { v.SubmittedFilesHash = problem.HashContent("different bytes") },
		func(v *Validation) { v.TestCount, v.PassedCount = 0, 0 },
	} {
		v := Validation{CandidateID: invalid.ID, Kind: "formal", Result: "passed", FilesHash: invalid.FilesHash, SavedFilesHash: invalid.FilesHash, SubmittedFilesHash: invalid.FilesHash, RevisionHash: invalid.RevisionHash, ContractHash: invalid.ContractHash, Terminal: true, Complete: true, TestCount: 2, PassedCount: 2}
		mutate(&v)
		if err := s.RecordValidation(ctx, v); err == nil {
			t.Fatal("invalid formal proof was accepted")
		}
	}
	want := Summary{VerifiedObjectiveQuestions: 3, VerifiedProgrammingProblems: 2, TotalVerifiedQuestions: 5}
	if got, err := s.Summary(ctx); err != nil || got != want {
		t.Fatalf("summary = %+v, %v; want %+v", got, err, want)
	}
	validate(second, "selftest", "failed")
	want.VerifiedProgrammingProblems, want.TotalVerifiedQuestions = 1, 4
	if got, err := s.Summary(ctx); err != nil || got != want {
		t.Fatalf("withdrawn pass remained counted: %+v, %v", got, err)
	}
	validate(second, "formal", "passed")
	want.VerifiedProgrammingProblems, want.TotalVerifiedQuestions = 2, 5
	if got, err := s.Summary(ctx); err != nil || got != want {
		t.Fatalf("restored formal pass = %+v, %v", got, err)
	}
}

func TestSummaryPropagatesDatabaseAndContextErrors(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Summary(ctx); err == nil {
		t.Fatal("cancelled summary returned a fabricated zero")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Summary(context.Background()); err == nil {
		t.Fatalf("closed database returned a fabricated summary: %+v", got)
	}
}

func TestSummaryRequiresCompleteBoundPersistedProgrammingEvidence(t *testing.T) {
	for name, mutate := range map[string]func(*Validation){
		"nonterminal":            func(v *Validation) { v.Terminal = false },
		"incomplete":             func(v *Validation) { v.Complete = false },
		"saved bytes differ":     func(v *Validation) { v.SavedFilesHash = problem.HashContent("other bytes") },
		"submitted bytes differ": func(v *Validation) { v.SubmittedFilesHash = problem.HashContent("other bytes") },
		"revision differs":       func(v *Validation) { v.RevisionHash = problem.HashContent("other revision") },
		"contract differs":       func(v *Validation) { v.ContractHash = problem.HashContent("other contract") },
		"partial tests":          func(v *Validation) { v.PassedCount = 1 },
		"no tests":               func(v *Validation) { v.TestCount, v.PassedCount = 0, 0 },
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s, err := Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			ref, err := s.UpsertSnapshot(ctx, bankSnapshot())
			if err != nil {
				t.Fatal(err)
			}
			c, err := s.AddCandidate(ctx, ref, CandidateInput{Files: []problem.SolutionFile{{Path: "src/main.cpp", Content: "fixture"}}, Source: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			v := Validation{CandidateID: c.ID, Kind: "formal", Result: "passed", FilesHash: c.FilesHash, SavedFilesHash: c.FilesHash, SubmittedFilesHash: c.FilesHash, RevisionHash: c.RevisionHash, ContractHash: c.ContractHash, Terminal: true, Complete: true, TestCount: 2, PassedCount: 2}
			mutate(&v)
			// Model old/imported rows that predate RecordValidation's strict
			// contract. A historical passed label alone is insufficient.
			if _, err := s.db.ExecContext(ctx, "INSERT INTO verifications(solution_id,evidence_json,created_at) VALUES(?,?,?)", c.ID, marshal(v), now()); err != nil {
				t.Fatal(err)
			}
			if got, err := s.Summary(ctx); err != nil || got != (Summary{}) {
				t.Fatalf("incomplete persisted proof was counted: %+v %v", got, err)
			}
		})
	}
}
