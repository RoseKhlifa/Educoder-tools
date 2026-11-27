package educoder

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
)

// IsTerminalTask identifies the observed terminal-only programming layout.
// An editor task with an extra terminal must keep its editor execution flow.
func (t *Task) IsTerminalTask() bool {
	_, err := terminalEnvironment(t)
	return err == nil
}

// IsHadoopWorkspaceTask identifies the observed mixed editor/terminal layout
// where the editor path is a placeholder (hint.txt) and the statement
// explicitly directs the learner to use the command line. The editor remains
// the programming contract; tab_type=4 is an execution workspace only.
func (t *Task) IsHadoopWorkspaceTask() bool {
	_, err := hadoopWorkspaceEnvironment(t)
	return err == nil
}

func hadoopWorkspaceEnvironment(t *Task) (ProgrammingEnvironment, error) {
	if t == nil || !strings.EqualFold(strings.TrimSpace(t.CodeEditor.Name), "Hadoop") || len(t.CodePaths()) != 1 || !strings.EqualFold(t.CodePaths()[0], "src/step1/hint.txt") {
		return ProgrammingEnvironment{}, errors.New("任务不是已观察的 Hadoop 工作区布局")
	}
	statement := strings.ToLower(strings.TrimSpace(t.Challenge.Description))
	if !strings.Contains(statement, "命令行") && !strings.Contains(statement, "command line") && !strings.Contains(statement, "terminal") {
		return ProgrammingEnvironment{}, errors.New("Hadoop 题干未明确要求使用命令行")
	}
	return attachedTerminalEnvironment(t)
}

// attachedTerminalEnvironment finds the command line beside a code editor: the
// unique non-VNC tab_type=4 environment that shares the editor's environment
// ID, next to exactly one tab_type=1 editor. Big-data levels (Hadoop, HBase)
// use it for the services their statements ask the learner to start before
// evaluating editor code; the editor stays the programming contract.
func attachedTerminalEnvironment(t *Task) (ProgrammingEnvironment, error) {
	if t == nil || t.ST == nil || *t.ST != 0 || t.Challenge.ST == nil || *t.Challenge.ST != 0 || t.Challenge.WithCodeFile == nil || !*t.Challenge.WithCodeFile || len(t.Chooses) != 0 || t.CodeEditor.EnvironmentID <= 0 || bool(t.VNCURL) {
		return ProgrammingEnvironment{}, errors.New("任务不是带命令行的代码编辑器布局")
	}
	var terminal ProgrammingEnvironment
	count, editorCount := 0, 0
	for _, env := range t.Environments {
		if env.EnvironmentID != t.CodeEditor.EnvironmentID {
			continue
		}
		if env.TabType == 1 {
			editorCount++
		}
		if env.TabType == 4 && !bool(env.VNCURL) {
			terminal = env
			count++
		}
	}
	if count != 1 || editorCount != 1 {
		return ProgrammingEnvironment{}, errors.New("代码编辑器没有唯一匹配的 tab_type=4 命令行")
	}
	return terminal, nil
}

func executionEnvironment(t *Task) (ProgrammingEnvironment, error) {
	if env, err := terminalEnvironment(t); err == nil {
		return env, nil
	}
	return hadoopWorkspaceEnvironment(t)
}

func terminalEnvironment(t *Task) (ProgrammingEnvironment, error) {
	if t == nil || t.ST == nil || *t.ST != 0 || t.Challenge.ST == nil || *t.Challenge.ST != 0 || t.Challenge.WithCodeFile == nil || !*t.Challenge.WithCodeFile || len(t.Chooses) != 0 || t.CodeEditor.EnvironmentID != 0 || len(t.Environments) != 1 {
		return ProgrammingEnvironment{}, errors.New("任务不是已观察的独立终端布局")
	}
	env := t.Environments[0]
	if env.EnvironmentID <= 0 || env.TabType != 4 || bool(env.VNCURL) || bool(t.VNCURL) {
		return ProgrammingEnvironment{}, errors.New("任务缺少唯一的 tab_type=4 终端环境")
	}
	return env, nil
}

