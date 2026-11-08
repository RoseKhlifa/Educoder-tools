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

func coverageProgress(count int) *educoder.HomeworkProgress {
	p := &educoder.HomeworkProgress{}
	for i := 1; i <= count; i++ {
		p.Challenges = append(p.Challenges, educoder.ChallengeProgress{ChallengeID: int64(i + 9), Name: fmt.Sprintf("level%d", i), RequiredStatus: "required", ChallengeScore: "10", GameScore: "10"})
	}
	return p
}

func TestHomeworkPlanCoverageRequiresAllOfficialRequiredIdentities(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*educoder.HomeworkProgress, *[]BatchLevel)
		valid  bool
	}{
		{"full", func(*educoder.HomeworkProgress, *[]BatchLevel) {}, true},
		{"header subset", func(_ *educoder.HomeworkProgress, plan *[]BatchLevel) { *plan = (*plan)[:1] }, false},
		{"partial progress", func(p *educoder.HomeworkProgress, _ *[]BatchLevel) { p.Challenges = p.Challenges[:1] }, false},
		{"duplicate progress", func(p *educoder.HomeworkProgress, _ *[]BatchLevel) { p.Challenges[1].ChallengeID = 10 }, false},
		{"replacement identity", func(p *educoder.HomeworkProgress, _ *[]BatchLevel) { p.Challenges[1].ChallengeID = 12 }, false},
		{"duplicate plan", func(_ *educoder.HomeworkProgress, plan *[]BatchLevel) { (*plan)[1].ChallengeID = 10 }, false},
		{"official optional omission", func(p *educoder.HomeworkProgress, plan *[]BatchLevel) {
			p.Challenges[1].RequiredStatus = "optional"
			*plan = (*plan)[:1]
		}, true},
		{"zero score is required", func(p *educoder.HomeworkProgress, plan *[]BatchLevel) {
			p.Challenges[1].GameScore = "0"
			*plan = (*plan)[:1]
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := coverageProgress(2)
			plan := []BatchLevel{{ChallengeID: 10}, {ChallengeID: 11}}
			tc.mutate(p, &plan)
			if err := homeworkPlanCoverage(educoder.Homework{ChallengeCount: 2}, p, plan); (err == nil) != tc.valid {
				t.Fatalf("valid=%v want=%v err=%v", err == nil, tc.valid, err)
			}
		})
	}
}

// No model or mutation is needed to reproduce the production 3/4 and 1/4
// shapes: unobservable required runtimes remain in the complete failed plan.
func TestHomeworkPartialHeaderRetainsAllFourRequiredLevels(t *testing.T) {
	for _, completed := range []int{3, 1} {
		t.Run(fmt.Sprintf("%d_of_4", completed), func(t *testing.T) {
			var writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method != http.MethodGet {
					writes.Add(1)
				}
				switch r.URL.Path {
				case "/api/courses/C/homework_commons.json":
					fmt.Fprint(w, `{"homeworks":[{"homework_id":44,"is_shixun":true,"shixun_identifier":"SHIXUN","challenge_count":4}]}`)
				case "/api/shixun_homeworks/44/header_info.json":
					ch := []map[string]any{}
					for i := 1; i <= completed; i++ {
						ch = append(ch, map[string]any{"id": i + 9, "name": fmt.Sprintf("level%d", i)})
					}
					json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"homework_id": 44, "work_id": 5, "myshixun_identifier": "RUNTIME", "shixun_identifier": "SHIXUN", "challenges": ch}})
				case "/api/homework_commons/44/shixun_challenge_data.json":
					p := coverageProgress(4)
					for i := completed; i < 4; i++ {
						p.Challenges[i].GameScore = "0"
					}
					json.NewEncoder(w).Encode(map[string]any{"data": p})
				case "/api/myshixuns/RUNTIME/challenges.json":
					games := []map[string]any{}
					for i := 1; i <= 4; i++ {
						games = append(games, map[string]any{"identifier": fmt.Sprintf("G%d", i), "position": i, "name": fmt.Sprintf("level%d", i)})
					}
					json.NewEncoder(w).Encode(games)
				default:
					var i int
					if _, err := fmt.Sscanf(r.URL.Path, "/api/tasks/G%d.json", &i); err == nil && i >= 1 && i <= 4 {
						if i <= completed {
							fmt.Fprint(w, batchChoiceJSON(fmt.Sprintf("G%d", i), fmt.Sprintf("level%d", i), int64(i+9), 8, int64(i)))
						} else {
							http.Error(w, "fixture runtime unavailable", http.StatusServiceUnavailable)
						}
					} else {
						t.Errorf("unexpected request %s", r.URL.Path)
						http.NotFound(w, r)
					}
				}
			}))
			defer server.Close()
			provider := &fakeProvider{}
			got, err := New(bank(t), provider, Config{}).RunHomework(context.Background(), educoder.New(server.URL), Request{Owner: "o", Course: "C", HomeworkID: 44, Mode: solver.ModeSubmit}, nil)
			if err != nil || got.Status == "passed" || got.TotalLevels != 4 || got.CompletedLevels != completed || got.FailedLevels != 4-completed {
				t.Fatalf("required work disappeared: result=%+v err=%v", got, err)
			}
			if writes.Load() != 0 || provider.objectiveCalls.Load() != 0 || provider.programmingCalls.Load() != 0 {
				t.Fatal("coverage regression invoked a mutation/model")
			}
		})
	}
}

