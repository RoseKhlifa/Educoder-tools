package taskservice

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/solver"

	"github.com/coder/websocket"
)

// bankHitHomework serves a two-level single-file programming assignment whose
// levels share one runtime, and records every upstream request in order. The
// first run generates and formally verifies both candidates, so a second run
// over the same question bank is the production "bank hit, self-test skipped"
// shape measured in docs/performance-capacity-plan.md.
type bankHitHomework struct {
	t      *testing.T
	levels int

	mu       sync.Mutex
	source   map[int]string
	passed   map[int]bool
	sequence []string
	server   *httptest.Server
}

func newBankHitHomework(t *testing.T, levels int) *bankHitHomework {
	h := &bankHitHomework{t: t, levels: levels, source: map[int]string{}, passed: map[int]bool{}}
	for p := 1; p <= levels; p++ {
		h.source[p] = "// starter"
	}
	h.server = httptest.NewServer(http.HandlerFunc(h.handle))
	t.Cleanup(h.server.Close)
	return h
}

// record appends one upstream call in arrival order. Requests of one homework
// are serial except for the preflight, so the order is the execution order.
func (h *bankHitHomework) record(label string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sequence = append(h.sequence, label)
}

func (h *bankHitHomework) take() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := append([]string(nil), h.sequence...)
	h.sequence = nil
	return out
}

func (h *bankHitHomework) reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sequence = nil
	for p := 1; p <= h.levels; p++ {
		h.passed[p] = false
	}
}

func bankHitTask(position int) string {
	return fmt.Sprintf(`{"st":0,"homework_common_id":44,"game":{"identifier":"G%d","id":%d,"myshixun_id":8,"status":0},"myshixun":{"id":8,"identifier":"RUNTIME"},"shixun":{"identifier":"SHIXUN","name":"experiment","challenges_count":%d},"user":{"user_id":5},"challenge":{"id":%d,"st":0,"with_code_file":true,"position":%d,"subject":%q,"task_pass":"print ok","path":"main.cpp"},"code_editor":{"shixun_environment_id":12,"name":"C/C++"},"shixun_environments":[{"shixun_environment_id":12,"tab_type":1}],"test_sets":[{"is_public":true,"input":"1","output":"ok"}]}`,
		position, 300+position, 9, preflightChallengeID(position), position, preflightTitle(position))
}

