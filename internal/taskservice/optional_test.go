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

// The upstream progress contains the complete assignment. Elective levels may
// have zero points and no runnable game; they are not part of service work.
func TestHomeworkOptionalLevelsNeverLoadOrCount(t *testing.T) {
	for _, allOptional := range []bool{false, true} {
		t.Run(fmt.Sprintf("all_optional=%v", allOptional), func(t *testing.T) {
			var writes, optionalReads atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method != http.MethodGet {
					writes.Add(1)
				}
				switch r.URL.Path {
				case "/api/courses/C/homework_commons.json":
					fmt.Fprint(w, `{"homeworks":[{"homework_id":44,"is_shixun":true,"shixun_identifier":"SHIXUN","challenge_count":2}]}`)
				case "/api/shixun_homeworks/44/header_info.json":
					fmt.Fprint(w, `{"data":{"homework_id":44,"work_id":5,"myshixun_identifier":"RUNTIME","shixun_identifier":"SHIXUN","challenges":[{"id":10,"name":"required"},{"id":11,"name":"optional"}]}}`)
				case "/api/homework_commons/44/shixun_challenge_data.json":
					status := "required"
					if allOptional {
						status = "optional"
					}
					fmt.Fprintf(w, `{"data":{"challenge_settings":[{"challenge_id":10,"challenge_name":"required","status":%q,"challenge_score":"10","game_score":"10"},{"challenge_id":11,"challenge_name":"optional","status":"optional","challenge_st":999,"challenge_score":"0","game_score":"0"}]}}`, status)
				case "/api/myshixuns/RUNTIME/challenges.json":
					// No mapping for the optional level at all.
					fmt.Fprint(w, `[{"identifier":"G1","position":1,"name":"required"}]`)
				case "/api/tasks/G1.json":
					if allOptional {
						optionalReads.Add(1)
					}
					fmt.Fprint(w, batchChoiceJSON("G1", "required", 10, 8, 1))
				default:
					optionalReads.Add(1)
					t.Errorf("optional or unexpected route %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			provider := &fakeProvider{}
			result, err := New(bank(t), provider, Config{}).RunHomework(context.Background(), educoder.New(server.URL), Request{Owner: "o", Course: "C", HomeworkID: 44, Mode: solver.ModeSubmit}, nil)
			want := 1
			if allOptional {
				want = 0
			}
			if err != nil || result.Status != "passed" || result.TotalLevels != want || result.CompletedLevels != want || len(result.Levels) != want || result.FailedLevels != 0 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if allOptional && result.ReasonCode != "no_required_levels" {
				t.Fatalf("all optional needs explicit no-work reason: %+v", result)
			}
			if optionalReads.Load() != 0 || writes.Load() != 0 || provider.objectiveCalls.Load() != 0 || provider.programmingCalls.Load() != 0 {
				t.Fatalf("optional_reads=%d writes=%d model_calls=%d/%d", optionalReads.Load(), writes.Load(), provider.objectiveCalls.Load(), provider.programmingCalls.Load())
			}
		})
	}
}

func TestAllOptionalHomeworkWithoutRuntimeNeverEnters(t *testing.T) {
	var enters atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/courses/C/homework_commons.json":
			fmt.Fprint(w, `{"homeworks":[{"homework_id":44,"is_shixun":true,"shixun_identifier":"SHIXUN","challenge_count":1}]}`)
		case "/api/shixun_homeworks/44/header_info.json":
			fmt.Fprint(w, `{"data":{"homework_id":44,"work_id":5,"shixun_identifier":"SHIXUN"}}`)
		case "/api/homework_commons/44/shixun_challenge_data.json":
			fmt.Fprint(w, `{"data":{"challenge_settings":[{"challenge_id":11,"challenge_name":"elective","status":"optional","challenge_score":"0","game_score":"0"}]}}`)
		default:
			enters.Add(1)
			t.Errorf("all-optional assignment entered or loaded %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	result, err := New(bank(t), &fakeProvider{}, Config{}).RunHomework(context.Background(), educoder.New(server.URL), Request{Owner: "o", Course: "C", HomeworkID: 44, Mode: solver.ModeSubmit}, nil)
	if err != nil || result.Status != "passed" || result.ReasonCode != "no_required_levels" || result.TotalLevels != 0 || enters.Load() != 0 {
		t.Fatalf("result=%+v err=%v runtime_enters=%d", result, err, enters.Load())
	}
}

func TestCourseCompletionIgnoresOptionalScoresAndAllOptionalAssignments(t *testing.T) {
	var taskReads, writes atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			writes.Add(1)
		}
		switch r.URL.Path {
		case "/api/users/student/courses.json":
			fmt.Fprint(w, `{"count":1,"courses":[{"id":1,"name":"Data structures","first_category_url":"/classrooms/C/shixun_homework"}]}`)
		case "/api/courses/C/homework_commons.json":
			fmt.Fprint(w, `{"query_total_count":2,"homeworks":[{"homework_id":44,"name":"Introduction","is_shixun":true,"shixun_identifier":"S44","challenge_count":10},{"homework_id":45,"name":"Electives","is_shixun":true,"shixun_identifier":"S45","challenge_count":1}]}`)
		case "/api/homework_commons/44/shixun_challenge_data.json":
			fmt.Fprint(w, `{"data":{"challenge_settings":[`)
			for i := 1; i <= 10; i++ {
				if i > 1 {
					fmt.Fprint(w, ",")
				}
				status, score := "required", "16"
				if i > 5 {
					status, score = "optional", "0"
				}
				fmt.Fprintf(w, `{"challenge_id":%d,"challenge_name":"level %d","status":%q,"is_choose_todo":false,"challenge_score":%q,"game_score":%q}`, i, i, status, score, score)
			}
			fmt.Fprint(w, `]}}`)
		case "/api/homework_commons/45/shixun_challenge_data.json":
			fmt.Fprint(w, `{"data":{"challenge_settings":[{"challenge_id":100,"challenge_name":"elective","status":"optional","challenge_score":"0","game_score":"0"}]}}`)
		default:
			taskReads.Add(1)
			t.Errorf("already complete required work should not load %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := educoder.New(server.URL)
	client.LoginName = "student"
	provider := &fakeProvider{}
	result, err := New(bank(t), provider, Config{}).RunCourses(context.Background(), client, Request{Owner: "o", Course: "C", Mode: solver.ModeSubmit}, nil)
	if err != nil || result.Status != "passed" || result.TotalHomeworks != 1 || result.CompletedHomeworks != 1 || result.SkippedHomeworks != 1 || result.NeedsServiceHomeworks != 0 || len(result.Homeworks) != 1 || result.Homeworks[0].Title != "Introduction" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if taskReads.Load() != 0 || writes.Load() != 0 || provider.objectiveCalls.Load() != 0 {
		t.Fatalf("task_reads=%d writes=%d model_calls=%d", taskReads.Load(), writes.Load(), provider.objectiveCalls.Load())
	}
}

func TestZeroPointsAndUnknownRequirementRemainRequired(t *testing.T) {
	for _, status := range []string{"", "required", "unknown", "0"} {
		progress := &educoder.HomeworkProgress{Challenges: []educoder.ChallengeProgress{{ChallengeID: 1, Name: "required unless explicit", RequiredStatus: status, ChallengeScore: "0", GameScore: "0"}}}
		if homeworkExactlyComplete(educoder.Homework{ChallengeCount: 1}, progress) {
			t.Fatalf("zero score status %q was treated as optional", status)
		}
		if count, valid := requiredHomeworkCount(educoder.Homework{ChallengeCount: 1}, progress); !valid || count != 1 {
			t.Fatalf("zero score status %q count=%d valid=%v", status, count, valid)
		}
	}
}

func TestDirectRunSkipsOptionalBeforeKindModelOrWrite(t *testing.T) {
	for _, kind := range []string{objectiveJSON(), programmingJSON(), `{"st":999,"game":{"identifier":"OPTIONAL"},"challenge":{"id":11}}`} {
		var reads, writes atomic.Int64
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.Method != http.MethodGet {
				writes.Add(1)
			}
			reads.Add(1)
			if r.URL.Path != "/api/tasks/OPTIONAL.json" {
				t.Errorf("optional task made extra request %s", r.URL.Path)
			}
			kind = strings.ReplaceAll(strings.ReplaceAll(kind, `"OBJGAME"`, `"OPTIONAL"`), `"PROGRAM"`, `"OPTIONAL"`)
			fmt.Fprint(w, strings.Replace(kind, "{", `{"status":"optional",`, 1))
		}))
		provider := &fakeProvider{}
		result, err := New(bank(t), provider, Config{}).Run(context.Background(), educoder.New(server.URL), Request{Owner: "o", Course: "C", HomeworkID: 44, Game: "OPTIONAL", Mode: solver.ModeSubmit}, nil)
		server.Close()
		if err != nil || result.Status != "skipped_optional" || reads.Load() != 1 || writes.Load() != 0 || provider.objectiveCalls.Load() != 0 || provider.programmingCalls.Load() != 0 {
			t.Fatalf("result=%+v err=%v reads=%d writes=%d calls=%d/%d", result, err, reads.Load(), writes.Load(), provider.objectiveCalls.Load(), provider.programmingCalls.Load())
		}
	}
}

func TestDirectRunStopsWhenTaskBecomesOptionalOnReload(t *testing.T) {
	var reads, writes atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			writes.Add(1)
		}
		if r.URL.Path != "/api/tasks/OBJGAME.json" {
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		status := "required"
		if reads.Add(1) > 1 {
			status = "optional"
		}
		fmt.Fprint(w, strings.Replace(objectiveJSON(), "{", fmt.Sprintf(`{"status":%q,`, status), 1))
	}))
	defer server.Close()
	provider := &fakeProvider{}
	result, err := New(bank(t), provider, Config{}).Run(context.Background(), educoder.New(server.URL), Request{Owner: "o", Course: "C", HomeworkID: 44, Game: "OBJGAME", Mode: solver.ModeSubmit}, nil)
	if err != nil || result.Status != "skipped_optional" || reads.Load() != 2 || writes.Load() != 0 || provider.objectiveCalls.Load() != 0 {
		t.Fatalf("result=%+v err=%v reads=%d writes=%d calls=%d", result, err, reads.Load(), writes.Load(), provider.objectiveCalls.Load())
	}
}

