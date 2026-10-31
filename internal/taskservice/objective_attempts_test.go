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

type objectiveSequenceProvider struct {
	fakeProvider
	answers  []string
	errors   map[int]error
	requests []answer.ObjectiveRequest
}

func (p *objectiveSequenceProvider) GenerateObjective(_ context.Context, req answer.ObjectiveRequest) (answer.ObjectiveResult, error) {
	p.requests = append(p.requests, req)
	index := len(p.requests) - 1
	result := answer.ObjectiveResult{Model: req.Profile.Model, CostKnown: true, CostMicroUSD: 2}
	if err := p.errors[index]; err != nil {
		return result, err
	}
	result.Candidate = answer.ObjectiveCandidate{Kind: "objective", Answers: []string{p.answers[index]}}
	return result, nil
}

func objectiveAttemptServer(t *testing.T, passedAfter int, failSubmit bool) (*educoder.Client, *atomic.Int64) {
	t.Helper()
	submits := &atomic.Int64{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tasks/OBJGAME.json":
			fmt.Fprint(w, strings.Replace(objectiveJSON(), `{"option_name":"Second","position":1}`, `{"option_name":"Second","position":1},{"option_name":"Third","position":2}`, 1))
		case "/api/tasks/OBJGAME/choose_build.json":
			submits.Add(1)
			if failSubmit {
				http.Error(w, "unknown submit outcome", http.StatusBadGateway)
				return
			}
			fmt.Fprint(w, `{"status":0,"choose_correct_num":null,"challenge_chooses_count":1}`)
		case "/api/homework_commons/44/shixun_challenge_data.json":
			score := "12"
			if int(submits.Load()) >= passedAfter {
				score = "13.3"
			}
			fmt.Fprintf(w, `{"data":{"passed_count":1,"challenge_settings":[{"challenge_id":9,"challenge_score":"13.3","game_score":"%s","passed_status":2}]}}`, score)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return educoder.New(server.URL), submits
}

func historyFromObjectiveFeedback(t *testing.T, feedback []answer.Feedback) []objectiveAttempt {
	t.Helper()
	for _, item := range feedback {
		if item.Kind != "formal_history" {
			continue
		}
		index := strings.Index(item.Message, "[")
		if index < 0 {
			t.Fatal("history has no bounded candidate list")
		}
		var history []objectiveAttempt
		if err := json.Unmarshal([]byte(item.Message[index:]), &history); err != nil {
			t.Fatal(err)
		}
		return history
	}
	return nil
}

func TestObjectiveKnownFailedCandidatesNeverSubmittedTwice(t *testing.T) {
	for _, tc := range []struct {
		name      string
		answers   []string
		unique    int
		histories []int
	}{
		{"repeat previous", []string{"A", "A", "B"}, 2, []int{0, 1, 1}},
		{"repeat older", []string{"A", "B", "A", "C"}, 3, []int{0, 1, 2, 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, submits := objectiveAttemptServer(t, tc.unique, false)
			provider := &objectiveSequenceProvider{answers: tc.answers}
			result, err := New(bank(t), provider, Config{MaxModelCalls: len(tc.answers), MaxEvaluations: len(tc.answers)}).Run(context.Background(), client, req(solver.ModeSubmit), nil)
			if err != nil || result.Status != "passed" || result.ModelCalls != len(tc.answers) || int(submits.Load()) != tc.unique {
				t.Fatalf("result=%+v error=%v model calls=%d submits=%d", result, err, len(provider.requests), submits.Load())
			}
			for index, request := range provider.requests {
				history := historyFromObjectiveFeedback(t, request.Feedback)
				if len(history) != tc.histories[index] {
					t.Fatalf("request %d history=%+v", index+1, history)
				}
				for _, prior := range history {
					if prior.Score != "12" || prior.MaxScore != "13.3" || prior.Candidate.Kind != "objective" {
						t.Fatalf("lost observed candidate/score pair: %+v", prior)
					}
				}
			}
			public, err := json.Marshal(result)
			if err != nil || strings.Contains(string(public), `"answers"`) || strings.Contains(string(public), "formal_history") {
				t.Fatalf("model-only attempt history leaked into result %s error=%v", public, err)
			}
		})
	}
}

func TestObjectiveRepeatedCandidateStillConsumesBoundedModelBudget(t *testing.T) {
	client, submits := objectiveAttemptServer(t, 99, false)
	provider := &objectiveSequenceProvider{answers: []string{"A", "A", "A"}}
	store := bank(t)
	result, err := New(store, provider, Config{MaxModelCalls: 3, MaxEvaluations: 3}).Run(context.Background(), client, req(solver.ModeSubmit), nil)
	if err != nil || result.Status != "attempts_exhausted" || result.ModelCalls != 3 || result.CostMicroUSD != 6 || submits.Load() != 1 {
		t.Fatalf("result=%+v error=%v submits=%d", result, err, submits.Load())
	}
	usage, err := store.UsageForRun(context.Background(), result.RunID)
	if err != nil || len(usage) != 3 {
		t.Fatalf("repeat generation usage lost: usage=%+v error=%v", usage, err)
	}
}

func TestObjectiveAttemptHistorySurvivesRetryableProviderError(t *testing.T) {
	client, submits := objectiveAttemptServer(t, 2, false)
	provider := &objectiveSequenceProvider{answers: []string{"A", "", "A", "B"}, errors: map[int]error{1: &answer.Error{Kind: "truncated_output"}}}
	result, err := New(bank(t), provider, Config{MaxModelCalls: 4, MaxEvaluations: 4}).Run(context.Background(), client, req(solver.ModeSubmit), nil)
	if err != nil || result.Status != "passed" || result.ModelCalls != 4 || submits.Load() != 2 {
		t.Fatalf("result=%+v error=%v submits=%d", result, err, submits.Load())
	}
	if history := historyFromObjectiveFeedback(t, provider.requests[2].Feedback); len(history) != 1 || history[0].Candidate.Answers[0] != "A" {
		t.Fatalf("provider error dropped known-failed history: %+v", history)
	}
}

func TestObjectiveAttemptHistoryDoesNotRetryUnknownSubmission(t *testing.T) {
	client, submits := objectiveAttemptServer(t, 99, true)
	provider := &objectiveSequenceProvider{answers: []string{"A", "B", "C"}}
	result, err := New(bank(t), provider, Config{MaxModelCalls: 3, MaxEvaluations: 3}).Run(context.Background(), client, req(solver.ModeSubmit), nil)
	if err == nil || result.Status != "uncertain" || result.ModelCalls != 1 || submits.Load() != 1 {
		t.Fatalf("unknown mutation was replayed: result=%+v error=%v submits=%d", result, err, submits.Load())
	}
}

func TestObjectiveAttemptHistoryIsBoundedAndCopiesAnswers(t *testing.T) {
	history := newObjectiveAttemptHistory(2)
	candidate := answer.ObjectiveCandidate{Kind: "objective", Answers: []string{"A"}}
	history.remember(candidate, "1", "2")
	candidate.Answers[0] = "B"
	history.remember(candidate, "0", "2")
	candidate.Answers[0] = "C"
	history.remember(candidate, "0", "2")
	if len(history.byHash) != 2 || len(history.attempts) != 2 || history.attempts[0].Candidate.Answers[0] != "A" || history.attempts[1].Candidate.Answers[0] != "B" {
		t.Fatalf("history is unbounded or retained mutable candidate: %+v", history)
	}
	if !history.contains(answer.ObjectiveCandidate{Kind: "objective", Answers: []string{" a "}}) {
		t.Fatal("equivalent normalized candidate bypassed repeat guard")
	}
	if objectiveAttemptKey(answer.ObjectiveCandidate{Answers: []string{"AB", "C"}}) == objectiveAttemptKey(answer.ObjectiveCandidate{Answers: []string{"A", "BC"}}) {
		t.Fatal("candidate hashing lost question boundaries")
	}
	if newObjectiveAttemptHistory(999).limit != 10 {
		t.Fatal("history upper bound exceeded configured hard limit")
	}
}

func objectiveQuestionResultFixture() (*educoder.ChoiceTask, answer.ObjectiveCandidate, *educoder.ChoiceSubmitResult) {
	trueResult, falseResult, correct := true, false, 1
	options := []educoder.ChoiceOption{{OptionName: "First", Position: 0}, {OptionName: "Second", Position: 1}}
	task := &educoder.ChoiceTask{Questions: []educoder.ChoiceQuestion{
		{Position: 1, QuestionName: "First question", QuestionType: 0, Subject: "Choose first", Category: 1, Options: options},
		{Position: 2, QuestionName: "Second question", QuestionType: 1, Subject: "Choose second", Category: 1, Options: options},
	}}
	candidate := answer.ObjectiveCandidate{Kind: "objective", Answers: []string{"A", "B"}}
	response := &educoder.ChoiceSubmitResult{Count: 2, Correct: &correct, TestSets: []educoder.ChoiceTestSet{
		// Response order does not need to match; the explicit positions do.
		{Position: 2, QuestionName: "Second question", QuestionType: 1, ActualOutput: "B", Result: &falseResult, StandardAnswer: "hidden expected value"},
		{Position: 1, QuestionName: "First question", QuestionType: 0, ActualOutput: "A", Result: &trueResult, StandardAnswer: "hidden expected value"},
	}}
	return task, candidate, response
}

func TestObjectiveQuestionResultsRequireExactCurrentAnswerBinding(t *testing.T) {
	task, candidate, response := objectiveQuestionResultFixture()
	results := trustedObjectiveQuestionResults(task, candidate, response)
	expected := []objectiveQuestionResult{{Position: 1, Answer: "A", Correct: true}, {Position: 2, Answer: "B", Correct: false}}
	if !reflect.DeepEqual(results, expected) {
		t.Fatalf("trusted results=%+v", results)
	}
	history := newObjectiveAttemptHistory(10)
	history.remember(candidate, "1", "2", results...)
	results[0].Answer = "B"
	feedback := history.feedback(nil)
	encoded, err := json.Marshal(feedback)
	if err != nil || strings.Contains(string(encoded), "hidden expected value") || strings.Contains(string(encoded), "standard_answer") {
		t.Fatalf("hidden answer reached model feedback: %s err=%v", encoded, err)
	}
	stored := historyFromObjectiveFeedback(t, feedback)
	if len(stored) != 1 || !reflect.DeepEqual(stored[0].QuestionResults, expected) {
		t.Fatalf("precise question feedback lost or mutated: %+v", stored)
	}
}

func TestObjectiveQuestionResultsIgnoreAmbiguousOrStaleEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*educoder.ChoiceTask, *answer.ObjectiveCandidate, *educoder.ChoiceSubmitResult)
	}{
		{"response count", func(_ *educoder.ChoiceTask, _ *answer.ObjectiveCandidate, r *educoder.ChoiceSubmitResult) { r.Count++ }},
		{"missing result", func(_ *educoder.ChoiceTask, _ *answer.ObjectiveCandidate, r *educoder.ChoiceSubmitResult) {
			r.TestSets = r.TestSets[:1]
		}},
		{"duplicate position", func(_ *educoder.ChoiceTask, _ *answer.ObjectiveCandidate, r *educoder.ChoiceSubmitResult) {
			r.TestSets[0].Position = r.TestSets[1].Position
		}},
		{"wrong position", func(_ *educoder.ChoiceTask, _ *answer.ObjectiveCandidate, r *educoder.ChoiceSubmitResult) {
			r.TestSets[0].Position = 3
		}},
		{"wrong question name", func(_ *educoder.ChoiceTask, _ *answer.ObjectiveCandidate, r *educoder.ChoiceSubmitResult) {
			r.TestSets[0].QuestionName = "Other question"
		}},
		{"empty question name", func(q *educoder.ChoiceTask, _ *answer.ObjectiveCandidate, r *educoder.ChoiceSubmitResult) {
			q.Questions[1].QuestionName, r.TestSets[0].QuestionName = "", ""
		}},
		{"wrong question type", func(_ *educoder.ChoiceTask, _ *answer.ObjectiveCandidate, r *educoder.ChoiceSubmitResult) {
			r.TestSets[0].QuestionType++
		}},
		{"stale answer", func(_ *educoder.ChoiceTask, _ *answer.ObjectiveCandidate, r *educoder.ChoiceSubmitResult) {
			r.TestSets[0].ActualOutput = "A"
		}},
		{"missing answer", func(_ *educoder.ChoiceTask, _ *answer.ObjectiveCandidate, r *educoder.ChoiceSubmitResult) {
			r.TestSets[0].ActualOutput = nil
		}},
		{"nonstring answer", func(_ *educoder.ChoiceTask, _ *answer.ObjectiveCandidate, r *educoder.ChoiceSubmitResult) {
			r.TestSets[0].ActualOutput = []any{"B"}
		}},
		{"missing correctness", func(_ *educoder.ChoiceTask, _ *answer.ObjectiveCandidate, r *educoder.ChoiceSubmitResult) {
			r.TestSets[0].Result = nil
		}},
		{"wrong correctness total", func(_ *educoder.ChoiceTask, _ *answer.ObjectiveCandidate, r *educoder.ChoiceSubmitResult) {
			value := 0
			r.Correct = &value
		}},
		{"missing candidate answer", func(_ *educoder.ChoiceTask, c *answer.ObjectiveCandidate, _ *educoder.ChoiceSubmitResult) {
			c.Answers = c.Answers[:1]
		}},
		{"duplicate task position", func(q *educoder.ChoiceTask, _ *answer.ObjectiveCandidate, _ *educoder.ChoiceSubmitResult) {
			q.Questions[0].Position = q.Questions[1].Position
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task, candidate, response := objectiveQuestionResultFixture()
			tc.mutate(task, &candidate, response)
			if results := trustedObjectiveQuestionResults(task, candidate, response); results != nil {
				t.Fatalf("ambiguous/stale evidence accepted: %+v", results)
			}
		})
	}
}