func (h *bankHitHomework) handle(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/runtime/log" {
		h.record("WS   /log (self-test stream)")
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			h.t.Error(err)
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
	position := 0
	if rest, ok := strings.CutPrefix(path, "/api/tasks/G"); ok {
		position, _ = strconv.Atoi(strings.SplitN(rest, "/", 2)[0][:1])
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case path == "/api/courses/C/homework_commons.json":
		h.sequence = append(h.sequence, "GET  homework_commons (assignment list)")
		fmt.Fprintf(w, `{"homeworks":[{"homework_id":44,"name":"hw","is_shixun":true,"shixun_identifier":"SHIXUN","challenge_count":%d}]}`, h.levels)
	case path == "/api/shixun_homeworks/44/header_info.json":
		h.sequence = append(h.sequence, "GET  header_info (runtime binding)")
		rows := make([]string, 0, h.levels)
		for p := 1; p <= h.levels; p++ {
			rows = append(rows, fmt.Sprintf(`{"id":%d,"name":%q}`, preflightChallengeID(p), preflightTitle(p)))
		}
		fmt.Fprintf(w, `{"data":{"homework_id":44,"work_id":5,"myshixun_identifier":"RUNTIME","shixun_identifier":"SHIXUN","challenges":[%s]}}`, strings.Join(rows, ","))
	case path == "/api/homework_commons/44/shixun_challenge_data.json":
		h.sequence = append(h.sequence, "GET  shixun_challenge_data (assignment progress)")
		rows := make([]string, 0, h.levels)
		for p := 1; p <= h.levels; p++ {
			score := "0"
			if h.passed[p] {
				score = "10"
			}
			rows = append(rows, fmt.Sprintf(`{"challenge_id":%d,"challenge_name":%q,"status":"required","challenge_score":"10","game_score":%q}`, preflightChallengeID(p), preflightTitle(p), score))
		}
		fmt.Fprintf(w, `{"data":{"challenge_settings":[%s]}}`, strings.Join(rows, ","))
	case path == "/api/myshixuns/RUNTIME/challenges.json":
		h.sequence = append(h.sequence, "GET  myshixun challenges (runtime roster)")
		rows := make([]string, 0, h.levels)
		for p := 1; p <= h.levels; p++ {
			rows = append(rows, fmt.Sprintf(`{"identifier":"G%d","position":%d,"name":%q,"status":2}`, p, p, preflightTitle(p)))
		}
		fmt.Fprintf(w, `[%s]`, strings.Join(rows, ","))
	case strings.HasSuffix(path, "/rep_content.json"):
		h.sequence = append(h.sequence, fmt.Sprintf("GET  L%d rep_content (editor file)", position))
		json.NewEncoder(w).Encode(map[string]any{"filename": "main.cpp", "language": "cpp", "content": map[string]any{"content": base64.StdEncoding.EncodeToString([]byte(h.source[position]))}})
	case path == "/api/myshixuns/RUNTIME/update_file.json":
		var body struct {
			Content  string `json:"content"`
			Evaluate int    `json:"evaluate"`
			Path     string `json:"path"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		target := 0
		for p := 1; p <= h.levels; p++ {
			if !h.passed[p] {
				target = p
				break
			}
		}
		h.source[target] = body.Content
		h.sequence = append(h.sequence, fmt.Sprintf("POST L%d update_file evaluate=%d (save)", target, body.Evaluate))
		json.NewEncoder(w).Encode(map[string]any{"content": map[string]any{"commitID": "commit", "size": len(body.Content)}, "sec_key": "fixture-only", "resubmit": ""})
	case strings.HasSuffix(path, "/log_output"):
		h.sequence = append(h.sequence, fmt.Sprintf("POST L%d log_output (self-test address)", position))
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"wss_url": "ws" + strings.TrimPrefix(h.server.URL, "http") + "/runtime"}})
	case strings.HasSuffix(path, "/game_build.json"):
		h.sequence = append(h.sequence, fmt.Sprintf("POST L%d game_build (start evaluation)", position))
		fmt.Fprint(w, `{"status":1,"port":1}`)
	case strings.HasSuffix(path, "/game_status.json"):
		h.sequence = append(h.sequence, fmt.Sprintf("GET  L%d game_status (evaluation verdict)", position))
		h.passed[position] = true
		fmt.Fprint(w, `{"status":2,"test_sets_count":1,"sets_error_count":0,"test_sets":[{"is_public":true,"input":"1","output":"ok","result":true,"compile_success":1}]}`)
	case strings.HasPrefix(path, "/api/tasks/G") && strings.HasSuffix(path, ".json"):
		h.sequence = append(h.sequence, fmt.Sprintf("GET  L%d task (task details)", position))
		fmt.Fprint(w, bankHitTask(position))
	default:
		h.t.Errorf("unexpected bank-hit route %s", path)
		http.NotFound(w, r)
	}
}

// TestBankHitLevelUpstreamSequence documents, in order, every upstream request
// a bank-hit programming level makes, and the requests that sit between two
// levels. It is the request-count half of the per-level timing measurement in
// docs/performance-capacity-plan.md; the budget keeps a later change from
// silently adding round trips to the hot path.
func TestBankHitLevelUpstreamSequence(t *testing.T) {
	h := newBankHitHomework(t, 2)
	store := bank(t)
	provider := &fakeProvider{}
	svc := New(store, provider, Config{BudgetMicroUSD: 1000})
	request := Request{Owner: "o", Course: "C", HomeworkID: 44, Mode: solver.ModeSubmit}
	first, err := svc.RunHomework(context.Background(), educoder.New(h.server.URL), request, nil)
	primed := h.take()
	if err != nil || first.Status != "passed" || first.CompletedLevels != 2 {
		t.Fatalf("priming run=%+v err=%v\n%s", first, err, strings.Join(primed, "\n"))
	}
	t.Logf("model-generated run (%d requests):\n  %s", len(primed), strings.Join(primed, "\n  "))
	// The scores drop back to zero, as if the levels were never submitted. The
	// bank now holds a formally verified candidate for each level's exact
	// revision and contract, so the second run is a bank hit on both.
	h.reset()
	provider.generate = func(context.Context) error { return fmt.Errorf("bank hit must not call the model") }
	second, err := svc.RunHomework(context.Background(), educoder.New(h.server.URL), Request{Owner: "o", Course: "C", HomeworkID: 44, Mode: solver.ModeSubmit, IdempotencyKey: "second"}, nil)
	sequence := h.take()
	if err != nil || second.Status != "passed" || second.CompletedLevels != 2 || second.ModelCalls != 0 {
		t.Fatalf("bank-hit run=%+v err=%v\n%s", second, err, strings.Join(sequence, "\n"))
	}
	t.Logf("bank-hit run (%d requests):\n  %s", len(sequence), strings.Join(sequence, "\n  "))
	for _, unwanted := range []string{"log_output", "self-test"} {
		for _, line := range sequence {
			if strings.Contains(line, unwanted) {
				t.Fatalf("verified candidate ran a public self-test: %q", line)
			}
		}
	}
	// Per-level budget measured at v0.1.149: one preflight task read plus the
	// 12 execution requests of a verified candidate — 5 task detail reads
	// (admission, two loads, submit), 5 editor file reads (two loads, the
	// first-write guard, the save read-back, the post-evaluation read-back),
	// one save, one game_build and one game_status. The homework also reads the
	// assignment progress five times (roster, preflight, after each level, and
	// the final reconciliation) and its assignment list once (two pages).
	perLevel := map[string]int{}
	for _, line := range sequence {
		if position := strings.Index(line, " L"); position >= 0 {
			perLevel[line[position+1:position+3]]++
		}
	}
	for level, count := range perLevel {
		if count > 13 {
			t.Errorf("%s used %d level-scoped requests, budget 13:\n  %s", level, count, strings.Join(sequence, "\n  "))
		}
	}
	if len(sequence) > 35 {
		t.Errorf("bank-hit homework used %d upstream requests, budget 35:\n  %s", len(sequence), strings.Join(sequence, "\n  "))
	}
}
