package educoder

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func TestProtocolVectors(t *testing.T) {
	if got, want := EncryptPassword("test-password"), "p15krDvHEPYqjf560oABdQ=="; got != want {
		t.Fatalf("password encryption = %q, want %q", got, want)
	}
	if got, want := Signature("GET", "1793348448103"), "48d0f98354bbf9387e51614db35b4cae"; got != want {
		t.Fatalf("HAR signature = %q, want %q", got, want)
	}
}

func TestPhoneCodeLoginProtocol(t *testing.T) {
	var requests []string
	var trace []string
	const phone = "13000000000"
	const code = "sms-code-canary"
	digest := md5.Sum([]byte(phoneVerificationSalt + phone))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/accounts/valid_email_and_phone.json":
			if r.Method != http.MethodGet || r.URL.Query().Get("login") != phone || r.URL.Query().Get("type") != "1" {
				t.Errorf("unexpected phone validation request: %s %s", r.Method, r.URL.String())
			}
			_, _ = w.Write([]byte(`{"status":-1}`))
		case "/api/accounts/get_verification_code.json":
			if r.URL.Query().Get("login") != phone || r.URL.Query().Get("type") != "6" || r.URL.Query().Get("smscode") != hex.EncodeToString(digest[:]) {
				t.Errorf("unexpected code request: %s", r.URL.String())
			}
			_, _ = w.Write([]byte(`{"status":1}`))
		case "/api/accounts/login_for_phone.json":
			if r.Method != http.MethodGet || r.URL.Query().Get("phone") != phone || r.URL.Query().Get("code") != code || len(r.URL.Query()) != 2 {
				t.Errorf("unexpected phone login request: %s %s", r.Method, r.URL.String())
			}
			http.SetCookie(w, &http.Cookie{Name: "_educoder_session", Value: "phone-session-canary", Path: "/"})
			_, _ = w.Write([]byte(`{"status":0}`))
		case "/api/users/get_user_info.json":
			if r.Header.Get("Pc-Authorization") != "phone-session-canary" {
				t.Errorf("userinfo request did not use phone session")
			}
			_, _ = w.Write([]byte(`{"user_id":12,"login":"sms-user","name":"SMS User","identity":"student"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	c := New(server.URL)
	c.Trace = func(value string) { trace = append(trace, value) }
	if err := c.SendPhoneVerificationCode(context.Background(), phone); err != nil {
		t.Fatalf("send verification code: %v", err)
	}
	user, err := c.LoginWithPhone(context.Background(), phone, code)
	if err != nil || user == nil || user.UserID != 12 || user.Login != "sms-user" {
		t.Fatalf("phone login: user=%+v err=%v", user, err)
	}
	if c.LoginName != "sms-user" {
		t.Fatalf("LoginName = %q", c.LoginName)
	}
	if strings.Contains(strings.Join(trace, "\n"), phone) || strings.Contains(strings.Join(trace, "\n"), code) {
		t.Fatal("protocol trace contained phone or SMS code")
	}
	joined := strings.Join(requests, "\n")
	for _, want := range []string{"valid_email_and_phone.json", "get_verification_code.json", "login_for_phone.json", "get_user_info.json"} {
		if !strings.Contains(joined, want) {
			t.Errorf("request sequence missing %s:\n%s", want, joined)
		}
	}
}

func TestPhoneCodeDoesNotSendForUnknownPhone(t *testing.T) {
	var sendCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/accounts/valid_email_and_phone.json" {
			_, _ = w.Write([]byte(`{"status":1,"message":"not registered"}`))
			return
		}
		if r.URL.Path == "/api/accounts/get_verification_code.json" {
			sendCalls++
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	c := New(server.URL)
	err := c.SendPhoneVerificationCode(context.Background(), "13000000000")
	if !errors.Is(err, ErrPhoneNotRegistered) || sendCalls != 0 {
		t.Fatalf("unknown phone: err=%v sendCalls=%d", err, sendCalls)
	}
}

func TestPhoneVerificationStrictResponseAndNoUnconfirmedSend(t *testing.T) {
	for _, test := range []struct {
		name, check, sent string
		wantSend          bool
		wantSuccess       bool
	}{
		{"existing-negative", `{"status":-1}`, `{"status":1}`, true, true},
		{"existing-zero", `{"status":0}`, `{"status":0}`, true, true},
		{"existing-other-negative-follows-client", `{"status":-7}`, `{"status":1}`, true, true},
		{"unregistered", `{"status":1}`, `{"status":1}`, false, false},
		{"check-missing", `{}`, `{"status":1}`, false, false},
		{"check-null", `{"status":null}`, `{"status":1}`, false, false},
		{"check-string", `{"status":"-1"}`, `{"status":1}`, false, false},
		{"check-fractional", `{"status":-0.5}`, `{"status":1}`, false, false},
		{"send-missing", `{"status":-1}`, `{}`, true, false},
		{"send-null", `{"status":-1}`, `{"status":null}`, true, false},
		{"send-string", `{"status":-1}`, `{"status":"1"}`, true, false},
		{"send-negative", `{"status":-1}`, `{"status":-1}`, true, false},
		{"send-unknown-positive", `{"status":-1}`, `{"status":2}`, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			sendCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/accounts/valid_email_and_phone.json" {
					_, _ = io.WriteString(w, test.check)
					return
				}
				sendCalls++
				_, _ = io.WriteString(w, test.sent)
			}))
			defer server.Close()
			err := New(server.URL).SendPhoneVerificationCode(context.Background(), "13000000000")
			if (err == nil) != test.wantSuccess || (sendCalls == 1) != test.wantSend || sendCalls > 1 {
				t.Fatalf("success=%v err=%v sends=%d", test.wantSuccess, err, sendCalls)
			}
		})
	}
}

func TestPhoneLoginStrictResultAndContinuationStates(t *testing.T) {
	for _, test := range []struct {
		name, body string
		wantError  error
	}{
		{"missing", `{}`, nil},
		{"null", `{"status":null}`, nil},
		{"string", `{"status":"0"}`, nil},
		{"fractional", `{"status":0.5}`, nil},
		{"unknown-positive", `{"status":1}`, nil},
		{"binding-required", `{"status":-4}`, ErrLoginBindingRequired},
		{"exam-restricted", `{"status":-5}`, ErrLoginExamRestricted},
		{"rejected", `{"status":-1}`, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			loginCalls, infoCalls := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path != "/api/accounts/login_for_phone.json" {
					infoCalls++
					_, _ = io.WriteString(w, `{"user_id":1,"login":"unexpected"}`)
					return
				}
				loginCalls++
				http.SetCookie(w, &http.Cookie{Name: "_educoder_session", Value: "failed-auth-session", Path: "/"})
				w.Header().Set("cs", "failed-auth-header-session")
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			client := New(server.URL)
			user, err := client.LoginWithPhone(context.Background(), "13000000000", "code-canary")
			if err == nil || user != nil || loginCalls != 1 || infoCalls != 0 || hasEducoderSession(client) || client.LoginName != "" {
				t.Fatalf("user=%v err=%v loginCalls=%d infoCalls=%d session=%v", user, err, loginCalls, infoCalls, hasEducoderSession(client))
			}
			if test.wantError != nil && !errors.Is(err, test.wantError) {
				t.Fatalf("error=%v, want classification %v", err, test.wantError)
			}
		})
	}
}

func TestPhoneLoginUsesOnlyNewSessionAndAuthoritativeIdentity(t *testing.T) {
	for _, test := range []struct {
		name, cookie, cs, identity string
		wantSuccess                bool
	}{
		{"set-cookie", "fresh-cookie", "", `{"user_id":21,"login":"new-user"}`, true},
		{"cs-header", "", "fresh-cs", `{"user_id":21,"login":"new-user"}`, true},
		{"cs-precedes-cookie", "fresh-cookie", "fresh-cs", `{"user_id":21,"login":"new-user"}`, true},
		{"stale-cookie-only", "", "", `{"user_id":21,"login":"new-user"}`, false},
		{"invalid-cookie-marker", "logout", "", `{"user_id":21,"login":"new-user"}`, false},
		{"invalid-cs", "", "invalid;cookie", `{"user_id":21,"login":"new-user"}`, false},
		{"oversize-cs", "", strings.Repeat("x", 8193), `{"user_id":21,"login":"new-user"}`, false},
		{"identity-no-id", "fresh-cookie", "", `{"login":"new-user"}`, false},
		{"identity-negative-id", "fresh-cookie", "", `{"user_id":-1,"login":"new-user"}`, false},
		{"identity-blank-login", "fresh-cookie", "", `{"user_id":21,"login":" "}`, false},
		{"identity-null-status", "fresh-cookie", "", `{"status":null,"user_id":21,"login":"new-user"}`, false},
		{"identity-string-status", "fresh-cookie", "", `{"status":"0","user_id":21,"login":"new-user"}`, false},
		{"identity-rejected-status", "fresh-cookie", "", `{"status":-1,"user_id":21,"login":"new-user"}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/accounts/login_for_phone.json" {
					if r.Header.Get("Pc-Authorization") != "null" || r.Header.Get("Cookie") != "" || r.URL.Query().Get("zzud") != "" {
						t.Error("previous account leaked into authentication request")
					}
					if test.cookie != "" {
						http.SetCookie(w, &http.Cookie{Name: "_educoder_session", Value: test.cookie, Path: "/"})
					}
					w.Header().Set("cs", test.cs)
					_, _ = io.WriteString(w, `{"status":0}`)
					return
				}
				want := test.cs
				if want == "" {
					want = test.cookie
				}
				if r.Header.Get("Pc-Authorization") != want {
					t.Error("identity check did not use newly established session")
				}
				_, _ = io.WriteString(w, test.identity)
			}))
			defer server.Close()
			client := New(server.URL)
			base, _ := url.Parse(server.URL)
			oldHTTP := client.HTTP
			oldHTTP.Jar.SetCookies(base, []*http.Cookie{{Name: "_educoder_session", Value: "old-session", Path: "/"}})
			client.LoginName = "previous-user"
			user, err := client.LoginWithPhone(context.Background(), "13000000000", "code-canary")
			if (err == nil) != test.wantSuccess || (user != nil) != test.wantSuccess {
				t.Fatalf("user=%v err=%v wantSuccess=%v", user, err, test.wantSuccess)
			}
			if !test.wantSuccess && (hasEducoderSession(client) || client.LoginName != "") {
				t.Fatal("failed login left an authenticated session installed")
			}
			if oldHTTP.Jar.Cookies(base)[0].Value != "old-session" {
				t.Fatal("reset modified another client's existing jar")
			}
		})
	}
}

