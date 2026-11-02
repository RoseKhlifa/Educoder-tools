package educoder

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolveHomeworkLevelLazilyAndValidateExactTarget(t *testing.T) {
	for _, tc := range []struct {
		name, entry, taskChanges, wantErr string
		wantTask                          bool
	}{
		{"created challenge", `{"game_identifier":"G2","access":true}`, "", "", true},
		{"legacy access omitted", `{"game_identifier":"G2"}`, "", "", true},
		{"sequence restriction", `{"game_identifier":"G1","access":false}`, "", "upstream sequencing", false},
		{"missing game", `{"access":true}`, "", "no valid game", false},
		{"wrong challenge", `{"game_identifier":"G2","access":true}`, `"challenge":{"id":99,"subject":"second","position":2,"st":0,"with_code_file":true}`, "mismatches planned roster", true},
		{"wrong position", `{"game_identifier":"G2","access":true}`, `"challenge":{"id":11,"subject":"second","position":3,"st":0,"with_code_file":true}`, "mismatches planned roster", true},
		{"wrong runtime", `{"game_identifier":"G2","access":true}`, `"myshixun":{"id":9,"identifier":"OTHER"}`, "mismatches planned roster", true},
		{"wrong course", `{"game_identifier":"G2","access":true}`, `"course_identifier":"OTHER"`, "identity missing or mismatched", true},
		{"wrong homework", `{"game_identifier":"G2","access":true}`, `"homework_common_id":45`, "identity missing or mismatched", true},
		{"became optional", `{"game_identifier":"G2","access":true}`, `"status":"optional"`, "optional homework", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entryCalls, taskCalls := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method != http.MethodGet {
					t.Fatal("unexpected write")
				}
				switch req.URL.Path {
				case "/api/shixuns/SHIXUN/shixun_exec.json":
					entryCalls++
					q := req.URL.Query()
					if q.Get("challenge_id") != "11" || q.Get("homework_common_id") != "44" || q.Get("id") != "SHIXUN" {
						t.Error("entry did not bind canonical challenge and homework")
					}
					fmt.Fprint(w, tc.entry)
				case "/api/tasks/G2.json":
					taskCalls++
					if req.URL.Query().Get("homework_common_id") != "44" {
						t.Error("task lost homework scope")
					}
					task := `{"status":"required","homework_common_id":44,"course_identifier":"COURSE","st":0,"game":{"id":12,"identifier":"G2","myshixun_id":9},"challenge":{"id":11,"subject":"second","position":2,"st":0,"with_code_file":true},"myshixun":{"id":9,"identifier":"RUNTIME"},"shixun":{"identifier":"SHIXUN"}}`
					if tc.taskChanges != "" {
						for _, original := range []string{`"status":"required"`, `"homework_common_id":44`, `"course_identifier":"COURSE"`, `"challenge":{"id":11,"subject":"second","position":2,"st":0,"with_code_file":true}`, `"myshixun":{"id":9,"identifier":"RUNTIME"}`} {
							if strings.HasPrefix(tc.taskChanges, strings.SplitN(original, ":", 2)[0]+":") {
								task = strings.Replace(task, original, tc.taskChanges, 1)
							}
						}
					}
					fmt.Fprint(w, task)
				default:
					t.Errorf("unexpected resolver request %s", req.URL.Path)
				}
			}))
			defer server.Close()
			st := 0
			level := HomeworkLevel{ChallengeID: 11, Title: "second", Position: 2, ST: &st, RuntimePending: true}
			roster := &HomeworkRoster{Course: "COURSE", HomeworkID: 44, Shixun: "SHIXUN", MyShixun: "RUNTIME", MyShixunID: 9, Levels: []HomeworkLevel{level}}
			resolved, task, err := New(server.URL).ResolveHomeworkLevel(context.Background(), roster, level)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) || task != nil {
					t.Fatalf("wanted %s; task=%+v error=%v", tc.wantErr, task, err)
				}
			} else if err != nil || task == nil || resolved.Game != "G2" || resolved.RuntimePending {
				t.Fatalf("level=%+v task=%+v error=%v", resolved, task, err)
			}
			if entryCalls != 1 || (taskCalls == 1) != tc.wantTask {
				t.Fatalf("entry calls=%d task calls=%d", entryCalls, taskCalls)
			}
			if !roster.Levels[0].RuntimePending || roster.Levels[0].Game != "" {
				t.Fatal("resolver mutated shared plan")
			}
		})
	}
}

func TestResolveHomeworkLevelRefusesUnplannedOrOptionalEntry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		t.Errorf("unplanned or elective level reached %s", req.URL.Path)
	}))
	defer server.Close()
	level := HomeworkLevel{ChallengeID: 11, Title: "second", Position: 2, RuntimePending: true}
	for _, tc := range []struct {
		name    string
		planned []HomeworkLevel
		level   HomeworkLevel
	}{
		{"absent", nil, level},
		{"duplicate", []HomeworkLevel{level, level}, level},
		{"modified position", []HomeworkLevel{level}, HomeworkLevel{ChallengeID: 11, Title: "second", Position: 3, RuntimePending: true}},
		{"modified game", []HomeworkLevel{level}, HomeworkLevel{ChallengeID: 11, Title: "second", Position: 2, Game: "FORGED"}},
		{"optional", []HomeworkLevel{{ChallengeID: 11, Title: "second", Optional: true}}, HomeworkLevel{ChallengeID: 11, Title: "second", Optional: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &HomeworkRoster{Course: "COURSE", HomeworkID: 44, Shixun: "SHIXUN", MyShixun: "RUNTIME", Levels: tc.planned}
			_, task, err := New(server.URL).ResolveHomeworkLevel(context.Background(), r, tc.level)
			if err == nil || task != nil {
				t.Fatalf("task=%+v err=%v", task, err)
			}
		})
	}
}

func TestResolveHomeworkLevelReusesInitializedRuntime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/api/tasks/G1.json" {
			t.Errorf("existing game must not be initialized again: %s", req.URL.Path)
		}
		fmt.Fprint(w, `{"status":"required","homework_common_id":44,"st":0,"game":{"id":1,"identifier":"G1","myshixun_id":2},"challenge":{"id":10,"subject":"first","position":1,"st":0,"with_code_file":true},"myshixun":{"id":2,"identifier":"RUNTIME"},"shixun":{"identifier":"SHIXUN"}}`)
	}))
	defer server.Close()
	level := HomeworkLevel{ChallengeID: 10, Game: "G1", Title: "first", Position: 1}
	r := &HomeworkRoster{Course: "COURSE", HomeworkID: 44, Shixun: "SHIXUN", MyShixun: "RUNTIME", Levels: []HomeworkLevel{level}}
	_, task, err := New(server.URL).ResolveHomeworkLevel(context.Background(), r, level)
	if err != nil || task == nil {
		t.Fatalf("task=%+v err=%v", task, err)
	}
	level.Optional = true
	r.Levels[0] = level
	if _, _, err := New(server.URL).ResolveHomeworkLevel(context.Background(), r, level); !errors.Is(err, ErrOptionalTask) {
		t.Fatalf("optional runtime was not rejected: %v", err)
	}
}
