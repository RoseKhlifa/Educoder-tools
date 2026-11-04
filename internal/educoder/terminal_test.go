package educoder

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func terminalTaskFixture() *Task {
	zero, yes := 0, true
	task := &Task{ST: &zero, HomeworkID: 123, CourseIdentifier: "COURSE1", Environments: []ProgrammingEnvironment{{EnvironmentID: 42, TabType: 4}}}
	task.Game.ID, task.Game.Identifier, task.Game.MyShixunID = 11, "GAME1", 80
	task.MyShixun.ID, task.MyShixun.Identifier = 80, "MY1"
	task.Challenge.ID, task.Challenge.ST, task.Challenge.WithCodeFile = 9, &zero, &yes
	task.Challenge.Path = "tmp/main.sh"
	task.User.UserID = 99
	return task
}

func hadoopEditorTerminalTaskFixture() *Task {
	zero, yes := 0, true
	task := &Task{ST: &zero, HomeworkID: 123, CourseIdentifier: "COURSE1", Environments: []ProgrammingEnvironment{
		{EnvironmentID: 42, TabType: 1}, {EnvironmentID: 42, TabType: 4},
	}}
	task.CodeEditor.EnvironmentID, task.CodeEditor.Name = 42, "Hadoop"
	task.Game.ID, task.Game.Identifier, task.Game.MyShixunID = 11, "GAME1", 80
	task.MyShixun.ID, task.MyShixun.Identifier = 80, "MY1"
	task.Challenge.ID, task.Challenge.ST, task.Challenge.WithCodeFile = 9, &zero, &yes
	task.Challenge.Path = "src/step1/hint.txt"
	task.Challenge.Description = "请使用命令行完成本题"
	task.User.UserID = 99
	return task
}

// hadoopWorkspaceTaskFixture models the recorded mixed Hadoop page: the
// editor and terminal share one environment ID, while the terminal is the
// unique tab_type=4 companion. The challenge names only its upstream hint
// file, and its statement explicitly directs the student to the command line.
func hadoopWorkspaceTaskFixture() *Task {
	zero, yes := 0, true
	task := &Task{ST: &zero, HomeworkID: 123, CourseIdentifier: "COURSE1", Environments: []ProgrammingEnvironment{
		{EnvironmentID: 42, TabType: 1}, {EnvironmentID: 42, TabType: 4},
	}}
	task.CodeEditor.EnvironmentID, task.CodeEditor.Name = 42, "Hadoop"
	task.Game.ID, task.Game.Identifier, task.Game.MyShixunID = 11, "GAME1", 80
	task.MyShixun.ID, task.MyShixun.Identifier = 80, "MY1"
	task.Challenge.ID, task.Challenge.ST, task.Challenge.WithCodeFile = 9, &zero, &yes
	task.Challenge.Path = "src/step1/hint.txt"
	task.Challenge.Description = "本关请使用命令行完成 Hadoop 操作。"
	task.User.UserID = 99
	return task
}

func TestIsHadoopWorkspaceTaskRequiresRecordedMixedWorkspace(t *testing.T) {
	valid := hadoopWorkspaceTaskFixture()
	if !valid.IsHadoopWorkspaceTask() {
		t.Fatal("recorded Hadoop mixed workspace was rejected")
	}
	for _, test := range []struct {
		name   string
		change func(*Task)
	}{
		{"java-file-remains-editor", func(task *Task) {
			task.CodeEditor.Name = "Java"
			task.Challenge.Path = "src/Main.java"
		}},
		{"generic-editor", func(task *Task) { task.CodeEditor.Name = "C/C++" }},
		{"missing-command-line-statement", func(task *Task) { task.Challenge.Description = "请完成本关代码。" }},
		{"extra-source-file", func(task *Task) { task.Challenge.Path = "src/step1/hint.txt；src/Main.java" }},
		{"wrong-hint-path", func(task *Task) { task.Challenge.Path = "src/step2/hint.txt" }},
		{"duplicate-terminal", func(task *Task) {
			task.Environments = append(task.Environments, ProgrammingEnvironment{EnvironmentID: 42, TabType: 4})
		}},
		{"terminal-bound-to-other-environment", func(task *Task) {
			task.Environments[1].EnvironmentID = 43
		}},
		{"missing-editor-tab", func(task *Task) {
			task.Environments = []ProgrammingEnvironment{{EnvironmentID: 42, TabType: 4}}
		}},
		{"editor-bound-to-other-environment", func(task *Task) {
			task.Environments[0].EnvironmentID = 43
		}},
		{"terminal-vnc", func(task *Task) { task.Environments[1].VNCURL = true }},
		{"editor-vnc", func(task *Task) { task.VNCURL = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			task := hadoopWorkspaceTaskFixture()
			test.change(task)
			if task.IsHadoopWorkspaceTask() {
				t.Fatal("accepted an unobserved Hadoop workspace variant")
			}
		})
	}
}

