package educoder

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRepositoryNameOnlyEntriesUseExactRequestedDirectory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Path string `json:"path"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch body.Path {
		case "":
			fmt.Fprint(w, `{"trees":[{"name":"step","type":"tree"}]}`)
		case "step":
			fmt.Fprint(w, `{"trees":[{"name":"answer.cpp","type":"blob"}]}`)
		default:
			t.Errorf("unexpected directory: %q", body.Path)
		}
	}))
	defer server.Close()
	client := New(server.URL)
	task := supportFixture(t)
	root, err := client.ListRepository(context.Background(), task, nil)
	if err != nil || len(root) != 1 || root[0].Path != "step" {
		t.Fatalf("root=%v err=%v", root, err)
	}
	children, err := client.ListRepository(context.Background(), task, &root[0])
	if err != nil || len(children) != 1 || children[0].Path != "step/answer.cpp" {
		t.Fatalf("children=%v err=%v", children, err)
	}
}

func TestRepositoryNameOnlyNeverRepairsExplicitConflictOrTraversal(t *testing.T) {
	for _, entry := range []string{
		`{"name":"answer.cpp","path":"other.cpp","type":"blob"}`,
		`{"name":"answer.cpp","path":"","type":"blob"}`,
		`{"name":"answer.cpp","path":null,"type":"blob"}`,
		`{"name":"../answer.cpp","type":"blob"}`,
		`{"name":"a/b.cpp","type":"blob"}`,
		`{"name":"","type":"blob"}`,
		`{"name":"a\\b.cpp","type":"blob"}`,
	} {
		t.Run(entry, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprintf(w, `{"trees":[%s]}`, entry) }))
			defer server.Close()
			if rows, err := New(server.URL).ListRepository(context.Background(), supportFixture(t), nil); err == nil || len(rows) > 0 {
				t.Fatalf("invalid entry escaped: %v %v", rows, err)
			}
		})
	}
}
