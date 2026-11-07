package questionbank

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
)

// A compatibility alias adds a context to an existing immutable answer. It does
// not copy or move that answer, its evidence, contribution receipts or IDs.
// Proof references are checked again on every lookup, including later failures.
const objectiveLegacyAliasSchema = `
CREATE TABLE IF NOT EXISTS objective_legacy_context_aliases (
 context_key TEXT NOT NULL REFERENCES objective_questions(question_key),
 answer_id INTEGER NOT NULL REFERENCES objective_answers(id),
 snapshot_hash TEXT NOT NULL REFERENCES objective_snapshots(snapshot_hash),
 candidate_id INTEGER NOT NULL DEFAULT 0,
 answer_verification_id INTEGER NOT NULL DEFAULT 0,
 created_at TEXT NOT NULL,
 PRIMARY KEY(context_key,answer_id,snapshot_hash,candidate_id,answer_verification_id),
 CHECK(candidate_id>0 OR answer_verification_id>0)
);
CREATE INDEX IF NOT EXISTS objective_legacy_alias_answers ON objective_legacy_context_aliases(answer_id);
`

// ObjectiveLegacyMigrationReport describes preservation and compatibility, not
// just the number of newly inserted aliases. Unresolved legacy records remain
// intact and may be adapted when their exact historical material is recovered.
type ObjectiveLegacyMigrationReport struct {
	LegacyQuestions           int64 `json:"legacy_questions"`
	LegacyAnswers             int64 `json:"legacy_answers"`
	AnswerVerifications       int64 `json:"answer_verifications"`
	Contributions             int64 `json:"contributions"`
	ContextAliases            int64 `json:"context_aliases"`
	AdaptedLegacyQuestions    int64 `json:"adapted_legacy_questions"`
	AdaptedLegacyAnswers      int64 `json:"adapted_legacy_answers"`
	UnresolvedLegacyQuestions int64 `json:"unresolved_legacy_questions"`
	SnapshotsExamined         int64 `json:"snapshots_examined"`
	IncompleteSnapshots       int64 `json:"incomplete_snapshots"`
	AliasesAdded              int64 `json:"aliases_added"`
	AnswersExtracted          int64 `json:"answers_extracted"`
	ConflictingContexts       int64 `json:"conflicting_contexts"`
}

// AdaptLegacyObjectiveBank is idempotent and transactional. It also runs during
// Open, before the service accepts requests. An interrupted migration leaves
// neither partial aliases nor partial vector expansion in the bank.
func (s *Store) AdaptLegacyObjectiveBank(ctx context.Context) (ObjectiveLegacyMigrationReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ObjectiveLegacyMigrationReport{}, err
	}
	defer tx.Rollback()
	report, err := adaptLegacyObjectiveBankTx(ctx, tx, "")
	if err != nil {
		return report, err
	}
	if err = tx.Commit(); err != nil {
		return report, err
	}
	return report, nil
}

// ObjectiveLegacyCompatibilityReport is read-only; it never marks a reference
// verified, assigns a contribution or guesses missing shared material.
func (s *Store) ObjectiveLegacyCompatibilityReport(ctx context.Context) (ObjectiveLegacyMigrationReport, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ObjectiveLegacyMigrationReport{}, err
	}
	defer tx.Rollback()
	return objectiveLegacyReportTx(ctx, tx)
}

