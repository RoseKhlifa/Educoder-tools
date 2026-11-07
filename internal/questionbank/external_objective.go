package questionbank

import (
	"context"
	"encoding/json"
	"fmt"
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
	_, ok := normalizeExternalChoiceReference(ref)
	return ok
}

// normalizeExternalSelected accepts both the canonical form ("A", "B") and
// the compact form emitted by older inventories ("AB" or "ABD"). Compact
// strings are expanded into letters, then sorted and de-duplicated so the
// stored representation is deterministic. Invalid characters are rejected.
func normalizeExternalSelected(selected []string) ([]string, bool) {
	if len(selected) == 0 {
		return nil, false
	}
	seen := make(map[byte]bool, 26)
	for _, raw := range selected {
		raw = strings.ToUpper(strings.TrimSpace(raw))
		if raw == "" {
			return nil, false
		}
		for i := 0; i < len(raw); i++ {
			if raw[i] < 'A' || raw[i] > 'Z' {
				return nil, false
			}
			seen[raw[i]] = true
		}
		if len(seen) > 26 {
			return nil, false
		}
	}
	letters := make([]string, 0, len(seen))
	for letter := byte('A'); letter <= 'Z'; letter++ {
		if seen[letter] {
			letters = append(letters, string(letter))
		}
	}
	return letters, len(letters) > 0
}

func normalizeExternalChoiceReference(ref externalChoiceReference) (externalChoiceReference, bool) {
	if len(ref.Answer) == 0 || len(ref.Answer) > 256 {
		return externalChoiceReference{}, false
	}
	seen := make(map[int]bool, len(ref.Answer))
	for i := range ref.Answer {
		slot := &ref.Answer[i]
		if slot.Number <= 0 || seen[slot.Number] || slot.Category < 0 {
			return externalChoiceReference{}, false
		}
		letters, ok := normalizeExternalSelected(slot.Selected)
		if !ok || slot.Category == 1 && len(letters) != 1 {
			return externalChoiceReference{}, false
		}
		slot.Selected = letters
		seen[slot.Number] = true
	}
	return ref, true
}

func externalObjectiveVector(raw json.RawMessage, snapshot answer.ObjectiveSnapshot) (answer.ObjectiveCandidate, bool) {
	var ref externalChoiceReference
	if json.Unmarshal(raw, &ref) != nil || len(ref.Answer) != len(snapshot.Questions) || len(ref.Answer) == 0 {
		return answer.ObjectiveCandidate{}, false
	}
	var normalized bool
	ref, normalized = normalizeExternalChoiceReference(ref)
	if !normalized {
		return answer.ObjectiveCandidate{}, false
	}
	byPosition := map[int]externalChoiceSlot{}
	for _, slot := range ref.Answer {
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
		c.Answers = append(c.Answers, strings.Join(slot.Selected, ""))
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
