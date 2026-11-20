package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func livingProgress(homeworks int) *BatchProgress {
	p := &BatchProgress{TotalHomeworks: homeworks, TotalLevels: 2, CompletedLevels: 1, Stage: "selftesting"}
	for i := 0; i < homeworks; i++ {
		p.Homeworks = append(p.Homeworks, HomeworkProgress{
			Course: "course", CourseName: "课程", HomeworkID: int64(i + 1), Title: "作业", Status: "running",
			TotalLevels: 2, CompletedLevels: 1, CurrentPosition: 2, CurrentTitle: "第二关", Stage: "selftesting",
			Levels: []LevelProgress{{Position: 1, Title: "第一关", Status: "passed"}, {Position: 2, Title: "第二关", Status: "running"}},
		})
	}
	return p
}

// The structured snapshot is published to users and operators, so every entry
// must pass the same boundary the counters already pass.
func TestPublicProgressBoundsAndSanitizesHomeworkDetail(t *testing.T) {
	oversized := livingProgress(1)
	for i := 0; i < 160; i++ {
		oversized.Homeworks = append(oversized.Homeworks, HomeworkProgress{HomeworkID: int64(100 + i), Title: "作业", Status: "waiting"})
	}
	oversized.TotalHomeworks = 100
	got := publicProgress(oversized)
	if got == nil {
		t.Fatal("a bounded snapshot was rejected outright")
	}
	if len(got.Homeworks) != 100 {
		t.Fatalf("assignment list not bounded: %d", len(got.Homeworks))
	}

	levels := livingProgress(1)
	for i := 0; i < 200; i++ {
		levels.Homeworks[0].Levels = append(levels.Homeworks[0].Levels, LevelProgress{Position: 10 + i, Title: "关卡", Status: "waiting"})
	}
	if got = publicProgress(levels); got == nil || len(got.Homeworks[0].Levels) != 100 {
		t.Fatalf("level list not bounded: %+v", got)
	}

	// An unknown status is not display text: the entry is dropped.
	unknown := livingProgress(1)
	unknown.Homeworks[0].Status = "internal_retry_window"
	if got = publicProgress(unknown); got == nil || len(got.Homeworks) != 0 {
		t.Fatalf("unknown assignment status survived: %+v", got)
	}
	unknownLevel := livingProgress(1)
	unknownLevel.Homeworks[0].Levels[1].Status = "awaiting_graphic_ssh"
	if got = publicProgress(unknownLevel); got == nil || len(got.Homeworks[0].Levels) != 1 {
		t.Fatalf("unknown level status survived: %+v", got.Homeworks[0].Levels)
	}

	// Credential-shaped text, non-identifier courses and unknown reason labels
	// never reach a page.
	leaky := livingProgress(1)
	leaky.Homeworks[0].Title = "Authorization: Bearer abcdef"
	leaky.Homeworks[0].CurrentTitle = "https://user:pass@host/x?sec_key=abc"
	leaky.Homeworks[0].Course = "course id with spaces"
	leaky.Homeworks[0].ReasonCode = "reason with spaces"
	leaky.Homeworks[0].Stage = "stage with spaces"
	got = publicProgress(leaky)
	if got == nil || len(got.Homeworks) != 1 {
		t.Fatalf("sanitized entry was dropped: %+v", got)
	}
	entry := got.Homeworks[0]
	if entry.Title != "" || entry.Course != "" || entry.ReasonCode != "" || entry.Stage != "" {
		t.Fatalf("unsafe fields survived sanitizing: %+v", entry)
	}
	if strings.Contains(entry.CurrentTitle, "sec_key") || strings.Contains(entry.CurrentTitle, "pass@") {
		t.Fatalf("signed or credential text survived: %q", entry.CurrentTitle)
	}

	// Impossible counters are clamped rather than published.
	wrong := livingProgress(1)
	wrong.Homeworks[0].CompletedLevels = 99
	wrong.Homeworks[0].SkippedLevels = 99
	wrong.Homeworks[0].FailedLevels = 99
	if got = publicProgress(wrong); got == nil || got.Homeworks[0].CompletedLevels != 2 || got.Homeworks[0].SkippedLevels != 2 || got.Homeworks[0].FailedLevels != 2 {
		t.Fatalf("level counters were not clamped: %+v", got.Homeworks[0])
	}

	// A detail-only snapshot (no counters yet) is still publishable.
	detailOnly := &BatchProgress{Homeworks: []HomeworkProgress{{HomeworkID: 1, Title: "作业", Status: "waiting"}}}
	if got = publicProgress(detailOnly); got == nil || len(got.Homeworks) != 1 {
		t.Fatalf("detail-only snapshot rejected: %+v", got)
	}
}

