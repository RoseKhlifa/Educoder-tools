package solver

import (
	"strings"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
)

// normalizeSingleFileCRLF matches the single-file browser editor's newline
// representation before hashing or saving a candidate. It never changes the
// byte-sensitive bank/hash primitives, and does not affect workspace scripts or
// graphic payloads. The input slice stays untouched so historical source and
// its verification evidence remain bound to their original bytes.
func normalizeSingleFileCRLF(snap problem.Snapshot, candidate answer.Candidate) (answer.Candidate, bool) {
	if snap.EditMode != "single_file" || len(candidate.Files) != 1 || !strings.Contains(candidate.Files[0].Content, "\r\n") {
		return candidate, false
	}
	candidate.Files = append([]problem.SolutionFile(nil), candidate.Files...)
	candidate.Files[0].Content = strings.ReplaceAll(candidate.Files[0].Content, "\r\n", "\n")
	return candidate, true
}
