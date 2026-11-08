package taskservice

import (
	"errors"
	"fmt"

	"educoder-cli/internal/educoder"
)

// A runtime/header is a locator, not proof of the complete assignment. Every
// required identity from a strict, complete official progress snapshot must be
// present in the plan. Explicitly optional rows may leave it; a missing row or
// a new required identity can never turn a smaller plan into complete work.
func homeworkPlanCoverage(hw educoder.Homework, progress *educoder.HomeworkProgress, plan []BatchLevel) error {
	if _, _, valid := educoder.RequiredHomeworkCounts(hw, progress); !valid {
		return errors.New("official assignment progress is incomplete or ambiguous")
	}
	current := make(map[int64]educoder.ChallengeProgress, len(progress.Challenges))
	for _, level := range progress.Challenges {
		current[level.ChallengeID] = level
	}
	planned := make(map[int64]bool, len(plan))
	for _, level := range plan {
		if level.ChallengeID <= 0 || planned[level.ChallengeID] {
			return errors.New("assignment plan contains an invalid or duplicate challenge identity")
		}
		if _, found := current[level.ChallengeID]; !found {
			return fmt.Errorf("planned challenge %d is absent from current assignment progress", level.ChallengeID)
		}
		planned[level.ChallengeID] = true
	}
	for _, level := range progress.Challenges {
		if !level.IsOptional() && !planned[level.ChallengeID] {
			return fmt.Errorf("required challenge %d is absent from the assignment plan", level.ChallengeID)
		}
	}
	return nil
}
