package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/platform"
)

func TestPlatformLoginProvisioningAndImmediateDisable(t *testing.T) {
	ps, err := platform.Open(filepath.Join(t.TempDir(), "platform.sqlite"), platform.Options{AdminUserIDs: []int64{1}})
	if err != nil {
		t.Fatal(err)
	}
	defer ps.Close()
	admin, err := ps.UpsertLogin(context.Background(), platform.Identity{UpstreamID: 1, Login: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeClient{user: educoder.User{UserID: 9, Login: "student", Name: "同学", Identity: "teacher"}}
	app := NewServerWithFactory(DefaultConfig(), func(string) EducoderClient { return fake })
	app.AttachPlatform(ps)
	ts := httptest.NewServer(app)
	defer ts.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	login := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/login", `{"login":"student","password":"fixture-only"}`)
	if login.StatusCode != 200 || !strings.Contains(login.Body, `"role":"user"`) {
		t.Fatalf("login = %d %s", login.StatusCode, login.Body)
	}
	var body struct {
		Data struct {
			Platform struct {
				ID string `json:"id"`
			} `json:"platform"`
		} `json:"data"`
	}
	if err = json.Unmarshal([]byte(login.Body), &body); err != nil {
		t.Fatal(err)
	}
	user, err := ps.LookupUser(context.Background(), body.Data.Platform.ID)
	if err != nil {
		t.Fatal(err)
	}
	if user.IsAdmin() {
		t.Fatal("upstream teacher identity incorrectly grants admin")
	}
	if err = ps.SetStatus(context.Background(), admin.ID, user.ID, "disabled"); err != nil {
		t.Fatal(err)
	}
	me := doJSON(t, client, http.MethodGet, ts.URL+"/api/auth/me", "")
	if me.StatusCode != 401 {
		t.Fatalf("disabled session still active: %d %s", me.StatusCode, me.Body)
	}
	if app.Store().Len() != 0 {
		t.Fatal("disabled session not removed")
	}
}

func TestLoginLimiterBoundedByAccountAndSource(t *testing.T) {
	l := newLoginLimiter()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	key := loginAccountKey(" STUDENT ")
	if key != loginAccountKey("student") {
		t.Fatal("account limiter normalizes inconsistently")
	}
	for i := 0; i < 12; i++ {
		if !l.allow(key, 12) {
			t.Fatal("account limit reached early")
		}
	}
	if l.allow(key, 12) {
		t.Fatal("account limit ignored")
	}
	if !l.allow(loginAccountKey("other"), 12) {
		t.Fatal("account limits leaked between identities")
	}
	now = now.Add(5 * time.Minute)
	if !l.allow(key, 12) {
		t.Fatal("limiter window did not recover")
	}
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.RemoteAddr = "192.0.2.10:1234"
	r.Header.Set("X-Forwarded-For", "attacker-controlled")
	if loginSource(r) != "192.0.2.10" {
		t.Fatal("untrusted forwarded address bypassed limiter")
	}
}

func TestPlatformLoginRetainsCSRFAndCookieProtectionsWithoutCards(t *testing.T) {
	ctx := context.Background()
	ps, err := platform.Open(filepath.Join(t.TempDir(), "platform.sqlite"), platform.Options{AdminUserIDs: []int64{1}})
	if err != nil {
		t.Fatal(err)
	}
	defer ps.Close()
	fake := &fakeClient{user: educoder.User{UserID: 9, Login: "student"}}
	cfg := DefaultConfig()
	cfg.WebOrigin = "https://service.example"
	app := NewServerWithFactory(cfg, func(string) EducoderClient { return fake })
	app.AttachPlatform(ps)
	body := `{"login":"student","password":"fixture-only"}`
	request := func(method, path, origin, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "http://service.example"+path, strings.NewReader(body))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		return w
	}
	for _, origin := range []string{"https://foreign.example", "http://service.example", "null"} {
		w := request(http.MethodPost, "/api/auth/login", origin, body, nil)
		if w.Code != http.StatusForbidden || fake.loginName != "" {
			t.Fatalf("CSRF login invoked upstream: %q %d", origin, w.Code)
		}
	}
	users, err := ps.ListUsers(ctx, 10)
	if err != nil || len(users) != 0 {
		t.Fatal("blocked login created platform user")
	}
	login := request(http.MethodPost, "/api/auth/login", cfg.WebOrigin, body, nil)
	if login.Code != http.StatusOK || strings.Contains(login.Body.String(), "fixture-only") {
		t.Fatalf("password-only login = %d %s", login.Code, login.Body.String())
	}
	users, err = ps.ListUsers(ctx, 10)
	if err != nil || len(users) != 1 || users[0].Status != "active" {
		t.Fatal("login failed to provision active account")
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatal("production session cookie lacks protections")
	}
	cookie := cookies[0]
	logout := request(http.MethodPost, "/api/auth/logout", "https://foreign.example", "", cookie)
	if logout.Code != http.StatusForbidden {
		t.Fatal("cross-origin logout accepted")
	}
	if _, ok := app.Store().Get(cookie.Value); !ok {
		t.Fatal("cross-origin logout removed session")
	}
	r := httptest.NewRequest(http.MethodPost, "http://service.example/api/auth/logout", nil)
	r.AddCookie(cookie)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatal("cross-site request without Origin accepted")
	}
	if me := request(http.MethodGet, "/api/auth/me", cfg.WebOrigin, "", cookie); me.Code != http.StatusOK {
		t.Fatalf("valid session rejected: %d", me.Code)
	}
}

