package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/questionbank"
)

func addWebCompletion(t *testing.T, f *platformHTTPFixture, id, owner string) {
	t.Helper()
	ctx := context.Background()
	q := answer.ObjectiveQuestion{Position: 1, Question: "PRIVATE_STEM_SENTINEL", QuestionType: 0, Category: 1, Options: []answer.ObjectiveOption{{Label: "A", Text: "PRIVATE_OPTION_SENTINEL", Position: 0}}}
	snap := answer.ObjectiveSnapshot{Kind: "objective", Questions: []answer.ObjectiveQuestion{q}}
	key, err := f.bank.PutObjectiveSnapshot(ctx, snap)
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.bank.AddObjectiveCandidate(ctx, key, snap, answer.ObjectiveCandidate{Kind: "objective", Answers: []string{"A"}}, "PRIVATE_SOURCE_SENTINEL")
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(owner))
	raw, _ := json.Marshal(map[string]any{"mode": "submit", "target": map[string]any{"Course": "PRIVATE_COURSE_SENTINEL", "HomeworkID": 1, "Game": "PRIVATE_GAME_SENTINEL"}, "result": map[string]any{"run_id": id, "kind": "objective", "status": "passed", "candidate_id": c.ID}})
	if err := f.bank.PutRun(ctx, questionbank.RunRecord{ID: id, Owner: hex.EncodeToString(hash[:]), Status: "passed", Data: raw}); err != nil {
		t.Fatal(err)
	}
	if err := f.bank.RecordObjectiveVerification(ctx, c.ID, id, "full_score", "1", "1"); err != nil {
		t.Fatal(err)
	}
}

func TestTodaySummaryIsAuthenticatedAnonymousAggregateOfLinkedStudents(t *testing.T) {
	f := newPlatformHTTPFixture(t, nil)
	addWebCompletion(t, f, "alice-completed", f.alice.Owner())
	addWebCompletion(t, f, "alice-retry", f.alice.Owner())
	addWebCompletion(t, f, "bob-completed", f.bob.Owner())
	addWebCompletion(t, f, "old-cli", "legacy-cli-owner")
	url := f.server.URL + "/api/service/today-summary"
	unauth := doJSON(t, http.DefaultClient, http.MethodGet, url, "")
	if unauth.StatusCode != http.StatusUnauthorized || !strings.Contains(unauth.Body, "AUTH_REQUIRED") {
		t.Fatalf("unauthenticated today summary = %d %s", unauth.StatusCode, unauth.Body)
	}
	response, err := f.aliceClient.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body struct {
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" || len(body.Data) != 4 || body.Data["completed_students"] != float64(2) || body.Data["completed_questions"] != float64(2) || body.Data["timezone"] != "Asia/Shanghai" {
		t.Fatalf("anonymous aggregate = %d %+v", response.StatusCode, body.Data)
	}
	if body.Data["date"] != time.Now().In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02") {
		t.Fatalf("wrong local service day: %+v", body.Data)
	}
	admin := doJSON(t, f.adminClient, http.MethodGet, url, "")
	if admin.StatusCode != http.StatusOK || strings.Contains(admin.Body, "PRIVATE_") || strings.Contains(admin.Body, f.alice.ID) || strings.Contains(admin.Body, "owner") {
		t.Fatalf("summary disclosed private evidence: %d %s", admin.StatusCode, admin.Body)
	}
	// Cache hits must still validate the latest status of the requester.
	if err := f.platform.SetStatus(context.Background(), f.admin.ID, f.alice.ID, "disabled"); err != nil {
		t.Fatal(err)
	}
	if res := doJSON(t, f.aliceClient, http.MethodGet, url, ""); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("cached aggregate accepted disabled principal: %d %s", res.StatusCode, res.Body)
	}
	if res := doJSON(t, f.bobClient, http.MethodPost, url, `{}`); res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("aggregate accepted mutation: %d %s", res.StatusCode, res.Body)
	}
	foreign := httptest.NewRequest(http.MethodGet, "http://service.example/api/service/today-summary", nil)
	foreign.Header.Set("Origin", "https://foreign.example")
	w := httptest.NewRecorder()
	f.app.ServeHTTP(w, foreign)
	if w.Code != http.StatusForbidden {
		t.Fatal("summary bypassed same-origin check")
	}
}

func TestTodaySummaryUnavailableDoesNotFabricateZero(t *testing.T) {
	for _, tc := range []string{"runtime", "bank", "platform", "closed-bank", "closed-platform"} {
		t.Run(tc, func(t *testing.T) {
			f := newPlatformHTTPFixture(t, nil)
			switch tc {
			case "runtime":
				f.app.jobs = nil
			case "bank":
				f.app.jobs.bank = nil
			case "platform":
				f.app.platform = nil
			case "closed-bank":
				f.bank.Close()
			case "closed-platform":
				// A closed platform invalidates the current session too. Exercise
				// the cache loader to verify its internal read failure separately.
				f.platform.Close()
				if _, err := f.app.todaySummary.load(context.Background(), f.bank, f.platform, time.Now()); err == nil {
					t.Fatal("failed owner registry fabricated zero")
				}
				return
			}
			res := doJSON(t, f.bobClient, http.MethodGet, f.server.URL+"/api/service/today-summary", "")
			if res.StatusCode != http.StatusServiceUnavailable || !strings.Contains(res.Body, `"data":null`) || !strings.Contains(res.Body, "SERVICE_SUMMARY_UNAVAILABLE") || strings.Contains(res.Body, "database") {
				t.Fatalf("unavailable source became zero or disclosed internals: %d %s", res.StatusCode, res.Body)
			}
		})
	}
}

func TestTodaySummaryCacheCoalescesRequestsAndChangesAtShanghaiMidnight(t *testing.T) {
	f := newPlatformHTTPFixture(t, nil)
	cache := &f.app.todaySummary
	at := time.Date(2026, 11, 2, 15, 59, 50, 0, time.UTC)
	if got, err := cache.load(context.Background(), f.bank, f.platform, at); err != nil || got.Date != "2026-11-02" || got.CompletedQuestions != 0 {
		t.Fatalf("empty genuine summary = %+v %v", got, err)
	}
	f.bank.Close()
	if _, err := cache.load(context.Background(), f.bank, f.platform, at.Add(5*time.Second)); err != nil {
		t.Fatalf("cache issued a repeated database read: %v", err)
	}
	if _, err := cache.load(context.Background(), f.bank, f.platform, at.Add(10*time.Second)); err == nil {
		t.Fatal("yesterday's cache survived Shanghai midnight")
	}
	if _, err := cache.load(context.Background(), f.bank, f.platform, at.Add(61*time.Second)); err == nil {
		t.Fatal("expired cache hid an unavailable database")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cache.load(ctx, f.bank, f.platform, at.Add(5*time.Second)); err == nil {
		t.Fatal("canceled request reused the cache")
	}
}
