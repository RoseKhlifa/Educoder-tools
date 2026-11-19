package taskservice

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/solver"

	"github.com/coder/websocket"
)

// upstreamCounter records every upstream request of one homework run so the
// per-level request budget is a tested property instead of an estimate.
type upstreamCounter struct {
	mu     sync.Mutex
	counts map[string]int
}

func (c *upstreamCounter) add(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts[key]++
}

func (c *upstreamCounter) snapshot() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]int, len(c.counts))
	for k, v := range c.counts {
		out[k] = v
	}
	return out
}

func (c *upstreamCounter) total() int {
	n := 0
	for _, v := range c.snapshot() {
		n += v
	}
	return n
}

func (c *upstreamCounter) String() string {
	counts := c.snapshot()
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, counts[k]))
	}
	return strings.Join(parts, " ")
}

func programmingBatchTask() string {
	return `{"st":0,"homework_common_id":44,"game":{"identifier":"G2","id":32,"myshixun_id":8,"status":0},"myshixun":{"id":8,"identifier":"RUNTIME"},"shixun":{"identifier":"SHIXUN","name":"experiment","challenges_count":2},"user":{"user_id":5},"challenge":{"id":11,"st":0,"with_code_file":true,"position":2,"subject":"second","task_pass":"print ok","path":"main.cpp"},"code_editor":{"shixun_environment_id":12,"name":"C/C++"},"shixun_environments":[{"shixun_environment_id":12,"tab_type":1}],"test_sets":[{"is_public":true,"input":"1","output":"ok"}]}`
}

func TestHomeworkUpstreamRequestBudget(t *testing.T) {
	counter := &upstreamCounter{counts: map[string]int{}}
	var mu sync.Mutex
	source := "// starter"
	formalPassed := false
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		counter.add(key)
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
				return
			}
			for _, frame := range []string{"ok\n", "ProcessFinished"} {
				_ = conn.Write(ctx, websocket.MessageText, []byte(base64.StdEncoding.EncodeToString([]byte(frame))))
			}
			_, _, _ = conn.Read(ctx)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/api/courses/C/homework_commons.json":
			fmt.Fprint(w, `{"homeworks":[{"homework_id":44,"name":"hw","is_shixun":true,"shixun_identifier":"SHIXUN","challenge_count":2}]}`)
		case "/api/shixun_homeworks/44/header_info.json":
			fmt.Fprint(w, `{"data":{"homework_id":44,"work_id":5,"myshixun_identifier":"RUNTIME","shixun_identifier":"SHIXUN","challenges":[{"id":10,"name":"first"},{"id":11,"name":"second"}]}}`)
		case "/api/homework_commons/44/shixun_challenge_data.json":
			second := "0"
			if formalPassed {
				second = "10"
			}
			fmt.Fprintf(w, `{"data":{"challenge_settings":[{"challenge_id":10,"challenge_name":"first","challenge_score":"10","game_score":"10"},{"challenge_id":11,"challenge_name":"second","challenge_score":"10","game_score":%q}]}}`, second)
		case "/api/myshixuns/RUNTIME/challenges.json":
			fmt.Fprint(w, `[{"identifier":"G1","position":1,"name":"first","status":2},{"identifier":"G2","position":2,"name":"second","status":2}]`)
		case "/api/tasks/G1.json":
			fmt.Fprint(w, batchChoiceJSON("G1", "first", 10, 8, 1))
		case "/api/tasks/G2.json":
			fmt.Fprint(w, programmingBatchTask())
		case "/api/tasks/G2/rep_content.json":
			json.NewEncoder(w).Encode(map[string]any{"filename": "main.cpp", "language": "cpp", "content": map[string]any{"content": base64.StdEncoding.EncodeToString([]byte(source))}})
		case "/api/myshixuns/RUNTIME/update_file.json":
			var body struct {
				Content string `json:"content"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			source = body.Content
			json.NewEncoder(w).Encode(map[string]any{"content": map[string]any{"commitID": "commit", "size": len(source)}, "sec_key": "fixture-only", "resubmit": ""})
		case "/api/tasks/G2/log_output":
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"wss_url": "ws" + strings.TrimPrefix(server.URL, "http") + "/runtime"}})
		case "/api/tasks/G2/game_build.json":
			fmt.Fprint(w, `{"status":1,"port":1}`)
		case "/api/tasks/G2/game_status.json":
			formalPassed = true
			fmt.Fprint(w, `{"status":2,"test_sets_count":1,"sets_error_count":0,"test_sets":[{"is_public":true,"input":"1","output":"ok","result":true,"compile_success":1}]}`)
		default:
			t.Errorf("unexpected route %s", key)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	provider := &fakeProvider{}
	s := New(bank(t), provider, Config{BudgetMicroUSD: 1000})
	r, err := s.RunHomework(context.Background(), educoder.New(server.URL), Request{Owner: "o", Course: "C", HomeworkID: 44, Mode: solver.ModeSubmit}, nil)
	if err != nil || r.Status != "passed" || r.CompletedLevels != 2 || r.SkippedLevels != 1 || provider.programmingCalls.Load() != 1 {
		t.Fatalf("batch=%+v err=%v requests=%s", r, err, counter)
	}
	t.Logf("upstream requests (%d): %s", counter.total(), counter)
	// Measured budget for one skipped level plus one model-generated level with
	// one public sample (v0.1.132 baseline: 31). Every remaining read is a
	// safety check: post-lock reload, first-write editor guard, formal
	// read-backs and the independent progress verifications.
	counts := counter.snapshot()
	for key, limit := range map[string]int{
		"GET /api/courses/C/homework_commons.json":                2, // was 4: per-level admission reuses the batch's homework
		"GET /api/homework_commons/44/shixun_challenge_data.json": 4, // was 5: no informational solver progress read
		"GET /api/tasks/G2.json":                                  5, // was 6: self-test reuses the fresh bound task
		"GET /api/tasks/G2/rep_content.json":                      6, // was 7: no guard read right after our own read-back
	} {
		if counts[key] > limit {
			t.Errorf("%s=%d exceeds budget %d (%s)", key, counts[key], limit, counter)
		}
	}
	if total := counter.total(); total > 26 {
		t.Errorf("homework used %d upstream requests, budget 26: %s", total, counter)
	}
}
