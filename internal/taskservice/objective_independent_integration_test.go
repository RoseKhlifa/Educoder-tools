package taskservice

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/solver"
)

// The fixture models a repeatable anchoring failure, not platform answers:
// a full-history repair repeats AA, whereas clean question reasoning yields BA.
type independentConvergenceProvider struct {
	fakeProvider
	requests []answer.ObjectiveRequest
	failAt   int
}

func (p *independentConvergenceProvider) GenerateObjective(_ context.Context, request answer.ObjectiveRequest) (answer.ObjectiveResult, error) {
	p.requests = append(p.requests, request)
	result := answer.ObjectiveResult{Model: request.Profile.Model, CostKnown: true, CostMicroUSD: 2}
	if p.failAt > 0 && len(p.requests) == p.failAt {
		return result, &answer.Error{Kind: "truncated_output"}
	}
	values := make([]string, len(request.Problem.Questions))
	for i, q := range request.Problem.Questions {
		values[i] = "A"
		if len(request.Problem.Questions) == 1 && request.Previous == nil && len(request.Feedback) == 0 && q.Position == 1 {
			values[i] = "B"
		}
	}
	result.Candidate = answer.ObjectiveCandidate{Kind: "objective", Answers: values}
	return result, nil
}

func independentConvergenceServer(t *testing.T) (*educoder.Client, *atomic.Int64) {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(objectiveJSON()), &root); err != nil {
		t.Fatal(err)
	}
	var questions []map[string]any
	if err := json.Unmarshal(root["chooses"], &questions); err != nil {
		t.Fatal(err)
	}
	second := map[string]any{}
	for k, v := range questions[0] {
		second[k] = v
	}
	second["position"], second["subject"], second["challenge_choose_id"] = 2, "Independent sibling question", 11
	root["chooses"], _ = json.Marshal(append(questions, second))
	root["choose_test_cases"] = json.RawMessage(`{"challenge_chooses_count":2}`)
	submits := &atomic.Int64{}
	var passed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tasks/OBJGAME.json":
			json.NewEncoder(w).Encode(root)
		case "/api/tasks/OBJGAME/choose_build.json":
			var body struct {
				Answer []string `json:"answer"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			submits.Add(1)
			passed.Store(reflect.DeepEqual(body.Answer, []string{"B", "A"}))
			fmt.Fprint(w, `{"status":0,"choose_correct_num":null,"challenge_chooses_count":2}`)
		case "/api/homework_commons/44/shixun_challenge_data.json":
			score := "0"
			if passed.Load() {
				score = "2"
			}
			fmt.Fprintf(w, `{"data":{"challenge_settings":[{"challenge_id":9,"challenge_score":"2","game_score":"%s","passed_status":2}]}}`, score)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return educoder.New(server.URL), submits
}

func TestObjectiveIndependentConvergence(t *testing.T) {
	for _, failAt := range []int{0} {
		t.Run(fmt.Sprint("provider_error_at_", failAt), func(t *testing.T) {
			client, submits := independentConvergenceServer(t)
			store := bank(t)
			provider := &independentConvergenceProvider{failAt: failAt}
			limit := 3
			if failAt > 0 {
				limit++
			}
			result, err := New(store, provider, Config{MaxModelCalls: limit, MaxEvaluations: limit}).Run(context.Background(), client, req(solver.ModeSubmit), nil)
			if err != nil || result.Status != "passed" || result.ModelCalls != limit || result.CostMicroUSD != int64(2*limit) || submits.Load() != 2 {
				t.Fatalf("independent convergence: status=%s calls=%d submits=%d cost=%d err=%v", result.Status, result.ModelCalls, submits.Load(), result.CostMicroUSD, err)
			}
			usage, err := store.UsageForRun(context.Background(), result.RunID)
			if err != nil || len(usage) != limit {
				t.Fatalf("usage=%d err=%v", len(usage), err)
			}
			for _, request := range provider.requests[1:] {
				if len(request.Problem.Questions) != 1 || request.Previous != nil || len(request.Feedback) != 0 {
					t.Fatal("review retained whole-vector anchoring")
				}
			}
			t.Logf("status=passed calls=%d submits=2 usage=%d score=2/2", result.ModelCalls, len(usage))
		})
	}
}

func TestObjectiveIndependentReviewDoesNotSubmitPartialSweep(t *testing.T) {
	client, submits := independentConvergenceServer(t)
	provider := &independentConvergenceProvider{failAt: 3}
	result, _ := New(bank(t), provider, Config{MaxModelCalls: 3, MaxEvaluations: 3}).Run(context.Background(), client, req(solver.ModeSubmit), nil)
	if result.Status == "passed" || result.ModelCalls != 3 || submits.Load() != 1 {
		t.Fatalf("partial sweep submitted: %+v submits=%d", result, submits.Load())
	}
}

func TestObjectiveIndependentReviewPreflightsWholeSweepBudget(t *testing.T) {
	client, submits := independentConvergenceServer(t)
	provider := &independentConvergenceProvider{}
	// Initial call settles two, leaving nineteen. Each reservation is ten:
	// an individual call fits, but a complete two-question sweep does not.
	result, err := New(bank(t), provider, Config{MaxModelCalls: 3, MaxEvaluations: 3, BudgetMicroUSD: 21}).Run(context.Background(), client, req(solver.ModeSubmit), nil)
	if err != nil || result.Status != "attempts_exhausted" || result.ModelCalls != 3 || submits.Load() != 1 {
		t.Fatalf("budget preflight: %+v err=%v submits=%d", result, err, submits.Load())
	}
	for _, request := range provider.requests {
		if len(request.Problem.Questions) != 2 {
			t.Fatal("started a predictably unaffordable partial sweep")
		}
	}
}
