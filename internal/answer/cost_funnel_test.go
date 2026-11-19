package answer

import "testing"

func TestCostFunnelDefaultOutputWindowAndReasoning(t *testing.T) {
	if p := DefaultProfile(); p.MaxTokens != MaxOutputTokens || p.Thinking != "disabled" {
		t.Fatalf("first generation profile=%+v", p)
	}
	// Reasoning tokens share max_tokens with the JSON answer, so reasoning
	// profiles receive the larger window instead of failing at 8192.
	for _, p := range []Profile{RepairProfile(), ProProfile()} {
		if p.MaxTokens != MaxThinkingOutputTokens || p.Thinking != "enabled" {
			t.Fatalf("reasoning profile=%+v", p)
		}
	}
	if MaxOutputTokens != 8192 || MaxThinkingOutputTokens != 32768 {
		t.Fatalf("output windows changed: %d/%d", MaxOutputTokens, MaxThinkingOutputTokens)
	}
}

func TestOutputWindowDependsOnThinkingMode(t *testing.T) {
	cases := []struct {
		profile Profile
		valid   bool
	}{
		{Profile{Model: "m", Thinking: "disabled", MaxTokens: MaxOutputTokens}, true},
		{Profile{Model: "m", Thinking: "disabled", MaxTokens: MaxOutputTokens + 1}, false},
		{Profile{Model: "m", Thinking: "enabled", ReasoningEffort: "low", MaxTokens: MaxThinkingOutputTokens}, true},
		{Profile{Model: "m", Thinking: "enabled", ReasoningEffort: "low", MaxTokens: MaxThinkingOutputTokens + 1}, false},
		{Profile{Model: "m", Thinking: "enabled", MaxTokens: 0}, false},
		{Profile{Model: "m", Thinking: "auto", MaxTokens: 1024}, false},
	}
	snapshot := objectiveFixture()
	for _, tc := range cases {
		_, programmingErr := buildRequest(Request{Problem: testSnapshot(), Profile: tc.profile})
		_, objectiveErr := buildObjectiveRequest(ObjectiveRequest{Problem: snapshot, Profile: tc.profile})
		for name, err := range map[string]error{"programming": programmingErr, "objective": objectiveErr} {
			if (err == nil) != tc.valid {
				t.Fatalf("%s profile %+v valid=%v err=%v", name, tc.profile, tc.valid, err)
			}
		}
	}
	wire, err := buildRequest(Request{Problem: testSnapshot(), Profile: RepairProfile()})
	if err != nil || wire.MaxTokens != MaxThinkingOutputTokens || wire.Thinking["type"] != "enabled" || wire.ReasoningEffort != "low" {
		t.Fatalf("repair wire request=%+v err=%v", wire, err)
	}
}

func TestThinkingReservationCoversWholeOutputWindow(t *testing.T) {
	provider, err := NewDeepSeek(Config{APIKey: "test"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := provider.Estimate(Request{Problem: testSnapshot(), Profile: DefaultProfile()})
	if err != nil {
		t.Fatal(err)
	}
	repair, err := provider.Estimate(Request{Problem: testSnapshot(), Profile: RepairProfile()})
	if err != nil {
		t.Fatal(err)
	}
	if first.OutputTokens != MaxOutputTokens || repair.OutputTokens != MaxThinkingOutputTokens || repair.CostMicroUSD <= first.CostMicroUSD {
		t.Fatalf("reservations do not reflect output windows: first=%+v repair=%+v", first, repair)
	}
}
