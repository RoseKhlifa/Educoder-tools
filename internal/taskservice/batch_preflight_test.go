package taskservice

import (
	"context"
	"errors"
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
)

// preflightHomework serves one homework whose levels all already hold a full
// score, so a run exercises the preflight and nothing else. Each level's task
// read can be delayed or made to fail, and the fixture records how many reads
// happened, in which order they completed, and how many overlapped.
type preflightHomework struct {
	t      *testing.T
	levels int
	// delay, body and status are keyed by level position.
	delay    map[int]time.Duration
	body     map[int]string
	status   map[int]int
	optional map[int]bool
	// onRead runs on the server goroutine before a task read answers.
	onRead func(position int)

	mu             sync.Mutex
	reads          int
	completed      []int
	inflight, peak int
}

func newPreflightHomework(t *testing.T, levels int) *preflightHomework {
	return &preflightHomework{t: t, levels: levels, delay: map[int]time.Duration{}, body: map[int]string{}, status: map[int]int{}, optional: map[int]bool{}}
}

func (h *preflightHomework) stats() (reads int, completed []int, peak int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.reads, append([]int(nil), h.completed...), h.peak
}

func preflightChallengeID(position int) int64 { return int64(100 + position) }
func preflightTitle(position int) string      { return fmt.Sprintf("level%d", position) }

func (h *preflightHomework) server() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		switch {
		case path == "/api/courses/C/homework_commons.json":
			fmt.Fprintf(w, `{"homeworks":[{"homework_id":44,"name":"hw","is_shixun":true,"shixun_identifier":"SHIXUN","challenge_count":%d}]}`, h.levels)
		case path == "/api/shixun_homeworks/44/header_info.json":
			rows := make([]string, 0, h.levels)
			for p := 1; p <= h.levels; p++ {
				rows = append(rows, fmt.Sprintf(`{"id":%d,"name":%q}`, preflightChallengeID(p), preflightTitle(p)))
			}
			fmt.Fprintf(w, `{"data":{"homework_id":44,"work_id":5,"myshixun_identifier":"RUNTIME","shixun_identifier":"SHIXUN","challenges":[%s]}}`, strings.Join(rows, ","))
		case path == "/api/homework_commons/44/shixun_challenge_data.json":
			rows := make([]string, 0, h.levels)
			for p := 1; p <= h.levels; p++ {
				required := "required"
				if h.optional[p] {
					required = "optional"
				}
				rows = append(rows, fmt.Sprintf(`{"challenge_id":%d,"challenge_name":%q,"status":%q,"challenge_score":"10","game_score":"10"}`, preflightChallengeID(p), preflightTitle(p), required))
			}
			fmt.Fprintf(w, `{"data":{"challenge_settings":[%s]}}`, strings.Join(rows, ","))
		case path == "/api/myshixuns/RUNTIME/challenges.json":
			rows := make([]string, 0, h.levels)
			for p := 1; p <= h.levels; p++ {
				rows = append(rows, fmt.Sprintf(`{"identifier":"G%d","position":%d,"name":%q,"status":2}`, p, p, preflightTitle(p)))
			}
			fmt.Fprintf(w, `[%s]`, strings.Join(rows, ","))
		case strings.HasPrefix(path, "/api/tasks/G") && strings.HasSuffix(path, ".json"):
			position, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(path, "/api/tasks/G"), ".json"))
			h.mu.Lock()
			h.reads++
			h.inflight++
			h.peak = max(h.peak, h.inflight)
			h.mu.Unlock()
			if h.onRead != nil {
				h.onRead(position)
			}
			if d := h.delay[position]; d > 0 {
				time.Sleep(d)
			}
			h.mu.Lock()
			h.inflight--
			h.completed = append(h.completed, position)
			h.mu.Unlock()
			if code := h.status[position]; code != 0 {
				http.Error(w, "task unavailable", code)
				return
			}
			if body := h.body[position]; body != "" {
				fmt.Fprint(w, body)
				return
			}
			fmt.Fprint(w, batchChoiceJSON(fmt.Sprintf("G%d", position), preflightTitle(position), preflightChallengeID(position), 8, int64(position)))
		default:
			h.t.Errorf("unexpected preflight route %s", path)
			http.NotFound(w, r)
		}
	}))
}

func (h *preflightHomework) run(ctx context.Context) (*BatchResult, error) {
	server := h.server()
	h.t.Cleanup(server.Close)
	return New(bank(h.t), &fakeProvider{}, Config{}).RunHomework(ctx, educoder.New(server.URL), Request{Owner: "o", Course: "C", HomeworkID: 44, Mode: solver.ModeSubmit}, nil)
}

