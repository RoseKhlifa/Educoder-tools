package questionbank

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
)

// Contributions are observations from a server-derived account, not credits
// for being the first person to add a question to the global bank. Alternative
// answers, repeated collections and retries never multiply a semantic question.
// Historical collections have no owner metadata and are deliberately not
// guessed from timestamps, target IDs, source labels or aggregate job counters.
const contributionSchema = `
CREATE TABLE IF NOT EXISTS objective_contributions (
 owner TEXT NOT NULL, answer_id INTEGER NOT NULL REFERENCES objective_answers(id),
 created_at TEXT NOT NULL, PRIMARY KEY(owner,answer_id)
);
`

// RecordObjectiveContribution is called only after the collector has checked
// the account's exact full-score answer sheet against two authenticated reads.
// It writes an idempotent owner receipt only while matching reusable evidence
// exists. Recording duplicates is intentional: another user's existing answer
// does not erase this account's verified contribution, and a retried collection
// can repair a receipt write interrupted after answer validation was persisted.
func (s *Store) RecordObjectiveContribution(ctx context.Context, owner string, answerID int64) error {
	if !collectionOwner.MatchString(owner) || answerID <= 0 {
		return errors.New("invalid objective contribution")
	}
	result, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO objective_contributions(owner,answer_id,created_at)
 SELECT ?,a.id,? FROM objective_answers a
 JOIN objective_questions q ON q.question_key=a.question_key AND q.resource_complete=1
 WHERE a.id=? AND EXISTS(SELECT 1 FROM objective_answer_verifications v WHERE v.answer_id=a.id AND v.verified=1)`, problem.HashContent(owner), now(), answerID)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n > 0 {
		return nil
	}
	// INSERT OR IGNORE also returns zero for a successfully recorded replay.
	var exists bool
	err = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM objective_contributions c
 JOIN objective_answers a ON a.id=c.answer_id
 JOIN objective_questions q ON q.question_key=a.question_key AND q.resource_complete=1
 WHERE c.owner=? AND c.answer_id=? AND EXISTS(SELECT 1 FROM objective_answer_verifications v WHERE v.answer_id=a.id AND v.verified=1))`, problem.HashContent(owner), answerID).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("contribution has no reusable answer evidence")
	}
	return nil
}

