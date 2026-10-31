package taskservice

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

type collectionFixture struct {
	task                   *educoder.Task
	progress               *educoder.HomeworkProgress
	files                  []educoder.CodeFile
	reads, codeReads       int
	changeTask, changeCode bool
	listErr                error
	hwOperations           []json.RawMessage
}

func (f *collectionFixture) CollectionCoursesPage(ctx context.Context, status string, page int) ([]educoder.Course, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if f.listErr != nil {
		return nil, f.listErr
	}
	if status == "processing" && page == 1 {
		return []educoder.Course{{ID: 1, Identifier: "C"}}, nil
	}
	return nil, nil
}
func (f *collectionFixture) CollectionHomeworksPage(_ context.Context, _ string, page int) ([]educoder.Homework, error) {
	if page == 1 {
		return []educoder.Homework{{ID: 1, IsShixun: true, ShixunIdentifier: "S", ChallengeCount: len(f.progress.Challenges), Operation: f.hwOperations}}, nil
	}
	return nil, nil
}
func (f *collectionFixture) HomeworkProgress(context.Context, string, int64) (*educoder.HomeworkProgress, error) {
	return f.progress, nil
}
func (f *collectionFixture) CollectionLoadTask(context.Context, string, int64, string) (*educoder.Task, error) {
	f.reads++
	b, _ := json.Marshal(f.task)
	var t educoder.Task
	json.Unmarshal(b, &t)
	if f.changeTask && f.reads > 1 {
		t.Chooses[0].Subject = "changed"
	}
	return &t, nil
}
func (f *collectionFixture) ReadProgramming(_ context.Context, t *educoder.Task) (*educoder.ProgrammingTask, error) {
	f.codeReads++
	files := append([]educoder.CodeFile(nil), f.files...)
	if f.changeCode && f.codeReads > 1 {
		files[0].Content += "\nchanged"
	}
	return &educoder.ProgrammingTask{Task: t, Files: files}, nil
}

func collectionTestSetup(t *testing.T) (*Collector, *collectionFixture, string) {
	t.Helper()
	bank, e := questionbank.Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { bank.Close() })
	x := &educoder.Task{}
	st := 1
	x.ST = &st
	x.Challenge.ST = &st
	x.Game.ID = 1
	x.Game.Identifier = "G"
	x.Game.MyShixunID = 3
	x.MyShixun.ID = 3
	x.Game.Status = 2
	x.HomeworkID = 1
	x.CourseIdentifier = "C"
	x.Challenge.ID = 9
	x.Challenge.Subject = "Q"
	x.Challenge.Description = "Choose"
	x.Shixun.Identifier = "S"
	x.Chooses = []educoder.ChoiceQuestion{{ChallengeID: 9, ChallengeChooseID: 10, Position: 1, QuestionName: "single", Subject: "Question", Category: 1, Options: []educoder.ChoiceOption{{Position: 0, OptionName: "A text"}, {Position: 1, OptionName: "B text"}}}}
	correct := 1
	yes := true
	x.ChooseTestCases = educoder.ChoiceTestCases{Count: 1, Correct: &correct, TestSets: []educoder.ChoiceTestSet{{Position: 1, QuestionName: "single", ActualOutput: "B", Result: &yes}}}
	f := &collectionFixture{task: x, progress: &educoder.HomeworkProgress{Challenges: []educoder.ChallengeProgress{{ChallengeID: 9, GameScore: "2.00", ChallengeScore: "2"}}}}
	owner := problem.HashContent("account")
	e = bank.RememberCollectionTarget(context.Background(), owner, questionbank.CollectionTarget{Course: "C", HomeworkID: 1, Game: "G", ChallengeID: 9, Shixun: "S"})
	if e != nil {
		t.Fatal(e)
	}
	return &Collector{Bank: bank, Limits: DefaultCollectionLimits()}, f, owner
}

