// Package educoder implements the HTTP flow observed in the supplied HAR.
package educoder

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const DefaultBaseURL = "https://data.educoder.net"
const DefaultOrigin = "https://www.educoder.net"

// Page navigation fans out into several API calls. Share a generously sized
// keep-alive pool across clients while each user's CookieJar stays separate.
// The default two idle connections per host otherwise discard most of these
// connections between navigations and repeat cross-region TLS handshakes.
var upstreamTransport = func() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 128
	transport.MaxIdleConnsPerHost = 32
	transport.IdleConnTimeout = 90 * time.Second
	return transport
}()

// These are public web-client protocol constants, decoded from module 527951.
// They are not user credentials. The password key is confirmed against the HAR.
var accessKey = decodeTwice("WlRsa1pEVmlORE15TW1ZNVpqZGtPRE5rTURBNVpHVTVZbVpoTVRBd1l6TT0=")
var secretKey = decodeTwice("TW1VelpHRXdObUZsTWpaaVlUbG1OelpoTldRNFpETTFOVGMwTm1ZeVptVT0=")

// Login popup module 22887387 uses this literal UTF-8 key as both AES key and IV.
var passwordKey = []byte("5183666c72eec9e4")

func decodeTwice(value string) string {
	a, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		panic(err)
	}
	b, err := base64.StdEncoding.DecodeString(string(a))
	if err != nil {
		panic(err)
	}
	return string(b)
}

// EncryptPassword reproduces AES-CBC with PKCS#7 padding from the web client.
func EncryptPassword(password string) string {
	plain := []byte(strings.TrimSpace(password))
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	plain = append(plain, bytes.Repeat([]byte{byte(pad)}, pad)...)
	block, _ := aes.NewCipher(passwordKey)
	out := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, passwordKey).CryptBlocks(out, plain)
	return base64.StdEncoding.EncodeToString(out)
}

func Signature(method, timestamp string) string {
	canonical := "method=" + strings.ToUpper(method) + "&ak=" + accessKey + "&sk=" + secretKey + "&time=" + timestamp
	encoded := base64.StdEncoding.EncodeToString([]byte(canonical))
	sum := md5.Sum([]byte(encoded))
	return hex.EncodeToString(sum[:])
}

type User struct {
	UserID   int64  `json:"user_id"`
	Login    string `json:"login"`
	Name     string `json:"name"`
	RealName string `json:"real_name,omitempty"`
	Identity string `json:"identity"`
}

type Course struct {
	ID               int64  `json:"id"`
	Identifier       string `json:"identifier"`
	Name             string `json:"name"`
	FirstCategoryURL string `json:"first_category_url"`
	IsEnd            bool   `json:"is_end"`
	HomeworkCount    int    `json:"homework_commons_count"`
}

type Homework struct {
	ID               int64             `json:"homework_id"`
	Name             string            `json:"name"`
	Status           []string          `json:"status"`
	ShixunIdentifier string            `json:"shixun_identifier"`
	IsShixun         bool              `json:"is_shixun"`
	EnterStatus      int               `json:"shixun_enter_status"`
	Category         string            `json:"upper_category_name,omitempty"`
	Operation        []json.RawMessage `json:"task_operation,omitempty"`
}

type Task struct {
	RequiredStatus string `json:"status"`
	Game           struct {
		ID         int64  `json:"id"`
		Identifier string `json:"identifier"`
		MyShixunID int64  `json:"myshixun_id"`
		Status     int    `json:"status"`
	} `json:"game"`
	Challenge struct {
		ID          int64  `json:"id"`
		Subject     string `json:"subject"`
		Description string `json:"task_pass"`
		Path        string `json:"path"`
	} `json:"challenge"`
	Shixun struct {
		ID         int64  `json:"id"`
		Identifier string `json:"identifier"`
		Name       string `json:"name"`
	} `json:"shixun"`
	CourseIdentifier string  `json:"course_identifier"`
	HomeworkID       int64   `json:"homework_common_id"`
	NextGame         *string `json:"next_game"`
	URL              string  `json:"url"`
}

type Client struct {
	HTTP      *http.Client
	BaseURL   string
	Origin    string
	LoginName string
	// Trace receives method/path/status only. It must never receive credentials.
	Trace func(string)
}

func New(baseURL string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	jar, _ := cookiejar.New(nil)
	return &Client{HTTP: &http.Client{Transport: upstreamTransport, Jar: jar, Timeout: 45 * time.Second}, BaseURL: strings.TrimRight(baseURL, "/"), Origin: DefaultOrigin}
}

