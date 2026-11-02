package taskservice

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/solver"
)

func TestRunHomeworkCreatesPendingGameOnlyAfterPreviousLevelPasses(t *testing.T) {
	passed := map[string]bool{}
	initializations := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/courses/C/homework_commons.json":
			fmt.Fprint(w, `{"homeworks":[{"homework_id":44,"is_shixun":true,"shixun_identifier":"SHIXUN","challenge_count":2}]}`)
		case "/api/shixun_homeworks/44/header_info.json":
			fmt.Fprint(w, `{"data":{"homework_id":44,"work_id":5,"myshixun_identifier":"RUNTIME","shixun_identifier":"SHIXUN","challenges":[{"id":10,"name":"first"},{"id":11,"name":"second"}]}}`)
		case "/api/homework_commons/44/shixun_challenge_data.json":
			score1, score2 := "0", "0"
			if passed["G1"] {
				score1 = "10"
			}
			if passed["G2"] {
				score2 = "10"
			}
			fmt.Fprintf(w, `{"data":{"challenge_settings":[{"challenge_id":10,"challenge_name":"first","challenge_score":"10","game_score":%q},{"challenge_id":11,"challenge_name":"second","challenge_score":"10","game_score":%q}]}}`, score1, score2)
		case "/api/myshixuns/RUNTIME/challenges.json":
			fmt.Fprint(w, `[{"identifier":"G1","position":1,"name":"first","status":0},{"identifier":null,"position":2,"name":"second","status":3}]`)
		case "/api/shixuns/SHIXUN/shixun_exec.json":
			initializations++
			if !passed["G1"] || r.URL.Query().Get("challenge_id") != "11" || r.URL.Query().Get("homework_common_id") != "44" {
				t.Error("created the wrong level or created it before previous full-score verification")
			}
			fmt.Fprint(w, `{"game_identifier":"G2","access":true}`)
		case "/api/tasks/G1.json":
			fmt.Fprint(w, batchChoiceJSON("G1", "first", 10, 8, 1))
		case "/api/tasks/G2.json":
			if initializations == 0 {
				t.Error("uninitialized game was read")
			}
			fmt.Fprint(w, batchChoiceJSON("G2", "second", 11, 8, 2))
		case "/api/tasks/G1/choose_build.json":
			passed["G1"] = true
			fmt.Fprint(w, `{"status":0,"choose_correct_num":1,"challenge_chooses_count":1}`)
		case "/api/tasks/G2/choose_build.json":
			passed["G2"] = true
			fmt.Fprint(w, `{"status":0,"choose_correct_num":1,"challenge_chooses_count":1}`)
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	r, err := New(bank(t), &fakeProvider{}, Config{}).RunHomework(context.Background(), educoder.New(server.URL), Request{Owner: "o", Course: "C", HomeworkID: 44, Mode: solver.ModeSubmit}, nil)
	if err != nil || r.Status != "passed" || r.CompletedLevels != 2 || initializations != 1 {
		t.Fatalf("result=%+v err=%v initialized=%d", r, err, initializations)
	}
}
