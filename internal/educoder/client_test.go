package educoder

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestProtocolVectors(t *testing.T) {
	if got, want := EncryptPassword("test-password"), "p15krDvHEPYqjf560oABdQ=="; got != want {
		t.Fatalf("password encryption = %q, want %q", got, want)
	}
	if got, want := Signature("GET", "1793348448103"), "48d0f98354bbf9387e51614db35b4cae"; got != want {
		t.Fatalf("HAR signature = %q, want %q", got, want)
	}
}

func TestLoginCourseHomeworkEnterFlow(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Method != http.MethodOptions {
			if r.Header.Get("X-EDU-Type") != "pc" || !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(r.Header.Get("X-EDU-Signature")) {
				t.Errorf("missing protocol headers for %s", r.URL.Path)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/accounts/login.json":
			w.Header().Add("Set-Cookie", "_educoder_session=test-session; Path=/")
			json.NewEncoder(w).Encode(map[string]any{"user_id": 1, "login": "demo", "name": "Demo", "identity": "student"})
		case r.URL.Path == "/api/users/get_user_info.json":
			if got := r.Header.Get("Pc-Authorization"); got != "test-session" {
				t.Errorf("Pc-Authorization = %q", got)
			}
			json.NewEncoder(w).Encode(map[string]any{"user_id": 1, "login": "demo", "name": "Demo", "identity": "student"})
		case r.URL.Path == "/api/users/demo/courses.json":
			json.NewEncoder(w).Encode(map[string]any{"count": 1, "courses": []any{map[string]any{"id": 10, "name": "Course", "first_category_url": "/classrooms/COURSE1/announcement", "homework_commons_count": 1}}})
		case r.URL.Path == "/api/courses/COURSE1/homework_commons.json":
			json.NewEncoder(w).Encode(map[string]any{"query_total_count": 1, "homeworks": []any{map[string]any{"homework_id": 99, "name": "Homework", "is_shixun": true, "shixun_identifier": "SHIXUN1", "status": []string{"提交中"}}}})
		case r.URL.Path == "/api/shixuns/SHIXUN1/shixun_exec.json":
			json.NewEncoder(w).Encode(map[string]any{"game_identifier": "GAME1"})
		case r.URL.Path == "/api/tasks/GAME1.json":
			json.NewEncoder(w).Encode(map[string]any{"game": map[string]any{"id": 7, "identifier": "GAME1", "myshixun_id": 8}, "challenge": map[string]any{"id": 9, "subject": "Challenge", "task_pass": "Description"}, "shixun": map[string]any{"id": 11, "identifier": "SHIXUN1", "name": "Shixun"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	c := New(server.URL)
	user, err := c.Login(context.Background(), "demo", "test-password")
	if err != nil || user.Login != "demo" {
		t.Fatalf("login: user=%+v err=%v", user, err)
	}
	if _, err = c.UserInfo(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	courses, err := c.Courses(context.Background(), "processing")
	if err != nil || len(courses) != 1 || courses[0].Identifier != "COURSE1" {
		t.Fatalf("courses=%+v err=%v", courses, err)
	}
	task, err := c.Enter(context.Background(), "COURSE1", 99, "")
	if err != nil || task.Game.Identifier != "GAME1" {
		t.Fatalf("task=%+v err=%v", task, err)
	}
	joined := strings.Join(requests, "\n")
	for _, want := range []string{"POST /api/accounts/login.json", "GET /api/users/get_user_info.json", "GET /api/users/demo/courses.json", "GET /api/courses/COURSE1/homework_commons.json", "GET /api/shixuns/SHIXUN1/shixun_exec.json", "GET /api/tasks/GAME1.json"} {
		if !strings.Contains(joined, want) {
			t.Errorf("request sequence missing %q:\n%s", want, joined)
		}
	}
}
