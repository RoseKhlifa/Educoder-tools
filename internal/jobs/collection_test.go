package jobs

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestCollectionNormalizationAndRestartNeverReplay(t *testing.T) {
	req := Request{Mode: "collect_verified", IdempotencyKey: "collection-day"}
	_, payload, target, e := normalize(req)
	if e != nil {
		t.Fatal(e)
	}
	for _, bad := range []Request{{Mode: "collect_verified", Course: "C"}, {Mode: "collect_verified", HomeworkID: 1}, {Mode: "collect_verified", Game: "G"}} {
		if _, _, _, e = normalize(bad); e == nil {
			t.Fatal("accepted external target")
		}
	}
	path := filepath.Join(t.TempDir(), "jobs.sqlite")
	cfg := Config{Path: path, Workers: 1, QueueSize: 4, PerOwnerPending: 2}
	cfg, e = cfg.defaults()
	if e != nil {
		t.Fatal(e)
	}
	db, e := openStore(context.Background(), cfg)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	s := stored{Job: Job{ID: "collection-interrupted", Status: Running, Phase: "collecting", Kind: "collection", Request: req, CreatedAt: now, UpdatedAt: now}, owner: "owner", payload: payload, target: target}
	if e = insertStore(db, &s); e != nil {
		t.Fatal(e)
	}
	db.Close()
	m, e := Open(context.Background(), cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close(context.Background())
	var executed atomic.Int32
	j, reused, e := m.Submit(context.Background(), "owner", req, func(context.Context, Request, func(Event)) (Outcome, error) { executed.Add(1); return Outcome{}, nil })
	if e != nil || !reused || j.Status != Interrupted || executed.Load() != 0 {
		t.Fatalf("%+v %v %v", j, reused, e)
	}
	if _, e = m.Get(context.Background(), "another", j.ID); !IsCode(e, "not_found") {
		t.Fatal("owner leak")
	}
}
