package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type stored struct {
	Job
	owner, payload, target string
	mutating               bool
}

func openStore(ctx context.Context, cfg Config) (*sql.DB, error) {
	if cfg.Path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(cfg.Path), 0700); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(cfg.Path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		_ = f.Close()
		if err = os.Chmod(cfg.Path, 0600); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", cfg.Path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, q := range []string{
		`PRAGMA busy_timeout=5000`, `PRAGMA journal_mode=WAL`,
		`CREATE TABLE IF NOT EXISTS async_jobs (id TEXT PRIMARY KEY, owner TEXT NOT NULL, status TEXT NOT NULL, phase TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, payload TEXT NOT NULL, target TEXT NOT NULL, idempotency_key TEXT NOT NULL DEFAULT '', mutating INTEGER NOT NULL DEFAULT 0, data BLOB NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS async_jobs_owner_created ON async_jobs(owner,created_at DESC)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS async_jobs_idempotency ON async_jobs(owner,idempotency_key) WHERE idempotency_key <> ''`,
		`CREATE TABLE IF NOT EXISTS async_job_keys (owner TEXT NOT NULL, key TEXT NOT NULL, payload TEXT NOT NULL, job_id TEXT NOT NULL, PRIMARY KEY(owner,key))`,
		`INSERT OR IGNORE INTO async_job_keys(owner,key,payload,job_id) SELECT owner,idempotency_key,payload,id FROM async_jobs WHERE idempotency_key <> ''`,
	} {
		if _, err = db.ExecContext(ctx, q); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	return db, nil
}

type storeWriter interface {
	Exec(string, ...any) (sql.Result, error)
}

func writeStore(db storeWriter, s *stored, insert bool) error {
	b, err := json.Marshal(s.Job)
	if err != nil {
		return err
	}
	if insert {
		_, err = db.Exec(`INSERT INTO async_jobs(id,owner,status,phase,created_at,updated_at,payload,target,idempotency_key,mutating,data) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, s.ID, s.owner, s.Status, s.Phase, s.CreatedAt.UnixNano(), s.UpdatedAt.UnixNano(), s.payload, s.target, s.Request.IdempotencyKey, s.mutating, b)
	} else {
		_, err = db.Exec(`UPDATE async_jobs SET status=?,phase=?,updated_at=?,mutating=?,data=? WHERE id=?`, s.Status, s.Phase, s.UpdatedAt.UnixNano(), s.mutating, b, s.ID)
	}
	return err
}

func insertStore(db *sql.DB, s *stored) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = writeStore(tx, s, true); err != nil {
		return err
	}
	if s.Request.IdempotencyKey != "" {
		if _, err = tx.Exec(`INSERT INTO async_job_keys(owner,key,payload,job_id) VALUES(?,?,?,?)`, s.owner, s.Request.IdempotencyKey, s.payload, s.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func readStored(row interface{ Scan(...any) error }) (*stored, error) {
	s := &stored{}
	var b []byte
	if err := row.Scan(&s.owner, &s.payload, &s.target, &s.mutating, &b); err != nil {
		if err == sql.ErrNoRows {
			return nil, fail("not_found", "Job not found")
		}
		return nil, err
	}
	if err := json.Unmarshal(b, &s.Job); err != nil {
		return nil, fmt.Errorf("decode stored job: %w", err)
	}
	return s, nil
}

func recoverStore(ctx context.Context, db *sql.DB, now time.Time, retention time.Duration) error {
	rows, err := db.QueryContext(ctx, `SELECT owner,payload,target,mutating,data FROM async_jobs WHERE status IN ('queued','running')`)
	if err != nil {
		return err
	}
	var interrupted []*stored
	for rows.Next() {
		s, e := readStored(rows)
		if e != nil {
			_ = rows.Close()
			return e
		}
		interrupted = append(interrupted, s)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	for _, s := range interrupted {
		s.Status = Interrupted
		s.ErrorCode = "service_restarted"
		s.Message = "Service restarted; this job was not replayed"
		if s.mutating {
			s.Status = Uncertain
			s.ErrorCode = "submission_uncertain"
			s.Message = "Service restarted after submission began; check the upstream result before retrying"
		}
		s.UpdatedAt = now
		s.FinishedAt = &now
		if err = writeStore(db, s, false); err != nil {
			return err
		}
	}
	_, err = db.ExecContext(ctx, `DELETE FROM async_jobs WHERE status NOT IN ('queued','running') AND updated_at < ?`, now.Add(-retention).UnixNano())
	if err == nil {
		_, err = db.ExecContext(ctx, `DELETE FROM async_job_keys WHERE job_id NOT IN (SELECT id FROM async_jobs)`)
	}
	return err
}
