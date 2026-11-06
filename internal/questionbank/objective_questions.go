package questionbank

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strings"
	"time"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
)

// ObjectiveResource binds an image locator to the hash of the bytes that were
// actually inspected. Locators alone cannot establish unchanged image content.
type ObjectiveResource struct {
	Locator string `json:"locator"`
	SHA256  string `json:"sha256"`
}

// ObjectiveQuestionContext is the shared material that applies to every
// question in a challenge. A question answer is reusable only when the
// collector explicitly confirms that this context was read completely. The
// explicit flag is intentional: an empty title or statement can be valid
// upstream data, while an omitted field is indistinguishable from an empty
// field unless the reader records completeness separately.
type ObjectiveQuestionContext struct {
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Statement string `json:"statement"`
	Complete  bool   `json:"complete"`
}

// ObjectiveQuestionInput is the only context-aware collection input. The
// old question-only API remains for compatibility, but production collection
// should use this form so a partial upstream read becomes non-reusable rather
// than accidentally matching a complete historical question.
type ObjectiveQuestionInput struct {
	Question  answer.ObjectiveQuestion `json:"question"`
	Context   ObjectiveQuestionContext `json:"context"`
	Resources []ObjectiveResource      `json:"resources"`
}

type ObjectiveAnswer struct {
	ID               int64                 `json:"id"`
	QuestionKey      string                `json:"question_key"`
	Answer           string                `json:"answer"`
	AnswerHash       string                `json:"answer_hash"`
	Source           string                `json:"source"`
	Model            string                `json:"model,omitempty"`
	Verified         bool                  `json:"verified"`
	ResourceComplete bool                  `json:"resource_complete"`
	Reusable         bool                  `json:"reusable"`
	CreatedAt        time.Time             `json:"created_at"`
	Validations      []ObjectiveValidation `json:"validations,omitempty"`
}

// A full-score batch can verify each answer submitted in that batch. An
// explicit true result for an individual question can also verify its answer;
// a submitted flag or a partial batch score never suffices.
type ObjectiveValidation struct {
	ID                 int64     `json:"id,omitempty"`
	AnswerID           int64     `json:"answer_id,omitempty"`
	Passed             bool      `json:"passed"`
	Confirmed          bool      `json:"confirmed"`
	FullScore          bool      `json:"full_score"`
	PerQuestionCorrect bool      `json:"per_question_correct,omitempty"`
	Score              string    `json:"score,omitempty"`
	MaxScore           string    `json:"max_score,omitempty"`
	AnswerHash         string    `json:"answer_hash"`
	ScopeHash          string    `json:"scope_hash"`
	Feedback           string    `json:"feedback,omitempty"`
	CreatedAt          time.Time `json:"created_at,omitempty"`
}

