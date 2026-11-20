package educoder

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type weChatTestTransport func(*http.Request) (*http.Response, error)

func (f weChatTestTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func weChatTestResponse(req *http.Request, body []byte) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), Request: req}
}

func TestWeChatLoginStartUsesFixedOfficialURLs(t *testing.T) {
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 16, 16))); err != nil {
		t.Fatal(err)
	}
	client := New("")
	calls := 0
	client.HTTP.Transport = weChatTestTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Scheme != "https" || req.URL.Host != "open.weixin.qq.com" || req.Header.Get("Pc-Authorization") != "" {
			t.Fatalf("unexpected QR request target or upstream credentials")
		}
		switch calls {
		case 1:
			q := req.URL.Query()
			if req.URL.Path != "/connect/qrconnect" || q.Get("appid") != weChatLoginAppID || q.Get("redirect_uri") != DefaultOrigin+"/otherloginstart" || q.Get("scope") != "snsapi_login" || q.Get("response_type") != "code" || q.Get("state") != "null" {
				t.Fatal("unexpected QR query contract")
			}
			return weChatTestResponse(req, []byte(`<img src="/connect/qrcode/fixture_uuid"><img src='/connect/qrcode/fixture_uuid'>`)), nil
		case 2:
			if req.URL.Path != "/connect/qrcode/fixture_uuid" || req.URL.RawQuery != "" {
				t.Fatal("unexpected QR image request")
			}
			return weChatTestResponse(req, imageBytes.Bytes()), nil
		default:
			t.Fatal("unexpected QR retry")
			return nil, errors.New("unexpected retry")
		}
	})
	qr, err := client.StartWeChatLogin(context.Background())
	if err != nil || qr.UUID != "fixture_uuid" || !bytes.Equal(qr.Image, imageBytes.Bytes()) || calls != 2 {
		t.Fatalf("start result: %v, calls=%d", err, calls)
	}
}

func TestWeChatLoginStartRejectsConflictingSourcesAndRedirects(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		code int
	}{
		{"conflicting sources", `<img src="/connect/qrcode/fixture_uuid"><img src="/connect/qrcode/different_uuid">`, 200},
		{"external source", `<img src="https://example.invalid/connect/qrcode/fixture_uuid">`, 200},
		{"redirect", "", 302},
		{"large response", strings.Repeat("x", (512<<10)+1), 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := New("")
			calls := 0
			client.HTTP.Transport = weChatTestTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				response := weChatTestResponse(req, []byte(test.body))
				response.StatusCode = test.code
				response.Header.Set("Location", "https://example.invalid/?secret=fixture")
				return response, nil
			})
			if _, err := client.StartWeChatLogin(context.Background()); err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatal("unsafe QR response was accepted or leaked")
			}
			if calls != 1 {
				t.Fatalf("followed a redirect or downloaded ambiguous source: calls=%d", calls)
			}
		})
	}
}

func TestWeChatLoginStartAcceptsJPEGQRCode(t *testing.T) {
	var imageBytes bytes.Buffer
	if err := jpeg.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 16, 16)), nil); err != nil {
		t.Fatal(err)
	}
	client := New("")
	client.HTTP.Transport = weChatTestTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/connect/qrconnect" {
			return weChatTestResponse(req, []byte(`<img src="/connect/qrcode/fixture_uuid">`)), nil
		}
		return weChatTestResponse(req, imageBytes.Bytes()), nil
	})
	qr, err := client.StartWeChatLogin(context.Background())
	if err != nil || http.DetectContentType(qr.Image) != "image/jpeg" {
		t.Fatalf("JPEG QR was not accepted: %v", err)
	}
}

func TestWeChatLoginPollStrictScriptAndState(t *testing.T) {
	for _, test := range []struct {
		script string
		status string
		code   string
	}{
		{`window.wx_errcode=408;window.wx_code='';`, "waiting", ""},
		{`window.wx_errcode=404;window.wx_code="";`, "scanned", ""},
		{`window.wx_errcode=403;window.wx_code='';`, "canceled", ""},
		{`window.wx_errcode=402;window.wx_code='';`, "expired", ""},
		{`window.wx_errcode=500;window.wx_code='';`, "expired", ""},
		{`window.wx_errcode=405;window.wx_code='fixture_code';`, "confirmed", "fixture_code"},
	} {
		t.Run(test.status, func(t *testing.T) {
			poll, err := parseWeChatLoginPoll([]byte(test.script))
			if err != nil || poll.Status != test.status || poll.Code != test.code {
				t.Fatalf("parse: %v", err)
			}
		})
	}
	for _, script := range []string{
		`window.wx_errcode=405;window.wx_code='';`,
		`window.wx_errcode=408;window.wx_code='fixture_code';`,
		`window.wx_errcode=405;window.wx_code='fixture_code';alert(document.cookie);`,
		`window.wx_errcode=405;window.wx_code=fetch('https://example.invalid');`,
		`window.wx_errcode=405;window.wx_code='fixture_code';window.wx_code='other';`,
		`{"wx_errcode":405,"wx_code":"fixture_code"}`,
		`window.wx_errcode=999;window.wx_code='';`,
	} {
		if _, err := parseWeChatLoginPoll([]byte(script)); err == nil || strings.Contains(err.Error(), "fixture_code") {
			t.Fatal("unsupported script was accepted or leaked")
		}
	}
}

