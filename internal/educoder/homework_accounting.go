package educoder

import (
	"context"
	"strings"
)

// RequiredHomeworkCounts is shared by execution and browser summaries. An
// incomplete or ambiguous score roster cannot prove zero work or completion.
// A catalog/header subset needs separate exact header coverage, so this helper
// deliberately returns unknown when given catalog counts without that proof.
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

// AssignedHomeworkCounts projects already-read progress onto the same canonical
// assignment as HomeworkRoster, without reading runtime games or creating an
// environment. Invalid or unavailable header evidence is unknown, never 0/0.
// Callers can retain RequiredHomeworkCounts as the no-header complete fast path.
func (c *Client) AssignedHomeworkCounts(ctx context.Context, course string, hw Homework, progress *HomeworkProgress) (required, completed int, valid bool) {
	if progress == nil || hw.ChallengeCount < 1 || hw.ChallengeCount > 100 {
		return 0, 0, false
	}
	header, err := c.readHomeworkHeader(ctx, course, hw)
	if err != nil {
		return 0, 0, false
	}
	canonical, err := canonicalHomeworkAssignment(hw, progress, header)
	if err != nil {
		return 0, 0, false
	}
	for _, level := range canonical {
		if !level.IsOptional() {
			required++
			if level.HasFullScore() {
				completed++
			}
		}
	}
	return required, completed, true
}

// CanContinueAfterConfirmedFailure is capability evidence, not a completion
// claim. Missing flags, false flags and nonboolean JSON never authorize skip.
func (t *Task) CanContinueAfterConfirmedFailure() bool {
	return t != nil && t.SkipLevel != nil && *t.SkipLevel && t.Shixun.TaskPass != nil && *t.Shixun.TaskPass
}
