package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/jobs"
)

type recoveryFixtureClient struct {
	*fakeClient
	courses  []educoder.Course
	homework []educoder.Homework
	progress *educoder.HomeworkProgress
	readErr  error
	reads    atomic.Int64
}

func newRecoveryFixtureClient() *recoveryFixtureClient {
	return &recoveryFixtureClient{fakeClient: &fakeClient{}, courses: []educoder.Course{{Identifier: "C"}}, homework: []educoder.Homework{{ID: 42, Name: "作业", ChallengeCount: 2}}, progress: &educoder.HomeworkProgress{Challenges: []educoder.ChallengeProgress{{ChallengeID: 1, Name: "关卡一", ChallengeScore: "10", GameScore: "10"}, {ChallengeID: 2, Name: "关卡二", ChallengeScore: "10", GameScore: "0"}}}}
}

func (c *recoveryFixtureClient) Courses(context.Context, string) ([]educoder.Course, error) {
	c.reads.Add(1)
	return c.courses, c.readErr
}
func (c *recoveryFixtureClient) Homeworks(context.Context, string, string) ([]educoder.Homework, error) {
	c.reads.Add(1)
	return c.homework, c.readErr
}
func (c *recoveryFixtureClient) HomeworkProgress(context.Context, string, int64) (*educoder.HomeworkProgress, error) {
	c.reads.Add(1)
	return c.progress, c.readErr
}

