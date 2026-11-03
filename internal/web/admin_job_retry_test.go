package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"educoder-cli/internal/jobs"
)

func TestAdminJobRetryUsesTargetOwnersClientAndAuditsAcceptedAction(t *testing.T) {
	ownerClient, adminClient := newRecoveryFixtureClient(), newRecoveryFixtureClient()
	var calls atomic.Int64
	observations := make(chan struct {
		owner   string
		request jobs.Request
	}, 1)
	f := newPlatformHTTPFixture(t, func(_ context.Context, client EducoderClient, owner string, req jobs.Request, _ func(jobs.Event)) (jobs.Outcome, error) {
		if client != ownerClient || client == adminClient {
			t.Error("administrator retry used a different account's client")
		}
		calls.Add(1)
		observations <- struct {
			owner   string
			request jobs.Request
		}{owner, req}
		return jobs.Outcome{Status: jobs.Failed, ErrorCode: "model_error", Result: json.RawMessage(`{"status":"model_error","diagnostics":["operator-only-marker"]}`)}, nil
	})
	installRecoveryClient(t, f, f.aliceClient, ownerClient)
	installRecoveryClient(t, f, f.adminClient, adminClient)
	req := jobs.Request{Mode: "submit", Scope: "course", Course: "C"}
	original := recoveryOriginal(t, f, req, jobs.Failed, `{"status":"generation_stalled"}`)
	listed := f.request(t, f.adminClient, "GET", "/api/admin/jobs/"+original.ID, "")
	if listed.StatusCode != http.StatusOK || !strings.Contains(listed.Body, `"can_retry":true`) || !strings.Contains(listed.Body, `"can_recover":false`) {
		t.Fatalf("eligible admin job has no explicit retry capability: %s", listed.Body)
	}
	path := "/api/admin/jobs/" + original.ID + "/retry"
	for _, client := range []*http.Client{f.server.Client(), f.bobClient, f.aliceClient} {
		response := f.request(t, client, "POST", path, "{}")
		want := http.StatusForbidden
		if client == f.server.Client() {
			want = http.StatusUnauthorized
		}
		if response.StatusCode != want {
			t.Fatalf("non-admin retry status=%d want=%d", response.StatusCode, want)
		}
	}
	for _, body := range []string{`{"owner":"other"}`, `{"course":"OTHER"}`, `{"homework_id":99}`, `{"scope":"all"}`, `{"mode":"selftest"}`, `{"idempotency_key":"new-key"}`, `{"recovery_of":"other"}`, `null`, `[]`, `{} {}`, strings.Repeat(" ", 1025) + `{}`} {
		if got := f.request(t, f.adminClient, "POST", path, body); got.StatusCode != http.StatusBadRequest {
			t.Fatalf("argument injection admitted: %q %d %s", body, got.StatusCode, got.Body)
		}
	}
	f.app.jobs.modelConfigured = false
	if got := f.request(t, f.adminClient, "POST", path, "{}"); got.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured model admitted: %d %s", got.StatusCode, got.Body)
	}
	f.app.jobs.modelConfigured = true
	audits, err := f.platform.ListAudit(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, audit := range audits {
		if audit.Action == "job.retry" {
			t.Fatal("rejected action produced retry audit")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("rejected request executed a task")
	}
	response := f.request(t, f.adminClient, "POST", path+"?owner=other&course=OTHER", " {} ")
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("retry status=%d body=%s", response.StatusCode, response.Body)
	}
	var accepted adminJobResponse
	if json.Unmarshal([]byte(response.Body), &struct {
		Data *adminJobResponse `json:"data"`
	}{Data: &accepted}) != nil || accepted.OwnerID != f.alice.Owner() || accepted.Context.User == nil || accepted.Context.User.Name != f.alice.Name {
		t.Fatalf("retry did not return the admin response shape: %s", response.Body)
	}
	child := f.waitJob(t, accepted.ID)
	if calls.Load() != 1 {
		t.Fatalf("accepted retry executions=%d", calls.Load())
	}
	observed := <-observations
	if calls.Load() != 1 || observed.owner != f.alice.Owner() || observed.request.Scope != req.Scope || observed.request.Course != req.Course || observed.request.IdempotencyKey != "retry-job:"+original.ID || child.Request != observed.request {
		t.Fatalf("owner/target/key changed: owner=%s request=%+v child=%+v calls=%d", observed.owner, observed.request, child.Request, calls.Load())
	}
	audits, err = f.platform.ListAudit(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	matching := 0
	for _, audit := range audits {
		if audit.Action == "job.retry" && audit.TargetID == original.ID && audit.ActorID == f.admin.ID {
			matching++
		}
	}
	if matching != 1 {
		t.Fatalf("accepted retry audit count=%d", matching)
	}
	consumer := f.request(t, f.aliceClient, "GET", "/api/jobs/"+child.ID, "")
	assertConsumerBoundary(t, consumer.Body)
	if strings.Contains(consumer.Body, "operator-only-marker") {
		t.Fatal("administrator detail leaked to consumer response")
	}
}

func TestAdminJobRetryRechecksOwnerBeforeQueuedExecution(t *testing.T) {
	for _, state := range []string{"expired", "disabled"} {
		t.Run(state, func(t *testing.T) {
			var calls atomic.Int64
			f := newPlatformHTTPFixture(t, func(context.Context, EducoderClient, string, jobs.Request, func(jobs.Event)) (jobs.Outcome, error) {
				calls.Add(1)
				return jobs.Outcome{Status: jobs.Succeeded}, nil
			})
			original := recoveryOriginal(t, f, jobs.Request{Mode: "submit", Scope: "course", Course: "C"}, jobs.Failed, `{}`)
			entered, release := make(chan struct{}, 2), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			for i := int64(100); i < 102; i++ {
				_, _, err := f.manager.Submit(context.Background(), f.alice.Owner(), jobs.Request{Mode: "submit", Scope: "homework", Course: "OTHER", HomeworkID: i}, func(context.Context, jobs.Request, func(jobs.Event)) (jobs.Outcome, error) {
					entered <- struct{}{}
					<-release
					return jobs.Outcome{Status: jobs.Succeeded}, nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				select {
				case <-entered:
				case <-time.After(3 * time.Second):
					t.Fatal("owner slots did not fill")
				}
			}
			response := f.request(t, f.adminClient, "POST", "/api/admin/jobs/"+original.ID+"/retry", "{}")
			if response.StatusCode != http.StatusAccepted {
				t.Fatal(response.Body)
			}
			child := decodeJob(t, response)
			if child.Status != jobs.Queued {
				t.Fatalf("retry bypassed owner concurrency: %s", child.Status)
			}
			want := "ACCESS_REVOKED"
			if state == "disabled" {
				if err := f.platform.SetStatus(context.Background(), f.admin.ID, f.alice.ID, "disabled"); err != nil {
					t.Fatal(err)
				}
			} else {
				session, ok := f.app.store.sessionForOwner(f.alice.Owner())
				if !ok {
					t.Fatal("owner session missing")
				}
				previousNow := f.app.store.now
				f.app.store.now = func() time.Time { return session.ExpiresAt.Add(time.Second) }
				defer func() { f.app.store.now = previousNow }()
				want = "OWNER_LOGIN_REQUIRED"
			}
			once.Do(func() { close(release) })
			ended := f.waitJob(t, child.ID)
			if ended.Status != jobs.Failed || ended.ErrorCode != want || calls.Load() != 0 {
				t.Fatalf("queued retry ignored updated access: status=%s error=%s executions=%d", ended.Status, ended.ErrorCode, calls.Load())
			}
		})
	}
}

func TestAdminJobRetryRequiresActiveOwnerAndLiveSession(t *testing.T) {
	for _, state := range []string{"disabled", "expired", "logged_out"} {
		t.Run(state, func(t *testing.T) {
			var calls atomic.Int64
			f := newPlatformHTTPFixture(t, func(context.Context, EducoderClient, string, jobs.Request, func(jobs.Event)) (jobs.Outcome, error) {
				calls.Add(1)
				return jobs.Outcome{Status: jobs.Succeeded}, nil
			})
			original := recoveryOriginal(t, f, jobs.Request{Mode: "submit", Scope: "course", Course: "C"}, jobs.Failed, `{}`)
			code := "OWNER_LOGIN_REQUIRED"
			switch state {
			case "disabled":
				if err := f.platform.SetStatus(context.Background(), f.admin.ID, f.alice.ID, "disabled"); err != nil {
					t.Fatal(err)
				}
				code = "OWNER_ACCESS_REVOKED"
			case "expired":
				f.app.store.mu.Lock()
				for id, session := range f.app.store.sessions {
					if sessionJobOwner(session) == f.alice.Owner() {
						session.ExpiresAt = time.Now().Add(-time.Hour)
						f.app.store.sessions[id] = session
					}
				}
				f.app.store.mu.Unlock()
			case "logged_out":
				f.app.store.DeleteUser(f.alice.ID)
			}
			response := f.request(t, f.adminClient, "POST", "/api/admin/jobs/"+original.ID+"/retry", "{}")
			if response.StatusCode != http.StatusConflict || !strings.Contains(response.Body, code) || calls.Load() != 0 {
				t.Fatalf("unavailable owner executed: %d %s calls=%d", response.StatusCode, response.Body, calls.Load())
			}
		})
	}
}

func TestAdminAndConsumerRetryShareDurableIdempotency(t *testing.T) {
	for _, firstAdmin := range []bool{false, true} {
		t.Run(map[bool]string{false: "consumer_first", true: "admin_first"}[firstAdmin], func(t *testing.T) {
			var calls atomic.Int64
			f := newPlatformHTTPFixture(t, func(context.Context, EducoderClient, string, jobs.Request, func(jobs.Event)) (jobs.Outcome, error) {
				calls.Add(1)
				return jobs.Outcome{Status: jobs.Failed}, nil
			})
			original := recoveryOriginal(t, f, jobs.Request{Mode: "submit", Scope: "homework", Course: "C", HomeworkID: 42}, jobs.Failed, `{}`)
			adminPath, consumerPath := "/api/admin/jobs/"+original.ID+"/retry", "/api/jobs/"+original.ID+"/retry"
			paths := []struct {
				client *http.Client
				path   string
			}{{f.aliceClient, consumerPath}, {f.adminClient, adminPath}, {f.aliceClient, consumerPath}, {f.adminClient, adminPath}}
			if firstAdmin {
				paths[0], paths[1] = paths[1], paths[0]
			}
			childID := ""
			for _, route := range paths {
				response := f.request(t, route.client, "POST", route.path, "{}")
				if response.StatusCode != http.StatusAccepted {
					t.Fatalf("retry status=%d body=%s", response.StatusCode, response.Body)
				}
				child := decodeJob(t, response)
				if childID != "" && child.ID != childID {
					t.Fatal("admin and consumer created separate retries")
				}
				childID = child.ID
				f.waitJob(t, childID)
			}
			if calls.Load() != 1 {
				t.Fatalf("deduplicated retry executed %d times", calls.Load())
			}
		})
	}
}

func TestAdminJobRetryCapabilitiesRejectUnknownNestedAndUnsupportedRequests(t *testing.T) {
	for _, tc := range []struct {
		name, status, result string
		request              jobs.Request
	}{
		{"unknown nested", jobs.Failed, `{"homeworks":[{"result":{"levels":[{"status":"submission_uncertain"}]}}]}`, jobs.Request{Mode: "submit", Scope: "course", Course: "C"}},
		{"unknown result", jobs.Uncertain, `{}`, jobs.Request{Mode: "submit", Scope: "course", Course: "C"}},
		{"completed", jobs.Succeeded, `{}`, jobs.Request{Mode: "submit", Scope: "course", Course: "C"}},
		{"exercise", jobs.Failed, `{}`, jobs.Request{Mode: "submit", Scope: "exercise", Course: "C", ExerciseID: 4}},
		{"collection", jobs.Failed, `{}`, jobs.Request{Mode: "collect_verified"}},
		{"single level", jobs.Failed, `{}`, jobs.Request{Mode: "submit", Scope: "current", Course: "C", HomeworkID: 42, Game: "G"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			f := newPlatformHTTPFixture(t, func(context.Context, EducoderClient, string, jobs.Request, func(jobs.Event)) (jobs.Outcome, error) {
				calls.Add(1)
				return jobs.Outcome{Status: jobs.Succeeded}, nil
			})
			original := recoveryOriginal(t, f, tc.request, tc.status, tc.result)
			response := f.request(t, f.adminClient, "POST", "/api/admin/jobs/"+original.ID+"/retry", "{}")
			if response.StatusCode != http.StatusConflict || !strings.Contains(response.Body, "JOB_NOT_RETRYABLE") || calls.Load() != 0 {
				t.Fatalf("protected task retried: %d %s", response.StatusCode, response.Body)
			}
			views, err := f.app.adminJobResponses(context.Background(), []jobs.AdminRecord{{Job: original, OwnerID: f.alice.Owner()}})
			if err != nil || len(views) != 1 || views[0].CanRetry || views[0].CanRecover != canRecoverJob(original) {
				t.Fatalf("admin capabilities changed recovery boundary: %+v %v", views, err)
			}
			wire, err := json.Marshal(views[0])
			if err != nil || !strings.Contains(string(wire), `"can_retry":false`) {
				t.Fatal("false retry capability missing from admin response")
			}
		})
	}
}

func TestAdminJobRetryNeverDuplicatesAnActiveTask(t *testing.T) {
	var calls atomic.Int64
	f := newPlatformHTTPFixture(t, func(context.Context, EducoderClient, string, jobs.Request, func(jobs.Event)) (jobs.Outcome, error) {
		calls.Add(1)
		return jobs.Outcome{Status: jobs.Succeeded}, nil
	})
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	original, _, err := f.manager.Submit(context.Background(), f.alice.Owner(), jobs.Request{Mode: "submit", Scope: "course", Course: "C"}, func(context.Context, jobs.Request, func(jobs.Event)) (jobs.Outcome, error) {
		close(entered)
		<-release
		return jobs.Outcome{Status: jobs.Succeeded}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("original did not start")
	}
	response := f.request(t, f.adminClient, "POST", "/api/admin/jobs/"+original.ID+"/retry", "{}")
	if response.StatusCode != http.StatusConflict || !strings.Contains(response.Body, "JOB_NOT_RETRYABLE") || calls.Load() != 0 {
		t.Fatalf("active task duplicated: %d %s", response.StatusCode, response.Body)
	}
}
