package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"educoder-cli/internal/educoder"
)

type wechatFakeClient struct {
	fakeClient
	starts      atomic.Int32
	polls       atomic.Int32
	completions atomic.Int32
	result      *educoder.WeChatLoginPoll
	err         error
	onPoll      func(context.Context) (*educoder.WeChatLoginPoll, error)
	onComplete  func(context.Context) (*educoder.User, error)
}

func (f *wechatFakeClient) StartWeChatLogin(context.Context) (*educoder.WeChatLoginQR, error) {
	f.starts.Add(1)
	return &educoder.WeChatLoginQR{UUID: "private-qr-canary", Image: []byte("\x89PNG\r\n\x1a\nfixture")}, nil
}
func (f *wechatFakeClient) PollWeChatLogin(ctx context.Context, uuid, last string) (*educoder.WeChatLoginPoll, error) {
	f.polls.Add(1)
	if f.onPoll != nil {
		return f.onPoll(ctx)
	}
	if f.result != nil {
		return f.result, nil
	}
	return &educoder.WeChatLoginPoll{Status: "confirmed", Code: "private-oauth-canary"}, nil
}
func (f *wechatFakeClient) CompleteWeChatLogin(ctx context.Context, _ string) (*educoder.User, error) {
	f.completions.Add(1)
	if f.onComplete != nil {
		return f.onComplete(ctx)
	}
	if f.err != nil {
		return nil, f.err
	}
	return &educoder.User{UserID: 91, Login: "wechat-student", Name: "扫码用户"}, nil
}

func wechatRequest(app *Server, action string, cookie *http.Cookie) *httptest.ResponseRecorder {
	method, body := http.MethodPost, "{}"
	if action == "image" {
		method, body = http.MethodGet, ""
	}
	r := httptest.NewRequest(method, "/api/auth/wechat/"+action, strings.NewReader(body))
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	return w
}

func startFakeWeChat(t *testing.T, app *Server) *http.Cookie {
	t.Helper()
	w := wechatRequest(app, "start", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("start=%d %s", w.Code, w.Body.String())
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == wechatAttemptCookie {
			return cookie
		}
	}
	t.Fatal("start did not bind browser cookie")
	return nil
}

func TestWeChatLoginCookieBindingIdentityAndAtMostOnceCompletion(t *testing.T) {
	fake := &wechatFakeClient{}
	cfg := DefaultConfig()
	cfg.WebOrigin = "https://service.example"
	app := NewServerWithFactory(cfg, func(string) EducoderClient { return fake })
	cookie := startFakeWeChat(t, app)
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/api/auth/wechat" {
		t.Fatalf("unprotected attempt cookie: %+v", cookie)
	}
	if w := wechatRequest(app, "image", nil); w.Code != http.StatusGone {
		t.Fatalf("unbound browser read QR=%d", w.Code)
	}
	if w := wechatRequest(app, "poll", nil); w.Code != http.StatusGone || fake.polls.Load() != 0 {
		t.Fatalf("unbound browser polled=%d", w.Code)
	}
	if w := wechatRequest(app, "image", cookie); w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/png" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("bound image=%d", w.Code)
	}
	var sessionID string
	for i := 0; i < 2; i++ {
		w := wechatRequest(app, "poll", cookie)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status":"succeeded"`) || !strings.Contains(w.Body.String(), "wechat-student") {
			t.Fatalf("poll=%d %s", w.Code, w.Body.String())
		}
		for _, secret := range []string{"private-qr-canary", "private-oauth-canary", cookie.Value} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatal("public response leaked login transaction")
			}
		}
		for _, returned := range w.Result().Cookies() {
			if returned.Name == sessionCookieName {
				if sessionID != "" && sessionID != returned.Value {
					t.Fatal("replayed success created another session")
				}
				sessionID = returned.Value
			}
		}
	}
	if fake.completions.Load() != 1 || fake.polls.Load() != 1 || app.Store().Len() != 1 || sessionID == "" {
		t.Fatal("lost success replay re-consumed code or duplicated session")
	}
	request := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionID})
	w := httptest.NewRecorder()
	app.ServeHTTP(w, request)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "wechat-student") {
		t.Fatal("wechat did not establish normal session")
	}
}

func TestWeChatLoginErrorsNeverReplayCodeOrExposeRawCause(t *testing.T) {
	for _, fixture := range []struct {
		err  error
		code string
	}{
		{errors.New("private-oauth-canary cookie-canary"), "WECHAT_LOGIN_FAILED"},
		{educoder.ErrLoginBindingRequired, "WECHAT_BINDING_REQUIRED"},
		{educoder.ErrLoginExamRestricted, "WECHAT_RESTRICTED"},
	} {
		t.Run(fixture.code, func(t *testing.T) {
			fake := &wechatFakeClient{err: fixture.err}
			app := NewServerWithFactory(DefaultConfig(), func(string) EducoderClient { return fake })
			cookie := startFakeWeChat(t, app)
			for i := 0; i < 2; i++ {
				w := wechatRequest(app, "poll", cookie)
				if w.Code < 400 || !strings.Contains(w.Body.String(), fixture.code) || strings.Contains(w.Body.String(), "canary") {
					t.Fatalf("error=%d %s", w.Code, w.Body.String())
				}
			}
			if fake.completions.Load() != 1 || app.Store().Len() != 0 {
				t.Fatal("uncertain callback replayed or authenticated")
			}
		})
	}
}

