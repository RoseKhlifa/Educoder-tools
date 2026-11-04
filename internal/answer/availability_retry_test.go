package answer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAvailabilityRetryDelayIsProgressiveJitteredAndBounded(t *testing.T) {
	for streak := 1; streak <= 10; streak++ {
		base := time.Second << min(streak-1, 3)
		r := AvailabilityRetry{Streak: streak}
		low, high := availabilityRetryDelay(r, 0), availabilityRetryDelay(r, uint64(base/2))
		if low != base/2 || high != base || low == high || high > 8*time.Second {
			t.Fatalf("streak=%d delay range=%s..%s", streak, low, high)
		}
	}
	if got := availabilityRetryDelay(AvailabilityRetry{Streak: 1, RetryAfterSeconds: 12}, 0); got != 12*time.Second {
		t.Fatalf("server delay ignored: %s", got)
	}
	if got := availabilityRetryDelay(AvailabilityRetry{Streak: 32, RetryAfterSeconds: 9999}, 0); got != 60*time.Second {
		t.Fatalf("unbounded server delay: %s", got)
	}
	if got := availabilityRetryDelay(AvailabilityRetry{}, 0); got != 0 {
		t.Fatalf("nonretry was delayed: %s", got)
	}
}

func TestRetryAfterParsesSecondsAndHTTPDateWithoutRetainingRawText(t *testing.T) {
	now := time.Date(2026, 11, 3, 10, 0, 0, 500000000, time.UTC)
	for _, tc := range []struct {
		name, raw string
		want      int
	}{
		{"seconds", " 12 ", 12},
		{"zero", "0", 0},
		{"cap", "99999", 60},
		{"date", now.Add(12 * time.Second).Format(http.TimeFormat), 12},
		{"date cap", now.Add(time.Hour).Format(http.TimeFormat), 60},
		{"past date", now.Add(-time.Hour).Format(http.TimeFormat), 0},
		{"negative", "-2", 0},
		{"fractional", "1.5", 0},
		{"private junk", "private-provider-header", 0},
		{"too long", strings.Repeat("9", 129), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := retryAfterSeconds(tc.raw, now); got != tc.want {
				t.Fatalf("delay=%d want=%d", got, tc.want)
			}
		})
	}
}

func TestAvailabilityRetryWaitCancellationAndResets(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := AvailabilityRetry{}
	if !r.Observe(&Error{Kind: "upstream_error", HTTPStatus: 503, Retryable: true, BilledUnknown: true, RetryAfterSeconds: 12}) || r.Streak != 1 {
		t.Fatal("availability error was not observed")
	}
	waits := 0
	err := r.Wait(ctx, func(_ context.Context, delay time.Duration) error {
		waits++
		if delay != 12*time.Second {
			t.Fatalf("bounded server delay was ignored: %s", delay)
		}
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || waits != 1 || r.Streak != 1 {
		t.Fatalf("cancellation lost or state mutated: err=%v retry=%+v", err, r)
	}
	if err := r.Wait(ctx, func(context.Context, time.Duration) error { t.Fatal("wait entered after cancellation"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled wait=%v", err)
	}
	for _, err := range []error{nil, &Error{Kind: "truncated_output"}, &Error{Kind: "invalid_output_json"}, &Error{Kind: "provider_payment_required", HTTPStatus: http.StatusPaymentRequired, BilledUnknown: true}, &Error{Kind: "upstream_error", HTTPStatus: 401}, &Error{Kind: "response_read_failed", HTTPStatus: 200, Retryable: true}} {
		r = AvailabilityRetry{Streak: 4, RetryAfterSeconds: 60}
		if r.Observe(err) || r.Streak != 0 || r.RetryAfterSeconds != 0 {
			t.Fatalf("nonavailability error retained retry state: err=%v retry=%+v", err, r)
		}
	}
	// The real timer must also return immediately for an already canceled
	// context, even when the provider requested its maximum wait.
	if err := (AvailabilityRetry{Streak: 4, RetryAfterSeconds: 60}).Wait(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("timer ignored cancellation: %v", err)
	}
}

func TestAllProviderShapesCaptureOnlyBoundedRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "999999")
		http.Error(w, "private-provider-body", 503)
	}))
	defer server.Close()
	d, err := NewDeepSeek(Config{APIKey: "synthetic", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"programming", func() error { _, e := d.Generate(context.Background(), Request{Problem: testSnapshot()}); return e }},
		{"objective", func() error {
			_, e := d.GenerateObjective(context.Background(), ObjectiveRequest{Problem: objectiveFixture()})
			return e
		}},
		{"exercise", func() error {
			_, e := d.GenerateExercise(context.Background(), ExerciseRequest{Problem: exerciseFixture()})
			return e
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			var e *Error
			if !errors.As(err, &e) || e.RetryAfterSeconds != 60 || !e.BilledUnknown || !IsAvailabilityError(err) || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "999999") {
				t.Fatalf("provider metadata was missing or unsanitized: %+v", err)
			}
		})
	}
}
