package taskservice

import (
	"context"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/solver"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestCourseHeaderSubsetNeverExcludesRequiredProgressLevels(t *testing.T) {
	for _, headerOptional := range []bool{false, true} {
		t.Run(fmt.Sprintf("header_rows_optional=%v", headerOptional), func(t *testing.T) {
			var taskReads, writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method != http.MethodGet {
					writes.Add(1)
				}
				switch r.URL.Path {
				case "/api/users/student/courses.json":
					fmt.Fprint(w, `{"count":1,"courses":[{"id":1,"name":"Complete assignment","first_category_url":"/classrooms/C/shixun_homework"}]}`)
				case "/api/courses/C/homework_commons.json":
					fmt.Fprint(w, `{"query_total_count":1,"homeworks":[{"homework_id":44,"name":"Four catalog levels","is_shixun":true,"shixun_identifier":"SHIXUN","challenge_count":4}]}`)
				case "/api/shixun_homeworks/44/header_info.json":
					fmt.Fprint(w, `{"data":{"homework_id":44,"work_id":5,"myshixun_identifier":"RUNTIME","shixun_identifier":"SHIXUN","challenges":[{"id":10,"name":"first"},{"id":11,"name":"optional"}]}}`)
				case "/api/homework_commons/44/shixun_challenge_data.json":
					firstStatus, firstScore := "required", "10"
					if headerOptional {
						firstStatus, firstScore = "optional", "0"
					}
					fmt.Fprintf(w, `{"data":{"challenge_settings":[{"challenge_id":10,"challenge_name":"first","status":%q,"challenge_score":%q,"game_score":%q},{"challenge_id":11,"challenge_name":"optional","status":"optional","challenge_score":"0","game_score":"0"},{"challenge_id":12,"challenge_name":"required third","status":"required","challenge_score":"10","game_score":"0"},{"challenge_id":13,"challenge_name":"required fourth","status":"required","challenge_score":"10","game_score":"0"}]}}`, firstStatus, firstScore, firstScore)
				case "/api/myshixuns/RUNTIME/challenges.json":
					fmt.Fprint(w, `[{"identifier":"G1","position":1,"name":"first"},{"identifier":"G3","position":3,"name":"required third"},{"identifier":"G4","position":4,"name":"required fourth"}]`)
				case "/api/tasks/G1.json":
					taskReads.Add(1)
					fmt.Fprint(w, batchChoiceJSON("G1", "first", 10, 8, 1))
				case "/api/tasks/G3.json", "/api/tasks/G4.json":
					// These are required rows omitted by the partial header, not
					// unassigned work. Unavailable runtime details must remain failed.
					taskReads.Add(1)
					http.Error(w, "fixture runtime unavailable", http.StatusServiceUnavailable)
				default:
					t.Errorf("unexpected task was read: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client := educoder.New(server.URL)
			client.LoginName = "student"
			provider := &fakeProvider{}
			result, err := New(bank(t), provider, Config{}).RunCourses(context.Background(), client, Request{Owner: "o", Course: "C", Mode: solver.ModeSubmit}, nil)
			wantLevels, wantReads := 3, int32(3)
			if headerOptional {
				wantLevels, wantReads = 2, 2
			}
			if err != nil || result.Status != "needs_service" || result.TotalHomeworks != 1 || result.CompletedHomeworks != 0 || result.NeedsServiceHomeworks != 1 || len(result.Homeworks) != 1 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			batch := result.Homeworks[0].Result
			if batch == nil || batch.Status == "passed" || batch.TotalLevels != wantLevels || batch.FailedLevels != 2 || len(batch.Levels) != wantLevels {
				t.Fatalf("header omitted required rows from batch=%+v", batch)
			}
			for _, id := range []int64{12, 13} {
				index := batchLevelIndex(batch, id)
				if index < 0 || batch.Levels[index].Status != "precheck_failed" {
					t.Fatalf("required challenge %d disappeared: %+v", id, batch)
				}
			}
			if taskReads.Load() != wantReads || writes.Load() != 0 || provider.objectiveCalls.Load() != 0 || provider.programmingCalls.Load() != 0 {
				t.Fatalf("task_reads=%d writes=%d model_calls=%d/%d", taskReads.Load(), writes.Load(), provider.objectiveCalls.Load(), provider.programmingCalls.Load())
			}
		})
	}
}
