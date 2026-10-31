package educoder

import (
	"encoding/json"
	"testing"
	"time"
)

func TestHomeworkIsClosedExplicitStatus(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, homeworkLocation)
	if !(Homework{Status: []string{"已截止"}, EndTime: "2099-01-01 00:00:00"}).IsClosed(now) {
		t.Fatal("explicit 已截止 status must be closed even when date is future")
	}
	if !(Homework{Status: []string{"closed"}}).IsClosed(now) {
		t.Fatal("closed status must be closed")
	}
}

func TestHomeworkIsClosedEndTimeInShanghai(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	if !(Homework{EndTime: "2026-10-23 22:59:00"}).IsClosed(now) {
		t.Fatal("past local end_time must be closed")
	}
	if (Homework{EndTime: "2026-11-08 22:59:00"}).IsClosed(now) {
		t.Fatal("future local end_time must remain open")
	}
}

func TestHomeworkIsClosedNumericAndJSONNumber(t *testing.T) {
	now := time.Unix(1793448000, 0) // 2026-10-31 12:00 UTC
	if !(Homework{EndTime: int64(1793185140000)}).IsClosed(now) {
		t.Fatal("millisecond deadline must be recognized")
	}
	if !(Homework{EndTime: json.Number("1793185140")}).IsClosed(now) {
		t.Fatal("second deadline must be recognized")
	}
	if (Homework{EndTime: "1794399540000"}).IsClosed(now) {
		t.Fatal("future numeric string must remain open")
	}
}

func TestHomeworkIsClosedAllowsLateUntilExtension(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, homeworkLocation)
	if (Homework{AllowLate: true, EndTime: "2026-10-23 22:59:00"}).IsClosed(now) {
		t.Fatal("allow_late without extension must not be closed by ordinary deadline")
	}
	if (Homework{AllowLate: true, EndTime: "2026-10-23 22:59:00", LateTime: "2026-11-08 22:59:00"}).IsClosed(now) {
		t.Fatal("future late deadline must remain open")
	}
	if !(Homework{AllowLate: true, EndTime: "2026-10-23 22:59:00", LateTime: "2026-10-30 22:59:00"}).IsClosed(now) {
		t.Fatal("past late deadline must close")
	}
}

func TestHomeworkIsClosedMissingDateStaysOpen(t *testing.T) {
	if (Homework{Status: []string{"提交后"}, EndTime: "--"}).IsClosed(time.Now()) {
		t.Fatal("missing deadline is not evidence of closure")
	}
}

func TestOpenHomeworksFiltersOnlyClosedEntries(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, homeworkLocation)
	input := []Homework{
		{ID: 1, Status: []string{"已截止"}, EndTime: "2099-01-01 00:00:00"},
		{ID: 2, Status: []string{"提交后"}, EndTime: "2026-11-08 22:59:00"},
		{ID: 3, Status: []string{"提交后"}, EndTime: "--"},
	}
	open := OpenHomeworks(input, now)
	if len(open) != 2 || open[0].ID != 2 || open[1].ID != 3 {
		t.Fatalf("open homework filter = %#v", open)
	}
	if len(input) != 3 {
		t.Fatal("open homework filter mutated source slice")
	}
}
