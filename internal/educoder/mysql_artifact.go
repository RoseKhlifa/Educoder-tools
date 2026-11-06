package educoder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MySQLArtifactReference is an exact, public-task-bound reference. A stored
// DESCRIBE result supplies schema knowledge; it is never copied to the answer.
// Callers obtain this record from the reference bank, not from model output.
type MySQLArtifactReference struct {
	Experiment string
	Title      string
	Position   int
	Path       string
	Content    string
	Source     string
}

type MySQLColumn struct {
	Field    string `json:"field"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
	Key      string `json:"key"`
	Default  string `json:"default"`
	Extra    string `json:"extra"`
}

// MySQLArtifactPlan is immutable after construction. It contains schema SQL and
// source attribution, never public environment passwords or runtime sessions.
type MySQLArtifactPlan struct {
	Path         string `json:"path"`
	Source       string `json:"source"`
	SchemaSHA256 string `json:"schema_sha256"`
	columns      []MySQLColumn
	taskSHA256   string
	seal         string
}

var mysqlArtifactPathPattern = regexp.MustCompile(`/data/workspace/myshixun/[A-Za-z0-9_/-]+\.txt`)
var mysqlPublicCommand = regexp.MustCompile(`(?m)\bmysql[ \t]+-u([A-Za-z_][A-Za-z0-9_]{0,31})[ \t]+-p([A-Za-z0-9_.@%+:-]{1,128})[ \t]+-h127\.0\.0\.1(?:[ \t]*\r?\n|[ \t]*$)`)
var mysqlIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
var mysqlSimpleType = regexp.MustCompile(`^(tinyint|smallint|mediumint|int|bigint)(?:\(([0-9]{1,3})\))?( unsigned)?$`)
var mysqlStringType = regexp.MustCompile(`^(char|varchar)\(([0-9]{1,4})\)$`)
var mysqlDecimalType = regexp.MustCompile(`^decimal\(([0-9]{1,2}),([0-9]{1,2})\)$`)
var mysqlDefaultText = regexp.MustCompile(`^[A-Za-z0-9_ .:+-]{0,255}$`)

func normalizeMySQLArtifactPath(value string) (string, error) {
	value = strings.TrimPrefix(value, GraphicWorkspace+"/")
	if value == "" || len(value) > 512 || !strings.HasSuffix(value, ".txt") || strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\\\x00\r\n") {
		return "", errors.New("MySQL 结果必须是题干明确指定的工作区 txt 文件")
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." || strings.HasPrefix(part, ".") || part == "pingce" || part == "secret" || !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(part) {
			return "", errors.New("MySQL 结果路径包含不明确或受保护的目录")
		}
	}
	return value, nil
}

func mysqlArtifactTaskPath(t *Task) (string, error) {
	if t == nil || len(t.Chooses) > 0 {
		return "", errors.New("缺少明确的 MySQL VNC 任务")
	}
	text := t.Challenge.Description
	lower := strings.ToLower(text)
	if !strings.Contains(lower, "mysql") || !strings.Contains(lower, "desc") || !strings.Contains(lower, "txt") || len(mysqlPublicCommand.FindAllStringSubmatch(text, -1)) != 1 {
		return "", errors.New("任务没有明确的 MySQL 登录及 DESCRIBE 结果契约")
	}
	seen := map[string]bool{}
	for _, loc := range mysqlArtifactPathPattern.FindAllStringIndex(text, -1) {
		if !graphicPathStartBoundary(text, loc[0]) || !graphicValueBoundary(text, loc[1]) {
			return "", errors.New("MySQL 目标路径边界不明确")
		}
		p, err := normalizeMySQLArtifactPath(text[loc[0]:loc[1]])
		if err != nil {
			return "", err
		}
		seen[p] = true
	}
	if len(seen) != 1 {
		return "", errors.New("MySQL 任务必须明确指定唯一的 txt 结果路径")
	}
	for p := range seen {
		return p, nil
	}
	panic("unreachable")
}

// IsMySQLArtifactTask recognizes a VNC task whose public instructions require a
// real MySQL DESCRIBE artifact. It does not read checker files or infer targets.
func (t *Task) IsMySQLArtifactTask() bool { _, _, err := requireMySQLArtifact(t); return err == nil }

// MySQLArtifactTarget returns the explicit public result path.
func MySQLArtifactTarget(t *Task) (string, error) {
	p, _, err := requireMySQLArtifact(t)
	return p, err
}

func requireMySQLArtifact(t *Task) (string, ProgrammingEnvironment, error) {
	if t == nil || validIdentifier(t.Game.Identifier) != nil || validIdentifier(t.MyShixun.Identifier) != nil || validIdentifier(t.CourseIdentifier) != nil || t.HomeworkID <= 0 || t.MyShixun.ID <= 0 || t.Game.ID <= 0 || t.Game.MyShixunID != t.MyShixun.ID || t.User.UserID <= 0 || t.Challenge.ID <= 0 {
		return "", ProgrammingEnvironment{}, errors.New("MySQL 任务身份缺失或不匹配")
	}
	target, err := mysqlArtifactTaskPath(t)
	if err != nil {
		return "", ProgrammingEnvironment{}, err
	}
	env, err := graphicEnvironment(t)
	return target, env, err
}

func mysqlValidType(value string) bool {
	if m := mysqlSimpleType.FindStringSubmatch(value); m != nil {
		if m[2] == "" {
			return true
		}
		n, _ := strconv.Atoi(m[2])
		return n >= 1 && n <= 255
	}
	if m := mysqlStringType.FindStringSubmatch(value); m != nil {
		n, _ := strconv.Atoi(m[2])
		return n >= 1 && n <= 4096
	}
	if m := mysqlDecimalType.FindStringSubmatch(value); m != nil {
		p, _ := strconv.Atoi(m[1])
		s, _ := strconv.Atoi(m[2])
		return p >= 1 && p <= 65 && s <= 30 && s <= p
	}
	switch value {
	case "text", "tinytext", "mediumtext", "longtext", "double", "float", "date", "datetime", "timestamp", "time", "year":
		return true
	}
	return false
}

// ParseMySQLDescribe accepts only the ordinary six-column MySQL table format.
// Ambiguous aliases, unknown SQL syntax and trailing instructions are rejected.
func ParseMySQLDescribe(content string) ([]MySQLColumn, error) {
	if len(content) == 0 || len(content) > 64<<10 || !utf8.ValidString(content) {
		return nil, errors.New("MySQL 参考不是大小受限的 UTF-8 表结构")
	}
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(content, "\r\n", "\n")), "\n")
	if len(lines) < 5 {
		return nil, errors.New("MySQL 参考缺少标准 DESCRIBE 表")
	}
	border := regexp.MustCompile(`^\+(?:-+\+){6}$`)
	if !border.MatchString(strings.TrimSpace(lines[0])) || !border.MatchString(strings.TrimSpace(lines[2])) || !border.MatchString(strings.TrimSpace(lines[len(lines)-1])) {
		return nil, errors.New("MySQL 参考表边界不完整")
	}
	split := func(line string) ([]string, bool) {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") {
			return nil, false
		}
		parts := strings.Split(line[1:len(line)-1], "|")
		if len(parts) != 6 {
			return nil, false
		}
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		return parts, true
	}
	header, ok := split(lines[1])
	if !ok || strings.Join(header, ",") != "Field,Type,Null,Key,Default,Extra" {
		return nil, errors.New("MySQL 参考必须使用 Field/Type/Null/Key/Default/Extra 列")
	}
	var columns []MySQLColumn
	seen := map[string]bool{}
	primary, auto := 0, 0
	for _, line := range lines[3 : len(lines)-1] {
		v, ok := split(line)
		if !ok {
			return nil, errors.New("MySQL 参考包含无法解析的行")
		}
		name := strings.ToLower(v[0])
		typ := strings.ToLower(v[1])
		if !mysqlIdentifier.MatchString(v[0]) || seen[name] || !mysqlValidType(typ) || v[2] != "YES" && v[2] != "NO" || v[3] != "" && v[3] != "PRI" || v[5] != "" && v[5] != "auto_increment" || !mysqlDefaultText.MatchString(v[4]) {
			return nil, errors.New("MySQL 参考字段、类型、键或默认值不受支持")
		}
		if v[3] == "PRI" {
			primary++
			if v[2] != "NO" {
				return nil, errors.New("MySQL 主键不能允许 NULL")
			}
		}
		if v[5] != "" {
			auto++
			if v[3] != "PRI" || !mysqlSimpleType.MatchString(typ) {
				return nil, errors.New("MySQL 自增列缺少明确整数主键")
			}
		}
		if strings.Contains(typ, "text") && v[4] != "NULL" {
			return nil, errors.New("MySQL TEXT 默认值不受支持")
		}
		seen[name] = true
		columns = append(columns, MySQLColumn{v[0], typ, v[2] == "YES", v[3], v[4], v[5]})
	}
	if len(columns) == 0 || len(columns) > 64 || primary > 1 || auto > 1 {
		return nil, errors.New("MySQL 表结构为空、过大或存在歧义主键")
	}
	return columns, nil
}

func mysqlSchemaSQL(columns []MySQLColumn) string {
	var defs []string
	for _, c := range columns {
		def := "`" + c.Field + "` " + c.Type
		if c.Nullable {
			def += " NULL"
		} else {
			def += " NOT NULL"
		}
		if c.Default == "NULL" {
			if c.Nullable {
				def += " DEFAULT NULL"
			}
		} else {
			def += " DEFAULT '" + c.Default + "'"
		}
		if c.Key == "PRI" {
			def += " PRIMARY KEY"
		}
		if c.Extra != "" {
			def += " AUTO_INCREMENT"
		}
		defs = append(defs, def)
	}
	return "CREATE TABLE `educoder_reference` (" + strings.Join(defs, ", ") + ") ENGINE=InnoDB DEFAULT CHARSET=utf8;"
}

func mysqlTaskHash(t *Task) string {
	target, env, _ := requireMySQLArtifact(t)
	data, _ := json.Marshal([]any{t.CourseIdentifier, t.HomeworkID, t.Game.Identifier, t.Game.ID, t.MyShixun.Identifier, t.MyShixun.ID, t.User.UserID, t.Challenge.ID, t.Shixun.Name, t.Challenge.Subject, t.Challenge.Position, graphicHash([]byte(t.Challenge.Description)), target, env.EnvironmentID})
	return graphicHash(data)
}
func (p *MySQLArtifactPlan) planSeal() string {
	data, _ := json.Marshal([]any{p.Path, p.Source, p.SchemaSHA256, p.columns, p.taskSHA256})
	return graphicHash(data)
}
func (p *MySQLArtifactPlan) CanonicalSQL() string {
	if p == nil {
		return ""
	}
	return mysqlSchemaSQL(p.columns)
}
func (p *MySQLArtifactPlan) validate(t *Task) error {
	if p == nil || p.taskSHA256 != mysqlTaskHash(t) || p.seal != p.planSeal() || p.SchemaSHA256 != graphicHash([]byte(p.CanonicalSQL())) {
		return errors.New("MySQL 计划没有绑定本次公开题目和参考结构")
	}
	return nil
}

func BuildMySQLArtifactPlan(t *Task, ref MySQLArtifactReference) (*MySQLArtifactPlan, error) {
	target, _, err := requireMySQLArtifact(t)
	if err != nil {
		return nil, err
	}
	normalize := func(v string) string { return strings.Join(strings.Fields(v), " ") }
	refpath, err := normalizeMySQLArtifactPath(ref.Path)
	if err != nil || refpath != target || normalize(ref.Experiment) == "" || normalize(ref.Experiment) != normalize(t.Shixun.Name) || normalize(ref.Title) != normalize(t.Challenge.Subject) || ref.Position > 0 && t.Challenge.Position > 0 && ref.Position != t.Challenge.Position || ref.Source == "" || len(ref.Source) > 1024 || strings.ContainsAny(ref.Source, "\r\n\x00") {
		return nil, errors.New("MySQL 参考的实验、题名、关卡或路径不匹配")
	}
	cols, err := ParseMySQLDescribe(ref.Content)
	if err != nil {
		return nil, err
	}
	p := &MySQLArtifactPlan{Path: target, Source: ref.Source, columns: cols, taskSHA256: mysqlTaskHash(t)}
	sum := sha256.Sum256([]byte(p.CanonicalSQL()))
	p.SchemaSHA256 = hex.EncodeToString(sum[:])
	p.seal = p.planSeal()
	return p, nil
}

// MySQLArtifactSession runs only a compiled schema plan. The production
// implementation generates a fresh private database and captures real DESC
// output, then commits that exact artifact in the task's own workspace.
type MySQLArtifactSession interface {
	ReadMySQLArtifact(context.Context, string) ([]byte, bool, error)
	ExecuteMySQLArtifact(context.Context, *MySQLArtifactPlan, string, string) (string, error)
}

// MySQLArtifactStage records each mutation boundary. A false field after an
// attempted request means the result is unknown, not that the request did not
// reach the platform; recovery must therefore re-read before submitting again.
type MySQLArtifactStage struct {
	ExecutionAttempted   bool   `json:"execution_attempted"`
	ExecutionConfirmed   bool   `json:"execution_confirmed"`
	ArtifactHash         string `json:"artifact_hash,omitempty"`
	CommitID             string `json:"commit_id,omitempty"`
	PreparationAttempted bool   `json:"preparation_attempted"`
	PreparationConfirmed bool   `json:"preparation_confirmed"`
	BuildAttempted       bool   `json:"build_attempted"`
	BuildAccepted        bool   `json:"build_accepted"`
	EvaluationAttempted  bool   `json:"evaluation_attempted"`
	EvaluationObserved   bool   `json:"evaluation_observed"`
	EvaluationCompleted  bool   `json:"evaluation_completed"`
	EvaluationPassed     bool   `json:"evaluation_passed"`
}

type MySQLArtifactResult struct {
	*GraphicSubmitResult
	SchemaSHA256       string              `json:"schema_sha256"`
	Source             string              `json:"source"`
	ExecutionAttempted bool                `json:"execution_attempted"`
	Stage              *MySQLArtifactStage `json:"stage,omitempty"`
}

func (p *GraphicProtocol) RunMySQLArtifact(ctx context.Context, t *Task, plan *MySQLArtifactPlan, options PollOptions) (result *MySQLArtifactResult, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	target, env, err := requireMySQLArtifact(t)
	if err != nil {
		return nil, err
	}
	if err = plan.validate(t); err != nil {
		return nil, err
	}
	if p == nil || p.Client == nil || p.Transport == nil {
		return nil, errors.New("MySQL VNC 协议未初始化")
	}
	current, err := p.Client.LoadTask(ctx, t.CourseIdentifier, t.HomeworkID, t.Game.Identifier)
	if err != nil {
		return nil, err
	}
	if err = plan.validate(current); err != nil {
		return nil, err
	}
	session, cleanup, err := p.acquireEnvironment(ctx, current, env)
	if err != nil {
		return nil, err
	}
	defer func() {
		if result != nil {
			cleanup(result.GraphicSubmitResult)
		} else {
			cleanup(nil)
		}
	}()
	sqlSession, ok := session.(MySQLArtifactSession)
	if !ok {
		return nil, errors.New("VNC 会话不支持真实 MySQL 结构执行")
	}
	login := mysqlPublicCommand.FindStringSubmatch(current.Challenge.Description)
	stage := &MySQLArtifactStage{ExecutionAttempted: true}
	result = &MySQLArtifactResult{GraphicSubmitResult: &GraphicSubmitResult{Stage: GraphicStageSaveUnconfirmed}, SchemaSHA256: plan.SchemaSHA256, Source: plan.Source, ExecutionAttempted: true, Stage: stage}
	commit, err := sqlSession.ExecuteMySQLArtifact(ctx, plan, login[1], login[2])
	if err != nil {
		return result, fmt.Errorf("MySQL 结构执行结果未确认，不重放执行: %w", err)
	}
	if !graphicCommit.MatchString(commit) {
		return result, errors.New("MySQL 结果缺少工作区提交版本")
	}
	data, exists, err := sqlSession.ReadMySQLArtifact(ctx, target)
	if err != nil || !exists {
		return result, errors.New("MySQL 结果文件缺少可读取的执行证据")
	}
	actual, err := ParseMySQLDescribe(string(data))
	if err != nil || mysqlSchemaSQL(actual) != plan.CanonicalSQL() {
		return result, errors.New("MySQL 实际 DESCRIBE 结果与参考结构不匹配")
	}
	stage.ExecutionConfirmed = true
	stage.ArtifactHash = graphicHash(data)
	stage.CommitID = commit
	result.GraphicSubmitResult = &GraphicSubmitResult{ContentHash: stage.ArtifactHash, CommitID: commit, Saved: true, Stage: GraphicStageSavedNotSubmitted}
	result.GraphicSubmitResult, err = p.evaluateWorkspace(ctx, current, current, env, target, sqlSession.ReadMySQLArtifact, result.GraphicSubmitResult, options, requireMySQLArtifact, stage)
	return result, err
}
