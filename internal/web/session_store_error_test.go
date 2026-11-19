package web

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/platform"
)

// A platform store that cannot be read says nothing about the account. The
// session must survive it, because signing back in needs the Educoder password.
func TestSessionSurvivesUnreadablePlatformStore(t *testing.T) {
	ps, err := platform.Open(filepath.Join(t.TempDir(), "platform.sqlite"), platform.Options{})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeClient{user: educoder.User{UserID: 9, Login: "student", Name: "同学"}}
	app := NewServerWithFactory(DefaultConfig(), func(string) EducoderClient { return fake })
	app.AttachPlatform(ps)
	ts := httptest.NewServer(app)
	defer ts.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	login := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/login", `{"login":"student","password":"fixture-only"}`)
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login = %d %s", login.StatusCode, login.Body)
	}
	if err = ps.Close(); err != nil {
		t.Fatal(err)
	}
	me := doJSON(t, client, http.MethodGet, ts.URL+"/api/auth/me", "")
	if me.StatusCode != http.StatusServiceUnavailable || !strings.Contains(me.Body, "SESSION_CHECK_UNAVAILABLE") {
		t.Fatalf("store error answered %d %s, want 503 SESSION_CHECK_UNAVAILABLE", me.StatusCode, me.Body)
	}
	if app.Store().Len() != 1 {
		t.Fatal("a platform store error logged the user out")
	}
}
