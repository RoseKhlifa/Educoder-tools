package educoder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type progressReadRoundTripper func(*http.Request) (*http.Response, error)

func (f progressReadRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func progressReadResponse(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
}

const progressReadFullScore = `{"data":{"challenge_settings":[{"challenge_id":7,"challenge_name":"required","challenge_score":"100.0","game_score":"100.00"}]}}`

func TestHomeworkProgressRetriesActualClientTimeoutWithFreshScore(t *testing.T) {
	client := New("https://fixture.invalid")
	client.HTTP.Timeout = 15 * time.Millisecond
	var calls atomic.Int64
	client.HTTP.Transport = progressReadRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/homework_commons/4/shixun_challenge_data.json" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if calls.Add(1) == 1 {
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		return progressReadResponse(200, progressReadFullScore), nil
	})
	progress, err := client.HomeworkProgress(context.Background(), "C", 4)
	if err != nil || calls.Load() != 2 || progress == nil || len(progress.Challenges) != 1 || !progress.Challenges[0].HasFullScore() {
		t.Fatalf("progress=%+v err=%v calls=%d", progress, err, calls.Load())
	}
}

type progressInterruptedBody struct{}

func (progressInterruptedBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (progressInterruptedBody) Close() error             { return nil }

func TestHomeworkProgressRetriesOnlyObservedTransientReadFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail func() (*http.Response, error)
	}{
		{"transport EOF", func() (*http.Response, error) { return nil, io.EOF }},
		{"body EOF", func() (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: progressInterruptedBody{}}, nil
		}},
		{"HTTP 429", func() (*http.Response, error) { return progressReadResponse(429, "private upstream text"), nil }},
		{"HTTP 503", func() (*http.Response, error) { return progressReadResponse(503, "private upstream text"), nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := New("https://fixture.invalid")
			var calls int
			client.HTTP.Transport = progressReadRoundTripper(func(*http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return tc.fail()
				}
				return progressReadResponse(200, progressReadFullScore), nil
			})
			progress, err := client.HomeworkProgress(context.Background(), "C", 4)
			if err != nil || progress == nil || calls != 2 {
				t.Fatalf("progress=%+v err=%v calls=%d", progress, err, calls)
			}
		})
	}
}

func TestHomeworkProgressExhaustionKeepsSanitizedCategoryAndBound(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		category string
		timeout  bool
	}{
		{name: "client timeout", category: "timeout", timeout: true},
		{name: "503", status: 503, category: "http_status"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := New("https://fixture.invalid")
			client.LoginName = "secret-login"
			client.HTTP.Timeout = 5 * time.Millisecond
			var calls int
			client.HTTP.Transport = progressReadRoundTripper(func(r *http.Request) (*http.Response, error) {
				calls++
				if tc.timeout {
					<-r.Context().Done()
					return nil, fmt.Errorf("private signed URL ?TOKEN=secret: %w", r.Context().Err())
				}
				return progressReadResponse(tc.status, `{"message":"private COOKIE=secret"}`), nil
			})
			progress, err := client.HomeworkProgress(context.Background(), "C", 4)
			var readError *HomeworkProgressReadError
			if progress != nil || !errors.As(err, &readError) || calls != 3 || readError.Attempts != 3 || readError.Category != tc.category || readError.HTTPStatus != tc.status {
				t.Fatalf("progress=%+v err=%v calls=%d", progress, err, calls)
			}
			if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "fixture") {
				t.Fatalf("client timeout confused with parent deadline or private data leaked: %v", err)
			}
		})
	}
}

func TestHomeworkProgressDoesNotRetryAuthSchemaOrPermanentFailures(t *testing.T) {
	for _, tc := range []struct {
		name, body, category string
		status               int
	}{
		{"401", `{"message":"secret credential"}`, "http_status", 401},
		{"403", `{"message":"secret credential"}`, "http_status", 403},
		{"404", `{"message":"secret credential"}`, "http_status", 404},
		{"API auth", `{"status":-1,"message":"secret session"}`, "api_status", 200},
		{"malformed JSON", `{"data":`, "schema", 200},
		{"invalid score type", `{"data":{"challenge_settings":[{"challenge_id":"bad"}]}}`, "schema", 200},
		{"oversized", strings.Repeat(" ", (16<<20)+1), "response_limit", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := New("https://fixture.invalid")
			var calls int
			client.HTTP.Transport = progressReadRoundTripper(func(*http.Request) (*http.Response, error) {
				calls++
				return progressReadResponse(tc.status, tc.body), nil
			})
			_, err := client.HomeworkProgress(context.Background(), "C", 4)
			var readError *HomeworkProgressReadError
			if !errors.As(err, &readError) || calls != 1 || readError.Attempts != 1 || readError.Category != tc.category || strings.Contains(err.Error(), "secret") {
				t.Fatalf("err=%v calls=%d", err, calls)
			}
		})
	}
}

