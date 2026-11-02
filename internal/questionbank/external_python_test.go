package questionbank

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"educoder-cli/internal/problem"
)

func TestExternalPythonWhitespaceOnlyOutsideStrings(t *testing.T) {
	for _, tc := range []struct {
		name, source, want string
	}{
		{"generator shape", "\"\"\"task\ntext\n\"\"\"\n\u00a0\ndef count():\n    yield 0\n\u00a0\nfor x in range(1):\n\tprint(x)", "\"\"\"task\ntext\n\"\"\"\n \ndef count():\n    yield 0\n \nfor x in range(1):\n    print(x)"},
		{"scope shape", "\"\"\"task\"\"\"\n \ndef compare(x,y):\n\treturn x if x > y else y\ndef maximum(*items):\n\tglobal value\n\tvalue = items[0]\n\tfor x in items:\n\t\tvalue = compare(x,value)", "\"\"\"task\"\"\"\n \ndef compare(x,y):\n    return x if x > y else y\ndef maximum(*items):\n    global value\n    value = items[0]\n    for x in items:\n        value = compare(x,value)"},
		{"average shape", "\"\"\"task\"\"\"\n\u00a0\ndef average(*args):\n    return sum(args) / len(args)\nif score:\n\tprint(score)\nelse:\n\tprint('invalid')", "\"\"\"task\"\"\"\n \ndef average(*args):\n    return sum(args) / len(args)\nif score:\n    print(score)\nelse:\n    print('invalid')"},
		{"literal bytes", "if True:\n\tvalue = '\t\u00a0'\n\tother = \"\t\u00a0\"\n", "if True:\n    value = '\t\u00a0'\n    other = \"\t\u00a0\"\n"},
		{"multiline literal", "value = \"\"\"first\n\tinside\n\u00a0\n\"\"\"\nif value:\n\tprint(value)\n\u00a0", "value = \"\"\"first\n\tinside\n\u00a0\n\"\"\"\nif value:\n    print(value)\n "},
		{"triple single literal", "value = '''first\n\tinside\n\u00a0\n'''\nif value:\n\tpass", "value = '''first\n\tinside\n\u00a0\n'''\nif value:\n    pass"},
		{"escaped physical newline", "value = 'first\\\n\t\u00a0second'\nif value:\n\tpass", "value = 'first\\\n\t\u00a0second'\nif value:\n    pass"},
		{"raw escaped quote and newline", "value = r'first\\'\\\n\t\u00a0second'\nif value:\n\tpass", "value = r'first\\'\\\n\t\u00a0second'\nif value:\n    pass"},
		{"comment quotes", "# ''' \" never a string\nif True:\n\tpass", "# ''' \" never a string\nif True:\n    pass"},
		{"adjacent quoted strings", "if True:\n\tvalue = 'a' \"b\" r'c' b'd' u'e' br'f' rb'g'", "if True:\n    value = 'a' \"b\" r'c' b'd' u'e' br'f' rb'g'"},
		{"tab-only blank line", "\t\nif True:\n\tpass\n", "    \nif True:\n    pass\n"},
		{"multiple NBSP left intact", "\u00a0\u00a0\nif True:\n\tpass", "\u00a0\u00a0\nif True:\n    pass"},
		{"mixed space then tab", "if True:\n\tpass\n \tpass", "if True:\n\tpass\n \tpass"},
		{"mixed tab then space", "if True:\n\tpass\n\t pass", "if True:\n\tpass\n\t pass"},
		{"formfeed indentation", "if True:\n\tpass\n\f\tpass", "if True:\n\tpass\n\f\tpass"},
		{"formatted string conservative", "if True:\n\tpass\nvalue = f'{thing}'", "if True:\n\tpass\nvalue = f'{thing}'"},
		{"raw formatted string conservative", "if True:\n\tpass\nvalue = Fr'''first\n\t{thing}\n'''", "if True:\n\tpass\nvalue = Fr'''first\n\t{thing}\n'''"},
		{"template string conservative", "if True:\n\tpass\nvalue = t'{thing}'", "if True:\n\tpass\nvalue = t'{thing}'"},
		{"unclosed triple conservative", "if True:\n\tpass\nvalue = '''first\n\u00a0", "if True:\n\tpass\nvalue = '''first\n\u00a0"},
		{"invalid single string conservative", "if True:\n\tpass\nvalue = 'first\n\u00a0", "if True:\n\tpass\nvalue = 'first\n\u00a0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeExternalPythonWhitespace(tc.source)
			if got != tc.want {
				t.Fatalf("got %q; want %q", got, tc.want)
			}
			if again := normalizeExternalPythonWhitespace(got); again != got {
				t.Fatalf("normalization is not idempotent: %q", again)
			}
		})
	}
}

