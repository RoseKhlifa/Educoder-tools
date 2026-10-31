package taskservice

import (
	"context"
	"crypto/sha256"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/solver"
	"errors"
	"fmt"
	"regexp"
	"time"
)

const (
	serviceMaxCourses     = 20
	serviceMaxHomeworks   = 100
	serviceBudgetMicroUSD = int64(2000000) // $2.00, including unknown-cost reservations.
)

var courseIdentifier = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

// ServiceProgress contains counters and public target names only. Detailed
// execution failures belong to the operator-facing result, not user messages.
type ServiceProgress struct {
	TotalHomeworks        int            `json:"total_homeworks"`
	CompletedHomeworks    int            `json:"completed_homeworks"`
	SkippedHomeworks      int            `json:"skipped_homeworks"`
	NeedsServiceHomeworks int            `json:"needs_service_homeworks"`
	CurrentCourse         string         `json:"current_course,omitempty"`
	CurrentHomeworkID     int64          `json:"current_homework_id,omitempty"`
	CurrentTitle          string         `json:"current_title,omitempty"`
	Batch                 *BatchProgress `json:"batch,omitempty"`
}

type CourseHomeworkResult struct {
	Course     string       `json:"course"`
	CourseName string       `json:"course_name"`
	HomeworkID int64        `json:"homework_id"`
	Title      string       `json:"title"`
	Status     string       `json:"status"`
	ReasonCode string       `json:"reason_code,omitempty"`
	Result     *BatchResult `json:"result,omitempty"`
}

type CourseBatchResult struct {
	Status                string                 `json:"status"`
	Kind                  string                 `json:"kind"`
	Scope                 string                 `json:"scope"`
	TotalCourses          int                    `json:"total_courses"`
	TotalHomeworks        int                    `json:"total_homeworks"`
	CompletedHomeworks    int                    `json:"completed_homeworks"`
	SkippedHomeworks      int                    `json:"skipped_homeworks"`
	NeedsServiceHomeworks int                    `json:"needs_service_homeworks"`
	ModelCalls            int                    `json:"model_calls"`
	CostMicroUSD          int64                  `json:"cost_micro_usd"`
	ReservedMicroUSD      int64                  `json:"reserved_micro_usd,omitempty"`
	ReasonCode            string                 `json:"reason_code,omitempty"`
	Homeworks             []CourseHomeworkResult `json:"homeworks"`
}

type courseHomeworkPlan struct {
	course educoder.Course
	hw     educoder.Homework
}

