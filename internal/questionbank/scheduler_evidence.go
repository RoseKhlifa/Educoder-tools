package questionbank

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// ReusableEvidenceRevision is a cheap, content-free invalidation watermark.
// It is deliberately broader than a single target: unrelated new evidence may
// reopen a fuse, but a newly useful candidate can never be hidden behind it.
// Failed verifications alone do not create a new opportunity to retry.
func (s *Store) ReusableEvidenceRevision(ctx context.Context) (string, error) {
	var values [6]int64
	err := s.db.QueryRowContext(ctx, `SELECT
		COALESCE((SELECT MAX(id) FROM solutions),0),
		COALESCE((SELECT MAX(id) FROM verifications WHERE json_extract(evidence_json,'$.result')='passed'),0),
		COALESCE((SELECT MAX(id) FROM objective_candidates),0),
		COALESCE((SELECT MAX(id) FROM objective_verifications WHERE result='full_score'),0),
		COALESCE((SELECT MAX(id) FROM objective_answer_verifications WHERE verified=1),0),
		COALESCE((SELECT MAX(id) FROM external_entries),0)`).Scan(&values[0], &values[1], &values[2], &values[3], &values[4], &values[5])
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256([]byte(fmt.Sprint(values)))
	return hex.EncodeToString(hash[:]), nil
}

// UnresolvedHomeworkRuns finds unknown outcomes across idempotency keys. A new
// batch is not permission to replay a prior uncertain mutation. Results remain
// owner-scoped and contain no source, credentials or runtime transport data.
func (s *Store) UnresolvedHomeworkRuns(ctx context.Context, owner, course string, homeworkID int64) ([]UnresolvedHomeworkRun, error) {
	if err := validateOwner(owner); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, COALESCE(json_extract(data_json,'$.target.Game'),'') FROM solver_runs
		WHERE owner=? AND status IN ('uncertain','verification_unconfirmed','usage_persistence_failed','verification_persistence_failed')
		AND json_extract(data_json,'$.target.Course')=? AND json_extract(data_json,'$.target.HomeworkID')=?`, owner, course, homeworkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UnresolvedHomeworkRun
	for rows.Next() {
		var row UnresolvedHomeworkRun
		if err := rows.Scan(&row.RunID, &row.Game); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

type UnresolvedHomeworkRun struct{ RunID, Game string }
