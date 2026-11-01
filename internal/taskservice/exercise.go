package taskservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"strings"
	"time"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/questionbank"
	"educoder-cli/internal/solver"
)

type ExerciseRequest struct {
	Owner, Course, IdempotencyKey string
	ExerciseID                    int64
	Mode                          solver.Mode
	BudgetMicroUSD                int64
}
type ExerciseResult struct {
	Status             string `json:"status"`
	Kind               string `json:"kind"`
	RunID              string `json:"run_id,omitempty"`
	Title              string `json:"title"`
	Course             string `json:"course"`
	ExerciseID         int64  `json:"exercise_id"`
	ExerciseUserID     int64  `json:"exercise_user_id,omitempty"`
	TotalQuestions     int    `json:"total_questions"`
	CompletedQuestions int    `json:"completed_questions"`
	Submitted          bool   `json:"submitted"`
	ScoreKnown         bool   `json:"score_known"`
	Score              string `json:"score,omitempty"`
	MaxScore           string `json:"max_score,omitempty"`
	ReasonCode         string `json:"reason_code,omitempty"`
	ModelCalls         int    `json:"model_calls"`
	CostMicroUSD       int64  `json:"cost_micro_usd"`
	ReservedMicroUSD   int64  `json:"reserved_micro_usd,omitempty"`
}
type ExerciseProgress struct {
	ExerciseID         int64  `json:"exercise_id"`
	Title              string `json:"title"`
	TotalQuestions     int    `json:"total_questions"`
	CompletedQuestions int    `json:"completed_questions"`
}

// The service's persistent envelope is intentionally a public result plus
// target identity. Questions and generated answers stay only in this worker's
// memory and are never copied into jobs, public results or reusable verified
// answer tables merely because the platform acknowledged a submission.
type exerciseState struct {
	Result          ExerciseResult `json:"result"`
	Mode            solver.Mode    `json:"mode"`
	MutationStarted bool           `json:"mutation_started"`
	CommitStarted   bool           `json:"commit_started"`
}
type exerciseProtocol interface {
	ExercisePaper(context.Context, string, int64) (*educoder.ExercisePaper, error)
	ExerciseUserInfo(context.Context, string, int64) (*educoder.ExerciseUserInfo, error)
	ExerciseSubmission(context.Context, string, int64) (*educoder.ExerciseSubmission, error)
	ExerciseWriteGuard(context.Context, string, int64) (*educoder.ExercisePaper, error)
	SaveExerciseAnswer(context.Context, string, int64, educoder.ExerciseAnswer) (*educoder.ExerciseAnswerAck, error)
	BeginExerciseCommit(context.Context, string, int64) (*educoder.ExerciseCommitCheck, error)
	CommitExercise(context.Context, string, int64) (*educoder.ExerciseCommitResult, error)
}
type exerciseOverviewProtocol interface {
	ExerciseOverview(context.Context, string, int64) (*educoder.ExerciseOverview, error)
}

func (s *Service) RunExercise(ctx context.Context, client *educoder.Client, req ExerciseRequest, report func(Progress)) (*ExerciseResult, error) {
	if client == nil {
		return nil, errors.New("exercise requires an authenticated client")
	}
	return s.runExercise(ctx, client, req, report)
}