var objectiveSHA256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var objectiveMediaPattern = regexp.MustCompile(`(?i)<(?:img|svg|object|embed|video|audio|canvas|iframe)\b|!\[`)
var objectiveMediaLocatorPattern = regexp.MustCompile(`(?i)(?:\bsrc\s*=\s*["']([^"']+)["']|!\[[^\]]*\]\(\s*<?([^\s)>]+))`)
var objectiveEmbeddedMediaPattern = regexp.MustCompile(`(?i)<(?:svg|object|embed|video|audio|canvas|iframe)\b`)
var objectiveDecimalPattern = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]{1,3})?$`)

func prepareObjectiveQuestion(in answer.ObjectiveQuestion, resources []ObjectiveResource) (answer.ObjectiveQuestion, []ObjectiveResource, string, bool, error) {
	q := in
	// Position is local to a challenge, not part of the meaning of a question.
	q.Position = 1
	q.Question = strings.ReplaceAll(q.Question, "\r\n", "\n")
	q.Options = append([]answer.ObjectiveOption(nil), in.Options...)
	sort.Slice(q.Options, func(i, j int) bool { return q.Options[i].Position < q.Options[j].Position })
	positions := map[int]bool{}
	for i := range q.Options {
		o := &q.Options[i]
		o.Text = strings.ReplaceAll(o.Text, "\r\n", "\n")
		if positions[o.Position] || o.Position >= 26 || o.Label != string(rune('A'+o.Position)) {
			return q, nil, "", false, errors.New("objective option position or label is invalid")
		}
		positions[o.Position] = true
	}
	if err := answer.ValidateObjectiveSnapshot(answer.ObjectiveSnapshot{Kind: "objective", Questions: []answer.ObjectiveQuestion{q}}); err != nil {
		return q, nil, "", false, err
	}
	resources = append([]ObjectiveResource{}, resources...)
	sort.Slice(resources, func(i, j int) bool { return resources[i].Locator < resources[j].Locator })
	hashes := map[string]string{}
	for _, r := range resources {
		if r.Locator == "" || !objectiveSHA256Pattern.MatchString(r.SHA256) || hashes[r.Locator] != "" {
			return q, nil, "", false, errors.New("objective resource hash is invalid or duplicated")
		}
		if err := problem.ValidatePublicText(r.Locator); err != nil {
			return q, nil, "", false, err
		}
		hashes[r.Locator] = r.SHA256
	}
	texts := []string{q.Question}
	for _, option := range q.Options {
		texts = append(texts, option.Text)
	}
	complete := true
	for _, text := range texts {
		if !objectiveMediaPattern.MatchString(text) {
			continue
		}
		locators := objectiveMediaLocatorPattern.FindAllStringSubmatch(text, -1)
		if len(locators) == 0 {
			complete = false
		}
		for _, match := range locators {
			locator := match[1]
			if locator == "" {
				locator = match[2]
			}
			if hashes[locator] == "" {
				complete = false
			}
		}
		// An inline or embedded object has semantics beyond an img src. Keep it
		// stored, but require a later dedicated representation before reuse.
		if objectiveEmbeddedMediaPattern.MatchString(text) {
			complete = false
		}
	}
	encoded, err := json.Marshal(struct {
		Version   int                      `json:"version"`
		Question  answer.ObjectiveQuestion `json:"question"`
		Resources []ObjectiveResource      `json:"resources"`
	}{1, q, resources})
	if err != nil {
		return q, nil, "", false, err
	}
	return q, resources, problem.HashContent(string(encoded)), complete, nil
}

// ObjectiveQuestionKey excludes challenge, task, account, title, and local
// question position; ordered option text, category, and raw media references
// remain in the contract. It never mutates the caller's slices.
func ObjectiveQuestionKey(q answer.ObjectiveQuestion) (string, error) {
	_, _, key, _, err := prepareObjectiveQuestion(q, nil)
	return key, err
}

// ObjectiveQuestionKeyWithContext fingerprints one question together with the
// complete shared challenge context and inspected media. The boolean result is
// false whenever any part of the read is incomplete. Incomplete keys are still
// persisted for auditability, but they are deliberately different from the
// legacy question-only key and can never yield a reusable answer.
func ObjectiveQuestionKeyWithContext(in ObjectiveQuestionInput) (string, bool, error) {
	q, resources, _, questionResourcesComplete, err := prepareObjectiveQuestion(in.Question, in.Resources)
	if err != nil {
		return "", false, err
	}
	if err = validateObjectiveQuestionContext(in.Context); err != nil {
		return "", false, err
	}
	contextComplete := in.Context.Complete
	questionComplete := objectiveQuestionFieldsComplete(q)
	sharedResourcesComplete := objectiveObjectiveMediaComplete([]string{in.Context.Title, in.Context.Statement}, resources)
	complete := contextComplete && questionComplete && questionResourcesComplete && sharedResourcesComplete
	encoded, err := json.Marshal(struct {
		Version   int                      `json:"version"`
		Complete  bool                     `json:"complete"`
		Context   ObjectiveQuestionContext `json:"context"`
		Question  answer.ObjectiveQuestion `json:"question"`
		Resources []ObjectiveResource      `json:"resources"`
	}{2, complete, in.Context, q, resources})
	if err != nil {
		return "", false, err
	}
	return problem.HashContent(string(encoded)), complete, nil
}

func validateObjectiveQuestionContext(ctx ObjectiveQuestionContext) error {
	if ctx.Kind != "" && ctx.Kind != "objective" {
		return errors.New("objective context kind is invalid")
	}
	for _, text := range []string{ctx.Kind, ctx.Title, ctx.Statement} {
		if err := problem.ValidatePublicText(text); err != nil {
			return err
		}
	}
	if ctx.Complete && ctx.Kind != "objective" {
		return errors.New("complete objective context must declare kind")
	}
	return nil
}

func objectiveQuestionFieldsComplete(q answer.ObjectiveQuestion) bool {
	if strings.TrimSpace(q.Question) == "" || len(q.Options) == 0 {
		return false
	}
	for i, option := range q.Options {
		if strings.TrimSpace(option.Text) == "" || option.Position != i || option.Label != string(rune('A'+i)) {
			return false
		}
	}
	return true
}

// objectiveObjectiveMediaComplete checks shared title/statement media with
// the same content-hash contract as question and option media. It is kept
// separate from prepareObjectiveQuestion so the legacy question-only key does
// not silently acquire a context it never received.
func objectiveObjectiveMediaComplete(texts []string, resources []ObjectiveResource) bool {
	hashes := make(map[string]string, len(resources))
	for _, resource := range resources {
		hashes[resource.Locator] = resource.SHA256
	}
	for _, text := range texts {
		if !objectiveMediaPattern.MatchString(text) {
			continue
		}
		matches := objectiveMediaLocatorPattern.FindAllStringSubmatch(text, -1)
		if len(matches) == 0 || objectiveEmbeddedMediaPattern.MatchString(text) {
			return false
		}
		for _, match := range matches {
			locator := match[1]
			if locator == "" {
				locator = match[2]
			}
			if hashes[locator] == "" {
				return false
			}
		}
	}
	return true
}

func (s *Store) UpsertObjectiveQuestion(ctx context.Context, q answer.ObjectiveQuestion) (string, error) {
	return s.UpsertObjectiveQuestionWithResources(ctx, q, nil)
}

func (s *Store) UpsertObjectiveQuestionWithResources(ctx context.Context, in answer.ObjectiveQuestion, resources []ObjectiveResource) (string, error) {
	q, resources, key, complete, err := prepareObjectiveQuestion(in, resources)
	if err != nil {
		return "", err
	}
	_, err = s.db.ExecContext(ctx, "INSERT OR IGNORE INTO objective_questions(question_key,question_json,resources_json,resource_complete,created_at) VALUES(?,?,?,?,?)", key, marshal(q), marshal(resources), complete, now())
	return key, err
}

// UpsertObjectiveQuestionWithContext stores the context-aware fingerprint and
// returns whether it is safe for automatic reuse. A partial read is retained
// so the collector can diagnose it later, but context_complete/resource_complete
// remain false and lookup will not promote any answer under that key.
func (s *Store) UpsertObjectiveQuestionWithContext(ctx context.Context, in ObjectiveQuestionInput) (string, bool, error) {
	key, reusable, err := ObjectiveQuestionKeyWithContext(in)
	if err != nil {
		return "", false, err
	}
	q, resources, _, questionResourcesComplete, err := prepareObjectiveQuestion(in.Question, in.Resources)
	if err != nil {
		return "", false, err
	}
	sharedResourcesComplete := objectiveObjectiveMediaComplete([]string{in.Context.Title, in.Context.Statement}, resources)
	contextComplete := in.Context.Complete && in.Context.Kind == "objective"
	_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO objective_questions(
		question_key,question_json,resources_json,resource_complete,context_json,context_complete,fingerprint_version,created_at
	) VALUES(?,?,?,?,?,?,?,?)`, key, marshal(q), marshal(resources), questionResourcesComplete && sharedResourcesComplete, marshal(in.Context), contextComplete, 2, now())
	return key, reusable, err
}

