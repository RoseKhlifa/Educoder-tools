package educoder

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
)

type TaskKind string

const (
	TaskKindProgramming TaskKind = "programming"
	TaskKindChoice      TaskKind = "choice"
	// TaskKindObjective is an explicit alias for callers that use the
	// objective-question terminology. Both values serialize as "choice".
	TaskKindObjective TaskKind = TaskKindChoice
	TaskKindUnknown   TaskKind = "unknown"
)

var errResponseNotModified = errors.New("upstream response not modified")

type ProgrammingEnvironment struct {
	EnvironmentID int64  `json:"shixun_environment_id"`
	TabType       int    `json:"tab_type"`
	Name          string `json:"name"`
}

// Kind requires every observed discriminator. Missing zero-valued fields do
// not silently turn an unobserved question type into a programming task.
func (t *Task) Kind() TaskKind {
	if t != nil && t.ST != nil && *t.ST == 0 && t.Challenge.ST != nil && *t.Challenge.ST == 0 && t.Challenge.WithCodeFile != nil && *t.Challenge.WithCodeFile {
		return TaskKindProgramming
	}
	if t != nil && t.ST != nil && *t.ST == 1 && t.Challenge.ST != nil && *t.Challenge.ST == 1 && len(t.Chooses) > 0 {
		return TaskKindChoice
	}
	return TaskKindUnknown
}

func (t *Task) CodePaths() []string {
	if t == nil {
		return nil
	}
	var paths []string
	seen := map[string]bool{}
	for _, item := range strings.Split(t.Challenge.Path, "；") {
		item = strings.TrimSpace(item)
		if item != "" && !seen[item] {
			paths = append(paths, item)
			seen[item] = true
		}
	}
	return paths
}

func (c *Client) LoadTask(ctx context.Context, course string, homeworkID int64, game string) (*Task, error) {
	if err := validIdentifier(course); err != nil {
		return nil, err
	}
	if err := validIdentifier(game); err != nil {
		return nil, err
	}
	if homeworkID <= 0 {
		return nil, errors.New("homework ID 必须大于 0")
	}
	var task Task
	ref := "/tasks/" + course + "/" + strconv.FormatInt(homeworkID, 10) + "/" + game
	if err := c.request(ctx, http.MethodGet, "/api/tasks/"+game+".json", url.Values{"homework_common_id": {strconv.FormatInt(homeworkID, 10)}}, nil, ref, &task); err != nil {
		return nil, err
	}
	if task.Game.Identifier != game {
		return nil, errors.New("任务详情缺少或返回不匹配的 game.identifier")
	}
	if task.HomeworkID != 0 && task.HomeworkID != homeworkID {
		return nil, errors.New("任务详情的 homework_common_id 与所选作业不匹配")
	}
	task.HomeworkID = homeworkID
	task.CourseIdentifier = course
	task.URL = c.Origin + ref
	return &task, nil
}

// CodeFile is the current editor file when read, not a pristine template.
// Filename and language preserve the response values, including non-ASCII names.
type CodeFile struct {
	Path      string `json:"path"`
	Filename  string `json:"filename"`
	Extension string `json:"extension"`
	Language  string `json:"language"`
	FileType  string `json:"file_type"`
	Content   string `json:"content"`
	Size      int64  `json:"size"`
}

type ProgrammingTask struct {
	Kind     TaskKind   `json:"kind"`
	Task     *Task      `json:"task"`
	Markdown string     `json:"markdown"`
	Files    []CodeFile `json:"files"`
}

func requireProgramming(t *Task) error {
	if t.Kind() != TaskKindProgramming {
		return errors.New("题型为 unknown：本链路仅覆盖录制中 st=0 且 with_code_file=true 的代码题")
	}
	if validIdentifier(t.Game.Identifier) != nil || t.HomeworkID <= 0 {
		return errors.New("代码题缺少 game.identifier 或 homework_common_id")
	}
	return nil
}

func taskRef(t *Task) string {
	return "/tasks/" + t.CourseIdentifier + "/" + strconv.FormatInt(t.HomeworkID, 10) + "/" + t.Game.Identifier
}

