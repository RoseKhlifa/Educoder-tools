package taskservice

import (
	"context"
	"testing"
)

// Mirrors real GET task responses: 32 full-score answer sheets returned null
// choose_correct_num/result because correctness visibility is disabled.
func TestCollectionHiddenCorrectnessRequiresFullScoreAndCompletePersistedSheet(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*collectionFixture)
		want   int
	}{
		{"hidden exact full", func(*collectionFixture) {}, 1},
		{"partial", func(f *collectionFixture) { f.progress.Challenges[0].GameScore = "1.99" }, 0},
		{"unsubmitted", func(f *collectionFixture) { f.task.ChooseTestCases.HadSubmit = false }, 0},
		{"not all submitted", func(f *collectionFixture) { f.task.ChooseTestCases.HadAllSubmit = false }, 0},
		{"actual missing", func(f *collectionFixture) { f.task.ChooseTestCases.TestSets[0].ActualOutput = nil }, 0},
		{"explicit false", func(f *collectionFixture) { no := false; f.task.ChooseTestCases.TestSets[0].Result = &no }, 0},
		{"explicit count conflict", func(f *collectionFixture) { zero := 0; f.task.ChooseTestCases.Correct = &zero }, 0},
		{"question cross challenge", func(f *collectionFixture) { f.task.Chooses[0].ChallengeID = 77 }, 0},
		{"changing answer sheet", func(f *collectionFixture) { f.changeTask = true }, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, f, owner := collectionTestSetup(t)
			f.task.ChooseTestCases.Correct = nil
			f.task.ChooseTestCases.TestSets[0].Result = nil
			f.task.ChooseTestCases.HadSubmit = true
			f.task.ChooseTestCases.HadAllSubmit = true
			tc.mutate(f)
			s, e := h.Scan(context.Background(), f, owner, nil)
			if e != nil || s.Collected != tc.want {
				t.Fatalf("%+v %v", s, e)
			}
		})
	}
}
