package taskservice

import (
	"context"

	"educoder-cli/internal/solver"
)

type programmingRecoveryRunner interface {
	Run(context.Context, solver.Adapter, solver.Request) (*solver.Result, error)
	ContinueRun(context.Context, solver.Adapter, solver.Request, string, solver.Continuation) (*solver.Result, error)
}

// A single run owns the complete per-level budget. Historical repair-round
// configuration must not grant more calls or evaluations automatically.
func runProgrammingRecovery(ctx context.Context, runner programmingRecoveryRunner, adapter solver.Adapter, req solver.Request, rounds int, admit func() (bool, error)) (*solver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return runner.Run(ctx, adapter, req)
}
