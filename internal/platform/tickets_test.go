package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func ticketUsers(t *testing.T, s *Store) (User, User, User) {
	t.Helper()
	ctx := context.Background()
	var out []User
	for _, i := range []Identity{{UpstreamID: 1, Login: "admin", Name: "客服"}, {UpstreamID: 2, Login: "alice", Name: "同学甲"}, {UpstreamID: 3, Login: "bob", Name: "同学乙"}} {
		u, err := s.UpsertLogin(ctx, i)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, u)
	}
	return out[0], out[1], out[2]
}

func createTicket(t *testing.T, s *Store, u User, key string) TicketDetail {
	t.Helper()
	out, created, err := s.CreateTicket(context.Background(), u.ID, CreateTicketInput{Subject: "任务进度问题", Category: "task", Body: "正文隐私标记 PRIVATE_BODY_SENTINEL", IdempotencyKey: key})
	if err != nil || !created || len(out.Messages) != 1 || out.Ticket.MessageCount != 1 {
		t.Fatalf("create %+v %v %v", out, created, err)
	}
	return out
}

func TestTicketOwnershipAdminNamespacesAndAuditPrivacy(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, Options{AdminUserIDs: []int64{1}})
	admin, alice, bob := ticketUsers(t, s)
	detail := createTicket(t, s, alice, "create-one")
	id := detail.Ticket.ID
	if detail.Ticket.User != nil {
		t.Fatal("consumer projection leaked identity")
	}
	for _, u := range []User{bob, admin} {
		if _, err := s.GetTicket(ctx, u.ID, id, false); !errors.Is(err, ErrNotFound) {
			t.Fatalf("user namespace cross-owner read: %v", err)
		}
		if _, _, err := s.ReplyTicket(ctx, u.ID, id, "other reply", "reply-one", false); !errors.Is(err, ErrNotFound) {
			t.Fatalf("cross-owner reply: %v", err)
		}
		if _, err := s.SetTicketStatus(ctx, u.ID, id, "closed", false); !errors.Is(err, ErrNotFound) {
			t.Fatalf("cross-owner status: %v", err)
		}
		list, err := s.ListTickets(ctx, u.ID, false, TicketListOptions{})
		if err != nil || len(list.Items) != 0 {
			t.Fatalf("cross-owner list: %+v %v", list, err)
		}
	}
	if _, err := s.GetTicket(ctx, bob.ID, id, true); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ordinary admin namespace read: %v", err)
	}
	got, err := s.GetTicket(ctx, admin.ID, id, true)
	if err != nil || got.Ticket.User == nil || got.Ticket.User.ID != alice.ID || got.Ticket.User.Name != alice.Name {
		t.Fatalf("admin projection: %+v %v", got, err)
	}
	if _, err := s.SetTicketStatus(ctx, alice.ID, id, "resolved", false); !errors.Is(err, ErrTicketInvalid) {
		t.Fatal("user set admin state")
	}
	got, created, err := s.ReplyTicket(ctx, admin.ID, id, "管理员正文 ADMIN_BODY_SENTINEL", "admin-reply-one", true)
	if err != nil || !created || got.Ticket.Status != "processing" || got.Messages[1].AuthorRole != "admin" {
		t.Fatalf("admin reply: %+v %v", got, err)
	}
	if _, err = s.SetTicketStatus(ctx, admin.ID, id, "resolved", true); err != nil {
		t.Fatal(err)
	}
	got, _, err = s.ReplyTicket(ctx, alice.ID, id, "仍需帮助", "user-reply-one", false)
	if err != nil || got.Ticket.Status != "open" {
		t.Fatalf("user reply should reopen resolved: %+v %v", got, err)
	}
	if _, err = s.SetTicketStatus(ctx, alice.ID, id, "closed", false); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.ReplyTicket(ctx, admin.ID, id, "closed reply", "admin-closed-reply", true); !errors.Is(err, ErrTicketConflict) {
		t.Fatalf("closed thread accepted reply: %v", err)
	}
	if _, err = s.SetTicketStatus(ctx, alice.ID, id, "open", false); err != nil {
		t.Fatal(err)
	}
	audit, err := s.ListAudit(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(audit)
	if strings.Contains(string(raw), "BODY_SENTINEL") || strings.Contains(string(raw), "任务进度问题") || strings.Contains(string(raw), "仍需帮助") {
		t.Fatalf("audit contains content: %s", raw)
	}
	if !strings.Contains(string(raw), "ticket.create") || !strings.Contains(string(raw), "ticket.reply") || !strings.Contains(string(raw), "ticket.status") {
		t.Fatal("missing metadata audit")
	}
	if err = s.SetStatus(ctx, admin.ID, alice.ID, "disabled"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetTicket(ctx, alice.ID, id, false); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled read: %v", err)
	}
}

