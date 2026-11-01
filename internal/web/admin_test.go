package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/jobs"
	"educoder-cli/internal/platform"
	"educoder-cli/internal/questionbank"
)

type platformHTTPFixture struct {
	server                              *httptest.Server
	app                                 *Server
	manager                             *jobs.Manager
	platform                            *platform.Store
	bank                                *questionbank.Store
	platformPath                        string
	admin, alice, bob                   platform.User
	adminClient, aliceClient, bobClient *http.Client
}

func newPlatformHTTPFixture(t *testing.T, execute func(context.Context, EducoderClient, string, jobs.Request, func(jobs.Event)) (jobs.Outcome, error)) *platformHTTPFixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	f := &platformHTTPFixture{platformPath: filepath.Join(dir, "platform.sqlite")}
	var err error
	f.platform, err = platform.Open(f.platformPath, platform.Options{AdminUserIDs: []int64{1}})
	if err != nil {
		t.Fatal(err)
	}
	f.manager, err = jobs.Open(ctx, jobs.Config{Path: filepath.Join(dir, "jobs.sqlite"), Workers: 4, QueueSize: 16, PerOwnerPending: 8, PerOwnerRunning: 2})
	if err != nil {
		f.platform.Close()
		t.Fatal(err)
	}
	f.bank, err = questionbank.Open(filepath.Join(dir, "bank.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	f.app = NewServerWithFactory(DefaultConfig(), func(string) EducoderClient { return &fakeClient{} })
	f.app.AttachPlatform(f.platform)
	if execute == nil {
		execute = func(context.Context, EducoderClient, string, jobs.Request, func(jobs.Event)) (jobs.Outcome, error) {
			return jobs.Outcome{Status: jobs.Succeeded}, nil
		}
	}
	f.app.AttachJobs(&JobRuntime{manager: f.manager, bank: f.bank, run: execute, modelConfigured: true, modelConcurrency: 7})
	f.server = httptest.NewServer(f.app)
	t.Cleanup(func() {
		f.server.Close()
		closeCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if err := f.manager.Close(closeCtx); err != nil {
			t.Error(err)
		}
		f.bank.Close()
		f.platform.Close()
	})
	f.admin, err = f.platform.UpsertLogin(ctx, platform.Identity{UpstreamID: 1, Login: "administrator", Name: "管理员"})
	if err != nil {
		t.Fatal(err)
	}
	f.alice, err = f.platform.UpsertLogin(ctx, platform.Identity{UpstreamID: 2, Login: "alice", Name: "同学 A"})
	if err != nil {
		t.Fatal(err)
	}
	f.bob, err = f.platform.UpsertLogin(ctx, platform.Identity{UpstreamID: 3, Login: "bob", Name: "同学 B"})
	if err != nil {
		t.Fatal(err)
	}
	f.adminClient = f.browser(t, f.admin)
	f.aliceClient = f.browser(t, f.alice)
	f.bobClient = f.browser(t, f.bob)
	return f
}

func (f *platformHTTPFixture) browser(t *testing.T, u platform.User) *http.Client {
	t.Helper()
	id, err := f.app.Store().createPrincipal(&fakeClient{}, educoder.User{UserID: u.UpstreamID, Login: u.Login, Name: u.Name}, "b", u)
	if err != nil {
		t.Fatal(err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := http.NewRequest(http.MethodGet, f.server.URL, nil)
	jar.SetCookies(r.URL, []*http.Cookie{{Name: sessionCookieName, Value: id, Path: "/"}})
	return &http.Client{Jar: jar}
}

func (f *platformHTTPFixture) request(t *testing.T, client *http.Client, method, path, body string) jsonResult {
	t.Helper()
	return doJSON(t, client, method, f.server.URL+path, body)
}

func (f *platformHTTPFixture) waitJob(t *testing.T, id string) jobs.Job {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		record, err := f.manager.AdminGet(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		switch record.Status {
		case jobs.Queued, jobs.Running:
			time.Sleep(5 * time.Millisecond)
		default:
			return record.Job
		}
	}
	t.Fatal("job did not reach terminal state")
	return jobs.Job{}
}

func TestAllAdminRoutesRejectUnauthenticatedAndOrdinaryAccounts(t *testing.T) {
	f := newPlatformHTTPFixture(t, nil)
	routes := []struct{ method, path, body string }{
		{"GET", "/api/admin/users", ""}, {"GET", "/api/admin/audit", ""}, {"GET", "/api/admin/jobs", ""}, {"GET", "/api/admin/jobs/example", ""}, {"GET", "/api/admin/overview", ""}, {"GET", "/api/admin/usage", ""},
		{"POST", "/api/admin/users/" + f.bob.ID + "/status", `{"status":"disabled"}`}, {"POST", "/api/admin/jobs/example/cancel", ""}, {"GET", "/api/admin/unknown", ""},
	}
	for _, route := range routes {
		for _, browser := range []struct {
			client *http.Client
			status int
		}{{f.server.Client(), 401}, {f.aliceClient, 403}} {
			got := f.request(t, browser.client, route.method, route.path, route.body)
			if got.StatusCode != browser.status {
				t.Fatalf("%s %s = %d want %d: %s", route.method, route.path, got.StatusCode, browser.status, got.Body)
			}
		}
	}
	bob, err := f.platform.LookupUser(context.Background(), f.bob.ID)
	if err != nil || bob.Status != "active" {
		t.Fatal("rejected admin request changed user status")
	}

}

func TestAdminGetsOperatorDiagnosticsAndActualLedgerUsage(t *testing.T) {
	f := newPlatformHTTPFixture(t, func(_ context.Context, _ EducoderClient, _ string, _ jobs.Request, report func(jobs.Event)) (jobs.Outcome, error) {
		report(jobs.Event{Phase: "repairing", Message: "operator-event-marker", Progress: &jobs.BatchProgress{TotalLevels: 6, CompletedLevels: 3, FailedLevels: 1, CurrentTitle: "第四关"}})
		return jobs.Outcome{Status: jobs.Failed, ErrorCode: "model_error", Message: "operator-message-marker", Result: json.RawMessage(`{"total_levels":6,"completed_levels":3,"diagnostics":["operator-diagnostic-marker"],"model_calls":99,"cost_micro_usd":99999}`)}, nil
	})
	created := f.request(t, f.aliceClient, "POST", "/api/jobs", `{"scope":"homework","mode":"submit","course":"COURSE_A","homework_id":42}`)
	if created.StatusCode != 202 {
		t.Fatal(created.Body)
	}
	id := decodeJob(t, created).ID
	f.waitJob(t, id)
	for _, path := range []string{"/api/admin/jobs", "/api/admin/jobs/" + id} {
		got := f.request(t, f.adminClient, "GET", path, "")
		if got.StatusCode != 200 {
			t.Fatal(got.Body)
		}
		for _, expected := range []string{"operator-diagnostic-marker", "operator-event-marker", `"error_code":"model_error"`, `"cost_micro_usd":99999`, f.alice.Owner()} {
			if !strings.Contains(got.Body, expected) {
				t.Fatalf("admin %s missing %s: %s", path, expected, got.Body)
			}
		}
	}
	ctx := context.Background()
	now := time.Now().UTC()
	if err := f.bank.PutRun(ctx, questionbank.RunRecord{ID: "ledger-fixture", Owner: f.alice.Owner(), Status: "failed", Data: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, usage := range []questionbank.Usage{{RunID: "ledger-fixture", Provider: "fixture", Model: "fixture-model", PricingVersion: "test", InputTokens: 20, OutputTokens: 3, ReservedMicroUSD: 10, CostMicroUSD: 8, Status: "settled"}, {RunID: "ledger-fixture", Provider: "fixture", Model: "fixture-model", PricingVersion: "test", InputTokens: 5, OutputTokens: 1, ReservedMicroUSD: 17, Status: "unknown"}} {
		if err := f.bank.RecordUsage(ctx, usage); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"/api/admin/usage", "/api/admin/overview"} {
		got := f.request(t, f.adminClient, "GET", path, "")
		if got.StatusCode != 200 {
			t.Fatal(got.Body)
		}
		var envelope struct {
			Data struct {
				Usage questionbank.UsageSummary `json:"usage"`
				Users platform.Stats            `json:"users"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(got.Body), &envelope); err != nil {
			t.Fatal(err)
		}
		u := envelope.Data.Usage
		if u.ModelCalls != 2 || u.InputTokens != 25 || u.OutputTokens != 4 || u.CostMicroUSD != 8 || u.SettledCostMicroUSD != 8 || u.ReservedMicroUSD != 17 || u.UnknownCalls != 1 {
			t.Fatalf("usage inferred from job summary instead of ledger: %+v", u)
		}
		if envelope.Data.Users.TotalUsers != 3 || !strings.Contains(got.Body, `"model_concurrency":7`) {
			t.Fatalf("overview not actual capacity/user state: %s", got.Body)
		}
	}
}

func TestAdminDisableCancelsRunningOwnerAndRevokesSessions(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	f := newPlatformHTTPFixture(t, func(ctx context.Context, _ EducoderClient, _ string, _ jobs.Request, report func(jobs.Event)) (jobs.Outcome, error) {
		report(jobs.Event{Phase: "reading", Message: "fixture running"})
		close(started)
		<-ctx.Done()
		close(stopped)
		return jobs.Outcome{}, ctx.Err()
	})
	created := f.request(t, f.aliceClient, "POST", "/api/jobs", `{"scope":"homework","mode":"submit","course":"COURSE_A","homework_id":42}`)
	if created.StatusCode != 202 {
		t.Fatal(created.Body)
	}
	id := decodeJob(t, created).ID
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("executor did not start")
	}
	disabled := f.request(t, f.adminClient, "POST", "/api/admin/users/"+f.alice.ID+"/status", `{"status":"disabled"}`)
	if disabled.StatusCode != 200 {
		t.Fatal(disabled.Body)
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("disabled account executor kept running")
	}
	job := f.waitJob(t, id)
	if job.Status != jobs.Canceled {
		t.Fatalf("disabled non-mutating job status = %s", job.Status)
	}
	for _, path := range []string{"/api/auth/me", "/api/jobs"} {
		got := f.request(t, f.aliceClient, "GET", path, "")
		if got.StatusCode != 401 {
			t.Fatalf("disabled session retained %s: %d", path, got.StatusCode)
		}
	}
	if got := f.request(t, f.bobClient, "GET", "/api/auth/me", ""); got.StatusCode != 200 {
		t.Fatal("disable crossed tenants")
	}
}

func TestDisabledOwnerCannotStartExecutor(t *testing.T) {
	f := newPlatformHTTPFixture(t, nil)
	ctx := context.Background()
	if err := f.platform.SetStatus(ctx, f.admin.ID, f.alice.ID, "disabled"); err != nil {
		t.Fatal(err)
	}
	called := false
	out, err := f.app.runAuthorized(ctx, f.alice.Owner(), func(context.Context) (jobs.Outcome, error) {
		called = true
		return jobs.Outcome{Status: jobs.Succeeded}, nil
	})
	if err != nil || called || out.Status != jobs.Failed || out.ErrorCode != "ACCESS_REVOKED" {
		t.Fatalf("disabled owner started execution: called=%v outcome=%+v err=%v", called, out, err)
	}
}

func TestRetiredCardAdminAPIsReturnNotFound(t *testing.T) {
	f := newPlatformHTTPFixture(t, nil)
	for _, route := range []struct{ method, path, body string }{
		{"GET", "/api/admin/cards", ""},
		{"POST", "/api/admin/cards", `{"label":"retired"}`},
		{"POST", "/api/admin/cards/historical-fixture/revoke", ""},
	} {
		got := f.request(t, f.adminClient, route.method, route.path, route.body)
		if got.StatusCode != http.StatusNotFound {
			t.Fatalf("retired %s %s still available: %d %s", route.method, route.path, got.StatusCode, got.Body)
		}
	}
	got := f.request(t, f.adminClient, "GET", "/api/admin/overview", "")
	if got.StatusCode != http.StatusOK || strings.Contains(got.Body, "access_card") {
		t.Fatalf("overview contains retired card policy: %s", got.Body)
	}
	audit, err := f.platform.ListAudit(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range audit {
		if strings.HasPrefix(event.Action, "card.") {
			t.Fatal("removed API changed card state")
		}
	}
}

func TestDetachedExecutionRechecksDisabledAccountAfterLogout(t *testing.T) {
	ctx := context.Background()
	ps, err := platform.Open(filepath.Join(t.TempDir(), "platform.sqlite"), platform.Options{AdminUserIDs: []int64{1}})
	if err != nil {
		t.Fatal(err)
	}
	defer ps.Close()
	admin, err := ps.UpsertLogin(ctx, platform.Identity{UpstreamID: 1, Login: "administrator"})
	if err != nil {
		t.Fatal(err)
	}
	u, err := ps.UpsertLogin(ctx, platform.Identity{UpstreamID: 2, Login: "student"})
	if err != nil {
		t.Fatal(err)
	}
	app := NewServerWithFactory(DefaultConfig(), func(string) EducoderClient { return &fakeClient{} })
	app.AttachPlatform(ps)
	token, err := app.Store().createPrincipal(&fakeClient{}, educoder.User{UserID: u.UpstreamID, Login: u.Login}, "b", u)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	finished := make(chan error, 1)
	runCtx, cancel := context.WithTimeout(ctx, 7*time.Second)
	defer cancel()
	go func() {
		_, err := app.runAuthorized(runCtx, u.Owner(), func(ctx context.Context) (jobs.Outcome, error) {
			close(started)
			<-ctx.Done()
			return jobs.Outcome{}, ctx.Err()
		})
		finished <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("authorized detached work did not start")
	}
	logoutReq := httptest.NewRequest(http.MethodPost, "http://service.example/api/auth/logout", nil)
	logoutReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	logout := httptest.NewRecorder()
	app.ServeHTTP(logout, logoutReq)
	if logout.Code != 200 || app.Store().Len() != 0 {
		t.Fatal("logout did not remove browser session")
	}
	select {
	case <-finished:
		t.Fatal("logout itself canceled valid detached work")
	default:
	}
	if err = ps.SetStatus(ctx, admin.ID, u.ID, "disabled"); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("disabled account did not cooperatively cancel execution: %v", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("detached work ignored disabled account")
	}
}
