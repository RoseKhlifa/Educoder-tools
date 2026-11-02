package taskservice

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"testing"
	"time"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

type incrementalCollectionFixture struct {
	*collectionFixture
	tasks   map[string]*educoder.Task
	loads   map[string]int
	fail    map[string]bool
	locates int
	onLoad  func(string)
}

func (f *incrementalCollectionFixture) CollectionLocate(_ context.Context, _ string, _ educoder.Homework, _ *educoder.HomeworkProgress) (educoder.CollectionLocationResult, error) {
	f.locates++
	games := make([]string, 0, len(f.tasks))
	for game := range f.tasks {
		games = append(games, game)
	}
	sort.Strings(games)
	out := educoder.CollectionLocationResult{Reasons: map[string]int{}}
	for _, game := range games {
		t := f.tasks[game]
		out.Targets = append(out.Targets, educoder.CollectionLocator{Game: game, ChallengeID: t.Challenge.ID, MyShixunIdentifier: t.MyShixun.Identifier, Position: t.Challenge.Position, Name: t.Challenge.Subject})
	}
	return out, nil
}

func (f *incrementalCollectionFixture) CollectionLoadTask(ctx context.Context, _ string, _ int64, game string) (*educoder.Task, error) {
	f.loads[game]++
	if f.onLoad != nil {
		f.onLoad(game)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.fail[game] {
		return nil, errors.New("fixture task read failed")
	}
	if f.tasks[game] == nil {
		return nil, errors.New("missing fixture game")
	}
	body, _ := json.Marshal(f.tasks[game])
	var task educoder.Task
	_ = json.Unmarshal(body, &task)
	return &task, nil
}

func incrementalCollectionSetup(t *testing.T) (*Collector, *incrementalCollectionFixture, string) {
	h, base, owner := collectionTestSetup(t)
	data, _ := json.Marshal(base.task)
	var second educoder.Task
	_ = json.Unmarshal(data, &second)
	second.Game.ID, second.Game.Identifier = 2, "G2"
	second.Challenge.ID, second.Challenge.Subject = 19, "second challenge"
	second.Chooses[0].ChallengeID, second.Chooses[0].ChallengeChooseID = 19, 20
	second.Chooses[0].Subject = "second question"
	base.progress.Challenges = append(base.progress.Challenges, educoder.ChallengeProgress{ChallengeID: 19, ChallengeScore: "2", GameScore: "2"})
	return h, &incrementalCollectionFixture{collectionFixture: base, tasks: map[string]*educoder.Task{"G": base.task, "G2": &second}, loads: map[string]int{}, fail: map[string]bool{}}, owner
}

func TestIncrementalCollectionReusesVerifiedReadsUntilDeepAuditIsDue(t *testing.T) {
	h, f, owner := incrementalCollectionSetup(t)
	now := time.Now()
	h.Now = func() time.Time { return now }
	first, err := h.Scan(context.Background(), f, owner, nil)
	if err != nil || first.Collected != 2 || f.loads["G"] != 2 || f.loads["G2"] != 2 {
		t.Fatalf("first=%+v loads=%v err=%v", first, f.loads, err)
	}
	for i := 0; i < 3; i++ {
		now = now.Add(time.Hour)
		got, err := h.Scan(context.Background(), f, owner, nil)
		if err != nil || got.ReusedLevels != 2 || got.ReusedQuestions != 2 || got.Duplicates != 2 || got.ObjectiveAnswersRead != 0 || f.locates != 1 || f.loads["G"] != 2 || f.loads["G2"] != 2 {
			t.Fatalf("reuse=%+v reads=%v locates=%d err=%v", got, f.loads, f.locates, err)
		}
	}
	// Reuse must not refresh verified_at or postpone deep audits indefinitely.
	f.tasks["G"].Chooses[0].Subject = "updated question without a score change"
	now = now.Add(21 * time.Hour)
	got, err := h.Scan(context.Background(), f, owner, nil)
	if err != nil || got.Collected != 1 || got.ReusedLevels != 0 || f.loads["G"] != 4 || f.loads["G2"] != 4 {
		t.Fatalf("audit=%+v reads=%v err=%v", got, f.loads, err)
	}
}

type collectionWithoutLocator struct{ CollectionReader }

func TestIncrementalCollectionDoesNotCutNextGameDiscoveryUsingStaleSavedSeeds(t *testing.T) {
	h, f, owner := incrementalCollectionSetup(t)
	f.fail["G2"] = true
	first, err := h.Scan(context.Background(), f, owner, nil)
	if err != nil || first.Collected != 1 {
		t.Fatalf("first=%+v %v", first, err)
	}
	next := "NEW"
	f.tasks["G"].NextGame = &next
	newTask := *f.tasks["G2"]
	newTask.Game.Identifier = "NEW"
	f.tasks["NEW"] = &newTask
	wrong := *f.tasks["G2"]
	wrong.Game.Identifier, wrong.Challenge.ID = "OLD", 999
	f.tasks["OLD"] = &wrong
	if err := h.Bank.RememberCollectionTarget(context.Background(), owner, questionbank.CollectionTarget{Course: "C", HomeworkID: 1, Game: "OLD", ChallengeID: 19, Shixun: "S"}); err != nil {
		t.Fatal(err)
	}
	got, err := h.Scan(context.Background(), collectionWithoutLocator{f}, owner, nil)
	if err != nil || got.Collected != 1 || f.loads["NEW"] != 2 {
		t.Fatalf("linked new game missed: %+v reads=%v err=%v", got, f.loads, err)
	}
}

func TestIncrementalCollectionFindsNewPassAndChangedEvaluationImmediately(t *testing.T) {
	h, f, owner := incrementalCollectionSetup(t)
	f.progress.Challenges[1].GameScore = "0"
	first, err := h.Scan(context.Background(), f, owner, nil)
	if err != nil || first.Collected != 1 || f.loads["G2"] != 0 {
		t.Fatalf("first=%+v %v", first, err)
	}
	f.progress.Challenges[1].GameScore = "2"
	got, err := h.Scan(context.Background(), f, owner, nil)
	if err != nil || got.Collected != 1 || got.ReusedLevels != 1 || f.loads["G"] != 2 || f.loads["G2"] != 2 {
		t.Fatalf("new pass=%+v reads=%v err=%v", got, f.loads, err)
	}
	f.progress.Challenges[0].EvaluateCount++
	got, err = h.Scan(context.Background(), f, owner, nil)
	if err != nil || got.ReusedLevels != 1 || f.loads["G"] != 4 || f.loads["G2"] != 2 {
		t.Fatalf("changed evaluation=%+v reads=%v err=%v", got, f.loads, err)
	}
}

func TestIncrementalCollectionRetriesMissingAnswersAndPreservesCanceledPartialWork(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "read failure", true: "cancellation"}[canceled], func(t *testing.T) {
			h, f, owner := incrementalCollectionSetup(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if canceled {
				f.onLoad = func(game string) {
					if game == "G2" {
						cancel()
					}
				}
			} else {
				f.fail["G2"] = true
			}
			first, err := h.Scan(ctx, f, owner, nil)
			if first.Collected != 1 || first.ScanExhausted || canceled && !errors.Is(err, context.Canceled) {
				t.Fatalf("first=%+v err=%v", first, err)
			}
			f.fail["G2"], f.onLoad = false, nil
			got, err := h.Scan(context.Background(), f, owner, nil)
			if err != nil || got.Collected != 1 || got.ReusedLevels != 1 || f.loads["G"] != 2 || !got.ScanExhausted {
				t.Fatalf("resume=%+v reads=%v err=%v", got, f.loads, err)
			}
		})
	}
}

func TestIncrementalCollectionNeverSharesReceiptsAcrossUsersOrTrustsTruncatedRoster(t *testing.T) {
	h, f, owner := incrementalCollectionSetup(t)
	if _, err := h.Scan(context.Background(), f, owner, nil); err != nil {
		t.Fatal(err)
	}
	other, err := h.Scan(context.Background(), f, problem.HashContent("second account"), nil)
	if err != nil || other.ReusedLevels != 0 || f.loads["G"] != 4 {
		t.Fatalf("cross owner=%+v %v", other, err)
	}
	f.progress.Challenges = f.progress.Challenges[:1]
	// The fixture's homework count follows progress, so force the fingerprint
	// to differ through the roster and ensure old receipts are not accepted.
	got, err := h.Scan(context.Background(), f, owner, nil)
	if err != nil || got.ReusedLevels != 0 || f.loads["G"] != 6 {
		t.Fatalf("roster changed=%+v reads=%v err=%v", got, f.loads, err)
	}
}
