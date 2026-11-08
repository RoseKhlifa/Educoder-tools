package educoder

import (
	"context"
	"strings"
)

// RequiredHomeworkCounts is shared by execution and browser summaries. An
// incomplete or ambiguous score roster cannot prove zero work or completion.
// Header challenges may omit objective levels; only a complete homework-specific
// progress roster matching the catalog count proves the assignment denominator.
func RequiredHomeworkCounts(hw Homework, progress *HomeworkProgress) (required, completed int, valid bool) {
	if progress == nil || hw.ChallengeCount < 1 || hw.ChallengeCount > 100 || len(progress.Challenges) != hw.ChallengeCount {
		return 0, 0, false
	}
	ids, names := map[int64]bool{}, map[string]bool{}
	for _, level := range progress.Challenges {
		name := strings.TrimSpace(level.Name)
		if level.ChallengeID <= 0 || name == "" || ids[level.ChallengeID] || names[name] {
			return 0, 0, false
		}
		ids[level.ChallengeID], names[name] = true, true
		if !level.IsOptional() {
			required++
			if level.HasFullScore() {
				completed++
			}
		}
	}
	return required, completed, true
}

// AssignedHomeworkCounts retains the optional reader interface while applying
// the complete homework-specific progress/catalog proof. It makes no requests:
// header challenges are a code subset, not an authoritative assignment roster.
func (c *Client) AssignedHomeworkCounts(ctx context.Context, course string, hw Homework, progress *HomeworkProgress) (required, completed int, valid bool) {
	return RequiredHomeworkCounts(hw, progress)
}

// CanContinueAfterConfirmedFailure is capability evidence, not a completion
// claim. Missing flags, false flags and nonboolean JSON never authorize skip.
func (t *Task) CanContinueAfterConfirmedFailure() bool {
	return t != nil && t.SkipLevel != nil && *t.SkipLevel && t.Shixun.TaskPass != nil && *t.Shixun.TaskPass
}
