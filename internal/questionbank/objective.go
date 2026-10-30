package questionbank

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
	"encoding/hex"
	"encoding/json"
	"errors"
)

// Objective answers have their own schema and never enter source-file contracts.
const objectiveSchema = `
CREATE TABLE IF NOT EXISTS objective_snapshots (snapshot_hash TEXT PRIMARY KEY,snapshot_json TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS objective_candidates (id INTEGER PRIMARY KEY,snapshot_hash TEXT NOT NULL REFERENCES objective_snapshots(snapshot_hash),candidate_hash TEXT NOT NULL,candidate_json TEXT NOT NULL,source TEXT NOT NULL,created_at TEXT NOT NULL,UNIQUE(snapshot_hash,candidate_hash));
CREATE TABLE IF NOT EXISTS objective_verifications (id INTEGER PRIMARY KEY,candidate_id INTEGER NOT NULL REFERENCES objective_candidates(id),run_id TEXT NOT NULL,result TEXT NOT NULL,score TEXT NOT NULL,max_score TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS objective_verified ON objective_verifications(candidate_id,result);
`

type ObjectiveCandidate struct {
	ID        int64                     `json:"id"`
	Hash      string                    `json:"hash"`
	Candidate answer.ObjectiveCandidate `json:"candidate"`
	Source    string                    `json:"source"`
}

func (s *Store) PutObjectiveSnapshot(ctx context.Context, snapshot answer.ObjectiveSnapshot) (string, error) {
	if err := answer.ValidateObjectiveSnapshot(snapshot); err != nil {
		return "", err
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(raw)
	key := hex.EncodeToString(h[:])
	_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO objective_snapshots(snapshot_hash,snapshot_json,created_at) VALUES(?,?,?)`, key, string(raw), now())
	return key, err
}
func (s *Store) AddObjectiveCandidate(ctx context.Context, key string, snapshot answer.ObjectiveSnapshot, c answer.ObjectiveCandidate, source string) (ObjectiveCandidate, error) {
	if err := answer.ValidateObjectiveCandidate(snapshot, c); err != nil {
		return ObjectiveCandidate{}, err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return ObjectiveCandidate{}, err
	}
	if err = problem.ValidatePublicText(string(raw)); err != nil {
		return ObjectiveCandidate{}, err
	}
	h := sha256.Sum256(raw)
	hash := hex.EncodeToString(h[:])
	_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO objective_candidates(snapshot_hash,candidate_hash,candidate_json,source,created_at) VALUES(?,?,?,?,?)`, key, hash, string(raw), source, now())
	if err != nil {
		return ObjectiveCandidate{}, err
	}
	var id int64
	err = s.db.QueryRowContext(ctx, `SELECT id FROM objective_candidates WHERE snapshot_hash=? AND candidate_hash=?`, key, hash).Scan(&id)
	return ObjectiveCandidate{ID: id, Hash: hash, Candidate: c, Source: source}, err
}
func (s *Store) FindObjectiveVerified(ctx context.Context, key string) (ObjectiveCandidate, error) {
	var c ObjectiveCandidate
	var snapshot answer.ObjectiveSnapshot
	var snapshotJSON string
	err := s.db.QueryRowContext(ctx, `SELECT snapshot_json FROM objective_snapshots WHERE snapshot_hash=?`, key).Scan(&snapshotJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal([]byte(snapshotJSON), &snapshot); err != nil {
		return c, err
	}
	if err = answer.ValidateObjectiveSnapshot(snapshot); err != nil {
		return c, err
	}
	// The legacy whole-task snapshot has image locators but no content hashes.
	// It cannot establish that an image is unchanged. Image-aware callers use
	// UpsertObjectiveQuestionWithResources and the individual-answer API.
	if objectiveSnapshotHasMedia(snapshot) {
		return c, ErrNotFound
	}
	var raw string
	err = s.db.QueryRowContext(ctx, `SELECT c.id,c.candidate_hash,c.candidate_json,c.source FROM objective_candidates c WHERE c.snapshot_hash=? AND EXISTS(SELECT 1 FROM objective_verifications v WHERE v.candidate_id=c.id AND v.result='full_score') ORDER BY c.id DESC LIMIT 1`, key).Scan(&c.ID, &c.Hash, &raw, &c.Source)
	if errors.Is(err, sql.ErrNoRows) {
		return s.objectiveCandidateFromAnswers(ctx, key, snapshot)
	}
	if err != nil {
		return c, err
	}
	err = json.Unmarshal([]byte(raw), &c.Candidate)
	if err == nil {
		err = answer.ValidateObjectiveCandidate(snapshot, c.Candidate)
	}
	return c, err
}

func objectiveSnapshotHasMedia(snapshot answer.ObjectiveSnapshot) bool {
	if objectiveMediaPattern.MatchString(snapshot.Title) || objectiveMediaPattern.MatchString(snapshot.Statement) {
		return true
	}
	for _, q := range snapshot.Questions {
		if objectiveMediaPattern.MatchString(q.Question) {
			return true
		}
		for _, option := range q.Options {
			if objectiveMediaPattern.MatchString(option.Text) {
				return true
			}
		}
	}
	return false
}

// objectiveCandidateFromAnswers bridges the single-question bank into the
// published whole-task API. It creates no fabricated whole-task score evidence:
// the caller still submits and verifies this candidate in its current context.
func (s *Store) objectiveCandidateFromAnswers(ctx context.Context, key string, snapshot answer.ObjectiveSnapshot) (ObjectiveCandidate, error) {
	candidate := answer.ObjectiveCandidate{Kind: "objective", Answers: make([]string, len(snapshot.Questions))}
	for i, q := range snapshot.Questions {
		questionKey, err := ObjectiveQuestionKey(q)
		if err != nil {
			return ObjectiveCandidate{}, err
		}
		answers, err := s.LookupObjectiveAnswers(ctx, questionKey)
		if err != nil {
			return ObjectiveCandidate{}, err
		}
		for _, a := range answers {
			if a.Reusable {
				candidate.Answers[i] = a.Answer
				break
			}
		}
		if candidate.Answers[i] == "" {
			return ObjectiveCandidate{}, ErrNotFound
		}
	}
	return s.AddObjectiveCandidate(ctx, key, snapshot, candidate, "question_bank")
}

func (s *Store) RecordObjectiveVerification(ctx context.Context, candidateID int64, runID, status, score, max string) error {
	if status != "full_score" && status != "not_full_score" && status != "unconfirmed" && status != "uncertain" {
		return errors.New("invalid objective verification")
	}
	if status == "full_score" {
		if !objectiveHasFullScore(score, max) {
			return errors.New("full score evidence does not match")
		}
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO objective_verifications(candidate_id,run_id,result,score,max_score,created_at) VALUES(?,?,?,?,?,?)`, candidateID, runID, status, score, max, now())
	return err
}
