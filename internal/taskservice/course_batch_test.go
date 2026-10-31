package taskservice

import (
	"context"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/solver"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func courseFixture(t *testing.T, uncertainSubmit ...bool) (*educoder.Client, *atomic.Int64, *atomic.Int64) {
	t.Helper()
	submits, otherCourseReads := &atomic.Int64{}, &atomic.Int64{}
	passed46 := &atomic.Bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/users/student/courses.json":
			fmt.Fprint(w, `{"count":2,"courses":[{"id":1,"name":"Course C","first_category_url":"/classrooms/C/shixun_homework"},{"id":2,"name":"Course D","first_category_url":"/classrooms/D/shixun_homework"}]}`)
		case "/api/courses/C/homework_commons.json":
			// These summaries intentionally disagree with exact scores: completed
			// 44 says unfinished and failed 45 says finished. Neither is trusted.
			fmt.Fprint(w, `{"query_total_count":3,"homeworks":[{"homework_id":44,"name":"done","is_shixun":true,"shixun_identifier":"S44","challenge_count":1,"finished_challenge_count":0},{"homework_id":45,"name":"staff needed","is_shixun":true,"shixun_identifier":"S45","challenge_count":1,"finished_challenge_count":1},{"homework_id":46,"name":"next assignment","is_shixun":true,"shixun_identifier":"S46","challenge_count":1}]}`)
		case "/api/courses/D/homework_commons.json":
			otherCourseReads.Add(1)
			fmt.Fprint(w, `{"query_total_count":1,"homeworks":[{"homework_id":47,"name":"done too","is_shixun":true,"shixun_identifier":"S47","challenge_count":1}]}`)
		default:
			var id int
			if strings.Contains(r.URL.Path, "/shixun_challenge_data.json") {
				fmt.Sscanf(r.URL.Path, "/api/homework_commons/%d/shixun_challenge_data.json", &id)
				score := "0"
				if id == 44 || id == 47 || (id == 46 && passed46.Load()) {
					score = "10.0"
				}
				fmt.Fprintf(w, `{"data":{"challenge_settings":[{"challenge_id":%d,"challenge_name":"level %d","challenge_score":"10","game_score":%q}]}}`, id, id, score)
				return
			}
			if strings.HasSuffix(r.URL.Path, "/header_info.json") {
				fmt.Sscanf(r.URL.Path, "/api/shixun_homeworks/%d/header_info.json", &id)
				fmt.Fprintf(w, `{"data":{"homework_id":%d,"work_id":%d,"myshixun_identifier":"R%d","shixun_identifier":"S%d","challenges":[{"id":%d,"name":"level %d"}]}}`, id, id, id, id, id, id)
				return
			}
			if strings.HasSuffix(r.URL.Path, "/challenges.json") {
				fmt.Sscanf(r.URL.Path, "/api/myshixuns/R%d/challenges.json", &id)
				fmt.Fprintf(w, `[{"identifier":"G%d","position":1,"name":"level %d"}]`, id, id)
				return
			}
			if strings.HasSuffix(r.URL.Path, "/choose_build.json") {
				fmt.Sscanf(r.URL.Path, "/api/tasks/G%d/choose_build.json", &id)
				submits.Add(1)
				if id == 45 && len(uncertainSubmit) > 0 && uncertainSubmit[0] {
					http.Error(w, "submission response interrupted", http.StatusBadGateway)
					return
				}
				correct := 0
				if id == 46 {
					passed46.Store(true)
					correct = 1
				}
				fmt.Fprintf(w, `{"status":0,"choose_correct_num":%d,"challenge_chooses_count":1}`, correct)
				return
			}
			if strings.HasPrefix(r.URL.Path, "/api/tasks/G") {
				fmt.Sscanf(r.URL.Path, "/api/tasks/G%d.json", &id)
				fmt.Fprintf(w, `{"st":1,"homework_common_id":%d,"game":{"identifier":"G%d","id":%d,"myshixun_id":%d},"myshixun":{"id":%d,"identifier":"R%d"},"shixun":{"identifier":"S%d","challenges_count":1},"challenge":{"id":%d,"st":1,"position":1,"subject":"level %d","task_pass":"Choose the first"},"chooses":[{"challenge_choose_id":%d,"challenge_id":%d,"position":1,"category":1,"subject":"Question %d?","challenge_question":[{"option_name":"First","position":0},{"option_name":"Second","position":1}]}]}`, id, id, id+100, id+1000, id+1000, id, id, id, id, id+10000, id, id)
				return
			}
			t.Errorf("unexpected course route %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client := educoder.New(server.URL)
	client.LoginName = "student"
	return client, submits, otherCourseReads
}

func TestRunCoursesDoesNotReplayUncertainWriteAndContinuesOtherHomework(t *testing.T) {
	client, submits, _ := courseFixture(t, true)
	result, err := New(bank(t), &fakeProvider{}, Config{MaxEvaluations: 3}).RunCourses(context.Background(), client, Request{Owner: "o", Course: "C", Mode: solver.ModeSubmit}, nil)
	if err != nil || result.Status != "needs_service" || result.NeedsServiceHomeworks != 1 || result.CompletedHomeworks != 2 || submits.Load() != 2 {
		t.Fatalf("uncertain result=%+v err=%v submits=%d", result, err, submits.Load())
	}
	if result.Homeworks[1].Result == nil || result.Homeworks[1].Result.Status != "uncertain" || result.Homeworks[2].Status != "passed" {
		t.Fatalf("uncertain child replayed or unrelated work blocked: %+v", result.Homeworks)
	}
}

func TestRunCoursesCompletesIndependentAssignmentsAfterKnownFailure(t *testing.T) {
	client, submits, other := courseFixture(t)
	provider := &fakeProvider{}
	service := New(bank(t), provider, Config{MaxModelCalls: 1, MaxEvaluations: 1})
	var snapshots []ServiceProgress
	result, err := service.RunCourses(context.Background(), client, Request{Owner: "student-owner", Mode: solver.ModeSubmit}, func(p Progress) {
		if p.Service != nil {
			snapshots = append(snapshots, *p.Service)
		}
	})
	if err != nil || result.Status != "needs_service" || result.Scope != "all" || result.TotalCourses != 2 || result.TotalHomeworks != 4 || result.CompletedHomeworks != 3 || result.SkippedHomeworks != 2 || result.NeedsServiceHomeworks != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if submits.Load() != 2 || other.Load() != 1 || provider.objectiveCalls.Load() != 2 {
		t.Fatalf("submits=%d other=%d calls=%d", submits.Load(), other.Load(), provider.objectiveCalls.Load())
	}
	if result.Homeworks[1].Status != "needs_service" || result.Homeworks[1].Result.Status != "attempts_exhausted" || result.Homeworks[2].Status != "passed" {
		t.Fatalf("child results=%+v", result.Homeworks)
	}
	last := snapshots[len(snapshots)-1]
	if last.CompletedHomeworks != 3 || last.NeedsServiceHomeworks != 1 || last.CurrentHomeworkID != 0 {
		t.Fatalf("last progress=%+v", last)
	}
}

func TestRunCoursesSelectedCourseAndMembership(t *testing.T) {
	client, submits, other := courseFixture(t)
	service := New(bank(t), &fakeProvider{}, Config{MaxModelCalls: 1, MaxEvaluations: 1})
	result, err := service.RunCourses(context.Background(), client, Request{Owner: "o", Course: "C", Mode: solver.ModeSubmit}, nil)
	if err != nil || result.Scope != "course" || result.TotalHomeworks != 3 || result.TotalCourses != 1 || other.Load() != 0 || submits.Load() != 2 {
		t.Fatalf("result=%+v err=%v submits=%d other=%d", result, err, submits.Load(), other.Load())
	}
	before := submits.Load()
	result, err = service.RunCourses(context.Background(), client, Request{Owner: "o", Course: "NOT_MINE", Mode: solver.ModeSubmit}, nil)
	if err == nil || result.ReasonCode != "course_not_found" || submits.Load() != before {
		t.Fatalf("membership result=%+v err=%v", result, err)
	}
}

func TestRunCoursesCancellationAndAuthenticationNeverSubmit(t *testing.T) {
	client, submits, _ := courseFixture(t)
	service := New(bank(t), &fakeProvider{}, Config{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result, err := service.RunCourses(ctx, client, Request{Owner: "o", Course: "C", Mode: solver.ModeSubmit}, func(p Progress) {
		if p.Service != nil && p.Service.CurrentHomeworkID > 0 {
			cancel()
		}
	})
	if err == nil || result.Status != "canceled" || submits.Load() != 0 {
		t.Fatalf("cancellation=%+v err=%v", result, err)
	}
	client.LoginName = ""
	result, err = service.RunCourses(context.Background(), client, Request{Owner: "o", Mode: solver.ModeSubmit}, nil)
	if err == nil || result.ReasonCode != "course_sync_failed" || submits.Load() != 0 {
		t.Fatalf("authentication=%+v err=%v", result, err)
	}
}

func TestRunCoursesBudgetFlowsIntoChildHomework(t *testing.T) {
	client, submits, _ := courseFixture(t)
	provider := &fakeProvider{estimated: 10}
	result, err := New(bank(t), provider, Config{}).RunCourses(context.Background(), client, Request{Owner: "o", Course: "C", Mode: solver.ModeSubmit, BudgetMicroUSD: 1}, nil)
	if err != nil || result.Status != "needs_service" || result.CompletedHomeworks != 1 || result.NeedsServiceHomeworks != 2 || submits.Load() != 0 || provider.objectiveCalls.Load() != 0 || result.CostMicroUSD+result.ReservedMicroUSD > 1 {
		t.Fatalf("budget=%+v err=%v submits=%d", result, err, submits.Load())
	}
}

func TestCourseServicePreflightBoundsBeforeExecution(t *testing.T) {
	for _, count := range []int{serviceMaxCourses + 1, 1} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			progressReads := &atomic.Int64{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.HasPrefix(r.URL.Path, "/api/users/") {
					courses := []educoder.Course{}
					for i := 0; i < count; i++ {
						courses = append(courses, educoder.Course{ID: int64(i + 1), FirstCategoryURL: fmt.Sprintf("/classrooms/C%d/shixun_homework", i)})
					}
					json.NewEncoder(w).Encode(map[string]any{"courses": courses, "count": count})
					return
				}
				if strings.HasPrefix(r.URL.Path, "/api/courses/") {
					hws := []educoder.Homework{}
					for i := 0; i <= serviceMaxHomeworks; i++ {
						hws = append(hws, educoder.Homework{ID: int64(i + 1)})
					}
					json.NewEncoder(w).Encode(map[string]any{"homeworks": hws, "query_total_count": len(hws)})
					return
				}
				progressReads.Add(1)
				http.Error(w, "must not execute", 500)
			}))
			defer server.Close()
			client := educoder.New(server.URL)
			client.LoginName = "student"
			result, err := New(bank(t), nil, Config{}).RunCourses(context.Background(), client, Request{Owner: "o", Mode: solver.ModeSubmit}, nil)
			if err == nil || progressReads.Load() != 0 || (result.ReasonCode != "course_limit" && result.ReasonCode != "homework_limit") {
				t.Fatalf("limits=%+v err=%v reads=%d", result, err, progressReads.Load())
			}
		})
	}
}