func objectiveLegacyReportTx(ctx context.Context, tx *sql.Tx) (ObjectiveLegacyMigrationReport, error) {
	var r ObjectiveLegacyMigrationReport
	counts := []struct {
		query string
		dest  *int64
	}{
		{`SELECT count(*) FROM objective_questions WHERE fingerprint_version=1`, &r.LegacyQuestions},
		{`SELECT count(*) FROM objective_answers a JOIN objective_questions q ON q.question_key=a.question_key WHERE q.fingerprint_version=1`, &r.LegacyAnswers},
		{`SELECT count(*) FROM objective_answer_verifications`, &r.AnswerVerifications},
		{`SELECT count(*) FROM objective_contributions`, &r.Contributions},
		{`SELECT count(*) FROM objective_legacy_context_aliases`, &r.ContextAliases},
		{`SELECT count(DISTINCT a.question_key) FROM objective_legacy_context_aliases l JOIN objective_answers a ON a.id=l.answer_id JOIN objective_questions q ON q.question_key=a.question_key WHERE q.fingerprint_version=1`, &r.AdaptedLegacyQuestions},
		{`SELECT count(DISTINCT a.id) FROM objective_legacy_context_aliases l JOIN objective_answers a ON a.id=l.answer_id JOIN objective_questions q ON q.question_key=a.question_key WHERE q.fingerprint_version=1`, &r.AdaptedLegacyAnswers},
	}
	for _, c := range counts {
		if err := tx.QueryRowContext(ctx, c.query).Scan(c.dest); err != nil {
			return r, err
		}
	}
	r.UnresolvedLegacyQuestions = r.LegacyQuestions - r.AdaptedLegacyQuestions
	// Count disagreements rather than selecting the newest of two proven answers.
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT context_key FROM objective_legacy_context_aliases l JOIN objective_answers a ON a.id=l.answer_id GROUP BY context_key HAVING count(DISTINCT a.answer_hash)>1)`).Scan(&r.ConflictingContexts); err != nil {
		return r, err
	}
	return r, nil
}

// ObjectiveQuestionResources keeps only inspected resources actually referenced
// by this question or its shared material. An unrelated question's image must
// not make a portable single-question fingerprint depend on the whole level.
func ObjectiveQuestionResources(in ObjectiveQuestionInput) []ObjectiveResource {
	texts := []string{in.Context.Title, in.Context.Statement, in.Question.Question}
	for _, o := range in.Question.Options {
		texts = append(texts, o.Text)
	}
	needed := map[string]bool{}
	for _, text := range texts {
		for _, match := range objectiveMediaLocatorPattern.FindAllStringSubmatch(text, -1) {
			locator := match[1]
			if locator == "" {
				locator = match[2]
			}
			needed[locator] = true
		}
	}
	out := make([]ObjectiveResource, 0, len(in.Resources))
	for _, r := range in.Resources {
		if needed[r.Locator] {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Locator < out[j].Locator })
	return out
}

func historicalObjectiveSnapshot(raw, key string) (answer.ObjectiveSnapshot, bool) {
	var s answer.ObjectiveSnapshot
	if problem.HashContent(raw) != key || json.Unmarshal([]byte(raw), &s) != nil || answer.ValidateObjectiveSnapshot(s) != nil {
		return s, false
	}
	// A missing field is not a deliberately empty field. Old corrupt/partial JSON
	// remains stored, but cannot provide context for another fingerprint.
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &object) != nil {
		return s, false
	}
	for _, field := range []string{"kind", "title", "statement", "questions"} {
		if v, ok := object[field]; !ok || string(v) == "null" {
			return s, false
		}
	}
	var questions []map[string]json.RawMessage
	if json.Unmarshal(object["questions"], &questions) != nil || len(questions) != len(s.Questions) {
		return s, false
	}
	positions := map[int]bool{}
	for i, q := range s.Questions {
		if positions[q.Position] || !objectiveQuestionFieldsComplete(q) {
			return s, false
		}
		positions[q.Position] = true
		for _, field := range []string{"position", "question", "question_type", "category", "options"} {
			if v, ok := questions[i][field]; !ok || string(v) == "null" {
				return s, false
			}
		}
		var options []map[string]json.RawMessage
		if json.Unmarshal(questions[i]["options"], &options) != nil || len(options) != len(q.Options) {
			return s, false
		}
		for _, o := range options {
			for _, field := range []string{"label", "text", "position"} {
				if v, ok := o[field]; !ok || string(v) == "null" {
					return s, false
				}
			}
		}
	}
	return s, true
}

func historicalObjectiveInput(s answer.ObjectiveSnapshot, q answer.ObjectiveQuestion) (ObjectiveQuestionInput, bool) {
	in := ObjectiveQuestionInput{Question: q, Context: ObjectiveQuestionContext{Kind: "objective", Title: s.Title, Statement: s.Statement, Complete: true}}
	texts := []string{s.Title, s.Statement, q.Question}
	for _, o := range q.Options {
		texts = append(texts, o.Text)
	}
	hashes := map[string]bool{}
	for _, text := range texts {
		// Historical URLs are not image bytes. Only canonical hash placeholders,
		// originally written after fetching bytes, can establish portable media.
		if objectiveUnsupportedSnapshotMedia.MatchString(objectiveCanonicalImage.ReplaceAllString(text, "")) {
			return in, false
		}
		for _, m := range objectiveCanonicalImage.FindAllStringSubmatch(text, -1) {
			hashes[m[1]] = true
		}
	}
	for h := range hashes {
		in.Resources = append(in.Resources, ObjectiveResource{Locator: "sha256:" + h, SHA256: h})
	}
	sort.Slice(in.Resources, func(i, j int) bool { return in.Resources[i].Locator < in.Resources[j].Locator })
	_, complete, err := ObjectiveQuestionKeyWithContext(in)
	return in, err == nil && complete
}

type legacyObjectiveAnswer struct {
	ID                                           int64
	Key, Answer, Hash, QuestionRaw, ResourcesRaw string
	FingerprintVersion                           int
}
type historicalObjectiveCandidate struct {
	ID                              int64
	Raw, Source, Score, Max, Status string
}

func adaptLegacyObjectiveBankTx(ctx context.Context, tx *sql.Tx, onlySnapshot string) (ObjectiveLegacyMigrationReport, error) {
	var report ObjectiveLegacyMigrationReport
	var answerCountBefore, aliasCountBefore int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM objective_answers`).Scan(&answerCountBefore); err != nil {
		return report, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM objective_legacy_context_aliases`).Scan(&aliasCountBefore); err != nil {
		return report, err
	}
	query := `SELECT snapshot_hash,snapshot_json FROM objective_snapshots`
	var args []any
	if onlySnapshot != "" {
		query += ` WHERE snapshot_hash=?`
		args = append(args, onlySnapshot)
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return report, err
	}
	type snapshotRow struct{ key, raw string }
	var snapshots []snapshotRow
	for rows.Next() {
		var row snapshotRow
		if err = rows.Scan(&row.key, &row.raw); err != nil {
			break
		}
		snapshots = append(snapshots, row)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return report, err
	}
	// Pre-index immutable old questions; snapshots frequently repeat the same
	// question, so decoding each bank row for each snapshot is unnecessary.
	rows, err = tx.QueryContext(ctx, `SELECT a.id,a.question_key,a.answer,a.answer_hash,q.question_json,q.resources_json,q.fingerprint_version FROM objective_answers a JOIN objective_questions q ON q.question_key=a.question_key WHERE q.fingerprint_version=1`)
	if err != nil {
		return report, err
	}
	legacy := map[string][]legacyObjectiveAnswer{}
	for rows.Next() {
		var a legacyObjectiveAnswer
		if err = rows.Scan(&a.ID, &a.Key, &a.Answer, &a.Hash, &a.QuestionRaw, &a.ResourcesRaw, &a.FingerprintVersion); err != nil {
			break
		}
		var q answer.ObjectiveQuestion
		if json.Unmarshal([]byte(a.QuestionRaw), &q) != nil {
			continue
		}
		normalized, _, _, _, e := prepareObjectiveQuestion(q, nil)
		if e != nil {
			continue
		}
		legacy[marshal(normalized)] = append(legacy[marshal(normalized)], a)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return report, err
	}
	for _, row := range snapshots {
		report.SnapshotsExamined++
		snapshot, complete := historicalObjectiveSnapshot(row.raw, row.key)
		if !complete {
			report.IncompleteSnapshots++
			continue
		}
		candidates, err := historicalObjectiveCandidates(ctx, tx, row.key)
		if err != nil {
			return report, err
		}
		for qi, q := range snapshot.Questions {
			in, complete := historicalObjectiveInput(snapshot, q)
			if !complete {
				continue
			}
			contextKey, _, err := ObjectiveQuestionKeyWithContext(in)
			if err != nil {
				return report, err
			}
			contextPersisted := false
			persistContext := func() error {
				if contextPersisted {
					return nil
				}
				_, _, e := upsertObjectiveQuestionContextTx(ctx, tx, in)
				if e == nil {
					contextPersisted = true
				}
				return e
			}
			normalized, _, _, _, _ := prepareObjectiveQuestion(q, nil)
			matching := legacy[marshal(normalized)]
			// Scope-bound original individual evidence can bridge even a partial-score
			// vector: only the explicitly correct question is reused, not its siblings.
			for _, a := range matching {
				if !legacyResourcesMatchSnapshot(a.ResourcesRaw, snapshot) {
					continue
				}
				var proofID int64
				var proofJSON string
				var verified bool
				e := tx.QueryRowContext(ctx, `SELECT id,verified,evidence_json FROM objective_answer_verifications WHERE answer_id=? AND CASE WHEN json_valid(evidence_json) THEN json_extract(evidence_json,'$.scope_hash') END=? ORDER BY id DESC LIMIT 1`, a.ID, row.key).Scan(&proofID, &verified, &proofJSON)
				if e != nil && !errors.Is(e, sql.ErrNoRows) {
					return report, e
				}
				if e == nil && verified && validLegacyIndividualProof(proofJSON, a.Hash, row.key) {
					if err = persistContext(); err != nil {
						return report, err
					}
					if err = insertObjectiveLegacyAlias(ctx, tx, contextKey, a.ID, row.key, 0, proofID); err != nil {
						return report, err
					}
				}
			}
			for _, c := range candidates {
				if c.Status != "full_score" || !objectiveHasFullScore(c.Score, c.Max) {
					continue
				}
				var candidate answer.ObjectiveCandidate
				if json.Unmarshal([]byte(c.Raw), &candidate) != nil || answer.ValidateObjectiveCandidate(snapshot, candidate) != nil {
					continue
				}
				if err = persistContext(); err != nil {
					return report, err
				}
				answerText := candidate.Answers[qi]
				var ids []int64
				for _, a := range matching {
					if a.Answer == answerText && legacyResourcesMatchSnapshot(a.ResourcesRaw, snapshot) {
						ids = append(ids, a.ID)
					}
				}
				if len(ids) == 0 {
					// A previously whole-vector-only answer is now extracted once. Prefer an
					// existing v2 answer before adding a genuinely new individual record.
					var id int64
					e := tx.QueryRowContext(ctx, `SELECT id FROM objective_answers WHERE question_key=? AND answer_hash=?`, contextKey, problem.HashContent(answerText)).Scan(&id)
					if errors.Is(e, sql.ErrNoRows) {
						_, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO objective_answers(question_key,answer,answer_hash,source,model,created_at) VALUES(?,?,?,?,?,?)`, contextKey, answerText, problem.HashContent(answerText), c.Source, "", now())
						if e == nil {
							e = tx.QueryRowContext(ctx, `SELECT id FROM objective_answers WHERE question_key=? AND answer_hash=?`, contextKey, problem.HashContent(answerText)).Scan(&id)
						}
					}
					if e != nil {
						return report, e
					}
					ids = append(ids, id)
				}
				for _, id := range ids {
					if err = insertObjectiveLegacyAlias(ctx, tx, contextKey, id, row.key, c.ID, 0); err != nil {
						return report, err
					}
				}
			}
		}
	}
	result, err := objectiveLegacyReportTx(ctx, tx)
	if err != nil {
		return report, err
	}
	var answersAfter int64
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM objective_answers`).Scan(&answersAfter); err != nil {
		return report, err
	}
	result.SnapshotsExamined = report.SnapshotsExamined
	result.IncompleteSnapshots = report.IncompleteSnapshots
	result.AliasesAdded = result.ContextAliases - aliasCountBefore
	result.AnswersExtracted = answersAfter - answerCountBefore
	return result, nil
}

func historicalObjectiveCandidates(ctx context.Context, tx *sql.Tx, key string) ([]historicalObjectiveCandidate, error) {
	rows, err := tx.QueryContext(ctx, `SELECT c.id,c.candidate_json,c.source,v.result,v.score,v.max_score FROM objective_candidates c JOIN objective_verifications v ON v.candidate_id=c.id WHERE c.snapshot_hash=? AND v.id=(SELECT max(v2.id) FROM objective_verifications v2 WHERE v2.candidate_id=c.id)`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []historicalObjectiveCandidate
	for rows.Next() {
		var c historicalObjectiveCandidate
		if err = rows.Scan(&c.ID, &c.Raw, &c.Source, &c.Status, &c.Score, &c.Max); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func legacyResourcesMatchSnapshot(raw string, snapshot answer.ObjectiveSnapshot) bool {
	var resources []ObjectiveResource
	if json.Unmarshal([]byte(raw), &resources) != nil {
		return false
	}
	available := map[string]string{}
	for _, q := range snapshot.Questions {
		in, ok := historicalObjectiveInput(snapshot, q)
		if !ok {
			return false
		}
		for _, r := range in.Resources {
			available[r.Locator] = r.SHA256
		}
	}
	for _, r := range resources {
		if available[r.Locator] != r.SHA256 {
			return false
		}
	}
	return true
}
func validLegacyIndividualProof(raw, hash, scope string) bool {
	var v ObjectiveValidation
	return json.Unmarshal([]byte(raw), &v) == nil && v.AnswerHash == hash && v.ScopeHash == scope && v.Confirmed && v.Passed && (v.PerQuestionCorrect || (v.FullScore && objectiveHasFullScore(v.Score, v.MaxScore)))
}
func insertObjectiveLegacyAlias(ctx context.Context, tx *sql.Tx, key string, id int64, snapshot string, candidate, verification int64) error {
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO objective_legacy_context_aliases(context_key,answer_id,snapshot_hash,candidate_id,answer_verification_id,created_at) VALUES(?,?,?,?,?,?)`, key, id, snapshot, candidate, verification, now())
	return err
}
func upsertObjectiveQuestionContextTx(ctx context.Context, tx *sql.Tx, in ObjectiveQuestionInput) (string, bool, error) {
	key, reusable, err := ObjectiveQuestionKeyWithContext(in)
	if err != nil {
		return "", false, err
	}
	q, resources, _, qr, err := prepareObjectiveQuestion(in.Question, in.Resources)
	if err != nil {
		return "", false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO objective_questions(question_key,question_json,resources_json,resource_complete,context_json,context_complete,fingerprint_version,created_at) VALUES(?,?,?,?,?,?,2,?)`, key, marshal(q), marshal(resources), qr && objectiveObjectiveMediaComplete([]string{in.Context.Title, in.Context.Statement}, resources), marshal(in.Context), in.Context.Complete && in.Context.Kind == "objective", now())
	return key, reusable, err
}

// lookupObjectiveLegacyAliases returns original answer IDs plus scoped proof;
// direct per-question history stays attached to those IDs and is not rewritten.
func (s *Store) lookupObjectiveLegacyAliases(ctx context.Context, key string) ([]ObjectiveAnswer, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.id,a.question_key,a.answer,a.answer_hash,a.source,a.model,a.created_at,q.resource_complete,l.snapshot_hash,l.candidate_id,l.answer_verification_id
 FROM objective_legacy_context_aliases l JOIN objective_answers a ON a.id=l.answer_id JOIN objective_questions q ON q.question_key=l.context_key
 WHERE l.context_key=? AND q.context_complete=1 AND q.fingerprint_version>=2`, key)
	if err != nil {
		return nil, err
	}
	type alias struct {
		a                       ObjectiveAnswer
		snapshot                string
		candidate, verification int64
	}
	var aliases []alias
	for rows.Next() {
		var l alias
		var stamp string
		if err = rows.Scan(&l.a.ID, &l.a.QuestionKey, &l.a.Answer, &l.a.AnswerHash, &l.a.Source, &l.a.Model, &stamp, &l.a.ResourceComplete, &l.snapshot, &l.candidate, &l.verification); err != nil {
			break
		}
		l.a.CreatedAt = parseTime(stamp)
		aliases = append(aliases, l)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	byID := map[int64]ObjectiveAnswer{}
	for _, l := range aliases {
		proven := false
		var proof ObjectiveValidation
		if l.candidate > 0 {
			var status, score, max, raw, snapshot, stamp string
			e := s.db.QueryRowContext(ctx, `SELECT v.result,v.score,v.max_score,c.candidate_json,c.snapshot_hash,v.created_at FROM objective_candidates c JOIN objective_verifications v ON v.candidate_id=c.id WHERE c.id=? ORDER BY v.id DESC LIMIT 1`, l.candidate).Scan(&status, &score, &max, &raw, &snapshot, &stamp)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return nil, e
			}
			proven = e == nil && snapshot == l.snapshot && status == "full_score" && objectiveHasFullScore(score, max)

			if proven {
				proof = ObjectiveValidation{AnswerID: l.a.ID, Confirmed: true, Passed: true, FullScore: true, Score: score, MaxScore: max, AnswerHash: l.a.AnswerHash, ScopeHash: l.snapshot, CreatedAt: parseTime(stamp), Feedback: fmt.Sprintf("Historical exact-vector full-score evidence, candidate %d; original answer ID preserved.", l.candidate)}
			}
		} else {
			// A recovered collection scope includes its original game,
			// challenge, complete snapshot and answer vector. The alias keeps
			// the original proof ID, so recover that exact scope rather than
			// pretending the old evidence used the newer snapshot-only hash.
			var originalRaw string
			e := s.db.QueryRowContext(ctx, `SELECT evidence_json FROM objective_answer_verifications WHERE id=? AND answer_id=?`, l.verification, l.a.ID).Scan(&originalRaw)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return nil, e
			}
			var original ObjectiveValidation
			if e != nil || json.Unmarshal([]byte(originalRaw), &original) != nil {
				continue
			}
			var id int64
			var raw, stamp string
			var verified bool
			e = s.db.QueryRowContext(ctx, `SELECT id,verified,evidence_json,created_at FROM objective_answer_verifications WHERE answer_id=? AND CASE WHEN json_valid(evidence_json) THEN json_extract(evidence_json,'$.scope_hash') END=? ORDER BY id DESC LIMIT 1`, l.a.ID, original.ScopeHash).Scan(&id, &verified, &raw, &stamp)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return nil, e
			}
			proven = e == nil && verified && validLegacyIndividualProof(raw, l.a.AnswerHash, original.ScopeHash)
			if proven {
				_ = json.Unmarshal([]byte(raw), &proof)
				proof.ID = id
				proof.CreatedAt = parseTime(stamp)
			}
		}
		if proven {
			var latestVerified bool
			var latestStamp string
			e := s.db.QueryRowContext(ctx, `SELECT v.verified,v.created_at FROM objective_answer_verifications v
              WHERE v.answer_id=? AND CASE WHEN json_valid(v.evidence_json) THEN json_extract(v.evidence_json,'$.scope_hash') END IN
               (SELECT snapshot_hash FROM objective_legacy_context_aliases WHERE context_key=? AND answer_id=?
                UNION SELECT CASE WHEN json_valid(p.evidence_json) THEN json_extract(p.evidence_json,'$.scope_hash') END
                 FROM objective_legacy_context_aliases al JOIN objective_answer_verifications p ON p.id=al.answer_verification_id
                 WHERE al.context_key=? AND al.answer_id=?)
              ORDER BY v.id DESC LIMIT 1`, l.a.ID, key, l.a.ID, key, l.a.ID).Scan(&latestVerified, &latestStamp)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return nil, e
			}
			if e == nil && !latestVerified && parseTime(latestStamp).After(proof.CreatedAt) {
				proven = false
			}
		}
		if !proven {
			continue
		}
		if _, ok := byID[l.a.ID]; !ok {
			l.a.Verified = true
			l.a.Reusable = l.a.ResourceComplete
			l.a.Validations = []ObjectiveValidation{proof}
			byID[l.a.ID] = l.a
		}
	}
	out := make([]ObjectiveAnswer, 0, len(byID))
	for _, a := range byID {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// rejectObjectiveConflicts keeps all evidence visible but exposes no automatic
// winner when the same exact context has different currently verified answers.
func rejectObjectiveConflicts(answers []ObjectiveAnswer) []ObjectiveAnswer {
	hashes := map[string]bool{}
	for _, a := range answers {
		if a.Reusable {
			hashes[strings.TrimSpace(a.AnswerHash)] = true
		}
	}
	if len(hashes) > 1 {
		for i := range answers {
			answers[i].Reusable = false
		}
	}
	return answers
}

// LookupObjectiveLegacyReferences exposes exact old question/option/resource
// matches for contextual confirmation. Verified means historical correctness;
// Reusable deliberately stays false until shared-context proof is established.
func (s *Store) LookupObjectiveLegacyReferences(ctx context.Context, in ObjectiveQuestionInput) ([]ObjectiveAnswer, error) {
	_, complete, err := ObjectiveQuestionKeyWithContext(in)
	if err != nil {
		return nil, err
	}
	// A media fetch can be unavailable during the cheap preflight. Legacy
	// question records that never had media bindings are still safe to compare
	// by exact text/options; only a complete context may promote an answer to
	// reusable, so an incomplete input remains a reference for model review.
	_ = complete
	normalized, _, _, _, err := prepareObjectiveQuestion(in.Question, nil)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT q.question_key,q.question_json,q.resources_json FROM objective_questions q WHERE q.fingerprint_version=1`)
	if err != nil {
		return nil, err
	}
	var keys []string
	for rows.Next() {
		var key, raw, resourcesRaw string
		if err = rows.Scan(&key, &raw, &resourcesRaw); err != nil {
			break
		}
		var q answer.ObjectiveQuestion
		if json.Unmarshal([]byte(raw), &q) != nil {
			continue
		}
		q, _, _, _, e := prepareObjectiveQuestion(q, nil)
		if e != nil || marshal(q) != marshal(normalized) {
			continue
		}
		var resources []ObjectiveResource
		if json.Unmarshal([]byte(resourcesRaw), &resources) != nil {
			continue
		}
		if !legacyResourceSetsCompatible(resources, in.Resources) {
			continue
		}
		keys = append(keys, key)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	var out []ObjectiveAnswer
	for _, key := range keys {
		answers, e := s.LookupObjectiveAnswers(ctx, key)
		if e != nil && !errors.Is(e, ErrNotFound) {
			return nil, e
		}
		for _, a := range answers {
			a.Reusable = false
			out = append(out, a)
		}
	}
	return out, nil
}

func legacyResourceSetsCompatible(old, current []ObjectiveResource) bool {
	available := map[string]string{}
	for _, r := range current {
		available[r.Locator] = r.SHA256
	}
	for _, r := range old {
		if available[r.Locator] != r.SHA256 {
			return false
		}
	}
	return true
}

// RecordObjectiveContextValidation binds real current-scope grading evidence to
// the original answer ID. It never turns model agreement into a verified answer.
// The scope must be an exact persisted snapshot, not a guessed shared context.
func (s *Store) RecordObjectiveContextValidation(ctx context.Context, in ObjectiveQuestionInput, answerID int64, v ObjectiveValidation) error {
	key, complete, err := ObjectiveQuestionKeyWithContext(in)
	if err != nil {
		return err
	}
	if !complete {
		return errors.New("objective context validation requires a complete input")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw, resourcesRaw, hash, snapshotRaw string
	err = tx.QueryRowContext(ctx, `SELECT q.question_json,q.resources_json,a.answer_hash FROM objective_answers a JOIN objective_questions q ON q.question_key=a.question_key WHERE a.id=?`, answerID).Scan(&raw, &resourcesRaw, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	var q answer.ObjectiveQuestion
	var resources []ObjectiveResource
	if json.Unmarshal([]byte(raw), &q) != nil || json.Unmarshal([]byte(resourcesRaw), &resources) != nil {
		return errors.New("invalid legacy objective answer contract")
	}
	normalized, _, _, _, err := prepareObjectiveQuestion(q, nil)
	if err != nil {
		return err
	}
	current, _, _, _, err := prepareObjectiveQuestion(in.Question, nil)
	if err != nil {
		return err
	}
	if marshal(normalized) != marshal(current) || !legacyResourceSetsCompatible(resources, in.Resources) || v.AnswerHash != hash {
		return errors.New("objective context validation does not match original answer")
	}
	if err = tx.QueryRowContext(ctx, `SELECT snapshot_json FROM objective_snapshots WHERE snapshot_hash=?`, v.ScopeHash).Scan(&snapshotRaw); err != nil {
		return errors.New("objective context validation requires exact persisted snapshot scope")
	}
	snapshot, valid := historicalObjectiveSnapshot(snapshotRaw, v.ScopeHash)
	if !valid {
		return errors.New("objective context validation snapshot is incomplete")
	}
	matches := false
	for _, sq := range snapshot.Questions {
		historical, ok := historicalObjectiveInput(snapshot, sq)
		if !ok {
			continue
		}
		historicalKey, _, e := ObjectiveQuestionKeyWithContext(historical)
		if e == nil && historicalKey == key {
			matches = true
			break
		}
	}
	if !matches {
		return errors.New("objective context validation input differs from snapshot scope")
	}
	for _, text := range []string{v.Feedback, v.Score, v.MaxScore} {
		if err = problem.ValidatePublicText(text); err != nil {
			return err
		}
	}
	verified := v.Confirmed && v.Passed && (v.PerQuestionCorrect || (v.FullScore && objectiveHasFullScore(v.Score, v.MaxScore)))
	v.ID = 0
	v.AnswerID = answerID
	v.CreatedAt = parseTime(now())
	result, err := tx.ExecContext(ctx, `INSERT INTO objective_answer_verifications(answer_id,verified,evidence_json,created_at) VALUES(?,?,?,?)`, answerID, verified, marshal(v), v.CreatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"))
	if err != nil {
		return err
	}
	proofID, err := result.LastInsertId()
	if err != nil {
		return err
	}
	if _, _, err = upsertObjectiveQuestionContextTx(ctx, tx, in); err != nil {
		return err
	}
	// Negative results are retained too. A pre-existing alias will observe that
	// latest failure and stop returning a reusable answer under this scope.
	if err = insertObjectiveLegacyAlias(ctx, tx, key, answerID, v.ScopeHash, 0, proofID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) adaptObjectiveSnapshotAfterWrite(ctx context.Context, snapshot string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Upgrade fixtures can write published evidence before their schema is opened
	// by the application. Open performs the transactional migration afterwards.
	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='objective_legacy_context_aliases'`).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return nil
	}
	if _, err = adaptLegacyObjectiveBankTx(ctx, tx, snapshot); err != nil {
		return err
	}
	return tx.Commit()
}

