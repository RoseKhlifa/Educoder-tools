package taskservice

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/solver"
)

// scoredObjectiveServer scores the single-question quiz by the submitted
// answer: the first distinct answer it sees scores firstScore, any other
// otherScore, never the full 13.3. It records every submitted answer.
func scoredObjectiveServer(t *testing.T, firstScore, otherScore string) (*educoder.Client, *atomic.Int64, func() []string) {
	t.Helper()
	submits := &atomic.Int64{}
	var mu sync.Mutex
	var seen []string
	last := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tasks/OBJGAME.json":
			fmt.Fprint(w, strings.Replace(objectiveJSON(), `{"option_name":"Second","position":1}`, `{"option_name":"Second","position":1},{"option_name":"Third","position":2}`, 1))
		case "/api/tasks/OBJGAME/choose_build.json":
			submits.Add(1)
			body, _ := io.ReadAll(r.Body)
			var payload struct {
				Answer []json.RawMessage `json:"answer"`
			}
			_ = json.Unmarshal(body, &payload)
			mu.Lock()
			last = string(payload.Answer[0])
			seen = append(seen, last)
			mu.Unlock()
			fmt.Fprint(w, `{"status":0,"choose_correct_num":null,"challenge_chooses_count":1}`)
		case "/api/homework_commons/44/shixun_challenge_data.json":
			mu.Lock()
			score := otherScore
			if len(seen) > 0 && last == seen[0] {
				score = firstScore
			}
			mu.Unlock()
			fmt.Fprintf(w, `{"data":{"passed_count":1,"challenge_settings":[{"challenge_id":9,"challenge_score":"13.3","game_score":"%s","passed_status":2}]}}`, score)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return educoder.New(server.URL), submits, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

// A repair that scores lower must not be where the run leaves the student:
// before stopping, the best answers already submitted are submitted again.
func TestObjectiveRunRestoresItsBestScoreBeforeStopping(t *testing.T) {
	client, submits, seen := scoredObjectiveServer(t, "12", "10")
	provider := &objectiveSequenceProvider{answers: []string{"A", "B"}}
	result, err := New(bank(t), provider, Config{MaxModelCalls: 2, MaxEvaluations: 2}).Run(context.Background(), client, req(solver.ModeSubmit), nil)
	answers := seen()
	if err != nil || result.Status != "attempts_exhausted" || submits.Load() != 3 || len(answers) != 3 || answers[2] != answers[0] || result.Score != "12" {
		t.Fatalf("best score not restored: result=%+v err=%v submitted=%v", result, err, answers)
	}
	if !strings.Contains(strings.Join(result.Diagnostics, "|"), "已重新提交本次得分最高的答案") {
		t.Fatalf("restore not reported: %v", result.Diagnostics)
	}
}

// When the last submission is already the best, nothing is resubmitted.
func TestObjectiveRunKeepsALastSubmissionThatIsAlreadyBest(t *testing.T) {
	client, submits, _ := scoredObjectiveServer(t, "10", "12")
	provider := &objectiveSequenceProvider{answers: []string{"A", "B"}}
	result, err := New(bank(t), provider, Config{MaxModelCalls: 2, MaxEvaluations: 2}).Run(context.Background(), client, req(solver.ModeSubmit), nil)
	if err != nil || result.Status != "attempts_exhausted" || submits.Load() != 2 || result.Score != "12" {
		t.Fatalf("unexpected resubmission: result=%+v err=%v submits=%d", result, err, submits.Load())
	}
}

func TestObjectiveRestoreTargetComparesOnlyLikeScores(t *testing.T) {
	h := newObjectiveAttemptHistory(15)
	a := objectiveAttempt{Score: "12.15", MaxScore: "13.5"}
	h.attempts = []objectiveAttempt{a, {Score: "10.80", MaxScore: "13.5"}, {Score: "50", MaxScore: "100"}}
	if got, ok := h.restoreTarget("10.80", "13.5"); !ok || got.Score != "12.15" {
		t.Fatalf("restore target = %+v %v", got, ok)
	}
	if _, ok := h.restoreTarget("12.15", "13.5"); ok {
		t.Fatal("restored when the last score is already the best")
	}
	if _, ok := h.restoreTarget("", "13.5"); ok {
		t.Fatal("restored without a known last score")
	}
}

// A malformed reply switches only the next call to answers-only; after the
// model answers again and the platform scores it, the repair asks for ranked
// alternatives so the score search can localize the wrong question.
func TestObjectiveMalformedReplyDoesNotPinAnswersOnlyForTheRun(t *testing.T) {
	client, _ := objectiveAttemptServer(t, 99, false)
	provider := &objectiveSequenceProvider{answers: []string{"", "A", "B"}, errors: map[int]error{0: &answer.Error{Kind: "invalid_output_json", Retryable: true}}}
	_, _ = New(bank(t), provider, Config{MaxModelCalls: 3, MaxEvaluations: 3}).Run(context.Background(), client, req(solver.ModeSubmit), nil)
	if len(provider.requests) != 3 {
		t.Fatalf("model requests=%d", len(provider.requests))
	}
	if !provider.requests[1].AnswersOnly || provider.requests[1].RequireRankedAnswers {
		t.Fatalf("the call after a malformed reply must be answers-only: %+v", provider.requests[1])
	}
	if provider.requests[2].AnswersOnly || !provider.requests[2].RequireRankedAnswers {
		t.Fatalf("the repair after a scored submission must ask for ranked alternatives again: answers_only=%v ranked=%v", provider.requests[2].AnswersOnly, provider.requests[2].RequireRankedAnswers)
	}
}
