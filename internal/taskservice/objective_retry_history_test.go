package taskservice

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/questionbank"
	"educoder-cli/internal/solver"
)

func TestObjectiveRecoveryRestoresDurableFailuresWithoutResubmitting(t *testing.T) {
	ctx := context.Background()
	client, submits := objectiveAttemptServer(t, 3, false)
	path := filepath.Join(t.TempDir(), "recovery.sqlite")
	store, err := questionbank.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	firstProvider := &objectiveSequenceProvider{answers: []string{"A", "B"}}
	first, err := New(store, firstProvider, Config{MaxModelCalls: 2, MaxEvaluations: 2}).Run(ctx, client, req(solver.ModeSubmit), nil)
	if err != nil || first.Status != "attempts_exhausted" || submits.Load() != 2 {
		t.Fatalf("first run did not establish two failures: %+v err=%v submits=%d", first, err, submits.Load())
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = questionbank.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	// The first recovery answer repeats an older failure, not just Previous.
	// It must consume model usage, but no platform submission, before a fresh
	// independently generated candidate can be evaluated.
	recoveryProvider := &objectiveSequenceProvider{answers: []string{"A", "C"}}
	recovered, err := New(store, recoveryProvider, Config{MaxModelCalls: 2, MaxEvaluations: 2}).Run(ctx, client, req(solver.ModeSubmit), nil)
	if err != nil || recovered.Status != "passed" || submits.Load() != 3 || recovered.ModelCalls != 2 || recovered.CostMicroUSD != 4 {
		t.Fatalf("recovery replayed failure or lost budget: %+v err=%v submits=%d", recovered, err, submits.Load())
	}
	for index, request := range recoveryProvider.requests {
		history := historyFromObjectiveFeedback(t, request.Feedback)
		if len(history) != 2 || history[0].Candidate.Answers[0] != "A" || history[1].Candidate.Answers[0] != "B" {
			t.Fatalf("recovery %d lost exact failed vectors: %+v", index, history)
		}
		for _, observation := range history {
			if observation.Score != "12" || observation.MaxScore != "13.3" || len(observation.QuestionResults) != 0 {
				t.Fatalf("total score became per-question correctness: %+v", observation)
			}
		}
		if request.Profile.Thinking != answer.RepairProfile().Thinking || request.Profile.ReasoningEffort != answer.RepairProfile().ReasoningEffort {
			t.Fatalf("recovery did not begin in reasoning repair: %+v", request.Profile)
		}
	}
	if previous := recoveryProvider.requests[0].Previous; previous == nil || !reflect.DeepEqual(previous.Answers, []string{"B"}) {
		t.Fatalf("recovery did not retain most recent observed vector: %+v", previous)
	}
	usage, err := store.UsageForRun(ctx, recovered.RunID)
	if err != nil || len(usage) != 2 || usage[0].CostMicroUSD != 2 || usage[1].CostMicroUSD != 2 {
		t.Fatalf("repeated candidate usage not durable: %+v err=%v", usage, err)
	}
	public, err := json.Marshal(recovered)
	if err != nil || strings.Contains(string(public), `"answers"`) || strings.Contains(string(public), "formal_history") {
		t.Fatalf("private history leaked into result: %s err=%v", public, err)
	}
}

func TestObjectiveRecoveryRepeatedFailureKeepsModelBudgetAndNeverSubmits(t *testing.T) {
	client, submits := objectiveAttemptServer(t, 99, false)
	store := bank(t)
	first, err := New(store, &objectiveSequenceProvider{answers: []string{"A"}}, Config{MaxModelCalls: 1, MaxEvaluations: 1}).Run(context.Background(), client, req(solver.ModeSubmit), nil)
	if err != nil || first.Status != "attempts_exhausted" || submits.Load() != 1 {
		t.Fatalf("first failure missing: %+v err=%v", first, err)
	}
	provider := &objectiveSequenceProvider{answers: []string{"A", "A", "A"}}
	recovered, err := New(store, provider, Config{MaxModelCalls: 3, MaxEvaluations: 3, BudgetMicroUSD: 11}).Run(context.Background(), client, req(solver.ModeSubmit), nil)
	// Each call reserves ten units and settles two. After the first call, a
	// second reservation would exceed eleven; failure history bypasses neither
	// the existing budget gate nor model accounting.
	if err != nil || recovered.Status != "budget_exhausted" || recovered.ModelCalls != 1 || recovered.CostMicroUSD != 2 || submits.Load() != 1 {
		t.Fatalf("recovery bypassed budget or repeated write: %+v err=%v submits=%d", recovered, err, submits.Load())
	}
}

type objectiveProfileSpy struct {
	objectiveSequenceProvider
	validator *answer.DeepSeek
	estimates []answer.ObjectiveRequest
}

func (p *objectiveProfileSpy) EstimateObjective(req answer.ObjectiveRequest) (answer.Estimate, error) {
	// EstimateObjective builds the real provider's wire request and validates
	// its profile/media contract locally; no model HTTP request is sent.
	if _, err := p.validator.EstimateObjective(req); err != nil {
		return answer.Estimate{}, err
	}
	p.estimates = append(p.estimates, req)
	return p.fakeProvider.EstimateObjective(req)
}

func TestObjectiveImageRepairsRetainReasoning(t *testing.T) {
	for _, tc := range []struct {
		name        string
		providerErr error
		thinking    []string
		passAfter   int
		sharedImage bool
	}{
		{"score repair and pro", nil, []string{"disabled", "enabled", "enabled"}, 3, false},
		{"shared tutorial does not downgrade text questions", nil, []string{"disabled", "enabled", "enabled"}, 3, true},
		{"malformed output retains reasoning", &answer.Error{Kind: "invalid_output_json"}, []string{"disabled", "enabled", "enabled"}, 2, false},
		{"truncation fallback stays compact", &answer.Error{Kind: "truncated_output"}, []string{"disabled", "disabled", "disabled"}, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var task educoder.Task
			if err := json.Unmarshal([]byte(objectiveJSON()), &task); err != nil {
				t.Fatal(err)
			}
			if tc.sharedImage {
				task.Challenge.Description += `<img src="/api/attachments/diagram"> General tutorial figure.`
			} else {
				task.Chooses[0].Subject = `<img src="/api/attachments/diagram"> Which option matches the diagram?`
			}
			task.Chooses[0].Options = append(task.Chooses[0].Options, educoder.ChoiceOption{OptionName: "Third", Position: 2})
			var submits atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch req.URL.Path {
				case "/api/tasks/OBJGAME.json":
					json.NewEncoder(w).Encode(task)
				case "/api/attachments/diagram":
					w.Header().Set("Content-Type", "image/png")
					w.Write(objectiveResourcePNG(t, 1))
				case "/api/tasks/OBJGAME/choose_build.json":
					submits.Add(1)
					fmt.Fprint(w, `{"status":0,"choose_correct_num":null,"challenge_chooses_count":1}`)
				case "/api/homework_commons/44/shixun_challenge_data.json":
					score := "12"
					if int(submits.Load()) >= tc.passAfter {
						score = "13.3"
					}
					fmt.Fprintf(w, `{"data":{"challenge_settings":[{"challenge_id":9,"challenge_score":"13.3","game_score":"%s","passed_status":2}]}}`, score)
				default:
					t.Errorf("unexpected fixture endpoint %s", req.URL.Path)
					http.NotFound(w, req)
				}
			}))
			defer server.Close()
			validator, err := answer.NewDeepSeek(answer.Config{APIKey: "fixture-not-used"})
			if err != nil {
				t.Fatal(err)
			}
			provider := &objectiveProfileSpy{objectiveSequenceProvider: objectiveSequenceProvider{answers: []string{"A", "B", "C"}, errors: map[int]error{0: tc.providerErr}}, validator: validator}
			result, err := New(bank(t), provider, Config{MaxModelCalls: 3, MaxEvaluations: 3}).Run(context.Background(), educoder.New(server.URL), req(solver.ModeSubmit), nil)
			if err != nil || result.Status != "passed" || result.ModelCalls != 3 || result.CostMicroUSD != 6 || len(provider.estimates) != 3 || int(submits.Load()) != tc.passAfter {
				t.Fatalf("image repair failed: %+v err=%v estimates=%d submits=%d", result, err, len(provider.estimates), submits.Load())
			}
			for index, request := range provider.requests {
				if len(request.Resources) != 1 || request.Profile.Thinking != tc.thinking[index] || request.Profile.MaxTokens != 8192 || !reflect.DeepEqual(provider.estimates[index].Profile, request.Profile) {
					t.Fatalf("image/profile drift on attempt %d: %+v resources=%d", index, request.Profile, len(request.Resources))
				}
				if request.Profile.Thinking == "enabled" && request.Profile.ReasoningEffort != "low" {
					t.Fatalf("repair reasoning effort lost: %+v", request.Profile)
				}
				if request.Profile.Thinking == "disabled" && request.Profile.ReasoningEffort != "" {
					t.Fatalf("disabled thinking retained incompatible reasoning effort: %+v", request.Profile)
				}
			}
			if !tc.sharedImage && tc.providerErr == nil && provider.requests[2].Profile.Model != answer.RepairProfile().Model {
				t.Fatalf("repair unexpectedly escalated to Pro: %+v", provider.requests[2].Profile)
			}
		})
	}
}
