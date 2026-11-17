package educoder

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func supportFixture(t *testing.T) *Task {
	t.Helper()
	fixture := programmingTaskFixture("GAME1", 1)
	fixture["challenge"].(map[string]any)["path"] = "answer.h"
	return decodeTaskFixture(t, fixture)
}

func TestProgrammingSupportFixedDiagnosticStagesAndReasons(t *testing.T) {
	for _, tc := range []struct {
		name, root, status, stage, reason, filename string
		size                                        int
	}{
		{"missing-identifier", "", "unavailable", "repository_task", "missing_task_identifier", "", 0},
		{"no-driver", `{"trees":[]}`, "missing", "association", "no_matching_driver", "", 0},
		{"missing-trees", `{}`, "unavailable", "root", "missing_trees", "", 0},
		{"invalid-json", `{`, "unavailable", "root", "invalid_response", "", 0},
		{"http-failure", "HTTP403", "unavailable", "root", "unavailable_http", "", 0},
		{"filename-mismatch", "DRIVER", "unavailable", "driver", "filename_mismatch", "other.cpp", 0},
		{"content-size", "DRIVER", "unavailable", "driver", "content_size_mismatch", "main.cpp", 999},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := supportFixture(t)
			if tc.name == "missing-identifier" {
				task.MyShixun.Identifier = ""
			}
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if strings.HasSuffix(r.URL.Path, "repository.json") {
					if tc.root == "HTTP403" {
						w.WriteHeader(403)
						fmt.Fprint(w, `{"message":"sensitive sentinel https://example.invalid/?token=private"}`)
						return
					}
					if tc.root == "DRIVER" {
						fmt.Fprint(w, `{"trees":[{"name":"main.cpp","path":"main.cpp","type":"blob"}]}`)
					} else {
						fmt.Fprint(w, tc.root)
					}
					return
				}
				source := "#include \"answer.h\"\nint main(){}\n"
				_ = json.NewEncoder(w).Encode(map[string]any{"filename": tc.filename, "language": "cpp", "content": map[string]any{"content": base64.StdEncoding.EncodeToString([]byte(source)), "size": tc.size}})
			}))
			defer server.Close()
			files, d := New(server.URL).ReadProgrammingSupportWithStatus(context.Background(), task)
			if len(files) != 0 || d == nil || d.Status != tc.status || d.Stage != tc.stage || d.Reason != tc.reason {
				t.Fatalf("files=%v diagnostic=%+v", files, d)
			}
			raw, _ := json.Marshal(d)
			if strings.Contains(string(raw), "sentinel") || strings.Contains(string(raw), "https:") {
				t.Fatalf("raw upstream content leaked: %s", raw)
			}
			if tc.name == "missing-identifier" && requests != 0 {
				t.Fatal("invalid identifier reached upstream")
			}
			if tc.root == "HTTP403" && requests != 1 {
				t.Fatal("permanent HTTP response was retried")
			}
		})
	}
}

func TestProgrammingSupportTimeoutHasOneBoundedRetry(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); time.Sleep(60 * time.Millisecond) }))
	defer server.Close()
	client := New(server.URL)
	client.HTTP.Timeout = 20 * time.Millisecond
	started := time.Now()
	files, d := client.ReadProgrammingSupportWithStatus(context.Background(), supportFixture(t))
	if len(files) != 0 || d == nil || d.Stage != "root" || d.Reason != "unavailable_timeout" || requests.Load() != 2 || time.Since(started) > time.Second {
		t.Fatalf("files=%v diagnostic=%+v requests=%d elapsed=%v", files, d, requests.Load(), time.Since(started))
	}
	if ProgrammingSupportTimeout != 30*time.Second {
		t.Fatal("optional context total deadline regressed")
	}
}

func TestMatchingStemNamesAndActualMainAreRequired(t *testing.T) {
	for _, name := range []string{"answer.c", "answer.cc", "answer.cpp", "answer.cxx", "main.cc", "main.cxx"} {
		if !headerDriverName(name, []string{"answer.h"}) {
			t.Errorf("matching caller rejected: %s", name)
		}
	}
	for _, name := range []string{"other.cpp", "answer_test.cpp", "grader.cpp", "test.cpp", "answer.txt"} {
		if headerDriverName(name, []string{"answer.h"}) {
			t.Errorf("unrelated/evaluation artifact accepted: %s", name)
		}
	}
	for _, source := range []string{"int main();", "// int main(){}", `const char *s="int main(){}";`, "/* int main(){} */", `auto s=R"tag(int main(){})tag";`, "#define FAKE int main(){}", "#include \"answer.h\" /*\nint main(){}\n*/"} {
		if sourceHasMainDefinition(source) {
			t.Errorf("non-executable main accepted: %s", source)
		}
	}
	for _, source := range []string{"int main(){return 0;}", "int main(int argc,char **argv) {return 0;}", "auto main() -> int {return 0;}"} {
		if !sourceHasMainDefinition(source) {
			t.Errorf("actual main rejected: %s", source)
		}
	}
}

func TestProgrammingSupportNetworkFailureRetriesOnceWithoutRawError(t *testing.T) {
	requests := 0
	client := New("https://example.invalid")
	client.HTTP.Transport = programmingRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		return nil, fmt.Errorf("sensitive sentinel https://private.invalid/?token=secret")
	})
	files, d := client.ReadProgrammingSupportWithStatus(context.Background(), supportFixture(t))
	if len(files) != 0 || d == nil || d.Reason != "unavailable_network" || d.Stage != "root" || requests != 2 {
		t.Fatalf("files=%v diagnostic=%+v requests=%d", files, d, requests)
	}
	raw, _ := json.Marshal(d)
	if strings.Contains(string(raw), "sentinel") || strings.Contains(string(raw), "token") {
		t.Fatalf("unsafe diagnostic: %s", raw)
	}
}
