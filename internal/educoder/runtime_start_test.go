package educoder

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
)

// hbaseEditorTerminalTaskFixture models the HBase Java levels read on
// 2026-11-27: a Java editor named Hbase whose environment also has a command
// line (tab_type=4) with the same environment ID.
func hbaseEditorTerminalTaskFixture() *Task {
	zero, yes := 0, true
	task := &Task{ST: &zero, HomeworkID: 123, CourseIdentifier: "COURSE1", Environments: []ProgrammingEnvironment{
		{EnvironmentID: 42, TabType: 1, Name: "命令行"}, {EnvironmentID: 42, TabType: 4, Name: "命令行"},
	}}
	task.CodeEditor.EnvironmentID, task.CodeEditor.Name = 42, "Hbase"
	task.Game.ID, task.Game.Identifier, task.Game.MyShixunID = 11, "GAME1", 80
	task.MyShixun.ID, task.MyShixun.Identifier = 80, "MY1"
	task.Challenge.ID, task.Challenge.ST, task.Challenge.WithCodeFile = 9, &zero, &yes
	task.Challenge.Path = "src/step1/Task.java"
	task.User.UserID = 99
	return task
}

func TestAttachedTerminalEnvironmentIsOnlyTheEditorsOwnCommandLine(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Task)
		want   bool
	}{
		{"hbase-java", func(*Task) {}, true},
		{"terminal-of-another-environment", func(task *Task) { task.Environments[1].EnvironmentID = 43 }, false},
		{"two-terminals", func(task *Task) {
			task.Environments = append(task.Environments, ProgrammingEnvironment{EnvironmentID: 42, TabType: 4})
		}, false},
		{"vnc-terminal", func(task *Task) { task.Environments[1].VNCURL = true }, false},
		{"vnc-task", func(task *Task) { task.VNCURL = true }, false},
		{"no-editor", func(task *Task) { task.CodeEditor.EnvironmentID = 0 }, false},
		{"objective", func(task *Task) { *task.ST = 1 }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			task := hbaseEditorTerminalTaskFixture()
			test.change(task)
			env, err := attachedTerminalEnvironment(task)
			if (err == nil) != test.want || test.want && (env.TabType != 4 || env.EnvironmentID != 42) {
				t.Fatalf("env=%+v err=%v", env, err)
			}
		})
	}
	task := hbaseEditorTerminalTaskFixture()
	if task.IsTerminalTask() || task.IsHadoopWorkspaceTask() {
		t.Fatal("the HBase Java editor must keep its editor execution route")
	}
}

// runtimeStartServer accepts one fixed start script on the attached terminal
// and exits with exitCode. It records the decoded script it was sent.
func runtimeStartServer(t *testing.T, exitCode int) (*Client, *atomic.Int32, *atomic.Pointer[string]) {
	t.Helper()
	var commands atomic.Int32
	var sent atomic.Pointer[string]
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/myshixuns/MY1/start.json":
			if q := r.URL.Query(); q.Get("tab_type") != "4" || q.Get("shixun_environment_id") != "42" || q.Get("game_id") != "11" {
				t.Errorf("start query lost the attached terminal binding: %v", q)
			}
			endpoint := strings.Replace(server.URL, "http://", "ws://", 1) + "/ws"
			json.NewEncoder(w).Encode(map[string]any{"status": 0, "data": map[string]any{"host": "runtime", "port": "2222", "link_url": endpoint, "username": "root", "password": "secret", "myshixun_id": 80}})
		case "/ws":
			conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
			if err != nil {
				return
			}
			defer conn.CloseNow()
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
			write := func(text string) error {
				return conn.Write(ctx, websocket.MessageBinary, []byte(base64.RawStdEncoding.EncodeToString([]byte(text))))
			}
			if write("$ ") != nil {
				return
			}
			_, payload, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var client struct {
				TP   string `json:"tp"`
				Data string `json:"data"`
			}
			if json.Unmarshal(payload, &client) != nil || client.TP != "client" {
				t.Error("expected one start command")
				return
			}
			commands.Add(1)
			// Only the quoted chunks piped to base64 -d carry the script.
			encoded := client.Data[strings.Index(client.Data, "printf '%s' "):strings.Index(client.Data, " | base64 -d")]
			var decoded strings.Builder
			for _, chunk := range regexp.MustCompile(`'([A-Za-z0-9+/=]+)'`).FindAllStringSubmatch(encoded, -1) {
				if data, decodeErr := base64.StdEncoding.DecodeString(chunk[1]); decodeErr == nil {
					decoded.Write(data)
				}
			}
			script := decoded.String()
			sent.Store(&script)
			hash := sha256.Sum256([]byte(script))
			nonce := regexp.MustCompile(`__EDUCODER_START_' '([a-f0-9]{32})__`).FindStringSubmatch(client.Data)
			if len(nonce) != 2 {
				t.Error("start command omitted its execution boundary")
				return
			}
			start, finish := "__EDUCODER_START_"+nonce[1]+"__", "__EDUCODER_FINISH_"+nonce[1]+"__"
			_ = write(client.Data + "\r\n\r\n" + start + "\r\nstarting master\r\n\r\n" + finish + ":" + hex.EncodeToString(hash[:]) + fmt.Sprintf(":%d\r\n$ ", exitCode))
			_, _, _ = conn.Read(ctx)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return New(server.URL), &commands, &sent
}

