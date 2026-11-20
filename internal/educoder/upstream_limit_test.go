package educoder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type inflightServer struct {
	*httptest.Server
	mu      sync.Mutex
	current int
	peak    int
	release chan struct{}
	total   atomic.Int64
}

func newInflightServer(t *testing.T) *inflightServer {
	t.Helper()
	s := &inflightServer{release: make(chan struct{})}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.current++
		s.peak = max(s.peak, s.current)
		s.mu.Unlock()
		s.total.Add(1)
		select {
		case <-s.release:
		case <-time.After(150 * time.Millisecond):
		}
		s.mu.Lock()
		s.current--
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":0}`))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *inflightServer) peakValue() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.peak
}

func withUpstreamLimits(t *testing.T, account, global int) {
	t.Helper()
	if err := SetUpstreamLimits(account, global); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = SetUpstreamLimits(DefaultAccountUpstreamConcurrency, DefaultGlobalUpstreamConcurrency) })
}

func fire(t *testing.T, clients []*Client, perClient int) {
	t.Helper()
	var wg sync.WaitGroup
	for _, c := range clients {
		for i := 0; i < perClient; i++ {
			wg.Add(1)
			go func(c *Client) {
				defer wg.Done()
				if err := c.request(context.Background(), http.MethodGet, "/api/ping.json", nil, nil, "/", nil); err != nil {
					t.Error(err)
				}
			}(c)
		}
	}
	wg.Wait()
}

func TestAccountUpstreamConcurrencyIsSharedByClones(t *testing.T) {
	withUpstreamLimits(t, 2, 16)
	server := newInflightServer(t)
	client := New(server.URL)
	required, err := client.WithRequiredTasks()
	if err != nil {
		t.Fatal(err)
	}
	// The session client and its job clone belong to one account.
	fire(t, []*Client{client, required}, 4)
	if peak := server.peakValue(); peak > 2 || peak < 1 || server.total.Load() != 8 {
		t.Fatalf("account limit not shared: peak=%d total=%d", peak, server.total.Load())
	}
}

func TestGlobalUpstreamConcurrencyBoundsAllAccounts(t *testing.T) {
	withUpstreamLimits(t, 3, 4)
	server := newInflightServer(t)
	clients := []*Client{New(server.URL), New(server.URL), New(server.URL)}
	fire(t, clients, 3)
	if peak := server.peakValue(); peak > 4 || peak < 2 || server.total.Load() != 9 {
		t.Fatalf("global limit not enforced: peak=%d total=%d", peak, server.total.Load())
	}
}

func TestUpstreamSlotWaitHonoursCancellation(t *testing.T) {
	withUpstreamLimits(t, 1, 4)
	server := newInflightServer(t)
	client := New(server.URL)
	blocker := make(chan struct{})
	go func() {
		defer close(blocker)
		_ = client.request(context.Background(), http.MethodGet, "/api/ping.json", nil, nil, "/", nil)
	}()
	time.Sleep(30 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := client.request(ctx, http.MethodGet, "/api/ping.json", nil, nil, "/", nil)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 120*time.Millisecond {
		t.Fatalf("waiting request was not canceled promptly: err=%v after=%v", err, time.Since(started))
	}
	<-blocker
}

func TestUpstreamLimitsRejectInvalidConfiguration(t *testing.T) {
	for _, tc := range [][2]int{{0, 4}, {5, 4}, {257, 4096}, {4, 5000}} {
		if SetUpstreamLimits(tc[0], tc[1]) == nil {
			t.Fatalf("accepted invalid limits %v", tc)
		}
	}
}

func loginInflightServer(t *testing.T) *inflightServer {
	t.Helper()
	s := &inflightServer{release: make(chan struct{})}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/accounts/login.json" {
			var body struct {
				Login string `json:"login"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			http.SetCookie(w, &http.Cookie{Name: "_educoder_session", Value: "s-" + body.Login, Path: "/"})
			id := map[string]int{"alice": 7, "bob": 8}[body.Login]
			_, _ = w.Write([]byte(fmt.Sprintf(`{"login":%q,"user_id":%d}`, body.Login, id)))
			return
		}
		s.mu.Lock()
		s.current++
		s.peak = max(s.peak, s.current)
		s.mu.Unlock()
		s.total.Add(1)
		time.Sleep(150 * time.Millisecond)
		s.mu.Lock()
		s.current--
		s.mu.Unlock()
		_, _ = w.Write([]byte(`{"status":0}`))
	}))
	t.Cleanup(s.Close)
	return s
}

func loggedIn(t *testing.T, server *inflightServer, login string) *Client {
	t.Helper()
	c := New(server.URL)
	if _, err := c.Login(context.Background(), login, "fixture-only"); err != nil {
		t.Fatal(err)
	}
	return c
}

// Two sessions of one account share that account's bound; another account
// keeps its own.
func TestAccountUpstreamConcurrencyIsSharedAcrossSessions(t *testing.T) {
	withUpstreamLimits(t, 2, 16)
	server := loginInflightServer(t)
	first, second := loggedIn(t, server, "alice"), loggedIn(t, server, "alice")
	fire(t, []*Client{first, second}, 4)
	if peak := server.peakValue(); peak > 2 || server.total.Load() != 8 {
		t.Fatalf("one account exceeded its bound across sessions: peak=%d total=%d", peak, server.total.Load())
	}
	other := newPeakReset(server)
	bob := loggedIn(t, server, "bob")
	fire(t, []*Client{first, bob}, 4)
	if peak := other(); peak < 3 || peak > 4 {
		t.Fatalf("separate accounts should not share a bound: peak=%d", peak)
	}
}

func newPeakReset(s *inflightServer) func() int {
	s.mu.Lock()
	s.peak = 0
	s.mu.Unlock()
	return s.peakValue
}
