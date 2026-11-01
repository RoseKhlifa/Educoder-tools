package educoder

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// CourseMetadata contains only the displayed instructor and exact student
// count. The course list's members_count includes instructors as well.
type CourseMetadata struct {
	TeacherName  string `json:"teacher_name"`
	StudentCount *int   `json:"student_count"`
}

type cachedCourseMetadata struct {
	metadata  CourseMetadata
	expiresAt time.Time
}

// Pointer ownership lets execution clients share their original cookie jar
// without copying a live mutex when they install a narrower task transport.
type courseMetadataCache struct {
	mu      sync.Mutex
	entries map[string]cachedCourseMetadata
}

// CourseMetadata reads the recorded classroom top banner without entering a
// task. It is separate from Courses so background course scans do not fan out
// into extra metadata requests. Only successful reads are cached per client.
func (c *Client) CourseMetadata(ctx context.Context, course string) (CourseMetadata, error) {
	if err := validIdentifier(course); err != nil {
		return CourseMetadata{}, err
	}
	cache := c.courseMetadataCache
	if cache != nil {
		cache.mu.Lock()
		cached, ok := cache.entries[course]
		cache.mu.Unlock()
		if ok && cached.expiresAt.After(time.Now()) {
			return copyCourseMetadata(cached.metadata), nil
		}
	}
	var result CourseMetadata
	if err := c.request(ctx, http.MethodGet, "/api/courses/"+url.PathEscape(course)+"/top_banner.json", nil, nil, "/classrooms/"+course+"/shixun_homework", &result); err != nil {
		return CourseMetadata{}, err
	}
	result.TeacherName = strings.TrimSpace(result.TeacherName)
	if result.StudentCount != nil && *result.StudentCount < 0 {
		result.StudentCount = nil
	}
	if cache != nil {
		cache.mu.Lock()
		if cache.entries == nil || len(cache.entries) >= 1000 {
			cache.entries = make(map[string]cachedCourseMetadata)
		}
		cache.entries[course] = cachedCourseMetadata{metadata: copyCourseMetadata(result), expiresAt: time.Now().Add(5 * time.Minute)}
		cache.mu.Unlock()
	}
	return result, nil
}

func copyCourseMetadata(value CourseMetadata) CourseMetadata {
	if value.StudentCount != nil {
		count := *value.StudentCount
		value.StudentCount = &count
	}
	return value
}
