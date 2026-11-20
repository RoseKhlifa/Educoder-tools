package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/platform"
)

func TestUserDTODisplaysRealNameFromProfileWithoutChangingIdentity(t *testing.T) {
	for _, fixture := range []struct {
		name, realName, login, want string
	}{
		{"", "  示例姓名  ", "generated-login", "示例姓名"},
		{"自动昵称", "示例姓名", "generated-login", "示例姓名"},
		{"  已有昵称  ", " \t ", "generated-login", "已有昵称"},
		{" \t ", "", "generated-login", "generated-login"},
	} {
		user := educoder.User{UserID: 42, Name: fixture.name, RealName: fixture.realName, Login: fixture.login}
		got := toUserDTO(user)
		if got.Name != fixture.want || got.UserID != 42 || got.Login != fixture.login || got.RealName != strings.TrimSpace(fixture.realName) {
			t.Fatalf("name=%q realName=%q: dto=%+v want=%q", fixture.name, fixture.realName, got, fixture.want)
		}
		if user.Name != fixture.name || user.RealName != fixture.realName {
			t.Fatal("display resolution mutated upstream evidence")
		}
	}
}

func TestEveryLoginStoresAndReturnsTheVerifiedRealName(t *testing.T) {
	for _, mode := range []string{"password", "sms", "wechat"} {
		t.Run(mode, func(t *testing.T) {
			ps, err := platform.Open(filepath.Join(t.TempDir(), "platform.sqlite"), platform.Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer ps.Close()
			original, err := ps.UpsertLogin(context.Background(), platform.Identity{UpstreamID: 91, Login: "generated-login", Name: "旧昵称"})
			if err != nil {
				t.Fatal(err)
			}
			fake := &fakeClient{user: educoder.User{UserID: 91, Login: "generated-login", RealName: "示例姓名"}}
			app := NewServerWithFactory(DefaultConfig(), func(string) EducoderClient { return fake })
			app.AttachPlatform(ps)
			r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"login":"generated-login","password":"fixture-only"}`))
			if mode == "sms" {
				r = httptest.NewRequest(http.MethodPost, "/api/auth/sms/login", strings.NewReader(`{"phone":"13000000000","code":"fixture-only"}`))
			}
			w := httptest.NewRecorder()
			if mode == "wechat" {
				// All three authentication handlers converge here only after the
				// protocol client verifies the user's stable numeric identity.
				app.completeLogin(w, r, "fixture", fake, &fake.user)
			} else {
				app.ServeHTTP(w, r)
			}
			if w.Code != http.StatusOK {
				t.Fatalf("login=%d %s", w.Code, w.Body.String())
			}
			var body struct {
				Data struct {
					User userDTO `json:"user"`
				} `json:"data"`
			}
			if err = json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Data.User.Name != "示例姓名" || body.Data.User.RealName != "示例姓名" || body.Data.User.UserID != 91 || body.Data.User.Login != "generated-login" {
				t.Fatalf("login profile=%+v", body.Data.User)
			}
			stored, err := ps.LookupUser(context.Background(), original.ID)
			if err != nil || stored.Name != "示例姓名" || stored.UpstreamID != 91 || stored.Login != "generated-login" {
				t.Fatalf("stored identity=%+v err=%v", stored, err)
			}
			meRequest := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
			for _, cookie := range w.Result().Cookies() {
				meRequest.AddCookie(cookie)
			}
			me := httptest.NewRecorder()
			app.ServeHTTP(me, meRequest)
			if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), `"name":"示例姓名"`) {
				t.Fatalf("me=%d %s", me.Code, me.Body.String())
			}
		})
	}
}
