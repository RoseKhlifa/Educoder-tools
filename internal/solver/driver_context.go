package solver

import (
	"strings"

	"educoder-cli/internal/problem"
)

// A technical read failure is not evidence about source correctness. Preserve
// candidate revalidation, but do not pay for generation with silently lost
// caller data. A legitimately absent optional driver retains legacy behavior.
func headerContextUnavailable(snap problem.Snapshot) bool {
	if snap.ReadOnlyContext == nil || snap.ReadOnlyContext.Status != "unavailable" || len(snap.ReadOnlyFiles) > 0 || len(snap.Files) == 0 {
		return false
	}
	for _, file := range snap.Files {
		switch strings.ToLower(file.Extension) {
		case ".h", ".hpp", ".hh", ".hxx":
		default:
			return false
		}
	}
	return true
}