func installRecoveryClient(t *testing.T, f *platformHTTPFixture, browser *http.Client, client EducoderClient) {
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

func recoveryOriginal(t *testing.T, f *platformHTTPFixture, req jobs.Request, status, result string) jobs.Job {
	t.Helper()
	job, _, err := f.manager.Submit(context.Background(), f.alice.Owner(), req, func(context.Context, jobs.Request, func(jobs.Event)) (jobs.Outcome, error) {
		return jobs.Outcome{Status: status, Result: json.RawMessage(result)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return f.waitJob(t, job.ID)
}

func TestConsumerRecoveryCapabilityKeepsRetryProtection(t *testing.T) {
	for _, tc := range []struct {
		name, status, result string
		req                  jobs.Request
		want                 bool
	}{
		{"uncertain homework", jobs.Uncertain, `{"status":"uncertain"}`, jobs.Request{Mode: "submit", Scope: "homework", Course: "C", HomeworkID: 42}, true},
		{"failed course uncertain leaf", jobs.Failed, `{"homeworks":[{"result":{"status":"verification_unconfirmed"}}]}`, jobs.Request{Mode: "submit", Scope: "course", Course: "C"}, true},
		{"uncertain all", jobs.Uncertain, `{}`, jobs.Request{Mode: "submit", Scope: "all"}, true},
		{"known failure", jobs.Failed, `{"status":"model_error"}`, jobs.Request{Mode: "submit", Scope: "course", Course: "C"}, false},
		{"running", jobs.Running, `{"status":"uncertain"}`, jobs.Request{Mode: "submit", Scope: "course", Course: "C"}, false},
		{"completed", jobs.Succeeded, `{"status":"uncertain"}`, jobs.Request{Mode: "submit", Scope: "course", Course: "C"}, false},
		{"exam", jobs.Uncertain, `{}`, jobs.Request{Mode: "submit", Scope: "exercise", Course: "C", ExerciseID: 42}, false},
		{"collection", jobs.Uncertain, `{}`, jobs.Request{Mode: "collect_verified"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := jobs.Job{Status: tc.status, Request: tc.req, Result: json.RawMessage(tc.result)}
			public := toConsumerJob(original)
			if public.CanRecover != tc.want || tc.want && public.CanRetry {
				t.Fatalf("recover=%v retry=%v want recover=%v", public.CanRecover, public.CanRetry, tc.want)
			}
			body, _ := json.Marshal(public)
			assertConsumerBoundary(t, string(body))
		})
	}
}

func TestRecoveryPreflightRequiresExactFreshEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result string
		mutate func(*recoveryFixtureClient)
		want   bool
	}{
		{"specific uncertain passed", `{"homeworks":[{"course":"C","homework_id":42,"result":{"status":"verification_unconfirmed","levels":[{"challenge_id":1,"status":"verification_unconfirmed"},{"challenge_id":2,"status":"planned"}]}}]}`, nil, true},
		{"specific uncertain unconfirmed", `{"homeworks":[{"course":"C","homework_id":42,"result":{"levels":[{"challenge_id":2,"status":"uncertain"}]}}]}`, nil, false},
		{"unknown challenge id", `{"homeworks":[{"course":"C","homework_id":42,"result":{"levels":[{"challenge_id":99,"status":"uncertain"}]}}]}`, nil, false},
		{"missing child id requires whole completion", `{"homeworks":[{"course":"C","homework_id":42,"result":{"status":"uncertain"}}]}`, nil, false},
		{"whole complete without child identity", `{"homeworks":[{"course":"C","homework_id":42,"result":{"status":"uncertain"}}]}`, func(c *recoveryFixtureClient) { c.progress.Challenges[1].GameScore = "10" }, true},
		{"canonical optional", `{"homeworks":[{"course":"C","homework_id":42,"result":{"levels":[{"challenge_id":2,"status":"uncertain"}]}}]}`, func(c *recoveryFixtureClient) { c.progress.Challenges[1].RequiredStatus = "optional" }, true},
		{"closed assignment", `{"homeworks":[{"course":"C","homework_id":42,"result":{"status":"uncertain"}}]}`, func(c *recoveryFixtureClient) { c.homework[0].Status = []string{"已截止"} }, true},
		{"short roster", `{"homeworks":[{"course":"C","homework_id":42,"result":{"levels":[{"challenge_id":1,"status":"uncertain"}]}}]}`, func(c *recoveryFixtureClient) { c.progress.Challenges = c.progress.Challenges[:1] }, false},
		{"duplicate roster identity", `{"homeworks":[{"course":"C","homework_id":42,"result":{"status":"uncertain"}}]}`, func(c *recoveryFixtureClient) { c.progress.Challenges[1].ChallengeID = 1 }, false},
		{"membership changed", `{"homeworks":[{"course":"C","homework_id":42,"result":{"status":"uncertain"}}]}`, func(c *recoveryFixtureClient) { c.courses = []educoder.Course{{Identifier: "OTHER"}} }, false},
		{"other homework", `{"homeworks":[{"course":"C","homework_id":99,"result":{"status":"uncertain"}}]}`, nil, false},
		{"other course", `{"homeworks":[{"course":"OTHER","homework_id":42,"result":{"status":"uncertain"}}]}`, nil, false},
		{"missing coursewide identity", `{"status":"uncertain"}`, nil, false},
		{"read unavailable", `{"homeworks":[{"course":"C","homework_id":42,"result":{"status":"uncertain"}}]}`, func(c *recoveryFixtureClient) { c.readErr = errors.New("offline") }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newRecoveryFixtureClient()
			if tc.mutate != nil {
				tc.mutate(client)
			}
			job := jobs.Job{Status: jobs.Uncertain, Request: jobs.Request{Mode: "submit", Scope: "course", Course: "C"}, Result: json.RawMessage(tc.result)}
			err := recoveryPreflight(context.Background(), client, job)
			if (err == nil) != tc.want {
				t.Fatalf("preflight err=%v want allowed=%v", err, tc.want)
			}
		})
	}
}

func TestHTTPRecoveryUsesCurrentOwnerClientAndScopeWithIdempotency(t *testing.T) {
	var calls atomic.Int64
	current := newRecoveryFixtureClient()
	f := newPlatformHTTPFixture(t, func(_ context.Context, client EducoderClient, owner string, req jobs.Request, _ func(jobs.Event)) (jobs.Outcome, error) {
		calls.Add(1)
		if client != current || !strings.HasPrefix(owner, "platform:") || req.Scope != "course" || req.Course != "C" || req.HomeworkID != 0 || req.Game != "" || !strings.HasPrefix(req.IdempotencyKey, "recover-job:") || current.reads.Load() < 3 {
			return jobs.Outcome{Status: jobs.Failed, Message: "wrong recovery scope or client"}, nil
		}
		return jobs.Outcome{Status: jobs.Succeeded}, nil
	})
	installRecoveryClient(t, f, f.aliceClient, current)
	original := recoveryOriginal(t, f, jobs.Request{Mode: "submit", Scope: "course", Course: "C"}, jobs.Failed, `{"status":"needs_service","total_homeworks":19,"completed_homeworks":13,"homeworks":[{"course":"C","homework_id":42,"result":{"levels":[{"challenge_id":1,"status":"verification_unconfirmed"}]}}]}`)
	path := "/api/jobs/" + original.ID + "/recover"
	if got := f.request(t, f.bobClient, "POST", path, "{}"); got.StatusCode != 404 {
		t.Fatal("cross owner recovery admitted")
	}
	if got := f.request(t, f.aliceClient, "POST", path, `{"course":"OTHER"}`); got.StatusCode != 400 {
		t.Fatal("recovery accepted scope override")
	}
	response := f.request(t, f.aliceClient, "POST", path, "{}")
	if response.StatusCode != 202 {
		t.Fatal(response.Body)
	}
	childID := decodeJob(t, response).ID
	if f.waitJob(t, childID).Status != jobs.Succeeded {
		t.Fatal("recovery did not use current authenticated client and original scope")
	}
	var wait sync.WaitGroup
	failures := make(chan string, 6)
	for i := 0; i < 6; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			req, _ := http.NewRequest("POST", f.server.URL+path, strings.NewReader("{}"))
			resp, err := f.aliceClient.Do(req)
			if err != nil {
				failures <- err.Error()
				return
			}
			defer resp.Body.Close()
			var body struct {
				Data consumerJob `json:"data"`
			}
			if json.NewDecoder(resp.Body).Decode(&body) != nil || resp.StatusCode != 202 || body.Data.ID != childID || resp.Header.Get("X-Job-Reused") != "true" {
				failures <- "recovery click not reused"
			}
		}()
	}
	wait.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
	if calls.Load() != 1 {
		t.Fatalf("recovery executions=%d", calls.Load())
	}
	stored, _ := f.manager.Get(context.Background(), f.alice.Owner(), original.ID)
	if string(stored.Result) != string(original.Result) || stored.Status != original.Status {
		t.Fatal("historical original job was rewritten")
	}
}

