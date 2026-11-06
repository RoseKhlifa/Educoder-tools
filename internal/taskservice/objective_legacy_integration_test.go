package taskservice

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
	"educoder-cli/internal/solver"
)

func legacyIntegrationSnapshot(t *testing.T) answer.ObjectiveSnapshot {
	t.Helper()
	var task educoder.Task
	if err := json.Unmarshal([]byte(objectiveJSON()), &task); err != nil {
		t.Fatal(err)
	}
	return objectiveSnapshot(&educoder.ChoiceTask{Task: &task, Questions: task.Chooses})
}

func seedLegacyIntegrationAnswer(t *testing.T, store *questionbank.Store, snapshot answer.ObjectiveSnapshot, scope string) int64 {
	t.Helper()
	ctx := context.Background()
	key, err := store.UpsertObjectiveQuestion(ctx, snapshot.Questions[0])
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.AddObjectiveAnswer(ctx, key, "A", "preserved-old-bank", "old-model")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.RecordObjectiveValidation(ctx, id, questionbank.ObjectiveValidation{
		Confirmed: true, Passed: true, FullScore: true, Score: "13.30", MaxScore: "13.3",
		AnswerHash: problem.HashContent("A"), ScopeHash: scope,
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestLegacyObjectiveExactHistoricalContextAvoidsProviderCalls(t *testing.T) {
	ctx := context.Background()
	store := bank(t)
	snapshot := legacyIntegrationSnapshot(t)
	scope, err := store.PutObjectiveSnapshot(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	id := seedLegacyIntegrationAnswer(t, store, snapshot, scope)
	if _, err = store.AdaptLegacyObjectiveBank(ctx); err != nil {
		t.Fatal(err)
	}
	client, submits := objectiveServer(t, `"13.30"`, false)
	provider := &fakeProvider{}
	result, err := New(store, provider, Config{}).Run(ctx, client, req(solver.ModeSubmit), nil)
	if err != nil || result == nil || result.Status != "passed" || result.ModelCalls != 0 || provider.objectiveCalls.Load() != 0 || submits.Load() != 1 {
		t.Fatalf("legacy exact-context reuse invoked provider or failed: result=%+v err=%v calls=%d submits=%d", result, err, provider.objectiveCalls.Load(), submits.Load())
	}
	input := objectiveBankInput(snapshot, snapshot.Questions[0], nil, true)
	key, _, err := store.UpsertObjectiveQuestionWithContext(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	answers, err := store.LookupObjectiveAnswersWithContext(ctx, key)
	if err != nil || len(answers) != 1 || answers[0].ID != id || !answers[0].Reusable {
		t.Fatalf("legacy exact-context answer ID was lost/copied: answers=%+v err=%v", answers, err)
	}
}

type legacyReferenceProvider struct {
	fakeProvider
	requests []answer.ObjectiveRequest
	answers  []string
}

func (p *legacyReferenceProvider) GenerateObjective(ctx context.Context, request answer.ObjectiveRequest) (answer.ObjectiveResult, error) {
	p.requests = append(p.requests, request)
	if p.answers != nil {
		p.objectiveCalls.Add(1)
		return answer.ObjectiveResult{Candidate: answer.ObjectiveCandidate{Kind: "objective", Answers: append([]string(nil), p.answers...)}, Model: "fake", CostKnown: true, CostMicroUSD: 2}, nil
	}
	return p.fakeProvider.GenerateObjective(ctx, request)
}

func TestLegacyObjectiveNineOfTenAnswersStayFixedDuringSingleProviderCall(t *testing.T) {
	ctx := context.Background()
	store := bank(t)
	var task educoder.Task
	if err := json.Unmarshal([]byte(objectiveJSON()), &task); err != nil {
		t.Fatal(err)
	}
	original := task.Chooses[0]
	task.Chooses = nil
	for i := 0; i < 10; i++ {
		question := original
		question.Subject = fmt.Sprintf("Question %d?", i+1)
		question.Position = i + 1
		question.ChallengeChooseID = int64(10 + i)
		task.Chooses = append(task.Chooses, question)
	}
	task.ChooseTestCases.Count = len(task.Chooses)
	wire, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := objectiveSnapshot(&educoder.ChoiceTask{Task: &task, Questions: task.Chooses})
	scope, err := store.PutObjectiveSnapshot(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 9)
	for i := range ids {
		one := snapshot
		one.Questions = snapshot.Questions[i : i+1]
		ids[i] = seedLegacyIntegrationAnswer(t, store, one, scope)
	}
	if _, err = store.AdaptLegacyObjectiveBank(ctx); err != nil {
		t.Fatal(err)
	}
	var submits atomic.Int64
	var completeVector atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tasks/OBJGAME.json":
			w.Write(wire)
		case "/api/tasks/OBJGAME/choose_build.json":
			submits.Add(1)
			var body struct {
				Answers []string `json:"answer"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Answers) != 10 {
				t.Errorf("expected one complete ten-question answer vector: %+v err=%v", body, err)
			} else {
				valid := body.Answers[9] == "B"
				for _, value := range body.Answers[:9] {
					valid = valid && value == "A"
				}
				completeVector.Store(valid)
			}
			fmt.Fprint(w, `{"status":0,"choose_correct_num":null,"challenge_chooses_count":10}`)
		case "/api/homework_commons/44/shixun_challenge_data.json":
			fmt.Fprint(w, `{"data":{"challenge_settings":[{"challenge_id":9,"challenge_score":"13.3","game_score":"13.30"}]}}`)
		default:
			t.Errorf("unexpected partial-bank endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	provider := &legacyReferenceProvider{answers: []string{"A", "A", "A", "A", "A", "A", "A", "A", "A", "B"}}
	result, err := New(store, provider, Config{MaxModelCalls: 1, MaxEvaluations: 1}).Run(ctx, educoder.New(server.URL), req(solver.ModeSubmit), nil)
	if err != nil || result == nil || result.Status != "passed" || result.ModelCalls != 1 || provider.objectiveCalls.Load() != 1 || submits.Load() != 1 || !completeVector.Load() || len(provider.requests) != 1 {
		t.Fatalf("partial old-bank reuse failed: result=%+v err=%v calls=%d submits=%d", result, err, provider.objectiveCalls.Load(), submits.Load())
	}
	fixed := provider.requests[0].FixedAnswers
	if len(fixed) != 10 || fixed[9] != "" {
		t.Fatalf("unknown question was omitted or falsely fixed: %v", fixed)
	}
	for i, oldID := range ids {
		if fixed[i] != "A" {
			t.Fatalf("known legacy question %d was unnecessarily re-solved: %v", i+1, fixed)
		}
		key, _, err := store.UpsertObjectiveQuestionWithContext(ctx, objectiveBankInput(snapshot, snapshot.Questions[i], nil, true))
		if err != nil {
			t.Fatal(err)
		}
		answers, err := store.LookupObjectiveAnswersWithContext(ctx, key)
		if err != nil || len(answers) != 1 || answers[0].ID != oldID || !answers[0].Reusable {
			t.Fatalf("known legacy question %d was copied/lost: answers=%+v err=%v", i+1, answers, err)
		}
	}
	reused, err := New(store, nil, Config{}).Run(ctx, educoder.New(server.URL), req(solver.ModeGenerateOnly), nil)
	if err != nil || reused == nil || reused.Status != "generated" || reused.ModelCalls != 0 || submits.Load() != 1 {
		t.Fatalf("confirmed ten-question vector required another model call: result=%+v err=%v submits=%d", reused, err, submits.Load())
	}
}

func TestLegacyObjectiveNewExplicitWrongResultReleasesFixedAnswerForNextRepair(t *testing.T) {
	ctx := context.Background()
	store := bank(t)
	snapshot := legacyIntegrationSnapshot(t)
	scope, err := store.PutObjectiveSnapshot(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	oldID := seedLegacyIntegrationAnswer(t, store, snapshot, scope)
	if _, err = store.AdaptLegacyObjectiveBank(ctx); err != nil {
		t.Fatal(err)
	}
	wire := strings.Replace(objectiveJSON(), `"question_type":0`, `"question_name":"single","question_type":0`, 1)
	var submits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tasks/OBJGAME.json":
			fmt.Fprint(w, wire)
		case "/api/tasks/OBJGAME/choose_build.json":
			attempt := submits.Add(1)
			var body struct {
				Answers []string `json:"answer"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if attempt == 1 {
				if len(body.Answers) != 1 || body.Answers[0] != "A" {
					t.Errorf("first submission lost the historical fixed answer: %+v", body)
				}
				fmt.Fprint(w, `{"status":0,"choose_correct_num":0,"challenge_chooses_count":1,"test_sets":[{"question_name":"single","question_type":0,"position":1,"actual_output":"A","result":false}]}`)
			} else {
				if len(body.Answers) != 1 || body.Answers[0] != "B" {
					t.Errorf("repair repeated the explicitly wrong old fixed answer: %+v", body)
				}
				fmt.Fprint(w, `{"status":0,"choose_correct_num":1,"challenge_chooses_count":1}`)
			}
		case "/api/homework_commons/44/shixun_challenge_data.json":
			score := "0"
			if submits.Load() > 1 {
				score = "13.30"
			}
			fmt.Fprintf(w, `{"data":{"challenge_settings":[{"challenge_id":9,"challenge_score":"13.3","game_score":%q}]}}`, score)
		default:
			t.Errorf("unexpected repair endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	provider := &legacyReferenceProvider{answers: []string{"B"}}
	result, err := New(store, provider, Config{MaxModelCalls: 1, MaxEvaluations: 2}).Run(ctx, educoder.New(server.URL), req(solver.ModeSubmit), nil)
	if err != nil || result == nil || result.Status != "passed" || result.ModelCalls != 1 || submits.Load() != 2 || len(provider.requests) != 1 {
		t.Fatalf("explicit failure did not unlock and repair old answer: result=%+v err=%v submits=%d requests=%d", result, err, submits.Load(), len(provider.requests))
	}
	if fixed := provider.requests[0].FixedAnswers; len(fixed) != 1 || fixed[0] != "" {
		t.Fatalf("new wrong result left stale answer locked during repair: %v", fixed)
	}
	key, _, err := store.UpsertObjectiveQuestionWithContext(ctx, objectiveBankInput(snapshot, snapshot.Questions[0], nil, true))
	if err != nil {
		t.Fatal(err)
	}
	answers, err := store.LookupObjectiveAnswersWithContext(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	passed := false
	for _, candidate := range answers {
		if candidate.ID == oldID && candidate.Reusable {
			t.Fatal("explicitly rejected old answer remained reusable")
		}
		passed = passed || candidate.Reusable && candidate.Answer == "B"
	}
	if !passed {
		t.Fatalf("current full-score repair was not reusable: %+v", answers)
	}
}

func TestLegacyObjectiveStandaloneReferenceBecomesContextAliasWithoutCopyingContribution(t *testing.T) {
	ctx := context.Background()
	store := bank(t)
	snapshot := legacyIntegrationSnapshot(t)
	// A standalone record has real historical correctness, but no historical
	// shared material. It must be preserved as a reference, not fabricated a lock.
	id := seedLegacyIntegrationAnswer(t, store, snapshot, problem.HashContent("old-independent-scope"))
	owner := problem.HashContent("original-account")
	if err := store.RecordObjectiveContribution(ctx, owner, id); err != nil {
		t.Fatal(err)
	}
	client, submits := objectiveServer(t, `"13.30"`, false)
	provider := &legacyReferenceProvider{}
	request := req(solver.ModeSubmit)
	request.Owner = owner
	result, err := New(store, provider, Config{}).Run(ctx, client, request, nil)
	if err != nil || result == nil || result.Status != "passed" || result.ModelCalls != 1 || provider.objectiveCalls.Load() != 1 || submits.Load() != 1 || len(provider.requests) != 1 {
		t.Fatalf("standalone legacy confirmation failed: result=%+v err=%v calls=%d submits=%d", result, err, provider.objectiveCalls.Load(), submits.Load())
	}
	generated := provider.requests[0]
	if len(generated.FixedAnswers) != 1 || generated.FixedAnswers[0] != "" {
		t.Fatalf("unbound legacy reference became a locked answer: %+v", generated.FixedAnswers)
	}
	referenceFound := false
	for _, feedback := range generated.Feedback {
		if feedback.Kind == "legacy_bank_reference" && strings.Contains(feedback.Message, `"answer":"A"`) {
			referenceFound = true
		}
	}
	if !referenceFound {
		t.Fatal("verified standalone legacy answer was discarded instead of supplied as a reference")
	}
	input := objectiveBankInput(snapshot, snapshot.Questions[0], nil, true)
	key, _, err := store.UpsertObjectiveQuestionWithContext(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	answers, err := store.LookupObjectiveAnswersWithContext(ctx, key)
	if err != nil || len(answers) != 1 || answers[0].ID != id || !answers[0].Reusable || answers[0].Source != "preserved-old-bank" {
		t.Fatalf("current formal pass failed to bind original answer: answers=%+v err=%v", answers, err)
	}
	report, err := store.ObjectiveLegacyCompatibilityReport(ctx)
	if err != nil || report.LegacyAnswers != 1 || report.AdaptedLegacyAnswers != 1 || report.Contributions != 1 {
		t.Fatalf("legacy answer/contribution was lost or duplicated: report=%+v err=%v", report, err)
	}
	if count, err := store.ContributedQuestions(ctx, owner); err != nil || count != 1 {
		t.Fatalf("original contribution changed after contextual confirmation: count=%d err=%v", count, err)
	}
	// Full-score responses with hidden test_sets still establish an exact
	// whole-vector proof and per-question compatibility for future requests.
	request.Mode = solver.ModeGenerateOnly
	reused, err := New(store, nil, Config{}).Run(ctx, client, request, nil)
	if err != nil || reused == nil || reused.Status != "generated" || reused.ModelCalls != 0 || submits.Load() != 1 {
		t.Fatalf("confirmed old answer required another solve: result=%+v err=%v submits=%d", reused, err, submits.Load())
	}
}

func mutateObjectiveWire(t *testing.T, raw string, mutate func(map[string]any)) string {
	t.Helper()
	var fixture map[string]any
	if err := json.Unmarshal([]byte(raw), &fixture); err != nil {
		t.Fatal(err)
	}
	mutate(fixture)
	changed, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	return string(changed)
}

func TestIncompleteObjectiveCurrentReadDoesNotUseLegacyBankModelOrSubmit(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"omitted type not zero type", func(f map[string]any) { delete(f["chooses"].([]any)[0].(map[string]any), "question_type") }},
		{"omitted shared material", func(f map[string]any) { delete(f["challenge"].(map[string]any), "task_pass") }},
		{"advertised roster truncated", func(f map[string]any) { f["choose_test_cases"].(map[string]any)["challenge_chooses_count"] = 2 }},
		{"truncated option roster", func(f map[string]any) {
			question := f["chooses"].([]any)[0].(map[string]any)
			question["challenge_question"] = question["challenge_question"].([]any)[:1]
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := bank(t)
			snapshot := legacyIntegrationSnapshot(t)
			scope, err := store.PutObjectiveSnapshot(ctx, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			seedLegacyIntegrationAnswer(t, store, snapshot, scope)
			if _, err = store.AdaptLegacyObjectiveBank(ctx); err != nil {
				t.Fatal(err)
			}
			raw := mutateObjectiveWire(t, objectiveJSON(), tc.mutate)
			var writes atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					writes.Add(1)
				}
				if r.URL.Path != "/api/tasks/OBJGAME.json" {
					t.Errorf("incomplete objective task reached unexpected endpoint %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				fmt.Fprint(w, raw)
			}))
			defer server.Close()
			provider := &fakeProvider{}
			result, err := New(store, provider, Config{}).Run(ctx, educoder.New(server.URL), req(solver.ModeSubmit), nil)
			if err == nil || result == nil || result.Status != "objective_read_incomplete" || provider.objectiveCalls.Load() != 0 || writes.Load() != 0 {
				t.Fatalf("incomplete read used bank/model/upstream write: result=%+v err=%v calls=%d writes=%d", result, err, provider.objectiveCalls.Load(), writes.Load())
			}
		})
	}
}

func TestObjectiveChangedSnapshotDuringGenerationStopsBeforeUpstreamWrite(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"shared material changed", func(f map[string]any) { f["challenge"].(map[string]any)["task_pass"] = "Now choose the second option" }},
		{"question changed", func(f map[string]any) { f["chooses"].([]any)[0].(map[string]any)["subject"] = "Different question?" }},
		{"option changed", func(f map[string]any) {
			f["chooses"].([]any)[0].(map[string]any)["challenge_question"].([]any)[0].(map[string]any)["option_name"] = "Changed meaning"
		}},
		{"fresh read incomplete", func(f map[string]any) { delete(f["chooses"].([]any)[0].(map[string]any), "question_type") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := mutateObjectiveWire(t, objectiveJSON(), tc.mutate)
			var generated atomic.Bool
			var writes atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					writes.Add(1)
				}
				if r.URL.Path != "/api/tasks/OBJGAME.json" {
					t.Errorf("changed snapshot reached unexpected endpoint %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				if generated.Load() {
					fmt.Fprint(w, changed)
				} else {
					fmt.Fprint(w, objectiveJSON())
				}
			}))
			defer server.Close()
			provider := &fakeProvider{generate: func(context.Context) error { generated.Store(true); return nil }}
			result, err := New(bank(t), provider, Config{}).Run(context.Background(), educoder.New(server.URL), req(solver.ModeSubmit), nil)
			if err == nil || result == nil || result.Status != "verification_unconfirmed" || provider.objectiveCalls.Load() != 1 || writes.Load() != 0 {
				t.Fatalf("changed snapshot received a stale answer write: result=%+v err=%v calls=%d writes=%d", result, err, provider.objectiveCalls.Load(), writes.Load())
			}
		})
	}
}
