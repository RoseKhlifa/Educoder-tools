// Package compat serves the published Educoder build and an explicit list of
// observed read-only page compatibility routes plus login. It does not
// implement a general HTTP reverse proxy.
package compat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	buildinfo "educoder-cli"
	"educoder-cli/internal/educoder"
)

const sessionCookie = "edureuse_session"
const markerCookie = "_educoder_session"

type Config struct {
	Addr         string
	StaticDir    string
	APIBaseURL   string
	SessionTTL   time.Duration
	CookieSecure bool
}

func DefaultConfig() Config {
	return Config{Addr: "0.0.0.0:11454", StaticDir: filepath.FromSlash("resource-reuse/site"), APIBaseURL: educoder.DefaultBaseURL, SessionTTL: 8 * time.Hour}
}

func LoadConfig() (Config, error) {
	cfg := DefaultConfig()
	if value := os.Getenv("REUSE_ADDR"); value != "" {
		cfg.Addr = value
	}
	if value := os.Getenv("REUSE_STATIC_DIR"); value != "" {
		cfg.StaticDir = value
	}
	if value := os.Getenv("EDUCODER_API_BASE_URL"); value != "" {
		cfg.APIBaseURL = strings.TrimRight(value, "/")
	}
	if value := os.Getenv("REUSE_SESSION_TTL"); value != "" {
		ttl, err := time.ParseDuration(value)
		if err != nil || ttl <= 0 {
			return Config{}, errors.New("invalid REUSE_SESSION_TTL")
		}
		cfg.SessionTTL = ttl
	}
	cfg.CookieSecure = strings.EqualFold(os.Getenv("REUSE_COOKIE_SECURE"), "true")
	return cfg, nil
}

type Server struct {
	config         Config
	store          *sessionStore
	newClient      func() *educoder.Client
	unsupportedLog *log.Logger
	avatars        *avatarService
	apiCache       *apiResponseCache
	staticCache    *staticAssetCache
}

func NewServer(cfg Config) *Server {
	defaults := DefaultConfig()
	if cfg.Addr == "" {
		cfg.Addr = defaults.Addr
	}
	if cfg.StaticDir == "" {
		cfg.StaticDir = defaults.StaticDir
	}
	if cfg.APIBaseURL == "" {
		cfg.APIBaseURL = defaults.APIBaseURL
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = defaults.SessionTTL
	}
	s := &Server{config: cfg, store: newSessionStore(cfg.SessionTTL), unsupportedLog: log.New(os.Stderr, "reuse unsupported API: ", log.LstdFlags)}
	s.avatars = newAvatarService(cfg.StaticDir)
	s.apiCache = newAPIResponseCache()
	s.staticCache = newStaticAssetCache(64 << 20)
	s.newClient = func() *educoder.Client {
		client := educoder.New(cfg.APIBaseURL)
		client.HTTP.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		return client
	}
	return s
}

func (s *Server) Handler() http.Handler { return s }
func (s *Server) Sweep() {
	s.store.sweep()
	s.apiCache.sweep()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-App-Version", buildinfo.Current())
	if s.serveAvatar(w, r) {
		return
	}
	if r.URL.Path == "/healthz" || r.URL.Path == "/version" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var result any = buildinfo.Release()
		if r.URL.Path == "/healthz" {
			result = struct {
				Status string `json:"status"`
				buildinfo.Info
			}{Status: "ok", Info: buildinfo.Release()}
		}
		body, _ := json.Marshal(result)
		writeJSON(w, http.StatusOK, body)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		s.serveAPI(w, r)
		return
	}
	s.serveStatic(w, r)
}

