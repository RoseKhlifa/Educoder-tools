package platform

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	ErrTicketInvalid  = errors.New("invalid ticket input")
	ErrTicketConflict = errors.New("ticket idempotency conflict")
	ErrTicketLimit    = errors.New("ticket limit exceeded")
)

const ticketSchema = `
CREATE TABLE IF NOT EXISTS support_tickets (
 id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES platform_users(id),
 subject TEXT NOT NULL, category TEXT NOT NULL CHECK(category IN ('task','account','data','suggestion','other')),
 status TEXT NOT NULL CHECK(status IN ('open','processing','resolved','closed')),
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 message_count INTEGER NOT NULL, last_author_role TEXT NOT NULL,
 idempotency_key TEXT NOT NULL, payload_hash TEXT NOT NULL,
 UNIQUE(owner_id,idempotency_key)
);
CREATE INDEX IF NOT EXISTS support_tickets_owner_updated ON support_tickets(owner_id,updated_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS support_tickets_updated ON support_tickets(updated_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS support_tickets_owner_created ON support_tickets(owner_id,created_at);
CREATE TABLE IF NOT EXISTS support_ticket_messages (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE,
 ticket_id TEXT NOT NULL REFERENCES support_tickets(id), author_id TEXT NOT NULL REFERENCES platform_users(id),
 author_role TEXT NOT NULL CHECK(author_role IN ('user','admin')),
 body TEXT NOT NULL, created_at TEXT NOT NULL,
 idempotency_key TEXT NOT NULL, payload_hash TEXT NOT NULL,
 UNIQUE(author_id,idempotency_key)
);
CREATE INDEX IF NOT EXISTS support_ticket_messages_thread ON support_ticket_messages(ticket_id,seq);
CREATE INDEX IF NOT EXISTS support_ticket_messages_author_created ON support_ticket_messages(author_id,created_at);
`

type TicketUser struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type TicketSummary struct {
	ID             string      `json:"id"`
	Subject        string      `json:"subject"`
	Category       string      `json:"category"`
	Status         string      `json:"status"`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
	MessageCount   int         `json:"message_count"`
	LastAuthorRole string      `json:"last_author_role"`
	User           *TicketUser `json:"user,omitempty"`
}

type TicketMessage struct {
	ID         string    `json:"id"`
	AuthorRole string    `json:"author_role"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"created_at"`
}

type TicketDetail struct {
	Ticket   TicketSummary   `json:"ticket"`
	Messages []TicketMessage `json:"messages"`
}

type TicketPage struct {
	Items      []TicketSummary `json:"items"`
	NextCursor string          `json:"next_cursor"`
	Limit      int             `json:"limit"`
}

type TicketListOptions struct {
	Limit          int
	Cursor, Status string
}
type CreateTicketInput struct{ Subject, Category, Body, IdempotencyKey string }

const ticketStamp = "2006-01-02T15:04:05.000000000Z"

var ticketSecret = regexp.MustCompile(`(?i)(sk-[a-z0-9_-]{16,}|bearer\s+[a-z0-9._~+/-]{12,}|-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----|(?:password|passwd|密码|cookie|authorization|api[_ -]?key|access[_ -]?token|session(?:id)?|密钥)["']?\s*[:=：]\s*\S{4,}|(?:[?&])(?:token|signature|x-amz-signature|access_token)=\S+)`)

func validTicketText(value string, max int) bool {
	if !utf8.ValidString(value) || strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > max || ticketSecret.MatchString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return false
		}
	}
	return true
}

