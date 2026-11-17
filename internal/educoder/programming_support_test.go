package educoder

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHeaderCallerAssociationIsConservative(t *testing.T) {
	for _, tc := range []struct {
		source string
		want   bool
	}{
		{"#include \"answer.h\"\nint main(){}", true},
		{"#include <stdio.h>\n#include \"./answer.h\"\nint main(){}", true},
		{"#include \"other.h\"\nint main(){}", false},
		{"#include \"answer.h\"\n#include \"other.h\"\nint main(){}", false},
		{"#include \"../answer.h\"\nint main(){}", false},
		{"#include \"another/answer.h\"\nint main(){}", false},
		{"/*\n#include \"answer.h\"\n*/\nint main(){}", false},
		{"#include <stdio.h> /*\n#include \"answer.h\"\n*/\nint main(){}", false},
		{"// comment \\\n#include \"answer.h\"\nint main(){}", false},
		{"const char *s = R\"tag(\n#include \"answer.h\"\n)tag\";\nint main(){}", false},
		{"const char *s = \"\\\n#include \\\"answer.h\\\"\";\nint main(){}", false},
		{"#if 0\n#include \"answer.h\"\n#endif\nint main(){}", false},
		{"/* comment */ # include \"answer.h\"\nint main(){}", true},
	} {
		if got := headerCallerMatches(tc.source, []string{"answer.h"}); got != tc.want {
			t.Errorf("association=%v want=%v source=%q", got, tc.want, tc.source)
		}
	}
}

func TestProgrammingSupportNeverProvidesMissingOversizedOrMismatchedContent(t *testing.T) {
	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"missing-content", map[string]any{"size": 20}},
		{"inconsistent-size", map[string]any{"content": base64.StdEncoding.EncodeToString([]byte("#include \"answer.h\"")), "size": 999}},
		{"oversized", map[string]any{"content": base64.StdEncoding.EncodeToString([]byte("#include \"answer.h\"\n" + strings.Repeat(" ", 64<<10)))}},
		{"invalid-utf8", map[string]any{"content": base64.StdEncoding.EncodeToString([]byte{255})}},
		{"empty-content", map[string]any{"content": ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := programmingTaskFixture("GAME1", 1)
			fixture["challenge"].(map[string]any)["path"] = "answer.h"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/myshixuns/MY1/repository.json":
					fmt.Fprint(w, `{"trees":[{"name":"main.cpp","path":"main.cpp","type":"blob"}]}`)
				case "/api/tasks/GAME1/rep_content.json":
					_ = json.NewEncoder(w).Encode(map[string]any{"filename": "main.cpp", "language": "cpp", "content": tc.body})
				default:
					t.Errorf("unexpected %s", r.URL.Path)
				}
			}))
			defer server.Close()
			files, err := New(server.URL).ReadProgrammingSupport(context.Background(), decodeTaskFixture(t, fixture))
			if err == nil || len(files) != 0 {
				t.Fatalf("incomplete caller escaped: files=%v err=%v", files, err)
			}
		})
	}
}

func TestProgrammingSupportNonHeaderDoesNotListRepository(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("non-header requested extra context: %s", r.URL.Path)
	}))
	defer server.Close()
	files, err := New(server.URL).ReadProgrammingSupport(context.Background(), decodeTaskFixture(t, programmingTaskFixture("GAME1", 1)))
	if err != nil || len(files) != 0 {
		t.Fatalf("files=%v err=%v", files, err)
	}
}
