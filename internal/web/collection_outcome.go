package web

import (
	"context"
	"encoding/json"
	"errors"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/jobs"
	"educoder-cli/internal/taskservice"
)

// Only exhausted enumeration can be complete. Partial and historical explicitly
// bounded scans keep their useful verified answers without masquerading as full
// account collection; upstream error strings are never persisted.
func collectionOutcome(summary taskservice.CollectionSummary, err error) (jobs.Outcome, error) {
	result := struct {
		taskservice.CollectionSummary
		ScanComplete bool   `json:"scan_complete"`
		StopReason   string `json:"stop_reason,omitempty"`
	}{CollectionSummary: summary, ScanComplete: summary.ScanExhausted && err == nil && !summary.LimitReached && summary.Failed == 0}
	switch {
	case errors.Is(err, context.Canceled):
		result.StopReason = "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		result.StopReason = "request_timeout"
		if summary.LimitReached {
			result.StopReason = "time_limit"
		}
	case errors.Is(err, educoder.ErrCollectionLimit):
		result.StopReason = "read_budget_limit"
	case err != nil || summary.Failed > 0:
		result.StopReason = "collection_stopped"
	case summary.LimitReached:
		result.StopReason = "scan_limit"
	case !summary.ScanExhausted:
		result.StopReason = "scan_incomplete"
	}
	raw, _ := json.Marshal(result)
	out := jobs.Outcome{Status: jobs.Succeeded, Result: raw, Message: taskservice.CollectionMessage(summary)}
	bounded := summary.LimitReached && summary.Failed == 0 && (err == nil || errors.Is(err, educoder.ErrCollectionLimit))
	switch {
	case errors.Is(err, context.Canceled):
		out.Status, out.ErrorCode, out.Message = jobs.Canceled, "collection_canceled", "采集已停止，已保存本次结果"
	case bounded:
		out.Message = "本次采集达到上限，已保存结果；尚未扫描全部内容"
	case errors.Is(err, context.DeadlineExceeded):
		out.Status, out.ErrorCode, out.Message = jobs.Failed, "collection_stopped", "采集请求超时，已保留结果；尚未扫描全部内容"
	case err != nil || summary.Failed > 0:
		out.Status, out.ErrorCode, out.Message = jobs.Failed, "collection_stopped", "采集存在读取或保存异常，已保留结果，请查看原因明细"
	case !summary.ScanExhausted:
		out.Status, out.ErrorCode, out.Message = jobs.Failed, "collection_incomplete", "采集尚未完成全部扫描，已保留结果"
	}
	// Returning an already classified outcome prevents the jobs manager from
	// replacing it with execution_failed. Parent cancellation/timeouts still
	// override the outcome in the manager, as with every other executor.
	return out, nil
}