func TestHadoopWorkspaceDoesNotChangeIsTerminalTask(t *testing.T) {
	if hadoopWorkspaceTaskFixture().IsTerminalTask() {
		t.Fatal("mixed Hadoop workspace was classified as terminal-only")
	}
	if !terminalTaskFixture().IsTerminalTask() {
		t.Fatal("terminal-only layout no longer satisfies IsTerminalTask")
	}
}

func TestJavaSourceRemainsEditorExecution(t *testing.T) {
	task := hadoopWorkspaceTaskFixture()
	task.CodeEditor.Name = "Java"
	task.Challenge.Path = "src/Main.java"
	if task.IsHadoopWorkspaceTask() {
		t.Fatal("Java source entered the Hadoop workspace route")
	}
	if task.IsTerminalTask() {
		t.Fatal("Java editor plus terminal was classified as terminal-only")
	}
	env, err := programmingEnvironment(task)
	if err != nil || env.TabType != 1 || env.EnvironmentID != 42 {
		t.Fatalf("Java source lost its editor environment: env=%+v err=%v", env, err)
	}
}

func TestRunTerminalCommandUsesHadoopWorkspaceTab4(t *testing.T) {
	client, starts, commands := terminalSocketFixture(t, terminalFixtureOptions{mode: "success"})
	result, err := client.RunTerminalCommand(context.Background(), hadoopWorkspaceTaskFixture(), "printf 'workspace\\n'", TerminalOptions{Timeout: time.Second})
	if err != nil || result == nil || !result.Completed || result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("mixed Hadoop terminal completion=%+v err=%v", result, err)
	}
	if starts.Load() != 1 || commands.Load() != 1 {
		t.Fatalf("tab4 runtime was not used exactly once: starts=%d commands=%d", starts.Load(), commands.Load())
	}
}

func TestIsTerminalTaskRequiresObservedLayout(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Task)
		want   bool
	}{
		{"sole-terminal", func(*Task) {}, true},
		{"nil", nil, false},
		{"ordinary-editor", func(task *Task) { task.CodeEditor.EnvironmentID = 42; task.Environments[0].TabType = 1 }, false},
		{"editor-plus-terminal", func(task *Task) {
			task.Environments = append(task.Environments, ProgrammingEnvironment{EnvironmentID: 43, TabType: 1})
		}, false},
		{"missing-with-code-file", func(task *Task) { task.Challenge.WithCodeFile = nil }, false},
		{"objective", func(task *Task) { *task.ST = 1 }, false},
		{"graphic", func(task *Task) { task.Environments[0].VNCURL = true }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			task := terminalTaskFixture()
			if test.change == nil {
				task = nil
			} else {
				test.change(task)
			}
			if got := task.IsTerminalTask(); got != test.want {
				t.Fatalf("IsTerminalTask = %v", got)
			}
		})
	}
}