func TestTicketIdempotencyConcurrentCreationAndPersistence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "platform.sqlite")
	s, err := Open(path, Options{AdminUserIDs: []int64{1}})
	if err != nil {
		t.Fatal(err)
	}
	_, alice, _ := ticketUsers(t, s)
	input := CreateTicketInput{Subject: "题库问题", Category: "data", Body: "重试正文", IdempotencyKey: "create-shared"}
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, _, e := s.CreateTicket(ctx, alice.ID, input)
			if e != nil {
				errs <- e
				return
			}
			ids <- d.Ticket.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	var id string
	for got := range ids {
		if id != "" && id != got {
			t.Fatal("concurrent retry duplicated tickets")
		}
		id = got
	}
	input.Body = "changed"
	if _, _, err = s.CreateTicket(ctx, alice.ID, input); !errors.Is(err, ErrTicketConflict) {
		t.Fatalf("creation conflict: %v", err)
	}
	out, created, err := s.ReplyTicket(ctx, alice.ID, id, "reply", "reply-shared", false)
	if err != nil || !created {
		t.Fatal(err)
	}
	reused, created, err := s.ReplyTicket(ctx, alice.ID, id, "reply", "reply-shared", false)
	if err != nil || created || len(reused.Messages) != 2 || out.Messages[1].ID != reused.Messages[1].ID {
		t.Fatal("reply was duplicated")
	}
	if _, _, err = s.ReplyTicket(ctx, alice.ID, id, "different reply", "reply-shared", false); !errors.Is(err, ErrTicketConflict) {
		t.Fatalf("reply conflict: %v", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.GetTicket(ctx, alice.ID, id, false)
	if err != nil || len(got.Messages) != 2 || got.Messages[1].Body != "reply" {
		t.Fatalf("restart lost thread: %+v %v", got, err)
	}
	input.Body = "重试正文"
	if _, created, err = s.CreateTicket(ctx, alice.ID, input); err != nil || created {
		t.Fatalf("restart lost idempotency: %v", err)
	}
}

func TestTicketInputPlainTextLimitsAndPagination(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, Options{AdminUserIDs: []int64{1}})
	_, alice, bob := ticketUsers(t, s)
	base := CreateTicketInput{Subject: "正常标题", Category: "other", Body: "正常正文", IdempotencyKey: "valid-key"}
	for _, mutate := range []func(*CreateTicketInput){
		func(i *CreateTicketInput) { i.Subject = strings.Repeat("字", 101) }, func(i *CreateTicketInput) { i.Body = strings.Repeat("字", 5001) }, func(i *CreateTicketInput) { i.Body = " " }, func(i *CreateTicketInput) { i.Subject = "a\nb" }, func(i *CreateTicketInput) { i.Category = "unknown" }, func(i *CreateTicketInput) { i.IdempotencyKey = "bad key" },
		func(i *CreateTicketInput) { i.Body = "密码: secret-fixture" }, func(i *CreateTicketInput) { i.Body = "Cookie: session-fixture" }, func(i *CreateTicketInput) { i.Body = "sk-0123456789abcdef0123456789abcdef" }, func(i *CreateTicketInput) { i.Body = "Authorization: Bearer fake-secret-token" }, func(i *CreateTicketInput) { i.Body = "https://example.test/?token=secret" }, func(i *CreateTicketInput) { i.Body = "bad\x00text" },
	} {
		in := base
		mutate(&in)
		if _, _, err := s.CreateTicket(ctx, alice.ID, in); !errors.Is(err, ErrTicketInvalid) {
			t.Fatalf("invalid accepted %+v %v", in, err)
		}
	}
	s.now = func() time.Time { return time.Date(2026, 11, 2, 1, 0, 0, 0, time.UTC) }
	for n := 0; n < 5; n++ {
		in := base
		in.IdempotencyKey = fmt.Sprintf("ticket-%08d", n)
		in.Body = "<script>alert('plain text')</script>"
		if _, _, err := s.CreateTicket(ctx, alice.ID, in); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.CreateTicket(ctx, alice.ID, base); !errors.Is(err, ErrTicketLimit) {
		t.Fatalf("creation rate not enforced %v", err)
	}
	createTicket(t, s, bob, "another-user")
	seen := map[string]bool{}
	cursor := ""
	for {
		page, err := s.ListTickets(ctx, alice.ID, false, TicketListOptions{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if seen[item.ID] {
				t.Fatal("duplicate across pages")
			}
			seen[item.ID] = true
			d, e := s.GetTicket(ctx, alice.ID, item.ID, false)
			if e != nil || d.Messages[0].Body != "<script>alert('plain text')</script>" {
				t.Fatal("text was transformed")
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != 5 {
		t.Fatalf("pagination skipped records %d", len(seen))
	}
	for _, opts := range []TicketListOptions{{Limit: 101}, {Status: "wrong"}, {Cursor: "bad"}} {
		if _, err := s.ListTickets(ctx, alice.ID, false, opts); !errors.Is(err, ErrTicketInvalid) {
			t.Fatal("invalid pagination accepted")
		}
	}
	// Rate windows expire, while already-open ticket limits remain in force.
	s.now = func() time.Time { return time.Date(2026, 11, 2, 1, 11, 0, 0, time.UTC) }
	d := createTicket(t, s, alice, "after-window")
	for i := 0; i < 9; i++ {
		if _, _, err := s.ReplyTicket(ctx, alice.ID, d.Ticket.ID, "message", fmt.Sprintf("message-%08d", i), false); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.ReplyTicket(ctx, alice.ID, d.Ticket.ID, "message", "message-over-limit", false); !errors.Is(err, ErrTicketLimit) {
		t.Fatalf("message rate not enforced %v", err)
	}
	if _, err := s.db.Exec("UPDATE support_tickets SET message_count=200 WHERE id=?", d.Ticket.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ReplyTicket(ctx, alice.ID, d.Ticket.ID, "message", "thread-over-limit", false); !errors.Is(err, ErrTicketLimit) {
		t.Fatalf("thread cap not enforced %v", err)
	}
}

func TestTicketActiveCapCannotBeBypassedByReopen(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, Options{AdminUserIDs: []int64{1}})
	_, alice, _ := ticketUsers(t, s)
	now := time.Date(2026, 11, 2, 1, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	var closed string
	for i := 0; i < 21; i++ {
		now = now.Add(11 * time.Minute)
		d := createTicket(t, s, alice, fmt.Sprintf("cap-%08d", i))
		if i == 0 {
			closed = d.Ticket.ID
			if _, err := s.SetTicketStatus(ctx, alice.ID, closed, "closed", false); err != nil {
				t.Fatal(err)
			}
		}
	}
	now = now.Add(11 * time.Minute)
	if _, _, err := s.CreateTicket(ctx, alice.ID, CreateTicketInput{Subject: "over", Category: "other", Body: "over", IdempotencyKey: "over-active-limit"}); !errors.Is(err, ErrTicketLimit) {
		t.Fatalf("active cap not enforced %v", err)
	}
	if _, err := s.SetTicketStatus(ctx, alice.ID, closed, "open", false); !errors.Is(err, ErrTicketLimit) {
		t.Fatalf("reopen bypassed active cap %v", err)
	}
}
