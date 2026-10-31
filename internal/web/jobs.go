package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/jobs"
	"educoder-cli/internal/questionbank"
	"educoder-cli/internal/solver"
	"educoder-cli/internal/taskservice"
)

// JobRuntime is application state shared by all HTTP handlers. It is initialized
// once at startup, not once per request. Executable closures stay in memory.
type JobRuntime struct {
	manager          *jobs.Manager
	bank             *questionbank.Store
	run              func(context.Context, EducoderClient, string, jobs.Request, func(jobs.Event)) (jobs.Outcome, error)
	modelConfigured  bool
	modelConcurrency int
	collect          func(context.Context, EducoderClient, string, func(jobs.Event)) (jobs.Outcome, error)
}

func loadJobConfig(cfg *Config) error {
	if value := strings.TrimSpace(os.Getenv("APP_DATA_DIR")); value != "" {
		cfg.DataDir = value
	}
	for name, value := range map[string]*int{
		"JOB_WORKERS": &cfg.JobWorkers, "JOB_QUEUE_SIZE": &cfg.JobQueueSize,
		"JOB_PER_ACCOUNT_PENDING": &cfg.JobPerOwner, "JOB_PER_ACCOUNT_RUNNING": &cfg.JobPerOwnerRunning,
		"MODEL_CONCURRENCY": &cfg.ModelConcurrency,
	} {
		if raw := os.Getenv(name); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > 100000 {
				return fmt.Errorf("invalid %s", name)
			}
			*value = n
		}
	}
	if cfg.JobWorkers > 256 || cfg.ModelConcurrency > 256 || cfg.JobPerOwner > cfg.JobQueueSize+cfg.JobWorkers || cfg.JobPerOwnerRunning > cfg.JobWorkers {
		return errors.New("invalid worker/model concurrency or account pending limit")
	}
	for name, value := range map[string]*time.Duration{"JOB_TIMEOUT": &cfg.JobTimeout, "MODEL_TIMEOUT": &cfg.ModelTimeout} {
		if raw := os.Getenv(name); raw != "" {
			duration, err := time.ParseDuration(raw)
			if err != nil || duration < time.Second || duration > 24*time.Hour {
				return fmt.Errorf("invalid %s", name)
			}
			*value = duration
		}
	}
	cfg.ModelAPIKeyFile = strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY_FILE"))
	cfg.ModelBaseURL = strings.TrimSpace(os.Getenv("DEEPSEEK_BASE_URL"))
	return nil
}

