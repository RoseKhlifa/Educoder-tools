package web

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"educoder-cli/internal/jobs"
)

func liveJobProgress() *jobs.BatchProgress {
	started := time.Date(2026, 11, 20, 2, 0, 0, 0, time.UTC)
	updated := started.Add(90 * time.Second)
	return &jobs.BatchProgress{
		TotalHomeworks: 3, CompletedHomeworks: 1, SkippedHomeworks: 1, NeedsServiceHomeworks: 0,
		CurrentCourse: "COURSE", CurrentHomeworkID: 2, CurrentHomeworkTitle: "作业二",
		TotalLevels: 4, CompletedLevels: 2, CurrentPosition: 3, CurrentTitle: "第三关", Stage: "selftesting",
		Homeworks: []jobs.HomeworkProgress{
			{Course: "COURSE", CourseName: "课程", HomeworkID: 1, Title: "作业一", Status: "skipped", ReasonCode: "already_passed", TotalLevels: 2, CompletedLevels: 2, SkippedLevels: 2},
			{Course: "COURSE", CourseName: "课程", HomeworkID: 2, Title: "作业二", Status: "running", TotalLevels: 4, CompletedLevels: 2, CurrentPosition: 3, CurrentTitle: "第三关", Stage: "selftesting", StartedAt: &started, UpdatedAt: &updated,
				Levels: []jobs.LevelProgress{{Position: 1, Title: "第一关", Status: "passed"}, {Position: 2, Title: "第二关", Status: "passed"}, {Position: 3, Title: "第三关", Status: "running"}}},
			{Course: "COURSE", CourseName: "课程", HomeworkID: 3, Title: "作业三", Status: "waiting"},
		},
	}
}

// The live snapshot is what the running-job pages render. The list projection
// stays summary-only; only a single job's detail carries its levels.
func TestConsumerLiveProgressProjectsAssignmentsAndLevels(t *testing.T) {
	job := jobs.Job{ID: "live", Status: jobs.Running, Request: jobs.Request{Mode: "submit", Scope: "course", Course: "COURSE"}, Progress: liveJobProgress()}

	list := toConsumerJob(job)
	if list.Progress == nil || len(list.Progress.Homeworks) != 3 {
		t.Fatalf("list projection lost the assignment list: %+v", list.Progress)
	}
	if list.Progress.Stage != "selftesting" || list.Progress.CurrentPosition != 3 || list.Progress.CurrentLevel != "第三关" {
		t.Fatalf("list projection lost the current stage: %+v", list.Progress)
	}
	if list.Progress.SkippedHomeworks != 1 {
		t.Fatalf("list projection lost the skipped counter: %+v", list.Progress)
	}
	for _, item := range list.Progress.Homeworks {
		if item.Levels != nil {
			t.Fatalf("list projection carries nested levels: %+v", item)
		}
	}

	detail := toConsumerDetail(job)
	if len(detail.Progress.Homeworks) != 3 {
		t.Fatalf("detail projection lost assignments: %+v", detail.Progress)
	}
	first, running, last := detail.Progress.Homeworks[0], detail.Progress.Homeworks[1], detail.Progress.Homeworks[2]
	if first.Status != "completed" || first.Reason != "already_passed" {
		t.Fatalf("already-passed assignment projected wrong: %+v", first)
	}
	if running.Status != "processing" || running.Stage != "selftesting" || running.CurrentPosition != 3 || running.CurrentTitle != "第三关" {
		t.Fatalf("running assignment projected wrong: %+v", running)
	}
	if running.TotalLevels != 4 || running.CompletedLevels != 2 {
		t.Fatalf("running assignment lost its level counters: %+v", running)
	}
	if running.StartedAt == nil || running.UpdatedAt == nil {
		t.Fatalf("running assignment lost its timestamps: %+v", running)
	}
	if len(running.Levels) != 3 || running.Levels[2].Status != "processing" || running.Levels[2].Position != 3 {
		t.Fatalf("level detail projected wrong: %+v", running.Levels)
	}
	if last.Status != "waiting" {
		t.Fatalf("planned assignment projected wrong: %+v", last)
	}

	// A settled job publishes no live tree: the stored result owns the outcome.
	settled := job
	settled.Status = jobs.Succeeded
	settled.Progress = &jobs.BatchProgress{TotalHomeworks: 3, CompletedHomeworks: 3, TotalLevels: 4, CompletedLevels: 4}
	if got := toConsumerDetail(settled); got.Progress == nil || got.Progress.Homeworks != nil {
		t.Fatalf("settled job published a live tree: %+v", got.Progress)
	}

	body, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	assertConsumerBoundary(t, string(body))
	if strings.Contains(string(body), "COURSE") && !strings.Contains(string(body), `"course":"COURSE"`) {
		t.Fatalf("unexpected identifier shape in consumer body: %s", body)
	}
}

