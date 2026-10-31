package taskservice

import (
	"crypto/sha256"
	"encoding/json"
	"strings"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/educoder"
)

// objectiveAttemptHistory belongs to one run only. It remembers complete,
// validated candidates whose platform score was confirmed as not full score.
// It never derives individual answers from score changes or enumerates options.
type objectiveAttemptHistory struct {
	limit    int
	byHash   map[[32]byte]objectiveAttempt
	attempts []objectiveAttempt
}

type objectiveAttempt struct {
	Candidate       answer.ObjectiveCandidate `json:"candidate"`
	Score           string                    `json:"score"`
	MaxScore        string                    `json:"max_score"`
	QuestionResults []objectiveQuestionResult `json:"question_results,omitempty"`
}

type objectiveQuestionResult struct {
	Position int    `json:"position"`
	Answer   string `json:"answer"`
	Correct  bool   `json:"correct"`
}

func newObjectiveAttemptHistory(limit int) *objectiveAttemptHistory {
	if limit <= 0 || limit > 10 {
		limit = 10
	}
	return &objectiveAttemptHistory{limit: limit, byHash: make(map[[32]byte]objectiveAttempt)}
}

func objectiveAttemptKey(candidate answer.ObjectiveCandidate) [32]byte {
	answers := make([]string, len(candidate.Answers))
	for i, value := range candidate.Answers {
		answers[i] = strings.ToUpper(strings.TrimSpace(value))
	}
	// Encoding the entire ordered vector keeps question boundaries intact.
	encoded, _ := json.Marshal(answers)
	return sha256.Sum256(encoded)
}

func (h *objectiveAttemptHistory) contains(candidate answer.ObjectiveCandidate) bool {
	_, ok := h.byHash[objectiveAttemptKey(candidate)]
	return ok
}

func (h *objectiveAttemptHistory) remember(candidate answer.ObjectiveCandidate, score, maxScore string, results ...objectiveQuestionResult) {
	key := objectiveAttemptKey(candidate)
	if _, ok := h.byHash[key]; ok || len(h.attempts) >= h.limit {
		return
	}
	copy := candidate
	copy.Answers = append([]string(nil), candidate.Answers...)
	attempt := objectiveAttempt{Candidate: copy, Score: score, MaxScore: maxScore, QuestionResults: append([]objectiveQuestionResult(nil), results...)}
	h.byHash[key] = attempt
	h.attempts = append(h.attempts, attempt)
}

// feedback is used only in the provider request, never in Result, Progress,
// diagnostics or public HTTP output. The score is observation data, not an
// invitation to search answer combinations against the platform scorer.
func (h *objectiveAttemptHistory) feedback(current []answer.Feedback) []answer.Feedback {
	result := append([]answer.Feedback(nil), current...)
	if len(h.attempts) == 0 {
		return result
	}
	encoded, _ := json.Marshal(h.attempts)
	return append(result, answer.Feedback{
		Kind:    "formal_history",
		Message: "以下完整答案已经提交并确认未满分；相同完整答案不会再次提交。question_results 仅在完整响应与本次题目及答案精确对应时提供，correct=true 的答案应保留，其余答案请根据题干、选项和学科知识独立复核并修正。不要枚举选项或把平台得分当成答案搜索器。已知尝试：" + string(encoded),
	})
}

// trustedObjectiveQuestionResults accepts only a complete, unambiguous result
// roster that proves it belongs to the just-submitted ordered answer vector.
// Neither a total score nor a hidden standard_answer is used to infer answers.
func trustedObjectiveQuestionResults(task *educoder.ChoiceTask, candidate answer.ObjectiveCandidate, submitted *educoder.ChoiceSubmitResult) []objectiveQuestionResult {
	if task == nil || submitted == nil || len(task.Questions) == 0 || len(task.Questions) > 256 || len(candidate.Answers) != len(task.Questions) || submitted.Count != len(task.Questions) || len(submitted.TestSets) != len(task.Questions) {
		return nil
	}
	byPosition := make(map[int]educoder.ChoiceTestSet, len(submitted.TestSets))
	for _, result := range submitted.TestSets {
		if result.Position <= 0 || result.Result == nil || strings.TrimSpace(result.QuestionName) == "" {
			return nil
		}
		if _, duplicate := byPosition[result.Position]; duplicate {
			return nil
		}
		byPosition[result.Position] = result
	}
	results := make([]objectiveQuestionResult, 0, len(task.Questions))
	correct := 0
	for index, question := range task.Questions {
		result, ok := byPosition[question.Position]
		if !ok || result.QuestionName != question.QuestionName || result.QuestionType != question.QuestionType {
			return nil
		}
		delete(byPosition, question.Position)
		actual, ok := result.ActualOutput.(string)
		if !ok {
			return nil
		}
		actual, actualErr := educoder.CanonicalizeChoiceAnswer(question, actual)
		expected, expectedErr := educoder.CanonicalizeChoiceAnswer(question, candidate.Answers[index])
		if actualErr != nil || expectedErr != nil || actual != expected {
			return nil
		}
		results = append(results, objectiveQuestionResult{Position: question.Position, Answer: expected, Correct: *result.Result})
		if *result.Result {
			correct++
		}
	}
	if len(byPosition) != 0 || (submitted.Correct != nil && *submitted.Correct != correct) {
		return nil
	}
	return results
}
