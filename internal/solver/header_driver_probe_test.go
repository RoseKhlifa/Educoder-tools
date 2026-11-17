package solver

import (
	"context"
	"educoder-cli/internal/educoder"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestHeaderAdapterIncludesOnlyObservedSiblingDriverAsReadOnlyContext(t *testing.T) {
	fixture := adapterFixture(12)
	fixture["challenge"].(map[string]any)["path"] = "src/step/answer.h"
	var readFiles []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tasks/g.json":
			_ = json.NewEncoder(w).Encode(fixture)
		case "/api/myshixuns/instance/repository.json":
			var body struct {
				Path string `json:"path"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			switch body.Path {
			case "":
				fmt.Fprint(w, `{"trees":[{"name":"src","path":"src","type":"tree"},{"name":"test","path":"test","type":"tree"}]}`)
			case "src":
				fmt.Fprint(w, `{"trees":[{"name":"step","path":"src/step","type":"tree"},{"name":"private","path":"src/private","type":"tree"}]}`)
			case "src/step":
				fmt.Fprint(w, `{"trees":[{"name":"main.cpp","path":"src/step/main.cpp","type":"blob"},{"name":"answer.h","path":"src/step/answer.h","type":"blob"},{"name":"grader.cpp","path":"src/step/grader.cpp","type":"blob"}]}`)
			default:
				t.Errorf("unrelated directory queried: %q", body.Path)
				http.Error(w, "unexpected", 500)
			}
		case "/api/tasks/g/rep_content.json":
			file := r.URL.Query().Get("path")
			readFiles = append(readFiles, file)
			content, filename := "void solve();", "answer.h"
			if file == "src/step/main.cpp" {
				content, filename = "#include \"answer.h\"\nint main(){solve();}\n", "main.cpp"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"filename": filename, "language": "cpp", "content": map[string]any{"content": base64.StdEncoding.EncodeToString([]byte(content)), "size": len(content)}})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	a, _ := NewEducoderAdapter(educoder.New(server.URL))
	loaded, err := a.Load(context.Background(), Target{"c", 1, "g"})
	if err != nil {
		t.Fatal(err)
	}
	var observed struct {
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
		ReadOnly []struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		} `json:"read_only_files"`
	}
	raw, err := json.Marshal(loaded.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &observed); err != nil {
		t.Fatal(err)
	}
	if len(observed.Files) != 1 || observed.Files[0].Path != "src/step/answer.h" || len(observed.ReadOnly) != 1 || observed.ReadOnly[0].Path != "src/step/main.cpp" || observed.ReadOnly[0].Content != "#include \"answer.h\"\nint main(){solve();}\n" {
		t.Fatalf("missing public driver context or expanded writable files: %+v", observed)
	}
	if !reflect.DeepEqual(readFiles, []string{"src/step/answer.h", "src/step/main.cpp"}) {
		t.Fatalf("read unexpected file: %v", readFiles)
	}
}
