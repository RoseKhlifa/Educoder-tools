package platform

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
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
	one, err := s.UpsertLogin(ctx, Identity{UpstreamID: 42, Login: "student-first", Name: "同学"})
	if err != nil {
		t.Fatal(err)
	}
	if one.Role != "user" || one.Status != "active" || one.TenantID == "" {
		t.Fatalf("invalid default identity: %+v", one)
	}
	if _, err = s.UpsertLogin(ctx, Identity{Login: "teacher"}); !errors.Is(err, ErrInvalidIdentity) {
		t.Fatalf("unverified identity accepted: %v", err)
	}
	s.Close()
	s, err = Open(path, Options{AdminUserIDs: []int64{41}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	two, err := s.UpsertLogin(ctx, Identity{UpstreamID: 42, Login: "student-renamed", Name: "新名称"})
	if err != nil {
		t.Fatal(err)
	}
	if one.ID != two.ID || one.TenantID != two.TenantID || one.Owner() != two.Owner() {
		t.Fatal("renamed login changed tenant or owner")
	}
	three, err := s.UpsertLogin(ctx, Identity{UpstreamID: 43, Login: "student-first"})
	if err != nil {
		t.Fatal(err)
	}
	if three.ID == one.ID || three.TenantID == one.TenantID {
		t.Fatal("reused display login merged identities")
	}
	admin, err := s.UpsertLogin(ctx, Identity{UpstreamID: 41, Login: "administrator"})
	if err != nil || !admin.IsAdmin() {
		t.Fatalf("explicit bootstrap failed: %+v %v", admin, err)
	}
	if err = s.SetStatus(ctx, one.ID, three.ID, "disabled"); !errors.Is(err, ErrForbidden) {
		t.Fatal("ordinary user changed another status")
	}
	if err = s.SetStatus(ctx, admin.ID, one.ID, "disabled"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpsertLogin(ctx, Identity{UpstreamID: 42, Login: "student-renamed"}); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled account logged in: %v", err)
	}
	if err = s.SetRole(ctx, admin.ID, admin.ID, "user"); !errors.Is(err, ErrForbidden) {
		t.Fatal("last admin accidentally demoted self")
	}
}

func TestStatsCountsBeyondVisibleUserPage(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, Options{AdminUserIDs: []int64{1}})
	admin, err := s.UpsertLogin(ctx, Identity{UpstreamID: 1, Login: "administrator"})
	if err != nil {
		t.Fatal(err)
	}
	var last User
	for id := int64(2); id <= 105; id++ {
		last, err = s.UpsertLogin(ctx, Identity{UpstreamID: id, Login: "fixture-" + strconv.FormatInt(id, 10)})
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

func TestActiveAccountAccessDoesNotDependOnHistoricalCards(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "platform.sqlite")
	s, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var freshTables int
	if err = s.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('access_cards','access_card_redemptions')").Scan(&freshTables); err != nil {
		t.Fatal(err)
	}
	if freshTables != 0 {
		t.Fatal("new database still provisions retired card tables")
	}
	// Simulate a pre-retirement database. Opening it must neither read these
	// obsolete records for authorization nor delete the historical records.
	if _, err = s.db.Exec(`CREATE TABLE access_cards (id TEXT PRIMARY KEY, status TEXT NOT NULL);
 CREATE TABLE access_card_redemptions (card_id TEXT, user_id TEXT);
 INSERT INTO access_cards VALUES ('historical-fixture','revoked');
 INSERT INTO access_card_redemptions VALUES ('historical-fixture','legacy-user');`); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	user, err := s.UpsertLogin(ctx, Identity{UpstreamID: 2, Login: "student"})
	if err != nil {
		t.Fatal(err)
	}
	if allowed, err := s.CanAccess(ctx, user); err != nil || !allowed {
		t.Fatal("active account denied without a card")
	}
	user.Status = "disabled"
	if allowed, err := s.CanAccess(ctx, user); err != nil || allowed {
		t.Fatal("disabled account retained access")
	}
	var records, redemptions int
	if err = s.db.QueryRow("SELECT COUNT(*) FROM access_cards WHERE id='historical-fixture' AND status='revoked'").Scan(&records); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow("SELECT COUNT(*) FROM access_card_redemptions WHERE card_id='historical-fixture'").Scan(&redemptions); err != nil {
		t.Fatal(err)
	}
	if records != 1 || redemptions != 1 {
		t.Fatal("opening database changed historical card data")
	}
	audits, err := s.ListAudit(ctx, 10)
	if err != nil || len(audits) != 1 || audits[0].Action != "auth.login" {
		t.Fatal("password-only login did not retain audit record")
	}
}
