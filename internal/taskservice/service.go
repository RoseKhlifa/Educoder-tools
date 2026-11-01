// Package taskservice is the shared in-process application boundary for HTTP
// workers and the optional CLI. It starts no subprocess and owns no credentials.
package taskservice

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"educoder-cli/internal/answer"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/questionbank"
	"educoder-cli/internal/solver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

type Provider interface {
	answer.Provider
	answer.ObjectiveProvider
}
type Config struct {
	ModelConcurrency        int
	Timeout                 time.Duration
	MaxModelCalls           int
	MaxEvaluations          int
	ProgrammingRepairRounds int
	BudgetMicroUSD          int64
}
type Request struct {
	Owner, Course, Game, IdempotencyKey string
	HomeworkID                          int64
	Mode                                solver.Mode
	// BudgetMicroUSD optionally caps this invocation. Batch callers use it to
	// pass the remaining shared budget into each child run.
	BudgetMicroUSD int64
}
type Progress struct {
	Phase    string            `json:"phase"`
	Kind     string            `json:"kind"`
	Message  string            `json:"message"`
	Batch    *BatchProgress    `json:"batch,omitempty"`
	Service  *ServiceProgress  `json:"service,omitempty"`
	Exercise *ExerciseProgress `json:"exercise,omitempty"`
}

type BatchProgress struct {
	TotalLevels     int    `json:"total_levels"`
	CompletedLevels int    `json:"completed_levels"`
	SkippedLevels   int    `json:"skipped_levels"`
	FailedLevels    int    `json:"failed_levels"`
	CurrentPosition int    `json:"current_position,omitempty"`
	CurrentTitle    string `json:"current_title,omitempty"`
}
type Result struct {
	Status           string         `json:"status"`
	Kind             string         `json:"kind"`
	RunID            string         `json:"run_id,omitempty"`
	CandidateID      int64          `json:"candidate_id,omitempty"`
	Source           string         `json:"source,omitempty"`
	ModelCalls       int            `json:"model_calls"`
	CostMicroUSD     int64          `json:"cost_micro_usd"`
	ReservedMicroUSD int64          `json:"reserved_micro_usd,omitempty"`
	Score            string         `json:"score,omitempty"`
	MaxScore         string         `json:"max_score,omitempty"`
	ScoreCheck       string         `json:"score_check,omitempty"`
	ScoreKnown       bool           `json:"score_known,omitempty"`
	Programming      *solver.Result `json:"programming,omitempty"`
	Diagnostics      []string       `json:"diagnostics,omitempty"`
}
type Service struct {
	store            *questionbank.Store
	provider         Provider
	exerciseProvider answer.ExerciseProvider
	programming      *solver.Service
	cfg              Config
	targetMu         sync.Mutex
	targets          map[string]*targetLock
}
type targetLock struct {
	sem  chan struct{}
	refs int
}

