package platform

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

type Card struct {
	ID          string     `json:"id"`
	MaskedCode  string     `json:"masked_code"`
	Label       string     `json:"label"`
	Status      string     `json:"status"`
	BoundUserID string     `json:"bound_user_id,omitempty"`
	Reusable    bool       `json:"reusable"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	RedeemedAt  *time.Time `json:"redeemed_at,omitempty"`
}

type CardOptions struct {
	Label       string
	BoundUserID string
	Reusable    bool
	ExpiresAt   time.Time
}

type IssuedCard struct {
	Card Card `json:"card"`
	// Code is returned exactly once to the issuing administrator, never stored.
	Code string `json:"code"`
}

func (s *Store) IssueCard(ctx context.Context, actor string, options CardOptions) (IssuedCard, error) {
	if len(options.Label) > 120 {
		return IssuedCard{}, errors.New("card label too long")
	}
	if !options.ExpiresAt.IsZero() && !options.ExpiresAt.After(s.now()) {
		return IssuedCard{}, errors.New("card expiry must be in the future")
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return IssuedCard{}, err
	}
	code := "TG-" + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
	id, err := randomID()
	if err != nil {
		return IssuedCard{}, err
	}
	now := s.now().UTC()
	card := Card{ID: id, MaskedCode: code[:9] + "…", Label: strings.TrimSpace(options.Label), Status: "active", BoundUserID: options.BoundUserID, Reusable: options.Reusable, CreatedAt: now}
	expires := ""
	if !options.ExpiresAt.IsZero() {
		t := options.ExpiresAt.UTC()
		card.ExpiresAt = &t
		expires = t.Format(time.RFC3339Nano)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return IssuedCard{}, err
	}
	defer tx.Rollback()
	if err = checkAdmin(ctx, tx, actor); err != nil {
		return IssuedCard{}, err
	}
	var bound any
	if options.BoundUserID != "" {
		bound = options.BoundUserID
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO access_cards (id,code_hash,masked_code,label,status,bound_user_id,reusable,expires_at,created_at) VALUES (?,?,?,?,?,?,?,?,?)", id, hashCode(code), card.MaskedCode, card.Label, card.Status, bound, card.Reusable, expires, now.Format(time.RFC3339Nano))
	if err != nil {
		return IssuedCard{}, err
	}
	if err = appendAudit(ctx, tx, actor, "card.issue", id, map[string]string{"masked_code": card.MaskedCode}, now); err != nil {
		return IssuedCard{}, err
	}
	if err = tx.Commit(); err != nil {
		return IssuedCard{}, err
	}
	return IssuedCard{Card: card, Code: code}, nil
}

func hashCode(code string) string {
	sum := sha256.Sum256([]byte(strings.ToUpper(strings.TrimSpace(code))))
	return hex.EncodeToString(sum[:])
}

func (s *Store) redeemTx(ctx context.Context, tx *sql.Tx, userID, code string, now time.Time) error {
	var id, status, expiry string
	var bound sql.NullString
	var reusable bool
	err := tx.QueryRowContext(ctx, "SELECT id,status,bound_user_id,reusable,expires_at FROM access_cards WHERE code_hash=?", hashCode(code)).Scan(&id, &status, &bound, &reusable, &expiry)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidCard
		}
		return err
	}
	if status != "active" {
		return ErrInvalidCard
	}
	if expiry != "" {
		t, e := time.Parse(time.RFC3339Nano, expiry)
		if e != nil || !t.After(now) {
			return ErrInvalidCard
		}
	}
	if bound.Valid && bound.String != "" && bound.String != userID {
		return ErrInvalidCard
	}
	if !reusable && !bound.Valid {
		result, e := tx.ExecContext(ctx, "UPDATE access_cards SET bound_user_id=?,redeemed_at=? WHERE id=? AND bound_user_id IS NULL AND status='active'", userID, now.UTC().Format(time.RFC3339Nano), id)
		if e != nil {
			return e
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return ErrInvalidCard
		}
	}
	result, err := tx.ExecContext(ctx, "INSERT INTO access_card_redemptions (card_id,user_id,redeemed_at) VALUES (?,?,?) ON CONFLICT(card_id,user_id) DO NOTHING", id, userID, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n > 0 {
		return appendAudit(ctx, tx, userID, "card.redeem", id, map[string]string{"status": "granted"}, now)
	}
	return nil
}

func (s *Store) RevokeCard(ctx context.Context, actor, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = checkAdmin(ctx, tx, actor); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, "UPDATE access_cards SET status='revoked' WHERE id=?", id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrNotFound
	}
	if err = appendAudit(ctx, tx, actor, "card.revoke", id, map[string]string{"status": "revoked"}, s.now()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListCards(ctx context.Context, limit int) ([]Card, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id,masked_code,label,status,COALESCE(bound_user_id,''),reusable,expires_at,created_at,redeemed_at FROM access_cards ORDER BY created_at DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cards := []Card{}
	for rows.Next() {
		var c Card
		var expiry, created, redeemed string
		if err = rows.Scan(&c.ID, &c.MaskedCode, &c.Label, &c.Status, &c.BoundUserID, &c.Reusable, &expiry, &created, &redeemed); err != nil {
			return nil, err
		}
		c.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		if expiry != "" {
			t, _ := time.Parse(time.RFC3339Nano, expiry)
			c.ExpiresAt = &t
		}
		if redeemed != "" {
			t, _ := time.Parse(time.RFC3339Nano, redeemed)
			c.RedeemedAt = &t
		}
		cards = append(cards, c)
	}
	return cards, rows.Err()
}
