package educoder

import (
	"encoding/json"
	"sort"
	"strings"

	"educoder-cli/internal/problem"
)

// UnmarshalJSON keeps objective read evidence separate from field values. In
// particular, an omitted task_pass is not the same as an explicit empty shared
// description, and an omitted question_type is not evidence of type zero.
func (t *Task) UnmarshalJSON(data []byte) error {
	type plain Task
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*t = Task(decoded)
	var wire struct {
		Challenge json.RawMessage   `json:"challenge"`
		Chooses   []json.RawMessage `json:"chooses"`
		TestCases json.RawMessage   `json:"choose_test_cases"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	t.choiceReadPresent = knownFields(data, "challenge", "chooses", "choose_test_cases") &&
		knownFields(wire.Challenge, "id", "subject", "task_pass") &&
		knownFields(wire.TestCases, "challenge_chooses_count")
	for _, raw := range wire.Chooses {
		if !knownFields(raw, "challenge_choose_id", "challenge_id", "position", "subject", "question_type", "category", "challenge_question") {
			t.choiceReadPresent = false
		}
		var question struct {
			Options []json.RawMessage `json:"challenge_question"`
		}
		if err := json.Unmarshal(raw, &question); err != nil {
			return err
		}
		for _, option := range question.Options {
			if !knownFields(option, "position", "option_name") {
				t.choiceReadPresent = false
			}
		}
	}
	return nil
}

// ObjectiveReadComplete reports whether this live JSON read includes the
// shared context and a complete, unambiguous advertised question roster. It is
// a bank-reuse gate, not an additional loader requirement: older protocol
// responses can still be loaded/diagnosed without being certified complete.
// Programmatically constructed Tasks carry no wire-presence evidence and
// deliberately return false. Resources need their own content-read check.
func (t *Task) ObjectiveReadComplete() bool {
	if t == nil || !t.choiceReadPresent || t.Kind() != TaskKindChoice || t.Challenge.ID <= 0 ||
		t.ChooseTestCases.Count <= 0 || t.ChooseTestCases.Count != len(t.Chooses) {
		return false
	}
	if strings.TrimSpace(t.Challenge.Subject) == "" || problem.ValidatePublicText(t.Challenge.Subject+t.Challenge.Description) != nil {
		return false
	}
	ids, positions := map[int64]bool{}, map[int]bool{}
	for _, question := range t.Chooses {
		if question.ChallengeChooseID <= 0 || ids[question.ChallengeChooseID] || question.ChallengeID != t.Challenge.ID ||
			question.Position < 1 || question.Position > len(t.Chooses) || positions[question.Position] ||
			strings.TrimSpace(question.Subject) == "" || question.Category <= 0 || question.QuestionType < 0 ||
			len(question.Options) < 2 || validateChoiceQuestion(question) != nil {
			return false
		}
		ids[question.ChallengeChooseID], positions[question.Position] = true, true
		options := map[int]bool{}
		for _, option := range question.Options {
			if option.Position >= len(question.Options) || options[option.Position] {
				return false
			}
			options[option.Position] = true
		}
	}
	return true
}

// ReadComplete also binds the execution's sorted Questions to the exact task
// read. A detached, truncated or edited question slice cannot inherit the
// underlying task's complete-read evidence.
func (t *ChoiceTask) ReadComplete() bool {
	if t == nil || t.Task == nil || !t.Task.ObjectiveReadComplete() ||
		t.TestCases.Count != t.Task.ChooseTestCases.Count || len(t.Questions) != len(t.Task.Chooses) {
		return false
	}
	questions := make(map[int64]ChoiceQuestion, len(t.Task.Chooses))
	for _, question := range t.Task.Chooses {
		questions[question.ChallengeChooseID] = question
	}
	for _, question := range t.Questions {
		original, ok := questions[question.ChallengeChooseID]
		if !ok || !sameReadChoiceQuestion(original, question) {
			return false
		}
		delete(questions, question.ChallengeChooseID)
	}
	return len(questions) == 0
}

func sameReadChoiceQuestion(a, b ChoiceQuestion) bool {
	if a.ChallengeChooseID != b.ChallengeChooseID || a.ChallengeID != b.ChallengeID || a.Position != b.Position ||
		a.QuestionName != b.QuestionName || a.QuestionType != b.QuestionType || a.Category != b.Category ||
		a.Subject != b.Subject || len(a.Options) != len(b.Options) {
		return false
	}
	a.Options, b.Options = append([]ChoiceOption(nil), a.Options...), append([]ChoiceOption(nil), b.Options...)
	sort.Slice(a.Options, func(i, j int) bool { return a.Options[i].Position < a.Options[j].Position })
	sort.Slice(b.Options, func(i, j int) bool { return b.Options[i].Position < b.Options[j].Position })
	for i := range a.Options {
		if a.Options[i] != b.Options[i] {
			return false
		}
	}
	return true
}
