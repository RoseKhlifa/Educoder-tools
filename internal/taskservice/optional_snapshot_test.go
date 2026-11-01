package taskservice

import (
	"encoding/json"
	"testing"

	"educoder-cli/internal/educoder"
)

// This is the failing assignment's public metadata shape: five required
// full-score levels followed by five optional zero-point levels. Required
// completion must not depend on the optional results or their zero maximum.
func TestRequiredHomeworkCompletionWithFiveOptionalLevels(t *testing.T) {
	var p educoder.HomeworkProgress
	if err := json.Unmarshal([]byte(`{"challenge_settings":[
		{"challenge_id":1,"challenge_name":"required one","status":"required","game_score":"16.00","challenge_score":16},
		{"challenge_id":2,"challenge_name":"required two","status":"required","game_score":"16.00","challenge_score":16},
		{"challenge_id":3,"challenge_name":"required three","status":"required","game_score":"16.00","challenge_score":16},
		{"challenge_id":4,"challenge_name":"required four","status":"required","game_score":"16.00","challenge_score":16},
		{"challenge_id":5,"challenge_name":"required five","status":"required","game_score":"16.00","challenge_score":16},
		{"challenge_id":6,"challenge_name":"optional six","status":"optional","game_score":"0.00","challenge_score":0},
		{"challenge_id":7,"challenge_name":"optional seven","status":"optional","game_score":"0.00","challenge_score":0},
		{"challenge_id":8,"challenge_name":"optional eight","status":"optional","game_score":"0.00","challenge_score":0},
		{"challenge_id":9,"challenge_name":"optional nine","status":"optional","game_score":"0.00","challenge_score":0},
		{"challenge_id":10,"challenge_name":"optional ten","status":"optional","game_score":"0.00","challenge_score":0}
	]}`), &p); err != nil {
		t.Fatal(err)
	}
	if !homeworkExactlyComplete(educoder.Homework{ChallengeCount: 10}, &p) {
		t.Fatal("five required full-score levels must complete the assignment; five optional levels must not block it")
	}
	// A zero-point required level still does not prove a full-score result.
	var unknown educoder.HomeworkProgress
	if err := json.Unmarshal([]byte(`{"challenge_settings":[{"challenge_id":1,"challenge_name":"required zero","status":"required","game_score":"0.00","challenge_score":0}]}`), &unknown); err != nil {
		t.Fatal(err)
	}
	if homeworkExactlyComplete(educoder.Homework{ChallengeCount: 1}, &unknown) {
		t.Fatal("zero maximum must not be used as an optional flag")
	}
}
