package questionbank

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"educoder-cli/internal/answer"
)

// External choices are proposals, never verified locks. Original imports that
// predate executable choice support remain intact and can be bound on demand.
type externalChoiceReference struct {
	Answer []externalChoiceSlot `json:"answer"`
}
type externalChoiceSlot struct {
	Number       int      `json:"number"`
	Selected     []string `json:"selected"`
	Category     int      `json:"category,omitempty"`
	QuestionType *int     `json:"question_type,omitempty"`
}

func validExternalChoiceReference(ref externalChoiceReference) bool {
	if len(ref.Answer) == 0 || len(ref.Answer) > 256 {
		return false
	}
	seen := map[int]bool{}
	for _, slot := range ref.Answer {
		if slot.Number <= 0 || seen[slot.Number] || len(slot.Selected) == 0 || len(slot.Selected) > 26 || slot.Category < 0 || slot.Category == 1 && len(slot.Selected) != 1 {
			return false
		}
		seen[slot.Number] = true
		labels := map[string]bool{}
		for _, v := range slot.Selected {
			v = strings.ToUpper(strings.TrimSpace(v))
			if len(v) != 1 || v[0] < 'A' || v[0] > 'Z' || labels[v] {
				return false
			}
			labels[v] = true
		}
	}
	return true
}

func externalObjectiveVector(raw json.RawMessage, snapshot answer.ObjectiveSnapshot) (answer.ObjectiveCandidate, bool) {
	var ref externalChoiceReference
	if json.Unmarshal(raw, &ref) != nil || len(ref.Answer) != len(snapshot.Questions) || len(ref.Answer) == 0 {
		return answer.ObjectiveCandidate{}, false
	}
	byPosition := map[int]externalChoiceSlot{}
	for _, slot := range ref.Answer {
		if slot.Number <= 0 || len(slot.Selected) == 0 || len(slot.Selected) > 26 {
			return answer.ObjectiveCandidate{}, false
		}
		if _, dup := byPosition[slot.Number]; dup {
			return answer.ObjectiveCandidate{}, false
		}
		byPosition[slot.Number] = slot
	}
	c := answer.ObjectiveCandidate{Kind: "objective"}
	for _, q := range snapshot.Questions {
		slot, ok := byPosition[q.Position]
		if !ok || len(q.Options) < 2 || strings.TrimSpace(q.Question) == "" || (q.Category != 1 && q.Category != 2) || slot.Category > 0 && slot.Category != q.Category || slot.QuestionType != nil && *slot.QuestionType != q.QuestionType {
			return answer.ObjectiveCandidate{}, false
		}
		delete(byPosition, q.Position)
		letters := append([]string(nil), slot.Selected...)
		for i, l := range letters {
			l = strings.ToUpper(strings.TrimSpace(l))
			if len(l) != 1 || l[0] < 'A' || l[0] > 'Z' {
				return answer.ObjectiveCandidate{}, false
			}
			letters[i] = l
		}
		sort.Strings(letters)
		for i := 1; i < len(letters); i++ {
			if letters[i] == letters[i-1] {
				return answer.ObjectiveCandidate{}, false
			}
		}
		c.Answers = append(c.Answers, strings.Join(letters, ""))
	}
	return c, len(byPosition) == 0 && answer.ValidateObjectiveCandidate(snapshot, c) == nil && answer.ValidateObjectiveFixedAnswers(snapshot, c.Answers) == nil
}

// FindExternalObjectiveCandidate binds an exact experiment/title/roster shape
// to the CURRENT snapshot. A positional source lacks stem identity, so the
// result remains unverified until normal platform grading proves it. Conflicting
// sources do not select an arbitrary winner. Missing/malformed fields never match.
func (s *Store) FindExternalObjectiveCandidate(ctx context.Context, identity ExternalIdentity, snapshot answer.ObjectiveSnapshot) (ObjectiveCandidate, error) {
	identity.Experiment = normalizeExternalIdentity(identity.Experiment)
	identity.Title = normalizeExternalIdentity(identity.Title)
	if identity.Experiment == "" || identity.Title == "" || answer.ValidateObjectiveSnapshot(snapshot) != nil {
		return ObjectiveCandidate{}, ErrNotFound
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+externalColumns+` FROM external_entries WHERE experiment=? AND title=? AND kind='objective' AND (status='candidate' OR (status='reference' AND reason='missing_question_and_option_identity')) ORDER BY id`, identity.Experiment, identity.Title)
	if err != nil {
		return ObjectiveCandidate{}, err
	}
	var matches []ExternalEntry
	var chosen answer.ObjectiveCandidate
	for rows.Next() {
		e, eErr := scanExternal(rows)
		if eErr != nil {
			rows.Close()
			return ObjectiveCandidate{}, eErr
		}
		if e.Position > 0 && identity.Position > 0 && e.Position != identity.Position {
			continue
		}
		c, ok := externalObjectiveVector(e.Reference, snapshot)
		if !ok {
			continue
		}
		if len(matches) > 0 && strings.Join(c.Answers, "\x00") != strings.Join(chosen.Answers, "\x00") {
			rows.Close()
			return ObjectiveCandidate{}, ErrNotFound
		}
		matches = append(matches, e)
		chosen = c
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return ObjectiveCandidate{}, err
	}
	if len(matches) == 0 {
		return ObjectiveCandidate{}, ErrNotFound
	}
	key, err := s.PutObjectiveSnapshot(ctx, snapshot)
	if err != nil {
		return ObjectiveCandidate{}, err
	}
	e := matches[0]
	c, err := s.AddObjectiveCandidate(ctx, key, snapshot, chosen, fmt.Sprintf("external:%s@%s:%d", e.Repository, e.Commit, e.ID))
	if err != nil {
		return c, err
	}
	for _, e := range matches {
		if _, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO external_objective_bindings(candidate_id,external_entry_id,created_at) VALUES(?,?,?)`, c.ID, e.ID, now()); err != nil {
			return c, err
		}
	}
	return c, nil
}