// ContributedQuestions counts distinct reusable questions that this account
// has supplied or verified. It is not a completed-work counter or a count of
// globally first additions. Objective receipts cover new read-only collection;
// older formal solver runs can bridge existing individual-answer evidence.
// Programming provenance already binds exact immutable files to owner-bound
// formal run evidence, so its collector needs no second receipt table.
// Raw owner is derived by the server; solver_runs hashes it exactly once.
func (s *Store) ContributedQuestions(ctx context.Context, owner string) (int64, error) {
	if !collectionOwner.MatchString(owner) {
		return 0, errors.New("invalid contribution owner")
	}
	ownerHash := problem.HashContent(owner)
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	questions := map[string]struct{}{}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT q.question_key FROM objective_contributions c
 JOIN objective_answers a ON a.id=c.answer_id
 JOIN objective_questions q ON q.question_key=a.question_key AND q.resource_complete=1
 WHERE c.owner=? AND EXISTS(SELECT 1 FROM objective_answer_verifications v WHERE v.answer_id=a.id AND v.verified=1)`, ownerHash)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return 0, err
		}
		questions["objective:"+key] = struct{}{}
	}
	if err := closeContributionRows(rows); err != nil {
		return 0, err
	}

	// Select only identity/proof fields from run state; avoid reading persisted
	// repair history or generated code just to render one sidebar number.
	rows, err = tx.QueryContext(ctx, `SELECT r.id,`+completionRunProjection+`,snap.snapshot_json,c.candidate_json,v.score,v.max_score,c.id
 FROM solver_runs r JOIN objective_verifications v ON v.run_id=r.id AND v.result='full_score'
 JOIN objective_candidates c ON c.id=v.candidate_id
 JOIN objective_snapshots snap ON snap.snapshot_hash=c.snapshot_hash
 WHERE r.owner=? AND r.status='passed'`, ownerHash)
	if err != nil {
		return 0, err
	}
	type objectiveMatch struct{ key, answer string }
	matches := map[objectiveMatch]struct{}{}
	for rows.Next() {
		var runID, rawRun, rawSnapshot, rawCandidate, score, maximum string
		var candidateID int64
		if err := rows.Scan(&runID, &rawRun, &rawSnapshot, &rawCandidate, &score, &maximum, &candidateID); err != nil {
			rows.Close()
			return 0, err
		}
		var run completionRun
		var snapshot answer.ObjectiveSnapshot
		var candidate answer.ObjectiveCandidate
		if json.Unmarshal([]byte(rawRun), &run) != nil || !contributionRunMatches(run, runID, candidateID) || run.Result.Kind != "objective" || !objectiveHasFullScore(score, maximum) || json.Unmarshal([]byte(rawSnapshot), &snapshot) != nil || json.Unmarshal([]byte(rawCandidate), &candidate) != nil || answer.ValidateObjectiveCandidate(snapshot, candidate) != nil {
			continue
		}
		for i, question := range snapshot.Questions {
			key, err := ObjectiveQuestionKey(question)
			if err == nil {
				matches[objectiveMatch{key, candidate.Answers[i]}] = struct{}{}
			}
		}
	}
	if err := closeContributionRows(rows); err != nil {
		return 0, err
	}
	for match := range matches {
		var reusable bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM objective_answers a
 JOIN objective_questions q ON q.question_key=a.question_key AND q.resource_complete=1
 WHERE q.question_key=? AND a.answer_hash=? AND EXISTS(SELECT 1 FROM objective_answer_verifications v WHERE v.answer_id=a.id AND v.verified=1))`, match.key, problem.HashContent(match.answer)).Scan(&reusable); err != nil {
			return 0, err
		}
		if reusable {
			questions["objective:"+match.key] = struct{}{}
		}
	}

	rows, err = tx.QueryContext(ctx, `SELECT r.id,`+completionRunProjection+`,s.id,s.revision_hash,s.contract_hash,s.files_hash,v.evidence_json
 FROM solutions s JOIN problem_revisions rev ON rev.revision_hash=s.revision_hash
 JOIN problems p ON p.id=rev.problem_id AND p.kind='programming'
 JOIN execution_contracts contract ON contract.contract_hash=s.contract_hash
 JOIN verifications v ON v.solution_id=s.id
 JOIN verifications current ON current.solution_id=s.id AND current.id=(
  SELECT MAX(decisive.id) FROM verifications decisive WHERE decisive.solution_id=s.id
  AND (json_extract(decisive.evidence_json,'$.result')='failed' OR
   (json_extract(decisive.evidence_json,'$.kind') IN ('formal','formal_workspace') AND json_extract(decisive.evidence_json,'$.result')='passed')))
 JOIN solver_runs r ON r.id=json_extract(v.evidence_json,'$.run_id') AND r.status='passed'
 WHERE r.owner=? AND json_extract(v.evidence_json,'$.kind')='formal' AND json_extract(v.evidence_json,'$.result')='passed'
 AND COALESCE(json_extract(contract.contract_json,'$.edit_mode'),'')<>'terminal'
 AND json_extract(current.evidence_json,'$.kind')='formal' AND json_extract(current.evidence_json,'$.result')='passed'
 AND json_extract(current.evidence_json,'$.files_hash')=s.files_hash
 AND json_extract(current.evidence_json,'$.saved_files_hash')=s.files_hash
 AND json_extract(current.evidence_json,'$.submitted_files_hash')=s.files_hash
 AND json_extract(current.evidence_json,'$.revision_hash')=s.revision_hash
 AND json_extract(current.evidence_json,'$.contract_hash')=s.contract_hash
 AND json_extract(current.evidence_json,'$.terminal')=1 AND json_extract(current.evidence_json,'$.complete')=1
 AND json_extract(current.evidence_json,'$.test_count')>0
 AND json_extract(current.evidence_json,'$.test_count')=json_extract(current.evidence_json,'$.passed_count')`, ownerHash)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var runID, rawRun, revision, contract, hash, rawProof string
		var candidateID int64
		if err := rows.Scan(&runID, &rawRun, &candidateID, &revision, &contract, &hash, &rawProof); err != nil {
			rows.Close()
			return 0, err
		}
		var run completionRun
		var proof Validation
		if json.Unmarshal([]byte(rawRun), &run) != nil || !contributionRunMatches(run, runID, candidateID) || json.Unmarshal([]byte(rawProof), &proof) != nil || proof.CandidateID != candidateID || proof.RunID != runID || proof.Kind != "formal" || proof.Result != "passed" || !proof.Terminal || !proof.Complete || proof.TestCount <= 0 || proof.TestCount != proof.PassedCount || hash == "" || proof.FilesHash != hash || proof.SavedFilesHash != hash || proof.SubmittedFilesHash != hash || proof.RevisionHash != revision || proof.ContractHash != contract {
			continue
		}
		questions["programming:"+revision] = struct{}{}
	}
	if err := closeContributionRows(rows); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int64(len(questions)), nil
}

func contributionRunMatches(run completionRun, runID string, candidateID int64) bool {
	return run.Mode == "submit" && run.Result.Status == "passed" && run.Result.RunID == runID && run.Result.CandidateID == candidateID && run.Target.Course != "" && run.Target.HomeworkID > 0 && run.Target.Game != ""
}

func closeContributionRows(rows *sql.Rows) error {
	err := rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return err
	}
	return closeErr
}
