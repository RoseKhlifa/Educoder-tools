package web

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/jobs"
	"educoder-cli/internal/taskservice"
)

func TestCollectionOutcomeKeepsBoundedResultsAndDoesNotMaskFailures(t *testing.T) {
	for _, tc := range []struct {
		name      string
		limit     bool
		failed    int
		err       error
		status    string
		stop      string
		complete  bool
		exhausted bool
	}{
		{"complete", false, 0, nil, jobs.Succeeded, "", true, true},
		{"time budget", true, 0, context.DeadlineExceeded, jobs.Failed, "time_limit", false, false},
		{"read budget", true, 0, educoder.ErrCollectionLimit, jobs.Succeeded, "read_budget_limit", false, false},
		{"enumeration budget", true, 0, nil, jobs.Succeeded, "scan_limit", false, false},
		{"budget with failed reads", true, 1, context.DeadlineExceeded, jobs.Failed, "time_limit", false, false},
		{"unrecognized failure", true, 0, errors.New("private upstream path"), jobs.Failed, "collection_stopped", false, false},
		{"request timeout", false, 0, context.DeadlineExceeded, jobs.Failed, "request_timeout", false, false},
		{"failed reads despite nil error", false, 1, nil, jobs.Failed, "collection_stopped", false, false},
		{"not exhausted despite nil error", false, 0, nil, jobs.Failed, "scan_incomplete", false, false},
		{"canceled", false, 0, context.Canceled, jobs.Canceled, "canceled", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			summary := taskservice.CollectionSummary{Collected: 2, Duplicates: 18, Skipped: 3, Failed: tc.failed, LimitReached: tc.limit, ScanExhausted: tc.exhausted, Reasons: map[string]int{"programming_submitted_source_unproven": 3}}
			out, err := collectionOutcome(summary, tc.err)
			var result struct {
				taskservice.CollectionSummary
				ScanComplete bool   `json:"scan_complete"`
				StopReason   string `json:"stop_reason"`
			}
			if err != nil || json.Unmarshal(out.Result, &result) != nil || out.Status != tc.status || result.ScanComplete != tc.complete || result.StopReason != tc.stop {
				t.Fatalf("classification: %+v result=%+v err=%v", out, result, err)
			}
			if result.Collected != 2 || result.Duplicates != 18 || result.Skipped != 3 || result.Reasons["programming_submitted_source_unproven"] != 3 {
				t.Fatalf("partial verified results lost: %s", out.Result)
			}
			if strings.Contains(out.Message+string(out.Result), "private upstream") || (tc.status == jobs.Failed && out.ErrorCode != "collection_stopped" && out.ErrorCode != "collection_incomplete") {
				t.Fatalf("unsafe error projection or missing stable classification: %+v", out)
			}
		})
	}
}
