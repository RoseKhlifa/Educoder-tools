package educoder

// This file contains the observed objective-question protocol. It is kept
// separate from programming.go so choice tasks cannot reach code-editor,
// WebSocket, or compiler endpoints.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"educoder-cli/internal/problem"
)

type ChoiceOption struct {
	OptionName string `json:"option_name"`
	Position   int    `json:"position"`
}

type ChoiceQuestion struct {
	ChallengeChooseID int64          `json:"challenge_choose_id"`
	ChallengeID       int64          `json:"challenge_id"`
	Position          int            `json:"position"`
	QuestionName      string         `json:"question_name"`
	QuestionType      int            `json:"question_type"`
	Category          int            `json:"category"`
	Subject           string         `json:"subject"`
	Options           []ChoiceOption `json:"challenge_question"`
}

type ChoiceTestSet struct {
	QuestionName   string `json:"question_name"`
	QuestionType   int    `json:"question_type"`
	Position       int    `json:"position"`
	ActualOutput   any    `json:"actual_output"`
	Result         *bool  `json:"result"`
	StandardAnswer any    `json:"standard_answer"`
}

type ChoiceTestCases struct {
	Count        int             `json:"challenge_chooses_count"`
	Correct      *int            `json:"choose_correct_num"`
	HadSubmit    bool            `json:"had_submmit"`
	HadAllSubmit bool            `json:"had_all_submmit"`
	TestSets     []ChoiceTestSet `json:"test_sets"`
}

type ChoiceTask struct {
	Kind      TaskKind         `json:"kind"`
	Task      *Task            `json:"task"`
	Questions []ChoiceQuestion `json:"questions"`
	TestCases ChoiceTestCases  `json:"test_cases"`
}

func requireChoice(t *Task) error {
	if t == nil || t.Kind() != TaskKindChoice {
		return errors.New("题型不是已观察的选择题")
	}
	if t.HomeworkID <= 0 || validIdentifier(t.Game.Identifier) != nil || t.Challenge.ID <= 0 {
		return errors.New("选择题缺少关卡或作业上下文")
	}
	return nil
}

func (c *Client) LoadChoiceTask(ctx context.Context, course string, homeworkID int64, game string) (*ChoiceTask, error) {
	t, err := c.LoadTask(ctx, course, homeworkID, game)
	if err != nil {
		return nil, err
	}
	if err := requireChoice(t); err != nil {
		return nil, err
	}
	if len(t.Chooses) == 0 {
		return nil, errors.New("选择题响应缺少 chooses")
	}
	qs := append([]ChoiceQuestion(nil), t.Chooses...)
	sort.Slice(qs, func(i, j int) bool { return qs[i].Position < qs[j].Position })
	for i := range qs {
		if qs[i].Position <= 0 || qs[i].Subject == "" || len(qs[i].Options) == 0 {
			return nil, errors.New("选择题题干或选项不完整")
		}
		if err := validateChoiceQuestion(qs[i]); err != nil {
			return nil, err
		}
	}
	return &ChoiceTask{Kind: TaskKindChoice, Task: t, Questions: qs, TestCases: t.ChooseTestCases}, nil
}

func validateChoiceQuestion(q ChoiceQuestion) error {
	if err := problem.ValidatePublicText(q.Subject); err != nil {
		return errors.New("选择题题干无效")
	}
	seen := map[int]bool{}
	for _, o := range q.Options {
		if o.Position < 0 || o.Position >= 26 || seen[o.Position] || strings.TrimSpace(o.OptionName) == "" {
			return errors.New("选择题选项无效")
		}
		if err := problem.ValidatePublicText(o.OptionName); err != nil {
			return errors.New("选择题选项无效")
		}
		seen[o.Position] = true
	}
	return nil
}

func choiceLetter(position int) byte { return byte('A' + position) }

// CanonicalizeChoiceAnswer accepts the wire answer format and produces the
// exact letter string expected by choose_build.json.
func CanonicalizeChoiceAnswer(q ChoiceQuestion, raw string) (string, error) {
	if err := validateChoiceQuestion(q); err != nil {
		return "", err
	}
	raw = strings.TrimSpace(raw)
	if !utf8.ValidString(raw) || raw == "" {
		return "", errors.New("选择题答案为空")
	}
	if len(q.Options) == 0 {
		return "", errors.New("选择题没有选项")
	}
	valid := map[byte]bool{}
	for _, o := range q.Options {
		valid[choiceLetter(o.Position)] = true
	}
	letters := make([]byte, 0, len(raw))
	for _, r := range strings.ToUpper(raw) {
		if r == ',' || r == '，' || r == ';' || r == '；' || r == ' ' || r == '\t' {
			continue
		}
		if r < 'A' || r > 'Z' || !valid[byte(r)] {
			return "", errors.New("选择题答案必须是已有选项字母")
		}
		letters = append(letters, byte(r))
	}
	if len(letters) == 0 {
		return "", errors.New("选择题答案为空")
	}
	if q.Category == 1 && len(letters) != 1 {
		return "", errors.New("单选题必须只有一个答案")
	}
	sort.Slice(letters, func(i, j int) bool { return letters[i] < letters[j] })
	out := letters[:0]
	for _, letter := range letters {
		if len(out) == 0 || out[len(out)-1] != letter {
			out = append(out, letter)
		}
	}
	if q.Category == 1 && len(out) != 1 {
		return "", errors.New("单选题答案重复")
	}
	return string(out), nil
}

type ChoiceSubmitResult struct {
	Status             int             `json:"status"`
	Correct            *int            `json:"choose_correct_num"`
	Count              int             `json:"challenge_chooses_count"`
	AnswerOpen         any             `json:"answer_open"`
	GameOriginalStatus int             `json:"game_original_status"`
	NextGame           *string         `json:"next_game"`
	TestSets           []ChoiceTestSet `json:"test_sets"`
	Passed             bool            `json:"passed"`
}

// ObjectiveSubmit sends only the observed choose_build mutation. It never
// calls any programming endpoint and preserves answer order by question
// position.
func (c *Client) ObjectiveSubmit(ctx context.Context, task *ChoiceTask, answers []string) (*ChoiceSubmitResult, error) {
	if task == nil || task.Task == nil {
		return nil, errors.New("选择题上下文为空")
	}
	if err := requireChoice(task.Task); err != nil {
		return nil, err
	}
	if len(answers) != len(task.Questions) {
		return nil, errors.New("答案数量与选择题数量不一致")
	}
	canonical := make([]string, len(answers))
	for i := range answers {
		v, err := CanonicalizeChoiceAnswer(task.Questions[i], answers[i])
		if err != nil {
			return nil, fmt.Errorf("第%d题答案无效: %w", i+1, err)
		}
		canonical[i] = v
	}
	body := map[string]any{"answer": canonical, "challenge_id": task.Task.Challenge.ID, "subject_id": "", "ip": "", "exercise_id": "", "competition_entry_id": "", "smart_plan_page_item_bank_id": ""}
	query := url.Values{"homework_common_id": {strconv.FormatInt(task.Task.HomeworkID, 10)}}
	var response ChoiceSubmitResult
	if err := c.requestWithStatuses(ctx, http.MethodPost, "/api/tasks/"+task.Task.Game.Identifier+"/choose_build.json", query, body, taskRef(task.Task), &response, 0, 1, 2); err != nil {
		return nil, err
	}
	response.Passed = response.Count > 0 && ((response.Correct != nil && *response.Correct == response.Count) || (response.Correct == nil && response.GameOriginalStatus == 2))
	return &response, nil
}
