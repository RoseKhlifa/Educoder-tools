package educoder

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestHomeworkRosterUsesCompleteProgressNotCodeOnlyHeader(t *testing.T) {
	const fullProgress = `[{"challenge_id":10,"challenge_name":"first","status":"required","challenge_st":0},{"challenge_id":11,"challenge_name":"second","status":"required","challenge_st":0},{"challenge_id":12,"challenge_name":"third","status":"required","challenge_st":0},{"challenge_id":13,"challenge_name":"outside","status":"required","challenge_st":1}]`
	const allGames = `[{"identifier":"G1","position":1,"name":"first"},{"identifier":"G2","position":2,"name":"second"},{"identifier":"G3","position":3,"name":"third"},{"identifier":"G4","position":4,"name":"outside"}]`
	for _, tc := range []struct {
		name, header, progress, games                        string
		catalogCount, wantLevels, wantRequired, wantPosition int
		wantErr                                              bool
	}{
		{name: "code header has three but all four levels are assigned", catalogCount: 4,
			header: `[{"id":10,"name":"first"},{"id":11,"name":"second"},{"id":12,"name":"third"}]`, progress: fullProgress, games: allGames,
			wantLevels: 4, wantRequired: 4, wantPosition: 1},
		{name: "code header has one but all four levels are assigned", catalogCount: 4,
			header: `[{"id":11,"name":"second"}]`, progress: fullProgress, games: allGames,
			wantLevels: 4, wantRequired: 4, wantPosition: 1},
		{name: "partial scores matching code header do not prove assignment", catalogCount: 4,
			header:   `[{"id":10,"name":"first"},{"id":11,"name":"second"}]`,
			progress: `[{"challenge_id":10,"challenge_name":"first"},{"challenge_id":11,"challenge_name":"second"}]`, games: allGames,
			wantErr: true},
		{name: "explicit optional excluded but header omitted required rows remain", catalogCount: 4,
			header:   `[{"id":10,"name":"first"},{"id":11,"name":"second"}]`,
			progress: `[{"challenge_id":10,"challenge_name":"first","status":"required"},{"challenge_id":11,"challenge_name":"second","status":"optional"},{"challenge_id":12,"challenge_name":"third"},{"challenge_id":13,"challenge_name":"outside"}]`,
			games:    allGames, wantLevels: 4, wantRequired: 3, wantPosition: 1},
		{name: "incomplete assigned coverage is rejected", catalogCount: 4,
			header:   `[{"id":10,"name":"first"},{"id":11,"name":"second"},{"id":12,"name":"third"}]`,
			progress: `[{"challenge_id":10,"challenge_name":"first"},{"challenge_id":11,"challenge_name":"second"}]`, games: allGames, wantErr: true},
		{name: "missing header cannot justify catalog mismatch", catalogCount: 4, header: `[]`,
			progress: `[{"challenge_id":10,"challenge_name":"first"},{"challenge_id":11,"challenge_name":"second"}]`, games: allGames, wantErr: true},
		{name: "selected name mismatch is rejected", catalogCount: 4, header: `[{"id":11,"name":"renamed"}]`, progress: fullProgress, games: allGames, wantErr: true},
		{name: "same header name with different challenge ID is rejected", catalogCount: 4, header: `[{"id":99,"name":"first"}]`, progress: fullProgress, games: allGames, wantErr: true},
		{name: "duplicate selected progress cannot justify coverage", catalogCount: 4, header: `[{"id":10,"name":"first"}]`,
			progress: `[{"challenge_id":10,"challenge_name":"first"},{"challenge_id":10,"challenge_name":"first"}]`, games: allGames, wantErr: true},
		{name: "duplicate header is rejected", catalogCount: 4, header: `[{"id":10,"name":"first"},{"id":10,"name":"first"}]`, progress: fullProgress, games: allGames, wantErr: true},
		{name: "catalog smaller than complete header is rejected", catalogCount: 1, header: `[{"id":10,"name":"first"},{"id":11,"name":"second"}]`, progress: fullProgress, games: allGames, wantErr: true},
		{name: "legacy broader header with catalog selected count remains valid", catalogCount: 1,
			header:   `[{"id":10,"name":"first"},{"id":11,"name":"second"},{"id":12,"name":"third"}]`,
			progress: `[{"challenge_id":10,"challenge_name":"first"}]`, games: allGames, wantLevels: 1, wantRequired: 1, wantPosition: 1},
		{name: "no header with exact catalog count remains valid", catalogCount: 4, header: `[]`, progress: fullProgress, games: allGames, wantLevels: 4, wantRequired: 4, wantPosition: 1},
		{name: "missing catalog cannot prove assignment from header", catalogCount: 0, header: `[{"id":10,"name":"first"}]`, progress: `[{"challenge_id":10,"challenge_name":"first"}]`, games: allGames, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var runtimeReads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("roster made non-read request: %s", r.Method)
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/shixun_homeworks/44/header_info.json":
					fmt.Fprintf(w, `{"data":{"homework_id":44,"work_id":5,"myshixun_identifier":"RUNTIME","shixun_identifier":"SHIXUN","challenges":%s}}`, tc.header)
				case "/api/homework_commons/44/shixun_challenge_data.json":
					fmt.Fprintf(w, `{"data":{"challenge_settings":%s}}`, tc.progress)
				case "/api/myshixuns/RUNTIME/challenges.json":
					runtimeReads.Add(1)
					fmt.Fprint(w, tc.games)
				default:
					t.Errorf("unexpected roster route %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			roster, err := New(server.URL).HomeworkRoster(context.Background(), "COURSE", Homework{ID: 44, IsShixun: true, ShixunIdentifier: "SHIXUN", ChallengeCount: tc.catalogCount})
			if (err != nil) != tc.wantErr {
				t.Fatalf("roster=%+v err=%v", roster, err)
			}
			if tc.wantErr {
				if runtimeReads.Load() != 0 {
					t.Fatalf("unproven score roster reached runtime: reads=%d", runtimeReads.Load())
				}
				return
			}
			if len(roster.Levels) != tc.wantLevels || len(roster.RequiredLevels()) != tc.wantRequired || roster.Levels[0].Position != tc.wantPosition {
				t.Fatalf("selected roster=%+v required=%+v", roster.Levels, roster.RequiredLevels())
			}
		})
	}
}
