package taskservice

import (
	"context"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/solver"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func batchChoiceJSON(game, title string, challenge, myshixun, pos int64) string {
	return fmt.Sprintf(`{"st":1,"homework_common_id":44,"game":{"identifier":%q,"id":%d,"myshixun_id":%d},"myshixun":{"id":%d,"identifier":"RUNTIME"},"shixun":{"identifier":"SHIXUN","challenges_count":2},"challenge":{"id":%d,"st":1,"position":%d,"subject":%q,"task_pass":"Choose"},"chooses":[{"challenge_choose_id":10,"challenge_id":%d,"position":1,"category":1,"subject":"First?","challenge_question":[{"option_name":"First","position":0},{"option_name":"Second","position":1}]}]}`, game, gameID(pos), myshixun, myshixun, challenge, pos, title, challenge)
}

func gameID(pos int64) int64 { return 100 + pos }

func TestRunHomeworkSkipsVerifiedAndProcessesRemaining(t *testing.T) {
	scoreCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/courses/C/homework_commons.json":
			fmt.Fprint(w, `{"homeworks":[{"homework_id":44,"name":"hw","is_shixun":true,"shixun_identifier":"SHIXUN","challenge_count":2}]}`)
		case "/api/shixun_homeworks/44/header_info.json":
			fmt.Fprint(w, `{"data":{"homework_id":44,"work_id":5,"myshixun_identifier":"RUNTIME","shixun_identifier":"SHIXUN","challenges":[{"id":10,"name":"first"},{"id":11,"name":"second"}]}}`)
		case "/api/homework_commons/44/shixun_challenge_data.json":
			scoreCalls++
			if scoreCalls <= 4 {
				fmt.Fprint(w, `{"data":{"challenge_settings":[{"challenge_id":10,"challenge_name":"first","challenge_score":"10","game_score":"10"},{"challenge_id":11,"challenge_name":"second","challenge_score":"10","game_score":"0"}]}}`)
			} else {
				fmt.Fprint(w, `{"data":{"challenge_settings":[{"challenge_id":10,"challenge_name":"first","challenge_score":"10","game_score":"10"},{"challenge_id":11,"challenge_name":"second","challenge_score":"10","game_score":"10"}]}}`)
			}
		case "/api/myshixuns/RUNTIME/challenges.json":
			fmt.Fprint(w, `[{"identifier":"G1","position":1,"name":"first","status":2},{"identifier":"G2","position":2,"name":"second","status":2}]`)
		case "/api/tasks/G1.json":
			fmt.Fprint(w, batchChoiceJSON("G1", "first", 10, 8, 1))
		case "/api/tasks/G2.json":
			fmt.Fprint(w, batchChoiceJSON("G2", "second", 11, 8, 2))
		case "/api/tasks/G2/choose_build.json":
			fmt.Fprint(w, `{"status":0,"choose_correct_num":1,"challenge_chooses_count":1}`)
		default:
			t.Errorf("unexpected batch route %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	p := &fakeProvider{}
	s := New(bank(t), p, Config{BudgetMicroUSD: 1000})
	r, err := s.RunHomework(context.Background(), educoder.New(server.URL), Request{Owner: "o", Course: "C", HomeworkID: 44, Mode: solver.ModeSubmit}, nil)
	if err != nil || r.Status != "passed" || r.TotalLevels != 2 || r.CompletedLevels != 2 || r.SkippedLevels != 1 || r.FailedLevels != 0 || p.objectiveCalls.Load() < 1 {
		t.Fatalf("batch=%+v err=%v calls=%d", r, err, p.objectiveCalls.Load())
	}
	if r.Levels[0].Status != "skipped" || r.Levels[1].Status != "passed" {
		t.Fatalf("levels=%+v", r.Levels)
	}
}

func TestRunHomeworkRejectsIncompleteRosterBeforeModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/courses/C/homework_commons.json":
			fmt.Fprint(w, `{"homeworks":[{"homework_id":44,"name":"hw","is_shixun":true,"shixun_identifier":"SHIXUN","challenge_count":2}]}`)
		case "/api/shixun_homeworks/44/header_info.json":
			fmt.Fprint(w, `{"data":{"homework_id":44,"work_id":5,"myshixun_identifier":"RUNTIME","shixun_identifier":"SHIXUN","challenges":[{"id":10,"name":"first"},{"id":11,"name":"second"}]}}`)
		case "/api/homework_commons/44/shixun_challenge_data.json":
			fmt.Fprint(w, `{"data":{"challenge_settings":[{"challenge_id":10,"challenge_name":"first","challenge_score":"10","game_score":"0"},{"challenge_id":11,"challenge_name":"second","challenge_score":"10","game_score":"0"}]}}`)
		case "/api/myshixuns/RUNTIME/challenges.json":
			fmt.Fprint(w, `[{"identifier":"G1","position":1,"name":"first","status":2}]`)
		default:
			t.Errorf("unexpected preflight route %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	p := &fakeProvider{}
	r, err := New(bank(t), p, Config{}).RunHomework(context.Background(), educoder.New(server.URL), Request{Owner: "o", Course: "C", HomeworkID: 44, Mode: solver.ModeSubmit}, nil)
	if err == nil || r.Status != "unsupported_roster" || p.objectiveCalls.Load() != 0 {
		t.Fatalf("batch=%+v err=%v calls=%d", r, err, p.objectiveCalls.Load())
	}
}

func TestRunHomeworkFinalVerificationRejectsLostFullScore(t *testing.T) {
	progressCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/courses/C/homework_commons.json":
			fmt.Fprint(w, `{"homeworks":[{"homework_id":44,"name":"hw","is_shixun":true,"shixun_identifier":"SHIXUN","challenge_count":1}]}`)
		case "/api/shixun_homeworks/44/header_info.json":
			fmt.Fprint(w, `{"data":{"homework_id":44,"work_id":5,"myshixun_identifier":"RUNTIME","shixun_identifier":"SHIXUN","challenges":[{"id":10,"name":"first"}]}}`)
		case "/api/homework_commons/44/shixun_challenge_data.json":
			progressCalls++
			if progressCalls < 3 {
				fmt.Fprint(w, `{"data":{"challenge_settings":[{"challenge_id":10,"challenge_name":"first","challenge_score":"10","game_score":"10"}]}}`)
			} else {
				fmt.Fprint(w, `{"data":{"challenge_settings":[{"challenge_id":10,"challenge_name":"first","challenge_score":"10","game_score":"9"}]}}`)
			}
		case "/api/myshixuns/RUNTIME/challenges.json":
			fmt.Fprint(w, `[{"identifier":"G1","position":1,"name":"first","status":2}]`)
		case "/api/tasks/G1.json":
			fmt.Fprint(w, batchChoiceJSON("G1", "first", 10, 8, 1))
		default:
			t.Errorf("unexpected final verification route %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	r, err := New(bank(t), nil, Config{}).RunHomework(context.Background(), educoder.New(server.URL), Request{Owner: "o", Course: "C", HomeworkID: 44, Mode: solver.ModeSubmit}, nil)
	if err == nil || r.Status != "verification_unconfirmed" || r.CompletedLevels != 1 || r.SkippedLevels != 1 || r.FailedLevels != 1 || r.Levels[0].Status != "verification_unconfirmed" {
		t.Fatalf("batch=%+v err=%v progress_calls=%d", r, err, progressCalls)
	}
}