func New(store *questionbank.Store, provider Provider, cfg Config) *Service {
	if cfg.ModelConcurrency <= 0 {
		cfg.ModelConcurrency = 2
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Minute
	}
	if cfg.MaxModelCalls <= 0 || cfg.MaxModelCalls > 10 {
		cfg.MaxModelCalls = 10
	}
	if cfg.MaxEvaluations <= 0 || cfg.MaxEvaluations > 10 {
		cfg.MaxEvaluations = 10
	}
	if cfg.ProgrammingRepairRounds <= 0 || cfg.ProgrammingRepairRounds > 10 {
		cfg.ProgrammingRepairRounds = 4
	}
	if cfg.BudgetMicroUSD <= 0 || cfg.BudgetMicroUSD > 50000 {
		cfg.BudgetMicroUSD = 50000
	}
	var wrapped Provider
	var prog answer.Provider
	var exerciseProvider answer.ExerciseProvider
	if provider != nil {
		limited := &limitedProvider{provider: provider, sem: make(chan struct{}, cfg.ModelConcurrency)}
		wrapped = limited
		prog = wrapped
		if _, ok := provider.(answer.ExerciseProvider); ok {
			exerciseProvider = limited
		}
	}
	return &Service{store: store, provider: wrapped, exerciseProvider: exerciseProvider, programming: solver.New(store, prog, solver.Config{Timeout: cfg.Timeout, MaxModelCalls: cfg.MaxModelCalls, MaxEvaluations: cfg.MaxEvaluations, BudgetMicroUSD: cfg.BudgetMicroUSD}), cfg: cfg, targets: make(map[string]*targetLock)}
}
func (s *Service) lock(ctx context.Context, key string) (func(), error) {
	s.targetMu.Lock()
	l := s.targets[key]
	if l == nil {
		l = &targetLock{sem: make(chan struct{}, 1)}
		s.targets[key] = l
	}
	l.refs++
	s.targetMu.Unlock()
	releaseRef := func() {
		s.targetMu.Lock()
		l.refs--
		if l.refs == 0 {
			delete(s.targets, key)
		}
		s.targetMu.Unlock()
	}
	select {
	case l.sem <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-l.sem
			releaseRef()
			return nil, err
		}
		return func() { <-l.sem; releaseRef() }, nil
	case <-ctx.Done():
		releaseRef()
		return nil, ctx.Err()
	}
}
func (s *Service) Run(ctx context.Context, client *educoder.Client, req Request, report func(Progress)) (*Result, error) {
	if s == nil || s.store == nil || client == nil {
		return nil, errors.New("task service requires store and authenticated client")
	}
	if req.Owner == "" || req.Course == "" || req.HomeworkID <= 0 {
		return nil, errors.New("task owner and target are required")
	}
	if req.Mode != solver.ModeGenerateOnly && req.Mode != solver.ModeSelfTest && req.Mode != solver.ModeSubmit {
		return nil, errors.New("invalid execution mode")
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	emit := func(phase, kind, message string) {
		if report != nil {
			report(Progress{Phase: phase, Kind: kind, Message: message})
		}
	}
	emit("loading", "", "正在读取题目类型")
	var task *educoder.Task
	var err error
	if req.Game == "" && req.Mode == solver.ModeSubmit {
		// Enter may create the student's runtime. Resolve the assignment's
		// availability before that call when a direct request did not provide a
		// pre-resolved game identifier.
		homeworks, e := client.Homeworks(ctx, req.Course, "")
		if e != nil {
			return nil, e
		}
		var selected *educoder.Homework
		for i := range homeworks {
			if homeworks[i].ID == req.HomeworkID {
				item := homeworks[i]
				selected = &item
				break
			}
		}
		if selected == nil {
			return nil, fmt.Errorf("课堂中没有作业 %d", req.HomeworkID)
		}
		if selected.IsClosed(time.Now()) {
			return &Result{Status: "deadline_passed", Kind: "service", Diagnostics: []string{"deadline_passed"}}, nil
		}
	}
	if req.Game == "" {
		task, err = client.Enter(ctx, req.Course, req.HomeworkID, "")
	} else {
		task, err = client.LoadTask(ctx, req.Course, req.HomeworkID, req.Game)
	}
	if err != nil {
		return optionalResult(nil, err)
	}
	if task.IsOptional() {
		return &Result{Status: "skipped_optional", Kind: "service"}, nil
	}
	client, err = client.WithRequiredTasks()
	if err != nil {
		return nil, err
	}
	req.Game = task.Game.Identifier
	_ = s.store.RememberCollectionTarget(ctx, req.Owner, questionbank.CollectionTarget{Course: req.Course, HomeworkID: req.HomeworkID, Game: req.Game, ChallengeID: task.Challenge.ID, Shixun: task.Shixun.Identifier})
	// A target lock spans all mutating operations; separate accounts and tasks
	// retain concurrency, while one account cannot overwrite its own editor.
	key := req.Owner + "|" + fmt.Sprint(task.MyShixun.ID) + "|" + req.Game
	unlock, err := s.lock(ctx, key)
	if err != nil {
		return nil, err
	}
	defer unlock()
	switch task.Kind() {
	case educoder.TaskKindProgramming:
		if req.Mode == solver.ModeSubmit {
			// A direct single-level programming request bypasses RunHomework.
			// Recheck the assignment history before invoking the solver, which is
			// the first point at which a model call or write can occur.
			homeworks, e := client.Homeworks(ctx, req.Course, "")
			if e != nil {
				return nil, e
			}
			var selected *educoder.Homework
			for i := range homeworks {
				if homeworks[i].ID == req.HomeworkID {
					item := homeworks[i]
					selected = &item
					break
				}
			}
			if selected == nil {
				return nil, fmt.Errorf("课堂中没有作业 %d", req.HomeworkID)
			}
			if selected.IsClosed(time.Now()) {
				return &Result{Status: "deadline_passed", Kind: "programming", Diagnostics: []string{"deadline_passed"}}, nil
			}
		}
		adapter, adapterErr := solver.NewEducoderAdapter(client)
		if adapterErr != nil {
			return nil, adapterErr
		}
		solverReq := solver.Request{Owner: req.Owner, Course: req.Course, HomeworkID: req.HomeworkID, Game: req.Game, Mode: req.Mode, IdempotencyKey: req.IdempotencyKey, BudgetMicroUSD: req.BudgetMicroUSD, MaxModelCalls: s.cfg.MaxModelCalls, MaxEvaluations: s.cfg.MaxEvaluations, OnProgress: func(st solver.Stage) { emit(st.Phase, "programming", st.Message) }}
		if solverReq.BudgetMicroUSD <= 0 {
			solverReq.BudgetMicroUSD = s.cfg.BudgetMicroUSD
		}
		r, e := runProgrammingRecovery(ctx, s.programming, adapter, solverReq, s.cfg.ProgrammingRepairRounds, func() (bool, error) {
			homeworks, readErr := client.Homeworks(ctx, req.Course, "")
			if readErr != nil {
				return false, readErr
			}
			for _, homework := range homeworks {
				if homework.ID == req.HomeworkID {
					return !homework.IsClosed(time.Now()), nil
				}
			}
			return false, nil
		})
		if r == nil {
			return optionalResult(nil, e)
		}
		return optionalResult(&Result{Status: r.Status, Kind: "programming", RunID: r.RunID, CandidateID: r.CandidateID, Source: r.Source, ModelCalls: r.ModelCalls, CostMicroUSD: r.CostMicroUSD, ReservedMicroUSD: r.ReservedMicroUSD, Programming: publicProgramming(r), Diagnostics: r.Diagnostics}, e)
	case educoder.TaskKindChoice:
		r, e := s.runObjective(ctx, client, req, func(phase, message string) { emit(phase, "objective", message) }, req.BudgetMicroUSD)
		return optionalResult(r, e)
	default:
		return nil, errors.New("unsupported task kind")
	}
}
func runID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "objective_" + hex.EncodeToString(b[:])
}
func ownerHash(v string) string { b := sha256.Sum256([]byte(v)); return hex.EncodeToString(b[:]) }
func objectiveSnapshot(t *educoder.ChoiceTask) answer.ObjectiveSnapshot {
	s := answer.ObjectiveSnapshot{Kind: "objective", Title: t.Task.Challenge.Subject, Statement: t.Task.Challenge.Description}
	for _, q := range t.Questions {
		x := answer.ObjectiveQuestion{Position: q.Position, Question: q.Subject, QuestionType: q.QuestionType, Category: q.Category}
		for _, o := range q.Options {
			x.Options = append(x.Options, answer.ObjectiveOption{Label: string(rune('A' + o.Position)), Text: o.OptionName, Position: o.Position})
		}
		s.Questions = append(s.Questions, x)
	}
	return s
}

