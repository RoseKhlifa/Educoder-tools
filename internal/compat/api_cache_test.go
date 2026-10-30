package compat

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAPICacheIsolatesSessionsAndCanonicalizesEffectiveQuery(t *testing.T) {
	f := newCompatFixture(t)
	_, firstSession, firstMarker := f.login(t)
	_, secondSession, secondMarker := f.login(t)
	before := len(f.requests())
	first := f.request(http.MethodGet, "/api/users/student1/courses.json?page=1&zzud=spoofed&username=spoofed", "", firstSession)
	if first.Header().Get("X-App-Cache") != "MISS" {
		t.Fatalf("first read did not miss: %v", first.Header())
	}
	hit := f.request(http.MethodGet, "/api/users/student1/courses.json?username=different&zzud=different&page=1", "", firstSession)
	if hit.Header().Get("X-App-Cache") != "HIT" || hit.Body.String() != first.Body.String() || len(f.requests()) != before+1 {
		t.Fatalf("effective query was not cached: state=%s calls=%d", hit.Header().Get("X-App-Cache"), len(f.requests()))
	}
	if hit.Header().Get("Cache-Control") != "no-store" || !strings.Contains(hit.Header().Get("Server-Timing"), "upstream;dur=0.000") {
		t.Errorf("cache response headers: %v", hit.Header())
	}
	assertNoUpstreamSecrets(t, hit)
	if !strings.Contains(hit.Body.String(), firstMarker.Value) {
		t.Fatal("cache lost this session's readable marker")
	}
	second := f.request(http.MethodGet, "/api/users/student1/courses.json?page=1", "", secondSession)
	if second.Header().Get("X-App-Cache") != "MISS" || !strings.Contains(second.Body.String(), secondMarker.Value) || strings.Contains(second.Body.String(), firstMarker.Value) {
		t.Fatalf("different session reused private payload: %s", second.Body.String())
	}
	pageTwo := f.request(http.MethodGet, "/api/users/student1/courses.json?page=2", "", firstSession)
	if pageTwo.Header().Get("X-App-Cache") != "MISS" || len(f.requests()) != before+3 {
		t.Fatalf("distinct query did not fetch separately: state=%s calls=%d", pageTwo.Header().Get("X-App-Cache"), len(f.requests()))
	}
	// Public routes can include user-specific fields once authenticated.
	for index, cookie := range []*http.Cookie{nil, nil, firstSession, secondSession} {
		var cookies []*http.Cookie
		if cookie != nil {
			cookies = append(cookies, cookie)
		}
		w := f.request(http.MethodGet, "/api/home/index.json", "", cookies...)
		want := "MISS"
		if index == 1 {
			want = "HIT"
		}
		if w.Header().Get("X-App-Cache") != want {
			t.Errorf("public/authenticated scope %d: got %s, want %s", index, w.Header().Get("X-App-Cache"), want)
		}
	}
	f.request(http.MethodPost, "/api/accounts/logout.json", "", firstSession)
	if w := f.request(http.MethodGet, "/api/users/student1/courses.json?page=1", "", firstSession); w.Code != http.StatusUnauthorized {
		t.Fatal("cached response bypassed logout")
	}
}

