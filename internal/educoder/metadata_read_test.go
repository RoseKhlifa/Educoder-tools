package educoder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func metadataList(t *testing.T, client *Client, operation string) (int, error) {
	t.Helper()
	if operation == metadataCourses {
		client.LoginName = "PRIVATE_LOGIN"
		rows, err := client.Courses(context.Background(), "processing")
		return len(rows), err
	}
	rows, err := client.Homeworks(context.Background(), "C", "")
	return len(rows), err
}

func metadataEmpty(operation string) string {
	if operation == metadataCourses {
		return `{"count":0,"courses":[]}`
	}
	return `{"query_total_count":0,"homeworks":[]}`
}

func TestMetadataListsRecoverTransientReads(t *testing.T) {
	for _, operation := range []string{metadataCourses, metadataHomeworks} {
		for _, status := range []int{429, 503} {
			t.Run(fmt.Sprintf("%s/%d", operation, status), func(t *testing.T) {
				client := New("https://fixture.invalid")
				calls := 0
				client.HTTP.Transport = progressReadRoundTripper(func(r *http.Request) (*http.Response, error) {
					calls++
					if r.Method != http.MethodGet {
						t.Fatalf("unexpected mutation: %s", r.Method)
					}
					if calls == 1 {
						return progressReadResponse(status, `{"message":"PRIVATE_COOKIE"}`), nil
					}
					return progressReadResponse(200, metadataEmpty(operation)), nil
				})
				rows, err := metadataList(t, client, operation)
				if err != nil || rows != 0 || calls != 2 {
					t.Fatalf("rows=%d calls=%d err=%v", rows, calls, err)
				}
			})
		}
	}
}

func TestMetadataListsRejectMissingNullAndIncompletePages(t *testing.T) {
	for _, operation := range []string{metadataCourses, metadataHomeworks} {
		bodies := []string{`{}`, `null`, `{"courses":null,"homeworks":null}`, `{"count":2,"courses":[],"query_total_count":2,"homeworks":[]}`}
		for i, body := range bodies {
			t.Run(fmt.Sprintf("%s/%d", operation, i), func(t *testing.T) {
				client := New("https://fixture.invalid")
				calls := 0
				client.HTTP.Transport = progressReadRoundTripper(func(*http.Request) (*http.Response, error) {
					calls++
					return progressReadResponse(200, body), nil
				})
				rows, err := metadataList(t, client, operation)
				var readError *MetadataReadError
				if rows != 0 || !errors.As(err, &readError) || readError.Category != "schema" || readError.Operation != operation || readError.Attempts != 1 || calls != 1 {
					t.Fatalf("missing data became an empty plan: rows=%d calls=%d err=%v", rows, calls, err)
				}
			})
		}
	}
}

func TestMetadataPaginationDoesNotReturnPartialRosterAsComplete(t *testing.T) {
	for _, operation := range []string{metadataCourses, metadataHomeworks} {
		client := New("https://fixture.invalid")
		calls := 0
		client.HTTP.Transport = progressReadRoundTripper(func(*http.Request) (*http.Response, error) {
			calls++
			if operation == metadataCourses {
				return progressReadResponse(200, `{"count":2,"courses":[{"id":1,"first_category_url":"/classrooms/C/shixun_homework"}]}`), nil
			}
			return progressReadResponse(200, `{"query_total_count":2,"homeworks":[{"homework_id":1}]}`), nil
		})
		rows, err := metadataList(t, client, operation)
		var readError *MetadataReadError
		if rows != 0 || !errors.As(err, &readError) || readError.Category != "schema" || calls != 2 {
			t.Fatalf("repeated page became complete: operation=%s rows=%d calls=%d err=%v", operation, rows, calls, err)
		}
	}
}