type objectiveState struct {
	Result Result        `json:"result"`
	Target solver.Target `json:"target"`
	Mode   solver.Mode   `json:"mode"`
}

func (s *Service) persistObjective(ctx context.Context, req Request, r *Result, created time.Time) error {
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
	}
	raw, err := json.Marshal(objectiveState{Result: *r, Target: solver.Target{Course: req.Course, HomeworkID: req.HomeworkID, Game: req.Game}, Mode: req.Mode})
	if err != nil {
		return err
	}
	return s.store.PutRun(ctx, questionbank.RunRecord{ID: r.RunID, Owner: ownerHash(req.Owner), IdempotencyKey: req.IdempotencyKey, Status: r.Status, Data: raw, CreatedAt: created, UpdatedAt: time.Now().UTC()})
}
func (s *Service) runObjective(ctx context.Context, client *educoder.Client, req Request, emit func(string, string), budget int64) (r *Result, err error) {
	if budget <= 0 {
		budget = s.cfg.BudgetMicroUSD
	}
	if req.Mode == solver.ModeSelfTest {
		return nil, errors.New("objective tasks support generate_only or submit")
	}
	if req.IdempotencyKey != "" {
		old, e := s.store.FindRunByIdempotency(ctx, ownerHash(req.Owner), req.IdempotencyKey)
		if e == nil {
			var state objectiveState
			if e = json.Unmarshal(old.Data, &state); e != nil {
				return nil, e
			}
			result := state.Result
			if state.Target != (solver.Target{Course: req.Course, HomeworkID: req.HomeworkID, Game: req.Game}) || state.Mode != req.Mode {
				return nil, errors.New("idempotency key belongs to another target or mode")
			}
			if result.Kind != "objective" {
				return nil, errors.New("idempotency key belongs to another task kind")
			}
			return &result, nil
		}
		if !errors.Is(e, questionbank.ErrNotFound) {
			return nil, e
		}
	}
	choice, err := client.LoadChoiceTask(ctx, req.Course, req.HomeworkID, req.Game)
	if err != nil {
		return nil, err
	}
	emit("loading_resources", "正在读取题干和选项中的图片")
	snapshot, resources, err := prepareObjectiveResources(ctx, client, choice)
	if err != nil {
		return &Result{Kind: "objective", Status: "resource_unavailable"}, err
	}
	hash, err := s.store.PutObjectiveSnapshot(ctx, snapshot)
	if err != nil {
		return nil, err
	}
	r = &Result{RunID: runID(), Kind: "objective", Status: "running"}
	created := time.Now().UTC()
	if err = s.persistObjective(ctx, req, r, created); err != nil {
		return r, err
	}
	defer func() {
		if errors.Is(err, educoder.ErrOptionalTask) {
			r.Status = "skipped_optional"
			r.Diagnostics = nil
		}
		if ctx.Err() != nil && r.Status != "uncertain" && r.Status != "passed" {
			r.Status = "canceled"
		}
		if e := s.persistObjective(ctx, req, r, created); e != nil && err == nil {
			err = e
		}
	}()
	emit("bank_lookup", "正在查询客观题题库")
	cached, cacheErr := s.store.FindObjectiveVerified(ctx, hash)
	if cacheErr != nil && !errors.Is(cacheErr, questionbank.ErrNotFound) {
		r.Status = "failed"
		return r, cacheErr
	}
	var previous *answer.ObjectiveCandidate
	var feedback []answer.Feedback
	attemptHistory := newObjectiveAttemptHistory(s.cfg.MaxEvaluations)
	compactOutput := false
	for attempt := 0; attempt < s.cfg.MaxEvaluations; attempt++ {
		var c answer.ObjectiveCandidate
		if attempt == 0 && cacheErr == nil {
			c = cached.Candidate
			r.CandidateID = cached.ID
			r.Source = "bank"
		} else {
			if s.provider == nil {
				r.Status = "needs_provider"
				return r, errors.New("model provider is required for a cache miss")
			}
			if r.ModelCalls >= s.cfg.MaxModelCalls {
				r.Status = "budget_exhausted"
				return r, nil
			}
			profile := answer.DefaultProfile()
			if r.ModelCalls > 0 {
				profile = answer.RepairProfile()
			}
			if r.ModelCalls >= 2 {
				profile = answer.ProProfile()
			}
			// Objective answers are compact, but reasoning-enabled repair and
			// pro profiles can consume the entire 4096-token budget before the
			// JSON answer is emitted. Leave enough room for a complete candidate.
			profile.MaxTokens = 8192
			if compactOutput || len(resources) > 0 {
				// Reasoning and answers share the output limit. If a prior
				// response was truncated, the next response uses the entire
				// allowance for the compact answer JSON.
				profile.Thinking, profile.ReasoningEffort = "disabled", ""
			}
			pr := answer.ObjectiveRequest{Problem: snapshot, Previous: previous, Feedback: attemptHistory.feedback(feedback), Profile: profile, Resources: resources}
			estimate, e := s.provider.EstimateObjective(pr)
			if e != nil {
				r.Status = "budget_unavailable"
				return r, e
			}
			if estimate.CostMicroUSD <= 0 || r.CostMicroUSD+estimate.CostMicroUSD > budget {
				r.Status = "budget_exhausted"
				return r, nil
			}
			emit("generating", "正在生成或修复客观题答案")
			generated, e := s.provider.GenerateObjective(ctx, pr)
			r.ModelCalls++
			if generated.Model == "" {
				generated.Model = profile.Model
			}
			if !generated.CostKnown && generated.CostMicroUSD < estimate.CostMicroUSD {
				generated.CostMicroUSD = estimate.CostMicroUSD
			}
			r.CostMicroUSD += generated.CostMicroUSD
			status := "unknown"
			if generated.CostKnown {
				status = "settled"
			}
			usageCtx, usageCancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			priceVersion := estimate.PriceVersion
			if priceVersion == "" {
				priceVersion = "provider-reported"
			}
			usageErr := s.store.RecordUsage(usageCtx, questionbank.Usage{RunID: r.RunID, Provider: "deepseek", Model: generated.Model, PricingVersion: priceVersion, InputTokens: generated.Usage.PromptTokens, CachedInputTokens: generated.Usage.CacheHitTokens, OutputTokens: generated.Usage.CompletionTokens, ReservedMicroUSD: estimate.CostMicroUSD, CostMicroUSD: generated.CostMicroUSD, Status: status, FinishReason: generated.FinishReason, LatencyMillis: generated.Duration.Milliseconds()})
			usageCancel()
			if usageErr != nil {
				r.Status = "usage_persistence_failed"
				return r, errors.Join(e, usageErr)
			}
			if e != nil {
				if retryableObjectiveModelError(e) && attempt+1 < s.cfg.MaxEvaluations && r.ModelCalls < s.cfg.MaxModelCalls && ctx.Err() == nil {
					compactOutput = compactOutput || answer.IsTruncatedOutput(e)
					r.Diagnostics = append(r.Diagnostics, e.Error())
					feedback = []answer.Feedback{{Kind: "model", Message: e.Error()}}
					if persistErr := s.persistObjective(ctx, req, r, created); persistErr != nil {
						r.Status = "checkpoint_persistence_failed"
						return r, persistErr
					}
					emit("generating", "模型输出未完成，正在自动重试生成")
					continue
				}
				r.Status = "model_error"
				return r, e
			}
			if r.CostMicroUSD > budget {
				r.Status = "budget_exhausted"
				return r, nil
			}
			c = generated.Candidate
			r.Source = "model"
		}
		emit("validating", "正在校验客观题答案和选项")
		if e := answer.ValidateObjectiveCandidate(snapshot, c); e != nil {
			r.Status = "invalid_candidate"
			return r, e
		}
		if attemptHistory.contains(c) {
			// Generation consumes the existing bounded budget, but a known
			// failed answer must not consume another platform submission.
			previous = &c
			feedback = []answer.Feedback{{Kind: "formal", Message: "模型重复返回了已确认未满分的完整答案，本次没有再次提交；请根据题目和选项重新推理并修正。"}}
			emit("repairing", "答案与已知未满分尝试重复，已跳过重复提交并继续修复")
			continue
		}
		candidate, e := s.store.AddObjectiveCandidate(ctx, hash, snapshot, c, r.Source)
		if e != nil {
			r.Status = "failed"
			return r, e
		}
		r.CandidateID = candidate.ID

		if req.Mode == solver.ModeGenerateOnly {
			r.Status = "generated"
			return r, nil
		}
		// Generation can take minutes. Refresh requirement metadata before the
		// first mutating request so an assignment changed to elective does not
		// receive an answer prepared while it was required. The client's bound
		// roster guard also verifies homework, game and challenge identity.
		freshTask, freshErr := client.LoadTask(ctx, req.Course, req.HomeworkID, req.Game)
		if freshErr != nil {
			r.Status = "verification_unconfirmed"
			return r, freshErr
		}
		if freshTask.IsOptional() {
			return r, educoder.ErrOptionalTask
		}
		if freshTask.Challenge.ID != choice.Task.Challenge.ID || freshTask.Kind() != educoder.TaskKindChoice {
			r.Status = "verification_unconfirmed"
			return r, errors.New("objective task identity or question kind changed before submission")
		}
		r.Status = "submitting"
		if e = s.persistObjective(ctx, req, r, created); e != nil {
			return r, e
		}
		emit("submitting", "正在提交客观题答案")
		emit("verifying", "正在复核当前关卡实际得分")
		submitted, e := client.ObjectiveSubmit(ctx, choice, c.Answers)
		if e != nil {
			r.Status = "uncertain"
			durable, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			_ = s.store.RecordObjectiveVerification(durable, r.CandidateID, r.RunID, "uncertain", "", "")
			cancel()
			return r, e
		}
		r.Score, r.MaxScore, r.ScoreKnown, r.ScoreCheck = submitted.Score, submitted.MaxScore, submitted.ScoreKnown, submitted.ScoreCheck
		if submitted.ScoreDiagnostic != "" {
			r.Diagnostics = append(r.Diagnostics, submitted.ScoreDiagnostic)
		}
		if e = s.store.RecordObjectiveVerification(ctx, r.CandidateID, r.RunID, submitted.ScoreCheck, submitted.Score, submitted.MaxScore); e != nil {
			r.Status = "verification_persistence_failed"
			return r, e
		}
		if submitted.Passed && submitted.ScoreKnown && submitted.ScoreCheck == "full_score" {
			r.Status = "passed"
			emit("bank_saved", "已记录客观题满分验证和可复用答案")
			return r, nil
		}
		if submitted.ScoreCheck != "not_full_score" {
			r.Status = "verification_unconfirmed"
			return r, nil
		}
		if submitted.ScoreKnown {
			attemptHistory.remember(c, submitted.Score, submitted.MaxScore, trustedObjectiveQuestionResults(choice, c, submitted)...)
		}
		previous = &c
		feedback = []answer.Feedback{{Kind: "formal", Message: fmt.Sprintf("本关得分 %s / %s，尚未满分，请复核全部题目和选项", submitted.Score, submitted.MaxScore)}}
		emit("repairing", "当前关卡未满分，正在根据得分自动修复")
	}
	r.Status = "attempts_exhausted"
	return r, nil
}

