package questionbank

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
)

func legacyAdapterSnapshot(n int) answer.ObjectiveSnapshot {
	snapshot := answer.ObjectiveSnapshot{Kind: "objective", Title: "Traversal", Statement: "Select the correct traversal for each case."}
	for i := 0; i < n; i++ {
		q := objectiveTestQuestion()
		q.Position = i + 1
		q.Question += strings.Repeat("?", i)
		snapshot.Questions = append(snapshot.Questions, q)
	}
	return snapshot
}
func legacyAdapterInput(snapshot answer.ObjectiveSnapshot, index int) ObjectiveQuestionInput {
	in, _ := historicalObjectiveInput(snapshot, snapshot.Questions[index])
	return in
}
func legacyAdapterAnswer(t *testing.T, s *Store, q answer.ObjectiveQuestion, value string) int64 {
	t.Helper()
	key, err := s.UpsertObjectiveQuestion(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.AddObjectiveAnswer(context.Background(), key, value, "old-bank", "legacy-model")
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func legacyAdapterVector(t *testing.T, s *Store, snapshot answer.ObjectiveSnapshot, answers []string) (string, ObjectiveCandidate) {
	t.Helper()
	ctx := context.Background()
	key, err := s.PutObjectiveSnapshot(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.AddObjectiveCandidate(ctx, key, snapshot, answer.ObjectiveCandidate{Kind: "objective", Answers: answers}, "old-vector")
	if err != nil {
		t.Fatal(err)
	}
	return key, c
}
func requireLegacyAlias(t *testing.T, s *Store, in ObjectiveQuestionInput, wantID int64) []ObjectiveAnswer {
	t.Helper()
	key, _, err := s.UpsertObjectiveQuestionWithContext(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.LookupObjectiveAnswersWithContext(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range got {
		if a.ID == wantID && a.Reusable {
			return got
		}
	}
	t.Fatalf("original ID %d not reusable: %+v", wantID, got)
	return nil
}

func TestLegacyObjectiveUpgradePreservesIDsEvidenceAndContributions(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := legacyAdapterSnapshot(2)
	key, c := legacyAdapterVector(t, s, snapshot, []string{"A", "B"})
	ids := []int64{legacyAdapterAnswer(t, s, snapshot.Questions[0], "A"), legacyAdapterAnswer(t, s, snapshot.Questions[1], "B")}
	for i, id := range ids {
		v := objectiveTestValidation([]string{"A", "B"}[i])
		v.ScopeHash = key
		if err = s.RecordObjectiveValidation(ctx, id, v); err != nil {
			t.Fatal(err)
		}
		if _, err = s.db.Exec(`INSERT INTO objective_contributions(owner,answer_id,created_at) VALUES('preserved-owner',?,?)`, id, now()); err != nil {
			t.Fatal(err)
		}
	}
	// Simulate evidence already present before this release: upgrade, not a fresh
	// solve or a fresh collection, must establish every compatibility alias.
	if _, err = s.db.Exec(`INSERT INTO objective_verifications(candidate_id,run_id,result,score,max_score,created_at) VALUES(?,?,'full_score','2','2',?)`, c.ID, "original-run", now()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`DROP TABLE objective_legacy_context_aliases`); err != nil {
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
	for i, id := range ids {
		requireLegacyAlias(t, s, legacyAdapterInput(snapshot, i), id)
	}
	report, err := s.ObjectiveLegacyCompatibilityReport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.LegacyQuestions != 2 || report.LegacyAnswers != 2 || report.AdaptedLegacyAnswers != 2 || report.AnswerVerifications != 2 || report.Contributions != 2 {
		t.Fatalf("legacy records lost, copied or not adapted: %+v", report)
	}
	var answers int
	if err = s.db.QueryRow(`SELECT count(*) FROM objective_answers`).Scan(&answers); err != nil || answers != 2 {
		t.Fatalf("answers duplicated: %d %v", answers, err)
	}
	again, err := s.AdaptLegacyObjectiveBank(ctx)
	if err != nil || again.AliasesAdded != 0 || again.AnswersExtracted != 0 || again.Contributions != 2 {
		t.Fatalf("migration is not idempotent: %+v %v", again, err)
	}
}

func TestLegacyObjectiveFullVectorExpandsOnceWithoutFabricatedIndividualEvidence(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snapshot := legacyAdapterSnapshot(3)
	_, c := legacyAdapterVector(t, s, snapshot, []string{"A", "B", "A"})
	if err = s.RecordObjectiveVerification(ctx, c.ID, "old-formal-run", "full_score", "3.0", "3"); err != nil {
		t.Fatal(err)
	}
	for i := range snapshot.Questions {
		in := legacyAdapterInput(snapshot, i)
		key, _, _ := s.UpsertObjectiveQuestionWithContext(ctx, in)
		got, err := s.LookupObjectiveAnswersWithContext(ctx, key)
		if err != nil || len(got) != 1 || !got[0].Reusable || got[0].Source != "old-vector" {
			t.Fatalf("full vector question %d not expanded: %+v %v", i, got, err)
		}
	}
	report, err := s.AdaptLegacyObjectiveBank(ctx)
	if err != nil || report.AliasesAdded != 0 || report.AnswersExtracted != 0 || report.AnswerVerifications != 0 {
		t.Fatalf("replayed expansion duplicated or fabricated evidence: %+v %v", report, err)
	}
	// The alias points at live published evidence; later rejection is observed.
	if err = s.RecordObjectiveVerification(ctx, c.ID, "later-run", "not_full_score", "2", "3"); err != nil {
		t.Fatal(err)
	}
	key, _, _ := s.UpsertObjectiveQuestionWithContext(ctx, legacyAdapterInput(snapshot, 0))
	got, err := s.LookupObjectiveAnswersWithContext(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range got {
		if a.Reusable {
			t.Fatal("later rejected historical vector still reusable")
		}
	}
}

func TestLegacyObjectivePartialNineOfTenReusesOriginalQuestions(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snapshot := legacyAdapterSnapshot(10)
	key, err := s.PutObjectiveSnapshot(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 10)
	for i, q := range snapshot.Questions {
		ids[i] = legacyAdapterAnswer(t, s, q, "A")
		v := objectiveTestValidation("A")
		v.ScopeHash = key
		v.FullScore = false
		v.PerQuestionCorrect = i < 9
		v.Passed = i < 9
		v.Score = "9"
		v.MaxScore = "10"
		if err = s.RecordObjectiveValidation(ctx, ids[i], v); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.AdaptLegacyObjectiveBank(ctx); err != nil {
		t.Fatal(err)
	}
	hits := 0
	for i := range snapshot.Questions {
		key, _, _ := s.UpsertObjectiveQuestionWithContext(ctx, legacyAdapterInput(snapshot, i))
		got, err := s.LookupObjectiveAnswersWithContext(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range got {
			if a.Reusable {
				if a.ID != ids[i] {
					t.Fatal("partial reuse changed answer ID")
				}
				hits++
			}
		}
	}
	if hits != 9 {
		t.Fatalf("wanted 9 old answers, got %d", hits)
	}
}

func TestLegacyObjectiveReferencesNeedCurrentContextVerification(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snapshot := legacyAdapterSnapshot(1)
	id := legacyAdapterAnswer(t, s, snapshot.Questions[0], "A")
	if err = s.RecordObjectiveValidation(ctx, id, objectiveTestValidation("A")); err != nil {
		t.Fatal(err)
	}
	in := legacyAdapterInput(snapshot, 0)
	key, _, _ := s.UpsertObjectiveQuestionWithContext(ctx, in)
	got, err := s.LookupObjectiveAnswersWithContext(ctx, key)
	if err != nil || len(got) != 0 {
		t.Fatalf("unknown old context silently reused: %+v %v", got, err)
	}
	references, err := s.LookupObjectiveLegacyReferences(ctx, in)
	if err != nil || len(references) != 1 || references[0].ID != id || !references[0].Verified || references[0].Reusable {
		t.Fatalf("old verified answer was discarded instead of exposed as exact reference: %+v %v", references, err)
	}
	scope, err := s.PutObjectiveSnapshot(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	v := objectiveTestValidation("A")
	v.ScopeHash = scope
	if err = s.RecordObjectiveContextValidation(ctx, in, id, v); err != nil {
		t.Fatal(err)
	}
	requireLegacyAlias(t, s, in, id)
	var count int
	if err = s.db.QueryRow(`SELECT count(*) FROM objective_answers`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("context validation duplicated answer: %d %v", count, err)
	}
	v.Passed = false
	v.FullScore = false
	v.Score = "0"
	if err = s.RecordObjectiveContextValidation(ctx, in, id, v); err != nil {
		t.Fatal(err)
	}
	got, err = s.LookupObjectiveAnswersWithContext(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range got {
		if a.Reusable {
			t.Fatal("context validation ignored later failure")
		}
	}
	changed := in
	changed.Context.Statement += " Changed context."
	if err = s.RecordObjectiveContextValidation(ctx, changed, id, objectiveTestValidation("A")); err == nil {
		t.Fatal("invented context scope accepted")
	}
}

func TestLegacyObjectiveMissingAndChangedFieldsNeverCreateFalseAliases(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snapshot := legacyAdapterSnapshot(1)
	id := legacyAdapterAnswer(t, s, snapshot.Questions[0], "A")
	_, c := legacyAdapterVector(t, s, snapshot, []string{"A"})
	if err = s.RecordObjectiveVerification(ctx, c.ID, "historical", "full_score", "1", "1"); err != nil {
		t.Fatal(err)
	}
	requireLegacyAlias(t, s, legacyAdapterInput(snapshot, 0), id)
	cases := map[string]func(*ObjectiveQuestionInput){
		"shared material":    func(in *ObjectiveQuestionInput) { in.Context.Statement += " not" },
		"context incomplete": func(in *ObjectiveQuestionInput) { in.Context.Complete = false },
		"changed option":     func(in *ObjectiveQuestionInput) { in.Question.Options[0].Text = "changed" },
		"missing option":     func(in *ObjectiveQuestionInput) { in.Question.Options = in.Question.Options[:1] },
		"unread image": func(in *ObjectiveQuestionInput) {
			in.Context.Statement += ` <img src="https://example.org/diagram.png">`
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			in := legacyAdapterInput(snapshot, 0)
			in.Question.Options = append([]answer.ObjectiveOption(nil), in.Question.Options...)
			change(&in)
			key, _, err := s.UpsertObjectiveQuestionWithContext(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.LookupObjectiveAnswersWithContext(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			for _, a := range got {
				if a.Reusable {
					t.Fatal("changed or partial input reused historical answer")
				}
			}
		})
	}
	raw := marshal(snapshot)
	var obj map[string]json.RawMessage
	_ = json.Unmarshal([]byte(raw), &obj)
	delete(obj, "statement")
	missing := marshal(obj)
	badKey := problem.HashContent(missing)
	if _, err = s.db.Exec(`INSERT INTO objective_snapshots(snapshot_hash,snapshot_json,created_at) VALUES(?,?,?)`, badKey, missing, now()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO objective_candidates(snapshot_hash,candidate_hash,candidate_json,source,created_at) VALUES(?,?,?,?,?)`, badKey, problem.HashContent("bad-snapshot"), `{"kind":"objective","answers":["B"]}`, "incomplete", now()); err != nil {
		t.Fatal(err)
	}
	var cid int64
	_ = s.db.QueryRow(`SELECT id FROM objective_candidates WHERE snapshot_hash=?`, badKey).Scan(&cid)
	if err = s.RecordObjectiveVerification(ctx, cid, "bad", "full_score", "1", "1"); err != nil {
		t.Fatal(err)
	}
	report, err := s.AdaptLegacyObjectiveBank(ctx)
	if err != nil || report.IncompleteSnapshots != 1 {
		t.Fatalf("missing raw field did not remain unresolved: %+v %v", report, err)
	}
	var n int
	_ = s.db.QueryRow(`SELECT count(*) FROM objective_legacy_context_aliases WHERE snapshot_hash=?`, badKey).Scan(&n)
	if n != 0 {
		t.Fatal("missing historical shared field aliased a verified answer")
	}
}

func TestLegacyObjectiveConflictingVerifiedAnswersHaveNoAutomaticWinner(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snapshot := legacyAdapterSnapshot(1)
	for _, v := range []string{"A", "B"} {
		_, c := legacyAdapterVector(t, s, snapshot, []string{v})
		if err = s.RecordObjectiveVerification(ctx, c.ID, "confirmed-"+v, "full_score", "1", "1"); err != nil {
			t.Fatal(err)
		}
	}
	key, _, _ := s.UpsertObjectiveQuestionWithContext(ctx, legacyAdapterInput(snapshot, 0))
	got, err := s.LookupObjectiveAnswersWithContext(ctx, key)
	if err != nil || len(got) != 2 {
		t.Fatalf("conflicting history lost: %+v %v", got, err)
	}
	for _, a := range got {
		if a.Reusable {
			t.Fatalf("conflict chose newest answer: %+v", got)
		}
	}
}

func TestLegacyObjectiveCanonicalMediaFollowsQuestionBindings(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snapshot := legacyAdapterSnapshot(2)
	first, second := problem.HashContent("first image"), problem.HashContent("second image")
	snapshot.Questions[0].Question += ` <img src="sha256:` + first + `">`
	snapshot.Questions[1].Question += ` <img src="sha256:` + second + `">`
	id := legacyAdapterAnswer(t, s, snapshot.Questions[0], "A")
	_, c := legacyAdapterVector(t, s, snapshot, []string{"A", "B"})
	if err = s.RecordObjectiveVerification(ctx, c.ID, "image-vector", "full_score", "2", "2"); err != nil {
		t.Fatal(err)
	}
	in := legacyAdapterInput(snapshot, 0)
	requireLegacyAlias(t, s, in, id)
	in.Resources = append(in.Resources, ObjectiveResource{Locator: "sha256:" + second, SHA256: second})
	in.Resources = ObjectiveQuestionResources(in)
	if len(in.Resources) != 1 || in.Resources[0].SHA256 != first {
		t.Fatalf("unrelated image retained in per-question identity: %+v", in.Resources)
	}
	in.Resources[0].SHA256 = second
	key, _, err := s.UpsertObjectiveQuestionWithContext(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.LookupObjectiveAnswersWithContext(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range got {
		if a.Reusable {
			t.Fatal("different media content reused old answer")
		}
	}
}

func TestImportLegacyObjectiveCollectionContextRequiresExactOriginalScope(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snapshot := legacyAdapterSnapshot(2)
	answers := []string{"A", "B"}
	game := "original-game"
	challenge := int64(42)
	scope := problem.HashContent(marshal(struct {
		Game      string
		Challenge int64
		Snapshot  answer.ObjectiveSnapshot
		Answers   []string
	}{game, challenge, snapshot, answers}))
	ids := []int64{legacyAdapterAnswer(t, s, snapshot.Questions[0], "A"), legacyAdapterAnswer(t, s, snapshot.Questions[1], "B")}
	for i, id := range ids {
		v := objectiveTestValidation(answers[i])
		v.ScopeHash = scope
		if err = s.RecordObjectiveValidation(ctx, id, v); err != nil {
			t.Fatal(err)
		}
	}
	before, err := s.ObjectiveLegacyCompatibilityReport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := s.ImportLegacyObjectiveContext(ctx, snapshot, "another-game", challenge, answers)
	if err != nil || wrong.AliasesAdded != 0 {
		t.Fatalf("wrong original scope accepted: %+v %v", wrong, err)
	}
	partial := snapshot
	partial.Questions = append([]answer.ObjectiveQuestion(nil), snapshot.Questions...)
	partial.Questions[0].Options = partial.Questions[0].Options[:1]
	wrong, err = s.ImportLegacyObjectiveContext(ctx, partial, game, challenge, answers)
	if err != nil || wrong.AliasesAdded != 0 {
		t.Fatalf("partial source snapshot accepted: %+v %v", wrong, err)
	}
	got, err := s.ImportLegacyObjectiveContext(ctx, snapshot, game, challenge, answers)
	if err != nil || got.AliasesAdded != 2 {
		t.Fatalf("original scope not recovered: %+v %v", got, err)
	}
	for i, id := range ids {
		requireLegacyAlias(t, s, legacyAdapterInput(snapshot, i), id)
	}
	if got.LegacyAnswers != before.LegacyAnswers || got.AnswerVerifications != before.AnswerVerifications || got.Contributions != before.Contributions {
		t.Fatalf("import rewrote original records: before=%+v after=%+v", before, got)
	}
	again, err := s.ImportLegacyObjectiveContext(ctx, snapshot, game, challenge, answers)
	if err != nil || again.AliasesAdded != 0 {
		t.Fatalf("replayed import not idempotent: %+v %v", again, err)
	}
	// A new real grading result in the current snapshot context must supersede
	// older imported collection correctness for the same exact question context.
	currentScope, err := s.PutObjectiveSnapshot(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	failure := objectiveTestValidation("A")
	failure.ScopeHash = currentScope
	failure.FullScore = false
	failure.Passed = false
	failure.Score = "0"
	if err = s.RecordObjectiveContextValidation(ctx, legacyAdapterInput(snapshot, 0), ids[0], failure); err != nil {
		t.Fatal(err)
	}
	key, _, _ := s.UpsertObjectiveQuestionWithContext(ctx, legacyAdapterInput(snapshot, 0))
	result, err := s.LookupObjectiveAnswersWithContext(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range result {
		if a.Reusable {
			t.Fatal("latest current-scope rejection ignored in favour of imported old proof")
		}
	}
}

func TestImportLegacyObjectiveContextDoesNotPromotePartialScoreEvidence(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snapshot := legacyAdapterSnapshot(1)
	answers := []string{"A"}
	id := legacyAdapterAnswer(t, s, snapshot.Questions[0], "A")
	scope := problem.HashContent(marshal(struct {
		Game      string
		Challenge int64
		Snapshot  answer.ObjectiveSnapshot
		Answers   []string
	}{"original-game", 42, snapshot, answers}))
	v := objectiveTestValidation("A")
	v.ScopeHash = scope
	v.Score = "0.5"
	v.MaxScore = "1"
	if err = s.RecordObjectiveValidation(ctx, id, v); err != nil {
		t.Fatal(err)
	}
	got, err := s.ImportLegacyObjectiveContext(ctx, snapshot, "original-game", 42, answers)
	if err != nil || got.AliasesAdded != 0 {
		t.Fatalf("partial score promoted by context recovery: %+v %v", got, err)
	}
}

func TestLegacyObjectiveNewNativeRejectionWithdrawsAllIDsWithoutClockAssumptions(t *testing.T) {
	for _, clock := range []string{"equal", "backwards"} {
		t.Run(clock, func(t *testing.T) {
			ctx := context.Background()
			s, err := Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			snapshot := legacyAdapterSnapshot(1)
			oldID := legacyAdapterAnswer(t, s, snapshot.Questions[0], "A")
			scope, c := legacyAdapterVector(t, s, snapshot, []string{"A"})
			if err = s.RecordObjectiveVerification(ctx, c.ID, "old-full-vector", "full_score", "1", "1"); err != nil {
				t.Fatal(err)
			}
			in := legacyAdapterInput(snapshot, 0)
			requireLegacyAlias(t, s, in, oldID)
			key, _, _ := s.UpsertObjectiveQuestionWithContext(ctx, in)
			nativeID, err := s.AddObjectiveAnswer(ctx, key, "A", "native-v2", "")
			if err != nil {
				t.Fatal(err)
			}
			failure := objectiveTestValidation("A")
			failure.ScopeHash = scope
			failure.Passed = false
			failure.FullScore = false
			failure.Score = "0"
			if err = s.RecordObjectiveValidation(ctx, nativeID, failure); err != nil {
				t.Fatal(err)
			}
			stamp := "2026-11-06T00:00:00Z"
			if clock == "backwards" {
				stamp = "2000-01-01T00:00:00Z"
			}
			if _, err = s.db.Exec(`UPDATE objective_answer_verifications SET created_at=?`, stamp); err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec(`UPDATE objective_verifications SET created_at='2026-11-06T00:00:00Z'`); err != nil {
				t.Fatal(err)
			}
			got, err := s.LookupObjectiveAnswersWithContext(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			for _, a := range got {
				if a.Reusable {
					t.Fatalf("old alias revived newer native rejection, clock=%s answers=%+v", clock, got)
				}
			}
			positive := objectiveTestValidation("A")
			positive.ScopeHash = scope
			if err = s.RecordObjectiveContextValidation(ctx, in, oldID, positive); err != nil {
				t.Fatal(err)
			}
			requireLegacyAlias(t, s, in, oldID)
		})
	}
}

func TestLegacyObjectiveAliasesDoNotMultiplyInventoryOrVerifiedQuestions(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snapshot := legacyAdapterSnapshot(1)
	id := legacyAdapterAnswer(t, s, snapshot.Questions[0], "A")
	scope, c := legacyAdapterVector(t, s, snapshot, []string{"A"})
	v := objectiveTestValidation("A")
	v.ScopeHash = scope
	if err = s.RecordObjectiveValidation(ctx, id, v); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordObjectiveVerification(ctx, c.ID, "full-vector", "full_score", "1", "1"); err != nil {
		t.Fatal(err)
	}
	inventory, err := s.InventorySummary(ctx)
	if err != nil || inventory.ObjectiveQuestions != 1 {
		t.Fatalf("alias multiplied inventory: %+v %v", inventory, err)
	}
	summary, err := s.Summary(ctx)
	if err != nil || summary.VerifiedObjectiveQuestions != 1 {
		t.Fatalf("alias multiplied verified count: %+v %v", summary, err)
	}
	snapshot2 := legacyAdapterSnapshot(1)
	snapshot2.Questions[0].Question = "A genuinely new question"
	_, c2 := legacyAdapterVector(t, s, snapshot2, []string{"B"})
	if err = s.RecordObjectiveVerification(ctx, c2.ID, "new-full-vector", "full_score", "1", "1"); err != nil {
		t.Fatal(err)
	}
	inventory, err = s.InventorySummary(ctx)
	if err != nil || inventory.ObjectiveQuestions != 2 {
		t.Fatalf("new extraction omitted from inventory: %+v %v", inventory, err)
	}
	summary, err = s.Summary(ctx)
	if err != nil || summary.VerifiedObjectiveQuestions != 2 {
		t.Fatalf("new extracted verified answer omitted: %+v %v", summary, err)
	}
}

func TestWholeObjectiveVectorCannotBypassNewContextAnswerRejection(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snapshot := legacyAdapterSnapshot(1)
	scope, c := legacyAdapterVector(t, s, snapshot, []string{"A"})
	if err = s.RecordObjectiveVerification(ctx, c.ID, "old-passed-vector", "full_score", "1", "1"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.FindObjectiveVerified(ctx, scope); err != nil {
		t.Fatal(err)
	}
	in := legacyAdapterInput(snapshot, 0)
	key, _, _ := s.UpsertObjectiveQuestionWithContext(ctx, in)
	id, err := s.AddObjectiveAnswer(ctx, key, "A", "new-context", "")
	if err != nil {
		t.Fatal(err)
	}
	failed := objectiveTestValidation("A")
	failed.ScopeHash = scope
	failed.Passed = false
	failed.FullScore = false
	failed.Score = "0"
	if err = s.RecordObjectiveValidation(ctx, id, failed); err != nil {
		t.Fatal(err)
	}
	if _, err = s.FindObjectiveVerified(ctx, scope); !errors.Is(err, ErrNotFound) {
		t.Fatalf("whole vector bypassed newer per-question rejection: %v", err)
	}
}
