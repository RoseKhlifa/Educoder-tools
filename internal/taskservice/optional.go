package taskservice

import (
	"educoder-cli/internal/educoder"
	"errors"
)

// Only explicit upstream optional metadata changes admission. In particular,
// zero scores, missing scores and unsupported question kinds remain required.
func optionalResult(result *Result, err error) (*Result, error) {
	if !errors.Is(err, educoder.ErrOptionalTask) {
		return result, err
	}
	if result == nil {
		result = &Result{Kind: "service"}
	}
	result.Status = "skipped_optional"
	result.Diagnostics = nil
	return result, nil
}

func batchLevelIndex(result *BatchResult, id int64) int {
	for i := range result.Levels {
		if result.Levels[i].ChallengeID == id {
			return i
		}
	}
	return -1
}

// Remove an optional target rather than presenting it as completed or pending.
// Aggregate costs remain intact if optional metadata changed during execution.
func omitBatchLevel(result *BatchResult, index int) {
	level := result.Levels[index]
	if level.Status == "skipped" {
		result.SkippedLevels--
		result.CompletedLevels--
	} else if level.Status == "passed" {
		result.CompletedLevels--
	}
	result.Levels = append(result.Levels[:index], result.Levels[index+1:]...)
	result.TotalLevels--
}

func progressOptional(progress *educoder.HomeworkProgress, id int64) bool {
	if progress == nil {
		return false
	}
	for _, level := range progress.Challenges {
		if level.ChallengeID == id {
			return level.IsOptional()
		}
	}
	return false
}

// The full upstream assignment roster must still be present and unambiguous.
// Optional entries are omitted from service counts, not from identity checks.
func requiredHomeworkCount(hw educoder.Homework, progress *educoder.HomeworkProgress) (int, bool) {
	if progress == nil || hw.ChallengeCount < 1 || hw.ChallengeCount > 100 || len(progress.Challenges) != hw.ChallengeCount {
		return 0, false
	}
	seen := map[int64]bool{}
	required := 0
	for _, level := range progress.Challenges {
		if level.ChallengeID <= 0 || seen[level.ChallengeID] || level.Name == "" {
			return 0, false
		}
		seen[level.ChallengeID] = true
		if !level.IsOptional() {
			required++
		}
	}
	return required, true
}
