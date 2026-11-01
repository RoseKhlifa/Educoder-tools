package educoder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestHomeworkRosterAllowsHomeworkSubsetOfShixunHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/shixun_homeworks/44/header_info.json":
			fmt.Fprint(w, `{"data":{"homework_id":44,"work_id":5,"myshixun_identifier":"RUNTIME","shixun_identifier":"SHIXUN","challenges":[{"id":10,"name":"selected"},{"id":11,"name":"other"},{"id":12,"name":"third"}]}}`)
		case "/api/homework_commons/44/shixun_challenge_data.json":
			fmt.Fprint(w, `{"data":{"challenge_settings":[{"challenge_id":10,"challenge_name":"selected","challenge_score":"10","game_score":"0"}]}}`)
		case "/api/myshixuns/RUNTIME/challenges.json":
			fmt.Fprint(w, `[{"identifier":"G1","position":1,"name":"selected","status":2},{"identifier":"G2","position":2,"name":"other","status":2},{"identifier":"G3","position":3,"name":"third","status":2}]`)
		default:
			t.Errorf("unexpected roster route %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	roster, err := New(server.URL).HomeworkRoster(context.Background(), "COURSE", Homework{ID: 44, IsShixun: true, ShixunIdentifier: "SHIXUN", ChallengeCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(roster.Levels) != 1 || roster.Levels[0].ChallengeID != 10 || roster.Levels[0].Game != "G1" {
		t.Fatalf("roster=%+v", roster.Levels)
	}
}

func TestBoundHomeworkClientStopsWhenTaskBecomesOptional(t *testing.T) {
	var status atomic.Value
	status.Store("required")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tasks/G1.json" || r.URL.Query().Get("homework_common_id") != "44" {
			t.Errorf("unexpected task request: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":%q,"homework_common_id":44,"st":0,"game":{"id":1,"identifier":"G1","myshixun_id":2},"challenge":{"id":10,"subject":"required","position":1,"st":0,"with_code_file":true},"myshixun":{"id":2,"identifier":"RUNTIME"},"shixun":{"identifier":"SHIXUN"}}`, status.Load().(string))
	}))
	defer server.Close()
	r := &HomeworkRoster{Course: "COURSE", HomeworkID: 44, Shixun: "SHIXUN", MyShixun: "RUNTIME"}
	l := HomeworkLevel{ChallengeID: 10, Game: "G1", Title: "required", Position: 1}
	client, err := New(server.URL).BindHomeworkLevel(r, l)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.LoadTask(context.Background(), "COURSE", 44, "G1"); err != nil {
		t.Fatal(err)
	}
	status.Store("optional")
	if _, err := client.LoadTask(context.Background(), "COURSE", 44, "G1"); !errors.Is(err, ErrOptionalTask) {
		t.Fatalf("changed task was not excluded: %v", err)
	}
	var changed Task
	if err := json.Unmarshal([]byte(`{"status":"optional","homework_common_id":44,"game":{"id":1,"identifier":"G1","myshixun_id":2},"challenge":{"id":10,"subject":"required","position":1},"myshixun":{"id":2,"identifier":"RUNTIME"},"shixun":{"identifier":"SHIXUN"}}`), &changed); err != nil {
		t.Fatal(err)
	}
	if err := r.ValidateTask(&changed, l); !errors.Is(err, ErrOptionalTask) {
		t.Fatalf("optional kind must be skipped before kind inspection: %v", err)
	}
	changed.Challenge.ID++
	if err := r.ValidateTask(&changed, l); err == nil || errors.Is(err, ErrOptionalTask) {
		t.Fatalf("optional status weakened identity guard: %v", err)
	}
}

func TestHomeworkRosterDoesNotRequireOptionalRuntime(t *testing.T) {
	for _, tc := range []struct {
		name, secondStatus string
		wantErr            bool
	}{
		{"explicit optional", "optional", false},
		{"required zero score", "required", true},
		{"unknown zero score", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/shixun_homeworks/44/header_info.json":
					fmt.Fprint(w, `{"data":{"homework_id":44,"work_id":5,"myshixun_identifier":"RUNTIME","shixun_identifier":"SHIXUN","challenges":[{"id":10,"name":"required"},{"id":11,"name":"elective"}]}}`)
				case "/api/homework_commons/44/shixun_challenge_data.json":
					fmt.Fprintf(w, `{"data":{"challenge_settings":[{"challenge_id":10,"challenge_name":"required","status":"required","challenge_score":16,"game_score":"16"},{"challenge_id":11,"challenge_name":"elective","status":%q,"challenge_score":0,"game_score":"0"}]}}`, tc.secondStatus)
				case "/api/myshixuns/RUNTIME/challenges.json":
					fmt.Fprint(w, `[{"identifier":"G1","position":1,"name":"required","status":2}]`)
				default:
					t.Errorf("unexpected roster route %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client := New(server.URL)
			roster, err := client.HomeworkRoster(context.Background(), "COURSE", Homework{ID: 44, IsShixun: true, ShixunIdentifier: "SHIXUN", ChallengeCount: 2})
			if (err != nil) != tc.wantErr {
				t.Fatalf("roster=%+v err=%v", roster, err)
			}
			if tc.wantErr {
				return
			}
			if len(roster.Levels) != 2 || len(roster.RequiredLevels()) != 1 || roster.RequiredLevels()[0].Game != "G1" || !roster.Levels[1].Optional || roster.Levels[1].Game != "" {
				t.Fatalf("roster=%+v", roster)
			}
			if _, err := client.BindHomeworkLevel(roster, roster.Levels[1]); !errors.Is(err, ErrOptionalTask) {
				t.Fatalf("binding optional level: %v", err)
			}
		})
	}
}
