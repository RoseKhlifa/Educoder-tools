package taskservice

import (
	"context"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/solver"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func courseProgressReadFixture(t *testing.T, failCount, failStatus int) (*educoder.Client, *atomic.Int64, *atomic.Int64) {
	t.Helper()
	reads, mutations := &atomic.Int64{}, &atomic.Int64{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			mutations.Add(1)
			http.Error(w, "must not write", 500)
			return
		}
		switch r.URL.Path {
		case "/api/users/student/courses.json":
			fmt.Fprint(w, `{"count":1,"courses":[{"id":1,"name":"Course C","first_category_url":"/classrooms/C/shixun_homework"}]}`)
		case "/api/courses/C/homework_commons.json":
			fmt.Fprint(w, `{"query_total_count":2,"homeworks":[{"homework_id":44,"name":"first","is_shixun":true,"shixun_identifier":"S44","challenge_count":1},{"homework_id":45,"name":"second","is_shixun":true,"shixun_identifier":"S45","challenge_count":1}]}`)
		case "/api/homework_commons/44/shixun_challenge_data.json":
			if int(reads.Add(1)) <= failCount {
				w.WriteHeader(failStatus)
				fmt.Fprint(w, `{"message":"secret upstream response ?TOKEN=private"}`)
				return
			}
			fmt.Fprint(w, `{"data":{"challenge_settings":[{"challenge_id":44,"challenge_name":"one","challenge_score":"10","game_score":"10.00"}]}}`)
		case "/api/homework_commons/45/shixun_challenge_data.json":
			fmt.Fprint(w, `{"data":{"challenge_settings":[{"challenge_id":45,"challenge_name":"two","challenge_score":"10","game_score":"10.00"}]}}`)
		default:
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client := educoder.New(server.URL)
	client.LoginName = "student"
	return client, reads, mutations
}

func TestCourseProgressTransientReadRecoversFreshFullScoreWithoutExecution(t *testing.T) {
	client, reads, mutations := courseProgressReadFixture(t, 1, 503)
	provider := &fakeProvider{}
	result, err := New(bank(t), provider, Config{}).RunCourses(context.Background(), client, Request{Owner: "owner", Course: "C", Mode: solver.ModeSubmit}, nil)
	if err != nil || result.Status != "passed" || result.CompletedHomeworks != 2 || result.SkippedHomeworks != 2 || result.NeedsServiceHomeworks != 0 || reads.Load() != 2 || mutations.Load() != 0 || provider.objectiveCalls.Load() != 0 || result.ModelCalls != 0 {
		t.Fatalf("result=%+v err=%v reads=%d mutations=%d models=%d", result, err, reads.Load(), mutations.Load(), provider.objectiveCalls.Load())
	}
}

func TestCourseProgressExhaustionRetainsFixedDiagnosticsAndContinues(t *testing.T) {
	for _, tc := range []struct {
		status, wantReads int
	}{
		{503, 3},
		{401, 1},
	} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			client, reads, mutations := courseProgressReadFixture(t, 20, tc.status)
			provider := &fakeProvider{}
			result, err := New(bank(t), provider, Config{}).RunCourses(context.Background(), client, Request{Owner: "owner", Course: "C", Mode: solver.ModeSubmit}, nil)
			if err != nil || result.Status != "needs_service" || result.CompletedHomeworks != 1 || result.NeedsServiceHomeworks != 1 || reads.Load() != int64(tc.wantReads) || mutations.Load() != 0 || provider.objectiveCalls.Load() != 0 {
				t.Fatalf("result=%+v err=%v reads=%d mutations=%d", result, err, reads.Load(), mutations.Load())
			}
			first := result.Homeworks[0]
			if first.ReasonCode != "progress_unavailable" || first.Result == nil || len(first.Result.Diagnostics) != 1 || result.Homeworks[1].Status != "skipped" {
				t.Fatalf("results=%+v", result.Homeworks)
			}
			want := fmt.Sprintf("homework_progress stage=http category=http_status status=%d attempts=%d", tc.status, tc.wantReads)
			if first.Result.Diagnostics[0] != want || strings.Contains(first.Result.Diagnostics[0], "private") {
				t.Fatalf("diagnostics=%v want=%s", first.Result.Diagnostics, want)
			}
		})
	}
}
