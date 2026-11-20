package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"sync"
	"time"

	"educoder-cli/internal/educoder"
)

const wechatAttemptCookie = "educoder_wechat_attempt"
const wechatAttemptTTL = 5 * time.Minute

type wechatAuthClient interface {
	StartWeChatLogin(context.Context) (*educoder.WeChatLoginQR, error)
	PollWeChatLogin(context.Context, string, string) (*educoder.WeChatLoginPoll, error)
	CompleteWeChatLogin(context.Context, string) (*educoder.User, error)
}

// A login transaction owns one upstream client and one opaque, browser-bound
// cookie. OAuth codes, QR UUIDs and Educoder sessions never leave this store.
type wechatLoginAttempt struct {
	mu        sync.Mutex
	client    EducoderClient
	auth      wechatAuthClient
	source    string
	uuid      string
	image     []byte
	status    string
	last      string
	code      string // fixed public error code only, never an OAuth code
	sessionID string
	polling   bool
	expires   time.Time
	ctx       context.Context
	cancel    context.CancelFunc
}

type wechatLoginStore struct {
	mu       sync.Mutex
	attempts map[string]*wechatLoginAttempt
	now      func() time.Time
}

func newWeChatLoginStore() *wechatLoginStore {
	return &wechatLoginStore{attempts: make(map[string]*wechatLoginAttempt), now: time.Now}
}

func (s *wechatLoginStore) create(source string, client EducoderClient, auth wechatAuthClient) (string, *wechatLoginAttempt, error) {
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", nil, err
	}
	id := base64.RawURLEncoding.EncodeToString(token[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	live := 0
	for key, attempt := range s.attempts {
		if !attempt.expires.After(now) {
			attempt.cancel()
			delete(s.attempts, key)
			continue
		}
		if attempt.source == source {
			live++
		}
	}
	// Bound image storage and in-flight QR creation, including requests that
	// have not yet received a cookie. Each attempt also has a fixed deadline.
	// A transaction exists before its browser can prove anything, so one
	// caller discarding its cookie must not be able to hold every slot and
	// make WeChat login unavailable for the whole campus.
	if len(s.attempts) >= wechatAttemptCapacity || live >= wechatAttemptsPerSource {
		return "", nil, errors.New("login transaction capacity reached")
	}
	ctx, cancel := context.WithTimeout(context.Background(), wechatAttemptTTL)
	attempt := &wechatLoginAttempt{client: client, auth: auth, source: source, status: "waiting", expires: now.Add(wechatAttemptTTL), ctx: ctx, cancel: cancel}
	s.attempts[id] = attempt
	return id, attempt, nil
}

// A real QR image is about 30 KB and is capped at 256 KB, so 512 live attempts
// hold at most 128 MB and typically a few MB. One address may hold 64: enough
// for a classroom behind one NAT, while no single client can take the store.
const (
	wechatAttemptCapacity   = 512
	wechatAttemptsPerSource = 64
)

func (s *wechatLoginStore) get(id string) *wechatLoginAttempt {
	s.mu.Lock()
	defer s.mu.Unlock()
	attempt := s.attempts[id]
	if attempt != nil && !attempt.expires.After(s.now()) {
		attempt.cancel()
		delete(s.attempts, id)
		return nil
	}
	return attempt
}

func (s *wechatLoginStore) remove(id string) {
	s.mu.Lock()
	if attempt := s.attempts[id]; attempt != nil {
		attempt.cancel()
	}
	delete(s.attempts, id)
	s.mu.Unlock()
}

func (s *Server) wechatAttempt(r *http.Request) (string, *wechatLoginAttempt) {
	cookie, err := r.Cookie(wechatAttemptCookie)
	if err != nil || len(cookie.Value) != 43 {
		return "", nil
	}
	return cookie.Value, s.wechatLogins.get(cookie.Value)
}

func (s *Server) setWeChatCookie(w http.ResponseWriter, id string) {
	maxAge := int(wechatAttemptTTL / time.Second)
	if id == "" {
		maxAge = -1
	}
	http.SetCookie(w, &http.Cookie{Name: wechatAttemptCookie, Value: id, Path: "/api/auth/wechat", MaxAge: maxAge, HttpOnly: true, Secure: s.config.CookieSecure, SameSite: http.SameSiteStrictMode})
}

func (s *Server) cancelWeChatAttempt(r *http.Request) {
	id, attempt := s.wechatAttempt(r)
	if attempt == nil {
		return
	}
	// Cancel network work before waiting for finalization's mutex. If an
	// in-flight callback already completed, revoke just that attempt's session.
	attempt.cancel()
	attempt.mu.Lock()
	attempt.status = "canceled"
	attempt.image = nil
	if attempt.sessionID != "" {
		s.store.Delete(attempt.sessionID)
	}
	attempt.mu.Unlock()
	s.wechatLogins.remove(id)
}

func (s *Server) handleWeChatLogin(w http.ResponseWriter, r *http.Request, requestID, action string) {
	if action != "start" && action != "poll" && action != "cancel" && action != "image" {
		s.writeError(w, requestID, http.StatusNotFound, "NOT_FOUND", "接口不存在")
		return
	}
	if action == "image" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			s.methodError(w, requestID)
			return
		}
		s.wechatImage(w, r, requestID)
		return
	}
	if r.Method != http.MethodPost {
		s.methodError(w, requestID)
		return
	}
	var input struct{}
	if r.URL.RawQuery != "" || decodeJSON(r, &input) != nil {
		s.writeError(w, requestID, http.StatusBadRequest, "INVALID_REQUEST", "请求参数不正确")
		return
	}
	switch action {
	case "cancel":
		s.cancelWeChatAttempt(r)
		s.setWeChatCookie(w, "")
		s.writeJSON(w, requestID, http.StatusOK, map[string]any{"status": "canceled"})
	case "start":
		s.startWeChatLogin(w, r, requestID)
	case "poll":
		s.pollWeChatLogin(w, r, requestID)
	}
}

