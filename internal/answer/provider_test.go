package answer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"educoder-cli/internal/problem"
)

func testSnapshot() problem.Snapshot {
	return problem.Snapshot{Kind: "programming", Title: "sum", StatementMarkdown: "read two numbers", Files: []problem.File{{Path: "src/main.cpp", Language: "cpp", Extension: ".cpp", CurrentContent: "int main() { return 0; }"}}, PublicTests: []problem.PublicTest{{Stdin: "1 2\n", ExpectedStdout: "3\n"}}, SnapshotCompleteness: "complete"}
}

func responseFor(content, finish string) string {
	b, _ := json.Marshal(map[string]any{"id": "req-test", "model": "deepseek-flash", "choices": []any{map[string]any{"finish_reason": finish, "message": map[string]string{"content": content}}}, "usage": map[string]any{"prompt_tokens": 20, "completion_tokens": 15, "total_tokens": 35, "prompt_cache_hit_tokens": 3, "prompt_cache_miss_tokens": 17, "completion_tokens_details": map[string]int{"reasoning_tokens": 2}}})
	return string(b)
}

func TestDeepSeekWireAndStrictCandidate(t *testing.T) {
	var got wireRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Errorf("authorization header missing")
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(responseFor(`{"kind":"programming","language":"cpp","files":[{"path":"src/main.cpp","content":"#include <iostream>\nint main(){std::cout << 3 << '\\n';}\n"}]}`, "stop")))
	}))
	defer server.Close()
	p, err := NewDeepSeek(Config{APIKey: "test-secret", BaseURL: server.URL, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.Generate(context.Background(), Request{Problem: testSnapshot(), Profile: DefaultProfile()})
	if err != nil {
		t.Fatal(err)
	}
	if got.ResponseFormat.Type != "json_object" || got.Thinking["type"] != "disabled" || got.MaxTokens != 4096 || got.Stream {
		t.Fatalf("wire defaults not preserved: %#v", got)
	}
	if !strings.Contains(got.Messages[0].Content.textOnly(), "only for tasks classified as programming") || !strings.Contains(got.Messages[0].Content.textOnly(), "turn a header into a source file") || !strings.Contains(got.Messages[0].Content.textOnly(), "exact filename and extension") {
		t.Fatalf("prompt does not make suffix semantics explicit: %s", got.Messages[0].Content.textOnly())
	}
	if got.Model != "deepseek-flash" || result.ProviderRequestID != "req-test" || result.Usage.CacheHitTokens != 3 || result.Usage.ReasoningTokens != 2 {
		t.Fatalf("metadata not preserved: %#v", result)
	}
	var userPayload map[string]any
	if err := json.Unmarshal([]byte(got.Messages[1].Content.textOnly()), &userPayload); err != nil {
		t.Fatal(err)
	}
	contracts, ok := userPayload["file_contract"].([]any)
	if !ok || len(contracts) != 1 {
		t.Fatalf("file contract missing from model payload: %#v", userPayload["file_contract"])
	}
	contract := contracts[0].(map[string]any)
	if contract["filename"] != "main.cpp" && contract["filename"] != "src/main.cpp" {
		t.Fatalf("filename not forwarded: %#v", contract)
	}
	if contract["extension"] != ".cpp" || contract["language"] != "cpp" {
		t.Fatalf("file type not forwarded: %#v", contract)
	}
	if result.Candidate.Files[0].Content == "" {
		t.Fatal("candidate missing")
	}
}

func TestStrictCandidateRejectsUnknownDuplicateAndPath(t *testing.T) {
	s := testSnapshot()
	for _, content := range []string{
		`{"kind":"programming","language":"cpp","files":[{"path":"src/main.cpp","content":"x","extra":1}]}`,
		`{"kind":"programming","language":"cpp","files":[{"path":"src/main.cpp","content":"x"},{"path":"src/main.cpp","content":"y"}]}`,
		`{"kind":"programming","language":"cpp","files":[{"path":"other.cpp","content":"x"}]}`,
		`{"kind":"programming","language":"cpp","files":[{"path":"src/main.cpp","content":"x"}],"kind":"programming"}`,
	} {
		if _, err := parseCandidate(s, content); err == nil {
			t.Fatalf("accepted malformed candidate: %s", content)
		}
	}
}

func TestOnlyCompleteJSONFenceIsStripped(t *testing.T) {
	s := testSnapshot()
	valid := `{"kind":"programming","language":"cpp","files":[{"path":"src/main.cpp","content":"int main(){}"}]}`
	fence := strings.Repeat(string(rune(96)), 3)
	if _, err := parseCandidate(s, fence+"json\n"+valid+"\n"+fence); err != nil {
		t.Fatalf("fenced JSON: %v", err)
	}
	if _, err := parseCandidate(s, "prefix "+valid); err == nil {
		t.Fatal("accepted non-fence prefix")
	}
}

func TestTruncatedAndHTTPFailures(t *testing.T) {
	for _, status := range []int{429, 500} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "provider detail must not escape", status) }))
		p, _ := NewDeepSeek(Config{APIKey: "secret", BaseURL: server.URL})
		_, err := p.Generate(context.Background(), Request{Problem: testSnapshot(), Profile: DefaultProfile()})
		server.Close()
		pe, ok := err.(*Error)
		if !ok || pe.HTTPStatus != status || !pe.BilledUnknown || !pe.Retryable {
			t.Fatalf("status %d error: %#v", status, err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(responseFor(`{"kind":"programming","language":"cpp","files":[]}`, "length")))
	}))
	p, _ := NewDeepSeek(Config{APIKey: "secret", BaseURL: server.URL})
	result, err := p.Generate(context.Background(), Request{Problem: testSnapshot(), Profile: DefaultProfile()})
	server.Close()
	if result.FinishReason != "length" || err == nil || !strings.Contains(err.Error(), "truncated_output") {
		t.Fatalf("truncation: %#v %#v", result, err)
	}
}

func TestTimeoutIsClassifiedWithoutResponseDetails(t *testing.T) {
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	p, _ := NewDeepSeek(Config{APIKey: "secret-key", BaseURL: "http://provider.invalid", HTTPClient: &http.Client{Transport: rt}, Timeout: 10 * time.Millisecond})
	_, err := p.Generate(context.Background(), Request{Problem: testSnapshot(), Profile: DefaultProfile()})
	pe, ok := err.(*Error)
	if !ok || pe.Kind != "timeout" || !pe.Retryable || !pe.BilledUnknown || strings.Contains(err.Error(), "secret-key") {
		t.Fatalf("timeout classification: %#v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestEstimateRequiresConfiguredPrice(t *testing.T) {
	p, _ := NewDeepSeek(Config{APIKey: "secret", BaseURL: "http://127.0.0.1", Prices: map[string]Pricing{}})
	_, err := p.Estimate(Request{Problem: testSnapshot(), Profile: DefaultProfile()})
	pe, ok := err.(*Error)
	if !ok || pe.Kind != "price_unavailable" {
		t.Fatalf("estimate error: %#v", err)
	}
}
