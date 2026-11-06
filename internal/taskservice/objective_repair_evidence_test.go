package taskservice

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/solver"
)

type objectiveRepairEvidenceProvider struct {
	fakeProvider
	requests []answer.ObjectiveRequest
}

func (p *objectiveRepairEvidenceProvider) GenerateObjective(_ context.Context, request answer.ObjectiveRequest) (answer.ObjectiveResult, error) {
	p.requests = append(p.requests, request)
	answers := []string{"A", "A"}
	if len(p.requests) > 1 {
		answers[1] = "B"
	}
	return answer.ObjectiveResult{
		Candidate: answer.ObjectiveCandidate{Kind: "objective", Answers: answers},
		Model:     "fake", CostKnown: true, CostMicroUSD: 2,
	}, nil
}

// The submit response deliberately omits test_sets. Only the subsequent
// complete task read exposes the exact results for that submitted vector.
// All question text and hidden-answer sentinels are synthetic test data.
func objectiveRepairEvidenceServer(t *testing.T, mutate func(*educoder.ChoiceTestCases)) (*educoder.Client, *atomic.Int64) {
	t.Helper()
	var submits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tasks/OBJGAME.json":
			cases := educoder.ChoiceTestCases{Count: 2}
			if submits.Load() > 0 {
				correct, yes, no := 1, true, false
				cases.Correct, cases.HadSubmit = &correct, true
				cases.TestSets = []educoder.ChoiceTestSet{
					{QuestionName: "first-local", QuestionType: 0, Position: 1, ActualOutput: "A", Result: &yes, StandardAnswer: "private-key-sentinel"},
					{QuestionName: "second-local", QuestionType: 0, Position: 2, ActualOutput: "A", Result: &no, StandardAnswer: "private-key-sentinel"},
				}
				if mutate != nil {
					mutate(&cases)
				}
			}
			encoded, err := json.Marshal(cases)
			if err != nil {
				t.Errorf("encode task results: %v", err)
				return
			}
			fmt.Fprintf(w, `{"st":1,"homework_common_id":44,"game":{"identifier":"OBJGAME","id":7,"myshixun_id":8},"challenge":{"id":9,"st":1,"subject":"Local evidence quiz","task_pass":"Choose the matching local label."},"choose_test_cases":%s,"chooses":[{"challenge_choose_id":10,"challenge_id":9,"position":1,"question_name":"first-local","question_type":0,"category":1,"subject":"First local question?","challenge_question":[{"option_name":"First value","position":0},{"option_name":"Second value","position":1}]},{"challenge_choose_id":11,"challenge_id":9,"position":2,"question_name":"second-local","question_type":0,"category":1,"subject":"Second local question?","challenge_question":[{"option_name":"Left value","position":0},{"option_name":"Right value","position":1}]}]}`, encoded)
		case "/api/tasks/OBJGAME/choose_build.json":
			submits.Add(1)
			fmt.Fprint(w, `{"status":0,"choose_correct_num":null,"challenge_chooses_count":2}`)
		case "/api/homework_commons/44/shixun_challenge_data.json":
			score := "1"
			if submits.Load() >= 2 {
				score = "2"
			}
			fmt.Fprintf(w, `{"data":{"challenge_settings":[{"challenge_id":9,"challenge_score":"2","game_score":"%s"}]}}`, score)
		default:
			t.Errorf("unexpected evidence endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return educoder.New(server.URL), &submits
}

func TestObjectiveRefreshBoundResultsReachRepairHistoryAndLockCorrectAnswers(t *testing.T) {
	client, submits := objectiveRepairEvidenceServer(t, nil)
	provider := &objectiveRepairEvidenceProvider{}
	result, err := New(bank(t), provider, Config{MaxModelCalls: 2, MaxEvaluations: 2}).Run(context.Background(), client, req(solver.ModeSubmit), nil)
	if err != nil || result == nil || result.Status != "passed" || submits.Load() != 2 || len(provider.requests) != 2 {
		t.Fatalf("bound-refresh repair failed: result=%+v err=%v submits=%d requests=%d", result, err, submits.Load(), len(provider.requests))
	}
	repair := provider.requests[1]
	wantResults := []objectiveQuestionResult{{Position: 1, Answer: "A", Correct: true}, {Position: 2, Answer: "A", Correct: false}}
	history := historyFromObjectiveFeedback(t, repair.Feedback)
	if len(history) != 1 || !reflect.DeepEqual(history[0].QuestionResults, wantResults) {
		t.Fatalf("refresh-bound correct/wrong evidence missing from next repair: %+v", history)
	}
	if !reflect.DeepEqual(repair.FixedAnswers, []string{"A", ""}) {
		t.Fatalf("refresh result did not lock only the explicitly correct answer: %v", repair.FixedAnswers)
	}
	feedback, err := json.Marshal(repair.Feedback)
	if err != nil || strings.Contains(string(feedback), "private-key-sentinel") || strings.Contains(string(feedback), "standard_answer") {
		t.Fatalf("hidden expected answer leaked to repair: %s err=%v", feedback, err)
	}
}

func TestObjectiveAmbiguousRefreshResultsNeverBecomeRepairEvidenceOrLocks(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*educoder.ChoiceTestCases)
	}{
		{"stale answer", func(c *educoder.ChoiceTestCases) { c.TestSets[1].ActualOutput = "B" }},
		{"wrong question", func(c *educoder.ChoiceTestCases) { c.TestSets[1].QuestionName = "unrelated-local" }},
		{"missing result", func(c *educoder.ChoiceTestCases) { c.TestSets[1].Result = nil }},
		{"incomplete result roster", func(c *educoder.ChoiceTestCases) { c.TestSets = c.TestSets[:1] }},
		{"duplicate position", func(c *educoder.ChoiceTestCases) { c.TestSets[1].Position = 1 }},
		{"contradictory total", func(c *educoder.ChoiceTestCases) { value := 0; c.Correct = &value }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, submits := objectiveRepairEvidenceServer(t, tc.mutate)
			provider := &objectiveRepairEvidenceProvider{}
			result, err := New(bank(t), provider, Config{MaxModelCalls: 2, MaxEvaluations: 2}).Run(context.Background(), client, req(solver.ModeSubmit), nil)
			if err != nil || result == nil || result.Status != "passed" || submits.Load() != 2 || len(provider.requests) != 2 {
				t.Fatalf("ambiguous-refresh flow failed: result=%+v err=%v submits=%d requests=%d", result, err, submits.Load(), len(provider.requests))
			}
			repair := provider.requests[1]
			history := historyFromObjectiveFeedback(t, repair.Feedback)
			if len(history) != 1 || len(history[0].QuestionResults) != 0 {
				t.Fatalf("ambiguous refresh became trusted per-question history: %+v", history)
			}
			if !reflect.DeepEqual(repair.FixedAnswers, []string{"", ""}) {
				t.Fatalf("ambiguous refresh incorrectly locked an answer: %v", repair.FixedAnswers)
			}
		})
	}
}
