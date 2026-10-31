package web

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"educoder-cli/internal/jobs"
	"educoder-cli/internal/platform"
	"educoder-cli/internal/questionbank"
)

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request, requestID string) {
	session, ok := s.adminSession(w, r, requestID)
	if !ok {
		return
	}
	parts := splitPath(r.URL.Path)
	actor := session.Principal.ID
	if len(parts) == 3 && r.Method == http.MethodGet {
		switch parts[2] {
		case "users":
			users, err := s.platform.ListUsers(r.Context(), 100)
			if err != nil {
				s.adminError(w, requestID, err)
				return
			}
			s.writeJSON(w, requestID, 200, map[string]any{"items": users, "limit": 100})
		case "cards":
			cards, err := s.platform.ListCards(r.Context(), 100)
			if err != nil {
				s.adminError(w, requestID, err)
				return
			}
			s.writeJSON(w, requestID, 200, map[string]any{"items": cards, "limit": 100, "access_card_required": s.config.AccessCardRequired})
		case "audit":
			items, err := s.platform.ListAudit(r.Context(), 100)
			if err != nil {
				s.adminError(w, requestID, err)
				return
			}
			s.writeJSON(w, requestID, 200, map[string]any{"items": items, "limit": 100})
		case "jobs":
			items := []jobs.AdminRecord{}
			if s.jobs != nil {
				var err error
				items, err = s.jobs.manager.AdminList(r.Context(), 100)
				if err != nil {
					s.adminError(w, requestID, err)
					return
				}
			}
			s.writeJSON(w, requestID, 200, map[string]any{"items": items, "limit": 100})
		case "overview", "usage":
			users, err := s.platform.Stats(r.Context())
			if err != nil {
				s.adminError(w, requestID, err)
				return
			}
			usage := questionbank.UsageSummary{}
			var capacity any
			if s.jobs != nil {
				capacity = map[string]any{"jobs": s.jobs.manager.Stats(), "model_concurrency": s.jobs.modelConcurrency, "model_configured": s.jobs.modelConfigured}
				if s.jobs.bank != nil {
					var err error
					usage, err = s.jobs.bank.UsageSummary(r.Context())
					if err != nil {
						s.adminError(w, requestID, err)
						return
					}
				}
			}
			s.writeJSON(w, requestID, 200, map[string]any{"usage": usage, "capacity": capacity, "users": users, "access_card_required": s.config.AccessCardRequired})
		default:
			s.writeError(w, requestID, 404, "NOT_FOUND", "管理接口不存在")
		}
		return
	}
	if len(parts) == 5 && parts[2] == "users" && parts[4] == "status" && r.Method == http.MethodPost {
		var input struct {
			Status string `json:"status"`
		}
		if decodeJSON(r, &input) != nil || (input.Status != "active" && input.Status != "disabled") {
			s.writeError(w, requestID, 400, "INVALID_REQUEST", "账号状态无效")
			return
		}
		if err := s.platform.SetStatus(r.Context(), actor, parts[3], input.Status); err != nil {
			s.adminError(w, requestID, err)
			return
		}
		if input.Status == "disabled" {
			s.store.DeleteUser(parts[3])
			if s.jobs != nil {
				if err := s.jobs.manager.CancelOwner(r.Context(), "platform:"+parts[3]); err != nil {
					s.adminError(w, requestID, err)
					return
				}
			}
		}
		user, err := s.platform.LookupUser(r.Context(), parts[3])
		if err != nil {
			s.adminError(w, requestID, err)
			return
		}
		s.writeJSON(w, requestID, 200, user)
		return
	}
	if len(parts) == 3 && parts[2] == "cards" && r.Method == http.MethodPost {
		var input struct {
			Label       string     `json:"label"`
			BoundUserID string     `json:"bound_user_id"`
			Reusable    bool       `json:"reusable"`
			ExpiresAt   *time.Time `json:"expires_at"`
		}
		if decodeJSON(r, &input) != nil || len(input.Label) > 120 {
			s.writeError(w, requestID, 400, "INVALID_REQUEST", "卡密参数无效")
			return
		}
		options := platform.CardOptions{Label: input.Label, BoundUserID: input.BoundUserID, Reusable: input.Reusable}
		if input.ExpiresAt != nil {
			options.ExpiresAt = *input.ExpiresAt
		}
		if !options.ExpiresAt.IsZero() && !options.ExpiresAt.After(time.Now()) {
			s.writeError(w, requestID, 400, "INVALID_REQUEST", "有效期需晚于当前时间")
			return
		}
		if options.BoundUserID != "" {
			if _, err := s.platform.LookupUser(r.Context(), options.BoundUserID); err != nil {
				s.adminError(w, requestID, err)
				return
			}
		}
		issued, err := s.platform.IssueCard(r.Context(), actor, options)
		if err != nil {
			s.adminError(w, requestID, err)
			return
		}
		s.writeJSON(w, requestID, http.StatusCreated, issued)
		return
	}
	if len(parts) == 5 && parts[2] == "cards" && parts[4] == "revoke" && r.Method == http.MethodPost {
		if err := s.platform.RevokeCard(r.Context(), actor, parts[3]); err != nil {
			s.adminError(w, requestID, err)
			return
		}
		// Recheck every connected principal; losing this card need not revoke a
		// user who has another valid card. No raw code is read back from storage.
		if s.config.AccessCardRequired {
			for _, id := range s.store.UserIDs() {
				user, err := s.platform.LookupUser(r.Context(), id)
				if err != nil {
					continue
				}
				allowed, err := s.platform.CanAccess(r.Context(), user)
				if err == nil && !allowed {
					s.store.DeleteUser(id)
					if s.jobs != nil {
						_ = s.jobs.manager.CancelOwner(r.Context(), user.Owner())
					}
				}
			}
		}
		s.writeJSON(w, requestID, 200, map[string]bool{"revoked": true})
		return
	}
	if len(parts) == 4 && parts[2] == "jobs" && r.Method == http.MethodGet {
		if s.jobs == nil {
			s.writeError(w, requestID, 503, "SERVICE_UNAVAILABLE", "服务暂不可用")
			return
		}
		job, err := s.jobs.manager.AdminGet(r.Context(), parts[3])
		if err != nil {
			s.jobError(w, requestID, err)
			return
		}
		s.writeJSON(w, requestID, 200, job)
		return
	}
	if len(parts) == 5 && parts[2] == "jobs" && parts[4] == "cancel" && r.Method == http.MethodPost {
		if s.jobs == nil {
			s.writeError(w, requestID, 503, "SERVICE_UNAVAILABLE", "服务暂不可用")
			return
		}
		job, err := s.jobs.manager.AdminGet(r.Context(), parts[3])
		if err != nil {
			s.jobError(w, requestID, err)
			return
		}
		if err = s.platform.RecordAudit(r.Context(), actor, "job.cancel", job.ID, map[string]string{"status": "requested"}); err != nil {
			s.adminError(w, requestID, err)
			return
		}
		canceled, err := s.jobs.manager.Cancel(r.Context(), job.OwnerID, job.ID)
		if err != nil {
			s.jobError(w, requestID, err)
			return
		}
		s.writeJSON(w, requestID, 200, jobs.AdminRecord{Job: canceled, OwnerID: job.OwnerID})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/admin/") {
		s.writeError(w, requestID, 404, "NOT_FOUND", "管理接口不存在")
	}
}

func (s *Server) adminError(w http.ResponseWriter, requestID string, err error) {
	switch {
	case errors.Is(err, platform.ErrNotFound):
		s.writeError(w, requestID, 404, "NOT_FOUND", "记录不存在")
	case errors.Is(err, platform.ErrForbidden):
		s.writeError(w, requestID, 403, "ADMIN_REQUIRED", "此操作未获授权")
	default:
		s.writeError(w, requestID, 503, "SERVICE_UNAVAILABLE", "管理服务暂不可用")
	}
}
