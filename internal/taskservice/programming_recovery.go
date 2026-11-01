package taskservice

import (
	"context"
	"fmt"
	"strings"

	"educoder-cli/internal/solver"
)

type programmingRecoveryRunner interface {
	Run(context.Context, solver.Adapter, solver.Request) (*solver.Result, error)
	ContinueRun(context.Context, solver.Adapter, solver.Request, string, solver.Continuation) (*solver.Result, error)
}

// Continue the same durable run after a confirmed code failure. Feedback,
// rejected hashes, charged usage and unresolved reservations stay attached to
// it; the assignment's time and total money budget are never reset.
func runProgrammingRecovery(ctx context.Context, runner programmingRecoveryRunner, adapter solver.Adapter, req solver.Request, rounds int, admit func() (bool, error)) (*solver.Result, error) {
	r, err := runner.Run(ctx, adapter, req)
	for round := 1; round < rounds && req.Mode == solver.ModeSubmit && err == nil && recoverableProgrammingFailure(r, req.BudgetMicroUSD); round++ {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		allowed, checkErr := admit()
		if checkErr != nil {
			return r, checkErr
		}
		if !allowed {
			r.Status = "deadline_passed"
			r.Diagnostics = append(r.Diagnostics, "作业已截止或已不在当前课堂，停止后续修复")
			return r, nil
		}
		if req.OnProgress != nil {
			req.OnProgress(solver.Stage{Phase: "repairing", Message: fmt.Sprintf("正在继续第 %d 轮自动修复，保留原有失败反馈与已完成进度", round+1)})
		}
		next, nextErr := runner.ContinueRun(ctx, adapter, req, r.RunID, solver.Continuation{AdditionalModelCalls: req.MaxModelCalls, AdditionalEvaluations: req.MaxEvaluations})
		if next != nil {
			r = next
		}
		err = nextErr
	}
	return r, err
}

func recoverableProgrammingFailure(r *solver.Result, budget int64) bool {
	if r == nil || r.RunID == "" || budget <= 0 || r.CostMicroUSD+r.ReservedMicroUSD >= budget || r.Evaluation != nil && !r.Evaluation.Completed {
		return false
	}
	switch r.Status {
	case "selftest_failed", "attempts_exhausted":
		return true
	case "budget_exhausted":
		for _, diagnostic := range r.Diagnostics {
			if strings.TrimSpace(diagnostic) == "model call budget exhausted" {
				return true
			}
		}
	}
	return false
}
