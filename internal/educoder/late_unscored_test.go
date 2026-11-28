package educoder

import "testing"

// 2026-11-27: a homework finished after its 11-01 deadline (截止后通关) whose
// four objective levels Educoder accepted as passed but scored 0/25 each.
func TestLevelPassedLateAtZeroIsSettled(t *testing.T) {
	late := Homework{ChallengeCount: 4, EndTime: "2026-11-01 23:59:00", StudentPassedTime: "2026-11-27 21:42:00"}
	onTime := Homework{ChallengeCount: 4, EndTime: "2026-11-01 23:59:00", StudentPassedTime: "2026-10-30 10:00:00"}
	unknown := Homework{ChallengeCount: 4, EndTime: "2026-11-01 23:59:00"}
	zero := ChallengeProgress{ChallengeID: 1, Name: "a", PassedStatus: 1, GameScore: "0.00", ChallengeScore: "25"}
	for _, tc := range []struct {
		name  string
		hw    Homework
		level ChallengeProgress
		want  bool
	}{
		{"late zero", late, zero, true},
		{"full", onTime, ChallengeProgress{PassedStatus: 2, GameScore: "25.00", ChallengeScore: "25"}, true},
		{"late partial", late, ChallengeProgress{PassedStatus: 1, GameScore: "12.5", ChallengeScore: "25"}, false},
		{"zero before the deadline", onTime, zero, false},
		{"zero not passed", late, ChallengeProgress{PassedStatus: 0, GameScore: "0", ChallengeScore: "25"}, false},
		{"no finish time", unknown, zero, false},
	} {
		if got := tc.level.Settled(tc.hw); got != tc.want {
			t.Errorf("%s: Settled = %v", tc.name, got)
		}
	}
	progress := &HomeworkProgress{Challenges: []ChallengeProgress{zero, {ChallengeID: 2, Name: "b", PassedStatus: 1, GameScore: "0.00", ChallengeScore: "25"}, {ChallengeID: 3, Name: "c", PassedStatus: 1, GameScore: "0", ChallengeScore: "25"}, {ChallengeID: 4, Name: "d", PassedStatus: 1, GameScore: "0.00", ChallengeScore: "25"}}}
	if required, completed, valid := RequiredHomeworkCounts(late, progress); !valid || required != 4 || completed != 4 {
		t.Fatalf("late homework counts = %d/%d %v", completed, required, valid)
	}
	if _, completed, _ := RequiredHomeworkCounts(onTime, progress); completed != 0 {
		t.Fatalf("zero levels finished on time counted complete: %d", completed)
	}
}
