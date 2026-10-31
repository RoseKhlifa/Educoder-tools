package web

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	buildinfo "educoder-cli"
	"educoder-cli/internal/educoder"
)

func TestProductionConfigurationAndReleaseEndpoints(t *testing.T) {
	for _, key := range []string{"HTTP_ADDR", "WEB_ORIGIN", "EDUCODER_API_BASE_URL", "SESSION_TTL", "WEB_STATIC_DIR", "WEB_COOKIE_SECURE"} {
		t.Setenv(key, "")
	}
	cfg, err := LoadConfig()
	if err != nil || cfg.HTTPAddr != "0.0.0.0:11454" || cfg.WebOrigin != "" {
		t.Fatalf("default config: %#v %v", cfg, err)
	}
	t.Setenv("WEB_ORIGIN", "https://touge.example/")
	t.Setenv("WEB_COOKIE_SECURE", "true")
	cfg, err = LoadConfig()
	if err != nil || !cfg.CookieSecure || cfg.WebOrigin != "https://touge.example" {
		t.Fatalf("deployment config: %#v %v", cfg, err)
	}
	for _, origin := range []string{"*", "null", "https://example.test/path", "https://user:pass@example.test"} {
		t.Setenv("WEB_ORIGIN", origin)
		if _, err := LoadConfig(); err == nil {
			t.Fatalf("accepted invalid origin %q", origin)
		}
	}
	s := NewServer(DefaultConfig())
	for _, path := range []string{"/healthz", "/version"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		var body map[string]any
		if json.Unmarshal(w.Body.Bytes(), &body) != nil || body["version"] != buildinfo.Current() || body["commit"] != buildinfo.Commit {
			t.Fatalf("release response %s = %s", path, w.Body.String())
		}
		if body["data"] != nil || w.Header().Get("X-App-Version") != buildinfo.Current() || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("release metadata must be top-level and uncached: %s", w.Body.String())
		}
		if path == "/healthz" && body["status"] != "ok" {
			t.Fatalf("unhealthy: %s", w.Body.String())
		}
	}
}

func TestStaticHostPublishesOnlyBuildOutputs(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"index.html":       "<!doctype html><title>workbench</title>",
		"styles.css":       "body { color: #123; }",
		"dist/app.js":      "console.log('workbench');",
		"dist/app.js.map":  "source map must not leak",
		"dist/.private.js": "private build output",
		"check.mjs":        "build helper must not leak",
		"source.ts":        "source must not leak",
	} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := DefaultConfig()
	cfg.StaticDir = dir
	s := NewServer(cfg)
	for _, target := range []string{"/", "/index.html", "/styles.css", "/dist/app.js"} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("public output %s: %d %s", target, w.Code, w.Body.String())
		}
	}
	for _, target := range []string{"/check.mjs", "/source.ts", "/dist/app.js.map", "/dist/.private.js", "/dist/", "/unknown", "/src/app.ts", "/dist/../index.html", "/dist/%2e%2e/index.html", "/dist%5capp.js", "/.git/config", "/_assets/old.js"} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("private or invalid path %s returned %d", target, w.Code)
		}
	}
	for _, target := range []string{"/users/student/classrooms", "/classrooms/COURSE1/shixun_homework"} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		if w.Code != http.StatusTemporaryRedirect || w.Header().Get("Location") != "/" {
			t.Fatalf("legacy bookmark %s: %d location=%q", target, w.Code, w.Header().Get("Location"))
		}
	}
}

