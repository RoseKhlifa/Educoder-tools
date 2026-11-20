package taskservice

import "time"

// Public live-progress enums. They are rendered by the consumer and operator
// views and must stay a small closed set: no upstream text, no identifiers.
const (
	ProgressWaiting   = "waiting"
	ProgressRunning   = "running"
	ProgressPassed    = "passed"
	ProgressSkipped   = "skipped"
	ProgressFailed    = "failed"
	ProgressStopped   = "stopped"
	ProgressAttention = "needs_attention"
)

const (
	// Bounds mirror serviceMaxHomeworks and the level bound enforced by the
	// job manager, so a snapshot can never grow with upstream input.
	progressMaxHomeworks = serviceMaxHomeworks
	progressMaxLevels    = 100
)

// ServiceLevelProgress is one level's public live state inside an assignment.
type ServiceLevelProgress struct {
	Position int    `json:"position"`
	Title    string `json:"title,omitempty"`
	Status   string `json:"status"`
}

// ServiceHomeworkProgress is one assignment's public live state. Reason codes
// are enums; execution diagnostics stay in the operator-facing result.
type ServiceHomeworkProgress struct {
	Course          string                 `json:"course,omitempty"`
	CourseName      string                 `json:"course_name,omitempty"`
	HomeworkID      int64                  `json:"homework_id,omitempty"`
	Title           string                 `json:"title,omitempty"`
	Status          string                 `json:"status"`
	ReasonCode      string                 `json:"reason_code,omitempty"`
	TotalLevels     int                    `json:"total_levels"`
	CompletedLevels int                    `json:"completed_levels"`
	SkippedLevels   int                    `json:"skipped_levels"`
	FailedLevels    int                    `json:"failed_levels"`
	CurrentPosition int                    `json:"current_position,omitempty"`
	CurrentTitle    string                 `json:"current_title,omitempty"`
	Stage           string                 `json:"stage,omitempty"`
	StartedAt       *time.Time             `json:"started_at,omitempty"`
	UpdatedAt       *time.Time             `json:"updated_at,omitempty"`
	Levels          []ServiceLevelProgress `json:"levels,omitempty"`
}

// HomeworkProgressTracker derives one assignment's live per-level state from
// the sequence of BatchProgress reports that RunHomework already emits.
//
// RunHomework reports a level's start with CurrentPosition set, then reports
// its outcome without a position: a pass increments CompletedLevels and a
// recorded failure increments FailedLevels. The tracker closes the running
// level from those deltas, so no extra reporting is required inside the
// per-assignment loop. Settle replaces the derived list with the authoritative
// level roster once the assignment returns.
type HomeworkProgressTracker struct {
	state     ServiceHomeworkProgress
	positions map[int]int
	// startCompleted/startFailed are the counters observed when the current
	// level became active, so an outcome is attributed to the right level even
	// when several reports arrive between its start and its result.
	startCompleted int
	startFailed    int
}

// NewHomeworkProgressTracker starts a tracker for a planned assignment.
func NewHomeworkProgressTracker(course, courseName string, homeworkID int64, title string) *HomeworkProgressTracker {
	return &HomeworkProgressTracker{
		state:     ServiceHomeworkProgress{Course: course, CourseName: courseName, HomeworkID: homeworkID, Title: title, Status: ProgressWaiting},
		positions: map[int]int{},
	}
}

// Start marks the assignment as running without changing any counter.
func (t *HomeworkProgressTracker) Start(now time.Time) {
	if t == nil {
		return
	}
	if t.state.StartedAt == nil {
		at := now
		t.state.StartedAt = &at
	}
	if t.state.Status == ProgressWaiting {
		t.state.Status = ProgressRunning
	}
	t.touch(now)
}