// ImportLegacyObjectiveContext recovers context from a retained original
// collection recording. Recomputing the old scope hash cryptographically binds
// game, challenge, every question/option and the complete recorded answer vector
// to original verified evidence. A plausible-looking recording without this
// exact binding adds no aliases and cannot promote any old answer.
func (s *Store) ImportLegacyObjectiveContext(ctx context.Context, snapshot answer.ObjectiveSnapshot, game string, challenge int64, answers []string) (ObjectiveLegacyMigrationReport, error) {
	var empty ObjectiveLegacyMigrationReport
	if challenge <= 0 || strings.TrimSpace(game) == "" {
		return empty, errors.New("legacy context requires original game and challenge")
	}
	if err := problem.ValidatePublicText(game); err != nil {
		return empty, err
	}
	if err := answer.ValidateObjectiveCandidate(snapshot, answer.ObjectiveCandidate{Kind: "objective", Answers: answers}); err != nil {
		return empty, err
	}
	snapshotRaw := marshal(snapshot)
	snapshotHash := problem.HashContent(snapshotRaw)
	if _, complete := historicalObjectiveSnapshot(snapshotRaw, snapshotHash); !complete {
		return empty, errors.New("legacy context snapshot is incomplete")
	}
	scopeRaw := marshal(struct {
		Game      string
		Challenge int64
		Snapshot  answer.ObjectiveSnapshot
		Answers   []string
	}{game, challenge, snapshot, answers})
	scope := problem.HashContent(scopeRaw)
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	var aliasesBefore int64
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM objective_legacy_context_aliases`).Scan(&aliasesBefore); err != nil {
		return empty, err
	}
	type binding struct {
		input             ObjectiveQuestionInput
		answerID, proofID int64
	}
	var bindings []binding
	for i, q := range snapshot.Questions {
		in, complete := historicalObjectiveInput(snapshot, q)
		if !complete {
			continue
		}
		normalized, _, _, _, err := prepareObjectiveQuestion(q, nil)
		if err != nil {
			return empty, err
		}
		rows, err := tx.QueryContext(ctx, `SELECT a.id,a.answer_hash,q.question_json,q.resources_json FROM objective_answers a JOIN objective_questions q ON q.question_key=a.question_key WHERE q.fingerprint_version=1 AND a.answer_hash=?`, problem.HashContent(answers[i]))
		if err != nil {
			return empty, err
		}
		type original struct {
			id                   int64
			hash, raw, resources string
		}
		var originals []original
		for rows.Next() {
			var a original
			if err = rows.Scan(&a.id, &a.hash, &a.raw, &a.resources); err != nil {
				break
			}
			originals = append(originals, a)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return empty, err
		}
		for _, a := range originals {
			var old answer.ObjectiveQuestion
			var resources []ObjectiveResource
			if json.Unmarshal([]byte(a.raw), &old) != nil || json.Unmarshal([]byte(a.resources), &resources) != nil {
				continue
			}
			old, _, _, _, err = prepareObjectiveQuestion(old, nil)
			if err != nil || marshal(old) != marshal(normalized) || !legacyResourceSetsCompatible(resources, in.Resources) {
				continue
			}
			var proofID int64
			var verified bool
			var raw string
			err = tx.QueryRowContext(ctx, `SELECT id,verified,evidence_json FROM objective_answer_verifications WHERE answer_id=? AND CASE WHEN json_valid(evidence_json) THEN json_extract(evidence_json,'$.scope_hash') END=? ORDER BY id DESC LIMIT 1`, a.id, scope).Scan(&proofID, &verified, &raw)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return empty, err
			}
			if err == nil && verified && validLegacyIndividualProof(raw, a.hash, scope) {
				bindings = append(bindings, binding{in, a.id, proofID})
			}
		}
	}
	if len(bindings) > 0 {
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO objective_snapshots(snapshot_hash,snapshot_json,created_at) VALUES(?,?,?)`, snapshotHash, snapshotRaw, now()); err != nil {
			return empty, err
		}
		for _, b := range bindings {
			key, _, err := upsertObjectiveQuestionContextTx(ctx, tx, b.input)
			if err != nil {
				return empty, err
			}
			if err = insertObjectiveLegacyAlias(ctx, tx, key, b.answerID, snapshotHash, 0, b.proofID); err != nil {
				return empty, err
			}
		}
	}
	report, err := objectiveLegacyReportTx(ctx, tx)
	if err != nil {
		return empty, err
	}
	report.AliasesAdded = report.ContextAliases - aliasesBefore
	report.SnapshotsExamined = 1
	if err = tx.Commit(); err != nil {
		return report, err
	}
	return report, nil
}

