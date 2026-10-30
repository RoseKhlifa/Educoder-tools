package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testManager(t *testing.T, c Config) *Manager {
	t.Helper()
	if c.Path == "" {
		c.Path = filepath.Join(t.TempDir(), "jobs.sqlite")
	}
	m, e := Open(context.Background(), c)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if e := m.Close(ctx); e != nil {
			t.Errorf("close: %v", e)
		}
	})
	return m
}

func request(game string) Request {
	return Request{Course: "course", HomeworkID: 42, Game: game, Mode: "generate_only"}
}
func submit(t *testing.T, m *Manager, owner string, r Request, e Executor) Job {
	t.Helper()
	j, _, err := m.Submit(context.Background(), owner, r, e)
	if err != nil {
		t.Fatal(err)
	}
	return j
}
func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for executor")
		var z T
		return z
	}
}
func waitJob(t *testing.T, m *Manager, owner, id, status string) Job {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		j, e := m.Get(context.Background(), owner, id)
		if e != nil {
			t.Fatal(e)
		}
		if j.Status == status {
			return j
		}
		if j.Status != Queued && j.Status != Running {
			t.Fatalf("job status = %s, want %s: %+v", j.Status, status, j)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for job state")
	return Job{}
}

func TestFairSchedulingAndConcurrency(t *testing.T) {
	m := testManager(t, Config{Workers: 2, QueueSize: 10, PerOwnerPending: 4})
	started := make(chan string, 10)
	var mu sync.Mutex
	owners := map[string]int{}
	max := 0
	current := 0
	gates := map[string]chan struct{}{"a1": make(chan struct{}), "a2": make(chan struct{}), "a3": make(chan struct{}), "b1": make(chan struct{}), "c1": make(chan struct{})}
	t.Cleanup(func() {
		for _, ch := range gates {
			select {
			case <-ch:
			default:
				close(ch)
			}
		}
	})
	exec := func(owner string) Executor {
		return func(ctx context.Context, r Request, _ func(Event)) (Outcome, error) {
			mu.Lock()
			owners[owner]++
			current++
			if current > max {
				max = current
			}
			if owners[owner] != 1 {
				t.Error("same owner ran concurrently")
			}
			mu.Unlock()
			started <- r.Game
			select {
			case <-gates[r.Game]:
			case <-ctx.Done():
			}
			mu.Lock()
			owners[owner]--
			current--
			mu.Unlock()
			return Outcome{}, ctx.Err()
		}
	}
	a1 := submit(t, m, "a", request("a1"), exec("a"))
	if got := receive(t, started); got != "a1" {
		t.Fatal(got)
	}
	a2 := submit(t, m, "a", request("a2"), exec("a"))
	a3 := submit(t, m, "a", request("a3"), exec("a"))
	b := submit(t, m, "b", request("b1"), exec("b"))
	if got := receive(t, started); got != "b1" {
		t.Fatalf("busy owner blocked other account: %s", got)
	}
	c := submit(t, m, "c", request("c1"), exec("c"))
	if s := m.Stats(); s.Running != 2 || s.Queued != 3 {
		t.Fatalf("stats: %+v", s)
	}
	close(gates["b1"])
	waitJob(t, m, "b", b.ID, Succeeded)
	if got := receive(t, started); got != "c1" {
		t.Fatalf("fair scan: %s", got)
	}
	close(gates["c1"])
	waitJob(t, m, "c", c.ID, Succeeded)
	close(gates["a1"])
	waitJob(t, m, "a", a1.ID, Succeeded)
	if got := receive(t, started); got != "a2" {
		t.Fatal(got)
	}
	close(gates["a2"])
	waitJob(t, m, "a", a2.ID, Succeeded)
	if got := receive(t, started); got != "a3" {
		t.Fatal(got)
	}
	close(gates["a3"])
	waitJob(t, m, "a", a3.ID, Succeeded)
	mu.Lock()
	defer mu.Unlock()
	if max != 2 {
		t.Fatalf("maximum concurrency = %d", max)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.active) != 0 || len(m.owners) != 0 {
		t.Fatal("terminal execution credentials/closures retained")
	}
}

func TestQueueLimitsAndQueuedCancellation(t *testing.T) {
	m := testManager(t, Config{Workers: 1, QueueSize: 1, PerOwnerPending: 1})
	started := make(chan struct{})
	gate := make(chan struct{})
	defer close(gate)
	block := func(ctx context.Context, _ Request, _ func(Event)) (Outcome, error) {
		close(started)
		select {
		case <-gate:
		case <-ctx.Done():
		}
		return Outcome{}, ctx.Err()
	}
	a := submit(t, m, "a", request("one"), block)
	receive(t, started)
	_, _, e := m.Submit(context.Background(), "a", request("two"), block)
	if !IsCode(e, "owner_limit") {
		t.Fatalf("owner limit: %v", e)
	}
	var calls atomic.Int32
	b := submit(t, m, "b", request("b"), func(context.Context, Request, func(Event)) (Outcome, error) { calls.Add(1); return Outcome{}, nil })
	_, _, e = m.Submit(context.Background(), "c", request("c"), block)
	if !IsCode(e, "queue_full") {
		t.Fatalf("queue limit: %v", e)
	}
	j, e := m.Cancel(context.Background(), "b", b.ID)
	if e != nil || j.Status != Canceled {
		t.Fatalf("cancel: %+v %v", j, e)
	}
	j, e = m.Cancel(context.Background(), "b", b.ID)
	if e != nil || j.Status != Canceled {
		t.Fatal("cancel was not idempotent")
	}
	if calls.Load() != 0 {
		t.Fatal("canceled queued job executed")
	}
	if m.Stats().Queued != 0 {
		t.Fatal("queue slot not released")
	}
	_, e = m.Cancel(context.Background(), "a", a.ID)
	if e != nil {
		t.Fatal(e)
	}
	waitJob(t, m, "a", a.ID, Canceled)
}

func TestIdempotencyDeduplicationAndOwnerIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.sqlite")
	m := testManager(t, Config{Path: path, Workers: 1})
	started := make(chan struct{})
	gate := make(chan struct{})
	var once sync.Once
	var calls atomic.Int32
	exec := func(ctx context.Context, _ Request, _ func(Event)) (Outcome, error) {
		calls.Add(1)
		once.Do(func() { close(started) })
		select {
		case <-gate:
		case <-ctx.Done():
		}
		return Outcome{Result: json.RawMessage(`{"ok":true}`)}, ctx.Err()
	}
	r := request("one")
	r.IdempotencyKey = "key-1"
	j := submit(t, m, "owner-a", r, exec)
	receive(t, started)
	again, reused, e := m.Submit(context.Background(), "owner-a", r, exec)
	if e != nil || !reused || again.ID != j.ID {
		t.Fatal("idempotency did not reuse")
	}
	other := r
	other.Game = "different"
	_, _, e = m.Submit(context.Background(), "owner-a", other, exec)
	if !IsCode(e, "idempotency_conflict") {
		t.Fatalf("conflict: %v", e)
	}
	alias := r
	alias.IdempotencyKey = "key-alias"
	same, reused, e := m.Submit(context.Background(), "owner-a", alias, exec)
	if e != nil || !reused || same.ID != j.ID {
		t.Fatal("target dedup failed")
	}
	if _, e = m.Get(context.Background(), "owner-b", j.ID); !IsCode(e, "not_found") {
		t.Fatal("cross-owner get exposed job")
	}
	if _, e = m.Cancel(context.Background(), "owner-b", j.ID); !IsCode(e, "not_found") {
		t.Fatal("cross-owner cancel exposed job")
	}
	list, e := m.List(context.Background(), "owner-b", 100)
	if e != nil || len(list) != 0 {
		t.Fatal("cross-owner list exposed job")
	}
	close(gate)
	waitJob(t, m, "owner-a", j.ID, Succeeded)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if e = m.Close(ctx); e != nil {
		t.Fatal(e)
	}
	m2 := testManager(t, Config{Path: path})
	same, reused, e = m2.Submit(context.Background(), "owner-a", alias, exec)
	if e != nil || !reused || same.ID != j.ID {
		t.Fatalf("alias not durable: %v %+v", e, same)
	}
	if calls.Load() != 1 {
		t.Fatal("idempotent request re-executed")
	}
	otherOwner := submit(t, m2, "owner-b", r, exec)
	waitJob(t, m2, "owner-b", otherOwner.ID, Succeeded)
	if otherOwner.ID == j.ID {
		t.Fatal("owners coalesced")
	}
}

