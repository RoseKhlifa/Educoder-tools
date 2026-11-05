package taskservice

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/solver"
)

type courseDeadlineTransport func(*http.Request) (*http.Response, error)

func (f courseDeadlineTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestRunCoursesUsesConfiguredDeadline(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		configured, parent, want time.Duration
	}{
		{"all courses two hours", 2 * time.Hour, 0, 2 * time.Hour},
		{"operator four hours", 4 * time.Hour, 0, 4 * time.Hour},
		{"short configured deadline", 3 * time.Minute, 0, 3 * time.Minute},
		{"earlier parent deadline", 2 * time.Hour, 20 * time.Minute, 20 * time.Minute},
		{"hard upper bound", 25 * time.Hour, 0, 24 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, course := range []string{"", "C"} {
				t.Run("course="+course, func(t *testing.T) {
					ctx := context.Background()
					if tc.parent > 0 {
						var cancel context.CancelFunc
						ctx, cancel = context.WithTimeout(ctx, tc.parent)
						defer cancel()
					}
					started := time.Now()
					reads := 0
					client := educoder.New("http://course.invalid")
					client.LoginName = "student"
					// Inspect the context actually passed to the protocol request,
					// without waiting for a long deadline or using the HTTP client's
					// independent short per-request timeout.
					client.HTTP = &http.Client{Transport: courseDeadlineTransport(func(r *http.Request) (*http.Response, error) {
						deadline, ok := r.Context().Deadline()
						if !ok {
							t.Fatal("course protocol request has no bounded deadline")
						}
						actual := deadline.Sub(started)
						if actual < tc.want-time.Second || actual > tc.want+time.Second {
							t.Fatalf("scope=%q configured=%v parent=%v deadline=%v want=%v", course, tc.configured, tc.parent, actual, tc.want)
						}
						reads++
						body := ""
						switch r.URL.Path {
						case "/api/users/student/courses.json":
							body = `{"count":1,"courses":[{"id":1,"name":"Course","first_category_url":"/classrooms/C/shixun_homework"}]}`
						case "/api/courses/C/homework_commons.json":
							body = `{"query_total_count":0,"homeworks":[]}`
						default:
							t.Fatalf("unexpected protocol request %s", r.URL.Path)
						}
						return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
					})}
					result, err := New(bank(t), nil, Config{Timeout: tc.configured}).RunCourses(ctx, client, Request{Owner: "owner", Course: course, Mode: solver.ModeSubmit}, nil)
					if err != nil || result == nil || result.Status != "passed" || reads != 2 {
						t.Fatalf("result=%+v err=%v reads=%d", result, err, reads)
					}
				})
			}
		})
	}
}

func TestRunCoursesConfiguredDeadlinePreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := educoder.New("http://course.invalid")
	client.LoginName = "student"
	client.HTTP = &http.Client{Transport: courseDeadlineTransport(func(r *http.Request) (*http.Response, error) {
		cancel()
		return nil, r.Context().Err()
	})}
	result, err := New(bank(t), nil, Config{Timeout: 2 * time.Hour}).RunCourses(ctx, client, Request{Owner: "owner", Mode: solver.ModeSubmit}, nil)
	if !errors.Is(err, context.Canceled) || result == nil || result.Status != "canceled" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
