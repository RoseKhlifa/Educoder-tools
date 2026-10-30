package educoder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// RecordedRoute identifies the observed page and programming compatibility
// routes. Evidence comes from HAR entries, the published frontend and live page
// requests. This is not a general forwarding API: unlisted methods and paths
// never reach a server.
func RecordedRoute(method, path string) bool {
	path = NormalizeObservedPath(path)
	if method == http.MethodPost && path == "/api/accounts/login.json" {
		return true
	}
	if method == http.MethodPost {
		parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
		if len(parts) != 4 || parts[0] != "api" || validIdentifier(parts[2]) != nil {
			return false
		}
		return parts[1] == "myshixuns" && parts[3] == "update_file.json" ||
			parts[1] == "tasks" && (parts[3] == "log_output" || parts[3] == "game_build.json" || parts[3] == "choose_build.json" || parts[3] == "cost_time.json")
	}
	if method != http.MethodGet {
		return false
	}
	switch path {
	case "/api/users/get_user_info.json", "/api/users/get_notice.json", "/api/users/system_update.json", "/api/setting.json", "/api/home/index.json",
		"/api/popup_windows/popup_windows_counts.json", "/api/popup_windows/get_popup_windows.json":
		return true
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 3 || parts[0] != "api" {
		return false
	}
	if parts[1] == "tasks" && len(parts) == 3 && strings.HasSuffix(parts[2], ".json") {
		return validIdentifier(strings.TrimSuffix(parts[2], ".json")) == nil
	}
	if parts[1] == "homework_commons" && len(parts) == 3 && strings.HasSuffix(parts[2], ".json") {
		return positiveObservedID(strings.TrimSuffix(parts[2], ".json"))
	}
	if len(parts) != 4 || validIdentifier(parts[2]) != nil {
		return false
	}
	switch parts[1] {
	case "users":
		// The published user service and user detail layout call this GET while
		// loading the profile header around the classroom page.
		return parts[3] == "courses.json" || parts[3] == "homepage_info.json"
	case "courses":
		switch parts[3] {
		case "top_banner.json", "left_banner.json", "informs.json", "homework_commons.json":
			return true
		}
	case "shixuns":
		return parts[3] == "shixun_exec.json"
	case "tasks":
		return parts[3] == "rep_content.json" || parts[3] == "game_status.json"
	case "homework_commons":
		return positiveObservedID(parts[2]) && (parts[3] == "show_comment.json" || parts[3] == "shixun_challenge_data.json")
	case "shixun_homeworks":
		return positiveObservedID(parts[2]) && (parts[3] == "student_works.json" || parts[3] == "header_info.json")
	case "myshixuns":
		return parts[3] == "version_repository_delete.json"
	}
	return false
}

// NormalizeObservedPath handles the one extra slash emitted by the published
// task bundle. It intentionally does not clean or normalize any other path.
func NormalizeObservedPath(path string) string {
	const prefix = "/api//shixuns/"
	if strings.HasPrefix(path, prefix) {
		return "/api/shixuns/" + strings.TrimPrefix(path, prefix)
	}
	return path
}

func positiveObservedID(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	id, err := strconv.ParseInt(value, 10, 64)
	return err == nil && id > 0
}

// RecordedResponse keeps the upstream JSON representation intact. It excludes
// response headers because upstream authentication cookies belong only to the
// server-side cookie jar.
type RecordedResponse struct {
	StatusCode int
	Body       json.RawMessage
}

// RecordedRequest signs one allowlisted API request and keeps all upstream
// credentials in Client.HTTP.Jar. A login password is already encrypted by the
// unmodified browser bundle; its JSON bytes are sent without another AES pass.
func (c *Client) RecordedRequest(ctx context.Context, method, path string, query url.Values, body json.RawMessage) (*RecordedResponse, error) {
	path = NormalizeObservedPath(path)
	if !RecordedRoute(method, path) {
		return nil, errors.New("unsupported observed route")
	}
	q := make(url.Values, len(query)+1)
	for key, values := range query {
		q[key] = append([]string(nil), values...)
	}
	// The browser's identity hint is never an authority. Every authenticated
	// request uses the login associated with this client.
	q.Del("zzud")
	if c.LoginName != "" {
		q.Set("zzud", c.LoginName)
	}
	u, err := url.Parse(c.BaseURL + path)
	if err != nil {
		return nil, errors.New("invalid upstream URL")
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("invalid upstream request")
	}
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Origin", DefaultOrigin)
	req.Header.Set("Referer", DefaultOrigin+"/")
	req.Header.Set("User-Agent", "educoder-compat/1.0")
	req.Header.Set("X-EDU-Type", "pc")
	req.Header.Set("X-EDU-Timestamp", timestamp)
	req.Header.Set("X-EDU-Signature", Signature(method, timestamp))
	req.Header.Set("X-Original-Protocol", "https:")
	req.Header.Set("X-Original-Host", "www.educoder.net")
	req.Header.Set("X-Original-Origin", DefaultOrigin)
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
		return nil, errors.New("upstream request failed")
	}
	defer resp.Body.Close()
	if c.Trace != nil {
		c.Trace(fmt.Sprintf("%s %s -> %d", method, path, resp.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (16<<20)+1))
	if err != nil || len(data) > 16<<20 {
		return nil, errors.New("upstream response exceeded limit")
	}
	if len(data) == 0 && (resp.StatusCode == http.StatusNotModified || method == http.MethodPost && path != "/api/accounts/login.json") {
		return &RecordedResponse{StatusCode: resp.StatusCode, Body: data}, nil
	}
	if !json.Valid(data) {
		return nil, errors.New("upstream did not return JSON")
	}
	return &RecordedResponse{StatusCode: resp.StatusCode, Body: data}, nil
}

// AcceptRecordedLogin binds identity only after both a successful JSON login
// and an upstream session cookie. It does not trust browser authorization data.
func (c *Client) AcceptRecordedLogin(response *RecordedResponse) (string, error) {
	if response == nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", errors.New("login failed")
	}
	var user struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(response.Body, &user); err != nil || validIdentifier(user.Login) != nil {
		return "", errors.New("login failed")
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || c.HTTP.Jar == nil {
		return "", errors.New("login failed")
	}
	for _, cookie := range c.HTTP.Jar.Cookies(u) {
		if cookie.Name == "_educoder_session" && cookie.Value != "" {
			c.LoginName = user.Login
			return user.Login, nil
		}
	}
	return "", errors.New("login session missing")
}

// RecordedSecrets is used only to remove upstream credential values from JSON
// before it is returned to the browser. It must never be logged or persisted.
func (c *Client) RecordedSecrets() []string {
	u, err := url.Parse(c.BaseURL)
	if err != nil || c.HTTP.Jar == nil {
		return nil
	}
	var values []string
	for _, cookie := range c.HTTP.Jar.Cookies(u) {
		if cookie.Value != "" && (cookie.Name == "_educoder_session" || cookie.Name == "autologin_trustie") {
			values = append(values, cookie.Value)
		}
	}
	return values
}
