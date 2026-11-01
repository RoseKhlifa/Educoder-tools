package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	buildinfo "educoder-cli"
	"educoder-cli/internal/avatar"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/platform"
)

const sessionCookieName = "educoder_web_session"

type Config struct {
	HTTPAddr             string
	WebOrigin            string
	APIBaseURL           string
	SessionTTL           time.Duration
	StaticDir            string
	CookieSecure         bool
	DataDir              string
	JobWorkers           int
	JobQueueSize         int
	JobPerOwner          int
	JobPerOwnerRunning   int
	JobTimeout           time.Duration
	ModelConcurrency     int
	ModelTimeout         time.Duration
	ModelAPIKeyFile      string
	ModelBaseURL         string
	PlatformAdminUserIDs []int64
}

func DefaultConfig() Config {
	return Config{HTTPAddr: "0.0.0.0:11454", APIBaseURL: educoder.DefaultBaseURL, SessionTTL: 8 * time.Hour, StaticDir: filepath.FromSlash("web"), DataDir: "data", JobWorkers: 16, JobQueueSize: 256, JobPerOwner: 16, JobPerOwnerRunning: 4, JobTimeout: 30 * time.Minute, ModelConcurrency: 8, ModelTimeout: 2 * time.Minute}
}

func LoadConfig() (Config, error) {
	cfg := DefaultConfig()
	if value := strings.TrimSpace(os.Getenv("HTTP_ADDR")); value != "" {
		cfg.HTTPAddr = value
	}
	if value := strings.TrimSpace(os.Getenv("WEB_ORIGIN")); value != "" {
		cfg.WebOrigin = strings.TrimRight(value, "/")
	}
	if value := strings.TrimSpace(os.Getenv("EDUCODER_API_BASE_URL")); value != "" {
		cfg.APIBaseURL = strings.TrimRight(value, "/")
	}
	if value := strings.TrimSpace(os.Getenv("SESSION_TTL")); value != "" {
		ttl, err := time.ParseDuration(value)
		if err != nil || ttl <= 0 {
			return Config{}, fmt.Errorf("invalid SESSION_TTL")
		}
		cfg.SessionTTL = ttl
	}
	if value := strings.TrimSpace(os.Getenv("WEB_STATIC_DIR")); value != "" {
		cfg.StaticDir = value
	}
	if value := strings.TrimSpace(os.Getenv("WEB_COOKIE_SECURE")); value != "" {
		secure, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, errors.New("invalid WEB_COOKIE_SECURE")
		}
		cfg.CookieSecure = secure
	}
	if cfg.WebOrigin != "" && !validOrigin(cfg.WebOrigin) {
		return Config{}, errors.New("invalid WEB_ORIGIN: expected an exact http(s) origin")
	}
	if err := loadJobConfig(&cfg); err != nil {
		return Config{}, err
	}
	if err := loadPlatformConfig(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

type ClientFactory func(baseURL string) EducoderClient

type Server struct {
	config              Config
	store               *SessionStore
	factory             ClientFactory
	static              http.Handler
	avatars             *avatar.Service
	jobs                *JobRuntime
	platform            *platform.Store
	loginLimits         *loginLimiter
	questionBankSummary questionBankSummaryCache
}

func NewServer(config Config) *Server {
	return NewServerWithFactory(config, func(baseURL string) EducoderClient { return educoder.New(baseURL) })
}

func NewServerWithFactory(config Config, factory ClientFactory) *Server {
	if config.HTTPAddr == "" {
		config.HTTPAddr = "0.0.0.0:11454"
	}
	if strings.HasPrefix(strings.ToLower(config.WebOrigin), "https://") {
		config.CookieSecure = true
	}
	if config.APIBaseURL == "" {
		config.APIBaseURL = educoder.DefaultBaseURL
	}
	if config.SessionTTL <= 0 {
		config.SessionTTL = 8 * time.Hour
	}
	if config.StaticDir == "" {
		config.StaticDir = filepath.FromSlash("web")
	}
	if factory == nil {
		factory = func(baseURL string) EducoderClient { return educoder.New(baseURL) }
	}
	s := &Server{config: config, store: NewSessionStore(config.SessionTTL), factory: factory, avatars: avatar.New(), loginLimits: newLoginLimiter()}
	s.static = newStaticHandler(config.StaticDir)
	return s
}

func (s *Server) Store() *SessionStore { return s.store }

func (s *Server) Handler() http.Handler { return s }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestID := newRequestID()
	w.Header().Set("X-Request-Id", requestID)
	w.Header().Set("X-App-Version", buildinfo.Current())
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
	if r.URL.Path == "/healthz" || r.URL.Path == "/version" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			s.writeError(w, requestID, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "请求方法不支持")
			return
		}
		var result any = buildinfo.Release()
		if r.URL.Path == "/healthz" {
			result = struct {
				Status string `json:"status"`
				buildinfo.Info
			}{Status: "ok", Info: buildinfo.Release()}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(result)
		}
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		if !s.cors(w, r, requestID) {
			return
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		s.handleAPI(w, r, requestID)
		return
	}
	s.static.ServeHTTP(w, r)
}