func (s *Server) serveAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-App-Cache", "BYPASS")
	path := educoder.NormalizeObservedPath(r.URL.Path)
	if path == "/api/accounts/logout.json" {
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			s.logUnsupported(r.Method, path)
			apiError(w, http.StatusMethodNotAllowed, "请求方法不支持")
			return
		}
		if cookie, err := r.Cookie(sessionCookie); err == nil {
			s.store.remove(cookie.Value)
		}
		s.clearCookies(w)
		writeJSON(w, http.StatusOK, []byte(`{"status":0,"message":"success"}`))
		return
	}
	if r.Method == http.MethodOptions && (educoder.RecordedRoute(http.MethodGet, path) || educoder.RecordedRoute(http.MethodPost, path)) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !educoder.RecordedRoute(r.Method, path) {
		s.logUnsupported(r.Method, path)
		apiError(w, http.StatusNotFound, "接口尚未支持")
		return
	}
	if r.Method == http.MethodPost && path == "/api/accounts/login.json" {
		s.login(w, r)
		return
	}
	item, authenticated := s.fromRequest(r)
	if !authenticated && path == "/api/users/get_user_info.json" {
		writeJSON(w, http.StatusOK, []byte(`{"status":0,"login":"","user_id":null}`))
		return
	}
	if !authenticated && !publicRoute(path) {
		apiError(w, http.StatusUnauthorized, "请先登录")
		return
	}
	client := item.client
	if client == nil {
		client = s.newClient()
	}
	if strings.HasPrefix(path, "/api/users/") && (strings.HasSuffix(path, "/courses.json") || strings.HasSuffix(path, "/homepage_info.json")) {
		parts := strings.Split(path, "/")
		if !authenticated || len(parts) != 5 || parts[3] != item.login {
			apiError(w, http.StatusForbidden, "用户身份不匹配")
			return
		}
	}
	query := r.URL.Query()
	if strings.HasSuffix(path, "/courses.json") {
		query.Set("username", item.login)
	}
	fetch := func(ctx context.Context) (apiResponse, error) {
		started := time.Now()
		response, err := client.RecordedRequest(ctx, r.Method, path, query, nil)
		upstreamTime := time.Since(started)
		if err != nil {
			return apiResponse{}, errors.New("上游请求失败")
		}
		started = time.Now()
		body, err := cleanJSON(response.Body, item.marker, client.RecordedSecrets())
		if err != nil {
			return apiResponse{}, errors.New("上游响应格式错误")
		}
		body, err = s.localizeImageURLs(body)
		if err != nil {
			return apiResponse{}, errors.New("图片地址处理失败")
		}
		return apiResponse{status: response.StatusCode, body: body, upstream: upstreamTime, processing: time.Since(started)}, nil
	}
	started := time.Now()
	var result apiResponse
	var err error
	cacheState := "BYPASS"
	if ttl := apiCacheTTL(r.Method, path); ttl > 0 && !apiCacheBypass(r) {
		scope := "anonymous"
		if authenticated {
			cookie, _ := r.Cookie(sessionCookie)
			scope = "session:" + cookie.Value
		}
		result, cacheState, err = s.apiCache.get(r.Context(), makeAPICacheKey(scope, r.Method, path, query), ttl, fetch)
	} else {
		result, err = fetch(r.Context())
	}
	w.Header().Set("X-App-Cache", cacheState)
	w.Header().Set("Server-Timing", fmt.Sprintf("upstream;dur=%.3f, processing;dur=%.3f, total;dur=%.3f", float64(result.upstream)/float64(time.Millisecond), float64(result.processing)/float64(time.Millisecond), float64(time.Since(started))/float64(time.Millisecond)))
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		apiError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, result.status, result.body)
}

func publicRoute(path string) bool {
	switch path {
	case "/api/setting.json", "/api/home/index.json", "/api/users/system_update.json", "/api/popup_windows/get_popup_windows.json", "/api/popup_windows/popup_windows_counts.json":
		return true
	}
	return false
}

