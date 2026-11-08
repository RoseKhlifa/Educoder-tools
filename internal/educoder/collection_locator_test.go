package educoder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCollectionLocateExistingRuntimeWithoutStartingIt(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any, []map[string]any, *Homework, *HomeworkProgress)
		want   int
	}{
		{"passed subset", func(map[string]any, []map[string]any, *Homework, *HomeworkProgress) {}, 1},
		{"lazy unfinished game does not block passed collection", func(_ map[string]any, g []map[string]any, _ *Homework, _ *HomeworkProgress) {
			g[1]["identifier"] = nil
			g[1]["status"] = 3
		}, 1},
		{"missing game with full score is rejected", func(_ map[string]any, g []map[string]any, _ *Homework, _ *HomeworkProgress) {
			g[0]["identifier"] = nil
			g[0]["status"] = 3
		}, 0},
		{"header omits challenges", func(h map[string]any, _ []map[string]any, _ *Homework, _ *HomeworkProgress) { delete(h, "challenges") }, 1},
		{"header omits challenges ambiguous scores", func(h map[string]any, _ []map[string]any, _ *Homework, p *HomeworkProgress) {
			delete(h, "challenges")
			p.Challenges[1].Name = p.Challenges[0].Name
		}, 0},
		{"wrong homework", func(h map[string]any, _ []map[string]any, _ *Homework, _ *HomeworkProgress) { h["homework_id"] = 77 }, 0},
		{"wrong work", func(h map[string]any, _ []map[string]any, _ *Homework, _ *HomeworkProgress) { h["work_id"] = 77 }, 0},
		{"wrong shixun", func(h map[string]any, _ []map[string]any, _ *Homework, _ *HomeworkProgress) {
			h["shixun_identifier"] = "OTHER"
		}, 0},
		{"wrong runtime", func(h map[string]any, _ []map[string]any, _ *Homework, _ *HomeworkProgress) {
			h["myshixun_identifier"] = "OTHER"
		}, 0},
		{"missing runtime", func(h map[string]any, _ []map[string]any, _ *Homework, _ *HomeworkProgress) {
			h["myshixun_identifier"] = ""
		}, 0},
		{"duplicate position", func(_ map[string]any, g []map[string]any, _ *Homework, _ *HomeworkProgress) { g[1]["position"] = 1 }, 0},
		{"duplicate game", func(_ map[string]any, g []map[string]any, _ *Homework, _ *HomeworkProgress) {
			g[1]["identifier"] = "GAME_PASSED"
		}, 0},
		{"wrong name", func(_ map[string]any, g []map[string]any, _ *Homework, _ *HomeworkProgress) { g[0]["name"] = "other" }, 0},
		{"passed label insufficient", func(_ map[string]any, _ []map[string]any, _ *Homework, p *HomeworkProgress) {
			p.Challenges[0].GameScore = "9.99"
		}, 0},
		{"wrong score target", func(_ map[string]any, _ []map[string]any, _ *Homework, p *HomeworkProgress) {
			p.Challenges[0].ChallengeID = 88
		}, 0},
		{"wrong progress name", func(_ map[string]any, _ []map[string]any, _ *Homework, p *HomeworkProgress) {
			p.Challenges[0].Name = "other"
		}, 0},
		{"runtime incomplete", func(_ map[string]any, g []map[string]any, _ *Homework, _ *HomeworkProgress) { g[0]["status"] = 0 }, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hw := Homework{ID: 4, StudentWorkID: 5, MyShixunIdentifier: "RUNTIME", ShixunIdentifier: "SHIXUN", ChallengeCount: 2}
			p := &HomeworkProgress{Challenges: []ChallengeProgress{{ChallengeID: 10, Name: "passed", GameScore: "10.00", ChallengeScore: "10"}, {ChallengeID: 11, Name: "unfinished", GameScore: "0", ChallengeScore: "10"}}}
			h := map[string]any{"homework_id": 4, "work_id": 5, "myshixun_identifier": "RUNTIME", "shixun_identifier": "SHIXUN", "challenges": []any{map[string]any{"id": 10, "name": "passed"}, map[string]any{"id": 11, "name": "unfinished"}}}
			games := []map[string]any{{"identifier": "GAME_PASSED", "position": 1, "name": "passed", "status": 2}, {"identifier": "GAME_UNFINISHED", "position": 2, "name": "unfinished", "status": 0}}
			tc.mutate(h, games, &hw, p)
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Fatal("write")
				}
				switch r.URL.Path {
				case "/api/shixun_homeworks/4/header_info.json":
					json.NewEncoder(w).Encode(map[string]any{"data": h})
				case "/api/myshixuns/RUNTIME/challenges.json":
					if r.URL.Query().Get("homework_common_id") != "4" {
						t.Error("missing homework scope")
					}
					json.NewEncoder(w).Encode(games)
				default:
					t.Error("unreviewed endpoint", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer s.Close()
			client := New(s.URL)
			client.LoginName = "demo"
			reader, e := client.CollectionReader(CollectionBudget{Requests: 4, ResponseBytes: 1024, TotalBytes: 4096})
			if e != nil {
				t.Fatal(e)
			}
			result, e := reader.CollectionLocate(context.Background(), "COURSE", hw, p)
			if e != nil || len(result.Targets) != tc.want {
				t.Fatalf("%+v %v", result, e)
			}
			if tc.want > 0 {
				v := result.Targets[0]
				if v.Game != "GAME_PASSED" || v.Game == hw.MyShixunIdentifier || v.ChallengeID != 10 || v.MyShixunIdentifier != "RUNTIME" {
					t.Fatal(v)
				}
			}
		})
	}
}

