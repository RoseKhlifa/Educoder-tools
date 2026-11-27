package taskservice

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/solver"
)

// withheldObjectiveServer scores the one-question quiz with game_score and
// answers choose_build with build, counting submissions.
func withheldObjectiveServer(t *testing.T, gameScore, build string) (*educoder.Client, *atomic.Int64) {
	t.Helper()
	submits := &atomic.Int64{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tasks/OBJGAME.json":
			fmt.Fprint(w, objectiveJSON())
		case "/api/tasks/OBJGAME/choose_build.json":
			submits.Add(1)
			fmt.Fprint(w, build)
		case "/api/homework_commons/44/shixun_challenge_data.json":
			fmt.Fprintf(w, `{"data":{"passed_count":1,"challenge_settings":[{"challenge_id":9,"challenge_score":"13.3","game_score":%q,"passed_status":2}]}}`, gameScore)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return educoder.New(server.URL), submits
}

// A 补交 quiz scored 0 for the bank's full-score answer (2026-11-27). The run
// stops without paying the model to change correct answers.
func TestObjectiveVerifiedAnswerScoringZeroStopsWithoutTheModel(t *testing.T) {
	store := bank(t)
	seed, _ := withheldObjectiveServer(t, "13.3", `{"status":0,"choose_correct_num":null,"challenge_chooses_count":1}`)
	if r, err := New(store, &fakeProvider{}, Config{}).Run(context.Background(), seed, req(solver.ModeSubmit), nil); err != nil || r.Status != "passed" {
		t.Fatalf("seed run: %+v %v", r, err)
	}
	client, submits := withheldObjectiveServer(t, "0.00", `{"status":0,"choose_correct_num":null,"challenge_chooses_count":1}`)
	provider := &fakeProvider{}
	r, err := New(store, provider, Config{MaxModelCalls: 3, MaxEvaluations: 3}).Run(context.Background(), client, req(solver.ModeSubmit), nil)
	if err != nil || r.Status != "score_withheld" || r.ModelCalls != 0 || provider.objectiveCalls.Load() != 0 || submits.Load() != 1 || !strings.Contains(strings.Join(r.Diagnostics, "|"), "没有再调用模型") {
		t.Fatalf("result=%+v err=%v model=%d submits=%d", r, err, provider.objectiveCalls.Load(), submits.Load())
	}
}

// When the platform counts every answer correct, a score below full is not
// the answers' fault either.
func TestObjectiveAllAnswersCountedCorrectStopsWithoutRepair(t *testing.T) {
	client, submits := withheldObjectiveServer(t, "6.65", `{"status":0,"choose_correct_num":1,"challenge_chooses_count":1}`)
	provider := &fakeProvider{}
	r, err := New(bank(t), provider, Config{MaxModelCalls: 3, MaxEvaluations: 3}).Run(context.Background(), client, req(solver.ModeSubmit), nil)
	if err != nil || r.Status != "score_withheld" || provider.objectiveCalls.Load() != 1 || submits.Load() != 1 || !strings.Contains(strings.Join(r.Diagnostics, "|"), "1/1 题全部正确") {
		t.Fatalf("result=%+v err=%v model=%d submits=%d", r, err, provider.objectiveCalls.Load(), submits.Load())
	}
}

// A platform that counts an answer wrong still gets the normal repair.
func TestObjectiveWrongCountedAnswerIsStillRepaired(t *testing.T) {
	client, submits := withheldObjectiveServer(t, "0.00", `{"status":0,"choose_correct_num":0,"challenge_chooses_count":1}`)
	provider := &fakeProvider{}
	r, _ := New(bank(t), provider, Config{MaxModelCalls: 2, MaxEvaluations: 2}).Run(context.Background(), client, req(solver.ModeSubmit), nil)
	if r == nil || r.Status == "score_withheld" || provider.objectiveCalls.Load() < 2 || submits.Load() < 1 {
		t.Fatalf("wrong answer was not repaired: result=%+v model=%d submits=%d", r, provider.objectiveCalls.Load(), submits.Load())
	}
}
