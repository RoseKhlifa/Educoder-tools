package web

import (
	"encoding/json"
	"strings"
	"time"

	"educoder-cli/internal/jobs"
)

// This allowlist is the API boundary, not a UI hiding rule. Operator events,
// provider failures, generated answers, usage and execution settings stay out.
type consumerJob struct {
	ID         string            `json:"id"`
	Status     string            `json:"status"`
	Phase      string            `json:"phase"`
	Kind       string            `json:"kind"`
	Request    jobs.Request      `json:"request"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
	StartedAt  *time.Time        `json:"started_at,omitempty"`
	Message    string            `json:"message"`
	CanRetry   bool              `json:"can_retry"`
	CanRecover bool              `json:"can_recover"`
	CanCancel  bool              `json:"can_cancel"`
	Result     consumerResult    `json:"result"`
	Progress   *consumerProgress `json:"progress,omitempty"`
	Recovery   *consumerRecovery `json:"recovery,omitempty"`
}
type consumerRecovery struct {
	OriginalID string `json:"original_id"`
	State      string `json:"state"`
	Message    string `json:"message"`
}
type consumerResult struct {
	Attention          string             `json:"attention,omitempty"`
	Reason             string             `json:"reason,omitempty"`
	AutoRetry          *consumerAutoRetry `json:"auto_retry,omitempty"`
	Title              string             `json:"title,omitempty"`
	ExerciseID         int64              `json:"exercise_id,omitempty"`
	TotalQuestions     int                `json:"total_questions,omitempty"`
	CompletedQuestions int                `json:"completed_questions,omitempty"`
	Submitted          bool               `json:"submitted,omitempty"`
	ScoreKnown         bool               `json:"score_known,omitempty"`
	Score              string             `json:"score,omitempty"`
	MaxScore           string             `json:"max_score,omitempty"`
	ServiceStatus      string             `json:"service_status"`
	TotalHomeworks     int                `json:"total_homeworks"`
	CompletedHomeworks int                `json:"completed_homeworks"`
	TotalLevels        int                `json:"total_levels"`
	CompletedLevels    int                `json:"completed_levels"`
	CurrentTitle       string             `json:"current_title,omitempty"`
	Homeworks          []consumerItem     `json:"homeworks,omitempty"`
	Levels             []consumerItem     `json:"levels,omitempty"`
}

// consumerAutoRetry mirrors the scheduled automatic retry the executor records.
// It is validated, not trusted: an unparseable or out-of-range schedule is
// dropped so the page never promises a retry that will not happen.
type consumerAutoRetry struct {
	NextAt  time.Time `json:"next_at"`
	Attempt int       `json:"attempt,omitempty"`
	Max     int       `json:"max,omitempty"`
}

// consumerItem is one assignment or level in the progress tree. Assignments and
// levels share this shape so the consumer and operator views can render the
// same component for a live job and for a finished record.
type consumerItem struct {
	Position        int            `json:"position,omitempty"`
	Title           string         `json:"title"`
	CourseName      string         `json:"course_name,omitempty"`
	Status          string         `json:"status"`
	Reason          string         `json:"reason,omitempty"`
	TotalLevels     int            `json:"total_levels,omitempty"`
	CompletedLevels int            `json:"completed_levels,omitempty"`
	SkippedLevels   int            `json:"skipped_levels,omitempty"`
	CurrentPosition int            `json:"current_position,omitempty"`
	CurrentTitle    string         `json:"current_title,omitempty"`
	Stage           string         `json:"stage,omitempty"`
	StartedAt       *time.Time     `json:"started_at,omitempty"`
	UpdatedAt       *time.Time     `json:"updated_at,omitempty"`
	Levels          []consumerItem `json:"levels,omitempty"`
}
type summaryItem struct {
	Position   int    `json:"position"`
	Title      string `json:"title"`
	CourseName string `json:"course_name"`
	Status     string `json:"status"`
	ReasonCode string `json:"reason_code"`
	Result     *struct {
		Status          string `json:"status"`
		ReasonCode      string `json:"reason_code"`
		TotalLevels     int    `json:"total_levels"`
		CompletedLevels int    `json:"completed_levels"`
		SkippedLevels   int    `json:"skipped_levels"`
		CurrentPosition int    `json:"current_position"`
		CurrentTitle    string `json:"current_title"`
		// Levels stays raw so the frequently polled list endpoint skips it and
		// only the single-job detail pays for the nested decode.
		Levels json.RawMessage `json:"levels"`
	} `json:"result"`
}

// consumerReasonCodes translates internal execution reasons into the small
// vocabulary the consumer API publishes. Internal codes are operator data and
// are never repeated verbatim: a user gets a stable category their page can
// explain, and an unmapped internal state degrades to service_followup rather
// than leaking a new code name.
var consumerReasonCodes = map[string]string{
	"already_passed": "already_passed", "skipped": "already_passed",
	"no_required_levels": "no_required_levels",
	"deadline_passed":    "deadline_passed",

	// Educoder could not give us a runtime or a reading right now. The user can
	// simply run it again later; this is not a staff hand-off.
	"resource_unavailable": "platform_busy", "runtime_initialization_failed": "platform_busy",
	"progress_unavailable": "platform_unavailable", "course_sync_failed": "platform_unavailable",
	"homework_sync_failed": "platform_unavailable",

	// A write whose result we cannot prove: the user must reconcile, not replay.
	"uncertain": "needs_review", "submission_uncertain": "needs_review",
	"verification_unconfirmed": "needs_review", "assignment_verification_unconfirmed": "needs_review",
	"assignment_roster_incomplete": "needs_review", "editor_changed": "needs_review",
	"selftest_stream_unconfirmed": "needs_review",

	// Explicitly before formal evaluation: safe to run again.
	"saved_not_submitted": "retry_later", "save_unverified": "retry_later", "save_error": "retry_later",
	"prewrite_error": "retry_later", "selftest_connection_failed": "retry_later",

	// We could not produce an answer that passes. Nothing for the user to fix.
	"model_error": "answer_unavailable", "model_timeout": "answer_unavailable",
	"model_network_error": "answer_unavailable", "model_rate_limited": "answer_unavailable",
	"model_provider_missing": "answer_unavailable", "model_provider_payment_required": "answer_unavailable",
	"invalid_output_json": "answer_unavailable", "model_response_invalid": "answer_unavailable",
	"model_output_truncated": "answer_unavailable", "generation_stalled": "answer_unavailable",
	"invalid_candidate": "answer_unavailable", "attempts_exhausted": "answer_unavailable",
	"selftest_failed": "answer_unavailable", "context_unavailable": "answer_unavailable",

	"public_contract_conflict": "question_conflict",

	"unsupported_homework": "unsupported", "unsupported_roster": "unsupported",
	"unsupported_multifile": "unsupported", "unsupported_question_kind": "unsupported",
	"task_identity_mismatch": "unsupported",

	"budget_exhausted": "quota_reached", "budget_unavailable": "quota_reached",
	"course_limit": "quota_reached", "homework_limit": "quota_reached",
	"time_limit": "quota_reached", "deadline_exceeded": "quota_reached", "context_deadline_exceeded": "quota_reached",

	"unchanged_failure_cooldown": "paused_cooldown", "blocker_state_unavailable": "paused_cooldown",

	"canceled":    "stopped",
	"interrupted": "interrupted", "service_restarted": "interrupted",

	"service_required": "service_followup", "execution_failed": "service_followup",
	"needs_service": "service_followup", "precheck_failed": "service_followup",
	"usage_persistence_failed": "service_followup", "verification_persistence_failed": "service_followup",
	"blocker_state_persistence_failed": "service_followup", "failed": "service_followup",
}

// consumerReasonVocabulary is the published category set, derived from the
// mapping so the two cannot drift. Mapping is idempotent: an already-published
// category (for example one read back from a child entry) passes through.
var consumerReasonVocabulary = func() map[string]bool {
	out := map[string]bool{}
	for _, value := range consumerReasonCodes {
		out[value] = true
	}
	return out
}()

func consumerReason(values ...string) string {
	for _, value := range values {
		code := strings.ToLower(strings.TrimSpace(value))
		if code == "" {
			continue
		}
		if consumerReasonVocabulary[code] {
			return code
		}
		if mapped, ok := consumerReasonCodes[code]; ok {
			return mapped
		}
		return "service_followup"
	}
	return ""
}

// consumerStatus maps every execution state onto the four states the consumer
// pages render. A parent that is no longer running cannot leave a child
// claiming to be in progress.
func consumerStatus(status, parentStatus string) string {
	out := "support"
	switch status {
	case "passed", "skipped", "completed", "succeeded", "skipped_optional", "optional":
		out = "completed"
	case "running", "processing":
		out = "processing"
	case "planned", "pending", "waiting", "":
		out = "waiting"
	}
	if out != "completed" && parentStatus != jobs.Running && parentStatus != jobs.Queued {
		return "support"
	}
	if out == "processing" && parentStatus == jobs.Queued {
		return "waiting"
	}
	return out
}

func consumerItems(items []summaryItem, parentStatus string, detail bool) []consumerItem {
	out := make([]consumerItem, 0, min(len(items), 100))
	for _, item := range items {
		if len(out) >= 100 {
			break
		}
		entry := consumerItem{Position: item.Position, Title: adminContextText(item.Title, 200), CourseName: adminContextText(item.CourseName, 200), Status: consumerStatus(item.Status, parentStatus)}
		reason := item.ReasonCode
		if batch := item.Result; batch != nil {
			if batch.ReasonCode != "" {
				reason = batch.ReasonCode
			}
			entry.TotalLevels, entry.CompletedLevels, entry.SkippedLevels = batch.TotalLevels, batch.CompletedLevels, batch.SkippedLevels
			entry.CurrentPosition, entry.CurrentTitle = batch.CurrentPosition, adminContextText(batch.CurrentTitle, 200)
			if detail && len(batch.Levels) > 0 {
				var levels []summaryItem
				if json.Unmarshal(batch.Levels, &levels) == nil {
					entry.Levels = consumerItems(levels, parentStatus, false)
				}
			}
		}
		if entry.Status != "completed" {
			entry.Reason = consumerReason(reason)
		} else if code := consumerReason(reason); code == "already_passed" || code == "no_required_levels" {
			// A completed assignment explains itself only when it was skipped.
			entry.Reason = code
		} else if item.Status == "skipped" || item.Status == "skipped_optional" {
			entry.Reason = "already_passed"
		}
		out = append(out, entry)
	}
	return out
}

type consumerProgress struct {
	TotalQuestions     int            `json:"total_questions,omitempty"`
	CompletedQuestions int            `json:"completed_questions,omitempty"`
	TotalHomeworks     int            `json:"total_homeworks"`
	CompletedHomeworks int            `json:"completed_homeworks"`
	SkippedHomeworks   int            `json:"skipped_homeworks,omitempty"`
	AttentionHomeworks int            `json:"attention_homeworks,omitempty"`
	TotalLevels        int            `json:"total_levels"`
	CompletedLevels    int            `json:"completed_levels"`
	CurrentTitle       string         `json:"current_title,omitempty"`
	CurrentPosition    int            `json:"current_position,omitempty"`
	CurrentLevel       string         `json:"current_level,omitempty"`
	Stage              string         `json:"stage,omitempty"`
	Homeworks          []consumerItem `json:"homeworks,omitempty"`
}

// consumerLiveItems projects the live snapshot the executor publishes. Levels
// are included only for the single-job detail endpoint.
func consumerLiveItems(items []jobs.HomeworkProgress, detail bool) []consumerItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]consumerItem, 0, min(len(items), 100))
	for _, item := range items {
		if len(out) >= 100 {
			break
		}
		entry := consumerItem{
			Title: item.Title, CourseName: item.CourseName, Status: consumerLiveStatus(item.Status), Reason: consumerReason(item.ReasonCode),
			TotalLevels: item.TotalLevels, CompletedLevels: item.CompletedLevels, SkippedLevels: item.SkippedLevels,
			CurrentPosition: item.CurrentPosition, CurrentTitle: item.CurrentTitle, Stage: item.Stage,
			StartedAt: item.StartedAt, UpdatedAt: item.UpdatedAt,
		}
		if entry.Title == "" {
			// A single-assignment job reports its levels before its own name is
			// resolved; the card above already names the target.
			entry.Title = "本次作业"
		}
		if detail {
			for _, level := range item.Levels {
				if len(entry.Levels) >= 100 {
					break
				}
				entry.Levels = append(entry.Levels, consumerItem{Position: level.Position, Title: level.Title, Status: consumerLiveStatus(level.Status)})
			}
		}
		out = append(out, entry)
	}
	return out
}

func consumerLiveStatus(status string) string {
	switch status {
	case "passed":
		return "completed"
	case "skipped":
		return "completed"
	case "running":
		return "processing"
	case "waiting":
		return "waiting"
	default:
		return "support"
	}
}

func toConsumerJob(j jobs.Job) consumerJob    { return consumerView(j, false) }
func toConsumerDetail(j jobs.Job) consumerJob { return consumerView(j, true) }

func consumerView(j jobs.Job, detail bool) consumerJob {
	status, message := "support", "本次处理已暂停，已完成进度保留，剩余内容待跟进"
	switch j.Status {
	case jobs.Queued:
		status, message = "waiting", "已接收，正在安排处理"
	case jobs.Running:
		status, message = "processing", "正在为你处理作业"
	case jobs.Succeeded:
		status, message = "completed", "已完成"
	case jobs.Canceled:
		status, message = "canceled", "本次处理已停止"
	}
	req := j.Request
	req.IdempotencyKey = ""
	out := consumerJob{ID: j.ID, Status: status, Phase: status, Kind: "service", Request: req, CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt, StartedAt: j.StartedAt, Message: message, CanRetry: canRetryJob(j), CanRecover: canRecoverJob(j), CanCancel: isRestartableRequest(req) && (j.Status == jobs.Queued || j.Status == jobs.Running)}
	if recoveryOriginalID(j) != "" {
		recovery := publicRecovery(j)
		out.Recovery = &recovery
		out.Message = recovery.Message
		req.RecoveryOf = ""
		out.Request = req
	}
	if req.Mode == "collect_verified" {
		out.Kind = "collection"
	}
	out.Result.ServiceStatus = status
	if status == "support" {
		var reason retryResultState
		if json.Unmarshal(j.Result, &reason) == nil && hasPublicContractConflict(reason) {
			out.Result.Attention = "upstream_question_conflict"
		}
	}
	// Decode a tiny typed projection; never copy the executor result map.
	var summary struct {
		Status             string             `json:"status"`
		ReasonCode         string             `json:"reason_code"`
		StopReason         string             `json:"stop_reason"`
		AutoRetry          *consumerAutoRetry `json:"auto_retry"`
		Title              string             `json:"title"`
		ExerciseID         int64              `json:"exercise_id"`
		TotalQuestions     int                `json:"total_questions"`
		CompletedQuestions int                `json:"completed_questions"`
		Submitted          bool               `json:"submitted"`
		ScoreKnown         bool               `json:"score_known"`
		Score              string             `json:"score"`
		MaxScore           string             `json:"max_score"`
		TotalHomeworks     int                `json:"total_homeworks"`
		CompletedHomeworks int                `json:"completed_homeworks"`
		TotalLevels        int                `json:"total_levels"`
		CompletedLevels    int                `json:"completed_levels"`
		Homeworks          []summaryItem      `json:"homeworks"`
		Levels             []summaryItem      `json:"levels"`
	}
	if json.Unmarshal(j.Result, &summary) == nil {
		out.Result.TotalHomeworks, out.Result.CompletedHomeworks = summary.TotalHomeworks, summary.CompletedHomeworks
		out.Result.TotalLevels, out.Result.CompletedLevels = summary.TotalLevels, summary.CompletedLevels
		out.Result.Homeworks, out.Result.Levels = consumerItems(summary.Homeworks, j.Status, detail), consumerItems(summary.Levels, j.Status, false)
		out.Result.AutoRetry = publicAutoRetry(summary.AutoRetry)
		if status == "support" || status == "canceled" {
			// The reason comes from the public result only. ErrorCode is an
			// operator field and never crosses this boundary. A specific cause
			// beats the generic batch verdict: "a classroom needs service" says
			// nothing a user can act on, "Educoder is busy" does.
			child := firstAttentionReason(out.Result.Homeworks, out.Result.Levels)
			out.Result.Reason = consumerReason(specificReason(summary.ReasonCode), specificReason(summary.StopReason), child, summary.ReasonCode, summary.StopReason, summary.Status)
		}
	}
	if req.Scope == "exercise" {
		out.Kind = "exercise"
		out.Result.Title = adminContextText(summary.Title, 500)
		out.Result.ExerciseID = req.ExerciseID
		out.Result.TotalQuestions, out.Result.CompletedQuestions = summary.TotalQuestions, summary.CompletedQuestions
		out.Result.Submitted, out.Result.ScoreKnown = summary.Submitted, summary.ScoreKnown
		if summary.ScoreKnown {
			out.Result.Score = adminContextText(summary.Score, 80)
			out.Result.MaxScore = adminContextText(summary.MaxScore, 80)
		}
		if summary.Submitted {
			out.Status, out.Phase, out.Message = "submitted", "submitted", "已交卷，成绩以头歌公布为准"
			if summary.ScoreKnown {
				out.Status, out.Phase, out.Message = "graded", "graded", "考试成绩已公布"
			}
		} else if j.Status == jobs.Succeeded {
			// An exercise's successful executor response cannot manufacture a
			// confirmed submission or full-score result.
			out.Status, out.Phase, out.Message = "support", "support", "考试状态正在核对"
		}
		out.Result.ServiceStatus = out.Status
	}
	if p := j.Progress; p != nil {
		if out.Result.TotalHomeworks == 0 {
			out.Result.TotalHomeworks, out.Result.CompletedHomeworks = p.TotalHomeworks, p.CompletedHomeworks
		}
		if out.Result.TotalLevels == 0 {
			out.Result.TotalLevels, out.Result.CompletedLevels = p.TotalLevels, p.CompletedLevels
		}
		title := p.CurrentHomeworkTitle
		if title == "" {
			title = p.CurrentTitle
		}
		out.Result.CurrentTitle = strings.TrimSpace(title)
		out.Progress = &consumerProgress{TotalHomeworks: out.Result.TotalHomeworks, CompletedHomeworks: out.Result.CompletedHomeworks, SkippedHomeworks: p.SkippedHomeworks, AttentionHomeworks: p.NeedsServiceHomeworks, TotalLevels: out.Result.TotalLevels, CompletedLevels: out.Result.CompletedLevels, CurrentTitle: out.Result.CurrentTitle}
		if j.Status == jobs.Running || j.Status == jobs.Queued {
			out.Progress.CurrentPosition, out.Progress.CurrentLevel = p.CurrentPosition, strings.TrimSpace(p.CurrentTitle)
			out.Progress.Stage = p.Stage
			out.Progress.Homeworks = consumerLiveItems(p.Homeworks, detail)
		}
		if req.Scope == "exercise" {
			if out.Result.TotalQuestions == 0 {
				out.Result.TotalQuestions, out.Result.CompletedQuestions = p.TotalQuestions, p.CompletedQuestions
			}
			out.Progress.TotalQuestions, out.Progress.CompletedQuestions = out.Result.TotalQuestions, out.Result.CompletedQuestions
			if out.Result.Title == "" {
				out.Result.Title = adminContextText(p.CurrentExerciseTitle, 500)
			}
		}
	}
	return out
}

// publicAutoRetry keeps only a schedule that is both parseable and plausible.
// The page renders a promise to the user, so an implausible value is dropped.
func publicAutoRetry(value *consumerAutoRetry) *consumerAutoRetry {
	if value == nil || value.NextAt.IsZero() || value.Attempt < 0 || value.Attempt > 100 || value.Max < 0 || value.Max > 100 {
		return nil
	}
	if value.Max > 0 && value.Attempt > value.Max {
		return nil
	}
	out := *value
	out.NextAt = out.NextAt.UTC()
	return &out
}

func firstAttentionReason(groups ...[]consumerItem) string {
	for _, items := range groups {
		for _, item := range items {
			if item.Status != "completed" && item.Reason != "" && item.Reason != "service_followup" {
				return item.Reason
			}
		}
	}
	return ""
}

// specificReason drops the batch-level verdicts that only restate "this job
// did not finish". They are kept as a last resort, after any child cause.
func specificReason(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "service_required", "execution_failed", "needs_service", "failed", "":
		return ""
	}
	return value
}

func hasPublicContractConflict(result retryResultState) bool {
	if result.Status == "public_contract_conflict" || result.ReasonCode == "public_contract_conflict" {
		return true
	}
	if result.Result != nil && hasPublicContractConflict(*result.Result) {
		return true
	}
	for _, child := range result.Homeworks {
		if hasPublicContractConflict(child) {
			return true
		}
	}
	for _, child := range result.Levels {
		if hasPublicContractConflict(child) {
			return true
		}
	}
	return false
}

func publicRecovery(j jobs.Job) consumerRecovery {
	state, message := "failed", "复核后仍未完成，已保留进度，可再次复核"
	if j.Status == jobs.Queued {
		state, message = "queued", "复核任务已排队，尚未开始新的提交"
	} else if j.Status == jobs.Running {
		switch j.Phase {
		case "reconciling":
			state, message = "reconciling", "正在只读复核上次提交结果，尚未开始新的提交"
		case "loading", "starting":
			state, message = "continuing", "复核已通过，正在继续剩余内容"
		default:
			state, message = "continuing", "复核已通过，正在继续剩余内容"
		}
	} else if j.Status == jobs.Succeeded {
		state, message = "completed", "复核完成，剩余内容已继续处理"
	} else if j.Status == jobs.Canceled {
		state, message = "stopped", "复核任务已停止，已完成进度保留"
	} else {
		var result struct {
			ReasonCode string `json:"reason_code"`
		}
		if json.Unmarshal(j.Result, &result) == nil && result.ReasonCode == "recovery_recheck_required" {
			state, message = "blocked", "复核未通过，未发送新的提交；可再次复核"
		}
	}
	return consumerRecovery{OriginalID: recoveryOriginalID(j), State: state, Message: message}
}

func recoveryOriginalID(j jobs.Job) string {
	if j.Request.RecoveryOf != "" {
		return j.Request.RecoveryOf
	}
	// v0.1.65 children predate RecoveryOf. The server-owned key and persisted
	// reconciliation failure together identify their read-only recovery path.
	id, ok := strings.CutPrefix(j.Request.IdempotencyKey, "recover-job:")
	if !ok || len(id) != 32 || strings.Trim(id, "0123456789abcdef") != "" {
		return ""
	}
	var result struct {
		ReasonCode string `json:"reason_code"`
	}
	if json.Unmarshal(j.Result, &result) != nil || result.ReasonCode != "recovery_recheck_required" {
		return ""
	}
	return id
}
