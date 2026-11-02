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
	"time"

	"github.com/coder/websocket"
)

func TestSelfTestRecordsContentFreeStreamEvidence(t *testing.T) {
	unicodeOutput := []byte("图书\r\n")
	for _, test := range []struct {
		name       string
		chunks     []string
		finish     bool
		rawFinish  bool
		expected   string
		limit      int64
		wantOutput string
		wantFrames int
		wantBytes  int64
		wantSeen   bool
		wantDone   bool
		wantMatch  bool
		wantErr    bool
	}{
		{name: "completion-without-output", finish: true, expected: "expected\n", wantSeen: true, wantDone: true},
		{name: "empty-output-may-match", finish: true, wantSeen: true, wantDone: true, wantMatch: true},
		{name: "empty-log-frame", chunks: []string{""}, finish: true, expected: "expected\n", wantFrames: 1, wantSeen: true, wantDone: true},
		{name: "unicode-across-frames", chunks: []string{string(unicodeOutput[:2]), string(unicodeOutput[2:])}, finish: true, expected: "图书\n", wantOutput: string(unicodeOutput), wantFrames: 2, wantBytes: int64(len(unicodeOutput)), wantSeen: true, wantDone: true, wantMatch: true},
		{name: "closed-with-partial-output", chunks: []string{"partial"}, wantOutput: "partial", wantFrames: 1, wantBytes: 7, wantErr: true},
		{name: "bare-marker-is-observed-but-not-completion", rawFinish: true, wantSeen: true, wantErr: true},
		{name: "observed-bytes-include-over-limit-frame", chunks: []string{"long"}, limit: 3, wantFrames: 1, wantBytes: 4, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/update_file.json"):
					fmt.Fprint(w, `{"content":{"commitID":"fixture-commit"}}`)
				case strings.HasSuffix(r.URL.Path, "/log_output"):
					json.NewEncoder(w).Encode(map[string]any{"status": 0, "data": map[string]string{"wss_url": strings.Replace(server.URL, "http://", "ws://", 1)}})
				case r.URL.Path == "/log":
					conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
					if err != nil {
						return
					}
					defer conn.CloseNow()
					ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
					defer cancel()
					if _, _, err := conn.Read(ctx); err != nil {
						return
					}
					for _, chunk := range test.chunks {
						if err := conn.Write(ctx, websocket.MessageBinary, []byte(base64.StdEncoding.EncodeToString([]byte(chunk)))); err != nil {
							return
						}
					}
					if test.finish || test.rawFinish {
						marker := "ProcessFinished"
						if !test.rawFinish {
							marker = base64.RawStdEncoding.EncodeToString([]byte(marker))
						}
						if err := conn.Write(ctx, websocket.MessageText, []byte(marker)); err != nil {
							return
						}
					}
					conn.Close(websocket.StatusNormalClosure, "")
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client := New(server.URL)
			task := decodeTaskFixture(t, programmingTaskFixture("GAME1", 1))
			result, err := client.SelfTest(context.Background(), task, "src/第1关.cpp", "", "", SelfTestOptions{Timeout: 2 * time.Second, MaxOutputBytes: test.limit, ExpectedOutput: &test.expected})
			if (err != nil) != test.wantErr || result == nil {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if result.LogFrames != test.wantFrames || result.OutputBytes != test.wantBytes || result.ProcessFinishedSeen != test.wantSeen || result.Completed != test.wantDone || result.Output != test.wantOutput {
				t.Fatalf("unexpected stream evidence: %+v", result)
			}
			if test.wantDone {
				if result.Matches == nil || *result.Matches != test.wantMatch {
					t.Fatalf("completion/match semantics changed: %+v", result)
				}
			} else if result.Matches != nil {
				t.Fatalf("unfinished stream produced an output verdict: %+v", result)
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"log_frames", "output_bytes", "process_finished_seen"} {
				if _, exists := fields[key]; !exists {
					t.Fatalf("zero-valued stream evidence was omitted: %s", encoded)
				}
			}
		})
	}
}
