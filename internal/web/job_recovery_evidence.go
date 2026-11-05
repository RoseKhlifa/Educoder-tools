package web

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"educoder-cli/internal/jobs"
	"educoder-cli/internal/problem"
)

func (s *Server) preformalRecoveryEvidence(owner string, original jobs.Job) func(context.Context, string, int64, int64) (bool, error) {
	return func(ctx context.Context, course string, homeworkID, challengeID int64) (bool, error) {
		if s.jobs.bank == nil {
			return false, nil
		}
		root := original
		seen := map[string]bool{}
		for depth := 0; ; depth++ {
			var result struct {
				ReasonCode string `json:"reason_code"`
			}
			if json.Unmarshal(root.Result, &result) != nil || result.ReasonCode != "recovery_recheck_required" {
				break
			}
			parent := root.Request.RecoveryOf
			if parent == "" {
				parent = strings.TrimPrefix(root.Request.IdempotencyKey, "recover-job:")
				if parent == root.Request.IdempotencyKey {
					parent = ""
				}
			}
			if parent == "" {
				break
			}
			if depth >= 48 || seen[parent] {
				return false, nil
			}
			seen[parent] = true
			previous, err := s.jobs.manager.Get(ctx, owner, parent)
			if err != nil {
				return false, nil
			}
			if previous.Request.Mode != root.Request.Mode || previous.Request.Scope != root.Request.Scope || previous.Request.Course != root.Request.Course || previous.Request.HomeworkID != root.Request.HomeworkID {
				return false, nil
			}
			root = previous
		}
		if root.Request.IdempotencyKey == "" || challengeID <= 0 {
			return false, nil
		}
		key := root.Request.IdempotencyKey
		if root.Request.Scope == "course" || root.Request.Scope == "all" {
			hash := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%d", key, course, homeworkID)))
			key = fmt.Sprintf("course-%x", hash)
		} else if root.Request.Scope != "homework" {
			return false, nil
		}
		key = fmt.Sprintf("%s:%d", key, challengeID)
		allowed, err := s.jobs.bank.PreformalProgrammingFailure(ctx, problem.HashContent(owner), key, course, homeworkID)
		if err != nil || allowed {
			return allowed, err
		}
		return s.jobs.bank.PreformalGraphicFailure(ctx, problem.HashContent(owner), key, course, homeworkID)
	}
}