func (s *Store) AddObjectiveAnswer(ctx context.Context, key, answerText, source, model string) (int64, error) {
	answerText = strings.ToUpper(strings.TrimSpace(answerText))
	var raw string
	if err := s.db.QueryRowContext(ctx, "SELECT question_json FROM objective_questions WHERE question_key=?", key).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	var q answer.ObjectiveQuestion
	if err := json.Unmarshal([]byte(raw), &q); err != nil {
		return 0, err
	}
	for _, text := range []string{source, model} {
		if err := problem.ValidatePublicText(text); err != nil {
			return 0, err
		}
	}
	if err := answer.ValidateObjectiveCandidate(answer.ObjectiveSnapshot{Kind: "objective", Questions: []answer.ObjectiveQuestion{q}}, answer.ObjectiveCandidate{Kind: "objective", Answers: []string{answerText}}); err != nil {
		return 0, err
	}
	hash := problem.HashContent(answerText)
	_, err := s.db.ExecContext(ctx, "INSERT OR IGNORE INTO objective_answers(question_key,answer,answer_hash,source,model,created_at) VALUES(?,?,?,?,?,?)", key, answerText, hash, source, model, now())
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.db.QueryRowContext(ctx, "SELECT id FROM objective_answers WHERE question_key=? AND answer_hash=?", key, hash).Scan(&id)
	return id, err
}

func objectiveHasFullScore(earned, maximum string) bool {
	earned, maximum = strings.TrimSpace(earned), strings.TrimSpace(maximum)
	if len(earned) > 128 || len(maximum) > 128 || !objectiveDecimalPattern.MatchString(earned) || !objectiveDecimalPattern.MatchString(maximum) {
		return false
	}
	a, okA := new(big.Rat).SetString(earned)
	b, okB := new(big.Rat).SetString(maximum)
	return okA && okB && a.Sign() >= 0 && b.Sign() > 0 && a.Cmp(b) == 0
}