// RunCourses completes the unfinished assignments in one authenticated course,
// or in all active courses when Course is empty. A failed assignment is handed
// to service staff while unrelated assignments continue. An uncertain write is
// never replayed by this loop. Per-homework roster, identity, retry and final
// score checks remain in RunHomework.
func (s *Service) RunCourses(ctx context.Context, client *educoder.Client, req Request, report func(Progress)) (*CourseBatchResult, error) {
	if s == nil || s.store == nil || client == nil {
		return nil, errors.New("task service requires store and authenticated client")
	}
	if req.Owner == "" || req.Mode != solver.ModeSubmit || req.HomeworkID != 0 || req.Game != "" || (req.Course != "" && !courseIdentifier.MatchString(req.Course)) || req.BudgetMicroUSD < 0 {
		return nil, errors.New("course service requires owner, submit mode and no homework or game target")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	scope := "all"
	if req.Course != "" {
		scope = "course"
	}
	out := &CourseBatchResult{Kind: "service", Scope: scope, Status: "running", Homeworks: []CourseHomeworkResult{}}
	current := ServiceProgress{}
	emit := func(phase, message string, batch *BatchProgress) {
		if report == nil {
			return
		}
		p := current
		p.TotalHomeworks, p.CompletedHomeworks, p.SkippedHomeworks, p.NeedsServiceHomeworks = out.TotalHomeworks, out.CompletedHomeworks, out.SkippedHomeworks, out.NeedsServiceHomeworks
		if batch != nil {
			copy := *batch
			p.Batch = &copy
		}
		report(Progress{Phase: phase, Kind: "service", Message: message, Service: &p})
	}
	stop := func(err error, reason string) (*CourseBatchResult, error) {
		out.Status, out.ReasonCode = "needs_service", reason
		if ctx.Err() != nil {
			out.Status, out.ReasonCode = "canceled", "canceled"
			return out, ctx.Err()
		}
		return out, err
	}
	emit("loading", "正在同步课堂和作业", nil)
	courses, err := client.Courses(ctx, "processing")
	if err != nil {
		return stop(err, "course_sync_failed")
	}
	selected := make([]educoder.Course, 0, len(courses))
	seenCourses := map[string]bool{}
	for _, c := range courses {
		if req.Course != "" && c.Identifier != req.Course {
			continue
		}
		if !courseIdentifier.MatchString(c.Identifier) || seenCourses[c.Identifier] {
			return stop(errors.New("course identity is missing or ambiguous"), "course_identity_invalid")
		}
		seenCourses[c.Identifier] = true
		selected = append(selected, c)
	}
	if req.Course != "" && len(selected) != 1 {
		return stop(errors.New("selected course does not belong to this account's active classrooms"), "course_not_found")
	}
	if len(selected) > serviceMaxCourses {
		return stop(errors.New("service course count exceeds limit"), "course_limit")
	}
	out.TotalCourses = len(selected)
	// Resolve and bound the whole target list before any Enter/save/submit.
	plan := []courseHomeworkPlan{}
	seenHomeworks := map[int64]bool{}
	for _, c := range selected {
		if err := ctx.Err(); err != nil {
			return stop(err, "canceled")
		}
		homeworks, err := client.Homeworks(ctx, c.Identifier, "")
		if err != nil {
			return stop(err, "homework_sync_failed")
		}
		for _, h := range homeworks {
			if h.ID <= 0 || seenHomeworks[h.ID] {
				return stop(errors.New("homework identity is missing or ambiguous"), "homework_identity_invalid")
			}
			seenHomeworks[h.ID] = true
			// Closed assignments remain visible on Educoder for history, but
			// cannot be scheduled by this service. Filter them before the plan is
			// counted so an all-closed scope is a valid zero-work request and does
			// not consume model calls or enter a runtime.
			if h.IsClosed(time.Now()) {
				continue
			}
			plan = append(plan, courseHomeworkPlan{c, h})
			if len(plan) > serviceMaxHomeworks {
				return stop(errors.New("service homework count exceeds limit"), "homework_limit")
			}
		}
	}
	out.TotalHomeworks = len(plan)
	for _, p := range plan {
		out.Homeworks = append(out.Homeworks, CourseHomeworkResult{Course: p.course.Identifier, CourseName: p.course.Name, HomeworkID: p.hw.ID, Title: p.hw.Name, Status: "planned"})
	}
	budget := serviceBudgetMicroUSD
	if req.BudgetMicroUSD > 0 && req.BudgetMicroUSD < budget {
		budget = req.BudgetMicroUSD
	}
	key := req.IdempotencyKey
	if key == "" {
		key = runID()
	}
	for i, p := range plan {
		if err := ctx.Err(); err != nil {
			return stop(err, "canceled")
		}
		current = ServiceProgress{CurrentCourse: p.course.Identifier, CurrentHomeworkID: p.hw.ID, CurrentTitle: p.hw.Name}
		emit("loading", "正在处理作业", nil)
		h := &out.Homeworks[i]
		// A request can be queued before the deadline and begin afterwards.
		// Recheck the locally supplied deadline immediately before any progress
		// read, runtime entry, model call, or mutation.
		if p.hw.IsClosed(time.Now()) {
			h.Status, h.ReasonCode = "needs_service", "deadline_passed"
			out.NeedsServiceHomeworks++
			emit("service_pending", "这份作业已超过截止时间，已停止安排", nil)
			continue
		}
		if !p.hw.IsShixun || !courseIdentifier.MatchString(p.hw.ShixunIdentifier) {
			h.Status, h.ReasonCode = "needs_service", "unsupported_homework"
			out.NeedsServiceHomeworks++
			emit("service_pending", "这份作业已转交服务人员处理", nil)
			continue
		}
		// Summary passed_count/finished_count are insufficient evidence. Only an
		// unambiguous, complete assigned score roster with exact full scores can
		// skip an assignment without entering its runtime.
		progress, e := client.HomeworkProgress(ctx, p.course.Identifier, p.hw.ID)
		if e != nil {
			if ctx.Err() != nil {
				return stop(e, "canceled")
			}
			h.Status, h.ReasonCode = "needs_service", "progress_unavailable"
			out.NeedsServiceHomeworks++
			emit("service_pending", "这份作业已转交服务人员处理", nil)
			continue
		}
		if homeworkExactlyComplete(p.hw, progress) {
			h.Status = "skipped"
			out.CompletedHomeworks++
			out.SkippedHomeworks++
			emit("verified", "已完成的作业已自动跳过", nil)
			continue
		}
		remaining := budget - out.CostMicroUSD - out.ReservedMicroUSD
		if remaining <= 0 {
			h.Status, h.ReasonCode = "needs_service", "budget_exhausted"
			out.NeedsServiceHomeworks++
			emit("service_pending", "这份作业已转交服务人员处理", nil)
			continue
		}
		childKey := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%d", key, p.course.Identifier, p.hw.ID)))
		batch, childErr := s.RunHomework(ctx, client, Request{Owner: req.Owner, Course: p.course.Identifier, HomeworkID: p.hw.ID, Mode: solver.ModeSubmit, IdempotencyKey: fmt.Sprintf("course-%x", childKey), BudgetMicroUSD: remaining}, func(p Progress) {
			emit(p.Phase, "正在处理作业", p.Batch)
		})
		h.Result = batch
		if batch != nil {
			out.ModelCalls += batch.ModelCalls
			out.CostMicroUSD += batch.CostMicroUSD
			out.ReservedMicroUSD += batch.ReservedMicroUSD
		}
		if ctx.Err() != nil {
			h.Status, h.ReasonCode = "needs_service", "interrupted"
			out.NeedsServiceHomeworks++
			return stop(childErr, "canceled")
		}
		if childErr == nil && batch != nil && batch.Status == "passed" && batch.TotalLevels > 0 && batch.CompletedLevels == batch.TotalLevels && batch.FailedLevels == 0 {
			h.Status = "passed"
			out.CompletedHomeworks++
			emit("verified", "这份作业已完成", nil)
		} else {
			h.Status, h.ReasonCode = "needs_service", "execution_failed"
			if batch != nil && batch.ReasonCode != "" {
				h.ReasonCode = batch.ReasonCode
			}
			out.NeedsServiceHomeworks++
			emit("service_pending", "这份作业已转交服务人员处理", nil)
		}
	}
	current = ServiceProgress{}
	if out.CompletedHomeworks == out.TotalHomeworks && out.NeedsServiceHomeworks == 0 {
		out.Status = "passed"
		if out.TotalHomeworks == 0 {
			out.ReasonCode = "no_active_homework"
		}
	} else {
		out.Status, out.ReasonCode = "needs_service", "service_required"
	}
	emit("completed", "本次作业处理进度已更新", nil)
	return out, nil
}

func homeworkExactlyComplete(hw educoder.Homework, progress *educoder.HomeworkProgress) bool {
	if progress == nil || hw.ChallengeCount < 1 || hw.ChallengeCount > 100 || len(progress.Challenges) != hw.ChallengeCount {
		return false
	}
	seen := map[int64]bool{}
	for _, p := range progress.Challenges {
		if p.ChallengeID <= 0 || seen[p.ChallengeID] || p.Name == "" || !p.HasFullScore() {
			return false
		}
		seen[p.ChallengeID] = true
	}
	return true
}
