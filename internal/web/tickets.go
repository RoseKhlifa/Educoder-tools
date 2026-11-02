package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"educoder-cli/internal/platform"
)

// Ticket contents never enter request tracing or audit metadata. They are
// returned only to the ticket owner or an authenticated administrator.
func (s *Server) handleTickets(w http.ResponseWriter, r *http.Request, requestID string) {
	admin := strings.HasPrefix(r.URL.Path, "/api/admin/")
	var session Session
	var ok bool
	if admin {
		session, ok = s.adminSession(w, r, requestID)
	} else {
		session, ok = s.sessionFromRequest(r)
		if !ok {
			s.authError(w, requestID)
		}
	}
	if !ok {
		return
	}
	if s.platform == nil || session.Principal.ID == "" {
		s.writeError(w, requestID, 503, "TICKETS_UNAVAILABLE", "反馈服务暂不可用，请稍后重试")
		return
	}
	parts := splitPath(r.URL.Path)
	base := 2
	if admin {
		base = 3
	}
	actor := session.Principal.ID
	if len(parts) == base {
		switch r.Method {
		case http.MethodGet:
			limit := 30
			if raw := r.URL.Query().Get("limit"); raw != "" {
				var err error
				limit, err = strconv.Atoi(raw)
				if err != nil || limit < 1 || limit > 100 {
					s.ticketError(w, requestID, platform.ErrTicketInvalid)
					return
				}
			}
			page, err := s.platform.ListTickets(r.Context(), actor, admin, platform.TicketListOptions{Limit: limit, Cursor: r.URL.Query().Get("cursor"), Status: r.URL.Query().Get("status")})
			if err != nil {
				s.ticketError(w, requestID, err)
				return
			}
			s.writeJSON(w, requestID, 200, page)
		case http.MethodPost:
			if admin {
				w.Header().Set("Allow", "GET")
				s.methodError(w, requestID)
				return
			}
			var input struct {
				Subject  string `json:"subject"`
				Category string `json:"category"`
				Body     string `json:"body"`
				Key      string `json:"idempotency_key"`
			}
			if decodeJSON(r, &input) != nil {
				s.ticketError(w, requestID, platform.ErrTicketInvalid)
				return
			}
			detail, created, err := s.platform.CreateTicket(r.Context(), actor, platform.CreateTicketInput{Subject: input.Subject, Category: input.Category, Body: input.Body, IdempotencyKey: input.Key})
			if err != nil {
				s.ticketError(w, requestID, err)
				return
			}
			status := 200
			if created {
				status = 201
			}
			s.writeJSON(w, requestID, status, detail)
		default:
			w.Header().Set("Allow", "GET, POST")
			if admin {
				w.Header().Set("Allow", "GET")
			}
			s.methodError(w, requestID)
		}
		return
	}
	if len(parts) < base+1 || len(parts) > base+2 {
		s.writeError(w, requestID, 404, "NOT_FOUND", "反馈记录不存在")
		return
	}
	id := parts[base]
	if len(id) != 32 {
		s.writeError(w, requestID, 404, "NOT_FOUND", "反馈记录不存在")
		return
	}
	if len(parts) == base+1 {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			s.methodError(w, requestID)
			return
		}
		detail, err := s.platform.GetTicket(r.Context(), actor, id, admin)
		if err != nil {
			s.ticketError(w, requestID, err)
			return
		}
		s.writeJSON(w, requestID, 200, detail)
		return
	}
	if parts[base+1] != "messages" && parts[base+1] != "status" {
		s.writeError(w, requestID, 404, "NOT_FOUND", "反馈接口不存在")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		s.methodError(w, requestID)
		return
	}
	if parts[base+1] == "messages" {
		var input struct {
			Body string `json:"body"`
			Key  string `json:"idempotency_key"`
		}
		if decodeJSON(r, &input) != nil {
			s.ticketError(w, requestID, platform.ErrTicketInvalid)
			return
		}
		detail, created, err := s.platform.ReplyTicket(r.Context(), actor, id, input.Body, input.Key, admin)
		if err != nil {
			s.ticketError(w, requestID, err)
			return
		}
		status := 200
		if created {
			status = 201
		}
		s.writeJSON(w, requestID, status, detail)
		return
	}
	var input struct {
		Status string `json:"status"`
	}
	if decodeJSON(r, &input) != nil {
		s.ticketError(w, requestID, platform.ErrTicketInvalid)
		return
	}
	detail, err := s.platform.SetTicketStatus(r.Context(), actor, id, input.Status, admin)
	if err != nil {
		s.ticketError(w, requestID, err)
		return
	}
	s.writeJSON(w, requestID, 200, detail)
}

func (s *Server) ticketError(w http.ResponseWriter, requestID string, err error) {
	switch {
	case errors.Is(err, platform.ErrTicketInvalid):
		s.writeError(w, requestID, 400, "TICKET_INVALID", "请检查分类、标题和内容；标题最多100字、内容最多5000字，请勿提交密码、Cookie或密钥")
	case errors.Is(err, platform.ErrTicketConflict):
		s.writeError(w, requestID, 409, "TICKET_CONFLICT", "这次操作与已有记录冲突，或工单已关闭，请刷新后重试")
	case errors.Is(err, platform.ErrTicketLimit):
		w.Header().Set("Retry-After", "60")
		s.writeError(w, requestID, 429, "TICKET_RATE_LIMITED", "反馈较频繁或工单数量达到上限，请稍后再试，或先处理已有工单")
	case errors.Is(err, platform.ErrNotFound):
		s.writeError(w, requestID, 404, "NOT_FOUND", "反馈记录不存在")
	case errors.Is(err, platform.ErrDisabled):
		s.authError(w, requestID)
	case errors.Is(err, platform.ErrForbidden):
		s.writeError(w, requestID, 403, "ADMIN_REQUIRED", "此操作需要管理员权限")
	default:
		s.writeError(w, requestID, 503, "TICKETS_UNAVAILABLE", "反馈服务暂不可用，请稍后重试")
	}
}