func (s *Store) RecordObjectiveValidation(ctx context.Context, id int64, v ObjectiveValidation) error {
	var hash string
	err := s.db.QueryRowContext(ctx, "SELECT answer_hash FROM objective_answers WHERE id=?", id).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if v.AnswerHash != hash || !objectiveSHA256Pattern.MatchString(v.ScopeHash) {
		return errors.New("objective validation must match the exact answer hash and a submission scope hash")
	}
	for _, text := range []string{v.Feedback, v.Score, v.MaxScore} {
		if err := problem.ValidatePublicText(text); err != nil {
			return err
		}
	}
	// Never trust a caller's FullScore flag independently of the decimal values.
	verified := v.Confirmed && v.Passed && (v.PerQuestionCorrect || (v.FullScore && objectiveHasFullScore(v.Score, v.MaxScore)))
	v.ID, v.AnswerID, v.CreatedAt = 0, id, time.Now().UTC()
	_, err = s.db.ExecContext(ctx, "INSERT INTO objective_answer_verifications(answer_id,verified,evidence_json,created_at) VALUES(?,?,?,?)", id, verified, marshal(v), v.CreatedAt.Format(time.RFC3339Nano))
	return err
}

// LookupObjectiveAnswers preserves failed and unconfirmed candidates. Only
// Verified && Reusable answers are eligible for automatic cache reuse.
func (s *Store) LookupObjectiveAnswers(ctx context.Context, key string) ([]ObjectiveAnswer, error) {
	return s.lookupObjectiveAnswers(ctx, key, false)
}

// LookupObjectiveAnswersWithContext requires the v2 context-aware fingerprint
// and complete shared context before exposing any answer as reusable. It still
// returns validation history for diagnostics, but callers must use Reusable.
func (s *Store) LookupObjectiveAnswersWithContext(ctx context.Context, key string) ([]ObjectiveAnswer, error) {
	return s.lookupObjectiveAnswers(ctx, key, true)
}

func (s *Store) lookupObjectiveAnswers(ctx context.Context, key string, requireContext bool) ([]ObjectiveAnswer, error) {
	query := `SELECT a.id,a.question_key,a.answer,a.answer_hash,a.source,a.model,a.created_at,q.resource_complete,
 COALESCE((SELECT v.verified FROM objective_answer_verifications v WHERE v.answer_id=a.id ORDER BY v.id DESC LIMIT 1),0)
 FROM objective_answers a JOIN objective_questions q ON q.question_key=a.question_key
 WHERE a.question_key=?`
	if requireContext {
		query += ` AND q.context_complete=1 AND q.fingerprint_version>=2`
	}
	query += ` ORDER BY 9 DESC,a.id DESC`
	rows, err := s.db.QueryContext(ctx, query, key)
	if err != nil {
		return nil, err
	}
	var out []ObjectiveAnswer
	for rows.Next() {
		var a ObjectiveAnswer
		var stamp string
		if err = rows.Scan(&a.ID, &a.QuestionKey, &a.Answer, &a.AnswerHash, &a.Source, &a.Model, &stamp, &a.ResourceComplete, &a.Verified); err != nil {
			rows.Close()
			return nil, err
		}
		a.CreatedAt = parseTime(stamp)
		a.Reusable = a.Verified && a.ResourceComplete
		out = append(out, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	// Release rows before the second query: Store uses one SQLite connection.
	for i := range out {
		rows, err := s.db.QueryContext(ctx, "SELECT id,evidence_json FROM objective_answer_verifications WHERE answer_id=? ORDER BY id", out[i].ID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var v ObjectiveValidation
			var id int64
			var raw string
			if err = rows.Scan(&id, &raw); err != nil {
				break
			}
			if err = json.Unmarshal([]byte(raw), &v); err != nil {
				break
			}
			v.ID = id
			out[i].Validations = append(out[i].Validations, v)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return nil, fmt.Errorf("read objective validation: %w", err)
		}
	}
	if requireContext {
		aliases, err := s.lookupObjectiveLegacyAliases(ctx, key)
		if err != nil {
			return nil, err
		}
		positions := map[int64]int{}
		for i, a := range out {
			positions[a.ID] = i
		}
		for _, a := range aliases {
			if i, ok := positions[a.ID]; ok {
				out[i].Verified = out[i].Verified || a.Verified
				out[i].Reusable = out[i].Reusable || a.Reusable
				out[i].Validations = append(out[i].Validations, a.Validations...)
			} else {
				positions[a.ID] = len(out)
				out = append(out, a)
			}
		}
		out, err = s.withdrawObjectiveContextFailures(ctx, key, out)
		if err != nil {
			return nil, err
		}
	}
	return rejectObjectiveConflicts(out), nil
}
