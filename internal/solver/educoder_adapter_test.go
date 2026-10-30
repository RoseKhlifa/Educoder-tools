package solver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/problem"
)

func adapterFixture(environmentID int) map[string]any {
	return map[string]any{
		"st": 0, "homework_common_id": 1,
		"game":                map[string]any{"id": 3, "identifier": "g", "myshixun_id": 4},
		"myshixun":            map[string]any{"id": 4, "identifier": "instance"},
		"user":                map[string]any{"user_id": 5},
		"challenge":           map[string]any{"id": 6, "subject": "task", "st": 0, "with_code_file": true, "task_pass": "solve", "path": "main.cpp"},
		"code_editor":         map[string]any{"shixun_environment_id": environmentID, "name": "C/C++"},
		"shixun_environments": []any{map[string]any{"shixun_environment_id": environmentID, "tab_type": 1}},
		"test_sets":           []any{map[string]any{"is_public": true, "input": "public", "output": "yes"}, map[string]any{"is_public": false, "input": "private-input", "output": "private-output"}},
	}
}

func TestAdapterContractIsPortableAndExcludesPrivateTests(t *testing.T) {
	environmentID := 12
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/tasks/g.json" {
			json.NewEncoder(w).Encode(adapterFixture(environmentID))
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"filename": "main.cpp", "language": "cpp", "content": map[string]any{"content": base64.StdEncoding.EncodeToString([]byte("template"))}})
	}))
	defer server.Close()
	a, _ := NewEducoderAdapter(educoder.New(server.URL))
	one, err := a.Load(context.Background(), Target{"c", 1, "g"})
	if err != nil {
		t.Fatal(err)
	}
	environmentID = 99
	two, err := a.Load(context.Background(), Target{"c", 1, "g"})
	if err != nil {
		t.Fatal(err)
	}
	x, _ := problem.Prepare(one.Snapshot)
	y, _ := problem.Prepare(two.Snapshot)
	if x.ContractHash != y.ContractHash {
		t.Fatal("runtime environment ID polluted portable contract")
	}
	if len(x.PublicTests) != 1 || x.PublicTests[0].Stdin != "public" {
		t.Fatalf("private tests leaked: %+v", x.PublicTests)
	}
	if strings.Join(x.EnvironmentContract, ",") != "editor:c/c++,language:cpp" {
		t.Fatalf("contract=%v", x.EnvironmentContract)
	}
}

func TestAdapterConcurrentEditorChangePreventsEveryWrite(t *testing.T) {
	current := "template"
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			writes++
			http.Error(w, "unexpected write", http.StatusConflict)
			return
		}
		if r.URL.Path == "/api/tasks/g.json" {
			json.NewEncoder(w).Encode(adapterFixture(12))
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"filename": "main.cpp", "language": "cpp", "content": map[string]any{"content": base64.StdEncoding.EncodeToString([]byte(current))}})
	}))
	defer server.Close()
	a, _ := NewEducoderAdapter(educoder.New(server.URL))
	loaded, err := a.Load(context.Background(), Target{"c", 1, "g"})
	if err != nil {
		t.Fatal(err)
	}
	a.bindSnapshot(loaded)
	current = "concurrent user edit"
	if _, err := a.SelfTest(context.Background(), Target{"c", 1, "g"}, candidate("good").Files[0], "1", "ok"); err == nil || !strings.Contains(err.Error(), "changed concurrently") {
		t.Fatalf("selftest err=%v", err)
	}
	if _, err := a.Submit(context.Background(), Target{"c", 1, "g"}, candidate("good").Files[0]); err == nil || !strings.Contains(err.Error(), "changed concurrently") {
		t.Fatalf("submit err=%v", err)
	}
	if writes != 0 {
		t.Fatalf("concurrent edit overwritten by %d writes", writes)
	}
}

func TestAdapterFormalEvidenceUsesReadBackHash(t *testing.T) {
	current := "template"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tasks/g.json":
			json.NewEncoder(w).Encode(adapterFixture(12))
		case "/api/tasks/g/rep_content.json":
			json.NewEncoder(w).Encode(map[string]any{"filename": "main.cpp", "language": "cpp", "content": map[string]any{"content": base64.StdEncoding.EncodeToString([]byte(current))}})
		case "/api/myshixuns/instance/update_file.json":
			var body struct {
				Content string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			current = body.Content
			json.NewEncoder(w).Encode(map[string]any{"content": map[string]any{"commitID": "commit", "size": len(current)}, "sec_key": "fixture-only", "resubmit": "1"})
		case "/api/tasks/g/game_build.json":
			json.NewEncoder(w).Encode(map[string]any{"status": 1, "port": 1})
		case "/api/tasks/g/game_status.json":
			json.NewEncoder(w).Encode(map[string]any{"status": 2, "test_sets_count": 1, "sets_error_count": 0, "test_sets": []any{map[string]any{"is_public": true, "input": "1", "output": "ok", "result": true, "compile_success": 1}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	a, _ := NewEducoderAdapter(educoder.New(server.URL))
	loaded, err := a.Load(context.Background(), Target{"c", 1, "g"})
	if err != nil {
		t.Fatal(err)
	}
	a.bindSnapshot(loaded)
	r, err := a.Submit(context.Background(), Target{"c", 1, "g"}, candidate("good").Files[0])
	if err != nil {
		t.Fatal(err)
	}
	want, _ := problem.HashFiles(candidate("good").Files)
	if !r.Completed || !r.Passed || r.SavedFilesHash != want || r.SubmittedFilesHash != want {
		t.Fatalf("evidence=%+v", r)
	}
}