// The header is a code-level subset on mixed code/objective assignments. It
// must not narrow the complete assignment-specific score roster or index its
// rows by runtime position.
func TestCollectionLocateMixedKindsUsesCompleteAssignmentProgress(t *testing.T) {
	for _, headerCount := range []int{0, 1, 3} {
		t.Run(fmt.Sprintf("header_%d_of_4", headerCount), func(t *testing.T) {
			code, objective := 0, 1
			p := &HomeworkProgress{}
			h := map[string]any{"homework_id": 4, "work_id": 5, "myshixun_identifier": "RUNTIME", "shixun_identifier": "SHIXUN"}
			var header []map[string]any
			var games []map[string]any
			for i := 1; i <= 4; i++ {
				name := fmt.Sprintf("level %d", i)
				st := &objective
				if (headerCount == 1 && i == 2) || (headerCount == 3 && i < 4) {
					st = &code
					header = append(header, map[string]any{"id": i, "name": name})
				}
				p.Challenges = append(p.Challenges, ChallengeProgress{ChallengeID: int64(i), Name: name, ST: st, GameScore: "25", ChallengeScore: "25"})
				games = append(games, map[string]any{"identifier": fmt.Sprintf("GAME%d", i), "position": i, "name": name, "status": 2, "challenge_st": *st})
			}
			h["challenges"] = header
			got, err := collectionLocatorFixture(t, h, games, Homework{ID: 4, StudentWorkID: 5, MyShixunIdentifier: "RUNTIME", ShixunIdentifier: "SHIXUN", ChallengeCount: 4}, p)
			if err != nil || len(got.Targets) != 4 || len(got.Reasons) != 0 {
				t.Fatalf("targets=%+v reasons=%v err=%v", got.Targets, got.Reasons, err)
			}
			for i, target := range got.Targets {
				if target.ChallengeID != int64(i+1) || target.Position != i+1 || target.Game != fmt.Sprintf("GAME%d", i+1) {
					t.Fatal(target)
				}
			}
		})
	}
}

