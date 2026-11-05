package educoder

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

const graphicProject = `{"_type":"Project","name":"fixture","ownedElements":[{"_type":"UMLModel","_id":"model"}]}`
const graphicCommitFixture = "0123456789abcdef0123456789abcdef01234567"

func graphicTaskFixture(t *testing.T) *Task {
	t.Helper()
	f := programmingTaskFixture("GRAPHIC1", 1)
	f["vnc_url"] = "https://runtime.educoder.net/desktop"
	f["challenge"] = map[string]any{"id": 201, "st": 0, "with_code_file": false, "path": "pingce/readme.txt", "task_pass": "答案保存为：/data/workspace/myshixun/answer/step1/step1.mdj"}
	f["shixun_environments"] = []any{map[string]any{"shixun_environment_id": 23, "tab_type": 3, "vnc_url": "https://runtime.educoder.net/desktop"}}
	return decodeTaskFixture(t, f)
}

func TestGraphicPathsAndKind(t *testing.T) {
	for _, tc := range []struct{ description, challenge, want string }{
		{"答案保存为：/data/workspace/myshixun/answer/step1/step1.mdj", "pingce/readme.txt", "answer/step1/step1.mdj"},
		{"请保存在 workspace/myshixun/submit/ 文件夹下，并将文件名改为 step2。", "", "submit/step2.mdj"},
		{"请保存在“workspace/myshixun/用例图/submit”目录下，命名为step3，格式为mdj。", "", "用例图/submit/step3.mdj"},
		{"绘制用例图", "answer/example.mdj", "answer/example.mdj"},
	} {
		task := graphicTaskFixture(t)
		task.Challenge.Description = tc.description
		task.Challenge.Path = tc.challenge
		if got := task.GraphicPaths(); !reflect.DeepEqual(got, []string{tc.want}) {
			t.Errorf("paths(%q)=%v", tc.description, got)
		}
		if task.Kind() != TaskKindGraphic {
			t.Error("explicit graphic task not recognized")
		}
	}
	task := graphicTaskFixture(t)
	task.Challenge.Description = "答案保存为 /data/workspace/myshixun/a.mdj 或 /data/workspace/myshixun/b.mdj"
	if len(task.GraphicPaths()) != 2 || task.Kind() == TaskKindGraphic {
		t.Fatal("ambiguous paths accepted")
	}
	task = graphicTaskFixture(t)
	one := 1
	task.ST = &one
	task.Challenge.ST = &one
	task.Chooses = []ChoiceQuestion{{}}
	if task.Kind() != TaskKindChoice {
		t.Fatal("choice sharing VNC misclassified")
	}
	for _, bad := range []string{"../a.mdj", "/etc/a.mdj", "answer/../../a.mdj", "pingce/a.mdj", "secret/a.mdj", ".git/config.mdj", "answer\\a.mdj", "answer\na.mdj"} {
		if _, err := normalizeGraphicPath(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

type fakeGraphicTransport struct {
	session     *fakeGraphicSession
	connections []GraphicConnection
	err         error
}

func (f *fakeGraphicTransport) Open(_ context.Context, c GraphicConnection) (GraphicSession, error) {
	f.connections = append(f.connections, c)
	if f.err != nil {
		return nil, f.err
	}
	return f.session, nil
}

type fakeGraphicSession struct {
	content []byte
	exists  bool
	saves   int
	err     error
}

func (f *fakeGraphicSession) ReadFile(context.Context, string) ([]byte, bool, error) {
	return append([]byte{}, f.content...), f.exists, nil
}
func (f *fakeGraphicSession) SaveAndCommit(_ context.Context, _ string, hash string, exists bool, content []byte) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if hash != graphicHash(f.content) || exists != f.exists {
		return "", ErrGraphicChanged
	}
	f.saves++
	f.content = append([]byte{}, content...)
	f.exists = true
	return graphicCommitFixture, nil
}
func (f *fakeGraphicSession) Close() error { return nil }

func TestGraphicHTTPProtocolUsesRuntimeCommitThenEvaluation(t *testing.T) {
	task := graphicTaskFixture(t)
	var events []string
	built := false
	polls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		events = append(events, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tasks/GRAPHIC1.json":
			clone := *task
			if built {
				clone.Game.Status = 2
			}
			_ = json.NewEncoder(w).Encode(clone)
		case "/api/myshixuns/MY1/start.json":
			if q := r.URL.Query(); q.Get("tab_type") != "3" || q.Get("shixun_environment_id") != "23" || q.Get("game_id") != "101" || q.Get("homework_common_id") != "99" {
				t.Errorf("wrong start context: %v", q)
			}
			ioJSON(w, `{"status":0,"data":{"ssh_address":"ssh.educoder.net","port":"2222","username":"headless","password":"ephemeral-password","myshixun_id":80}}`)
		case "/api/tasks/GRAPHIC1/game_status.json":
			if !built {
				ioJSON(w, `{"status":0,"sec_key":"ephemeral-key"}`)
				break
			}
			polls++
			if polls == 1 {
				ioJSON(w, `{"status":0,"running_code_status":1}`)
			} else {
				ioJSON(w, `{"status":2,"test_sets_count":1,"sets_error_count":0,"test_sets":[{"result":true,"output":"matched"}]}`)
			}
		case "/api/tasks/GRAPHIC1/game_build.json":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["tab_type"] != float64(3) || body["sec_key"] != "ephemeral-key" || body["extras"].(map[string]any)["commitID"] != graphicCommitFixture {
				t.Errorf("build did not use runtime commit")
			}
			built = true
			ioJSON(w, `{"status":1,"port":55}`)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	transport := &fakeGraphicTransport{session: &fakeGraphicSession{}}
	protocol := NewGraphicProtocol(New(server.URL), transport)
	doc, err := protocol.Read(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Exists || doc.Content != "" {
		t.Fatal("new graphic file baseline lost")
	}
	result, err := protocol.Submit(context.Background(), task, doc, graphicProject, PollOptions{Interval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Confirmed || !result.Saved || !result.Submitted || result.Document == nil || result.Document.Hash != result.ContentHash || result.Evaluation == nil || !result.Evaluation.Passed || result.ContentHash != graphicHash([]byte(graphicProject)) || transport.session.saves != 1 {
		t.Fatalf("result=%+v", result)
	}
	if len(transport.connections) != 2 {
		t.Fatal("read and submit must obtain current runtime credentials")
	}
	encoded, _ := json.Marshal(transport.connections)
	if strings.Contains(string(encoded), "password") || strings.Contains(string(encoded), "ssh.educoder") {
		t.Fatal("runtime credentials serializable")
	}
	want := []string{"GET /api/myshixuns/MY1/start.json", "GET /api/tasks/GRAPHIC1.json", "GET /api/myshixuns/MY1/start.json", "GET /api/tasks/GRAPHIC1/game_status.json", "POST /api/tasks/GRAPHIC1/game_build.json", "GET /api/tasks/GRAPHIC1/game_status.json", "GET /api/tasks/GRAPHIC1/game_status.json", "GET /api/tasks/GRAPHIC1.json"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events=%v", events)
	}
}

func TestGraphicDoesNotAcceptCachedVerdict(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ioJSON(w, `{"status":2,"test_sets_count":1,"sets_error_count":0,"test_sets":[{"result":true}]}`)
	}))
	defer server.Close()
	var baseline EvaluationResult
	_ = json.Unmarshal([]byte(`{"status":2,"test_sets_count":1,"sets_error_count":0,"test_sets":[{"result":true}]}`), &baseline)
	protocol := NewGraphicProtocol(New(server.URL), &fakeGraphicTransport{})
	if result, err := protocol.wait(context.Background(), graphicTaskFixture(t), nil, baseline, PollOptions{Timeout: 20 * time.Millisecond, Interval: time.Millisecond}); !errors.Is(err, context.DeadlineExceeded) || result != nil {
		t.Fatalf("cached previous verdict accepted: %v %v", result, err)
	}
}

func TestGraphicReadRetriesEventuallyConsistentRuntimeBinding(t *testing.T) {
	task := graphicTaskFixture(t)
	starts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "start.json") {
			starts++
			if starts < 3 {
				ioJSON(w, `{"status":0,"data":{}}`)
				return
			}
			ioJSON(w, `{"status":0,"data":{"ssh_address":"ssh.educoder.net","port":2222,"username":"headless","password":"ephemeral-password","myshixun_id":80}}`)
			return
		}
		_ = json.NewEncoder(w).Encode(task)
	}))
	defer server.Close()
	protocol := NewGraphicProtocol(New(server.URL), &fakeGraphicTransport{session: &fakeGraphicSession{content: []byte(graphicProject), exists: true}})
	if _, err := protocol.Read(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if starts != 3 {
		t.Fatalf("start attempts=%d want 3", starts)
	}
}