// Only vetted reason codes may be repeated to a user: a new internal state
// must arrive as the generic attention code, never as an unexplained string.
func TestConsumerReasonUsesAllowlistAndFriendlyResourceWording(t *testing.T) {
	for _, tc := range []struct{ result, want string }{
		{`{"status":"needs_service","reason_code":"resource_unavailable"}`, "platform_busy"},
		{`{"status":"needs_service","reason_code":"deadline_passed"}`, "deadline_passed"},
		{`{"status":"canceled","reason_code":"canceled","stop_reason":"time_limit"}`, "stopped"},
		{`{"status":"needs_service","reason_code":"internal_future_state"}`, "service_followup"},
		{`{"status":"needs_service","homeworks":[{"title":"作业","status":"needs_service","reason_code":"selftest_failed"}]}`, "answer_unavailable"},
		{`{"status":"needs_service","homeworks":[{"title":"作业","status":"needs_service","reason_code":"secret-ish code"}]}`, "service_followup"},
		{`{"status":"needs_service","reason_code":"model_provider_payment_required"}`, "answer_unavailable"},
	} {
		got := toConsumerJob(jobs.Job{Status: jobs.Failed, Result: json.RawMessage(tc.result)})
		if got.Result.Reason != tc.want {
			t.Fatalf("result %s produced reason %q, want %q", tc.result, got.Result.Reason, tc.want)
		}
	}
	// No internal code is ever published verbatim, whatever the result says.
	for _, code := range []string{"resource_unavailable", "model_error", "selftest_failed", "submission_uncertain", "unchanged_failure_cooldown"} {
		got := toConsumerDetail(jobs.Job{Status: jobs.Failed, Result: json.RawMessage(`{"status":"needs_service","reason_code":"` + code + `","homeworks":[{"title":"作业","status":"needs_service","reason_code":"` + code + `"}]}`)})
		body, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), code) {
			t.Fatalf("internal reason %q was published verbatim: %s", code, body)
		}
	}
	// A completed job explains nothing: there is nothing to explain.
	if got := toConsumerJob(jobs.Job{Status: jobs.Succeeded, Result: json.RawMessage(`{"status":"passed","reason_code":"resource_unavailable"}`)}); got.Result.Reason != "" {
		t.Fatalf("a passed job published a stop reason: %q", got.Result.Reason)
	}
}

