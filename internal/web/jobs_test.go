package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/jobs"
)

func jobsHTTP(t *testing.T, cfg jobs.Config, run func(context.Context, EducoderClient, string, jobs.Request, func(jobs.Event)) (jobs.Outcome, error)) (*httptest.Server, *Server, *jobs.Manager) {
	t.Helper()
	cfg.Path = filepath.Join(t.TempDir(), "jobs.sqlite")
	manager, err := jobs.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	app := NewServerWithFactory(DefaultConfig(), func(string) EducoderClient { return &fakeClient{} })
	app.AttachJobs(&JobRuntime{manager: manager, run: run, modelConfigured: true, modelConcurrency: 2})
	server := httptest.NewServer(app)
	t.Cleanup(func() {
		server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := manager.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return server, app, manager
}

func jobAccount(t *testing.T, server *httptest.Server, login string) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	r := doJSON(t, client, http.MethodPost, server.URL+"/api/auth/login", fmt.Sprintf("{\"login\":%q,\"password\":\"fixture\"}", login))
	if r.StatusCode != http.StatusOK {
		t.Fatalf("login %d %s", r.StatusCode, r.Body)
	}
	return client
}

func decodeJob(t *testing.T, r jsonResult) jobs.Job {
	t.Helper()
	var envelope struct {
		Data jobs.Job `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.Body), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}

func TestHTTPJobsAcceptQuicklyDetachAndEnforceAccountOwnership(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	server, _, manager := jobsHTTP(t, jobs.Config{Workers: 2, QueueSize: 8, PerOwnerPending: 3}, func(ctx context.Context, _ EducoderClient, _ string, _ jobs.Request, report func(jobs.Event)) (jobs.Outcome, error) {
		report(jobs.Event{Phase: "generating", Kind: "choice", Message: "正在生成候选"})
		select {
		case <-release:
			return jobs.Outcome{Status: jobs.Succeeded, Result: json.RawMessage(`{"status":"generated","kind":"choice"}`)}, nil
		case <-ctx.Done():
			return jobs.Outcome{}, ctx.Err()
		}
	})
	alice := jobAccount(t, server, "alice")
	bob := jobAccount(t, server, "bob")
	requestCtx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, server.URL+"/api/jobs", strings.NewReader(`{"course":"C","homework_id":1,"game":"g","mode":"generate_only","idempotency_key":"one"}`))
	if err != nil {
		t.Fatal(err)
	}
	response, err := alice.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Data jobs.Job `json:"data"`
	}
	if err = json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	cancel()
	if response.StatusCode != http.StatusAccepted || envelope.Data.ID == "" {
		t.Fatalf("accepted response=%d %#v", response.StatusCode, envelope)
	}
	id := envelope.Data.ID
	if response.Header.Get("Location") != "/api/jobs/"+id {
		t.Fatal("missing durable job location")
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		path := "/api/jobs/" + id
		if method == http.MethodPost {
			path += "/cancel"
		}
		got := doJSON(t, bob, method, server.URL+path, "")
		if got.StatusCode != http.StatusNotFound {
			t.Fatalf("cross account %s=%d", method, got.StatusCode)
		}
	}
	duplicate := doJSON(t, alice, http.MethodPost, server.URL+"/api/jobs", `{"course":"C","homework_id":1,"game":"g","mode":"generate_only","idempotency_key":"one"}`)
	if duplicate.StatusCode != http.StatusAccepted || decodeJob(t, duplicate).ID != id {
		t.Fatal("idempotent request started a duplicate")
	}
	conflict := doJSON(t, alice, http.MethodPost, server.URL+"/api/jobs", `{"course":"C","homework_id":1,"game":"other","mode":"generate_only","idempotency_key":"one"}`)
	if conflict.StatusCode != http.StatusConflict {
		t.Fatalf("idempotency conflict=%d", conflict.StatusCode)
	}
	listed := doJSON(t, bob, http.MethodGet, server.URL+"/api/jobs", "")
	if strings.Contains(listed.Body, id) {
		t.Fatal("job leaked into another account list")
	}
	job, err := manager.Get(context.Background(), accountJobOwner("alice"), id)
	if err != nil || (job.Status != jobs.Queued && job.Status != jobs.Running) {
		t.Fatalf("HTTP cancellation stopped detached job: %#v %v", job, err)
	}
	own := doJSON(t, alice, http.MethodGet, server.URL+"/api/jobs/"+id, "")
	if own.StatusCode != http.StatusOK || strings.Contains(own.Body, "owner") || strings.Contains(own.Body, "fixture") {
		t.Fatalf("unsafe job DTO %s", own.Body)
	}
	canceled := doJSON(t, alice, http.MethodPost, server.URL+"/api/jobs/"+id+"/cancel", "")
	if canceled.StatusCode != http.StatusOK {
		t.Fatalf("cancel=%d %s", canceled.StatusCode, canceled.Body)
	}
}

func TestHTTPJobsValidateInputsAndModelAvailability(t *testing.T) {
	server, app, _ := jobsHTTP(t, jobs.Config{Workers: 1, QueueSize: 1, PerOwnerPending: 1}, func(context.Context, EducoderClient, string, jobs.Request, func(jobs.Event)) (jobs.Outcome, error) {
		return jobs.Outcome{Status: jobs.Succeeded}, nil
	})
	client := jobAccount(t, server, "owner")
	unauth := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", `{}`)
	if unauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth=%d", unauth.StatusCode)
	}
	for _, body := range []string{`{"course":"C","homework_id":1,"mode":"invalid"}`, `{"course":"../C","homework_id":1,"mode":"submit"}`, `{"course":"C","homework_id":1,"mode":"submit","owner":"other"}`, `{"course":"C","homework_id":1,"mode":"submit"}{}`} {
		r := doJSON(t, client, http.MethodPost, server.URL+"/api/jobs", body)
		if r.StatusCode != http.StatusBadRequest {
			t.Fatalf("accepted invalid request: %d %s", r.StatusCode, body)
		}
	}
	// Startup with no model credential remains browseable and reports readiness.
	app.jobs.modelConfigured = false
	list := doJSON(t, client, http.MethodGet, server.URL+"/api/jobs", "")
	if list.StatusCode != http.StatusOK || !strings.Contains(list.Body, `"model_configured":false`) {
		t.Fatal(list.Body)
	}
	r := doJSON(t, client, http.MethodPost, server.URL+"/api/jobs", `{"course":"C","homework_id":1,"mode":"submit"}`)
	if r.StatusCode != http.StatusServiceUnavailable || !strings.Contains(r.Body, "MODEL_NOT_CONFIGURED") {
		t.Fatal(r.Body)
	}
}

func TestHTTPJobsAdmissionUnderConcurrentAccountsIsBounded(t *testing.T) {
	release := make(chan struct{})
	var active, maximum atomic.Int32
	server, app, manager := jobsHTTP(t, jobs.Config{Workers: 8, QueueSize: 64, PerOwnerPending: 4}, func(ctx context.Context, _ EducoderClient, _ string, _ jobs.Request, report func(jobs.Event)) (jobs.Outcome, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); n > old; old = maximum.Load() {
			if maximum.CompareAndSwap(old, n) {
				break
			}
		}
		report(jobs.Event{Phase: "generating", Kind: "programming", Message: "正在生成"})
		select {
		case <-release:
			return jobs.Outcome{Status: jobs.Succeeded}, nil
		case <-ctx.Done():
			return jobs.Outcome{}, ctx.Err()
		}
	})
	const accounts = 40
	var wait sync.WaitGroup
	statuses := make(chan int, accounts)
	started := time.Now()
	for i := 0; i < accounts; i++ {
		session, err := app.Store().Create(&fakeClient{}, educoder.User{Login: fmt.Sprintf("account%d", i)})
		if err != nil {
			t.Fatal(err)
		}
		wait.Add(1)
		go func(cookie string) {
			defer wait.Done()
			request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/jobs", strings.NewReader(`{"course":"C","homework_id":1,"game":"g","mode":"submit"}`))
			request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
			response, err := server.Client().Do(request)
			if err != nil {
				statuses <- 0
				return
			}
			response.Body.Close()
			statuses <- response.StatusCode
		}(session)
	}
	wait.Wait()
	close(statuses)
	for status := range statuses {
		if status != http.StatusAccepted {
			t.Fatalf("admission status=%d", status)
		}
	}
	stats := manager.Stats()
	if stats.Running > 8 || stats.Running+stats.Queued != accounts || maximum.Load() > 8 {
		t.Fatalf("unbounded work: %#v max=%d", stats, maximum.Load())
	}
	t.Logf("concurrent HTTP accounts=%d, workers=%d, running=%d, queued=%d, admission=%s", accounts, stats.Workers, stats.Running, stats.Queued, time.Since(started))
	close(release)
}

func TestJobRuntimeCredentialAndConfig(t *testing.T) {
	for _, name := range []string{"JOB_WORKERS", "JOB_QUEUE_SIZE", "JOB_PER_ACCOUNT_PENDING", "JOB_PER_ACCOUNT_RUNNING", "MODEL_CONCURRENCY", "JOB_TIMEOUT", "MODEL_TIMEOUT", "APP_DATA_DIR", "DEEPSEEK_API_KEY_FILE", "DEEPSEEK_API_KEY", "DEEPSEEK_BASE_URL"} {
		t.Setenv(name, "")
	}
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	runtime, err := NewJobRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.modelConfigured || runtime.manager.Stats().Workers != 16 || runtime.manager.Stats().PerOwnerRunning != 4 || runtime.modelConcurrency != 8 {
		t.Fatal("unexpected runtime defaults")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = runtime.Close(ctx); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"0", "-1", "junk", "257"} {
		t.Setenv("JOB_WORKERS", bad)
		if _, err = LoadConfig(); err == nil {
			t.Fatalf("accepted invalid workers %q", bad)
		}
	}
	t.Setenv("JOB_PER_ACCOUNT_RUNNING", "17")
	if _, err = LoadConfig(); err == nil {
		t.Fatal("accepted per-account running limit above worker count")
	}
}
