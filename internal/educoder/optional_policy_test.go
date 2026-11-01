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

func TestOptionalPolicyUsesExplicitAssignmentStatus(t *testing.T) {
	for _, tc := range []struct {
		name, wire string
		optional   bool
	}{
		{"required positive", `{"status":"required","challenge_score":16,"game_score":"16.00","is_choose_todo":false}`, false},
		{"observed optional zero", `{"status":"optional","challenge_score":0,"game_score":"0.00","is_choose_todo":false}`, true},
		{"optional positive", `{"status":"optional","challenge_score":16,"game_score":"0.00"}`, true},
		{"required zero", `{"status":"required","challenge_score":0,"game_score":"0.00"}`, false},
		{"missing status zero", `{"challenge_score":0,"game_score":"0.00"}`, false},
		{"unknown status", `{"status":"unknown","challenge_score":0}`, false},
		{"unverified choose flag", `{"status":"required","is_choose_todo":true,"challenge_score":0}`, false},
		{"unverified challenge optional flag", `{"challenge_optional":true,"challenge_score":0}`, false},
		{"explicit label", `{"status":"选做","challenge_score":0}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var progress ChallengeProgress
			var task Task
			if err := json.Unmarshal([]byte(tc.wire), &progress); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tc.wire), &task); err != nil {
				t.Fatal(err)
			}
			if progress.IsOptional() != tc.optional || task.IsOptional() != tc.optional {
				t.Fatalf("progress=%+v task-status=%q optional=%v", progress, task.RequiredStatus, tc.optional)
			}
		})
	}
}

func TestRequiredChallengesPreservesRawHistory(t *testing.T) {
	p := &HomeworkProgress{Challenges: []ChallengeProgress{
		{ChallengeID: 1, RequiredStatus: "required", ChallengeScore: "16", GameScore: "16"},
		{ChallengeID: 2, RequiredStatus: "optional", ChallengeScore: "0", GameScore: "0"},
		{ChallengeID: 3, ChallengeScore: "0", GameScore: "0"},
	}}
	filtered := p.RequiredChallenges()
	if len(filtered) != 2 || filtered[0].ChallengeID != 1 || filtered[1].ChallengeID != 3 {
		t.Fatalf("required=%+v", filtered)
	}
	filtered[0].Name = "local view"
	if len(p.Challenges) != 3 || p.Challenges[0].Name != "" || !p.Challenges[1].IsOptional() {
		t.Fatalf("raw progress was changed: %+v", p)
	}
	if ((*Task)(nil)).IsOptional() || len(((*HomeworkProgress)(nil)).RequiredChallenges()) != 0 {
		t.Fatal("nil raw metadata should not become optional")
	}
}

func TestExecutionClientRejectsTaskThatBecomesOptionalOnReload(t *testing.T) {
	var status atomic.Value
	status.Store("required")
	var writes atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":%q,"game":{"identifier":"G1"},"homework_common_id":44}`, status.Load().(string))
	}))
	defer server.Close()
	client := New(server.URL)
	guarded, err := client.WithRequiredTasks()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := guarded.LoadTask(context.Background(), "COURSE", 44, "G1"); err != nil {
		t.Fatal(err)
	}
	status.Store("optional")
	if _, err := guarded.LoadTask(context.Background(), "COURSE", 44, "G1"); !errors.Is(err, ErrOptionalTask) {
		t.Fatalf("optional reload error=%v", err)
	}
	raw, err := client.LoadTask(context.Background(), "COURSE", 44, "G1")
	if err != nil || !raw.IsOptional() {
		t.Fatalf("original read client changed: task=%+v err=%v", raw, err)
	}
	if writes.Load() != 0 {
		t.Fatalf("read guard wrote %d times", writes.Load())
	}
}