func TestObjectiveBecomesOptionalDuringGenerationDoesNotSubmit(t *testing.T) {
	var optional atomic.Bool
	var writes, reads atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			writes.Add(1)
		}
		if r.URL.Path != "/api/tasks/OBJGAME.json" {
			t.Errorf("optional target made unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		reads.Add(1)
		status := "required"
		if optional.Load() {
			status = "optional"
		}
		fmt.Fprint(w, strings.Replace(objectiveJSON(), "{", fmt.Sprintf(`{"status":%q,`, status), 1))
	}))
	defer server.Close()
	provider := &fakeProvider{generate: func(context.Context) error {
		optional.Store(true)
		return nil
	}}
	store := bank(t)
	service := New(store, provider, Config{})
	request := Request{Owner: "o", Course: "C", HomeworkID: 44, Game: "OBJGAME", Mode: solver.ModeSubmit, IdempotencyKey: "became-optional"}
	result, err := service.Run(context.Background(), educoder.New(server.URL), request, nil)
	if err != nil || result.Status != "skipped_optional" || reads.Load() != 3 || writes.Load() != 0 || result.ModelCalls != 1 || result.CostMicroUSD != 2 || provider.objectiveCalls.Load() != 1 {
		t.Fatalf("result=%+v err=%v reads=%d writes=%d calls=%d", result, err, reads.Load(), writes.Load(), provider.objectiveCalls.Load())
	}
	// The durable idempotency result must be a deliberate skip, never an
	// uncertain submission or a pending task after no write was attempted.
	record, err := store.FindRunByIdempotency(context.Background(), ownerHash(request.Owner), request.IdempotencyKey)
	if err != nil || record.Status != "skipped_optional" {
		t.Fatalf("record=%+v err=%v", record, err)
	}
}

