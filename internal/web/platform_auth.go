package web

import (
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
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.authError(w, requestID)
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
