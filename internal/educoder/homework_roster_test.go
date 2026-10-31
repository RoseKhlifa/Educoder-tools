package educoder

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
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