// A context can contain a native v2 answer and a legacy ID for the same answer
// bytes. Their evidence shares one append-only ID sequence. A newer scoped
// rejection must withdraw every copy/alias, even after clock rollback or when
// two writes have identical timestamps. A whole-vector alias never overrides
// the latest explicitly negative individual proof by timestamp guessing.
func (s *Store) withdrawObjectiveContextFailures(ctx context.Context, key string, answers []ObjectiveAnswer) ([]ObjectiveAnswer, error) {
	checked := map[string]bool{}
	for _, a := range answers {
		if checked[a.AnswerHash] {
			continue
		}
		checked[a.AnswerHash] = true
		var verified bool
		err := s.db.QueryRowContext(ctx, `SELECT v.verified FROM objective_answer_verifications v
   JOIN objective_answers a ON a.id=v.answer_id
   WHERE a.answer_hash=? AND (a.question_key=? OR EXISTS(
    SELECT 1 FROM objective_legacy_context_aliases l
     LEFT JOIN objective_answer_verifications original ON original.id=l.answer_verification_id
    WHERE l.context_key=? AND l.answer_id=a.id AND
     CASE WHEN json_valid(v.evidence_json) THEN json_extract(v.evidence_json,'$.scope_hash') END IN
      (l.snapshot_hash,CASE WHEN json_valid(original.evidence_json) THEN json_extract(original.evidence_json,'$.scope_hash') END)
   )) ORDER BY v.id DESC LIMIT 1`, a.AnswerHash, key, key).Scan(&verified)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if err == nil && !verified {
			for i := range answers {
				if answers[i].AnswerHash == a.AnswerHash {
					answers[i].Verified = false
					answers[i].Reusable = false
				}
			}
		}
	}
	return answers, nil
}
