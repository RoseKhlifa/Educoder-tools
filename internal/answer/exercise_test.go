package answer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func exerciseFixture() ExerciseSnapshot {
	return ExerciseSnapshot{Kind: "exercise", Title: "Synthetic exercise", Questions: []ExerciseQuestion{
		{ID: 11, Position: 1, Type: "single", Title: "Choose", Options: []ExerciseOption{{ID: 101, Text: "First"}, {ID: 109, Text: "Second"}}},
		{ID: 12, Position: 2, Type: "judgment", Title: "True?", Options: []ExerciseOption{{ID: 77, Text: "False"}, {ID: 82, Text: "True"}}},
		{ID: 13, Position: 3, Type: "fill_blank", Title: "Case-sensitive value", BlankCount: 1},
	}}
}
func TestExerciseTypedCandidateRejectsContractConfusion(t *testing.T) {
	s := exerciseFixture()
	good := `{"kind":"exercise","answers":[{"question_id":11,"type":"single","option_id":109},{"question_id":12,"type":"judgment","option_id":77},{"question_id":13,"type":"fill_blank","text":"Node<T>\n  XyZ"}]}`
	c, err := parseExerciseCandidate(s, good)
	if err != nil || c.Answers[2].Text == nil || *c.Answers[2].Text != "Node<T>\n  XyZ" {
		t.Fatalf("candidate=%+v err=%v", c, err)
	}
	for name, input := range map[string]string{
		"letter_option":        strings.Replace(good, `"option_id":109`, `"option_id":"B"`, 1),
		"position_option":      strings.Replace(good, `"option_id":109`, `"option_id":2`, 1),
		"unknown_question":     strings.Replace(good, `"question_id":11`, `"question_id":99`, 1),
		"duplicate_question":   strings.Replace(good, `"question_id":12`, `"question_id":11`, 1),
		"wrong_type":           strings.Replace(good, `"type":"single"`, `"type":"multiple"`, 1),
		"unknown_field":        strings.Replace(good, `"option_id":109`, `"option_id":109,"explanation":"extra"`, 1),
		"duplicate_key":        strings.Replace(good, `"kind":"exercise"`, `"kind":"exercise","kind":"exercise"`, 1),
		"extra_json":           good + `{}`,
		"blank_as_option":      strings.Replace(good, `"text":"Node<T>\n  XyZ"`, `"option_id":1`, 1),
		"null_blank":           strings.Replace(good, `"text":"Node<T>\n  XyZ"`, `"text":null`, 1),
		"mixed_choice_payload": strings.Replace(good, `"option_id":109`, `"option_id":109,"text":null`, 1),
		"mixed_blank_payload":  strings.Replace(good, `"text":"Node<T>\n  XyZ"`, `"text":"Node<T>\n  XyZ","option_id":null`, 1),
		"missing_question":     `{"kind":"exercise","answers":[{"question_id":11,"type":"single","option_id":109}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseExerciseCandidate(s, input); err == nil {
				t.Fatalf("accepted %s", input)
			}
		})
	}
}
func TestExerciseRejectsUnknownMediaAndUnsupportedBeforeProvider(t *testing.T) {
	for _, change := range []func(*ExerciseSnapshot){
		func(s *ExerciseSnapshot) { s.Questions[0].Title = `<img src="https://example.invalid/a.png">` },
		func(s *ExerciseSnapshot) { s.Questions[0].Options[0].Text = `![diagram](anything.png)` },
		func(s *ExerciseSnapshot) { s.Questions[2].BlankCount = 2 },
		func(s *ExerciseSnapshot) { s.Questions[0].Type = "programming" },
	} {
		s := exerciseFixture()
		change(&s)
		if _, err := buildExerciseRequest(ExerciseRequest{Problem: s}); err == nil {
			t.Fatal("accepted unsupported input")
		}
	}
}
func TestExerciseDeepSeekTransportUsageAndTruncation(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer synthetic-key" {
			t.Error("configured transport not used")
		}
		var req wireRequest
		if json.NewDecoder(r.Body).Decode(&req) != nil || !strings.Contains(req.Messages[1].Content.textOnly(), `"id":109`) || req.Model != "deepseek-flash" {
			t.Error("typed prompt not preserved")
		}
		finish := "stop"
		candidate := `{"kind":"exercise","answers":[{"question_id":11,"type":"single","option_id":109},{"question_id":12,"type":"judgment","option_id":77},{"question_id":13,"type":"fill_blank","text":"AbCd"}]}`
		if calls == 2 {
			finish = "length"
			candidate = `{"kind":`
		}
		encoded, _ := json.Marshal(candidate)
		fmt.Fprintf(w, `{"id":"synthetic-request","model":"deepseek-flash","choices":[{"finish_reason":%q,"message":{"content":%s}}],"usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150}}`, finish, encoded)
	}))
	defer server.Close()
	d, err := NewDeepSeek(Config{APIKey: "synthetic-key", BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	est, err := d.EstimateExercise(ExerciseRequest{Problem: exerciseFixture()})
	if err != nil || est.CostMicroUSD <= 0 {
		t.Fatalf("estimate=%+v err=%v", est, err)
	}
	r, err := d.GenerateExercise(context.Background(), ExerciseRequest{Problem: exerciseFixture()})
	if err != nil || !r.CostKnown || !r.Usage.Known || r.Usage.TotalTokens != 150 || *r.Candidate.Answers[2].Text != "AbCd" {
		t.Fatalf("result=%+v err=%v", r, err)
	}
	r, err = d.GenerateExercise(context.Background(), ExerciseRequest{Problem: exerciseFixture()})
	if !IsTruncatedOutput(err) || !r.CostKnown || r.Candidate.Kind != "" {
		t.Fatalf("truncation=%+v err=%v", r, err)
	}
}