func TestAcceptedJobOutlivesHTTPRequest(t *testing.T) {
	m := testManager(t, Config{Workers: 1})
	httpCtx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	gate := make(chan struct{})
	defer close(gate)
	exec := func(ctx context.Context, _ Request, _ func(Event)) (Outcome, error) {
		close(started)
		select {
		case <-gate:
			return Outcome{}, nil
		case <-ctx.Done():
			return Outcome{}, ctx.Err()
		}
	}
	j, _, e := m.Submit(httpCtx, "a", request("one"), exec)
	if e != nil {
		t.Fatal(e)
	}
	cancel()
	receive(t, started)
	if got, e := m.Get(context.Background(), "a", j.ID); e != nil || got.Status != Running {
		t.Fatalf("request context canceled accepted work: %+v %v", got, e)
	}
	_, e = m.Cancel(context.Background(), "a", j.ID)
	if e != nil {
		t.Fatal(e)
	}
	waitJob(t, m, "a", j.ID, Canceled)
}

func TestRunningCancellationAndMutationUncertainty(t *testing.T) {
	for _, phase := range []string{"loading", "saving", "submitting", "evaluating"} {
		t.Run(phase, func(t *testing.T) {
			m := testManager(t, Config{Workers: 1})
			started := make(chan struct{})
			j := submit(t, m, "a", request("one"), func(ctx context.Context, _ Request, report func(Event)) (Outcome, error) {
				report(Event{Phase: phase, Message: "Working"})
				close(started)
				<-ctx.Done()
				return Outcome{}, ctx.Err()
			})
			receive(t, started)
			if _, e := m.Cancel(context.Background(), "a", j.ID); e != nil {
				t.Fatal(e)
			}
			want := Uncertain
			if phase == "loading" {
				want = Canceled
			}
			finished := waitJob(t, m, "a", j.ID, want)
			if want == Uncertain && finished.ErrorCode != "submission_uncertain" {
				t.Fatal(finished)
			}
		})
	}
}