func modelKey(cfg Config) (string, error) {
	if cfg.ModelAPIKeyFile != "" {
		file, err := os.Open(cfg.ModelAPIKeyFile)
		if err != nil {
			// A missing optional secret keeps browsing available. The deployment
			// UI reports model_configured=false and can be enabled on restart.
			return "", nil
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 8192 {
			return "", nil
		}
		raw, err := os.ReadFile(cfg.ModelAPIKeyFile)
		if err != nil {
			return "", nil
		}
		value := strings.TrimSpace(string(raw))
		if strings.ContainsAny(value, "\r\n\x00") {
			return "", errors.New("invalid model credential file")
		}
		return value, nil
	}
	value := strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	if strings.ContainsAny(value, "\r\n\x00") || len(value) > 8192 {
		return "", errors.New("invalid model credential")
	}
	return value, nil
}

func NewJobRuntime(cfg Config) (*JobRuntime, error) {
	defaults := DefaultConfig()
	if cfg.DataDir == "" {
		cfg.DataDir = defaults.DataDir
	}
	if cfg.JobWorkers <= 0 {
		cfg.JobWorkers = defaults.JobWorkers
	}
	if cfg.JobQueueSize <= 0 {
		cfg.JobQueueSize = defaults.JobQueueSize
	}
	if cfg.JobPerOwner <= 0 {
		cfg.JobPerOwner = defaults.JobPerOwner
	}
	if cfg.JobPerOwnerRunning <= 0 {
		cfg.JobPerOwnerRunning = defaults.JobPerOwnerRunning
	}
	if cfg.JobTimeout <= 0 {
		cfg.JobTimeout = defaults.JobTimeout
	}
	if cfg.ModelConcurrency <= 0 {
		cfg.ModelConcurrency = defaults.ModelConcurrency
	}
	if cfg.ModelTimeout <= 0 {
		cfg.ModelTimeout = defaults.ModelTimeout
	}
	key, err := modelKey(cfg)
	if err != nil {
		return nil, err
	}
	bank, err := questionbank.Open(filepath.Join(cfg.DataDir, "questionbank.sqlite"))
	if err != nil {
		return nil, errors.New("open question bank failed")
	}
	var provider taskservice.Provider
	if key != "" {
		provider, err = answer.NewDeepSeek(answer.Config{APIKey: key, BaseURL: cfg.ModelBaseURL, Timeout: cfg.ModelTimeout})
		if err != nil {
			bank.Close()
			return nil, errors.New("initialize model provider failed")
		}
	}
	manager, err := jobs.Open(context.Background(), jobs.Config{Path: filepath.Join(cfg.DataDir, "jobs.sqlite"), Workers: cfg.JobWorkers, QueueSize: cfg.JobQueueSize, PerOwnerPending: cfg.JobPerOwner, PerOwnerRunning: cfg.JobPerOwnerRunning, Timeout: cfg.JobTimeout})
	if err != nil {
		bank.Close()
		return nil, err
	}
	service := taskservice.New(bank, provider, taskservice.Config{ModelConcurrency: cfg.ModelConcurrency, Timeout: cfg.JobTimeout})
	runtime := &JobRuntime{manager: manager, bank: bank, modelConfigured: key != "", modelConcurrency: cfg.ModelConcurrency}
	collector := &taskservice.Collector{Bank: bank, Limits: taskservice.DefaultCollectionLimits()}
	scanSlot := make(chan struct{}, 1)
	runtime.collect = func(ctx context.Context, client EducoderClient, owner string, report func(jobs.Event)) (jobs.Outcome, error) {
		select {
		case scanSlot <- struct{}{}:
			defer func() { <-scanSlot }()
		case <-ctx.Done():
			return jobs.Outcome{}, ctx.Err()
		}
		protocol, ok := client.(*educoder.Client)
		if !ok {
			return jobs.Outcome{Status: jobs.Failed, ErrorCode: "collection_client_unsupported", Message: "当前会话不支持只读采集"}, nil
		}
		reader, e := protocol.CollectionReader(educoder.DefaultCollectionBudget())
		if e != nil {
			return jobs.Outcome{}, errors.New("collection session unavailable")
		}
		report(jobs.Event{Phase: "collecting", Kind: "collection", Message: "正在只读核验已满分任务，不调用模型"})
		result, e := collector.Scan(ctx, reader, owner, func(summary taskservice.CollectionSummary) {
			report(jobs.Event{Phase: "collecting", Kind: "collection", Message: taskservice.CollectionMessage(summary)})
		})
		raw, _ := json.Marshal(result)
		out := jobs.Outcome{Status: jobs.Succeeded, Result: raw, Message: taskservice.CollectionMessage(result)}
		if e != nil {
			out.Status = jobs.Failed
			out.ErrorCode = "collection_stopped"
			out.Message = "采集已停止，已记录统计和跳过原因"
			// Do not persist upstream error text (which may include an account
			// path). Public reasons and counters already explain the stopped scan.
			return out, errors.New("Read-only collection stopped; inspect its summary")
		}
		return out, e
	}
	runtime.run = func(ctx context.Context, client EducoderClient, owner string, req jobs.Request, report func(jobs.Event)) (jobs.Outcome, error) {
		protocol, ok := client.(*educoder.Client)
		if !ok {
			return jobs.Outcome{Status: jobs.Failed, ErrorCode: "CLIENT_UNSUPPORTED", Message: "当前会话尚未支持答题执行"}, errors.New("unsupported task client")
		}
		if req.Scope == "homework" {
			batch, err := service.RunHomework(ctx, protocol, taskservice.Request{Owner: owner, Course: req.Course, HomeworkID: req.HomeworkID, Mode: solver.ModeSubmit, IdempotencyKey: req.IdempotencyKey}, func(progress taskservice.Progress) {
				e := jobs.Event{Phase: progress.Phase, Kind: progress.Kind, Message: progress.Message}
				if progress.Batch != nil {
					e.Progress = &jobs.BatchProgress{TotalLevels: progress.Batch.TotalLevels, CompletedLevels: progress.Batch.CompletedLevels, SkippedLevels: progress.Batch.SkippedLevels, FailedLevels: progress.Batch.FailedLevels, CurrentPosition: progress.Batch.CurrentPosition, CurrentTitle: progress.Batch.CurrentTitle}
				}
				report(e)
			})
			return homeworkOutcome(batch, err)
		}
		result, err := service.Run(ctx, protocol, taskservice.Request{Owner: owner, Course: req.Course, HomeworkID: req.HomeworkID, Game: req.Game, Mode: solver.Mode(req.Mode), IdempotencyKey: req.IdempotencyKey}, func(progress taskservice.Progress) {
			e := jobs.Event{Phase: progress.Phase, Kind: progress.Kind, Message: progress.Message}
			if progress.Batch != nil {
				e.Progress = &jobs.BatchProgress{TotalLevels: progress.Batch.TotalLevels, CompletedLevels: progress.Batch.CompletedLevels, SkippedLevels: progress.Batch.SkippedLevels, FailedLevels: progress.Batch.FailedLevels, CurrentPosition: progress.Batch.CurrentPosition, CurrentTitle: progress.Batch.CurrentTitle}
			}
			report(e)
		})
		out := jobs.Outcome{Status: jobs.Failed, ErrorCode: "EXECUTION_FAILED", Message: "任务执行未完成，请查看进度"}
		if result != nil {
			out.Result, _ = json.Marshal(result)
			switch result.Status {
			case "passed", "generated", "selftest_passed", "selftest_skipped":
				out.Status = jobs.Succeeded
				out.ErrorCode = ""
				out.Message = "任务已完成"
			case "uncertain", "verification_unconfirmed":
				out.Status = jobs.Uncertain
				out.ErrorCode = "RESULT_UNCONFIRMED"
				out.Message = "平台结果待复核，请勿重复提交"
			case "canceled":
				out.Status = jobs.Canceled
				out.ErrorCode = ""
				out.Message = "执行已停止"
			default:
				out.Message = "任务未通过，结果已保存"
			}
		}
		return out, err
	}
	return runtime, nil
}

func homeworkOutcome(batch *taskservice.BatchResult, err error) (jobs.Outcome, error) {
	if batch == nil {
		return jobs.Outcome{}, err
	}
	raw, _ := json.Marshal(batch)
	out := jobs.Outcome{Status: jobs.Failed, Result: raw, ErrorCode: "HOMEWORK_BATCH_FAILED", Message: "整份作业尚未完成，请查看停止关卡和原因"}
	switch batch.Status {
	case "passed":
		if err == nil && batch.TotalLevels > 0 && batch.CompletedLevels == batch.TotalLevels && batch.FailedLevels == 0 {
			out.Status, out.ErrorCode, out.Message = jobs.Succeeded, "", "全部关卡已核验满分"
		}
	case "uncertain", "verification_unconfirmed":
		out.Status, out.ErrorCode, out.Message = jobs.Uncertain, "RESULT_UNCONFIRMED", "当前关卡的评测结果待复核，已保留已完成关卡"
		return out, nil
	case "canceled":
		out.Status, out.ErrorCode, out.Message = jobs.Canceled, "", "后续关卡已停止"
	case "model_error", "selftest_failed", "attempts_exhausted", "invalid_candidate", "budget_exhausted", "budget_unavailable", "unsupported_multifile", "unsupported_roster", "usage_persistence_failed", "verification_persistence_failed", "resource_unavailable":
		// Earlier levels may have submitted successfully. A known failure in
		// a later generation or confirmed self-test must not be turned into an
		// unknown submission merely because Manager's mutation flag is sticky.
		return out, nil
	}
	return out, err
}

func (r *JobRuntime) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if err := r.manager.Close(ctx); err != nil {
		return err
	}
	if r.bank != nil {
		return r.bank.Close()
	}
	return nil
}