// The preflight reads levels concurrently but must decide in roster order: a
// late failure that completes first may not take the earlier level's place in
// the diagnostics, and the reported reason stays the last failing level's, as
// it was when every level was read one after another.
func TestPreflightKeepsRosterOrderWhenReadsCompleteOutOfOrder(t *testing.T) {
	h := newPreflightHomework(t, 6)
	// Level 2 fails on identity and answers last; level 4 fails to read and
	// answers first. Completion order is therefore the reverse of roster order.
	h.body[2] = batchChoiceJSON("G2", preflightTitle(2), preflightChallengeID(5), 8, 2)
	h.delay[2] = 400 * time.Millisecond
	h.status[4] = http.StatusInternalServerError
	got, err := h.run(context.Background())
	if err != nil {
		t.Fatalf("preflight err=%v batch=%+v", err, got)
	}
	if got.Status != "unsupported_roster" || got.ReasonCode != "task_precheck_failed" {
		t.Fatalf("status=%q reason=%q, want the last failing level in roster order", got.Status, got.ReasonCode)
	}
	if len(got.Diagnostics) != 2 || !strings.HasPrefix(got.Diagnostics[0], "第 2 关预检未通过") || !strings.HasPrefix(got.Diagnostics[1], "第 4 关预检未通过") {
		t.Fatalf("diagnostics=%q, want roster order 2 then 4", got.Diagnostics)
	}
	if got.TotalLevels != 6 || got.FailedLevels != 2 || got.CompletedLevels != 4 || got.SkippedLevels != 4 {
		t.Fatalf("counters=%+v", got)
	}
	for i, level := range got.Levels {
		if level.Position != i+1 || level.ChallengeID != preflightChallengeID(i+1) {
			t.Fatalf("levels out of roster order: %+v", got.Levels)
		}
	}
	if got.Levels[1].Status != "precheck_failed" || got.Levels[1].ReasonCode != "task_identity_mismatch" {
		t.Fatalf("level 2 = %+v", got.Levels[1])
	}
	if got.Levels[3].Status != "precheck_failed" || got.Levels[3].ReasonCode != "task_precheck_failed" {
		t.Fatalf("level 4 = %+v", got.Levels[3])
	}
	reads, completed, _ := h.stats()
	if reads != 6 {
		t.Fatalf("preflight issued %d task reads for 6 levels", reads)
	}
	if len(completed) == 0 || completed[len(completed)-1] != 2 {
		t.Fatalf("fixture did not reorder completions: %v", completed)
	}
}

// Each precheck outcome keeps the exact status, reason code and plan membership
// it had when the reads were serial.
func TestPreflightFailureDecisionsAreUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name               string
		prepare            func(*preflightHomework)
		wantStatus, wantID string
		wantBatchReason    string
		wantLevels         int
	}{
		{
			name:            "read error",
			prepare:         func(h *preflightHomework) { h.status[2] = http.StatusBadGateway },
			wantStatus:      "precheck_failed",
			wantID:          "task_precheck_failed",
			wantBatchReason: "task_precheck_failed",
			wantLevels:      3,
		},
		{
			name: "identity mismatch",
			prepare: func(h *preflightHomework) {
				h.body[2] = batchChoiceJSON("G2", preflightTitle(2), preflightChallengeID(2), 0, 2)
			},
			wantStatus:      "precheck_failed",
			wantID:          "task_identity_mismatch",
			wantBatchReason: "task_identity_mismatch",
			wantLevels:      3,
		},
		{
			name: "unsupported question kind",
			prepare: func(h *preflightHomework) {
				h.body[2] = fmt.Sprintf(`{"st":1,"homework_common_id":44,"game":{"identifier":"G2","id":102,"myshixun_id":8},"myshixun":{"id":8,"identifier":"RUNTIME"},"shixun":{"identifier":"SHIXUN","challenges_count":3},"challenge":{"id":%d,"st":1,"position":2,"subject":%q,"task_pass":"Choose"}}`, preflightChallengeID(2), preflightTitle(2))
			},
			wantStatus:      "precheck_failed",
			wantID:          "unsupported_question_kind",
			wantBatchReason: "unsupported_question_kind",
			wantLevels:      3,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newPreflightHomework(t, 3)
			tc.prepare(h)
			got, err := h.run(context.Background())
			if err != nil {
				t.Fatalf("err=%v batch=%+v", err, got)
			}
			if got.Status != "unsupported_roster" || got.ReasonCode != tc.wantBatchReason {
				t.Fatalf("status=%q reason=%q", got.Status, got.ReasonCode)
			}
			if got.TotalLevels != tc.wantLevels || len(got.Levels) != tc.wantLevels {
				t.Fatalf("levels=%+v", got.Levels)
			}
			if got.Levels[1].Status != tc.wantStatus || got.Levels[1].ReasonCode != tc.wantID {
				t.Fatalf("failing level=%+v", got.Levels[1])
			}
			if len(got.Diagnostics) != 1 || !strings.HasPrefix(got.Diagnostics[0], "第 2 关预检未通过") {
				t.Fatalf("diagnostics=%q", got.Diagnostics)
			}
			if reads, _, _ := h.stats(); reads != tc.wantLevels {
				t.Fatalf("preflight issued %d task reads for %d levels", reads, tc.wantLevels)
			}
		})
	}
}

