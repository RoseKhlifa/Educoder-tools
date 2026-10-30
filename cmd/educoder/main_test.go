package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/educoder"
)

func TestObjectiveOutputRequiresFullScoreInBothModes(t *testing.T) {
	for _, jsonOutput := range []bool{false, true} {
		for _, passed := range []bool{false, true} {
			var output bytes.Buffer
			result := &educoder.ChoiceSubmitResult{Passed: passed, ScoreKnown: true, Score: "12.00", MaxScore: "13.3"}
			if passed {
				result.Score = "13.30"
			}
			err := writeObjectiveSubmission(&output, answer.ObjectiveCandidate{Kind: "objective", Answers: []string{"A"}}, result, jsonOutput)
			if (err == nil) != passed {
				t.Fatalf("mode json=%t passed=%t error=%v", jsonOutput, passed, err)
			}
			if jsonOutput {
				var report struct {
					Evaluation educoder.ChoiceSubmitResult `json:"evaluation"`
				}
				if json.Unmarshal(output.Bytes(), &report) != nil || report.Evaluation.Passed != passed {
					t.Fatalf("result report missing on failure: %s", output.String())
				}
			}
		}
	}
}
