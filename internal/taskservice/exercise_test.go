package taskservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/solver"
)

type exerciseFake struct {
	paper                                        educoder.ExercisePaper
	guardErr                                     error
	guardCount, saves, commits, subReads, checks int
	paperReads                                   int
	forbidPaper                                  bool
	saved                                        []educoder.ExerciseAnswer
	readbackMismatch                             bool
	commitUnknown                                bool
	commitUnconfirmed                            bool
	initiallyCommitted                           bool
	changeAfterGeneration                        bool
}

func syntheticExercisePaper() educoder.ExercisePaper {
	return educoder.ExercisePaper{Course: "SYNTHCOURSE", Exercise: educoder.ExerciseSettings{ID: 51, ExerciseUserID: 97, Name: "Synthetic paper"}, Questions: []educoder.ExerciseQuestion{{ID: 101, Type: 0, Position: 1, Title: "Select the first", Choices: []educoder.ExerciseChoice{{ID: 1001, Text: "First"}, {ID: 1009, Text: "Other"}}}}}
}
func (x *exerciseFake) ExercisePaper(context.Context, string, int64) (*educoder.ExercisePaper, error) {
	x.paperReads++
	if x.forbidPaper {
		return nil, errors.New("teacher disabled submitted paper")
	}
	// Round-trip to avoid test mutations aliasing the baseline snapshot.
	raw, _ := json.Marshal(x.paper)
	var p educoder.ExercisePaper
	_ = json.Unmarshal(raw, &p)
	for _, saved := range x.saved {
		for i := range p.Questions {
			if p.Questions[i].ID == saved.QuestionID {
				if p.Questions[i].Type == 3 && saved.AnswerText != nil && !x.readbackMismatch {
					p.Questions[i].UserAnswer, _ = json.Marshal([]map[string]any{{"choice_id": saved.ChoiceID, "answer_text": *saved.AnswerText}})
				}
				for j := range p.Questions[i].Choices {
					p.Questions[i].Choices[j].UserAnswer = !x.readbackMismatch && p.Questions[i].Choices[j].ID == saved.ChoiceID
				}
			}
		}
	}
	return &p, nil
}
func (x *exerciseFake) ExerciseOverview(context.Context, string, int64) (*educoder.ExerciseOverview, error) {
	return &educoder.ExerciseOverview{ID: 51, ExerciseUserID: 97, Name: x.paper.Exercise.Name, TotalQuestions: len(x.paper.Questions), MaxScore: json.RawMessage(`100`)}, nil
}
func (x *exerciseFake) ExerciseUserInfo(context.Context, string, int64) (*educoder.ExerciseUserInfo, error) {
	return &educoder.ExerciseUserInfo{ExerciseUserID: 97, IsCommit: x.initiallyCommitted}, nil
}
func (x *exerciseFake) ExerciseSubmission(context.Context, string, int64) (*educoder.ExerciseSubmission, error) {
	x.subReads++
	return &educoder.ExerciseSubmission{ExerciseUserID: 97, CommitStatus: 2, ConfirmedSubmitted: x.initiallyCommitted || (x.commits > 0 && !x.commitUnconfirmed)}, nil
}
func (x *exerciseFake) ExerciseWriteGuard(ctx context.Context, c string, id int64) (*educoder.ExercisePaper, error) {
	x.guardCount++
	if x.guardErr != nil {
		return nil, x.guardErr
	}
	p, _ := x.ExercisePaper(ctx, c, id)
	if x.changeAfterGeneration && x.guardCount > 1 {
		p.Questions[0].Title = "Changed question"
	}
	return p, nil
}
func (x *exerciseFake) SaveExerciseAnswer(_ context.Context, _ string, _ int64, a educoder.ExerciseAnswer) (*educoder.ExerciseAnswerAck, error) {
	x.saves++
	x.saved = append(x.saved, a)
	return &educoder.ExerciseAnswerAck{Saved: true}, nil
}
func (x *exerciseFake) BeginExerciseCommit(context.Context, string, int64) (*educoder.ExerciseCommitCheck, error) {
	x.checks++
	return &educoder.ExerciseCommitCheck{}, nil
}
func (x *exerciseFake) CommitExercise(context.Context, string, int64) (*educoder.ExerciseCommitResult, error) {
	x.commits++
	if x.commitUnknown {
		return nil, errors.New("transport unavailable")
	}
	return &educoder.ExerciseCommitResult{Acknowledged: true, Submission: &educoder.ExerciseSubmission{ExerciseUserID: 97, ConfirmedSubmitted: !x.commitUnconfirmed}}, nil
}

