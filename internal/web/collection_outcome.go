package web

import (
	"context"
	"encoding/json"
	"errors"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/jobs"
	"educoder-cli/internal/taskservice"
)

// A bounded scan can keep useful verified answers without completing the whole
// account. Preserve that distinction, and never persist upstream error strings.
func collectionOutcome(summary taskservice.CollectionSummary, err error) (jobs.Outcome, error) {
	result := struct {
		taskservice.CollectionSummary
		ScanComplete bool   `json:"scan_complete"`
		StopReason   string `json:"stop_reason,omitempty"`
	}{CollectionSummary: summary, ScanComplete: err == nil && !summary.LimitReached && summary.Failed == 0}
	switch {
	case errors.Is(err, context.Canceled):
		result.StopReason = "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		result.StopReason = "time_limit"
	case errors.Is(err, educoder.ErrCollectionLimit):
		result.StopReason = "read_budget_limit"
	case err != nil || summary.Failed > 0:
		result.StopReason = "collection_stopped"
	case summary.LimitReached:
		result.StopReason = "scan_limit"
	}
	raw, _ := json.Marshal(result)
	out := jobs.Outcome{Status: jobs.Succeeded, Result: raw, Message: taskservice.CollectionMessage(summary)}
	bounded := summary.LimitReached && summary.Failed == 0 && (err == nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, educoder.ErrCollectionLimit))
	switch {
	case errors.Is(err, context.Canceled):
		out.Status, out.ErrorCode, out.Message = jobs.Canceled, "collection_canceled", "采集已停止，已保存本次结果"
	case bounded:
		out.Message = "本次采集达到上限，已保存结果；尚未扫描全部内容"
	case err != nil || summary.Failed > 0:
		out.Status, out.ErrorCode, out.Message = jobs.Failed, "collection_stopped", "采集存在读取或保存异常，已保留结果，请查看原因明细"
	}
	// Returning an already classified outcome prevents the jobs manager from
	// replacing it with execution_failed. Parent cancellation/timeouts still
	// override the outcome in the manager, as with every other executor.
	return out, nil
}