func requireCodePath(t *Task, filePath string) error {
	if err := requireProgramming(t); err != nil {
		return err
	}
	for _, candidate := range t.CodePaths() {
		if filePath == candidate {
			return nil
		}
	}
	return errors.New("文件路径必须来自该关卡 challenge.path")
}

func (c *Client) ReadCodeFile(ctx context.Context, t *Task, filePath string) (*CodeFile, error) {
	if err := requireCodePath(t, filePath); err != nil {
		return nil, err
	}
	var response struct {
		Content struct {
			Content string `json:"content"`
			Size    int64  `json:"size"`
		} `json:"content"`
		Filename string `json:"filename"`
		Language string `json:"language"`
		FileType string `json:"file_type"`
	}
	query := url.Values{"path": {filePath}, "homework_common_id": {strconv.FormatInt(t.HomeworkID, 10)}}
	if err := c.request(ctx, http.MethodGet, "/api/tasks/"+t.Game.Identifier+"/rep_content.json", query, nil, taskRef(t), &response); err != nil {
		return nil, err
	}
	content, err := base64.StdEncoding.DecodeString(response.Content.Content)
	if err != nil || !utf8.Valid(content) {
		return nil, errors.New("文件 content.content 不是有效的 Base64 UTF-8 代码")
	}
	return &CodeFile{Path: filePath, Filename: response.Filename, Extension: path.Ext(response.Filename), Language: response.Language, FileType: response.FileType, Content: string(content), Size: response.Content.Size}, nil
}

func (c *Client) ReadProgramming(ctx context.Context, t *Task) (*ProgrammingTask, error) {
	if err := requireProgramming(t); err != nil {
		return nil, err
	}
	result := &ProgrammingTask{Kind: t.Kind(), Task: t, Markdown: t.Challenge.Description, Files: []CodeFile{}}
	for _, filePath := range t.CodePaths() {
		file, err := c.ReadCodeFile(ctx, t, filePath)
		if err != nil {
			return nil, err
		}
		result.Files = append(result.Files, *file)
	}
	if len(result.Files) == 0 {
		return nil, errors.New("代码题 challenge.path 没有文件")
	}
	return result, nil
}

func programmingEnvironment(t *Task) (ProgrammingEnvironment, error) {
	if err := requireProgramming(t); err != nil {
		return ProgrammingEnvironment{}, err
	}
	for _, environment := range t.Environments {
		if environment.EnvironmentID == t.CodeEditor.EnvironmentID && environment.EnvironmentID > 0 && environment.TabType == 1 {
			return environment, nil
		}
	}
	return ProgrammingEnvironment{}, errors.New("缺少与 code_editor 匹配的 tab_type=1 编程环境")
}

func programmingExtras(t *Task) map[string]any {
	return map[string]any{"exercise_id": "", "question_id": "", "challenge_id": t.Challenge.ID, "subject_id": "", "smart_plan_page_item_bank_id": "", "homework_common_id": strconv.FormatInt(t.HomeworkID, 10), "competition_entry_id": "", "currentUserId": t.User.UserID}
}

// SavedCode keeps the evaluation key in memory only. JSON output omits it.
// Its private binding prevents another task/file's save response being reused.
type SavedCode struct {
	CommitID        string `json:"commitID"`
	Resubmit        string `json:"resubmit"`
	ContentModified int    `json:"content_modified"`
	Size            int64  `json:"size"`
	Evaluate        int    `json:"evaluate"`
	SecKey          string `json:"-"`
	gameIdentifier  string
	filePath        string
	myshixunID      int64
	evaluationPort  int
}