func TestGraphicRetriesMissingEvaluationKeyBeforeDeclaringSavedNotSubmitted(t *testing.T) {
	task := graphicTaskFixture(t)
	statusReads := 0
	built := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "start.json"):
			ioJSON(w, `{"status":0,"data":{"ssh_address":"ssh.educoder.net","port":2222,"username":"headless","password":"private-secret","myshixun_id":80}}`)
		case strings.HasSuffix(r.URL.Path, "game_status.json"):
			statusReads++
			if statusReads < 3 {
				ioJSON(w, `{"status":0}`)
			} else if !built {
				ioJSON(w, `{"status":0,"sec_key":"eventual-key"}`)
			} else {
				ioJSON(w, `{"status":2,"test_sets_count":1,"sets_error_count":0,"test_sets":[{"result":true,"compile_success":1}]}`)
			}
		case strings.HasSuffix(r.URL.Path, "game_build.json"):
			built = true
			ioJSON(w, `{"status":1,"port":1234}`)
		default:
			clone := *task
			if built {
				clone.Game.Status = 2
			}
			_ = json.NewEncoder(w).Encode(&clone)
		}
	}))
	defer server.Close()
	fake := &fakeGraphicTransport{session: &fakeGraphicSession{}}
	protocol := NewGraphicProtocol(New(server.URL), fake)
	doc, err := protocol.Read(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	result, err := protocol.Submit(context.Background(), task, doc, graphicProject, PollOptions{})
	if err != nil || result == nil || !result.Saved || !result.Submitted || statusReads < 4 {
		t.Fatalf("missing-key retry behavior result=%+v err=%v reads=%d", result, err, statusReads)
	}
}

