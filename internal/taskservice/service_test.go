package taskservice

import (
	"context"
	"educoder-cli/internal/answer"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
	"educoder-cli/internal/solver"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type fakeProvider struct {
	programmingCalls atomic.Int64
	objectiveCalls   atomic.Int64
	generate         func(context.Context) error
	estimated        int64
	unknown          bool
}

func (p *fakeProvider) Estimate(answer.Request) (answer.Estimate, error) { return p.estimate(), nil }
func (p *fakeProvider) EstimateObjective(answer.ObjectiveRequest) (answer.Estimate, error) {
	return p.estimate(), nil
}
func (p *fakeProvider) estimate() answer.Estimate {
	cost := p.estimated
	if cost == 0 {
		cost = 10
	}
	return answer.Estimate{CostMicroUSD: cost, Model: "fake", PriceVersion: "test"}
}
func (p *fakeProvider) Generate(ctx context.Context, r answer.Request) (answer.Result, error) {
	p.programmingCalls.Add(1)
	if p.generate != nil {
		if err := p.generate(ctx); err != nil {
			return answer.Result{}, err
		}
	}
	return answer.Result{Candidate: answer.Candidate{Kind: "programming", Language: "cpp", Files: []problem.SolutionFile{{Path: "main.cpp", Content: "int main(){return 0;}"}}}, Model: "fake", CostKnown: !p.unknown, CostMicroUSD: 2}, nil
}
func (p *fakeProvider) GenerateObjective(ctx context.Context, r answer.ObjectiveRequest) (answer.ObjectiveResult, error) {
	p.objectiveCalls.Add(1)
	if p.generate != nil {
		if err := p.generate(ctx); err != nil {
			return answer.ObjectiveResult{}, err
		}
	}
	return answer.ObjectiveResult{Candidate: answer.ObjectiveCandidate{Kind: "objective", Answers: []string{"A"}}, Model: "fake", CostKnown: !p.unknown, CostMicroUSD: 2}, nil
}
func objectiveJSON() string {
	return `{"st":1,"homework_common_id":44,"game":{"identifier":"OBJGAME","id":7,"myshixun_id":8},"challenge":{"id":9,"st":1,"subject":"choice","task_pass":"Choose the first option"},"chooses":[{"challenge_choose_id":10,"challenge_id":9,"position":1,"category":1,"subject":"First?","challenge_question":[{"option_name":"First","position":0},{"option_name":"Second","position":1}]}]}`
}
func programmingJSON() string {
	return `{"st":0,"homework_common_id":44,"game":{"identifier":"PROGRAM","id":8,"myshixun_id":9},"myshixun":{"id":9},"challenge":{"id":11,"st":0,"with_code_file":true,"subject":"program","task_pass":"Return zero","path":"main.cpp"},"code_editor":{"shixun_environment_id":12,"name":"C/C++"},"test_sets":[]}`
}
func bank(t *testing.T) *questionbank.Store {
	t.Helper()
	s, err := questionbank.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func objectiveServer(t *testing.T, score string, failSubmit bool) (*educoder.Client, *atomic.Int64) {
	t.Helper()
	submits := &atomic.Int64{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tasks/OBJGAME.json":
			fmt.Fprint(w, objectiveJSON())
		case "/api/tasks/OBJGAME/choose_build.json":
			submits.Add(1)
			if failSubmit {
				http.Error(w, "temporary", 502)
				return
			}
			fmt.Fprint(w, `{"status":0,"choose_correct_num":null,"challenge_chooses_count":1}`)
		case "/api/homework_commons/44/shixun_challenge_data.json":
			fmt.Fprintf(w, `{"data":{"passed_count":1,"challenge_settings":[{"challenge_id":9,"challenge_score":"13.3","game_score":%s,"passed_status":2}]}}`, score)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return educoder.New(server.URL), submits
}
func req(mode solver.Mode) Request {
	return Request{Owner: "owner", Course: "COURSE", HomeworkID: 44, Game: "OBJGAME", Mode: mode}
}
func TestObjectiveFullScoreBankReuseAndPublicSummary(t *testing.T) {
	store := bank(t)
	client, submits := objectiveServer(t, `"13.30"`, false)
	provider := &fakeProvider{}
	service := New(store, provider, Config{})
	var phases []string
	r, err := service.Run(context.Background(), client, req(solver.ModeSubmit), func(p Progress) { phases = append(phases, p.Phase) })
	if err != nil || r.Status != "passed" || r.ScoreCheck != "full_score" || r.ModelCalls != 1 || r.CandidateID == 0 {
		t.Fatalf("result=%+v err=%v", r, err)
	}
	for _, phase := range []string{"loading", "bank_lookup", "generating", "validating", "submitting", "verifying", "bank_saved"} {
		if !strings.Contains(strings.Join(phases, "|"), phase) {
			t.Errorf("missing phase %s", phase)
		}
	}
	// A separately configured service without a model can reuse verified answers.
	r2, err := New(store, nil, Config{}).Run(context.Background(), client, req(solver.ModeSubmit), nil)
	if err != nil || r2.Status != "passed" || r2.Source != "bank" || r2.ModelCalls != 0 || submits.Load() != 2 || provider.objectiveCalls.Load() != 1 {
		t.Fatalf("reuse=%+v err=%v submits=%d", r2, err, submits.Load())
	}
	raw, _ := json.Marshal(r2)
	if strings.Contains(string(raw), `"answers"`) || strings.Contains(string(raw), "Choose the first") {
		t.Fatalf("private answer data in result %s", raw)
	}
}
func TestObjectiveIncompleteOrUncertainNeverPromotesOrRetries(t *testing.T) {
	for _, tc := range []struct {
		name, score, status string
		fail                bool
	}{{"score missing", `null`, "verification_unconfirmed", false}, {"mutation fails", `"13.30"`, "uncertain", true}, {"partial", `"12"`, "attempts_exhausted", false}} {
		t.Run(tc.name, func(t *testing.T) {
			store := bank(t)
			client, submits := objectiveServer(t, tc.score, tc.fail)
			service := New(store, &fakeProvider{}, Config{MaxEvaluations: 1})
			r, err := service.Run(context.Background(), client, req(solver.ModeSubmit), nil)
			if r == nil || r.Status != tc.status || submits.Load() != 1 {
				t.Fatalf("result=%+v err=%v submits=%d", r, err, submits.Load())
			}
			if tc.fail && err == nil {
				t.Fatal("missing mutation error")
			}
			// A failed or missing-score candidate is retained but is not a reusable hit.
			r2, e := New(store, nil, Config{}).Run(context.Background(), client, req(solver.ModeGenerateOnly), nil)
			if e == nil || r2.Status != "needs_provider" {
				t.Fatalf("unverified candidate reused: %+v err=%v", r2, e)
			}
		})
	}
}
func TestObjectiveBudgetStopsBeforeGeneration(t *testing.T) {
	client, submits := objectiveServer(t, `"13.3"`, false)
	p := &fakeProvider{estimated: 101}
	r, e := New(bank(t), p, Config{BudgetMicroUSD: 100}).Run(context.Background(), client, req(solver.ModeSubmit), nil)
	if e != nil || r.Status != "budget_exhausted" || p.objectiveCalls.Load() != 0 || submits.Load() != 0 {
		t.Fatalf("r=%+v e=%v", r, e)
	}
}
func TestObjectiveCancellationPersistsTerminalAndReservation(t *testing.T) {
	store := bank(t)
	client, submits := objectiveServer(t, `"13.3"`, false)
	entered := make(chan struct{})
	p := &fakeProvider{generate: func(ctx context.Context) error { close(entered); <-ctx.Done(); return ctx.Err() }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var r *Result
	var err error
	go func() { r, err = New(store, p, Config{}).Run(ctx, client, req(solver.ModeSubmit), nil); close(done) }()
	<-entered
	cancel()
	<-done
	if !errors.Is(err, context.Canceled) || r == nil || r.Status != "canceled" || r.CostMicroUSD != 10 || submits.Load() != 0 {
		t.Fatalf("r=%+v e=%v", r, err)
	}
	saved, e := store.GetRun(context.Background(), r.RunID)
	if e != nil || saved.Status != "canceled" {
		t.Fatalf("persisted=%+v e=%v", saved, e)
	}
	usage, e := store.UsageForRun(context.Background(), r.RunID)
	if e != nil || len(usage) != 1 || usage[0].Status != "unknown" || usage[0].ReservedMicroUSD != 10 {
		t.Fatalf("usage=%+v e=%v", usage, e)
	}
}
func TestSharedModelLimiterBothKindsAndCancellableWaiter(t *testing.T) {
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	p := &fakeProvider{generate: func(ctx context.Context) error {
		entered <- struct{}{}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	limited := &limitedProvider{provider: p, sem: make(chan struct{}, 1)}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); _, _ = limited.Generate(context.Background(), answer.Request{}) }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := limited.GenerateObjective(ctx, answer.ObjectiveRequest{})
	if !errors.Is(err, context.Canceled) || p.objectiveCalls.Load() != 0 {
		t.Fatalf("waiting objective executed e=%v calls=%d", err, p.objectiveCalls.Load())
	}
	close(release)
	wg.Wait()
	_, err = limited.GenerateObjective(context.Background(), answer.ObjectiveRequest{})
	if err != nil || p.objectiveCalls.Load() != 1 {
		t.Fatalf("objective permit not released e=%v", err)
	}
}
func TestProgrammingUsesSameApplicationServiceAndOmitsSources(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tasks/PROGRAM.json":
			fmt.Fprint(w, programmingJSON())
		case "/api/tasks/PROGRAM/rep_content.json":
			fmt.Fprintf(w, `{"filename":"main.cpp","language":"cpp","content":{"content":"%s"}}`, base64.StdEncoding.EncodeToString([]byte("// starter")))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	p := &fakeProvider{}
	q := req(solver.ModeGenerateOnly)
	q.Game = "PROGRAM"
	r, e := New(bank(t), p, Config{}).Run(context.Background(), educoder.New(server.URL), q, nil)
	if e != nil || r.Kind != "programming" || r.Status != "generated" || r.ModelCalls != 1 || p.objectiveCalls.Load() != 0 {
		t.Fatalf("result=%+v e=%v", r, e)
	}
	raw, _ := json.Marshal(r)
	if strings.Contains(string(raw), "int main") {
		t.Fatalf("source leaked %s", raw)
	}
}
func TestTargetLockCancellationAndCleanup(t *testing.T) {
	s := New(bank(t), nil, Config{})
	release, e := s.lock(context.Background(), "same")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = s.lock(ctx, "same"); !errors.Is(e, context.Canceled) {
		t.Fatalf("lock e=%v", e)
	}
	release()
	s.targetMu.Lock()
	defer s.targetMu.Unlock()
	if len(s.targets) != 0 {
		t.Fatalf("lock entries leaked %d", len(s.targets))
	}
}
func TestObjectiveIdempotencyPreventsRepeatSubmissionAndRejectsModeChange(t *testing.T) {
	store := bank(t)
	client, submits := objectiveServer(t, `"13.30"`, false)
	s := New(store, &fakeProvider{}, Config{})
	q := req(solver.ModeSubmit)
	q.IdempotencyKey = "key"
	r, e := s.Run(context.Background(), client, q, nil)
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.Run(context.Background(), client, q, nil)
	if e != nil || again.RunID != r.RunID || submits.Load() != 1 {
		t.Fatalf("again=%+v e=%v", again, e)
	}
	q.Mode = solver.ModeGenerateOnly
	if _, e = s.Run(context.Background(), client, q, nil); e == nil {
		t.Fatal("reused idempotency key with changed mode")
	}
}
