package web

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"educoder-cli/internal/jobs"
)

func TestAdminJobContextNestedFailureTargetsAndZeroScore(t *testing.T) {
	// This mirrors the reported course task: nine assignments are full-score,
	// while the sixth level of the last assignment has a zero maximum.
	job := jobs.Job{Status: jobs.Failed, ErrorCode: "SERVICE_REQUIRED", Message: "部分作业需要服务人员处理", Request: jobs.Request{Course: "COURSE", Scope: "course", Mode: "submit"}, Result: json.RawMessage(`{
		"status":"needs_service","reason_code":"service_required","total_homeworks":10,"completed_homeworks":9,
		"homeworks":[
			{"course":"COURSE","course_name":"数据结构课程","homework_id":11,"title":"已通过的作业","status":"skipped"},
			{"course":"COURSE","course_name":"数据结构课程","homework_id":12,"title":"客观题测试-第1章绪论 ","status":"needs_service","reason_code":"verification_unconfirmed","result":{
				"status":"verification_unconfirmed","reason_code":"verification_unconfirmed","total_levels":10,"completed_levels":5,"current_position":6,"current_title":"绪论客观题测试（六）",
				"diagnostics":["目标关卡得分或正满分分值缺失/无效，满分尚未确认"],
				"levels":[{"position":1,"title":"绪论客观题测试（一）","status":"skipped","score":"16.00","max_score":"16.0"},{"position":6,"title":"绪论客观题测试（六）","status":"verification_unconfirmed","reason_code":"verification_unconfirmed","score":"0.00","max_score":"0.0"},{"position":7,"title":"绪论客观题测试（七）","status":"planned"}]
			}}
		]}`)}
	out := deriveJobContext(job)
	if out.User != nil || out.Course.Name != "数据结构课程" || out.Homework.Name != "客观题测试-第1章绪论" || out.Homework.ID != 12 || out.Level.Title != "绪论客观题测试（六）" || out.Level.Position != 6 || out.Level.Score != "0.00" || out.Level.MaxScore != "0.0" {
		t.Fatalf("wrong human target/zero-score projection: %+v", out)
	}
	if out.Summary.ReasonCode != "verification_unconfirmed" || len(out.Summary.Diagnostics) != 1 || len(out.Summary.AttentionHomeworks) != 1 {
		t.Fatalf("nested reason must take priority over generic service_required: %+v", out.Summary)
	}
	item := out.Summary.AttentionHomeworks[0]
	if item.Name != out.Homework.Name || item.CourseName != out.Course.Name || item.LevelTitle != out.Level.Title || item.LevelPosition != 6 || item.ReasonCode != "verification_unconfirmed" {
		t.Fatalf("attention row must identify the same failed level: %+v", item)
	}
	if !strings.Contains(out.Summary.ProgressLabel, "9 / 10 份作业") || !strings.Contains(out.Summary.ProgressLabel, "5 / 10") {
		t.Fatalf("course and homework counters must both be preserved: %q", out.Summary.ProgressLabel)
	}
}

func TestAdminJobContextFailureWinsOverLatestSuccessfulProgress(t *testing.T) {
	job := jobs.Job{Request: jobs.Request{Scope: "all"}, Progress: &jobs.BatchProgress{CurrentCourse: "LATER", CurrentHomeworkID: 22, CurrentHomeworkTitle: "后面完成的作业", CurrentTitle: "最后一关"}, Result: json.RawMessage(`{"homeworks":[
		{"course":"FIRST","course_name":"先前课程","homework_id":11,"title":"实际失败的作业","status":"needs_service","result":{"status":"failed","reason_code":"model_error","current_position":2,"current_title":"失败关卡","diagnostics":["provider request failed"]}},
		{"course":"LATER","course_name":"后面课程","homework_id":22,"title":"后面完成的作业","status":"passed"}]}`)}
	out := deriveJobContext(job)
	if out.Course.Identifier != "FIRST" || out.Course.Name != "先前课程" || out.Homework.ID != 11 || out.Homework.Name != "实际失败的作业" || out.Level.Title != "失败关卡" || out.Summary.ReasonCode != "model_error" {
		t.Fatalf("last successful progress hid a prior failure: %+v", out)
	}
}

