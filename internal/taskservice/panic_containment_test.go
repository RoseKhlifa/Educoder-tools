package taskservice

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/solver"
)

// Before homeworks ran in parallel a panic unwound the job goroutine, where the
// job manager's recover failed the job and the service kept running. A panic in
// a homework goroutine must keep that outcome instead of ending the process:
// it is re-raised on the caller after in-flight work finishes, siblings waiting
// for the panicked homework's budget reservation wake up, and no further
// homework starts.
func TestParallelCourseHomeworkPanicIsReraisedOnTheJobGoroutine(t *testing.T) {
	course := newParallelCourse(t, 41, 42, 43, 44)
	server := course.server()
	defer server.Close()
	client := educoder.New(server.URL)
	client.LoginName = "student"
	var calls atomic.Int32
	provider := &fakeProvider{generate: func(context.Context) error {
		if calls.Add(1) == 1 {
			panic("homework worker fault")
		}
		return nil
	}}
	svc := New(bank(t), provider, Config{HomeworkParallelism: 3, ModelConcurrency: 4})
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		// The budget fits one homework cap at a time, so the siblings are
		// waiting for the reservation of the homework that panics.
		svc.RunCourses(context.Background(), client, Request{Owner: "o", Course: "C", Mode: solver.ModeSubmit, BudgetMicroUSD: batchBudgetMicroUSD + 1}, nil)
	}()
	select {
	case got := <-done:
		if got != "homework worker fault" {
			t.Fatalf("recovered %v, want the homework panic re-raised on the job goroutine", got)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("course hung after a homework panicked")
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("homeworks kept starting after a panic: model calls=%d", n)
	}
}

func TestObjectiveFigureFetchPanicIsReraisedOnTheCaller(t *testing.T) {
	fetch := func(_ context.Context, locator string) (educoder.QuestionImage, error) {
		if locator == "/api/attachments/2" {
			panic("figure reader fault")
		}
		return educoder.QuestionImage{MediaType: "image/png", Content: []byte("x")}, nil
	}
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		prefetchObjectiveImages(context.Background(), fetch, []string{"/api/attachments/1", "/api/attachments/2", "/api/attachments/3"})
	}()
	select {
	case got := <-done:
		if got != "figure reader fault" {
			t.Fatalf("recovered %v, want the figure panic re-raised on the caller", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("figure prefetch hung after a read panicked")
	}
}
