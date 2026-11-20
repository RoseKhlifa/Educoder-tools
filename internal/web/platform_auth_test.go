package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strconv"
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
	app := NewServerWithFactory(DefaultConfig(), func(string) EducoderClient { return &fakeClient{} })
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.RemoteAddr = "192.0.2.10:1234"
	r.Header.Set("X-Forwarded-For", "attacker-controlled")
	if app.loginSource(r) != "192.0.2.10" {
		t.Fatal("untrusted forwarded address bypassed limiter")
	}
}

// Production publishes one container port to a reverse proxy on the same host,
// so every student connects from the bridge gateway. A single shared bucket
// both locks out the campus after 120 attempts and leaves an abuser unmetered.
func TestLoginSourceSeparatesUsersBehindTheReverseProxy(t *testing.T) {
	app := NewServerWithFactory(DefaultConfig(), func(string) EducoderClient { return &fakeClient{} })
	forwarded := func(peer, header string, values ...string) string {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
		r.RemoteAddr = peer
		for _, value := range values {
			r.Header.Add(header, value)
		}
		return app.loginSource(r)
	}
	if got := forwarded("172.18.0.1:52344", "X-Forwarded-For", "203.0.113.7"); got != "203.0.113.7" {
		t.Fatalf("proxied student shares the proxy bucket: %q", got)
	}
	if got := forwarded("127.0.0.1:52344", "X-Real-IP", "203.0.113.8"); got != "203.0.113.8" {
		t.Fatalf("X-Real-IP from the local proxy ignored: %q", got)
	}
	if a, b := forwarded("172.18.0.1:1", "X-Forwarded-For", "203.0.113.7"), forwarded("172.18.0.1:2", "X-Forwarded-For", "203.0.113.9"); a == b {
		t.Fatal("two students behind the proxy still share one bucket")
	}
	// Production: student -> Cloudflare edge -> OpenResty on the host -> docker
	// gateway. OpenResty appends the edge to the X-Forwarded-For that Cloudflare
	// set, so the student is the first untrusted address from the right; a
	// value the student wrote in front is never reached, and a direct hit on the
	// origin is keyed by the caller OpenResty saw.
	if got := forwarded("192.168.96.1:41000", "X-Forwarded-For", "203.0.113.7, 172.70.12.34"); got != "203.0.113.7" {
		t.Fatalf("student behind Cloudflare keyed by %q", got)
	}
	if got := forwarded("192.168.96.1:41000", "X-Forwarded-For", "198.51.100.1, 203.0.113.7, 2606:4700:10::ac43:1"); got != "203.0.113.7" {
		t.Fatalf("spoofed prefix through Cloudflare keyed by %q", got)
	}
	if got := forwarded("192.168.96.1:41000", "X-Forwarded-For", "172.70.12.34, 198.51.100.9"); got != "198.51.100.9" {
		t.Fatalf("a direct origin caller claiming to be Cloudflare keyed by %q", got)
	}
	// A header a trusted hop did not add is never believed, from any direction.
	for _, spoofed := range [][]string{{"203.0.113.7"}, {"198.51.100.1, 203.0.113.7"}, {"198.51.100.1", "203.0.113.7"}} {
		if got := forwarded("198.51.100.9:443", "X-Forwarded-For", spoofed...); got != "198.51.100.9" {
			t.Fatalf("direct caller chose its own bucket with %v: %q", spoofed, got)
		}
	}
	// Chained trusted hops resolve to the first address none of them vouched for.
	if got := forwarded("172.18.0.1:1", "X-Forwarded-For", "203.0.113.7, 10.1.2.3"); got != "203.0.113.7" {
		t.Fatalf("nested proxy chain resolved to %q", got)
	}
	// Nonsense or absent headers fall back to the peer instead of one key per
	// attacker-chosen string, which would also flood the limiter map.
	for _, header := range []string{"", "not-an-address", "10.1.2.3"} {
		values := []string{header}
		if header == "" {
			values = nil
		}
		if got := forwarded("172.18.0.1:1", "X-Forwarded-For", values...); got != "172.18.0.1" {
			t.Fatalf("header %q produced bucket %q", header, got)
		}
	}
	strict := DefaultConfig()
	strict.TrustedProxies = []netip.Prefix{}
	app = NewServerWithFactory(strict, func(string) EducoderClient { return &fakeClient{} })
	if got := forwarded("127.0.0.1:1", "X-Forwarded-For", "203.0.113.7"); got != "127.0.0.1" {
		t.Fatalf("TRUSTED_PROXY_CIDRS=none still trusted a forwarded address: %q", got)
	}
}

