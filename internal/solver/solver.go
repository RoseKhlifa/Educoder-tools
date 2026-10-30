// Package solver coordinates problem snapshots, answer generation, local
// validation, and the observed Educoder programming protocol.  Runtime
// credentials and upstream execution keys are intentionally confined to the
// Adapter implementation and never enter this package's persisted records.
package solver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

// Mode controls how far a run proceeds.
type Mode string

const (
	ModeGenerateOnly Mode = "generate_only"
	ModeSelfTest     Mode = "selftest"
	ModeSubmit       Mode = "submit"
)

// Request identifies a single challenge. Owner is an opaque local account
// identifier (a hash is preferable); it is never interpreted as a credential.
type Request struct {
	Owner          string
	IdempotencyKey string
	Course         string
	HomeworkID     int64
	Game           string
	Mode           Mode
	MaxModelCalls  int
	MaxEvaluations int
	BudgetMicroUSD int64
	Timeout        time.Duration
	OnProgress     func(Stage) `json:"-"`
}

// Stage reports a public lifecycle event to server and CLI callers.
type Stage struct{ Phase, Message string }

func stage(req Request, phase, message string) {
	if req.OnProgress != nil {
		req.OnProgress(Stage{phase, message})
	}
}

// Config contains bounded defaults. The service never retries upstream
// mutations; retries are limited to model generation and feedback repair.
type Config struct {
	MaxModelCalls  int
	MaxEvaluations int
	BudgetMicroUSD int64
	Timeout        time.Duration
	Profile        answer.Profile
	RepairProfile  answer.Profile
	ProProfile     answer.Profile
}

func defaultConfig() Config {
	return Config{MaxModelCalls: 3, MaxEvaluations: 3, BudgetMicroUSD: 50000, Timeout: 5 * time.Minute,
		Profile: answer.DefaultProfile(), RepairProfile: answer.RepairProfile(), ProProfile: answer.ProProfile()}
}

// Adapter is the small execution boundary needed by the state machine. A
// production implementation wraps educoder.Client; tests use a deterministic
// fake. The adapter owns sessions, cookies, websocket URLs and sec_key values.
type Adapter interface {
	Load(ctx context.Context, target Target) (LoadedProblem, error)
	SelfTest(ctx context.Context, target Target, file problem.SolutionFile, input, expected string) (SelfTest, error)
	Submit(ctx context.Context, target Target, file problem.SolutionFile) (Evaluation, error)
	Progress(ctx context.Context, target Target) (Progress, error)
}

type Target struct {
	Course     string
	HomeworkID int64
	Game       string
}

type LoadedProblem struct {
	Snapshot problem.Snapshot
	// Key is a per-runtime lock key such as myshixun ID. It is not persisted.
	Key string
}

type SelfTest struct {
	Completed bool
	Output    string
	Matches   bool
	CompileOK *bool
}

type Evaluation struct {
	Completed   bool
	Passed      bool
	Status      string
	Feedback    string
	TestCount   int
	PassedCount int
}

type Progress struct {
	PassedCount       int
	ChallengeStatuses []int
}

// Result is safe for API responses: it contains no upstream credentials.
type Result struct {
	RunID          string      `json:"run_id"`
	Status         string      `json:"status"`
	CandidateID    int64       `json:"candidate_id,omitempty"`
	Source         string      `json:"source,omitempty"`
	CodeHash       string      `json:"code_hash,omitempty"`
	ModelCalls     int         `json:"model_calls"`
	CostMicroUSD   int64       `json:"cost_micro_usd"`
	SelfTestStatus string      `json:"self_test_status,omitempty"`
	ProgressError  string      `json:"progress_error,omitempty"`
	Diagnostics    []string    `json:"diagnostics,omitempty"`
	SelfTests      []SelfTest  `json:"self_tests,omitempty"`
	Evaluation     *Evaluation `json:"evaluation,omitempty"`
	Progress       *Progress   `json:"progress,omitempty"`
}

