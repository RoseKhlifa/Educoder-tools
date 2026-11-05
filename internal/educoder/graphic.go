package educoder

// The graphic route is inferred from the upstream StarUML client. It does not
// invent an HTTP upload endpoint: .mdj files live in the authenticated runtime's
// Git workspace, followed by the ordinary game_build/game_status API.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const GraphicWorkspace = "/data/workspace/myshixun"
const MaxGraphicBytes = 4 << 20

var graphicTags = regexp.MustCompile(`<[^>]*>`)
var graphicFullPath = regexp.MustCompile(`(?:/data/workspace/myshixun/|/home/headless/Desktop/workspace/myshixun/|workspace/myshixun/)[^\s\x60"'<>，。；（）)]+(?i:\.mdj)`)
var graphicQuotedDir = regexp.MustCompile(`["“]([^"”]+)["”]\s*目录下`)
var graphicSaveDir = regexp.MustCompile(`保存在[^，。；]*?((?:/data/workspace/myshixun/|workspace/myshixun/)[^\s，。；]+)`)
var graphicName = regexp.MustCompile(`(?:命名为|文件名改为)\s*[\x60"“']?([A-Za-z0-9_-]+(?:\.mdj)?)`)
var graphicFormat = regexp.MustCompile(`格式为\s*[\x60"“']?([A-Za-z0-9]+)`)

func normalizeGraphicPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	for _, prefix := range []string{GraphicWorkspace + "/", "/home/headless/Desktop/workspace/myshixun/", "workspace/myshixun/", "workspace/"} {
		if strings.HasPrefix(value, prefix) {
			value = strings.TrimPrefix(value, prefix)
			break
		}
	}
	if value == "" || len(value) > 1024 || !utf8.ValidString(value) || strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\\\x00\r\n") || !strings.EqualFold(path.Ext(value), ".mdj") {
		return "", errors.New("图形文件必须是工作区内明确的 .mdj 相对路径")
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." || strings.HasPrefix(part, ".git") || part == "pingce" || part == "secret" {
			return "", errors.New("图形文件路径包含受保护或不明确的目录")
		}
	}
	return value, nil
}

// GraphicPaths returns only explicit .mdj paths. Different explicit targets are
// intentionally retained as ambiguous; callers never spray copies into guessed
// directories such as submit/ and submit_step_N/.
func (t *Task) GraphicPaths() []string {
	if t == nil {
		return nil
	}
	seen := map[string]bool{}
	var result []string
	add := func(raw string) {
		if p, err := normalizeGraphicPath(raw); err == nil && !seen[p] {
			seen[p] = true
			result = append(result, p)
		}
	}
	// Task instructions supersede an editor path, which often names a readme.
	text := html.UnescapeString(graphicTags.ReplaceAllString(t.Challenge.Description, " "))
	for _, p := range graphicFullPath.FindAllString(text, -1) {
		add(p)
	}
	if len(result) == 0 {
		var dir string
		if m := graphicQuotedDir.FindStringSubmatch(text); len(m) > 0 {
			dir = m[1]
		}
		if m := graphicSaveDir.FindStringSubmatch(text); dir == "" && len(m) > 0 {
			dir = m[1]
		}
		dir = strings.TrimSuffix(strings.TrimSuffix(strings.TrimRight(dir, "/"), "文件夹下"), "下")
		dir = strings.TrimRight(dir, "/")
		name := graphicName.FindStringSubmatch(text)
		format := graphicFormat.FindStringSubmatch(text)
		if dir != "" && len(name) > 0 && (len(format) == 0 || strings.EqualFold(format[1], "mdj")) {
			filename := name[1]
			if path.Ext(filename) == "" {
				filename += ".mdj"
			}
			add(dir + "/" + filename)
		}
	}
	if len(result) == 0 {
		for _, p := range strings.FieldsFunc(t.Challenge.Path, func(r rune) bool { return r == '；' || r == ';' }) {
			add(p)
		}
	}
	return result
}