func TestAPICacheRequestBypassAndLiveRoutesStayFresh(t *testing.T) {
	f := newCompatFixture(t)
	_, session, _ := f.login(t)
	const cachedPath = "/api/courses/COURSE1/homework_commons.json"
	f.request(http.MethodGet, cachedPath, "", session)
	for _, directive := range []string{"no-cache", "private, NO-STORE", "no-cache=\"set-cookie\""} {
		before := len(f.requests())
		r := httptest.NewRequest(http.MethodGet, cachedPath, nil)
		r.AddCookie(session)
		r.Header.Set("Cache-Control", directive)
		w := httptest.NewRecorder()
		f.server.ServeHTTP(w, r)
		if w.Code != http.StatusOK || w.Header().Get("X-App-Cache") != "BYPASS" || len(f.requests()) != before+1 {
			t.Errorf("directive %q did not force upstream read: status=%d state=%s calls=%d", directive, w.Code, w.Header().Get("X-App-Cache"), len(f.requests()))
		}
	}
	for _, path := range []string{
		"/api/shixuns/SHIXUN1/shixun_exec.json",
		"/api/myshixuns/SHIXUN1/version_repository_delete.json",
		"/api/popup_windows/popup_windows_counts.json",
		"/api/popup_windows/get_popup_windows.json",
		"/api/tasks/GAME1.json",
		"/api/tasks/GAME1/rep_content.json",
	} {
		before := len(f.requests())
		for range 2 {
			w := f.request(http.MethodGet, path, "", session)
			if w.Code != http.StatusOK || w.Header().Get("X-App-Cache") != "BYPASS" {
				t.Errorf("fresh route was cached: %s status=%d state=%s", path, w.Code, w.Header().Get("X-App-Cache"))
			}
		}
		if len(f.requests()) != before+2 {
			t.Errorf("fresh route did not execute twice: %s", path)
		}
	}
}

func TestAPIResponseCacheExpiryAndMemoryBounds(t *testing.T) {
	cache := newAPIResponseCache()
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	cache.now = func() time.Time { return now }
	cache.maxEntries = 2
	var calls int
	fetch := func(context.Context) (apiResponse, error) {
		calls++
		return apiResponse{status: http.StatusOK, body: []byte(`{"status":0}`)}, nil
	}
	read := func(name string) string {
		t.Helper()
		_, state, err := cache.get(context.Background(), apiCacheKey{scope: "session", path: name}, time.Second, fetch)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	if read("one") != "MISS" || read("two") != "MISS" || read("one") != "HIT" || calls != 2 {
		t.Fatalf("unexpected initial cache behavior: calls=%d", calls)
	}
	read("three")
	if read("two") != "MISS" || calls != 4 {
		t.Fatalf("least-recently-used entry was not evicted: calls=%d", calls)
	}
	now = now.Add(2 * time.Second)
	if read("two") != "MISS" || calls != 5 {
		t.Fatalf("expired entry was served: calls=%d", calls)
	}
	now = now.Add(2 * time.Second)
	cache.sweep()
	if cache.bytes != 0 || len(cache.items) != 0 {
		t.Fatal("sweep retained expired bodies")
	}
	cache.maxBytes = len(`{"status":0}`)
	read("one")
	read("two")
	if cache.bytes > cache.maxBytes || len(cache.items) != 1 {
		t.Fatalf("cache exceeded byte bound: bytes=%d entries=%d", cache.bytes, len(cache.items))
	}
	cache.maxBytes = 1
	if read("large") != "MISS" || read("large") != "MISS" {
		t.Fatal("oversized body entered cache")
	}
}

func TestAPIResponseCacheRejectsFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		err    error
	}{
		{"HTTP failure", 502, `{"status":0}`, nil},
		{"application failure", 200, `{"status":-1,"message":"retry"}`, nil},
		{"positive application failure", 200, `{"status":1,"message":"retry"}`, nil},
		{"application auth failure", 200, `{"status":401}`, nil},
		{"error field", 200, `{"status":0,"error":"retry"}`, nil},
		{"malformed", 200, `not-json`, nil},
		{"transport failure", 0, "", errors.New("mock upstream failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := newAPIResponseCache()
			var calls int
			for range 2 {
				_, state, _ := cache.get(context.Background(), apiCacheKey{path: "failure"}, time.Second, func(context.Context) (apiResponse, error) {
					calls++
					return apiResponse{status: tc.status, body: []byte(tc.body)}, tc.err
				})
				if state != "MISS" {
					t.Errorf("failure was cached: %s", state)
				}
			}
			if calls != 2 || len(cache.items) != 0 {
				t.Errorf("failed response cached: calls=%d entries=%d", calls, len(cache.items))
			}
		})
	}
}