func TestGraphicMissingEvaluationKeyIsSavedNotSubmitted(t *testing.T) {
	task := graphicTaskFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "start.json"):
			ioJSON(w, `{"status":0,"data":{"ssh_address":"ssh.educoder.net","port":2222,"username":"headless","password":"private-secret","myshixun_id":80}}`)
		case strings.HasSuffix(r.URL.Path, "game_status.json"):
			ioJSON(w, `{"status":0}`)
		case strings.HasSuffix(r.URL.Path, "game_build.json"):
			t.Error("formal build without key")
		default:
			_ = json.NewEncoder(w).Encode(task)
		}
	}))
	defer server.Close()
	fake := &fakeGraphicTransport{session: &fakeGraphicSession{}}
	protocol := NewGraphicProtocol(New(server.URL), fake)
	doc, err := protocol.Read(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	result, err := protocol.Submit(context.Background(), task, doc, graphicProject, PollOptions{})
	if err == nil || result == nil || !result.Saved || result.Submitted || result.Document == nil {
		t.Fatalf("wrong partial result %+v %v", result, err)
	}
	if result.Document.Hash != graphicHash(fake.session.content) {
		t.Fatal("saved baseline not updated")
	}
}

func TestGraphicRuntimeCredentialsNeverBecomeTaskJSONOrErrors(t *testing.T) {
	task := graphicTaskFixture(t)
	encoded, _ := json.Marshal(task)
	if strings.Contains(string(encoded), "runtime.educoder.net") {
		t.Fatal("task JSON retained a runtime URL")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		ioJSON(w, `{"status":-1,"message":"password=secret-do-not-log"}`)
	}))
	defer server.Close()
	if _, err := NewGraphicProtocol(New(server.URL), &fakeGraphicTransport{}).Read(context.Background(), task); err == nil || strings.Contains(err.Error(), "secret-do-not-log") {
		t.Fatalf("start error leaked: %v", err)
	}
}
func ioJSON(w http.ResponseWriter, s string) { _, _ = w.Write([]byte(s)) }

func TestGraphicRejectsChangedFileAndInvalidProjectBeforeBuild(t *testing.T) {
	task := graphicTaskFixture(t)
	builds := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "start.json"):
			ioJSON(w, `{"status":0,"data":{"ssh_address":"ssh.educoder.net","port":2222,"username":"headless","password":"private-secret","myshixun_id":80}}`)
		case r.URL.Path == "/api/tasks/GRAPHIC1.json":
			_ = json.NewEncoder(w).Encode(task)
		default:
			builds++
			t.Errorf("mutation attempted %s", r.URL.Path)
		}
	}))
	defer server.Close()
	fake := &fakeGraphicTransport{session: &fakeGraphicSession{content: []byte(graphicProject), exists: true}}
	protocol := NewGraphicProtocol(New(server.URL), fake)
	doc, err := protocol.Read(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{`not json`, `{"_type":"UMLModel"}`, `[]`} {
		if _, err := protocol.Submit(context.Background(), task, doc, content, PollOptions{}); err == nil {
			t.Fatal("invalid StarUML accepted")
		}
	}
	fake.session.content = []byte(`{"_type":"Project","name":"manual edit"}`)
	if _, err := protocol.Submit(context.Background(), task, doc, graphicProject, PollOptions{}); !errors.Is(err, ErrGraphicChanged) {
		t.Fatalf("changed baseline error=%v", err)
	}
	if builds != 0 || fake.session.saves != 0 {
		t.Fatal("changed or invalid file submitted")
	}
	fake.session.err = errors.New("password=private-secret signed-url-secret")
	if _, err := protocol.Submit(context.Background(), task, doc, graphicProject, PollOptions{}); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("transport diagnostic leaked")
	}
}

func TestGraphicRejectsTaskOwnerChange(t *testing.T) {
	task := graphicTaskFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clone := *task
		clone.User.UserID = 999
		_ = json.NewEncoder(w).Encode(clone)
	}))
	defer server.Close()
	fake := &fakeGraphicTransport{session: &fakeGraphicSession{}}
	protocol := NewGraphicProtocol(New(server.URL), fake)
	doc := &GraphicDocument{Path: task.GraphicPaths()[0], Hash: graphicHash(nil), game: task.Game.Identifier, myshixunID: task.MyShixun.ID, userID: task.User.UserID}
	if _, err := protocol.Submit(context.Background(), task, doc, graphicProject, PollOptions{}); err == nil {
		t.Fatal("changed task owner accepted")
	}
	if len(fake.connections) != 0 {
		t.Fatal("changed owner opened runtime")
	}
}
