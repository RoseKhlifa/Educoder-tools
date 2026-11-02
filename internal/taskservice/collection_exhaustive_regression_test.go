package taskservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/questionbank"
)

// This regression deliberately uses only pre-existing APIs and helpers so it
// can be copied unchanged to a release baseline to demonstrate the old caps.
func TestCollectionDefaultExhaustiveTraversal(t *testing.T) {
	h, base, owner := collectionTestSetup(t)
	bank, openErr := questionbank.Open(":memory:")
	if openErr != nil {
		t.Fatal(openErr)
	}
	t.Cleanup(func() { bank.Close() })
	h.Bank = bank
	f := &exhaustiveCollectionFixture{collectionFixture: base, courses: 24, levels: 601}
	for i := 1; i <= f.levels; i++ {
		if err := h.Bank.RememberCollectionTarget(context.Background(), owner, questionbank.CollectionTarget{Course: "C", HomeworkID: 1, Game: fmt.Sprintf("G%04d", i), ChallengeID: int64(i + 100), Shixun: "S"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := h.Scan(context.Background(), f, owner, nil)
	if err != nil || got.LimitReached || got.Courses != 25 || got.Homeworks != 3000 || got.Levels != 601 || got.Collected != 1 || got.Duplicates != 600 || f.hadDeadline {
		t.Fatalf("exhaustive scan got courses=%d homeworks=%d levels=%d collected=%d duplicates=%d limited=%t deadline=%t err=%v", got.Courses, got.Homeworks, got.Levels, got.Collected, got.Duplicates, got.LimitReached, f.hadDeadline, err)
	}
	t.Logf("EXHAUSTIVE courses=%d homeworks=%d levels=%d collected=%d duplicates=%d limited=%t deadline=%t", got.Courses, got.Homeworks, got.Levels, got.Collected, got.Duplicates, got.LimitReached, f.hadDeadline)
}

type exhaustiveCollectionFixture struct {
	*collectionFixture
	courses, levels int
	hadDeadline     bool
}

func (f *exhaustiveCollectionFixture) CollectionCoursesPage(ctx context.Context, status string, page int) ([]educoder.Course, error) {
	if _, ok := ctx.Deadline(); ok {
		f.hadDeadline = true
	}
	if status == "end" {
		// An entirely overlapping first page is NOT end-of-list.
		if page == 1 {
			return []educoder.Course{{Identifier: "C"}}, nil
		}
		if page == 2 {
			return []educoder.Course{{Identifier: "ENDED"}}, nil
		}
		return nil, nil
	}
	first := (page - 1) * 4
	if first >= f.courses {
		return nil, nil
	}
	var out []educoder.Course
	for i := first; i < first+4 && i < f.courses; i++ {
		id := fmt.Sprintf("C%d", i)
		if i == 0 {
			id = "C"
		}
		out = append(out, educoder.Course{ID: int64(i + 1), Identifier: id})
	}
	return out, nil
}
func (f *exhaustiveCollectionFixture) CollectionHomeworksPage(_ context.Context, course string, page int) ([]educoder.Homework, error) {
	if page > 6 {
		return nil, nil
	}
	var out []educoder.Homework
	for i := (page-1)*20 + 1; i <= page*20; i++ {
		hw := educoder.Homework{ID: int64(i)}
		if course == "C" && i == 1 {
			hw.IsShixun = true
			hw.ShixunIdentifier = "S"
			hw.ChallengeCount = f.levels
		}
		out = append(out, hw)
	}
	return out, nil
}
func (f *exhaustiveCollectionFixture) HomeworkProgress(context.Context, string, int64) (*educoder.HomeworkProgress, error) {
	out := &educoder.HomeworkProgress{}
	for i := 1; i <= f.levels; i++ {
		out.Challenges = append(out.Challenges, educoder.ChallengeProgress{ChallengeID: int64(i + 100), ChallengeScore: "2", GameScore: "2"})
	}
	return out, nil
}
func (f *exhaustiveCollectionFixture) CollectionLoadTask(_ context.Context, _ string, _ int64, game string) (*educoder.Task, error) {
	n, err := strconv.Atoi(strings.TrimPrefix(game, "G"))
	if err != nil {
		return nil, errors.New("unknown fixture game")
	}
	b, _ := json.Marshal(f.task)
	var out educoder.Task
	json.Unmarshal(b, &out)
	out.Game.Identifier = game
	out.Challenge.ID = int64(n + 100)
	out.Chooses[0].ChallengeID = out.Challenge.ID
	return &out, nil
}
