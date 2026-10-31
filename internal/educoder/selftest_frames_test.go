package educoder

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestSelfTestWireFrameCompatibility(t *testing.T) {
	const finished = "UHJvY2Vzc0ZpbmlzaGVk"
	for _, test := range []struct {
		name    string
		frames  []string
		want    string
		wantErr string
	}{
		// These two exact server payloads are present in the captured /log
		// exchange. They are application Base64, not HAR's binary encoding.
		{name: "recorded-unpadded-base64", frames: []string{"WUVTDQo", finished}, want: "YES\r\n"},
		// The captured task bundle's js-base64 decoder normalizes '-'/'_'
		// before decoding. Unlike js-base64, we reject other punctuation.
		{name: "url-safe-alphabet", frames: []string{"Pj4-Pz8_", finished}, want: ">>>???"},
		// A live read-only probe of the failing task returned URL-safe '-'
		// inside a UTF-8 stdout frame. This equivalent synthetic payload has no
		// user/task data, but exercises the same wire bytes and final decoding.
		{name: "url-safe-multibyte-stdout", frames: []string{"6K-E5rWL5oiQ5YqfDQo", finished}, want: "评测成功\r\n"},
		{name: "url-safe-multibyte-and-both-alphabet-symbols", frames: []string{"6K-E5rWL5oiQ5YqfDQo-Pj4_Pz8", finished}, want: "评测成功\r\n>>>???"},
		{name: "ascii-whitespace", frames: []string{" \tWUVT\r\nDQo=\f", finished}, want: "YES\r\n"},
		{name: "malformed-encoded-frame-does-not-become-stdout", frames: []string{"WUVTDQo", "not%base64", finished}, want: "YES\r\n", wantErr: "无效编码帧"},
		{name: "runtime-error-is-not-stdout", frames: []string{"WUVTDQo", `{"tp":"error","data":"private-runtime-details"}`}, want: "YES\r\n", wantErr: "运行器返回错误帧"},
		{name: "damaged-base64-cannot-signal-completion", frames: []string{"WUVTDQo", finished + "!"}, want: "YES\r\n", wantErr: "无效编码帧"},
		{name: "plain-stdout-does-not-bypass-base64", frames: []string{"YES\r\n!", finished}, wantErr: "无效编码帧"},
		{name: "unencoded-control-is-not-completion", frames: []string{"WUVTDQo", "ProcessFinished"}, want: "YES\r\n", wantErr: "未编码的结束控制帧"},
		{name: "decoded-utf8-remains-required", frames: []string{"_w==", finished}, want: string([]byte{0xff}), wantErr: "不是有效的 UTF-8"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var submissions atomic.Int32
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "update_file.json"):
					fmt.Fprint(w, `{"content":{"commitID":"commit"}}`)
				case strings.HasSuffix(r.URL.Path, "game_build.json"):
					submissions.Add(1)
					http.Error(w, "self-test must not submit", http.StatusBadRequest)
				case strings.HasSuffix(r.URL.Path, "/log_output"):
					json.NewEncoder(w).Encode(map[string]any{"status": 0, "data": map[string]any{"wss_url": strings.Replace(server.URL, "http://", "ws://", 1)}})
				case r.URL.Path == "/log":
					conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
					if err != nil {
						return
					}
					defer conn.CloseNow()
					ctx, cancel := context.WithTimeout(r.Context(), time.Second)
					defer cancel()
					if _, _, err := conn.Read(ctx); err != nil {
						return
					}
					for i, frame := range test.frames {
						// The runtime's recorded binary and text transports carry
						// the same Base64 wire format; both must remain supported.
						kind := websocket.MessageText
						if i%2 == 0 {
							kind = websocket.MessageBinary
						}
						if err := conn.Write(ctx, kind, []byte(frame)); err != nil {
							return
						}
					}
					_ = conn.Close(websocket.StatusNormalClosure, "")
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client := New(server.URL)
			task := decodeTaskFixture(t, programmingTaskFixture("GAME1", 1))
			result, err := client.SelfTest(context.Background(), task, "src/第1关.cpp", "", "", SelfTestOptions{Timeout: time.Second, ExpectedOutput: &test.want})
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) || strings.Contains(err.Error(), "private-runtime-details") || result.Completed {
					t.Fatalf("result=%+v err=%v", result, err)
				}
			} else if err != nil || !result.Completed || result.Matches == nil || !*result.Matches {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if result.Output != test.want {
				t.Fatalf("output=%q, want %q", result.Output, test.want)
			}
			if submissions.Load() != 0 {
				t.Fatal("self-test attempted formal submission")
			}
		})
	}
}
