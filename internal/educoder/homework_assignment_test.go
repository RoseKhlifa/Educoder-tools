package educoder

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAssignedHomeworkCountsUsesCompleteProgressWithoutHeaderReads(t *testing.T) {
	const fullProgress = `[{"challenge_id":10,"challenge_name":"code first","status":"required","challenge_st":0,"challenge_score":"25","game_score":"25"},{"challenge_id":11,"challenge_name":"code second","status":"required","challenge_st":0,"challenge_score":"25","game_score":"25"},{"challenge_id":12,"challenge_name":"code third","status":"required","challenge_st":0,"challenge_score":"25","game_score":"25"},{"challenge_id":13,"challenge_name":"objective","status":"required","challenge_st":1,"challenge_score":"25","game_score":"0"}]`
	for _, tc := range []struct {
		name, progress         string
		catalog                int
		wantRequired, wantDone int
		wantValid              bool
	}{
		{name: "three code passed and required objective unsubmitted", progress: fullProgress, catalog: 4, wantRequired: 4, wantDone: 3, wantValid: true},
		{name: "one code passed and three required levels unsubmitted", progress: strings.Replace(fullProgress, `"game_score":"25"`, `"game_score":"0"`, 2), catalog: 4, wantRequired: 4, wantDone: 1, wantValid: true},
		{name: "explicit optional is excluded but required objective remains", progress: strings.Replace(fullProgress, `"status":"required"`, `"status":"optional"`, 1), catalog: 4, wantRequired: 3, wantDone: 2, wantValid: true},
		{name: "unknown score stays incomplete", progress: strings.Replace(fullProgress, `"game_score":"25"`, `"game_score":"unknown"`, 1), catalog: 4, wantRequired: 4, wantDone: 2, wantValid: true},
		{name: "all optional", progress: strings.ReplaceAll(fullProgress, `"status":"required"`, `"status":"optional"`), catalog: 4, wantValid: true},
		{name: "assignment subset proved by exact progress and catalog", progress: `[{"challenge_id":10,"challenge_name":"selected","challenge_score":"25","game_score":"25"}]`, catalog: 1, wantRequired: 1, wantDone: 1, wantValid: true},
		{name: "partial data does not shrink denominator", progress: `[{"challenge_id":10,"challenge_name":"code first"},{"challenge_id":11,"challenge_name":"code second"}]`, catalog: 4},
		{name: "duplicate progress ID", progress: `[{"challenge_id":10,"challenge_name":"first"},{"challenge_id":10,"challenge_name":"second"}]`, catalog: 2},
		{name: "duplicate normalized progress name", progress: `[{"challenge_id":10,"challenge_name":"first"},{"challenge_id":11,"challenge_name":" first "}]`, catalog: 2},
		{name: "missing catalog", progress: fullProgress},
		{name: "catalog exceeds limit", progress: fullProgress, catalog: 101},
		{name: "nil progress", catalog: 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var progress *HomeworkProgress
			if tc.progress != "" {
				progress = &HomeworkProgress{}
				if err := json.Unmarshal([]byte(tc.progress), &progress.Challenges); err != nil {
					t.Fatal(err)
				}
			}
			var reads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads.Add(1)
				t.Errorf("progress accounting made an unexpected request: %s %s", r.Method, r.URL.String())
				http.NotFound(w, r)
			}))
			defer server.Close()
			required, completed, valid := New(server.URL).AssignedHomeworkCounts(context.Background(), "COURSE", Homework{ID: 44, IsShixun: true, ShixunIdentifier: "SHIXUN", ChallengeCount: tc.catalog}, progress)
			if required != tc.wantRequired || completed != tc.wantDone || valid != tc.wantValid {
				t.Fatalf("counts=(%d,%d,%t), want=(%d,%d,%t)", required, completed, valid, tc.wantRequired, tc.wantDone, tc.wantValid)
			}
			if reads.Load() != 0 {
				t.Fatalf("accounting made %d requests; want none", reads.Load())
			}
		})
	}
}

func TestCanonicalHomeworkAssignmentSelectedCountLimit(t *testing.T) {
	for _, count := range []int{0, 100, 101} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			progress := &HomeworkProgress{}
			for i := 1; i <= count; i++ {
				progress.Challenges = append(progress.Challenges, ChallengeProgress{ChallengeID: int64(i), Name: fmt.Sprintf("challenge-%d", i)})
			}
			canonical, err := canonicalHomeworkAssignment(Homework{ChallengeCount: count}, progress, collectionHeader{})
			if count == 100 {
				if err != nil || len(canonical) != count {
					t.Fatalf("100-level boundary: len=%d err=%v", len(canonical), err)
				}
			} else if err == nil {
				t.Fatalf("invalid catalog count %d was accepted", count)
			}
		})
	}
}

func TestReadHomeworkHeaderRejectsIdentityMismatch(t *testing.T) {
	const validHeader = `{"data":{"homework_id":44,"work_id":5,"myshixun_identifier":"RUNTIME","shixun_identifier":"SHIXUN","challenges":[{"id":10,"name":"code only"}]}}`
	for _, tc := range []struct{ name, header string }{
		{"homework", strings.Replace(validHeader, `"homework_id":44`, `"homework_id":45`, 1)},
		{"shixun", strings.Replace(validHeader, `"shixun_identifier":"SHIXUN"`, `"shixun_identifier":"OTHER"`, 1)},
		{"work", strings.Replace(validHeader, `"work_id":5`, `"work_id":6`, 1)},
		{"runtime", strings.Replace(validHeader, `"myshixun_identifier":"RUNTIME"`, `"myshixun_identifier":"OTHER"`, 1)},
		{"missing runtime", strings.Replace(validHeader, `"myshixun_identifier":"RUNTIME"`, `"myshixun_identifier":""`, 1)},
		{"missing body", `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/shixun_homeworks/44/header_info.json" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
				}
				fmt.Fprint(w, tc.header)
			}))
			defer server.Close()
			_, err := New(server.URL).readHomeworkHeader(context.Background(), "COURSE", Homework{ID: 44, IsShixun: true, ShixunIdentifier: "SHIXUN", StudentWorkID: 5, MyShixunIdentifier: "RUNTIME"})
			if err == nil {
				t.Fatal("mismatched header identity was accepted")
			}
		})
	}
}