type exerciseModel struct {
	fakeProvider
	calls     int
	unknown   bool
	err       error
	candidate *answer.ExerciseCandidate
}

func (p *exerciseModel) EstimateExercise(answer.ExerciseRequest) (answer.Estimate, error) {
	return answer.Estimate{CostMicroUSD: 10, Model: "synthetic", PriceVersion: "synthetic-v1"}, nil
}
func (p *exerciseModel) GenerateExercise(_ context.Context, req answer.ExerciseRequest) (answer.ExerciseResult, error) {
	p.calls++
	c := answer.ExerciseCandidate{Kind: "exercise"}
	for _, q := range req.Problem.Questions {
		a := answer.ExerciseAnswer{QuestionID: q.ID, Type: q.Type}
		if q.Type == "fill_blank" {
			s := "AbCd  X"
			a.Text = &s
		} else {
			a.OptionID = q.Options[0].ID
		}
		c.Answers = append(c.Answers, a)
	}
	if p.candidate != nil {
		c = *p.candidate
	}
	return answer.ExerciseResult{Candidate: c, Model: "synthetic", CostKnown: !p.unknown, CostMicroUSD: 2, Usage: answer.Usage{Known: !p.unknown, PromptTokens: 10, CompletionTokens: 5}, FinishReason: "stop"}, p.err
}
func exerciseReq() ExerciseRequest {
	return ExerciseRequest{Owner: "synthetic-owner", Course: "SYNTHCOURSE", ExerciseID: 51, IdempotencyKey: "synthetic-key", Mode: solver.ModeSubmit}
}
func exerciseService(t *testing.T, p *exerciseModel) *Service {
	t.Helper()
	s := New(bank(t), p, Config{})
	return s
}

func TestExerciseAlreadyCommittedIsReadOnly(t *testing.T) {
	p := &exerciseModel{}
	s := exerciseService(t, p)
	x := &exerciseFake{paper: syntheticExercisePaper(), initiallyCommitted: true}
	r, e := s.runExercise(context.Background(), x, exerciseReq(), nil)
	if e != nil || r.Status != "awaiting_review" || !r.Submitted || r.ScoreKnown || p.calls != 0 || x.guardCount != 0 || x.saves != 0 || x.commits != 0 {
		t.Fatalf("result=%+v err=%v model=%d writes=%d/%d", r, e, p.calls, x.saves, x.commits)
	}
	raw, _ := json.Marshal(r)
	if strings.Contains(string(raw), "answers") || strings.Contains(string(raw), "Select the first") {
		t.Fatalf("private question leaked: %s", raw)
	}
}

func TestExerciseCommittedNeedsNoConfiguredModel(t *testing.T) {
	s := New(bank(t), nil, Config{})
	x := &exerciseFake{paper: syntheticExercisePaper(), initiallyCommitted: true, forbidPaper: true}
	r, e := s.runExercise(context.Background(), x, exerciseReq(), nil)
	if e != nil || !r.Submitted || x.guardCount != 0 || x.saves != 0 || x.commits != 0 || x.paperReads != 0 || r.TotalQuestions != 1 || r.CompletedQuestions != 0 || r.Title != "Synthetic paper" || r.MaxScore != "100" {
		t.Fatalf("result=%+v err=%v", r, e)
	}
}

