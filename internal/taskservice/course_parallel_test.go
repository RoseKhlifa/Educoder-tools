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

// parallelCourse serves one course with independent one-level objective
// homeworks (distinct runtimes) and records concurrent formal submissions.
type parallelCourse struct {
	t         *testing.T
	homeworks []int64
	mu        sync.Mutex
	submitted map[int64]bool
	inflight  int
	peak      int
	hold      time.Duration
}

func newParallelCourse(t *testing.T, ids ...int64) *parallelCourse {
	return &parallelCourse{t: t, homeworks: ids, submitted: map[int64]bool{}, hold: 150 * time.Millisecond}
}

func (c *parallelCourse) peakSubmits() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.peak
}

func parallelChoiceTask(hw int64) string {
	return fmt.Sprintf(`{"st":1,"homework_common_id":%d,"game":{"identifier":"G%d","id":%d,"myshixun_id":%d},"myshixun":{"id":%d,"identifier":"RT%d"},"shixun":{"identifier":"SX%d","challenges_count":1},"challenge":{"id":%d,"st":1,"position":1,"subject":"level","task_pass":"Choose"},"choose_test_cases":{"challenge_chooses_count":1},"chooses":[{"challenge_choose_id":%d,"challenge_id":%d,"position":1,"question_type":0,"category":1,"subject":"First?","challenge_question":[{"option_name":"First","position":0},{"option_name":"Second","position":1}]}]}`,
		hw, hw, 1000+hw, 500+hw, 500+hw, hw, hw, 100+hw, 200+hw, 100+hw)
}

func (c *parallelCourse) server() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		switch {
		case path == "/api/users/student/courses.json":
			fmt.Fprint(w, `{"count":1,"courses":[{"id":1,"first_category_url":"/classrooms/C/shixun_homework"}]}`)
		case path == "/api/courses/C/homework_commons.json":
			rows := make([]string, 0, len(c.homeworks))
			for _, hw := range c.homeworks {
				rows = append(rows, fmt.Sprintf(`{"homework_id":%d,"name":"hw%d","is_shixun":true,"shixun_identifier":"SX%d","challenge_count":1}`, hw, hw, hw))
			}
			fmt.Fprintf(w, `{"query_total_count":%d,"homeworks":[%s]}`, len(rows), strings.Join(rows, ","))
		case strings.HasPrefix(path, "/api/shixun_homeworks/") && strings.HasSuffix(path, "/header_info.json"):
			hw := idIn(path, "/api/shixun_homeworks/", "/header_info.json")
			fmt.Fprintf(w, `{"data":{"homework_id":%d,"work_id":5,"myshixun_identifier":"RT%d","shixun_identifier":"SX%d","challenges":[{"id":%d,"name":"level"}]}}`, hw, hw, hw, 100+hw)
		case strings.HasPrefix(path, "/api/homework_commons/") && strings.HasSuffix(path, "/shixun_challenge_data.json"):
			hw := idIn(path, "/api/homework_commons/", "/shixun_challenge_data.json")
			c.mu.Lock()
			score := "0"
			if c.submitted[hw] {
				score = "10"
			}
			c.mu.Unlock()
			fmt.Fprintf(w, `{"data":{"challenge_settings":[{"challenge_id":%d,"challenge_name":"level","challenge_score":"10","game_score":%q}]}}`, 100+hw, score)
		case strings.HasPrefix(path, "/api/myshixuns/RT") && strings.HasSuffix(path, "/challenges.json"):
			hw := idIn(path, "/api/myshixuns/RT", "/challenges.json")
			fmt.Fprintf(w, `[{"identifier":"G%d","position":1,"name":"level","status":2}]`, hw)
		case strings.HasPrefix(path, "/api/tasks/G") && strings.HasSuffix(path, "/choose_build.json"):
			hw := idIn(path, "/api/tasks/G", "/choose_build.json")
			c.mu.Lock()
			c.inflight++
			c.peak = max(c.peak, c.inflight)
			c.mu.Unlock()
			time.Sleep(c.hold)
			c.mu.Lock()
			c.inflight--
			c.submitted[hw] = true
			c.mu.Unlock()
			fmt.Fprint(w, `{"status":0,"choose_correct_num":1,"challenge_chooses_count":1}`)
		case strings.HasPrefix(path, "/api/tasks/G") && strings.HasSuffix(path, ".json"):
			fmt.Fprint(w, parallelChoiceTask(idIn(path, "/api/tasks/G", ".json")))
		default:
			c.t.Errorf("unexpected request %s", path)
			http.NotFound(w, r)
		}
	}))
}

