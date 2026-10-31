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
	ID        string            `json:"id"`
	Status    string            `json:"status"`
	Phase     string            `json:"phase"`
	Kind      string            `json:"kind"`
	Request   jobs.Request      `json:"request"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
	Message   string            `json:"message"`
	Result    consumerResult    `json:"result"`
	Progress  *consumerProgress `json:"progress,omitempty"`
}
type consumerResult struct {
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

func consumerItems(items []summaryItem) []consumerItem {
	out := make([]consumerItem, 0, len(items))
	for _, item := range items {
		status := "waiting"
		switch item.Status {
		case "passed", "skipped", "completed":
			status = "completed"
		case "running":
			status = "processing"
		case "planned", "pending":
			status = "waiting"
		default:
			status = "support"
		}
		out = append(out, consumerItem{Title: item.Title, Status: status})
	}
	return out
}

type consumerProgress struct {
	TotalHomeworks     int    `json:"total_homeworks"`
	CompletedHomeworks int    `json:"completed_homeworks"`
	TotalLevels        int    `json:"total_levels"`
	CompletedLevels    int    `json:"completed_levels"`
	CurrentTitle       string `json:"current_title,omitempty"`
}

func toConsumerJob(j jobs.Job) consumerJob {
	status, message := "support", "服务处理中，管理员将协助完成"
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
	out := consumerJob{ID: j.ID, Status: status, Phase: status, Kind: "service", Request: req, CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt, Message: message}
	if req.Mode == "collect_verified" {
		out.Kind = "collection"
	}
	out.Result.ServiceStatus = status
	// Decode a tiny typed projection; never copy the executor result map.
	var summary struct {
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
		out.Result.Homeworks, out.Result.Levels = consumerItems(summary.Homeworks), consumerItems(summary.Levels)
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
	}
	return out
}
