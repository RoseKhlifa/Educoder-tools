package answer

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxRetryAfterSeconds = 60

// RetryWait is an optional per-service wait implementation. A nil value uses a
// context-aware timer; tests can observe delays without sleeping or a global
// clock. It does not authorize another request or change any usage budget.
type RetryWait func(context.Context, time.Duration) error

// AvailabilityRetry contains only bounded operational metadata, never response
// bodies or headers. Persist it with the run before another model dispatch.
type AvailabilityRetry struct {
	Streak            int `json:"streak,omitempty"`
	RetryAfterSeconds int `json:"retry_after_seconds,omitempty"`
}

// IsAvailabilityError excludes generated-output defects, authentication and
// interrupted successful responses. Callers must separately ensure that no
// partial candidate was produced and that no platform mutation is replayed.
func IsAvailabilityError(err error) bool {
	var e *Error
	if !errors.As(err, &e) || e == nil || !e.Retryable {
		return false
	}
	switch e.Kind {
	case "upstream_error":
		return e.HTTPStatus == 429 || e.HTTPStatus >= 500 && e.HTTPStatus < 600
	case "network_error", "timeout":
		return e.HTTPStatus == 0
	case "response_read_failed":
		return e.HTTPStatus >= 500 && e.HTTPStatus < 600 && e.ReadCause != "canceled"
	default:
		return false
	}
}

func (r *AvailabilityRetry) Observe(err error) bool {
	if !IsAvailabilityError(err) {
		*r = AvailabilityRetry{}
		return false
	}
	// The delay already reaches its cap at four failures. Keep durable counters
	// bounded even when callers explicitly grant a run more attempts.
	if r.Streak < 32 {
		r.Streak++
	}
	if r.Streak < 1 {
		r.Streak = 1
	}
	var e *Error
	errors.As(err, &e)
	r.RetryAfterSeconds = min(max(e.RetryAfterSeconds, 0), maxRetryAfterSeconds)
	return true
}

func availabilityRetryDelay(r AvailabilityRetry, sample uint64) time.Duration {
	if r.Streak <= 0 {
		return 0
	}
	base := time.Second << min(r.Streak-1, 3)
	// Equal jitter keeps every retry delayed while spreading concurrent callers
	// over [0.5, 1], [1, 2], [2, 4], then [4, 8] seconds.
	delay := base/2 + time.Duration(sample%(uint64(base/2)+1))
	serverDelay := time.Duration(min(max(r.RetryAfterSeconds, 0), maxRetryAfterSeconds)) * time.Second
	return max(delay, serverDelay)
}

func (r AvailabilityRetry) Wait(ctx context.Context, wait RetryWait) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	delay := availabilityRetryDelay(r, rand.Uint64())
	if delay == 0 {
		return nil
	}
	if wait != nil {
		if err := wait(ctx, delay); err != nil {
			return err
		}
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

func retryAfterSeconds(raw string, now time.Time) int {
	if len(raw) > 128 {
		return 0
	}
	raw = strings.TrimSpace(raw)
	if seconds, err := strconv.ParseUint(raw, 10, 64); err == nil {
		return int(min(seconds, uint64(maxRetryAfterSeconds)))
	}
	when, err := http.ParseTime(raw)
	if err != nil || !when.After(now) {
		return 0
	}
	delay := when.Sub(now)
	if delay >= maxRetryAfterSeconds*time.Second {
		return maxRetryAfterSeconds
	}
	return int((delay + time.Second - 1) / time.Second)
}

func providerHTTPError(status int, header http.Header) *Error {
	return &Error{Kind: "upstream_error", HTTPStatus: status, Retryable: status == 429 || status >= 500, BilledUnknown: true, RetryAfterSeconds: retryAfterSeconds(header.Get("Retry-After"), time.Now())}
}
