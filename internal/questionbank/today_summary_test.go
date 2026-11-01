package questionbank

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
)

func completionTestTime(t *testing.T, raw string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func addObjectiveCompletion(t *testing.T, s *Store, id, owner, game, mode, status, stamp string) int64 {
	t.Helper()
	ctx := context.Background()
	q := objectiveTestQuestion()
	q2 := q
	q2.Position, q2.Question = 2, "Second question"
	snapshot := answer.ObjectiveSnapshot{Kind: "objective", Questions: []answer.ObjectiveQuestion{q, q2}}
	key, err := s.PutObjectiveSnapshot(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.AddObjectiveCandidate(ctx, key, snapshot, answer.ObjectiveCandidate{Kind: "objective", Answers: []string{"A", "A"}}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if id != "" {
		raw, _ := json.Marshal(map[string]any{"mode": mode, "target": completionTarget{Course: "COURSE", HomeworkID: 1, Game: game}, "result": map[string]any{"run_id": id, "kind": "objective", "status": status, "candidate_id": c.ID}})
		// UpdatedAt intentionally has a different day: the aggregate must use
		// the append-only verification time rather than mutable run timestamps.
		if err := s.PutRun(ctx, RunRecord{ID: id, Owner: owner, Status: status, Data: raw, UpdatedAt: completionTestTime(t, "2026-11-30T00:00:00Z")}); err != nil {
			t.Fatal(err)
		}
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO objective_verifications(candidate_id,run_id,result,score,max_score,created_at) VALUES(?,?,?,?,?,?)`, c.ID, id, "full_score", "2", "2", stamp)
	if err != nil {
		t.Fatal(err)
	}
	idnum, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return idnum
}

func addProgrammingCompletion(t *testing.T, s *Store, id, owner, game, mode, status, stamp string) int64 {
	t.Helper()
	ctx := context.Background()
	ref, err := s.UpsertSnapshot(ctx, bankSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.AddCandidate(ctx, ref, CandidateInput{Files: []problem.SolutionFile{{Path: "src/main.cpp", Content: "int main() {}"}}, Source: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"mode": mode, "target": completionTarget{Course: "COURSE", HomeworkID: 1, Game: game}, "result": map[string]any{"run_id": id, "status": status, "candidate_id": c.ID}})
	if err := s.PutRun(ctx, RunRecord{ID: id, Owner: owner, Status: status, Data: raw}); err != nil {
		t.Fatal(err)
	}
	v := Validation{CandidateID: c.ID, RunID: id, Kind: "formal", Result: "passed", FilesHash: c.FilesHash, SavedFilesHash: c.FilesHash, SubmittedFilesHash: c.FilesHash, RevisionHash: c.RevisionHash, ContractHash: c.ContractHash, Terminal: true, Complete: true, TestCount: 2, PassedCount: 2}
	proof, _ := json.Marshal(v)
	res, err := s.db.ExecContext(ctx, `INSERT INTO verifications(solution_id,evidence_json,created_at) VALUES(?,?,?)`, c.ID, proof, stamp)
	if err != nil {
		t.Fatal(err)
	}
	n, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTodaySummaryCountsVerifiedStudentsAndIndividualQuestionsWithoutListLimit(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	stamp := "2026-11-02T04:00:00Z"
	owners := map[string]struct{}{"student-a": {}, "student-b": {}}
	addObjectiveCompletion(t, s, "objective-a", "student-a", "choices", "submit", "passed", stamp)
	addObjectiveCompletion(t, s, "objective-retry", "student-a", "choices", "submit", "passed", stamp)
	addObjectiveCompletion(t, s, "objective-b", "student-b", "choices", "submit", "passed", stamp)
	addProgrammingCompletion(t, s, "program-a", "student-a", "code", "submit", "passed", stamp)
	addProgrammingCompletion(t, s, "program-retry", "student-a", "code", "submit", "passed", stamp)
	// More than both UI list limits; the service must read every qualifying
	// verification, not the latest page of jobs or the question-bank total.
	for i := 0; i < 130; i++ {
		addProgrammingCompletion(t, s, fmt.Sprintf("many-%d", i), "student-b", fmt.Sprintf("code-%d", i), "submit", "passed", stamp)
	}
	got, err := s.TodaySummary(context.Background(), completionTestTime(t, stamp), owners)
	if err != nil || got != (TodaySummary{Date: "2026-11-02", Timezone: "Asia/Shanghai", CompletedStudents: 2, CompletedQuestions: 135}) {
		t.Fatalf("distinct completions = %+v, %v", got, err)
	}
	if got, err = s.TodaySummary(context.Background(), completionTestTime(t, stamp), nil); err != nil || got.CompletedStudents != 0 || got.CompletedQuestions != 0 {
		t.Fatalf("unlinked service owners leaked into student count: %+v %v", got, err)
	}
}

func TestTodaySummaryUsesShanghaiNaturalDayAndVerificationTimestamp(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i, stamp := range []string{"2026-11-01T15:59:59.999999999Z", "2026-11-01T16:00:00Z", "2026-11-01T16:00:00.000000001Z", "2026-11-02T15:59:59.999999999Z", "2026-11-02T16:00:00Z"} {
		addProgrammingCompletion(t, s, fmt.Sprintf("boundary-%d", i), "student-a", fmt.Sprintf("game-%d", i), "submit", "passed", stamp)
	}
	owners := map[string]struct{}{"student-a": {}}
	got, err := s.TodaySummary(context.Background(), completionTestTime(t, "2026-11-02T15:59:00Z"), owners)
	if err != nil || got.CompletedQuestions != 3 || got.CompletedStudents != 1 || got.Date != "2026-11-02" {
		t.Fatalf("Shanghai boundaries = %+v %v", got, err)
	}
	got, err = s.TodaySummary(context.Background(), completionTestTime(t, "2026-11-02T16:00:00Z"), owners)
	if err != nil || got.CompletedQuestions != 1 || got.Date != "2026-11-03" {
		t.Fatalf("next Shanghai day = %+v %v", got, err)
	}
}

func TestTodaySummaryExcludesCollectionSkipsUnconfirmedAndUngradedExams(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	stamp := "2026-11-02T04:00:00Z"
	for _, c := range []struct{ id, owner, mode, status string }{
		{"collection-mode", "student-a", "collect_verified", "passed"},
		{"generated", "student-a", "generate_only", "passed"},
		{"selftest", "student-a", "selftest", "passed"},
		{"skipped", "student-a", "submit", "skipped"},
		{"optional", "student-a", "submit", "skipped_optional"},
		{"unconfirmed", "student-a", "submit", "verification_unconfirmed"},
		{"failed", "student-a", "submit", "failed"},
		{"unlinked-cli", "unlinked-owner", "submit", "passed"},
	} {
		addObjectiveCompletion(t, s, c.id, c.owner, c.id, c.mode, c.status, stamp)
	}
	addObjectiveCompletion(t, s, "", "student-a", "collector-with-no-run", "", "", stamp)
	partial := addObjectiveCompletion(t, s, "partial-score", "student-a", "partial-score", "submit", "passed", stamp)
	if _, err := s.db.Exec(`UPDATE objective_verifications SET score='1' WHERE id=?`, partial); err != nil {
		t.Fatal(err)
	}
	incomplete := addProgrammingCompletion(t, s, "incomplete-code", "student-a", "incomplete-code", "submit", "passed", stamp)
	if _, err := s.db.Exec(`UPDATE verifications SET evidence_json=json_set(evidence_json,'$.complete',json('false')) WHERE id=?`, incomplete); err != nil {
		t.Fatal(err)
	}
	wrongHash := addProgrammingCompletion(t, s, "hash-mismatch", "student-a", "hash-mismatch", "submit", "passed", stamp)
	if _, err := s.db.Exec(`UPDATE verifications SET evidence_json=json_set(evidence_json,'$.submitted_files_hash','other') WHERE id=?`, wrongHash); err != nil {
		t.Fatal(err)
	}
	selftest := addProgrammingCompletion(t, s, "only-selftest", "student-a", "only-selftest", "submit", "passed", stamp)
	if _, err := s.db.Exec(`UPDATE verifications SET evidence_json=json_set(evidence_json,'$.kind','selftest') WHERE id=?`, selftest); err != nil {
		t.Fatal(err)
	}
	// A saved/handed-in exam is not evidence of graded correct answers.
	exam := json.RawMessage(`{"mode":"submit","target":{"course":"COURSE","exercise_id":1},"result":{"run_id":"exam","status":"submitted","submitted":true,"total_questions":50,"completed_questions":50}}`)
	if err := s.PutRun(context.Background(), RunRecord{ID: "exam", Owner: "student-a", Status: "submitted", Data: exam}); err != nil {
		t.Fatal(err)
	}
	got, err := s.TodaySummary(context.Background(), completionTestTime(t, stamp), map[string]struct{}{"student-a": {}})
	if err != nil || got.CompletedQuestions != 0 || got.CompletedStudents != 0 {
		t.Fatalf("non-completion evidence counted as actual work: %+v %v", got, err)
	}
}

func TestTodaySummaryErrorsDoNotFabricateEmptyResults(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.TodaySummary(ctx, time.Now(), nil); err == nil {
		t.Fatal("canceled query returned a fabricated empty summary")
	}
	s.Close()
	if _, err := s.TodaySummary(context.Background(), time.Now(), nil); err == nil {
		t.Fatal("failed DB returned a fabricated empty summary")
	}
}