func TestHomeworkDropsLevelThatBecomesOptionalBeforeRun(t *testing.T) {
	var taskReads, writes atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			writes.Add(1)
		}
		switch r.URL.Path {
		case "/api/courses/C/homework_commons.json":
			fmt.Fprint(w, `{"homeworks":[{"homework_id":44,"is_shixun":true,"shixun_identifier":"SHIXUN","challenge_count":1}]}`)
		case "/api/shixun_homeworks/44/header_info.json":
			fmt.Fprint(w, `{"data":{"homework_id":44,"work_id":5,"myshixun_identifier":"RUNTIME","shixun_identifier":"SHIXUN","challenges":[{"id":10,"name":"first"}]}}`)
		case "/api/homework_commons/44/shixun_challenge_data.json":
			fmt.Fprint(w, `{"data":{"challenge_settings":[{"challenge_id":10,"challenge_name":"first","status":"required","challenge_score":"10","game_score":"0"}]}}`)
		case "/api/myshixuns/RUNTIME/challenges.json":
			fmt.Fprint(w, `[{"identifier":"G1","position":1,"name":"first"}]`)
		case "/api/tasks/G1.json":
			status := "required"
			if taskReads.Add(1) > 1 {
				status = "optional"
			}
			fmt.Fprint(w, strings.Replace(batchChoiceJSON("G1", "first", 10, 8, 1), "{", fmt.Sprintf(`{"status":%q,`, status), 1))
		default:
			t.Errorf("optional target made unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	provider := &fakeProvider{}
	result, err := New(bank(t), provider, Config{}).RunHomework(context.Background(), educoder.New(server.URL), Request{Owner: "o", Course: "C", HomeworkID: 44, Mode: solver.ModeSubmit}, nil)
	if err != nil || result.Status != "passed" || result.ReasonCode != "no_required_levels" || result.TotalLevels != 0 || result.CompletedLevels != 0 || result.FailedLevels != 0 || len(result.Levels) != 0 || taskReads.Load() != 2 || writes.Load() != 0 || provider.objectiveCalls.Load() != 0 {
		t.Fatalf("result=%+v err=%v task_reads=%d writes=%d calls=%d", result, err, taskReads.Load(), writes.Load(), provider.objectiveCalls.Load())
	}
}
