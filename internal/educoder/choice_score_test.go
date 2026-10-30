package educoder

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChoiceFullScoreGate(t *testing.T) {
	for _, tc := range []struct {
		name, score, max, correct string
		count                     int
		known, passed             bool
	}{
		{"mixed full score", `"13.30"`, `13.3`, `null`, 2, true, true},
		{"string full score", `"13.30"`, `"13.3"`, `null`, 2, true, true},
		{"numeric full score", `13.30`, `13.3`, `null`, 2, true, true},
		{"numeric earned string max", `13.30`, `"13.3"`, `null`, 2, true, true},
		{"partial submitted", `"12.00"`, `13.3`, `null`, 2, true, false},
		{"partial despite correct count", `"12.00"`, `13.3`, `2`, 2, true, false},
		{"less than full without float rounding", `"13.299999999999999999"`, `13.3`, `null`, 2, true, false},
		{"over full is inconsistent", `"13.31"`, `13.3`, `null`, 2, true, false},
		{"zero max", `0`, `0`, `null`, 2, false, false},
		{"negative scores", `-13.3`, `-13.3`, `null`, 2, false, false},
		{"null max despite correct count", `"13.3"`, `null`, `2`, 2, false, false},
		{"null earned", `null`, `13.3`, `null`, 2, false, false},
		{"hidden earned", `"--"`, `13.3`, `null`, 2, false, false},
		{"fraction is invalid", `"133/10"`, `13.3`, `null`, 2, false, false},
		{"NaN is invalid", `"NaN"`, `13.3`, `null`, 2, false, false},
		{"wrong count contradicts full score", `"13.30"`, `13.3`, `1`, 2, true, false},
		{"wrong total contradicts full score", `"13.30"`, `13.3`, `1`, 1, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/tasks/OBJGAME.json":
					_ = json.NewEncoder(w).Encode(objectiveFixture())
				case "/api/tasks/OBJGAME/choose_build.json":
					_, _ = fmt.Fprintf(w, `{"status":0,"choose_correct_num":%s,"challenge_chooses_count":%d,"game_original_status":2,"passed":true,"score_known":true}`, tc.correct, tc.count)
				case "/api/homework_commons/44/shixun_challenge_data.json":
					_, _ = fmt.Fprintf(w, `{"data":{"passed_count":1,"challenge_settings":[{"challenge_id":9,"challenge_score":%s,"game_score":%s,"passed_status":2}]}}`, tc.max, tc.score)
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
			if err != nil || result.ScoreKnown != tc.known || result.Passed != tc.passed {
				t.Fatalf("result=%+v error=%v", result, err)
			}
		})
	}
}

func TestChoiceMissingScoreCannotBeOverriddenBySubmissionFlags(t *testing.T) {
	for _, progress := range []string{
		`{"data":{"challenge_settings":[{"challenge_id":10,"challenge_score":13.3,"game_score":"13.30"}]}}`,
		`{"data":{"challenge_settings":[{"challenge_id":9}]}}`,
		`{"status":-1,"message":"unavailable"}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/tasks/OBJGAME.json":
				_ = json.NewEncoder(w).Encode(objectiveFixture())
			case "/api/tasks/OBJGAME/choose_build.json":
				fmt.Fprint(w, `{"status":0,"choose_correct_num":2,"challenge_chooses_count":2,"game_original_status":2,"passed":true,"score_known":true,"score":"13.30","max_score":"13.3"}`)
			default:
				fmt.Fprint(w, progress)
			}
		}))
		client := New(server.URL)
		task, err := client.LoadChoiceTask(context.Background(), "COURSE", 44, "OBJGAME")
		if err != nil {
			t.Fatal(err)
		}
		result, err := client.ObjectiveSubmit(context.Background(), task, []string{"A", "AB"})
		server.Close()
		if err != nil || result.Passed || result.ScoreKnown || result.ScoreCheck != "unconfirmed" || result.ScoreDiagnostic == "" {
			t.Fatalf("submission flags bypassed score verification: result=%+v error=%v", result, err)
		}
	}
}

func TestProgressDecodesDecimalScoresWithoutRounding(t *testing.T) {
	var p ChallengeProgress
	if err := json.Unmarshal([]byte(`{"challenge_id":9,"challenge_score":13.3,"game_score":13.299999999999999999}`), &p); err != nil {
		t.Fatal(err)
	}
	if p.ChallengeID != 9 || p.ChallengeScore != "13.3" || p.GameScore != "13.299999999999999999" || p.HasFullScore() {
		t.Fatalf("score text lost precision or other progress fields: %+v", p)
	}
	if err := json.Unmarshal([]byte(`{"challenge_id":9,"challenge_score":13.3,"game_score":"13.30"}`), &p); err != nil || !p.HasFullScore() {
		t.Fatalf("equivalent decimal scores did not match: %+v error=%v", p, err)
	}
}