func TestExerciseEmptyIdempotencyKeyIsGeneratedByService(t *testing.T) {
	p := &exerciseModel{}
	s := exerciseService(t, p)
	x := &exerciseFake{paper: syntheticExercisePaper(), initiallyCommitted: true}
	req := exerciseReq()
	req.IdempotencyKey = ""
	r, e := s.runExercise(context.Background(), x, req, nil)
	if e != nil || !r.Submitted {
		t.Fatalf("result=%+v err=%v", r, e)
	}
	stored, e := s.store.GetRun(context.Background(), r.RunID)
	if e != nil || !strings.HasPrefix(stored.IdempotencyKey, "auto-exercise-") {
		t.Fatalf("stored=%+v err=%v", stored, e)
	}
}

func TestExerciseCheckpointFailureHasStableOperatorReason(t *testing.T) {
	p := &exerciseModel{}
	s := exerciseService(t, p)
	x := &exerciseFake{paper: syntheticExercisePaper()}
	// Closing after initial lookup but before persist is hard to inject through
	// the concrete store. A public progress callback supplies that exact point:
	// loading is emitted after the initial checkpoint and before another save.
	r, e := s.runExercise(context.Background(), x, exerciseReq(), func(p Progress) {
		if p.Phase == "loading" {
			_ = s.store.Close()
		}
	})
	if e == nil || r == nil || r.Status != "checkpoint_persistence_failed" || r.ReasonCode != "checkpoint_persistence_failed" || x.saves != 0 || x.commits != 0 {
		t.Fatalf("result=%+v err=%v saves=%d commits=%d", r, e, x.saves, x.commits)
	}
}

func TestExerciseStudentStatusIsNotCommitStatus(t *testing.T) {
	p := &exerciseModel{}
	s := exerciseService(t, p)
	paper := syntheticExercisePaper()
	paper.Exercise.StudentCommitStatus = 2
	x := &exerciseFake{paper: paper}
	r, e := s.runExercise(context.Background(), x, exerciseReq(), nil)
	if e != nil || !r.Submitted || p.calls != 1 || x.saves != 1 || x.commits != 1 {
		t.Fatalf("result=%+v err=%v calls=%d saves=%d commits=%d", r, e, p.calls, x.saves, x.commits)
	}
}
func TestExerciseUnknownAndProtectedNeverGenerateOrWrite(t *testing.T) {
	for _, scenario := range []string{"unsupported", "guard_locked", "guard_monitor", "media", "multiblank"} {
		t.Run(scenario, func(t *testing.T) {
			p := &exerciseModel{}
			s := exerciseService(t, p)
			x := &exerciseFake{paper: syntheticExercisePaper()}
			switch scenario {
			case "unsupported":
				x.paper.Questions[0].Type = 6
			case "guard_locked", "guard_monitor":
				x.guardErr = errors.New("platform action required")
			case "media":
				x.paper.Questions[0].Title = `<img src="https://example.invalid/image.png">`
			case "multiblank":
				x.paper.Questions[0].Type = 3
				x.paper.Questions[0].Choices = nil
				x.paper.Questions[0].BlankCount = 2
			}
			r, e := s.runExercise(context.Background(), x, exerciseReq(), nil)
			if e == nil || r == nil || p.calls != 0 || x.saves != 0 || x.commits != 0 {
				t.Fatalf("result=%+v error=%v model=%d writes=%d/%d", r, e, p.calls, x.saves, x.commits)
			}
		})
	}
}
func TestExerciseValidatesWholeCandidateBeforeAnySave(t *testing.T) {
	p := &exerciseModel{candidate: &answer.ExerciseCandidate{Kind: "exercise", Answers: []answer.ExerciseAnswer{{QuestionID: 999, Type: "single", OptionID: 1001}}}}
	s := exerciseService(t, p)
	x := &exerciseFake{paper: syntheticExercisePaper()}
	r, e := s.runExercise(context.Background(), x, exerciseReq(), nil)
	if e == nil || r.Status != "invalid_candidate" || x.saves != 0 || x.commits != 0 {
		t.Fatalf("result=%+v err=%v saves=%d", r, e, x.saves)
	}
}
func TestExercisePaperChangeBeforeSaveStops(t *testing.T) {
	p := &exerciseModel{}
	s := exerciseService(t, p)
	x := &exerciseFake{paper: syntheticExercisePaper(), changeAfterGeneration: true}
	r, e := s.runExercise(context.Background(), x, exerciseReq(), nil)
	if e != nil || r.ReasonCode != "paper_changed" || x.saves != 0 || x.commits != 0 {
		t.Fatalf("result=%+v err=%v", r, e)
	}
}
func TestExerciseSaveAcknowledgementWithoutReadbackStops(t *testing.T) {
	p := &exerciseModel{}
	s := exerciseService(t, p)
	x := &exerciseFake{paper: syntheticExercisePaper(), readbackMismatch: true}
	r, e := s.runExercise(context.Background(), x, exerciseReq(), nil)
	if e != nil || r.Status != "verification_unconfirmed" || r.ReasonCode != "answer_readback_unconfirmed" || r.CompletedQuestions != 0 || x.saves != 1 || x.commits != 0 {
		t.Fatalf("result=%+v err=%v saves=%d commits=%d", r, e, x.saves, x.commits)
	}
	// Replaying the same key only reconciles, it does not repeat the save.
	r, e = s.runExercise(context.Background(), x, exerciseReq(), nil)
	if e != nil || x.saves != 1 || x.commits != 0 || p.calls != 1 {
		t.Fatalf("replayed side effects: result=%+v err=%v", r, e)
	}
}
func TestExerciseFillBlankPreservesCaseAndRequiresFreshReadback(t *testing.T) {
	p := &exerciseModel{}
	s := exerciseService(t, p)
	paper := syntheticExercisePaper()
	paper.Questions[0].Type = 3
	paper.Questions[0].BlankCount = 1
	paper.Questions[0].Choices = nil
	x := &exerciseFake{paper: paper}
	r, e := s.runExercise(context.Background(), x, exerciseReq(), nil)
	if e != nil || !r.Submitted || r.ScoreKnown || r.CompletedQuestions != 1 || x.commits != 1 || len(x.saved) != 1 || x.saved[0].ChoiceID != 1 || x.saved[0].AnswerText == nil || *x.saved[0].AnswerText != "AbCd  X" {
		t.Fatalf("result=%+v err=%v saved=%+v", r, e, x.saved)
	}
}

