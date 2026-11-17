package taskservice

import "testing"

func TestPublicContractAndContextFailuresKeepActionableReason(t *testing.T) {
	for _, status := range []string{"public_contract_conflict", "context_unavailable"} {
		r := &Result{Status: status}
		if got := batchFailureReason(r, nil); got != status {
			t.Fatalf("reason %q became %q", status, got)
		}
		if got := terminalBatchStatus(r, nil); got != status {
			t.Fatalf("terminal status %q became %q", status, got)
		}
	}
}
