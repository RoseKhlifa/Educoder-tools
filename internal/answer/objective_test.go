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
