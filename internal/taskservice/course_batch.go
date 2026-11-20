package taskservice

import (
	"context"
	"crypto/sha256"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/solver"
	"errors"
	"fmt"
	"os"
	"regexp"
	"runtime/debug"
	"sync"
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
// Homeworks carries the per-assignment live detail; Current* describes the
// assignment the snapshot is focused on.
type ServiceProgress struct {
	TotalHomeworks        int                       `json:"total_homeworks"`
	CompletedHomeworks    int                       `json:"completed_homeworks"`
	SkippedHomeworks      int                       `json:"skipped_homeworks"`
	NeedsServiceHomeworks int                       `json:"needs_service_homeworks"`
	CurrentCourse         string                    `json:"current_course,omitempty"`
	CurrentHomeworkID     int64                     `json:"current_homework_id,omitempty"`
	CurrentTitle          string                    `json:"current_title,omitempty"`
	Stage                 string                    `json:"stage,omitempty"`
	Batch                 *BatchProgress            `json:"batch,omitempty"`
	Homeworks             []ServiceHomeworkProgress `json:"homeworks,omitempty"`
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

var errHomeworkWorkerPanicked = errors.New("homework worker stopped unexpectedly")

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
	// A course can contain many multi-level assignments. Share the worker's
	// configured batch deadline instead of imposing another fixed 30-minute
	// deadline; parent cancellation and the per-homework deadline still apply.
	timeout := s.cfg.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Hour
	}
	if timeout > 24*time.Hour {
		timeout = 24 * time.Hour
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	scope := "all"
	if req.Course != "" {
		scope = "course"
	}
	out := &CourseBatchResult{Kind: "service", Scope: scope, Status: "running", Homeworks: []CourseHomeworkResult{}}
	defer func() {
		visible := out.Homeworks[:0]
		for _, homework := range out.Homeworks {
			if homework.Status != "skipped_optional" {
				visible = append(visible, homework)
			}
		}
		out.Homeworks = visible
	}()
	current := ServiceProgress{}
	// live is the structured snapshot shared by the consumer and operator views.
	// It stays nil until the whole plan is resolved and bounded.
	var live *courseProgress
	emit := func(phase, message string) {
		if report == nil {
			return
		}
		p := current
		p.TotalHomeworks, p.CompletedHomeworks, p.SkippedHomeworks, p.NeedsServiceHomeworks = out.TotalHomeworks, out.CompletedHomeworks, out.SkippedHomeworks, out.NeedsServiceHomeworks
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
	emit("loading", "正在同步课堂和作业")
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
	live = newCourseProgress(plan)
	budget := serviceBudgetMicroUSD
	if req.BudgetMicroUSD > 0 && req.BudgetMicroUSD < budget {
		budget = req.BudgetMicroUSD
	}
	key := req.IdempotencyKey
	if key == "" {
		key = runID()
	}
	parallel := s.cfg.HomeworkParallelism
	if parallel < 1 {
		parallel = 1
	}
	// Distinct homeworks own distinct runtimes, so up to `parallel` of them run
	// at once; runtime, target and graphic account locks still serialize any
	// shared resource. Counters, the budget, the live progress snapshot and
	// events are guarded by mu, and each worker writes only its own
	// out.Homeworks[i] and its own progress slot.
	var mu sync.Mutex
	budgetReleased := sync.NewCond(&mu)
	var reservedBudget int64
	var stopErr error
	// workerPanic holds the first panic of a homework goroutine. It is
	// re-raised on this (the job's) goroutine once in-flight homeworks finish.
	var workerPanic any
	locked := func(f func()) {
		mu.Lock()
		defer mu.Unlock()
		f()
	}
	// emitFor builds and publishes the snapshot while holding mu. Snapshotting
	// outside the lock let two parallel assignments deliver their reports in the
	// opposite order, which moved the counters and the current assignment
	// backwards in the consumer and operator views.
	emitFor := func(i int, phase, message string, batch *BatchProgress) {
		if report == nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		live.at(i).Observe(phase, batch, time.Now().UTC())
		p := live.snapshot(out.TotalHomeworks, out.CompletedHomeworks, out.SkippedHomeworks, out.NeedsServiceHomeworks)
		report(Progress{Phase: phase, Kind: "service", Message: message, Service: &p})
	}
	// settle records an assignment's terminal state and publishes it in one
	// lock hold, so no snapshot can show a finished assignment as still running.
	settle := func(i int, status, reason, phase, message string, result *BatchResult, update func()) {
		mu.Lock()
		defer mu.Unlock()
		if update != nil {
			update()
		}
		live.at(i).Settle(status, reason, result, time.Now().UTC())
		if report == nil {
			return
		}
		p := live.snapshot(out.TotalHomeworks, out.CompletedHomeworks, out.SkippedHomeworks, out.NeedsServiceHomeworks)
		report(Progress{Phase: phase, Kind: "service", Message: message, Service: &p})
	}
	halt := func(err error) {
		locked(func() {
			if stopErr == nil {
				stopErr = err
			}
		})
	}
	process := func(i int, p courseHomeworkPlan) {
		emitFor(i, "loading", "正在处理作业", nil)
		h := &out.Homeworks[i]
		needsService := func(reason, message string, result *BatchResult) {
			settle(i, ProgressAttention, reason, "service_pending", message, result, func() {
				h.Status, h.ReasonCode = "needs_service", reason
				if result != nil {
					h.Result = result
				}
				out.NeedsServiceHomeworks++
			})
		}
		skippedOptional := func() {
			settle(i, ProgressSkipped, "no_required_levels", "verified", "这份作业没有需要安排的必做关卡", nil, func() {
				h.Status, h.ReasonCode = "skipped_optional", "no_required_levels"
				out.TotalHomeworks--
				live.hide(i)
			})
		}
		interrupted := func(err error) {
			locked(func() {
				h.Status, h.ReasonCode = "needs_service", "interrupted"
				out.NeedsServiceHomeworks++
				live.at(i).Settle(ProgressAttention, "interrupted", nil, time.Now().UTC())
			})
			halt(err)
		}
		// A request can be queued before the deadline and begin afterwards.
		// Recheck the locally supplied deadline immediately before any progress
		// read, runtime entry, model call, or mutation.
		if p.hw.IsClosed(time.Now()) {
			needsService("deadline_passed", "这份作业已超过截止时间，已停止安排", nil)
			return
		}
		if !p.hw.IsShixun || !courseIdentifier.MatchString(p.hw.ShixunIdentifier) {
			needsService("unsupported_homework", "这份作业已转交服务人员处理", nil)
			return
		}
		// Summary passed_count/finished_count are insufficient evidence. Only an
		// unambiguous, complete assigned score roster with exact full scores can
		// skip an assignment without entering its runtime.
		progress, e := client.HomeworkProgress(ctx, p.course.Identifier, p.hw.ID)
		if e != nil {
			if ctx.Err() != nil {
				halt(e)
				return
			}
			var result *BatchResult
			var readError *educoder.HomeworkProgressReadError
			if errors.As(e, &readError) {
				result = &BatchResult{Kind: "homework", Scope: "homework", Status: "progress_unavailable", ReasonCode: "progress_unavailable", Diagnostics: []string{readError.Error()}}
			}
			needsService("progress_unavailable", "这份作业已转交服务人员处理", result)
			return
		}
		if required, valid := requiredHomeworkCount(p.hw, progress); valid && required == 0 {
			skippedOptional()
			return
		}
		if homeworkExactlyComplete(p.hw, progress) {
			settle(i, ProgressSkipped, "already_passed", "verified", "已完成的作业已自动跳过", nil, func() {
				h.Status = "skipped"
				out.CompletedHomeworks++
				out.SkippedHomeworks++
			})
			return
		}
		resolvedHomework := p.hw
		childRequest := Request{Owner: req.Owner, Course: p.course.Identifier, HomeworkID: p.hw.ID, Homework: &resolvedHomework, Mode: solver.ModeSubmit}
		block, fuseErr := s.homeworkFuse(ctx, childRequest, p.hw, progress, time.Now().UTC())
		if fuseErr != nil {
			needsService("blocker_state_unavailable", "这份作业的恢复状态需要核验，已保留进度", nil)
			return
		}
		if block != nil {
			needsService("unchanged_failure_cooldown", "这份作业没有新的可用进展，已暂停重复安排", &BatchResult{Kind: "homework", Scope: "homework", Status: "needs_service", ReasonCode: block.Reason,
				Diagnostics: []string{"上次已确认失败且作业进度未变化；24 小时内不重复消耗资源，可单独重试这份作业。"}})
			return
		}
		// Reserve the child's whole cap before it starts, so concurrently running
		// children never commit more than the course budget together. A child
		// receives exactly the cap it would get running alone; while concurrent
		// reservations leave less than that it waits for them to settle, instead
		// of starting with (or failing on) a sliver the sequential order would
		// never produce. Waiting implies another child holds a reservation, and
		// that child always releases it and broadcasts when it returns.
		var childCap int64
		halted := false
		mu.Lock()
		for {
			if stopErr != nil {
				halted = true
				break
			}
			spent := out.CostMicroUSD + out.ReservedMicroUSD
			full := min(budget-spent, batchBudgetMicroUSD)
			if full <= 0 || ctx.Err() != nil {
				break
			}
			if budget-spent-reservedBudget >= full {
				childCap = full
				reservedBudget += childCap
				break
			}
			budgetReleased.Wait()
		}
		mu.Unlock()
		if halted {
			// Another homework stopped the job while this one waited for budget;
			// it never started and stays planned, as in sequential order.
			return
		}
		if childCap <= 0 && ctx.Err() != nil {
			halt(ctx.Err())
			return
		}
		if childCap <= 0 {
			needsService("budget_exhausted", "这份作业已转交服务人员处理", nil)
			return
		}
		childKey := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%d", key, p.course.Identifier, p.hw.ID)))
		childRequest.IdempotencyKey, childRequest.BudgetMicroUSD = fmt.Sprintf("course-%x", childKey), childCap
		batch, childErr := s.RunHomework(ctx, client, childRequest, func(update Progress) {
			emitFor(i, update.Phase, "正在处理作业", update.Batch)
		})
		locked(func() {
			reservedBudget -= childCap
			budgetReleased.Broadcast()
			h.Result = batch
			if batch != nil {
				out.ModelCalls += batch.ModelCalls
				out.CostMicroUSD += batch.CostMicroUSD
				out.ReservedMicroUSD += batch.ReservedMicroUSD
			}
		})
		if ctx.Err() != nil {
			interrupted(childErr)
			return
		}
		// A child can only verify its own plan. Independently require complete
		// official assignment evidence before trusting passed/no-required; a
		// truncated header or a late required row must not produce a course pass.
		if childErr == nil && batch != nil && batch.Status == "passed" {
			latest, readErr := client.HomeworkProgress(ctx, p.course.Identifier, p.hw.ID)
			if ctx.Err() != nil {
				interrupted(ctx.Err())
				return
			}
			var verificationErr error
			if readErr != nil {
				verificationErr = errors.New("final official homework progress is unavailable")
			} else if coverageErr := homeworkPlanCoverage(p.hw, latest, batch.Levels); coverageErr != nil {
				verificationErr = coverageErr
			} else if !homeworkExactlyComplete(p.hw, latest) {
				verificationErr = errors.New("final official homework progress does not confirm every required challenge at full score")
			}
			if verificationErr != nil {
				batch.Status, batch.ReasonCode = "verification_unconfirmed", "assignment_verification_unconfirmed"
				batch.Diagnostics = append(batch.Diagnostics, verificationErr.Error())
				childErr = verificationErr
			} else if required, valid := requiredHomeworkCount(p.hw, latest); valid && required == 0 {
				// The independent official read also owns late optional changes.
				// Do not keep an assignment in course totals just because the
				// child completed it before it became wholly optional.
				skippedOptional()
				return
			}
		}
		if childErr == nil && batch != nil && batch.Status == "passed" && batch.TotalLevels == 0 && batch.ReasonCode == "no_required_levels" {
			skippedOptional()
			return
		}
		if childErr == nil && batch != nil && batch.Status == "passed" && batch.TotalLevels > 0 && batch.CompletedLevels == batch.TotalLevels && batch.FailedLevels == 0 {
			settle(i, ProgressPassed, "", "verified", "这份作业已完成", batch, func() {
				h.Status = "passed"
				out.CompletedHomeworks++
			})
			return
		}
		reason := "execution_failed"
		if batch != nil && batch.ReasonCode != "" {
			reason = batch.ReasonCode
		}
		if deterministicHomeworkFailure(reason) {
			// Persist only after an independent read proves the exact canonical
			// score/requirement roster is unchanged. A partial read cannot fuse.
			if after, readErr := client.HomeworkProgress(ctx, p.course.Identifier, p.hw.ID); readErr == nil {
				if saveErr := s.rememberHomeworkFailure(ctx, childRequest, p.hw, progress, after, batch, time.Now().UTC()); saveErr != nil {
					reason = "blocker_state_persistence_failed"
				}
			}
		}
		needsService(reason, "这份作业已转交服务人员处理", nil)
	}
	slots := make(chan struct{}, parallel)
	var workers sync.WaitGroup
dispatch:
	for i, p := range plan {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			break dispatch
		}
		halted := false
		locked(func() { halted = stopErr != nil })
		if halted || ctx.Err() != nil {
			<-slots
			break
		}
		workers.Add(1)
		go func(i int, p courseHomeworkPlan) {
			defer workers.Done()
			defer func() { <-slots }()
			defer func() {
				// A panic here must not end the process. Stop dispatching and wake
				// budget waiters: they check stopErr before budget, so a reservation
				// the panicked homework still holds cannot keep them waiting. The
				// panic is re-raised after in-flight homeworks finish.
				if r := recover(); r != nil {
					// Type and stack only: a panic value may carry upstream text.
					fmt.Fprintf(os.Stderr, "course homework worker panic (%T)\n%s", r, debug.Stack())
					locked(func() {
						if workerPanic == nil {
							workerPanic = r
						}
						if stopErr == nil {
							stopErr = errHomeworkWorkerPanicked
						}
						budgetReleased.Broadcast()
					})
				}
			}()
			process(i, p)
		}(i, p)
	}
	workers.Wait()
	if workerPanic != nil {
		// Same outcome as before homeworks ran in parallel: the job goroutine
		// unwinds and the job manager's recover fails (or, after a write,
		// marks uncertain) this job while the service keeps running.
		panic(workerPanic)
	}
	if err := ctx.Err(); err != nil {
		if stopErr == nil {
			stopErr = err
		}
		return stop(stopErr, "canceled")
	}
	if out.CompletedHomeworks == out.TotalHomeworks && out.NeedsServiceHomeworks == 0 {
		out.Status = "passed"
		if out.TotalHomeworks == 0 {
			out.ReasonCode = "no_active_homework"
		}
	} else {
		out.Status, out.ReasonCode = "needs_service", "service_required"
	}
	current = live.snapshot(out.TotalHomeworks, out.CompletedHomeworks, out.SkippedHomeworks, out.NeedsServiceHomeworks)
	emit("completed", "本次作业处理进度已更新")
	return out, nil
}

func homeworkExactlyComplete(hw educoder.Homework, progress *educoder.HomeworkProgress) bool {
	if _, valid := requiredHomeworkCount(hw, progress); !valid {
		return false
	}
	for _, p := range progress.Challenges {
		if !p.IsOptional() && !p.HasFullScore() {
			return false
		}
	}
	return true
}
