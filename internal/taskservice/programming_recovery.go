package taskservice

import (
	"context"

	"educoder-cli/internal/solver"
)

type programmingRecoveryRunner interface {
	Run(context.Context, solver.Adapter, solver.Request) (*solver.Result, error)
}

// A single run owns the complete per-level budget. Historical continuation
// rounds are intentionally not part of the task-service path: retrying a
// programming level is an explicit new task, not an automatic budget grant.
func runProgrammingRecovery(ctx context.Context, runner programmingRecoveryRunner, adapter solver.Adapter, req solver.Request) (*solver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return runner.Run(ctx, adapter, req)
}
