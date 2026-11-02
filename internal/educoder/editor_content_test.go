package educoder

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadCodeFileRequiresExplicitContentField(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		wantError      bool
		wantContent    string
	}{
		{"missing envelope", `{}`, true, ""},
		{"null envelope", `{"content":null}`, true, ""},
		{"missing nested field", `{"content":{}}`, true, ""},
		{"null nested field", `{"content":{"content":null}}`, true, ""},
		{"invalid base64", `{"content":{"content":"not:base64"}}`, true, ""},
		{"invalid UTF-8", `{"content":{"content":"/w=="}}`, true, ""},
		{"explicit empty file", `{"content":{"content":""}}`, false, ""},
		{"source preserves CRLF", `{"content":{"content":"b25lDQp0d28NCg=="}}`, false, "one\r\ntwo\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method != http.MethodGet || r.URL.Path != "/api/tasks/GAME1/rep_content.json" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				w.Write([]byte(tc.response))
			}))
			defer server.Close()
			client := New(server.URL)
			task := decodeTaskFixture(t, programmingTaskFixture("GAME1", 1))
			file, err := client.ReadCodeFile(context.Background(), task, "src/第1关.cpp")
			if tc.wantError {
				if err == nil || file != nil {
					t.Fatalf("unconfirmed content accepted: file=%+v err=%v", file, err)
				}
				if strings.Contains(err.Error(), "not:base64") {
					t.Fatal("untrusted content leaked into diagnostic")
				}
			} else if err != nil || file == nil || file.Content != tc.wantContent {
				t.Fatalf("explicit content lost: file=%+v err=%v", file, err)
			}
		})
	}
}
