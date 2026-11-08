package taskservice

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/solver"
)

func TestCourseIndependentlyVerifiesChildAssignmentCompletion(t *testing.T) {
	for _, tc := range []struct {
		name          string
		childOptional bool
		parent        string
		passed        bool
	}{
		{"complete child and parent", false, "full", true},
		{"child passed parent pending", false, "pending", false},
		{"child passed parent partial", false, "partial", false},
		{"child passed parent duplicate", false, "duplicate", false},
		{"child passed parent unavailable", false, "unavailable", false},
		{"no required still officially optional", true, "optional", true},
		{"completed child becomes wholly optional", false, "optional", true},
		{"no required but parent sees required", true, "pending", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reads, writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method != http.MethodGet {
					writes.Add(1)
				}
				switch r.URL.Path {
				case "/api/users/student/courses.json":
					fmt.Fprint(w, `{"count":1,"courses":[{"id":1,"first_category_url":"/classrooms/C/shixun_homework"}]}`)
				case "/api/courses/C/homework_commons.json":
					fmt.Fprint(w, `{"homeworks":[{"homework_id":44,"is_shixun":true,"shixun_identifier":"SHIXUN","challenge_count":2}]}`)
				case "/api/shixun_homeworks/44/header_info.json":
					fmt.Fprint(w, `{"data":{"homework_id":44,"work_id":5,"myshixun_identifier":"RUNTIME","shixun_identifier":"SHIXUN","challenges":[{"id":10,"name":"level1"},{"id":11,"name":"level2"}]}}`)
				case "/api/myshixuns/RUNTIME/challenges.json":
					fmt.Fprint(w, `[{"identifier":"G1","position":1,"name":"level1"},{"identifier":"G2","position":2,"name":"level2"}]`)
				case "/api/homework_commons/44/shixun_challenge_data.json":
					call := reads.Add(1)
					p := coverageProgress(2)
					if call == 1 {
						p.Challenges[1].GameScore = "0"
					} else if tc.childOptional {
						for i := range p.Challenges {
							p.Challenges[i].RequiredStatus = "optional"
						}
					}
					parentRead := int32(5)
					if tc.childOptional {
						parentRead = 4
					}
					if call >= parentRead {
						switch tc.parent {
						case "optional":
							for i := range p.Challenges {
								p.Challenges[i].RequiredStatus, p.Challenges[i].GameScore = "optional", "0"
							}
						case "pending":
							p = coverageProgress(2)
							p.Challenges[1].GameScore = "0"
						case "partial":
							p.Challenges = p.Challenges[:1]
						case "duplicate":
							p.Challenges[1].ChallengeID = 10
						case "unavailable":
							http.Error(w, "fixture progress unavailable", http.StatusServiceUnavailable)
							return
						}
					}
					json.NewEncoder(w).Encode(map[string]any{"data": p})
				case "/api/tasks/G1.json":
					fmt.Fprint(w, batchChoiceJSON("G1", "level1", 10, 8, 1))
				case "/api/tasks/G2.json":
					fmt.Fprint(w, batchChoiceJSON("G2", "level2", 11, 8, 2))
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client := educoder.New(server.URL)
			client.LoginName = "student"
			provider := &fakeProvider{}
			got, err := New(bank(t), provider, Config{}).RunCourses(context.Background(), client, Request{Owner: "o", Course: "C", Mode: solver.ModeSubmit}, nil)
			if err != nil || (got.Status == "passed") != tc.passed {
				t.Fatalf("parent trusted stale child: %+v %v reads=%d", got, err, reads.Load())
			}
			if !tc.passed && (got.CompletedHomeworks != 0 || got.NeedsServiceHomeworks != 1 || len(got.Homeworks) != 1 || got.Homeworks[0].ReasonCode != "assignment_verification_unconfirmed") {
				t.Fatalf("wrong final accounting: %+v", got)
			}
			if tc.passed && tc.parent == "optional" && (got.TotalHomeworks != 0 || len(got.Homeworks) != 0) {
				t.Fatalf("official optional work remained: %+v", got)
			}
			if writes.Load() != 0 || provider.objectiveCalls.Load() != 0 || provider.programmingCalls.Load() != 0 {
				t.Fatal("parent verification invoked mutation/model")
			}
		})
	}
}

func TestTaskOptionalFlagCannotEraseOfficialRequiredLevel(t *testing.T) {
	var taskReads, writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			writes.Add(1)
		}
		switch r.URL.Path {
		case "/api/courses/C/homework_commons.json":
			fmt.Fprint(w, `{"homeworks":[{"homework_id":44,"is_shixun":true,"shixun_identifier":"SHIXUN","challenge_count":1}]}`)
		case "/api/shixun_homeworks/44/header_info.json":
			fmt.Fprint(w, `{"data":{"homework_id":44,"work_id":5,"myshixun_identifier":"RUNTIME","shixun_identifier":"SHIXUN","challenges":[{"id":10,"name":"level1"}]}}`)
		case "/api/myshixuns/RUNTIME/challenges.json":
			fmt.Fprint(w, `[{"identifier":"G1","position":1,"name":"level1"}]`)
		case "/api/homework_commons/44/shixun_challenge_data.json":
			p := coverageProgress(1)
			p.Challenges[0].GameScore = "0"
			json.NewEncoder(w).Encode(map[string]any{"data": p})
		case "/api/tasks/G1.json":
			status := "required"
			if taskReads.Add(1) > 1 {
				status = "optional"
			}
			var task map[string]any
			json.Unmarshal([]byte(batchChoiceJSON("G1", "level1", 10, 8, 1)), &task)
			task["status"] = status
			json.NewEncoder(w).Encode(task)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	provider := &fakeProvider{}
	got, err := New(bank(t), provider, Config{}).RunHomework(context.Background(), educoder.New(server.URL), Request{Owner: "o", Course: "C", HomeworkID: 44, Mode: solver.ModeSubmit}, nil)
	if err == nil || got.Status == "passed" || got.ReasonCode != "assignment_roster_incomplete" {
		t.Fatalf("task-only optional flag erased official requirement: %+v %v", got, err)
	}
	if writes.Load() != 0 || provider.objectiveCalls.Load() != 0 {
		t.Fatal("conflicting optional metadata caused mutation/model")
	}
}
