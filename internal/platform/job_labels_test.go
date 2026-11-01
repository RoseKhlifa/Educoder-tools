package platform

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJobLabelsPersistAcrossRestartAndKeepHomeworkCourseScope(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "platform.sqlite")
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RememberCourses(ctx, []CourseLabel{{Identifier: "COURSE_A", Name: "数据结构"}, {Identifier: "COURSE_B", Name: "计算机系统基础"}}); err != nil {
		t.Fatal(err)
	}
	if err = s.RememberHomeworks(ctx, "COURSE_A", []HomeworkLabel{{ID: 3985868, Name: "客观题测试-第1章绪论"}}); err != nil {
		t.Fatal(err)
	}
	if err = s.RememberHomeworks(ctx, "COURSE_B", []HomeworkLabel{{ID: 3985868, Name: "加减运算指令"}}); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, want := range []struct{ course, courseName, homeworkName string }{
		{"COURSE_A", "数据结构", "客观题测试-第1章绪论"},
		{"COURSE_B", "计算机系统基础", "加减运算指令"},
	} {
		course, homework, err := s.LookupJobLabels(ctx, want.course, 3985868)
		if err != nil || course != want.courseName || homework != want.homeworkName {
			t.Fatalf("lookup %s = %q, %q, %v", want.course, course, homework, err)
		}
	}
	course, homework, err := s.LookupJobLabels(ctx, "COURSE_A", 0)
	if err != nil || course != "数据结构" || homework != "" {
		t.Fatalf("course-only lookup = %q, %q, %v", course, homework, err)
	}
	course, homework, err = s.LookupJobLabels(ctx, "UNKNOWN", 42)
	if err != nil || course != "" || homework != "" {
		t.Fatalf("unknown lookup = %q, %q, %v", course, homework, err)
	}
	course, homework, err = s.LookupJobLabels(ctx, "COURSE_A", 42)
	if err != nil || course != "数据结构" || homework != "" {
		t.Fatalf("unknown homework = %q, %q, %v", course, homework, err)
	}
}

func TestJobLabelsDoNotChangeIdentityOrAuthorizeOwnership(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, Options{AdminUserIDs: []int64{1}})
	user, err := s.UpsertLogin(ctx, Identity{UpstreamID: 2, Login: "student", Name: "同学"})
	if err != nil {
		t.Fatal(err)
	}
	// These strings are display data. Even names resembling roles, user IDs,
	// SQL, or markup do not touch account tables or grant any authority.
	if err = s.RememberCourses(ctx, []CourseLabel{{Identifier: "admin", Name: "<script>role='admin'</script>"}}); err != nil {
		t.Fatal(err)
	}
	if err = s.RememberHomeworks(ctx, "admin", []HomeworkLabel{{ID: 2, Name: "platform:" + user.ID + "'; DROP TABLE platform_users; --"}}); err != nil {
		t.Fatal(err)
	}
	actual, err := s.LookupUser(ctx, user.ID)
	if err != nil || actual != user || actual.IsAdmin() {
		t.Fatalf("display label altered identity: %+v, %v", actual, err)
	}
	if err = s.SetStatus(ctx, user.ID, user.ID, "disabled"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("display label granted administrator status: %v", err)
	}
	stats, err := s.Stats(ctx)
	if err != nil || stats.TotalUsers != 1 {
		t.Fatalf("display labels created platform users: %+v, %v", stats, err)
	}
}