// Store is implemented by questionbank.DB. It is kept as an interface to
// allow an in-memory fake in tests and to keep SQLite details out of solver.
type Store interface {
	UpsertSnapshot(context.Context, problem.Snapshot) (questionbank.Ref, error)
	Lookup(context.Context, questionbank.Ref) ([]questionbank.Candidate, error)
	AddCandidate(context.Context, questionbank.Ref, questionbank.CandidateInput) (questionbank.Candidate, error)
	RecordValidation(context.Context, questionbank.Validation) error
	PutRun(context.Context, questionbank.RunRecord) error
	GetRun(context.Context, string) (questionbank.RunRecord, error)
	FindRunByIdempotency(context.Context, string, string) (questionbank.RunRecord, error)
	RecordUsage(context.Context, questionbank.Usage) error
}

type Service struct {
	store       Store
	provider    answer.Provider
	cfg         Config
	lockMu      sync.Mutex
	locks       map[string]*runtimeLock
	generateMu  sync.Mutex
	generations map[string]*generationCall
}

type generationCall struct {
	done   chan struct{}
	result answer.Result
	err    error
}

func New(store Store, provider answer.Provider, cfg Config) *Service {
	d := defaultConfig()
	if cfg.MaxModelCalls > 0 {
		d.MaxModelCalls = cfg.MaxModelCalls
	}
	if cfg.MaxEvaluations > 0 {
		d.MaxEvaluations = cfg.MaxEvaluations
	}
	if cfg.BudgetMicroUSD > 0 {
		d.BudgetMicroUSD = cfg.BudgetMicroUSD
	}
	if cfg.Timeout > 0 {
		d.Timeout = cfg.Timeout
	}
	if cfg.Profile.Model != "" {
		d.Profile = cfg.Profile
	}
	if cfg.RepairProfile.Model != "" {
		d.RepairProfile = cfg.RepairProfile
	}
	if cfg.ProProfile.Model != "" {
		d.ProProfile = cfg.ProProfile
	}
	return &Service{store: store, provider: provider, cfg: d, generations: make(map[string]*generationCall), locks: make(map[string]*runtimeLock)}
}

// generateOnce coalesces concurrent model requests for the same problem,
// candidate and feedback. The caller still records its own attempt, while the
// provider sees one billable generation.
func (s *Service) generateOnce(ctx context.Context, key string, req answer.Request) (answer.Result, error) {
	s.generateMu.Lock()
	if call := s.generations[key]; call != nil {
		s.generateMu.Unlock()
		select {
		case <-call.done:
			return call.result, call.err
		case <-ctx.Done():
			return answer.Result{}, ctx.Err()
		}
	}
	call := &generationCall{done: make(chan struct{})}
	s.generations[key] = call
	s.generateMu.Unlock()
	call.result, call.err = s.provider.Generate(ctx, req)
	s.generateMu.Lock()
	delete(s.generations, key)
	close(call.done)
	s.generateMu.Unlock()
	return call.result, call.err
}

type runtimeLock struct {
	sem  chan struct{}
	refs int
}

