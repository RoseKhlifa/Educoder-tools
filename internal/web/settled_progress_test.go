package web

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"educoder-cli/internal/educoder"
)

func settledTestProgress(levels int, full int) *educoder.HomeworkProgress {
	p := &educoder.HomeworkProgress{}
	for i := 1; i <= levels; i++ {
		score := "0"
		if i <= full {
			score = "16"
		}
		p.Challenges = append(p.Challenges, educoder.ChallengeProgress{ChallengeID: int64(i), Name: fmt.Sprintf("关卡%d", i), RequiredStatus: "required", GameScore: score, ChallengeScore: "16"})
	}
	return p
}

// Finished and all-optional assignments are settled: the course list reuses
// their last complete read for a bounded time. Unfinished assignments and
// failed reads are read on every refresh exactly as before.
func TestCourseListReusesOnlySettledAssignmentProgress(t *testing.T) {
	var mu sync.Mutex
	reads := map[int64]int{}
	levels := map[int64]int{1: 2, 2: 2, 3: 1, 4: 1}
	fake := &optionalProgressClient{fakeClient: &fakeClient{}, read: func(_ context.Context, _ string, id int64) (*educoder.HomeworkProgress, error) {
		mu.Lock()
		reads[id]++
		n := levels[id]
		mu.Unlock()
		switch id {
		case 1: // every required level full-scored
			return settledTestProgress(n, n), nil
		case 2: // one required level still open
			return settledTestProgress(n, 1), nil
		case 3: // nothing required
			return optionalListProgress(n, 0), nil
		default:
			return nil, errors.New("read failed")
		}
	}}
	list := func() []educoder.Homework {
		mu.Lock()
		defer mu.Unlock()
		return []educoder.Homework{{ID: 1, ChallengeCount: levels[1]}, {ID: 2, ChallengeCount: levels[2]}, {ID: 3, ChallengeCount: levels[3]}, {ID: 4, ChallengeCount: levels[4]}}
	}
	now := time.Unix(1_800_000_000, 0)
	cache := newSettledProgressCache()
	cache.now = func() time.Time { return now }
	resetReads := func() map[int64]int {
		mu.Lock()
		defer mu.Unlock()
		got := reads
		reads = map[int64]int{}
		return got
	}
	describe := func(result []homeworkDTO) string {
		out := ""
		for _, dto := range result {
			if dto.RequiredKnown {
				out += fmt.Sprintf("%d:%d/%d ", dto.ID, *dto.FinishedRequired, *dto.RequiredCount)
			} else {
				out += fmt.Sprintf("%d:? ", dto.ID)
			}
		}
		return out
	}

	first := serviceHomeworksSettled(context.Background(), fake, "COURSE1", list(), cache, "owner-a")
	if got := resetReads(); got[1] != 1 || got[2] != 1 || got[3] != 1 || got[4] != 1 {
		t.Fatalf("first refresh reads=%v", got)
	}
	want := "1:2/2 2:1/2 4:? "
	if describe(first) != want {
		t.Fatalf("first refresh=%q want %q", describe(first), want)
	}

	second := serviceHomeworksSettled(context.Background(), fake, "COURSE1", list(), cache, "owner-a")
	if got := resetReads(); got[1] != 0 || got[3] != 0 || got[2] != 1 || got[4] != 1 {
		t.Fatalf("settled assignments were read again or open ones skipped: %v", got)
	}
	if describe(second) != want {
		t.Fatalf("reused refresh=%q want %q", describe(second), want)
	}

	// Another account never sees this account's settled state.
	serviceHomeworksSettled(context.Background(), fake, "COURSE1", list(), cache, "owner-b")
	if got := resetReads(); got[1] != 1 || got[3] != 1 {
		t.Fatalf("settled state leaked across accounts: %v", got)
	}
	// Another course is another key.
	serviceHomeworksSettled(context.Background(), fake, "COURSE2", list(), cache, "owner-a")
	if got := resetReads(); got[1] != 1 || got[3] != 1 {
		t.Fatalf("settled state leaked across courses: %v", got)
	}

	// A changed level count (a teacher added a level) is read again at once.
	mu.Lock()
	levels[1] = 3
	mu.Unlock()
	serviceHomeworksSettled(context.Background(), fake, "COURSE1", list(), cache, "owner-a")
	if got := resetReads(); got[1] != 1 || got[3] != 0 {
		t.Fatalf("changed roster not re-read: %v", got)
	}

	// After the bound every assignment is read again.
	now = now.Add(settledProgressTTL)
	serviceHomeworksSettled(context.Background(), fake, "COURSE1", list(), cache, "owner-a")
	if got := resetReads(); got[1] != 1 || got[3] != 1 {
		t.Fatalf("expired entries reused: %v", got)
	}

	// Without an owner nothing is reused.
	serviceHomeworksSettled(context.Background(), fake, "COURSE1", list(), cache, "")
	serviceHomeworksSettled(context.Background(), fake, "COURSE1", list(), cache, "")
	if got := resetReads(); got[1] != 2 || got[3] != 2 {
		t.Fatalf("anonymous refresh reused state: %v", got)
	}
}

func TestSettledProgressCacheStoresOnlySettledStates(t *testing.T) {
	cache := newSettledProgressCache()
	key := settledProgressKey{owner: "o", course: "C", homework: 1, challenges: 3}
	cache.remember(key, 3, 2)
	if _, ok := cache.lookup(key); ok {
		t.Fatal("an assignment with open required levels was cached")
	}
	cache.remember(key, 3, 3)
	if required, ok := cache.lookup(key); !ok || required != 3 {
		t.Fatalf("finished assignment not cached: %d %v", required, ok)
	}
	var none *settledProgressCache
	none.remember(key, 1, 1)
	if _, ok := none.lookup(key); ok {
		t.Fatal("nil cache returned an entry")
	}
}
