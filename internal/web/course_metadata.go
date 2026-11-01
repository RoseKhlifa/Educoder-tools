package web

import (
	"context"
	"strings"
	"sync"
	"time"

	"educoder-cli/internal/educoder"
)

type courseMetadataReader interface {
	CourseMetadata(context.Context, string) (educoder.CourseMetadata, error)
}

// enrichCourseMetadata only reads identifiers already returned by the user's
// own classroom list. A slow or unavailable banner leaves the list usable and
// the student count unknown rather than substituting total member counts.
func enrichCourseMetadata(ctx context.Context, client EducoderClient, courses []educoder.Course) []educoder.Course {
	reader, ok := client.(courseMetadataReader)
	if !ok || len(courses) == 0 {
		return courses
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	result := append([]educoder.Course(nil), courses...)
	indices := make(chan int)
	var workers sync.WaitGroup
	for worker := 0; worker < min(4, len(courses)); worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range indices {
				if ctx.Err() != nil {
					continue
				}
				metadata, err := reader.CourseMetadata(ctx, result[index].Identifier)
				if err != nil {
					continue
				}
				if name := strings.TrimSpace(metadata.TeacherName); name != "" {
					result[index].TeacherName = name
				}
				if metadata.StudentCount != nil && *metadata.StudentCount >= 0 {
					count := *metadata.StudentCount
					result[index].StudentCount = &count
				}
			}
		}()
	}
	for index := range result {
		select {
		case indices <- index:
		case <-ctx.Done():
			close(indices)
			workers.Wait()
			return result
		}
	}
	close(indices)
	workers.Wait()
	return result
}
