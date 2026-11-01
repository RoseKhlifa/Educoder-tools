package web

import (
	"encoding/json"
	"fmt"
	"strings"

	"educoder-cli/internal/jobs"
)

// AdminJobContext is an operator-only projection. It must never be embedded in
// consumerJob: account names, diagnostic details and internal target identifiers
// are intentionally available only from the authenticated admin endpoints.
type AdminJobContext struct {
	User       *AdminJobContextUser `json:"user,omitempty"`
	Course     AdminJobCourse       `json:"course"`
	Homework   AdminJobHomework     `json:"homework"`
	Level      AdminJobLevel        `json:"level"`
	ScopeLabel string               `json:"scope_label"`
	Summary    AdminJobSummary      `json:"summary"`
}

type AdminJobContextUser struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Login  string `json:"login,omitempty"`
	Linked bool   `json:"linked"`
}

type AdminJobCourse struct {
	Identifier string `json:"identifier,omitempty"`
	Name       string `json:"name,omitempty"`
}

type AdminJobHomework struct {
	ID   int64  `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

type AdminJobLevel struct {
	Title    string `json:"title,omitempty"`
	Position int    `json:"position,omitempty"`
	Score    string `json:"score,omitempty"`
	MaxScore string `json:"max_score,omitempty"`
}

type AdminJobSummary struct {
	ReasonCode         string                   `json:"reason_code,omitempty"`
	Message            string                   `json:"message,omitempty"`
	Diagnostics        []string                 `json:"diagnostics"`
	ProgressLabel      string                   `json:"progress_label,omitempty"`
	AttentionHomeworks []AdminAttentionHomework `json:"attention_homeworks"`
}

type AdminAttentionHomework struct {
	ID               int64    `json:"id,omitempty"`
	Name             string   `json:"name,omitempty"`
	CourseIdentifier string   `json:"course_identifier,omitempty"`
	CourseName       string   `json:"course_name,omitempty"`
	ReasonCode       string   `json:"reason_code,omitempty"`
	Diagnostics      []string `json:"diagnostics"`
	LevelTitle       string   `json:"level_title,omitempty"`
	LevelPosition    int      `json:"level_position,omitempty"`
}

// A fixed-depth projection avoids interpreting arbitrary executor payloads or
// returning generated code/answer bodies. The stored result itself is bounded by
// jobs.Config.MaxResultBytes; the additional bounds keep operator lists small.
type adminContextResult struct {
	Status             string                 `json:"status"`
	ReasonCode         string                 `json:"reason_code"`
	Course             string                 `json:"course"`
	CourseName         string                 `json:"course_name"`
	HomeworkID         int64                  `json:"homework_id"`
	HomeworkName       string                 `json:"homework_name"`
	TotalHomeworks     int                    `json:"total_homeworks"`
	CompletedHomeworks int                    `json:"completed_homeworks"`
	TotalLevels        int                    `json:"total_levels"`
	CompletedLevels    int                    `json:"completed_levels"`
	CurrentPosition    int                    `json:"current_position"`
	CurrentTitle       string                 `json:"current_title"`
	Score              string                 `json:"score"`
	MaxScore           string                 `json:"max_score"`
	Diagnostics        []string               `json:"diagnostics"`
	Levels             []adminContextLevel    `json:"levels"`
	Homeworks          []adminContextHomework `json:"homeworks"`
	Programming        *struct {
		Status      string   `json:"status"`
		Diagnostics []string `json:"diagnostics"`
	} `json:"programming"`
}

type adminContextHomework struct {
	Course     string             `json:"course"`
	CourseName string             `json:"course_name"`
	HomeworkID int64              `json:"homework_id"`
	Title      string             `json:"title"`
	Status     string             `json:"status"`
	ReasonCode string             `json:"reason_code"`
	Result     *adminContextBatch `json:"result"`
}

type adminContextBatch struct {
	Status          string              `json:"status"`
	ReasonCode      string              `json:"reason_code"`
	CurrentPosition int                 `json:"current_position"`
	CurrentTitle    string              `json:"current_title"`
	Diagnostics     []string            `json:"diagnostics"`
	TotalLevels     int                 `json:"total_levels"`
	CompletedLevels int                 `json:"completed_levels"`
	Levels          []adminContextLevel `json:"levels"`
}

type adminContextLevel struct {
	Position    int      `json:"position"`
	Title       string   `json:"title"`
	Status      string   `json:"status"`
	ReasonCode  string   `json:"reason_code"`
	Score       string   `json:"score"`
	MaxScore    string   `json:"max_score"`
	Diagnostics []string `json:"diagnostics"`
}

func deriveJobContext(j jobs.Job) AdminJobContext {
	out := AdminJobContext{
		Course:   AdminJobCourse{Identifier: j.Request.Course},
		Homework: AdminJobHomework{ID: j.Request.HomeworkID},
		Summary:  AdminJobSummary{Diagnostics: []string{}, AttentionHomeworks: []AdminAttentionHomework{}},
	}
	switch {
	case j.Request.Mode == "collect_verified":
		out.ScopeLabel = "历史题库采集"
	case j.Request.Scope == "all":
		out.ScopeLabel = "全部课程"
	case j.Request.Scope == "course":
		out.ScopeLabel = "整门课程"
	case j.Request.Scope == "homework":
		out.ScopeLabel = "整份作业"
	default:
		out.ScopeLabel = "单个关卡"
	}
	out.Summary.ReasonCode = adminContextText(j.ErrorCode, 120)
	out.Summary.Message = adminContextText(j.Message, 1200)
	var result adminContextResult
	if len(j.Result) <= 4*1024*1024 {
		if err := json.Unmarshal(j.Result, &result); err != nil {
			result = adminContextResult{}
		}
	}
	if out.Course.Identifier == "" {
		out.Course.Identifier = adminContextText(result.Course, 100)
	}
	out.Course.Name = adminContextText(result.CourseName, 500)
	if out.Homework.ID == 0 {
		out.Homework.ID = result.HomeworkID
	}
	out.Homework.Name = adminContextText(result.HomeworkName, 500)
	out.Level = AdminJobLevel{Title: adminContextText(result.CurrentTitle, 500), Position: result.CurrentPosition, Score: adminContextText(result.Score, 80), MaxScore: adminContextText(result.MaxScore, 80)}
	if p := j.Progress; p != nil {
		if out.Course.Identifier == "" {
			out.Course.Identifier = p.CurrentCourse
		}
		if out.Homework.ID == 0 {
			out.Homework.ID = p.CurrentHomeworkID
		}
		if out.Homework.Name == "" {
			out.Homework.Name = adminContextText(p.CurrentHomeworkTitle, 500)
		}
		if out.Level.Title == "" {
			out.Level.Title = adminContextText(p.CurrentTitle, 500)
			out.Level.Position = p.CurrentPosition
		}
	}
	out.Summary.Diagnostics = adminContextDiagnostics(result.Diagnostics)
	if result.ReasonCode != "" {
		out.Summary.ReasonCode = adminContextText(result.ReasonCode, 120)
	}
	if out.Summary.ReasonCode == "" && adminContextNeedsAttention(result.Status) {
		out.Summary.ReasonCode = adminContextText(result.Status, 120)
	}
	if result.Programming != nil {
		if adminContextNeedsAttention(result.Programming.Status) {
			out.Summary.ReasonCode = adminContextText(result.Programming.Status, 120)
		}
		out.Summary.Diagnostics = adminContextDiagnostics(append(out.Summary.Diagnostics, result.Programming.Diagnostics...))
	}
	batch := adminContextBatch{Status: result.Status, ReasonCode: result.ReasonCode, CurrentTitle: result.CurrentTitle, CurrentPosition: result.CurrentPosition, TotalLevels: result.TotalLevels, CompletedLevels: result.CompletedLevels, Diagnostics: result.Diagnostics, Levels: result.Levels}
	primaryBatch := &batch
	// The first failed assignment is more informative than the last progress
	// event, which may point at a subsequently completed assignment.
	var firstIssue bool
	for i, h := range result.Homeworks {
		if i >= 100 {
			break
		}
		if out.Course.Name == "" && (out.Course.Identifier == "" || out.Course.Identifier == h.Course) {
			out.Course.Identifier, out.Course.Name = adminContextText(h.Course, 100), adminContextText(h.CourseName, 500)
		}
		if out.Homework.ID == h.HomeworkID && out.Homework.Name == "" {
			out.Homework.Name = adminContextText(h.Title, 500)
		}
		if !adminContextNeedsAttention(h.Status) && (h.Result == nil || !adminContextNeedsAttention(h.Result.Status)) {
			continue
		}
		issue, level := adminContextIssue(h)
		out.Summary.AttentionHomeworks = append(out.Summary.AttentionHomeworks, issue)
		if !firstIssue {
			firstIssue = true
			out.Course = AdminJobCourse{Identifier: issue.CourseIdentifier, Name: issue.CourseName}
			out.Homework = AdminJobHomework{ID: issue.ID, Name: issue.Name}
			out.Level = level
			out.Summary.ReasonCode, out.Summary.Diagnostics = issue.ReasonCode, issue.Diagnostics
			// The job's outer message is often the generic "service required";
			// prefer the leaf diagnostic rather than masking the actual cause.
			out.Summary.Message = ""
			if len(issue.Diagnostics) > 0 {
				out.Summary.Message = issue.Diagnostics[0]
			}
			primaryBatch = h.Result
		}
	}
	if !firstIssue {
		if level, found := adminContextProblemLevel(batch); found {
			out.Level = adminContextLevelRef(level)
			out.Summary.ReasonCode = adminContextFirst(level.ReasonCode, batch.ReasonCode, level.Status, out.Summary.ReasonCode)
			out.Summary.Diagnostics = adminContextDiagnostics(append(level.Diagnostics, batch.Diagnostics...))
		}
	}
	if j.Request.Mode != "collect_verified" {
		total, completed := result.TotalHomeworks, result.CompletedHomeworks
		if total == 0 && j.Progress != nil {
			total, completed = j.Progress.TotalHomeworks, j.Progress.CompletedHomeworks
		}
		if total > 0 {
			out.Summary.ProgressLabel = fmt.Sprintf("%d / %d 份作业", completed, total)
		}
		if primaryBatch != nil && primaryBatch.TotalLevels > 0 {
			// Historical payloads can predate optional-level filtering, so these
			// counters describe the original plan, not a retroactive required count.
			levelLabel := fmt.Sprintf("%d / %d 个关卡", primaryBatch.CompletedLevels, primaryBatch.TotalLevels)
			if out.Summary.ProgressLabel != "" {
				out.Summary.ProgressLabel += " · 当前作业 " + levelLabel
			} else {
				out.Summary.ProgressLabel = levelLabel
			}
		} else if out.Summary.ProgressLabel == "" && j.Progress != nil && j.Progress.TotalLevels > 0 {
			out.Summary.ProgressLabel = fmt.Sprintf("%d / %d 个关卡", j.Progress.CompletedLevels, j.Progress.TotalLevels)
		}
	}
	return out
}

func adminContextIssue(h adminContextHomework) (AdminAttentionHomework, AdminJobLevel) {
	issue := AdminAttentionHomework{ID: h.HomeworkID, Name: adminContextText(h.Title, 500), CourseIdentifier: adminContextText(h.Course, 100), CourseName: adminContextText(h.CourseName, 500), ReasonCode: adminContextFirst(h.ReasonCode, h.Status), Diagnostics: []string{}}
	var level AdminJobLevel
	if h.Result != nil {
		b := *h.Result
		issue.ReasonCode = adminContextFirst(b.ReasonCode, h.ReasonCode, b.Status, h.Status)
		issue.Diagnostics = adminContextDiagnostics(b.Diagnostics)
		level = AdminJobLevel{Title: adminContextText(b.CurrentTitle, 500), Position: b.CurrentPosition}
		if l, found := adminContextProblemLevel(b); found {
			level = adminContextLevelRef(l)
			issue.ReasonCode = adminContextFirst(l.ReasonCode, b.ReasonCode, h.ReasonCode, l.Status, b.Status, h.Status)
			issue.Diagnostics = adminContextDiagnostics(append(l.Diagnostics, b.Diagnostics...))
		}
	}
	issue.LevelTitle, issue.LevelPosition = level.Title, level.Position
	return issue, level
}

func adminContextProblemLevel(b adminContextBatch) (adminContextLevel, bool) {
	var first adminContextLevel
	found := false
	for i, l := range b.Levels {
		if i >= 100 {
			break
		}
		if !adminContextNeedsAttention(l.Status) {
			continue
		}
		if l.Position == b.CurrentPosition && b.CurrentPosition > 0 {
			return l, true
		}
		if !found {
			first, found = l, true
		}
	}
	return first, found
}

func adminContextNeedsAttention(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "queued", "running", "planned", "pending", "waiting", "passed", "succeeded", "skipped", "completed", "skipped_optional", "optional", "deadline_passed", "generated", "selftest_passed", "selftest_skipped":
		return false
	default:
		return true
	}
}

func adminContextLevelRef(l adminContextLevel) AdminJobLevel {
	return AdminJobLevel{Title: adminContextText(l.Title, 500), Position: l.Position, Score: adminContextText(l.Score, 80), MaxScore: adminContextText(l.MaxScore, 80)}
}

func adminContextFirst(values ...string) string {
	for _, value := range values {
		if value = adminContextText(value, 120); value != "" {
			return value
		}
	}
	return ""
}

func adminContextText(value string, max int) string {
	value = strings.TrimSpace(value)
	r := []rune(value)
	if len(r) > max {
		value = string(r[:max]) + "…"
	}
	return value
}

func adminContextDiagnostics(values []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		value = adminContextText(value, 1200)
		if value != "" && !seen[value] {
			out, seen[value] = append(out, value), true
		}
		if len(out) >= 8 {
			break
		}
	}
	return out
}
