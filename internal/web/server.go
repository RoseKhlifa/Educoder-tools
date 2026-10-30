package web

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"educoder-cli/internal/educoder"
)

const sessionCookieName = "educoder_web_session"

type Config struct {
	HTTPAddr     string
	WebOrigin    string
	APIBaseURL   string
	SessionTTL   time.Duration
	StaticDir    string
	CookieSecure bool
}

func DefaultConfig() Config {
	return Config{HTTPAddr: ":8080", WebOrigin: "http://localhost:5173", APIBaseURL: educoder.DefaultBaseURL, SessionTTL: 8 * time.Hour, StaticDir: filepath.FromSlash("web")}
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
	if strings.EqualFold(strings.TrimSpace(os.Getenv("WEB_COOKIE_SECURE")), "true") {
		cfg.CookieSecure = true
	}
	return cfg, nil
}

type ClientFactory func(baseURL string) EducoderClient

type Server struct {
	config  Config
	store   *SessionStore
	factory ClientFactory
	static  http.Handler
}

func NewServer(config Config) *Server {
	return NewServerWithFactory(config, func(baseURL string) EducoderClient { return educoder.New(baseURL) })
}

func NewServerWithFactory(config Config, factory ClientFactory) *Server {
	if config.HTTPAddr == "" {
		config.HTTPAddr = ":8080"
	}
	if config.WebOrigin == "" {
		config.WebOrigin = "http://localhost:5173"
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
	s := &Server{config: config, store: NewSessionStore(config.SessionTTL), factory: factory}
	if info, err := os.Stat(config.StaticDir); err == nil && info.IsDir() {
		s.static = http.FileServer(http.Dir(config.StaticDir))
	}
	return s
}

func (s *Server) Store() *SessionStore { return s.store }

func (s *Server) Handler() http.Handler { return s }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestID := newRequestID()
	w.Header().Set("X-Request-Id", requestID)
	if !s.cors(w, r) {
		return
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.URL.Path == "/healthz" {
		if r.Method != http.MethodGet {
			s.writeError(w, requestID, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "请求方法不支持")
			return
		}
		s.writeJSON(w, requestID, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		s.handleAPI(w, r, requestID)
		return
	}
	s.handleStatic(w, r)
}

func (s *Server) cors(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if s.config.WebOrigin == "*" {
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		}
	} else if origin != "" && origin == s.config.WebOrigin {
		w.Header().Set("Access-Control-Allow-Origin", origin)
	}
	if origin != "" && (s.config.WebOrigin == "*" || origin == s.config.WebOrigin) {
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Request-Id")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Add("Vary", "Origin")
	}
	return true
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if s.static == nil {
		http.NotFound(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if info, err := os.Stat(filepath.Join(s.config.StaticDir, filepath.FromSlash(path))); err == nil && !info.IsDir() {
		s.static.ServeHTTP(w, r)
		return
	}
	// SPA fallback lets browser-side routes render after a hard refresh.
	index := filepath.Join(s.config.StaticDir, "index.html")
	if _, err := os.Stat(index); err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, index)
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request, requestID string) {
	parts := splitPath(r.URL.Path)
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
	if err := decodeJSON(r, &input); err != nil || strings.TrimSpace(input.Login) == "" || input.Password == "" {
		s.writeError(w, requestID, http.StatusBadRequest, "INVALID_REQUEST", "请输入账号和密码")
		return
	}
	client := s.factory(s.config.APIBaseURL)
	ctx := r.Context()
	user, err := client.Login(ctx, strings.TrimSpace(input.Login), input.Password)
	input.Password = ""
	if err != nil || user == nil || user.Login == "" {
		s.writeError(w, requestID, http.StatusUnauthorized, "AUTH_FAILED", "登录失败，请检查账号或密码")
		return
	}
	if old, err := r.Cookie(sessionCookieName); err == nil {
		s.store.Delete(old.Value)
	}
	// Store only the public user fields returned by the upstream client.
	sessionID, err := s.store.Create(client, *user)
	if err != nil {
		s.writeError(w, requestID, http.StatusInternalServerError, "SESSION_CREATE_FAILED", "创建会话失败")
		return
	}
	s.setSessionCookie(w, sessionID)
	s.writeJSON(w, requestID, http.StatusOK, map[string]any{"user": toUserDTO(*user)})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, requestID string) {
	session, ok := s.sessionFromRequest(r)
	if !ok {
		s.authError(w, requestID)
		return
	}
	s.writeJSON(w, requestID, http.StatusOK, map[string]any{"user": toUserDTO(session.User)})
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
	result := make([]homeworkDTO, 0, len(homeworks))
	for _, homework := range homeworks {
		result = append(result, toHomeworkDTO(homework))
	}
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
	if err != nil || task == nil {
		s.upstreamError(w, requestID)
		return
	}
	s.writeJSON(w, requestID, http.StatusOK, toTaskDTO(*task))
}

func (s *Server) sessionFromRequest(r *http.Request) (Session, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return Session{}, false
	}
	return s.store.Get(cookie.Value)
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
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
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