func (c *Client) SaveCode(ctx context.Context, t *Task, filePath, content string, evaluate bool) (*SavedCode, error) {
	if err := requireCodePath(t, filePath); err != nil {
		return nil, err
	}
	environment, err := programmingEnvironment(t)
	if err != nil {
		return nil, err
	}
	if validIdentifier(t.MyShixun.Identifier) != nil || t.MyShixun.ID <= 0 || t.Game.ID <= 0 || t.Challenge.ID <= 0 || t.User.UserID <= 0 || t.Game.MyShixunID != t.MyShixun.ID {
		return nil, errors.New("保存缺少或不匹配 myshixun、game、challenge 或 user.user_id 上下文")
	}
	if !utf8.ValidString(content) {
		return nil, errors.New("代码内容必须是 UTF-8")
	}
	mode := 0
	if evaluate {
		mode = 1
	}
	body := map[string]any{"path": filePath, "evaluate": mode, "content": content, "game_id": t.Game.ID, "tab_type": environment.TabType, "exercise_id": nil, "homework_common_id": strconv.FormatInt(t.HomeworkID, 10), "extras": programmingExtras(t)}
	var response struct {
		Content struct {
			CommitID string `json:"commitID"`
			Size     int64  `json:"size"`
		} `json:"content"`
		Resubmit        string `json:"resubmit"`
		SecKey          string `json:"sec_key"`
		ContentModified int    `json:"content_modified"`
	}
	if err := c.request(ctx, http.MethodPost, "/api/myshixuns/"+t.MyShixun.Identifier+"/update_file.json", nil, body, taskRef(t), &response); err != nil {
		return nil, err
	}
	if response.Content.CommitID == "" || evaluate && response.SecKey == "" {
		return nil, errors.New("保存响应缺少 commitID 或评测密钥")
	}
	return &SavedCode{CommitID: response.Content.CommitID, Resubmit: response.Resubmit, ContentModified: response.ContentModified, Size: response.Content.Size, Evaluate: mode, SecKey: response.SecKey, gameIdentifier: t.Game.Identifier, filePath: filePath, myshixunID: t.MyShixun.ID}, nil
}

type SelfTestOptions struct {
	Timeout        time.Duration
	MaxOutputBytes int64
	ExpectedOutput *string
}

type SelfTestResult struct {
	Completed bool   `json:"completed"`
	Output    string `json:"output"`
	// Matches is absent unless the caller supplied expected output. Comparison
	// only normalizes CRLF; spaces and a final newline remain significant.
	Matches *bool `json:"matches,omitempty"`
}