func TestMetadataExhaustionAndPermanentFailuresStayBoundedPrivate(t *testing.T) {
	for _, tc := range []struct{ status, want int }{{503, 3}, {401, 1}} {
		client := New("https://fixture.invalid")
		calls := 0
		client.HTTP.Transport = progressReadRoundTripper(func(*http.Request) (*http.Response, error) {
			calls++
			return progressReadResponse(tc.status, `{"message":"PRIVATE_COOKIE"}`), nil
		})
		_, err := metadataList(t, client, metadataCourses)
		var readError *MetadataReadError
		if !errors.As(err, &readError) || calls != tc.want || readError.Attempts != tc.want || strings.Contains(err.Error(), "PRIVATE") || strings.Contains(err.Error(), "fixture") {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
	}
}

func TestMetadataRosterRetriesOnlyItsKnownReadEndpoints(t *testing.T) {
	client := New("https://fixture.invalid")
	calls := map[string]int{}
	client.HTTP.Transport = progressReadRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls[r.URL.Path]++
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected mutation: %s", r.Method)
		}
		switch r.URL.Path {
		case "/api/shixun_homeworks/44/header_info.json":
			if calls[r.URL.Path] == 1 {
				return progressReadResponse(503, "private"), nil
			}
			return progressReadResponse(200, `{"data":{"homework_id":44,"work_id":5,"myshixun_identifier":"R","shixun_identifier":"S","challenges":[{"id":7,"name":"required"}]}}`), nil
		case "/api/homework_commons/44/shixun_challenge_data.json":
			return progressReadResponse(200, progressReadFullScore), nil
		case "/api/myshixuns/R/challenges.json":
			if calls[r.URL.Path] == 1 {
				return nil, io.EOF
			}
			return progressReadResponse(200, `[{"identifier":"G","position":1,"name":"required","status":2}]`), nil
		}
		t.Fatalf("unexpected endpoint: %s", r.URL.Path)
		return nil, errors.New("unexpected endpoint")
	})
	roster, err := client.HomeworkRoster(context.Background(), "C", Homework{ID: 44, IsShixun: true, ShixunIdentifier: "S", ChallengeCount: 1})
	if err != nil || roster == nil || len(roster.Levels) != 1 || calls["/api/shixun_homeworks/44/header_info.json"] != 2 || calls["/api/myshixuns/R/challenges.json"] != 2 {
		t.Fatalf("roster=%+v calls=%v err=%v", roster, calls, err)
	}
}

func TestMetadataRetryDoesNotMultiplyCollectionAttempts(t *testing.T) {
	client := New("https://fixture.invalid")
	client.LoginName = "student"
	calls := 0
	client.HTTP.Transport = progressReadRoundTripper(func(*http.Request) (*http.Response, error) {
		calls++
		return progressReadResponse(503, "temporary"), nil
	})
	reader, err := client.CollectionReader(CollectionBudget{Requests: 20, ResponseBytes: 2 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Courses(context.Background(), "processing"); err == nil || calls != 3 {
		t.Fatalf("collection attempts=%d err=%v", calls, err)
	}
	calls = 0
	if _, err := reader.HomeworkProgress(context.Background(), "C", 44); err == nil || calls != 3 {
		t.Fatalf("collection progress attempts=%d err=%v", calls, err)
	}
}

func TestMetadataRetryCannotWrapRuntimeCreationAndCancelsBackoff(t *testing.T) {
	client := New("https://fixture.invalid")
	calls := 0
	ctx, cancel := context.WithCancel(context.Background())
	client.HTTP.Transport = progressReadRoundTripper(func(*http.Request) (*http.Response, error) {
		calls++
		cancel()
		return progressReadResponse(503, "temporary"), nil
	})
	if err := client.readMetadata(ctx, metadataHeader, "/api/shixuns/S/shixun_exec.json", nil, "", nil, nil); err == nil || calls != 0 {
		t.Fatalf("runtime creation accepted as read: calls=%d err=%v", calls, err)
	}
	started := time.Now()
	_, err := client.Homeworks(ctx, "C", "")
	if !errors.Is(err, context.Canceled) || calls != 1 || time.Since(started) > time.Second {
		t.Fatalf("backoff ignored cancellation: calls=%d err=%v", calls, err)
	}
}