func TestHomeworkCompleteRequiresExactEntireUnambiguousScores(t *testing.T) {
	full := educoder.ChallengeProgress{ChallengeID: 7, Name: "one", GameScore: "13.30", ChallengeScore: "13.3"}
	for _, tc := range []struct {
		name     string
		count    int
		progress *educoder.HomeworkProgress
		want     bool
	}{
		{"exact full", 1, &educoder.HomeworkProgress{Challenges: []educoder.ChallengeProgress{full}}, true},
		{"no exact data", 1, &educoder.HomeworkProgress{PassedCount: 1}, false},
		{"partial assignment", 2, &educoder.HomeworkProgress{Challenges: []educoder.ChallengeProgress{full}}, false},
		{"duplicate id", 2, &educoder.HomeworkProgress{Challenges: []educoder.ChallengeProgress{full, full}}, false},
		{"unknown count", 0, &educoder.HomeworkProgress{Challenges: []educoder.ChallengeProgress{full}}, false},
		{"not full", 1, &educoder.HomeworkProgress{Challenges: []educoder.ChallengeProgress{{ChallengeID: 7, Name: "one", GameScore: "13.29999", ChallengeScore: "13.3", PassedStatus: 2}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := homeworkExactlyComplete(educoder.Homework{ChallengeCount: tc.count}, tc.progress); got != tc.want {
				t.Fatalf("complete=%v want=%v", got, tc.want)
			}
		})
	}
}