func idIn(path, prefix, suffix string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix), 10, 64)
	return n
}

func runParallelCourse(t *testing.T, course *parallelCourse, cfg Config, budget int64) (*CourseBatchResult, error) {
	t.Helper()
	server := course.server()
	t.Cleanup(server.Close)
	client := educoder.New(server.URL)
	client.LoginName = "student"
	cfg.ModelConcurrency = 4
	return New(bank(t), &fakeProvider{}, cfg).RunCourses(context.Background(), client, Request{Owner: "o", Course: "C", Mode: solver.ModeSubmit, BudgetMicroUSD: budget}, nil)
}

func TestCourseHomeworksRunInParallelWithinBound(t *testing.T) {
	course := newParallelCourse(t, 41, 42, 43, 44, 45)
	got, err := runParallelCourse(t, course, Config{HomeworkParallelism: 3}, 0)
	if err != nil || got.Status != "passed" || got.CompletedHomeworks != 5 || got.NeedsServiceHomeworks != 0 {
		t.Fatalf("parallel course=%+v err=%v", got, err)
	}
	if peak := course.peakSubmits(); peak < 2 || peak > 3 {
		t.Fatalf("formal submissions overlapped %d at once; want 2..3", peak)
	}
	// Results keep the plan order regardless of completion order.
	for i, h := range got.Homeworks {
		if h.HomeworkID != int64(41+i) || h.Status != "passed" {
			t.Fatalf("homework %d out of order or failed: %+v", i, h)
		}
	}
}

func TestCourseHomeworksStaySequentialByDefault(t *testing.T) {
	course := newParallelCourse(t, 41, 42, 43)
	got, err := runParallelCourse(t, course, Config{}, 0)
	if err != nil || got.Status != "passed" || got.CompletedHomeworks != 3 || course.peakSubmits() != 1 {
		t.Fatalf("default course is not sequential: %+v err=%v peak=%d", got, err, course.peakSubmits())
	}
}

func TestParallelCourseBudgetWaitsForTheSequentialCap(t *testing.T) {
	// The course budget covers one full homework cap: a second child must wait
	// for the first to settle instead of starting with a sliver or failing.
	course := newParallelCourse(t, 41, 42, 43)
	got, err := runParallelCourse(t, course, Config{HomeworkParallelism: 3}, batchBudgetMicroUSD+1)
	if err != nil || got.Status != "passed" || got.CompletedHomeworks != 3 || got.NeedsServiceHomeworks != 0 {
		t.Fatalf("budget-limited course=%+v err=%v", got, err)
	}
	if peak := course.peakSubmits(); peak != 1 {
		t.Fatalf("budget reservations overlapped: peak=%d", peak)
	}
	if got.CostMicroUSD+got.ReservedMicroUSD > batchBudgetMicroUSD+1 {
		t.Fatalf("course spent beyond its budget: %+v", got)
	}
}

func TestParallelCourseCancellationStopsEveryChild(t *testing.T) {
	course := newParallelCourse(t, 41, 42, 43, 44, 45)
	server := course.server()
	defer server.Close()
	client := educoder.New(server.URL)
	client.LoginName = "student"
	entered := make(chan struct{}, 8)
	provider := &fakeProvider{generate: func(ctx context.Context) error {
		entered <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		result *CourseBatchResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		r, err := New(bank(t), provider, Config{HomeworkParallelism: 3, ModelConcurrency: 4}).RunCourses(ctx, client, Request{Owner: "o", Course: "C", Mode: solver.ModeSubmit}, nil)
		done <- outcome{r, err}
	}()
	for i := 0; i < 3; i++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d children started concurrently", i)
		}
	}
	cancel()
	var got outcome
	select {
	case got = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("canceled course did not return")
	}
	if !errors.Is(got.err, context.Canceled) || got.result.Status != "canceled" {
		t.Fatalf("cancellation result=%+v err=%v", got.result, got.err)
	}
	interrupted, planned := 0, 0
	for _, h := range got.result.Homeworks {
		switch {
		case h.Status == "needs_service" && h.ReasonCode == "interrupted":
			interrupted++
		case h.Status == "planned":
			planned++
		default:
			t.Fatalf("unexpected homework state after cancellation: %+v", h)
		}
	}
	if interrupted != 3 || planned != 2 {
		t.Fatalf("interrupted=%d planned=%d", interrupted, planned)
	}
}
