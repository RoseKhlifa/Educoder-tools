package taskservice

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/questionbank"
)

func TestCollectionDiscoversPriorCompletedTaskWithEmptyLocalBank(t *testing.T) {
	for _, badRuntime := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh discovery and dedup", true: "cross runtime rejected"}[badRuntime], func(t *testing.T) {
			h, f, owner := collectionTestSetup(t)
			// Throw away even the fixture's saved target. The regression requires
			// discovering somebody who never opened an answer job on this service.
			bank, e := questionbank.Open(":memory:")
			if e != nil {
				t.Fatal(e)
			}
			defer bank.Close()
			h.Bank = bank
			f.task.Challenge.Position = 1
			f.task.MyShixun.Identifier = "RUNTIME"
			f.progress.Challenges[0].Name = f.task.Challenge.Subject
			if badRuntime {
				f.task.MyShixun.Identifier = "OTHER"
			}
			reads := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Fatal("upstream write")
				}
				switch r.URL.Path {
				case "/api/users/demo/courses.json":
					if r.URL.Query().Get("status") == "processing" && r.URL.Query().Get("page") == "1" {
						w.Write([]byte(`{"courses":[{"id":1,"identifier":"C"}]}`))
					} else {
						w.Write([]byte(`{"courses":[]}`))
					}
				case "/api/courses/C/homework_commons.json":
					if r.URL.Query().Get("page") == "1" {
						w.Write([]byte(`{"homeworks":[{"homework_id":1,"is_shixun":true,"shixun_identifier":"S","myshixun_identifier":"RUNTIME","student_work_id":7,"challenge_count":2}]}`))
					} else {
						w.Write([]byte(`{"homeworks":[]}`))
					}
				case "/api/homework_commons/1/shixun_challenge_data.json":
					// Partially completed homework: never read UNFINISHED's detail.
					json.NewEncoder(w).Encode(map[string]any{"data": educoder.HomeworkProgress{Challenges: []educoder.ChallengeProgress{f.progress.Challenges[0], {ChallengeID: 88, Name: "unfinished", ChallengeScore: "2", GameScore: "0"}}}})
				case "/api/shixun_homeworks/1/header_info.json":
					w.Write([]byte(`{"data":{"homework_id":1,"work_id":7,"myshixun_identifier":"RUNTIME","shixun_identifier":"S"}}`))
				case "/api/myshixuns/RUNTIME/challenges.json":
					if r.URL.Query().Get("homework_common_id") != "1" {
						t.Error("scope lost")
					}
					w.Write([]byte(`[{"identifier":"G","position":1,"name":"Q","status":2},{"identifier":"UNFINISHED","position":2,"name":"unfinished","status":0}]`))
				case "/api/tasks/G.json":
					reads++
					json.NewEncoder(w).Encode(f.task)
				default:
					t.Error("unreviewed or unfinished endpoint", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer upstream.Close()
			client := educoder.New(upstream.URL)
			client.LoginName = "demo"
			newReader := func() *educoder.Client {
				r, e := client.CollectionReader(educoder.CollectionBudget{Requests: 50, ResponseBytes: 2 << 20, TotalBytes: 4 << 20})
				if e != nil {
					t.Fatal(e)
				}
				return r
			}
			result, e := h.Scan(context.Background(), newReader(), owner, nil)
			if e != nil {
				t.Fatal(e)
			}
			if badRuntime {
				if result.Collected != 0 || result.LocatedLevels != 0 || result.Reasons["runtime_game_challenge_identity_mismatch"] != 1 {
					t.Fatalf("%+v", result)
				}
				return
			}
			if result.Collected != 1 || result.LocatedLevels != 1 || result.FullScoreLevels != 1 || result.ObjectiveAnswersRead != 1 || reads != 2 {
				t.Fatalf("%+v reads=%d", result, reads)
			}
			result, e = h.Scan(context.Background(), newReader(), owner, nil)
			if e != nil || result.Collected != 0 || result.Duplicates != 1 {
				t.Fatalf("duplicate %+v %v", result, e)
			}
		})
	}
}
