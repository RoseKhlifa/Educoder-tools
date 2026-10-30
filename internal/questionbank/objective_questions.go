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
	rows, err := s.db.QueryContext(ctx, `SELECT a.id,a.question_key,a.answer,a.answer_hash,a.source,a.model,a.created_at,q.resource_complete,
 EXISTS(SELECT 1 FROM objective_answer_verifications v WHERE v.answer_id=a.id AND v.verified=1)
 FROM objective_answers a JOIN objective_questions q ON q.question_key=a.question_key
 WHERE a.question_key=? ORDER BY 9 DESC,a.id DESC`, key)
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
	return out, nil
}