// SelfTest saves with evaluate=0 and streams one explicit stdin execution.
// A matching output is an observation, and never triggers a submission.
func (c *Client) SelfTest(ctx context.Context, t *Task, filePath, content, input string, options SelfTestOptions) (*SelfTestResult, error) {
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	limit := options.MaxOutputBytes
	if limit <= 0 {
		limit = 1 << 20
	}
	if limit > 16<<20 {
		return nil, errors.New("自测输出上限至多为 16 MiB")
	}
	if _, err := c.SaveCode(ctx, t, filePath, content, false); err != nil {
		return nil, err
	}
	environment, _ := programmingEnvironment(t)
	body := map[string]any{"test_case_content": input, "shixun_environment_id": environment.EnvironmentID, "tab_type": environment.TabType, "extras": map[string]any{"homework_common_id": strconv.FormatInt(t.HomeworkID, 10)}}
	var response struct {
		Data struct {
			URL string `json:"wss_url"`
		} `json:"data"`
	}
	if err := c.request(ctx, http.MethodPost, "/api/tasks/"+t.Game.Identifier+"/log_output", nil, body, taskRef(t), &response); err != nil {
		return nil, err
	}
	endpoint, err := url.Parse(response.Data.URL)
	if err != nil || (endpoint.Scheme != "wss" && endpoint.Scheme != "ws") || endpoint.Host == "" || endpoint.User != nil || endpoint.Fragment != "" {
		return nil, errors.New("自测响应缺少有效 WebSocket 地址")
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/log"
	// This runtime stream is a separate origin; do not copy the API CookieJar
	// or the HTTP signature/session header to it.
	wsHTTP := &http.Client{Transport: c.HTTP.Transport, Timeout: c.HTTP.Timeout}
	connection, upgrade, err := websocket.Dial(ctx, endpoint.String(), &websocket.DialOptions{HTTPClient: wsHTTP, HTTPHeader: http.Header{"Origin": {c.Origin}}})
	if upgrade != nil && upgrade.Body != nil {
		defer upgrade.Body.Close()
	}
	if err != nil {
		return nil, errors.New("自测 WebSocket 连接失败")
	}
	defer connection.CloseNow()
	connection.SetReadLimit(int64(base64.StdEncoding.EncodedLen(int(limit))) + 1024)
	request, _ := json.Marshal(map[string]any{"tp": "log", "data": t.MyShixun.ID})
	if err := connection.Write(ctx, websocket.MessageText, request); err != nil {
		return nil, errors.New("自测 WebSocket 启动消息发送失败")
	}
	result := &SelfTestResult{}
	var output strings.Builder
	for {
		_, message, err := connection.Read(ctx)
		if err != nil {
			result.Output = output.String()
			if ctx.Err() != nil {
				return result, fmt.Errorf("自测等待结束: %w", ctx.Err())
			}
			return result, errors.New("自测流在 ProcessFinished 前关闭或读取失败")
		}
		// The published task bundle Base64-decodes O.data before consuming
		// stdout. This is wire encoding, including on binary message frames.
		decoded, decodeErr := base64.StdEncoding.DecodeString(string(message))
		if decodeErr != nil {
			decoded, decodeErr = base64.RawStdEncoding.DecodeString(string(message))
		}
		if decodeErr != nil || !utf8.Valid(decoded) {
			return result, errors.New("自测帧不是有效的 Base64 UTF-8 数据")
		}
		if string(decoded) == "ProcessFinished" {
			result.Completed = true
			result.Output = output.String()
			if options.ExpectedOutput != nil {
				matches := strings.ReplaceAll(result.Output, "\r\n", "\n") == strings.ReplaceAll(*options.ExpectedOutput, "\r\n", "\n")
				result.Matches = &matches
			}
			_ = connection.Close(websocket.StatusNormalClosure, "")
			return result, nil
		}
		if int64(output.Len())+int64(len(decoded)) > limit {
			result.Output = output.String()
			return result, errors.New("自测输出超过字节上限")
		}
		output.Write(decoded)
	}
}

type BuildResult struct {
	Status int `json:"status"`
	Port   int `json:"port"`
}

func validateSaved(t *Task, saved *SavedCode, evaluation bool) error {
	if err := requireProgramming(t); err != nil {
		return err
	}
	if saved == nil || saved.gameIdentifier != t.Game.Identifier || saved.myshixunID != t.MyShixun.ID || saved.SecKey == "" || saved.CommitID == "" {
		return errors.New("评测需要当前关卡在内存中的保存响应")
	}
	if evaluation && saved.Evaluate != 1 {
		return errors.New("正式提交需要 evaluate=1 的保存响应")
	}
	return requireCodePath(t, saved.filePath)
}

func (c *Client) BuildCode(ctx context.Context, t *Task, saved *SavedCode) (*BuildResult, error) {
	if err := validateSaved(t, saved, true); err != nil {
		return nil, err
	}
	environment, err := programmingEnvironment(t)
	if err != nil {
		return nil, err
	}
	extras := programmingExtras(t)
	extras["commitID"] = saved.CommitID
	body := map[string]any{"sec_key": saved.SecKey, "resubmit": saved.Resubmit, "first": 1, "content_modified": saved.ContentModified, "shixun_environment_id": environment.EnvironmentID, "tab_type": environment.TabType, "extras": extras}
	var response BuildResult
	if err := c.requestWithStatuses(ctx, http.MethodPost, "/api/tasks/"+t.Game.Identifier+"/game_build.json", nil, body, taskRef(t), &response, 1); err != nil {
		return nil, err
	}
	if response.Status != 1 {
		return nil, errors.New("提交响应没有返回已观察的启动状态 1")
	}
	saved.evaluationPort = response.Port
	// res.success=false is present in both successful recorded starts.
	return &response, nil
}

type TestSet struct {
	IsPublic       bool     `json:"is_public"`
	Result         *bool    `json:"result"`
	Input          string   `json:"input"`
	Output         string   `json:"output"`
	ActualOutput   *string  `json:"actual_output"`
	CompileSuccess *int     `json:"compile_success"`
	Time           *float64 `json:"ts_time,omitempty"`
	Memory         *float64 `json:"ts_mem,omitempty"`
	IsFile         bool     `json:"is_file"`
}

type EvaluationResult struct {
	Status            *int      `json:"status,omitempty"`
	RunningCodeStatus *int      `json:"running_code_status,omitempty"`
	TestSetsCount     *int      `json:"test_sets_count,omitempty"`
	SetsErrorCount    *int      `json:"sets_error_count,omitempty"`
	TestSets          []TestSet `json:"test_sets,omitempty"`
	LastCompileOutput string    `json:"last_compile_output,omitempty"`
	NextGame          *string   `json:"next_game,omitempty"`
	HadDone           int       `json:"had_done"`
	Completed         bool      `json:"completed"`
	Passed            bool      `json:"passed"`
}

type PollOptions struct {
	Timeout  time.Duration
	Interval time.Duration
}

func (c *Client) WaitEvaluation(ctx context.Context, t *Task, saved *SavedCode, options PollOptions) (*EvaluationResult, error) {
	if err := validateSaved(t, saved, true); err != nil {
		return nil, err
	}
	timeout, interval := options.Timeout, options.Interval
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	if interval <= 0 {
		interval = time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	query := url.Values{"resubmit": {saved.Resubmit}, "time_out": {"false"}, "port": {strconv.Itoa(saved.evaluationPort)}, "sec_key": {saved.SecKey}, "challenge_id": {strconv.FormatInt(t.Challenge.ID, 10)}, "subject_id": {""}, "homework_common_id": {strconv.FormatInt(t.HomeworkID, 10)}}
	for {
		var response EvaluationResult
		err := c.requestWithStatuses(ctx, http.MethodGet, "/api/tasks/"+t.Game.Identifier+"/game_status.json", query, nil, taskRef(t), &response, 2)
		if err != nil && !errors.Is(err, errResponseNotModified) {
			return nil, err
		}
		if err == nil {
			if response.RunningCodeStatus != nil && (*response.RunningCodeStatus == 1 || *response.RunningCodeStatus == 2) {
				// Both are intermediate states in the recording.
			} else if response.Status != nil && *response.Status == 2 {
				response.Completed = true
				response.Passed = response.TestSetsCount != nil && *response.TestSetsCount > 0 && *response.TestSetsCount == len(response.TestSets) && response.SetsErrorCount != nil && *response.SetsErrorCount == 0
				for _, test := range response.TestSets {
					if test.Result == nil || !*test.Result || test.CompileSuccess == nil || *test.CompileSuccess != 1 {
						response.Passed = false
					}
				}
				return &response, nil
			} else {
				return nil, errors.New("评测返回未观察到的状态组合")
			}
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("评测轮询结束: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

// SubmitCode saves once, starts one evaluation, and polls it. Mutations are
// never retried, and next_game is returned for an explicit subsequent load.
func (c *Client) SubmitCode(ctx context.Context, t *Task, filePath, content string, options PollOptions) (*EvaluationResult, error) {
	saved, err := c.SaveCode(ctx, t, filePath, content, true)
	if err != nil {
		return nil, err
	}
	if _, err := c.BuildCode(ctx, t, saved); err != nil {
		return nil, err
	}
	return c.WaitEvaluation(ctx, t, saved, options)
}

type ChallengeProgress struct {
	ChallengeID    int64  `json:"challenge_id"`
	Name           string `json:"challenge_name"`
	ST             *int   `json:"challenge_st"`
	PassedStatus   int    `json:"passed_status"`
	EvaluateCount  int    `json:"evaluate_count"`
	ChallengeScore string `json:"challenge_score"`
	GameScore      string `json:"game_score"`
}

type HomeworkProgress struct {
	Challenges      []ChallengeProgress `json:"challenge_settings"`
	PassedCount     int                 `json:"passed_count"`
	NoEvaluateCount int                 `json:"no_evaluate_count"`
	ProgressCount   int                 `json:"progress_count"`
}

func (c *Client) HomeworkProgress(ctx context.Context, course string, homeworkID int64) (*HomeworkProgress, error) {
	if validIdentifier(course) != nil || homeworkID <= 0 {
		return nil, errors.New("进度读取需要课堂标识和正整数作业 ID")
	}
	var response struct {
		Data HomeworkProgress `json:"data"`
	}
	ref := "/classrooms/" + course + "/shixun_homework/" + strconv.FormatInt(homeworkID, 10) + "/detail?tabs=1"
	if err := c.request(ctx, http.MethodGet, "/api/homework_commons/"+strconv.FormatInt(homeworkID, 10)+"/shixun_challenge_data.json", nil, nil, ref, &response); err != nil {
		return nil, err
	}
	return &response.Data, nil
}
