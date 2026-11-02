package questionbank

import (
	"context"
	"strings"
	"testing"

	"educoder-cli/internal/problem"
)

func TestExternalWindowsCheckoutCreatesExactLFCandidateAndRetiresOldCRLF(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	raw := "#!/bin/bash\r\necho hello\r\n"
	lf := strings.ReplaceAll(raw, "\r\n", "\n")
	externalFixture(t, root, "windows", "code", "Linux", "创建文件", "case1/code.sh", "answer.sh", raw)
	// Equivalent content from another export must not become an ambiguity.
	externalFixture(t, root, "unix", "code", "Linux", "创建文件", "case1/code.sh", "answer.sh", lf)
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.ImportExternalDirectory(ctx, externalOptions(root)); err != nil {
		t.Fatal(err)
	}
	entries, err := s.ExternalEntries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var source ExternalEntry
	for _, e := range entries {
		if e.Files[0].Content == raw {
			source = e
		}
	}
	if source.ID == 0 {
		t.Fatal("original source bytes were not preserved")
	}
	snapshot, ref := externalSnapshot(t, s, "case1/code.sh")
	old, err := s.AddCandidate(ctx, ref, CandidateInput{Source: "external:legacy", Files: []problem.SolutionFile{{Path: "case1/code.sh", Content: raw}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `INSERT INTO external_candidate_bindings(candidate_id,external_entry_id,created_at) VALUES(?,?,?)`, old.ID, source.ID, now()); err != nil {
		t.Fatal(err)
	}
	if reusable, err := s.IsExternalCandidate(ctx, old.ID); err != nil || reusable {
		t.Fatalf("legacy CRLF remained reusable: %v %v", reusable, err)
	}
	bound, err := s.BindExternalCandidates(ctx, ref, ExternalIdentity{Experiment: "Linux", Title: "创建文件"}, snapshot)
	if err != nil || len(bound) != 1 {
		t.Fatalf("bind: %+v %v", bound, err)
	}
	c := bound[0]
	if c.ID == old.ID || c.Files[0].Content != lf || c.FilesHash == old.FilesHash {
		t.Fatal("normalized execution candidate was not distinct from source bytes")
	}
	wantHash, err := problem.HashFiles([]problem.SolutionFile{{Path: "case1/code.sh", Content: lf}})
	if err != nil || c.FilesHash != wantHash {
		t.Fatal("candidate hash does not represent exact executable bytes")
	}
	if reusable, err := s.IsExternalCandidate(ctx, c.ID); err != nil || !reusable {
		t.Fatalf("LF candidate not reusable: %v %v", reusable, err)
	}
	after, err := s.ExternalEntries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range after {
		if e.ID == source.ID && (e.SourceSHA256 != source.SourceSHA256 || e.EntrySHA256 != source.EntrySHA256 || e.Files[0].Content != raw) {
			t.Fatal("candidate normalization rewrote provenance")
		}
	}
	if report, err := s.ImportExternalDirectory(ctx, externalOptions(root)); err != nil || report.Existing != 2 {
		t.Fatalf("pinned repeat import changed: %+v %v", report, err)
	}
}
