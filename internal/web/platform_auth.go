package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/netip"
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
	if value, ok := os.LookupEnv("TRUSTED_PROXY_CIDRS"); ok && strings.TrimSpace(value) != "" {
		// An empty value keeps the defaults so an unset deployment variable
		// cannot silently collapse every student into one limiter bucket.
		if strings.EqualFold(strings.TrimSpace(value), "none") {
			cfg.TrustedProxies = []netip.Prefix{}
			return nil
		}
		proxies, err := parseTrustedProxies(value)
		if err != nil {
			return err
		}
		cfg.TrustedProxies = proxies
	}
	return nil
}

// The service publishes one port to a reverse proxy that terminates TLS on the
// same host, so the connecting address of a real user is the proxy, never the
// student. Peers inside these ranges may state the client address they are
// forwarding; TRUSTED_PROXY_CIDRS overrides the list and "none" disables it.
// touge.mom is served through Cloudflare, so the hop in front of OpenResty is
// a Cloudflare edge. Trusting Cloudflare's published ranges
// (www.cloudflare.com/ips, checked 2026-11-21) lets the walk reach the
// student's own address that Cloudflare put in X-Forwarded-For; without it
// every student in one region would share an edge address and one budget.
var defaultTrustedProxyCIDRs = []string{"127.0.0.0/8", "::1/128", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16", "fc00::/7", "fe80::/10",
	"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22", "141.101.64.0/18", "108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20",
	"197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13", "104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
	"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32", "2405:8100::/32", "2a06:98c0::/29", "2c0f:f248::/32"}

// Per-address budgets are sized for a campus NAT: one classroom or one
// carrier gateway can put a hundred students behind a single address. Brute
// force is stopped per account and per phone number, SMS breadth by the
// service-wide budget; the per-address budget only slows one noisy client.
const (
	loginSourceBudget  = 600
	smsSourceBudget    = 200
	wechatSourceBudget = 300
)

func defaultTrustedProxies() []netip.Prefix {
	proxies, err := parseTrustedProxies(strings.Join(defaultTrustedProxyCIDRs, ","))
	if err != nil {
		return []netip.Prefix{}
	}
	return proxies
}

func parseTrustedProxies(value string) ([]netip.Prefix, error) {
	proxies := []netip.Prefix{}
	for _, raw := range strings.Split(value, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(raw); err == nil {
			proxies = append(proxies, prefix.Masked())
			continue
		}
		addr, err := netip.ParseAddr(raw)
		if err != nil {
			return nil, errors.New("invalid TRUSTED_PROXY_CIDRS: use IP addresses or CIDR ranges, or none")
		}
		addr = normalizeAddr(addr)
		proxies = append(proxies, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return proxies, nil
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

func normalizeAddr(addr netip.Addr) netip.Addr { return addr.Unmap().WithZone("") }

func requestPeer(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	host = strings.Trim(host, "[]")
	if addr, err := netip.ParseAddr(host); err == nil {
		return normalizeAddr(addr).String()
	}
	return host
}

func (s *Server) trustedProxy(value string) bool {
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return false
	}
	addr = normalizeAddr(addr)
	for _, prefix := range s.config.TrustedProxies {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// forwardedClients lists the addresses a proxy chain claims, nearest hop first.
// An entry that is not an address ends the chain: nothing further left can be
// attributed to a trusted hop, so it is ignored rather than trusted.
func forwardedClients(r *http.Request) []string {
	headers := r.Header.Values("X-Forwarded-For")
	if len(headers) == 0 {
		headers = r.Header.Values("X-Real-IP")
	}
	var fields []string
	for _, header := range headers {
		fields = append(fields, strings.Split(header, ",")...)
	}
	if len(fields) > 16 {
		fields = fields[len(fields)-16:]
	}
	clients := make([]string, 0, len(fields))
	for i := len(fields) - 1; i >= 0; i-- {
		value := strings.TrimSpace(fields[i])
		if host, _, err := net.SplitHostPort(value); err == nil {
			value = host
		}
		addr, err := netip.ParseAddr(strings.Trim(value, "[]"))
		if err != nil {
			break
		}
		clients = append(clients, normalizeAddr(addr).String())
	}
	return clients
}

// loginSource identifies one client for the per-source login budgets. Behind
// the deployment's reverse proxy every student connects from the same address,
// which would make one shared bucket both useless against a single abuser and
// an outage for everyone else, so a forwarded address is believed when - and
// only when - the peer is a trusted proxy. A direct caller's headers are
// ignored, so nobody can choose their own bucket.
func (s *Server) loginSource(r *http.Request) string {
	peer := requestPeer(r)
	if !s.trustedProxy(peer) {
		return peer
	}
	for _, client := range forwardedClients(r) {
		if !s.trustedProxy(client) {
			return client
		}
	}
	return peer
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
	if !s.loginLimits.allow("sms-source:"+s.loginSource(r), smsSourceBudget) {
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
	// Per-phone limits protect one victim; this bounds how many different
	// numbers the whole service can text in a window, so a caller that rotates
	// numbers cannot turn the login page into an SMS cannon. It is consumed
	// only once a request is about to reach the upstream sender.
	if !s.loginLimits.allow("sms-global", 300) {
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
	if !s.loginLimits.allow("source:"+s.loginSource(r), loginSourceBudget) {
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
		s.enqueueLoginCollection(ctx, client, sessionJobOwner(Session{User: *user, Principal: principal}))
	}
	s.writeJSON(w, requestID, http.StatusOK, response)
	return sessionID
}