func TestLegacyCardConfigurationIgnoredAndEndpointsRemoved(t *testing.T) {
	for _, value := range []string{"true", "invalid-old-value"} {
		t.Setenv("ACCESS_CARD_REQUIRED", value)
		cfg := DefaultConfig()
		if err := loadPlatformConfig(&cfg); err != nil {
			t.Fatalf("retired configuration still parsed: %v", err)
		}
		fake := &fakeClient{user: educoder.User{UserID: 9, Login: "student"}}
		app := NewServerWithFactory(cfg, func(string) EducoderClient { return fake })
		ps, err := platform.Open(filepath.Join(t.TempDir(), "platform.sqlite"), platform.Options{})
		if err != nil {
			t.Fatal(err)
		}
		app.AttachPlatform(ps)
		login := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"login":"student","password":"fixture-only"}`))
		result := httptest.NewRecorder()
		app.ServeHTTP(result, login)
		if result.Code != http.StatusOK {
			t.Fatalf("legacy setting blocked login: %d %s", result.Code, result.Body.String())
		}
		ps.Close()
	}
	app := NewServerWithFactory(DefaultConfig(), func(string) EducoderClient { return &fakeClient{} })
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		r := httptest.NewRequest(method, "/api/auth/config", nil)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Fatalf("retired auth config route still exposed: %s %d", method, w.Code)
		}
	}
	legacy := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"login":"student","password":"fixture-only","access_code":"retired"}`))
	w := httptest.NewRecorder()
	app.ServeHTTP(w, legacy)
	if w.Code != http.StatusBadRequest {
		t.Fatal("retired login field still accepted")
	}
}

func TestAdministratorPermissionsAreRefreshedFromStore(t *testing.T) {
	ctx := context.Background()
	ps, err := platform.Open(filepath.Join(t.TempDir(), "platform.sqlite"), platform.Options{AdminUserIDs: []int64{1}})
	if err != nil {
		t.Fatal(err)
	}
	defer ps.Close()
	admin, err := ps.UpsertLogin(ctx, platform.Identity{UpstreamID: 1, Login: "administrator"})
	if err != nil {
		t.Fatal(err)
	}
	u, err := ps.UpsertLogin(ctx, platform.Identity{UpstreamID: 9, Login: "student"})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeClient{}
	app := NewServerWithFactory(DefaultConfig(), func(string) EducoderClient { return fake })
	app.AttachPlatform(ps)
	id, err := app.Store().createPrincipal(fake, educoder.User{UserID: 9, Login: "student"}, "b", u)
	if err != nil {
		t.Fatal(err)
	}
	check := func() bool {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: id})
		_, ok := app.adminSession(httptest.NewRecorder(), r, "fixture")
		return ok
	}
	if check() {
		t.Fatal("ordinary user authorized as admin")
	}
	if err = ps.SetRole(ctx, admin.ID, u.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if !check() {
		t.Fatal("granted role not refreshed")
	}
	if err = ps.SetRole(ctx, admin.ID, u.ID, "user"); err != nil {
		t.Fatal(err)
	}
	if check() {
		t.Fatal("stale session retained revoked admin role")
	}
}
