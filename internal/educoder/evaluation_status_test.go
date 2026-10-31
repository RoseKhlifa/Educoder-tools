package educoder

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A game evaluation's zero status means it did not pass when accompanied by
// actual evaluation evidence. It is not a generic API failure, nor proof that
// an otherwise empty response completed an evaluation.
func TestEvaluationZeroStatus(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response string
		wantErr  string
	}{
		{"failed-test", `{"status":0,"test_sets_count":1,"sets_error_count":1,"test_sets":[{"result":false,"compile_success":1}]}`, ""},
		{"hidden-test", `{"status":0,"test_sets_count":2,"sets_error_count":1,"test_sets":[]}`, ""},
		{"compile-error", `{"status":0,"last_compile_output":"fixture compile error","test_sets":[{"compile_success":0}]}`, ""},
		{"failed-result-without-counts", `{"status":0,"test_sets":[{"is_public":false,"input":"","output":"","result":false,"compile_success":1}]}`, ""},
		{"counts-alone", `{"status":0,"test_sets_count":2,"sets_error_count":0,"test_sets":[]}`, "未观察到的状态组合"},
		{"test-metadata-alone", `{"status":0,"test_sets":[{"is_public":false,"compile_success":1}]}`, "未观察到的状态组合"},
		{"compile-output-alone", `{"status":0,"last_compile_output":"compile successfully"}`, "未观察到的状态组合"},
		{"contradictory-pass-data", `{"status":0,"test_sets_count":1,"sets_error_count":0,"test_sets":[{"result":true,"compile_success":1}]}`, "未观察到的状态组合"},
		{"no-completion-evidence", `{"status":0}`, "未观察到的状态组合"},
		{"application-error", `{"status":-1,"message":"fixture secret"}`, "API status -1"},
		{"unknown-status", `{"status":3,"test_sets_count":1,"sets_error_count":0}`, "API status 3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != http.MethodGet || r.URL.Path != "/api/tasks/GAME1/game_status.json" {
					t.Errorf("unexpected evaluation request %s %s", r.Method, r.URL.Path)
				}
				fmt.Fprint(w, tc.response)
			}))
			defer server.Close()
			client := New(server.URL)
			task := decodeTaskFixture(t, programmingTaskFixture("GAME1", 1))
			saved := evaluationStatusSavedFixture()
			result, err := client.WaitEvaluation(context.Background(), task, saved, PollOptions{Timeout: time.Second})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) || strings.Contains(err.Error(), "fixture secret") {
					t.Fatalf("result=%+v err=%v", result, err)
				}
			} else if err != nil || result == nil || !result.Completed || result.Passed || result.Status == nil || *result.Status != 0 {
				t.Fatalf("expected a completed unsuccessful evaluation: result=%+v err=%v", result, err)
			}
			if requests != 1 {
				t.Fatalf("evaluation was unexpectedly replayed: requests=%d", requests)
			}
		})
	}
}

func TestEvaluationZeroStatusIntermediateContinuesPolling(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch requests {
		case 1:
			fmt.Fprint(w, `{"status":0,"running_code_status":1}`)
		case 2:
			fmt.Fprint(w, `{"status":0,"running_code_status":2}`)
		default:
			fmt.Fprint(w, `{"status":2,"test_sets_count":1,"sets_error_count":0,"test_sets":[{"result":true,"compile_success":1}]}`)
		}
	}))
	defer server.Close()
	client := New(server.URL)
	task := decodeTaskFixture(t, programmingTaskFixture("GAME1", 1))
	result, err := client.WaitEvaluation(context.Background(), task, evaluationStatusSavedFixture(), PollOptions{Timeout: time.Second, Interval: time.Millisecond})
	if err != nil || result == nil || !result.Completed || !result.Passed || requests != 3 {
		t.Fatalf("result=%+v err=%v requests=%d", result, err, requests)
	}
}

func TestEvaluationZeroStatusDoesNotSuppressHTTPFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"status":0,"test_sets_count":1,"sets_error_count":1,"message":"fixture secret"}`)
	}))
	defer server.Close()
	client := New(server.URL)
	task := decodeTaskFixture(t, programmingTaskFixture("GAME1", 1))
	result, err := client.WaitEvaluation(context.Background(), task, evaluationStatusSavedFixture(), PollOptions{Timeout: time.Second})
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") || strings.Contains(err.Error(), "fixture secret") || result != nil {
		t.Fatalf("HTTP failure was suppressed or leaked upstream message: result=%+v err=%v", result, err)
	}
}

func evaluationStatusSavedFixture() *SavedCode {
	return &SavedCode{CommitID: "fixture-commit", SecKey: "fixture-key", Evaluate: 1, gameIdentifier: "GAME1", myshixunID: 80, filePath: "src/第1关.cpp"}
}