func TestCollectionObjectiveFullScoreDedupAndOwner(t *testing.T) {
	h, f, owner := collectionTestSetup(t)
	s, e := h.Scan(context.Background(), f, owner, nil)
	if e != nil || s.Collected != 1 || s.Failed != 0 || s.Unconfirmed != 0 {
		t.Fatalf("%+v %v", s, e)
	}
	snapshot, _, reason := collectionObjective(f.task)
	if reason != "" {
		t.Fatal(reason)
	}
	hash, e := h.Bank.PutObjectiveSnapshot(context.Background(), snapshot)
	if e != nil {
		t.Fatal(e)
	}
	cached, e := h.Bank.FindObjectiveVerified(context.Background(), hash)
	if e != nil || len(cached.Candidate.Answers) != 1 || cached.Candidate.Answers[0] != "B" {
		t.Fatalf("collected answer not reusable: %+v %v", cached, e)
	}
	s, e = h.Scan(context.Background(), f, owner, nil)
	if e != nil || s.Collected != 0 || s.Duplicates != 1 {
		t.Fatalf("duplicate %+v %v", s, e)
	}
	s, e = h.Scan(context.Background(), f, problem.HashContent("another owner"), nil)
	if e != nil || s.Levels != 0 || s.Reasons["no_readonly_game_locator"] != 1 {
		t.Fatalf("owner %+v %v", s, e)
	}
	keys, _ := h.Bank.CollectionTargets(context.Background(), owner, 500)
	if len(keys) != 1 {
		t.Fatal(keys)
	}
}

func TestCollectionObjectiveRejectsUnprovenOrMismatchedAnswers(t *testing.T) {
	cases := map[string]func(*collectionFixture){
		"cross_game":      func(f *collectionFixture) { f.task.Game.Identifier = "OTHER" },
		"cross_homework":  func(f *collectionFixture) { f.task.HomeworkID = 22 },
		"cross_challenge": func(f *collectionFixture) { f.task.Chooses[0].ChallengeID = 99 },
		"cross_shixun":    func(f *collectionFixture) { f.task.Shixun.Identifier = "OTHER" },
		"partial_score":   func(f *collectionFixture) { f.progress.Challenges[0].GameScore = "1.999999" },
		"missing_score":   func(f *collectionFixture) { f.progress.Challenges[0].ChallengeScore = "" },
		"summary_only":    func(f *collectionFixture) { f.progress.Challenges = nil; f.progress.PassedCount = 1 },
		"missing_actual":  func(f *collectionFixture) { f.task.ChooseTestCases.TestSets[0].ActualOutput = nil },
		"standard_answer_only": func(f *collectionFixture) {
			f.task.ChooseTestCases.TestSets[0].ActualOutput = nil
			f.task.ChooseTestCases.TestSets[0].StandardAnswer = "B"
		},
		"had_submit_only": func(f *collectionFixture) {
			f.task.ChooseTestCases.Correct = nil
			f.task.ChooseTestCases.HadAllSubmit = true
			f.task.ChooseTestCases.HadSubmit = true
		},
		"wrong_result":      func(f *collectionFixture) { no := false; f.task.ChooseTestCases.TestSets[0].Result = &no },
		"position_mismatch": func(f *collectionFixture) { f.task.ChooseTestCases.TestSets[0].Position = 2 },
		"type_mismatch":     func(f *collectionFixture) { f.task.ChooseTestCases.TestSets[0].QuestionType = 4 },
		"duplicate_result": func(f *collectionFixture) {
			f.task.ChooseTestCases.TestSets = append(f.task.ChooseTestCases.TestSets, f.task.ChooseTestCases.TestSets[0])
		},
		"changed_answer":     func(f *collectionFixture) { f.changeTask = true },
		"unproven_image":     func(f *collectionFixture) { f.task.Chooses[0].Subject = `<img src="https://example.test/q.png">` },
		"credential_content": func(f *collectionFixture) { f.task.Chooses[0].Subject = "cookie: private-material" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			h, f, owner := collectionTestSetup(t)
			mutate(f)
			s, e := h.Scan(context.Background(), f, owner, nil)
			if e != nil {
				t.Fatal(e)
			}
			if s.Collected != 0 || s.Duplicates != 0 || s.Skipped+s.Failed == 0 {
				t.Fatalf("accepted %+v", s)
			}
		})
	}
}