func TestExerciseBlankReadbackStrictPublishedShape(t *testing.T) {
	if !exerciseBlankReadbackMatches(json.RawMessage(`[{"choice_id":1,"answer_text":"AbCd  X"}]`), "AbCd  X") {
		t.Fatal("exact published shape not accepted")
	}
	for name, raw := range map[string]string{
		"case_changed":       `[{"choice_id":1,"answer_text":"ABCD  X"}]`,
		"whitespace_changed": `[{"choice_id":1,"answer_text":"AbCd X"}]`,
		"wrong_slot":         `[{"choice_id":2,"answer_text":"AbCd  X"}]`,
		"string_slot":        `[{"choice_id":"1","answer_text":"AbCd  X"}]`,
		"null_slot":          `[{"choice_id":null,"answer_text":"AbCd  X"}]`,
		"null_text":          `[{"choice_id":1,"answer_text":null}]`,
		"duplicate_slot":     `[{"choice_id":1,"answer_text":"AbCd  X"},{"choice_id":1,"answer_text":"AbCd  X"}]`,
		"duplicate_key":      `[{"choice_id":1,"choice_id":1,"answer_text":"AbCd  X"}]`,
		"unknown_field":      `[{"choice_id":1,"answer_text":"AbCd  X","answer":true}]`,
		"missing_field":      `[{"answer_text":"AbCd  X"}]`,
		"unknown_shape":      `{"choice_id":1,"answer_text":"AbCd  X"}`,
		"empty":              `[]`, "null": `null`, "trailing_json": `[{"choice_id":1,"answer_text":"AbCd  X"}][]`,
	} {
		t.Run(name, func(t *testing.T) {
			if exerciseBlankReadbackMatches(json.RawMessage(raw), "AbCd  X") {
				t.Fatalf("accepted %s", raw)
			}
		})
	}
}