func (s *Service) lock(ctx context.Context, key string) (func(), error) {
	if key == "" {
		key = "unknown"
	}
	s.lockMu.Lock()
	l := s.locks[key]
	if l == nil {
		l = &runtimeLock{sem: make(chan struct{}, 1)}
		s.locks[key] = l
	}
	l.refs++
	s.lockMu.Unlock()
	releaseRef := func() {
		s.lockMu.Lock()
		l.refs--
		if l.refs == 0 {
			delete(s.locks, key)
		}
		s.lockMu.Unlock()
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

func validMode(m Mode) bool { return m == ModeGenerateOnly || m == ModeSelfTest || m == ModeSubmit }

type runState struct {
	Result         Result            `json:"result"`
	Target         Target            `json:"target"`
	Mode           Mode              `json:"mode"`
	Owner          string            `json:"owner,omitempty"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
	SnapshotRef    questionbank.Ref  `json:"snapshot_ref"`
	CandidateIDs   []int64           `json:"candidate_ids,omitempty"`
	Feedback       []answer.Feedback `json:"feedback,omitempty"`
	Attempts       int               `json:"attempts"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

func ownerKey(owner string) string {
	s := sha256.Sum256([]byte(owner))
	return hex.EncodeToString(s[:])
}

func (s *Service) persist(ctx context.Context, id string, state runState, status string, created time.Time) error {
	state.UpdatedAt = time.Now().UTC()
	state = sanitizeRunState(state)
	b, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
	}
	return s.store.PutRun(ctx, questionbank.RunRecord{ID: id, Owner: state.Owner, IdempotencyKey: state.IdempotencyKey, Status: status, Data: b, CreatedAt: created, UpdatedAt: state.UpdatedAt})
}

func sanitizeRunState(state runState) runState {
	for i, d := range state.Result.Diagnostics {
		if problem.ValidatePublicText(d) != nil {
			state.Result.Diagnostics[i] = "diagnostic withheld by public-data validator"
		}
	}
	for i := range state.Result.SelfTests {
		if problem.ValidatePublicText(state.Result.SelfTests[i].Output) != nil {
			state.Result.SelfTests[i].Output = "output withheld by public-data validator"
		}
	}
	if state.Result.Evaluation != nil && problem.ValidatePublicText(state.Result.Evaluation.Feedback) != nil {
		state.Result.Evaluation.Feedback = "evaluation feedback withheld by public-data validator"
	}
	return state
}

// Run executes one challenge. It keeps every generated candidate and every
// validation result in the bank, including failed and uncertain attempts.
func (s *Service) Run(ctx context.Context, adapter Adapter, req Request) (*Result, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("solver store is required")
	}
	if adapter == nil {
		return nil, errors.New("solver adapter is required")
	}
	if req.Owner == "" {
		return nil, errors.New("solver owner is required")
	}
	if req.Course == "" || req.HomeworkID <= 0 || req.Game == "" {
		return nil, errors.New("solver target is incomplete")
	}
	if !validMode(req.Mode) {
		return nil, errors.New("solver mode must be generate_only, selftest, or submit")
	}
	if req.MaxModelCalls <= 0 {
		req.MaxModelCalls = s.cfg.MaxModelCalls
	}
	if req.MaxEvaluations <= 0 {
		req.MaxEvaluations = s.cfg.MaxEvaluations
	}
	if req.BudgetMicroUSD <= 0 {
		req.BudgetMicroUSD = s.cfg.BudgetMicroUSD
	}
	if req.Timeout <= 0 {
		req.Timeout = s.cfg.Timeout
	}
	if req.IdempotencyKey != "" {
		if old, err := s.store.FindRunByIdempotency(ctx, ownerKey(req.Owner), req.IdempotencyKey); err == nil && old.ID != "" {
			var previous runState
			if err := json.Unmarshal(old.Data, &previous); err != nil {
				return nil, err
			}
			if previous.Target != (Target{req.Course, req.HomeworkID, req.Game}) || (previous.Mode != "" && previous.Mode != req.Mode) {
				return nil, errors.New("idempotency key belongs to another target or mode")
			}
			return decodeRun(old)
		} else if err != nil && !errors.Is(err, questionbank.ErrNotFound) {
			return nil, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()
	stage(req, "loading", "正在读取题目和编辑器文件")
	loaded, err := adapter.Load(ctx, Target{req.Course, req.HomeworkID, req.Game})
	if err != nil {
		return nil, err
	}
	snap, err := problem.Prepare(loaded.Snapshot)
	if err != nil {
		return nil, fmt.Errorf("prepare problem: %w", err)
	}
	ref, err := s.store.UpsertSnapshot(ctx, snap)
	if err != nil {
		return nil, err
	}
	runID := newRunID()
	result := &Result{RunID: runID, Status: "running"}
	state := runState{Result: *result, Mode: req.Mode, Target: Target{req.Course, req.HomeworkID, req.Game}, Owner: ownerKey(req.Owner), IdempotencyKey: req.IdempotencyKey, SnapshotRef: ref}
	created := time.Now().UTC()
	if err := s.store.PutRun(ctx, questionbank.RunRecord{ID: runID, Owner: ownerKey(req.Owner), IdempotencyKey: req.IdempotencyKey, Status: "running", Data: mustJSON(state), CreatedAt: created, UpdatedAt: created}); err != nil {
		return nil, err
	}
	unlock, err := s.lock(ctx, loaded.Key)
	if err != nil {
		result.Status = "canceled"
		state.Result = *result
		_ = s.persist(ctx, runID, state, result.Status, created)
		return result, err
	}
	defer unlock()
	defer func() {
		if ctx.Err() != nil && result.Status != "uncertain" && result.Status != "passed" {
			result.Status = "canceled"
			state.Result = *result
			_ = s.persist(ctx, runID, state, result.Status, created)
		}
	}()
	return s.execute(ctx, adapter, req, snap, ref, state, result, created)
}

func newRunID() string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	return fmt.Sprintf("run_%d_%s", time.Now().UnixNano(), hex.EncodeToString(sum[:8]))
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func generationKey(req answer.Request) string {
	// The snapshot contains only public problem data. Hashing the complete
	// request keeps the dedupe key bounded while separating repair feedback and
	// model profiles.
	b, _ := json.Marshal(req)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func decodeRun(record questionbank.RunRecord) (*Result, error) {
	var st runState
	if err := json.Unmarshal(record.Data, &st); err != nil {
		return nil, err
	}
	st.Result.Status = record.Status
	return &st.Result, nil
}

func (s *Service) execute(ctx context.Context, adapter Adapter, req Request, snap problem.Snapshot, ref questionbank.Ref, state runState, result *Result, created time.Time) (*Result, error) {
	target := state.Target
	stage(req, "bank_lookup", "正在查询题库候选")
	var candidates, err = s.store.Lookup(ctx, ref)
	if err != nil {
		return nil, err
	}
	// Reuse only candidates with formal upstream evidence.
	verified := candidates[:0]
	for _, c := range candidates {
		if c.Status == "upstream_verified" {
			verified = append(verified, c)
		}
	}
	candidates = verified
	// Revalidate only the best reusable candidate first. If it fails, feed its
	// concrete output back to the model instead of spending all attempts on
	// stale candidates; every other candidate remains queryable in the bank.
	if len(candidates) > 1 {
		candidates = candidates[:1]
	}
	var previous *answer.Candidate
	feedback := append([]answer.Feedback(nil), state.Feedback...)
	if len(feedback) > 0 && len(candidates) > 0 {
		seed := answer.Candidate{Kind: "programming", Language: languageOf(snap), Files: candidates[0].Files}
		previous = &seed
	}
	for attempt := 0; attempt < req.MaxEvaluations; attempt++ {
		state.Attempts = attempt + 1
		var candidate answer.Candidate
		var bankID int64
		if attempt < len(candidates) && len(feedback) == 0 {
			bc := candidates[attempt]
			bankID = bc.ID
			candidate = answer.Candidate{Kind: "programming", Language: languageOf(snap), Files: bc.Files}
			result.Source = bc.Source
			result.CandidateID = bankID
		} else {
			if s.provider == nil {
				result.Status = "needs_provider"
				result.Diagnostics = append(result.Diagnostics, "题库没有可用候选且未配置模型 provider")
				state.Result = *result
				_ = s.persist(ctx, result.RunID, state, result.Status, created)
				return result, errors.New("solver provider is required for a cache miss")
			}
			if result.ModelCalls >= req.MaxModelCalls {
				result.Status = "budget_exhausted"
				result.Diagnostics = append(result.Diagnostics, "model call budget exhausted")
				state.Result = *result
				_ = s.persist(ctx, result.RunID, state, result.Status, created)
				return result, nil
			}
			profile := s.cfg.Profile
			if result.ModelCalls > 0 {
				profile = s.cfg.RepairProfile
			}
			if result.ModelCalls >= 2 {
				profile = s.cfg.ProProfile
			}
			providerReq := answer.Request{Problem: snap, Previous: previous, Feedback: feedback, Profile: profile}
			estimate, ee := s.provider.Estimate(providerReq)
			if ee != nil {
				result.Status = "budget_unavailable"
				result.Diagnostics = append(result.Diagnostics, ee.Error())
				state.Result = *result
				_ = s.persist(ctx, result.RunID, state, result.Status, created)
				return result, ee
			}
			if estimate.CostMicroUSD <= 0 || result.CostMicroUSD+estimate.CostMicroUSD > req.BudgetMicroUSD {
				result.Status = "budget_exhausted"
				result.Diagnostics = append(result.Diagnostics, "model cost budget exhausted before request")
				state.Result = *result
				_ = s.persist(ctx, result.RunID, state, result.Status, created)
				return result, nil
			}
			stage(req, "generating", "正在生成或修复候选答案")
			gen, ge := s.generateOnce(ctx, generationKey(providerReq), providerReq)
			if gen.Model == "" {
				gen.Model = profile.Model
			}
			if !gen.CostKnown && gen.CostMicroUSD < estimate.CostMicroUSD {
				gen.CostMicroUSD = estimate.CostMicroUSD
			}
			result.ModelCalls++
			result.CostMicroUSD += gen.CostMicroUSD
			usageStatus := "unknown"
			if gen.CostKnown {
				usageStatus = "settled"
			}
			usageCtx := ctx
			usageCancel := func() {}
			if ctx.Err() != nil {
				usageCtx, usageCancel = context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			}
			usageErr := s.store.RecordUsage(usageCtx, questionbank.Usage{RunID: result.RunID, CandidateID: 0, Provider: "deepseek", Model: gen.Model, PricingVersion: "provider-reported", InputTokens: gen.Usage.PromptTokens, CachedInputTokens: gen.Usage.CacheHitTokens, OutputTokens: gen.Usage.CompletionTokens, ReservedMicroUSD: estimate.CostMicroUSD, CostMicroUSD: gen.CostMicroUSD, Status: usageStatus, FinishReason: gen.FinishReason, LatencyMillis: gen.Duration.Milliseconds()})
			usageCancel()
			if usageErr != nil {
				result.Status = "usage_persistence_failed"
				result.Diagnostics = append(result.Diagnostics, "模型用量记录失败")
				state.Result = *result
				_ = s.persist(ctx, result.RunID, state, result.Status, created)
				return result, usageErr
			}
			if ge != nil {
				// Providers may return a partially parsed candidate together with a
				// truncation/usage error. Preserve it as a bank candidate so a later
				// resume can inspect or repair it; never silently discard output.
				if len(gen.Candidate.Files) > 0 {
					result.CodeHash, _ = problem.HashFiles(gen.Candidate.Files)
					if bc, ae := s.store.AddCandidate(ctx, ref, questionbank.CandidateInput{Files: gen.Candidate.Files, Source: "model_error", Provider: "deepseek", Model: gen.Model, PromptVersion: "v1"}); ae == nil {
						result.CandidateID = bc.ID
					}
				}
				result.Diagnostics = append(result.Diagnostics, ge.Error())
				result.Status = "model_error"
				state.Result = *result
				_ = s.persist(ctx, result.RunID, state, result.Status, created)
				return result, ge
			}
			candidate = gen.Candidate
			result.Source = "model"
			result.CodeHash, _ = problem.HashFiles(candidate.Files)
			bc, ae := s.store.AddCandidate(ctx, ref, questionbank.CandidateInput{Files: candidate.Files, Source: "model", Provider: "deepseek", Model: gen.Model, PromptVersion: "v1"})
			if ae != nil {
				return nil, ae
			}
			bankID = bc.ID
			result.CandidateID = bankID
			previous = &candidate
			if result.CostMicroUSD > req.BudgetMicroUSD {
				result.Status = "budget_exhausted"
				result.Diagnostics = append(result.Diagnostics, "model cost exceeded configured budget")
				state.Result = *result
				_ = s.persist(ctx, result.RunID, state, result.Status, created)
				return result, nil
			}
		}
		stage(req, "validating", "正在校验候选答案结构")
		if err := validateCandidate(snap, candidate); err != nil {
			result.Diagnostics = append(result.Diagnostics, err.Error())
			feedback = []answer.Feedback{{Kind: "schema", Message: err.Error()}}
			previous = &candidate
			continue
		}
		result.CodeHash, _ = problem.HashFiles(candidate.Files)
		if req.Mode == ModeGenerateOnly {
			result.Status = "generated"
			state.Result = *result
			state.CandidateIDs = append(state.CandidateIDs, bankID)
			_ = s.persist(ctx, result.RunID, state, result.Status, created)
			return result, nil
		}
		if len(candidate.Files) != 1 {
			result.Status = "unsupported_multifile"
			result.Diagnostics = append(result.Diagnostics, "首版执行适配器只支持单文件代码题")
			state.Result = *result
			_ = s.persist(ctx, result.RunID, state, result.Status, created)
			return result, nil
		}
		result.SelfTests = nil
		result.SelfTestStatus = "skipped"
		if len(snap.PublicTests) > 0 {
			result.SelfTestStatus = "running"
		}
		stage(req, "saving", "准备保存候选代码")
		stage(req, "selftesting", "正在执行公开自测")
		allPass := true
		feedback = nil
		for _, test := range snap.PublicTests {
			st, se := adapter.SelfTest(ctx, target, candidate.Files[0], test.Stdin, test.ExpectedStdout)
			result.SelfTests = append(result.SelfTests, st)
			ok := se == nil && st.Completed && st.Matches
			if !ok {
				allPass = false
				msg := st.Output
				if se != nil {
					msg = se.Error()
				}
				feedback = append(feedback, answer.Feedback{Kind: "selftest", Message: msg, Stdin: test.Stdin, ExpectedStdout: test.ExpectedStdout, ActualStdout: st.Output})
			}
			if ve := s.store.RecordValidation(ctx, questionbank.Validation{CandidateID: bankID, Kind: "selftest", Result: map[bool]string{true: "passed", false: "failed"}[ok], FilesHash: result.CodeHash, RevisionHash: snap.RevisionHash, ContractHash: snap.ContractHash, Feedback: feedbackString(feedback), RunID: result.RunID, Terminal: true, Complete: st.Completed, TestCount: 1, PassedCount: boolInt(ok)}); ve != nil {
				result.Diagnostics = append(result.Diagnostics, "selftest evidence persistence failed")
			}
			if !ok {
				break
			}
		}
		state.CandidateIDs = append(state.CandidateIDs, bankID)
		state.Feedback = append([]answer.Feedback(nil), feedback...)
		state.Result = *result
		_ = s.persist(ctx, result.RunID, state, "running", created)
		if !allPass {
			result.SelfTestStatus = "failed"
			if ctx.Err() != nil {
				result.Status = "canceled"
				state.Result = *result
				_ = s.persist(ctx, result.RunID, state, result.Status, created)
				return result, ctx.Err()
			}
			previous = &candidate
			if result.ModelCalls >= req.MaxModelCalls {
				result.Status = "selftest_failed"
				state.Result = *result
				_ = s.persist(ctx, result.RunID, state, result.Status, created)
				return result, nil
			}
			continue
		}
		if len(snap.PublicTests) > 0 {
			result.SelfTestStatus = "passed"
			if !allPass {
				result.SelfTestStatus = "failed"
			}
		}
		if req.Mode == ModeSelfTest {
			state.Feedback = nil
			result.Status = "selftest_passed"
			if len(snap.PublicTests) == 0 {
				result.Status = "selftest_skipped"
				result.Diagnostics = append(result.Diagnostics, "题目没有可执行的公开样例")
			}
			state.Result = *result
			_ = s.persist(ctx, result.RunID, state, result.Status, created)
			return result, nil
		}
		state.Result = *result
		if err := s.persist(ctx, result.RunID, state, "submitting", created); err != nil {
			result.Status = "persistence_failed"
			return result, err
		}
		stage(req, "submitting", "正在保存代码并提交评测")
		stage(req, "evaluating", "等待正式评测结果")
		eval, ee := adapter.Submit(ctx, target, candidate.Files[0])
		result.Evaluation = &eval
		passed := ee == nil && eval.Completed && eval.Passed
		kind := "formal"
		vr := "failed"
		if passed {
			vr = "passed"
		}
		if ee != nil {
			vr = "unknown"
			result.Diagnostics = append(result.Diagnostics, ee.Error())
		}
		evidenceCtx, evidenceCancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		ve := s.store.RecordValidation(evidenceCtx, questionbank.Validation{CandidateID: bankID, Kind: kind, Result: vr, FilesHash: result.CodeHash, SavedFilesHash: result.CodeHash, SubmittedFilesHash: result.CodeHash, RevisionHash: snap.RevisionHash, ContractHash: snap.ContractHash, Feedback: publicText(eval.Feedback), RunID: result.RunID, Terminal: eval.Completed, Complete: eval.Completed, TestCount: eval.TestCount, PassedCount: eval.PassedCount})
		evidenceCancel()
		if ve != nil {
			result.Status = "verification_persistence_failed"
			if ee != nil {
				result.Status = "uncertain"
			}
			result.Diagnostics = append(result.Diagnostics, "formal evidence persistence failed")
			state.Result = *result
			_ = s.persist(ctx, result.RunID, state, result.Status, created)
			return result, ve
		}
		if passed {
			stage(req, "bank_saved", "已保存正式通过证据")
			stage(req, "verifying", "正在复核课堂状态")
			result.Status = "passed"
			result.ProgressPtr(adapter, ctx, target)
			state.Result = *result
			_ = s.persist(ctx, result.RunID, state, result.Status, created)
			return result, nil
		}
		if ee != nil {
			result.Status = "uncertain"
			state.Result = *result
			_ = s.persist(ctx, result.RunID, state, result.Status, created)
			return result, ee
		}
		feedback = []answer.Feedback{{Kind: "formal", Message: eval.Feedback}}
		previous = &candidate
		state.Feedback = append([]answer.Feedback(nil), feedback...)
		state.Result = *result
		_ = s.persist(ctx, result.RunID, state, "running", created)
	}
	result.Status = "attempts_exhausted"
	state.Result = *result
	_ = s.persist(ctx, result.RunID, state, result.Status, created)
	return result, nil
}

func (r *Result) ProgressPtr(adapter Adapter, ctx context.Context, target Target) {
	if p, e := adapter.Progress(ctx, target); e == nil {
		r.Progress = &p
	} else {
		r.ProgressError = "正式评测通过，但课堂进度读取失败"
		r.Diagnostics = append(r.Diagnostics, r.ProgressError)
	}
}
func languageOf(s problem.Snapshot) string {
	if len(s.Files) > 0 {
		return s.Files[0].Language
	}
	return ""
}
func validateCandidate(s problem.Snapshot, c answer.Candidate) error {
	if err := answer.ValidateCandidate(s, c); err != nil {
		return err
	}
	if c.Kind != "" && c.Kind != "programming" {
		return errors.New("candidate kind must be programming")
	}
	if len(c.Files) != len(s.Files) {
		return fmt.Errorf("candidate files count %d does not match allowed count %d", len(c.Files), len(s.Files))
	}
	for _, f := range s.Files {
		found := false
		for _, cf := range c.Files {
			if cf.Path == f.Path {
				found = true
				if f.Language != "" && c.Language != "" && c.Language != f.Language {
					return errors.New("candidate language does not match")
				}
				break
			}
		}
		if !found {
			return fmt.Errorf("candidate is missing file %s", f.Path)
		}
	}
	_, err := problem.HashFiles(c.Files)
	return err
}
func feedbackString(f []answer.Feedback) string {
	b, _ := json.Marshal(f)
	return publicText(string(b))
}
func publicText(s string) string {
	if problem.ValidatePublicText(s) != nil {
		return "feedback withheld by public-data validator"
	}
	return s
}
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Resume only decodes durable state. A new authenticated adapter must be
// supplied by the caller before continuing upstream writes; this prevents a
// restarted process from guessing which session held an execution key.
func (s *Service) Resume(ctx context.Context, runID string) (*Result, error) {
	rec, e := s.store.GetRun(ctx, runID)
	if e != nil {
		return nil, e
	}
	return decodeRun(rec)
}

// ResumeRun reattaches a fresh authenticated adapter to a durable run. The
// bank supplies the saved snapshot/candidates and the run's feedback checkpoint
// is fed into the next model request. The caller must provide the target mode
// and a live session; no upstream execution key is reconstructed from disk.
func (s *Service) ResumeRun(ctx context.Context, adapter Adapter, req Request, runID string) (*Result, error) {
	if adapter == nil {
		return nil, errors.New("solver adapter is required")
	}
	record, err := s.store.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if req.Owner == "" || record.Owner != ownerKey(req.Owner) {
		return nil, errors.New("run owner does not match")
	}
	if record.Status != "running" && record.Status != "generated" && record.Status != "selftest_passed" && record.Status != "selftest_skipped" {
		return nil, errors.New("terminal or uncertain run cannot be resumed")
	}
	if !validMode(req.Mode) {
		return nil, errors.New("explicit resume mode is required")
	}
	var state runState
	if err := json.Unmarshal(record.Data, &state); err != nil {
		return nil, err
	}
	if (req.Course != "" && req.Course != state.Target.Course) || (req.HomeworkID != 0 && req.HomeworkID != state.Target.HomeworkID) || (req.Game != "" && req.Game != state.Target.Game) {
		return nil, errors.New("resume target does not match")
	}
	loaded, err := adapter.Load(ctx, state.Target)
	if err != nil {
		return nil, err
	}
	snap, err := problem.Prepare(loaded.Snapshot)
	if err != nil {
		return nil, err
	}
	ref, err := s.store.UpsertSnapshot(ctx, snap)
	if err != nil {
		return nil, err
	}
	state.SnapshotRef = ref
	if req.Course == "" {
		req.Course = state.Target.Course
	}
	if req.HomeworkID == 0 {
		req.HomeworkID = state.Target.HomeworkID
	}
	if req.Game == "" {
		req.Game = state.Target.Game
	}
	if req.Mode == "" {
		req.Mode = ModeSubmit
	}
	if req.MaxModelCalls <= 0 {
		req.MaxModelCalls = s.cfg.MaxModelCalls
	}
	if req.MaxEvaluations <= 0 {
		req.MaxEvaluations = s.cfg.MaxEvaluations
	}
	if req.BudgetMicroUSD <= 0 {
		req.BudgetMicroUSD = s.cfg.BudgetMicroUSD
	}
	if req.Timeout <= 0 {
		req.Timeout = s.cfg.Timeout
	}
	runCtx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()
	result := &state.Result
	unlock, err := s.lock(runCtx, loaded.Key)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return s.execute(runCtx, adapter, req, snap, ref, state, result, record.CreatedAt)
}
