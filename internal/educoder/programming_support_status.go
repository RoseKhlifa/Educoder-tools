package educoder

import (
	"context"
	"errors"
	"time"

	"educoder-cli/internal/problem"
)

const ProgrammingSupportTimeout = 30 * time.Second
const programmingSupportAttemptTimeout = 14 * time.Second

// ProgrammingSupportError exposes only fixed metadata, never upstream error
// strings, URLs, identifiers, response text, headers or session information.
type ProgrammingSupportError struct {
	Stage    string
	Reason   string
	canceled error
}

func (e *ProgrammingSupportError) Error() string {
	return "programming support stage=" + e.Stage + " reason=" + e.Reason
}
func (e *ProgrammingSupportError) Unwrap() error { return e.canceled }

func supportFailure(stage, reason string) error {
	return &ProgrammingSupportError{Stage: stage, Reason: reason}
}

func supportRequestFailure(stage string, err error) error {
	e := &ProgrammingSupportError{Stage: stage, Reason: "invalid_response"}
	var read *MetadataReadError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		e.Reason, e.canceled = "unavailable_timeout", context.DeadlineExceeded
	case errors.Is(err, context.Canceled):
		e.Reason, e.canceled = "unavailable_cancelled", context.Canceled
	case errors.As(err, &read):
		switch read.Category {
		case "timeout":
			e.Reason = "unavailable_timeout"
		case "transport", "temporary", "interrupted", "body_read":
			e.Reason = "unavailable_network"
		case "http_status":
			e.Reason = "unavailable_http"
		case "api_status":
			e.Reason = "unavailable_upstream"
		}
	}
	return e
}

// ReadProgrammingSupportWithStatus bounds the whole optional probe and permits
// one retry for transport/timeout failures only. It never retries model calls,
// ignores malformed metadata, or returns a partially collected source list.
func (c *Client) ReadProgrammingSupportWithStatus(ctx context.Context, t *Task) ([]CodeFile, *problem.ReadOnlyContextDiagnostic) {
	if !headerOnlyTask(t) {
		return nil, nil
	}
	budget, cancel := context.WithTimeout(ctx, ProgrammingSupportTimeout)
	defer cancel()
	for attempt := 0; attempt < 2; attempt++ {
		probe, stop := context.WithTimeout(budget, programmingSupportAttemptTimeout)
		files, err := c.ReadProgrammingSupport(probe, t)
		stop()
		if err == nil {
			if len(files) == 0 {
				return nil, &problem.ReadOnlyContextDiagnostic{Status: "missing", Stage: "association", Reason: "no_matching_driver"}
			}
			return files, &problem.ReadOnlyContextDiagnostic{Status: "available", Stage: "association", Reason: "observed_driver"}
		}
		var failure *ProgrammingSupportError
		if !errors.As(err, &failure) {
			failure = &ProgrammingSupportError{Stage: "repository_task", Reason: "invalid_response"}
		}
		if attempt == 0 && budget.Err() == nil && (failure.Reason == "unavailable_timeout" || failure.Reason == "unavailable_network") {
			continue
		}
		state := "unavailable"
		if failure.Reason == "no_matching_driver" {
			state = "missing"
		}
		return nil, &problem.ReadOnlyContextDiagnostic{Status: state, Stage: failure.Stage, Reason: failure.Reason}
	}
	return nil, nil
}

func headerOnlyTask(t *Task) bool {
	if t == nil || len(t.CodePaths()) == 0 {
		return false
	}
	for _, file := range t.CodePaths() {
		if !headerExtension(file) {
			return false
		}
	}
	return true
}