func TestAPIResponseCacheCoalescesConcurrentReads(t *testing.T) {
	cache := newAPIResponseCache()
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	fetch := func(context.Context) (apiResponse, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return apiResponse{status: http.StatusOK, body: []byte(`{"status":0}`)}, nil
	}
	key := apiCacheKey{scope: "session", path: "same"}
	var wait sync.WaitGroup
	states := make(chan string, 12)
	for range 12 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, state, err := cache.get(context.Background(), key, time.Minute, fetch)
			if err != nil {
				t.Error(err)
			}
			states <- state
		}()
	}
	<-started
	// A different route must proceed while the shared request is still blocked.
	_, state, err := cache.get(context.Background(), apiCacheKey{path: "different"}, time.Minute, func(context.Context) (apiResponse, error) {
		return apiResponse{status: http.StatusOK, body: []byte(`{"status":0}`)}, nil
	})
	if err != nil || state != "MISS" {
		t.Fatalf("unrelated request blocked or failed: state=%s err=%v", state, err)
	}
	close(release)
	wait.Wait()
	close(states)
	misses := 0
	for state := range states {
		if state == "MISS" {
			misses++
		}
	}
	if calls.Load() != 1 || misses != 1 {
		t.Errorf("concurrent reads not merged: calls=%d misses=%d", calls.Load(), misses)
	}
}

func TestAPIResponseCacheCancellationDoesNotAbortSharedFetch(t *testing.T) {
	cache := newAPIResponseCache()
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	key := apiCacheKey{path: "cancel"}
	go func() {
		_, _, err := cache.get(ctx, key, time.Minute, func(fetchCtx context.Context) (apiResponse, error) {
			close(started)
			<-release
			if fetchCtx.Err() != nil {
				return apiResponse{}, fetchCtx.Err()
			}
			return apiResponse{status: http.StatusOK, body: []byte(`{"status":0}`)}, nil
		})
		finished <- err
	}()
	<-started
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("cancelled caller did not return cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled browser kept waiting")
	}
	cache.mu.Lock()
	done := cache.flights[key].done
	cache.mu.Unlock()
	close(release)
	<-done
	_, state, err := cache.get(context.Background(), key, time.Minute, func(context.Context) (apiResponse, error) {
		return apiResponse{}, errors.New("should reuse shared result")
	})
	if state != "HIT" || err != nil {
		t.Fatalf("navigation cancellation discarded shared result: state=%s err=%v", state, err)
	}
}

func TestAPICacheTTLAllowlist(t *testing.T) {
	for path, want := range map[string]time.Duration{
		"/api/setting.json":                          5 * time.Minute,
		"/api/users/get_user_info.json":              15 * time.Second,
		"/api/users/get_notice.json":                 5 * time.Second,
		"/api/users/system_update.json":              30 * time.Second,
		"/api/users/student1/courses.json":           15 * time.Second,
		"/api/courses/COURSE1/homework_commons.json": 10 * time.Second,
		"/api/courses/COURSE1/top_banner.json":       time.Minute,
		"/api/unknown.json":                          0,
		"/api/tasks/GAME1.json":                      0,
		"/api/shixuns/SHIXUN1/shixun_exec.json":      0,
	} {
		if got := apiCacheTTL(http.MethodGet, path); got != want {
			t.Errorf("GET %s TTL=%v, want %v", path, got, want)
		}
		if got := apiCacheTTL(http.MethodPost, path); got != 0 {
			t.Errorf("POST %s cacheable", path)
		}
	}
	query := url.Values{"page": {"1"}, "tag": {"a", "b"}, "zzud": {"ignored"}}
	key := makeAPICacheKey("session", http.MethodGet, "/api/home/index.json", query)
	if key.query != "page=1&tag=a&tag=b" || query.Get("zzud") != "ignored" {
		t.Errorf("key changed input or query values: key=%s original=%v", fmt.Sprint(key), query)
	}
}
