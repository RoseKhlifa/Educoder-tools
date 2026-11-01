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
		name     string
		limit    bool
		failed   int
		err      error
		status   string
		stop     string
		complete bool
	}{
		{"complete", false, 0, nil, jobs.Succeeded, "", true},
		{"time budget", true, 0, context.DeadlineExceeded, jobs.Succeeded, "time_limit", false},
		{"read budget", true, 0, educoder.ErrCollectionLimit, jobs.Succeeded, "read_budget_limit", false},
		{"enumeration budget", true, 0, nil, jobs.Succeeded, "scan_limit", false},
		{"budget with failed reads", true, 1, context.DeadlineExceeded, jobs.Failed, "time_limit", false},
		{"unrecognized failure", true, 0, errors.New("private upstream path"), jobs.Failed, "collection_stopped", false},
		{"deadline without expected limit", false, 0, context.DeadlineExceeded, jobs.Failed, "time_limit", false},
		{"failed reads despite nil error", false, 1, nil, jobs.Failed, "collection_stopped", false},
		{"canceled", false, 0, context.Canceled, jobs.Canceled, "canceled", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			summary := taskservice.CollectionSummary{Collected: 2, Duplicates: 18, Skipped: 3, Failed: tc.failed, LimitReached: tc.limit, Reasons: map[string]int{"programming_submitted_source_unproven": 3}}
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
			if strings.Contains(out.Message+string(out.Result), "private upstream") || (tc.status == jobs.Failed && out.ErrorCode != "collection_stopped") {
				t.Fatalf("unsafe error projection or missing stable classification: %+v", out)
			}
		})
	}
}