func TestJobLabelsRepeatWithoutWriteAndRenameWithoutDuplication(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, Options{})
	first := time.Date(2026, 11, 1, 2, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return first }
	courses := []CourseLabel{{Identifier: "NTHPZ75R", Name: " 数据结构 "}, {Identifier: "NTHPZ75R", Name: "数据结构"}}
	homeworks := []HomeworkLabel{{ID: 7, Name: "第1章"}, {ID: 7, Name: "第1章"}}
	if err := s.RememberCourses(ctx, courses); err != nil {
		t.Fatal(err)
	}
	if err := s.RememberHomeworks(ctx, "NTHPZ75R", homeworks); err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return first.Add(time.Hour) }
	if err := s.RememberCourses(ctx, courses); err != nil {
		t.Fatal(err)
	}
	if err := s.RememberHomeworks(ctx, "NTHPZ75R", homeworks); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"job_course_labels", "job_homework_labels"} {
		var count int
		var stamp string
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*),MIN(updated_at) FROM "+table).Scan(&count, &stamp); err != nil {
			t.Fatal(err)
		}
		if count != 1 || stamp != first.Format(time.RFC3339Nano) {
			t.Fatalf("identical label wrote to %s: count=%d updated=%q", table, count, stamp)
		}
	}
	if err := s.RememberCourses(ctx, []CourseLabel{{Identifier: "NTHPZ75R", Name: "数据结构（更名）"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.RememberHomeworks(ctx, "NTHPZ75R", []HomeworkLabel{{ID: 7, Name: "第1章绪论"}}); err != nil {
		t.Fatal(err)
	}
	course, homework, err := s.LookupJobLabels(ctx, "NTHPZ75R", 7)
	if err != nil || course != "数据结构（更名）" || homework != "第1章绪论" {
		t.Fatalf("rename failed: %q, %q, %v", course, homework, err)
	}
}

func TestMissingJobLabelsFillUnknownWithoutOverwritingCurrentNames(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, Options{})
	first := time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return first }
	if err := s.RememberCourses(ctx, []CourseLabel{{Identifier: "KNOWN", Name: "课程当前名称"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.RememberHomeworks(ctx, "KNOWN", []HomeworkLabel{{ID: 10, Name: "作业当前名称"}}); err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return first.Add(time.Hour) }
	if err := s.RememberMissingCourses(ctx, []CourseLabel{{Identifier: "KNOWN", Name: "课程旧名称"}, {Identifier: "UNKNOWN", Name: "缺失课程补入"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.RememberMissingHomeworks(ctx, "KNOWN", []HomeworkLabel{{ID: 10, Name: "作业旧名称"}, {ID: 11, Name: "缺失作业补入"}}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		course, courseName, homeworkName string
		id                               int64
	}{
		{"KNOWN", "课程当前名称", "作业当前名称", 10},
		{"KNOWN", "课程当前名称", "缺失作业补入", 11},
		{"UNKNOWN", "缺失课程补入", "", 10},
	} {
		course, homework, err := s.LookupJobLabels(ctx, want.course, want.id)
		if err != nil || course != want.courseName || homework != want.homeworkName {
			t.Fatalf("missing label merge = %q, %q, %v; want %+v", course, homework, err, want)
		}
	}
	for _, query := range []string{
		"SELECT updated_at FROM job_course_labels WHERE course_identifier='KNOWN'",
		"SELECT updated_at FROM job_homework_labels WHERE course_identifier='KNOWN' AND homework_id=10",
	} {
		var stamp string
		if err := s.db.QueryRowContext(ctx, query).Scan(&stamp); err != nil || stamp != first.Format(time.RFC3339Nano) {
			t.Fatalf("historic metadata touched current label timestamp = %q, %v", stamp, err)
		}
	}
	// A later verified read remains authoritative even for a label first populated
	// by historical metadata.
	if err := s.RememberCourses(ctx, []CourseLabel{{Identifier: "UNKNOWN", Name: "补入课程的新名称"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.RememberHomeworks(ctx, "KNOWN", []HomeworkLabel{{ID: 11, Name: "补入作业的新名称"}}); err != nil {
		t.Fatal(err)
	}
	course, _, err := s.LookupJobLabels(ctx, "UNKNOWN", 0)
	if err != nil || course != "补入课程的新名称" {
		t.Fatalf("fresh course read did not replace historic name: %q, %v", course, err)
	}
	_, homework, err := s.LookupJobLabels(ctx, "KNOWN", 11)
	if err != nil || homework != "补入作业的新名称" {
		t.Fatalf("fresh homework read did not replace historic name: %q, %v", homework, err)
	}
}

func TestJobLabelsRejectInvalidBatchBeforeChangingDirectory(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, Options{})
	for _, labels := range [][]CourseLabel{
		{{Identifier: "", Name: "课程"}},
		{{Identifier: "class/123", Name: "课程"}},
		{{Identifier: strings.Repeat("a", 129), Name: "课程"}},
		{{Identifier: "VALID", Name: " "}},
		{{Identifier: "VALID", Name: strings.Repeat("课", 513)}},
		{{Identifier: "VALID", Name: "课程\x00秘密"}},
		{{Identifier: "VALID", Name: "https://example.test/resource?token=secret"}},
		{{Identifier: "VALID", Name: string([]byte{0xff})}},
		{{Identifier: "VALID", Name: "不得部分写入"}, {Identifier: "invalid?", Name: "课程"}},
		make([]CourseLabel, maxJobLabelBatch+1),
	} {
		for _, remember := range []func(context.Context, []CourseLabel) error{s.RememberCourses, s.RememberMissingCourses} {
			if err := remember(ctx, labels); !errors.Is(err, ErrInvalidJobLabel) {
				t.Fatalf("invalid course batch accepted: %v", err)
			}
		}
	}
	for _, labels := range [][]HomeworkLabel{
		{{ID: 0, Name: "作业"}},
		{{ID: -1, Name: "作业"}},
		{{ID: 1, Name: ""}},
		{{ID: 1, Name: strings.Repeat("业", 513)}},
		{{ID: 1, Name: "data:application/json,secrets"}},
		{{ID: 1, Name: "不得部分写入"}, {ID: -2, Name: "作业"}},
		make([]HomeworkLabel, maxJobLabelBatch+1),
	} {
		for _, remember := range []func(context.Context, string, []HomeworkLabel) error{s.RememberHomeworks, s.RememberMissingHomeworks} {
			if err := remember(ctx, "VALID", labels); !errors.Is(err, ErrInvalidJobLabel) {
				t.Fatalf("invalid homework batch accepted: %v", err)
			}
		}
	}
	if err := s.RememberHomeworks(ctx, "class/123", nil); !errors.Is(err, ErrInvalidJobLabel) {
		t.Fatalf("invalid homework course accepted: %v", err)
	}
	for _, invalid := range []struct {
		course string
		id     int64
	}{{"", 0}, {"../../course", 1}, {"VALID", -1}} {
		if _, _, err := s.LookupJobLabels(ctx, invalid.course, invalid.id); !errors.Is(err, ErrInvalidJobLabel) {
			t.Fatalf("invalid lookup accepted: %v", err)
		}
	}
	for _, table := range []string{"job_course_labels", "job_homework_labels"} {
		var count int
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("invalid batch partially changed %s: %d", table, count)
		}
	}
	// Count characters rather than UTF-8 bytes when enforcing the name bound.
	if err := s.RememberCourses(ctx, []CourseLabel{{Identifier: "VALID", Name: strings.Repeat("课", 512)}}); err != nil {
		t.Fatalf("valid bounded UTF-8 title rejected: %v", err)
	}
}
