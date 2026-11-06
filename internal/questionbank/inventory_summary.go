package questionbank

import "context"

// InventorySummary reports distinct recorded questions, independent of answer
// verification. Solver eligibility and trusted-answer qualification remain in
// Summary and the existing lookup/validation methods.
type InventorySummary struct {
	ObjectiveQuestions  int64 `json:"objective_questions"`
	ProgrammingProblems int64 `json:"programming_problems"`
	GraphicProblems     int64 `json:"graphic_problems"`
	TotalQuestions      int64 `json:"total_questions"`
}

// InventorySummary counts local question identities and complete usable external
// identities in one SQLite snapshot. Local revisions remain one question across
// alternative contracts or answers, with a graphic contract taking precedence.
// External answer variants and repeated provenance do not multiply a question;
// a source identity already bound to a local revision is represented there only.
// Incomplete references and rejected imports cannot create inventory questions.
// Terminal workspace snapshots are excluded until a self-contained script can
// be proven independently reproducible; their passing service runs remain kept.
func (s *Store) InventorySummary(ctx context.Context) (InventorySummary, error) {
	var out InventorySummary
	err := s.db.QueryRowContext(ctx, `
WITH local_revisions AS (
 SELECT r.revision_hash,
  MAX(CASE WHEN json_extract(e.contract_json, '$.edit_mode') = 'graphic' THEN 1 ELSE 0 END) AS graphic
 FROM problem_revisions r
 JOIN problems p ON p.id = r.problem_id AND p.kind = 'programming'
 JOIN problem_snapshots ps ON ps.revision_hash = r.revision_hash
 JOIN execution_contracts e ON e.contract_hash = ps.contract_hash
 WHERE COALESCE(json_extract(e.contract_json, '$.edit_mode'), '') <> 'terminal'
 GROUP BY r.revision_hash
), external_identities AS (
 SELECT DISTINCT experiment, title, position, kind
 FROM external_entries
 WHERE kind IN ('programming', 'diagram') AND status = 'candidate'
), unbound_external_identities AS (
 SELECT x.kind FROM external_identities x
 WHERE NOT EXISTS (
  SELECT 1 FROM external_entries e
  JOIN external_candidate_bindings b ON b.external_entry_id = e.id
  JOIN solutions s ON s.id = b.candidate_id
  JOIN local_revisions r ON r.revision_hash = s.revision_hash
  WHERE e.experiment = x.experiment AND e.title = x.title
   AND e.position = x.position AND e.kind = x.kind AND e.status = 'candidate'
 )
)
SELECT
 (SELECT COUNT(*) FROM objective_questions q
   WHERE NOT EXISTS (SELECT 1 FROM objective_legacy_context_aliases l WHERE l.context_key=q.question_key)
      OR EXISTS (SELECT 1 FROM objective_answers a WHERE a.question_key=q.question_key)),
 (SELECT COUNT(*) FROM local_revisions WHERE graphic = 0)
  + (SELECT COUNT(*) FROM unbound_external_identities WHERE kind = 'programming'),
 (SELECT COUNT(*) FROM local_revisions WHERE graphic = 1)
  + (SELECT COUNT(*) FROM unbound_external_identities WHERE kind = 'diagram')
`).Scan(&out.ObjectiveQuestions, &out.ProgrammingProblems, &out.GraphicProblems)
	if err != nil {
		return InventorySummary{}, err
	}
	out.TotalQuestions = out.ObjectiveQuestions + out.ProgrammingProblems + out.GraphicProblems
	return out, nil
}
