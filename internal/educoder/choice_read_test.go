package educoder

import (
	"encoding/json"
	"testing"
)

func completeChoiceReadFixture() map[string]any {
	fixture := objectiveFixture()
	challenge := fixture["challenge"].(map[string]any)
	challenge["subject"], challenge["task_pass"] = "完整的选择题关卡", ""
	return fixture
}

func decodeChoiceReadFixture(t *testing.T, fixture map[string]any) *Task {
	t.Helper()
	data, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	var task Task
	if err = json.Unmarshal(data, &task); err != nil {
		t.Fatal(err)
	}
	return &task
}

func TestObjectiveReadCompleteDistinguishesMissingAndExplicitZeroValues(t *testing.T) {
	complete := decodeChoiceReadFixture(t, completeChoiceReadFixture())
	if !complete.ObjectiveReadComplete() {
		t.Fatal("explicit empty shared description and question_type=0 must remain complete")
	}
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"title absent", func(f map[string]any) { delete(f["challenge"].(map[string]any), "subject") }},
		{"description absent", func(f map[string]any) { delete(f["challenge"].(map[string]any), "task_pass") }},
		{"description null", func(f map[string]any) { f["challenge"].(map[string]any)["task_pass"] = nil }},
		{"roster absent", func(f map[string]any) { delete(f, "chooses") }},
		{"count absent", func(f map[string]any) { delete(f["choose_test_cases"].(map[string]any), "challenge_chooses_count") }},
		{"count null", func(f map[string]any) { f["choose_test_cases"].(map[string]any)["challenge_chooses_count"] = nil }},
		{"count zero", func(f map[string]any) { f["choose_test_cases"].(map[string]any)["challenge_chooses_count"] = 0 }},
		{"question type absent", func(f map[string]any) { delete(f["chooses"].([]any)[0].(map[string]any), "question_type") }},
		{"question identity absent", func(f map[string]any) { delete(f["chooses"].([]any)[0].(map[string]any), "challenge_choose_id") }},
		{"question position absent", func(f map[string]any) { delete(f["chooses"].([]any)[0].(map[string]any), "position") }},
		{"question subject absent", func(f map[string]any) { delete(f["chooses"].([]any)[0].(map[string]any), "subject") }},
		{"question category absent", func(f map[string]any) { delete(f["chooses"].([]any)[0].(map[string]any), "category") }},
		{"option list absent", func(f map[string]any) { delete(f["chooses"].([]any)[0].(map[string]any), "challenge_question") }},
		{"option position absent", func(f map[string]any) {
			delete(f["chooses"].([]any)[0].(map[string]any)["challenge_question"].([]any)[0].(map[string]any), "position")
		}},
		{"option name absent", func(f map[string]any) {
			delete(f["chooses"].([]any)[0].(map[string]any)["challenge_question"].([]any)[0].(map[string]any), "option_name")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := completeChoiceReadFixture()
			tc.change(fixture)
			if decodeChoiceReadFixture(t, fixture).ObjectiveReadComplete() {
				t.Fatal("missing upstream field was certified complete")
			}
		})
	}
}

func TestObjectiveReadCompleteChecksRosterAndOptions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"truncated roster", func(f map[string]any) { f["chooses"] = f["chooses"].([]any)[:1] }},
		{"duplicate identity", func(f map[string]any) { f["chooses"].([]any)[1].(map[string]any)["challenge_choose_id"] = 10 }},
		{"wrong challenge", func(f map[string]any) { f["chooses"].([]any)[0].(map[string]any)["challenge_id"] = 999 }},
		{"duplicate position", func(f map[string]any) { f["chooses"].([]any)[1].(map[string]any)["position"] = 1 }},
		{"position gap", func(f map[string]any) { f["chooses"].([]any)[1].(map[string]any)["position"] = 3 }},
		{"blank subject", func(f map[string]any) { f["chooses"].([]any)[0].(map[string]any)["subject"] = " " }},
		{"truncated single option", func(f map[string]any) {
			q := f["chooses"].([]any)[0].(map[string]any)
			q["challenge_question"] = q["challenge_question"].([]any)[:1]
		}},
		{"option position gap", func(f map[string]any) {
			f["chooses"].([]any)[0].(map[string]any)["challenge_question"].([]any)[1].(map[string]any)["position"] = 2
		}},
		{"duplicate option", func(f map[string]any) {
			f["chooses"].([]any)[0].(map[string]any)["challenge_question"].([]any)[1].(map[string]any)["position"] = 0
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := completeChoiceReadFixture()
			tc.change(fixture)
			if decodeChoiceReadFixture(t, fixture).ObjectiveReadComplete() {
				t.Fatal("ambiguous or incomplete roster/options were certified complete")
			}
		})
	}
}

func TestChoiceReadCompleteAcceptsShuffledWireOrderButNotDetachedQuestionEdits(t *testing.T) {
	fixture := completeChoiceReadFixture()
	questions := fixture["chooses"].([]any)
	questions[0], questions[1] = questions[1], questions[0]
	options := questions[0].(map[string]any)["challenge_question"].([]any)
	options[0], options[2] = options[2], options[0]
	task := decodeChoiceReadFixture(t, fixture)
	choice := &ChoiceTask{Kind: TaskKindChoice, Task: task, Questions: append([]ChoiceQuestion(nil), task.Chooses...), TestCases: task.ChooseTestCases}
	choice.Questions[0], choice.Questions[1] = choice.Questions[1], choice.Questions[0]
	if !task.ObjectiveReadComplete() || !choice.ReadComplete() {
		t.Fatal("position-authoritative shuffled roster should remain complete")
	}
	choice.Questions[0].Subject = "detached changed question"
	if choice.ReadComplete() {
		t.Fatal("edited execution slice inherited original complete-read evidence")
	}
	choice.Questions = choice.Questions[:1]
	if choice.ReadComplete() {
		t.Fatal("truncated execution slice inherited original complete-read evidence")
	}
}

func TestObjectiveReadCompletenessEvidenceIsNotInferredOrSticky(t *testing.T) {
	task := decodeChoiceReadFixture(t, completeChoiceReadFixture())
	manual := *task
	manual.choiceReadPresent = false
	if manual.ObjectiveReadComplete() {
		t.Fatal("programmatic fields must not manufacture wire-presence evidence")
	}
	fixture := completeChoiceReadFixture()
	delete(fixture["challenge"].(map[string]any), "task_pass")
	data, _ := json.Marshal(fixture)
	if err := json.Unmarshal(data, task); err != nil {
		t.Fatal(err)
	}
	if task.ObjectiveReadComplete() {
		t.Fatal("re-decoding a partial response retained earlier completeness")
	}
}
