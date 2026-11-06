package questionbank

import "context"

// Summary separates reusable, verified questions from external graphic answer
// candidates. Validation attempts, whole-task snapshots, and model usage are not
// questions. Programming revisions identify semantic problem content; execution
// contracts and alternative verified source files do not multiply the problem.
type Summary struct {
	VerifiedObjectiveQuestions  int64 `json:"verified_objective_questions"`
	VerifiedProgrammingProblems int64 `json:"verified_programming_problems"`
	VerifiedGraphicProblems     int64 `json:"verified_graphic_problems"`
	ExternalGraphicCandidates   int64 `json:"external_graphic_candidates"`
	TotalVerifiedQuestions      int64 `json:"total_verified_questions"`
}

// Summary deduplicates objective proof aliases by the original question key.
// Objective qualification follows the active lookup evidence rather than
// counting context rows as additional contributed or verified questions.
// Programming qualification follows FindVerified's current status: a later
// failed validation withdraws a pass until a subsequent formal pass. The formal
// evidence is also checked against the immutable solution and submission bytes.
// A revision with a verified graphic contract is classified as graphic once,
// even when a verified code contract also exists. External graphic candidates
// deduplicate identical identities and files across import versions/provenance;
// they never enter the verified counters.
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
 WHERE result = 'failed' OR (kind IN ('formal','formal_workspace') AND result = 'passed')
 GROUP BY solution_id
), verified_revisions AS (
 SELECT s.revision_hash,
  MAX(CASE WHEN json_extract(e.contract_json, '$.edit_mode') = 'graphic' THEN 1 ELSE 0 END) AS graphic
 FROM solutions s
 JOIN current_programming_state c ON c.solution_id = s.id
 JOIN programming_evidence v ON v.id = c.evidence_id
 JOIN problem_revisions r ON r.revision_hash = s.revision_hash
 JOIN problems p ON p.id = r.problem_id AND p.kind = 'programming'
 JOIN execution_contracts e ON e.contract_hash = s.contract_hash
 WHERE v.kind = 'formal' AND v.result = 'passed'
  AND COALESCE(json_extract(e.contract_json, '$.edit_mode'), '') <> 'terminal'
  AND v.files_hash = s.files_hash AND v.saved_files_hash = s.files_hash
  AND v.submitted_files_hash = s.files_hash
  AND v.revision_hash = s.revision_hash AND v.contract_hash = s.contract_hash
  AND v.terminal = 1 AND v.complete = 1
  AND v.test_count > 0 AND v.passed_count = v.test_count
 GROUP BY s.revision_hash
)
SELECT
 (SELECT COUNT(*) FROM objective_questions q
  WHERE q.resource_complete = 1 AND EXISTS (
   SELECT 1 FROM objective_answers a
   JOIN objective_answer_verifications v ON v.answer_id = a.id AND v.verified = 1
   WHERE a.question_key = q.question_key
  )),
 (SELECT COUNT(*) FROM verified_revisions WHERE graphic = 0),
 (SELECT COUNT(*) FROM verified_revisions WHERE graphic = 1),
 (SELECT COUNT(*) FROM (
  SELECT DISTINCT experiment, title, position, extension, files_json
  FROM external_entries WHERE kind = 'diagram' AND status = 'candidate'
 ))`).Scan(&out.VerifiedObjectiveQuestions, &out.VerifiedProgrammingProblems, &out.VerifiedGraphicProblems, &out.ExternalGraphicCandidates)
	if err != nil {
		return Summary{}, err
	}
	out.VerifiedObjectiveQuestions, err = s.verifiedObjectiveQuestionCount(ctx)
	if err != nil {
		return Summary{}, err
	}
	out.TotalVerifiedQuestions = out.VerifiedObjectiveQuestions + out.VerifiedProgrammingProblems + out.VerifiedGraphicProblems
	return out, nil
}

// Evidence aliases do not own another question. Include newly extracted
// whole-vector answers, while counting an adapted old answer under its original
// semantic key exactly once, regardless of how many contexts reference it.
func (s *Store) verifiedObjectiveQuestionCount(ctx context.Context) (int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.question_key FROM objective_answers a JOIN objective_questions q ON q.question_key=a.question_key
  WHERE q.resource_complete=1 AND COALESCE((SELECT v.verified FROM objective_answer_verifications v WHERE v.answer_id=a.id ORDER BY v.id DESC LIMIT 1),0)=1
  GROUP BY a.question_key HAVING count(DISTINCT a.answer_hash)=1`)
	if err != nil {
		return 0, err
	}
	keys := map[string]bool{}
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			break
		}
		keys[key] = true
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return 0, err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT DISTINCT context_key FROM objective_legacy_context_aliases`)
	if err != nil {
		return 0, err
	}
	var contexts []string
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			break
		}
		contexts = append(contexts, key)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, key := range contexts {
		answers, e := s.LookupObjectiveAnswersWithContext(ctx, key)
		if e != nil {
			return 0, e
		}
		for _, a := range answers {
			if a.Reusable {
				keys[a.QuestionKey] = true
			}
		}
	}
	return int64(len(keys)), nil
}