func TestWeChatCancelInterruptsPollAndRevokesOnlyAttemptSession(t *testing.T) {
	entered := make(chan struct{})
	fake := &wechatFakeClient{onPoll: func(ctx context.Context) (*educoder.WeChatLoginPoll, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	app := NewServerWithFactory(DefaultConfig(), func(string) EducoderClient { return fake })
	unrelated, err := app.store.Create(fake, educoder.User{UserID: 10, Login: "unrelated"})
	if err != nil {
		t.Fatal(err)
	}
	cookie := startFakeWeChat(t, app)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- wechatRequest(app, "poll", cookie) }()
	<-entered
	if w := wechatRequest(app, "cancel", cookie); w.Code != http.StatusOK {
		t.Fatalf("cancel=%d", w.Code)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("canceled long poll did not return")
	}
	if fake.completions.Load() != 0 {
		t.Fatal("canceled poll consumed auth code")
	}
	if _, ok := app.store.Get(unrelated); !ok {
		t.Fatal("cancel revoked unrelated login")
	}
	if w := wechatRequest(app, "poll", cookie); w.Code != http.StatusGone {
		t.Fatalf("canceled transaction usable: %d", w.Code)
	}
	// A cancellation arriving just after completion also revokes that attempt's
	// session, protecting the switch to another login method from a late reply.
	fake.onPoll = nil
	cookie = startFakeWeChat(t, app)
	if w := wechatRequest(app, "poll", cookie); w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	if app.store.Len() != 2 {
		t.Fatal("completed login not stored")
	}
	wechatRequest(app, "cancel", cookie)
	if app.store.Len() != 1 {
		t.Fatal("cancel failed to revoke completed attempt")
	}
}

func TestWeChatExpiryAndForeignOriginDoNotReachUpstream(t *testing.T) {
	fake := &wechatFakeClient{}
	app := NewServerWithFactory(DefaultConfig(), func(string) EducoderClient { return fake })
	r := httptest.NewRequest(http.MethodPost, "/api/auth/wechat/start", strings.NewReader("{}"))
	r.Header.Set("Origin", "https://foreign.example")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || fake.starts.Load() != 0 {
		t.Fatal("foreign login reached upstream")
	}
	cookie := startFakeWeChat(t, app)
	now := app.wechatLogins.now()
	app.wechatLogins.now = func() time.Time { return now.Add(wechatAttemptTTL + time.Second) }
	if w := wechatRequest(app, "poll", cookie); w.Code != http.StatusGone || fake.polls.Load() != 0 {
		t.Fatal("expired transaction reached upstream")
	}
}

func TestWeChatLoginNeverAcceptsCallerSuppliedUUIDOrCode(t *testing.T) {
	fake := &wechatFakeClient{}
	app := NewServerWithFactory(DefaultConfig(), func(string) EducoderClient { return fake })
	for _, path := range []string{"/api/auth/wechat/start", "/api/auth/wechat/poll", "/api/auth/wechat/cancel"} {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"code":"attacker-code","uuid":"attacker-uuid"}`))
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("injected body accepted: %s %d", path, w.Code)
		}
	}
	if fake.starts.Load() != 0 || fake.polls.Load() != 0 || fake.completions.Load() != 0 {
		t.Fatal("caller controlled OAuth values")
	}
}

func TestWeChatConfirmationFinishesAfterBrowserDisconnectAndCanBeRecovered(t *testing.T) {
	requestCtx, disconnect := context.WithCancel(context.Background())
	defer disconnect()
	fake := &wechatFakeClient{}
	fake.onPoll = func(context.Context) (*educoder.WeChatLoginPoll, error) {
		disconnect()
		return &educoder.WeChatLoginPoll{Status: "confirmed", Code: "private-code"}, nil
	}
	fake.onComplete = func(ctx context.Context) (*educoder.User, error) {
		deadline, ok := ctx.Deadline()
		if ctx.Err() != nil || !ok || time.Until(deadline) < 15*time.Second {
			t.Error("callback inherited exhausted/canceled browser poll budget")
		}
		return &educoder.User{UserID: 91, Login: "wechat-student"}, nil
	}
	app := NewServerWithFactory(DefaultConfig(), func(string) EducoderClient { return fake })
	cookie := startFakeWeChat(t, app)
	r := httptest.NewRequest(http.MethodPost, "/api/auth/wechat/poll", strings.NewReader("{}"))
	r = r.WithContext(requestCtx)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != http.StatusOK || fake.completions.Load() != 1 {
		t.Fatalf("disconnect lost completed authentication: %d %s", w.Code, w.Body.String())
	}
	recovered := wechatRequest(app, "poll", cookie)
	if recovered.Code != http.StatusOK || !strings.Contains(recovered.Body.String(), `"status":"succeeded"`) || fake.completions.Load() != 1 {
		t.Fatal("completed transaction could not be recovered without repeating code")
	}
}
