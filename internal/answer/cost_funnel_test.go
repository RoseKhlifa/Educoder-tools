package answer

import "testing"

func TestCostFunnelDefaultOutputWindowAndReasoning(t *testing.T) {
	for _, p := range []Profile{DefaultProfile(), RepairProfile(), ProProfile()} {
		if p.MaxTokens != 8192 {
			t.Fatalf("output window=%d model=%s", p.MaxTokens, p.Model)
		}
	}
	if DefaultProfile().Thinking != "disabled" || RepairProfile().Thinking != "enabled" {
		t.Fatal("first generation/repair reasoning defaults differ from funnel")
	}
}
