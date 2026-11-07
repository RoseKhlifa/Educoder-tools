package questionbank

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"educoder-cli/internal/answer"
)

func externalObjectiveFixture() answer.ObjectiveSnapshot {
	return answer.ObjectiveSnapshot{Kind: "objective", Title: "Example level", Statement: "Complete common material", Questions: []answer.ObjectiveQuestion{
		{Position: 1, Question: "First question", QuestionType: 1, Category: 1, Options: []answer.ObjectiveOption{{Label: "A", Text: "one", Position: 0}, {Label: "B", Text: "two", Position: 1}}},
		{Position: 2, Question: "Second question", QuestionType: 1, Category: 2, Options: []answer.ObjectiveOption{{Label: "A", Text: "one", Position: 0}, {Label: "B", Text: "two", Position: 1}, {Label: "C", Text: "three", Position: 2}}},
	}}
}

func TestExternalObjectiveCandidateIsUnverifiedAndShapeBound(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	root := t.TempDir()
	// Older inventories compact multi-select answers into one string and may
	// repeat a letter across selected elements. Import canonicalizes both forms.
	externalFixture(t, root, "choice", "choice", "Example", "Example level", "answer.json", "answer.json", `{"answer":[{"number":1,"selected":["B"],"category":1},{"number":2,"selected":["CA","A"],"category":2}]}`)
	if _, err = s.ImportExternalDirectory(ctx, externalOptions(root)); err != nil {
		t.Fatal(err)
	}
	if report, err := s.ImportExternalDirectory(ctx, externalOptions(root)); err != nil || report.Existing != 1 {
		t.Fatalf("pinned compact multi-select reimport failed: %+v %v", report, err)
	}
	// Emulate a record imported by the pre-canonicalization release. The same
	// pinned source must migrate in place rather than being rejected as changed.
	entries, err := s.ExternalEntries(ctx)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries=%+v %v", entries, err)
	}
	legacy := entries[0]
	legacy.Status, legacy.Reason = "rejected", "invalid_choice_reference"
	legacy.Reference = json.RawMessage(`null`)
	legacy = finalizeExternalEntry(legacy)
	if _, err = s.db.Exec(`UPDATE external_entries SET status=?,reason=?,reference_json=?,entry_sha256=? WHERE id=?`, legacy.Status, legacy.Reason, string(legacy.Reference), legacy.EntrySHA256, entries[0].ID); err != nil {
		t.Fatal(err)
	}
	if report, err := s.ImportExternalDirectory(ctx, externalOptions(root)); err != nil || report.Existing != 1 {
		t.Fatalf("legacy compact import migration failed: %+v %v", report, err)
	}
	snapshot := externalObjectiveFixture()
	identity := ExternalIdentity{Experiment: "Example", Title: "Example level"}
	c, err := s.FindExternalObjectiveCandidate(ctx, identity, snapshot)
	if err != nil || c.ID <= 0 || c.Candidate.Answers[0] != "B" || c.Candidate.Answers[1] != "AC" {
		t.Fatalf("candidate=%+v err=%v", c, err)
	}
	key, _ := s.PutObjectiveSnapshot(ctx, snapshot)
	if _, err = s.FindObjectiveVerified(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ungraded external promoted: %v", err)
	}
	// Legacy reference status is still usable without reimport or losing IDs.
	if _, err = s.db.Exec(`UPDATE external_entries SET status='reference',reason='missing_question_and_option_identity'`); err != nil {
		t.Fatal(err)
	}
	old, err := s.FindExternalObjectiveCandidate(ctx, identity, snapshot)
	if err != nil || old.ID != c.ID {
		t.Fatalf("legacy=%+v %v", old, err)
	}
	for _, mutate := range []func(*answer.ObjectiveSnapshot){
		func(x *answer.ObjectiveSnapshot) { x.Questions = x.Questions[:1] },
		func(x *answer.ObjectiveSnapshot) { x.Questions[1].Category = 1 },
		func(x *answer.ObjectiveSnapshot) { x.Questions[0].Options = x.Questions[0].Options[:1] },
		func(x *answer.ObjectiveSnapshot) { x.Questions[0].Position = 2 },
	} {
		x := externalObjectiveFixture()
		mutate(&x)
		if _, err = s.FindExternalObjectiveCandidate(ctx, identity, x); !errors.Is(err, ErrNotFound) {
			t.Errorf("bad roster matched: %v", err)
		}
	}
	if _, err = s.FindExternalObjectiveCandidate(ctx, ExternalIdentity{Experiment: "Other", Title: identity.Title}, snapshot); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross experiment matched")
	}
	if err = s.RecordObjectiveVerification(ctx, c.ID, "fixture", "full_score", "2", "2"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.FindObjectiveVerified(ctx, key); err != nil {
		t.Fatalf("normal grading did not promote: %v", err)
	}
}

func TestExternalObjectiveConflictingSourcesDoNotSelectWinner(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	root := t.TempDir()
	for i, raw := range []string{`{"answer":[{"number":1,"selected":["A"]},{"number":2,"selected":["A","C"]}]}`, `{"answer":[{"number":1,"selected":["B"]},{"number":2,"selected":["A","C"]}]}`} {
		externalFixture(t, root, string(rune('a'+i)), "choice", "Example", "Example level", "answer.json", "answer.json", raw)
	}
	if _, err = s.ImportExternalDirectory(ctx, externalOptions(root)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.FindExternalObjectiveCandidate(ctx, ExternalIdentity{Experiment: "Example", Title: "Example level"}, externalObjectiveFixture()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ambiguous source selected: %v", err)
	}
}
