package solver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

func TestSelfTestProvenanceSurvivesDifferentRepeatedCandidate(t *testing.T) {
	for _, continued := range []bool{false, true} {
		name := "single run"
		if continued {
			name = "checkpoint continuation"
		}
		t.Run(name, func(t *testing.T) {
			store := &fakeStore{}
			provider := &scriptedProvider{estimateCost: 1, generate: func(_ context.Context, _ answer.Request, n int) (answer.Result, error) {
				if n == 2 {
					return solved("A", 1), nil
				}
				return solved("B", 1), nil
			}}
			adapter := &repairHistoryAdapter{fakeAdapter: fakeAdapter{snap: twoSampleSnapshot()}}
			req := recoveryRequest()
			req.Mode, req.MaxModelCalls, req.MaxEvaluations = ModeSubmit, 10, 10
			if continued {
				req.MaxModelCalls, req.MaxEvaluations = 2, 2
			}
			svc := New(store, provider, Config{})
			r, err := svc.Run(context.Background(), adapter, req)
			if continued {
				if err != nil || r.Status != "selftest_failed" || adapter.selfTests != 4 {
					t.Fatalf("first result=%+v err=%v tests=%d", r, err, adapter.selfTests)
				}
				r, err = New(store, provider, Config{}).ContinueRun(context.Background(), adapter, req, r.RunID, Continuation{AdditionalModelCalls: 8, AdditionalEvaluations: 8})
			}
			if err != nil || r.Status != "generation_stalled" || r.ModelCalls != 3 || r.CostMicroUSD != 3 || adapter.selfTests != 4 || adapter.submits != 0 || len(store.candidates) != 2 {
				t.Fatalf("result=%+v err=%v tested=%v candidates=%d", r, err, adapter.seen, len(store.candidates))
			}
			// B is the final generated candidate, but A owns the last actual
			// selftest evidence. Repeated B must not relabel A's observations.
			latest, tested := store.candidates[0], store.candidates[1]
			if r.CandidateID != latest.ID || r.CodeHash != latest.FilesHash || latest.FilesHash == tested.FilesHash || len(r.SelfTests) != 2 {
				t.Fatalf("missing distinct latest/tested candidates: result=%+v", r)
			}
			for _, st := range r.SelfTests {
				if st.CandidateID != tested.ID || st.FilesHash != tested.FilesHash || st.SavedFilesHash != tested.FilesHash {
					t.Fatalf("observation mislabeled as last generated candidate: %+v", st)
				}
			}
			var state runState
			record := store.runs[r.RunID]
			if err := json.Unmarshal(record.Data, &state); err != nil {
				t.Fatal(err)
			}
			for _, st := range state.Result.SelfTests {
				if st.CandidateID != tested.ID || st.FilesHash != tested.FilesHash {
					t.Fatalf("checkpoint lost selftest identity: %+v", st)
				}
			}
			if !strings.Contains(string(record.Data), `"candidate_id":`) || !strings.Contains(string(record.Data), `"files_hash":`) {
				t.Fatal("checkpoint did not persist explicit observation fields")
			}
		})
	}
}

func TestSelfTestProvenanceDoesNotReplaceSavedSourceMismatch(t *testing.T) {
	store := &fakeStore{}
	adapter := &repairHistoryAdapter{fakeAdapter: fakeAdapter{snap: testSnapshot()}, unknown: "changed_source"}
	provider := &fakeProvider{candidates: []answer.Candidate{candidate("C")}}
	r, err := New(store, provider, Config{}).Run(context.Background(), adapter, recoveryRequest())
	hash, hashErr := problem.HashFiles(candidate("C").Files)
	if err == nil || hashErr != nil || r.Status != "uncertain" || r.SelfTestStatus != "unknown" || len(r.SelfTests) != 1 || len(store.validations) != 1 {
		t.Fatalf("source mismatch no longer stops: result=%+v err=%v hasherr=%v", r, err, hashErr)
	}
	st := r.SelfTests[0]
	if st.CandidateID != r.CandidateID || st.FilesHash != hash || st.SavedFilesHash != "different" || store.validations[0].Result != "unknown" || store.validations[0].SavedFilesHash != "different" {
		t.Fatalf("candidate provenance overwrote readback evidence: observation=%+v validation=%+v", st, store.validations[0])
	}
}

func TestLegacySelfTestDoesNotInferProvenanceFromCurrentResult(t *testing.T) {
	legacy := []byte(`{"result":{"candidate_id":42,"code_hash":"latest-source","self_tests":[{"Completed":true,"Output":"old observation","SavedFilesHash":"old-readback"}]}}`)
	store := &fakeStore{runs: map[string]questionbank.RunRecord{"legacy": {ID: "legacy", Status: "generation_stalled", Data: legacy}}}
	r, err := New(store, nil, Config{}).Resume(context.Background(), "legacy")
	if err != nil || len(r.SelfTests) != 1 || r.CandidateID != 42 || r.SelfTests[0].CandidateID != 0 || r.SelfTests[0].FilesHash != "" || r.SelfTests[0].SavedFilesHash != "old-readback" {
		t.Fatalf("legacy observation inferred unsupported identity: result=%+v err=%v", r, err)
	}
	encoded, err := json.Marshal(r.SelfTests[0])
	if err != nil || strings.Contains(string(encoded), "candidate_id") || strings.Contains(string(encoded), "files_hash") {
		t.Fatalf("missing legacy provenance was serialized as asserted evidence: %s err=%v", encoded, err)
	}
}

func TestRepeatedSourceDiagnosticsDoNotClaimEnhancedModel(t *testing.T) {
	store := &fakeStore{}
	adapter := &fakeAdapter{snap: testSnapshot()}
	provider := &scriptedProvider{estimateCost: 10, generate: func(context.Context, answer.Request, int) (answer.Result, error) { return solved("bad", 1), nil }}
	r, err := New(store, provider, Config{}).Run(context.Background(), adapter, recoveryRequest())
	if err != nil || r.Status != "generation_stalled" || r.ModelCalls != 2 || adapter.selfTests != 1 || adapter.submits != 0 {
		t.Fatalf("repeat did not stop: result=%+v err=%v", r, err)
	}
	diagnostics := strings.Join(r.Diagnostics, "\n")
	if strings.Contains(diagnostics, "增强") || !strings.Contains(diagnostics, "立即停止重复付费") {
		t.Fatalf("diagnostic misreported model: %s", diagnostics)
	}
}
