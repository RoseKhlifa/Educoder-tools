package educoder

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRepositoryReadsSupportingCallerWithoutGrantingWrites(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/myshixuns/MY1/repository.json":
			if r.Method != http.MethodPost || r.Header.Get("X-EDU-Signature") == "" {
				t.Fatal("repository listing did not use observed signed POST")
			}
			var body struct {
				Path string `json:"path"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			switch body.Path {
			case "":
				fmt.Fprint(w, `{"trees":[{"name":"src","path":"src","type":"tree"}]}`)
			case "src":
				fmt.Fprint(w, `{"trees":[{"name":"main.cpp","path":"src/main.cpp","type":"blob"}]}`)
			default:
				t.Fatalf("unexpected directory: %q", body.Path)
			}
		case "/api/tasks/GAME1/rep_content.json":
			if r.Method != http.MethodGet || r.URL.Query().Get("path") != "src/main.cpp" || r.URL.Query().Get("homework_common_id") != "99" {
				t.Fatal("wrong repository read context")
			}
			json.NewEncoder(w).Encode(map[string]any{"filename": "main.cpp", "language": "cpp", "file_type": "txt", "content": map[string]any{"content": base64.StdEncoding.EncodeToString([]byte("#include \"answer.h\"\nint main() { return 0; }\n")), "size": 47}})
		default:
			t.Fatalf("unexpected route: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	c := New(server.URL)
	task := decodeTaskFixture(t, programmingTaskFixture("GAME1", 1))
	ctx := context.Background()
	root, err := c.ListRepository(ctx, task, nil)
	if err != nil || len(root) != 1 {
		t.Fatalf("root=%v err=%v", root, err)
	}
	entries, err := c.ListRepository(ctx, task, &root[0])
	if err != nil || len(entries) != 1 {
		t.Fatalf("children=%v err=%v", entries, err)
	}
	file, err := c.ReadRepositoryFile(ctx, task, entries[0])
	if err != nil || file.Filename != "main.cpp" || file.Extension != ".cpp" || file.Content != "#include \"answer.h\"\nint main() { return 0; }\n" {
		t.Fatalf("file=%+v err=%v", file, err)
	}
	before := requests
	if _, err := c.SaveCode(ctx, task, file.Path, "changed", false); err == nil {
		t.Error("supporting source was granted save access")
	}
	forged := entries[0]
	forged.Path = "src/private.cpp"
	if _, err := c.ReadRepositoryFile(ctx, task, forged); err == nil {
		t.Error("mutated listing path accepted")
	}
	if _, err := c.ReadRepositoryFile(ctx, task, RepositoryEntry{Path: "src/main.cpp", Type: "blob"}); err == nil {
		t.Error("unlisted path accepted")
	}
	other := *task
	other.Game.Identifier = "GAME2"
	if _, err := c.ReadRepositoryFile(ctx, &other, entries[0]); err == nil {
		t.Error("other task listing accepted")
	}
	if _, err := New(server.URL).ReadRepositoryFile(ctx, task, entries[0]); err == nil {
		t.Error("other session listing accepted")
	}
	if _, err := c.ListRepository(ctx, task, &entries[0]); err == nil {
		t.Error("blob treated as directory")
	}
	if requests != before {
		t.Errorf("invalid reads/writes reached upstream: %d additional requests", requests-before)
	}
}

func TestRepositoryRejectsTraversalAndUpstreamDenial(t *testing.T) {
	for _, tree := range []string{
		`{"trees":[{"name":"file","path":"../file","type":"blob"}]}`,
		`{"trees":[{"name":"file","path":"/file","type":"blob"}]}`,
		`{"trees":[{"name":"file","path":"src/file","type":"blob"}]}`,
		`{"trees":[{"name":"file","path":"file","type":"unknown"}]}`,
		`{"status":403,"message":"not available"}`,
	} {
		t.Run(tree, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tree) }))
			defer server.Close()
			c := New(server.URL)
			task := decodeTaskFixture(t, programmingTaskFixture("GAME1", 1))
			if _, err := c.ListRepository(context.Background(), task, nil); err == nil {
				t.Error("unexpected repository content accepted")
			}
		})
	}
}