func TestHomeworkCoverageRechecksPreflightAndFinalSnapshots(t *testing.T) {
	for _, phase := range []string{"preflight", "final"} {
		for _, change := range []string{"partial", "duplicate", "new_identity", "late_required", "official_optional"} {
			t.Run(phase+"/"+change, func(t *testing.T) {
				var reads, writes atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if r.Method != http.MethodGet {
						writes.Add(1)
					}
					switch r.URL.Path {
					case "/api/courses/C/homework_commons.json":
						fmt.Fprint(w, `{"homeworks":[{"homework_id":44,"is_shixun":true,"shixun_identifier":"SHIXUN","challenge_count":2}]}`)
					case "/api/shixun_homeworks/44/header_info.json":
						fmt.Fprint(w, `{"data":{"homework_id":44,"work_id":5,"myshixun_identifier":"RUNTIME","shixun_identifier":"SHIXUN","challenges":[{"id":10,"name":"level1"},{"id":11,"name":"level2"}]}}`)
					case "/api/myshixuns/RUNTIME/challenges.json":
						fmt.Fprint(w, `[{"identifier":"G1","position":1,"name":"level1"},{"identifier":"G2","position":2,"name":"level2"}]`)
					case "/api/homework_commons/44/shixun_challenge_data.json":
						p := coverageProgress(2)
						if change == "late_required" {
							p.Challenges[1].RequiredStatus = "optional"
						}
						trigger := int32(3)
						if phase == "preflight" {
							trigger = 2
						}
						if reads.Add(1) >= trigger {
							switch change {
							case "partial":
								p.Challenges = p.Challenges[:1]
							case "duplicate":
								p.Challenges[1].ChallengeID = 10
							case "new_identity":
								p.Challenges[1].ChallengeID = 12
							case "late_required":
								p.Challenges[1].RequiredStatus, p.Challenges[1].GameScore = "required", "0"
							case "official_optional":
								p.Challenges[1].RequiredStatus, p.Challenges[1].GameScore = "optional", "0"
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
				provider := &fakeProvider{}
				got, err := New(bank(t), provider, Config{}).RunHomework(context.Background(), educoder.New(server.URL), Request{Owner: "o", Course: "C", HomeworkID: 44, Mode: solver.ModeSubmit}, nil)
				if change == "official_optional" {
					if err != nil || got.Status != "passed" || got.TotalLevels != 1 || got.CompletedLevels != 1 {
						t.Fatalf("official optional change not respected: %+v %v", got, err)
					}
				} else if err == nil || got.Status == "passed" || got.ReasonCode != "assignment_roster_incomplete" {
					t.Fatalf("changed/incomplete assignment falsely passed: %+v %v", got, err)
				}
				if writes.Load() != 0 || provider.objectiveCalls.Load() != 0 {
					t.Fatal("coverage check invoked mutation/model")
				}
			})
		}
	}
}
