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

func TestAssignedHomeworkCountsOnlyReadsCanonicalAssignmentHeader(t *testing.T) {
	const selectedHeader = `[{"id":10,"name":"required"},{"id":11,"name":"elective"}]`
	const assignedProgress = `[{"challenge_id":10,"challenge_name":"required","status":"required","challenge_score":"10","game_score":"10"},{"challenge_id":11,"challenge_name":"elective","status":"optional","challenge_score":"10","game_score":"0"}]`
	const fullProgress = `[{"challenge_id":10,"challenge_name":"required","status":"required","challenge_score":"10","game_score":"10"},{"challenge_id":11,"challenge_name":"elective","status":"optional","challenge_score":"10","game_score":"0"},{"challenge_id":12,"challenge_name":"outside","status":"required","challenge_score":"10","game_score":"0"},{"challenge_id":13,"challenge_name":"also outside","status":"required","challenge_score":"10","game_score":"0"}]`
	headerResponse := func(challenges string) string {
		return fmt.Sprintf(`{"data":{"homework_id":44,"work_id":5,"myshixun_identifier":"RUNTIME","shixun_identifier":"SHIXUN","challenges":%s}}`, challenges)
	}
	for _, tc := range []struct {
		name, header, progress  string
		catalogCount, status    int
		wantRequired, wantDone  int
		wantValid               bool
		wantNoRead, nilProgress bool
	}{
		{name: "full catalog excludes outside zero scores and elective", header: headerResponse(selectedHeader), progress: fullProgress, catalogCount: 4, wantRequired: 1, wantDone: 1, wantValid: true},
		{name: "three assigned complete out of four catalog levels", header: headerResponse(`[{"id":10,"name":"required"},{"id":11,"name":"elective"},{"id":12,"name":"outside"}]`), progress: `[{"challenge_id":10,"challenge_name":"required","challenge_score":"10","game_score":"10"},{"challenge_id":11,"challenge_name":"elective","challenge_score":"10","game_score":"10"},{"challenge_id":12,"challenge_name":"outside","challenge_score":"10","game_score":"10"},{"challenge_id":13,"challenge_name":"also outside","challenge_score":"10","game_score":"0"}]`, catalogCount: 4, wantRequired: 3, wantDone: 3, wantValid: true},
		{name: "one assigned complete out of four catalog levels", header: headerResponse(`[{"id":10,"name":"required"}]`), progress: fullProgress, catalogCount: 4, wantRequired: 1, wantDone: 1, wantValid: true},
		{name: "assignment only progress with larger catalog", header: headerResponse(selectedHeader), progress: assignedProgress, catalogCount: 4, wantRequired: 1, wantDone: 1, wantValid: true},
		{name: "required zero score remains pending", header: headerResponse(selectedHeader), progress: strings.Replace(assignedProgress, `"game_score":"10"`, `"game_score":"0"`, 1), catalogCount: 4, wantRequired: 1, wantValid: true},
		{name: "required zero maximum remains pending", header: headerResponse(selectedHeader), progress: strings.Replace(assignedProgress, `"challenge_score":"10","game_score":"10"`, `"challenge_score":"0","game_score":"0"`, 1), catalogCount: 4, wantRequired: 1, wantValid: true},
		{name: "unknown required score remains pending", header: headerResponse(selectedHeader), progress: strings.Replace(assignedProgress, `"game_score":"10"`, `"game_score":"unknown"`, 1), catalogCount: 4, wantRequired: 1, wantValid: true},
		{name: "all assigned elective excludes outside required", header: headerResponse(selectedHeader), progress: strings.Replace(fullProgress, `"status":"required"`, `"status":"optional"`, 1), catalogCount: 4, wantValid: true},
		{name: "legacy broader header uses exact catalog assignment count", header: headerResponse(selectedHeader), progress: `[{"challenge_id":10,"challenge_name":"required","challenge_score":"10","game_score":"10"}]`, catalogCount: 1, wantRequired: 1, wantDone: 1, wantValid: true},
		{name: "empty header permits complete catalog only", header: headerResponse(`[]`), progress: fullProgress, catalogCount: 4, wantRequired: 3, wantDone: 1, wantValid: true},
		{name: "empty header rejects assignment only progress", header: headerResponse(`[]`), progress: assignedProgress, catalogCount: 4},
		{name: "selected progress missing", header: headerResponse(selectedHeader), progress: `[{"challenge_id":10,"challenge_name":"required"}]`, catalogCount: 4},
		{name: "selected name must match exactly", header: headerResponse(selectedHeader), progress: strings.Replace(assignedProgress, `"challenge_name":"required"`, `"challenge_name":"renamed"`, 1), catalogCount: 4},
		{name: "selected name whitespace is not normalized", header: headerResponse(selectedHeader), progress: strings.Replace(assignedProgress, `"challenge_name":"required"`, `"challenge_name":" required "`, 1), catalogCount: 4},
		{name: "duplicate selected progress", header: headerResponse(selectedHeader), progress: `[{"challenge_id":10,"challenge_name":"required"},{"challenge_id":10,"challenge_name":"required"},{"challenge_id":11,"challenge_name":"elective","status":"optional"}]`, catalogCount: 4},
		{name: "duplicate header ID", header: headerResponse(`[{"id":10,"name":"required"},{"id":10,"name":"elective"}]`), progress: fullProgress, catalogCount: 4},
		{name: "duplicate header name", header: headerResponse(`[{"id":10,"name":"required"},{"id":11,"name":"required"}]`), progress: fullProgress, catalogCount: 4},
		{name: "invalid header ID", header: headerResponse(`[{"id":0,"name":"required"}]`), progress: fullProgress, catalogCount: 4},
		{name: "blank header name", header: headerResponse(`[{"id":10,"name":"  "}]`), progress: fullProgress, catalogCount: 4},
		{name: "duplicate names without header", header: headerResponse(`[]`), progress: `[{"challenge_id":10,"challenge_name":"required"},{"challenge_id":11,"challenge_name":"required"}]`, catalogCount: 2},
		{name: "assignment exceeds catalog count", header: headerResponse(selectedHeader), progress: assignedProgress, catalogCount: 1},
		{name: "homework identity mismatch", header: strings.Replace(headerResponse(selectedHeader), `"homework_id":44`, `"homework_id":45`, 1), progress: fullProgress, catalogCount: 4},
		{name: "shixun identity mismatch", header: strings.Replace(headerResponse(selectedHeader), `"shixun_identifier":"SHIXUN"`, `"shixun_identifier":"OTHER"`, 1), progress: fullProgress, catalogCount: 4},
		{name: "student work identity mismatch", header: strings.Replace(headerResponse(selectedHeader), `"work_id":5`, `"work_id":6`, 1), progress: fullProgress, catalogCount: 4},
		{name: "runtime identity mismatch", header: strings.Replace(headerResponse(selectedHeader), `"myshixun_identifier":"RUNTIME"`, `"myshixun_identifier":"OTHER"`, 1), progress: fullProgress, catalogCount: 4},
		{name: "missing student work identity", header: strings.Replace(headerResponse(selectedHeader), `"work_id":5`, `"work_id":0`, 1), progress: fullProgress, catalogCount: 4},
		{name: "missing runtime identity", header: strings.Replace(headerResponse(selectedHeader), `"myshixun_identifier":"RUNTIME"`, `"myshixun_identifier":""`, 1), progress: fullProgress, catalogCount: 4},
		{name: "invalid runtime identity", header: strings.Replace(headerResponse(selectedHeader), `"myshixun_identifier":"RUNTIME"`, `"myshixun_identifier":"bad/runtime"`, 1), progress: fullProgress, catalogCount: 4},
		{name: "missing header body", header: `{}`, progress: fullProgress, catalogCount: 4},
		{name: "malformed header body", header: `{"data":`, progress: fullProgress, catalogCount: 4},
		{name: "header read fails", header: `{}`, status: http.StatusForbidden, progress: fullProgress, catalogCount: 4},
		{name: "catalog missing", progress: fullProgress, catalogCount: 0, wantNoRead: true},
		{name: "catalog exceeds limit", progress: fullProgress, catalogCount: 101, wantNoRead: true},
		{name: "nil progress", catalogCount: 4, nilProgress: true, wantNoRead: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var progress *HomeworkProgress
			if !tc.nilProgress {
				progress = &HomeworkProgress{}
				if err := json.Unmarshal([]byte(tc.progress), &progress.Challenges); err != nil {
					t.Fatal(err)
				}
			}
			var headerReads, unexpectedReads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/shixun_homeworks/44/header_info.json" || r.URL.RawQuery != "" {
					unexpectedReads.Add(1)
					t.Errorf("assignment counts used unexpected endpoint: %s %s", r.Method, r.URL.String())
					http.NotFound(w, r)
					return
				}
				headerReads.Add(1)
				if got := r.Header.Get("Referer"); !strings.HasSuffix(got, "/classrooms/COURSE/shixun_homework/44/detail") {
					t.Errorf("wrong homework header referrer: %s", got)
				}
				w.Header().Set("Content-Type", "application/json")
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				fmt.Fprint(w, tc.header)
			}))
			defer server.Close()
			hw := Homework{ID: 44, IsShixun: true, ShixunIdentifier: "SHIXUN", StudentWorkID: 5, MyShixunIdentifier: "RUNTIME", ChallengeCount: tc.catalogCount}
			required, completed, valid := New(server.URL).AssignedHomeworkCounts(context.Background(), "COURSE", hw, progress)
			if required != tc.wantRequired || completed != tc.wantDone || valid != tc.wantValid {
				t.Fatalf("counts=(%d,%d,%t), want=(%d,%d,%t)", required, completed, valid, tc.wantRequired, tc.wantDone, tc.wantValid)
			}
			wantReads := int32(1)
			if tc.wantNoRead {
				wantReads = 0
			}
			if headerReads.Load() != wantReads || unexpectedReads.Load() != 0 {
				t.Fatalf("header reads=%d, unexpected/runtime/write reads=%d; want %d header reads only", headerReads.Load(), unexpectedReads.Load(), wantReads)
			}
		})
	}
}