// retryableObjectiveModelError is limited to failures before the platform
// mutation. A truncated or malformed model response can be regenerated; an
// uncertain submit or platform evaluation must stop to avoid duplicate writes.
func retryableObjectiveModelError(err error) bool {
	return answer.IsRetryableError(err)
}

type limitedProvider struct {
	provider Provider
	sem      chan struct{}
}

func (p *limitedProvider) acquire(ctx context.Context) (func(), error) {
	select {
	case p.sem <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-p.sem
			return nil, err
		}
		return func() { <-p.sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (p *limitedProvider) Estimate(r answer.Request) (answer.Estimate, error) {
	return p.provider.Estimate(r)
}
func (p *limitedProvider) EstimateObjective(r answer.ObjectiveRequest) (answer.Estimate, error) {
	return p.provider.EstimateObjective(r)
}
func (p *limitedProvider) Generate(ctx context.Context, r answer.Request) (answer.Result, error) {
	release, e := p.acquire(ctx)
	if e != nil {
		return answer.Result{}, e
	}
	defer release()
	return p.provider.Generate(ctx, r)
}
func (p *limitedProvider) GenerateObjective(ctx context.Context, r answer.ObjectiveRequest) (answer.ObjectiveResult, error) {
	release, e := p.acquire(ctx)
	if e != nil {
		return answer.ObjectiveResult{}, e
	}
	defer release()
	return p.provider.GenerateObjective(ctx, r)
}

// Public results intentionally omit candidate source, answer letters and raw
// compiler/test output. Private bank evidence retains the complete audit trail.
func publicProgramming(in *solver.Result) *solver.Result {
	out := *in
	out.SelfTests = append([]solver.SelfTest(nil), in.SelfTests...)
	for i := range out.SelfTests {
		out.SelfTests[i].Output = ""
	}
	if out.Evaluation != nil {
		v := *out.Evaluation
		v.Feedback = ""
		out.Evaluation = &v
	}
	if len(out.SelfTests) > 64 {
		out.SelfTests = out.SelfTests[:64]
	}
	return &out
}
