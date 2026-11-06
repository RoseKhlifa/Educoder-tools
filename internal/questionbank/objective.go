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
	"regexp"
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
	return s.findObjectiveVerified(ctx, key, nil, false)
}

// FindObjectiveVerifiedWithResources permits whole-task image answers only
// when the exact persisted hash placeholders are bound to valid current image
// bytes at the same question/option positions. Resources must include SHA256;
// URL-only legacy snapshots and individual image answers remain ineligible.
func (s *Store) FindObjectiveVerifiedWithResources(ctx context.Context, key string, resources []answer.ImageResource) (ObjectiveCandidate, error) {
	return s.findObjectiveVerified(ctx, key, resources, true)
}

func (s *Store) findObjectiveVerified(ctx context.Context, key string, resources []answer.ImageResource, imageAware bool) (ObjectiveCandidate, error) {
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
	hasMedia := objectiveSnapshotHasMedia(snapshot)
	if !imageAware && hasMedia {
		return c, ErrNotFound
	}
	if imageAware {
		// Validate the current fetched bytes (including MIME, dimensions, digest
		// and position) before accepting the snapshot's content-hash contract.
		if err = answer.ValidateObjectiveResources(snapshot, resources); err != nil {
			return c, err
		}
		if !objectiveSnapshotResourcesMatch(snapshot, resources) {
			return c, ErrNotFound
		}
	}
	var raw string
	err = s.db.QueryRowContext(ctx, `SELECT c.id,c.candidate_hash,c.candidate_json,c.source
		FROM objective_candidates c
		JOIN objective_verifications v ON v.candidate_id=c.id AND v.result='full_score'
		WHERE c.snapshot_hash=?
		  AND NOT EXISTS (SELECT 1 FROM objective_verifications newer WHERE newer.candidate_id=c.id AND newer.id>v.id)
		ORDER BY c.id DESC LIMIT 1`, key).Scan(&c.ID, &c.Hash, &raw, &c.Source)
	if errors.Is(err, sql.ErrNoRows) {
		if hasMedia {
			return c, ErrNotFound
		}
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

var objectiveCanonicalImage = regexp.MustCompile(`<img src="sha256:([0-9a-f]{64})">`)
var objectiveUnsupportedSnapshotMedia = regexp.MustCompile(`(?is)<\s*(?:img|svg|object|embed|video|audio|canvas|iframe|picture|source)\b|!\[|\bsrcset\s*=|\burl\s*\(`)

// Only the canonical placeholder emitted after fetching an image is supported.
// Each binding includes the question and option, so matching bytes in another
// option cannot substitute for the referenced image. A single shared statement
// image may provide several exact question-position bindings.
func objectiveSnapshotResourcesMatch(snapshot answer.ObjectiveSnapshot, resources []answer.ImageResource) bool {
	type binding struct {
		position int
		option   string
		hash     string
	}
	available := make(map[binding]bool, len(resources))
	for _, resource := range resources {
		if !objectiveSHA256Pattern.MatchString(resource.SHA256) {
			return false
		}
		for _, bound := range resource.EffectiveBindings() {
			available[binding{bound.QuestionPosition, bound.OptionLabel, resource.SHA256}] = true
		}
	}
	used := make(map[binding]bool, len(resources))
	matchText := func(text string, position int, option string) bool {
		if objectiveUnsupportedSnapshotMedia.MatchString(objectiveCanonicalImage.ReplaceAllString(text, "")) {
			return false
		}
		for _, match := range objectiveCanonicalImage.FindAllStringSubmatch(text, -1) {
			key := binding{position, option, match[1]}
			if !available[key] {
				return false
			}
			used[key] = true
		}
		return true
	}
	positions := make(map[int]bool, len(snapshot.Questions))
	for _, question := range snapshot.Questions {
		if positions[question.Position] {
			return false
		}
		positions[question.Position] = true
		if !matchText(snapshot.Title, question.Position, "") || !matchText(snapshot.Statement, question.Position, "") || !matchText(question.Question, question.Position, "") {
			return false
		}
		for _, option := range question.Options {
			if !matchText(option.Text, question.Position, option.Label) {
				return false
			}
		}
	}
	return len(used) == len(available)
}

func objectiveSnapshotHasMedia(snapshot answer.ObjectiveSnapshot) bool {
	if objectiveUnsupportedSnapshotMedia.MatchString(snapshot.Title) || objectiveUnsupportedSnapshotMedia.MatchString(snapshot.Statement) {
		return true
	}
	for _, q := range snapshot.Questions {
		if objectiveUnsupportedSnapshotMedia.MatchString(q.Question) {
			return true
		}
		for _, option := range q.Options {
			if objectiveUnsupportedSnapshotMedia.MatchString(option.Text) {
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
