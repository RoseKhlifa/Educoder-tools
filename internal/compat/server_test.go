package compat

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"educoder-cli/internal/educoder"
)

const (
	testLogin       = "student1"
	upstreamSession = "upstream-session-secret-1234"
	upstreamTrust   = "upstream-trust-secret-5678"
	// This is EncryptPassword("test-password"), as sent by the original bundle.
	loginJSON = `{ "login": "student1", "password": "p15krDvHEPYqjf560oABdQ==", "autologin": true }`
)

// These are observed read-only page compatibility routes used by the homework UI.
var observedHomeworkGETs = []string{
	"/api/homework_commons/3985881/show_comment.json",
	"/api/homework_commons/3985881.json",
	"/api/homework_commons/3985881/shixun_challenge_data.json",
	"/api/shixun_homeworks/3985881/student_works.json",
	"/api/shixun_homeworks/3985881/header_info.json",
}

type recordedRequest struct {
	method string
	path   string
	query  url.Values
	header http.Header
	body   string
}

type compatFixture struct {
	server *Server
	mu     sync.Mutex
	seen   []recordedRequest
}

func newCompatFixture(t *testing.T) *compatFixture {
	t.Helper()
	f := &compatFixture{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read upstream body: %v", err)
		}
		f.mu.Lock()
		f.seen = append(f.seen, recordedRequest{r.Method, r.URL.Path, r.URL.Query(), r.Header.Clone(), string(body)})
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/accounts/login.json" {
			http.SetCookie(w, &http.Cookie{Name: "_educoder_session", Value: upstreamSession, Path: "/", HttpOnly: true})
			http.SetCookie(w, &http.Cookie{Name: "autologin_trustie", Value: upstreamTrust, Path: "/"})
			w.Header().Set("cs", upstreamSession)
			w.Header().Set("Authorization", upstreamSession)
			_, _ = io.WriteString(w, `{"status":0,"login":"student1","user_id":9007199254740993,"cookies":{"name":"_educoder_session","value":"`+upstreamSession+`"},"password":"ciphertext-secret","autologin_trustie":"`+upstreamTrust+`","nested":{"echo":"before-`+upstreamSession+`-after"}}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": 0, "route": r.URL.Path, "login": testLogin,
			"cookies": map[string]string{"value": upstreamSession},
			"nested":  map[string]string{"access_token": "hidden-token", "echo": upstreamTrust},
		})
	}))
	t.Cleanup(upstream.Close)
	f.server = NewServer(Config{APIBaseURL: upstream.URL, StaticDir: t.TempDir(), SessionTTL: time.Hour, CookieSecure: true})
	// Tests must never fetch public avatars from the real network.
	f.server.avatars.client.Transport = avatarRoundTripper(func(*http.Request) (*http.Response, error) {
		return avatarResponse(http.StatusNotFound, nil), nil
	})
	return f
}

func (f *compatFixture) requests() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedRequest(nil), f.seen...)
}

func (f *compatFixture) request(method, target, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, r)
	return w
}

func (f *compatFixture) login(t *testing.T) (*httptest.ResponseRecorder, *http.Cookie, *http.Cookie) {
	t.Helper()
	w := f.request(http.MethodPost, "/api/accounts/login.json", loginJSON)
	if w.Code != http.StatusOK {
		t.Fatalf("login: status=%d body=%s", w.Code, w.Body.String())
	}
	var session, marker *http.Cookie
	for _, cookie := range w.Result().Cookies() {
		switch cookie.Name {
		case sessionCookie:
			session = cookie
		case markerCookie:
			marker = cookie
		}
	}
	if session == nil || marker == nil {
		t.Fatalf("expected session and marker cookies, got %v", w.Header().Values("Set-Cookie"))
	}
	return w, session, marker
}

func decodeObject(t *testing.T, w *httptest.ResponseRecorder) map[string]json.RawMessage {
	t.Helper()
	var body map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON response: %v: %s", err, w.Body.String())
	}
	if _, exists := body["data"]; exists {
		t.Fatal("compat response unexpectedly wrapped in a data envelope")
	}
	return body
}

func assertNoUpstreamSecrets(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	content := w.Header().Get("Set-Cookie") + w.Header().Get("cs") + w.Header().Get("Authorization") + w.Body.String()
	for _, secret := range []string{upstreamSession, upstreamTrust, "ciphertext-secret", "hidden-token"} {
		if strings.Contains(content, secret) {
			t.Errorf("response leaked mock upstream secret %q", secret)
		}
	}
}

func TestRecordedLoginKeepsCredentialsServerSide(t *testing.T) {
	f := newCompatFixture(t)
	w, session, marker := f.login(t)
	seen := f.requests()
	if len(seen) != 1 || seen[0].method != http.MethodPost || seen[0].body != loginJSON {
		t.Fatalf("login JSON changed or was encrypted a second time: %+v", seen)
	}
	if !session.HttpOnly || marker.HttpOnly || !session.Secure || !marker.Secure {
		t.Errorf("incorrect cookie security attributes: session=%+v marker=%+v", session, marker)
	}
	for _, cookie := range []*http.Cookie{session, marker} {
		if cookie.Path != "/" || cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge != 3600 {
			t.Errorf("incorrect session cookie scope/lifetime: %+v", cookie)
		}
	}
	if session.Value == marker.Value || !strings.HasPrefix(marker.Value, "local-") || session.Value == upstreamSession {
		t.Fatal("local authentication session, readable marker, and upstream session must be independent")
	}
	if w.Header().Get("cs") != marker.Value {
		t.Errorf("cs is not the local marker: %q", w.Header().Get("cs"))
	}
	body := decodeObject(t, w)
	if string(body["user_id"]) != "9007199254740993" {
		t.Errorf("original numeric JSON representation lost precision: %s", body["user_id"])
	}
	var cookieValue struct{ Value string }
	if err := json.Unmarshal(body["cookies"], &cookieValue); err != nil || cookieValue.Value != marker.Value {
		t.Errorf("cookies.value must use local marker: %s, error=%v", body["cookies"], err)
	}
	if strings.Contains(w.Body.String(), session.Value) {
		t.Fatal("HttpOnly authentication session appeared in browser-readable JSON")
	}
	assertNoUpstreamSecrets(t, w)
	item, ok := f.server.store.get(session.Value)
	if !ok || item.login != testLogin || item.marker != marker.Value {
		t.Fatalf("authenticated local session not stored: %+v", item)
	}
	if got := item.client.RecordedSecrets(); len(got) != 2 {
		t.Errorf("server-side upstream cookie jar has %d entries, want 2", len(got))
	}

	_, anotherSession, anotherMarker := f.login(t)
	if anotherSession.Value == session.Value || anotherMarker.Value == marker.Value {
		t.Fatal("separate logins reused local session or marker")
	}
}

func TestOnlyHttpOnlySessionAuthenticatesRequests(t *testing.T) {
	f := newCompatFixture(t)
	_, session, marker := f.login(t)
	for _, tc := range []struct {
		name   string
		cookie *http.Cookie
	}{
		{"no cookie", nil},
		{"readable marker", marker},
		{"marker as session", &http.Cookie{Name: sessionCookie, Value: marker.Value}},
		{"upstream session as local session", &http.Cookie{Name: sessionCookie, Value: upstreamSession}},
		{"upstream cookie", &http.Cookie{Name: markerCookie, Value: upstreamSession}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(f.requests())
			r := httptest.NewRequest(http.MethodGet, "/api/courses/COURSE1/homework_commons.json", nil)
			if tc.cookie != nil {
				r.AddCookie(tc.cookie)
			}
			r.Header.Set("Pc-Authorization", upstreamSession)
			r.Header.Set("Authorization", "Bearer "+session.Value)
			r.Header.Set("cs", marker.Value)
			r.Header.Set("X-Original-Host", "www.educoder.net")
			r.Header.Set("X-Original-Origin", educoder.DefaultOrigin)
			w := httptest.NewRecorder()
			f.server.ServeHTTP(w, r)
			if w.Code != http.StatusUnauthorized || len(f.requests()) != before {
				t.Errorf("spoof authenticated or reached upstream: status=%d requests=%d (was %d)", w.Code, len(f.requests()), before)
			}
		})
	}
	before := len(f.requests())
	anonymous := f.request(http.MethodGet, "/api/users/get_user_info.json", "", marker)
	if anonymous.Code != http.StatusOK || len(f.requests()) != before || string(decodeObject(t, anonymous)["login"]) != `""` {
		t.Errorf("anonymous bootstrap must remain local and anonymous: %s", anonymous.Body.String())
	}
	authenticated := f.request(http.MethodGet, "/api/courses/COURSE1/homework_commons.json", "", session)
	if authenticated.Code != http.StatusOK || len(f.requests()) != before+1 {
		t.Errorf("HttpOnly session did not authenticate: status=%d body=%s", authenticated.Code, authenticated.Body.String())
	}
}

func TestRecordedGETFlowPreservesShapeQueryAndServerIdentity(t *testing.T) {
	f := newCompatFixture(t)
	_, session, marker := f.login(t)
	paths := []string{
		"/api/users/get_user_info.json",
		"/api/users/get_notice.json",
		"/api/users/system_update.json",
		"/api/users/student1/homepage_info.json",
		"/api/users/student1/courses.json",
		"/api/courses/COURSE1/top_banner.json",
		"/api/courses/COURSE1/left_banner.json",
		"/api/courses/COURSE1/informs.json",
		"/api/courses/COURSE1/homework_commons.json",
		"/api/shixuns/SHIXUN1/shixun_exec.json",
		"/api/myshixuns/myshixun_1-A/version_repository_delete.json",
		"/api/tasks/GAME1.json",
		"/api/tasks/GAME1/rep_content.json",
		"/api/setting.json",
		"/api/home/index.json",
		"/api/popup_windows/get_popup_windows.json",
		"/api/popup_windows/popup_windows_counts.json",
	}
	paths = append(paths, observedHomeworkGETs...)
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			query := "?page=2&limit=50&tag=a&tag=b&zzud=attacker"
			if strings.HasSuffix(path, "/courses.json") {
				query += "&username=another-user"
			}
			r := httptest.NewRequest(http.MethodGet, path+query, nil)
			r.AddCookie(session)
			r.AddCookie(marker)
			r.Header.Set("Pc-Authorization", "spoofed-browser-token")
			r.Header.Set("X-Original-Host", "attacker.invalid")
			r.Header.Set("X-Original-Origin", "https://attacker.invalid")
			w := httptest.NewRecorder()
			f.server.ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("GET status=%d body=%s", w.Code, w.Body.String())
			}
			body := decodeObject(t, w)
			if string(body["route"]) != `"`+path+`"` || string(body["status"]) != "0" {
				t.Errorf("original upstream JSON fields lost: %s", w.Body.String())
			}
			assertNoUpstreamSecrets(t, w)
			seen := f.requests()
			got := seen[len(seen)-1]
			if got.path != path || got.method != http.MethodGet || got.query.Get("page") != "2" || got.query.Get("limit") != "50" || strings.Join(got.query["tag"], ",") != "a,b" {
				t.Errorf("path, method, or query changed: %+v", got)
			}
			if got.query.Get("zzud") != testLogin {
				t.Errorf("untrusted zzud forwarded: %v", got.query)
			}
			if strings.HasSuffix(path, "/courses.json") && got.query.Get("username") != testLogin {
				t.Errorf("untrusted username forwarded: %v", got.query)
			}
			if got.header.Get("Pc-Authorization") != upstreamSession || !strings.Contains(got.header.Get("Cookie"), "_educoder_session="+upstreamSession) || !strings.Contains(got.header.Get("Cookie"), "autologin_trustie="+upstreamTrust) {
				t.Errorf("upstream request did not use server cookie jar: %v", got.header)
			}
			if strings.Contains(got.header.Get("Cookie"), session.Value) || strings.Contains(got.header.Get("Cookie"), marker.Value) {
				t.Fatal("local session/marker leaked upstream")
			}
			if got.header.Get("X-Original-Host") != "www.educoder.net" || got.header.Get("X-Original-Origin") != educoder.DefaultOrigin || got.header.Get("X-Original-Protocol") != "https:" {
				t.Errorf("browser spoofed upstream origin headers: %v", got.header)
			}
			timestamp := got.header.Get("X-EDU-Timestamp")
			if timestamp == "" || got.header.Get("X-EDU-Signature") != educoder.Signature(http.MethodGet, timestamp) {
				t.Error("server did not generate a valid signature")
			}
		})
	}
	if got := len(f.requests()); got != len(paths)+1 {
		t.Errorf("flow made %d upstream requests, want %d", got, len(paths)+1)
	}
}

func TestMyshixunVersionStatusOnlyAllowsAuthenticatedGET(t *testing.T) {
	f := newCompatFixture(t)
	f.server.unsupportedLog = log.New(io.Discard, "", 0)
	const path = "/api/myshixuns/myshixun_1-A/version_repository_delete.json"
	if w := f.request(http.MethodGet, path, ""); w.Code != http.StatusUnauthorized || len(f.requests()) != 0 {
		t.Fatalf("anonymous myshixun status: status=%d upstream=%d", w.Code, len(f.requests()))
	}
	_, session, _ := f.login(t)
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, path},
		{http.MethodPut, path},
		{http.MethodDelete, path},
		{http.MethodGet, "/api/myshixuns/myshixun_1-A/unknown.json"},
		{http.MethodGet, "/api/myshixuns/bad.identifier/version_repository_delete.json"},
		{http.MethodGet, "/api/myshixuns/bad%20identifier/version_repository_delete.json"},
		{http.MethodGet, "/api/myshixuns//version_repository_delete.json"},
		{http.MethodGet, "/api/myshixuns/a%2Fb/version_repository_delete.json"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			before := len(f.requests())
			w := f.request(tc.method, tc.path, `{"token":"must-not-be-forwarded"}`, session)
			if w.Code != http.StatusNotFound || len(f.requests()) != before {
				t.Errorf("unsupported myshixun request forwarded: status=%d upstream=%d (was %d)", w.Code, len(f.requests()), before)
			}
		})
	}
}

func TestOnlyObservedDoubleSlashShixunAliasIsNormalized(t *testing.T) {
	f := newCompatFixture(t)
	f.server.unsupportedLog = log.New(io.Discard, "", 0)
	_, session, _ := f.login(t)
	before := len(f.requests())
	w := f.request(http.MethodGet, "/api//shixuns/SHIXUN1/shixun_exec.json?homework_common_id=3985881&tag=a&tag=b&zzud=attacker", "", session)
	if w.Code != http.StatusOK {
		t.Fatalf("observed alias failed: status=%d body=%s", w.Code, w.Body.String())
	}
	seen := f.requests()
	if len(seen) != before+1 {
		t.Fatalf("alias made %d upstream requests, want %d", len(seen), before+1)
	}
	got := seen[len(seen)-1]
	const normalizedPath = "/api/shixuns/SHIXUN1/shixun_exec.json"
	if got.path != normalizedPath || got.query.Get("homework_common_id") != "3985881" || strings.Join(got.query["tag"], ",") != "a,b" || got.query.Get("zzud") != testLogin {
		t.Errorf("alias was not normalized before forwarding or query changed: %+v", got)
	}
	if route := string(decodeObject(t, w)["route"]); route != `"`+normalizedPath+`"` {
		t.Errorf("alias response did not preserve upstream JSON: %s", w.Body.String())
	}
	for _, path := range []string{
		"/api//users/student1/courses.json",
		"/api///shixuns/SHIXUN1/shixun_exec.json",
		"/api//shixuns/SHIXUN1/unknown.json",
		"/api//shixuns//SHIXUN1/shixun_exec.json",
	} {
		t.Run(path, func(t *testing.T) {
			before := len(f.requests())
			w := f.request(http.MethodGet, path, "", session)
			if w.Code != http.StatusNotFound || len(f.requests()) != before {
				t.Errorf("unobserved alias forwarded: path=%s status=%d upstream=%d (was %d)", path, w.Code, len(f.requests()), before)
			}
		})
	}
}

func TestObservedHomeworkRoutesRequireSessionAndPositiveIntegerID(t *testing.T) {
	f := newCompatFixture(t)
	f.server.unsupportedLog = log.New(io.Discard, "", 0)
	for _, path := range observedHomeworkGETs {
		t.Run("anonymous "+path, func(t *testing.T) {
			w := f.request(http.MethodGet, path, "")
			if w.Code != http.StatusUnauthorized || len(f.requests()) != 0 {
				t.Errorf("homework route allowed anonymous request: path=%s status=%d upstream=%d", path, w.Code, len(f.requests()))
			}
		})
	}
	_, session, _ := f.login(t)
	for _, path := range observedHomeworkGETs {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
			t.Run(method+" "+path, func(t *testing.T) {
				before := len(f.requests())
				w := f.request(method, path, `{"code":"must-not-be-forwarded"}`, session)
				if w.Code != http.StatusNotFound || len(f.requests()) != before {
					t.Errorf("homework mutation forwarded: method=%s path=%s status=%d upstream=%d (was %d)", method, path, w.Code, len(f.requests()), before)
				}
			})
		}
		for _, id := range []string{"0", "-1", "not-an-id", "1.0"} {
			invalidPath := strings.Replace(path, "3985881", id, 1)
			t.Run("invalid ID "+invalidPath, func(t *testing.T) {
				before := len(f.requests())
				w := f.request(http.MethodGet, invalidPath, "", session)
				if w.Code != http.StatusNotFound || len(f.requests()) != before {
					t.Errorf("nonpositive or noninteger homework ID accepted: path=%s status=%d upstream=%d (was %d)", invalidPath, w.Code, len(f.requests()), before)
				}
			})
		}
	}
	for _, path := range []string{"/api/homework_commons/3985881/unknown.json", "/api/shixun_homeworks/3985881/unknown.json"} {
		t.Run("unknown action "+path, func(t *testing.T) {
			before := len(f.requests())
			w := f.request(http.MethodGet, path, "", session)
			if w.Code != http.StatusNotFound || len(f.requests()) != before {
				t.Errorf("unknown homework action accepted: path=%s status=%d upstream=%d (was %d)", path, w.Code, len(f.requests()), before)
			}
		})
	}
}

func TestUnrecordedAndMutationRoutesNeverReachUpstream(t *testing.T) {
	f := newCompatFixture(t)
	_, session, _ := f.login(t)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/unknown.json"},
		{http.MethodGet, "/api/users/another-user/courses.json"},
		{http.MethodGet, "/api/users/another-user/homepage_info.json"},
		{http.MethodPost, "/api/courses/COURSE1/homework_commons.json"},
		{http.MethodPut, "/api/tasks/GAME1.json"},
		{http.MethodPatch, "/api/tasks/GAME1/rep_content.json"},
		{http.MethodDelete, "/api/tasks/GAME1.json"},
		{http.MethodPost, "/api/tasks/GAME1/run.json"},
		{http.MethodGet, "/api/tasks/GAME1/answer.json"},
		{http.MethodGet, "/api/accounts/login.json"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			before := len(f.requests())
			w := f.request(tc.method, tc.path, `{"code":"untrusted"}`, session)
			if w.Code < 400 || w.Code >= 500 || len(f.requests()) != before {
				t.Errorf("route forwarded or not rejected: status=%d requests=%d (was %d)", w.Code, len(f.requests()), before)
			}
		})
	}
	before := len(f.requests())
	w := f.request(http.MethodOptions, "/api/tasks/GAME1.json", "")
	if w.Code != http.StatusNoContent || len(f.requests()) != before {
		t.Error("OPTIONS preflight should be local")
	}
}

func TestAuxiliaryUserRoutesRequireTheExpectedSession(t *testing.T) {
	f := newCompatFixture(t)
	for _, path := range []string{"/api/users/get_notice.json", "/api/users/student1/homepage_info.json"} {
		w := f.request(http.MethodGet, path, "")
		if w.Code != http.StatusUnauthorized || len(f.requests()) != 0 {
			t.Errorf("anonymous auxiliary request reached upstream: path=%s status=%d", path, w.Code)
		}
	}
	w := f.request(http.MethodGet, "/api/users/system_update.json?zzud=attacker&version=1", "")
	if w.Code != http.StatusOK {
		t.Fatalf("public system update route failed: status=%d body=%s", w.Code, w.Body.String())
	}
	seen := f.requests()
	if len(seen) != 1 || seen[0].path != "/api/users/system_update.json" || seen[0].query.Has("zzud") || seen[0].query.Get("version") != "1" || seen[0].header.Get("Pc-Authorization") != "null" {
		t.Errorf("public system update forwarded browser identity or lost query: %+v", seen)
	}
}

func TestUnsupportedRouteLogsOnlyMethodAndQuotedPath(t *testing.T) {
	f := newCompatFixture(t)
	_, session, marker := f.login(t)
	var output bytes.Buffer
	f.server.unsupportedLog = log.New(&output, "", 0)
	before := len(f.requests())
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/unknown.json"},
		{http.MethodDelete, "/api/accounts/logout.json"},
		{http.MethodGet, "/api/not-recorded%0AINJECTED.json"},
	} {
		r := httptest.NewRequest(tc.method, tc.path+"?token=query-token-canary&password=query-password-canary", strings.NewReader(`{"password":"body-password-canary","token":"body-token-canary"}`))
		r.AddCookie(session)
		r.AddCookie(marker)
		r.Header.Set("Pc-Authorization", "header-token-canary")
		r.Header.Set("Authorization", "Bearer authorization-token-canary")
		r.Header.Set("X-Original-Origin", "https://header-origin-canary.invalid")
		w := httptest.NewRecorder()
		f.server.ServeHTTP(w, r)
		if w.Code < 400 || w.Code >= 500 {
			t.Errorf("unsupported request not rejected: %s %s status=%d", tc.method, tc.path, w.Code)
		}
	}
	want := "POST \"/api/unknown.json\"\nDELETE \"/api/accounts/logout.json\"\nGET \"/api/not-recorded\\nINJECTED.json\"\n"
	if got := output.String(); got != want {
		t.Errorf("unsupported logs must contain only method and quoted path:\n got %q\nwant %q", got, want)
	}
	for _, secret := range []string{"query-token-canary", "query-password-canary", "body-password-canary", "body-token-canary", "header-token-canary", "authorization-token-canary", "header-origin-canary", session.Value, marker.Value, upstreamSession, upstreamTrust} {
		if strings.Contains(output.String(), secret) {
			t.Errorf("unsupported logs leaked query/body/header/cookie value %q", secret)
		}
	}
	if len(f.requests()) != before {
		t.Error("unsupported logged requests reached upstream")
	}
}

func TestLogoutAndExpiryInvalidateRealSession(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			f := newCompatFixture(t)
			_, session, marker := f.login(t)
			before := len(f.requests())
			logout := f.request(method, "/api/accounts/logout.json", "", session, marker)
			if logout.Code != http.StatusOK || len(f.requests()) != before {
				t.Errorf("logout should succeed locally: status=%d", logout.Code)
			}
			cleared := logout.Result().Cookies()
			if len(cleared) != 2 {
				t.Fatalf("logout cleared %d cookies, want 2", len(cleared))
			}
			for _, cookie := range cleared {
				if cookie.Value != "" || cookie.MaxAge != -1 {
					t.Errorf("logout did not expire cookie: %+v", cookie)
				}
			}
			if w := f.request(http.MethodGet, "/api/courses/COURSE1/homework_commons.json", "", session, marker); w.Code != http.StatusUnauthorized {
				t.Errorf("old session still authenticated after logout: %d", w.Code)
			}
		})
	}
	t.Run("expiry", func(t *testing.T) {
		f := newCompatFixture(t)
		_, session, _ := f.login(t)
		f.server.store.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
		before := len(f.requests())
		w := f.request(http.MethodGet, "/api/courses/COURSE1/homework_commons.json", "", session)
		if w.Code != http.StatusUnauthorized || len(f.requests()) != before {
			t.Error("expired session still authenticated or reached upstream")
		}
	})
}

func TestStaticHostAllowlistMissingAssetsAndSPAFallback(t *testing.T) {
	f := newCompatFixture(t)
	for path, content := range map[string]string{
		"www.educoder.net/index.html":          "<html><main>recorded-app</main></html>",
		"www.educoder.net/app.js":              "window.recordedApp = true;",
		"www-cdn.educoder.net/assets/main.css": "body { color: #123; }",
		"ali-cdn.educoder.net/images/logo.svg": "<svg></svg>",
		"attacker.invalid/private.js":          "must not be served",
	} {
		absolute := filepath.Join(f.server.config.StaticDir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		path     string
		status   int
		contains string
	}{
		{"/", 200, "recorded-app"},
		{"/app.js", 200, "window.recordedApp"},
		{"/_assets/www.educoder.net/app.js", 200, "window.recordedApp"},
		{"/_assets/www-cdn.educoder.net/assets/main.css", 200, "body"},
		{"/_assets/ali-cdn.educoder.net/images/logo.svg", 200, "<svg>"},
		{"/user/login", 200, "recorded-app"},
		{"/users/student1/classrooms", 200, "recorded-app"},
		{"/classrooms/COURSE1/shixun_homework", 200, "recorded-app"},
		{"/tasks/GAME1", 200, "recorded-app"},
		{"/missing.js", 404, ""},
		{"/_assets/www.educoder.net/missing.js", 404, ""},
		{"/_assets/www-cdn.educoder.net/missing", 404, ""},
		{"/_assets/attacker.invalid/private.js", 404, ""},
		{"/_assets/www.educoder.net", 404, ""},
		{"/_assets/www.educoder.net/../index.html", 404, ""},
		{"/unrecorded-route", 404, ""},
	} {
		t.Run(tc.path, func(t *testing.T) {
			w := f.request(http.MethodGet, tc.path, "")
			if w.Code != tc.status || (tc.contains != "" && !strings.Contains(w.Body.String(), tc.contains)) {
				t.Errorf("GET %s: status=%d body=%q, want status=%d containing %q", tc.path, w.Code, w.Body.String(), tc.status, tc.contains)
			}
			if tc.status == http.StatusNotFound && strings.Contains(w.Body.String(), "recorded-app") {
				t.Error("missing asset incorrectly fell back to the SPA")
			}
		})
	}
	if w := f.request(http.MethodHead, "/app.js", ""); w.Code != http.StatusOK || w.Body.Len() != 0 {
		t.Errorf("HEAD static response: status=%d body=%q", w.Code, w.Body.String())
	}
	if w := f.request(http.MethodPost, "/app.js", ""); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST static response: status=%d", w.Code)
	}
	if len(f.requests()) != 0 {
		t.Fatal("static requests reached API upstream")
	}
}
