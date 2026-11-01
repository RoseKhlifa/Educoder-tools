package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

func TestQuestionBankSummaryIsSafeAuthenticatedAggregation(t *testing.T) {
	f := newPlatformHTTPFixture(t, nil)
	ctx := context.Background()
	q := answer.ObjectiveQuestion{Position: 1, Question: "PRIVATE_STEM_SENTINEL", QuestionType: 0, Category: 1, Options: []answer.ObjectiveOption{{Label: "A", Text: "PRIVATE_OPTION_SENTINEL", Position: 0}, {Label: "B", Text: "Other", Position: 1}}}
	key, err := f.bank.UpsertObjectiveQuestion(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	id, err := f.bank.AddObjectiveAnswer(ctx, key, "A", "PRIVATE_SOURCE_SENTINEL", "PRIVATE_MODEL_SENTINEL")
	if err != nil {
		t.Fatal(err)
	}
	proof := questionbank.ObjectiveValidation{Passed: true, Confirmed: true, PerQuestionCorrect: true, AnswerHash: problem.HashContent("A"), ScopeHash: problem.HashContent("scope"), Feedback: "PRIVATE_FEEDBACK_SENTINEL"}
	for i := 0; i < 2; i++ {
		if err := f.bank.RecordObjectiveValidation(ctx, id, proof); err != nil {
			t.Fatal(err)
		}
	}
	url := f.server.URL + "/api/question-bank/summary"
	unauthenticated := doJSON(t, http.DefaultClient, http.MethodGet, url, "")
	if unauthenticated.StatusCode != http.StatusUnauthorized || !strings.Contains(unauthenticated.Body, "AUTH_REQUIRED") {
		t.Fatalf("unauthenticated summary = %d %s", unauthenticated.StatusCode, unauthenticated.Body)
	}
	response, err := f.aliceClient.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body struct {
		Data  map[string]any `json:"data"`
		Error *apiError      `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" || body.Error != nil {
		t.Fatalf("authenticated ordinary user = %d %+v", response.StatusCode, body)
	}
	if len(body.Data) != 3 || body.Data["verified_objective_questions"] != float64(1) || body.Data["verified_programming_problems"] != float64(0) || body.Data["total_verified_questions"] != float64(1) {
		t.Fatalf("summary leaked data or miscounted evidence: %+v", body.Data)
	}
	// Cache access still validates the current platform principal each time.
	if err := f.platform.SetStatus(ctx, f.admin.ID, f.alice.ID, "disabled"); err != nil {
		t.Fatal(err)
	}
	disabled := doJSON(t, f.aliceClient, http.MethodGet, url, "")
	if disabled.StatusCode != http.StatusUnauthorized {
		t.Fatalf("cached summary ignored disabled user: %d %s", disabled.StatusCode, disabled.Body)
	}
	admin := doJSON(t, f.adminClient, http.MethodGet, url, "")
	if admin.StatusCode != http.StatusOK || strings.Contains(admin.Body, "PRIVATE_") || strings.Contains(admin.Body, f.alice.ID) {
		t.Fatalf("admin summary changed contract: %d %s", admin.StatusCode, admin.Body)
	}
	wrongMethod := doJSON(t, f.bobClient, http.MethodPost, url, `{}`)
	if wrongMethod.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("summary accepted mutation: %d %s", wrongMethod.StatusCode, wrongMethod.Body)
	}
	foreign := httptest.NewRequest(http.MethodGet, "http://service.example/api/question-bank/summary", nil)
	foreign.Header.Set("Origin", "https://foreign.example")
	w := httptest.NewRecorder()
	f.app.ServeHTTP(w, foreign)
	if w.Code != http.StatusForbidden {
		t.Fatal("summary bypassed same-origin protection")
	}
}

func TestQuestionBankSummaryUnavailableDoesNotReturnZero(t *testing.T) {
	f := newPlatformHTTPFixture(t, nil)
	url := f.server.URL + "/api/question-bank/summary"
	for _, runtime := range []*JobRuntime{nil, {}} {
		f.app.jobs = runtime
		res := doJSON(t, f.aliceClient, http.MethodGet, url, "")
		if res.StatusCode != http.StatusServiceUnavailable || !strings.Contains(res.Body, `"data":null`) || !strings.Contains(res.Body, "QUESTION_BANK_UNAVAILABLE") {
			t.Fatalf("missing bank was fabricated as zero: %d %s", res.StatusCode, res.Body)
		}
	}
	f.app.jobs = &JobRuntime{bank: f.bank}
	if err := f.bank.Close(); err != nil {
		t.Fatal(err)
	}
	res := doJSON(t, f.aliceClient, http.MethodGet, url, "")
	if res.StatusCode != http.StatusServiceUnavailable || !strings.Contains(res.Body, `"data":null`) || strings.Contains(res.Body, "database") {
		t.Fatalf("database failure leaked details or returned zero: %d %s", res.StatusCode, res.Body)
	}
}

func TestQuestionBankSummaryCacheAvoidsRepeatedQueriesAndExpires(t *testing.T) {
	f := newPlatformHTTPFixture(t, nil)
	url := f.server.URL + "/api/question-bank/summary"
	res := doJSON(t, f.aliceClient, http.MethodGet, url, "")
	if res.StatusCode != http.StatusOK || !strings.Contains(res.Body, `"total_verified_questions":0`) {
		t.Fatalf("genuine empty bank summary = %d %s", res.StatusCode, res.Body)
	}
	if err := f.bank.Close(); err != nil {
		t.Fatal(err)
	}
	// A closed DB proves that these repeated authenticated cache hits do not
	// issue another query. No account-specific values are stored in this cache.
	for _, client := range []*http.Client{f.aliceClient, f.bobClient, f.adminClient} {
		res := doJSON(t, client, http.MethodGet, url, "")
		if res.StatusCode != http.StatusOK {
			t.Fatalf("cache did not coalesce repeated requests: %d %s", res.StatusCode, res.Body)
		}
	}
	f.app.questionBankSummary.mu.Lock()
	f.app.questionBankSummary.expires = time.Now().Add(-time.Second)
	f.app.questionBankSummary.mu.Unlock()
	res = doJSON(t, f.aliceClient, http.MethodGet, url, "")
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expired cache hid failed query: %d %s", res.StatusCode, res.Body)
	}
}
