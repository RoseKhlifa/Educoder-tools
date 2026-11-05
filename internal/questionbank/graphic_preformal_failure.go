package questionbank

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"strings"

	"educoder-cli/internal/problem"
)

// PreformalGraphicFailure proves a historical graphic run did not reach its
// formal build. GraphicProtocol.Submit creates a source-bound result as soon as
// SaveAndCommit returns a valid commit, before reading the verdict baseline,
// preparing the evaluation key, or sending game_build. Every later return keeps
// that result; submitGraphic copies its content hash into SavedFilesHash even
// when the later operation fails. Thus the original evaluation with both hashes
// empty identifies a failure before that boundary, rather than an unknown build.
// This proof neither changes the old run nor promotes its candidate as correct.
func (s *Store) PreformalGraphicFailure(ctx context.Context, owner, key, course string, homeworkID int64) (bool, error) {
	if owner == "" || key == "" || course == "" || homeworkID <= 0 {
		return false, nil
	}
	run, err := s.FindRunByIdempotency(ctx, owner, key)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var state struct {
		Owner          string `json:"owner"`
		IdempotencyKey string `json:"idempotency_key"`
		Mode           string `json:"mode"`
		Target         struct {
			Course     string
			HomeworkID int64
			Game       string
		} `json:"target"`
		SnapshotRef Ref `json:"snapshot_ref"`
		Result      struct {
			RunID       string `json:"run_id"`
			Status      string `json:"status"`
			CandidateID int64  `json:"candidate_id"`
			CodeHash    string `json:"code_hash"`
			Evaluation  *struct {
				Status             string
				Completed          bool
				Passed             bool
				TestCount          int
				PassedCount        int
				SavedFilesHash     string
				SubmittedFilesHash string
			} `json:"evaluation"`
		} `json:"result"`
	}
	if json.Unmarshal(run.Data, &state) != nil || run.Status != "uncertain" || run.Owner != owner || run.IdempotencyKey != key || state.Owner != owner || state.IdempotencyKey != key || state.Mode != "submit" || state.Target.Course != course || state.Target.HomeworkID != homeworkID || state.Target.Game == "" || state.Result.RunID != run.ID || state.Result.Status != "uncertain" || state.Result.CandidateID <= 0 {
		return false, nil
	}
	eval := state.Result.Evaluation
	if eval == nil || eval.Status != "error" || eval.Completed || eval.Passed || eval.TestCount != 0 || eval.PassedCount != 0 || eval.SavedFilesHash != "" || eval.SubmittedFilesHash != "" {
		return false, nil
	}
	snapshot, err := s.Snapshot(ctx, state.SnapshotRef)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if snapshot.Kind != "programming" || snapshot.EditMode != "graphic" || len(snapshot.Files) != 1 || !strings.EqualFold(path.Ext(snapshot.Files[0].Path), ".mdj") {
		return false, nil
	}
	prepared, err := problem.Prepare(snapshot)
	if err != nil || prepared.RevisionHash != state.SnapshotRef.RevisionHash || prepared.ContractHash != state.SnapshotRef.ContractHash {
		return false, nil
	}

	// Read only the one expected source file. Candidate() also loads its entire
	// validation history, which is unnecessary for this bounded run-local proof.
	rows, err := s.db.QueryContext(ctx, `SELECT s.revision_hash,s.contract_hash,s.files_hash,f.path,f.content FROM solutions s JOIN solution_files f ON f.solution_id=s.id WHERE s.id=? ORDER BY f.path LIMIT 2`, state.Result.CandidateID)
	if err != nil {
		return false, err
	}
	var ref Ref
	var filesHash string
	var files []problem.SolutionFile
	for rows.Next() {
		var file problem.SolutionFile
		if err = rows.Scan(&ref.RevisionHash, &ref.ContractHash, &filesHash, &file.Path, &file.Content); err != nil {
			rows.Close()
			return false, err
		}
		files = append(files, file)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	if len(files) != 1 || ref != state.SnapshotRef || files[0].Path != snapshot.Files[0].Path {
		return false, nil
	}
	hash, err := problem.HashFiles(files)
	if err != nil || hash != filesHash || hash != state.Result.CodeHash {
		return false, nil
	}

	// Historical solver code filled missing validation saved/submitted hashes
	// with the candidate hash for legacy adapters. Those hashes alone therefore
	// cannot prove a save or build. Require the original empty-hash evaluation
	// above plus exclusively incomplete zero-test unknown evidence for this
	// exact run/candidate/contract. Any real verdict or other candidate fails
	// closed; excessive or missing history is not proof of non-submission.
	const maxEvidence = 64
	rows, err = s.db.QueryContext(ctx, `SELECT solution_id,evidence_json FROM verifications WHERE json_extract(evidence_json,'$.run_id')=? ORDER BY id LIMIT ?`, run.ID, maxEvidence+1)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var candidateID int64
		var raw []byte
		var validation Validation
		if err = rows.Scan(&candidateID, &raw); err != nil {
			return false, err
		}
		count++
		if count > maxEvidence || json.Unmarshal(raw, &validation) != nil || candidateID != state.Result.CandidateID || validation.CandidateID != candidateID || validation.RunID != run.ID || validation.Kind != "formal" || validation.Result != "unknown" || validation.Terminal || validation.Complete || validation.TestCount != 0 || validation.PassedCount != 0 || validation.FilesHash != hash || validation.RevisionHash != ref.RevisionHash || validation.ContractHash != ref.ContractHash || validation.SavedFilesHash != "" && validation.SavedFilesHash != hash || validation.SubmittedFilesHash != "" && validation.SubmittedFilesHash != hash {
			return false, nil
		}
	}
	if err = rows.Err(); err != nil {
		return false, err
	}
	return count > 0, nil
}