// VNCFlag retains only whether a runtime URL exists. The URL itself may contain
// access credentials and must never become part of task JSON or model input.
type VNCFlag bool

func (v *VNCFlag) UnmarshalJSON(data []byte) error {
	var enabled bool
	if json.Unmarshal(data, &enabled) == nil {
		*v = VNCFlag(enabled)
		return nil
	}
	var address string
	if json.Unmarshal(data, &address) == nil {
		*v = VNCFlag(strings.TrimSpace(address) != "")
		return nil
	}
	*v = false
	return nil
}

func graphicEnvironment(t *Task) (ProgrammingEnvironment, error) {
	if t == nil {
		return ProgrammingEnvironment{}, errors.New("缺少图形任务")
	}
	var found *ProgrammingEnvironment
	for i := range t.Environments {
		env := &t.Environments[i]
		if env.EnvironmentID > 0 && env.TabType == 3 && (bool(env.VNCURL) || bool(t.VNCURL)) {
			if found != nil {
				return ProgrammingEnvironment{}, errors.New("图形任务有多个未消歧的 VNC 环境")
			}
			found = env
		}
	}
	if found == nil {
		return ProgrammingEnvironment{}, errors.New("图形任务缺少明确的 tab_type=3 VNC 环境")
	}
	return *found, nil
}

func requireGraphic(t *Task) (string, ProgrammingEnvironment, error) {
	if t == nil || len(t.Chooses) > 0 || validIdentifier(t.Game.Identifier) != nil || validIdentifier(t.MyShixun.Identifier) != nil || validIdentifier(t.CourseIdentifier) != nil || t.HomeworkID <= 0 || t.MyShixun.ID <= 0 || t.Game.ID <= 0 || t.Game.MyShixunID != t.MyShixun.ID || t.User.UserID <= 0 || t.Challenge.ID <= 0 {
		return "", ProgrammingEnvironment{}, errors.New("图形任务上下文缺失或身份不匹配")
	}
	paths := t.GraphicPaths()
	if len(paths) != 1 {
		return "", ProgrammingEnvironment{}, errors.New("图形任务必须明确指定唯一的 .mdj 保存路径")
	}
	env, err := graphicEnvironment(t)
	return paths[0], env, err
}

func ValidateGraphicContent(content string) error {
	if len(content) == 0 || len(content) > MaxGraphicBytes || !utf8.ValidString(content) {
		return errors.New("StarUML 文件必须是 1 至 4 MiB 内的 UTF-8 JSON")
	}
	var project struct {
		Type string `json:"_type"`
	}
	if json.Unmarshal([]byte(content), &project) != nil || project.Type != "Project" {
		return errors.New("StarUML 文件必须是顶层 _type=Project 的有效 JSON")
	}
	return nil
}

func graphicHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// GraphicConnection exists only during an operation. Passwords, addresses and
// usernames deliberately cannot be JSON-serialized into jobs/model snapshots.
type GraphicConnection struct {
	Host     string `json:"-"`
	Port     int    `json:"-"`
	Username string `json:"-"`
	Password string `json:"-"`
}

// GraphicTransport implementations must enforce exact workspace paths and
// compare expectedHash/expectedExists immediately before a file mutation.
type GraphicTransport interface {
	Open(context.Context, GraphicConnection) (GraphicSession, error)
}
type GraphicSession interface {
	ReadFile(context.Context, string) (content []byte, exists bool, err error)
	SaveAndCommit(ctx context.Context, relativePath, expectedHash string, expectedExists bool, content []byte) (commitID string, err error)
	Close() error
}

type GraphicDocument struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	Hash       string `json:"hash"`
	Exists     bool   `json:"exists"`
	game       string
	myshixunID int64
	userID     int64
}
type GraphicSubmitResult struct {
	ContentHash string            `json:"content_hash"`
	CommitID    string            `json:"commit_id"`
	Saved       bool              `json:"saved"`
	Submitted   bool              `json:"submitted"`
	Document    *GraphicDocument  `json:"-"`
	Evaluation  *EvaluationResult `json:"evaluation,omitempty"`
	Confirmed   bool              `json:"confirmed"`
}
type GraphicProtocol struct {
	Client    *Client
	Transport GraphicTransport
}

