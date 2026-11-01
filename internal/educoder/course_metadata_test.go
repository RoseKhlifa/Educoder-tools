package educoder

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestCourseMetadataKeepsObservedTeacherAndExactStudentCount(t *testing.T) {
	var banners atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/users/demo/courses.json":
			w.Write([]byte(`{"count":1,"courses":[{"id":1,"name":"Example","first_category_url":"/classrooms/COURSE/shixun_homework","members_count":30,"teacher":{"real_name":" Example teacher "},"teacher_users":["Teacher A","Teacher B"]}]}`))
		case "/api/courses/COURSE/top_banner.json":
			banners.Add(1)
			if r.Method != http.MethodGet || r.Header.Get("X-EDU-Signature") == "" {
				t.Error("metadata must use the signed read-only protocol")
			}
			w.Write([]byte(`{"teacher_name":" Example teacher ","student_count":25,"teacher_count":5,"members_count":30,"teacher_login":"not-public"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c := New(server.URL)
	c.LoginName = "demo"
	courses, err := c.Courses(context.Background(), "processing")
	if err != nil || len(courses) != 1 {
		t.Fatalf("courses=%+v err=%v", courses, err)
	}
	if courses[0].TeacherName != "Example teacher" || courses[0].StudentCount != nil || banners.Load() != 0 {
		t.Fatalf("base course list must retain the observed teacher without mistaking members for students or fetching banners: %+v", courses[0])
	}
	metadata, err := c.CourseMetadata(context.Background(), "COURSE")
	if err != nil || metadata.TeacherName != "Example teacher" || metadata.StudentCount == nil || *metadata.StudentCount != 25 {
		t.Fatalf("metadata=%+v err=%v", metadata, err)
	}
	*metadata.StudentCount = 999 // Callers cannot mutate the cached response.
	cached, err := c.CourseMetadata(context.Background(), "COURSE")
	if err != nil || cached.StudentCount == nil || *cached.StudentCount != 25 || banners.Load() != 1 {
		t.Fatalf("cached=%+v banners=%d err=%v", cached, banners.Load(), err)
	}
	c.courseMetadataCache.mu.Lock()
	entry := c.courseMetadataCache.entries["COURSE"]
	if until := time.Until(entry.expiresAt); until < 4*time.Minute || until > 5*time.Minute {
		t.Errorf("metadata cache TTL=%v, want five minutes", until)
	}
	entry.expiresAt = time.Now().Add(-time.Second)
	c.courseMetadataCache.entries["COURSE"] = entry
	c.courseMetadataCache.mu.Unlock()
	if _, err := c.CourseMetadata(context.Background(), "COURSE"); err != nil || banners.Load() != 2 {
		t.Fatalf("expired cache did not refresh: requests=%d err=%v", banners.Load(), err)
	}
	other := New(server.URL)
	if _, err := other.CourseMetadata(context.Background(), "COURSE"); err != nil || banners.Load() != 3 {
		t.Fatalf("metadata cache crossed authenticated clients: requests=%d err=%v", banners.Load(), err)
	}
}

func TestCourseMetadataMissingZeroInvalidAndFailedReads(t *testing.T) {
	var failedReads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/courses/EMPTY/top_banner.json":
			w.Write([]byte(`{"members_count":100,"teacher_count":5}`))
		case "/api/courses/ZERO/top_banner.json":
			w.Write([]byte(`{"student_count":0}`))
		case "/api/courses/NEGATIVE/top_banner.json":
			w.Write([]byte(`{"student_count":-1}`))
		case "/api/courses/FAIL/top_banner.json":
			failedReads.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"message":"temporary unavailable"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c := New(server.URL)
	for _, name := range []string{"EMPTY", "NEGATIVE"} {
		got, err := c.CourseMetadata(context.Background(), name)
		if err != nil || got.StudentCount != nil {
			t.Fatalf("%s metadata=%+v err=%v, want unknown student count", name, got, err)
		}
	}
	zero, err := c.CourseMetadata(context.Background(), "ZERO")
	if err != nil || zero.StudentCount == nil || *zero.StudentCount != 0 {
		t.Fatalf("explicit zero lost: %+v err=%v", zero, err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := c.CourseMetadata(context.Background(), "FAIL"); err == nil {
			t.Fatal("failed metadata read was accepted")
		}
	}
	if failedReads.Load() != 2 {
		t.Fatal("failed metadata reads must not be cached")
	}
	if _, err := c.CourseMetadata(context.Background(), "../OTHER"); err == nil {
		t.Fatal("invalid identifier must be rejected before a request")
	}
	encoded, err := json.Marshal(zero)
	if err != nil || string(encoded) != `{"teacher_name":"","student_count":0}` {
		t.Fatalf("narrow metadata contract=%s err=%v", encoded, err)
	}
}
