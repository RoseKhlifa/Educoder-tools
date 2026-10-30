// Package questionbank stores immutable candidate sources and append-only
// validation evidence. It never takes an upstream protocol client or save key.
package questionbank

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"educoder-cli/internal/problem"
	_ "modernc.org/sqlite"
)

//go:embed migrations/001_initial.sql
var initialMigration string

type Store struct {
	db *sql.DB
	mu sync.Mutex
}

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("question bank path is required")
	}
	if path != ":memory:" {
		if strings.HasPrefix(path, "file:") {
			return nil, errors.New("question bank path must be a local filesystem path")
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		if err = file.Close(); err != nil {
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
	// A single connection keeps connection-local PRAGMAs in effect and serializes
	// short transactions. No model or network operation holds a transaction.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{db: db}
	for _, pragma := range []string{"PRAGMA busy_timeout=5000", "PRAGMA foreign_keys=ON", "PRAGMA journal_mode=WAL"} {
		if _, err = db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("configure question bank: %w", err)
		}
	}
	tx, err := db.Begin()
	if err != nil {
		db.Close()
		return nil, err
	}
	if _, err = tx.Exec(initialMigration); err != nil {
		tx.Rollback()
		db.Close()
		return nil, fmt.Errorf("migrate question bank: %w", err)
	}
	if err = tx.Commit(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error      { return s.db.Close() }
func now() string                  { return time.Now().UTC().Format(time.RFC3339Nano) }
func parseTime(s string) time.Time { t, _ := time.Parse(time.RFC3339Nano, s); return t }
func marshal(v any) string         { b, _ := json.Marshal(v); return string(b) }

func (s *Store) UpsertSnapshot(ctx context.Context, in problem.Snapshot) (Ref, error) {
	p, err := problem.Prepare(in)
	if err != nil {
		return Ref{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Ref{}, err
	}
	defer tx.Rollback()
	stamp := now()
	var existing int
	err = tx.QueryRowContext(ctx, "SELECT 1 FROM problem_revisions WHERE revision_hash=?", p.RevisionHash).Scan(&existing)
	if errors.Is(err, sql.ErrNoRows) {
		r, err := tx.ExecContext(ctx, "INSERT INTO problems(kind,title,created_at) VALUES(?,?,?)", p.Kind, p.Title, stamp)
		if err != nil {
			return Ref{}, err
		}
		id, err := r.LastInsertId()
		if err != nil {
			return Ref{}, err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO problem_revisions(revision_hash,problem_id,statement_markdown,public_tests_json,constraints_json,created_at) VALUES(?,?,?,?,?,?)", p.RevisionHash, id, p.StatementMarkdown, marshal(p.PublicTests), marshal(p.ObservedConstraints), stamp)
		if err != nil {
			return Ref{}, err
		}
	} else if err != nil {
		return Ref{}, err
	}
	// The contract row excludes changing current editor contents. Snapshots keep
	// the most recent editor version for change detection and model generation.
	contract := p
	contract.Title = ""
	contract.StatementMarkdown = ""
	contract.PublicTests = nil
	contract.ObservedConstraints = nil
	for i := range contract.Files {
		contract.Files[i].CurrentContent = ""
		contract.Files[i].CurrentContentHash = ""
	}
	if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO execution_contracts(contract_hash,contract_json,created_at) VALUES(?,?,?)", p.ContractHash, marshal(contract), stamp); err != nil {
		return Ref{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO problem_snapshots(revision_hash,contract_hash,snapshot_json,updated_at) VALUES(?,?,?,?) ON CONFLICT(revision_hash,contract_hash) DO UPDATE SET snapshot_json=excluded.snapshot_json,updated_at=excluded.updated_at", p.RevisionHash, p.ContractHash, marshal(p), stamp); err != nil {
		return Ref{}, err
	}
	if err = tx.Commit(); err != nil {
		return Ref{}, err
	}
	return Ref{p.RevisionHash, p.ContractHash}, nil
}

func (s *Store) Snapshot(ctx context.Context, ref Ref) (problem.Snapshot, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, "SELECT snapshot_json FROM problem_snapshots WHERE revision_hash=? AND contract_hash=?", ref.RevisionHash, ref.ContractHash).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return problem.Snapshot{}, ErrNotFound
	}
	if err != nil {
		return problem.Snapshot{}, err
	}
	var p problem.Snapshot
	err = json.Unmarshal([]byte(raw), &p)
	return p, err
}

func (s *Store) AddCandidate(ctx context.Context, ref Ref, in CandidateInput) (Candidate, error) {
	hash, err := problem.HashFiles(in.Files)
	if err != nil {
		return Candidate{}, err
	}
	for _, v := range []string{in.Source, in.Provider, in.Model, in.PromptVersion} {
		if err = problem.ValidatePublicText(v); err != nil {
			return Candidate{}, err
		}
	}
	p, err := s.Snapshot(ctx, ref)
	if err != nil {
		return Candidate{}, err
	}
	if len(p.Files) != len(in.Files) {
		return Candidate{}, errors.New("candidate files do not match problem contract")
	}
	allowed := map[string]bool{}
	for _, f := range p.Files {
		allowed[f.Path] = true
	}
	for _, f := range in.Files {
		if !allowed[f.Path] {
			return Candidate{}, errors.New("candidate path does not match problem contract")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Candidate{}, err
	}
	defer tx.Rollback()
	var existing int64
	err = tx.QueryRowContext(ctx, "SELECT id FROM solutions WHERE revision_hash=? AND contract_hash=? AND files_hash=?", ref.RevisionHash, ref.ContractHash, hash).Scan(&existing)
	if err == nil {
		tx.Rollback()
		return s.candidate(ctx, existing)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Candidate{}, err
	}
	if in.ParentCandidateID != 0 {
		var r, c string
		if err = tx.QueryRowContext(ctx, "SELECT revision_hash,contract_hash FROM solutions WHERE id=?", in.ParentCandidateID).Scan(&r, &c); err != nil {
			return Candidate{}, errors.New("parent candidate not found")
		}
		if r != ref.RevisionHash || c != ref.ContractHash {
			return Candidate{}, errors.New("parent candidate belongs to another problem contract")
		}
	}
	var parent any
	if in.ParentCandidateID != 0 {
		parent = in.ParentCandidateID
	}
	stamp := now()
	r, err := tx.ExecContext(ctx, "INSERT INTO solutions(revision_hash,contract_hash,files_hash,source,provider,model,prompt_version,parent_candidate_id,created_at) VALUES(?,?,?,?,?,?,?,?,?)", ref.RevisionHash, ref.ContractHash, hash, in.Source, in.Provider, in.Model, in.PromptVersion, parent, stamp)
	if err != nil {
		return Candidate{}, err
	}
	id, err := r.LastInsertId()
	if err != nil {
		return Candidate{}, err
	}
	for _, f := range in.Files {
		if _, err = tx.ExecContext(ctx, "INSERT INTO solution_files(solution_id,path,content,content_hash) VALUES(?,?,?,?)", id, f.Path, f.Content, problem.HashContent(f.Content)); err != nil {
			return Candidate{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return Candidate{}, err
	}
	return s.candidate(ctx, id)
}

func (s *Store) candidate(ctx context.Context, id int64) (Candidate, error) {
	var c Candidate
	var parent sql.NullInt64
	var stamp string
	err := s.db.QueryRowContext(ctx, "SELECT id,revision_hash,contract_hash,files_hash,source,provider,model,prompt_version,parent_candidate_id,created_at FROM solutions WHERE id=?", id).Scan(&c.ID, &c.RevisionHash, &c.ContractHash, &c.FilesHash, &c.Source, &c.Provider, &c.Model, &c.PromptVersion, &parent, &stamp)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, err
	}
	c.ParentCandidateID = parent.Int64
	c.CreatedAt = parseTime(stamp)
	rows, err := s.db.QueryContext(ctx, "SELECT path,content FROM solution_files WHERE solution_id=? ORDER BY path", id)
	if err != nil {
		return c, err
	}
	for rows.Next() {
		var f problem.SolutionFile
		if err = rows.Scan(&f.Path, &f.Content); err != nil {
			rows.Close()
			return c, err
		}
		c.Files = append(c.Files, f)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return c, err
	}
	rows, err = s.db.QueryContext(ctx, "SELECT id,evidence_json,created_at FROM verifications WHERE solution_id=? ORDER BY id", id)
	if err != nil {
		return c, err
	}
	c.Status = "candidate"
	for rows.Next() {
		var v Validation
		var raw, at string
		if err = rows.Scan(&v.ID, &raw, &at); err != nil {
			rows.Close()
			return c, err
		}
		vid := v.ID
		if err = json.Unmarshal([]byte(raw), &v); err != nil {
			rows.Close()
			return c, err
		}
		v.ID = vid
		v.CreatedAt = parseTime(at)
		c.Validations = append(c.Validations, v)
		if v.Result == "failed" {
			c.Status = "rejected"
		} else if v.Result == "passed" && v.Kind == "formal" {
			c.Status = "upstream_verified"
		} else if v.Result == "passed" && v.Kind == "selftest" && c.Status != "upstream_verified" {
			c.Status = "selftest_passed"
		}
	}
	err = rows.Err()
	rows.Close()
	return c, err
}

func (s *Store) Candidate(ctx context.Context, id int64) (Candidate, error) {
	return s.candidate(ctx, id)
}

// Lookup preserves all failures for diagnosis. Only upstream_verified candidates
// are reusable without a model; callers still revalidate their current instance.
func (s *Store) Lookup(ctx context.Context, ref Ref) ([]Candidate, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM solutions WHERE revision_hash=? AND contract_hash=? ORDER BY id DESC", ref.RevisionHash, ref.ContractHash)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	all := make([]Candidate, 0, len(ids))
	for _, id := range ids {
		c, e := s.candidate(ctx, id)
		if e != nil {
			return nil, e
		}
		all = append(all, c)
	}
	rank := func(s string) int {
		switch s {
		case "upstream_verified":
			return 0
		case "selftest_passed":
			return 1
		case "candidate":
			return 2
		default:
			return 3
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return rank(all[i].Status) < rank(all[j].Status) })
	return all, nil
}

// FindVerified is the strict cache path used before a model request. It only
// returns a formally passed candidate; failed/self-test-only history remains
// available through Lookup for repair and diagnostics.
func (s *Store) FindVerified(ctx context.Context, ref Ref) (Candidate, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM solutions WHERE revision_hash=? AND contract_hash=? ORDER BY id DESC", ref.RevisionHash, ref.ContractHash)
	if err != nil {
		return Candidate{}, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return Candidate{}, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return Candidate{}, err
	}
	if err := rows.Err(); err != nil {
		return Candidate{}, err
	}
	for _, id := range ids {
		c, err := s.candidate(ctx, id)
		if err != nil {
			return Candidate{}, err
		}
		if c.Status == "upstream_verified" {
			return c, nil
		}
	}
	return Candidate{}, ErrNotFound
}

// SaveCandidate is an explicit alias for orchestration code whose terminology
// follows the lifecycle diagram. Candidate rows remain immutable.
func (s *Store) SaveCandidate(ctx context.Context, ref Ref, in CandidateInput) (Candidate, error) {
	return s.AddCandidate(ctx, ref, in)
}

func (s *Store) RecordValidation(ctx context.Context, v Validation) error {
	c, err := s.candidate(ctx, v.CandidateID)
	if err != nil {
		return err
	}
	if v.Kind != "selftest" && v.Kind != "formal" && v.Kind != "structural" {
		return errors.New("unknown validation kind")
	}
	if v.Result != "passed" && v.Result != "failed" && v.Result != "error" && v.Result != "unknown" {
		return errors.New("unknown validation result")
	}
	if v.FilesHash != c.FilesHash || v.RevisionHash != c.RevisionHash || v.ContractHash != c.ContractHash {
		return errors.New("validation is not bound to candidate files and problem contract")
	}
	if v.TestCount < 0 || v.PassedCount < 0 || v.PassedCount > v.TestCount {
		return errors.New("invalid validation test counts")
	}
	if v.Result == "passed" && v.Kind == "formal" && (v.SavedFilesHash != c.FilesHash || v.SubmittedFilesHash != c.FilesHash || !v.Terminal || !v.Complete || v.TestCount == 0 || v.PassedCount != v.TestCount) {
		return errors.New("formal pass requires complete terminal evidence bound to saved and submitted files")
	}
	for _, text := range []string{v.Feedback, v.RunID} {
		if err = problem.ValidatePublicText(text); err != nil {
			return err
		}
	}
	v.ID = 0
	v.CreatedAt = time.Time{}
	_, err = s.db.ExecContext(ctx, "INSERT INTO verifications(solution_id,evidence_json,created_at) VALUES(?,?,?)", v.CandidateID, marshal(v), now())
	return err
}

// rejectRunData checks the serialized progress envelope before it reaches the
// database. It intentionally permits token/usage counters while rejecting
// authentication and runtime transport material by key and by value shape.
func rejectRunData(raw json.RawMessage) error {
	if len(raw) > 1<<20 || !json.Valid(raw) {
		return errors.New("run data must be valid JSON under 1 MiB")
	}
	var walk func(any) error
	walk = func(v any) error {
		switch x := v.(type) {
		case map[string]any:
			for k, val := range x {
				key := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(k, "-", "_"), " ", "_"))
				for _, bad := range []string{"cookie", "set_cookie", "authorization", "password", "passwd", "secret", "sec_key", "session_id", "sessionid", "access_token", "refresh_token", "api_key", "apikey", "token", "signed_url", "query_signature", "websocket_url", "runtime_url"} {
					if key == bad || strings.Contains(key, "cookie") || strings.Contains(key, "sec_key") {
						return errors.New("run data contains private execution material")
					}
				}
				if err := walk(val); err != nil {
					return err
				}
			}
		case []any:
			for _, val := range x {
				if err := walk(val); err != nil {
					return err
				}
			}
		case string:
			if err := problem.ValidatePublicText(x); err != nil {
				return fmt.Errorf("run data: %w", err)
			}
		}
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	return walk(v)
}

func validateOwner(owner string) error {
	if owner == "" || len(owner) > 256 {
		return errors.New("opaque run owner is required")
	}
	if err := problem.ValidatePublicText(owner); err != nil {
		return err
	}
	if strings.ContainsAny(owner, "\r\n") {
		return errors.New("invalid run owner")
	}
	return nil
}

func (s *Store) PutRun(ctx context.Context, r RunRecord) error {
	if err := validateOwner(r.Owner); err != nil {
		return err
	}
	if r.ID == "" || len(r.ID) > 128 {
		return errors.New("run id is required")
	}
	if len(r.Status) > 64 {
		return errors.New("invalid run status")
	}
	if err := rejectRunData(r.Data); err != nil {
		return err
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = r.CreatedAt
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `INSERT INTO solver_runs(id,owner,idempotency_key,status,data_json,created_at,updated_at)
VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET owner=excluded.owner,idempotency_key=excluded.idempotency_key,status=excluded.status,data_json=excluded.data_json,updated_at=excluded.updated_at`, r.ID, r.Owner, r.IdempotencyKey, r.Status, string(r.Data), r.CreatedAt.UTC().Format(time.RFC3339Nano), r.UpdatedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) GetRun(ctx context.Context, id string) (RunRecord, error) {
	var r RunRecord
	var created, updated, raw string
	err := s.db.QueryRowContext(ctx, "SELECT id,owner,idempotency_key,status,data_json,created_at,updated_at FROM solver_runs WHERE id=?", id).Scan(&r.ID, &r.Owner, &r.IdempotencyKey, &r.Status, &raw, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	r.Data = json.RawMessage(raw)
	r.CreatedAt = parseTime(created)
	r.UpdatedAt = parseTime(updated)
	return r, nil
}

func (s *Store) FindRunByIdempotency(ctx context.Context, owner, key string) (RunRecord, error) {
	if err := validateOwner(owner); err != nil {
		return RunRecord{}, err
	}
	if key == "" {
		return RunRecord{}, ErrNotFound
	}
	var id string
	err := s.db.QueryRowContext(ctx, "SELECT id FROM solver_runs WHERE owner=? AND idempotency_key=?", owner, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return RunRecord{}, ErrNotFound
	}
	if err != nil {
		return RunRecord{}, err
	}
	return s.GetRun(ctx, id)
}

func (s *Store) AddAttempt(ctx context.Context, a Attempt) error {
	if a.RunID == "" || a.Stage == "" || len(a.Stage) > 64 {
		return errors.New("invalid attempt")
	}
	if a.Number < 0 {
		return errors.New("invalid attempt number")
	}
	for _, x := range []string{a.Result, a.Feedback} {
		if err := problem.ValidatePublicText(x); err != nil {
			return err
		}
	}
	if _, err := s.GetRun(ctx, a.RunID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, "INSERT INTO attempts(run_id,solution_id,attempt_number,stage,result,feedback,created_at) VALUES(?,?,?,?,?,?,?)", a.RunID, nullInt(a.CandidateID), a.Number, a.Stage, a.Result, a.Feedback, now())
	return err
}

func nullInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func (s *Store) RecordUsage(ctx context.Context, u Usage) error {
	if u.RunID == "" || u.Provider == "" || u.Model == "" || u.PricingVersion == "" {
		return errors.New("usage provider, model, pricing version and run are required")
	}
	if u.InputTokens < 0 || u.CachedInputTokens < 0 || u.OutputTokens < 0 || u.ReservedMicroUSD < 0 || u.CostMicroUSD < 0 || u.LatencyMillis < 0 {
		return errors.New("usage counters cannot be negative")
	}
	if err := problem.ValidatePublicText(u.Provider); err != nil {
		return err
	}
	if err := problem.ValidatePublicText(u.Model); err != nil {
		return err
	}
	if err := problem.ValidatePublicText(u.PricingVersion); err != nil {
		return err
	}
	if err := problem.ValidatePublicText(u.Status); err != nil {
		return err
	}
	if err := problem.ValidatePublicText(u.FinishReason); err != nil {
		return err
	}
	if _, err := s.GetRun(ctx, u.RunID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, "INSERT INTO usage_ledger(run_id,solution_id,provider,model,pricing_version,input_tokens,cached_input_tokens,output_tokens,reserved_micro_usd,cost_micro_usd,status,finish_reason,latency_millis,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)", u.RunID, nullInt(u.CandidateID), u.Provider, u.Model, u.PricingVersion, u.InputTokens, u.CachedInputTokens, u.OutputTokens, u.ReservedMicroUSD, u.CostMicroUSD, u.Status, u.FinishReason, u.LatencyMillis, now())
	return err
}