func TestStartRuntimeRunsTheFixedHBaseScriptOnTheAttachedTerminal(t *testing.T) {
	client, commands, sent := runtimeStartServer(t, 0)
	if err := client.StartRuntime(context.Background(), hbaseEditorTerminalTaskFixture(), RuntimeHBase); err != nil {
		t.Fatalf("StartRuntime err=%v", err)
	}
	script := sent.Load()
	if commands.Load() != 1 || script == nil || *script != runtimeStarts[RuntimeHBase].script {
		t.Fatalf("HBase start was not the fixed script exactly once: commands=%d", commands.Load())
	}
	for _, step := range []string{"start-dfs.sh", "start-hbase.sh", "hbase shell", "row(s)"} {
		if !strings.Contains(*script, step) {
			t.Fatalf("HBase start script lost step %q", step)
		}
	}
}

func TestStartRuntimeReportsAnUnconfirmedReadiness(t *testing.T) {
	client, commands, _ := runtimeStartServer(t, 1)
	err := client.StartRuntime(context.Background(), hbaseEditorTerminalTaskFixture(), RuntimeHBase)
	if err == nil || !strings.Contains(err.Error(), "HBase 启动后未确认就绪") || commands.Load() != 1 {
		t.Fatalf("failed readiness err=%v commands=%d", err, commands.Load())
	}
}

func TestStartRuntimeRefusesUnknownServicesAndLayoutsBeforeNetwork(t *testing.T) {
	client, commands, _ := runtimeStartServer(t, 0)
	if err := client.StartRuntime(context.Background(), hbaseEditorTerminalTaskFixture(), RuntimeService("Spark")); err == nil {
		t.Fatal("ran a service without a fixed start script")
	}
	if err := client.StartRuntime(context.Background(), terminalTaskFixture(), RuntimeHBase); err == nil {
		t.Fatal("ran a start script on a terminal-only task")
	}
	if commands.Load() != 0 {
		t.Fatalf("refused starts reached the terminal: %d", commands.Load())
	}
}

// A statement's own install steps (tar -zxvf of HBase) print far more than
// the transcript limit. The completed script keeps its exit status, the start
// of its output and its latest output; only the middle is dropped.
func TestTerminalTranscriptKeepsHeadAndTailOfALongOutput(t *testing.T) {
	const start, finish = "__EDUCODER_START_n__", "__EDUCODER_FINISH_n__"
	hash := strings.Repeat("a", 64)
	var stream strings.Builder
	stream.WriteString("( printf ... )\r\n\r\n" + start + "\r\n")
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&stream, "hbase-2.1.1/文件%04d.txt\r\n", i)
	}
	stream.WriteString("配置完成\r\n\r\n" + finish + ":" + hash + ":0\r\n$ ")
	raw := []byte(stream.String())
	transcript := &terminalTranscript{limit: 8 << 10, start: []byte(start)}
	// Frames split multi-byte characters and the boundaries. Every cut must
	// fall between characters, not only the last one.
	for i, size := 0, 1; i < len(raw); i, size = i+size, size%13+1 {
		if !transcript.add(raw[i:min(i+size, len(raw))]) {
			t.Fatalf("long output after the start boundary was refused at byte %d", i)
		}
		if transcript.cut && (!utf8.Valid(transcript.head) || transcript.omitted > 0 && len(transcript.tail) > 0 && !utf8.RuneStart(transcript.tail[0])) {
			t.Fatalf("a cut split a UTF-8 character at byte %d", i)
		}
	}
	if prefix := strings.Index(stream.String(), start) + len(start) + 2; len(transcript.head) > prefix+terminalHeadBytes || len(transcript.tail) > transcript.limit/2 {
		t.Fatalf("transcript is not bounded: head=%d tail=%d", len(transcript.head), len(transcript.tail))
	}
	if !strings.Contains(string(transcript.recent(200)), finish) {
		t.Fatal("finish boundary is not in the recent window")
	}
	output, code, remoteHash := terminalOutput(transcript.bytes(), start, finish)
	if code == nil || *code != 0 || remoteHash != hash || !utf8.ValidString(output) {
		t.Fatalf("long output lost its completion: code=%v hash=%q valid=%v", code, remoteHash, utf8.ValidString(output))
	}
	if !strings.HasPrefix(output, "hbase-2.1.1/文件0000.txt\n") || !strings.HasSuffix(output, "hbase-2.1.1/文件2999.txt\n配置完成\n") || !strings.Contains(output, "已省略中间") {
		t.Fatalf("long output did not keep its head and tail: %q ... %q", output[:min(80, len(output))], output[max(0, len(output)-80):])
	}
}

func TestTerminalTranscriptStillFailsWithoutAStartBoundary(t *testing.T) {
	transcript := &terminalTranscript{limit: 1024, start: []byte("__EDUCODER_START_n__")}
	if !transcript.add([]byte(strings.Repeat("x", 1000))) || transcript.add([]byte(strings.Repeat("x", 100))) {
		t.Fatal("an overflow before the start boundary must stay a failure")
	}
}
