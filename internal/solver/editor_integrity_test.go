package solver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/problem"

	"github.com/coder/websocket"
)

func TestEditorMismatchDiagnosticDoesNotExposeSource(t *testing.T) {
	for _, tc := range []struct {
		name, candidate, observed string
		conversionOnly            bool
	}{
		{"CRLF converted to LF", "secret-command\r\nnext\r\n", "secret-command\nnext\n", true},
		{"LF converted to CRLF", "secret-command\nnext\n", "secret-command\r\nnext\r\n", true},
		{"source edited", "secret-command\nnext\n", "different-command\nnext\n", false},
		{"trailing newline removed", "secret-command\n", "secret-command", false},
		{"bare carriage return", "secret-command\rnext", "secret-command\nnext", false},
		{"whitespace edited", "secret-command a\n", "secret-command  a\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/tasks/g.json" {
					json.NewEncoder(w).Encode(adapterFixture(12))
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"content": map[string]any{"content": base64.StdEncoding.EncodeToString([]byte(tc.observed))}})
			}))
			defer server.Close()
			a, _ := NewEducoderAdapter(educoder.New(server.URL))
			task, err := a.task(context.Background(), Target{"c", 1, "g"})
			if err != nil {
				t.Fatal(err)
			}
			hash, err := a.confirmEditor(context.Background(), task, problem.SolutionFile{Path: "main.cpp", Content: tc.candidate})
			if err == nil || hash != "" {
				t.Fatalf("mismatched source confirmed: hash=%s err=%v", hash, err)
			}
			message := err.Error()
			wantConversion := "crlf_conversion_only=false"
			if tc.conversionOnly {
				wantConversion = "crlf_conversion_only=true"
			}
			for _, want := range []string{wantConversion, "candidate_sha256=" + problem.HashContent(tc.candidate), "observed_sha256=" + problem.HashContent(tc.observed)} {
				if !strings.Contains(message, want) {
					t.Fatalf("diagnostic %q lacks %q", message, want)
				}
			}
			if strings.Contains(message, "secret-command") || strings.Contains(message, "different-command") || strings.ContainsAny(message, "\r\n") {
				t.Fatalf("source leaked into mismatch diagnostic: %q", message)
			}
		})
	}
}

// A completed, matching runtime stream is still useful evidence when editor
// confirmation fails. It must not acquire a false candidate-bound source hash,
// and CRLF equivalence must not hide real byte differences in that evidence.
func TestSelfTestEditorConfirmationKeepsRuntimeEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, candidate, saved string
		confirmed              bool
		conversionOnly         bool
	}{
		{"exact LF", "command\nnext\n", "command\nnext\n", true, false},
		{"exact CRLF", "command\r\nnext\r\n", "command\r\nnext\r\n", true, false},
		{"CRLF normalized by server", "command\r\nnext\r\n", "command\nnext\n", false, true},
		{"another source saved", "command\nnext\n", "changed\nnext\n", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			current := "template"
			builds := 0
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/runtime/log" {
					conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
					if err != nil {
						t.Error(err)
						return
					}
					defer conn.CloseNow()
					ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
					defer cancel()
					if _, _, err := conn.Read(ctx); err != nil {
						t.Error(err)
						return
					}
					for _, frame := range []string{"ok\n", "ProcessFinished"} {
						if err := conn.Write(ctx, websocket.MessageText, []byte(base64.StdEncoding.EncodeToString([]byte(frame)))); err != nil {
							t.Error(err)
							return
						}
					}
					// Let the caller acknowledge the completion control frame.
					_, _, _ = conn.Read(ctx)
					return
				}
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/tasks/g.json":
					json.NewEncoder(w).Encode(adapterFixture(12))
				case "/api/tasks/g/rep_content.json":
					json.NewEncoder(w).Encode(map[string]any{"filename": "main.cpp", "language": "cpp", "content": map[string]any{"content": base64.StdEncoding.EncodeToString([]byte(current))}})
				case "/api/myshixuns/instance/update_file.json":
					var body struct {
						Content string `json:"content"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Content != tc.candidate {
						t.Errorf("candidate changed before save: err=%v", err)
					}
					current = tc.saved
					json.NewEncoder(w).Encode(map[string]any{"content": map[string]any{"commitID": "fixture", "size": len(current)}})
				case "/api/tasks/g/log_output":
					json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"wss_url": strings.Replace(server.URL, "http://", "ws://", 1) + "/runtime"}})
				case "/api/tasks/g/game_build.json":
					builds++
					http.Error(w, "unexpected submission", http.StatusConflict)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			a, _ := NewEducoderAdapter(educoder.New(server.URL))
			ctx := context.Background()
			loaded, err := a.Load(ctx, Target{"c", 1, "g"})
			if err != nil {
				t.Fatal(err)
			}
			a.bindSnapshot(loaded)
			file := problem.SolutionFile{Path: "main.cpp", Content: tc.candidate}
			result, err := a.SelfTest(ctx, Target{"c", 1, "g"}, file, "input", "ok\n")
			if !result.Completed || !result.Matches || result.Output != "ok\n" {
				t.Fatalf("completed matching stream lost: %+v err=%v", result, err)
			}
			if tc.confirmed {
				wantHash, _ := problem.HashFiles([]problem.SolutionFile{file})
				if err != nil || result.SavedFilesHash != wantHash {
					t.Fatalf("exact source not confirmed: %+v err=%v", result, err)
				}
				if a.expected["g"][file.Path] != problem.HashContent(tc.saved) {
					t.Fatal("next-write baseline was not refreshed with confirmed source")
				}
			} else {
				if err == nil || result.SavedFilesHash != "" {
					t.Fatalf("mismatched source received candidate evidence: %+v err=%v", result, err)
				}
				if tc.conversionOnly && !strings.Contains(err.Error(), "crlf_conversion_only=true") {
					t.Fatalf("CRLF conversion not diagnosed: %v", err)
				}
				if a.expected["g"][file.Path] != problem.HashContent("template") {
					t.Fatal("unconfirmed source replaced the baseline")
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if builds != 0 {
				t.Fatal("selftest submitted an evaluation")
			}
		})
	}
}