func TestAuthenticationCSIsScopedToSelectedSuccessResponses(t *testing.T) {
	for _, test := range []struct {
		path, body string
		want       bool
	}{
		{"/api/accounts/login.json", `{"login":"fixture"}`, true},
		{"/api/accounts/login_for_phone.json", `{"status":0}`, true},
		{"/api/auth/wechat/callback.json", `{"status":0}`, true},
		{"/api/auth/wechat/web_callback.json", `{"status":0}`, true},
		{"/api/auth/wechat/callback.json", `{"status":-3}`, false},
		{"/api/auth/wechat/callback.json", `{"status":null}`, false},
		{"/api/accounts/get_verification_code.json", `{"status":1}`, false},
		{"/api/users/get_user_info.json", `{"login":"fixture"}`, false},
	} {
		t.Run(test.path+test.body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("cs", "new-session")
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			client := New(server.URL)
			var out map[string]any
			if err := client.requestWithStatuses(context.Background(), http.MethodGet, test.path, nil, nil, "/", &out, anyStatus); err != nil {
				t.Fatal(err)
			}
			if hasEducoderSession(client) != test.want {
				t.Fatalf("session=%v want=%v", hasEducoderSession(client), test.want)
			}
		})
	}
}

func TestPasswordLoginAcceptsAuthenticationCS(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/accounts/login.json" {
			t.Errorf("unexpected password login request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("cs", "password-cs-session")
		_, _ = io.WriteString(w, `{"user_id":1,"login":"fixture-user"}`)
	}))
	defer server.Close()
	client := New(server.URL)
	user, err := client.Login(context.Background(), "fixture-user", "fixture-password")
	if err != nil || user == nil || user.Login != "fixture-user" || !hasEducoderSession(client) {
		t.Fatalf("password cs login: user=%v err=%v", user, err)
	}
}