func TestCanonicalHomeworkAssignmentSelectedCountLimit(t *testing.T) {
	for _, count := range []int{100, 101} {
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
				t.Fatal("selected roster above 100 levels was accepted")
			}
		})
	}
}

func TestAssignedHomeworkCountsRejectsInvalidContextWithoutReads(t *testing.T) {
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		http.NotFound(w, r)
	}))
	defer server.Close()
	validHomework := Homework{ID: 44, IsShixun: true, ShixunIdentifier: "SHIXUN", ChallengeCount: 1}
	progress := &HomeworkProgress{Challenges: []ChallengeProgress{{ChallengeID: 10, Name: "required"}}}
	for _, tc := range []struct {
		name, course string
		hw           Homework
	}{
		{name: "invalid course", course: "bad/course", hw: validHomework},
		{name: "missing homework", course: "COURSE", hw: Homework{IsShixun: true, ShixunIdentifier: "SHIXUN", ChallengeCount: 1}},
		{name: "invalid shixun", course: "COURSE", hw: Homework{ID: 44, IsShixun: true, ShixunIdentifier: "bad/shixun", ChallengeCount: 1}},
		{name: "non-shixun homework", course: "COURSE", hw: Homework{ID: 44, ShixunIdentifier: "SHIXUN", ChallengeCount: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			required, done, valid := New(server.URL).AssignedHomeworkCounts(context.Background(), tc.course, tc.hw, progress)
			if required != 0 || done != 0 || valid {
				t.Fatalf("invalid context got (%d,%d,%t)", required, done, valid)
			}
		})
	}
	if reads.Load() != 0 {
		t.Fatalf("invalid context made %d reads", reads.Load())
	}
}