// Graphic startup is eventually consistent upstream.  A successful start
// request can briefly return an empty or stale runtime binding while the
// workspace is being materialized.  These retries are deliberately small and
// bounded: they only repeat idempotent reads/connection attempts and never
// repeat a file save or a formal build.
const (
	graphicReadinessAttempts = 3
	graphicReadinessDelay    = 150 * time.Millisecond
)

func waitGraphicRetry(ctx context.Context, attempt int) error {
	if attempt >= graphicReadinessAttempts-1 {
		return nil
	}
	delay := graphicReadinessDelay * time.Duration(attempt+1)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func NewGraphicProtocol(client *Client, transport GraphicTransport) *GraphicProtocol {
	if transport == nil {
		transport = NewSSHGraphicTransport(GraphicSSHOptions{})
	}
	return &GraphicProtocol{Client: client, Transport: transport}
}

func (p *GraphicProtocol) open(ctx context.Context, t *Task) (GraphicSession, error) {
	_, env, err := requireGraphic(t)
	if err != nil {
		return nil, err
	}
	if p == nil || p.Client == nil || p.Transport == nil {
		return nil, errors.New("图形协议未初始化")
	}
	query := url.Values{"shixun_environment_id": {strconv.FormatInt(env.EnvironmentID, 10)}, "tab_type": {"3"}, "game_id": {strconv.FormatInt(t.Game.ID, 10)}, "homework_common_id": {strconv.FormatInt(t.HomeworkID, 10)}}
	var lastErr error
	for attempt := 0; attempt < graphicReadinessAttempts; attempt++ {
		var response struct {
			Data struct {
				SSHAddress string          `json:"ssh_address"`
				Port       json.RawMessage `json:"port"`
				Username   string          `json:"username"`
				Password   string          `json:"password"`
				MyShixunID *int64          `json:"myshixun_id"`
				Code       *int            `json:"code"`
			} `json:"data"`
		}
		if err := p.Client.request(ctx, http.MethodGet, "/api/myshixuns/"+t.MyShixun.Identifier+"/start.json", query, nil, taskRef(t), &response); err != nil {
			lastErr = err
		} else {
			d := response.Data
			var port int
			if json.Unmarshal(d.Port, &port) != nil {
				var text string
				if json.Unmarshal(d.Port, &text) == nil {
					port, _ = strconv.Atoi(text)
				}
			}
			if d.Code != nil && *d.Code != 0 || d.MyShixunID != nil && *d.MyShixunID != t.MyShixun.ID || port <= 0 || port > 65535 || d.SSHAddress == "" || d.Username == "" || d.Password == "" {
				lastErr = errors.New("图形环境未就绪或返回不匹配的运行实例")
			} else {
				session, openErr := p.Transport.Open(ctx, GraphicConnection{Host: d.SSHAddress, Port: port, Username: d.Username, Password: d.Password})
				if openErr == nil {
					return session, nil
				}
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				lastErr = errors.New("图形环境 SSH 连接失败")
			}
		}
		if err := waitGraphicRetry(ctx, attempt); err != nil {
			return nil, err
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("图形环境未就绪或返回不匹配的运行实例")
}

func (p *GraphicProtocol) Read(ctx context.Context, t *Task) (*GraphicDocument, error) {
	file, _, err := requireGraphic(t)
	if err != nil {
		return nil, err
	}
	session, err := p.open(ctx, t)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	content, exists, err := session.ReadFile(ctx, file)
	if err != nil {
		return nil, errors.New("图形文件读取失败")
	}
	if len(content) > MaxGraphicBytes || !utf8.Valid(content) {
		return nil, errors.New("图形文件不是支持范围内的 UTF-8 内容")
	}
	return &GraphicDocument{Path: file, Content: string(content), Hash: graphicHash(content), Exists: exists, game: t.Game.Identifier, myshixunID: t.MyShixun.ID, userID: t.User.UserID}, nil
}

func (p *GraphicProtocol) Submit(ctx context.Context, t *Task, document *GraphicDocument, content string, options PollOptions) (*GraphicSubmitResult, error) {
	if p == nil || p.Client == nil || p.Transport == nil {
		return nil, errors.New("图形协议未初始化")
	}
	file, env, err := requireGraphic(t)
	if err != nil {
		return nil, err
	}
	if err = ValidateGraphicContent(content); err != nil {
		return nil, err
	}
	if document == nil || document.Path != file || document.game != t.Game.Identifier || document.myshixunID != t.MyShixun.ID || document.userID != t.User.UserID || document.Hash != graphicHash([]byte(document.Content)) {
		return nil, errors.New("图形提交必须绑定本次读取的任务和文件版本")
	}
	current, err := p.Client.LoadTask(ctx, t.CourseIdentifier, t.HomeworkID, t.Game.Identifier)
	if err != nil {
		return nil, err
	}
	currentFile, currentEnv, err := requireGraphic(current)
	if err != nil || currentFile != file || current.User.UserID != t.User.UserID || current.MyShixun.ID != t.MyShixun.ID || current.Game.ID != t.Game.ID || current.MyShixun.Identifier != t.MyShixun.Identifier || currentEnv.EnvironmentID != env.EnvironmentID {
		return nil, errors.New("图形任务身份或目标路径在提交前发生变化")
	}
	session, err := p.open(ctx, current)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	commit, err := session.SaveAndCommit(ctx, file, document.Hash, document.Exists, []byte(content))
	if err != nil {
		if errors.Is(err, ErrGraphicChanged) {
			return nil, ErrGraphicChanged
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("图形文件保存或版本核对失败")
	}
	if !graphicCommit.MatchString(commit) {
		return nil, errors.New("图形环境没有返回有效 Git 提交版本")
	}
	result := &GraphicSubmitResult{ContentHash: graphicHash([]byte(content)), CommitID: commit, Saved: true}
	result.Document = &GraphicDocument{Path: file, Content: content, Hash: result.ContentHash, Exists: true, game: t.Game.Identifier, myshixunID: t.MyShixun.ID, userID: t.User.UserID}
	// Tokens are acquired after the commit and remain in local stack variables.
	var state struct {
		EvaluationResult
		SecKey string `json:"sec_key"`
	}
	statusQuery := url.Values{"homework_common_id": {strconv.FormatInt(t.HomeworkID, 10)}}
	var statusErr error
	for attempt := 0; attempt < graphicReadinessAttempts; attempt++ {
		state = struct {
			EvaluationResult
			SecKey string `json:"sec_key"`
		}{}
		statusErr = p.Client.requestWithStatuses(ctx, http.MethodGet, "/api/tasks/"+t.Game.Identifier+"/game_status.json", statusQuery, nil, taskRef(t), &state, 0, 1, 2)
		if statusErr == nil && state.SecKey != "" {
			break
		}
		if retryErr := waitGraphicRetry(ctx, attempt); retryErr != nil {
			return result, retryErr
		}
	}
	if statusErr != nil {
		return result, statusErr
	}
	if state.SecKey == "" {
		return result, errors.New("图形评测未返回当前关卡评测密钥，文件已保存但未提交")
	}
	extras := programmingExtras(current)
	extras["commitID"] = commit
	body := map[string]any{"sec_key": state.SecKey, "resubmit": "", "first": 1, "content_modified": 0, "shixun_environment_id": env.EnvironmentID, "tab_type": 3, "extras": extras}
	var build BuildResult
	if err = p.Client.requestWithStatuses(ctx, http.MethodPost, "/api/tasks/"+t.Game.Identifier+"/game_build.json", nil, body, taskRef(t), &build, 1); err != nil {
		return result, err
	}
	if build.Status != 1 {
		return result, errors.New("图形评测没有返回明确启动状态")
	}
	result.Submitted = true
	query := url.Values{"resubmit": {""}, "time_out": {"false"}, "port": {strconv.Itoa(build.Port)}, "sec_key": {state.SecKey}, "challenge_id": {strconv.FormatInt(t.Challenge.ID, 10)}, "subject_id": {""}, "homework_common_id": {strconv.FormatInt(t.HomeworkID, 10)}}
	result.Evaluation, err = p.wait(ctx, current, query, state.EvaluationResult, options)
	if err != nil {
		return result, err
	}
	stored, exists, readErr := session.ReadFile(ctx, file)
	if readErr != nil {
		return result, errors.New("图形评测后文件复核失败")
	}
	if !exists || graphicHash(stored) != result.ContentHash {
		return result, ErrGraphicChanged
	}
	if result.Evaluation.Passed {
		check, loadErr := p.Client.LoadTask(ctx, t.CourseIdentifier, t.HomeworkID, t.Game.Identifier)
		if loadErr != nil {
			return result, loadErr
		}
		checkFile, checkEnv, checkErr := requireGraphic(check)
		result.Confirmed = checkErr == nil && checkFile == file && checkEnv.EnvironmentID == env.EnvironmentID && check.Game.Status == 2 && check.User.UserID == t.User.UserID && check.MyShixun.ID == t.MyShixun.ID && check.Game.ID == t.Game.ID && check.MyShixun.Identifier == t.MyShixun.Identifier
	}
	return result, nil
}

var graphicCommit = regexp.MustCompile(`^[a-fA-F0-9]{40,64}$`)

func graphicVerdictSignature(result EvaluationResult) string {
	result.RunningCodeStatus = nil
	result.NextGame = nil
	result.HadDone = 0
	result.Completed = false
	result.Passed = false
	data, _ := json.Marshal(result)
	return string(data)
}

func (p *GraphicProtocol) wait(ctx context.Context, t *Task, query url.Values, baseline EvaluationResult, options PollOptions) (*EvaluationResult, error) {
	timeout, interval := options.Timeout, options.Interval
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	if interval <= 0 {
		interval = time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	observedRunning := false
	for {
		var r EvaluationResult
		err := p.Client.requestWithStatuses(ctx, http.MethodGet, "/api/tasks/"+t.Game.Identifier+"/game_status.json", query, nil, taskRef(t), &r, 0, 2)
		if err != nil && !errors.Is(err, errResponseNotModified) {
			return nil, err
		}
		if err == nil {
			if r.RunningCodeStatus != nil && (*r.RunningCodeStatus == 1 || *r.RunningCodeStatus == 2) {
				observedRunning = true
			} else if !observedRunning && graphicVerdictSignature(r) == graphicVerdictSignature(baseline) {
				// An unchanged cached verdict does not prove this commit ran.
			} else if r.Status != nil && *r.Status == 0 && evaluationHasFailure(r) {
				r.Completed = true
				return &r, nil
			} else if r.Status != nil && *r.Status == 2 {
				r.Completed = true
				r.Passed = r.TestSetsCount != nil && *r.TestSetsCount > 0 && *r.TestSetsCount == len(r.TestSets) && r.SetsErrorCount != nil && *r.SetsErrorCount == 0
				for _, test := range r.TestSets {
					if test.Result == nil || !*test.Result || test.CompileSuccess != nil && *test.CompileSuccess != 1 {
						r.Passed = false
					}
				}
				return &r, nil
			} else if r.Status != nil && *r.Status == 0 {
				// After game_build, Educoder can report status=0 while the
				// formal evaluator is queued or the runtime is materializing.
				// This is a non-terminal state; keep polling until status=2,
				// an explicit failure, or the bounded context timeout.
			} else {
				return nil, errors.New("图形评测返回未确认的状态组合")
			}
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