func (s *Service) runExercise(ctx context.Context, client exerciseProtocol, req ExerciseRequest, report func(Progress)) (r *ExerciseResult, err error) {
	if s == nil || s.store == nil || client == nil || req.Owner == "" || req.Course == "" || req.ExerciseID <= 0 {
		return nil, errors.New("exercise owner and target are required")
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		req.IdempotencyKey = "auto-exercise-" + runID()
	}
	if req.Mode == "" {
		req.Mode = solver.ModeSubmit
	}
	if req.Mode != solver.ModeSubmit && req.Mode != solver.ModeGenerateOnly {
		return nil, errors.New("exercise supports generate_only or submit")
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	unlock, e := s.lock(ctx, fmt.Sprintf("%s|exercise|%s|%d", req.Owner, req.Course, req.ExerciseID))
	if e != nil {
		return nil, e
	}
	defer unlock()
	if old, e := s.store.FindRunByIdempotency(ctx, ownerHash(req.Owner), req.IdempotencyKey); e == nil {
		var st exerciseState
		if json.Unmarshal(old.Data, &st) != nil || st.Result.Kind != "exercise" || st.Result.Course != req.Course || st.Result.ExerciseID != req.ExerciseID || st.Mode != req.Mode {
			return nil, errors.New("idempotency key belongs to another exercise target or mode")
		}
		r = &st.Result
		// Reconcile a saved/commit checkpoint using reads only. Never restart
		// generation or replay a possibly accepted exam mutation after a crash.
		if st.CommitStarted || st.MutationStarted {
			sub, readErr := client.ExerciseSubmission(ctx, req.Course, req.ExerciseID)
			if readErr == nil && exerciseSubmissionMatches(r, sub) && sub.ConfirmedSubmitted {
				applyExerciseSubmission(r, sub)
				if persistErr := s.persistExercise(ctx, req, exerciseState{Result: *r, Mode: req.Mode, MutationStarted: st.MutationStarted, CommitStarted: st.CommitStarted}, old.CreatedAt); persistErr != nil {
					r.Status = "checkpoint_persistence_failed"
					r.ReasonCode = "checkpoint_persistence_failed"
					return r, persistErr
				}
			}
		}
		if r.Status == "running" || r.Status == "saving" || r.Status == "committing" {
			r.Status = "verification_unconfirmed"
			r.ReasonCode = "previous_execution_interrupted"
		}
		return r, nil
	} else if !errors.Is(e, questionbank.ErrNotFound) {
		return nil, e
	}
	r = &ExerciseResult{Kind: "exercise", Status: "running", RunID: "exercise_" + strings.TrimPrefix(runID(), "objective_"), Course: req.Course, ExerciseID: req.ExerciseID}
	created := time.Now().UTC()
	state := exerciseState{Result: *r, Mode: req.Mode}
	persist := func() error {
		state.Result = *r
		if persistErr := s.persistExercise(ctx, req, state, created); persistErr != nil {
			r.Status = "checkpoint_persistence_failed"
			r.ReasonCode = "checkpoint_persistence_failed"
			state.Result = *r
			return persistErr
		}
		return nil
	}
	if e := persist(); e != nil {
		return r, e
	}
	defer func() {
		if ctx.Err() != nil && !r.Submitted && (r.Status == "running" || r.Status == "saving" || r.Status == "committing") {
			if state.MutationStarted {
				r.Status = "verification_unconfirmed"
				r.ReasonCode = "execution_interrupted"
			} else {
				r.Status = "canceled"
				r.ReasonCode = "execution_interrupted"
			}
		}
		if e := persist(); err == nil && e != nil {
			err = e
		}
	}()
	emit := func(phase, message string) {
		if report != nil {
			report(Progress{Phase: phase, Kind: "exercise", Message: message, Exercise: &ExerciseProgress{ExerciseID: r.ExerciseID, Title: r.Title, TotalQuestions: r.TotalQuestions, CompletedQuestions: r.CompletedQuestions}})
		}
	}
	emit("loading", "正在读取考试及本人答卷状态")
	// Submission summaries can remain readable after a teacher disables all
	// post-submission paper access. Check them first, so viewing an existing
	// result never needs a paper read, provider or writable admission state.
	if submitted, submissionErr := client.ExerciseSubmission(ctx, req.Course, req.ExerciseID); submissionErr == nil && submitted != nil && submitted.ConfirmedSubmitted && submitted.ExerciseUserID > 0 {
		r.ExerciseUserID = submitted.ExerciseUserID
		if metadata, ok := client.(exerciseOverviewProtocol); ok {
			if overview, metadataErr := metadata.ExerciseOverview(ctx, req.Course, req.ExerciseID); metadataErr == nil && overview != nil && overview.ID == req.ExerciseID && overview.ExerciseUserID == r.ExerciseUserID {
				r.Title = overview.Name
				r.TotalQuestions = overview.TotalQuestions
				if maximum, ok := exerciseScore(overview.MaxScore); ok {
					if value, _ := new(big.Rat).SetString(maximum); value.Sign() > 0 {
						r.MaxScore = maximum
					}
				}
			}
		}
		applyExerciseSubmission(r, submitted)
		return r, nil
	}
	paper, e := client.ExercisePaper(ctx, req.Course, req.ExerciseID)
	if e != nil {
		r.Status = "action_required"
		r.ReasonCode = string(educoder.ExerciseErrorCode(e))
		return r, e
	}
	if paper == nil || paper.Course != req.Course || paper.Exercise.ID != req.ExerciseID {
		r.Status = "action_required"
		r.ReasonCode = "identity_mismatch"
		return r, nil
	}
	r.Title, r.ExerciseUserID = paper.Exercise.Name, paper.Exercise.ExerciseUserID
	if maximum, ok := exerciseScore(paper.Exercise.TotalScore); ok {
		if value, _ := new(big.Rat).SetString(maximum); value.Sign() > 0 {
			r.MaxScore = maximum
		}
	}
	r.TotalQuestions = len(exerciseQuestions(paper))
	info, e := client.ExerciseUserInfo(ctx, req.Course, req.ExerciseID)
	if e != nil {
		r.Status = "action_required"
		r.ReasonCode = string(educoder.ExerciseErrorCode(e))
		return r, e
	}
	if info == nil || info.ExerciseUserID <= 0 || (r.ExerciseUserID > 0 && r.ExerciseUserID != info.ExerciseUserID) {
		r.Status = "action_required"
		r.ReasonCode = "identity_mismatch"
		return r, nil
	}
	r.ExerciseUserID = info.ExerciseUserID
	if info.IsCommit || paper.Exercise.CommitStatus == 2 {
		sub, e := client.ExerciseSubmission(ctx, req.Course, req.ExerciseID)
		if e == nil && exerciseSubmissionMatches(r, sub) && sub.ConfirmedSubmitted {
			applyExerciseSubmission(r, sub)
			return r, nil
		}
		r.Status = "verification_unconfirmed"
		r.ReasonCode = "submitted_state_unconfirmed"
		return r, e
	}
	paper, e = client.ExerciseWriteGuard(ctx, req.Course, req.ExerciseID)
	if e != nil {
		r.Status = "action_required"
		r.ReasonCode = string(educoder.ExerciseErrorCode(e))
		return r, e
	}
	if !exercisePaperMatches(r, paper) {
		r.Status = "action_required"
		r.ReasonCode = "identity_mismatch"
		return r, nil
	}
	snapshot, e := exerciseSnapshot(paper)
	if e != nil {
		r.Status = "unsupported"
		r.ReasonCode = "unsupported_question"
		var ae *answer.Error
		if errors.As(e, &ae) {
			r.ReasonCode = ae.Kind
		}
		return r, e
	}
	r.TotalQuestions = len(snapshot.Questions)
	if s.exerciseProvider == nil {
		r.Status = "needs_provider"
		r.ReasonCode = "model_provider_missing"
		return r, nil
	}
	budget := req.BudgetMicroUSD
	if budget <= 0 || budget > s.cfg.BudgetMicroUSD {
		budget = s.cfg.BudgetMicroUSD
	}
	profile := answer.DefaultProfile()
	profile.MaxTokens = 8192
	modelRequest := answer.ExerciseRequest{Problem: snapshot, Profile: profile}
	var candidate answer.ExerciseCandidate
	for {
		estimate, e := s.exerciseProvider.EstimateExercise(modelRequest)
		if e != nil {
			r.Status = "budget_unavailable"
			r.ReasonCode = "estimate_failed"
			return r, e
		}
		if estimate.CostMicroUSD <= 0 || r.CostMicroUSD+r.ReservedMicroUSD+estimate.CostMicroUSD > budget || r.ModelCalls >= s.cfg.MaxModelCalls {
			r.Status = "budget_exhausted"
			r.ReasonCode = "model_budget_exhausted"
			return r, nil
		}
		r.ReservedMicroUSD += estimate.CostMicroUSD
		r.ModelCalls++
		if e := persist(); e != nil {
			return r, e
		}
		emit("generating", "正在生成完整试卷答案，尚未保存或交卷")
		generated, modelErr := s.exerciseProvider.GenerateExercise(ctx, modelRequest)
		if generated.Model == "" {
			generated.Model = profile.Model
		}
		cost := generated.CostMicroUSD
		usageStatus := "settled"
		if generated.CostKnown {
			r.ReservedMicroUSD -= estimate.CostMicroUSD
			r.CostMicroUSD += cost
		} else {
			usageStatus = "unknown"
			cost = 0
		}
		version := estimate.PriceVersion
		if version == "" {
			version = "provider-reported"
		}
		durable, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		usageErr := s.store.RecordUsage(durable, questionbank.Usage{RunID: r.RunID, Provider: "deepseek", Model: generated.Model, PricingVersion: version, InputTokens: generated.Usage.PromptTokens, CachedInputTokens: generated.Usage.CacheHitTokens, OutputTokens: generated.Usage.CompletionTokens, ReservedMicroUSD: estimate.CostMicroUSD, CostMicroUSD: cost, Status: usageStatus, FinishReason: generated.FinishReason, LatencyMillis: generated.Duration.Milliseconds()})
		cancel()
		if usageErr != nil {
			r.Status = "usage_persistence_failed"
			r.ReasonCode = "usage_persistence_failed"
			return r, errors.Join(modelErr, usageErr)
		}
		if e := persist(); e != nil {
			return r, e
		}
		if modelErr != nil {
			if answer.IsRetryableError(modelErr) && ctx.Err() == nil && r.ModelCalls < s.cfg.MaxModelCalls {
				continue
			}
			r.Status = "model_error"
			r.ReasonCode = "model_generation_failed"
			return r, modelErr
		}
		candidate = generated.Candidate
		break
	}
	if e := answer.ValidateExerciseCandidate(snapshot, candidate); e != nil {
		r.Status = "invalid_candidate"
		r.ReasonCode = "exercise_candidate_contract"
		return r, e
	}
	if r.CostMicroUSD+r.ReservedMicroUSD > budget {
		r.Status = "budget_exhausted"
		r.ReasonCode = "model_budget_exhausted"
		return r, nil
	}
	if req.Mode == solver.ModeGenerateOnly {
		r.Status = "generated"
		return r, nil
	}
	// Re-fetch the complete paper and platform guard after generation. Question
	// identity, text, option mapping and type must still match before any save.
	fresh, e := client.ExerciseWriteGuard(ctx, req.Course, req.ExerciseID)
	if e != nil {
		r.Status = "action_required"
		r.ReasonCode = string(educoder.ExerciseErrorCode(e))
		return r, e
	}
	freshSnapshot, e := exerciseSnapshot(fresh)
	if e != nil || !exercisePaperMatches(r, fresh) || !sameExerciseSnapshot(snapshot, freshSnapshot) {
		r.Status = "verification_unconfirmed"
		r.ReasonCode = "paper_changed"
		return r, e
	}
	state.MutationStarted = true
	r.Status = "saving"
	if e := persist(); e != nil {
		return r, e
	}
	byID := map[int64]answer.ExerciseAnswer{}
	for _, a := range candidate.Answers {
		byID[a.QuestionID] = a
	}
	for _, q := range exerciseQuestions(fresh) {
		a := byID[q.ID]
		wire := educoder.ExerciseAnswer{QuestionID: q.ID, QuestionType: q.Type, ChoiceID: a.OptionID, AnswerText: a.Text, ExpectedQuestionHash: educoder.ExerciseQuestionHash(q)}
		if a.Type == "fill_blank" {
			wire.ChoiceID = 1
		}
		emit("saving", "正在逐题保存答案并读取确认")
		ack, e := client.SaveExerciseAnswer(ctx, req.Course, req.ExerciseID, wire)
		if e != nil || ack == nil || !ack.Saved {
			r.Status = "verification_unconfirmed"
			r.ReasonCode = "answer_save_unconfirmed"
			return r, e
		}
		readback, e := client.ExercisePaper(ctx, req.Course, req.ExerciseID)
		if e != nil || !exercisePaperMatches(r, readback) || !exerciseAnswerMatches(readback, q, a) {
			r.Status = "verification_unconfirmed"
			r.ReasonCode = "answer_readback_unconfirmed"
			return r, e
		}
		r.CompletedQuestions++
		if e := persist(); e != nil {
			return r, e
		}
	}
	// A final full-paper read binds every stored answer together before the
	// observed begin_commit readiness check. An ACK alone is never sufficient.
	finalPaper, e := client.ExerciseWriteGuard(ctx, req.Course, req.ExerciseID)
	if e != nil {
		r.Status = "action_required"
		r.ReasonCode = string(educoder.ExerciseErrorCode(e))
		return r, e
	}
	finalSnapshot, e := exerciseSnapshot(finalPaper)
	if e != nil || !exercisePaperMatches(r, finalPaper) || !sameExerciseSnapshot(snapshot, finalSnapshot) {
		r.Status = "verification_unconfirmed"
		r.ReasonCode = "paper_changed"
		return r, e
	}
	for _, q := range exerciseQuestions(finalPaper) {
		if !exerciseAnswerMatches(finalPaper, q, byID[q.ID]) {
			r.Status = "verification_unconfirmed"
			r.ReasonCode = "answer_readback_unconfirmed"
			return r, nil
		}
	}
	check, e := client.BeginExerciseCommit(ctx, req.Course, req.ExerciseID)
	if e != nil || check == nil {
		r.Status = "verification_unconfirmed"
		r.ReasonCode = "commit_readiness_unconfirmed"
		return r, e
	}
	if check.QuestionUndo != 0 || check.ShixunUndo != 0 || check.OJUndo != 0 {
		r.Status = "verification_unconfirmed"
		r.ReasonCode = "unanswered_questions"
		return r, nil
	}
	state.CommitStarted = true
	r.Status = "committing"
	if e := persist(); e != nil {
		return r, e
	}
	emit("submitting", "正在交卷并核验本人答卷状态")
	committed, e := client.CommitExercise(ctx, req.Course, req.ExerciseID)
	if e == nil && committed != nil && committed.Submission != nil && exerciseSubmissionMatches(r, committed.Submission) && committed.Submission.ConfirmedSubmitted {
		applyExerciseSubmission(r, committed.Submission)
		return r, nil
	}
	// Transport loss is not permission to POST again. Read the current answer
	// record once, including when the commit method returned an error.
	reconcileCtx, reconcileCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer reconcileCancel()
	sub, readErr := client.ExerciseSubmission(reconcileCtx, req.Course, req.ExerciseID)
	if readErr == nil && exerciseSubmissionMatches(r, sub) && sub.ConfirmedSubmitted {
		applyExerciseSubmission(r, sub)
		return r, nil
	}
	r.Status = "verification_unconfirmed"
	r.ReasonCode = "submission_uncertain"
	return r, errors.Join(e, readErr)
}

func (s *Service) persistExercise(ctx context.Context, req ExerciseRequest, state exerciseState, created time.Time) error {
	durable, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	raw, e := json.Marshal(state)
	if e != nil {
		return e
	}
	return s.store.PutRun(durable, questionbank.RunRecord{ID: state.Result.RunID, Owner: ownerHash(req.Owner), IdempotencyKey: req.IdempotencyKey, Status: state.Result.Status, Data: raw, CreatedAt: created, UpdatedAt: time.Now().UTC()})
}
func exerciseQuestions(p *educoder.ExercisePaper) []educoder.ExerciseQuestion {
	if p == nil {
		return nil
	}
	if len(p.Questions) > 0 {
		return p.Questions
	}
	var out []educoder.ExerciseQuestion
	for _, g := range p.QuestionTypes {
		out = append(out, g.Questions...)
	}
	return out
}
func exerciseSnapshot(p *educoder.ExercisePaper) (answer.ExerciseSnapshot, error) {
	s := answer.ExerciseSnapshot{Kind: "exercise"}
	if p == nil {
		return s, errors.New("exercise paper missing")
	}
	s.Title = p.Exercise.Name
	for index, q := range exerciseQuestions(p) {
		typeName := "unsupported"
		switch q.Type {
		case 0:
			typeName = "single"
		case 2:
			typeName = "judgment"
		case 3:
			typeName = "fill_blank"
		}
		item := answer.ExerciseQuestion{ID: q.ID, Position: index + 1, Type: typeName, Title: q.Title, BlankCount: q.BlankCount}
		for _, o := range q.Choices {
			item.Options = append(item.Options, answer.ExerciseOption{ID: o.ID, Text: o.Text})
		}
		s.Questions = append(s.Questions, item)
	}
	return s, answer.ValidateExerciseSnapshot(s)
}
func sameExerciseSnapshot(a, b answer.ExerciseSnapshot) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}
func exerciseAnswerMatches(p *educoder.ExercisePaper, expected educoder.ExerciseQuestion, a answer.ExerciseAnswer) bool {
	if p == nil {
		return false
	}
	for _, q := range exerciseQuestions(p) {
		if q.ID != expected.ID {
			continue
		}
		if q.Type != expected.Type || q.Title != expected.Title || len(q.Choices) != len(expected.Choices) {
			return false
		}
		if a.Type == "fill_blank" {
			return q.Type == 3 && q.BlankCount == 1 && expected.BlankCount == 1 && a.Text != nil && exerciseBlankReadbackMatches(q.UserAnswer, *a.Text)
		}
		selected := int64(0)
		for i, c := range q.Choices {
			if c.ID != expected.Choices[i].ID || c.Text != expected.Choices[i].Text {
				return false
			}
			if c.UserAnswer {
				if selected != 0 {
					return false
				}
				selected = c.ID
			}
		}
		return selected == a.OptionID
	}
	return false
}

