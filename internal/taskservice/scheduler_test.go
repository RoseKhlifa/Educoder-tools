package taskservice

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/questionbank"
)

func TestTaskContinueRequiresTwoExplicitUpstreamBooleans(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		want      bool
	}{
		{"both true", `{"skip_level":true,"shixun":{"task_pass":true}}`, true},
		{"skip missing", `{"shixun":{"task_pass":true}}`, false},
		{"task permission false", `{"skip_level":true,"shixun":{"task_pass":false}}`, false},
		{"challenge prose is not permission", `{"skip_level":true,"challenge":{"task_pass":"true"},"shixun":{}}`, false},
		{"nonboolean rejected", `{"skip_level":"true","shixun":{"task_pass":true}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var task educoder.Task
			if err := json.Unmarshal([]byte(tc.raw), &task); (err != nil) != (tc.name == "nonboolean rejected") {
				t.Fatalf("unmarshal err=%v", err)
			}
			if err := json.Unmarshal([]byte(tc.raw), &task); err == nil && task.CanContinueAfterConfirmedFailure() != tc.want {
				t.Fatalf("continue=%v want=%v", task.CanContinueAfterConfirmedFailure(), tc.want)
			}
		})
	}
}

func TestContinueAfterFailureStopsUnknownAndAllowsOnlyConfirmedFailures(t *testing.T) {
	trueValue := true
	task := &educoder.Task{SkipLevel: &trueValue}
	task.Shixun.TaskPass = &trueValue
	for _, tc := range []struct {
		status string
		want   bool
	}{
		{"attempts_exhausted", true},
		{"generation_stalled", true},
		{"uncertain", false},
		{"verification_unconfirmed", false},
		{"budget_exhausted", false},
	} {
		if got := mayContinueAfterLevel(task, &Result{Status: tc.status}, nil); got != tc.want {
			t.Errorf("status %s continue=%v want=%v", tc.status, got, tc.want)
		}
	}
	if mayContinueAfterLevel(task, &Result{Status: "attempts_exhausted"}, errors.New("write outcome unknown")) {
		t.Fatal("unknown error must stop")
	}
}

func TestFuseFingerprintIgnoresAttemptCounterButChangesProgress(t *testing.T) {
	hw := educoder.Homework{ChallengeCount: 1, ShixunIdentifier: "s"}
	first := &educoder.HomeworkProgress{Challenges: []educoder.ChallengeProgress{{ChallengeID: 1, Name: "a", GameScore: "0", ChallengeScore: "10", EvaluateCount: 1}}}
	second := &educoder.HomeworkProgress{Challenges: []educoder.ChallengeProgress{{ChallengeID: 1, Name: "a", GameScore: "0", ChallengeScore: "10", EvaluateCount: 20}}}
	changed := &educoder.HomeworkProgress{Challenges: []educoder.ChallengeProgress{{ChallengeID: 1, Name: "a", GameScore: "10", ChallengeScore: "10", EvaluateCount: 20}}}
	if homeworkProgressFingerprint(hw, first) != homeworkProgressFingerprint(hw, second) {
		t.Fatal("failed-attempt counter alone must not defeat no-progress fuse")
	}
	if homeworkProgressFingerprint(hw, first) == homeworkProgressFingerprint(hw, changed) {
		t.Fatal("score progress must reopen fuse")
	}
}

func TestFuseOwnerIsolationAndManualSingleHomeworkBypass(t *testing.T) {
	store := bank(t)
	defer store.Close()
	s := New(store, nil, Config{})
	hw := educoder.Homework{ID: 9, ChallengeCount: 1, ShixunIdentifier: "s"}
	p := &educoder.HomeworkProgress{Challenges: []educoder.ChallengeProgress{{ChallengeID: 1, Name: "a", GameScore: "0", ChallengeScore: "10"}}}
	when := time.Now().UTC()
	batchReq := Request{Owner: "owner-a", Course: "C", HomeworkID: hw.ID}
	if err := s.rememberHomeworkFailure(context.Background(), batchReq, hw, p, p, &BatchResult{Status: "attempts_exhausted", ReasonCode: "attempts_exhausted"}, when); err != nil {
		t.Fatal(err)
	}
	if block, err := s.homeworkFuse(context.Background(), batchReq, hw, p, when.Add(time.Minute)); err != nil || block == nil {
		t.Fatalf("owner target should be fused: block=%+v err=%v", block, err)
	}
	// This test also documents that a single-homework request does not consult
	// the deterministic cooldown; its separate unknown-write guard still does.
	if _, err := s.homeworkFuse(context.Background(), Request{Owner: "owner-b", Course: "C", HomeworkID: hw.ID}, hw, p, when.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
}

func TestGuardHomeworkUnknownReturnsOnlyFormalUnknownGames(t *testing.T) {
	store := bank(t)
	s := New(store, nil, Config{})
	ctx := context.Background()
	roster := &educoder.HomeworkRoster{Levels: []educoder.HomeworkLevel{{ChallengeID: 1, Game: "G1"}, {ChallengeID: 2, Game: "G2"}}}
	progress := &educoder.HomeworkProgress{Challenges: []educoder.ChallengeProgress{{ChallengeID: 1, GameScore: "0", ChallengeScore: "10"}, {ChallengeID: 2, GameScore: "0", ChallengeScore: "10"}}}
	record := questionbank.RunRecord{ID: "formal-unknown", Owner: ownerHash("owner"), Status: "uncertain", Data: []byte(`{"result":{"kind":"objective","status":"uncertain"},"target":{"Course":"C","HomeworkID":9,"Game":"G1"}}`), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.PutRun(ctx, record); err != nil {
		t.Fatal(err)
	}
	blocked, err := s.guardHomeworkUnknown(ctx, Request{Owner: "owner", Course: "C", HomeworkID: 9}, roster, progress)
	if err != nil || !blocked["G1"] || blocked["G2"] {
		t.Fatalf("blocked=%v err=%v", blocked, err)
	}
	// A self-test uncertainty for another level remains auditable but does not
	// block a fresh formal write because no evaluator mutation was attempted.
	if err := store.PutRun(ctx, questionbank.RunRecord{ID: "selftest-unknown", Owner: ownerHash("owner"), Status: "uncertain", Data: []byte(`{"result":{"kind":"programming","status":"uncertain","programming":{"self_test_status":"unknown"}},"target":{"Course":"C","HomeworkID":9,"Game":"G2"}}`), CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	blocked, err = s.guardHomeworkUnknown(ctx, Request{Owner: "owner", Course: "C", HomeworkID: 9}, roster, progress)
	if err != nil || !blocked["G1"] || blocked["G2"] {
		t.Fatalf("self-test uncertainty blocked unrelated level: blocked=%v err=%v", blocked, err)
	}
}
