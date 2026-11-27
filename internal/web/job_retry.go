package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/jobs"
)

// Only whole-assignment service jobs can be resumed by the consumer. Exams,
// collection and technical single-level operations keep their own lifecycles.
func isRestartableRequest(req jobs.Request) bool {
	if req.Mode != "submit" || req.Game != "" || req.ExerciseID != 0 {
		return false
	}
	switch req.Scope {
	case "homework", "course", "all":
		return true
	}
	return false
}

// canRetryJob offers a new run for any stopped whole-assignment job. A new run
// reads the platform first and skips levels that are already at full score, so
// a stop whose last result was not confirmed is simply run again; there is no
// separate read-only recheck step (removed 2026-11-27).
func canRetryJob(job jobs.Job) bool {
	if !isRestartableRequest(job.Request) {
		return false
	}
	switch job.Status {
	case jobs.Failed, jobs.Canceled, jobs.Interrupted, jobs.Uncertain:
		return true
	}
	return false
}

func emptyRetryBody(r *http.Request) error {
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, 1025))
	if err != nil {
		return err
	}
	if len(body) > 1024 {
		return errors.New("retry body is too large")
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil || len(fields) != 0 {
		return errors.New("retry accepts only an empty body or object")
	}
	return nil
}

func (s *Server) handleJobRetry(w http.ResponseWriter, r *http.Request, requestID string, session Session, id string) {
	// Resolve ownership before any eligibility response; another account must
	// receive exactly the same not-found response as an unknown job ID.
	original, err := s.jobs.manager.Get(r.Context(), sessionJobOwner(session), id)
	if err != nil {
		s.jobError(w, requestID, err)
		return
	}
	if err = emptyRetryBody(r); err != nil {
		s.writeError(w, requestID, http.StatusBadRequest, "INVALID_REQUEST", "重新执行时无需提供任务参数")
		return
	}
	if !canRetryJob(original) {
		s.writeError(w, requestID, http.StatusConflict, "JOB_NOT_RETRYABLE", "当前任务暂不支持重新执行，请刷新进度后再试")
		return
	}
	if !s.jobs.modelConfigured {
		s.writeError(w, requestID, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "服务暂时不可用，管理员正在处理，请稍后再试")
		return
	}
	req := original.Request
	// This key belongs to the original job, not a browser click. Manager keeps
	// the key after completion, so repeated clicks cannot create a second run.
	req.IdempotencyKey = "retry-job:" + original.ID
	s.submitConsumerJob(w, r, requestID, session, req)
}

func (s *Server) submitConsumerJob(w http.ResponseWriter, r *http.Request, requestID string, session Session, req jobs.Request) {
	job, reused, err := s.submitSessionJob(r.Context(), session, req)
	if err != nil {
		s.jobError(w, requestID, err)
		return
	}
	w.Header().Set("Location", "/api/jobs/"+job.ID)
	if reused {
		w.Header().Set("X-Job-Reused", "true")
	}
	s.writeJSON(w, requestID, http.StatusAccepted, toConsumerJob(job))
}

// Both retry entry points submit the same owner-scoped request and durable key.
// The administrator authorizes the action; only the owner's live client runs it.
func (s *Server) handleAdminJobRetry(w http.ResponseWriter, r *http.Request, requestID, actor, id string) {
	if s.jobs == nil {
		s.writeError(w, requestID, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "后台任务服务尚未启动")
		return
	}
	original, err := s.jobs.manager.AdminGet(r.Context(), id)
	if err != nil {
		s.jobError(w, requestID, err)
		return
	}
	if emptyRetryBody(r) != nil {
		s.writeError(w, requestID, http.StatusBadRequest, "INVALID_REQUEST", "重新执行时无需提供任务参数")
		return
	}
	if !canRetryJob(original.Job) {
		s.writeError(w, requestID, http.StatusConflict, "JOB_NOT_RETRYABLE", "当前任务不支持重新执行，请刷新任务记录")
		return
	}
	if !s.ownerCanRun(r.Context(), original.OwnerID) {
		s.writeError(w, requestID, http.StatusConflict, "OWNER_ACCESS_REVOKED", "该用户当前服务未开放，请先检查账号状态")
		return
	}
	session, ok := s.store.sessionForOwner(original.OwnerID)
	if !ok || sessionJobOwner(session) != original.OwnerID {
		s.writeError(w, requestID, http.StatusConflict, "OWNER_LOGIN_REQUIRED", "请该用户重新登录后再重新执行")
		return
	}
	if !s.jobs.modelConfigured {
		s.writeError(w, requestID, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "服务暂时不可用，请稍后再试")
		return
	}
	if err := s.platform.RecordAudit(r.Context(), actor, "job.retry", original.ID, map[string]string{"status": "requested"}); err != nil {
		s.adminError(w, requestID, err)
		return
	}
	req := original.Request
	req.IdempotencyKey = "retry-job:" + original.ID
	job, reused, err := s.submitSessionJob(r.Context(), session, req)
	if err != nil {
		s.jobError(w, requestID, err)
		return
	}
	views, err := s.adminJobResponses(r.Context(), []jobs.AdminRecord{{Job: job, OwnerID: original.OwnerID}}, true)
	if err != nil {
		s.adminError(w, requestID, err)
		return
	}
	w.Header().Set("Location", "/api/admin/jobs/"+job.ID)
	if reused {
		w.Header().Set("X-Job-Reused", "true")
	}
	s.writeJSON(w, requestID, http.StatusAccepted, views[0])
}

func (s *Server) submitSessionJob(ctx context.Context, session Session, req jobs.Request) (jobs.Job, bool, error) {
	owner := sessionJobOwner(session)
	// Capture only the current authenticated client. Retries never reuse an
	// expired executor closure or deserialize account credentials from a job.
	return s.jobs.manager.Submit(ctx, owner, req, func(ctx context.Context, request jobs.Request, report func(jobs.Event)) (jobs.Outcome, error) {
		return s.runAuthorized(ctx, owner, func(ctx context.Context) (jobs.Outcome, error) {
			if !session.ExpiresAt.After(s.store.now()) {
				return jobs.Outcome{Status: jobs.Failed, ErrorCode: "OWNER_LOGIN_REQUIRED", Message: "用户登录已过期，请重新登录后再执行"}, nil
			}
			// The progress callback runs on the executor's reporting path, which
			// a classroom job holds its own lock across. Remember each label
			// once per target instead of writing on every event.
			var labels labelMemo
			out, err := s.jobs.run(ctx, session.Client, owner, request, func(event jobs.Event) {
				if p := event.Progress; p != nil && p.CurrentCourse != "" && p.CurrentHomeworkID > 0 && p.CurrentHomeworkTitle != "" {
					if labels.first(p.CurrentCourse, p.CurrentHomeworkID, p.CurrentHomeworkTitle) {
						s.rememberHomeworkLabels(ctx, p.CurrentCourse, []educoder.Homework{{ID: p.CurrentHomeworkID, Name: p.CurrentHomeworkTitle}})
					}
				}
				report(event)
			})
			out = s.planAutoRetry(ctx, request, out, err)
			s.rememberResultLabels(ctx, request, out.Result, false)
			return out, err
		})
	})
}
