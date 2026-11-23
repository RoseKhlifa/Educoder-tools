package solver

import (
	"regexp"

	"educoder-cli/internal/problem"
)

// Educoder marks the region a student writes with comment banners such as
// "#********** Begin **********#". A level may also show a reference file in
// the editor, for example the saved HTML page a scraping exercise parses. That
// page has no answer region, and treating it as editable forced the model to
// reproduce 17 KB of HTML in its answer (truncated at the output limit) and
// failed the contract check because Python and HTML were mixed (production,
// 2026-11-21, course P27H8GVG).
var answerRegionMarker = regexp.MustCompile(`(?i)\*{3,}\s*(begin|end)\s*\*{3,}`)

// Limits mirror problem.prepareReadOnlyContext so a reference never makes a
// whole snapshot invalid; one that does not fit is left out of the context.
const (
	maxReferenceFiles     = 8
	maxReferenceFileBytes = 64 << 10
	maxReferenceBytes     = 128 << 10
)

// splitReferenceFiles keeps only the answer files editable when the editor
// mixes languages and at least one file carries an answer region. Unmarked
// files become read-only context for the model. A set that is uniform, has no
// marked file, or would still mix languages is returned unchanged.
func splitReferenceFiles(files []problem.File, readOnly []problem.ReadOnlyFile) ([]problem.File, []problem.ReadOnlyFile) {
	if len(files) < 2 || !mixedLanguages(files) {
		return files, readOnly
	}
	var answer, reference []problem.File
	for _, file := range files {
		if answerRegionMarker.MatchString(file.CurrentContent) {
			answer = append(answer, file)
		} else {
			reference = append(reference, file)
		}
	}
	if len(answer) == 0 || len(reference) == 0 || mixedLanguages(answer) {
		return files, readOnly
	}
	total := 0
	for _, file := range readOnly {
		total += len(file.Content)
	}
	out := append([]problem.ReadOnlyFile(nil), readOnly...)
	for _, file := range reference {
		content := file.CurrentContent
		if content == "" || file.Language == "" || len(out) >= maxReferenceFiles || len(content) > maxReferenceFileBytes || total+len(content) > maxReferenceBytes || problem.ValidatePublicText(content) != nil || problem.ValidatePublicText(file.Path) != nil {
			continue
		}
		total += len(content)
		out = append(out, problem.ReadOnlyFile{Path: file.Path, Language: file.Language, Content: content})
	}
	return answer, out
}

func mixedLanguages(files []problem.File) bool {
	for _, file := range files[1:] {
		if file.Language != files[0].Language {
			return true
		}
	}
	return false
}
