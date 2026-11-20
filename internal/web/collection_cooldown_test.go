package web

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"educoder-cli/internal/jobs"
)

func finishCollection(t *testing.T, m *jobs.Manager, owner string, status string, result string) {
	t.Helper()
	job, _, err := m.Submit(context.Background(), owner, jobs.Request{Mode: "collect_verified"}, func(context.Context, jobs.Request, func(jobs.Event)) (jobs.Outcome, error) {
		return jobs.Outcome{Status: status, Result: json.RawMessage(result)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		got, err := m.Get(context.Background(), owner, job.ID)
		if err == nil && got.FinishedAt != nil {
			return
		}
	}
	t.Fatal("collection job did not finish")
}

// A login skips the automatic scan while the account's last finished scan is
// recent: a complete scan for a day, an incomplete one for two hours. Failed
// scans and other owners' scans never suppress it.
func TestLoginCollectionCooldown(t *testing.T) {
	m, err := jobs.Open(context.Background(), jobs.Config{Path: filepath.Join(t.TempDir(), "jobs.sqlite"), Workers: 2, CollectionWorkers: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close(context.Background()) })
	now := time.Now()
	t.Cleanup(func() { collectionNow = time.Now })
	at := func(d time.Duration) { collectionNow = func() time.Time { return now.Add(d) } }
	ctx := context.Background()

	if recentCollection(ctx, m, "owner-a") {
		t.Fatal("an account that was never scanned must be scanned")
	}
	finishCollection(t, m, "owner-a", jobs.Succeeded, `{"scan_complete":true,"collected":0}`)
	at(time.Hour)
	if !recentCollection(ctx, m, "owner-a") {
		t.Fatal("a complete scan an hour ago must suppress the login scan")
	}
	at(loginCollectionCooldown + time.Minute)
	if recentCollection(ctx, m, "owner-a") {
		t.Fatal("a complete scan older than the cooldown must not suppress the login scan")
	}
	if recentCollection(ctx, m, "owner-b") {
		t.Fatal("another owner's scan must not suppress this owner's scan")
	}

	finishCollection(t, m, "owner-c", jobs.Succeeded, `{"scan_complete":false}`)
	at(time.Hour)
	if !recentCollection(ctx, m, "owner-c") {
		t.Fatal("an incomplete scan an hour ago must suppress the login scan")
	}
	at(loginIncompleteCollectionCooldown + time.Minute)
	if recentCollection(ctx, m, "owner-c") {
		t.Fatal("an incomplete scan older than its shorter cooldown must not suppress the login scan")
	}

	finishCollection(t, m, "owner-d", jobs.Failed, `{"scan_complete":true}`)
	at(time.Minute)
	if recentCollection(ctx, m, "owner-d") {
		t.Fatal("a failed scan must not suppress the next one")
	}
	if !collectionScanComplete(json.RawMessage(`"{\"scan_complete\":true}"`)) || collectionScanComplete(json.RawMessage(`null`)) {
		t.Fatal("scan_complete must decode from an object or a JSON-encoded object, and default to false")
	}
}