func TestHadoopStartUsesSameEnvironmentTab4AndFixedBootstrap(t *testing.T) {
	task := hadoopEditorTerminalTaskFixture()
	if task.IsTerminalTask() {
		t.Fatal("editor plus terminal layout changed IsTerminalTask semantics")
	}
	const script = "set -eu\ncommand -v start-dfs.sh >/dev/null\nif ! hdfs dfs -test -d / >/dev/null 2>&1; then\n  start-dfs.sh\nfi\nfor attempt in 1 2 3 4 5 6 7 8 9 10; do\n  if hdfs dfs -test -d / >/dev/null 2>&1; then exit 0; fi\n  sleep 1\ndone\nexit 1\n"
	expectedHash := sha256.Sum256([]byte(script))
	var starts, commands atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/myshixuns/MY1/start.json":
			starts.Add(1)
			q := r.URL.Query()
			if q.Get("tab_type") != "4" || q.Get("shixun_environment_id") != "42" || q.Get("game_id") != "11" || q.Get("coursesId") != "COURSE1" || q.Get("homework_common_id") != "123" {
				t.Errorf("Hadoop terminal start query lost binding: %v", q)
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
			if err := write("fixture shell banner\r\n$ "); err != nil {
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
				t.Error("expected Hadoop bootstrap command")
				return
			}
			commands.Add(1)
			chunks := regexp.MustCompile(`'([A-Za-z0-9+/=]+)'`).FindAllStringSubmatch(client.Data, -1)
			var decoded strings.Builder
			for _, chunk := range chunks {
				if data, decodeErr := base64.StdEncoding.DecodeString(chunk[1]); decodeErr == nil {
					decoded.Write(data)
				}
			}
			if !strings.Contains(decoded.String(), "start-dfs.sh") || !strings.Contains(decoded.String(), "hdfs dfs -test -d /") {
				t.Errorf("bootstrap command omitted fixed Hadoop readiness flow: %q", decoded.String())
			}
			hashMatch := regexp.MustCompile(`__educoder_hash" = '([a-f0-9]{64})'`).FindStringSubmatch(client.Data)
			if len(hashMatch) != 2 || hashMatch[1] != hex.EncodeToString(expectedHash[:]) {
				t.Errorf("bootstrap script hash=%v want=%s", hashMatch, hex.EncodeToString(expectedHash[:]))
			}
			nonce := regexp.MustCompile(`__EDUCODER_START_' '([a-f0-9]{32})__`).FindStringSubmatch(client.Data)
			if len(nonce) != 2 {
				t.Error("bootstrap command omitted execution boundary")
				return
			}
			start, finish := "__EDUCODER_START_"+nonce[1]+"__", "__EDUCODER_FINISH_"+nonce[1]+"__"
			if err := write(client.Data + "\r\n"); err != nil {
				return
			}
			body := "\r\n" + start + "\r\n\r\n" + finish + ":" + hex.EncodeToString(expectedHash[:]) + ":0\r\n$ "
			for i := 0; i < len(body); i += 7 {
				if err := write(body[i:min(i+7, len(body))]); err != nil {
					return
				}
			}
			_, _, _ = conn.Read(ctx)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := New(server.URL)
	if err := client.StartHadoop(context.Background(), task); err != nil {
		t.Fatalf("StartHadoop err=%v", err)
	}
	if starts.Load() != 1 || commands.Load() != 1 {
		t.Fatalf("Hadoop bootstrap replayed starts=%d commands=%d", starts.Load(), commands.Load())
	}
}

type terminalFixtureOptions struct {
	mode         string
	wrongBinding bool
	endpoint     string
}

func terminalSocketFixture(t *testing.T, options terminalFixtureOptions) (*Client, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	var starts, commands atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/myshixuns/MY1/start.json":
			starts.Add(1)
			if r.Method != http.MethodGet || r.URL.Query().Get("tab_type") != "4" || r.URL.Query().Get("shixun_environment_id") != "42" || r.URL.Query().Get("game_id") != "11" || r.URL.Query().Get("coursesId") != "COURSE1" || r.URL.Query().Get("homework_common_id") != "123" || r.URL.Query().Get("zzud") != "fixture-login" {
				t.Error("terminal start method/query lost task binding")
			}
			if options.mode == "http-error" {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"message":"runtime-password-canary wss://private-address-canary"}`)
				return
			}
			id := 80
			if options.wrongBinding {
				id = 81
			}
			endpoint := strings.Replace(server.URL, "http://", "ws://", 1) + "/ws"
			if options.endpoint != "" {
				endpoint = options.endpoint
			}
			json.NewEncoder(w).Encode(map[string]any{"status": 0, "data": map[string]any{"host": "runtime-address-canary.invalid", "port": "2222", "link_url": endpoint, "username": "runtime-user-canary", "password": "runtime-password-canary", "myshixun_id": id, "webssh_url": "https://runtime-address-canary.invalid", "remaining_time": 600, "ssh_address": "private-address-canary", "unexpected": "do-not-forward"}})
		case "/ws":
			for _, key := range []string{"Cookie", "Pc-Authorization", "X-EDU-Signature", "X-EDU-Timestamp"} {
				if r.Header.Get(key) != "" {
					t.Errorf("socket leaked API header %s", key)
				}
			}
			conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
			if err != nil {
				return
			}
			defer conn.CloseNow()
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			_, payload, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var init struct {
				TP   string         `json:"tp"`
				Data map[string]any `json:"data"`
			}
			if json.Unmarshal(payload, &init) != nil || init.TP != "init" || init.Data["secret"] != "runtime-password-canary" || init.Data["password"] != "runtime-password-canary" || init.Data["myshixun_id"] != float64(80) || init.Data["width"] != float64(1280) || init.Data["height"] != float64(720) || init.Data["rows"] != float64(40) || init.Data["columns"] != float64(160) || init.Data["unexpected"] != nil {
				t.Error("init did not preserve controlled observed wire data")
			}
			if options.mode == "no-banner" {
				_, _, _ = conn.Read(ctx)
				return
			}
			write := func(typ websocket.MessageType, text string) bool {
				return conn.Write(ctx, typ, []byte(base64.RawURLEncoding.EncodeToString([]byte(text)))) == nil
			}
			if !write(websocket.MessageText, "fixture shell banner\r\n$ ") {
				return
			}
			_, payload, err = conn.Read(ctx)
			if err != nil {
				return
			}
			var client struct {
				TP   string `json:"tp"`
				Data string `json:"data"`
			}
			if json.Unmarshal(payload, &client) != nil || client.TP != "client" {
				t.Error("expected one client command")
				return
			}
			commands.Add(1)
			nonce := regexp.MustCompile(`__EDUCODER_START_' '([a-f0-9]{32})__`).FindStringSubmatch(client.Data)
			if len(nonce) != 2 {
				t.Error("missing nonce in script wrapper")
				return
			}
			start, finish := "__EDUCODER_START_"+nonce[1]+"__", "__EDUCODER_FINISH_"+nonce[1]+"__"
			hashMatch := regexp.MustCompile(`__educoder_hash" = '([a-f0-9]{64})'`).FindStringSubmatch(client.Data)
			if len(hashMatch) != 2 {
				t.Error("missing independent script hash check")
				return
			}
			scriptHash := hashMatch[1]
			if strings.Contains(client.Data, start) || strings.Contains(client.Data, finish) {
				t.Error("boundary appears unbroken in command echo")
			}
			// Echo exactly what a PTY sends before executing the compound command.
			if !write(websocket.MessageBinary, client.Data+"\r\n") {
				return
			}
			switch options.mode {
			case "echo-only":
				_, _, _ = conn.Read(ctx)
				return
			case "close":
				return
			case "bad-frame":
				conn.Write(ctx, websocket.MessageText, []byte("#invalid-base64#"))
				return
			case "error-frame":
				conn.Write(ctx, websocket.MessageText, []byte(`{"tp":"error","error":"runtime-password-canary private-address-canary"}`))
				return
			case "too-large":
				write(websocket.MessageText, strings.Repeat("x", 8192))
				return
			}
			if options.mode == "wrong-hash" {
				scriptHash = strings.Repeat("0", 64)
			}
			body := "\r\n" + start + "\r\n图书\r\n\r\n" + finish + ":" + scriptHash + ":0\r\n$ "
			if options.mode == "exit-failure" {
				body = strings.Replace(body, ":0\r\n", ":7\r\n", 1)
			}
			// Split both UTF-8 characters and completion boundaries across frames.
			for i := 0; i < len(body); i += 7 {
				if !write(websocket.MessageBinary, body[i:min(i+7, len(body))]) {
					return
				}
			}
			// A successful client returns without sending a second command/build.
			_, _, _ = conn.Read(ctx)
		default:
			t.Errorf("unexpected request path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client := New(server.URL)
	client.LoginName = "fixture-login"
	u, _ := url.Parse(server.URL)
	client.HTTP.Jar.SetCookies(u, []*http.Cookie{{Name: "_educoder_session", Value: "session-canary"}})
	return client, &starts, &commands
}

func TestTerminalCommandCompletionAndPrivateReceipt(t *testing.T) {
	for _, mode := range []string{"success", "exit-failure"} {
		t.Run(mode, func(t *testing.T) {
			client, starts, commands := terminalSocketFixture(t, terminalFixtureOptions{mode: mode})
			script := "printf '图书\\n'"
			result, err := client.RunTerminalCommand(context.Background(), terminalTaskFixture(), script, TerminalOptions{Timeout: time.Second})
			if err != nil || result == nil || !result.Completed || result.ExitCode == nil || result.Output != "图书\n" {
				t.Fatalf("completion=%+v err=%v", result, err)
			}
			exit := 0
			if mode == "exit-failure" {
				exit = 7
			}
			if *result.ExitCode != exit {
				t.Fatalf("exit=%d", *result.ExitCode)
			}
			hash := sha256.Sum256([]byte(script))
			if result.ScriptHash != hex.EncodeToString(hash[:]) {
				t.Error("script hash changed submitted bytes")
			}
			if result.binding.client != client || result.binding.gameIdentifier != "GAME1" || result.binding.gameID != 11 || result.binding.myshixunID != 80 || result.binding.challengeID != 9 || result.binding.userID != 99 || result.binding.environmentID != 42 || result.binding.scriptHash != result.ScriptHash || result.state == nil || result.state.exitCode != exit {
				t.Error("receipt lost immutable binding")
			}
			data, _ := json.Marshal(result)
			for _, secret := range []string{"runtime-password-canary", "runtime-address-canary", "private-address-canary", "session-canary", "fixture-login", "binding", "consumed", "state"} {
				if strings.Contains(string(data), secret) {
					t.Errorf("JSON leaked %s", secret)
				}
			}
			if starts.Load() != 1 || commands.Load() != 1 {
				t.Fatalf("execution replay starts=%d commands=%d", starts.Load(), commands.Load())
			}
		})
	}
}

func TestTerminalUnconfirmedFailuresAreNotReplayed(t *testing.T) {
	for _, mode := range []string{"no-banner", "echo-only", "close", "bad-frame", "error-frame", "too-large", "http-error", "wrong-hash"} {
		t.Run(mode, func(t *testing.T) {
			client, starts, commands := terminalSocketFixture(t, terminalFixtureOptions{mode: mode})
			limit := int64(4096)
			result, err := client.RunTerminalCommand(context.Background(), terminalTaskFixture(), "printf 'hello'", TerminalOptions{Timeout: 100 * time.Millisecond, MaxOutputBytes: limit})
			if err == nil || result == nil || result.Completed || result.ExitCode != nil || result.binding.client != nil || result.state != nil {
				t.Fatalf("false confirmed result=%+v err=%v", result, err)
			}
			if (mode == "no-banner" || mode == "echo-only") && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("lost timeout identity: %v", err)
			}
			text := err.Error()
			for _, secret := range []string{"runtime-password-canary", "runtime-address-canary", "private-address-canary", "session-canary"} {
				if strings.Contains(text, secret) {
					t.Errorf("error leaked %s", secret)
				}
			}
			if starts.Load() != 1 || commands.Load() > 1 {
				t.Fatalf("failure replay starts=%d commands=%d", starts.Load(), commands.Load())
			}
			if mode == "no-banner" && commands.Load() != 0 {
				t.Error("command sent without initial banner")
			}
		})
	}
}

func TestTerminalRejectsMismatchedBindingAndSocketTargets(t *testing.T) {
	for _, options := range []terminalFixtureOptions{{wrongBinding: true}, {endpoint: "wss://unexpected.invalid/ws"}, {endpoint: "ws://webssh.educoder.net/ws"}, {endpoint: "wss://webssh.educoder.net/ws?secret=canary"}, {endpoint: "wss://webssh.educoder.net/log"}} {
		client, starts, commands := terminalSocketFixture(t, options)
		result, err := client.RunTerminalCommand(context.Background(), terminalTaskFixture(), "true", TerminalOptions{Timeout: time.Second})
		if err == nil || result == nil || result.Completed || commands.Load() != 0 || starts.Load() != 1 {
			t.Fatalf("accepted unbound runtime result=%+v err=%v", result, err)
		}
	}
}

func TestTerminalRejectsInvalidInputBeforeNetwork(t *testing.T) {
	client := New("https://unused.invalid")
	for _, test := range []struct {
		name    string
		task    *Task
		script  string
		options TerminalOptions
	}{
		{"editor", decodeTaskFixture(t, programmingTaskFixture("GAME1", 1)), "true", TerminalOptions{}},
		{"empty", terminalTaskFixture(), "", TerminalOptions{}},
		{"oversize", terminalTaskFixture(), strings.Repeat("x", 32769), TerminalOptions{}},
		{"nul", terminalTaskFixture(), "a\x00b", TerminalOptions{}},
		{"invalid-utf8", terminalTaskFixture(), string([]byte{255}), TerminalOptions{}},
		{"output-limit", terminalTaskFixture(), "true", TerminalOptions{MaxOutputBytes: (1 << 20) + 1}},
		{"timeout-limit", terminalTaskFixture(), "true", TerminalOptions{Timeout: 11 * time.Minute}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if result, err := client.RunTerminalCommand(context.Background(), test.task, test.script, test.options); result != nil || err == nil {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.RunTerminalCommand(ctx, terminalTaskFixture(), "true", TerminalOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation identity %v", err)
	}
}

func TestTerminalOutputRequiresWholeOrderedBoundaries(t *testing.T) {
	for _, raw := range []string{"\nfinish:0\n", "\nstart\ntext\nfinish:0", "\nstart\ntext\nfinish:+0\n", "\nstart\ntext\nfinish:256\n", "\nstart\ntext\nfinish:00\n", "\nstart\ntext\nfinish:0garbage\n"} {
		if _, code, _ := terminalOutput([]byte(raw), "start", "finish"); code != nil {
			t.Fatalf("false boundary completion on %q", raw)
		}
	}
}

func TestTerminalCommandPreservesScriptBytesThroughShell(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell is not installed")
	}
	// Include CRLF, shell metacharacters, multibyte text and terminal control
	// bytes inside a quoted string. Only their Base64 representation may travel
	// across the PTY; the independent hash covers these exact original bytes.
	payload := strings.Repeat("字'\"$`\\\r\n\x03\x1b", 90)
	script := "printf '%s' " + graphicQuote(payload) + "\nexit 7"
	command, start, finish := terminalCommand(script, "0123456789abcdef0123456789abcdef")
	if strings.Contains(command, payload) || strings.ContainsRune(strings.TrimSuffix(command, "\r"), '\r') || strings.ContainsRune(command, '\x03') {
		t.Fatal("literal control bytes entered the PTY command")
	}
	for _, line := range strings.Split(command, "\n") {
		if len(line) > 2048 {
			t.Fatal("shell transfer exceeded bounded canonical line length")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, shell, "-c", strings.TrimSuffix(command, "\r")).CombinedOutput()
	if err != nil {
		t.Fatalf("local shell wrapper failed: %v", err)
	}
	actual, code, hash := terminalOutput(output, start, finish)
	expectedHash := sha256.Sum256([]byte(script))
	if code == nil || *code != 7 || actual != strings.ReplaceAll(payload, "\r\n", "\n") || hash != hex.EncodeToString(expectedHash[:]) {
		t.Fatalf("script bytes or receipt changed: exit=%v hash=%s", code, hash)
	}
}
