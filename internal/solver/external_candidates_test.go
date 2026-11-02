package solver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

type importedAdapter struct {
	fakeAdapter
	identity questionbank.ExternalIdentity
}

func (a *importedAdapter) Load(ctx context.Context, target Target) (LoadedProblem, error) {
	p, err := a.fakeAdapter.Load(ctx, target)
	p.ExternalIdentity = a.identity
	return p, err
}

func (a *importedAdapter) SelfTest(ctx context.Context, target Target, file problem.SolutionFile, input, expected string) (SelfTest, error) {
	r, err := a.fakeAdapter.SelfTest(ctx, target, file, input, expected)
	r.SavedFilesHash, _ = problem.HashFiles([]problem.SolutionFile{file})
	return r, err
}

func (a *importedAdapter) Submit(ctx context.Context, target Target, file problem.SolutionFile) (Evaluation, error) {
	r, err := a.fakeAdapter.Submit(ctx, target, file)
	r.SavedFilesHash, _ = problem.HashFiles([]problem.SolutionFile{file})
	r.SubmittedFilesHash = r.SavedFilesHash
	return r, err
}

func importedFixture(t *testing.T, content string) *questionbank.Store {
	t.Helper()
	dir := t.TempDir()
	answerDir := filepath.Join(dir, "solutions", "experiment", "level")
	if err := os.MkdirAll(answerDir, 0700); err != nil {
		t.Fatal(err)
	}
	meta, _ := json.Marshal(map[string]any{"experiment": "Imported experiment", "subject": "stack", "path": "main.cpp", "kind": "code", "file": "answer.cpp", "gid": "ignored-account-instance"})
	if err := os.WriteFile(filepath.Join(answerDir, "meta.json"), meta, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(answerDir, "answer.cpp"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := questionbank.Open(filepath.Join(t.TempDir(), "bank.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	_, err = s.ImportExternalDirectory(context.Background(), questionbank.ExternalImportOptions{SourceDir: dir, Repository: "https://github.com/ppxxforever/fuck-educoder", Commit: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestImportedAnswerRunsWithoutModelAndRequiresFormalEvidence(t *testing.T) {
	for _, mode := range []Mode{ModeSelfTest, ModeSubmit} {
		t.Run(string(mode), func(t *testing.T) {
			bank := importedFixture(t, "good")
			adapter := &importedAdapter{fakeAdapter: fakeAdapter{snap: testSnapshot()}, identity: questionbank.ExternalIdentity{Experiment: "Imported experiment", Title: "stack", Position: 1}}
			svc := New(bank, nil, Config{Timeout: time.Second})
			r, err := svc.Run(context.Background(), adapter, Request{Owner: "owner", Course: "course", HomeworkID: 1, Game: "game", Mode: mode})
			if err != nil {
				t.Fatal(err)
			}
			if r.ModelCalls != 0 || adapter.selfTests != 1 || r.CandidateID == 0 {
				t.Fatalf("candidate was not validated: %+v", r)
			}
			snap, _ := problem.Prepare(testSnapshot())
			candidates, err := bank.Lookup(context.Background(), questionbank.Ref{RevisionHash: snap.RevisionHash, ContractHash: snap.ContractHash})
			if err != nil || len(candidates) != 1 {
				t.Fatalf("candidates=%+v err=%v", candidates, err)
			}
			want := "selftest_passed"
			if mode == ModeSubmit {
				want = "upstream_verified"
			}
			if candidates[0].Status != want {
				t.Fatalf("status=%s want=%s", candidates[0].Status, want)
			}
		})
	}
}

func TestImportedFailureRepairsInsteadOfReplayingSource(t *testing.T) {
	bank := importedFixture(t, "bad")
	provider := &fakeProvider{candidates: []answer.Candidate{candidate("good")}}
	adapter := &importedAdapter{fakeAdapter: fakeAdapter{snap: testSnapshot()}, identity: questionbank.ExternalIdentity{Experiment: "Imported experiment", Title: "stack"}}
	r, err := New(bank, provider, Config{Timeout: time.Second}).Run(context.Background(), adapter, Request{Owner: "owner", Course: "course", HomeworkID: 1, Game: "game", Mode: ModeSubmit})
	if err != nil || r == nil || r.Status != "passed" || r.ModelCalls != 1 || adapter.selfTests != 2 {
		t.Fatalf("result=%+v err=%v selftests=%d", r, err, adapter.selfTests)
	}
}

func TestImportedAnswerBindsOnContinuationAndRejectsNameMismatch(t *testing.T) {
	bank := importedFixture(t, "good")
	adapter := &importedAdapter{fakeAdapter: fakeAdapter{snap: testSnapshot()}, identity: questionbank.ExternalIdentity{Experiment: "Imported experiment", Title: "stack"}}
	svc := New(bank, nil, Config{Timeout: time.Second})
	req := Request{Owner: "owner", Course: "course", HomeworkID: 1, Game: "game", Mode: ModeGenerateOnly}
	r, err := svc.Run(context.Background(), adapter, req)
	if err != nil || r.Status != "generated" || adapter.submits != 0 {
		t.Fatalf("generate=%+v err=%v", r, err)
	}
	req.Mode = ModeSubmit
	r, err = svc.ResumeRun(context.Background(), adapter, req, r.RunID)
	if err != nil || r.Status != "passed" || r.ModelCalls != 0 || adapter.submits != 1 {
		t.Fatalf("resume=%+v err=%v", r, err)
	}
	other := &importedAdapter{fakeAdapter: fakeAdapter{snap: testSnapshot()}, identity: questionbank.ExternalIdentity{Experiment: "Other experiment", Title: "stack"}}
	// A different content revision has no previously verified answer to mask a miss.
	other.snap.StatementMarkdown = "different problem revision"
	r, err = svc.Run(context.Background(), other, req)
	if err == nil || r == nil || r.Status != "needs_provider" || other.selfTests != 0 || other.submits != 0 {
		t.Fatalf("mismatch=%+v err=%v", r, err)
	}
}