func TestTimeoutPanicAndSubsequentWork(t *testing.T) {
	m := testManager(t, Config{Workers: 1, Timeout: 30 * time.Millisecond})
	j := submit(t, m, "a", request("timeout"), func(ctx context.Context, _ Request, _ func(Event)) (Outcome, error) {
		<-ctx.Done()
		return Outcome{}, ctx.Err()
	})
	if got := waitJob(t, m, "a", j.ID, Failed); got.ErrorCode != "timeout" {
		t.Fatal(got)
	}
	j = submit(t, m, "a", request("panic"), func(context.Context, Request, func(Event)) (Outcome, error) { panic("authorization: Bearer private") })
	if got := waitJob(t, m, "a", j.ID, Failed); strings.Contains(got.Message, "private") {
		t.Fatal("panic exposed private text")
	}
	j = submit(t, m, "b", request("after"), func(context.Context, Request, func(Event)) (Outcome, error) { return Outcome{}, nil })
	waitJob(t, m, "b", j.ID, Succeeded)
}

func TestRestartMarksInterruptedAndDoesNotReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.sqlite")
	cfg, _ := (Config{Path: path}).defaults()
	db, e := openStore(context.Background(), cfg)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	for _, tc := range []struct {
		id, status, phase string
		mutating          bool
	}{{"queued", Queued, "queued", false}, {"reading", Running, "loading", false}, {"writing", Running, "evaluating", true}} {
		r := request(tc.id)
		r.IdempotencyKey = tc.id
		_, payload, target, _ := normalize(r)
		s := stored{Job: Job{ID: tc.id, Status: tc.status, Phase: tc.phase, Request: r, CreatedAt: now, UpdatedAt: now}, owner: "a", payload: payload, target: target, mutating: tc.mutating}
		if e = insertStore(db, &s); e != nil {
			t.Fatal(e)
		}
	}
	_ = db.Close()
	m := testManager(t, Config{Path: path})
	for _, id := range []string{"queued", "reading", "writing"} {
		j, e := m.Get(context.Background(), "a", id)
		if e != nil {
			t.Fatal(e)
		}
		want := Interrupted
		if id == "writing" {
			want = Uncertain
		}
		if j.Status != want || j.FinishedAt == nil {
			t.Fatalf("restart: %+v", j)
		}
		_, reused, e := m.Submit(context.Background(), "a", j.Request, func(context.Context, Request, func(Event)) (Outcome, error) {
			t.Error("replayed interrupted work")
			return Outcome{}, nil
		})
		if e != nil || !reused {
			t.Fatal("interrupted idempotency lost")
		}
	}
	if s := m.Stats(); s.Queued != 0 || s.Running != 0 {
		t.Fatal(s)
	}
}

