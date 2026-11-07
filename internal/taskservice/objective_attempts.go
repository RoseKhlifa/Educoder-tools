package taskservice

import (
	"crypto/sha256"
	"encoding/json"
	"math/big"
	"sort"
	"strings"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/questionbank"
)

// objectiveScoreFeedback is a bounded, one-question-at-a-time search over a
// model's ranked alternatives.  It is intentionally kept in taskservice (and
// not in the provider contract): the platform score is the only evidence used
// to accept or reject a probe.  A changed score can identify the changed
// question only because every other answer is held constant.  Equal scores
// merely reject both alternatives; they never identify a hidden answer.
type objectiveScoreFeedback struct {
	base      answer.ObjectiveCandidate
	questions []objectiveScoreQuestion
	active    int
	baseline  *big.Rat
	maxScore  string
	pending   *objectiveScoreProbe
	done      bool
}

type objectiveScoreQuestion struct {
	index   int
	choices []string
	next    int
	locked  bool
}

type objectiveScoreProbe struct {
	question int
	previous answer.ObjectiveCandidate
	trial    answer.ObjectiveCandidate
	choice   int
}

// newObjectiveScoreFeedback only accepts ranked output whose first choice is
// exactly the materialized answer.  A plain answer vector has no ordered
// alternatives and must continue through the ordinary model repair path.
func newObjectiveScoreFeedback(snapshot answer.ObjectiveSnapshot, candidate answer.ObjectiveCandidate, fixed []string) (*objectiveScoreFeedback, bool) {
	if len(candidate.RankedAnswers) != len(snapshot.Questions) || len(candidate.Answers) != len(snapshot.Questions) {
		return nil, false
	}
	search := &objectiveScoreFeedback{base: candidate}
	for i, question := range snapshot.Questions {
		if len(fixed) == len(snapshot.Questions) && strings.TrimSpace(fixed[i]) != "" {
			continue
		}
		slot := candidate.RankedAnswers[i]
		if slot.QuestionPosition != question.Position || len(slot.Choices) < 2 {
			continue
		}
		choices := make([]string, 0, len(slot.Choices))
		seen := make(map[string]bool, len(slot.Choices))
		for _, raw := range slot.Choices {
			value := strings.ToUpper(strings.TrimSpace(raw))
			if seen[value] || value == "" || answer.ValidateObjectiveCandidate(snapshot, answer.ObjectiveCandidate{Kind: "objective", Answers: replaceObjectiveAnswer(candidate.Answers, i, value)}) != nil {
				return nil, false
			}
			seen[value] = true
			choices = append(choices, value)
		}
		if choices[0] != strings.ToUpper(strings.TrimSpace(candidate.Answers[i])) {
			return nil, false
		}
		search.questions = append(search.questions, objectiveScoreQuestion{index: i, choices: choices, next: 1})
	}
	if len(search.questions) == 0 {
		return nil, false
	}
	// Weakest first is deterministic when confidences are present; the supplied
	// ranked order remains the tie-breaker. Missing confidences retain roster
	// order rather than inventing a confidence value.
	order := append([]answer.ObjectiveRankedSlot(nil), candidate.RankedAnswers...)
	confidence := func(index int) (float64, bool) {
		if index < len(order) && len(order[index].Confidence) > 0 {
			return order[index].Confidence[0], true
		}
		return 0, false
	}
	sort.SliceStable(search.questions, func(i, j int) bool {
		left, leftOK := confidence(search.questions[i].index)
		right, rightOK := confidence(search.questions[j].index)
		if leftOK && rightOK && left != right {
			return left < right
		}
		// Missing confidence never invents a priority; retain the original
		// question order as the deterministic tie-break.
		return search.questions[i].index < search.questions[j].index
	})
	return search, true
}

func replaceObjectiveAnswer(answers []string, index int, value string) []string {
	copy := append([]string(nil), answers...)
	copy[index] = value
	return copy
}

func objectiveScoreNumber(raw string) (*big.Rat, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 128 {
		return nil, false
	}
	for _, ch := range raw {
		if (ch < '0' || ch > '9') && ch != '.' {
			return nil, false
		}
	}
	value, ok := new(big.Rat).SetString(raw)
	if !ok || value.Sign() < 0 {
		return nil, false
	}
	return value, true
}

// next returns the next candidate only after observe has classified the
// previous platform submission.  It never emits a candidate that changes two
// question slots at once.
func (s *objectiveScoreFeedback) next(snapshot answer.ObjectiveSnapshot) (answer.ObjectiveCandidate, bool) {
	if s == nil || s.done || s.pending != nil || s.baseline == nil {
		return answer.ObjectiveCandidate{}, false
	}
	for s.active < len(s.questions) {
		question := &s.questions[s.active]
		if question.locked || question.next >= len(question.choices) {
			s.active++
			continue
		}
		trial := s.base
		trial.Answers = replaceObjectiveAnswer(s.base.Answers, question.index, question.choices[question.next])
		if answer.ValidateObjectiveCandidate(snapshot, trial) != nil {
			return answer.ObjectiveCandidate{}, false
		}
		s.pending = &objectiveScoreProbe{question: question.index, previous: s.base, trial: trial, choice: question.next}
		return trial, true
	}
	s.done = true
	return answer.ObjectiveCandidate{}, false
}