func TestStaticCompressionRevalidationHeadAndRanges(t *testing.T) {
	dir := t.TempDir()
	body := strings.Repeat("console.log('workbench');\n", 64)
	if err := os.Mkdir(filepath.Join(dir, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dist", "app.js"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := newStaticHandler(dir)
	req := httptest.NewRequest(http.MethodGet, "/dist/app.js", nil)
	req.Header.Set("Accept-Encoding", "br, gzip")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Header().Get("Content-Encoding") != "gzip" || w.Header().Get("ETag") == "" {
		t.Fatalf("gzip = %d %v", w.Code, w.Header())
	}
	zr, err := gzip.NewReader(bytes.NewReader(w.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(zr)
	_ = zr.Close()
	if err != nil || string(decoded) != body {
		t.Fatal("gzip payload differs from the build output")
	}
	req.Header.Set("If-None-Match", w.Header().Get("ETag"))
	cached := httptest.NewRecorder()
	handler.ServeHTTP(cached, req)
	if cached.Code != http.StatusNotModified || cached.Body.Len() != 0 {
		t.Fatalf("conditional request = %d %s", cached.Code, cached.Body.String())
	}
	head := httptest.NewRecorder()
	handler.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/dist/app.js", nil))
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") == "" {
		t.Fatalf("HEAD = %d %v", head.Code, head.Header())
	}
	rangeReq := httptest.NewRequest(http.MethodGet, "/dist/app.js", nil)
	rangeReq.Header.Set("Accept-Encoding", "gzip")
	rangeReq.Header.Set("Range", "bytes=0-6")
	rangeResult := httptest.NewRecorder()
	handler.ServeHTTP(rangeResult, rangeReq)
	if rangeResult.Code != http.StatusPartialContent || rangeResult.Body.String() != body[:7] || rangeResult.Header().Get("Content-Encoding") != "" {
		t.Fatalf("range = %d %s", rangeResult.Code, rangeResult.Body.String())
	}
	plainReq := httptest.NewRequest(http.MethodGet, "/dist/app.js", nil)
	plainReq.Header.Set("Accept-Encoding", "gzip;q=0")
	plain := httptest.NewRecorder()
	handler.ServeHTTP(plain, plainReq)
	if plain.Header().Get("Content-Encoding") != "" || plain.Body.String() != body {
		t.Fatal("gzip;q=0 must preserve identity output")
	}
}

func TestStaticModuleGraphVersionsOnlyPublishedLocalDependencies(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "dist", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	entry := strings.Join([]string{
		`import { api } from './api.js';`,
		`import './jobs.js?lang=zh&value=a%2Bb#section';`,
		`export { job } from "./jobs.js";`,
		`export * as nested from './nested/extra.js';`,
		`const lazy = import('./jobs.js');`,
		`const external = import('https://example.test/jobs.js?existing=1');`,
		`import '//example.test/jobs.js';`,
		`import 'package/jobs.js';`,
		`import './missing.js';`,
		`import './.private.js';`,
		`const text = "import './jobs.js'";`,
		"const template = `import './jobs.js'`;",
		`const expression = /import\('.\/jobs.js'\)/;`,
		`// import './jobs.js';`,
		`/* export * from './jobs.js'; */`,
		`object.import('./jobs.js');`,
	}, "\n")
	for name, body := range map[string]string{
		"index.html":           `<script type="module" src="/dist/app.js?v=release"></script>`,
		"dist/app.js":          entry,
		"dist/api.js":          `export const api = 1;`,
		"dist/jobs.js":         `export * from './app.js'; export const job = 1;`,
		"dist/nested/extra.js": `import '../jobs.js'; export * from '/dist/api.js?flag';`,
		"dist/.private.js":     `const secret = 1;`,
	} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	handler := newStaticHandler(dir)
	get := func(handler http.Handler, target string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", target, w.Code)
		}
		return w
	}
	index := get(handler, "/")
	if !strings.Contains(index.Body.String(), `/dist/app.js?v=release`) {
		t.Fatal("entry point version must be preserved")
	}
	response := get(handler, "/dist/app.js?v=release")
	match := regexp.MustCompile(`__asset=([a-f0-9]{16})`).FindStringSubmatch(response.Body.String())
	if len(match) != 2 {
		t.Fatalf("versioned entry point retained unversioned imports: %s", response.Body.String())
	}
	revision := match[1]
	for _, expected := range []string{
		`from './api.js?__asset=` + revision + `';`,
		`import './jobs.js?lang=zh&value=a%2Bb&__asset=` + revision + `#section';`,
		`from "./jobs.js?__asset=` + revision + `";`,
		`from './nested/extra.js?__asset=` + revision + `';`,
		`import('./jobs.js?__asset=` + revision + `');`,
	} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Fatalf("missing rewritten import %q in %s", expected, response.Body.String())
		}
	}
	for _, unchanged := range []string{
		`import('https://example.test/jobs.js?existing=1')`,
		`import '//example.test/jobs.js';`,
		`import 'package/jobs.js';`,
		`import './missing.js';`,
		`import './.private.js';`,
		`const text = "import './jobs.js'";`,
		"const template = `import './jobs.js'`;",
		`const expression = /import\('.\/jobs.js'\)/;`,
		`// import './jobs.js';`,
		`/* export * from './jobs.js'; */`,
		`object.import('./jobs.js');`,
	} {
		if !strings.Contains(response.Body.String(), unchanged) {
			t.Fatalf("non-module or unpublished specifier changed: %q", unchanged)
		}
	}
	nested := get(handler, "/dist/nested/extra.js?__asset="+revision)
	if !strings.Contains(nested.Body.String(), `import '../jobs.js?__asset=`+revision) || !strings.Contains(nested.Body.String(), `from '/dist/api.js?flag&__asset=`+revision) {
		t.Fatalf("nested/root reexports not versioned: %s", nested.Body.String())
	}
	jobs := get(handler, "/dist/jobs.js?__asset="+revision)
	if !strings.Contains(jobs.Body.String(), `from './app.js?__asset=`+revision) {
		t.Fatal("cyclic import must use the same graph revision")
	}
	if duplicate := get(newStaticHandler(dir), "/dist/app.js"); duplicate.Body.String() != response.Body.String() || duplicate.Header().Get("ETag") != response.Header().Get("ETag") {
		t.Fatal("graph revision changed without a build output change")
	}
	if err := os.WriteFile(filepath.Join(dir, "dist", "jobs.js"), []byte(`export const job = 2;`), 0o600); err != nil {
		t.Fatal(err)
	}
	updated := get(newStaticHandler(dir), "/dist/app.js?v=release")
	if updated.Body.String() == response.Body.String() || updated.Header().Get("ETag") == response.Header().Get("ETag") || strings.Contains(updated.Body.String(), "__asset="+revision) {
		t.Fatal("dependency-only changes must invalidate entry module imports and ETag")
	}
	if updated.Header().Get("Last-Modified") != "" {
		t.Fatal("an entry module's own mtime cannot validate a graph-derived representation")
	}
	modifiedSince := httptest.NewRequest(http.MethodGet, "/dist/app.js?v=release", nil)
	modifiedSince.Header.Set("If-Modified-Since", "Sat, 31 Oct 2026 09:00:00 GMT")
	graphResponse := httptest.NewRecorder()
	newStaticHandler(dir).ServeHTTP(graphResponse, modifiedSince)
	if graphResponse.Code != http.StatusOK || graphResponse.Body.String() != updated.Body.String() {
		t.Fatal("legacy Last-Modified cache must not hide a dependency-only change")
	}
	request := httptest.NewRequest(http.MethodGet, "/dist/app.js?v=release", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	compressed := httptest.NewRecorder()
	handler.ServeHTTP(compressed, request)
	zr, err := gzip.NewReader(bytes.NewReader(compressed.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(zr)
	_ = zr.Close()
	if err != nil || !bytes.Equal(decoded, response.Body.Bytes()) || compressed.Header().Get("ETag") == response.Header().Get("ETag") {
		t.Fatal("gzip/variant ETag must describe the rewritten module graph")
	}
	request.Header.Set("If-None-Match", compressed.Header().Get("ETag"))
	conditional := httptest.NewRecorder()
	handler.ServeHTTP(conditional, request)
	if conditional.Code != http.StatusNotModified || conditional.Body.Len() != 0 {
		t.Fatalf("rewritten gzip conditional response = %d", conditional.Code)
	}
	request = httptest.NewRequest(http.MethodGet, "/dist/.private.js?__asset="+revision, nil)
	private := httptest.NewRecorder()
	handler.ServeHTTP(private, request)
	if private.Code != http.StatusNotFound {
		t.Fatal("module versioning exposed an unpublished file")
	}
}

func TestCrossOriginCannotInvokeLogin(t *testing.T) {
	fake := &fakeClient{}
	cfg := DefaultConfig()
	s := NewServerWithFactory(cfg, func(string) EducoderClient { return fake })
	for _, origin := range []string{"https://foreign.test", "null", "https://same.test/path", "https://same.test@foreign.test"} {
		r := httptest.NewRequest(http.MethodPost, "http://same.test/api/auth/login", strings.NewReader(`{"login":"student","password":"secret"}`))
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden || fake.loginName != "" || len(w.Result().Cookies()) != 0 {
			t.Fatalf("cross-origin login %q returned %d and invoked upstream=%q", origin, w.Code, fake.loginName)
		}
	}
	// HTTPS on the public origin remains valid behind a TLS-terminating proxy.
	r := httptest.NewRequest(http.MethodPost, "http://same.test/api/auth/login", strings.NewReader(`{"login":"student","password":"secret"}`))
	r.Header.Set("Origin", "https://same.test")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusOK || fake.loginName != "student" {
		t.Fatalf("same-origin login = %d %s", w.Code, w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie lacks expected flags: %#v", cookie)
	}
}

func TestBrowserSessionsKeepUpstreamClientsIsolated(t *testing.T) {
	cfg := DefaultConfig()
	var clients []*fakeClient
	server := NewServerWithFactory(cfg, func(string) EducoderClient {
		fake := &fakeClient{}
		clients = append(clients, fake)
		return fake
	})
	ts := httptest.NewServer(server)
	defer ts.Close()
	browser := func() *http.Client {
		jar, _ := cookiejar.New(nil)
		return &http.Client{Jar: jar}
	}
	first, second := browser(), browser()
	for i, client := range []*http.Client{first, second} {
		name := []string{"first", "second"}[i]
		login := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/login", `{"login":"`+name+`","password":"test"}`)
		if login.StatusCode != http.StatusOK || !strings.Contains(login.Body, `"avatarUrl":"/api/avatar"`) {
			t.Fatalf("login = %d %s", login.StatusCode, login.Body)
		}
		clients[i].courses = []educoder.Course{{Identifier: name, Name: name}}
	}
	for i, client := range []*http.Client{first, second} {
		name := []string{"first", "second"}[i]
		result := doJSON(t, client, http.MethodGet, ts.URL+"/api/courses", "")
		if !strings.Contains(result.Body, `"identifier":"`+name+`"`) {
			t.Fatalf("session used another upstream client: %s", result.Body)
		}
	}
	_ = doJSON(t, first, http.MethodPost, ts.URL+"/api/auth/logout", "")
	if me := doJSON(t, second, http.MethodGet, ts.URL+"/api/auth/me", ""); me.StatusCode != http.StatusOK || !strings.Contains(me.Body, `"login":"second"`) {
		t.Fatalf("logout crossed browser session: %s", me.Body)
	}
	avatar := doJSON(t, ts.Client(), http.MethodGet, ts.URL+"/api/avatar", "")
	if avatar.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated avatar access = %d", avatar.StatusCode)
	}
	custom := doJSON(t, second, http.MethodGet, ts.URL+"/api/avatar?url=https://foreign.test/image.png", "")
	if custom.StatusCode != http.StatusBadRequest {
		t.Fatalf("avatar accepted arbitrary URL input: %d", custom.StatusCode)
	}
}
