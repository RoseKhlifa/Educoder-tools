package solver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

func TestReadOnlyReconciliationRequiresMatchingSourceAndFullScore(t *testing.T) {
	for _, tc := range []struct {
		name             string
		current          string
		score            string
		evaluationStatus string
		badCandidateHash bool
		badTest          bool
		changeAfterScore bool
		wantPassed       bool
	}{
		{name: "saved build later passed", current: "good", score: "13.30", evaluationStatus: "error", wantPassed: true},
		{name: "candidate hash corrupted", current: "good", score: "13.30", evaluationStatus: "error", badCandidateHash: true},
		{name: "old full score with different editor code", current: "unrelated old source", score: "13.30", evaluationStatus: "error"},
		{name: "partial score", current: "good", score: "10", evaluationStatus: "error"},
		{name: "test not fully verified", current: "good", score: "13.30", evaluationStatus: "error", badTest: true},
		{name: "build never started", current: "good", score: "13.30", evaluationStatus: "build_error"},
		{name: "editor changed during score read", current: "good", score: "13.30", evaluationStatus: "error", changeAfterScore: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := "good"
			writes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					writes++
					http.Error(w, "reconcile attempted write", 500)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/tasks/g.json":
					fixture := adapterFixture(12)
					fixture["game"].(map[string]any)["status"] = 2
					test := map[string]any{"is_public": true, "input": "public", "output": "yes", "result": true, "compile_success": 1}
					if tc.badTest {
						test["compile_success"] = nil
					}
					fixture["test_sets"] = []any{test}
					json.NewEncoder(w).Encode(fixture)
				case "/api/tasks/g/rep_content.json":
					json.NewEncoder(w).Encode(map[string]any{"filename": "main.cpp", "language": "cpp", "content": map[string]any{"content": base64.StdEncoding.EncodeToString([]byte(current))}})
				case "/api/homework_commons/1/shixun_challenge_data.json":
					json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"passed_count": 1, "challenge_settings": []any{map[string]any{"challenge_id": 6, "passed_status": 2, "evaluate_count": 1, "challenge_score": "13.3", "game_score": tc.score}}}})
					if tc.changeAfterScore {
						current = "concurrent change"
					}
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client := educoder.New(server.URL)
			adapter, _ := NewEducoderAdapter(client)
			loaded, err := adapter.Load(context.Background(), Target{"c", 1, "g"})
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := problem.Prepare(loaded.Snapshot)
			if err != nil {
				t.Fatal(err)
			}
			ref := questionbank.Ref{RevisionHash: snapshot.RevisionHash, ContractHash: snapshot.ContractHash}
			hash, _ := problem.HashFiles(candidate("good").Files)
			unknown := questionbank.Validation{CandidateID: 9, Kind: "formal", Result: "unknown", RunID: "run-uncertain", FilesHash: hash, SavedFilesHash: hash, SubmittedFilesHash: hash, RevisionHash: ref.RevisionHash, ContractHash: ref.ContractHash}
			cachedHash := hash
			if tc.badCandidateHash {
				cachedHash = strings.Repeat("a", 64)
			}
			store := &fakeStore{candidates: []questionbank.Candidate{{ID: 9, Ref: ref, CandidateInput: questionbank.CandidateInput{Files: candidate("good").Files}, FilesHash: cachedHash, Validations: []questionbank.Validation{unknown}}}, validations: []questionbank.Validation{unknown}}
			svc := New(store, nil, Config{})
			state := runState{Owner: ownerKey("account"), Target: Target{"c", 1, "g"}, SnapshotRef: ref, Result: Result{RunID: "run-uncertain", Status: "uncertain", CandidateID: 9, CodeHash: hash, ModelCalls: 2, CostMicroUSD: 7, Evaluation: &Evaluation{Status: tc.evaluationStatus, SavedFilesHash: hash}}}
			if err := svc.persist(context.Background(), "run-uncertain", state, "uncertain", time.Now()); err != nil {
				t.Fatal(err)
			}
			current = tc.current
			result, err := svc.ReconcileClient(context.Background(), client, "account", "run-uncertain")
			if tc.wantPassed {
				if err != nil || result.Status != "passed" || result.CostMicroUSD != 7 || result.ModelCalls != 2 {
					t.Fatalf("result=%+v err=%v", result, err)
				}
				if len(store.validations) != 2 || store.validations[0].Result != "unknown" || store.validations[1].Result != "passed" || store.validations[1].SubmittedFilesHash != hash {
					t.Fatalf("evidence=%+v", store.validations)
				}
				if _, err := svc.ReconcileClient(context.Background(), client, "account", "run-uncertain"); err != nil || len(store.validations) != 2 {
					t.Fatalf("idempotence err=%v validations=%d", err, len(store.validations))
				}
			} else {
				if err == nil || result.Status != "uncertain" || len(store.validations) != 1 || store.runs["run-uncertain"].Status != "uncertain" {
					t.Fatalf("unverified run was promoted result=%+v err=%v validations=%+v", result, err, store.validations)
				}
			}
			if writes != 0 {
				t.Fatalf("read-only reconciliation made %d writes", writes)
			}
		})
	}
}