func TestPhoneAuthenticationErrorsDoNotExposeSecretsOrRetry(t *testing.T) {
	const phone, code = "13000000000", "sms-secret-canary"
	for _, operation := range []string{"send", "login"} {
		for _, failure := range []string{"transport", "http", "body", "redirect"} {
			t.Run(operation+"/"+failure, func(t *testing.T) {
				client := New("https://fixture.invalid")
				calls := 0
				var trace []string
				client.Trace = func(s string) { trace = append(trace, s) }
				client.HTTP.Transport = progressReadRoundTripper(func(r *http.Request) (*http.Response, error) {
					calls++
					if r.Body == nil || r.GetBody != nil {
						t.Fatal("authentication GET can be implicitly replayed")
					}
					if operation == "send" && r.URL.Path == "/api/accounts/valid_email_and_phone.json" {
						return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"status":-1}`)), Request: r}, nil
					}
					if failure == "transport" {
						return nil, fmt.Errorf("echo %s %s %s", phone, code, r.URL)
					}
					response := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"status":-1,"message":"%s %s"}`, phone, code))), Request: r}
					switch failure {
					case "http":
						response.StatusCode = 500
					case "body":
						response.Body = phoneAuthErrorBody{errors.New(phone + " " + code)}
					case "redirect":
						response.StatusCode = 307
						response.Header.Set("Location", "https://other.invalid/consume?code="+code)
					}
					return response, nil
				})
				var err error
				wantCalls := 1
				if operation == "send" {
					wantCalls = 2
					err = client.SendPhoneVerificationCode(context.Background(), phone)
				} else {
					_, err = client.LoginWithPhone(context.Background(), phone, code)
				}
				if err == nil || calls != wantCalls {
					t.Fatalf("err=%v calls=%d want=%d", err, calls, wantCalls)
				}
				public := err.Error() + strings.Join(trace, " ")
				if strings.Contains(public, phone) || strings.Contains(public, code) {
					t.Fatal("authentication error or trace exposed a secret")
				}
			})
		}
	}
}

