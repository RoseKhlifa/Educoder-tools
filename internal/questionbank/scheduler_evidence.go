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

// ReusableEvidenceRevisionForHomework scopes the invalidation watermark to a
// single owner's homework. Failed/running solver rows are deliberately absent:
// merely retrying the same stuck homework must not reopen its no-progress fuse.
// Only durable, reusable evidence (a full-score objective result or a passed
// source-file verification) can invalidate the cooldown. The legacy global
// method remains for maintenance tools.
func (s *Store) ReusableEvidenceRevisionForHomework(ctx context.Context, owner, course string, homeworkID int64) (string, error) {
	var values [3]int64
	err := s.db.QueryRowContext(ctx, `SELECT
		COALESCE((SELECT MAX(v.id) FROM objective_verifications v JOIN solver_runs r ON r.id=v.run_id
			WHERE r.owner=? AND v.result='full_score' AND CASE WHEN json_valid(r.data_json) THEN COALESCE(json_extract(r.data_json,'$.target.course'),json_extract(r.data_json,'$.target.Course')) END=?
			AND CASE WHEN json_valid(r.data_json) THEN COALESCE(json_extract(r.data_json,'$.target.homework_id'),json_extract(r.data_json,'$.target.HomeworkID')) END=?),0),
		COALESCE((SELECT MAX(v.id) FROM verifications v JOIN solver_runs r ON r.id=json_extract(v.evidence_json,'$.run_id')
			WHERE r.owner=? AND json_extract(v.evidence_json,'$.result')='passed' AND CASE WHEN json_valid(r.data_json) THEN COALESCE(json_extract(r.data_json,'$.target.course'),json_extract(r.data_json,'$.target.Course')) END=?
			AND CASE WHEN json_valid(r.data_json) THEN COALESCE(json_extract(r.data_json,'$.target.homework_id'),json_extract(r.data_json,'$.target.HomeworkID')) END=?),0),
		COALESCE((SELECT MAX(v.id) FROM objective_answer_verifications v
			WHERE v.verified=1 AND json_extract(v.evidence_json,'$.scope_hash') IN (SELECT c.snapshot_hash FROM objective_candidates c JOIN objective_verifications ov ON ov.candidate_id=c.id JOIN solver_runs r ON r.id=ov.run_id
				WHERE r.owner=? AND CASE WHEN json_valid(r.data_json) THEN COALESCE(json_extract(r.data_json,'$.target.course'),json_extract(r.data_json,'$.target.Course')) END=?
				AND CASE WHEN json_valid(r.data_json) THEN COALESCE(json_extract(r.data_json,'$.target.homework_id'),json_extract(r.data_json,'$.target.HomeworkID')) END=?)),0)`,
		owner, course, homeworkID, owner, course, homeworkID, owner, course, homeworkID).Scan(&values[0], &values[1], &values[2])
	if err != nil {
		return "", err
	}
	// A classmate's full-score answer that has never been evaluated on a level
	// this owner already attempted is a new opportunity the solver now uses
	// before the model (solver.observedFullScoreAnswer). Without it here, a
	// homework stuck before that change stayed paused on retry (2026-11-27).
	// Each such answer stops counting once it has a verification, so the fuse
	// reopens at most once per untried answer. Homeworks without one keep
	// exactly their previous revision.
	// Start from this owner's runs of this homework (a few rows) and look the
	// problems up by index. Checking every observed answer against every run
	// took 13 s on production data; the store has one connection, so other
	// writes timed out behind it (2026-11-27: formal evidence of a passed
	// level could not be saved). This form takes milliseconds.
	var observed int64
	err = s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(s.id),0) FROM (
			SELECT DISTINCT json_extract(r.data_json,'$.snapshot_ref.revision_hash') AS rev, json_extract(r.data_json,'$.snapshot_ref.contract_hash') AS con
			FROM solver_runs r WHERE r.owner=?
			AND CASE WHEN json_valid(r.data_json) THEN COALESCE(json_extract(r.data_json,'$.target.course'),json_extract(r.data_json,'$.target.Course')) END=?
			AND CASE WHEN json_valid(r.data_json) THEN COALESCE(json_extract(r.data_json,'$.target.homework_id'),json_extract(r.data_json,'$.target.HomeworkID')) END=?) refs
		JOIN solutions s ON s.revision_hash=refs.rev AND s.contract_hash=refs.con
		WHERE s.source='collection:platform_observed'
		AND NOT EXISTS(SELECT 1 FROM verifications v WHERE v.solution_id=s.id)`,
		owner, course, homeworkID).Scan(&observed)
	if err != nil {
		return "", err
	}
	parts := []int64{values[0], values[1], values[2]}
	if observed > 0 {
		parts = append(parts, observed)
	}
	hash := sha256.Sum256([]byte(fmt.Sprint(parts)))
	return hex.EncodeToString(hash[:]), nil
}