func TestHomeworkProgressCancellationStopsReadOrBackoff(t *testing.T) {
	for _, inRead := range []bool{false, true} {
		t.Run(fmt.Sprint("in_read=", inRead), func(t *testing.T) {
			client := New("https://fixture.invalid")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var calls int
			client.HTTP.Transport = progressReadRoundTripper(func(r *http.Request) (*http.Response, error) {
				calls++
				if inRead {
					cancel()
					return nil, r.Context().Err()
				}
				time.AfterFunc(20*time.Millisecond, cancel)
				return progressReadResponse(503, "unavailable"), nil
			})
			started := time.Now()
			_, err := client.HomeworkProgress(ctx, "C", 4)
			var readError *HomeworkProgressReadError
			if !errors.Is(err, context.Canceled) || !errors.As(err, &readError) || readError.Category != "canceled" || calls != 1 || time.Since(started) > time.Second {
				t.Fatalf("err=%v calls=%d elapsed=%v", err, calls, time.Since(started))
			}
		})
	}
}

func TestHomeworkProgressReadRetryDoesNotChangeOtherProtocolRequests(t *testing.T) {
	client := New("https://fixture.invalid")
	var calls int
	client.HTTP.Transport = progressReadRoundTripper(func(*http.Request) (*http.Response, error) {
		calls++
		return progressReadResponse(503, `{"message":"unchanged response"}`), nil
	})
	err := client.request(context.Background(), http.MethodPost, "/api/tasks/G/game_build.json", nil, nil, "/", nil)
	var readError *HomeworkProgressReadError
	if err == nil || errors.As(err, &readError) || calls != 1 || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("ordinary request behavior changed: err=%v calls=%d", err, calls)
	}
}

func TestHomeworkProgressParentDeadlineIsNotClientTimeout(t *testing.T) {
	client := New("https://fixture.invalid")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	var calls int
	client.HTTP.Transport = progressReadRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	_, err := client.HomeworkProgress(ctx, "C", 4)
	var readError *HomeworkProgressReadError
	if !errors.Is(err, context.DeadlineExceeded) || !errors.As(err, &readError) || readError.Category != "canceled" || readError.Attempts != 1 || calls != 1 {
		t.Fatalf("parent deadline retried or lost: err=%v calls=%d", err, calls)
	}
}

type progressReadFailedBody struct{ err error }

func (b progressReadFailedBody) Read([]byte) (int, error) { return 0, b.err }
func (progressReadFailedBody) Close() error               { return nil }

func TestHomeworkProgressPermanentHTTPStatusDoesNotRetryBrokenBody(t *testing.T) {
	for _, status := range []int{401, 403, 404} {
		for _, tc := range []struct {
			category string
			cause    error
		}{
			{"interrupted", fmt.Errorf("private signed URL ?TOKEN=secret: %w", io.ErrUnexpectedEOF)},
			{"timeout", fmt.Errorf("private signed URL ?TOKEN=secret: %w", context.DeadlineExceeded)},
		} {
			t.Run(fmt.Sprintf("%d/%s", status, tc.category), func(t *testing.T) {
				client := New("https://fixture.invalid")
				var calls int
				client.HTTP.Transport = progressReadRoundTripper(func(*http.Request) (*http.Response, error) {
					calls++
					return &http.Response{StatusCode: status, Header: http.Header{}, Body: progressReadFailedBody{err: tc.cause}}, nil
				})
				_, err := client.HomeworkProgress(context.Background(), "C", 4)
				var readError *HomeworkProgressReadError
				if !errors.As(err, &readError) || readError.HTTPStatus != status || readError.Category != tc.category || readError.Attempts != 1 || calls != 1 {
					t.Fatalf("permanent status retried or metadata lost: err=%v calls=%d", err, calls)
				}
				if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "fixture") {
					t.Fatalf("private body error exposed: %v", err)
				}
			})
		}
	}
}
