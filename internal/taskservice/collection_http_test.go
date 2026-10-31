package taskservice

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"educoder-cli/internal/educoder"
)

func TestCollectionHTTPUsesOnlyExistingReadRoutes(t *testing.T) {
	for _, kind := range []string{"objective", "programming"} {
		t.Run(kind, func(t *testing.T) {
			h, f, owner := collectionTestSetup(t)
			if kind == "programming" {
				installCollectionCode(t, h, f, owner, "G", true)
			}
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "GET" {
					t.Error("write attempted")
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/users/demo/courses.json":
					if r.URL.Query().Get("status") == "processing" && r.URL.Query().Get("page") == "1" {
						json.NewEncoder(w).Encode(map[string]any{"courses": []any{map[string]any{"id": 1, "first_category_url": "/classrooms/C/announcement"}}})
					} else {
						w.Write([]byte(`{"courses":[]}`))
					}
				case "/api/courses/C/homework_commons.json":
					if r.URL.Query().Has("type") {
						t.Error("status/type narrowed")
					}
					if r.URL.Query().Get("page") == "1" {
						w.Write([]byte(`{"homeworks":[{"homework_id":1,"is_shixun":true,"shixun_identifier":"S","challenge_count":1}]}`))
					} else {
						w.Write([]byte(`{"homeworks":[]}`))
					}
				case "/api/homework_commons/1/shixun_challenge_data.json":
					json.NewEncoder(w).Encode(map[string]any{"data": f.progress})
				case "/api/shixun_homeworks/1/header_info.json":
					// Discovery can report an unavailable runtime and still use
					// this test's prior known target. New runtime mapping is tested
					// independently with a fresh bank and real locator responses.
					w.Write([]byte(`{"data":{"homework_id":1}}`))
				case "/api/tasks/G.json":
					if r.URL.Query().Get("homework_common_id") != "1" {
						t.Error("missing scope")
					}
					json.NewEncoder(w).Encode(f.task)
				case "/api/tasks/G/rep_content.json":
					if kind != "programming" {
						t.Error("objective reached code")
					}
					if r.URL.Query().Get("path") != "include/task.h" {
						t.Error("lost file contract")
					}
					json.NewEncoder(w).Encode(map[string]any{"content": map[string]any{"content": base64.StdEncoding.EncodeToString([]byte(f.files[0].Content)), "size": len(f.files[0].Content)}, "filename": "task.h", "language": "C++"})
				default:
					t.Errorf("unreviewed route %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer upstream.Close()
			client := educoder.New(upstream.URL)
			client.LoginName = "demo"
			reader, e := client.CollectionReader(educoder.CollectionBudget{Requests: 40, ResponseBytes: 2 << 20, TotalBytes: 4 << 20})
			if e != nil {
				t.Fatal(e)
			}
			s, e := h.Scan(context.Background(), reader, owner, nil)
			if e != nil {
				t.Fatal(e)
			}
			if kind == "objective" && s.Collected != 1 || kind == "programming" && s.Duplicates != 1 {
				t.Fatalf("%+v", s)
			}
			if calls.Load() == 0 || calls.Load() > 15 {
				t.Fatal(calls.Load())
			}
			// Account metadata and protocol credentials never enter collection state.
			b, _ := json.Marshal(s)
			if strings.Contains(string(b), "demo") || strings.Contains(string(b), "sec_key") {
				t.Fatal(string(b))
			}
		})
	}
}