func TestHTTPUnresolvedRecoveryRemainsRecoverableWithoutRunning(t *testing.T) {
	var calls atomic.Int64
	f := newPlatformHTTPFixture(t, func(context.Context, EducoderClient, string, jobs.Request, func(jobs.Event)) (jobs.Outcome, error) {
		calls.Add(1)
		return jobs.Outcome{Status: jobs.Succeeded}, nil
	})
	current := newRecoveryFixtureClient()
	installRecoveryClient(t, f, f.aliceClient, current)
	original := recoveryOriginal(t, f, jobs.Request{Mode: "submit", Scope: "homework", Course: "C", HomeworkID: 42}, jobs.Uncertain, `{"status":"uncertain","total_levels":2,"completed_levels":1,"levels":[{"challenge_id":2,"status":"uncertain"}]}`)
	response := f.request(t, f.aliceClient, "POST", "/api/jobs/"+original.ID+"/recover", "{}")
	if response.StatusCode != 202 {
		t.Fatal(response.Body)
	}
	child := f.waitJob(t, decodeJob(t, response).ID)
	if calls.Load() != 0 || child.Status != jobs.Failed || child.ErrorCode != "RECOVERY_RECHECK_REQUIRED" || !canRecoverJob(child) || canRetryJob(child) || !strings.Contains(string(child.Result), "未调用模型") {
		t.Fatalf("unresolved recovery changed state: child=%+v calls=%d", child, calls.Load())
	}
	public, _ := json.Marshal(toConsumerJob(child))
	assertConsumerBoundary(t, string(public))
	if strings.Contains(string(public), "关卡 2") || !strings.Contains(string(public), `"can_recover":true`) {
		t.Fatal("consumer recovery boundary invalid")
	}
	current.progress.Challenges[1].GameScore = "10"
	again := f.request(t, f.aliceClient, "POST", "/api/jobs/"+child.ID+"/recover", "{}")
	if again.StatusCode != 202 || f.waitJob(t, decodeJob(t, again).ID).Status != jobs.Succeeded || calls.Load() != 1 {
		t.Fatal("a previous unresolved recovery could not be checked again")
	}
}

func TestAdminRecoveryUsesOwnersSessionAndRequiresLogin(t *testing.T) {
	current := newRecoveryFixtureClient()
	adminUpstream := newRecoveryFixtureClient()
	var calls atomic.Int64
	f := newPlatformHTTPFixture(t, func(_ context.Context, client EducoderClient, owner string, _ jobs.Request, _ func(jobs.Event)) (jobs.Outcome, error) {
		if client != current || owner == "" {
			return jobs.Outcome{Status: jobs.Failed}, nil
		}
		calls.Add(1)
		return jobs.Outcome{Status: jobs.Succeeded}, nil
	})
	installRecoveryClient(t, f, f.aliceClient, current)
	installRecoveryClient(t, f, f.adminClient, adminUpstream)
	original := recoveryOriginal(t, f, jobs.Request{Mode: "submit", Scope: "homework", Course: "C", HomeworkID: 42}, jobs.Uncertain, `{"levels":[{"challenge_id":1,"status":"uncertain"}]}`)
	path := "/api/admin/jobs/" + original.ID + "/recover"
	if got := f.request(t, f.bobClient, "POST", path, "{}"); got.StatusCode != 403 {
		t.Fatal("ordinary account accessed admin recovery")
	}
	response := f.request(t, f.adminClient, "POST", path, "{}")
	if response.StatusCode != 202 || f.waitJob(t, decodeJob(t, response).ID).Status != jobs.Succeeded || calls.Load() != 1 || adminUpstream.reads.Load() != 0 {
		t.Fatalf("admin recovery used wrong session: response=%s calls=%d adminReads=%d", response.Body, calls.Load(), adminUpstream.reads.Load())
	}
	f.app.store.mu.Lock()
	for id, session := range f.app.store.sessions {
		if sessionJobOwner(session) == f.alice.Owner() {
			session.ExpiresAt = time.Now().Add(-time.Hour)
			f.app.store.sessions[id] = session
		}
	}
	f.app.store.mu.Unlock()
	got := f.request(t, f.adminClient, "POST", path, "{}")
	if got.StatusCode != 409 || !strings.Contains(got.Body, "OWNER_LOGIN_REQUIRED") || calls.Load() != 1 {
		t.Fatal("admin substituted its own upstream session after owner expired")
	}
}
