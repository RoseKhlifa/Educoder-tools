package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/platform"
)

func loadPlatformConfig(cfg *Config) error {
	if value := strings.TrimSpace(os.Getenv("PLATFORM_ADMIN_USER_IDS")); value != "" {
		for _, raw := range strings.Split(value, ",") {
			id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
			if err != nil || id <= 0 {
				return errors.New("invalid PLATFORM_ADMIN_USER_IDS: use verified upstream numeric IDs")
			}
			cfg.PlatformAdminUserIDs = append(cfg.PlatformAdminUserIDs, id)
		}
	}
	return nil
}

func (s *Server) AttachPlatform(store *platform.Store) { s.platform = store }

func sessionJobOwner(session Session) string {
	if session.Principal.ID != "" {
		return session.Principal.Owner()
	}
	return accountJobOwner(session.User.Login)
}

func principalDTO(user platform.User) map[string]any {
	if user.ID == "" {
		return map[string]any{"role": "user", "status": "active"}
	}
	return map[string]any{"id": user.ID, "tenant_id": user.TenantID, "role": user.Role, "status": user.Status}
}

func (s *Server) adminSession(w http.ResponseWriter, r *http.Request, requestID string) (Session, bool) {
	session, ok := s.requireSession(w, r, requestID)
	if !ok {
		return Session{}, false
	}
	if s.platform == nil || !session.Principal.IsAdmin() {
		s.writeError(w, requestID, http.StatusForbidden, "ADMIN_REQUIRED", "此操作需要管理员权限")
		return Session{}, false
	}
	return session, true
}

func (s *Server) platformLoginError(w http.ResponseWriter, requestID string, err error) {
	switch {
	case errors.Is(err, platform.ErrDisabled):
		s.writeError(w, requestID, http.StatusForbidden, "ACCOUNT_DISABLED", "账号服务暂不可用，请联系管理员")
	default:
		s.writeError(w, requestID, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "登录服务暂不可用，请稍后重试")
	}
}

