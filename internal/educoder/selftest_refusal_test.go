package educoder

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The body below is the platform's real reply observed on 2026-11-20 when its
// runtime pool was exhausted: status 0, no wss_url, an error_msg TIP.
const resourceBusyLogOutput = `{"status":0,"message":"success","data":{"error_msg":"【TIP】日志实时输出错误信息：当前平台资源紧张，请稍后重试，如问题持续存在，请联系平台运营人员!"}}`

func refusalServer(t *testing.T, logOutput string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/update_file.json"):
			w.Write([]byte(`{"content":{"commitID":"c1","size":4},"resubmit":"","sec_key":"k","content_modified":0}`))
		case strings.HasSuffix(r.URL.Path, "/log_output"):
			w.Write([]byte(logOutput))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestSelfTestClassifiesPlatformRefusalWithoutEchoingIt(t *testing.T) {
	for name, tc := range map[string]struct {
		body      string
		kind      string
		transient bool
	}{
		"resource busy": {resourceBusyLogOutput, "resource_busy", true},
		"other refusal": {`{"status":0,"data":{"error_msg":"实训已关闭"}}`, "refused", false},
	} {
		t.Run(name, func(t *testing.T) {
			server := refusalServer(t, tc.body)
			client := New(server.URL)
			task := selfTestTaskFixture()
			_, err := client.SelfTest(context.Background(), task, "main.cpp", "code", "in", SelfTestOptions{})
			var refused *SelfTestRefusedError
			if !errors.As(err, &refused) || refused.Kind != tc.kind || refused.Transient() != tc.transient {
				t.Fatalf("err=%v", err)
			}
			if strings.Contains(err.Error(), "TIP") || strings.Contains(err.Error(), "实训已关闭") || strings.Contains(err.Error(), "WebSocket") {
				t.Fatalf("diagnostic echoed platform text or misclassified: %q", err.Error())
			}
		})
	}
	// A reply with neither a stream nor an error message stays the protocol
	// failure it was before.
	server := refusalServer(t, `{"status":0,"data":{}}`)
	_, err := New(server.URL).SelfTest(context.Background(), selfTestTaskFixture(), "main.cpp", "code", "in", SelfTestOptions{})
	var refused *SelfTestRefusedError
	if err == nil || errors.As(err, &refused) {
		t.Fatalf("empty reply misclassified: %v", err)
	}
}

func selfTestTaskFixture() *Task {
	st, with := 0, true
	task := &Task{ST: &st, HomeworkID: 44, CourseIdentifier: "C"}
	task.Game.ID, task.Game.Identifier, task.Game.MyShixunID = 3, "g", 4
	task.Challenge.ID, task.Challenge.ST, task.Challenge.WithCodeFile, task.Challenge.Path = 6, &st, &with, "main.cpp"
	task.MyShixun.ID, task.MyShixun.Identifier = 4, "instance"
	task.User.UserID = 5
	task.CodeEditor.EnvironmentID = 12
	task.Environments = []ProgrammingEnvironment{{EnvironmentID: 12, TabType: 1}}
	return task
}