// The deployed browser and API share one origin. WEB_ORIGIN is an optional exact
// development origin; a wildcard never grants credentialed access. Matching
// the request Host also works behind the user's TLS-terminating reverse proxy.
func (s *Server) cors(w http.ResponseWriter, r *http.Request, requestID string) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			s.writeError(w, requestID, http.StatusForbidden, "ORIGIN_NOT_ALLOWED", "请求来源不匹配")
			return false
		}
		return true
	}
	parsed, err := url.Parse(origin)
	allowed := err == nil && validOrigin(origin)
	if s.config.WebOrigin != "" {
		allowed = allowed && origin == s.config.WebOrigin
	} else {
		allowed = allowed && strings.EqualFold(parsed.Host, r.Host)
	}
	if !allowed {
		s.writeError(w, requestID, http.StatusForbidden, "ORIGIN_NOT_ALLOWED", "请求来源不匹配")
		return false
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Request-Id, Idempotency-Key")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Add("Vary", "Origin")
	return true
}

func validOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.User == nil && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == ""
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.URL.Path == "/api/question-bank/summary" {
		s.handleQuestionBankSummary(w, r, requestID)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/admin/") {
		s.handleAdmin(w, r, requestID)
		return
	}
	if r.URL.Path == "/api/jobs" || strings.HasPrefix(r.URL.Path, "/api/jobs/") {
		s.handleJobs(w, r, requestID)
		return
	}
	if r.URL.Path == "/api/avatar" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			s.methodError(w, requestID)
			return
		}
		session, ok := s.sessionFromRequest(r)
		if !ok {
			s.authError(w, requestID)
			return
		}
		if r.URL.RawQuery != "" {
			s.writeError(w, requestID, http.StatusBadRequest, "INVALID_REQUEST", "头像接口不接受自定义地址")
			return
		}
		s.avatars.Serve(w, r, session.AvatarKey)
		return
	}
	parts := splitPath(r.URL.Path)
	if len(parts) == 2 && parts[0] == "api" && parts[1] == "collections" {
		s.handleCollection(w, r, requestID)
		return
	}
	if len(parts) == 3 && parts[0] == "api" && parts[1] == "auth" && parts[2] == "login" {
		if r.Method != http.MethodPost {
			s.methodError(w, requestID)
			return
		}
		s.handleLogin(w, r, requestID)
		return
	}
	if len(parts) == 3 && parts[0] == "api" && parts[1] == "auth" && parts[2] == "me" {
		if r.Method != http.MethodGet {
			s.methodError(w, requestID)
			return
		}
		s.handleMe(w, r, requestID)
		return
	}
	if len(parts) == 3 && parts[0] == "api" && parts[1] == "auth" && parts[2] == "logout" {
		if r.Method != http.MethodPost {
			s.methodError(w, requestID)
			return
		}
		s.handleLogout(w, r, requestID)
		return
	}
	if len(parts) == 2 && parts[0] == "api" && parts[1] == "courses" {
		if r.Method != http.MethodGet {
			s.methodError(w, requestID)
			return
		}
		s.handleCourses(w, r, requestID)
		return
	}
	if len(parts) >= 4 && parts[0] == "api" && parts[1] == "courses" {
		if r.Method != http.MethodGet {
			s.methodError(w, requestID)
			return
		}
		course := parts[2]
		if len(parts) == 4 && parts[3] == "homeworks" {
			s.handleHomeworks(w, r, requestID, course)
			return
		}
		if len(parts) == 4 && parts[3] == "exercises" {
			s.handleExercises(w, r, requestID, course)
			return
		}
		if len(parts) == 6 && (parts[3] == "homeworks" || parts[3] == "tasks") && parts[5] == "enter" {
			s.handleEnter(w, r, requestID, course, parts[4])
			return
		}
	}
	s.writeError(w, requestID, http.StatusNotFound, "NOT_FOUND", "接口不存在")
}

