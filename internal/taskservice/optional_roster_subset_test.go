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

func TestCourseAssignedHeaderSubsetExcludesUnassignedAndOptionalLevels(t *testing.T) {
	for _, allOptional := range []bool{false, true} {
		t.Run(fmt.Sprintf("all_assigned_optional=%v", allOptional), func(t *testing.T) {
			var taskReads, writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method != http.MethodGet {
					writes.Add(1)
				}
				switch r.URL.Path {
				case "/api/users/student/courses.json":
					fmt.Fprint(w, `{"count":1,"courses":[{"id":1,"name":"Assigned subset","first_category_url":"/classrooms/C/shixun_homework"}]}`)
				case "/api/courses/C/homework_commons.json":
					fmt.Fprint(w, `{"query_total_count":1,"homeworks":[{"homework_id":44,"name":"Selected levels","is_shixun":true,"shixun_identifier":"SHIXUN","challenge_count":4}]}`)
				case "/api/shixun_homeworks/44/header_info.json":
					fmt.Fprint(w, `{"data":{"homework_id":44,"work_id":5,"myshixun_identifier":"RUNTIME","shixun_identifier":"SHIXUN","challenges":[{"id":10,"name":"first"},{"id":11,"name":"optional"}]}}`)
				case "/api/homework_commons/44/shixun_challenge_data.json":
					firstStatus, firstScore := "required", "10"
					if allOptional {
						firstStatus, firstScore = "optional", "0"
					}
					fmt.Fprintf(w, `{"data":{"challenge_settings":[{"challenge_id":10,"challenge_name":"first","status":%q,"challenge_score":%q,"game_score":%q},{"challenge_id":11,"challenge_name":"optional","status":"optional","challenge_score":"0","game_score":"0"},{"challenge_id":12,"challenge_name":"unassigned third","status":"required","challenge_score":"10","game_score":"0"},{"challenge_id":13,"challenge_name":"unassigned fourth","status":"required","challenge_score":"10","game_score":"0"}]}}`, firstStatus, firstScore, firstScore)
				case "/api/myshixuns/RUNTIME/challenges.json":
					fmt.Fprint(w, `[{"identifier":"G1","position":1,"name":"first"},{"identifier":"G3","position":3,"name":"unassigned third"},{"identifier":"G4","position":4,"name":"unassigned fourth"}]`)
				case "/api/tasks/G1.json":
					taskReads.Add(1)
					fmt.Fprint(w, batchChoiceJSON("G1", "first", 10, 8, 1))
				default:
					t.Errorf("unassigned or optional task was read: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client := educoder.New(server.URL)
			client.LoginName = "student"
			provider := &fakeProvider{}
			result, err := New(bank(t), provider, Config{}).RunCourses(context.Background(), client, Request{Owner: "o", Course: "C", Mode: solver.ModeSubmit}, nil)
			wantHomeworks, wantReads := 1, int32(1)
			if allOptional {
				wantHomeworks, wantReads = 0, 0
			}
			if err != nil || result.Status != "passed" || result.TotalHomeworks != wantHomeworks || result.CompletedHomeworks != wantHomeworks || result.NeedsServiceHomeworks != 0 || len(result.Homeworks) != wantHomeworks {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if !allOptional {
				batch := result.Homeworks[0].Result
				if batch == nil || batch.TotalLevels != 1 || batch.CompletedLevels != 1 || batch.SkippedLevels != 1 || len(batch.Levels) != 1 || batch.Levels[0].ChallengeID != 10 {
					t.Fatalf("assigned-only batch=%+v", batch)
				}
			}
			if taskReads.Load() != wantReads || writes.Load() != 0 || provider.objectiveCalls.Load() != 0 || provider.programmingCalls.Load() != 0 {
				t.Fatalf("task_reads=%d writes=%d model_calls=%d/%d", taskReads.Load(), writes.Load(), provider.objectiveCalls.Load(), provider.programmingCalls.Load())
			}
		})
	}
}
