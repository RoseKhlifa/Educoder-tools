package solver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"educoder-cli/internal/educoder"
)

func TestAdapterDoesNotTurnHiddenEmptyPlaceholderIntoStdoutOracle(t *testing.T) {
	fixture := adapterFixture(12)
	fixture["test_sets"] = []any{
		map[string]any{"is_public": true, "input": "", "output": ""},
		map[string]any{"is_public": false, "input": "private", "output": "private"},
		map[string]any{"is_public": true, "input": "", "output": "hello"},
		map[string]any{"is_public": true, "input": "silence", "output": ""},
		map[string]any{"is_public": true, "input": " ", "output": "\n"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("discovery caused mutation: %s", r.Method)
		}
		if r.URL.Path == "/api/tasks/g.json" {
			_ = json.NewEncoder(w).Encode(fixture)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"filename": "main.cpp", "language": "cpp", "content": map[string]any{"content": base64.StdEncoding.EncodeToString([]byte("template"))}})
	}))
	defer server.Close()
	adapter, _ := NewEducoderAdapter(educoder.New(server.URL))
	loaded, err := adapter.Load(context.Background(), Target{"c", 1, "g"})
	if err != nil {
		t.Fatal(err)
	}
	tests := loaded.Snapshot.PublicTests
	if len(tests) != 3 || tests[0].Stdin != "" || tests[0].ExpectedStdout != "hello" || tests[1].Stdin != "silence" || tests[1].ExpectedStdout != "" || tests[2].Stdin != " " || tests[2].ExpectedStdout != "\n" {
		t.Fatalf("empty placeholder treated as an oracle or meaningful blank data discarded: %+v", tests)
	}
}
