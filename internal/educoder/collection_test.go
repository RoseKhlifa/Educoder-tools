package educoder

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCollectionReadOnlyBudgetsAndIdentity(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != "GET" {
			t.Error("collector wrote upstream")
		}
		switch r.URL.Path {
		case "/api/tasks/G.json":
			json.NewEncoder(w).Encode(map[string]any{"homework_common_id": 4, "game": map[string]any{"identifier": "G"}})
		case "/api/tasks/BIG.json":
			w.Write([]byte(strings.Repeat("x", 300)))
		case "/api/tasks/MISSING.json":
			w.Write([]byte(`{"game":{"identifier":"MISSING"}}`))
		case "/api/tasks/REDIRECT.json":
			http.Redirect(w, r, "/api/shixuns/S/shixun_exec.json", 302)
		default:
			w.Write([]byte(`{"courses":[],"homeworks":[]}`))
		}
	}))
	defer server.Close()
	original := New(server.URL)
	original.LoginName = "demo"
	reader, err := original.CollectionReader(CollectionBudget{Requests: 4, ResponseBytes: 256, TotalBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reader.CollectionLoadTask(context.Background(), "C", 4, "MISSING"); err == nil {
		t.Fatal("accepted missing homework identity")
	}
	if _, err = reader.CollectionLoadTask(context.Background(), "C", 4, "G"); err != nil {
		t.Fatal(err)
	}
	if _, err = reader.CollectionLoadTask(context.Background(), "C", 5, "G"); err == nil {
		t.Fatal("accepted different homework")
	}
	if _, err = reader.CollectionLoadTask(context.Background(), "C", 4, "BIG"); !errors.Is(err, ErrCollectionLimit) {
		t.Fatalf("size limit %v", err)
	}
	if _, err = reader.CollectionLoadTask(context.Background(), "C", 4, "G"); !errors.Is(err, ErrCollectionLimit) {
		t.Fatalf("requests limit %v", err)
	}
	if requests.Load() != 4 {
		t.Fatal(requests.Load())
	}
	for _, tc := range []struct{ method, path string }{{"POST", "/api/tasks/G/choose_build.json"}, {"GET", "/api/shixuns/S/shixun_exec.json"}, {"GET", "/api/myshixuns/1/version_repository_delete.json"}, {"GET", "/api/tasks/G/game_status.json"}, {"GET", "/api/tasks/G/rep_content.json/extra"}} {
		r, _ := http.NewRequest(tc.method, server.URL+tc.path, nil)
		if _, err = reader.HTTP.Do(r); !errors.Is(err, ErrCollectionReadOnly) {
			t.Fatalf("allow %s: %v", tc.path, err)
		}
	}
	if requests.Load() != 4 {
		t.Fatal("unsupported request reached transport")
	}
	second, _ := original.CollectionReader(CollectionBudget{Requests: 2, ResponseBytes: 1024, TotalBytes: 1024})
	if _, err = second.CollectionLoadTask(context.Background(), "C", 4, "REDIRECT"); err == nil {
		t.Fatal("followed redirect")
	}
	if requests.Load() != 5 {
		t.Fatal("redirect target reached")
	}
}

func TestCollectionPageLimitsAndCancellation(t *testing.T) {
	var count atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		if r.URL.Query().Get("page") != "1" {
			t.Error("page")
		}
		if strings.Contains(r.URL.Path, "homework_commons") && r.URL.Query().Has("type") {
			t.Error("restricted homework types")
		}
		w.Write([]byte(`{"courses":[],"homeworks":[]}`))
	}))
	defer s.Close()
	c := New(s.URL)
	c.LoginName = "demo"
	reader, _ := c.CollectionReader(CollectionBudget{Requests: 4, ResponseBytes: 1024, TotalBytes: 1024, Interval: time.Second})
	if _, e := reader.CollectionCoursesPage(context.Background(), "processing", 1); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := reader.CollectionHomeworksPage(ctx, "C", 1); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e := reader.CollectionCoursesPage(context.Background(), "processing", 6); !errors.Is(e, ErrCollectionLimit) {
		t.Fatal(e)
	}
	if count.Load() != 1 {
		t.Fatal(count.Load())
	}
	reader2, _ := c.CollectionReader(CollectionBudget{Requests: 4, ResponseBytes: 1024, TotalBytes: 10})
	if _, e := reader2.CollectionHomeworksPage(context.Background(), "C", 1); !errors.Is(e, ErrCollectionLimit) {
		t.Fatal(e)
	}
}
