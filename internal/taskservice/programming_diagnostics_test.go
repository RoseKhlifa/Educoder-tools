package taskservice

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/solver"
)

func TestProgrammingFailureReasonDistinguishesSelfTestFromFormalEvaluation(t *testing.T) {
	for _, tc := range []struct {
		name        string
		programming *solver.Result
		err         error
		diagnostics []string
		want        string
	}{
		{
			name:        "base64 self-test frame failure",
			programming: &solver.Result{SelfTestStatus: "unknown"},
			err:         errors.New("自测帧不是有效的 Base64 数据"),
			want:        "selftest_stream_unconfirmed",
		},
		{
			name:        "unrecognized self-test error wording",
			programming: &solver.Result{SelfTestStatus: "unknown"},
			err:         errors.New("invalid runtime frame envelope"),
			want:        "selftest_stream_unconfirmed",
		},
		{
			name:        "self-test unknown without error text",
			programming: &solver.Result{SelfTestStatus: "unknown"},
			want:        "selftest_stream_unconfirmed",
		},
		{
			name:        "connection reason remains specific",
			programming: &solver.Result{SelfTestStatus: "unknown"},
			err:         errors.New("自测 WebSocket 连接失败"),
			want:        "selftest_connection_failed",
		},
		{
			name:        "editor conflict remains specific",
			programming: &solver.Result{SelfTestStatus: "unknown"},
			err:         errors.New("selftest editor content does not match candidate"),
			want:        "editor_changed",
		},
		{
			name:        "formal evaluation polling failure",
			programming: &solver.Result{SelfTestStatus: "passed", Evaluation: &solver.Evaluation{Status: "error"}},
			err:         errors.New("GET game_status.json: API status 0"),
			want:        "submission_uncertain",
		},
		{
			name:        "formal evaluation remains authoritative with stale self-test unknown",
			programming: &solver.Result{SelfTestStatus: "unknown", Evaluation: &solver.Evaluation{Status: "error"}},
			err:         errors.New("evaluation polling interrupted"),
			diagnostics: []string{"自测流在 ProcessFinished 前关闭或读取失败", "自测 WebSocket 连接失败"},
			want:        "submission_uncertain",
		},
		{
			name: "objective or legacy uncertainty remains submission unknown",
			err:  errors.New("submission response unavailable"),
			want: "submission_uncertain",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := &Result{Status: "uncertain", Programming: tc.programming, Diagnostics: tc.diagnostics}
			if got := batchFailureReason(result, tc.err); got != tc.want {
				t.Fatalf("reason=%q, want %q", got, tc.want)
			}
			if result.Status != "uncertain" {
				t.Fatalf("diagnostic classification changed execution state: %+v", result)
			}
		})
	}
}

func TestRunHomeworkFailureDiagnosticsOnlyContainObservedMessages(t *testing.T) {
	for _, tc := range []struct {
		name           string
		modelErr       error
		wantStatus     string
		wantDiagnostic string
	}{
		{name: "confirmed failed attempts retain child diagnostics without empty error", wantStatus: "attempts_exhausted", wantDiagnostic: "目标关卡尚未获得满分"},
		{name: "model error is retained", modelErr: &answer.Error{Kind: "truncated_output"}, wantStatus: "model_error", wantDiagnostic: (&answer.Error{Kind: "truncated_output"}).Error()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/courses/C/homework_commons.json":
					fmt.Fprint(w, `{"homeworks":[{"homework_id":44,"is_shixun":true,"shixun_identifier":"SHIXUN","challenge_count":1}]}`)
				case "/api/shixun_homeworks/44/header_info.json":
					fmt.Fprint(w, `{"data":{"homework_id":44,"work_id":5,"myshixun_identifier":"RUNTIME","shixun_identifier":"SHIXUN","challenges":[{"id":10,"name":"first"}]}}`)
				case "/api/homework_commons/44/shixun_challenge_data.json":
					fmt.Fprint(w, `{"data":{"challenge_settings":[{"challenge_id":10,"challenge_name":"first","challenge_score":"10","game_score":"0"}]}}`)
				case "/api/myshixuns/RUNTIME/challenges.json":
					fmt.Fprint(w, `[{"identifier":"G1","position":1,"name":"first"}]`)
				case "/api/tasks/G1.json":
					fmt.Fprint(w, strings.Replace(batchChoiceJSON("G1", "first", 10, 8, 1), `"challenges_count":2`, `"challenges_count":1`, 1))
				case "/api/tasks/G1/choose_build.json":
					fmt.Fprint(w, `{"status":0,"choose_correct_num":0,"challenge_chooses_count":1}`)
				default:
					t.Errorf("unexpected route %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			provider := &fakeProvider{generate: func(context.Context) error { return tc.modelErr }}
			result, _ := New(bank(t), provider, Config{MaxModelCalls: 1, MaxEvaluations: 1}).RunHomework(context.Background(), educoder.New(server.URL), Request{Owner: "o", Course: "C", HomeworkID: 44, Mode: solver.ModeSubmit}, nil)
			if result == nil || result.Status != tc.wantStatus || len(result.Diagnostics) != 1 || result.Diagnostics[0] != tc.wantDiagnostic {
				t.Fatalf("result=%+v, want status=%s diagnostic=%q", result, tc.wantStatus, tc.wantDiagnostic)
			}
			for _, diagnostic := range result.Diagnostics {
				if strings.TrimSpace(diagnostic) == "" {
					t.Fatalf("empty diagnostic persisted: %+v", result.Diagnostics)
				}
			}
		})
	}
}
