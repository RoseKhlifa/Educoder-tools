package questionbank

import (
	"context"
	"strings"
	"testing"

	"educoder-cli/internal/problem"
)

func contributionAnswer(t *testing.T, s *Store, title, answer string, verified bool) int64 {
	t.Helper()
	q := objectiveTestQuestion()
	q.Question = title
	key, err := s.UpsertObjectiveQuestion(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.AddObjectiveAnswer(context.Background(), key, answer, "platform_collection", "")
	if err != nil {
		t.Fatal(err)
	}
	if verified {
		if err := s.RecordObjectiveValidation(context.Background(), id, objectiveTestValidation(answer)); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func TestContributionsRequireOwnerBoundReusableEvidenceAndDeduplicateSemanticQuestions(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	alice := "platform:" + strings.Repeat("a", 32)
	bob := "platform:" + strings.Repeat("b", 32)
	count := func(owner string, want int64) {
		t.Helper()
		got, err := s.ContributedQuestions(ctx, owner)
		if err != nil || got != want {
			t.Fatalf("count=%d err=%v want=%d", got, err, want)
		}
	}
	count(alice, 0)
	// A source label alone cannot establish whose account supplied the answer.
	first := contributionAnswer(t, s, "Contributed question", "A", true)
	count(alice, 0)
	for i := 0; i < 4; i++ {
		if err := s.RecordObjectiveContribution(ctx, alice, first); err != nil {
			t.Fatal(err)
		}
	}
	second := contributionAnswer(t, s, "Contributed question", "B", true)
	if err := s.RecordObjectiveContribution(ctx, alice, second); err != nil {
		t.Fatal(err)
	}
	count(alice, 1)
	count(bob, 0)
	// Another account may supply verified observations of the same question.
	if err := s.RecordObjectiveContribution(ctx, bob, first); err != nil {
		t.Fatal(err)
	}
	count(bob, 1)
	unconfirmed := contributionAnswer(t, s, "Unverified", "A", false)
	if err := s.RecordObjectiveContribution(ctx, alice, unconfirmed); err == nil {
		t.Fatal("unverified contribution accepted")
	}
	image := contributionAnswer(t, s, "Missing image ![graph](https://example.org/graph.png)", "A", true)
	if err := s.RecordObjectiveContribution(ctx, alice, image); err == nil {
		t.Fatal("incomplete-resource contribution accepted")
	}
	if err := s.RecordObjectiveContribution(ctx, "arbitrary-account", first); err == nil {
		t.Fatal("invalid owner accepted")
	}
	count(alice, 1)
}

func TestContributionBridgesExactFormalRunsAndRemainsSubsetOfReusableBank(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	alice := "platform:" + strings.Repeat("a", 32)
	bob := "platform:" + strings.Repeat("b", 32)
	owner := problem.HashContent(alice)
	stamp := "2026-11-02T02:00:00Z"
	// Formal batch evidence alone does not manufacture an individual bank entry.
	addObjectiveCompletion(t, s, "objective-pass", owner, "G", "submit", "passed", stamp)
	assertCount := func(want int64) {
		t.Helper()
		got, err := s.ContributedQuestions(ctx, alice)
		if err != nil || got != want {
			t.Fatalf("got %d/%v want %d", got, err, want)
		}
		global, err := s.Summary(ctx)
		if err != nil || got > global.TotalVerifiedQuestions {
			t.Fatalf("not a subset: %d %+v %v", got, global, err)
		}
	}
	assertCount(0)
	q := objectiveTestQuestion()
	contributionAnswer(t, s, q.Question, "A", true)
	contributionAnswer(t, s, "Second question", "A", true)
	assertCount(2)
	// Same semantic questions repeated in another game or run still count once.
	addObjectiveCompletion(t, s, "objective-repeat", owner, "G2", "submit", "passed", stamp)
	addObjectiveCompletion(t, s, "objective-other", problem.HashContent(bob), "G3", "submit", "passed", stamp)
	assertCount(2)
	addProgrammingCompletion(t, s, "program-pass", owner, "P", "submit", "passed", stamp)
	assertCount(3)
	addProgrammingCompletion(t, s, "program-repeat", owner, "P2", "submit", "passed", stamp)
	assertCount(3)
	var solutionID int64
	if err := s.db.QueryRow(`SELECT id FROM solutions LIMIT 1`).Scan(&solutionID); err != nil {
		t.Fatal(err)
	}
	// A later failure removes the candidate from the reusable bank and from
	// attributed contributions; an ordinary self-test pass cannot restore it.
	c, err := s.Candidate(ctx, solutionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordValidation(ctx, Validation{CandidateID: c.ID, Kind: "selftest", Result: "failed", FilesHash: c.FilesHash, RevisionHash: c.RevisionHash, ContractHash: c.ContractHash}); err != nil {
		t.Fatal(err)
	}
	assertCount(2)
	if err := s.RecordValidation(ctx, Validation{CandidateID: c.ID, Kind: "selftest", Result: "passed", FilesHash: c.FilesHash, RevisionHash: c.RevisionHash, ContractHash: c.ContractHash}); err != nil {
		t.Fatal(err)
	}
	assertCount(2)
	addProgrammingCompletion(t, s, "program-restored", problem.HashContent(bob), "P3", "submit", "passed", stamp)
	assertCount(3)
}

func TestContributionReceiptsPersistAndContextFailureDoesNotPretendZero(t *testing.T) {
	path := t.TempDir() + "/bank.sqlite"
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	owner := "platform:" + strings.Repeat("a", 32)
	id := contributionAnswer(t, s, "Persistent contribution", "A", true)
	if err = s.RecordObjectiveContribution(context.Background(), owner, id); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, err := s.ContributedQuestions(context.Background(), owner); err != nil || got != 1 {
		t.Fatalf("persistent %d %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.ContributedQuestions(ctx, owner); err == nil {
		t.Fatal("canceled query returned a zero success")
	}
}