func TestExerciseWholePaperSingleJudgmentAndFillCommitAfterReadback(t *testing.T) {
	p := &exerciseModel{}
	s := exerciseService(t, p)
	paper := syntheticExercisePaper()
	paper.Questions = append(paper.Questions, educoder.ExerciseQuestion{ID: 102, Type: 2, Position: 2, Title: "Judge", Choices: []educoder.ExerciseChoice{{ID: 2001, Text: "True"}, {ID: 2009, Text: "False"}}}, educoder.ExerciseQuestion{ID: 103, Type: 3, Position: 3, Title: "Text", BlankCount: 1})
	x := &exerciseFake{paper: paper}
	r, e := s.runExercise(context.Background(), x, exerciseReq(), nil)
	if e != nil || !r.Submitted || r.Status != "awaiting_review" || r.ScoreKnown || r.CompletedQuestions != 3 || x.saves != 3 || x.commits != 1 || p.calls != 1 {
		t.Fatalf("result=%+v err=%v saves=%d commits=%d", r, e, x.saves, x.commits)
	}
	for i, wire := range x.saved {
		if wire.ExpectedQuestionHash == "" || wire.ExpectedQuestionHash != educoder.ExerciseQuestionHash(paper.Questions[i]) {
			t.Fatalf("unbound question save: %+v", wire)
		}
	}
}

func TestExerciseBlankAckWithoutSavedValueNeverCommits(t *testing.T) {
	p := &exerciseModel{}
	s := exerciseService(t, p)
	paper := syntheticExercisePaper()
	paper.Questions[0].Type = 3
	paper.Questions[0].BlankCount = 1
	paper.Questions[0].Choices = nil
	x := &exerciseFake{paper: paper, readbackMismatch: true}
	r, e := s.runExercise(context.Background(), x, exerciseReq(), nil)
	if e != nil || r.Submitted || r.CompletedQuestions != 0 || r.ReasonCode != "answer_readback_unconfirmed" || x.saves != 1 || x.commits != 0 {
		t.Fatalf("result=%+v err=%v saves=%d commits=%d", r, e, x.saves, x.commits)
	}
}
func TestExerciseUnknownCommitOnlyReconcilesAndReplayIsReadOnly(t *testing.T) {
	p := &exerciseModel{}
	s := exerciseService(t, p)
	x := &exerciseFake{paper: syntheticExercisePaper(), commitUnknown: true}
	r, e := s.runExercise(context.Background(), x, exerciseReq(), nil)
	if e != nil || !r.Submitted || r.Status != "awaiting_review" || x.commits != 1 || x.saves != 1 || p.calls != 1 || r.ModelCalls != 1 || r.CostMicroUSD != 2 {
		t.Fatalf("result=%+v err=%v model=%d saves=%d commits=%d", r, e, p.calls, x.saves, x.commits)
	}
	usage, e := s.store.UsageForRun(context.Background(), r.RunID)
	if e != nil || len(usage) != 1 || usage[0].CostMicroUSD != 2 || usage[0].Status != "settled" {
		t.Fatalf("usage=%+v err=%v", usage, e)
	}
	stored, e := s.store.GetRun(context.Background(), r.RunID)
	if e != nil || stored.Owner != ownerHash(exerciseReq().Owner) || strings.Contains(string(stored.Data), "Select the first") || strings.Contains(string(stored.Data), `"option_id"`) || strings.Contains(string(stored.Data), `"answers"`) {
		t.Fatalf("persistent checkpoint: %+v err=%v", stored, e)
	}
	r, e = s.runExercise(context.Background(), x, exerciseReq(), nil)
	if e != nil || !r.Submitted || x.commits != 1 || x.saves != 1 || p.calls != 1 {
		t.Fatal("replayed model or mutation")
	}
	wrong := exerciseReq()
	wrong.ExerciseID++
	if _, e = s.runExercise(context.Background(), x, wrong, nil); e == nil {
		t.Fatal("idempotency target crossing accepted")
	}
}
func TestExerciseUnconfirmedCommitNotAutomaticallyRepeated(t *testing.T) {
	p := &exerciseModel{}
	s := exerciseService(t, p)
	x := &exerciseFake{paper: syntheticExercisePaper(), commitUnknown: true, commitUnconfirmed: true}
	r, _ := s.runExercise(context.Background(), x, exerciseReq(), nil)
	if r.Submitted || r.ReasonCode != "submission_uncertain" || x.commits != 1 {
		t.Fatalf("result=%+v commits=%d", r, x.commits)
	}
	_, _ = s.runExercise(context.Background(), x, exerciseReq(), nil)
	if x.commits != 1 || p.calls != 1 {
		t.Fatal("uncertain commit automatically repeated")
	}
}
func TestExerciseUnknownProviderUsageRetainsReservation(t *testing.T) {
	p := &exerciseModel{unknown: true}
	s := exerciseService(t, p)
	x := &exerciseFake{paper: syntheticExercisePaper()}
	req := exerciseReq()
	req.Mode = solver.ModeGenerateOnly
	r, e := s.runExercise(context.Background(), x, req, nil)
	if e != nil || r.Status != "generated" || r.CostMicroUSD != 0 || r.ReservedMicroUSD != 10 || x.saves != 0 || x.commits != 0 {
		t.Fatalf("result=%+v err=%v", r, e)
	}
	u, e := s.store.UsageForRun(context.Background(), r.RunID)
	if e != nil || len(u) != 1 || u[0].Status != "unknown" || u[0].ReservedMicroUSD != 10 || u[0].CostMicroUSD != 0 {
		t.Fatalf("usage=%+v err=%v", u, e)
	}
}