func TestTrustedProxyConfiguration(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "none")
	cfg := DefaultConfig()
	if err := loadPlatformConfig(&cfg); err != nil || cfg.TrustedProxies == nil || len(cfg.TrustedProxies) != 0 {
		t.Fatalf("none = %v %v", cfg.TrustedProxies, err)
	}
	t.Setenv("TRUSTED_PROXY_CIDRS", "10.9.0.0/16, 192.0.2.5")
	cfg = DefaultConfig()
	if err := loadPlatformConfig(&cfg); err != nil || len(cfg.TrustedProxies) != 2 {
		t.Fatalf("explicit list = %v %v", cfg.TrustedProxies, err)
	}
	app := NewServerWithFactory(cfg, func(string) EducoderClient { return &fakeClient{} })
	if !app.trustedProxy("192.0.2.5") || !app.trustedProxy("10.9.3.4") || app.trustedProxy("127.0.0.1") {
		t.Fatal("configured list did not replace the defaults")
	}
	for _, blank := range []string{"", "   "} {
		t.Setenv("TRUSTED_PROXY_CIDRS", blank)
		cfg = DefaultConfig()
		if err := loadPlatformConfig(&cfg); err != nil || len(cfg.TrustedProxies) != len(defaultTrustedProxyCIDRs) {
			t.Fatalf("an unset deployment variable dropped the defaults: %v %v", cfg.TrustedProxies, err)
		}
	}
	t.Setenv("TRUSTED_PROXY_CIDRS", "not-a-network")
	cfg = DefaultConfig()
	if err := loadPlatformConfig(&cfg); err == nil {
		t.Fatal("invalid trusted proxy configuration accepted")
	}
}

// An abuser that rotates phone numbers must not be able to use the login page
// as an SMS cannon even while every per-phone limit is respected.
func TestPhoneCodeSendsAreBoundedAcrossAllPhoneNumbers(t *testing.T) {
	cfg := DefaultConfig()
	fake := &fakeClient{}
	app := NewServerWithFactory(cfg, func(string) EducoderClient { return fake })
	now := time.Now()
	app.loginLimits.now = func() time.Time { return now }
	send := func(phone, peer string) int {
		r := httptest.NewRequest(http.MethodPost, "/api/auth/sms/send", strings.NewReader(`{"phone":"`+phone+`"}`))
		r.RemoteAddr = peer
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		return w.Code
	}
	sent := 0
	for i := 0; i < 400; i++ {
		// Fresh phone and fresh client each time: only a service-wide budget
		// can stop this, and the attacker is not blocked by its own IP.
		phone := "1" + strconv.Itoa(3000000000+i)
		if send(phone, "172.18.0."+strconv.Itoa(2+i%200)+":5000") == http.StatusOK {
			sent++
		}
	}
	if sent != 300 {
		t.Fatalf("service-wide SMS budget allowed %d sends", sent)
	}
	if code := send("13999999999", "172.18.0.9:5000"); code != http.StatusTooManyRequests {
		t.Fatalf("exhausted SMS budget still reached upstream: %d", code)
	}
	now = now.Add(5 * time.Minute)
	if code := send("13999999998", "172.18.0.9:5000"); code != http.StatusOK {
		t.Fatalf("SMS budget did not recover: %d", code)
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

func TestPhoneCodeEndpointAppliesOriginAndSendLimits(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WebOrigin = "https://service.example"
	fake := &fakeClient{}
	app := NewServerWithFactory(cfg, func(string) EducoderClient { return fake })
	now := time.Now()
	app.loginLimits.now = func() time.Time { return now }
	request := func(origin string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "http://service.example/api/auth/sms/send", strings.NewReader(`{"phone":"13000000000"}`))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		return w
	}
	if response := request("https://attacker.example"); response.Code != http.StatusForbidden || fake.codeSent != "" {
		t.Fatalf("foreign-origin SMS request reached upstream: %d %s", response.Code, fake.codeSent)
	}
	for i := 0; i < 3; i++ {
		if response := request(cfg.WebOrigin); response.Code != http.StatusOK {
			t.Fatalf("SMS request %d = %d %s", i+1, response.Code, response.Body.String())
		}
		if response := request(cfg.WebOrigin); response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "60" {
			t.Fatalf("SMS cooldown ignored: %d", response.Code)
		}
		now = now.Add(time.Minute)
	}
	if response := request(cfg.WebOrigin); response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "300" {
		t.Fatalf("fourth SMS request was not limited: %d %s", response.Code, response.Body.String())
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