// Observe folds one report into the assignment state. Counters only ever move
// forward, so a late or duplicated report cannot reduce visible progress.
func (t *HomeworkProgressTracker) Observe(phase string, batch *BatchProgress, now time.Time) {
	if t == nil {
		return
	}
	t.Start(now)
	if phase != "" {
		t.state.Stage = phase
	}
	t.touch(now)
	if batch == nil {
		return
	}
	previousCompleted, previousFailed := t.state.CompletedLevels, t.state.FailedLevels
	if batch.TotalLevels > t.state.TotalLevels {
		t.state.TotalLevels = min(batch.TotalLevels, progressMaxLevels)
	}
	t.state.CompletedLevels = max(t.state.CompletedLevels, min(batch.CompletedLevels, t.state.TotalLevels))
	t.state.SkippedLevels = max(t.state.SkippedLevels, min(batch.SkippedLevels, t.state.CompletedLevels))
	t.state.FailedLevels = max(t.state.FailedLevels, min(batch.FailedLevels, t.state.TotalLevels))
	outcome := ""
	switch {
	case t.state.CompletedLevels > previousCompleted:
		outcome = ProgressPassed
	case t.state.FailedLevels > previousFailed:
		outcome = ProgressFailed
	}
	position := batch.CurrentPosition
	if position > 0 && position != t.state.CurrentPosition {
		// A new level started. Close the previous one with whatever outcome the
		// counters proved; an unproven one is reported as stopped, never passed.
		t.closeCurrent(t.levelOutcome(outcome))
		t.state.CurrentPosition = position
		t.startCompleted, t.startFailed = t.state.CompletedLevels, t.state.FailedLevels
	}
	if position > 0 {
		if batch.CurrentTitle != "" {
			t.state.CurrentTitle = batch.CurrentTitle
		}
		t.setLevel(position, batch.CurrentTitle, ProgressRunning)
		return
	}
	if outcome != "" {
		t.closeCurrent(outcome)
	}
}

// Settle records the assignment's final state. The authoritative level roster
// replaces the derived list, so the finished view never shows a guessed status.
func (t *HomeworkProgressTracker) Settle(status, reason string, result *BatchResult, now time.Time) {
	if t == nil {
		return
	}
	t.state.Status, t.state.ReasonCode, t.state.Stage = status, reason, ""
	if result != nil {
		if result.TotalLevels > 0 {
			t.state.TotalLevels = min(result.TotalLevels, progressMaxLevels)
		}
		t.state.CompletedLevels = max(t.state.CompletedLevels, min(result.CompletedLevels, t.state.TotalLevels))
		t.state.SkippedLevels = max(t.state.SkippedLevels, min(result.SkippedLevels, t.state.CompletedLevels))
		t.state.FailedLevels = max(t.state.FailedLevels, min(result.FailedLevels, t.state.TotalLevels))
		if levels := levelRoster(result); len(levels) > 0 {
			t.state.Levels = levels
			t.positions = map[int]int{}
			for i, level := range levels {
				t.positions[level.Position] = i
			}
		}
	}
	if status == ProgressPassed || status == ProgressSkipped {
		t.state.CompletedLevels = t.state.TotalLevels
		t.state.CurrentPosition, t.state.CurrentTitle = 0, ""
	}
	t.closeCurrent(t.levelOutcome(""))
	t.touch(now)
}

// Snapshot returns a deep copy that the caller may publish without sharing any
// mutable state with the running assignment.
func (t *HomeworkProgressTracker) Snapshot() ServiceHomeworkProgress {
	if t == nil {
		return ServiceHomeworkProgress{Status: ProgressWaiting}
	}
	out := t.state
	out.Levels = append([]ServiceLevelProgress(nil), t.state.Levels...)
	if t.state.StartedAt != nil {
		at := *t.state.StartedAt
		out.StartedAt = &at
	}
	if t.state.UpdatedAt != nil {
		at := *t.state.UpdatedAt
		out.UpdatedAt = &at
	}
	return out
}

// Running reports whether this assignment is still being processed.
func (t *HomeworkProgressTracker) Running() bool {
	return t != nil && t.state.Status == ProgressRunning
}

func (t *HomeworkProgressTracker) touch(now time.Time) {
	at := now
	t.state.UpdatedAt = &at
}

// levelOutcome keeps an unproven close honest: "stopped" says the level did not
// finish, which is different from a recorded evaluation failure.
func (t *HomeworkProgressTracker) levelOutcome(outcome string) string {
	if outcome != "" {
		return outcome
	}
	if t.state.CompletedLevels > t.startCompleted {
		return ProgressPassed
	}
	if t.state.FailedLevels > t.startFailed {
		return ProgressFailed
	}
	return ProgressStopped
}

func (t *HomeworkProgressTracker) closeCurrent(status string) {
	position := t.state.CurrentPosition
	if position <= 0 {
		return
	}
	index, ok := t.positions[position]
	if !ok || t.state.Levels[index].Status != ProgressRunning {
		return
	}
	t.state.Levels[index].Status = status
}

