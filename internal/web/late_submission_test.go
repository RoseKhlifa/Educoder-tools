package web

import (
	"encoding/json"
	"testing"
	"time"

	"educoder-cli/internal/educoder"
)

// The shape Educoder returned on 2026-11-27 for an assignment the teacher
// reopened: the ordinary deadline has passed, the late deadline is ahead.
func lateHomework(t *testing.T, late time.Time) educoder.Homework {
	t.Helper()
	raw := `{"homework_id":4231211,"name":"软件项目质量管理","allow_late":true,"end_time":"2026-11-22 23:59:00","late_time":"` + late.In(time.FixedZone("CST", 8*3600)).Format("2006-01-02 15:04") + `","status":["补交中"],"time_status":2}`
	var homework educoder.Homework
	if err := json.Unmarshal([]byte(raw), &homework); err != nil {
		t.Fatal(err)
	}
	homework.Kind = "shixun"
	return homework
}

func TestReopenedAssignmentIsShownAsLateWithItsLateDeadline(t *testing.T) {
	late := time.Now().Add(9 * 24 * time.Hour).Truncate(time.Minute)
	dto := toHomeworkDTO(lateHomework(t, late))
	want := late.In(time.FixedZone("CST", 8*3600)).Format("2006-01-02 15:04")
	if !dto.LateSubmission || dto.Deadline != want {
		t.Fatalf("late assignment: late=%v deadline=%q want %q", dto.LateSubmission, dto.Deadline, want)
	}
}

func TestOrdinaryAndExpiredAssignmentsAreNotLate(t *testing.T) {
	expired := lateHomework(t, time.Now().Add(-time.Hour))
	if dto := toHomeworkDTO(expired); dto.LateSubmission || dto.Deadline != "2026-11-22 23:59:00" {
		t.Fatalf("expired late window still shown as late: %+v", dto)
	}
	open := educoder.Homework{ID: 1, Name: "x", Kind: "shixun", AllowLate: true, EndTime: time.Now().Add(48 * time.Hour).Format("2006-01-02 15:04:05"), Status: []string{"进行中"}}
	if dto := toHomeworkDTO(open); dto.LateSubmission {
		t.Fatalf("an assignment before its ordinary deadline is not late: %+v", dto)
	}
}
