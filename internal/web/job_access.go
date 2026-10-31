package web

import (
	"context"
	"strings"
	"time"

	"educoder-cli/internal/jobs"
)

func (s *Server) ownerCanRun(ctx context.Context, owner string) bool {
	if s.platform == nil {
		return true
	}
	id, ok := strings.CutPrefix(owner, "platform:")
	if !ok || id == "" {
		return false
	}
	u, err := s.platform.LookupUser(ctx, id)
	if err != nil {
		return false
	}
	allowed, err := s.platform.CanAccess(ctx, u)
	return err == nil && allowed
}

// Jobs outlive browser sessions. Recheck the persisted grant independently of
// that session so logout cannot leave disabled/expired-card jobs running.
func (s *Server) runAuthorized(ctx context.Context, owner string, run func(context.Context) (jobs.Outcome, error)) (jobs.Outcome, error) {
	if !s.ownerCanRun(ctx, owner) {
		return jobs.Outcome{Status: jobs.Failed, ErrorCode: "ACCESS_REVOKED", Message: "服务权限已更新"}, nil
	}
	if s.platform == nil {
		return run(ctx)
	}
	execCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-execCtx.Done():
				return
			case <-ticker.C:
				if !s.ownerCanRun(execCtx, owner) {
					cancel()
					return
				}
			}
		}
	}()
	return run(execCtx)
}
