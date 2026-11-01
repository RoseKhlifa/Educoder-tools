package web

import (
	"testing"

	"educoder-cli/internal/jobs"
	"educoder-cli/internal/taskservice"
)

func TestOptionalOnlyHomeworkIsNoWorkSuccessWithoutAcceptingUnknownEmptyRoster(t *testing.T) {
	for _, tc := range []struct {
		reason string
		want   string
	}{{"no_required_levels", jobs.Succeeded}, {"", jobs.Failed}, {"unsupported_roster", jobs.Failed}} {
		out, err := homeworkOutcome(&taskservice.BatchResult{Status: "passed", ReasonCode: tc.reason}, nil)
		if err != nil || out.Status != tc.want {
			t.Fatalf("reason=%q outcome=%+v err=%v", tc.reason, out, err)
		}
	}
}
