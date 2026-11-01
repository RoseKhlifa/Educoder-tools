package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"educoder-cli/internal/educoder"
)

type metadataFixture struct {
	*fakeClient
	read func(context.Context, string) (educoder.CourseMetadata, error)
}

func (f *metadataFixture) CourseMetadata(ctx context.Context, identifier string) (educoder.CourseMetadata, error) {
	return f.read(ctx, identifier)
}

func TestEnrichCourseMetadataBoundsFanoutAndPreservesUnknownCounts(t *testing.T) {
	var active, maximum atomic.Int32
	var mu sync.Mutex
	visited := map[string]bool{}
	start := make(chan struct{})
	fixture := &metadataFixture{fakeClient: &fakeClient{}}
	fixture.read = func(ctx context.Context, identifier string) (educoder.CourseMetadata, error) {
		current := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); current > old && !maximum.CompareAndSwap(old, current); old = maximum.Load() {
		}
		mu.Lock()
		visited[identifier] = true
		if len(visited) == 4 {
			close(start)
		}
		mu.Unlock()
		select {
		case <-start:
		case <-ctx.Done():
			return educoder.CourseMetadata{}, ctx.Err()
		}
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 3*time.Second {
			t.Error("metadata request must inherit a three-second total budget")
		}
		switch identifier {
		case "COURSE0":
			zero := 0
			return educoder.CourseMetadata{TeacherName: " New teacher ", StudentCount: &zero}, nil
		case "COURSE1":
			return educoder.CourseMetadata{}, errors.New("temporary upstream failure")
		case "COURSE2":
			return educoder.CourseMetadata{TeacherName: "Other teacher"}, nil
		default:
			count := 25
			return educoder.CourseMetadata{StudentCount: &count}, nil
		}
	}
	courses := make([]educoder.Course, 10)
	for i := range courses {
		courses[i] = educoder.Course{Identifier: fmt.Sprintf("COURSE%d", i), TeacherName: "Original teacher"}
	}
	got := enrichCourseMetadata(context.Background(), fixture, courses)
	if maximum.Load() != 4 || len(visited) != len(courses) {
		t.Fatalf("maximum=%d visited=%d, want four concurrent reads of exactly the listed courses", maximum.Load(), len(visited))
	}
	if got[0].TeacherName != "New teacher" || got[0].StudentCount == nil || *got[0].StudentCount != 0 {
		t.Fatalf("explicit metadata and zero count were not preserved: %+v", got[0])
	}
	if got[1].TeacherName != "Original teacher" || got[1].StudentCount != nil || got[2].StudentCount != nil {
		t.Fatalf("unavailable or missing student counts must remain unknown: %+v %+v", got[1], got[2])
	}
	if courses[0].TeacherName != "Original teacher" || courses[0].StudentCount != nil {
		t.Fatal("enrichment mutated the original cached classroom list")
	}
}

func TestEnrichCourseMetadataHonorsCancellation(t *testing.T) {
	fixture := &metadataFixture{fakeClient: &fakeClient{}, read: func(ctx context.Context, _ string) (educoder.CourseMetadata, error) {
		<-ctx.Done()
		return educoder.CourseMetadata{}, ctx.Err()
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	got := enrichCourseMetadata(ctx, fixture, []educoder.Course{{Identifier: "COURSE", TeacherName: "Known teacher"}})
	if time.Since(started) > time.Second || got[0].StudentCount != nil || got[0].TeacherName != "Known teacher" {
		t.Fatalf("canceled metadata read changed or blocked the list: %+v", got)
	}
}

func TestCourseDTOKeepsTeacherStudentDisplayFieldsNarrow(t *testing.T) {
	zero := 0
	course := educoder.Course{ID: 1, Identifier: "COURSE", Name: "Example", StudentCount: &zero}
	course.Teacher.RealName = " List teacher "
	got := toCourseDTO(course)
	if got.TeacherName != "List teacher" || got.StudentCount == nil || *got.StudentCount != 0 {
		t.Fatalf("DTO=%+v", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil || !strings.Contains(string(encoded), `"studentCount":0`) || !strings.Contains(string(encoded), `"teacherName":"List teacher"`) {
		t.Fatalf("DTO JSON=%s err=%v", encoded, err)
	}
	for _, private := range []string{"teacher_users", "teacher_login", "real_name", "members_count", "avatar_url"} {
		if strings.Contains(string(encoded), private) {
			t.Errorf("DTO exposed raw metadata field %s: %s", private, encoded)
		}
	}
	course.StudentCount = nil
	course.Teacher.RealName = ""
	missing, _ := json.Marshal(toCourseDTO(course))
	if strings.Contains(string(missing), "studentCount") || strings.Contains(string(missing), "teacherName") {
		t.Fatalf("unknown metadata should be absent: %s", missing)
	}
}
