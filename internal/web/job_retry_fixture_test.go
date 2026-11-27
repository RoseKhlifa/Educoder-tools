package web

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/jobs"
)

// Shared fixtures for the job retry tests: an upstream client with a course,
// one homework and its progress, and a helper that stores a finished job.
type retryFixtureClient struct {
	*fakeClient
	courses  []educoder.Course
	homework []educoder.Homework
	progress *educoder.HomeworkProgress
	readErr  error
	reads    atomic.Int64
}

func newRetryFixtureClient() *retryFixtureClient {
	return &retryFixtureClient{fakeClient: &fakeClient{}, courses: []educoder.Course{{Identifier: "C"}}, homework: []educoder.Homework{{ID: 42, Name: "作业", ChallengeCount: 2}}, progress: &educoder.HomeworkProgress{Challenges: []educoder.ChallengeProgress{{ChallengeID: 1, Name: "关卡一", ChallengeScore: "10", GameScore: "10"}, {ChallengeID: 2, Name: "关卡二", ChallengeScore: "10", GameScore: "0"}}}}
}

func (c *retryFixtureClient) Courses(context.Context, string) ([]educoder.Course, error) {
	c.reads.Add(1)
	return c.courses, c.readErr
}
func (c *retryFixtureClient) Homeworks(context.Context, string, string) ([]educoder.Homework, error) {
	c.reads.Add(1)
	return c.homework, c.readErr
}
func (c *retryFixtureClient) HomeworkProgress(context.Context, string, int64) (*educoder.HomeworkProgress, error) {
	c.reads.Add(1)
	return c.progress, c.readErr
}

func installRetryClient(t *testing.T, f *platformHTTPFixture, browser *http.Client, client EducoderClient) {
	t.Helper()
	req, _ := http.NewRequest("GET", f.server.URL, nil)
	for _, cookie := range browser.Jar.Cookies(req.URL) {
		if cookie.Name != sessionCookieName {
			continue
		}
		f.app.store.mu.Lock()
		session := f.app.store.sessions[cookie.Value]
		session.Client = client
		f.app.store.sessions[cookie.Value] = session
		f.app.store.mu.Unlock()
		return
	}
	t.Fatal("browser session missing")
}

func retryOriginal(t *testing.T, f *platformHTTPFixture, req jobs.Request, status, result string) jobs.Job {
	t.Helper()
	job, _, err := f.manager.Submit(context.Background(), f.alice.Owner(), req, func(context.Context, jobs.Request, func(jobs.Event)) (jobs.Outcome, error) {
		return jobs.Outcome{Status: status, Result: json.RawMessage(result)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return f.waitJob(t, job.ID)
}