func TestEventProgressKeepsHistoryToCounters(t *testing.T) {
	trimmed := eventProgress(livingProgress(3))
	if trimmed == nil || trimmed.Homeworks != nil {
		t.Fatalf("event kept the per-assignment detail: %+v", trimmed)
	}
	if trimmed.TotalLevels != 2 || trimmed.CompletedLevels != 1 || trimmed.Stage != "selftesting" {
		t.Fatalf("event lost its counters: %+v", trimmed)
	}
}

func TestCloneProgressDoesNotShareNestedState(t *testing.T) {
	source := livingProgress(1)
	at := time.Now().UTC()
	source.Homeworks[0].StartedAt = &at
	copied := cloneProgress(source)
	copied.Homeworks[0].Levels[0].Status = "failed"
	copied.Homeworks[0].Title = "changed"
	*copied.Homeworks[0].StartedAt = at.Add(time.Hour)
	if source.Homeworks[0].Levels[0].Status != "passed" || source.Homeworks[0].Title != "作业" {
		t.Fatalf("clone shared assignment state: %+v", source.Homeworks[0])
	}
	if !source.Homeworks[0].StartedAt.Equal(at) {
		t.Fatalf("clone shared a timestamp pointer: %v", source.Homeworks[0].StartedAt)
	}
}

