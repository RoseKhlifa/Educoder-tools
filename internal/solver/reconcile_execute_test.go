package solver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

// TestRunReconcilesUnknownFormalEvaluationReadOnly exercises the production
// solver path, rather than calling ReconcileClient directly. A status response
// that is unknown after the one allowed build is followed by fresh task,
// full-score and source reads. The solver may promote only that exact proof;
// no second save/build or model generation is allowed.
func TestRunReconcilesUnknownFormalEvaluationReadOnly(t *testing.T) {
	current := "template"
	var writes, providerCalls int
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		if r.Method != http.MethodGet {
			writes++
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tasks/g.json":
			fixture := adapterFixture(12)
			game := fixture["game"].(map[string]any)
			if writes >= 2 { // build has completed; the next GET is fresh proof.
				game["status"] = 2
			}
			// Keep the task contract stable across reads; only the runtime
			// status changes after the original build.
			fixture["test_sets"] = []any{map[string]any{"is_public": false, "result": true, "compile_success": 1}}
			json.NewEncoder(w).Encode(fixture)
		case "/api/tasks/g/rep_content.json":
			json.NewEncoder(w).Encode(map[string]any{
				"filename": "main.cpp", "language": "cpp",
				"content": map[string]any{"content": base64.StdEncoding.EncodeToString([]byte(current))},
			})
		case "/api/myshixuns/instance/update_file.json":
			current = "good"
			json.NewEncoder(w).Encode(map[string]any{"content": map[string]any{"commitID": "commit"}, "sec_key": "private", "resubmit": "resume"})
		case "/api/tasks/g/game_build.json":
			json.NewEncoder(w).Encode(map[string]any{"status": 1, "port": 1})
		case "/api/tasks/g/game_status.json":
			// Status 1 is not accepted by WaitEvaluation, so the delivery result
			// is unknown without asking the platform to execute anything again.
			json.NewEncoder(w).Encode(map[string]any{"status": 1})
		case "/api/homework_commons/1/shixun_challenge_data.json":
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
				"passed_count":       1,
				"challenge_settings": []any{map[string]any{"challenge_id": 6, "passed_status": 2, "evaluate_count": 1, "challenge_score": "13.3", "game_score": "13.3"}},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := educoder.New(server.URL)
	adapter, err := NewEducoderAdapter(client)
	if err != nil {
		t.Fatal(err)
	}
	wrapped := &wrappedEducoderAdapter{EducoderAdapter: adapter}
	provider := &fakeProvider{candidates: []answer.Candidate{candidate("good")}}
	countingProvider := &countingProvider{Provider: provider, calls: &providerCalls}
	store := &fakeStore{}
	// The in-memory test store normally uses sentinel refs. Reconciliation
	// intentionally requires the real revision/contract pair, so seed the
	// sentinel with the exact observed contract used by this run.
	initial, err := wrapped.Load(context.Background(), Target{"c", 1, "g"})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := problem.Prepare(initial.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	store.ref = questionbank.Ref{RevisionHash: prepared.RevisionHash, ContractHash: prepared.ContractHash}
	service := New(store, countingProvider, Config{Timeout: 3 * time.Second, MaxModelCalls: 1, MaxEvaluations: 1})
	result, err := service.Run(context.Background(), wrapped, Request{
		Owner: "account", Course: "c", HomeworkID: 1, Game: "g", Mode: ModeSubmit,
		IdempotencyKey: "reconcile-read-only", Timeout: 3 * time.Second,
	})
	if err != nil || result.Status != "passed" {
		t.Fatalf("unknown formal result was not reconciled: result=%+v err=%v paths=%v", result, err, paths)
	}
	if provider.calls != 1 || providerCalls != 1 {
		t.Fatalf("reconciliation invoked the model: provider=%d counted=%d", provider.calls, providerCalls)
	}
	// Exactly one SaveCode and one BuildCode are allowed. Reconciliation must
	// stay read-only and therefore cannot increase this count.
	if writes != 2 {
		t.Fatalf("reconciliation replayed a mutation: writes=%d", writes)
	}
	if len(store.validations) < 2 || store.validations[len(store.validations)-1].Result != "passed" {
		t.Fatalf("missing reconciled formal evidence: %+v", store.validations)
	}
}

// countingProvider makes the no-extra-model assertion explicit without
// changing the deterministic fake provider used by the solver test suite.
type countingProvider struct {
	answer.Provider
	calls *int
}

type wrappedEducoderAdapter struct{ *EducoderAdapter }

func (p *countingProvider) Generate(ctx context.Context, req answer.Request) (answer.Result, error) {
	*p.calls++
	return p.Provider.Generate(ctx, req)
}
