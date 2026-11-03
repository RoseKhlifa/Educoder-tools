package questionbank

import (
	"context"
	"encoding/json"
	"errors"

	"educoder-cli/internal/problem"
)

// PreformalProgrammingFailure proves that this exact owner/target/key stopped
// in an editor self-test before any formal submission. It does not promote an
// answer, reinterpret an uncertain build, or change the original run.
func (s *Store) PreformalProgrammingFailure(ctx context.Context, owner, key, course string, homeworkID int64) (bool, error) {
	run, err := s.FindRunByIdempotency(ctx, owner, key)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var state struct {
		Owner  string `json:"owner"`
		Mode   string `json:"mode"`
		Target struct {
			Course     string
			HomeworkID int64
			Game       string
		} `json:"target"`
		SnapshotRef Ref `json:"snapshot_ref"`
		Result      struct {
			RunID          string          `json:"run_id"`
			Status         string          `json:"status"`
			CandidateID    int64           `json:"candidate_id"`
			CodeHash       string          `json:"code_hash"`
			SelfTestStatus string          `json:"self_test_status"`
			Evaluation     json.RawMessage `json:"evaluation"`
		} `json:"result"`
	}
	if json.Unmarshal(run.Data, &state) != nil || run.Status != "uncertain" || state.Owner != owner || state.Mode != "submit" || state.Target.Course != course || state.Target.HomeworkID != homeworkID || state.Target.Game == "" || state.Result.RunID != run.ID || state.Result.Status != "uncertain" || state.Result.SelfTestStatus != "unknown" || state.Result.CandidateID <= 0 || len(state.Result.Evaluation) > 0 && string(state.Result.Evaluation) != "null" {
		return false, nil
	}
	snapshot, err := s.Snapshot(ctx, state.SnapshotRef)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if snapshot.Kind != "programming" || snapshot.EditMode != "single_file" {
		return false, nil
	}
	candidate, err := s.Candidate(ctx, state.Result.CandidateID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	hash, err := problem.HashFiles(candidate.Files)
	if err != nil || candidate.Ref != state.SnapshotRef || hash != candidate.FilesHash || hash != state.Result.CodeHash {
		return false, nil
	}
	// A formal attempt in this run, even for another candidate, invalidates
	// this narrow proof. Missing self-test evidence is not a negative oracle.
	var formal int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM verifications WHERE json_extract(evidence_json,'$.run_id')=? AND json_extract(evidence_json,'$.kind') IN ('formal','formal_workspace')`, run.ID).Scan(&formal); err != nil {
		return false, err
	}
	if formal != 0 {
		return false, nil
	}
	for _, validation := range candidate.Validations {
		if validation.RunID == run.ID && validation.Kind == "selftest" && validation.Result == "unknown" && validation.FilesHash == hash && validation.RevisionHash == state.SnapshotRef.RevisionHash && validation.ContractHash == state.SnapshotRef.ContractHash {
			return true, nil
		}
	}
	return false, nil
}
