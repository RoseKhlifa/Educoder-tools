package platform

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func openTest(t *testing.T, options Options) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "platform.sqlite"), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestVerifiedIdentityIsPersistentAndNeverUsesLoginForOwnership(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "platform.sqlite")
	s, err := Open(path, Options{AdminUserIDs: []int64{41}})
	if err != nil {
		t.Fatal(err)
	}
	one, err := s.UpsertLogin(ctx, Identity{UpstreamID: 42, Login: "student-first", Name: "同学"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if one.Role != "user" || one.Status != "active" || one.TenantID == "" {
		t.Fatalf("invalid default identity: %+v", one)
	}
	if _, err = s.UpsertLogin(ctx, Identity{Login: "teacher"}, ""); !errors.Is(err, ErrInvalidIdentity) {
		t.Fatalf("unverified identity accepted: %v", err)
	}
	s.Close()
	s, err = Open(path, Options{AdminUserIDs: []int64{41}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	two, err := s.UpsertLogin(ctx, Identity{UpstreamID: 42, Login: "student-renamed", Name: "新名称"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if one.ID != two.ID || one.TenantID != two.TenantID || one.Owner() != two.Owner() {
		t.Fatal("renamed login changed tenant or owner")
	}
	three, err := s.UpsertLogin(ctx, Identity{UpstreamID: 43, Login: "student-first"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if three.ID == one.ID || three.TenantID == one.TenantID {
		t.Fatal("reused display login merged identities")
	}
	admin, err := s.UpsertLogin(ctx, Identity{UpstreamID: 41, Login: "administrator"}, "")
	if err != nil || !admin.IsAdmin() {
		t.Fatalf("explicit bootstrap failed: %+v %v", admin, err)
	}
	if err = s.SetStatus(ctx, one.ID, three.ID, "disabled"); !errors.Is(err, ErrForbidden) {
		t.Fatal("ordinary user changed another status")
	}
	if err = s.SetStatus(ctx, admin.ID, one.ID, "disabled"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpsertLogin(ctx, Identity{UpstreamID: 42, Login: "student-renamed"}, ""); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled account logged in: %v", err)
	}
	if err = s.SetRole(ctx, admin.ID, admin.ID, "user"); !errors.Is(err, ErrForbidden) {
		t.Fatal("last admin accidentally demoted self")
	}
}

func TestSingleUserCardConcurrentRedemptionIsAtomicAndDoesNotPersistCode(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, Options{AdminUserIDs: []int64{1}, AccessCardRequired: true})
	admin, err := s.UpsertLogin(ctx, Identity{UpstreamID: 1, Login: "admin"}, "")
	if err != nil {
		t.Fatal(err)
	}
	issued, err := s.IssueCard(ctx, admin.ID, CardOptions{Label: "Concurrency fixture"})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	users := make(chan User, 2)
	for _, id := range []int64{2, 3} {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			u, e := s.UpsertLogin(ctx, Identity{UpstreamID: id, Login: "fixture"}, issued.Code)
			results <- e
			if e == nil {
				users <- u
			}
		}(id)
	}
	wg.Wait()
	close(results)
	close(users)
	success, failure := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrInvalidCard) {
			failure++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || failure != 1 {
		t.Fatalf("concurrent redemption: %d success %d failure", success, failure)
	}
	list, err := s.ListUsers(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("failed redemption committed a user: %d", len(list))
	}
	var winner User
	for u := range users {
		winner = u
	}
	allowed, err := s.CanAccess(ctx, winner)
	if err != nil || !allowed {
		t.Fatal("winner lacks access")
	}
	if _, err = s.UpsertLogin(ctx, Identity{UpstreamID: winner.UpstreamID, Login: "fixture"}, strings.ToLower(issued.Code)); err != nil {
		t.Fatalf("same user cannot log in again: %v", err)
	}
	cards, err := s.ListCards(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	audits, err := s.ListAudit(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal([]any{cards, audits})
	if strings.Contains(string(body), issued.Code) || strings.Contains(string(body), hashCode(issued.Code)) {
		t.Fatal("card listing or audit leaked secret")
	}
	var stored string
	if err = s.db.QueryRow("SELECT code_hash FROM access_cards WHERE id=?", issued.Card.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != hashCode(issued.Code) || stored == issued.Code {
		t.Fatal("card not stored by digest")
	}
	if err = s.RevokeCard(ctx, admin.ID, issued.Card.ID); err != nil {
		t.Fatal(err)
	}
	allowed, err = s.CanAccess(ctx, winner)
	if err != nil || allowed {
		t.Fatal("revoked card retained active access")
	}
	if _, err = s.UpsertLogin(ctx, Identity{UpstreamID: winner.UpstreamID, Login: "fixture"}, issued.Code); !errors.Is(err, ErrInvalidCard) {
		t.Fatal("revoked card was accepted")
	}
}

func TestCardPolicyDeferredAndExpiryEnforced(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, Options{})
	if _, err := s.UpsertLogin(ctx, Identity{UpstreamID: 2, Login: "student"}, "arbitrary reserved input"); err != nil {
		t.Fatalf("deferred card policy unexpectedly enforced: %v", err)
	}
	s2 := openTest(t, Options{AdminUserIDs: []int64{1}, AccessCardRequired: true})
	admin, err := s2.UpsertLogin(ctx, Identity{UpstreamID: 1, Login: "admin"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s2.UpsertLogin(ctx, Identity{UpstreamID: 2, Login: "student"}, ""); !errors.Is(err, ErrCardRequired) {
		t.Fatal("card policy ignored")
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s2.now = func() time.Time { return now }
	issued, err := s2.IssueCard(ctx, admin.ID, CardOptions{Reusable: true, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	u, err := s2.UpsertLogin(ctx, Identity{UpstreamID: 2, Login: "student"}, issued.Code)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s2.UpsertLogin(ctx, Identity{UpstreamID: 3, Login: "student-two"}, issued.Code); err != nil {
		t.Fatal("explicit reusable card did not allow second user")
	}
	now = now.Add(2 * time.Hour)
	if allowed, err := s2.CanAccess(ctx, u); err != nil || allowed {
		t.Fatal("expired card retained access")
	}
}

func TestStatsCountsBeyondVisibleUserPage(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, Options{AdminUserIDs: []int64{1}})
	admin, err := s.UpsertLogin(ctx, Identity{UpstreamID: 1, Login: "administrator"}, "")
	if err != nil {
		t.Fatal(err)
	}
	var last User
	for id := int64(2); id <= 105; id++ {
		last, err = s.UpsertLogin(ctx, Identity{UpstreamID: id, Login: "fixture-" + strconv.FormatInt(id, 10)}, "")
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = s.SetStatus(ctx, admin.ID, last.ID, "disabled"); err != nil {
		t.Fatal(err)
	}
	stats, err := s.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalUsers != 105 || stats.ActiveUsers != 104 || stats.DisabledUsers != 1 {
		t.Fatalf("stats truncated to visible page: %+v", stats)
	}
	page, err := s.ListUsers(ctx, 100)
	if err != nil || len(page) != 100 {
		t.Fatalf("page = %d %v", len(page), err)
	}
}
