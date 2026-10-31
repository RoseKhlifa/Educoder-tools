package web

import (
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	"educoder-cli/internal/educoder"
)

func TestHomeworkListExcludesClosedAssignmentsButKeepsOpenAndUndated(t *testing.T) {
	ts, fake := newTestServer()
	defer ts.Close()
	fake.homeworks = []educoder.Homework{
		{ID: 1, Name: "已截止", Status: []string{"已截止"}, EndTime: "2099-01-01 00:00:00", IsShixun: true},
		{ID: 2, Name: "已过期时间", Status: []string{"提交后"}, EndTime: "2026-10-23 22:59:00", IsShixun: true},
		{ID: 3, Name: "仍可安排", Status: []string{"提交后"}, EndTime: "2099-01-01 00:00:00", IsShixun: true},
		{ID: 4, Name: "没有截止时间", Status: []string{"提交后"}, EndTime: "--", IsShixun: true},
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	login := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/login", `{"login":"student","password":"secret"}`)
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login=%d %s", login.StatusCode, login.Body)
	}
	result := doJSON(t, client, http.MethodGet, ts.URL+"/api/courses/COURSE1/homeworks", "")
	if result.StatusCode != http.StatusOK {
		t.Fatalf("homeworks=%d %s", result.StatusCode, result.Body)
	}
	for _, closed := range []string{"已截止", "已过期时间"} {
		if strings.Contains(result.Body, closed) {
			t.Fatalf("closed homework leaked into user list: %s", result.Body)
		}
	}
	for _, open := range []string{"仍可安排", "没有截止时间"} {
		if !strings.Contains(result.Body, open) {
			t.Fatalf("open homework was filtered: %s", result.Body)
		}
	}
	if educoder.OpenHomeworks(fake.homeworks, time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC))[0].Name != "仍可安排" {
		t.Fatal("filter changed expected ordering or availability")
	}
}
