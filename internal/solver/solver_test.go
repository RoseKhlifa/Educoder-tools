package solver

import (
	"context"
	"errors"
	"testing"
	"time"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

type fakeStore struct {
	ref         questionbank.Ref
	candidates  []questionbank.Candidate
	runs        map[string]questionbank.RunRecord
	validations []questionbank.Validation
	usages      []questionbank.Usage
}

func (f *fakeStore) UpsertSnapshot(context.Context, problem.Snapshot) (questionbank.Ref, error) {
	if f.ref.RevisionHash == "" {
		f.ref = questionbank.Ref{RevisionHash: "rev", ContractHash: "contract"}
	}
	return f.ref, nil
}
func (f *fakeStore) Lookup(context.Context, questionbank.Ref) ([]questionbank.Candidate, error) {
	return append([]questionbank.Candidate(nil), f.candidates...), nil
}
func (f *fakeStore) AddCandidate(_ context.Context, ref questionbank.Ref, in questionbank.CandidateInput) (questionbank.Candidate, error) {
	h, e := problem.HashFiles(in.Files)
	if e != nil {
		return questionbank.Candidate{}, e
	}
	for _, c := range f.candidates {
		if c.FilesHash == h {
			return c, nil
		}
	}
	c := questionbank.Candidate{ID: int64(len(f.candidates) + 1), Ref: ref, CandidateInput: in, FilesHash: h, Status: "candidate"}
	f.candidates = append(f.candidates, c)
	return c, nil
}
func (f *fakeStore) RecordValidation(_ context.Context, v questionbank.Validation) error {
	f.validations = append(f.validations, v)
	for i := range f.candidates {
		if f.candidates[i].ID == v.CandidateID {
			f.candidates[i].Validations = append(f.candidates[i].Validations, v)
			if v.Result == "failed" {
				f.candidates[i].Status = "rejected"
			}
			if v.Result == "passed" && v.Kind == "formal" {
				f.candidates[i].Status = "upstream_verified"
			}
		}
	}
	return nil
}
func (f *fakeStore) PutRun(_ context.Context, r questionbank.RunRecord) error {
	if f.runs == nil {
		f.runs = map[string]questionbank.RunRecord{}
	}
	f.runs[r.ID] = r
	return nil
}
func (f *fakeStore) GetRun(_ context.Context, id string) (questionbank.RunRecord, error) {
	r, ok := f.runs[id]
	if !ok {
		return questionbank.RunRecord{}, questionbank.ErrNotFound
	}
	return r, nil
}
func (f *fakeStore) FindRunByIdempotency(_ context.Context, owner, key string) (questionbank.RunRecord, error) {
	for _, r := range f.runs {
		if r.Owner == owner && r.IdempotencyKey == key {
			return r, nil
		}
	}
	return questionbank.RunRecord{}, questionbank.ErrNotFound
}
func (f *fakeStore) RecordUsage(ctx context.Context, u questionbank.Usage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.usages = append(f.usages, u)
	return nil
}

type fakeProvider struct {
	calls      int
	candidates []answer.Candidate
}

func (p *fakeProvider) Estimate(answer.Request) (answer.Estimate, error) {
	return answer.Estimate{CostMicroUSD: 1, Model: "fake"}, nil
}
func (p *fakeProvider) Generate(context.Context, answer.Request) (answer.Result, error) {
	if p.calls >= len(p.candidates) {
		return answer.Result{}, errors.New("no fake candidate")
	}
	c := p.candidates[p.calls]
	p.calls++
	return answer.Result{Candidate: c, Model: "fake", CostMicroUSD: 1, CostKnown: true}, nil
}

type fakeAdapter struct {
	snap      problem.Snapshot
	selfTests int
	submits   int
}

func (a *fakeAdapter) Load(context.Context, Target) (LoadedProblem, error) {
	return LoadedProblem{Snapshot: a.snap, Key: "myshixun"}, nil
}
func (a *fakeAdapter) SelfTest(_ context.Context, _ Target, file problem.SolutionFile, input, expected string) (SelfTest, error) {
	a.selfTests++
	ok := input == "1" && file.Content == "good"
	output := expected
	if !ok {
		output = "wrong"
	}
	return SelfTest{Completed: true, Output: output, Matches: ok}, nil
}
func (a *fakeAdapter) Submit(context.Context, Target, problem.SolutionFile) (Evaluation, error) {
	a.submits++
	return Evaluation{Completed: true, Passed: true, Status: "2", TestCount: 1, PassedCount: 1}, nil
}
func (a *fakeAdapter) Progress(context.Context, Target) (Progress, error) {
	return Progress{PassedCount: 1, ChallengeStatuses: []int{2}}, nil
}

func testSnapshot() problem.Snapshot {
	return problem.Snapshot{Kind: "programming", Title: "stack", StatementMarkdown: "write code", Files: []problem.File{{Path: "main.cpp", Filename: "main.cpp", Language: "cpp", CurrentContent: "", StarterProvenance: "unknown"}}, PublicTests: []problem.PublicTest{{Stdin: "1", ExpectedStdout: "ok"}}}
}
func candidate(content string) answer.Candidate {
	return answer.Candidate{Kind: "programming", Language: "cpp", Files: []problem.SolutionFile{{Path: "main.cpp", Content: content}}}
}

func TestRunGeneratesSelfTestsAndFormalPass(t *testing.T) {
	store := &fakeStore{}
	provider := &fakeProvider{candidates: []answer.Candidate{candidate("good")}}
	adapter := &fakeAdapter{snap: testSnapshot()}
	svc := New(store, provider, Config{Timeout: time.Second})
	r, err := svc.Run(context.Background(), adapter, Request{Owner: "account", Course: "c", HomeworkID: 1, Game: "g", Mode: ModeSubmit, IdempotencyKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "passed" || provider.calls != 1 || adapter.selfTests != 1 || adapter.submits != 1 {
		t.Fatalf("unexpected result %#v calls=%d self=%d submit=%d", r, provider.calls, adapter.selfTests, adapter.submits)
	}
	if len(store.validations) < 2 {
		t.Fatalf("expected selftest and formal validations: %#v", store.validations)
	}
}

func TestRunRepairsSelfTestFailureAndKeepsCandidates(t *testing.T) {
	store := &fakeStore{}
	provider := &fakeProvider{candidates: []answer.Candidate{candidate("bad"), candidate("good")}}
	adapter := &fakeAdapter{snap: testSnapshot()}
	svc := New(store, provider, Config{Timeout: time.Second, MaxModelCalls: 2})
	r, err := svc.Run(context.Background(), adapter, Request{Owner: "account", Course: "c", HomeworkID: 1, Game: "g", Mode: ModeSelfTest})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "selftest_passed" || provider.calls != 2 {
		t.Fatalf("unexpected repair result %#v calls=%d", r, provider.calls)
	}
	if len(store.candidates) != 2 {
		t.Fatalf("failed candidate was not retained: %d", len(store.candidates))
	}
}

func TestRunRevalidatesBankCandidateWithoutModel(t *testing.T) {
	store := &fakeStore{}
	h, _ := problem.HashFiles(candidate("good").Files)
	store.candidates = []questionbank.Candidate{{ID: 9, Ref: questionbank.Ref{RevisionHash: "rev", ContractHash: "contract"}, CandidateInput: questionbank.CandidateInput{Files: candidate("good").Files, Source: "verified"}, FilesHash: h, Status: "upstream_verified"}}
	provider := &fakeProvider{candidates: []answer.Candidate{candidate("bad")}}
	adapter := &fakeAdapter{snap: testSnapshot()}
	service := New(store, provider, Config{Timeout: time.Second})
	r, err := service.Run(context.Background(), adapter, Request{Owner: "account", Course: "c", HomeworkID: 1, Game: "g", Mode: ModeSelfTest})
	if err != nil || r.Status != "selftest_passed" || provider.calls != 0 {
		t.Fatalf("verified candidate should avoid model: result=%#v err=%v calls=%d", r, err, provider.calls)
	}
}