func TestCollectionLocateRejectsIncompleteOrAmbiguousAssignmentProof(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any, []map[string]any, *Homework, *HomeworkProgress)
	}{
		{"missing catalog count", func(_ map[string]any, _ []map[string]any, hw *Homework, _ *HomeworkProgress) { hw.ChallengeCount = 0 }},
		{"truncated progress", func(_ map[string]any, _ []map[string]any, _ *Homework, p *HomeworkProgress) {
			p.Challenges = p.Challenges[:1]
		}},
		{"duplicate progress id", func(_ map[string]any, _ []map[string]any, _ *Homework, p *HomeworkProgress) {
			p.Challenges[1].ChallengeID = 10
		}},
		{"duplicate selected runtime name", func(_ map[string]any, games []map[string]any, _ *Homework, _ *HomeworkProgress) {
			games[1]["name"] = "passed"
		}},
		{"overlapping header id wrong name", func(h map[string]any, _ []map[string]any, _ *Homework, _ *HomeworkProgress) {
			h["challenges"] = []map[string]any{{"id": 10, "name": "other"}}
		}},
		{"overlapping header name wrong id", func(h map[string]any, _ []map[string]any, _ *Homework, _ *HomeworkProgress) {
			h["challenges"] = []map[string]any{{"id": 99, "name": "passed"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hw := Homework{ID: 4, StudentWorkID: 5, MyShixunIdentifier: "RUNTIME", ShixunIdentifier: "SHIXUN", ChallengeCount: 2}
			p := &HomeworkProgress{Challenges: []ChallengeProgress{{ChallengeID: 10, Name: "passed", GameScore: "10", ChallengeScore: "10"}, {ChallengeID: 11, Name: "unfinished", GameScore: "0", ChallengeScore: "10"}}}
			h := map[string]any{"homework_id": 4, "work_id": 5, "myshixun_identifier": "RUNTIME", "shixun_identifier": "SHIXUN", "challenges": []map[string]any{{"id": 10, "name": "passed"}}}
			games := []map[string]any{{"identifier": "GAME_PASSED", "position": 1, "name": "passed", "status": 2}, {"identifier": "GAME_UNFINISHED", "position": 2, "name": "unfinished", "status": 0}}
			tc.mutate(h, games, &hw, p)
			got, err := collectionLocatorFixture(t, h, games, hw, p)
			if err != nil || len(got.Targets) != 0 || len(got.Reasons) == 0 {
				t.Fatalf("targets=%+v reasons=%v err=%v", got.Targets, got.Reasons, err)
			}
		})
	}
}

func TestCollectionLocateGenuineAssignedSubsetRetainsRuntimePosition(t *testing.T) {
	hw := Homework{ID: 4, StudentWorkID: 5, MyShixunIdentifier: "RUNTIME", ShixunIdentifier: "SHIXUN", ChallengeCount: 1}
	p := &HomeworkProgress{Challenges: []ChallengeProgress{{ChallengeID: 12, Name: "selected", GameScore: "25", ChallengeScore: "25"}}}
	h := map[string]any{"homework_id": 4, "work_id": 5, "myshixun_identifier": "RUNTIME", "shixun_identifier": "SHIXUN", "challenges": []map[string]any{{"id": 12, "name": "selected"}}}
	games := []map[string]any{{"identifier": "GAME1", "position": 1, "name": "unselected 1", "status": 2}, {"identifier": "GAME2", "position": 2, "name": "unselected 2", "status": 2}, {"identifier": "GAME3", "position": 3, "name": "selected", "status": 2}, {"identifier": "GAME4", "position": 4, "name": "unselected 4", "status": 2}}
	got, err := collectionLocatorFixture(t, h, games, hw, p)
	if err != nil || len(got.Targets) != 1 || len(got.Reasons) != 0 || got.Targets[0].ChallengeID != 12 || got.Targets[0].Position != 3 || got.Targets[0].Game != "GAME3" {
		t.Fatalf("%+v %v", got, err)
	}
}

func collectionLocatorFixture(t *testing.T, header map[string]any, games []map[string]any, hw Homework, p *HomeworkProgress) (CollectionLocationResult, error) {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("write request", r.Method, r.URL.Path)
			http.Error(w, "write forbidden", http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/api/shixun_homeworks/4/header_info.json":
			json.NewEncoder(w).Encode(map[string]any{"data": header})
		case "/api/myshixuns/RUNTIME/challenges.json":
			if r.URL.Query().Get("homework_common_id") != "4" {
				t.Error("missing homework scope")
			}
			json.NewEncoder(w).Encode(games)
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	c := New(s.URL)
	c.LoginName = "demo"
	reader, err := c.CollectionReader(CollectionBudget{Requests: 2, ResponseBytes: 8192, TotalBytes: 16384})
	if err != nil {
		t.Fatal(err)
	}
	return reader.CollectionLocate(context.Background(), "COURSE", hw, p)
}

func TestCollectionLocatorReadOnlyAllowlist(t *testing.T) {
	for _, p := range []string{"/api/myshixuns/R/challenges.json", "/api/shixun_homeworks/4/header_info.json"} {
		if !collectionReadPath(p) {
			t.Fatal(p)
		}
	}
	for _, p := range []string{"/api/myshixuns/R/start.json", "/api/myshixuns/R/active_pod.json", "/api/myshixuns/R/reset_my_game.json", "/api/student_works/4/query_game_url.json"} {
		if collectionReadPath(p) {
			t.Fatal("unreviewed collection route", p)
		}
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"status":403,"message":"denied"}`)) }))
	defer s.Close()
	c := New(s.URL)
	c.LoginName = "demo"
	reader, _ := c.CollectionReader(CollectionBudget{Requests: 1, ResponseBytes: 1024, TotalBytes: 1024})
	_, err := reader.CollectionLocate(context.Background(), "C", Homework{ID: 4, ShixunIdentifier: "S"}, &HomeworkProgress{})
	if err == nil {
		t.Fatal("accepted permission error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = reader.CollectionLocate(ctx, "C", Homework{ID: 4, ShixunIdentifier: "S"}, &HomeworkProgress{})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