// The published student renderer reads user_answer as objects containing
// choice_id and answer_text; its successful save callback writes precisely
// {choice_id:slot,answer_text:text}. This source evidence establishes the
// schema, while acceptance still requires a fresh server paper read containing
// the exact text. The capture alone only demonstrated the save ACK.
func exerciseBlankReadbackMatches(raw json.RawMessage, expected string) bool {
	if len(raw) == 0 || len(raw) > 512<<10 {
		return false
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	open, err := d.Token()
	if err != nil || open != json.Delim('[') || !d.More() {
		return false
	}
	open, err = d.Token()
	if err != nil || open != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	var slot int64
	var text *string
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return false
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return false
		}
		seen[key] = true
		switch key {
		case "choice_id":
			if d.Decode(&slot) != nil {
				return false
			}
		case "answer_text":
			if d.Decode(&text) != nil {
				return false
			}
		default:
			return false
		}
	}
	close, err := d.Token()
	if err != nil || close != json.Delim('}') || d.More() {
		return false
	}
	close, err = d.Token()
	if err != nil || close != json.Delim(']') {
		return false
	}
	if _, err = d.Token(); err != io.EOF {
		return false
	}
	return len(seen) == 2 && slot == 1 && text != nil && *text == expected
}
func exerciseSubmissionMatches(r *ExerciseResult, s *educoder.ExerciseSubmission) bool {
	return s != nil && r.ExerciseUserID > 0 && s.ExerciseUserID == r.ExerciseUserID
}
func exercisePaperMatches(r *ExerciseResult, p *educoder.ExercisePaper) bool {
	return p != nil && p.Course == r.Course && p.Exercise.ID == r.ExerciseID && p.Exercise.ExerciseUserID == r.ExerciseUserID
}