func TestAdminJobContextMissingNamesRemainMissingForResolver(t *testing.T) {
	job := jobs.Job{Request: jobs.Request{Course: "ABC123", HomeworkID: 123, Scope: "homework"}, Progress: &jobs.BatchProgress{CurrentTitle: "公开样例", CurrentPosition: 2}, Result: json.RawMessage(`{"status":"failed","programming":{"status":"selftest_failed","diagnostics":["sample output mismatch"],"code":"private generated source"},"run_id":"private-run","candidate_id":77}`)}
	out := deriveJobContext(job)
	if out.Course.Name != "" || out.Homework.Name != "" || out.Course.Identifier != "ABC123" || out.Homework.ID != 123 || out.Level.Title != "公开样例" {
		t.Fatalf("IDs must not be passed off as names: %+v", out)
	}
	if out.Summary.ReasonCode != "selftest_failed" {
		t.Fatalf("programming outcome missing: %+v", out.Summary)
	}
	encoded, err := json.Marshal(out)
	if err != nil || strings.Contains(string(encoded), "private generated source") || strings.Contains(string(encoded), "private-run") || strings.Contains(string(encoded), "candidate_id") {
		t.Fatalf("only the allowlisted diagnostic projection belongs in context: %s / %v", encoded, err)
	}
}

func TestAdminJobContextCollectionHasDistinctScope(t *testing.T) {
	job := jobs.Job{Request: jobs.Request{Mode: "collect_verified"}, ErrorCode: "COLLECTION_STOPPED", Result: json.RawMessage(`{"failed":1,"reasons":{"unsupported":1}}`)}
	out := deriveJobContext(job)
	if out.ScopeLabel != "历史题库采集" || out.Homework.Name != "" || out.Homework.ID != 0 || len(out.Summary.AttentionHomeworks) != 0 || out.Summary.ProgressLabel != "" {
		t.Fatalf("collection must not masquerade as an unnamed answer assignment: %+v", out)
	}
}

func TestAdminJobContextBoundedDiagnosticsAndAttentionRows(t *testing.T) {
	rows := make([]string, 120)
	for i := range rows {
		rows[i] = fmt.Sprintf(`{"course":"C","course_name":"课程","homework_id":%d,"title":"作业","status":"needs_service","result":{"status":"failed","reason_code":"load_failed","diagnostics":["", "same", "same", %q]}}`, i+1, strings.Repeat("长", 2000))
	}
	job := jobs.Job{Result: json.RawMessage(`{"homeworks":[` + strings.Join(rows, ",") + `]}`)}
	out := deriveJobContext(job)
	if len(out.Summary.AttentionHomeworks) != 100 || len(out.Summary.Diagnostics) != 2 || len([]rune(out.Summary.Diagnostics[1])) != 1201 {
		t.Fatalf("unexpected projection bounds: rows=%d diagnostics=%v", len(out.Summary.AttentionHomeworks), out.Summary.Diagnostics)
	}
	job.Result = json.RawMessage(`{"levels":[{"status":"failed"`) // broken historic payload
	out = deriveJobContext(job)
	if out.Summary.Diagnostics == nil || out.Summary.AttentionHomeworks == nil {
		t.Fatal("malformed result must keep predictable empty list values")
	}
}

func TestAdminJobContextNoIssueForPlannedPassedOrOptionalLevels(t *testing.T) {
	job := jobs.Job{Request: jobs.Request{Scope: "homework"}, Result: json.RawMessage(`{"status":"passed","total_levels":2,"completed_levels":2,"levels":[{"status":"passed","position":1,"title":"完成"},{"status":"skipped","position":2,"title":"已完成"},{"status":"optional","position":3,"title":"选做"},{"status":"planned","position":4,"title":"未开始"}]}`)}
	out := deriveJobContext(job)
	if len(out.Summary.AttentionHomeworks) != 0 || out.Summary.ReasonCode != "" || out.Level.Title != "" {
		t.Fatalf("healthy/untouched levels should not manufacture a failure: %+v", out)
	}
}
