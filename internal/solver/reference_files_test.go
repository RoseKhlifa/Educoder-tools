package solver

import (
	"strings"
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
)

func scrapingFiles(page string) []problem.File {
	return []problem.File{
		{Path: "src/step1/student.py", Filename: "student.py", Language: "python", Extension: ".py", CurrentContent: "def get_data():\n    #********** Begin **********#\n    pass\n    #********** End **********#\n", StarterProvenance: "unknown"},
		{Path: "src/step1/test.html", Filename: "test.html", Language: "html", Extension: ".html", CurrentContent: page, StarterProvenance: "unknown"},
	}
}

// The production case: a Python answer file plus the saved page it parses.
// Only the answer file stays writable; the page becomes read-only context, so
// the model neither reproduces it nor trips the mixed-language contract.
func TestReferencePageBecomesReadOnlyContext(t *testing.T) {
	page := "<html><body><div class='left'><ul><li><strong><a>静夜思</a></strong></li></ul></div></body></html>\n"
	support := []problem.ReadOnlyFile{{Path: "src/step1/main.py", Language: "python", Content: "import student\n"}}
	files, readOnly := splitReferenceFiles(scrapingFiles(page), support)
	if len(files) != 1 || files[0].Path != "src/step1/student.py" {
		t.Fatalf("editable files: %+v", files)
	}
	if len(readOnly) != 2 || readOnly[0].Path != "src/step1/main.py" || readOnly[1].Path != "src/step1/test.html" || readOnly[1].Content != page {
		t.Fatalf("read-only context: %+v", readOnly)
	}
	snap := problem.Snapshot{SchemaVersion: problem.SchemaVersion, Kind: "programming", Title: "XPath", StatementMarkdown: "补全 get_data()", Files: files, ReadOnlyFiles: readOnly, EditMode: "single_file"}
	if _, err := problem.Prepare(snap); err != nil {
		t.Fatalf("snapshot no longer prepares: %v", err)
	}
	c := answer.Candidate{Kind: "programming", Language: "python", Files: []problem.SolutionFile{{Path: "src/step1/student.py", Content: "def get_data():\n    return []\n"}}}
	if err := answer.ValidateCandidate(snap, c); err != nil {
		t.Fatalf("a Python-only answer must satisfy the contract: %v", err)
	}
}

func TestReferenceSplitLeavesOtherFileSetsAlone(t *testing.T) {
	plain := scrapingFiles("<html></html>\n")
	plain[0].CurrentContent = "def get_data():\n    pass\n"
	if files, ro := splitReferenceFiles(plain, nil); len(files) != 2 || len(ro) != 0 {
		t.Fatalf("no answer region anywhere must stay unchanged: %d files, %d read-only", len(files), len(ro))
	}
	same := []problem.File{
		{Path: "a.cpp", Language: "cpp", CurrentContent: "/***** Begin *****/\n"},
		{Path: "b.cpp", Language: "cpp", CurrentContent: "int b;\n"},
	}
	if files, _ := splitReferenceFiles(same, nil); len(files) != 2 {
		t.Fatal("a single-language multi-file task must stay unchanged")
	}
	mixedAnswers := []problem.File{
		{Path: "index.html", Language: "html", CurrentContent: "<!-- ***** Begin ***** -->\n"},
		{Path: "app.js", Language: "javascript", CurrentContent: "// ***** Begin *****\n"},
		{Path: "data.txt", Language: "text", CurrentContent: "1 2 3\n"},
	}
	if files, _ := splitReferenceFiles(mixedAnswers, nil); len(files) != 3 {
		t.Fatal("answer files that themselves mix languages must stay unchanged")
	}
	huge := scrapingFiles(strings.Repeat("<p>x</p>\n", 9000))
	files, ro := splitReferenceFiles(huge, nil)
	if len(files) != 1 || len(ro) != 0 {
		t.Fatalf("an oversized reference is still not writable, and is left out of the context: %d files, %d read-only", len(files), len(ro))
	}
}
