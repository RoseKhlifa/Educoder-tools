package answer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func objectiveFixture() ObjectiveSnapshot {
	return ObjectiveSnapshot{Kind: "objective", Title: "local quiz", Statement: "choose answers", Questions: []ObjectiveQuestion{
		{Position: 1, Question: "one", Category: 1, Options: []ObjectiveOption{{Label: "A", Text: "first", Position: 0}, {Label: "B", Text: "second", Position: 1}}},
		{Position: 2, Question: "two", Category: 2, Options: []ObjectiveOption{{Label: "A", Text: "first", Position: 0}, {Label: "B", Text: "second", Position: 1}}},
	}}
}

func TestObjectiveProviderUsesSeparatePromptAndStrictAnswers(t *testing.T) {
	var wire wireRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(responseFor(`{"kind":"objective","answers":["a","AB"]}`, "stop")))
	}))
	defer server.Close()
	p, err := NewDeepSeek(Config{APIKey: "local-test", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.GenerateObjective(context.Background(), ObjectiveRequest{Problem: objectiveFixture(), Profile: DefaultProfile()})
	if err != nil {
		t.Fatal(err)
	}
	if result.Candidate.Kind != "objective" || strings.Join(result.Candidate.Answers, ",") != "A,AB" {
		t.Fatalf("candidate=%+v", result.Candidate)
	}
	if !strings.Contains(wire.Messages[0].Content.textOnly(), "objective/choice") || strings.Contains(wire.Messages[0].Content.textOnly(), "source code") == false {
		t.Fatalf("objective prompt not isolated: %s", wire.Messages[0].Content.textOnly())
	}
	if !strings.Contains(wire.Messages[1].Content.textOnly(), `"questions"`) || strings.Contains(wire.Messages[1].Content.textOnly(), "files") {
		t.Fatalf("objective payload mixed with programming files: %s", wire.Messages[1].Content.textOnly())
	}
}

func TestObjectiveValidationRejectsWrongKindAndDuplicateAnswers(t *testing.T) {
	s := objectiveFixture()
	if _, err := parseObjectiveCandidate(s, `{"kind":"programming","answers":["A","B"]}`); err == nil {
		t.Fatal("accepted programming candidate")
	}
	if _, err := parseObjectiveCandidate(s, `{"kind":"objective","answers":["A","AA"]}`); err == nil {
		t.Fatal("accepted duplicate multi answer")
	}
	if _, err := parseObjectiveCandidate(s, `{"kind":"objective","answers":["A","B"],"extra":1}`); err == nil {
		t.Fatal("accepted unknown field")
	}
}

func TestObjectiveRankedAnswersUseFirstChoiceAndValidateAllChoices(t *testing.T) {
	s := objectiveFixture()
	candidate, err := parseObjectiveCandidate(s, `{"kind":"objective","ranked_answers":[{"question_position":1,"choices":["B","A"],"confidence":[0.8,0.2]},{"question_position":2,"choices":["AB","A"],"confidence":[0.7,0.3]}]}`)
	if err != nil || strings.Join(candidate.Answers, ",") != "B,AB" {
		t.Fatalf("ranked candidate=%+v err=%v", candidate, err)
	}
	if _, err := parseObjectiveCandidate(s, `{"kind":"objective","ranked_answers":[{"question_position":1,"choices":["B","Z"]},{"question_position":2,"choices":["AB"]}]}`); err == nil {
		t.Fatal("accepted a ranked option outside the current question")
	}
}

func TestObjectiveFixedAnswersArePositionalAndPreserved(t *testing.T) {
	var wire wireRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(responseFor(`{"kind":"objective","answers":["B","AB"]}`, "stop")))
	}))
	defer server.Close()
	p, err := NewDeepSeek(Config{APIKey: "local-test", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.GenerateObjective(context.Background(), ObjectiveRequest{
		Problem:      objectiveFixture(),
		FixedAnswers: []string{"B", ""},
		Profile:      DefaultProfile(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(result.Candidate.Answers, ","); got != "B,AB" {
		t.Fatalf("candidate=%s", got)
	}
	var payload struct {
		FixedAnswers []string `json:"fixed_answers"`
	}
	if err := json.Unmarshal([]byte(wire.Messages[1].Content.textOnly()), &payload); err != nil {
		t.Fatal(err)
	}
	if strings.Join(payload.FixedAnswers, ",") != "B," {
		t.Fatalf("fixed_answers payload=%q", payload.FixedAnswers)
	}
	if !strings.Contains(wire.Messages[0].Content.textOnly(), "validated locked answer") || !strings.Contains(wire.Messages[0].Content.textOnly(), "incomplete data remains unresolved") {
		t.Fatalf("locked-answer prompt missing safeguards: %s", wire.Messages[0].Content.textOnly())
	}
}

func TestObjectiveFixedAnswersRejectIncompleteQuestionData(t *testing.T) {
	s := objectiveFixture()
	s.Questions[0].Options[0].Text = ""
	if err := ValidateObjectiveFixedAnswers(s, []string{"A", ""}); err == nil {
		t.Fatal("accepted a fixed answer for an incomplete question")
	} else if got := err.(*Error).Kind; got != "objective_fixed_answers_contract" {
		t.Fatalf("error kind=%q", got)
	}
	// An unresolved slot remains explicitly unresolved even when its question
	// data is incomplete; it must not be converted into a positional lock.
	if err := ValidateObjectiveFixedAnswers(s, []string{"", ""}); err != nil {
		t.Fatalf("rejected explicit unresolved slots: %v", err)
	}
	if err := ValidateObjectiveFixedAnswers(objectiveFixture(), []string{"A"}); err == nil {
		t.Fatal("accepted a vector with the wrong length")
	}
}

func TestObjectiveFixedAnswerMismatchIsRetryable(t *testing.T) {
	s := objectiveFixture()
	candidate := ObjectiveCandidate{Kind: "objective", Answers: []string{"A", "AB"}}
	err := validateObjectiveFixedAnswerMatch(s, []string{"B", ""}, candidate)
	if err == nil || err.(*Error).Kind != "objective_fixed_answers_mismatch" {
		t.Fatalf("mismatch error=%v", err)
	}
	if !IsRetryableError(err) {
		t.Fatal("fixed-answer mismatch is not retryable")
	}
}
