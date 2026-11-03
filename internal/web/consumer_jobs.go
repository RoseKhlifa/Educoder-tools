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
	Title              string         `json:"title,omitempty"`
	ExerciseID         int64          `json:"exercise_id,omitempty"`
	TotalQuestions     int            `json:"total_questions,omitempty"`
	CompletedQuestions int            `json:"completed_questions,omitempty"`
	Submitted          bool           `json:"submitted,omitempty"`
	ScoreKnown         bool           `json:"score_known,omitempty"`
	Score              string         `json:"score,omitempty"`
	MaxScore           string         `json:"max_score,omitempty"`
	ServiceStatus      string         `json:"service_status"`
	TotalHomeworks     int            `json:"total_homeworks"`
	CompletedHomeworks int            `json:"completed_homeworks"`
	TotalLevels        int            `json:"total_levels"`
	CompletedLevels    int            `json:"completed_levels"`
	CurrentTitle       string         `json:"current_title,omitempty"`
	Homeworks          []consumerItem `json:"homeworks,omitempty"`
	Levels             []consumerItem `json:"levels,omitempty"`
}
type consumerItem struct {
	Title  string `json:"title"`
	Status string `json:"status"`
}
type summaryItem struct {
	Title  string `json:"title"`
	Status string `json:"status"`
}

func consumerItems(items []summaryItem, parentStatus string) []consumerItem {
	out := make([]consumerItem, 0, len(items))
	for _, item := range items {
		status := "waiting"
		switch item.Status {
		case "passed", "skipped", "completed", "succeeded":
			status = "completed"
		case "running":
			status = "processing"
		case "planned", "pending":
			status = "waiting"
		default:
			status = "support"
		}
		if status != "completed" && parentStatus != jobs.Running && parentStatus != jobs.Queued {
			status = "support"
		} else if status == "processing" && parentStatus == jobs.Queued {
			status = "waiting"
		}
		out = append(out, consumerItem{Title: item.Title, Status: status})
	}
	return out
}

type consumerProgress struct {
	TotalQuestions     int    `json:"total_questions,omitempty"`
	CompletedQuestions int    `json:"completed_questions,omitempty"`
	TotalHomeworks     int    `json:"total_homeworks"`
	CompletedHomeworks int    `json:"completed_homeworks"`
	TotalLevels        int    `json:"total_levels"`
	CompletedLevels    int    `json:"completed_levels"`
	CurrentTitle       string `json:"current_title,omitempty"`
}

func toConsumerJob(j jobs.Job) consumerJob {
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
	out := consumerJob{ID: j.ID, Status: status, Phase: status, Kind: "service", Request: req, CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt, Message: message, CanRetry: canRetryJob(j), CanRecover: canRecoverJob(j), CanCancel: isRestartableRequest(req) && (j.Status == jobs.Queued || j.Status == jobs.Running)}
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
	// Decode a tiny typed projection; never copy the executor result map.
	var summary struct {
		Status             string        `json:"status"`
		Title              string        `json:"title"`
		ExerciseID         int64         `json:"exercise_id"`
		TotalQuestions     int           `json:"total_questions"`
		CompletedQuestions int           `json:"completed_questions"`
		Submitted          bool          `json:"submitted"`
		ScoreKnown         bool          `json:"score_known"`
		Score              string        `json:"score"`
		MaxScore           string        `json:"max_score"`
		TotalHomeworks     int           `json:"total_homeworks"`
		CompletedHomeworks int           `json:"completed_homeworks"`
		TotalLevels        int           `json:"total_levels"`
		CompletedLevels    int           `json:"completed_levels"`
		Homeworks          []summaryItem `json:"homeworks"`
		Levels             []summaryItem `json:"levels"`
	}
	if json.Unmarshal(j.Result, &summary) == nil {
		out.Result.TotalHomeworks, out.Result.CompletedHomeworks = summary.TotalHomeworks, summary.CompletedHomeworks
		out.Result.TotalLevels, out.Result.CompletedLevels = summary.TotalLevels, summary.CompletedLevels
		out.Result.Homeworks, out.Result.Levels = consumerItems(summary.Homeworks, j.Status), consumerItems(summary.Levels, j.Status)
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
		out.Progress = &consumerProgress{TotalHomeworks: out.Result.TotalHomeworks, CompletedHomeworks: out.Result.CompletedHomeworks, TotalLevels: out.Result.TotalLevels, CompletedLevels: out.Result.CompletedLevels, CurrentTitle: out.Result.CurrentTitle}
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
