package solver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"educoder-cli/internal/educoder"
)

func TestAdapterFindsSameStemCallerWithoutExpandingWritableFiles(t *testing.T) {
	fixture := adapterFixture(12)
	fixture["challenge"].(map[string]any)["path"] = "answer.h"
	var reads []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tasks/g.json":
			_ = json.NewEncoder(w).Encode(fixture)
		case "/api/myshixuns/instance/repository.json":
			fmt.Fprint(w, `{"trees":[{"name":"answer.cpp","path":"answer.cpp","type":"blob"},{"name":"test.cpp","path":"test.cpp","type":"blob"},{"name":"grader.cpp","path":"grader.cpp","type":"blob"},{"name":"other.cpp","path":"other.cpp","type":"blob"}]}`)
		case "/api/tasks/g/rep_content.json":
			file := r.URL.Query().Get("path")
			reads = append(reads, file)
			source := "void solve();"
			if file == "answer.cpp" {
				source = "#include \"answer.h\"\nint main(){solve();}\n"
			} else if file != "answer.h" {
				t.Errorf("read unrelated file: %s", file)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"filename": file, "language": "cpp", "content": map[string]any{"content": base64.StdEncoding.EncodeToString([]byte(source)), "size": len(source)}})
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer server.Close()
	a, _ := NewEducoderAdapter(educoder.New(server.URL))
	loaded, err := a.Load(context.Background(), Target{"c", 1, "g"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(loaded.Snapshot)
	var observed struct {
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
		ReadOnly []struct {
			Path string `json:"path"`
		} `json:"read_only_files"`
		Diagnostic struct {
			Status string `json:"status"`
			Stage  string `json:"stage"`
			Reason string `json:"reason"`
		} `json:"read_only_context"`
	}
	if json.Unmarshal(raw, &observed) != nil || len(observed.Files) != 1 || observed.Files[0].Path != "answer.h" || len(observed.ReadOnly) != 1 || observed.ReadOnly[0].Path != "answer.cpp" || observed.Diagnostic.Status != "available" || observed.Diagnostic.Reason != "observed_driver" {
		t.Fatalf("same-stem driver or fixed diagnostic missing: %s", raw)
	}
	if !reflect.DeepEqual(reads, []string{"answer.h", "answer.cpp"}) {
		t.Fatalf("unexpected source reads: %v", reads)
	}
}