func (c *Client) request(ctx context.Context, method, path string, query url.Values, body any, ref string, out any) error {
	if query == nil {
		query = url.Values{}
	}
	if c.LoginName != "" && query.Get("zzud") == "" {
		query.Set("zzud", c.LoginName)
	}
	u, err := url.Parse(c.BaseURL + path)
	if err != nil {
		return err
	}
	u.RawQuery = query.Encode()
	var payload io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return e
		}
		payload = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), payload)
	if err != nil {
		return err
	}
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Origin", c.Origin)
	req.Header.Set("Referer", c.Origin+ref)
	req.Header.Set("User-Agent", "educoder-cli/1.0")
	req.Header.Set("X-EDU-Type", "pc")
	req.Header.Set("X-EDU-Timestamp", timestamp)
	req.Header.Set("X-EDU-Signature", Signature(method, timestamp))
	req.Header.Set("X-Original-Protocol", "https:")
	origin, _ := url.Parse(c.Origin)
	req.Header.Set("X-Original-Host", origin.Host)
	req.Header.Set("X-Original-Origin", c.Origin)
	req.Header.Set("X-Request-Id", requestID())
	session := "null"
	if c.HTTP.Jar != nil {
		for _, cookie := range c.HTTP.Jar.Cookies(u) {
			if cookie.Name == "_educoder_session" {
				session = cookie.Value
			}
		}
	}
	req.Header.Set("Pc-Authorization", session)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if c.Trace != nil {
		c.Trace(fmt.Sprintf("%s %s -> %d", method, path, resp.StatusCode))
	}
	// Keep the response bounded; the task descriptions can be fairly large.
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	var envelope struct {
		Status  json.RawMessage `json:"status"`
		Message string          `json:"message"`
	}
	_ = json.Unmarshal(data, &envelope)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: HTTP %d%s", method, path, resp.StatusCode, messageSuffix(envelope.Message))
	}
	var code float64
	if len(envelope.Status) > 0 && json.Unmarshal(envelope.Status, &code) == nil && code != 0 {
		return fmt.Errorf("%s %s: API status %v%s", method, path, code, messageSuffix(envelope.Message))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("%s %s: expected JSON response: %w", method, path, err)
		}
	}
	return nil
}

func messageSuffix(s string) string {
	if s == "" {
		return ""
	}
	return ": " + s
}

func requestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	s := hex.EncodeToString(b)
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}

func (c *Client) Login(ctx context.Context, login, password string) (*User, error) {
	if login == "" || password == "" {
		return nil, errors.New("登录账号和密码不能为空")
	}
	c.LoginName = ""
	var user User
	body := map[string]any{"login": login, "password": EncryptPassword(password), "autologin": true}
	if err := c.request(ctx, http.MethodPost, "/api/accounts/login.json", nil, body, "/", &user); err != nil {
		return nil, err
	}
	if user.Login == "" {
		return nil, errors.New("登录响应缺少 login，可能需要验证码或其他登录步骤")
	}
	u, _ := url.Parse(c.BaseURL)
	hasSession := false
	for _, cookie := range c.HTTP.Jar.Cookies(u) {
		if cookie.Name == "_educoder_session" && cookie.Value != "" {
			hasSession = true
		}
	}
	if !hasSession {
		return nil, errors.New("登录成功响应未设置 _educoder_session Cookie")
	}
	c.LoginName = user.Login
	return &user, nil
}

func (c *Client) UserInfo(ctx context.Context, course string) (*User, error) {
	q := url.Values{"school": {"1"}}
	if course != "" {
		q.Set("course_id", course)
	}
	var user User
	if err := c.request(ctx, http.MethodGet, "/api/users/get_user_info.json", q, nil, "/", &user); err != nil {
		return nil, err
	}
	if user.Login == "" {
		return nil, errors.New("用户信息缺少 login，会话未认证")
	}
	return &user, nil
}

func CourseIdentifier(path string) string {
	u, err := url.Parse(path)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) >= 2 && parts[0] == "classrooms" {
		return parts[1]
	}
	return ""
}

