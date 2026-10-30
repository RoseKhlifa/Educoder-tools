package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestModelAuditRetainsUsageWithoutResponseContentOrCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	body := `{"id":"request-1","model":"model-1","message":"secret-provider-error","choices":[{"finish_reason":"length","message":{"content":"candidate-source-private","reasoning_content":"reasoning-private"}}],"usage":{"prompt_tokens":20,"completion_tokens":30,"total_tokens":50}}`
	audit := &auditTransport{file: path, base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://example.invalid/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer secret-key-canary")
	resp, err := audit.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	forwarded, _ := io.ReadAll(resp.Body)
	if string(forwarded) != body {
		t.Fatal("auditing changed the provider response")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret-key-canary", "secret-provider-error", "candidate-source-private", "reasoning-private"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("audit included private content %q", secret)
		}
	}
	var record struct {
		Model  string `json:"model"`
		Finish string `json:"finish_reason"`
		Usage  struct {
			Total int `json:"total_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(data, &record) != nil || record.Model != "model-1" || record.Finish != "length" || record.Usage.Total != 50 {
		t.Fatalf("billed truncated usage lost: %s", data)
	}
}
