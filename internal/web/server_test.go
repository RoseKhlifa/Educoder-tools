package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"educoder-cli/internal/educoder"
)

type fakeClient struct {
	loginName string
	password  string
	user      educoder.User
	courses   []educoder.Course
	homeworks []educoder.Homework
	task      educoder.Task
}

func (f *fakeClient) Login(_ context.Context, login, password string) (*educoder.User, error) {
	f.loginName, f.password = login, password
	user := f.user
	if user.Login == "" {
		user = educoder.User{UserID: 9, Login: login, Name: "学生", Identity: "student"}
	}
	return &user, nil
}
func (f *fakeClient) UserInfo(_ context.Context, _ string) (*educoder.User, error) {
	return &f.user, nil
}
func (f *fakeClient) Courses(_ context.Context, _ string) ([]educoder.Course, error) {
	return f.courses, nil
}
func (f *fakeClient) Homeworks(_ context.Context, _, _ string) ([]educoder.Homework, error) {
	return f.homeworks, nil
}
func (f *fakeClient) Enter(_ context.Context, _ string, _ int64, _ string) (*educoder.Task, error) {
	return &f.task, nil
}

func newTestServer() (*httptest.Server, *fakeClient) {
	fake := &fakeClient{
		courses:   []educoder.Course{{ID: 1, Identifier: "COURSE1", Name: "数据结构", HomeworkCount: 1}},
		homeworks: []educoder.Homework{{ID: 42, Name: "BF 算法", Status: []string{"未开始"}, ShixunIdentifier: "shixun1", IsShixun: true}},
		task:      educoder.Task{CourseIdentifier: "COURSE1", HomeworkID: 42, URL: "https://www.educoder.net/tasks/COURSE1/42/game1"},
	}
	factory := func(string) EducoderClient { return fake }
	cfg := DefaultConfig()
	cfg.StaticDir = ""
	cfg.WebOrigin = "http://frontend.test"
	return httptest.NewServer(NewServerWithFactory(cfg, factory).Handler()), fake
}

func TestLoginCoursesHomeworksEnter(t *testing.T) {
	ts, fake := newTestServer()
	defer ts.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}

	login := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/login", `{"login":"student","password":"secret"}`)
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, body=%s", login.StatusCode, login.Body)
	}
	if fake.loginName != "student" || fake.password != "secret" {
		t.Fatalf("fake did not receive credentials: %#v", fake)
	}
	if strings.Contains(login.Body, "secret") {
		t.Fatal("login response leaked password")
	}
	if len(jarCookies(t, jar, ts.URL)) != 1 {
		t.Fatal("expected one HttpOnly session cookie")
	}
	assertEnvelope(t, login.Body, true)

	me := doJSON(t, client, http.MethodGet, ts.URL+"/api/auth/me", "")
	if me.StatusCode != http.StatusOK || !strings.Contains(me.Body, `"login":"student"`) {
		t.Fatalf("me = %d %s", me.StatusCode, me.Body)
	}

	courses := doJSON(t, client, http.MethodGet, ts.URL+"/api/courses", "")
	if courses.StatusCode != http.StatusOK || !strings.Contains(courses.Body, "COURSE1") {
		t.Fatalf("courses = %d %s", courses.StatusCode, courses.Body)
	}

	homeworks := doJSON(t, client, http.MethodGet, ts.URL+"/api/courses/COURSE1/homeworks", "")
	if homeworks.StatusCode != http.StatusOK || !strings.Contains(homeworks.Body, "BF 算法") {
		t.Fatalf("homeworks = %d %s", homeworks.StatusCode, homeworks.Body)
	}

	fake.task.Game.Identifier = "game1"
	entered := doJSON(t, client, http.MethodGet, ts.URL+"/api/courses/COURSE1/homeworks/42/enter", "")
	if entered.StatusCode != http.StatusOK || !strings.Contains(entered.Body, "game1") {
		t.Fatalf("enter = %d %s", entered.StatusCode, entered.Body)
	}

	logout := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/logout", "")
	if logout.StatusCode != http.StatusOK {
		t.Fatalf("logout = %d %s", logout.StatusCode, logout.Body)
	}
	after := doJSON(t, client, http.MethodGet, ts.URL+"/api/courses", "")
	if after.StatusCode != http.StatusUnauthorized {
		t.Fatalf("courses after logout = %d %s", after.StatusCode, after.Body)
	}
}

func TestAuthAndValidationErrorsDoNotLeakSecrets(t *testing.T) {
	ts, _ := newTestServer()
	defer ts.Close()
	client := ts.Client()
	bad := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/login", `{"login":"","password":"super-secret"}`)
	if bad.StatusCode != http.StatusBadRequest || strings.Contains(bad.Body, "super-secret") {
		t.Fatalf("bad login = %d %s", bad.StatusCode, bad.Body)
	}
	unauth := doJSON(t, client, http.MethodGet, ts.URL+"/api/courses", "")
	if unauth.StatusCode != http.StatusUnauthorized || !strings.Contains(unauth.Body, "AUTH_REQUIRED") {
		t.Fatalf("unauth = %d %s", unauth.StatusCode, unauth.Body)
	}
	invalid := doJSON(t, client, http.MethodGet, ts.URL+"/api/courses/a%2Fb/homeworks", "")
	if invalid.StatusCode != http.StatusNotFound {
		t.Fatalf("invalid unauthenticated path = %d %s", invalid.StatusCode, invalid.Body)
	}
}

func TestSessionExpiry(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SessionTTL = time.Millisecond
	cfg.StaticDir = ""
	fake := &fakeClient{}
	s := NewServerWithFactory(cfg, func(string) EducoderClient { return fake })
	var now time.Time
	store := s.Store()
	now = time.Now()
	store.now = func() time.Time { return now }
	id, err := store.Create(fake, educoder.User{Login: "student"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Get(id); !ok {
		t.Fatal("session should be valid before expiry")
	}
	now = now.Add(time.Second)
	if _, ok := store.Get(id); ok {
		t.Fatal("session should expire")
	}
}

type jsonResult struct {
	StatusCode int
	Body       string
}

func doJSON(t *testing.T, client *http.Client, method, url, body string) jsonResult {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var payload any
	_ = json.NewDecoder(resp.Body).Decode(&payload)
	b, _ := json.Marshal(payload)
	return jsonResult{StatusCode: resp.StatusCode, Body: string(b)}
}

func assertEnvelope(t *testing.T, body string, success bool) {
	t.Helper()
	var envelope struct {
		Data      any    `json:"data"`
		Error     any    `json:"error"`
		RequestID string `json:"requestId"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.RequestID == "" {
		t.Fatal("missing request id")
	}
	if success && envelope.Error != nil {
		t.Fatalf("unexpected envelope error: %#v", envelope.Error)
	}
}

func jarCookies(t *testing.T, jar http.CookieJar, rawURL string) []*http.Cookie {
	t.Helper()
	parsed, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	return jar.Cookies(parsed.URL)
}
