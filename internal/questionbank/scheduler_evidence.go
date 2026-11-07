package questionbank

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	rows, err := s.db.QueryContext(ctx, `SELECT id, status, data_json, COALESCE(json_extract(data_json,'$.target.Game'),'') FROM solver_runs
		WHERE owner=? AND status IN ('uncertain','verification_unconfirmed','usage_persistence_failed','verification_persistence_failed')
		AND json_extract(data_json,'$.target.Course')=? AND json_extract(data_json,'$.target.HomeworkID')=?`, owner, course, homeworkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UnresolvedHomeworkRun
	for rows.Next() {
		var row UnresolvedHomeworkRun
		var status string
		var raw []byte
		if err := rows.Scan(&row.RunID, &status, &raw, &row.Game); err != nil {
			return nil, err
		}
		row.FormalUnknown = formalUnknownRun(status, raw)
		out = append(out, row)
	}
	return out, rows.Err()
}

// UnresolvedHomeworkRun includes all historical uncertain records for audit
// callers. FormalUnknown is deliberately narrower: it is true only when the
// durable result proves that a platform write reached a formal evaluator but
// the outcome was not confirmed. Pre-submit/self-test and resource failures
// stay visible to operators without blocking unrelated levels.
type UnresolvedHomeworkRun struct {
	RunID         string
	Game          string
	FormalUnknown bool
}

// formalUnknownRun recognizes the public run DTOs written by the programming
// and objective services. This parser is intentionally fail-closed for older
// or malformed records: without an explicit formal-evaluation marker a run
// cannot be used to block a new mutation.
func formalUnknownRun(runStatus string, raw []byte) bool {
	var envelope struct {
		Result struct {
			Kind        string `json:"kind"`
			Status      string `json:"status"`
			ScoreCheck  string `json:"score_check"`
			Programming *struct {
				Evaluation *struct {
					Status string `json:"status"`
				} `json:"evaluation"`
			} `json:"programming"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return false
	}
	switch envelope.Result.Kind {
	case "objective":
		// Objective verification_unconfirmed is also emitted before the
		// submit when the question changed. A non-empty score check is only
		// assigned after ObjectiveSubmit returned, so it proves a write.
		if runStatus == "uncertain" || runStatus == "verification_persistence_failed" {
			return true
		}
		return runStatus == "verification_unconfirmed" && envelope.Result.ScoreCheck != ""
	case "programming":
		p := envelope.Result.Programming
		if p == nil || p.Evaluation == nil {
			return false
		}
		// Graphic pre-write/save/resource failures expose an Evaluation DTO
		// only as an adapter diagnostic, but never reach the formal build.
		switch p.Evaluation.Status {
		case "prewrite_error", "resource_unavailable", "saved_not_submitted", "save_unverified", "save_error", "editor_changed":
			return false
		}
		return runStatus == "uncertain" || runStatus == "verification_unconfirmed" || runStatus == "verification_persistence_failed"
	default:
		return false
	}
}