func (s *Server) startWeChatLogin(w http.ResponseWriter, r *http.Request, requestID string) {
	source := s.loginSource(r)
	if !s.loginLimits.allow("wechat-source:"+source, wechatSourceBudget) {
		s.loginRateError(w, requestID)
		return
	}
	s.cancelWeChatAttempt(r)
	client := s.factory(s.config.APIBaseURL)
	auth, ok := client.(wechatAuthClient)
	if !ok {
		s.wechatLoginError(w, requestID, "WECHAT_LOGIN_UNAVAILABLE")
		return
	}
	id, attempt, err := s.wechatLogins.create(source, client, auth)
	if err != nil {
		s.wechatLoginError(w, requestID, "WECHAT_LOGIN_UNAVAILABLE")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
	defer cancel()
	qr, err := auth.StartWeChatLogin(ctx)
	if err != nil || qr == nil || qr.UUID == "" || len(qr.Image) == 0 || len(qr.Image) > 1<<20 {
		s.wechatLogins.remove(id)
		s.wechatLoginError(w, requestID, "WECHAT_LOGIN_UNAVAILABLE")
		return
	}
	attempt.mu.Lock()
	attempt.uuid, attempt.image = qr.UUID, qr.Image
	attempt.mu.Unlock()
	s.setWeChatCookie(w, id)
	s.writeJSON(w, requestID, http.StatusOK, map[string]any{"status": "waiting", "expiresInSeconds": int(wechatAttemptTTL / time.Second)})
}

func (s *Server) wechatImage(w http.ResponseWriter, r *http.Request, requestID string) {
	_, attempt := s.wechatAttempt(r)
	if attempt == nil {
		s.wechatLoginError(w, requestID, "WECHAT_LOGIN_EXPIRED")
		return
	}
	attempt.mu.Lock()
	defer attempt.mu.Unlock()
	if len(attempt.image) == 0 || attempt.status == "canceled" || attempt.status == "expired" {
		s.wechatLoginError(w, requestID, "WECHAT_LOGIN_EXPIRED")
		return
	}
	w.Header().Set("Content-Type", http.DetectContentType(attempt.image))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(attempt.image)
}

func (s *Server) pollWeChatLogin(w http.ResponseWriter, r *http.Request, requestID string) {
	_, attempt := s.wechatAttempt(r)
	if attempt == nil {
		s.wechatLoginError(w, requestID, "WECHAT_LOGIN_EXPIRED")
		return
	}
	attempt.mu.Lock()
	if attempt.sessionID != "" {
		defer attempt.mu.Unlock()
		check := r.Clone(r.Context())
		check.Header.Del("Cookie")
		check.AddCookie(&http.Cookie{Name: sessionCookieName, Value: attempt.sessionID})
		if session, state := s.lookupSession(check); state == sessionValid && attempt.status == "succeeded" {
			// Recover a lost success response without consuming the OAuth code
			// twice, duplicating collection, or creating another browser session.
			s.setSessionCookie(w, attempt.sessionID)
			s.writeJSON(w, requestID, http.StatusOK, map[string]any{"status": "succeeded", "user": toUserDTO(session.User), "platform": principalDTO(session.Principal)})
		} else {
			s.wechatLoginError(w, requestID, "WECHAT_LOGIN_EXPIRED")
		}
		return
	}
	if attempt.code != "" {
		code := attempt.code
		attempt.mu.Unlock()
		s.wechatLoginError(w, requestID, code)
		return
	}
	if attempt.ctx.Err() != nil || attempt.status == "expired" || attempt.status == "canceled" {
		status := attempt.status
		if status != "canceled" {
			status = "expired"
		}
		attempt.mu.Unlock()
		s.writeJSON(w, requestID, http.StatusOK, map[string]any{"status": status})
		return
	}
	if attempt.polling {
		status := attempt.status
		attempt.mu.Unlock()
		s.writeJSON(w, requestID, http.StatusOK, map[string]any{"status": status})
		return
	}
	attempt.polling = true
	last, uuid := attempt.last, attempt.uuid
	attempt.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
	stop := context.AfterFunc(attempt.ctx, cancel)
	defer cancel()
	defer stop()
	result, err := attempt.auth.PollWeChatLogin(ctx, uuid, last)
	attempt.mu.Lock()
	defer attempt.mu.Unlock()
	attempt.polling = false
	if attempt.ctx.Err() != nil {
		status := "expired"
		if attempt.status == "canceled" {
			status = "canceled"
		}
		s.writeJSON(w, requestID, http.StatusOK, map[string]any{"status": status})
		return
	}
	if err != nil || result == nil {
		if errors.Is(err, context.Canceled) && r.Context().Err() != nil {
			// Hiding a page can abort its transport. No callback started, so
			// keep this transaction available when the browser resumes.
			return
		}
		if errors.Is(err, context.DeadlineExceeded) && r.Context().Err() == nil {
			s.writeJSON(w, requestID, http.StatusOK, map[string]any{"status": attempt.status})
			return
		}
		attempt.code = "WECHAT_LOGIN_FAILED"
		s.wechatLoginError(w, requestID, attempt.code)
		return
	}
	switch result.Status {
	case "waiting", "scanned", "canceled", "expired":
		attempt.status = result.Status
		// Report the state already observed so the next request long-polls
		// instead of returning at once. Without it every poll completes
		// immediately and one waiting QR costs hundreds of upstream requests
		// instead of a handful.
		if result.Status == "waiting" || result.Status == "scanned" {
			attempt.last = result.Status
		}
		s.writeJSON(w, requestID, http.StatusOK, map[string]any{"status": attempt.status})
	case "confirmed":
		// Code consumption is deliberately at most once. Any uncertain callback
		// result ends this attempt; refreshing creates a new consent flow.
		attempt.code = "WECHAT_LOGIN_FAILED"
		// A confirmation may arrive at the end of the 35-second long poll.
		// Give callback + identity verification their own bounded budget. Once
		// consumption starts, a lost browser response must not abort it: the
		// next poll can recover its outcome. Explicit cancel still stops it.
		completionCtx, completionCancel := context.WithTimeout(attempt.ctx, 20*time.Second)
		defer completionCancel()
		user, completeErr := attempt.auth.CompleteWeChatLogin(completionCtx, result.Code)
		result.Code = ""
		if attempt.ctx.Err() != nil || completeErr != nil || user == nil || user.UserID <= 0 || user.Login == "" {
			attempt.code = wechatAuthErrorCode(completeErr)
			s.wechatLoginError(w, requestID, attempt.code)
			return
		}
		attempt.sessionID = s.completeLogin(w, r.Clone(completionCtx), requestID, attempt.client, user)
		if attempt.sessionID != "" {
			attempt.code = ""
			attempt.status = "succeeded"
			// A consumed QR is never shown again, so stop retaining it.
			attempt.image = nil
		}
	default:
		attempt.code = "WECHAT_LOGIN_FAILED"
		s.wechatLoginError(w, requestID, attempt.code)
	}
}

func wechatAuthErrorCode(err error) string {
	// Only fixed sentinels may influence public copy; upstream text remains
	// private even when an upstream endpoint echoes a code or cookie.
	if errors.Is(err, educoder.ErrLoginBindingRequired) {
		return "WECHAT_BINDING_REQUIRED"
	}
	if errors.Is(err, educoder.ErrLoginExamRestricted) {
		return "WECHAT_RESTRICTED"
	}
	return "WECHAT_LOGIN_FAILED"
}

func (s *Server) wechatLoginError(w http.ResponseWriter, requestID, code string) {
	message := "微信登录暂未完成，请刷新二维码后重试"
	status := http.StatusBadGateway
	switch code {
	case "WECHAT_LOGIN_EXPIRED":
		status, message = http.StatusGone, "二维码已过期，请重新获取"
	case "WECHAT_LOGIN_UNAVAILABLE":
		status, message = http.StatusServiceUnavailable, "微信登录暂不可用，请稍后重试"
	case "WECHAT_BINDING_REQUIRED":
		status, message = http.StatusConflict, "请先在头歌官网关联现有账号，再重新扫码"
	case "WECHAT_RESTRICTED":
		status, message = http.StatusForbidden, "头歌账号有登录限制，请先在官网处理"
	}
	s.writeError(w, requestID, status, code, message)
}
