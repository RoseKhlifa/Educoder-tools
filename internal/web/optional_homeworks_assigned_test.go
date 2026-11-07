package web

import (
	"context"
	"errors"
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

// Reproduces the two real false-pending shapes without retaining upstream data.
func TestAssignedHomeworkSummaryUsesAssignmentCounts(t *testing.T) {
	for _, assigned := range []int{3, 1} {
		t.Run(string(rune('0'+assigned))+" of 4 assigned", func(t *testing.T) {
			p := optionalListProgress(4, 4)
			for i := assigned; i < 4; i++ {
				p.Challenges[i].GameScore = "0"
			}
			var reads, headers atomic.Int32
			fake := &assignedProgressClient{optionalProgressClient: &optionalProgressClient{
				fakeClient: &fakeClient{},
				read: func(context.Context, string, int64) (*educoder.HomeworkProgress, error) {
					reads.Add(1)
					return p, nil
				},
			}}
			fake.counts = func(ctx context.Context, course string, hw educoder.Homework, got *educoder.HomeworkProgress) (int, int, bool) {
				headers.Add(1)
				deadline, ok := ctx.Deadline()
				if course != "COURSE1" || hw.ID != 42 || got != p || !ok || time.Until(deadline) > 12*time.Second {
					t.Errorf("assignment reader lost progress/context: course=%s hw=%d deadline=%v", course, hw.ID, deadline)
				}
				return assigned, assigned, true
			}
			got := serviceHomeworks(context.Background(), fake, "COURSE1", []educoder.Homework{{ID: 42, ChallengeCount: 4}})
			if len(got) != 1 || !got[0].RequiredKnown || got[0].RequiredCount == nil || *got[0].RequiredCount != assigned || got[0].FinishedRequired == nil || *got[0].FinishedRequired != assigned {
				t.Fatalf("assigned completion must be %d/%d, not catalog 4: %+v", assigned, assigned, got)
			}
			if reads.Load() != 1 || headers.Load() != 1 || fake.enters.Load() != 0 {
				t.Fatalf("unexpected reads/runtime entry: progress=%d header=%d enter=%d", reads.Load(), headers.Load(), fake.enters.Load())
			}
		})
	}
}

func TestAssignedHomeworkSummaryCompletionFastPath(t *testing.T) {
	for _, required := range []int{0, 2} {
		p := optionalListProgress(2, required)
		fake := &assignedProgressClient{optionalProgressClient: &optionalProgressClient{fakeClient: &fakeClient{}, read: func(context.Context, string, int64) (*educoder.HomeworkProgress, error) { return p, nil }}}
		fake.counts = func(context.Context, string, educoder.Homework, *educoder.HomeworkProgress) (int, int, bool) {
			t.Error("complete/all-optional catalog must not fetch a header")
			return 0, 0, false
		}
		got := serviceHomeworks(context.Background(), fake, "COURSE1", []educoder.Homework{{ID: 42, ChallengeCount: 2}})
		if required == 0 {
			if len(got) != 0 {
				t.Fatalf("verified all-optional assignment remained visible: %+v", got)
			}
		} else if len(got) != 1 || !got[0].RequiredKnown || *got[0].RequiredCount != 2 || *got[0].FinishedRequired != 2 {
			t.Fatalf("verified complete catalog changed: %+v", got)
		}
	}
}

func TestAssignedHomeworkSummaryUnknownAndPending(t *testing.T) {
	for _, test := range []struct {
		name                string
		required, completed int
		valid               bool
	}{
		{"genuinely pending", 3, 2, true},
		{"unavailable or ambiguous header", 0, 0, false},
		{"only assigned optional rows", 0, 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := optionalListProgress(4, 4)
			p.Challenges[3].GameScore = "0"
			fake := &assignedProgressClient{optionalProgressClient: &optionalProgressClient{fakeClient: &fakeClient{}, read: func(context.Context, string, int64) (*educoder.HomeworkProgress, error) { return p, nil }}}
			fake.counts = func(context.Context, string, educoder.Homework, *educoder.HomeworkProgress) (int, int, bool) {
				return test.required, test.completed, test.valid
			}
			got := serviceHomeworks(context.Background(), fake, "COURSE1", []educoder.Homework{{ID: 42, ChallengeCount: 4}})
			if test.valid && test.required == 0 {
				if len(got) != 0 {
					t.Fatalf("unassigned zeroes made all-optional assignment pending: %+v", got)
				}
				return
			}
			if len(got) != 1 || got[0].RequiredKnown != test.valid {
				t.Fatalf("invalid evidence became a completion claim: %+v", got)
			}
			if test.valid {
				if *got[0].RequiredCount != test.required || *got[0].FinishedRequired != test.completed {
					t.Fatalf("pending assigned counts lost: %+v", got[0])
				}
			} else if got[0].RequiredCount != nil || got[0].FinishedRequired != nil {
				t.Fatalf("unavailable header fell back to false catalog precision: %+v", got[0])
			}
		})
	}
}

func TestAssignedHomeworkSummaryProgressFailureSkipsHeader(t *testing.T) {
	fake := &assignedProgressClient{optionalProgressClient: &optionalProgressClient{fakeClient: &fakeClient{}, read: func(context.Context, string, int64) (*educoder.HomeworkProgress, error) {
		return nil, errors.New("unavailable")
	}}}
	fake.counts = func(context.Context, string, educoder.Homework, *educoder.HomeworkProgress) (int, int, bool) {
		t.Error("failed progress must not trigger another read")
		return 0, 0, false
	}
	got := serviceHomeworks(context.Background(), fake, "COURSE1", []educoder.Homework{{ID: 42, ChallengeCount: 4}})
	if len(got) != 1 || got[0].RequiredKnown {
		t.Fatalf("progress failure became a completion claim: %+v", got)
	}
}
