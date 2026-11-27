package educoder

import (
	"testing"
	"time"
)

func TestLateSubmissionOpenFollowsTheLateWindow(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	late := Homework{AllowLate: true, EndTime: "2026-11-22 23:59:00", LateTime: "2026-12-06 23:59", Status: []string{"补交中"}}
	if !late.LateSubmissionOpen(now) || late.IsClosed(now) {
		t.Fatal("reopened assignment must be open and late")
	}
	if at, ok := late.LateDeadline(); !ok || at.Format("2006-01-02 15:04") != "2026-12-06 23:59" {
		t.Fatalf("late deadline = %v %v", at, ok)
	}
	statusOnly := Homework{EndTime: "2026-11-22 23:59:00", LateTime: "2026-12-06 23:59", Status: []string{"补交中"}}
	if !statusOnly.LateSubmissionOpen(now) || statusOnly.IsClosed(now) {
		t.Fatal("an explicit 补交中 status alone keeps the assignment open")
	}
	if after := late; after.LateSubmissionOpen(time.Date(2026, 12, 7, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("late window has ended")
	}
	before := Homework{AllowLate: true, EndTime: "2026-12-01 23:59:00", LateTime: "2026-12-06 23:59", Status: []string{"进行中"}}
	if before.LateSubmissionOpen(now) {
		t.Fatal("not late before the ordinary deadline")
	}
}
