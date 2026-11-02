package educoder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCollectionDefaultBudgetExhaustiveAndResponseBounded(t *testing.T) {
	budget := DefaultCollectionBudget()
	if budget.Requests != 0 || budget.TotalBytes != 0 || budget.ResponseBytes != 2<<20 || budget.Interval != 100*time.Millisecond {
		t.Fatalf("unexpected defaults %+v", budget)
	}
	var count atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		if r.URL.Query().Get("page") == "2000" {
			w.Write([]byte(strings.Repeat("x", (2<<20)+1)))
			return
		}
		w.Write([]byte(`{"courses":[]}`))
	}))
	defer s.Close()
	c := New(s.URL)
	c.LoginName = "demo"
	budget.Interval = 0
	reader, err := c.CollectionReader(budget)
	if err != nil {
		t.Fatal(err)
	}
	if reader.HTTP.Timeout != 20*time.Second {
		t.Fatal("individual request timeout removed")
	}
	for i := 1; i <= 1201; i++ {
		if _, err = reader.CollectionCoursesPage(context.Background(), "processing", i); err != nil {
			t.Fatalf("page %d: %v", i, err)
		}
	}
	if count.Load() != 1201 {
		t.Fatal(count.Load())
	}
	if _, err = reader.CollectionCoursesPage(context.Background(), "processing", 2000); !errors.Is(err, ErrCollectionLimit) {
		t.Fatalf("oversized response accepted: %v", err)
	}
	if count.Load() != 1202 {
		t.Fatal("oversize guard retried unexpectedly", count.Load())
	}
	if _, err = c.CollectionReader(CollectionBudget{Requests: 5000, ResponseBytes: 2 << 20, TotalBytes: 128 << 20}); err != nil {
		t.Fatal("positive aggregate budget compatibility", err)
	}
}

func TestCollectionPaginationRequiresActualListsAndAdvertisedCoverage(t *testing.T) {
	for _, kind := range []string{"courses", "homeworks"} {
		for _, body := range []string{`{}`, `{"courses":null,"homeworks":null}`, `{"courses":[],"count":1,"homeworks":[],"query_total_count":1}`} {
			t.Run(kind+body, func(t *testing.T) {
				s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
				defer s.Close()
				c := New(s.URL)
				c.LoginName = "demo"
				r, err := c.CollectionReader(CollectionBudget{ResponseBytes: 2048})
				if err != nil {
					t.Fatal(err)
				}
				if kind == "courses" {
					_, err = r.CollectionCoursesPage(context.Background(), "processing", 1)
				} else {
					_, err = r.CollectionHomeworksPage(context.Background(), "C", 1)
				}
				if err == nil {
					t.Fatal("missing/truncated pager accepted")
				}
			})
		}
	}
	for _, kind := range []string{"courses", "homeworks"} {
		t.Run(kind+" retained total", func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("page") == "1" {
					w.Write([]byte(`{"courses":[{"identifier":"C"}],"count":2,"homeworks":[{"homework_id":1}],"query_total_count":2}`))
				} else {
					w.Write([]byte(`{"courses":[],"homeworks":[]}`))
				}
			}))
			defer s.Close()
			c := New(s.URL)
			c.LoginName = "demo"
			r, _ := c.CollectionReader(CollectionBudget{ResponseBytes: 2048})
			var err error
			if kind == "courses" {
				_, err = r.CollectionCoursesPage(context.Background(), "processing", 1)
			} else {
				_, err = r.CollectionHomeworksPage(context.Background(), "C", 1)
			}
			if err != nil {
				t.Fatal(err)
			}
			if kind == "courses" {
				_, err = r.CollectionCoursesPage(context.Background(), "processing", 2)
			} else {
				_, err = r.CollectionHomeworksPage(context.Background(), "C", 2)
			}
			if err == nil {
				t.Fatal("forgot advertised total from first page")
			}
		})
	}
}

func TestCollectionTransientReadRetryAndCancellation(t *testing.T) {
	for _, status := range []int{429, 502, 403} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var count atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if count.Add(1) <= 2 {
					w.WriteHeader(status)
					w.Write([]byte(`{"status":500}`))
					return
				}
				w.Write([]byte(`{"courses":[]}`))
			}))
			defer s.Close()
			c := New(s.URL)
			c.LoginName = "demo"
			r, _ := c.CollectionReader(CollectionBudget{ResponseBytes: 2048})
			_, err := r.CollectionCoursesPage(context.Background(), "processing", 1)
			if status == 403 {
				if err == nil || count.Load() != 1 {
					t.Fatalf("authorization retry: %d %v", count.Load(), err)
				}
			} else if err != nil || count.Load() != 3 {
				t.Fatalf("transient: %d %v", count.Load(), err)
			}
		})
	}
	var count atomic.Int32
	reached := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if count.Add(1) == 1 {
			close(reached)
		}
		w.WriteHeader(503)
	}))
	defer s.Close()
	c := New(s.URL)
	c.LoginName = "demo"
	r, _ := c.CollectionReader(CollectionBudget{ResponseBytes: 2048})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := r.CollectionCoursesPage(ctx, "processing", 1); done <- err }()
	<-reached
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("retry backoff ignored cancellation")
	}
	if count.Load() != 1 {
		t.Fatal("canceled read retried", count.Load())
	}
}

func TestCollectionLocatorHandlesMoreThan500ProvenLevels(t *testing.T) {
	const n = 601
	h := map[string]any{"homework_id": 4, "work_id": 5, "myshixun_identifier": "RUNTIME", "shixun_identifier": "SHIXUN"}
	var roster []map[string]any
	var games []map[string]any
	p := &HomeworkProgress{}
	for i := 1; i <= n; i++ {
		name := fmt.Sprintf("level %d", i)
		roster = append(roster, map[string]any{"id": i, "name": name})
		games = append(games, map[string]any{"identifier": fmt.Sprintf("G%d", i), "position": i, "name": name, "status": 2})
		p.Challenges = append(p.Challenges, ChallengeProgress{ChallengeID: int64(i), Name: name, GameScore: "2", ChallengeScore: "2"})
	}
	h["challenges"] = roster
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/shixun_homeworks/4/header_info.json":
			json.NewEncoder(w).Encode(map[string]any{"data": h})
		case "/api/myshixuns/RUNTIME/challenges.json":
			json.NewEncoder(w).Encode(games)
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	c := New(s.URL)
	c.LoginName = "demo"
	r, _ := c.CollectionReader(CollectionBudget{ResponseBytes: 2 << 20})
	got, err := r.CollectionLocate(context.Background(), "C", Homework{ID: 4, ShixunIdentifier: "SHIXUN", StudentWorkID: 5, MyShixunIdentifier: "RUNTIME"}, p)
	if err != nil || len(got.Targets) != n || len(got.Reasons) != 0 || got.Targets[n-1].Position != n {
		t.Fatalf("targets=%d reasons=%v err=%v", len(got.Targets), got.Reasons, err)
	}
}