// AttachJobs is called before ListenAndServe, keeping construction-only HTTP
// tests free of background workers and persistent side effects.
func (s *Server) AttachJobs(runtime *JobRuntime) { s.jobs = runtime }

func accountJobOwner(login string) string {
	digest := sha256.Sum256([]byte(login))
	return hex.EncodeToString(digest[:])
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request, requestID string) {
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.authError(w, requestID)
		return
	}
	owner := accountJobOwner(session.User.Login)
	parts := splitPath(r.URL.Path)
	if len(parts) == 2 && r.Method == http.MethodGet {
		if s.jobs == nil {
			s.writeJSON(w, requestID, http.StatusOK, map[string]any{"jobs": []jobs.Job{}, "capacity": map[string]any{"workers": 0, "queue_size": 0, "queued": 0, "running": 0, "per_owner_pending": 0, "per_owner_running": 0, "model_concurrency": 0, "model_configured": false}})
			return
		}
		list, err := s.jobs.manager.List(r.Context(), owner, 50)
		if err != nil {
			s.jobError(w, requestID, err)
			return
		}
		if list == nil {
			list = []jobs.Job{}
		}
		s.writeJSON(w, requestID, http.StatusOK, struct {
			Jobs     []jobs.Job `json:"jobs"`
			Capacity struct {
				jobs.Stats
				ModelConcurrency int  `json:"model_concurrency"`
				ModelConfigured  bool `json:"model_configured"`
			} `json:"capacity"`
		}{Jobs: list, Capacity: struct {
			jobs.Stats
			ModelConcurrency int  `json:"model_concurrency"`
			ModelConfigured  bool `json:"model_configured"`
		}{s.jobs.manager.Stats(), s.jobs.modelConcurrency, s.jobs.modelConfigured}})
		return
	}
	if s.jobs == nil {
		s.writeError(w, requestID, http.StatusServiceUnavailable, "JOBS_UNAVAILABLE", "后台任务服务尚未启动")
		return
	}
	if len(parts) == 2 && r.Method == http.MethodPost {
		if !s.jobs.modelConfigured {
			s.writeError(w, requestID, http.StatusServiceUnavailable, "MODEL_NOT_CONFIGURED", "服务器尚未配置模型服务")
			return
		}
		var req jobs.Request
		if err := decodeJSON(r, &req); err != nil {
			s.writeError(w, requestID, http.StatusBadRequest, "INVALID_REQUEST", "任务参数无效")
			return
		}
		if req.Mode == "collect_verified" {
			s.writeError(w, requestID, http.StatusBadRequest, "INVALID_REQUEST", "请使用只读采集接口")
			return
		}
		if req.Scope == "homework" && (req.Mode != "submit" || req.Game != "") {
			s.writeError(w, requestID, http.StatusBadRequest, "INVALID_REQUEST", "整份作业任务需要 submit 模式且不指定当前关卡")
			return
		}
		header := r.Header.Get("Idempotency-Key")
		if header != "" {
			if req.IdempotencyKey != "" && req.IdempotencyKey != header {
				s.writeError(w, requestID, http.StatusBadRequest, "INVALID_REQUEST", "任务幂等标识不一致")
				return
			}
			req.IdempotencyKey = header
		}
		// The executor captures only this authenticated client. No account,
		// cookie or execution key enters the durable job request.
		job, reused, err := s.jobs.manager.Submit(r.Context(), owner, req, func(ctx context.Context, request jobs.Request, report func(jobs.Event)) (jobs.Outcome, error) {
			return s.jobs.run(ctx, session.Client, owner, request, report)
		})
		if err != nil {
			s.jobError(w, requestID, err)
			return
		}
		w.Header().Set("Location", "/api/jobs/"+job.ID)
		if reused {
			w.Header().Set("X-Job-Reused", "true")
		}
		s.writeJSON(w, requestID, http.StatusAccepted, job)
		return
	}
	if len(parts) == 3 && r.Method == http.MethodGet {
		job, err := s.jobs.manager.Get(r.Context(), owner, parts[2])
		if err != nil {
			s.jobError(w, requestID, err)
			return
		}
		s.writeJSON(w, requestID, http.StatusOK, job)
		return
	}
	if len(parts) == 4 && parts[3] == "cancel" && r.Method == http.MethodPost {
		job, err := s.jobs.manager.Cancel(r.Context(), owner, parts[2])
		if err != nil {
			s.jobError(w, requestID, err)
			return
		}
		s.writeJSON(w, requestID, http.StatusOK, job)
		return
	}
	s.writeError(w, requestID, http.StatusNotFound, "NOT_FOUND", "任务接口不存在")
}

