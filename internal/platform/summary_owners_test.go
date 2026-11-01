package platform

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestVerifiedJobOwnersReadsAllLinkedAccountsWithoutDirectoryLimit(t *testing.T) {
	s := openTest(t, Options{})
	ctx := context.Background()
	// The UI directory has a 500 row cap. Statistics must cover account 501
	// and beyond without returning any personal directory fields.
	for i := 1; i <= 510; i++ {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO platform_users(id,tenant_id,upstream_id,login,name,role,status,created_at,last_login_at) VALUES(?,?,?,?,?,?,?,?,?)`, fmt.Sprintf("id-%d", i), fmt.Sprintf("tenant-%d", i), i, "PRIVATE_LOGIN", "PRIVATE_NAME", "user", "active", "2026-11-02T00:00:00Z", "2026-11-02T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	// Disabling an account does not erase the service's completed work.
	if _, err := s.db.ExecContext(ctx, `UPDATE platform_users SET status='disabled' WHERE id='id-510'`); err != nil {
		t.Fatal(err)
	}
	owners, err := s.VerifiedJobOwners(ctx)
	if err != nil || len(owners) != 510 {
		t.Fatalf("unbounded linked accounts = %d, %v", len(owners), err)
	}
	seen := map[string]bool{}
	for _, owner := range owners {
		if !strings.HasPrefix(owner, "platform:id-") || strings.Contains(owner, "PRIVATE_") || seen[owner] {
			t.Fatalf("owner projection contains duplicate or unrelated directory data: %q", owner)
		}
		seen[owner] = true
	}
	if !seen["platform:id-510"] {
		t.Fatal("linked disabled account was removed from completion history")
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.VerifiedJobOwners(ctx); err == nil {
		t.Fatal("canceled owner lookup fabricated an empty registry")
	}
}