type loginLimit struct {
	start time.Time
	count int
}
type loginLimiter struct {
	mu    sync.Mutex
	items map[string]loginLimit
	now   func() time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{items: make(map[string]loginLimit), now: time.Now}
}
func (l *loginLimiter) allow(key string, max int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	window := 5 * time.Minute
	for key, value := range l.items {
		if now.Sub(value.start) >= window {
			delete(l.items, key)
		}
	}
	value, ok := l.items[key]
	if !ok {
		if len(l.items) >= 10000 {
			return false
		}
		value = loginLimit{start: now}
	}
	if value.count >= max {
		return false
	}
	value.count++
	l.items[key] = value
	return true
}
func (l *loginLimiter) allowAfter(key string, interval time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if value, ok := l.items[key]; ok && now.Sub(value.start) < interval {
		return false
	}
	if len(l.items) >= 10000 {
		return false
	}
	l.items[key] = loginLimit{start: now, count: 1}
	return true
}
func loginAccountKey(value string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(value))))
	return "account:" + hex.EncodeToString(sum[:])
}
func loginSource(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
func (s *Server) loginRateError(w http.ResponseWriter, requestID string) {
	w.Header().Set("Retry-After", "300")
	s.writeError(w, requestID, http.StatusTooManyRequests, "LOGIN_RATE_LIMITED", "登录尝试较多，请稍后重试")
}

type phoneAuthClient interface {
	SendPhoneVerificationCode(context.Context, string) error
	LoginWithPhone(context.Context, string, string) (*educoder.User, error)
}

func (s *Server) handleSendPhoneCode(w http.ResponseWriter, r *http.Request, requestID string) {
	if !s.loginLimits.allow("sms-source:"+loginSource(r), 120) {
		s.loginRateError(w, requestID)
		return
	}
	var input struct {
		Phone string `json:"phone"`
	}
	if err := decodeJSON(r, &input); err != nil || !validLoginPhone(input.Phone) {
		s.writeError(w, requestID, http.StatusBadRequest, "INVALID_REQUEST", "请输入 11 位手机号")
		return
	}
	input.Phone = strings.TrimSpace(input.Phone)
	phoneKey := "sms-phone:" + loginAccountKey(input.Phone)
	// Reserve before sending. A lost upstream reply may still have sent SMS;
	// neither another tab nor an immediate retry may trigger another message.
	if !s.loginLimits.allowAfter("sms-cooldown:"+phoneKey, time.Minute) {
		w.Header().Set("Retry-After", "60")
		s.writeError(w, requestID, http.StatusTooManyRequests, "LOGIN_RATE_LIMITED", "获取验证码较频繁，请稍后重试")
		return
	}
	if !s.loginLimits.allow(phoneKey, 3) {
		s.loginRateError(w, requestID)
		return
	}
	client, ok := s.factory(s.config.APIBaseURL).(phoneAuthClient)
	if !ok {
		s.writeError(w, requestID, http.StatusServiceUnavailable, "SMS_LOGIN_UNAVAILABLE", "手机验证码登录暂不可用")
		return
	}
	if err := client.SendPhoneVerificationCode(r.Context(), input.Phone); err != nil {
		if errors.Is(err, educoder.ErrPhoneNotRegistered) {
			s.writeError(w, requestID, http.StatusBadRequest, "PHONE_NOT_REGISTERED", "该手机号尚未关联头歌账号")
			return
		}
		s.writeError(w, requestID, http.StatusBadGateway, "SMS_SEND_FAILED", "验证码发送结果未确认，请先查看短信，稍后再试")
		return
	}
	s.writeJSON(w, requestID, http.StatusOK, map[string]any{"sent": true, "retryAfterSeconds": 60})
}

func (s *Server) handlePhoneLogin(w http.ResponseWriter, r *http.Request, requestID string) {
	if !s.loginLimits.allow("source:"+loginSource(r), 120) {
		s.loginRateError(w, requestID)
		return
	}
	var input struct {
		Phone string `json:"phone"`
		Code  string `json:"code"`
	}
	if err := decodeJSON(r, &input); err != nil || !validLoginPhone(input.Phone) || strings.TrimSpace(input.Code) == "" || len(input.Code) > 128 {
		s.writeError(w, requestID, http.StatusBadRequest, "INVALID_REQUEST", "请输入手机号和短信验证码")
		return
	}
	input.Phone = strings.TrimSpace(input.Phone)
	if !s.loginLimits.allow(loginAccountKey(input.Phone), 12) {
		s.loginRateError(w, requestID)
		return
	}
	client := s.factory(s.config.APIBaseURL)
	phoneClient, ok := client.(phoneAuthClient)
	if !ok {
		s.writeError(w, requestID, http.StatusServiceUnavailable, "SMS_LOGIN_UNAVAILABLE", "手机验证码登录暂不可用")
		return
	}
	user, err := phoneClient.LoginWithPhone(r.Context(), input.Phone, input.Code)
	input.Code = ""
	if errors.Is(err, educoder.ErrLoginBindingRequired) {
		s.writeError(w, requestID, http.StatusConflict, "SMS_BINDING_REQUIRED", "请先在头歌官网完成账号绑定，再登录")
		return
	}
	if errors.Is(err, educoder.ErrLoginExamRestricted) {
		s.writeError(w, requestID, http.StatusForbidden, "SMS_RESTRICTED", "头歌账号有登录限制，请先在官网处理")
		return
	}
	if err != nil || user == nil || user.Login == "" || user.UserID <= 0 {
		s.writeError(w, requestID, http.StatusUnauthorized, "AUTH_FAILED", "手机号或验证码不正确，或验证码已失效")
		return
	}
	s.completeLogin(w, r, requestID, client, user)
}

func validLoginPhone(phone string) bool {
	phone = strings.TrimSpace(phone)
	if len(phone) != 11 || phone[0] != '1' {
		return false
	}
	for _, digit := range phone {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

func (s *Server) completeLogin(w http.ResponseWriter, r *http.Request, requestID string, client EducoderClient, user *educoder.User) string {
	ctx := r.Context()
	var principal platform.User
	var err error
	if s.platform != nil {
		principal, err = s.platform.UpsertLogin(ctx, platform.Identity{UpstreamID: user.UserID, Login: user.Login, Name: userDisplayName(*user)})
		if err != nil {
			s.platformLoginError(w, requestID, err)
			return ""
		}
	}
	if old, err := r.Cookie(sessionCookieName); err == nil {
		s.store.Delete(old.Value)
	}
	sessionID, err := s.store.createPrincipal(client, *user, resolveAvatarKey(ctx, client, *user), principal)
	if err != nil {
		s.writeError(w, requestID, http.StatusInternalServerError, "SESSION_CREATE_FAILED", "创建会话失败")
		return ""
	}
	s.setSessionCookie(w, sessionID)
	response := map[string]any{"status": "succeeded", "user": toUserDTO(*user), "platform": principalDTO(principal)}
	if s.jobs != nil && s.jobs.collect != nil {
		s.enqueueCollection(ctx, client, sessionJobOwner(Session{User: *user, Principal: principal}), "")
	}
	s.writeJSON(w, requestID, http.StatusOK, response)
	return sessionID
}