func TestExternalPythonWhitespaceUsesTargetContract(t *testing.T) {
	raw := "\u00a0\r\nif True:\r\n\tpass"
	lf := strings.ReplaceAll(raw, "\r\n", "\n")
	normalized := " \nif True:\n    pass"
	for _, tc := range []struct {
		name, kind string
		target     problem.File
		want       string
	}{
		{"Python path", "programming", problem.File{Path: "answer.py"}, normalized},
		{"matching Python contract", "programming", problem.File{Path: "answer.py", Language: "python"}, normalized},
		{"uppercase Python path", "programming", problem.File{Path: "answer.PY"}, normalized},
		{"Python language", "programming", problem.File{Path: "answer", Language: " Python3 "}, normalized},
		{"Python path conflicts with language", "programming", problem.File{Path: "answer.py", Language: "shell"}, lf},
		{"Python language conflicts with path", "programming", problem.File{Path: "answer.sh", Language: "python"}, lf},
		{"explicit unknown language", "programming", problem.File{Path: "answer.py", Language: "unknown"}, lf},
		{"non-Python", "programming", problem.File{Path: "answer.js", Language: "javascript"}, lf},
		{"unknown language", "programming", problem.File{Path: "answer", Language: "unknown"}, lf},
		{"diagram", "diagram", problem.File{Path: "answer.py", Language: "python"}, raw},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := externalExecutionContent(tc.kind, raw, tc.target); got != tc.want {
				t.Fatalf("got %q; want %q", got, tc.want)
			}
		})
	}
}

func TestExternalPythonNormalizationCreatesNewUnverifiedCandidate(t *testing.T) {
	for _, tc := range []struct {
		name, target, language string
	}{
		{"matching path and language", "answer.py", "python"},
		{"language gate", "answer", "python3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s, err := Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			root := t.TempDir()
			raw := "\"\"\"task\n\tunaltered docstring\n\"\"\"\n\u00a0\nif True:\n\tprint('ok')"
			want := "\"\"\"task\n\tunaltered docstring\n\"\"\"\n \nif True:\n    print('ok')"
			externalFixture(t, root, "raw", "code", "Python", "Normalize", tc.target, "answer.py", raw)
			externalFixture(t, root, "normalized", "code", "Python", "Normalize", tc.target, "answer.py", want)
			if _, err := s.ImportExternalDirectory(ctx, externalOptions(root)); err != nil {
				t.Fatal(err)
			}
			before, err := s.ExternalEntries(ctx)
			if err != nil || len(before) != 2 {
				t.Fatalf("sources: %+v %v", before, err)
			}
			var sourceID int64
			for _, e := range before {
				if e.Files[0].Content == raw {
					sourceID = e.ID
				}
			}
			if sourceID == 0 {
				t.Fatal("source bytes were not preserved")
			}
			snapshot, err := problem.Prepare(problem.Snapshot{Kind: "programming", Title: "Normalize", Files: []problem.File{{Path: tc.target, Language: tc.language, CurrentContent: "# starter"}}, EditMode: "code"})
			if err != nil {
				t.Fatal(err)
			}
			ref, err := s.UpsertSnapshot(ctx, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			old, err := s.AddCandidate(ctx, ref, CandidateInput{Source: "external:legacy", Files: []problem.SolutionFile{{Path: tc.target, Content: raw}}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.ExecContext(ctx, `INSERT INTO external_candidate_bindings(candidate_id,external_entry_id,created_at) VALUES(?,?,?)`, old.ID, sourceID, now()); err != nil {
				t.Fatal(err)
			}
			oldEvidence := Validation{CandidateID: old.ID, Kind: "selftest", Result: "passed", FilesHash: old.FilesHash, SavedFilesHash: old.FilesHash, RevisionHash: ref.RevisionHash, ContractHash: ref.ContractHash, Terminal: true, Complete: true, TestCount: 1, PassedCount: 1}
			if err := s.RecordValidation(ctx, oldEvidence); err != nil {
				t.Fatal(err)
			}
			oldBefore, err := s.Candidate(ctx, old.ID)
			if err != nil {
				t.Fatal(err)
			}
			if reusable, err := s.IsExternalCandidate(ctx, old.ID); err != nil || reusable {
				t.Fatalf("old source remained selectable: %v %v", reusable, err)
			}
			bound, err := s.BindExternalCandidates(ctx, ref, ExternalIdentity{Experiment: "Python", Title: "Normalize"}, snapshot)
			if err != nil || len(bound) != 1 {
				t.Fatalf("bind: %+v %v", bound, err)
			}
			c := bound[0]
			wantHash, err := problem.HashFiles([]problem.SolutionFile{{Path: tc.target, Content: want}})
			if err != nil || c.ID == old.ID || c.FilesHash != wantHash || c.FilesHash == old.FilesHash || c.Files[0].Content != want || c.Status != "candidate" || len(c.Validations) != 0 {
				t.Fatalf("new candidate acquired incorrect bytes or evidence: %+v %v", c, err)
			}
			if reusable, err := s.IsExternalCandidate(ctx, c.ID); err != nil || !reusable {
				t.Fatalf("normalized candidate not selectable: %v %v", reusable, err)
			}
			oldEvidence.CandidateID = c.ID
			if err := s.RecordValidation(ctx, oldEvidence); err == nil {
				t.Fatal("old hash-bound evidence promoted the normalized candidate")
			}
			if _, err := s.FindVerified(ctx, ref); !errors.Is(err, ErrNotFound) {
				t.Fatalf("normalization created a verified answer: %v", err)
			}
			oldAfter, err := s.Candidate(ctx, old.ID)
			if err != nil || !reflect.DeepEqual(oldBefore, oldAfter) {
				t.Fatalf("old candidate or evidence changed: %v", err)
			}
			after, err := s.ExternalEntries(ctx)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("normalization changed source/provenance: %v", err)
			}
			if again, err := s.BindExternalCandidates(ctx, ref, ExternalIdentity{Experiment: "Python", Title: "Normalize"}, snapshot); err != nil || len(again) != 1 || again[0].ID != c.ID {
				t.Fatalf("repeat binding changed candidate: %+v %v", again, err)
			}
		})
	}
}
