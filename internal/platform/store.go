// Package platform stores verified platform identities and service permissions.
// It never stores upstream passwords, cookies, session tokens or model secrets.
package platform

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrInvalidIdentity = errors.New("verified upstream numeric identity required")
	ErrDisabled        = errors.New("platform user disabled")
	ErrCardRequired    = errors.New("access card required")
	ErrInvalidCard     = errors.New("access card unavailable")
	ErrNotFound        = errors.New("platform record not found")
	ErrForbidden       = errors.New("administrator required")
)

type Options struct {
	AdminUserIDs       []int64
	AccessCardRequired bool
}

type Identity struct {
	UpstreamID int64
	Login      string
	Name       string
}

type User struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	UpstreamID  int64     `json:"upstream_id"`
	Login       string    `json:"login"`
	Name        string    `json:"name"`
	Role        string    `json:"role"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	LastLoginAt time.Time `json:"last_login_at"`
}

func (u User) Owner() string { return "platform:" + u.ID }
func (u User) IsAdmin() bool { return u.Role == "admin" && u.Status == "active" }

type Audit struct {
	ID        int64           `json:"id"`
	ActorID   string          `json:"actor_id"`
	Action    string          `json:"action"`
	TargetID  string          `json:"target_id"`
	Details   json.RawMessage `json:"details"`
	CreatedAt time.Time       `json:"created_at"`
}

type Stats struct {
	TotalUsers    int `json:"total_users"`
	ActiveUsers   int `json:"active_users"`
	DisabledUsers int `json:"disabled_users"`
}

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var stats Stats
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(SUM(CASE WHEN status='active' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN status='disabled' THEN 1 ELSE 0 END),0) FROM platform_users").Scan(&stats.TotalUsers, &stats.ActiveUsers, &stats.DisabledUsers)
	return stats, err
}

type Store struct {
	db          *sql.DB
	admins      map[int64]bool
	requireCard bool
	now         func() time.Time
}

const schema = `
CREATE TABLE IF NOT EXISTS platform_users (
 id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL UNIQUE,
 upstream_id INTEGER NOT NULL UNIQUE CHECK(upstream_id>0), login TEXT NOT NULL,
 name TEXT NOT NULL, role TEXT NOT NULL CHECK(role IN ('user','admin')),
 status TEXT NOT NULL CHECK(status IN ('active','disabled')),
 created_at TEXT NOT NULL, last_login_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS access_cards (
 id TEXT PRIMARY KEY, code_hash TEXT NOT NULL UNIQUE, masked_code TEXT NOT NULL,
 label TEXT NOT NULL, status TEXT NOT NULL CHECK(status IN ('active','revoked')),
 bound_user_id TEXT REFERENCES platform_users(id), reusable INTEGER NOT NULL DEFAULT 0,
 expires_at TEXT NOT NULL, created_at TEXT NOT NULL, redeemed_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS platform_audit (
 id INTEGER PRIMARY KEY AUTOINCREMENT, actor_id TEXT NOT NULL, action TEXT NOT NULL,
 target_id TEXT NOT NULL, details_json TEXT NOT NULL, created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS access_card_redemptions (
 card_id TEXT NOT NULL REFERENCES access_cards(id), user_id TEXT NOT NULL REFERENCES platform_users(id),
 redeemed_at TEXT NOT NULL, PRIMARY KEY(card_id,user_id)
);
CREATE INDEX IF NOT EXISTS platform_audit_created ON platform_audit(created_at);
CREATE TABLE IF NOT EXISTS job_course_labels (
 course_identifier TEXT PRIMARY KEY, name TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS job_homework_labels (
 course_identifier TEXT NOT NULL, homework_id INTEGER NOT NULL CHECK(homework_id>0),
 name TEXT NOT NULL, updated_at TEXT NOT NULL,
 PRIMARY KEY(course_identifier,homework_id)
);
`

func Open(path string, options Options) (*Store, error) {
	if path == "" || strings.HasPrefix(path, "file:") {
		return nil, errors.New("platform database requires a local path")
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		if err = f.Close(); err != nil {
			return nil, err
		}
		if err = os.Chmod(path, 0600); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	for _, statement := range []string{"PRAGMA busy_timeout=5000", "PRAGMA foreign_keys=ON", "PRAGMA journal_mode=WAL", schema} {
		if _, err = db.Exec(statement); err != nil {
			db.Close()
			return nil, fmt.Errorf("initialize platform database: %w", err)
		}
	}
	s := &Store{db: db, admins: make(map[int64]bool), requireCard: options.AccessCardRequired, now: time.Now}
	for _, id := range options.AdminUserIDs {
		if id > 0 {
			s.admins[id] = true
		}
	}
	return s, nil
}

func (s *Store) Close() error             { return s.db.Close() }
func (s *Store) AccessCardRequired() bool { return s.requireCard }

// CanAccess checks current status, including card revocation and expiry, so
// revoking a grant also invalidates an existing browser session.
func (s *Store) CanAccess(ctx context.Context, user User) (bool, error) {
	if user.Status != "active" {
		return false, nil
	}
	if !s.requireCard || user.IsAdmin() {
		return true, nil
	}
	rows, err := s.db.QueryContext(ctx, "SELECT c.expires_at FROM access_cards c JOIN access_card_redemptions r ON r.card_id=c.id WHERE r.user_id=? AND c.status='active' AND (c.bound_user_id IS NULL OR c.bound_user_id=?)", user.ID, user.ID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var expiry string
		if err = rows.Scan(&expiry); err != nil {
			return false, err
		}
		if expiry == "" {
			return true, nil
		}
		t, e := time.Parse(time.RFC3339Nano, expiry)
		if e == nil && t.After(s.now()) {
			return true, nil
		}
	}
	return false, rows.Err()
}

const userColumns = "id,tenant_id,upstream_id,login,name,role,status,created_at,last_login_at"

type scanner interface{ Scan(...any) error }

func scanUser(row scanner) (User, error) {
	var u User
	var created, last string
	err := row.Scan(&u.ID, &u.TenantID, &u.UpstreamID, &u.Login, &u.Name, &u.Role, &u.Status, &created, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	if err != nil {
		return u, err
	}
	u.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	u.LastLoginAt, _ = time.Parse(time.RFC3339Nano, last)
	return u, nil
}

// UpsertLogin must only receive identity returned by successful Educoder login.
// Creation, card binding and login audit commit atomically.
func (s *Store) UpsertLogin(ctx context.Context, identity Identity, accessCode string) (User, error) {
	if identity.UpstreamID <= 0 || strings.TrimSpace(identity.Login) == "" {
		return User{}, ErrInvalidIdentity
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	u, err := scanUser(tx.QueryRowContext(ctx, "SELECT "+userColumns+" FROM platform_users WHERE upstream_id=?", identity.UpstreamID))
	now := s.now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	if errors.Is(err, ErrNotFound) {
		id, e := randomID()
		if e != nil {
			return User{}, e
		}
		role := "user"
		if s.admins[identity.UpstreamID] {
			role = "admin"
		}
		u = User{ID: id, TenantID: "tenant_" + id, UpstreamID: identity.UpstreamID, Login: strings.TrimSpace(identity.Login), Name: identity.Name, Role: role, Status: "active", CreatedAt: now, LastLoginAt: now}
		_, err = tx.ExecContext(ctx, "INSERT INTO platform_users ("+userColumns+") VALUES (?,?,?,?,?,?,?,?,?)", u.ID, u.TenantID, u.UpstreamID, u.Login, u.Name, u.Role, u.Status, stamp, stamp)
	} else if err == nil {
		if u.Status != "active" {
			return User{}, ErrDisabled
		}
		u.Login = strings.TrimSpace(identity.Login)
		u.Name = identity.Name
		u.LastLoginAt = now
		if s.admins[u.UpstreamID] {
			u.Role = "admin"
		}
		_, err = tx.ExecContext(ctx, "UPDATE platform_users SET login=?,name=?,role=?,last_login_at=? WHERE id=?", u.Login, u.Name, u.Role, stamp, u.ID)
	}
	if err != nil {
		return User{}, err
	}
	if s.requireCard && !u.IsAdmin() {
		if strings.TrimSpace(accessCode) == "" {
			return User{}, ErrCardRequired
		}
		if err = s.redeemTx(ctx, tx, u.ID, accessCode, now); err != nil {
			return User{}, err
		}
	}
	if err = appendAudit(ctx, tx, u.ID, "auth.login", u.ID, map[string]string{"role": u.Role}, now); err != nil {
		return User{}, err
	}
	if err = tx.Commit(); err != nil {
		return User{}, err
	}
	return u, nil
}

func (s *Store) LookupUser(ctx context.Context, id string) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx, "SELECT "+userColumns+" FROM platform_users WHERE id=?", id))
}

func (s *Store) ListUsers(ctx context.Context, limit int) ([]User, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+userColumns+" FROM platform_users ORDER BY last_login_at DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []User{}
	for rows.Next() {
		u, e := scanUser(rows)
		if e != nil {
			return nil, e
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Store) SetStatus(ctx context.Context, actor, target, status string) error {
	if status != "active" && status != "disabled" {
		return errors.New("invalid user status")
	}
	return s.changeUser(ctx, actor, target, "user.status", "status", status)
}

func (s *Store) SetRole(ctx context.Context, actor, target, role string) error {
	if role != "user" && role != "admin" {
		return errors.New("invalid user role")
	}
	return s.changeUser(ctx, actor, target, "user.role", "role", role)
}

func (s *Store) changeUser(ctx context.Context, actor, target, action, column, value string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = checkAdmin(ctx, tx, actor); err != nil {
		return err
	}
	// Prevent accidental self-lockout. A different active administrator can act.
	if actor == target && (value == "disabled" || value == "user") {
		return ErrForbidden
	}
	result, err := tx.ExecContext(ctx, "UPDATE platform_users SET "+column+"=? WHERE id=?", value, target)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrNotFound
	}
	if err = appendAudit(ctx, tx, actor, action, target, map[string]string{column: value}, s.now()); err != nil {
		return err
	}
	return tx.Commit()
}

func checkAdmin(ctx context.Context, tx *sql.Tx, id string) error {
	u, err := scanUser(tx.QueryRowContext(ctx, "SELECT "+userColumns+" FROM platform_users WHERE id=?", id))
	if err != nil || !u.IsAdmin() {
		return ErrForbidden
	}
	return nil
}

func appendAudit(ctx context.Context, tx *sql.Tx, actor, action, target string, details any, now time.Time) error {
	body, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO platform_audit (actor_id,action,target_id,details_json,created_at) VALUES (?,?,?,?,?)", actor, action, target, string(body), now.UTC().Format(time.RFC3339Nano))
	return err
}

// RecordAudit accepts only non-secret operational metadata, never request bodies.
func (s *Store) RecordAudit(ctx context.Context, actor, action, target string, details map[string]string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = appendAudit(ctx, tx, actor, action, target, details, s.now()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListAudit(ctx context.Context, limit int) ([]Audit, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id,actor_id,action,target_id,details_json,created_at FROM platform_audit ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Audit{}
	for rows.Next() {
		var a Audit
		var details, stamp string
		if err = rows.Scan(&a.ID, &a.ActorID, &a.Action, &a.TargetID, &details, &stamp); err != nil {
			return nil, err
		}
		a.Details = json.RawMessage(details)
		a.CreatedAt, _ = time.Parse(time.RFC3339Nano, stamp)
		out = append(out, a)
	}
	return out, rows.Err()
}

func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
