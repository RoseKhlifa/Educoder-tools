package answer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestObjectiveFenceIsReportedNotUnwrapped(t *testing.T) {
	fenced := "```json\n{\"kind\":\"objective\",\"answers\":[\"A\",\"AB\"]}\n```"
	_, err := parseObjectiveCandidate(objectiveFixture(), fenced)
	var providerErr *Error
	if !errors.As(err, &providerErr) || providerErr.Kind != "invalid_output_json" || providerErr.OutputShape == nil || !providerErr.OutputShape.Fenced || !providerErr.OutputShape.ValidJSON {
		t.Fatalf("fenced objective output must stay rejected with a fenced shape: %v", err)
	}
}

func TestObjectiveAnswersOnlyRetryOverridesRankedContract(t *testing.T) {
	ranked, err := buildObjectiveRequest(ObjectiveRequest{Problem: objectiveFixture(), RequireRankedAnswers: true})
	if err != nil {
		t.Fatal(err)
	}
	system := ranked.Messages[0].Content.textOnly()
	if !strings.Contains(system, "ranked_answers is required") || !strings.Contains(system, "two or three alternatives at most") {
		t.Fatalf("ranked request is not bounded: %s", system[:200])
	}
	compact, err := buildObjectiveRequest(ObjectiveRequest{Problem: objectiveFixture(), RequireRankedAnswers: true, AnswersOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	system = compact.Messages[0].Content.textOnly()
	if strings.Contains(system, "ranked_answers is required") || !strings.Contains(system, "Return only {\"kind\":\"objective\",\"answers\":[...]} with exactly 2 answer strings") {
		t.Fatalf("answers-only retry kept the ranked contract: %s", system[:300])
	}
	var payload struct {
		OutputContract struct {
			RankedRequired bool `json:"ranked_answers_required"`
			AnswersOnly    bool `json:"answers_only"`
		} `json:"output_contract"`
	}
	if err := json.Unmarshal([]byte(compact.Messages[1].Content.textOnly()), &payload); err != nil || payload.OutputContract.RankedRequired || !payload.OutputContract.AnswersOnly {
		t.Fatalf("payload contract=%+v err=%v", payload.OutputContract, err)
	}
}

func TestOutputShapeDiagnosticsAreContentFree(t *testing.T) {
	responses := map[string]string{
		"truncated": `{"id":"x","model":"deepseek-flash","choices":[{"finish_reason":"length","message":{"content":"{\"kind\":\"objective\",\"ranked_answers\":[{\"question_position\":1,\"choices\":[\"PRIVATE-OPTION","reasoning_content":"PRIVATE-REASONING-TEXT"}}],"usage":{"prompt_tokens":5,"completion_tokens":8192,"total_tokens":8197}}`,
		"unknown":   responseFor(`{"kind":"objective","answers":["A","AB"],"explanation":"PRIVATE-EXPLANATION"}`, "stop"),
	}
	for name, body := range responses {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		}))
		p, err := NewDeepSeek(Config{APIKey: "local-test", BaseURL: server.URL})
		if err != nil {
			t.Fatal(err)
		}
		_, genErr := p.GenerateObjective(context.Background(), ObjectiveRequest{Problem: objectiveFixture()})
		server.Close()
		var providerErr *Error
		if !errors.As(genErr, &providerErr) || providerErr.OutputShape == nil {
			t.Fatalf("%s: missing output shape: %v", name, genErr)
		}
		message := genErr.Error()
		if strings.Contains(message, "PRIVATE") || strings.Contains(message, "explanation") {
			t.Fatalf("%s: diagnostic leaked content: %s", name, message)
		}
		switch name {
		case "truncated":
			if providerErr.Kind != "truncated_output" || providerErr.OutputShape.ValidJSON || providerErr.OutputShape.ReasoningBytes != len("PRIVATE-REASONING-TEXT") || !strings.Contains(message, "reasoning_bytes=22") {
				t.Fatalf("truncated shape=%+v message=%s", providerErr.OutputShape, message)
			}
		case "unknown":
			if providerErr.Kind != "invalid_output_json" || !providerErr.OutputShape.ValidJSON || providerErr.OutputShape.UnknownKeys != 1 || !strings.Contains(message, "keys=kind,answers unknown_keys=1") {
				t.Fatalf("unknown-key shape=%+v message=%s", providerErr.OutputShape, message)
			}
		}
	}
}