func (s *Server) jobError(w http.ResponseWriter, requestID string, err error) {
	code, status, message := "JOBS_ERROR", http.StatusInternalServerError, "后台任务服务暂时不可用"
	switch {
	case jobs.IsCode(err, "queue_full"):
		code, status, message = "QUEUE_FULL", http.StatusTooManyRequests, "任务队列已满，请稍后重试"
	case jobs.IsCode(err, "owner_limit"):
		code, status, message = "ACCOUNT_JOB_LIMIT", http.StatusTooManyRequests, "本账号待处理任务已达上限，请等待或取消已有任务"
	case jobs.IsCode(err, "idempotency_conflict"):
		code, status, message = "IDEMPOTENCY_CONFLICT", http.StatusConflict, "同一任务标识已用于其他请求"
	case jobs.IsCode(err, "not_found"):
		code, status, message = "NOT_FOUND", http.StatusNotFound, "任务不存在"
	case jobs.IsCode(err, "invalid_request"):
		code, status, message = "INVALID_REQUEST", http.StatusBadRequest, "任务参数无效"
	case jobs.IsCode(err, "closed"):
		code, status, message = "JOBS_UNAVAILABLE", http.StatusServiceUnavailable, "后台任务服务正在关闭"
	}
	if status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "3")
	}
	s.writeError(w, requestID, status, code, message)
}
