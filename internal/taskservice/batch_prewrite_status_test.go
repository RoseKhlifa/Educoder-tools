package taskservice

import (
	"errors"
	"testing"

	"educoder-cli/internal/solver"
)

func TestBatchPreservesKnownPreFormalFailureStatusAndReason(t *testing.T) {
	for _, status := range []string{"prewrite_error", "saved_not_submitted", "save_unverified", "save_error", "editor_changed"} {
		t.Run(status, func(t *testing.T) {
			result := &Result{
				Status: status, Kind: "programming",
				Programming: &solver.Result{Status: status, Evaluation: &solver.Evaluation{Status: status}},
				// A stale or differently worded diagnostic must not replace the
				// adapter's explicit pre-formal mutation stage.
				Diagnostics: []string{"editor content check interrupted"},
			}
			err := errors.New("graphic mutation stopped before formal evaluation")
			if got := terminalBatchStatus(result, err); got != status {
				t.Fatalf("terminal status=%q, want %q", got, status)
			}
			if got := batchFailureReason(result, err); got != status {
				t.Fatalf("reason=%q, want %q", got, status)
			}
		})
	}
}

func TestBatchFormalBuildUnknownRemainsUncertain(t *testing.T) {
	result := &Result{Status: "uncertain", Kind: "programming", Programming: &solver.Result{
		Status: "uncertain", Evaluation: &solver.Evaluation{Status: "error", SavedFilesHash: "saved", SubmittedFilesHash: "submitted"},
	}}
	err := errors.New("formal evaluation polling interrupted")
	if got := terminalBatchStatus(result, err); got != "uncertain" {
		t.Fatalf("terminal status=%q", got)
	}
	if got := batchFailureReason(result, err); got != "submission_uncertain" {
		t.Fatalf("reason=%q", got)
	}
}
