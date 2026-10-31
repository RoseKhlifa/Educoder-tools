package web

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/jobs"
	"educoder-cli/internal/questionbank"
)

func collectionTarget(t *educoder.Task) questionbank.CollectionTarget {
	return questionbank.CollectionTarget{Course: t.CourseIdentifier, HomeworkID: t.HomeworkID, Game: t.Game.Identifier, ChallengeID: t.Challenge.ID, Shixun: t.Shixun.Identifier}
}

// Admission is a local bounded database operation; no upstream scan is performed
// on the login goroutine, and admission failure never invalidates a login.
func (s *Server) enqueueCollection(ctx context.Context, client EducoderClient, owner, key string) map[string]any {
	if key == "" {
		key = "collection-v1-" + time.Now().UTC().Format("2006-01-02")
	}
	job, reused, err := s.jobs.manager.Submit(ctx, owner, jobs.Request{Mode: "collect_verified", IdempotencyKey: key}, func(ctx context.Context, _ jobs.Request, report func(jobs.Event)) (jobs.Outcome, error) {
		return s.jobs.collect(ctx, client, owner, report)
	})
	if err != nil {
		return map[string]any{"queued": false, "error_code": jobs.Code(err)}
	}
	return map[string]any{"queued": true, "job_id": job.ID, "status": job.Status, "reused": reused}
}

// Existing /api/jobs/{id} and /cancel provide owner-scoped status/events and
// cancellation for these jobs too. POST allows an explicit retry after a stop;
// the daily login key never silently replays a job interrupted by restart.
func (s *Server) handleCollection(w http.ResponseWriter, r *http.Request, requestID string) {
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.authError(w, requestID)
		return
	}
	if s.jobs == nil || s.jobs.collect == nil {
		s.writeError(w, requestID, 503, "COLLECTION_UNAVAILABLE", "只读采集服务尚未启动")
		return
	}
	owner := accountJobOwner(session.User.Login)
	switch r.Method {
	case http.MethodGet:
		list, err := s.jobs.manager.List(r.Context(), owner, 100)
		if err != nil {
			s.jobError(w, requestID, err)
			return
		}
		out := []jobs.Job{}
		for _, j := range list {
			if j.Request.Mode == "collect_verified" {
				out = append(out, j)
			}
		}
		s.writeJSON(w, requestID, 200, map[string]any{"jobs": out})
	case http.MethodPost:
		// No target or answer fields are accepted. The request body must be
		// empty or an empty object; keys come from the standard header only.
		if r.ContentLength != 0 {
			var v map[string]json.RawMessage
			if err := decodeJSON(r, &v); err != nil || len(v) != 0 {
				s.writeError(w, requestID, 400, "INVALID_REQUEST", "采集不接受关卡或答案参数")
				return
			}
		}
		result := s.enqueueCollection(r.Context(), session.Client, owner, r.Header.Get("Idempotency-Key"))
		if result["queued"] == false {
			s.writeJSON(w, requestID, http.StatusTooManyRequests, result)
			return
		}
		w.Header().Set("Location", "/api/jobs/"+result["job_id"].(string))
		s.writeJSON(w, requestID, http.StatusAccepted, result)
	default:
		s.writeError(w, requestID, 405, "METHOD_NOT_ALLOWED", "采集接口方法无效")
	}
}