type phoneAuthErrorBody struct{ err error }

func (b phoneAuthErrorBody) Read([]byte) (int, error) { return 0, b.err }
func (phoneAuthErrorBody) Close() error               { return nil }

func TestLoginCourseHomeworkEnterFlow(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Method != http.MethodOptions {
			if r.Header.Get("X-EDU-Type") != "pc" || !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(r.Header.Get("X-EDU-Signature")) {
				t.Errorf("missing protocol headers for %s", r.URL.Path)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/accounts/login.json":
			w.Header().Add("Set-Cookie", "_educoder_session=test-session; Path=/")
			json.NewEncoder(w).Encode(map[string]any{"user_id": 1, "login": "demo", "name": "Demo", "identity": "student"})
		case r.URL.Path == "/api/users/get_user_info.json":
			if got := r.Header.Get("Pc-Authorization"); got != "test-session" {
				t.Errorf("Pc-Authorization = %q", got)
			}
			json.NewEncoder(w).Encode(map[string]any{"user_id": 1, "login": "demo", "name": "Demo", "identity": "student"})
		case r.URL.Path == "/api/users/demo/courses.json":
			json.NewEncoder(w).Encode(map[string]any{"count": 1, "courses": []any{map[string]any{"id": 10, "name": "Course", "first_category_url": "/classrooms/COURSE1/announcement", "homework_commons_count": 1}}})
		case r.URL.Path == "/api/courses/COURSE1/homework_commons.json":
			json.NewEncoder(w).Encode(map[string]any{"query_total_count": 1, "homeworks": []any{map[string]any{"homework_id": 99, "name": "Homework", "is_shixun": true, "shixun_identifier": "SHIXUN1", "status": []string{"提交中"}}}})
		case r.URL.Path == "/api/shixuns/SHIXUN1/shixun_exec.json":
			json.NewEncoder(w).Encode(map[string]any{"game_identifier": "GAME1"})
		case r.URL.Path == "/api/tasks/GAME1.json":
			json.NewEncoder(w).Encode(map[string]any{"game": map[string]any{"id": 7, "identifier": "GAME1", "myshixun_id": 8}, "challenge": map[string]any{"id": 9, "subject": "Challenge", "task_pass": "Description"}, "shixun": map[string]any{"id": 11, "identifier": "SHIXUN1", "name": "Shixun"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	c := New(server.URL)
	user, err := c.Login(context.Background(), "demo", "test-password")
	if err != nil || user.Login != "demo" {
		t.Fatalf("login: user=%+v err=%v", user, err)
	}
	if _, err = c.UserInfo(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	courses, err := c.Courses(context.Background(), "processing")
	if err != nil || len(courses) != 1 || courses[0].Identifier != "COURSE1" {
		t.Fatalf("courses=%+v err=%v", courses, err)
	}
	task, err := c.Enter(context.Background(), "COURSE1", 99, "")
	if err != nil || task.Game.Identifier != "GAME1" {
		t.Fatalf("task=%+v err=%v", task, err)
	}
	joined := strings.Join(requests, "\n")
	for _, want := range []string{"POST /api/accounts/login.json", "GET /api/users/get_user_info.json", "GET /api/users/demo/courses.json", "GET /api/courses/COURSE1/homework_commons.json", "GET /api/shixuns/SHIXUN1/shixun_exec.json", "GET /api/tasks/GAME1.json"} {
		if !strings.Contains(joined, want) {
			t.Errorf("request sequence missing %q:\n%s", want, joined)
		}
	}
}
