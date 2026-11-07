package questionbank

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"strings"

	"educoder-cli/internal/answer"
)

// ObjectiveFailedAttempt is whole-task evidence for the exact snapshot. A
// total score never identifies which individual answer was right or wrong.
// Full-score candidates remain exclusively in the verified-answer lookup.
type ObjectiveFailedAttempt struct {
	Candidate ObjectiveCandidate
	Score     string
	MaxScore  string
}

// ObjectiveFailedAttempts returns at most fifteen distinct, recently confirmed
// failures in chronological order. It deliberately does not read run payloads,
// infer per-question correctness, or accept uncertain submission outcomes.
func (s *Store) ObjectiveFailedAttempts(ctx context.Context, key string, limit int) ([]ObjectiveFailedAttempt, error) {
	const maxObjectiveFailedAttempts = 15
	if limit <= 0 || limit > maxObjectiveFailedAttempts {
		limit = maxObjectiveFailedAttempts
	}
	var rawSnapshot string
	if err := s.db.QueryRowContext(ctx, `SELECT snapshot_json FROM objective_snapshots WHERE snapshot_hash=?`, key).Scan(&rawSnapshot); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	snapshotHash := sha256.Sum256([]byte(rawSnapshot))
	var snapshot answer.ObjectiveSnapshot
	if hex.EncodeToString(snapshotHash[:]) != key || json.Unmarshal([]byte(rawSnapshot), &snapshot) != nil || answer.ValidateObjectiveSnapshot(snapshot) != nil {
		return nil, errors.New("invalid objective history snapshot")
	}
	// Latest confirmed failure per candidate; any full-score proof removes a
	// candidate from the failed set. Bound both the scan and the model payload,
	// including when legacy rows are malformed or duplicate answer vectors.
	rows, err := s.db.QueryContext(ctx, `SELECT c.id,c.candidate_hash,c.candidate_json,c.source,v.score,v.max_score
		FROM objective_candidates c JOIN objective_verifications v ON v.candidate_id=c.id
		WHERE c.snapshot_hash=? AND v.result='not_full_score'
		AND NOT EXISTS(SELECT 1 FROM objective_verifications newer WHERE newer.candidate_id=c.id AND newer.result='not_full_score' AND newer.id>v.id)
		AND NOT EXISTS(SELECT 1 FROM objective_verifications passed WHERE passed.candidate_id=c.id AND passed.result='full_score')
		ORDER BY v.id DESC LIMIT 40`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := make(map[[32]byte]bool, limit)
	var result []ObjectiveFailedAttempt
	for rows.Next() {
		var attempt ObjectiveFailedAttempt
		var raw string
		if err := rows.Scan(&attempt.Candidate.ID, &attempt.Candidate.Hash, &raw, &attempt.Candidate.Source, &attempt.Score, &attempt.MaxScore); err != nil {
			return nil, err
		}
		if !objectiveConfirmedPartialScore(attempt.Score, attempt.MaxScore) || len(raw) > 64<<10 {
			continue
		}
		hash := sha256.Sum256([]byte(raw))
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.DisallowUnknownFields()
		if hex.EncodeToString(hash[:]) != attempt.Candidate.Hash || decoder.Decode(&attempt.Candidate.Candidate) != nil || decoder.Decode(new(any)) != io.EOF || answer.ValidateObjectiveCandidate(snapshot, attempt.Candidate.Candidate) != nil {
			continue
		}
		normalized := make([]string, len(attempt.Candidate.Candidate.Answers))
		for i, value := range attempt.Candidate.Candidate.Answers {
			normalized[i] = strings.ToUpper(strings.TrimSpace(value))
		}
		vector, _ := json.Marshal(normalized)
		vectorHash := sha256.Sum256(vector)
		if seen[vectorHash] {
			continue
		}
		seen[vectorHash] = true
		result = append(result, attempt)
		if len(result) == limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result, nil
}

func objectiveConfirmedPartialScore(earned, maximum string) bool {
	earned, maximum = strings.TrimSpace(earned), strings.TrimSpace(maximum)
	if len(earned) > 128 || len(maximum) > 128 || !objectiveDecimalPattern.MatchString(earned) || !objectiveDecimalPattern.MatchString(maximum) {
		return false
	}
	a, okA := new(big.Rat).SetString(earned)
	b, okB := new(big.Rat).SetString(maximum)
	return okA && okB && a.Sign() >= 0 && b.Sign() > 0 && a.Cmp(b) < 0
}
