package web

import (
	"encoding/json"

	"educoder-cli/internal/jobs"
	"educoder-cli/internal/taskservice"
)

func exerciseOutcome(result *taskservice.ExerciseResult, err error) (jobs.Outcome, error) {
	if result == nil {
		return jobs.Outcome{}, err
	}
	raw, _ := json.Marshal(result)
	out := jobs.Outcome{Status: jobs.Failed, Result: raw, ErrorCode: result.ReasonCode, Message: "考试尚未确认交卷，请查看处理状态"}
	if out.ErrorCode == "" {
		out.ErrorCode = "exercise_incomplete"
	}
	if result.Submitted {
		out.Status, out.ErrorCode, out.Message = jobs.Succeeded, "", "已交卷，成绩以头歌公布为准"
	} else if result.Status == "uncertain" || result.ReasonCode == "submission_uncertain" || result.Status == "verification_unconfirmed" {
		out.Status, out.Message = jobs.Uncertain, "考试保存或交卷状态待核对，已保留执行记录"
	}
	// The typed result retains a public reason. Raw upstream/provider errors
	// must not replace it or leak answer paths into consumer responses.
	return out, nil
}
