package compat

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"educoder-cli/internal/educoder"
)

func TestProgrammingRoutesForwardExactJSONAndUseServerSession(t *testing.T) {
	f := newCompatFixture(t)
	_, session, _ := f.login(t)
	const body = `{ "content": "line 1\n  line 2\n", "evaluate": 1, "extras": {"homework_common_id":"42"} }`
	for _, route := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/myshixuns/MY1/update_file.json", body},
		{http.MethodPost, "/api/tasks/GAME1/log_output", `{"test_case_content":"input\n"}`},
		{http.MethodPost, "/api/tasks/GAME1/game_build.json", `{"first":1,"sec_key":"local-evaluation-fixture"}`},
		{http.MethodPost, "/api/tasks/GAME1/cost_time.json", ""},
		{http.MethodGet, "/api/tasks/GAME1/game_status.json", ""},
	} {
		t.Run(route.path, func(t *testing.T) {
			before := len(f.requests())
			anonymous := f.request(route.method, route.path, route.body)
			if anonymous.Code != http.StatusUnauthorized || len(f.requests()) != before {
				t.Fatal("programming route must require the local session")
			}
			w := f.request(route.method, route.path+"?zzud=untrusted&homework_common_id=42", route.body, session)
			if w.Code != http.StatusOK || w.Header().Get("X-App-Cache") != "BYPASS" {
				t.Fatalf("status=%d cache=%s", w.Code, w.Header().Get("X-App-Cache"))
			}
			seen := f.requests()
			if len(seen) != before+1 {
				t.Fatal("expected exactly one upstream request")
			}
			got := seen[len(seen)-1]
			if got.body != route.body || got.method != route.method || got.query.Get("zzud") != testLogin {
				t.Fatal("request bytes, method or server identity changed")
			}
			if got.header.Get("Pc-Authorization") != upstreamSession || got.header.Get("X-EDU-Signature") != educoder.Signature(route.method, got.header.Get("X-EDU-Timestamp")) {
				t.Fatal("programming request did not use the existing signed session")
			}
			assertNoUpstreamSecrets(t, w)
			before = len(f.requests())
			if preflight := f.request(http.MethodOptions, route.path, ""); preflight.Code != http.StatusNoContent || len(f.requests()) != before {
				t.Fatal("preflight must stay local")
			}
		})
	}
}

func TestProgrammingBodyValidationAndExactMethods(t *testing.T) {
	f := newCompatFixture(t)
	_, session, _ := f.login(t)
	for _, body := range []string{"", "null", "[]", `{"content":`, "{} {}"} {
		before := len(f.requests())
		if w := f.request(http.MethodPost, "/api/myshixuns/MY1/update_file.json", body, session); w.Code != http.StatusBadRequest || len(f.requests()) != before {
			t.Fatalf("invalid JSON object reached upstream: status=%d", w.Code)
		}
	}
	before := len(f.requests())
	oversize := `{"content":"` + strings.Repeat("x", 2<<20) + `"}`
	if w := f.request(http.MethodPost, "/api/myshixuns/MY1/update_file.json", oversize, session); w.Code != http.StatusRequestEntityTooLarge || len(f.requests()) != before {
		t.Fatal("oversized body reached upstream")
	}
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/myshixuns/MY1/update_file.json"},
		{http.MethodGet, "/api/tasks/GAME1/game_build.json"},
		{http.MethodPost, "/api/tasks/GAME1/game_status.json"},
		{http.MethodPost, "/api/tasks/GAME1/log_output.json"},
		{http.MethodPost, "/api/myshixuns/bad.id/update_file.json"},
	} {
		if w := f.request(route.method, route.path, "{}", session); w.Code != http.StatusNotFound || len(f.requests()) != before {
			t.Fatalf("unobserved method/path reached upstream: %s %s", route.method, route.path)
		}
	}
}

func TestProgrammingEmptyAndNotModifiedResponses(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tasks/GAME1/game_status.json":
			w.WriteHeader(http.StatusNotModified)
		case "/api/myshixuns/MY1/update_file.json":
			w.WriteHeader(http.StatusOK)
		default:
			_, _ = io.WriteString(w, `{"status":-1}`)
		}
	}))
	defer upstream.Close()
	s := NewServer(Config{APIBaseURL: upstream.URL})
	client := educoder.New(upstream.URL)
	client.LoginName = testLogin
	id, _, err := s.store.create(client, testLogin)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodGet, "/api/tasks/GAME1/game_status.json", "", http.StatusNotModified},
		{http.MethodPost, "/api/myshixuns/MY1/update_file.json", "{}", http.StatusOK},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: id})
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != tc.status || w.Body.Len() != 0 {
			t.Fatalf("empty upstream response changed: status=%d bytes=%d", w.Code, w.Body.Len())
		}
	}
}