func storedJobJSON(t *testing.T, db *sql.DB, id string) Job {
	t.Helper()
	var raw []byte
	if err := db.QueryRow(`SELECT data FROM async_jobs WHERE id=?`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var job Job
	if err := json.Unmarshal(raw, &job); err != nil {
		t.Fatal(err)
	}
	return job
}

// Parallel assignments raised the event rate, and every event used to be an
// immediate SQLite write under a process-global mutex. Progress is now served
// from memory and persisted at most once a second, plus on every phase change
// and terminal state.
func TestProgressPersistenceIsThrottledButAlwaysLiveInMemory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.sqlite")
	m := testManager(t, Config{Path: path, Workers: 1})
	owner := "owner"
	release := make(chan struct{})
	reported := make(chan struct{})
	job := submit(t, m, owner, Request{Scope: "all", Mode: "submit"}, func(ctx context.Context, r Request, report func(Event)) (Outcome, error) {
		// Many updates inside one phase: only the first reaches storage.
		for i := 1; i <= 40; i++ {
			p := livingProgress(1)
			p.Homeworks[0].CurrentPosition = i
			report(Event{Phase: "selftesting", Message: "progress", Progress: p})
		}
		close(reported)
		<-release
		return Outcome{Status: Succeeded, Result: json.RawMessage(`{"status":"passed"}`)}, nil
	})
	<-reported

	// The in-memory job is current and carries the structured detail.
	live, err := m.Get(context.Background(), owner, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if live.Progress == nil || len(live.Progress.Homeworks) != 1 {
		t.Fatalf("live job has no structured progress: %+v", live.Progress)
	}
	if live.Progress.Homeworks[0].CurrentPosition != 40 {
		t.Fatalf("live job is stale: position %d", live.Progress.Homeworks[0].CurrentPosition)
	}
	if live.Phase != "selftesting" {
		t.Fatalf("live phase is wrong: %q", live.Phase)
	}
	// List overlays memory as well, so the consumer list is equally fresh.
	list, err := m.List(context.Background(), owner, 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%d err=%v", len(list), err)
	}
	if list[0].Progress == nil || list[0].Progress.Homeworks[0].CurrentPosition != 40 {
		t.Fatalf("list projection is stale: %+v", list[0].Progress)
	}
	// Stored rows lag deliberately: the first report of the phase was written,
	// the other 39 were not.
	stored := storedJobJSON(t, m.db, job.ID)
	if stored.Progress == nil {
		t.Fatalf("the first report of a phase was not persisted")
	}
	if stored.Progress.Homeworks[0].CurrentPosition == 40 {
		t.Fatalf("every report reached storage; persistence is not throttled")
	}
	// Events never carry the nested detail, whatever the executor reports.
	for _, event := range live.Events {
		if event.Progress != nil && event.Progress.Homeworks != nil {
			t.Fatalf("event history carries per-assignment detail: %+v", event.Progress)
		}
	}

	// A phase change is written immediately: a restart must not misclassify it.
	m.report(m.active[job.ID], Event{Phase: "evaluating", Message: "submitting", Progress: livingProgress(1)})
	if stored = storedJobJSON(t, m.db, job.ID); stored.Phase != "evaluating" {
		t.Fatalf("phase change was not flushed: %q", stored.Phase)
	}

	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		final, err := m.Get(context.Background(), owner, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if final.Status == Succeeded {
			// A settled job keeps its counters but drops the per-assignment
			// detail: the stored result owns that, and list responses stay small.
			if final.Progress != nil && final.Progress.Homeworks != nil {
				t.Fatalf("finished job still carries live detail: %+v", final.Progress)
			}
			if final.Progress == nil || final.Progress.TotalLevels != 2 {
				t.Fatalf("finished job lost its counters: %+v", final.Progress)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not settle: %+v", final)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if stored = storedJobJSON(t, m.db, job.ID); stored.Status != Succeeded || stored.Progress == nil || stored.Progress.Homeworks != nil {
		t.Fatalf("terminal state was not flushed correctly: %+v", stored)
	}
}

// An administrator reading a running job sees the same live state, without a
// second write path.
func TestAdminReadsOverlayLiveProgress(t *testing.T) {
	m := testManager(t, Config{Workers: 1})
	reported, release := make(chan struct{}), make(chan struct{})
	job := submit(t, m, "owner", Request{Course: "course", Scope: "course", Mode: "submit"}, func(ctx context.Context, r Request, report func(Event)) (Outcome, error) {
		p := livingProgress(2)
		p.Homeworks[1].CurrentPosition = 7
		report(Event{Phase: "evaluating", Message: "working", Progress: p})
		close(reported)
		<-release
		return Outcome{Status: Succeeded}, nil
	})
	<-reported
	defer close(release)
	record, err := m.AdminGet(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.OwnerID != "owner" {
		t.Fatalf("admin record lost its owner: %q", record.OwnerID)
	}
	if record.Progress == nil || len(record.Progress.Homeworks) != 2 || record.Progress.Homeworks[1].CurrentPosition != 7 {
		t.Fatalf("admin read is not live: %+v", record.Progress)
	}
	list, err := m.AdminList(context.Background(), 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("admin list=%d err=%v", len(list), err)
	}
	if list[0].Progress == nil || len(list[0].Progress.Homeworks) != 2 {
		t.Fatalf("admin list is not live: %+v", list[0].Progress)
	}
}

// The store is limited to one connection and a worker holds the manager mutex
// while it writes. A reader that takes the mutex with a result cursor still
// open deadlocks the two against each other, which froze every job listing
// until the process was restarted. Read paths must release the cursor first.
func TestListingsDoNotHoldTheStoreCursorWhileLocking(t *testing.T) {
	m := testManager(t, Config{Workers: 2})
	owners := []string{"owner-a", "owner-b"}
	release := make(chan struct{})
	var started sync.WaitGroup
	for _, owner := range owners {
		started.Add(1)
		// Each phase change forces a write while the manager mutex is held,
		// which is the half of the inversion the worker owns.
		submit(t, m, owner, Request{Scope: "all", Mode: "submit"}, func(ctx context.Context, r Request, report func(Event)) (Outcome, error) {
			started.Done()
			for i := 0; i < 400; i++ {
				phase := "selftesting"
				if i%2 == 0 {
					phase = "evaluating"
				}
				report(Event{Phase: phase, Message: "working", Progress: livingProgress(1)})
			}
			<-release
			return Outcome{Status: Succeeded}, nil
		})
	}
	started.Wait()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 300; i++ {
			if _, err := m.List(context.Background(), owners[i%len(owners)], 50); err != nil {
				t.Errorf("list: %v", err)
				return
			}
			if _, err := m.AdminList(context.Background(), 50); err != nil {
				t.Errorf("admin list: %v", err)
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		close(release)
		t.Fatal("listing deadlocked against a writing worker")
	}
	close(release)
}

// Parallel homeworks report in different phases on nearly every event. Those
// phase flips must not bypass the write throttle; only the first entry into a
// mutating phase (the crash classification) is written at once.
func TestParallelPhaseFlipsStayThrottled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.sqlite")
	m := testManager(t, Config{Path: path, Workers: 1})
	release := make(chan struct{})
	reported := make(chan struct{})
	job := submit(t, m, "owner", Request{Scope: "all", Mode: "submit"}, func(ctx context.Context, r Request, report func(Event)) (Outcome, error) {
		report(Event{Phase: "loading", Message: "start", Progress: livingProgress(1)})
		for i := 1; i <= 30; i++ {
			phase := "generating"
			if i%2 == 0 {
				phase = "selftesting"
			}
			p := livingProgress(1)
			p.Homeworks[0].CurrentPosition = i
			report(Event{Phase: phase, Message: "progress", Progress: p})
		}
		close(reported)
		<-release
		return Outcome{Status: Succeeded, Result: json.RawMessage(`{"status":"passed"}`)}, nil
	})
	<-reported
	stored := storedJobJSON(t, m.db, job.ID)
	if stored.Progress != nil && len(stored.Progress.Homeworks) > 0 && stored.Progress.Homeworks[0].CurrentPosition == 30 {
		t.Fatal("alternating non-mutating phases bypassed the write throttle")
	}
	// The first entry into a mutating phase is still flushed at once.
	m.mu.Lock()
	running := m.active[job.ID]
	m.mu.Unlock()
	m.report(running, Event{Phase: "evaluating", Message: "submitting"})
	var mutating bool
	if err := m.db.QueryRow(`SELECT mutating FROM async_jobs WHERE id=?`, job.ID).Scan(&mutating); err != nil || !mutating {
		t.Fatalf("mutating flag not persisted at once: %v err=%v", mutating, err)
	}
	close(release)
}