// auto_retry is rendered as a promise to the user, so an implausible schedule
// is dropped rather than displayed.
func TestConsumerAutoRetryIsValidated(t *testing.T) {
	old := autoRetryNow
	autoRetryNow = func() time.Time { return time.Date(2026, 11, 20, 3, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { autoRetryNow = old })
	good := toConsumerJob(jobs.Job{Status: jobs.Failed, Result: json.RawMessage(`{"status":"needs_service","reason_code":"resource_unavailable","auto_retry":{"next_at":"2026-11-20T03:04:05Z","attempt":2,"max":5}}`)})
	if good.Result.AutoRetry == nil || good.Result.AutoRetry.Attempt != 2 || good.Result.AutoRetry.Max != 5 {
		t.Fatalf("valid auto retry dropped: %+v", good.Result.AutoRetry)
	}
	stale := toConsumerJob(jobs.Job{Status: jobs.Failed, Result: json.RawMessage(`{"auto_retry":{"next_at":"2026-11-19T03:04:05Z","attempt":1,"max":3}}`)})
	if stale.Result.AutoRetry != nil {
		t.Fatalf("expired retry promise still shown: %+v", stale.Result.AutoRetry)
	}
	if !good.Result.AutoRetry.NextAt.Equal(time.Date(2026, 11, 20, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("auto retry time changed: %v", good.Result.AutoRetry.NextAt)
	}
	for _, raw := range []string{
		`{"auto_retry":{"attempt":1,"max":3}}`,
		`{"auto_retry":{"next_at":"2026-11-20T03:04:05Z","attempt":9,"max":3}}`,
		`{"auto_retry":{"next_at":"2026-11-20T03:04:05Z","attempt":-1}}`,
		`{"auto_retry":{"next_at":"not-a-time"}}`,
		`{"auto_retry":"soon"}`,
	} {
		got := toConsumerJob(jobs.Job{Status: jobs.Failed, Result: json.RawMessage(raw)})
		if got.Result.AutoRetry != nil {
			t.Fatalf("implausible auto retry published for %s: %+v", raw, got.Result.AutoRetry)
		}
	}
}

// Bounds are enforced in the projection as well: a stored result from an older
// or unexpected run cannot produce an unbounded response.
func TestConsumerProjectionBoundsAssignmentAndLevelLists(t *testing.T) {
	rows := make([]string, 0, 160)
	for i := 0; i < 160; i++ {
		levels := make([]string, 0, 150)
		for n := 0; n < 150; n++ {
			levels = append(levels, `{"position":1,"title":"关卡","status":"passed"}`)
		}
		rows = append(rows, `{"title":"作业","status":"needs_service","reason_code":"selftest_failed","result":{"total_levels":3,"completed_levels":1,"levels":[`+strings.Join(levels, ",")+`]}}`)
	}
	raw := `{"status":"needs_service","homeworks":[` + strings.Join(rows, ",") + `]}`
	got := toConsumerDetail(jobs.Job{Status: jobs.Failed, Result: json.RawMessage(raw)})
	if len(got.Result.Homeworks) != 100 {
		t.Fatalf("assignment list not bounded: %d", len(got.Result.Homeworks))
	}
	if len(got.Result.Homeworks[0].Levels) != 100 {
		t.Fatalf("level list not bounded: %d", len(got.Result.Homeworks[0].Levels))
	}

	long := strings.Repeat("标", 900)
	titled := toConsumerDetail(jobs.Job{Status: jobs.Failed, Result: json.RawMessage(`{"status":"needs_service","homeworks":[{"title":"` + long + `","status":"needs_service"}]}`)})
	if runes := []rune(titled.Result.Homeworks[0].Title); len(runes) > 201 {
		t.Fatalf("title not truncated: %d runes", len(runes))
	}
}

// The operator projection reuses the same shape so one component renders both,
// while keeping the exact reason code operators need.
func TestAdminLiveProgressProjectionMirrorsConsumerShape(t *testing.T) {
	job := jobs.Job{ID: "live", Status: jobs.Running, Request: jobs.Request{Mode: "submit", Scope: "course", Course: "COURSE"}, Progress: liveJobProgress()}
	list := adminJobProgress(job, false)
	if list == nil || len(list.Homeworks) != 3 || list.Stage != "selftesting" {
		t.Fatalf("admin list projection is wrong: %+v", list)
	}
	for _, item := range list.Homeworks {
		if item.Levels != nil {
			t.Fatalf("admin list projection carries levels: %+v", item)
		}
	}
	detail := adminJobProgress(job, true)
	if detail == nil || len(detail.Homeworks[1].Levels) != 3 {
		t.Fatalf("admin detail projection lost levels: %+v", detail)
	}
	if detail.Homeworks[0].Reason != "already_passed" {
		t.Fatalf("admin projection lost the exact reason code: %+v", detail.Homeworks[0])
	}
	// A settled job has no live snapshot; its stored result drives the view.
	settled := job
	settled.Status = jobs.Failed
	if got := adminJobProgress(settled, true); got != nil {
		t.Fatalf("settled job published live admin progress: %+v", got)
	}
}