// A level the assignment marks elective is never read at all: it is filtered
// out before any task detail read is scheduled.
func TestPreflightDoesNotReadOptionalLevels(t *testing.T) {
	h := newPreflightHomework(t, 3)
	h.optional = map[int]bool{2: true}
	got, err := h.run(context.Background())
	if err != nil {
		t.Fatalf("err=%v batch=%+v", err, got)
	}
	if got.Status != "passed" || got.TotalLevels != 2 || len(got.Levels) != 2 || got.Levels[0].Position != 1 || got.Levels[1].Position != 3 {
		t.Fatalf("optional level stayed in the plan: %+v", got)
	}
	if reads, _, _ := h.stats(); reads != 2 {
		t.Fatalf("preflight issued %d task reads, want 2 required levels only", reads)
	}
}

// Cancelling the context stops scheduling: the levels left unread report the
// cancellation without issuing a request, and the batch reports canceled at the
// first of them in roster order.
func TestPreflightCancellationStopsScheduling(t *testing.T) {
	h := newPreflightHomework(t, 12)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var once sync.Once
	h.onRead = func(int) { once.Do(cancel) }
	for p := 1; p <= 12; p++ {
		h.delay[p] = 50 * time.Millisecond
	}
	got, err := h.run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v batch=%+v", err, got)
	}
	if got == nil || got.Status != "canceled" || got.ReasonCode != "canceled" {
		t.Fatalf("batch=%+v", got)
	}
	reads, _, _ := h.stats()
	if reads > preflightTaskConcurrency {
		t.Fatalf("preflight kept scheduling after cancellation: %d reads", reads)
	}
}

func preflightLevels(n int) []educoder.HomeworkLevel {
	levels := make([]educoder.HomeworkLevel, 0, n)
	for p := 1; p <= n; p++ {
		levels = append(levels, educoder.HomeworkLevel{ChallengeID: preflightChallengeID(p), Game: fmt.Sprintf("G%d", p), Title: preflightTitle(p), Position: p})
	}
	return levels
}

// A context already canceled before the preflight issues no request at all and
// reports the cancellation for every level that would have been read.
func TestPreflightTaskReadsSkipEveryReadOnCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	levels := preflightLevels(4)
	levels[1].RuntimePending = true
	levels[2].RosterIssue = "ambiguous"
	var calls int
	reads := preflightTaskReads(ctx, func(context.Context, string) (*educoder.Task, error) {
		calls++
		return nil, nil
	}, levels)
	if len(reads) != len(levels) {
		t.Fatalf("reads=%d levels=%d", len(reads), len(levels))
	}
	if calls != 0 {
		t.Fatalf("canceled preflight issued %d reads", calls)
	}
	for i, want := range []bool{true, false, false, true} {
		if got := reads[i].err != nil; got != want {
			t.Fatalf("level %d error=%v, want error=%t", i+1, reads[i].err, want)
		}
		if want && !errors.Is(reads[i].err, context.Canceled) {
			t.Fatalf("level %d err=%v, want context cancellation", i+1, reads[i].err)
		}
		if reads[i].read {
			t.Fatalf("level %d was read despite cancellation", i+1)
		}
	}
}

// The reads overlap, but never more than the bound.
func TestPreflightReadConcurrencyStaysWithinBound(t *testing.T) {
	h := newPreflightHomework(t, 9)
	for p := 1; p <= 9; p++ {
		h.delay[p] = 60 * time.Millisecond
	}
	got, err := h.run(context.Background())
	if err != nil || got.Status != "passed" {
		t.Fatalf("batch=%+v err=%v", got, err)
	}
	reads, _, peak := h.stats()
	if reads != 9 {
		t.Fatalf("preflight issued %d task reads for 9 levels", reads)
	}
	if peak < 2 {
		t.Fatalf("preflight reads did not overlap (peak=%d)", peak)
	}
	if peak > preflightTaskConcurrency {
		t.Fatalf("preflight ran %d reads at once, bound is %d", peak, preflightTaskConcurrency)
	}
}

// A panic while reading one level must behave as it did when the preflight read
// every level itself: it unwinds the calling goroutine, where the job manager's
// recover fails the job, instead of ending the service. In-flight reads still
// finish first, and no further level is scheduled.
func TestPreflightTaskReadPanicIsReraisedOnTheCaller(t *testing.T) {
	var mu sync.Mutex
	started := 0
	read := func(_ context.Context, game string) (*educoder.Task, error) {
		mu.Lock()
		started++
		mu.Unlock()
		if game == "G2" {
			panic("preflight read fault")
		}
		time.Sleep(30 * time.Millisecond)
		return nil, errors.New("unused")
	}
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		preflightTaskReads(context.Background(), read, preflightLevels(12))
	}()
	select {
	case got := <-done:
		if got != "preflight read fault" {
			t.Fatalf("recovered %v, want the read panic re-raised on the caller", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("preflight hung after a read panicked")
	}
	mu.Lock()
	defer mu.Unlock()
	if started > preflightTaskConcurrency {
		t.Fatalf("preflight kept scheduling after a panic: %d reads started", started)
	}
}
