package web

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"educoder-cli/internal/educoder"
)

type assignedProgressClient struct {
	*optionalProgressClient
	counts func(context.Context, string, educoder.Homework, *educoder.HomeworkProgress) (int, int, bool)
	enters atomic.Int32
}

func (f *assignedProgressClient) AssignedHomeworkCounts(ctx context.Context, course string, hw educoder.Homework, p *educoder.HomeworkProgress) (int, int, bool) {
	return f.counts(ctx, course, hw, p)
}

func (f *assignedProgressClient) Enter(context.Context, string, int64, string) (*educoder.Task, error) {
	f.enters.Add(1)
	return nil, errors.New("counting must not enter a runtime")
}

// Code-only header metadata is not evidence that objective rows are unassigned.
func TestHomeworkSummaryDoesNotShrinkToCodeOnlyHeader(t *testing.T) {
	for _, done := range []int{3, 1} {
		t.Run(fmt.Sprintf("%d_of_4_passed", done), func(t *testing.T) {
			p := optionalListProgress(4, 4)
			for i := done; i < 4; i++ {
				p.Challenges[i].GameScore = "0"
			}
			var reads, headers atomic.Int32
			fake := &assignedProgressClient{optionalProgressClient: &optionalProgressClient{fakeClient: &fakeClient{}, read: func(ctx context.Context, course string, id int64) (*educoder.HomeworkProgress, error) {
				reads.Add(1)
				deadline, ok := ctx.Deadline()
				if course != "COURSE1" || id != 42 || !ok || time.Until(deadline) > 12*time.Second {
					t.Error("lost bounded assignment context")
				}
				return p, nil
			}}}
			fake.counts = func(context.Context, string, educoder.Homework, *educoder.HomeworkProgress) (int, int, bool) {
				headers.Add(1)
				return done, done, true
			}
			got := serviceHomeworks(context.Background(), fake, "COURSE1", []educoder.Homework{{ID: 42, ChallengeCount: 4}})
			if len(got) != 1 || !got[0].RequiredKnown || got[0].RequiredCount == nil || *got[0].RequiredCount != 4 || got[0].FinishedRequired == nil || *got[0].FinishedRequired != done {
				t.Fatalf("complete assignment must remain %d/4, not a header subset", done)
			}
			if reads.Load() != 1 || headers.Load() != 0 || fake.enters.Load() != 0 {
				t.Fatalf("progress=%d header=%d runtime=%d; want 1/0/0", reads.Load(), headers.Load(), fake.enters.Load())
			}
		})
	}
}
func TestHomeworkSummaryIncompleteScoresCannotUseHeaderAsProof(t *testing.T) {
	fake := &assignedProgressClient{optionalProgressClient: &optionalProgressClient{fakeClient: &fakeClient{}, read: func(context.Context, string, int64) (*educoder.HomeworkProgress, error) {
		return optionalListProgress(2, 2), nil
	}}}
	fake.counts = func(context.Context, string, educoder.Homework, *educoder.HomeworkProgress) (int, int, bool) {
		t.Error("partial score response cannot be legitimized by partial header")
		return 2, 2, true
	}
	got := serviceHomeworks(context.Background(), fake, "COURSE1", []educoder.Homework{{ID: 42, ChallengeCount: 4}})
	if len(got) != 1 || got[0].RequiredKnown || got[0].RequiredCount != nil || got[0].FinishedRequired != nil {
		t.Fatal("incomplete evidence must stay unknown")
	}
}
func TestHomeworkSummaryOptionalFilteringNeedsFullScoreRoster(t *testing.T) {
	for _, required := range []int{0, 2, 4} {
		p := optionalListProgress(4, required)
		fake := &assignedProgressClient{optionalProgressClient: &optionalProgressClient{fakeClient: &fakeClient{}, read: func(context.Context, string, int64) (*educoder.HomeworkProgress, error) { return p, nil }}}
		fake.counts = func(context.Context, string, educoder.Homework, *educoder.HomeworkProgress) (int, int, bool) {
			t.Error("complete score roster does not need header")
			return 0, 0, true
		}
		got := serviceHomeworks(context.Background(), fake, "COURSE1", []educoder.Homework{{ID: 42, ChallengeCount: 4}})
		if required == 0 {
			if len(got) != 0 {
				t.Fatal("complete all-optional assignment remained visible")
			}
		} else if len(got) != 1 || !got[0].RequiredKnown || *got[0].RequiredCount != required || *got[0].FinishedRequired != required {
			t.Fatalf("explicit requirement flags lost: required=%d", required)
		}
	}
}
func TestHomeworkSummaryProgressFailureSkipsHeader(t *testing.T) {
	fake := &assignedProgressClient{optionalProgressClient: &optionalProgressClient{fakeClient: &fakeClient{}, read: func(context.Context, string, int64) (*educoder.HomeworkProgress, error) {
		return nil, errors.New("unavailable")
	}}}
	fake.counts = func(context.Context, string, educoder.Homework, *educoder.HomeworkProgress) (int, int, bool) {
		t.Error("failed progress must not trigger header")
		return 0, 0, false
	}
	got := serviceHomeworks(context.Background(), fake, "COURSE1", []educoder.Homework{{ID: 42, ChallengeCount: 4}})
	if len(got) != 1 || got[0].RequiredKnown {
		t.Fatal("failed progress became completion claim")
	}
}
