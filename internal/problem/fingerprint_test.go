package problem

import "testing"

func sample() Snapshot {
	return Snapshot{Kind: "programming", Title: "Stack", StatementMarkdown: "Read input\r\nprint output", Files: []File{{Path: "242/main.cpp", Language: "cpp", CurrentContent: "int main() { return 0; }", StarterProvenance: "unknown"}}, PublicTests: []PublicTest{{Stdin: "a\r\nb", ExpectedStdout: "b\r\n"}}, ObservedConstraints: []string{"O(n)"}}
}

func TestPrepareSeparatesCurrentEditorContent(t *testing.T) {
	a, err := Prepare(sample())
	if err != nil {
		t.Fatal(err)
	}
	b := sample()
	b.Files[0].CurrentContent = "int main() { return 42; }"
	b.Files[0].CurrentContentHash = "old"
	b, err = Prepare(b)
	if err != nil {
		t.Fatal(err)
	}
	if a.RevisionHash != b.RevisionHash {
		t.Fatalf("current editor content changed revision: %s != %s", a.RevisionHash, b.RevisionHash)
	}
	if a.Files[0].CurrentContentHash == b.Files[0].CurrentContentHash {
		t.Fatal("current content hash did not change")
	}
}

func TestPrepareContractChangesForPathLanguageTemplate(t *testing.T) {
	a, _ := Prepare(sample())
	b := sample()
	b.Files[0].Path = "242/main.h"
	b.Files[0].Language = "c"
	b.Files[0].CurrentContent = ""
	b, _ = Prepare(b)
	if a.ContractHash == b.ContractHash {
		t.Fatal("contract did not change")
	}
	if a.RevisionHash != b.RevisionHash {
		t.Fatal("revision changed for execution contract only")
	}
}

func TestPrepareRejectsCredentialCanaries(t *testing.T) {
	for _, value := range []string{"sec_key=secret1", "Authorization: Bearer hello", "https://example.test/a?X-Amz-Signature=abc"} {
		s := sample()
		s.StatementMarkdown = value
		if _, err := Prepare(s); err == nil {
			t.Fatalf("accepted credential canary %q", value)
		}
	}
}

func TestHashFilesKeepsExactSourceBytes(t *testing.T) {
	a, err := HashFiles([]SolutionFile{{Path: "main.cpp", Content: "x\n"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashFiles([]SolutionFile{{Path: "main.cpp", Content: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("source whitespace was discarded")
	}
}
