package answer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type responseReadFunc func([]byte) (int, error)

func (f responseReadFunc) Read(p []byte) (int, error) { return f(p) }

func TestProgrammingResponseReadFailureHasSanitizedDiagnostics(t *testing.T) {
	const privateMarker = "https://private.invalid/path?token=secret-response-marker"
	prefix := `{"private":"` + privateMarker
	for _, tc := range []struct {
		name   string
		err    error
		status int
		cause  string
	}{
		{"deadline", fmt.Errorf("%s: %w", privateMarker, context.DeadlineExceeded), 200, "deadline"},
		{"canceled", fmt.Errorf("%s: %w", privateMarker, context.Canceled), 200, "canceled"},
		{"incomplete body", fmt.Errorf("%s: %w", privateMarker, io.ErrUnexpectedEOF), 200, "unexpected_eof"},
		{"generic read", errors.New(privateMarker), 200, "read"},
		{"server error read", errors.New(privateMarker), 503, "read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := io.MultiReader(strings.NewReader(prefix), responseReadFunc(func([]byte) (int, error) { return 0, tc.err }))
			p, err := NewDeepSeek(Config{APIKey: "synthetic-key", BaseURL: "https://provider.invalid", HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(body), ContentLength: 400}, nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			result, err := p.Generate(context.Background(), Request{Problem: testSnapshot(), Profile: DefaultProfile()})
			var providerErr *Error
			if !errors.As(err, &providerErr) || providerErr.Kind != "response_read_failed" || providerErr.HTTPStatus != tc.status || !providerErr.BilledUnknown || providerErr.Retryable != (tc.status >= 500) {
				t.Fatalf("read failure classification: %#v", err)
			}
			if providerErr.ResponsePhase != "body" || providerErr.ReadCause != tc.cause || providerErr.BytesRead != int64(len(prefix)) || providerErr.ExpectedBytes != 400 {
				t.Fatalf("read metadata: %+v", providerErr)
			}
			if result.CostKnown || result.Usage.Known || len(result.Candidate.Files) != 0 {
				t.Fatalf("interrupted response treated as usable or settled: %+v", result)
			}
			wantMessage := fmt.Sprintf("model provider response_read_failed (HTTP %d) [phase=body cause=%s bytes_read=%d expected_bytes=400]", tc.status, tc.cause, len(prefix))
			if err.Error() != wantMessage || strings.Contains(err.Error(), privateMarker) || strings.Contains(err.Error(), "synthetic-key") {
				t.Fatalf("unsanitized read error: %q", err.Error())
			}
			if tc.status == 200 && IsRetryableError(err) {
				t.Fatal("interrupted successful response enabled another unknown-billing request")
			}
		})
	}
}

func TestProgrammingResponseBodyDeadlineKeepsUnknownBillingAndNoRetry(t *testing.T) {
	p, err := NewDeepSeek(Config{APIKey: "synthetic-key", BaseURL: "https://provider.invalid", Timeout: 10 * time.Millisecond, HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := responseReadFunc(func([]byte) (int, error) {
			<-r.Context().Done()
			return 0, errors.New("private transport detail")
		})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(body), ContentLength: -1}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.Generate(context.Background(), Request{Problem: testSnapshot(), Profile: DefaultProfile()})
	var providerErr *Error
	if !errors.As(err, &providerErr) || providerErr.Kind != "response_read_failed" || providerErr.ReadCause != "deadline" || providerErr.ResponsePhase != "body" || providerErr.ExpectedBytes != -1 || !providerErr.BilledUnknown || IsRetryableError(err) || result.Duration < 10*time.Millisecond {
		t.Fatalf("body deadline classification: result=%+v err=%+v", result, providerErr)
	}
}

func TestProgrammingResponseSizeBoundary(t *testing.T) {
	valid := responseFor(`{"kind":"programming","language":"cpp","files":[{"path":"src/main.cpp","content":"int main(){}"}]}`, "stop")
	for _, extra := range []int{0, 1} {
		t.Run(fmt.Sprintf("extra_%d", extra), func(t *testing.T) {
			body := valid + strings.Repeat(" ", int(maxProgrammingResponseBytes)-len(valid)+extra)
			p, err := NewDeepSeek(Config{APIKey: "synthetic-key", BaseURL: "https://provider.invalid", HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}, nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			result, err := p.Generate(context.Background(), Request{Problem: testSnapshot(), Profile: DefaultProfile()})
			if extra == 0 {
				if err != nil || !result.CostKnown || len(result.Candidate.Files) != 1 {
					t.Fatalf("valid response at exact limit rejected: result=%+v err=%v", result, err)
				}
				return
			}
			var providerErr *Error
			if !errors.As(err, &providerErr) || providerErr.Kind != "response_too_large" || !providerErr.BilledUnknown || IsRetryableError(err) || providerErr.ResponsePhase != "body" || providerErr.BytesRead != maxProgrammingResponseBytes+1 || providerErr.ExpectedBytes != int64(len(body)) || result.CostKnown || len(result.Candidate.Files) != 0 {
				t.Fatalf("oversized response classification: result=%+v err=%+v", result, providerErr)
			}
		})
	}
}

func TestProviderErrorBodyMetadataUsesFixedEnumsOnly(t *testing.T) {
	err := &Error{Kind: "response_read_failed", HTTPStatus: 200, ResponsePhase: "body", ReadCause: "private-response-body https://private.invalid/token", BytesRead: -20, ExpectedBytes: -30}
	if got, want := err.Error(), "model provider response_read_failed (HTTP 200) [phase=body cause=read bytes_read=0 expected_bytes=-1]"; got != want {
		t.Fatalf("metadata did not sanitize unsupported values: %q", got)
	}
	err.ResponsePhase = "private-response-body"
	if got := err.Error(); got != "model provider response_read_failed (HTTP 200)" {
		t.Fatalf("unknown response phase escaped: %q", got)
	}
}
