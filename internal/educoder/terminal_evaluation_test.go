package educoder

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func terminalEvaluationTask(t *testing.T) *Task {
	f := programmingTaskFixture("TERM1", 1)
	f["code_editor"] = map[string]any{}
	f["shixun_environments"] = []any{map[string]any{"shixun_environment_id": 12, "tab_type": 4}}
	f["challenge"].(map[string]any)["path"] = "case1/code.sh；"
	return decodeTaskFixture(t, f)
}

func terminalReceiptFixture(c *Client, task *Task) *TerminalResult {
	zero := 0
	return &TerminalResult{Completed: true, ExitCode: &zero, ScriptHash: strings.Repeat("a", 64), binding: terminalBinding{client: c, gameIdentifier: task.Game.Identifier, myshixunID: task.MyShixun.ID, gameID: task.Game.ID, challengeID: task.Challenge.ID, userID: task.User.UserID, environmentID: 12, scriptHash: strings.Repeat("a", 64)}, state: &terminalReceiptState{}}
}

func TestTerminalEvaluationPreservesPlaceholderAndRequiresFullScore(t *testing.T) {
	for _, scenario := range []string{"passed", "no_editor_commit", "not_full_score", "unknown_build"} {
		t.Run(scenario, func(t *testing.T) {
			task := terminalEvaluationTask(t)
			const placeholder = "Please use the terminal, not this placeholder.\n"
			writes, builds, polls := 0, 0, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/tasks/TERM1.json":
					copy := *task
					if polls > 0 {
						copy.Game.Status = 2
					}
					json.NewEncoder(w).Encode(copy)
				case "/api/tasks/TERM1/rep_content.json":
					if r.URL.Query().Get("path") != "case1/code.sh" {
						t.Error("unexpected placeholder path")
					}
					json.NewEncoder(w).Encode(map[string]any{"content": map[string]any{"content": base64.StdEncoding.EncodeToString([]byte(placeholder))}, "filename": "code.sh", "language": "shell"})
				case "/api/myshixuns/MY1/update_file.json":
					writes++
					var body map[string]any
					json.NewDecoder(r.Body).Decode(&body)
					if body["content"] != placeholder || body["evaluate"] != float64(1) || body["tab_type"] != float64(4) {
						t.Errorf("terminal evaluation replaced placeholder or used wrong environment: %v", body)
					}
					if scenario == "no_editor_commit" {
						// Real terminal saves may omit content entirely, including its
						// editor commit, while still returning the evaluation key.
						json.NewEncoder(w).Encode(map[string]any{"status": 0, "sec_key": "ephemeral-test-key"})
						return
					}
					json.NewEncoder(w).Encode(map[string]any{"status": 0, "content": map[string]any{"commitID": "fixture-commit"}, "sec_key": "ephemeral-test-key"})
				case "/api/tasks/TERM1/game_build.json":
					builds++
					var body map[string]any
					json.NewDecoder(r.Body).Decode(&body)
					commit := "fixture-commit"
					if scenario == "no_editor_commit" {
						commit = ""
					}
					if body["tab_type"] != float64(4) || body["shixun_environment_id"] != float64(12) || body["extras"].(map[string]any)["commitID"] != commit {
						t.Error("wrong terminal build context")
					}
					if scenario == "unknown_build" {
						http.Error(w, "unconfirmed", 502)
						return
					}
					json.NewEncoder(w).Encode(map[string]any{"status": 1, "port": 1000})
				case "/api/tasks/TERM1/game_status.json":
					polls++
					json.NewEncoder(w).Encode(map[string]any{"status": 2, "test_sets_count": 1, "sets_error_count": 0, "test_sets": []any{map[string]any{"result": true, "compile_success": 1}}})
				case "/api/homework_commons/99/shixun_challenge_data.json":
					score := "25.0"
					if scenario == "not_full_score" {
						score = "0.0"
					}
					json.NewEncoder(w).Encode(map[string]any{"status": 0, "data": map[string]any{"challenge_settings": []any{map[string]any{"challenge_id": task.Challenge.ID, "status": "required", "passed_status": 1, "challenge_score": "25.0", "game_score": score}}}})
				default:
					t.Errorf("unexpected terminal request %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			c := New(srv.URL)
			receipt := terminalReceiptFixture(c, task)
			r, err := c.SubmitTerminal(context.Background(), task, receipt, PollOptions{Timeout: time.Second, Interval: time.Millisecond})
			if scenario == "passed" || scenario == "no_editor_commit" {
				if err != nil || r == nil || !r.Completed || !r.Passed {
					t.Fatalf("formal terminal result not confirmed: %+v %v", r, err)
				}
			} else if err == nil || r != nil && r.Passed {
				t.Fatalf("uncertain terminal evaluation claimed success: %+v %v", r, err)
			}
			if writes != 1 || builds != 1 {
				t.Fatalf("writes=%d builds=%d", writes, builds)
			}
			if _, err := c.SubmitTerminal(context.Background(), task, receipt, PollOptions{}); err == nil {
				t.Fatal("receipt reused")
			}
			if writes != 1 || builds != 1 {
				t.Fatal("uncertain or completed evaluation replayed")
			}
		})
	}
}

func TestTerminalEvaluationRejectsUnboundOrChangedReceipt(t *testing.T) {
	for _, scenario := range []string{"fabricated", "hash_changed", "client_changed", "game_changed", "not_completed", "failed_command", "missing_state", "changed_observed_exit"} {
		t.Run(scenario, func(t *testing.T) {
			task := terminalEvaluationTask(t)
			c := New("https://fixture.invalid")
			r := terminalReceiptFixture(c, task)
			switch scenario {
			case "fabricated":
				r.binding = terminalBinding{}
			case "hash_changed":
				r.ScriptHash = strings.Repeat("b", 64)
			case "client_changed":
				r.binding.client = New(c.BaseURL)
			case "game_changed":
				task.Game.ID++
			case "not_completed":
				r.Completed = false
			case "failed_command":
				code := 1
				r.ExitCode = &code
			case "missing_state":
				r.state = nil
			case "changed_observed_exit":
				// Changing the public exit code to zero cannot turn a command
				// that actually exited unsuccessfully into a submission receipt.
				r.state.exitCode = 7
			}
			if _, err := c.SubmitTerminal(context.Background(), task, r, PollOptions{}); err == nil {
				t.Fatal("unbound receipt accepted")
			}
		})
	}
}

func TestTerminalEvaluationCopiedReceiptSharesSingleUseState(t *testing.T) {
	task := terminalEvaluationTask(t)
	var writes, builds atomic.Int32
	const placeholder = "Use terminal only.\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tasks/TERM1.json":
			current := *task
			if builds.Load() > 0 {
				current.Game.Status = 2
			}
			json.NewEncoder(w).Encode(current)
		case "/api/tasks/TERM1/rep_content.json":
			json.NewEncoder(w).Encode(map[string]any{"content": map[string]any{"content": base64.StdEncoding.EncodeToString([]byte(placeholder))}, "filename": "code.sh"})
		case "/api/myshixuns/MY1/update_file.json":
			writes.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"status": 0, "sec_key": "ephemeral-test-key"})
		case "/api/tasks/TERM1/game_build.json":
			builds.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"status": 1, "port": 1000})
		case "/api/tasks/TERM1/game_status.json":
			json.NewEncoder(w).Encode(map[string]any{"status": 2, "test_sets_count": 1, "sets_error_count": 0, "test_sets": []any{map[string]any{"result": true, "compile_success": 1}}})
		case "/api/homework_commons/99/shixun_challenge_data.json":
			json.NewEncoder(w).Encode(map[string]any{"status": 0, "data": map[string]any{"challenge_settings": []any{map[string]any{"challenge_id": task.Challenge.ID, "status": "required", "passed_status": 1, "challenge_score": "25.0", "game_score": "25.0"}}}})
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := New(server.URL)
	receipt := terminalReceiptFixture(client, task)
	copyOne, copyTwo := *receipt, *receipt
	gate := make(chan struct{})
	results := make(chan error, 3)
	for _, current := range []*TerminalResult{receipt, &copyOne, &copyTwo} {
		go func(current *TerminalResult) {
			<-gate
			_, err := client.SubmitTerminal(context.Background(), task, current, PollOptions{Timeout: time.Second, Interval: time.Millisecond})
			results <- err
		}(current)
	}
	close(gate)
	passed := 0
	for range 3 {
		if <-results == nil {
			passed++
		}
	}
	if passed != 1 || writes.Load() != 1 || builds.Load() != 1 {
		t.Fatalf("copied receipt replay: successful=%d saves=%d builds=%d", passed, writes.Load(), builds.Load())
	}
}

func TestTerminalSaveCompatibilityDoesNotRelaxEditorCommitRequirement(t *testing.T) {
	var writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writes.Add(1)
		if !strings.HasSuffix(r.URL.Path, "/update_file.json") {
			t.Errorf("unexpected editor request %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"status": 0, "sec_key": "ephemeral-test-key"})
	}))
	defer server.Close()
	task := decodeTaskFixture(t, programmingTaskFixture("GAME1", 1))
	saved, err := New(server.URL).SaveCode(context.Background(), task, task.CodePaths()[0], "int main(){}", true)
	if err == nil || saved != nil || writes.Load() != 1 {
		t.Fatalf("editor accepted save without commit: saved=%+v err=%v writes=%d", saved, err, writes.Load())
	}
}