var exerciseDecimal = regexp.MustCompile(`^(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$`)

func exerciseScore(raw json.RawMessage) (string, bool) {
	var text string
	if len(raw) > 0 && raw[0] == '"' {
		if json.Unmarshal(raw, &text) != nil {
			return "", false
		}
	} else {
		text = string(raw)
	}
	if len(text) > 128 || !exerciseDecimal.MatchString(text) {
		return "", false
	}
	return text, true
}
func applyExerciseSubmission(r *ExerciseResult, s *educoder.ExerciseSubmission) {
	r.Submitted = true
	r.Status = "submitted"
	r.ReasonCode = ""
	// Submission is not evidence that every question was answered. Retain only
	// saves whose values this run actually read back; historical summary-only
	// results intentionally keep this counter at zero.
	r.ScoreKnown = false
	r.Score = ""
	if maximum, ok := exerciseScore(s.MaxScore); ok {
		if value, _ := new(big.Rat).SetString(maximum); value.Sign() > 0 {
			r.MaxScore = maximum
		}
	}
	if !s.OpenTotalScore {
		r.Status = "awaiting_review"
		return
	}
	text, ok := exerciseScore(s.Score)
	if !ok {
		r.Status = "awaiting_review"
		return
	}
	r.ScoreKnown = true
	r.Score = text
	r.Status = "graded"
	score, _ := new(big.Rat).SetString(text)
	if maximum, ok := new(big.Rat).SetString(r.MaxScore); ok && maximum.Sign() > 0 && score.Cmp(maximum) == 0 {
		r.Status = "fullscore"
	}
}