// observe classifies a known score.  If the score or maximum is unavailable,
// the search is abandoned rather than guessing.  Equal scores advance to the
// next alternative for the same question; higher/lower scores lock the new or
// old answer respectively.
func objectiveAnswersEqual(left, right answer.ObjectiveCandidate) bool {
	if len(left.Answers) != len(right.Answers) {
		return false
	}
	for i := range left.Answers {
		if strings.ToUpper(strings.TrimSpace(left.Answers[i])) != strings.ToUpper(strings.TrimSpace(right.Answers[i])) {
			return false
		}
	}
	return true
}

// observe accepts score evidence only for the vector that was actually sent
// to the platform.  In particular, a repeated model candidate must never
// establish a baseline for a searcher that was created for another vector.
// Once a probe is pending, the response must belong to that exact probe; a
// mismatch abandons the search instead of inferring from an incomparable
// score.
func (s *objectiveScoreFeedback) observe(submitted answer.ObjectiveCandidate, score, max string) {
	if s == nil || s.done {
		return
	}
	value, ok := objectiveScoreNumber(score)
	if !ok || strings.TrimSpace(max) == "" {
		s.done = true
		s.pending = nil
		return
	}
	if s.baseline == nil {
		if !objectiveAnswersEqual(submitted, s.base) {
			s.done = true
			s.pending = nil
			return
		}
		s.baseline, _ = objectiveScoreNumber(score)
		s.maxScore = strings.TrimSpace(max)
		return
	}
	if strings.TrimSpace(max) != s.maxScore || s.pending == nil || !objectiveAnswersEqual(submitted, s.pending.trial) {
		s.done = true
		s.pending = nil
		return
	}
	probe := s.pending
	question := s.question(probe.question)
	if question == nil {
		s.done = true
		s.pending = nil
		return
	}
	switch value.Cmp(s.baseline) {
	case 1:
		s.base = probe.trial
		question.locked = true
		s.active++
		s.baseline = value
	case -1:
		question.locked = true
		s.active++
	case 0:
		question.next++
	}
	s.pending = nil
}

func (s *objectiveScoreFeedback) question(index int) *objectiveScoreQuestion {
	for i := range s.questions {
		if s.questions[i].index == index {
			return &s.questions[i]
		}
	}
	return nil
}

// objectiveAttemptHistory remembers complete, validated candidates whose
// platform score was confirmed as not full score, including bounded durable
// evidence for the same exact snapshot from earlier runs.
// It may derive a bounded next probe from a confirmed aggregate score. A probe
// changes one question only; this is deliberately deterministic and bounded,
// never an unconstrained whole-vector enumeration.
type objectiveAttemptHistory struct {
	limit    int
	byHash   map[[32]byte]objectiveAttempt
	attempts []objectiveAttempt
	prior    []objectiveAttempt
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
	if limit <= 0 || limit > 15 {
		limit = 15
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

// restore seeds only exact, validated whole-vector failures. Keeping previous
// from the latest observation avoids restarting repairs from an empty context;
// a better total score alone is never used to infer any individual answer.
func (h *objectiveAttemptHistory) restore(failed []questionbank.ObjectiveFailedAttempt) *answer.ObjectiveCandidate {
	if len(failed) > 15 {
		failed = failed[len(failed)-15:]
	}
	for _, item := range failed {
		candidate := item.Candidate.Candidate
		candidate.Answers = append([]string(nil), candidate.Answers...)
		h.prior = append(h.prior, objectiveAttempt{Candidate: candidate, Score: item.Score, MaxScore: item.MaxScore})
	}
	if len(h.prior) == 0 {
		return nil
	}
	previous := h.prior[len(h.prior)-1].Candidate
	previous.Answers = append([]string(nil), previous.Answers...)
	return &previous
}

func (h *objectiveAttemptHistory) contains(candidate answer.ObjectiveCandidate) bool {
	key := objectiveAttemptKey(candidate)
	if _, ok := h.byHash[key]; ok {
		return true
	}
	for _, prior := range h.prior {
		if objectiveAttemptKey(prior.Candidate) == key {
			return true
		}
	}
	return false
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
	if len(h.prior) == 0 && len(h.attempts) == 0 {
		return result
	}
	history := append(append([]objectiveAttempt(nil), h.prior...), h.attempts...)
	encoded, _ := json.Marshal(history)
	return append(result, answer.Feedback{
		Kind:    "formal_history",
		Message: "以下完整答案已经提交并确认未满分；相同完整答案不会再次提交。question_results 仅在完整响应与本次题目及答案精确对应时提供，correct=true 的答案应保留，其余答案请根据题干、选项和学科知识独立复核并修正。平台总分只用于系统进行有界的单题变更探测，不是无限枚举或模型猜测依据。已知尝试：" + string(encoded),
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
