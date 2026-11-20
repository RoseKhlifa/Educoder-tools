package web

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"educoder-cli/internal/jobs"
)

// Automatic retry of jobs that stopped only because Educoder refused to run
// them (runtime pool exhausted, evaluation refused or left queued, lab quota).
// An unattended service should finish that work once the platform recovers,
// without anyone pressing "重新执行".
//
// The plan is written into the job result when the job ends, so the page can
// show it. A scheduler tick launches due retries through the owner's live
// session with the same idempotency key a manual retry uses
// ("retry-job:<id>"), so the two can never both run. Nothing is replayed
// after an unknown or uncertain write: such a result is not eligible.

// autoRetryDelays[i] is the wait before retry attempt i+1; its length is the
// maximum number of automatic retries of one original job.
var autoRetryDelays = []time.Duration{10 * time.Minute, 20 * time.Minute, 40 * time.Minute}

var autoRetryMax = len(autoRetryDelays)

// autoRetryWindow bounds how late a due retry may still start: the owner must
// have a live session to run it, so a retry that is many hours late would act
// on stale intent.
const autoRetryWindow = 6 * time.Hour

var autoRetryNow = time.Now

type autoRetryPlan struct {
	NextAt  time.Time `json:"next_at"`
	Attempt int       `json:"attempt"`
	Max     int       `json:"max"`
}

type retryLevelState struct {
	Status     string `json:"status"`
	ReasonCode string `json:"reason_code"`
}

type retryBatchState struct {
	Status     string            `json:"status"`
	ReasonCode string            `json:"reason_code"`
	Levels     []retryLevelState `json:"levels"`
}

type retryHomeworkState struct {
	Status     string           `json:"status"`
	ReasonCode string           `json:"reason_code"`
	Result     *retryBatchState `json:"result"`
}

func resourceRefusal(status, reason string) bool {
	return status == "resource_unavailable" || reason == "resource_unavailable"
}

// homeworkResourceOnly: some level was refused by the platform, and every
// other unfinished level either was refused too or never started because of
// that refusal (planned/pending or blocked from entering the next level).
func homeworkResourceOnly(b *retryBatchState) bool {
	if b == nil || len(b.Levels) == 0 {
		return false
	}
	refused := false
	for _, l := range b.Levels {
		switch {
		case l.Status == "passed" || l.Status == "skipped":
		case resourceRefusal(l.Status, l.ReasonCode):
			refused = true
		case l.Status == "planned" || l.Status == "pending" || l.ReasonCode == "runtime_initialization_failed":
		default:
			return false
		}
	}
	return refused
}

// resourceOnlyStop reports whether a homework/course/all result stopped only
// because the platform refused to run work. Anything uncertain, any code or
// model failure, or any other operator hand-off makes it ineligible.
func resourceOnlyStop(raw json.RawMessage) bool {
	var top struct {
		retryBatchState
		Homeworks []retryHomeworkState `json:"homeworks"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &top) != nil {
		return false
	}
	if len(top.Homeworks) == 0 {
		return homeworkResourceOnly(&top.retryBatchState)
	}
	refused := false
	for _, h := range top.Homeworks {
		switch h.Status {
		case "passed", "skipped", "skipped_optional":
			continue
		case "needs_service":
			if !homeworkResourceOnly(h.Result) {
				return false
			}
			refused = true
		default:
			return false
		}
	}
	return refused
}

// annotateAutoRetry adds the plan to a result object, keeping every field.
func annotateAutoRetry(raw json.RawMessage, plan autoRetryPlan) json.RawMessage {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return raw
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		return raw
	}
	fields["auto_retry"] = encoded
	out, err := json.Marshal(fields)
	if err != nil {
		return raw
	}
	return out
}

// nextAutoRetryAttempt numbers the retry this job would schedule: 1 for an
// original job, previous+1 for a job that is itself an automatic retry.
func (s *Server) nextAutoRetryAttempt(ctx context.Context, request jobs.Request) int {
	parentID, ok := strings.CutPrefix(request.IdempotencyKey, "retry-job:")
	if !ok || parentID == "" || s.jobs == nil {
		return 1
	}
	parent, err := s.jobs.manager.AdminGet(ctx, parentID)
	if err != nil {
		return 1
	}
	var result struct {
		AutoRetry *autoRetryPlan `json:"auto_retry"`
	}
	if json.Unmarshal(parent.Result, &result) != nil || result.AutoRetry == nil {
		return 1
	}
	return result.AutoRetry.Attempt + 1
}

// planAutoRetry decorates a finished outcome with a retry plan when eligible.
func (s *Server) planAutoRetry(ctx context.Context, request jobs.Request, out jobs.Outcome, err error) jobs.Outcome {
	if err != nil || out.Status != jobs.Failed || !isRestartableRequest(request) || !resourceOnlyStop(out.Result) {
		return out
	}
	return withAutoRetryPlan(out, s.nextAutoRetryAttempt(ctx, request))
}

// withAutoRetryPlan adds the plan for the given attempt, or leaves the outcome
// unchanged once the chain has used every attempt.
func withAutoRetryPlan(out jobs.Outcome, attempt int) jobs.Outcome {
	if attempt < 1 || attempt > len(autoRetryDelays) {
		return out
	}
	plan := autoRetryPlan{NextAt: autoRetryNow().UTC().Add(autoRetryDelays[attempt-1]).Truncate(time.Second), Attempt: attempt, Max: len(autoRetryDelays)}
	out.Result = annotateAutoRetry(out.Result, plan)
	return out
}

type autoRetryScheduler struct {
	mu       sync.Mutex
	launched map[string]bool
}

func (a *autoRetryScheduler) claim(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.launched == nil {
		a.launched = map[string]bool{}
	}
	if a.launched[id] {
		return false
	}
	if len(a.launched) > 10000 {
		a.launched = map[string]bool{}
	}
	a.launched[id] = true
	return true
}

// AutoRetryTick launches every due automatic retry whose owner still has a
// live session and access. It is safe to call repeatedly and concurrently
// with manual retries: each launch uses the parent's retry key.
func (s *Server) AutoRetryTick(ctx context.Context) int {
	if s.jobs == nil || s.jobs.manager == nil || !s.jobs.modelConfigured {
		return 0
	}
	records, err := s.jobs.manager.AdminList(ctx, 200)
	if err != nil {
		return 0
	}
	now := autoRetryNow()
	launched := 0
	for _, record := range records {
		if record.Status != jobs.Failed || !isRestartableRequest(record.Request) {
			continue
		}
		var result struct {
			AutoRetry *autoRetryPlan `json:"auto_retry"`
		}
		if json.Unmarshal(record.Result, &result) != nil || result.AutoRetry == nil {
			continue
		}
		plan := result.AutoRetry
		if now.Before(plan.NextAt) || now.Sub(plan.NextAt) > autoRetryWindow || !canRetryJob(record.Job) {
			continue
		}
		if !s.ownerCanRun(ctx, record.OwnerID) {
			continue
		}
		session, ok := s.store.sessionForOwner(record.OwnerID)
		if !ok || sessionJobOwner(session) != record.OwnerID {
			continue
		}
		if !s.autoRetry.claim(record.ID) {
			continue
		}
		req := record.Request
		req.IdempotencyKey = "retry-job:" + record.ID
		if _, _, err := s.submitSessionJob(ctx, session, req); err == nil {
			launched++
		}
	}
	return launched
}
