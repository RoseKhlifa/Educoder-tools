package educoder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const weChatLoginAppID = "wx6b119e2d829c13fa"

// WeChatLoginQR belongs to one pending login transaction. Neither its UUID nor
// its image should be persisted in jobs, logs, or question-bank records.
type WeChatLoginQR struct {
	UUID  string `json:"-"`
	Image []byte `json:"-"`
}

func (WeChatLoginQR) String() string   { return "WeChat login QR (redacted)" }
func (WeChatLoginQR) GoString() string { return "WeChat login QR (redacted)" }

// Code exists only after a confirmed scan and must be consumed once by the
// same pending authentication flow. Status is an application lifecycle label.
type WeChatLoginPoll struct {
	Status string
	Code   string `json:"-"`
}

func (p WeChatLoginPoll) String() string   { return "WeChat login status: " + p.Status }
func (p WeChatLoginPoll) GoString() string { return p.String() }

var weChatIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)
var weChatCodePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,512}$`)
var weChatQRSourcePattern = regexp.MustCompile(`src\s*=\s*["']/connect/qrcode/([A-Za-z0-9_-]{8,128})["']`)

// The upstream endpoint returns a script consisting of two assignments, not
// JSON. Accept only that grammar; never evaluate a response as JavaScript.
var weChatPollScriptPattern = regexp.MustCompile(`^\s*(?:window\.)?wx_errcode\s*=\s*([0-9]{3})\s*;\s*(?:window\.)?wx_code\s*=\s*(?:'([A-Za-z0-9_-]{0,512})'|"([A-Za-z0-9_-]{0,512})")\s*;?\s*$`)

// StartWeChatLogin uses the public official iframe protocol. The backend owns
// the QR transaction and can observe confirmation before the iframe would
// navigate to the fixed official callback domain.
func (c *Client) StartWeChatLogin(ctx context.Context) (*WeChatLoginQR, error) {
	query := url.Values{
		"appid":         {weChatLoginAppID},
		"redirect_uri":  {DefaultOrigin + "/otherloginstart"},
		"response_type": {"code"},
		"scope":         {"snsapi_login"},
		"state":         {"null"},
	}
	html, err := c.weChatLoginGET(ctx, "https://open.weixin.qq.com/connect/qrconnect?"+query.Encode(), 512<<10)
	if err != nil {
		return nil, err
	}
	matches := weChatQRSourcePattern.FindAllSubmatch(html, -1)
	if len(matches) == 0 {
		return nil, errors.New("WeChat QR response has no supported image")
	}
	uuid := string(matches[0][1])
	for _, match := range matches[1:] {
		if string(match[1]) != uuid {
			return nil, errors.New("WeChat QR response has conflicting images")
		}
	}
	imageBytes, err := c.weChatLoginGET(ctx, "https://open.weixin.qq.com/connect/qrcode/"+uuid, 256<<10)
	if err != nil {
		return nil, err
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(imageBytes))
	if err != nil || (format != "png" && format != "jpeg" && format != "gif") || config.Width <= 0 || config.Height <= 0 || config.Width > 1024 || config.Height > 1024 {
		return nil, errors.New("WeChat QR image is not supported")
	}
	return &WeChatLoginQR{UUID: uuid, Image: imageBytes}, nil
}

// PollWeChatLogin makes one bounded long-poll request. A caller controls its
// own transaction lifetime and must never turn a confirmed code into a replay.
func (c *Client) PollWeChatLogin(ctx context.Context, uuid, lastStatus string) (*WeChatLoginPoll, error) {
	if !weChatIdentifierPattern.MatchString(uuid) {
		return nil, errors.New("invalid WeChat QR identifier")
	}
	query := url.Values{"uuid": {uuid}}
	if lastStatus != "" {
		lastCode, ok := map[string]string{"waiting": "408", "scanned": "404", "canceled": "403"}[lastStatus]
		if !ok {
			return nil, errors.New("invalid WeChat polling state")
		}
		query.Set("last", lastCode)
	}
	data, err := c.weChatLoginGET(ctx, "https://lp.open.weixin.qq.com/connect/l/qrconnect?"+query.Encode(), 8<<10)
	if err != nil {
		return nil, err
	}
	return parseWeChatLoginPoll(data)
}

func parseWeChatLoginPoll(data []byte) (*WeChatLoginPoll, error) {
	match := weChatPollScriptPattern.FindSubmatch(data)
	if match == nil {
		return nil, errors.New("WeChat polling returned an unsupported response")
	}
	code := string(match[2]) + string(match[3])
	result := &WeChatLoginPoll{}
	switch string(match[1]) {
	case "408":
		result.Status = "waiting"
	case "404":
		result.Status = "scanned"
	case "403":
		result.Status = "canceled"
	case "402", "500":
		// The official page refreshes its QR after 500; the application should
		// request a new image instead of replaying this transaction.
		result.Status = "expired"
	case "405":
		if !weChatCodePattern.MatchString(code) {
			return nil, errors.New("WeChat confirmation has no supported code")
		}
		result.Status, result.Code = "confirmed", code
	default:
		return nil, errors.New("WeChat polling returned an unsupported status")
	}
	if result.Status != "confirmed" && code != "" {
		return nil, errors.New("WeChat polling returned an inconsistent state")
	}
	return result, nil
}

// CompleteWeChatLogin consumes one code using the official callback, then
// verifies a stable numeric user identity in the same upstream session.
func (c *Client) CompleteWeChatLogin(ctx context.Context, code string) (_ *User, err error) {
	if !weChatCodePattern.MatchString(code) {
		return nil, errors.New("invalid WeChat authorization code")
	}
	c.resetAuthenticationSession()
	complete := false
	defer func() {
		if !complete {
			c.resetAuthenticationSession()
		}
	}()
	var response struct {
		Status  json.RawMessage `json:"status"`
		NewUser bool            `json:"new_user"`
	}
	requestCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	if err := c.requestWithStatuses(requestCtx, http.MethodGet, "/api/auth/wechat/callback.json", url.Values{"code": {code}}, nil, "/otherloginstart", &response, anyStatus); err != nil {
		if requestCtx.Err() != nil {
			return nil, fmt.Errorf("WeChat callback: %w", requestCtx.Err())
		}
		return nil, errors.New("WeChat callback request failed")
	}
	status, err := numericAPIStatus(response.Status)
	if err != nil {
		return nil, errors.New("WeChat callback returned an unexpected response")
	}
	if status == -3 {
		return nil, ErrLoginExamRestricted
	}
	if status != 0 {
		return nil, &APIStatusError{Method: http.MethodGet, Path: "/api/auth/wechat/callback.json", Code: float64(status)}
	}
	if response.NewUser {
		return nil, ErrLoginBindingRequired
	}
	if !hasEducoderSession(c) {
		return nil, errors.New("WeChat callback did not establish an Educoder session")
	}
	user, err := c.authenticationUserInfo(requestCtx)
	if err != nil || user == nil || user.Login == "" || user.UserID <= 0 {
		if requestCtx.Err() != nil {
			return nil, fmt.Errorf("WeChat identity verification: %w", requestCtx.Err())
		}
		return nil, errors.New("WeChat login identity could not be verified")
	}
	c.LoginName = user.Login
	c.bindUpstreamAccount(user)
	complete = true
	return user, nil
}

func (c *Client) weChatLoginGET(ctx context.Context, target string, limit int64) ([]byte, error) {
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Fragment != "" {
		return nil, errors.New("invalid WeChat endpoint")
	}
	allowed := (u.Host == "open.weixin.qq.com" && (u.Path == "/connect/qrconnect" || strings.HasPrefix(u.Path, "/connect/qrcode/"))) ||
		(u.Host == "lp.open.weixin.qq.com" && u.Path == "/connect/l/qrconnect")
	if !allowed || c == nil || c.HTTP == nil {
		return nil, errors.New("invalid WeChat endpoint")
	}
	requestCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.New("WeChat request could not be prepared")
	}
	// A lost long-poll response may already contain the one-time code. Disable
	// net/http's implicit replay on a reused connection, as for auth API GETs.
	req.Body = io.NopCloser(strings.NewReader(""))
	req.GetBody = nil
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Referer", DefaultOrigin+"/")
	req.Header.Set("Accept", "*/*")
	httpClient := *c.HTTP
	if httpClient.Timeout <= 0 || httpClient.Timeout > 35*time.Second {
		httpClient.Timeout = 35 * time.Second
	}
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := httpClient.Do(req)
	if err != nil {
		if requestCtx.Err() != nil {
			return nil, fmt.Errorf("WeChat request: %w", requestCtx.Err())
		}
		return nil, errors.New("WeChat request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("WeChat endpoint returned HTTP " + strconv.Itoa(resp.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, errors.New("WeChat response could not be read")
	}
	if int64(len(data)) > limit {
		return nil, errors.New("WeChat response exceeded the limit")
	}
	return data, nil
}
