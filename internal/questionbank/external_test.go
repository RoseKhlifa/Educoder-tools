package questionbank

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"educoder-cli/internal/problem"
)

const externalTestCommit = "1234567890abcdef1234567890abcdef12345678"

func externalFixture(t *testing.T, root, folder, kind, experiment, title, target, filename, content string) {
	t.Helper()
	dir := filepath.Join(root, "solutions", "experiment", folder)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	metadata := map[string]any{"experiment": experiment, "subject": title, "kind": kind, "path": target, "file": filename, "gid": "private-runtime-id", "classroom": "private-course-id", "source": "submit"}
	b, _ := json.Marshal(metadata)
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func externalOptions(root string) ExternalImportOptions {
	return ExternalImportOptions{SourceDir: root, Repository: "https://github.com/example/public-bank", Commit: externalTestCommit}
}

func externalSnapshot(t *testing.T, store *Store, filename string) (problem.Snapshot, Ref) {
	t.Helper()
	snapshot := problem.Snapshot{Kind: "programming", Title: "创建文件", StatementMarkdown: "Create files", Files: []problem.File{{Path: filename, Language: "shell", CurrentContent: "# starter"}}, EditMode: "code"}
	p, err := problem.Prepare(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := store.UpsertSnapshot(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	return p, ref
}

func TestExternalImportLinuxPersistenceAndUnverifiedBinding(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	bankPath := filepath.Join(t.TempDir(), "bank.sqlite")
	externalFixture(t, root, "linux", "code", "Linux  文件管理", "创建文件", "case1/code.sh", "answer.sh", "#!/bin/bash\ntouch newFile\n")
	s, err := Open(bankPath)
	if err != nil {
		t.Fatal(err)
	}
	report, err := s.ImportExternalDirectory(ctx, externalOptions(root))
	if err != nil {
		t.Fatal(err)
	}
	if report.Total != 1 || report.Inserted != 1 || report.Usable != 1 {
		t.Fatalf("report=%+v", report)
	}
	report, err = s.ImportExternalDirectory(ctx, externalOptions(root))
	if err != nil || report.Existing != 1 || report.Inserted != 0 {
		t.Fatalf("repeat=%+v %v", report, err)
	}
	s.Close()
	s, err = Open(bankPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	entries, err := s.ExternalEntries(ctx)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries=%+v %v", entries, err)
	}
	b, _ := json.Marshal(entries)
	if strings.Contains(string(b), "private-runtime") || strings.Contains(string(b), "private-course") {
		t.Fatal("runtime metadata persisted")
	}
	p, ref := externalSnapshot(t, s, "case1/code.sh")
	originalRevision, originalContract := p.RevisionHash, p.ContractHash
	candidates, err := s.BindExternalCandidates(ctx, ref, ExternalIdentity{Experiment: " Linux\t文件管理 ", Title: "创建文件", Position: 1}, p)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("bind=%+v %v", candidates, err)
	}
	c := candidates[0]
	if c.Status != "candidate" || len(c.Validations) != 0 || c.Files[0].Content != "#!/bin/bash\ntouch newFile\n" {
		t.Fatalf("not an unverified full candidate: %+v", c)
	}
	if external, err := s.IsExternalCandidate(ctx, c.ID); err != nil || !external {
		t.Fatalf("provenance=%v %v", external, err)
	}
	if p.RevisionHash != originalRevision || p.ContractHash != originalContract {
		t.Fatal("binding changed snapshot")
	}
	if again, err := s.BindExternalCandidates(ctx, ref, ExternalIdentity{Experiment: "Linux 文件管理", Title: "创建文件"}, p); err != nil || len(again) != 1 || again[0].ID != c.ID {
		t.Fatalf("repeat bind=%+v %v", again, err)
	}
	if wrong, err := s.BindExternalCandidates(ctx, ref, ExternalIdentity{Experiment: "another course", Title: "创建文件"}, p); err != nil || len(wrong) != 0 {
		t.Fatal("matched unrelated experiment")
	}
}

func TestExternalContractMismatchAmbiguityAndSpoofedSource(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	externalFixture(t, root, "one", "code", "Linux", "创建文件", "case1/code.sh", "answer.sh", "touch one\n")
	externalFixture(t, root, "two", "code", "Linux", "创建文件", "case1/code.sh", "answer.sh", "touch two\n")
	if _, err = s.ImportExternalDirectory(ctx, externalOptions(root)); err != nil {
		t.Fatal(err)
	}
	p, ref := externalSnapshot(t, s, "case1/code.sh")
	identity := ExternalIdentity{Experiment: "Linux", Title: "创建文件"}
	if got, err := s.BindExternalCandidates(ctx, ref, identity, p); err != nil || len(got) != 0 {
		t.Fatalf("ambiguous source bound: %+v %v", got, err)
	}
	other, otherRef := externalSnapshot(t, s, "case2/code.sh")
	if got, err := s.BindExternalCandidates(ctx, otherRef, identity, other); err != nil || len(got) != 0 {
		t.Fatal("mismatched path bound")
	}
	spoof, err := s.AddCandidate(ctx, ref, CandidateInput{Source: "external:https://github.com/example/public-bank@" + externalTestCommit + ":1", Files: []problem.SolutionFile{{Path: "case1/code.sh", Content: "touch spoof"}}})
	if err != nil {
		t.Fatal(err)
	}
	if external, err := s.IsExternalCandidate(ctx, spoof.ID); err != nil || external {
		t.Fatal("trusted arbitrary candidate source")
	}
	p.Files = append(p.Files, problem.File{Path: "case1/helper.sh", Language: "shell"})
	if got, err := s.BindExternalCandidates(ctx, ref, identity, p); err != nil || len(got) != 0 {
		t.Fatal("bound partial multifile solution")
	}
}

func TestExternalReferencesAreNotVerifiedAnswers(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	externalFixture(t, root, "choice", "choice", "Linux", "创建文件", "answer.json", "answer.json", `{"title":"ignored","answer":[{"number":1,"selected":["A","C"]}],"gid":"not-copied"}`)
	externalFixture(t, root, "diagram-text", "uml", "Draw", "fallback", "submit/step.txt", "answer.txt", "draw a use case")
	externalFixture(t, root, "diagram", "uml", "Draw", "Project", "submit/step.mdj", "answer.mdj", `{"_type":"Project","ownedElements":[{"_type":"UMLModel"}]}`)
	report, err := s.ImportExternalDirectory(ctx, externalOptions(root))
	if err != nil {
		t.Fatal(err)
	}
	if report.Total != 3 || report.Reference != 1 || report.Usable != 2 || report.Kinds["objective"] != 1 {
		t.Fatalf("report=%+v", report)
	}
	p, ref := externalSnapshot(t, s, "answer.json")
	if candidates, err := s.BindExternalCandidates(ctx, ref, ExternalIdentity{Experiment: "Linux", Title: "创建文件"}, p); err != nil || len(candidates) != 0 {
		t.Fatal("positional choice used as programming answer")
	}
	entries, _ := s.ExternalEntries(ctx)
	for _, e := range entries {
		if strings.Contains(string(e.Reference), "not-copied") {
			t.Fatal("raw choice metadata persisted")
		}
	}
	p = problem.Snapshot{Kind: "programming", Title: "Project", Files: []problem.File{{Path: "submit/step.mdj", Language: "json"}}, EditMode: "graphic", EnvironmentContract: []string{"graphic:staruml"}}
	p, err = problem.Prepare(p)
	if err != nil {
		t.Fatal(err)
	}
	ref, err = s.UpsertSnapshot(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := s.BindExternalCandidates(ctx, ref, ExternalIdentity{Experiment: "Draw", Title: "Project"}, p)
	if err != nil || len(candidates) != 1 || candidates[0].Status != "candidate" {
		t.Fatalf("diagram bind=%+v %v", candidates, err)
	}
	if external, err := s.IsExternalCandidate(ctx, candidates[0].ID); err != nil || !external {
		t.Fatal("diagram provenance missing")
	}
}

func TestExternalFallbackPathlessExtensionAndPinnedContent(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	content := `{"classrooms":[{"experiments":[{"name":"Linux","levels":[{"title":"创建文件","kind":"code","position":1,"answered":true,"answer":{"language":"shell","code":"touch fallback\n"},"identifiers":["private-runtime"]},{"title":"untyped","kind":"code","answered":true,"answer":{"code":"no file metadata"}},{"title":"choice","kind":"choice","answered":true,"answer":[{"number":1,"selected":["A"]}]}]}]}]}`
	if err = os.WriteFile(filepath.Join(root, "answers.json"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	report, err := s.ImportExternalDirectory(ctx, externalOptions(root))
	if err != nil || report.Total != 2 || report.Usable != 1 || report.Reference != 1 {
		t.Fatalf("fallback=%+v %v", report, err)
	}
	p, ref := externalSnapshot(t, s, "different/location.sh")
	if candidates, err := s.BindExternalCandidates(ctx, ref, ExternalIdentity{Experiment: "Linux", Title: "创建文件", Position: 1}, p); err != nil || len(candidates) != 1 {
		t.Fatalf("fallback bind=%+v %v", candidates, err)
	}
	if candidates, err := s.BindExternalCandidates(ctx, ref, ExternalIdentity{Experiment: "Linux", Title: "创建文件", Position: 2}, p); err != nil || len(candidates) != 0 {
		t.Fatal("wrong position fallback bound")
	}
	if err = os.WriteFile(filepath.Join(root, "answers.json"), []byte(strings.Replace(content, "touch fallback", "touch changed", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ImportExternalDirectory(ctx, externalOptions(root)); err == nil {
		t.Fatal("changed source at pinned identity accepted")
	}
}

func TestExternalImportRejectsTraversalSecretsAndInvalidText(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	externalFixture(t, root, "secret", "code", "Linux", "secret", "case1/code.sh", "answer.sh", "api_key=abcdefghijklmnop\n")
	externalFixture(t, root, "traversal", "code", "Linux", "traversal", "../../escape.sh", "answer.sh", "touch file")
	report, err := s.ImportExternalDirectory(ctx, externalOptions(root))
	if err != nil || report.Rejected != 2 {
		t.Fatalf("report=%+v %v", report, err)
	}
	entries, err := s.ExternalEntries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(entries)
	if strings.Contains(string(b), "abcdefghijklmnop") || strings.Contains(string(b), "../../escape.sh") {
		t.Fatal("rejected private/unsafe content persisted")
	}
	if err = os.WriteFile(filepath.Join(root, "solutions", "experiment", "secret", "answer.sh"), []byte{0xff, 0xfe}, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ImportExternalDirectory(ctx, externalOptions(root)); err == nil {
		t.Fatal("non-UTF8 source accepted")
	}
}

func TestExternalImportDryRunAndSymlink(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	externalFixture(t, root, "linux", "code", "Linux", "创建文件", "case1/code.sh", "answer.sh", "touch file\n")
	opts := externalOptions(root)
	opts.DryRun = true
	report, err := s.ImportExternalDirectory(ctx, opts)
	if err != nil || report.Total != 1 || report.Inserted != 0 {
		t.Fatalf("dry-run=%+v %v", report, err)
	}
	entries, _ := s.ExternalEntries(ctx)
	if len(entries) != 0 {
		t.Fatal("dry-run persisted entries")
	}
	external := filepath.Join(t.TempDir(), "outside.sh")
	os.WriteFile(external, []byte("outside"), 0600)
	file := filepath.Join(root, "solutions", "experiment", "linux", "answer.sh")
	if err = os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(external, file); err != nil {
		t.Logf("symlink unavailable on test host: %v", err)
		return
	}
	if _, err = s.ImportExternalDirectory(ctx, opts); err == nil {
		t.Fatal("symlink source accepted")
	}
}

func TestExternalCheckoutProvenanceRejectsWrongCommitAndDirtyFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	ctx := context.Background()
	root := t.TempDir()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	externalFixture(t, root, "linux", "code", "Linux", "创建文件", "case1/code.sh", "answer.sh", "touch file\n")
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, b)
		}
		return strings.TrimSpace(string(b))
	}
	git("init")
	git("remote", "add", "origin", "https://github.com/example/public-bank.git")
	git("add", "solutions")
	git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-m", "Synthetic fixture")
	opts := externalOptions(root)
	opts.DryRun = true
	opts.Commit = git("rev-parse", "HEAD")
	if _, err = s.ImportExternalDirectory(ctx, opts); err != nil {
		t.Fatalf("clean pinned checkout: %v", err)
	}
	wrong := opts
	wrong.Commit = externalTestCommit
	if _, err = s.ImportExternalDirectory(ctx, wrong); err == nil {
		t.Fatal("accepted false commit attribution")
	}
	wrong = opts
	wrong.Repository = "https://github.com/another/repository"
	if _, err = s.ImportExternalDirectory(ctx, wrong); err == nil {
		t.Fatal("accepted false repository attribution")
	}
	if err = os.WriteFile(filepath.Join(root, "solutions", "experiment", "linux", "answer.sh"), []byte("touch changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ImportExternalDirectory(ctx, opts); err == nil {
		t.Fatal("accepted edited file under old commit")
	}
}
