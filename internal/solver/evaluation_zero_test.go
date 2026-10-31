package solver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/educoder"
)

func TestHiddenEvaluationZeroStatusRepairsWithoutReplayingCandidate(t *testing.T) {
	var mu sync.Mutex
	current := "template"
	var saves []string
	builds, polls, selfTests := 0, 0, 0
	fixture := adapterFixture(12)
	fixture["game"].(map[string]any)["status"] = 0
	fixture["test_sets"] = []any{map[string]any{"is_public": false, "input": "", "output": "", "result": false, "compile_success": 1}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tasks/g.json":
			json.NewEncoder(w).Encode(fixture)
		case "/api/tasks/g/rep_content.json":
			json.NewEncoder(w).Encode(map[string]any{"filename": "main.cpp", "language": "cpp", "content": map[string]any{"content": base64.StdEncoding.EncodeToString([]byte(current))}})
		case "/api/myshixuns/instance/update_file.json":
			var body struct {
				Path     string `json:"path"`
				Content  string `json:"content"`
				Evaluate int    `json:"evaluate"`
			}
			if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&body) != nil || body.Path != "main.cpp" || body.Evaluate != 1 {
				t.Errorf("unexpected save request: method=%s body=%+v", r.Method, body)
			}
			current = body.Content
			saves = append(saves, current)
			json.NewEncoder(w).Encode(map[string]any{"content": map[string]any{"commitID": fmt.Sprintf("commit-%d", len(saves)), "size": len(current)}, "sec_key": "fixture-key", "resubmit": "1"})
		case "/api/tasks/g/game_build.json":
			builds++
			var body struct {
				Extras struct {
					CommitID string `json:"commitID"`
				} `json:"extras"`
			}
			if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&body) != nil || builds != len(saves) || body.Extras.CommitID != fmt.Sprintf("commit-%d", builds) {
				t.Errorf("build was not bound once to candidate save: builds=%d saves=%d commit=%q", builds, len(saves), body.Extras.CommitID)
			}
			json.NewEncoder(w).Encode(map[string]any{"status": 1, "port": 1})
		case "/api/tasks/g/game_status.json":
			polls++
			passed, status, errors := builds == 2, 0, 1
			if passed {
				status, errors = 2, 0
			}
			json.NewEncoder(w).Encode(map[string]any{"status": status, "test_sets_count": 1, "sets_error_count": errors, "test_sets": []any{map[string]any{"is_public": false, "input": "", "output": "", "result": passed, "compile_success": 1}}})
		case "/api/tasks/g/log_output":
			selfTests++
			t.Error("hidden test was incorrectly used as a public self-test")
			http.Error(w, "no public tests", http.StatusBadRequest)
		case "/api/homework_commons/1/shixun_challenge_data.json":
			fmt.Fprint(w, `{"status":0,"data":{"passed_count":1,"challenge_settings":[{"challenge_id":6,"passed_status":2}]}}`)
		default:
			t.Errorf("unexpected endpoint: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	store := &fakeStore{}
	provider := &scriptedProvider{estimateCost: 1, generate: func(_ context.Context, req answer.Request, n int) (answer.Result, error) {
		if len(req.Problem.PublicTests) != 0 {
			t.Fatalf("hidden tests were presented as public examples: %+v", req.Problem.PublicTests)
		}
		if n == 1 {
			return solved("bad", 1), nil
		}
		if n != 2 || req.Previous == nil || req.Previous.Files[0].Content != "bad" || len(req.Feedback) != 1 || req.Feedback[0].Kind != "formal" || !strings.Contains(req.Feedback[0].Message, "1 failing test sets") {
			t.Fatalf("completed status=0 evaluation did not become repair feedback: call=%d previous=%+v feedback=%+v", n, req.Previous, req.Feedback)
		}
		return solved("good", 1), nil
	}}
	service := New(store, provider, Config{Timeout: 3 * time.Second, MaxModelCalls: 2, MaxEvaluations: 2})
	result, err := service.RunClient(context.Background(), educoder.New(server.URL), Request{Owner: "fixture-account", Course: "c", HomeworkID: 1, Game: "g", Mode: ModeSubmit})
	if err != nil || result == nil || result.Status != "passed" || result.ModelCalls != 2 || result.SelfTestStatus != "skipped" {
		t.Fatalf("status=0 must trigger code repair, not submission uncertainty: result=%+v err=%v", result, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(saves) != 2 || saves[0] != "bad" || saves[1] != "good" || builds != 2 || polls != 2 || selfTests != 0 {
		t.Fatalf("expected one formal save/build per distinct candidate: saves=%q builds=%d polls=%d selfTests=%d", saves, builds, polls, selfTests)
	}
	if len(store.validations) != 2 || store.validations[0].Kind != "formal" || store.validations[0].Result != "failed" || !store.validations[0].Terminal || store.validations[1].Result != "passed" || !store.validations[1].Terminal {
		t.Fatalf("expected complete failed/passed formal evidence: %+v", store.validations)
	}
	if result.Progress == nil || result.Progress.PassedCount != 1 || result.Evaluation == nil || !result.Evaluation.Completed || !result.Evaluation.Passed {
		t.Fatalf("final evaluation and progress were not confirmed: %+v", result)
	}
}
