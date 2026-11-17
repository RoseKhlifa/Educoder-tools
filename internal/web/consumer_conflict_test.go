package web

import (
	"encoding/json"
	"strings"
	"testing"

	"educoder-cli/internal/jobs"
)

func TestConsumerQuestionConflictUsesFixedAttentionWithoutRawReasons(t *testing.T) {
	for _, raw := range []string{
		`{"status":"public_contract_conflict","diagnostics":["PRIVATE_DIAG"]}`,
		`{"homeworks":[{"result":{"levels":[{"reason_code":"public_contract_conflict"}]}}]}`,
	} {
		job := jobs.Job{Status: jobs.Failed, Request: jobs.Request{Scope: "homework", Mode: "submit"}, Result: json.RawMessage(raw)}
		got := toConsumerJob(job)
		if got.Result.Attention != "upstream_question_conflict" || got.Status != "support" {
			t.Fatalf("missing fixed attention: %+v", got)
		}
		data, _ := json.Marshal(got)
		assertConsumerBoundary(t, string(data))
		if strings.Contains(string(data), "PRIVATE_DIAG") || strings.Contains(string(data), "public_contract_conflict") {
			t.Fatal("operator-only conflict evidence leaked")
		}
		job.Status = jobs.Succeeded
		if toConsumerJob(job).Result.Attention != "" {
			t.Fatal("completed task retained obsolete conflict notice")
		}
	}
}
