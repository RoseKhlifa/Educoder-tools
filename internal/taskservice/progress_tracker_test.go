package taskservice

import (
	"context"
	"sync"
	"testing"
	"time"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/solver"
)

// Live progress must stay correct while assignments run in parallel: the
// published snapshot is the only thing both the consumer and operator pages
// render, so a non-monotonic counter or a snapshot mixing two assignments is
// visible as progress jumping backwards.
func TestCourseProgressSnapshotsStayConsistentUnderParallelHomeworks(t *testing.T) {
	course := newParallelCourse(t, 41, 42, 43, 44, 45)
	server := course.server()
	defer server.Close()
	client := educoder.New(server.URL)
	client.LoginName = "student"

	var mu sync.Mutex
	var snapshots []ServiceProgress
	highest := map[int64]int{}
	seenTerminal := map[int64]string{}
	report := func(p Progress) {
		if p.Service == nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		snapshot := *p.Service
		snapshots = append(snapshots, snapshot)
		if len(snapshots) > 1 {
			previous := snapshots[len(snapshots)-2]
			if snapshot.CompletedHomeworks < previous.CompletedHomeworks {
				t.Errorf("completed assignments went backwards: %d then %d", previous.CompletedHomeworks, snapshot.CompletedHomeworks)
			}
			if snapshot.NeedsServiceHomeworks < previous.NeedsServiceHomeworks {
				t.Errorf("attention count went backwards: %d then %d", previous.NeedsServiceHomeworks, snapshot.NeedsServiceHomeworks)
			}
		}
		running := 0
		for _, item := range snapshot.Homeworks {
			if item.CompletedLevels > item.TotalLevels && item.TotalLevels > 0 {
				t.Errorf("assignment %d reports %d/%d levels", item.HomeworkID, item.CompletedLevels, item.TotalLevels)
			}
			if item.CompletedLevels < highest[item.HomeworkID] {
				t.Errorf("assignment %d level counter went backwards: %d then %d", item.HomeworkID, highest[item.HomeworkID], item.CompletedLevels)
			}
			highest[item.HomeworkID] = item.CompletedLevels
			// A settled assignment never returns to running: that would make the
			// page claim finished work is in progress again.
			if previous, ok := seenTerminal[item.HomeworkID]; ok && item.Status != previous {
				t.Errorf("assignment %d changed from terminal %q to %q", item.HomeworkID, previous, item.Status)
			}
			switch item.Status {
			case ProgressPassed, ProgressSkipped, ProgressAttention:
				seenTerminal[item.HomeworkID] = item.Status
			case ProgressRunning:
				running++
			}
			if item.Title == "" || item.Course == "" {
				t.Errorf("assignment %d is missing its public target: %+v", item.HomeworkID, item)
			}
		}
		// The focused assignment must be one of the running ones, never a mix.
		if snapshot.CurrentHomeworkID != 0 {
			found := false
			for _, item := range snapshot.Homeworks {
				if item.HomeworkID == snapshot.CurrentHomeworkID {
					found = item.Status == ProgressRunning
					if snapshot.CurrentTitle != item.Title {
						t.Errorf("focused title %q does not belong to assignment %d (%q)", snapshot.CurrentTitle, item.HomeworkID, item.Title)
					}
				}
			}
			if !found {
				t.Errorf("focused assignment %d is not running in its own snapshot", snapshot.CurrentHomeworkID)
			}
		}
	}

	got, err := New(bank(t), &fakeProvider{}, Config{HomeworkParallelism: 3, ModelConcurrency: 4}).
		RunCourses(context.Background(), client, Request{Owner: "o", Course: "C", Mode: solver.ModeSubmit}, report)
	if err != nil || got.Status != "passed" || got.CompletedHomeworks != 5 {
		t.Fatalf("parallel course=%+v err=%v", got, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(snapshots) < 5 {
		t.Fatalf("expected a snapshot per assignment at least, got %d", len(snapshots))
	}
	final := snapshots[len(snapshots)-1]
	if len(final.Homeworks) != 5 {
		t.Fatalf("final snapshot lists %d assignments, want 5", len(final.Homeworks))
	}
	for i, item := range final.Homeworks {
		if item.HomeworkID != int64(41+i) {
			t.Fatalf("assignment %d out of plan order: %+v", i, item)
		}
		if item.Status != ProgressPassed {
			t.Fatalf("assignment %d did not settle as passed: %+v", item.HomeworkID, item)
		}
		if item.CompletedLevels != item.TotalLevels || item.TotalLevels == 0 {
			t.Fatalf("assignment %d levels %d/%d", item.HomeworkID, item.CompletedLevels, item.TotalLevels)
		}
		if item.StartedAt == nil || item.UpdatedAt == nil {
			t.Fatalf("assignment %d has no timestamps: %+v", item.HomeworkID, item)
		}
	}
	if final.CurrentHomeworkID != 0 || final.Batch != nil {
		t.Fatalf("finished snapshot still focuses an assignment: %+v", final)
	}
}

// A skipped assignment leaves the plan, exactly as it leaves the result, so the
// two views cannot disagree about how much work there was.
func TestCourseProgressHidesOptionalAssignmentsAndKeepsSkippedOnes(t *testing.T) {
	plan := []courseHomeworkPlan{
		{course: educoder.Course{Identifier: "C", Name: "课程"}, hw: educoder.Homework{ID: 1, Name: "作业一"}},
		{course: educoder.Course{Identifier: "C", Name: "课程"}, hw: educoder.Homework{ID: 2, Name: "作业二"}},
	}
	live := newCourseProgress(plan)
	now := time.Now().UTC()
	live.at(0).Settle(ProgressSkipped, "already_passed", nil, now)
	live.at(1).Settle(ProgressSkipped, "no_required_levels", nil, now)
	live.hide(1)
	snapshot := live.snapshot(1, 1, 1, 0)
	if len(snapshot.Homeworks) != 1 || snapshot.Homeworks[0].HomeworkID != 1 {
		t.Fatalf("hidden assignment still published: %+v", snapshot.Homeworks)
	}
	if snapshot.TotalHomeworks != 1 || snapshot.CompletedHomeworks != 1 {
		t.Fatalf("counters do not match the visible plan: %+v", snapshot)
	}
}

// The tracker derives per-level state from the reports RunHomework already
// emits. It must never report an unfinished level as passed.
func TestHomeworkProgressTrackerDerivesLevelStatesFromReports(t *testing.T) {
	now := time.Now().UTC()
	tracker := NewHomeworkProgressTracker("C", "课程", 7, "作业")
	tracker.Observe("loading", &BatchProgress{}, now)
	tracker.Observe("preflight", &BatchProgress{TotalLevels: 3, CompletedLevels: 1, SkippedLevels: 1}, now)
	tracker.Observe("running", &BatchProgress{TotalLevels: 3, CompletedLevels: 1, SkippedLevels: 1, CurrentPosition: 2, CurrentTitle: "第二关"}, now)
	tracker.Observe("selftesting", &BatchProgress{TotalLevels: 3, CompletedLevels: 1, SkippedLevels: 1, CurrentPosition: 2, CurrentTitle: "第二关"}, now)
	state := tracker.Snapshot()
	if state.Status != ProgressRunning || state.Stage != "selftesting" || state.CurrentPosition != 2 || state.CurrentTitle != "第二关" {
		t.Fatalf("running state is wrong: %+v", state)
	}
	if len(state.Levels) != 1 || state.Levels[0].Status != ProgressRunning {
		t.Fatalf("current level is not marked running: %+v", state.Levels)
	}
	// The pass of level 2 arrives as a counter increase without a position.
	tracker.Observe("verified", &BatchProgress{TotalLevels: 3, CompletedLevels: 2, SkippedLevels: 1}, now)
	state = tracker.Snapshot()
	if state.Levels[0].Status != ProgressPassed || state.CompletedLevels != 2 {
		t.Fatalf("level pass not recorded: %+v", state)
	}
	// Level 3 starts and fails.
	tracker.Observe("running", &BatchProgress{TotalLevels: 3, CompletedLevels: 2, SkippedLevels: 1, CurrentPosition: 3, CurrentTitle: "第三关"}, now)
	tracker.Observe("service_pending", &BatchProgress{TotalLevels: 3, CompletedLevels: 2, SkippedLevels: 1, FailedLevels: 1}, now)
	state = tracker.Snapshot()
	if len(state.Levels) != 2 || state.Levels[1].Status != ProgressFailed {
		t.Fatalf("level failure not recorded: %+v", state.Levels)
	}
	// Counters never move backwards, even if a late report says otherwise.
	tracker.Observe("running", &BatchProgress{TotalLevels: 3, CompletedLevels: 0, SkippedLevels: 0, FailedLevels: 0}, now)
	if state = tracker.Snapshot(); state.CompletedLevels != 2 || state.FailedLevels != 1 {
		t.Fatalf("counters regressed on a stale report: %+v", state)
	}
	// Settle replaces the derived list with the authoritative roster.
	tracker.Settle(ProgressAttention, "selftest_failed", &BatchResult{TotalLevels: 3, CompletedLevels: 2, SkippedLevels: 1, FailedLevels: 1, Levels: []BatchLevel{
		{Position: 1, Title: "第一关", Status: "skipped"},
		{Position: 2, Title: "第二关", Status: "passed"},
		{Position: 3, Title: "第三关", Status: "selftest_failed"},
	}}, now)
	state = tracker.Snapshot()
	if state.Status != ProgressAttention || state.ReasonCode != "selftest_failed" || state.Stage != "" {
		t.Fatalf("settled state is wrong: %+v", state)
	}
	want := []string{ProgressSkipped, ProgressPassed, ProgressFailed}
	if len(state.Levels) != 3 {
		t.Fatalf("roster not adopted: %+v", state.Levels)
	}
	for i, level := range state.Levels {
		if level.Status != want[i] || level.Position != i+1 {
			t.Fatalf("level %d is %+v, want %s", i+1, level, want[i])
		}
	}
}

// An interrupted level must not be published as passed just because it stopped.
func TestHomeworkProgressTrackerReportsUnprovenLevelsAsStopped(t *testing.T) {
	now := time.Now().UTC()
	tracker := NewHomeworkProgressTracker("C", "", 9, "作业")
	tracker.Observe("running", &BatchProgress{TotalLevels: 2, CurrentPosition: 1, CurrentTitle: "第一关"}, now)
	tracker.Settle(ProgressAttention, "interrupted", nil, now)
	state := tracker.Snapshot()
	if len(state.Levels) != 1 || state.Levels[0].Status != ProgressStopped {
		t.Fatalf("unproven level was not reported as stopped: %+v", state.Levels)
	}
	if state.CompletedLevels != 0 {
		t.Fatalf("stopped assignment claims completed levels: %+v", state)
	}
}

// Bounds are enforced where the snapshot is built, not only at the transport
// boundary, so an unexpected upstream plan cannot grow a stored job.
func TestHomeworkProgressTrackerBoundsLevels(t *testing.T) {
	now := time.Now().UTC()
	tracker := NewHomeworkProgressTracker("C", "", 9, "作业")
	for position := 1; position <= progressMaxLevels+40; position++ {
		tracker.Observe("running", &BatchProgress{TotalLevels: progressMaxLevels + 40, CurrentPosition: position}, now)
	}
	if state := tracker.Snapshot(); len(state.Levels) > progressMaxLevels || state.TotalLevels > progressMaxLevels {
		t.Fatalf("level bounds exceeded: levels=%d total=%d", len(state.Levels), state.TotalLevels)
	}
}

// Snapshot must not share memory with the running assignment: the job manager
// stores what it is handed, and a later mutation would rewrite history.
func TestHomeworkProgressSnapshotIsIndependent(t *testing.T) {
	now := time.Now().UTC()
	tracker := NewHomeworkProgressTracker("C", "课程", 3, "作业")
	tracker.Observe("running", &BatchProgress{TotalLevels: 2, CurrentPosition: 1, CurrentTitle: "第一关"}, now)
	first := tracker.Snapshot()
	tracker.Observe("verified", &BatchProgress{TotalLevels: 2, CompletedLevels: 1}, now.Add(time.Second))
	if first.Levels[0].Status != ProgressRunning || first.CompletedLevels != 0 {
		t.Fatalf("earlier snapshot was mutated: %+v", first)
	}
	if first.UpdatedAt == nil || !first.UpdatedAt.Equal(now) {
		t.Fatalf("snapshot timestamp was shared: %+v", first.UpdatedAt)
	}
}

// An update that carries the homework's level roster is authoritative: each
// level shows its real status and the current one shows as running.
func TestTrackerUsesTheReportedLevelRoster(t *testing.T) {
	tr := NewHomeworkProgressTracker("C", "course", 44, "hw")
	now := time.Now()
	tr.Observe("running", &BatchProgress{TotalLevels: 3, CompletedLevels: 1, SkippedLevels: 1, CurrentPosition: 2, CurrentTitle: "second",
		Levels: []BatchProgressLevel{{1, "first", "skipped"}, {2, "second", "planned"}, {3, "third", "planned"}}}, now)
	got := tr.Snapshot().Levels
	if len(got) != 3 || got[0].Status != ProgressSkipped || got[1].Status != ProgressRunning || got[2].Status != ProgressWaiting {
		t.Fatalf("live roster=%+v", got)
	}
	// Level 2 fails, level 3 starts: no status is guessed from counters.
	tr.Observe("running", &BatchProgress{TotalLevels: 3, CompletedLevels: 1, SkippedLevels: 1, FailedLevels: 1, CurrentPosition: 3, CurrentTitle: "third",
		Levels: []BatchProgressLevel{{1, "first", "skipped"}, {2, "second", "selftest_failed"}, {3, "third", "planned"}}}, now)
	got = tr.Snapshot().Levels
	if got[1].Status != ProgressFailed || got[2].Status != ProgressRunning {
		t.Fatalf("after a failure roster=%+v", got)
	}
	// A stale counter-only update cannot move counters backwards.
	tr.Observe("evaluating", &BatchProgress{TotalLevels: 3, Levels: []BatchProgressLevel{{1, "first", "skipped"}, {2, "second", "selftest_failed"}, {3, "third", "passed"}}}, now)
	snap := tr.Snapshot()
	if snap.CompletedLevels != 1 || snap.FailedLevels != 1 || snap.Levels[2].Status != ProgressPassed {
		t.Fatalf("monotonic counters or final status lost: %+v", snap)
	}
}