func splitPath(path string) []string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request, requestID string) {
	var input struct {
		Login    string `json:"login"`
		Password string `json:"password"`
	}
	if !s.loginLimits.allow("source:"+loginSource(r), 120) {
		s.loginRateError(w, requestID)
		return
	}
	if err := decodeJSON(r, &input); err != nil || strings.TrimSpace(input.Login) == "" || input.Password == "" || len(input.Login) > 256 || len(input.Password) > 4096 {
		s.writeError(w, requestID, http.StatusBadRequest, "INVALID_REQUEST", "请输入账号和密码")
		return
	}
	if !s.loginLimits.allow(loginAccountKey(input.Login), 12) {
		s.loginRateError(w, requestID)
		return
	}
	client := s.factory(s.config.APIBaseURL)
	ctx := r.Context()
	user, err := client.Login(ctx, strings.TrimSpace(input.Login), input.Password)
	input.Password = ""
	if err != nil || user == nil || user.Login == "" || (s.platform != nil && user.UserID <= 0) {
		s.writeError(w, requestID, http.StatusUnauthorized, "AUTH_FAILED", "登录失败，请检查账号或密码")
		return
	}
	var principal platform.User
	if s.platform != nil {
		principal, err = s.platform.UpsertLogin(ctx, platform.Identity{UpstreamID: user.UserID, Login: user.Login, Name: user.Name})
		if err != nil {
			s.platformLoginError(w, requestID, err)
			return
		}
	}
	if old, err := r.Cookie(sessionCookieName); err == nil {
		s.store.Delete(old.Value)
	}
	// Store only the public user fields returned by the upstream client.
	sessionID, err := s.store.createPrincipal(client, *user, resolveAvatarKey(r.Context(), client, *user), principal)
	if err != nil {
		s.writeError(w, requestID, http.StatusInternalServerError, "SESSION_CREATE_FAILED", "创建会话失败")
		return
	}
	s.setSessionCookie(w, sessionID)
	response := map[string]any{"user": toUserDTO(*user), "platform": principalDTO(principal)}
	if s.jobs != nil && s.jobs.collect != nil {
		s.enqueueCollection(r.Context(), client, sessionJobOwner(Session{User: *user, Principal: principal}), "")
	}
	s.writeJSON(w, requestID, http.StatusOK, response)
}

// Profile requests stay on the authenticated protocol client. Only a public
// User resource key is retained; the browser never receives the upstream URL,
// image query string, cookies or arbitrary external fetch capability.
func resolveAvatarKey(ctx context.Context, client EducoderClient, user educoder.User) string {
	if recorder, ok := client.(interface {
		RecordedRequest(context.Context, string, string, url.Values, json.RawMessage) (*educoder.RecordedResponse, error)
	}); ok {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		response, err := recorder.RecordedRequest(ctx, http.MethodGet, "/api/users/get_user_info.json", url.Values{"school": {"1"}}, nil)
		if err == nil && response != nil && response.StatusCode == http.StatusOK {
			var profile map[string]json.RawMessage
			if json.Unmarshal(response.Body, &profile) == nil {
				for _, field := range []string{"image_url", "avatar_url", "avatar", "image"} {
					var value string
					if json.Unmarshal(profile[field], &value) == nil {
						if key, ok := avatar.Key(value); ok {
							return key
						}
					}
				}
			}
		}
	}
	if user.UserID > 0 {
		return strconv.FormatInt(user.UserID, 10)
	}
	return "b"
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, requestID string) {
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.authError(w, requestID)
		return
	}
	s.writeJSON(w, requestID, http.StatusOK, map[string]any{"user": toUserDTO(session.User), "platform": principalDTO(session.Principal)})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request, requestID string) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		s.store.Delete(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.config.CookieSecure})
	s.writeJSON(w, requestID, http.StatusOK, map[string]any{"loggedOut": true})
}

func (s *Server) handleCourses(w http.ResponseWriter, r *http.Request, requestID string) {
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.authError(w, requestID)
		return
	}
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "processing"
	}
	courses, err := session.Client.Courses(r.Context(), status)
	if err != nil {
		s.upstreamError(w, requestID)
		return
	}
	result := make([]courseDTO, 0, len(courses))
	s.rememberCourseLabels(r.Context(), courses)
	for _, course := range courses {
		result = append(result, toCourseDTO(course))
	}
	s.writeJSON(w, requestID, http.StatusOK, result)
}

