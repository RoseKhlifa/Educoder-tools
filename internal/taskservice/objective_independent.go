package taskservice

import (
	"errors"
	"strings"

	"educoder-cli/internal/answer"
)

// objectiveIndependentReview gives every unresolved question one clean model
// context before exposing a new complete vector for submission. A failed score
// or prior answer is deliberately not part of an individual review request.
// The caller owns call/cost limits and permits at most one sweep per run.
type objectiveIndependentReview struct {
	snapshot  answer.ObjectiveSnapshot
	resources []answer.ImageResource
	answers   []string
	queue     []int
	index     int
	remaining int
	issued    bool
}

func newObjectiveIndependentReview(snapshot answer.ObjectiveSnapshot, resources []answer.ImageResource, previous *answer.ObjectiveCandidate, fixed []string) (*objectiveIndependentReview, error) {
	if previous == nil {
		return nil, errors.New("independent objective review requires a complete previous candidate")
	}
	if err := answer.ValidateObjectiveCandidate(snapshot, *previous); err != nil {
		return nil, err
	}
	if err := answer.ValidateObjectiveFixedAnswers(snapshot, fixed); err != nil {
		return nil, err
	}
	// Reuse the strict fixed-answer completeness checks for every question,
	// including unresolved ones. A clean review must not guess from a missing
	// option, duplicate position or blank option text.
	complete := make([]string, len(snapshot.Questions))
	positions := make(map[int]bool, len(snapshot.Questions))
	for i, question := range snapshot.Questions {
		if positions[question.Position] {
			return nil, errors.New("independent objective review has duplicate question positions")
		}
		positions[question.Position] = true
		complete[i] = strings.ToUpper(strings.TrimSpace(question.Options[0].Label))
	}
	if err := answer.ValidateObjectiveFixedAnswers(snapshot, complete); err != nil {
		return nil, err
	}
	if err := answer.ValidateObjectiveResources(snapshot, resources); err != nil {
		return nil, err
	}
	s := &objectiveIndependentReview{
		snapshot:  cloneIndependentSnapshot(snapshot),
		resources: cloneIndependentResources(resources),
		answers:   append([]string(nil), previous.Answers...),
	}
	for i := range snapshot.Questions {
		if len(fixed) != 0 && strings.TrimSpace(fixed[i]) != "" {
			s.answers[i] = strings.ToUpper(strings.TrimSpace(fixed[i]))
			continue
		}
		s.queue = append(s.queue, i)
	}
	s.remaining = len(s.queue)
	return s, nil
}

// nextRequest keeps original question positions, the entire original context,
// and all image bindings, including sibling questions referenced by the target.
// It never exposes previous answers, scored history or legacy references.
func (s *objectiveIndependentReview) nextRequest(profile answer.Profile) (answer.ObjectiveRequest, bool) {
	if s == nil || s.remaining == 0 {
		return answer.ObjectiveRequest{}, false
	}
	s.issued = true
	return s.request(s.queue[s.index], profile), true
}

// requests returns independent copies of every remaining request for a budget
// preflight. Estimation must not issue a request or advance the review queue.
func (s *objectiveIndependentReview) requests(profile answer.Profile) []answer.ObjectiveRequest {
	if s == nil || s.remaining == 0 {
		return nil
	}
	requests := make([]answer.ObjectiveRequest, 0, s.remaining)
	for _, index := range s.queue[s.index:] {
		requests = append(requests, s.request(index, profile))
	}
	return requests
}

func (s *objectiveIndependentReview) request(index int, profile answer.Profile) answer.ObjectiveRequest {
	question := s.snapshot.Questions[index]
	question.Options = append([]answer.ObjectiveOption(nil), question.Options...)
	context := cloneIndependentSnapshot(s.snapshot)
	return answer.ObjectiveRequest{
		Problem: answer.ObjectiveSnapshot{
			Kind: s.snapshot.Kind, Title: s.snapshot.Title, Statement: s.snapshot.Statement,
			Questions: []answer.ObjectiveQuestion{question},
		},
		Context:   &context,
		Profile:   profile,
		Resources: cloneIndependentResources(s.resources),
	}
}

// accept advances only after strict one-question validation. No partial mixed
// vector escapes this helper: callers receive it only when the sweep is done.
func (s *objectiveIndependentReview) accept(candidate answer.ObjectiveCandidate) (answer.ObjectiveCandidate, bool, error) {
	if s == nil || s.remaining == 0 || !s.issued {
		return answer.ObjectiveCandidate{}, false, errors.New("no pending independent objective review")
	}
	index := s.queue[s.index]
	scoped := answer.ObjectiveSnapshot{Kind: s.snapshot.Kind, Title: s.snapshot.Title, Statement: s.snapshot.Statement, Questions: []answer.ObjectiveQuestion{s.snapshot.Questions[index]}}
	if err := answer.ValidateObjectiveCandidate(scoped, candidate); err != nil {
		return answer.ObjectiveCandidate{}, false, err
	}
	s.answers[index] = strings.ToUpper(strings.TrimSpace(candidate.Answers[0]))
	s.index++
	s.remaining--
	s.issued = false
	if s.remaining != 0 {
		return answer.ObjectiveCandidate{}, false, nil
	}
	full := answer.ObjectiveCandidate{Kind: "objective", Answers: append([]string(nil), s.answers...)}
	if err := answer.ValidateObjectiveCandidate(s.snapshot, full); err != nil {
		return answer.ObjectiveCandidate{}, false, err
	}
	return full, true, nil
}

func cloneIndependentSnapshot(snapshot answer.ObjectiveSnapshot) answer.ObjectiveSnapshot {
	copy := snapshot
	copy.Questions = append([]answer.ObjectiveQuestion(nil), snapshot.Questions...)
	for i := range copy.Questions {
		copy.Questions[i].Options = append([]answer.ObjectiveOption(nil), snapshot.Questions[i].Options...)
	}
	return copy
}

// Clone the complete resource set so a sibling stem/option referenced by the
// selected question remains available without exposing mutable input slices.
func cloneIndependentResources(resources []answer.ImageResource) []answer.ImageResource {
	var out []answer.ImageResource
	for _, resource := range resources {
		var bindings []answer.ImageBinding
		for _, binding := range resource.EffectiveBindings() {
			bindings = append(bindings, binding)
		}
		if len(bindings) == 0 {
			continue
		}
		copy := resource
		copy.Content = append([]byte(nil), resource.Content...)
		copy.Bindings = bindings
		copy.QuestionPosition, copy.OptionLabel = bindings[0].QuestionPosition, bindings[0].OptionLabel
		out = append(out, copy)
	}
	return out
}