func (s *Server) logUnsupported(method, path string) {
	// Never pass URL.String(), RawQuery, headers or request bodies to logging.
	// Quoting also keeps any escaped control characters on one log line.
	if s.unsupportedLog != nil {
		s.unsupportedLog.Printf("%s %q", method, path)
	}
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		apiError(w, http.StatusBadRequest, "登录请求无效")
		return
	}
	var input struct {
		Login    string `json:"login"`
		Password string `json:"password"`
	}
	if json.Unmarshal(body, &input) != nil || strings.TrimSpace(input.Login) == "" || input.Password == "" {
		apiError(w, http.StatusBadRequest, "请输入账号和密码")
		return
	}
	client := s.newClient()
	response, err := client.RecordedRequest(r.Context(), http.MethodPost, r.URL.Path, r.URL.Query(), body)
	input.Password = ""
	if err != nil {
		apiError(w, http.StatusBadGateway, "登录服务暂时不可用")
		return
	}
	login, err := client.AcceptRecordedLogin(response)
	if err != nil {
		clean, cleanErr := cleanJSON(response.Body, "", client.RecordedSecrets())
		if cleanErr != nil {
			apiError(w, http.StatusUnauthorized, "登录失败")
			return
		}
		writeJSON(w, response.StatusCode, clean)
		return
	}
	id, item, err := s.store.create(client, login)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "创建会话失败")
		return
	}
	if old, err := r.Cookie(sessionCookie); err == nil {
		s.store.remove(old.Value)
	}
	s.setCookies(w, id, item.marker)
	clean, err := cleanJSON(response.Body, item.marker, client.RecordedSecrets())
	if err != nil {
		apiError(w, http.StatusBadGateway, "登录响应格式错误")
		return
	}
	// The original bundle can read this marker. It is never used to authenticate
	// any local or upstream request; only the HttpOnly session cookie is.
	w.Header().Set("cs", item.marker)
	writeJSON(w, response.StatusCode, clean)
}

func (s *Server) fromRequest(r *http.Request) (session, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return session{}, false
	}
	return s.store.get(cookie.Value)
}

func (s *Server) setCookies(w http.ResponseWriter, id, marker string) {
	for _, entry := range []struct {
		name, value string
		httpOnly    bool
	}{{sessionCookie, id, true}, {markerCookie, marker, false}} {
		http.SetCookie(w, &http.Cookie{Name: entry.name, Value: entry.value, Path: "/", HttpOnly: entry.httpOnly, Secure: s.config.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: int(s.config.SessionTTL / time.Second)})
	}
}

func (s *Server) clearCookies(w http.ResponseWriter) {
	for _, name := range []string{sessionCookie, markerCookie} {
		http.SetCookie(w, &http.Cookie{Name: name, Path: "/", Value: "", MaxAge: -1, HttpOnly: name == sessionCookie, Secure: s.config.CookieSecure, SameSite: http.SameSiteLaxMode})
	}
}

func cleanJSON(raw json.RawMessage, marker string, secrets []string) ([]byte, error) {
	var root any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&root); err != nil {
		return nil, err
	}
	var clean func(any, string) any
	clean = func(value any, parent string) any {
		switch item := value.(type) {
		case map[string]any:
			for key, value := range item {
				lower := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
				if parent == "cookies" && lower == "value" {
					item[key] = marker
					continue
				}
				if sensitiveKey(lower) {
					item[key] = ""
					continue
				}
				item[key] = clean(value, lower)
			}
			return item
		case []any:
			for i, value := range item {
				item[i] = clean(value, parent)
			}
			return item
		case string:
			for _, secret := range secrets {
				if item == secret {
					return ""
				}
				if len(secret) >= 8 {
					item = strings.ReplaceAll(item, secret, "")
				}
			}
			return item
		default:
			return value
		}
	}
	return json.Marshal(clean(root, ""))
}

func sensitiveKey(key string) bool {
	switch key {
	case "password", "authorization", "pc_authorization", "_educoder_session", "autologin_trustie", "access_token", "refresh_token", "session_token", "token", "virtual_token", "user_competition_token_url":
		return true
	}
	return strings.HasSuffix(key, "_access_token") || strings.HasSuffix(key, "_refresh_token")
}

func writeJSON(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func apiError(w http.ResponseWriter, status int, message string) {
	body, _ := json.Marshal(map[string]any{"status": -1, "message": message})
	writeJSON(w, status, body)
}