type TerminalOptions struct {
	Timeout        time.Duration
	MaxOutputBytes int64
}

// TerminalResult describes command execution, not formal evaluation or a pass.
// Its receipt remains tied to the client and immutable task identities in memory.
type TerminalResult struct {
	Completed  bool   `json:"completed"`
	Output     string `json:"output"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	ScriptHash string `json:"script_hash"`
	binding    terminalBinding
	state      *terminalReceiptState
}

// A TerminalResult is public and can be copied by value. Copies must share the
// same single-use guard, while the actual observed exit code stays immutable.
type terminalReceiptState struct {
	consumed atomic.Bool
	exitCode int
}

type terminalBinding struct {
	client         *Client
	gameIdentifier string
	myshixunID     int64
	gameID         int64
	challengeID    int64
	userID         int64
	environmentID  int64
	scriptHash     string
}

// Keep runtime credentials in this unexported wire type only. In particular,
// unknown response fields are never forwarded into a separate-origin socket.
type terminalStartData struct {
	Host          string          `json:"host"`
	Port          string          `json:"port"`
	LinkURL       string          `json:"link_url"`
	Username      string          `json:"username"`
	Password      string          `json:"password"`
	MyShixunID    json.RawMessage `json:"myshixun_id"`
	WebSSHURL     string          `json:"webssh_url"`
	RemainingTime json.RawMessage `json:"remaining_time"`
	SSHAddress    string          `json:"ssh_address"`
}

func terminalRuntimeID(raw json.RawMessage) (int64, error) {
	var id int64
	if err := json.Unmarshal(raw, &id); err == nil {
		return id, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, errors.New("终端响应缺少有效运行实例绑定")
	}
	id, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, errors.New("终端响应缺少有效运行实例绑定")
	}
	return id, nil
}

func terminalEndpoint(base, endpoint string) (*url.URL, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Path != "/ws" || u.RawPath != "" {
		return nil, errors.New("终端响应的 WebSocket 地址无效")
	}
	if u.Scheme == "wss" && u.Host == "webssh.educoder.net" {
		return u, nil
	}
	// HTTP test fixtures may use their own same-origin socket. This does not
	// authorize arbitrary URLs returned by the production upstream.
	b, err := url.Parse(base)
	if err == nil && b.User == nil && b.RawQuery == "" && !b.ForceQuery && b.Fragment == "" && b.Path == "" && b.Host == u.Host && ((b.Scheme == "http" && u.Scheme == "ws") || (b.Scheme == "https" && u.Scheme == "wss")) && net.ParseIP(b.Hostname()).IsLoopback() {
		return u, nil
	}
	return nil, errors.New("终端 WebSocket 地址不属于已观察的服务")
}

func terminalCommand(script, nonce string) (command, start, finish string) {
	start = "__EDUCODER_START_" + nonce + "__"
	finish = "__EDUCODER_FINISH_" + nonce + "__"
	// A PTY interprets CR and control bytes, so only Base64 ASCII crosses its
	// line discipline. The runtime independently hashes the decoded file before
	// execution, and reports that hash in the completion boundary.
	hash := sha256.Sum256([]byte(script))
	scriptHash := hex.EncodeToString(hash[:])
	encoded := base64.StdEncoding.EncodeToString([]byte(script))
	var argument strings.Builder
	for len(encoded) > 0 {
		n := min(512, len(encoded))
		if argument.Len() > 0 {
			argument.WriteString("\\\n")
		}
		argument.WriteString("'" + encoded[:n] + "'")
		encoded = encoded[n:]
	}
	// Neither entire boundary occurs in the sent text. Echoing the command,
	// including multiline shell prompts, cannot complete this receipt.
	command = "( __educoder_tmp=$(mktemp) || exit; trap 'rm -f -- \"$__educoder_tmp\"' 0; printf '%s' " + argument.String() + " | base64 -d > \"$__educoder_tmp\" || exit; __educoder_hash=$(sha256sum \"$__educoder_tmp\") || exit; __educoder_hash=${__educoder_hash%% *}; printf '\\n%s%s\\n' '__EDUCODER_START_' '" + nonce + "__'; if [ \"$__educoder_hash\" = '" + scriptHash + "' ]; then sh \"$__educoder_tmp\"; __educoder_rc=$?; else __educoder_rc=125; fi; printf '\\n%s%s:%s:%d\\n' '__EDUCODER_FINISH_' '" + nonce + "__' \"$__educoder_hash\" \"$__educoder_rc\"; )\r"
	return command, start, finish
}

func terminalOutput(raw []byte, start, finish string) (string, *int, string) {
	// Terminal line endings may be LF or CRLF. Only complete boundary lines
	// count; a partial frame or the marker inside another line does not.
	normalized := bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	begin := bytes.Index(normalized, []byte("\n"+start+"\n"))
	if begin < 0 {
		return "", nil, ""
	}
	output := normalized[begin+len(start)+2:]
	prefix := []byte("\n" + finish + ":")
	for from := 0; from < len(output); {
		index := bytes.Index(output[from:], prefix)
		if index < 0 {
			break
		}
		index += from
		codeStart := index + len(prefix)
		end := bytes.IndexByte(output[codeStart:], '\n')
		if end < 0 {
			break
		}
		hashText, codeText, ok := strings.Cut(string(output[codeStart:codeStart+end]), ":")
		_, hashErr := hex.DecodeString(hashText)
		code, err := strconv.Atoi(codeText)
		if ok && len(hashText) == sha256.Size*2 && hashErr == nil && err == nil && code >= 0 && code <= 255 && strconv.Itoa(code) == codeText {
			return string(output[:index]), &code, hashText
		}
		from = codeStart + end + 1
	}
	return string(output), nil, ""
}

// terminalHeadBytes is how much script output a cut transcript keeps after
// the start boundary (at most a quarter of the limit); between a quarter and
// half of the limit holds the latest bytes, so the kept output stays below it.
const terminalHeadBytes = 8 << 10

// terminalTranscript bounds a terminal stream in memory. Below its limit it
// keeps every byte. Past it, it keeps everything through the start boundary
// plus the first terminalHeadBytes of script output, and a rolling tail of the
// latest bytes, so a long but ordinary output still reaches its finish
// boundary. On 2026-11-27 an HBase install level's own steps (tar -zxvf)
// printed more than the limit, and a script that completed was recorded as an
// unknown result. Only a stream that overflows before its start boundary
// fails. Cuts fall between UTF-8 characters, and the omitted size is reported
// where the bytes were dropped.
type terminalTranscript struct {
	limit   int
	start   []byte
	head    []byte
	tail    []byte
	omitted int64
	cut     bool
}

func (b *terminalTranscript) add(p []byte) bool {
	if b.cut {
		b.tail = append(b.tail, p...)
		if len(b.tail) > b.limit/2 {
			b.trim()
		}
		return true
	}
	if len(b.head)+len(p) <= b.limit {
		b.head = append(b.head, p...)
		return true
	}
	all := append(b.head, p...)
	i := bytes.Index(all, b.start)
	if i < 0 {
		return false
	}
	line := bytes.IndexByte(all[i:], '\n')
	if line < 0 {
		return false
	}
	body := i + line + 1
	keep := runeCut(all, min(len(all), body+min(terminalHeadBytes, b.limit/4)), body)
	b.head, b.tail, b.cut = all[:keep:keep], append([]byte(nil), all[keep:]...), true
	b.trim()
	return true
}

// trim keeps the latest limit/4 tail bytes. add calls it only when the tail
// doubled, so a stream of tiny frames is not copied once per frame.
func (b *terminalTranscript) trim() {
	keep := b.limit / 4
	if len(b.tail) <= keep {
		return
	}
	drop := len(b.tail) - keep
	for drop < len(b.tail) && !utf8.RuneStart(b.tail[drop]) {
		drop++
	}
	b.omitted += int64(drop)
	b.tail = append([]byte(nil), b.tail[drop:]...)
}

// recent returns at most the last n bytes received.
func (b *terminalTranscript) recent(n int) []byte {
	if !b.cut {
		return b.head[max(0, len(b.head)-n):]
	}
	if len(b.tail) >= n || b.omitted > 0 {
		return b.tail[max(0, len(b.tail)-n):]
	}
	// Right after the cut the window can still reach back into the head.
	return append(append([]byte(nil), b.head[max(0, len(b.head)-(n-len(b.tail))):]...), b.tail...)
}

func (b *terminalTranscript) bytes() []byte {
	if !b.cut {
		return b.head
	}
	out := append([]byte(nil), b.head...)
	if b.omitted > 0 {
		out = append(out, fmt.Sprintf("\n…（终端输出过长，已省略中间 %d 字节）…\n", b.omitted)...)
	}
	return append(out, b.tail...)
}

// runeCut moves cut back, not below floor, so b[:cut] does not end inside a
// UTF-8 character.
func runeCut(b []byte, cut, floor int) int {
	for i := cut - 1; i >= floor && i >= cut-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			if !utf8.FullRune(b[i:cut]) {
				return i
			}
			return cut
		}
	}
	return cut
}

func terminalFailure(ctx context.Context, message string) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%s: %w", message, ctx.Err())
	}
	return errors.New(message)
}

// RunTerminalCommand opens the observed interactive terminal and sends one
// child-shell execution. A timeout or broken stream is never replayed here.
func (c *Client) RunTerminalCommand(ctx context.Context, t *Task, script string, options TerminalOptions) (*TerminalResult, error) {
	env, err := executionEnvironment(t)
	if err != nil {
		return nil, err
	}
	return c.runBoundTerminalCommand(ctx, t, env, script, options)
}

func (c *Client) runBoundTerminalCommand(ctx context.Context, t *Task, env ProgrammingEnvironment, script string, options TerminalOptions) (*TerminalResult, error) {
	if c == nil || c.HTTP == nil || validIdentifier(t.Game.Identifier) != nil || validIdentifier(t.MyShixun.Identifier) != nil || validIdentifier(t.CourseIdentifier) != nil || t.Game.ID <= 0 || t.MyShixun.ID <= 0 || t.Game.MyShixunID != t.MyShixun.ID || t.HomeworkID <= 0 || t.User.UserID <= 0 || t.Challenge.ID <= 0 {
		return nil, errors.New("终端任务缺少完整且一致的运行实例绑定")
	}
	// All scalar task identities are copied before network activity. The
	// receipt cannot acquire another identity if the caller later reuses Task.
	taskSnapshot := *t
	t = &taskSnapshot
	if len(script) == 0 || len(script) > 32<<10 || strings.ContainsRune(script, 0) || !utf8.ValidString(script) {
		return nil, errors.New("终端脚本必须是 1 至 32768 字节的有效 UTF-8 文本且不含空字符")
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	if timeout > 10*time.Minute {
		return nil, errors.New("终端执行时限至多为 10 分钟")
	}
	limit := options.MaxOutputBytes
	if limit <= 0 {
		limit = 128 << 10
	}
	if limit > 1<<20 {
		return nil, errors.New("终端输出上限至多为 1 MiB")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	hash := sha256.Sum256([]byte(script))
	result := &TerminalResult{ScriptHash: hex.EncodeToString(hash[:])}
	var response struct {
		Data terminalStartData `json:"data"`
	}
	query := url.Values{"shixun_environment_id": {strconv.FormatInt(env.EnvironmentID, 10)}, "tab_type": {"4"}, "game_id": {strconv.FormatInt(t.Game.ID, 10)}, "coursesId": {t.CourseIdentifier}, "homework_common_id": {strconv.FormatInt(t.HomeworkID, 10)}}
	if err := c.request(ctx, http.MethodGet, "/api/myshixuns/"+t.MyShixun.Identifier+"/start.json", query, nil, taskRef(t), &response); err != nil {
		return result, terminalFailure(ctx, "终端启动请求失败")
	}
	runtimeID, err := terminalRuntimeID(response.Data.MyShixunID)
	port, portErr := strconv.Atoi(response.Data.Port)
	if err != nil || runtimeID != t.MyShixun.ID || response.Data.Host == "" || response.Data.Username == "" || response.Data.Password == "" || portErr != nil || port < 1 || port > 65535 {
		return result, errors.New("终端响应缺少有效连接参数或运行实例不匹配")
	}
	endpoint, err := terminalEndpoint(c.BaseURL, response.Data.LinkURL)
	if err != nil {
		return result, err
	}
	wsHTTP := &http.Client{Transport: c.HTTP.Transport, Timeout: c.HTTP.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	connection, upgrade, err := websocket.Dial(ctx, endpoint.String(), &websocket.DialOptions{HTTPClient: wsHTTP, HTTPHeader: http.Header{"Origin": {c.Origin}}})
	if upgrade != nil && upgrade.Body != nil {
		defer upgrade.Body.Close()
	}
	if err != nil {
		return result, terminalFailure(ctx, "终端 WebSocket 连接失败")
	}
	defer connection.CloseNow()
	connection.SetReadLimit(2*limit + 1024)
	init, _ := json.Marshal(struct {
		TP   string `json:"tp"`
		Data any    `json:"data"`
	}{"init", struct {
		terminalStartData
		Secret  string `json:"secret"`
		Width   int    `json:"width"`
		Height  int    `json:"height"`
		Rows    int    `json:"rows"`
		Columns int    `json:"columns"`
	}{response.Data, response.Data.Password, 1280, 720, 40, 160}})
	if err := connection.Write(ctx, websocket.MessageText, init); err != nil {
		return result, terminalFailure(ctx, "终端初始化消息发送失败")
	}
	// The socket implementation serializes concurrent writes. Heartbeats use
	// the same bounded context and are joined before this method returns.
	heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				if connection.Write(heartbeatCtx, websocket.MessageText, []byte(`{"tp":"h"}`)) != nil {
					return
				}
			}
		}
	}()
	defer func() { stopHeartbeat(); <-heartbeatDone }()
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return result, errors.New("终端执行边界生成失败")
	}
	command, start, finish := terminalCommand(script, hex.EncodeToString(nonceBytes))
	sent := false
	transcript := &terminalTranscript{limit: int(limit), start: []byte(start)}
	defer func() {
		if !result.Completed {
			result.Output, _, _ = terminalOutput(transcript.bytes(), start, finish)
		}
	}()
	for {
		_, message, err := connection.Read(ctx)
		if err != nil {
			return result, terminalFailure(ctx, "终端流在确认命令完成前关闭或读取失败")
		}
		if selfTestRuntimeError(message) {
			return result, errors.New("终端运行器返回错误帧")
		}
		decoded, err := decodeSelfTestBase64(message)
		if err != nil {
			return result, errors.New("终端流包含无效编码帧")
		}
		if !transcript.add(decoded) {
			return result, errors.New("终端输出超过字节上限")
		}
		if !sent {
			if len(decoded) == 0 {
				continue
			}
			payload, _ := json.Marshal(map[string]string{"tp": "client", "data": command})
			// Set before writing: a failed write has an unknown delivery outcome.
			sent = true
			if err := connection.Write(ctx, websocket.MessageText, payload); err != nil {
				return result, terminalFailure(ctx, "终端命令发送结果未确认")
			}
			continue
		}
		// The finish line can span frames. Search only the newly received bytes
		// and enough overlap for the marker/hash/exit line before parsing the
		// bounded stream. A one-byte-at-a-time stdout must not rescan its whole
		// history for every byte.
		if !bytes.Contains(transcript.recent(len(decoded)+len(finish)+80), []byte(finish)) {
			continue
		}
		var remoteHash string
		result.Output, result.ExitCode, remoteHash = terminalOutput(transcript.bytes(), start, finish)
		if result.ExitCode != nil {
			if remoteHash != result.ScriptHash {
				result.ExitCode = nil
				return result, errors.New("终端实际执行脚本的哈希不匹配")
			}
			if !utf8.ValidString(result.Output) {
				result.ExitCode = nil
				return result, errors.New("完整终端输出不是有效的 UTF-8 数据")
			}
			result.Completed = true
			result.binding = terminalBinding{client: c, gameIdentifier: t.Game.Identifier, myshixunID: t.MyShixun.ID, gameID: t.Game.ID, challengeID: t.Challenge.ID, userID: t.User.UserID, environmentID: env.EnvironmentID, scriptHash: result.ScriptHash}
			result.state = &terminalReceiptState{exitCode: *result.ExitCode}
			return result, nil
		}
	}
}

// RuntimeService names a service that a level's statement asks the learner to
// start in the attached command line before the evaluation can run.
type RuntimeService string

const (
	RuntimeHadoop RuntimeService = "Hadoop"
	RuntimeHBase  RuntimeService = "HBase"
)

// runtimeStarts are fixed preparation scripts. Each one starts its service
// only when it is not already up and exits 0 only after its own read-only
// readiness check passed. No model output or statement text runs here.
var runtimeStarts = map[RuntimeService]struct {
	script  string
	timeout time.Duration
}{
	// The bootstrap observed in the user's Hadoop HAR.
	RuntimeHadoop: {"set -eu\ncommand -v start-dfs.sh >/dev/null\nif ! hdfs dfs -test -d / >/dev/null 2>&1; then\n  start-dfs.sh\nfi\nfor attempt in 1 2 3 4 5 6 7 8 9 10; do\n  if hdfs dfs -test -d / >/dev/null 2>&1; then exit 0; fi\n  sleep 1\ndone\nexit 1\n", 90 * time.Second},
	// The steps the HBase Java statements give (2026-11-27): start HDFS with
	// start-dfs.sh and HBase with start-hbase.sh, then wait about 20 seconds
	// for HBase to initialize. HDFS is best effort, since a standalone HBase
	// does not need it; HBase is ready once its shell can list tables, which a
	// master that is still initializing refuses.
	RuntimeHBase: {`set -u
command -v start-hbase.sh >/dev/null 2>&1 || exit 1
if command -v start-dfs.sh >/dev/null 2>&1 && ! hdfs dfs -test -d / >/dev/null 2>&1; then
  start-dfs.sh
  n=0
  while [ "$n" -lt 20 ] && ! hdfs dfs -test -d / >/dev/null 2>&1; do n=$((n + 1)); sleep 1; done
fi
if ! jps 2>/dev/null | grep -q HMaster; then
  start-hbase.sh
fi
wrap=""
if command -v timeout >/dev/null 2>&1; then wrap="timeout 60"; fi
n=0
while [ "$n" -lt 15 ]; do
  if printf 'list\nexit\n' | $wrap hbase shell 2>/dev/null | grep -q 'row(s)'; then exit 0; fi
  n=$((n + 1))
  sleep 4
done
exit 1
`, 5 * time.Minute},
}

// StartRuntime runs the fixed start script for service in the command line
// attached to the task's editor. Mixed editor tasks retain their editor
// contract; the receipt is environment preparation, never submission evidence.
func (c *Client) StartRuntime(ctx context.Context, t *Task, service RuntimeService) error {
	start, ok := runtimeStarts[service]
	if !ok {
		return fmt.Errorf("没有 %s 的固定启动流程", service)
	}
	terminal, err := attachedTerminalEnvironment(t)
	if err != nil {
		return err
	}
	result, err := c.runBoundTerminalCommand(ctx, t, terminal, start.script, TerminalOptions{Timeout: start.timeout})
	if err != nil {
		return err
	}
	if result == nil || !result.Completed || result.ExitCode == nil || *result.ExitCode != 0 {
		return fmt.Errorf("%s 启动后未确认就绪", service)
	}
	return nil
}

// StartHadoop prepares the recorded Hadoop workspace before its script runs.
func (c *Client) StartHadoop(ctx context.Context, t *Task) error {
	return c.StartRuntime(ctx, t, RuntimeHadoop)
}