func TestPublicPayloadAndEventBounds(t *testing.T) {
	m := testManager(t, Config{Workers: 1, EventLimit: 4, MaxResultBytes: 64})
	j := submit(t, m, "opaque-owner", request("events"), func(_ context.Context, _ Request, report func(Event)) (Outcome, error) {
		for i := 0; i < 20; i++ {
			report(Event{Phase: "loading", Message: fmt.Sprintf("event %d", i)})
		}
		report(Event{Phase: "reading", Message: "Cookie: secret"})
		return Outcome{Result: json.RawMessage(`{"value":42}`)}, nil
	})
	finished := waitJob(t, m, "opaque-owner", j.ID, Succeeded)
	b, _ := json.Marshal(finished)
	if len(finished.Events) != 4 || strings.Contains(string(b), "secret") || strings.Contains(string(b), "opaque-owner") {
		t.Fatalf("public bounds: %s", b)
	}
	for _, raw := range []string{`{"cookie":"private"}`, `{"value":"` + strings.Repeat("x", 100) + `"}`, `{} {}`} {
		j = submit(t, m, "opaque-owner", request(fmt.Sprintf("result%d", len(raw))), func(context.Context, Request, func(Event)) (Outcome, error) {
			return Outcome{Result: json.RawMessage(raw)}, nil
		})
		got := waitJob(t, m, "opaque-owner", j.ID, Failed)
		if len(got.Result) != 0 {
			t.Fatal("invalid result persisted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, e := m.Submit(ctx, "a", request("canceled-context"), func(context.Context, Request, func(Event)) (Outcome, error) { return Outcome{}, nil }); !errors.Is(e, context.Canceled) {
		t.Fatalf("pre-canceled request: %v", e)
	}
}

func TestShutdownCancelsAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.sqlite")
	m := testManager(t, Config{Path: path, Workers: 1})
	started := make(chan struct{})
	j := submit(t, m, "a", request("running"), func(ctx context.Context, _ Request, report func(Event)) (Outcome, error) {
		report(Event{Phase: "submitting", Message: "Submitting"})
		close(started)
		<-ctx.Done()
		return Outcome{}, ctx.Err()
	})
	receive(t, started)
	queued := submit(t, m, "b", request("queued"), func(context.Context, Request, func(Event)) (Outcome, error) {
		t.Error("queued work executed during shutdown")
		return Outcome{}, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if e := m.Close(ctx); e != nil {
		t.Fatal(e)
	}
	_, _, e := m.Submit(context.Background(), "c", request("rejected"), func(context.Context, Request, func(Event)) (Outcome, error) { return Outcome{}, nil })
	if !IsCode(e, "closed") {
		t.Fatal(e)
	}
	m2 := testManager(t, Config{Path: path})
	if got, e := m2.Get(context.Background(), "a", j.ID); e != nil || got.Status != Uncertain {
		t.Fatalf("shutdown running: %+v %v", got, e)
	}
	if got, e := m2.Get(context.Background(), "b", queued.ID); e != nil || got.Status != Canceled {
		t.Fatalf("shutdown queued: %+v %v", got, e)
	}
}

func TestCloseDeadlineBoundsUncooperativeExecutor(t *testing.T) {
	m := testManager(t, Config{Workers: 1})
	started := make(chan struct{})
	release := make(chan struct{})
	submit(t, m, "a", request("one"), func(context.Context, Request, func(Event)) (Outcome, error) {
		close(started)
		<-release
		return Outcome{}, nil
	})
	receive(t, started)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	e := m.Close(ctx)
	close(release)
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("close wait unbounded: %v", e)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()
	if e = m.Close(ctx2); e != nil {
		t.Fatal(e)
	}
}

func TestConcurrentAdmissionAndExecution(t *testing.T) {
	m := testManager(t, Config{Workers: 8, QueueSize: 128, PerOwnerPending: 4})
	const owners = 24
	const each = 4
	var inflight, max, calls atomic.Int32
	gate := make(chan struct{})
	started := make(chan struct{}, owners*each)
	exec := func(ctx context.Context, _ Request, _ func(Event)) (Outcome, error) {
		n := inflight.Add(1)
		defer inflight.Add(-1)
		for old := max.Load(); n > old; old = max.Load() {
			if max.CompareAndSwap(old, n) {
				break
			}
		}
		started <- struct{}{}
		select {
		case <-gate:
		case <-ctx.Done():
		}
		calls.Add(1)
		return Outcome{Result: json.RawMessage(`{"completed":true}`)}, ctx.Err()
	}
	type admitted struct {
		owner string
		job   Job
	}
	results := make(chan admitted, owners*each)
	var wg sync.WaitGroup
	for i := 0; i < owners; i++ {
		for j := 0; j < each; j++ {
			wg.Add(1)
			go func(i, j int) {
				defer wg.Done()
				owner := fmt.Sprintf("owner%d", i)
				r := request(fmt.Sprintf("game%d", j))
				job, _, e := m.Submit(context.Background(), owner, r, exec)
				if e != nil {
					t.Errorf("concurrent admission: %v", e)
					return
				}
				results <- admitted{owner, job}
			}(i, j)
		}
	}
	wg.Wait()
	close(results)
	for i := 0; i < 8; i++ {
		receive(t, started)
	}
	if got := m.Stats(); got.Running != 8 || got.Queued != owners*each-8 {
		t.Errorf("concurrent admission stats: %+v", got)
	}
	close(gate)
	for r := range results {
		waitJob(t, m, r.owner, r.job.ID, Succeeded)
	}
	if calls.Load() != owners*each || max.Load() != 8 {
		t.Fatalf("calls=%d max=%d", calls.Load(), max.Load())
	}
}
