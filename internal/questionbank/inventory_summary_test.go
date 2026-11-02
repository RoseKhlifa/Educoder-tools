package questionbank

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"educoder-cli/internal/problem"
)

func TestInventorySummaryCountsUnverifiedQuestionsAndDistinctLocalRevisions(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, err := s.InventorySummary(ctx); err != nil || got != (InventorySummary{}) {
		t.Fatalf("empty inventory = %+v %v", got, err)
	}
	q := objectiveTestQuestion()
	key, err := s.UpsertObjectiveQuestion(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	q.Position = 20
	if duplicate, err := s.UpsertObjectiveQuestion(ctx, q); err != nil || duplicate != key {
		t.Fatal("local question position multiplied the question identity")
	}
	for _, value := range []string{"A", "B"} {
		if _, err := s.AddObjectiveAnswer(ctx, key, value, "fixture", ""); err != nil {
			t.Fatal(err)
		}
	}
	q.Question = "Another recorded question"
	if _, err := s.UpsertObjectiveQuestion(ctx, q); err != nil {
		t.Fatal(err)
	}
	code := bankSnapshot()
	code.EditMode = "code"
	ref, err := s.UpsertSnapshot(ctx, code)
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"candidate one", "candidate two"} {
		if _, err := s.AddCandidate(ctx, ref, CandidateInput{Files: []problem.SolutionFile{{Path: code.Files[0].Path, Content: content}}, Source: "fixture"}); err != nil {
			t.Fatal(err)
		}
	}
	graphic := code
	graphic.StatementMarkdown = "Draw another problem"
	graphic.EditMode = "graphic"
	graphic.Files = []problem.File{{Path: "submit/diagram.mdj", Language: "json"}}
	if _, err := s.UpsertSnapshot(ctx, graphic); err != nil {
		t.Fatal(err)
	}
	check := func(want InventorySummary) {
		t.Helper()
		if got, err := s.InventorySummary(ctx); err != nil || got != want {
			t.Fatalf("inventory = %+v %v; want %+v", got, err, want)
		}
	}
	check(InventorySummary{ObjectiveQuestions: 2, ProgrammingProblems: 1, GraphicProblems: 1, TotalQuestions: 4})
	// Another execution environment belongs to the same semantic revision.
	code.Files[0].Language = "c"
	otherRef, err := s.UpsertSnapshot(ctx, code)
	if err != nil || otherRef.RevisionHash != ref.RevisionHash || otherRef.ContractHash == ref.ContractHash {
		t.Fatalf("alternate contract = %+v %v", otherRef, err)
	}
	check(InventorySummary{ObjectiveQuestions: 2, ProgrammingProblems: 1, GraphicProblems: 1, TotalQuestions: 4})
	code.EditMode = "graphic"
	if _, err := s.UpsertSnapshot(ctx, code); err != nil {
		t.Fatal(err)
	}
	check(InventorySummary{ObjectiveQuestions: 2, GraphicProblems: 2, TotalQuestions: 4})
	// Display inventory must not make these unverified records eligible to solve.
	if verified, err := s.Summary(ctx); err != nil || verified != (Summary{}) {
		t.Fatalf("inventory weakened verified qualification: %+v %v", verified, err)
	}
}

func TestInventorySummaryDeduplicatesExternalIdentitiesAndBoundLocalQuestions(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	root := t.TempDir()
	project := `{"_type":"Project","ownedElements":[{"_type":"UMLModel"}]}`
	externalFixture(t, root, "code", "code", "Linux", "Create file", "case1/code.sh", "answer.sh", "touch one")
	externalFixture(t, root, "diagram", "uml", "Drawing", "Class diagram", "submit/diagram.mdj", "answer.mdj", project)
	externalFixture(t, root, "choice-reference", "choice", "Linux", "Choices", "answer.json", "answer.json", `{"answer":[{"number":1,"selected":["A"]}]}`)
	externalFixture(t, root, "diagram-reference", "uml", "Drawing", "Text reference", "submit/diagram.txt", "answer.txt", "draw a diagram")
	externalFixture(t, root, "diagram-rejected", "uml", "Drawing", "Invalid diagram", "submit/diagram.mdj", "answer.mdj", "invalid diagram")
	if err := os.WriteFile(filepath.Join(root, "answers.json"), []byte(`{"classrooms":[{"experiments":[{"name":"Other","levels":[{"title":"Missing contract","kind":"code","answered":true,"answer":{"code":"a fragment","language":"unknown"}}]}]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	importAt := func(commit string) {
		t.Helper()
		opts := externalOptions(root)
		opts.Commit = commit
		if _, err := s.ImportExternalDirectory(ctx, opts); err != nil {
			t.Fatal(err)
		}
	}
	check := func(programming, graphic int64) {
		t.Helper()
		want := InventorySummary{ProgrammingProblems: programming, GraphicProblems: graphic, TotalQuestions: programming + graphic}
		if got, err := s.InventorySummary(ctx); err != nil || got != want {
			t.Fatalf("inventory = %+v %v; want %+v", got, err, want)
		}
	}
	importAt(externalTestCommit)
	importAt(externalTestCommit)
	importAt(strings.Repeat("2", 40))
	check(1, 1)
	for _, setup := range []struct {
		experiment, title, path, language, mode string
	}{{"Linux", "Create file", "case1/code.sh", "shell", "code"}, {"Drawing", "Class diagram", "submit/diagram.mdj", "json", "graphic"}} {
		p := problem.Snapshot{Kind: "programming", Title: setup.title, StatementMarkdown: "Current full problem statement", EditMode: setup.mode, Files: []problem.File{{Path: setup.path, Language: setup.language}}, EnvironmentContract: []string{"graphic:staruml"}}
		ref, err := s.UpsertSnapshot(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		candidates, err := s.BindExternalCandidates(ctx, ref, ExternalIdentity{Experiment: setup.experiment, Title: setup.title}, p)
		if err != nil || len(candidates) != 1 {
			t.Fatalf("bind external source = %+v %v", candidates, err)
		}
		check(1, 1) // Binding creates a local representation, not another question.
	}
	// New alternatives have separate answer bytes/provenance, but retain the
	// external question identity already represented by the local revision.
	externalFixture(t, root, "code-alternative", "code", "Linux", "Create file", "case1/code.sh", "answer.sh", "touch two")
	externalFixture(t, root, "diagram-alternative", "uml", "Drawing", "Class diagram", "submit/diagram.mdj", "answer.mdj", `{"_type":"Project","ownedElements":[{"_type":"UMLClass"}]}`)
	importAt(strings.Repeat("3", 40))
	check(1, 1)
	externalFixture(t, root, "another-course", "code", "Different experiment", "Create file", "case1/code.sh", "answer.sh", "touch one")
	importAt(strings.Repeat("4", 40))
	check(2, 1)
	if verified, err := s.Summary(ctx); err != nil || verified.TotalVerifiedQuestions != 0 {
		t.Fatalf("unverified imports entered trusted summary: %+v %v", verified, err)
	}
}

func TestInventorySummaryPropagatesDatabaseAndContextErrors(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.InventorySummary(ctx); err == nil {
		t.Fatal("cancelled inventory returned fabricated counts")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InventorySummary(context.Background()); err == nil {
		t.Fatal("closed database returned fabricated counts")
	}
}