func (t *HomeworkProgressTracker) setLevel(position int, title, status string) {
	if position <= 0 {
		return
	}
	if index, ok := t.positions[position]; ok {
		if title != "" {
			t.state.Levels[index].Title = title
		}
		t.state.Levels[index].Status = status
		return
	}
	if len(t.state.Levels) >= progressMaxLevels {
		return
	}
	t.positions[position] = len(t.state.Levels)
	t.state.Levels = append(t.state.Levels, ServiceLevelProgress{Position: position, Title: title, Status: status})
}

func levelRoster(result *BatchResult) []ServiceLevelProgress {
	out := make([]ServiceLevelProgress, 0, min(len(result.Levels), progressMaxLevels))
	for _, level := range result.Levels {
		if len(out) >= progressMaxLevels {
			break
		}
		out = append(out, ServiceLevelProgress{Position: level.Position, Title: level.Title, Status: levelStatus(level.Status)})
	}
	return out
}

// levelStatus collapses the many execution statuses of a finished level into
// the public enum. Anything that is not proven complete needs attention.
func levelStatus(status string) string {
	switch status {
	case "passed", "completed", "succeeded", "generated", "selftest_passed", "selftest_skipped":
		return ProgressPassed
	case "skipped", "skipped_optional", "optional":
		return ProgressSkipped
	case "planned", "pending", "":
		return ProgressWaiting
	case "running", "processing":
		return ProgressRunning
	default:
		return ProgressFailed
	}
}

// courseProgress owns the live snapshot of a classroom or account-wide job.
// Every assignment writes only its own slot, and the whole snapshot is built
// and published under the job's mutex, so a snapshot is internally consistent
// and parallel assignments cannot publish each other's state out of order.
type courseProgress struct {
	items  []*HomeworkProgressTracker
	hidden []bool
}

func newCourseProgress(plan []courseHomeworkPlan) *courseProgress {
	out := &courseProgress{items: make([]*HomeworkProgressTracker, len(plan)), hidden: make([]bool, len(plan))}
	for i, p := range plan {
		out.items[i] = NewHomeworkProgressTracker(p.course.Identifier, p.course.Name, p.hw.ID, p.hw.Name)
	}
	return out
}

func (c *courseProgress) at(i int) *HomeworkProgressTracker {
	if c == nil || i < 0 || i >= len(c.items) {
		return nil
	}
	return c.items[i]
}

// hide drops an assignment that turned out to have no schedulable level. The
// course result omits it as well, so the two views stay identical.
func (c *courseProgress) hide(i int) {
	if c != nil && i >= 0 && i < len(c.hidden) {
		c.hidden[i] = true
	}
}

// snapshot publishes the plan-wide counters supplied by the caller together
// with the per-assignment detail. The current assignment is the running one
// with the lowest plan position, so it advances but never jumps back and forth
// between the assignments running in parallel.
func (c *courseProgress) snapshot(total, completed, skipped, needsService int) ServiceProgress {
	out := ServiceProgress{TotalHomeworks: total, CompletedHomeworks: completed, SkippedHomeworks: skipped, NeedsServiceHomeworks: needsService}
	if c == nil {
		return out
	}
	out.Homeworks = make([]ServiceHomeworkProgress, 0, min(len(c.items), progressMaxHomeworks))
	var focus *HomeworkProgressTracker
	for i, item := range c.items {
		if c.hidden[i] || item == nil {
			continue
		}
		if focus == nil && item.Running() {
			focus = item
		}
		if len(out.Homeworks) < progressMaxHomeworks {
			out.Homeworks = append(out.Homeworks, item.Snapshot())
		}
	}
	if focus != nil {
		state := focus.Snapshot()
		out.CurrentCourse, out.CurrentHomeworkID, out.CurrentTitle = state.Course, state.HomeworkID, state.Title
		out.Batch = &BatchProgress{TotalLevels: state.TotalLevels, CompletedLevels: state.CompletedLevels, SkippedLevels: state.SkippedLevels, FailedLevels: state.FailedLevels, CurrentPosition: state.CurrentPosition, CurrentTitle: state.CurrentTitle}
		out.Stage = state.Stage
	}
	return out
}