func (s *Server) handleHomeworks(w http.ResponseWriter, r *http.Request, requestID, course string) {
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.authError(w, requestID)
		return
	}
	if err := educoderIdentifier(course); err != nil {
		s.writeError(w, requestID, http.StatusBadRequest, "INVALID_COURSE", "课堂标识无效")
		return
	}
	homeworks, err := session.Client.Homeworks(r.Context(), course, r.URL.Query().Get("category"))
	if err != nil {
		s.upstreamError(w, requestID)
		return
	}
	s.rememberHomeworkLabels(r.Context(), course, homeworks)
	homeworks = educoder.OpenHomeworks(homeworks, time.Now())
	result := serviceHomeworks(r.Context(), session.Client, course, homeworks)
	s.writeJSON(w, requestID, http.StatusOK, result)
}

func (s *Server) handleEnter(w http.ResponseWriter, r *http.Request, requestID, course, rawID string) {
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.authError(w, requestID)
		return
	}
	if err := educoderIdentifier(course); err != nil {
		s.writeError(w, requestID, http.StatusBadRequest, "INVALID_COURSE", "课堂标识无效")
		return
	}
	homeworkID, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || homeworkID <= 0 {
		s.writeError(w, requestID, http.StatusBadRequest, "INVALID_HOMEWORK", "作业标识无效")
		return
	}
	task, err := session.Client.Enter(r.Context(), course, homeworkID, r.URL.Query().Get("shixun"))
	if errors.Is(err, educoder.ErrHomeworkClosed) {
		s.writeError(w, requestID, http.StatusGone, "HOMEWORK_CLOSED", "这份作业已截止，无法安排服务")
		return
	}
	if err != nil || task == nil {
		s.upstreamError(w, requestID)
		return
	}
	if s.jobs != nil && s.jobs.bank != nil {
		_ = s.jobs.bank.RememberCollectionTarget(r.Context(), sessionJobOwner(session), collectionTarget(task))
	}
	s.writeJSON(w, requestID, http.StatusOK, toTaskDTO(*task))
}

func (s *Server) sessionFromRequest(r *http.Request) (Session, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return Session{}, false
	}
	session, ok := s.store.Get(cookie.Value)
	if !ok {
		return Session{}, false
	}
	if s.platform != nil {
		if session.Principal.ID == "" {
			s.store.Delete(cookie.Value)
			return Session{}, false
		}
		principal, err := s.platform.LookupUser(r.Context(), session.Principal.ID)
		if err != nil || principal.Status != "active" {
			s.store.Delete(cookie.Value)
			return Session{}, false
		}
		allowed, err := s.platform.CanAccess(r.Context(), principal)
		if err != nil || !allowed {
			s.store.Delete(cookie.Value)
			return Session{}, false
		}
		session.Principal = principal
	}
	return session, true
}

func (s *Server) setSessionCookie(w http.ResponseWriter, id string) {
	maxAge := int(s.config.SessionTTL / time.Second)
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: id, Path: "/", MaxAge: maxAge, HttpOnly: true, Secure: s.config.CookieSecure, SameSite: http.SameSiteLaxMode})
}

func (s *Server) authError(w http.ResponseWriter, requestID string) {
	s.writeError(w, requestID, http.StatusUnauthorized, "AUTH_REQUIRED", "请先登录")
}

func (s *Server) upstreamError(w http.ResponseWriter, requestID string) {
	s.writeError(w, requestID, http.StatusBadGateway, "UPSTREAM_ERROR", "头歌服务暂时不可用，请稍后重试")
}

func (s *Server) methodError(w http.ResponseWriter, requestID string) {
	s.writeError(w, requestID, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "请求方法不支持")
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type apiResponse struct {
	Data      any       `json:"data"`
	Error     *apiError `json:"error"`
	RequestID string    `json:"requestId"`
}

func (s *Server) writeJSON(w http.ResponseWriter, requestID string, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiResponse{Data: data, Error: nil, RequestID: requestID})
}

func (s *Server) writeError(w http.ResponseWriter, requestID string, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiResponse{Data: nil, Error: &apiError{Code: code, Message: message}, RequestID: requestID})
}

func decodeJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, (1<<20)+1))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.InputOffset() > 1<<20 {
		return errors.New("JSON body too large")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("expected one JSON object")
	}
	return nil
}

func educoderIdentifier(value string) error {
	if value == "" {
		return errors.New("empty identifier")
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return errors.New("invalid identifier")
		}
	}
	return nil
}

func newRequestID() string {
	// A request id is only a tracing aid; keep it opaque and independent from
	// upstream credentials or cookies.
	b := make([]byte, 16)
	if _, err := rand.Read(b); err == nil {
		return hex.EncodeToString(b)
	}
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
