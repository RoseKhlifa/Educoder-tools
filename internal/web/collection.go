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
	// Login passes no durable key: repeated requests reuse the active target,
	// but a new authenticated login after an incomplete/finished scan can read
	// current upstream work with its current in-memory client. Explicit callers
	// may still opt into durable replay using the Idempotency-Key header.
	job, reused, err := s.jobs.manager.Submit(ctx, owner, jobs.Request{Mode: "collect_verified", IdempotencyKey: key}, func(ctx context.Context, _ jobs.Request, report func(jobs.Event)) (jobs.Outcome, error) {
		return s.runAuthorized(ctx, owner, func(ctx context.Context) (jobs.Outcome, error) { return s.jobs.collect(ctx, client, owner, report) })
	})
	if err != nil {
		return map[string]any{"queued": false, "error_code": jobs.Code(err)}
	}
	return map[string]any{"queued": true, "job_id": job.ID, "status": job.Status, "reused": reused}
}

// A login re-reads every passed level of the account. Measured in production
// on 2026-11-21: a repeat scan within a day read 237–626 Educoder pages over
// 5–7 minutes and collected 0–2 new answers, and one account was scanned five
// times in a day. For a free campus service those repeats would be the largest
// source of Educoder traffic and fill the two collection workers, so a login
// skips the scan while the account's last scan is recent. An explicit POST
// /api/collection still always runs.
const (
	loginCollectionCooldown           = 24 * time.Hour
	loginIncompleteCollectionCooldown = 2 * time.Hour
)

var collectionNow = time.Now

func (s *Server) enqueueLoginCollection(ctx context.Context, client EducoderClient, owner string) map[string]any {
	if recentCollection(ctx, s.jobs.manager, owner) {
		return map[string]any{"queued": false, "skipped": "recent_scan"}
	}
	return s.enqueueCollection(ctx, client, owner, "")
}

// recentCollection reports whether the owner's newest collection finished
// successfully inside its cooldown. A queued or running scan, a failed or
// stopped one, or a read error never suppresses the next scan: the active one
// is reused by Submit and the others deserve a retry.
func recentCollection(ctx context.Context, m *jobs.Manager, owner string) bool {
	list, err := m.List(ctx, owner, 100)
	if err != nil {
		return false
	}
	for _, j := range list {
		if j.Request.Mode != "collect_verified" {
			continue
		}
		if j.Status != jobs.Succeeded || j.FinishedAt == nil {
			return false
		}
		age := collectionNow().Sub(*j.FinishedAt)
		if collectionScanComplete(j.Result) {
			return age < loginCollectionCooldown
		}
		return age < loginIncompleteCollectionCooldown
	}
	return false
}

func collectionScanComplete(raw json.RawMessage) bool {
	var result struct {
		ScanComplete bool `json:"scan_complete"`
	}
	if json.Unmarshal(raw, &result) == nil {
		return result.ScanComplete
	}
	var nested string
	return json.Unmarshal(raw, &nested) == nil && json.Unmarshal([]byte(nested), &result) == nil && result.ScanComplete
}

// Existing /api/jobs/{id} and /cancel provide owner-scoped status/events and
// cancellation for these jobs too. A fresh authenticated login or a POST with
// no reused explicit key can retry after a stop, but never replays credentials
// or executable closures after service restart.
func (s *Server) handleCollection(w http.ResponseWriter, r *http.Request, requestID string) {
	session, ok := s.requireSession(w, r, requestID)
	if !ok {
		return
	}
	if s.jobs == nil || s.jobs.collect == nil {
		s.writeError(w, requestID, 503, "COLLECTION_UNAVAILABLE", "只读采集服务尚未启动")
		return
	}
	owner := sessionJobOwner(session)
	switch r.Method {
	case http.MethodGet:
		list, err := s.jobs.manager.List(r.Context(), owner, 100)
		if err != nil {
			s.jobError(w, requestID, err)
			return
		}
		out := []consumerJob{}
		for _, j := range list {
			if j.Request.Mode == "collect_verified" {
				out = append(out, toConsumerJob(j))
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
			s.writeError(w, requestID, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "服务人员正在处理，请稍后再试")
			return
		}
		w.Header().Set("Location", "/api/jobs/"+result["job_id"].(string))
		s.writeJSON(w, requestID, http.StatusAccepted, result)
	default:
		s.writeError(w, requestID, 405, "METHOD_NOT_ALLOWED", "采集接口方法无效")
	}
}
