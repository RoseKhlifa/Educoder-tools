package educoder

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func objectiveFixture() map[string]any {
	return map[string]any{
		"st": 1, "homework_common_id": 44,
		"game":      map[string]any{"id": 7, "identifier": "OBJGAME", "myshixun_id": 8},
		"challenge": map[string]any{"id": 9, "st": 1, "with_code_file": true, "path": nil},
		"chooses": []any{
			map[string]any{"challenge_choose_id": 10, "challenge_id": 9, "position": 1, "question_name": "单选题", "question_type": 0, "category": 1, "subject": "首个问题", "challenge_question": []any{
				map[string]any{"option_name": "甲", "position": 0}, map[string]any{"option_name": "乙", "position": 1},
			}},
			map[string]any{"challenge_choose_id": 11, "challenge_id": 9, "position": 2, "question_name": "多选题", "question_type": 0, "category": 2, "subject": "第二个问题", "challenge_question": []any{
				map[string]any{"option_name": "甲", "position": 0}, map[string]any{"option_name": "乙", "position": 1}, map[string]any{"option_name": "丙", "position": 2},
			}},
		},
		"choose_test_cases": map[string]any{"challenge_chooses_count": 2, "had_submmit": false, "had_all_submmit": false},
	}
}

func TestChoiceLoadAndSubmitUsesOnlyChooseBuild(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tasks/OBJGAME.json":
			_ = json.NewEncoder(w).Encode(objectiveFixture())
		case "/api/tasks/OBJGAME/choose_build.json":
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			_, _ = w.Write([]byte(`{"status":1,"choose_correct_num":2,"challenge_chooses_count":2,"test_sets":[]}`))
		case "/api/homework_commons/44/shixun_challenge_data.json":
			_, _ = w.Write([]byte(`{"data":{"challenge_settings":[{"challenge_id":9,"challenge_score":13.3,"game_score":"13.30"}]}}`))
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := New(server.URL)
	task, err := client.LoadChoiceTask(context.Background(), "COURSE", 44, "OBJGAME")
	if err != nil {
		t.Fatal(err)
	}
	if task.Kind != TaskKindChoice || len(task.Questions) != 2 {
		t.Fatalf("task=%+v", task)
	}
	result, err := client.ObjectiveSubmit(context.Background(), task, []string{"a", "c,a"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || !reflect.DeepEqual(body["answer"], []any{"A", "AC"}) {
		t.Fatalf("result=%+v body=%#v", result, body)
	}
	if body["challenge_id"] != float64(9) || body["homework_common_id"] != nil {
		t.Fatalf("body context=%#v", body)
	}
}

func TestChoiceValidationAndProgrammingIsolation(t *testing.T) {
	q := ChoiceQuestion{Position: 1, Category: 1, Subject: "q", Options: []ChoiceOption{{Position: 0, OptionName: "a"}, {Position: 1, OptionName: "b"}}}
	if got, err := CanonicalizeChoiceAnswer(q, " b "); err != nil || got != "B" {
		t.Fatalf("single=%q err=%v", got, err)
	}
	if _, err := CanonicalizeChoiceAnswer(q, "AB"); err == nil {
		t.Fatal("single accepted multiple answers")
	}
	q.Category = 2
	if got, err := CanonicalizeChoiceAnswer(q, "b,a,a"); err != nil || got != "AB" {
		t.Fatalf("multi=%q err=%v", got, err)
	}
	if RecordedRoute(http.MethodPost, "/api/tasks/GAME/choose_build.json") == false {
		t.Fatal("choose_build not allowlisted")
	}
	program := decodeTaskFixture(t, programmingTaskFixture("GAME", 1))
	if _, err := (&Client{}).ObjectiveSubmit(context.Background(), &ChoiceTask{Task: program}, nil); err == nil {
		t.Fatal("programming task entered objective submit")
	}
}

func TestChoiceHiddenCorrectCountRequiresExactFullScore(t *testing.T) {
	for _, tc := range []struct {
		name, score, max string
		passed           bool
	}{
		{"full score", "13.30", "13.3", true},
		{"partial score", "12.00", "13.3", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/tasks/OBJGAME.json":
					_ = json.NewEncoder(w).Encode(objectiveFixture())
				case "/api/tasks/OBJGAME/choose_build.json":
					_, _ = w.Write([]byte(`{"status":0,"choose_correct_num":null,"challenge_chooses_count":2,"game_original_status":2}`))
				case "/api/homework_commons/44/shixun_challenge_data.json":
					_, _ = fmt.Fprintf(w, `{"data":{"passed_count":0,"challenge_settings":[{"challenge_id":9,"challenge_score":"%s","game_score":"%s","passed_status":2}]}}`, tc.max, tc.score)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client := New(server.URL)
			task, err := client.LoadChoiceTask(context.Background(), "COURSE", 44, "OBJGAME")
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.ObjectiveSubmit(context.Background(), task, []string{"A", "AB"})
			if err != nil || !result.ScoreKnown || result.Passed != tc.passed {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}