func validTicketKey(key string) bool {
	if len(key) < 8 || len(key) > 128 {
		return false
	}
	for _, c := range key {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

func validTicketStatus(status string) bool {
	return status == "open" || status == "processing" || status == "resolved" || status == "closed"
}
func validTicketCategory(category string) bool {
	return category == "task" || category == "account" || category == "data" || category == "suggestion" || category == "other"
}
func ticketHash(parts ...string) string {
	b, _ := json.Marshal(parts)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Ticket access is checked again inside each transaction. Administrator accounts
// only gain cross-owner access when the caller uses the administrator namespace.
func ticketActor(ctx context.Context, tx *sql.Tx, actor string, admin bool) error {
	u, err := scanUser(tx.QueryRowContext(ctx, "SELECT "+userColumns+" FROM platform_users WHERE id=?", actor))
	if err != nil {
		return err
	}
	if u.Status != "active" {
		return ErrDisabled
	}
	if admin && !u.IsAdmin() {
		return ErrForbidden
	}
	return nil
}

const ticketColumns = "t.id,t.subject,t.category,t.status,t.created_at,t.updated_at,t.message_count,t.last_author_role,u.id,u.name"

func scanTicket(row scanner, admin bool) (TicketSummary, error) {
	var t TicketSummary
	var created, updated string
	var u TicketUser
	err := row.Scan(&t.ID, &t.Subject, &t.Category, &t.Status, &created, &updated, &t.MessageCount, &t.LastAuthorRole, &u.ID, &u.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	if err != nil {
		return t, err
	}
	t.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	t.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	if admin {
		t.User = &u
	}
	return t, nil
}

func ticketByID(ctx context.Context, tx *sql.Tx, actor, id string, admin bool) (TicketSummary, error) {
	query := "SELECT " + ticketColumns + " FROM support_tickets t JOIN platform_users u ON u.id=t.owner_id WHERE t.id=?"
	args := []any{id}
	if !admin {
		query += " AND t.owner_id=?"
		args = append(args, actor)
	}
	return scanTicket(tx.QueryRowContext(ctx, query, args...), admin)
}

func ticketDetail(ctx context.Context, tx *sql.Tx, actor, id string, admin bool) (TicketDetail, error) {
	t, err := ticketByID(ctx, tx, actor, id, admin)
	if err != nil {
		return TicketDetail{}, err
	}
	out := TicketDetail{Ticket: t, Messages: []TicketMessage{}}
	rows, err := tx.QueryContext(ctx, "SELECT id,author_role,body,created_at FROM support_ticket_messages WHERE ticket_id=? ORDER BY seq", id)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var m TicketMessage
		var stamp string
		if err = rows.Scan(&m.ID, &m.AuthorRole, &m.Body, &stamp); err != nil {
			return out, err
		}
		m.CreatedAt, _ = time.Parse(time.RFC3339Nano, stamp)
		out.Messages = append(out.Messages, m)
	}
	return out, rows.Err()
}

func (s *Store) GetTicket(ctx context.Context, actor, id string, admin bool) (TicketDetail, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TicketDetail{}, err
	}
	defer tx.Rollback()
	if err = ticketActor(ctx, tx, actor, admin); err != nil {
		return TicketDetail{}, err
	}
	return ticketDetail(ctx, tx, actor, id, admin)
}

type ticketCursor struct {
	Stamp string `json:"at"`
	ID    string `json:"id"`
}

func (s *Store) ListTickets(ctx context.Context, actor string, admin bool, opts TicketListOptions) (TicketPage, error) {
	if opts.Limit <= 0 {
		opts.Limit = 30
	}
	if opts.Limit > 100 {
		return TicketPage{}, ErrTicketInvalid
	}
	if opts.Status != "" && !validTicketStatus(opts.Status) {
		return TicketPage{}, ErrTicketInvalid
	}
	var cursor ticketCursor
	if opts.Cursor != "" {
		if len(opts.Cursor) > 512 {
			return TicketPage{}, ErrTicketInvalid
		}
		raw, err := base64.RawURLEncoding.DecodeString(opts.Cursor)
		if err != nil || len(raw) > 256 || json.Unmarshal(raw, &cursor) != nil || len(cursor.ID) != 32 {
			return TicketPage{}, ErrTicketInvalid
		}
		if _, err = time.Parse(ticketStamp, cursor.Stamp); err != nil {
			return TicketPage{}, ErrTicketInvalid
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TicketPage{}, err
	}
	defer tx.Rollback()
	if err = ticketActor(ctx, tx, actor, admin); err != nil {
		return TicketPage{}, err
	}
	query := "SELECT " + ticketColumns + " FROM support_tickets t JOIN platform_users u ON u.id=t.owner_id WHERE 1=1"
	args := []any{}
	if !admin {
		query += " AND t.owner_id=?"
		args = append(args, actor)
	}
	if opts.Status != "" {
		query += " AND t.status=?"
		args = append(args, opts.Status)
	}
	if opts.Cursor != "" {
		query += " AND (t.updated_at<? OR (t.updated_at=? AND t.id<?))"
		args = append(args, cursor.Stamp, cursor.Stamp, cursor.ID)
	}
	query += " ORDER BY t.updated_at DESC,t.id DESC LIMIT ?"
	args = append(args, opts.Limit+1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return TicketPage{}, err
	}
	defer rows.Close()
	out := TicketPage{Items: []TicketSummary{}, Limit: opts.Limit}
	for rows.Next() {
		t, e := scanTicket(rows, admin)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, t)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) > opts.Limit {
		out.Items = out.Items[:opts.Limit]
		last := out.Items[len(out.Items)-1]
		raw, _ := json.Marshal(ticketCursor{Stamp: last.UpdatedAt.UTC().Format(ticketStamp), ID: last.ID})
		out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return out, nil
}

func (s *Store) CreateTicket(ctx context.Context, actor string, input CreateTicketInput) (TicketDetail, bool, error) {
	input.Subject = strings.TrimSpace(input.Subject)
	input.Body = strings.TrimSpace(input.Body)
	if !validTicketText(input.Subject, 100) || strings.ContainsAny(input.Subject, "\r\n") || !validTicketText(input.Body, 5000) || !validTicketCategory(input.Category) || !validTicketKey(input.IdempotencyKey) {
		return TicketDetail{}, false, ErrTicketInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TicketDetail{}, false, err
	}
	defer tx.Rollback()
	if err = ticketActor(ctx, tx, actor, false); err != nil {
		return TicketDetail{}, false, err
	}
	hash := ticketHash(input.Subject, input.Category, input.Body)
	var existing, previousHash string
	err = tx.QueryRowContext(ctx, "SELECT id,payload_hash FROM support_tickets WHERE owner_id=? AND idempotency_key=?", actor, input.IdempotencyKey).Scan(&existing, &previousHash)
	if err == nil {
		if hash != previousHash {
			return TicketDetail{}, false, ErrTicketConflict
		}
		detail, e := ticketDetail(ctx, tx, actor, existing, false)
		return detail, false, e
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return TicketDetail{}, false, err
	}
	now := s.now().UTC()
	stamp := now.Format(ticketStamp)
	var recent, active int
	err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM support_tickets WHERE owner_id=? AND status<>'closed'", actor).Scan(&active)
	if err != nil {
		return TicketDetail{}, false, err
	}
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM support_tickets WHERE owner_id=? AND created_at>=?", actor, now.Add(-10*time.Minute).Format(ticketStamp)).Scan(&recent); err != nil {
		return TicketDetail{}, false, err
	}
	if recent >= 5 || active >= 20 {
		return TicketDetail{}, false, ErrTicketLimit
	}
	id, err := randomID()
	if err != nil {
		return TicketDetail{}, false, err
	}
	mid, err := randomID()
	if err != nil {
		return TicketDetail{}, false, err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO support_tickets (id,owner_id,subject,category,status,created_at,updated_at,message_count,last_author_role,idempotency_key,payload_hash) VALUES (?,?,?,?,?,?,?,?,?,?,?)", id, actor, input.Subject, input.Category, "open", stamp, stamp, 1, "user", input.IdempotencyKey, hash)
	if err != nil {
		return TicketDetail{}, false, err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO support_ticket_messages (id,ticket_id,author_id,author_role,body,created_at,idempotency_key,payload_hash) VALUES (?,?,?,?,?,?,?,?)", mid, id, actor, "user", input.Body, stamp, "initial."+id, hash)
	if err != nil {
		return TicketDetail{}, false, err
	}
	if err = appendAudit(ctx, tx, actor, "ticket.create", id, map[string]string{"category": input.Category, "status": "open"}, now); err != nil {
		return TicketDetail{}, false, err
	}
	out, err := ticketDetail(ctx, tx, actor, id, false)
	if err != nil {
		return out, false, err
	}
	if err = tx.Commit(); err != nil {
		return out, false, err
	}
	return out, true, nil
}

func (s *Store) ReplyTicket(ctx context.Context, actor, id, body, key string, admin bool) (TicketDetail, bool, error) {
	body = strings.TrimSpace(body)
	if !validTicketText(body, 5000) || !validTicketKey(key) {
		return TicketDetail{}, false, ErrTicketInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TicketDetail{}, false, err
	}
	defer tx.Rollback()
	if err = ticketActor(ctx, tx, actor, admin); err != nil {
		return TicketDetail{}, false, err
	}
	t, err := ticketByID(ctx, tx, actor, id, admin)
	if err != nil {
		return TicketDetail{}, false, err
	}
	role := "user"
	if admin {
		role = "admin"
	}
	hash := ticketHash(id, body, role)
	var previousHash string
	err = tx.QueryRowContext(ctx, "SELECT payload_hash FROM support_ticket_messages WHERE author_id=? AND idempotency_key=?", actor, key).Scan(&previousHash)
	if err == nil {
		if previousHash != hash {
			return TicketDetail{}, false, ErrTicketConflict
		}
		out, e := ticketDetail(ctx, tx, actor, id, admin)
		return out, false, e
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return TicketDetail{}, false, err
	}
	if t.Status == "closed" {
		return TicketDetail{}, false, ErrTicketConflict
	}
	if t.MessageCount >= 200 {
		return TicketDetail{}, false, ErrTicketLimit
	}
	now := s.now().UTC()
	stamp := now.Format(ticketStamp)
	var recent int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM support_ticket_messages WHERE author_id=? AND created_at>=?", actor, now.Add(-time.Minute).Format(ticketStamp)).Scan(&recent); err != nil {
		return TicketDetail{}, false, err
	}
	if recent >= 10 {
		return TicketDetail{}, false, ErrTicketLimit
	}
	mid, err := randomID()
	if err != nil {
		return TicketDetail{}, false, err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO support_ticket_messages (id,ticket_id,author_id,author_role,body,created_at,idempotency_key,payload_hash) VALUES (?,?,?,?,?,?,?,?)", mid, id, actor, role, body, stamp, key, hash)
	if err != nil {
		return TicketDetail{}, false, err
	}
	status := t.Status
	if !admin {
		status = "open"
	} else if status != "resolved" {
		status = "processing"
	}
	_, err = tx.ExecContext(ctx, "UPDATE support_tickets SET status=?,updated_at=?,message_count=message_count+1,last_author_role=? WHERE id=?", status, stamp, role, id)
	if err != nil {
		return TicketDetail{}, false, err
	}
	if err = appendAudit(ctx, tx, actor, "ticket.reply", id, map[string]string{"author_role": role, "status": status}, now); err != nil {
		return TicketDetail{}, false, err
	}
	out, err := ticketDetail(ctx, tx, actor, id, admin)
	if err != nil {
		return out, false, err
	}
	if err = tx.Commit(); err != nil {
		return out, false, err
	}
	return out, true, nil
}

func (s *Store) SetTicketStatus(ctx context.Context, actor, id, status string, admin bool) (TicketDetail, error) {
	if !validTicketStatus(status) || !admin && (status != "open" && status != "closed") {
		return TicketDetail{}, ErrTicketInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TicketDetail{}, err
	}
	defer tx.Rollback()
	if err = ticketActor(ctx, tx, actor, admin); err != nil {
		return TicketDetail{}, err
	}
	t, err := ticketByID(ctx, tx, actor, id, admin)
	if err != nil {
		return TicketDetail{}, err
	}
	if t.Status == status {
		return ticketDetail(ctx, tx, actor, id, admin)
	}
	now := s.now().UTC()
	var recent, active int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM platform_audit WHERE actor_id=? AND action='ticket.status' AND created_at>=?", actor, now.Add(-10*time.Minute).Format(time.RFC3339Nano)).Scan(&recent); err != nil {
		return TicketDetail{}, err
	}
	if recent >= 30 {
		return TicketDetail{}, ErrTicketLimit
	}
	if status != "closed" && t.Status == "closed" {
		var owner string
		if err = tx.QueryRowContext(ctx, "SELECT owner_id FROM support_tickets WHERE id=?", id).Scan(&owner); err != nil {
			return TicketDetail{}, err
		}
		if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM support_tickets WHERE owner_id=? AND status<>'closed'", owner).Scan(&active); err != nil {
			return TicketDetail{}, err
		}
		if active >= 20 {
			return TicketDetail{}, ErrTicketLimit
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE support_tickets SET status=?,updated_at=? WHERE id=?", status, now.Format(ticketStamp), id); err != nil {
		return TicketDetail{}, err
	}
	if err = appendAudit(ctx, tx, actor, "ticket.status", id, map[string]string{"from": t.Status, "status": status}, now); err != nil {
		return TicketDetail{}, err
	}
	out, err := ticketDetail(ctx, tx, actor, id, admin)
	if err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	return out, nil
}
