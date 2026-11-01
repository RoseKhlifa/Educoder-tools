package questionbank

import "context"

// Summary contains only reusable, distinct question counts. Candidate answers,
// validation attempts, whole-task snapshots, and model usage are not questions.
// Programming revisions identify semantic problem content; execution contracts
// and alternative verified source files do not multiply the same problem.
type Summary struct {
	VerifiedObjectiveQuestions  int64 `json:"verified_objective_questions"`
	VerifiedProgrammingProblems int64 `json:"verified_programming_problems"`
	TotalVerifiedQuestions      int64 `json:"total_verified_questions"`
}

// Summary reads both counters in one SQLite snapshot. Objective qualification
// matches LookupObjectiveAnswers (verified evidence plus complete resources).
// Programming qualification follows FindVerified's current status: a later
// failed validation withdraws a pass until a subsequent formal pass. The formal
// evidence is also checked against the immutable solution and submission bytes.
func (s *Store) Summary(ctx context.Context) (Summary, error) {
	var out Summary
	err := s.db.QueryRowContext(ctx, `
WITH programming_evidence AS (
 SELECT id, solution_id,
  json_extract(evidence_json, '$.kind') AS kind,
  json_extract(evidence_json, '$.result') AS result,
  json_extract(evidence_json, '$.files_hash') AS files_hash,
  json_extract(evidence_json, '$.saved_files_hash') AS saved_files_hash,
  json_extract(evidence_json, '$.submitted_files_hash') AS submitted_files_hash,
  json_extract(evidence_json, '$.revision_hash') AS revision_hash,
  json_extract(evidence_json, '$.contract_hash') AS contract_hash,
  json_extract(evidence_json, '$.terminal') AS terminal,
  json_extract(evidence_json, '$.complete') AS complete,
  json_extract(evidence_json, '$.test_count') AS test_count,
  json_extract(evidence_json, '$.passed_count') AS passed_count
 FROM verifications
), current_programming_state AS (
 SELECT solution_id, MAX(id) AS evidence_id
 FROM programming_evidence
 WHERE result = 'failed' OR (kind = 'formal' AND result = 'passed')
 GROUP BY solution_id
)
SELECT
 (SELECT COUNT(*) FROM objective_questions q
  WHERE q.resource_complete = 1 AND EXISTS (
   SELECT 1 FROM objective_answers a
   JOIN objective_answer_verifications v ON v.answer_id = a.id AND v.verified = 1
   WHERE a.question_key = q.question_key
  )),
 (SELECT COUNT(DISTINCT s.revision_hash) FROM solutions s
  JOIN current_programming_state c ON c.solution_id = s.id
  JOIN programming_evidence v ON v.id = c.evidence_id
  JOIN problem_revisions r ON r.revision_hash = s.revision_hash
  JOIN problems p ON p.id = r.problem_id AND p.kind = 'programming'
  WHERE v.kind = 'formal' AND v.result = 'passed'
   AND v.files_hash = s.files_hash AND v.saved_files_hash = s.files_hash
   AND v.submitted_files_hash = s.files_hash
   AND v.revision_hash = s.revision_hash AND v.contract_hash = s.contract_hash
   AND v.terminal = 1 AND v.complete = 1
   AND v.test_count > 0 AND v.passed_count = v.test_count
 )`).Scan(&out.VerifiedObjectiveQuestions, &out.VerifiedProgrammingProblems)
	if err != nil {
		return Summary{}, err
	}
	out.TotalVerifiedQuestions = out.VerifiedObjectiveQuestions + out.VerifiedProgrammingProblems
	return out, nil
}