func TestWeChatLoginPollUsesFixedHostAndLastCode(t *testing.T) {
	client := New("")
	calls := 0
	client.HTTP.Transport = weChatTestTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Scheme != "https" || req.URL.Host != "lp.open.weixin.qq.com" || req.URL.Path != "/connect/l/qrconnect" || req.URL.Query().Get("uuid") != "fixture_uuid" || req.URL.Query().Get("last") != "404" {
			t.Fatal("unexpected polling request")
		}
		return weChatTestResponse(req, []byte(`window.wx_errcode=405;window.wx_code='fixture_code';`)), nil
	})
	poll, err := client.PollWeChatLogin(context.Background(), "fixture_uuid", "scanned")
	if err != nil || poll.Status != "confirmed" || poll.Code != "fixture_code" {
		t.Fatalf("poll result: %v", err)
	}
	for _, uuid := range []string{"https://example.invalid/", "../fixture_uuid", "short", "fixture_uuid?x=y"} {
		if _, err := client.PollWeChatLogin(context.Background(), uuid, ""); err == nil {
			t.Fatal("invalid UUID accepted")
		}
	}
	if _, err := client.PollWeChatLogin(context.Background(), "fixture_uuid", "confirmed"); err == nil || calls != 1 {
		t.Fatal("terminal transaction was polled again")
	}
	if _, err := client.weChatLoginGET(context.Background(), "https://example.invalid/connect/qrconnect", 100); err == nil || calls != 1 {
		t.Fatal("non-allowlisted host was accessed")
	}
}

func TestWeChatLoginCompleteVerifiesCallbackSessionAndIdentity(t *testing.T) {
	callbackCalls, identityCalls := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/auth/wechat/callback.json":
			callbackCalls++
			if r.Method != http.MethodGet || r.URL.Query().Get("code") != "fixture_code" || r.URL.Query().Has("signedState") || r.Header.Get("Pc-Authorization") != "null" {
				t.Error("unexpected callback contract or stale authentication")
			}
			w.Header().Set("cs", "fixture_session")
			io.WriteString(w, `{"status":0,"new_user":false}`)
		case "/api/users/get_user_info.json":
			identityCalls++
			cookie, err := r.Cookie("_educoder_session")
			if err != nil || cookie.Value != "fixture_session" || r.Header.Get("Pc-Authorization") != "fixture_session" {
				t.Error("identity request lacks callback session")
			}
			io.WriteString(w, `{"user_id":42,"login":"fixture_user","name":"Fixture"}`)
		default:
			t.Error("unexpected request path")
		}
	}))
	defer server.Close()
	client := New(server.URL)
	parsed, _ := url.Parse(server.URL)
	client.HTTP.Jar.SetCookies(parsed, []*http.Cookie{{Name: "_educoder_session", Value: "previous_user", Path: "/"}})
	client.LoginName = "previous_user"
	user, err := client.CompleteWeChatLogin(context.Background(), "fixture_code")
	if err != nil || user.UserID != 42 || client.LoginName != "fixture_user" || callbackCalls != 1 || identityCalls != 1 {
		t.Fatalf("complete: %v callbacks=%d identities=%d", err, callbackCalls, identityCalls)
	}
}

func TestWeChatLoginCompleteRejectsIncompleteAuthentication(t *testing.T) {
	for _, test := range []struct {
		name, body, session string
		want                error
	}{
		{"binding", `{"status":0,"new_user":true}`, "fixture_session", ErrLoginBindingRequired},
		{"exam", `{"status":-3,"message":"fixture_code"}`, "fixture_session", ErrLoginExamRestricted},
		{"no session", `{"status":0}`, "", nil},
		{"missing status", `{}`, "fixture_session", nil},
		{"negative", `{"status":-1,"message":"fixture_code"}`, "fixture_session", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			identityCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("cs", test.session)
				if r.URL.Path == "/api/users/get_user_info.json" {
					identityCalls++
					io.WriteString(w, `{"user_id":0,"login":"fixture_user"}`)
					return
				}
				io.WriteString(w, test.body)
			}))
			defer server.Close()
			client := New(server.URL)
			_, err := client.CompleteWeChatLogin(context.Background(), "fixture_code")
			if err == nil || (test.want != nil && !errors.Is(err, test.want)) || strings.Contains(err.Error(), "fixture_code") || hasEducoderSession(client) || client.LoginName != "" || identityCalls != 0 {
				t.Fatalf("incomplete authentication accepted/leaked/residual session: %v", err)
			}
		})
	}
}