func installCollectionCode(t *testing.T, h *Collector, f *collectionFixture, owner, runGame string, bind bool) {
	t.Helper()
	st := 0
	yes := true
	f.task.ST = &st
	f.task.Challenge.ST = &st
	f.task.Challenge.WithCodeFile = &yes
	f.task.Challenge.Path = "include/task.h"
	f.task.CodeEditor.Name = "C++"
	compileOK := 1
	f.task.TestSets = []educoder.TestSet{{Result: &yes, CompileSuccess: &compileOK}}
	f.files = []educoder.CodeFile{{Path: "include/task.h", Filename: "task.h", Extension: ".h", Language: "C++", Content: "int answer(){return 42;}"}}
	if !bind {
		return
	}
	snapshot, files := collectionCodeSnapshot(f.task, &educoder.ProgrammingTask{Files: f.files})
	ref, e := h.Bank.UpsertSnapshot(context.Background(), snapshot)
	if e != nil {
		t.Fatal(e)
	}
	c, e := h.Bank.AddCandidate(context.Background(), ref, questionbank.CandidateInput{Source: "model", Files: files})
	if e != nil {
		t.Fatal(e)
	}
	e = h.Bank.RecordValidation(context.Background(), questionbank.Validation{CandidateID: c.ID, Kind: "formal", Result: "passed", FilesHash: c.FilesHash, SavedFilesHash: c.FilesHash, SubmittedFilesHash: c.FilesHash, RevisionHash: ref.RevisionHash, ContractHash: ref.ContractHash, RunID: "r", Terminal: true, Complete: true, TestCount: 1, PassedCount: 1})
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(map[string]any{"target": questionbank.CollectionTarget{Course: "C", HomeworkID: 1, Game: runGame}, "snapshot_ref": ref, "result": map[string]any{"candidate_id": c.ID, "code_hash": c.FilesHash}})
	e = h.Bank.PutRun(context.Background(), questionbank.RunRecord{ID: "r", Owner: problem.HashContent(owner), Status: "passed", Data: raw, CreatedAt: time.Now(), UpdatedAt: time.Now()})
	if e != nil {
		t.Fatal(e)
	}
}

func TestCollectionProgrammingRequiresBoundFormalHash(t *testing.T) {
	for _, tc := range []struct {
		name, owner, game string
		bind, change      bool
		duplicates        int
	}{
		{"exact", "account", "G", true, false, 1},
		{"no_submitted_hash", "account", "G", false, false, 0},
		{"other_game", "account", "OTHER", true, false, 0},
		{"other_owner", "other", "G", true, false, 0},
		{"editor_changed", "account", "G", true, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, f, owner := collectionTestSetup(t)
			installCollectionCode(t, h, f, problem.HashContent(tc.owner), tc.game, tc.bind)
			f.changeCode = tc.change
			s, e := h.Scan(context.Background(), f, owner, nil)
			if e != nil || s.Duplicates != tc.duplicates || s.Collected != 0 {
				t.Fatalf("%+v %v", s, e)
			}
			if tc.duplicates == 0 && s.Unconfirmed < 1 {
				t.Fatalf("unproven %+v", s)
			}
		})
	}
}

func TestCollectionLimitsCancelAndNoRuntimeDiscovery(t *testing.T) {
	h, f, owner := collectionTestSetup(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := h.Scan(ctx, f, owner, nil); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	f.listErr = educoder.ErrCollectionLimit
	s, e := h.Scan(context.Background(), f, owner, nil)
	if !errors.Is(e, educoder.ErrCollectionLimit) || !s.LimitReached {
		t.Fatalf("%+v %v", s, e)
	}
	f.listErr = nil
	next := "G2"
	f.task.NextGame = &next
	h.Limits.Levels = 1
	s, e = h.Scan(context.Background(), f, owner, nil)
	if e != nil || !s.LimitReached || s.Levels != 1 {
		t.Fatalf("%+v %v", s, e)
	}
	h.Limits.Levels = 500
	f.task.NextGame = nil
	// An unknown seed is never used to enter a partially passed homework.
	f.progress.Challenges = append(f.progress.Challenges, educoder.ChallengeProgress{ChallengeID: 10, GameScore: "0", ChallengeScore: "2"})
	f.reads = 0
	s, e = h.Scan(context.Background(), f, problem.HashContent("new account"), nil)
	if e != nil || s.Levels != 0 || f.reads != 0 {
		t.Fatalf("%+v %v", s, e)
	}
	if got := collectionOperationGames([]json.RawMessage{json.RawMessage(`{"url":"https://host/tasks/C/1/G?sec_key=ignored"}`), json.RawMessage(`"/tasks/C/2/WRONG"`)}, "C", 1); len(got) != 1 || got[0] != "G" {
		t.Fatal(got)
	}
}