func TestExercisePublishedExactMaximumRequiredForFullscore(t *testing.T) {
	r := ExerciseResult{Kind: "exercise", TotalQuestions: 2, MaxScore: "100"}
	applyExerciseSubmission(&r, &educoder.ExerciseSubmission{ConfirmedSubmitted: true, OpenTotalScore: true, Score: json.RawMessage(`"100.00"`)})
	if r.Status != "fullscore" || !r.ScoreKnown {
		t.Fatalf("result=%+v", r)
	}
	for _, raw := range []string{`"1/2"`, `"1e99999999"`, `"-1"`, `"NaN"`, `"--"`, `null`} {
		copy := r
		applyExerciseSubmission(&copy, &educoder.ExerciseSubmission{OpenTotalScore: true, Score: json.RawMessage(raw)})
		if copy.ScoreKnown || copy.Status != "awaiting_review" {
			t.Fatalf("accepted score %s: %+v", raw, copy)
		}
	}
}
func TestExerciseProgressAndScoreNeverClaimsUnprovedFullscore(t *testing.T) {
	p := &exerciseModel{}
	s := exerciseService(t, p)
	x := &exerciseFake{paper: syntheticExercisePaper()}
	var phases []string
	r, e := s.runExercise(context.Background(), x, exerciseReq(), func(p Progress) {
		if p.Exercise == nil || p.Exercise.ExerciseID != 51 {
			t.Error("missing typed exercise progress")
		}
		phases = append(phases, p.Phase)
	})
	if e != nil || !r.Submitted || len(phases) < 3 {
		t.Fatalf("result=%+v error=%v phases=%v", r, e, phases)
	}
	for _, score := range []string{`"--"`, `null`, `"100"`, `"99.9"`} {
		copy := *r
		applyExerciseSubmission(&copy, &educoder.ExerciseSubmission{OpenTotalScore: true, Score: json.RawMessage(score)})
		if copy.Status == "fullscore" {
			t.Fatal("unproved maximum")
		}
		if score == `"100"` && (!copy.ScoreKnown || copy.Status != "graded") {
			t.Fatal(fmt.Sprintf("score=%+v", copy))
		}
	}
}