func (c *Client) Courses(ctx context.Context, status string) ([]Course, error) {
	if c.LoginName == "" {
		return nil, errors.New("请先登录")
	}
	result := []Course{}
	seen := map[int64]bool{}
	for page := 1; page <= 1000; page++ {
		q := url.Values{"category": {""}, "status": {status}, "page": {strconv.Itoa(page)}, "per_page": {"15"}, "sort_by": {"updated_at"}, "sort_direction": {"desc"}, "username": {c.LoginName}}
		var data struct {
			Count   int      `json:"count"`
			Courses []Course `json:"courses"`
		}
		if err := c.request(ctx, http.MethodGet, "/api/users/"+url.PathEscape(c.LoginName)+"/courses.json", q, nil, "/users/"+c.LoginName+"/classrooms", &data); err != nil {
			return nil, err
		}
		added := 0
		for _, course := range data.Courses {
			if !seen[course.ID] {
				seen[course.ID] = true
				course.Identifier = CourseIdentifier(course.FirstCategoryURL)
				result = append(result, course)
				added++
			}
		}
		if len(data.Courses) == 0 || added == 0 || (data.Count > 0 && len(result) >= data.Count) {
			return result, nil
		}
	}
	return nil, errors.New("课堂分页超过 1000 页")
}

func (c *Client) Homeworks(ctx context.Context, course, category string) ([]Homework, error) {
	if err := validIdentifier(course); err != nil {
		return nil, err
	}
	result := []Homework{}
	seen := map[int64]bool{}
	for page := 1; page <= 1000; page++ {
		q := url.Values{"id": {course}, "limit": {"20"}, "page": {strconv.Itoa(page)}, "status": {"0"}, "type": {"4"}, "order": {"0"}}
		if category != "" {
			q.Set("category", category)
		}
		var data struct {
			Total     *int       `json:"query_total_count"`
			Homeworks []Homework `json:"homeworks"`
		}
		if err := c.request(ctx, http.MethodGet, "/api/courses/"+course+"/homework_commons.json", q, nil, "/classrooms/"+course+"/shixun_homework", &data); err != nil {
			return nil, err
		}
		added := 0
		for _, homework := range data.Homeworks {
			if !seen[homework.ID] {
				seen[homework.ID] = true
				result = append(result, homework)
				added++
			}
		}
		if len(data.Homeworks) == 0 || added == 0 || (data.Total != nil && len(result) >= *data.Total) {
			return result, nil
		}
	}
	return nil, errors.New("任务分页超过 1000 页")
}

func (c *Client) Enter(ctx context.Context, course string, homeworkID int64, shixun string) (*Task, error) {
	if err := validIdentifier(course); err != nil {
		return nil, err
	}
	if homeworkID <= 0 {
		return nil, errors.New("homework ID 必须大于 0")
	}
	// Always resolve the selected homework from this account's classroom list.
	homeworks, err := c.Homeworks(ctx, course, "")
	if err != nil {
		return nil, err
	}
	found := false
	for _, hw := range homeworks {
		if hw.ID == homeworkID {
			found = true
			if !hw.IsShixun || hw.ShixunIdentifier == "" {
				return nil, errors.New("选择的任务不是 HAR 覆盖的实训任务类型")
			}
			if shixun != "" && shixun != hw.ShixunIdentifier {
				return nil, errors.New("指定 shixun 与课堂任务不匹配")
			}
			shixun = hw.ShixunIdentifier
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("课堂 %s 的当前实训任务列表中没有作业 %d", course, homeworkID)
	}
	if err := validIdentifier(shixun); err != nil {
		return nil, err
	}
	q := url.Values{"id": {shixun}, "homework_common_id": {strconv.FormatInt(homeworkID, 10)}}
	var execution struct {
		GameIdentifier string `json:"game_identifier"`
	}
	if err := c.request(ctx, http.MethodGet, "/api/shixuns/"+shixun+"/shixun_exec.json", q, nil, "/classrooms/"+course+"/shixun_homework", &execution); err != nil {
		return nil, err
	}
	if execution.GameIdentifier == "" {
		return nil, errors.New("进入接口没有返回 game_identifier")
	}
	if err := validIdentifier(execution.GameIdentifier); err != nil {
		return nil, err
	}
	var task Task
	taskURL := "/tasks/" + course + "/" + strconv.FormatInt(homeworkID, 10) + "/" + execution.GameIdentifier
	if err := c.request(ctx, http.MethodGet, "/api/tasks/"+execution.GameIdentifier+".json", url.Values{"homework_common_id": {strconv.FormatInt(homeworkID, 10)}}, nil, taskURL, &task); err != nil {
		return nil, err
	}
	if task.Game.Identifier == "" {
		return nil, errors.New("任务详情缺少 game.identifier")
	}
	task.URL = c.Origin + taskURL
	return &task, nil
}

func validIdentifier(s string) error {
	if s == "" {
		return errors.New("课堂/实训标识不能为空")
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return errors.New("课堂/实训标识包含无效字符")
		}
	}
	return nil
}
