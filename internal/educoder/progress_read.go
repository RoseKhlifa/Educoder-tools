package educoder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

const homeworkProgressReadAttempts = 3

// MetadataReadError contains fixed diagnostic metadata only. It never
// retains an upstream URL, request query, response text, cookie or raw error.
type MetadataReadError struct {
	Operation  string
	Stage      string
	Category   string
	HTTPStatus int
	Attempts   int
	canceled   error
}

// Preserve the progress API's existing error contract for callers using As.
type HomeworkProgressReadError = MetadataReadError

func (e *MetadataReadError) Error() string {
	operation := metadataOperationLabel(e.Operation)
	return fmt.Sprintf("%s stage=%s category=%s status=%d attempts=%d", operation, e.Stage, e.Category, e.HTTPStatus, e.Attempts)
}

// Unwrap preserves parent cancellation without exposing transport errors that
// can contain the complete signed URL. An HTTP client timeout is independent
// of the parent context and remains a retryable timeout category.
func (e *HomeworkProgressReadError) Unwrap() error { return e.canceled }

func (e *HomeworkProgressReadError) transient() bool {
	if e.canceled != nil {
		return false
	}
	// Once an authentication or other permanent client status is observed, a
	// broken response body must not turn that response into a retryable read.
	if e.HTTPStatus >= 400 && e.HTTPStatus < 500 && e.HTTPStatus != http.StatusTooManyRequests {
		return false
	}
	return e.Category == "timeout" || e.Category == "interrupted" || e.Category == "temporary" ||
		e.Category == "http_status" && (e.HTTPStatus == http.StatusTooManyRequests || e.HTTPStatus >= 500 && e.HTTPStatus < 600)
}

func newHomeworkProgressReadError(ctx context.Context, stage, category string, status int, cause error) *HomeworkProgressReadError {
	e := &HomeworkProgressReadError{Stage: stage, Category: category, HTTPStatus: status, Attempts: 1}
	if ctx.Err() != nil {
		e.Category, e.canceled = "canceled", ctx.Err()
		return e
	}
	if stage == "transport" || stage == "body" {
		var network net.Error
		switch {
		case errors.Is(cause, context.DeadlineExceeded):
			e.Category = "timeout"
		case errors.As(cause, &network) && network.Timeout():
			e.Category = "timeout"
		case errors.Is(cause, io.EOF) || errors.Is(cause, io.ErrUnexpectedEOF):
			e.Category = "interrupted"
		case errors.As(cause, &network) && network.Temporary():
			e.Category = "temporary"
		}
	}
	return e
}

func waitHomeworkProgressRetry(ctx context.Context, attempt int) error {
	timer := time.NewTimer(time.Duration(1<<(attempt-1)) * 200 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
