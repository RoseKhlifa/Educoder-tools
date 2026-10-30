package educoder

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func programmingTaskFixture(game string, index int) map[string]any {
	return map[string]any{
		"st": 0, "game_count": 2, "homework_common_id": 99, "course_identifier": "COURSE1",
		"game":                map[string]any{"id": 100 + index, "identifier": game, "myshixun_id": 80},
		"myshixun":            map[string]any{"id": 80, "identifier": "MY1"},
		"user":                map[string]any{"user_id": 7},
		"challenge":           map[string]any{"id": 200 + index, "st": 0, "with_code_file": true, "task_pass": "# 题干\n保留 Markdown", "path": fmt.Sprintf(" src/第%d关.cpp；include/stack.h；； ", index)},
		"shixun":              map[string]any{"challenges_count": 12},
		"code_editor":         map[string]any{"shixun_environment_id": 12, "name": "C/C++"},
		"shixun_environments": []any{map[string]any{"shixun_environment_id": 12, "tab_type": 1}},
		"test_sets":           []any{map[string]any{"input": "1\n", "output": "YES\n", "result": nil, "compile_success": nil}},
	}
}

func decodeTaskFixture(t *testing.T, fixture map[string]any) *Task {
	t.Helper()
	data, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	var task Task
	if err := json.Unmarshal(data, &task); err != nil {
		t.Fatal(err)
	}
	return &task
}

func TestProgrammingTwoExplicitChallenges(t *testing.T) {
	var mu sync.Mutex
	var events []string
	polls := map[string]int{}
	builds := map[string]int{}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/runtime/log" {
			if r.Header.Get("Cookie") != "" || r.Header.Get("Pc-Authorization") != "" {
				t.Error("API credentials leaked to WebSocket")
			}
			conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.CloseNow()
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			typeID, message, err := conn.Read(ctx)
			if err != nil || typeID != websocket.MessageText || string(message) != `{"data":80,"tp":"log"}` {
				t.Errorf("start message type=%v message=%q err=%v", typeID, message, err)
			}
			for _, frame := range []struct {
				kind websocket.MessageType
				text string
			}{{websocket.MessageBinary, "YES\r\n"}, {websocket.MessageText, "ProcessFinished"}} {
				if err := conn.Write(ctx, frame.kind, []byte(base64.RawStdEncoding.EncodeToString([]byte(frame.text)))); err != nil {
					t.Error(err)
				}
			}
			_, _, err = conn.Read(ctx)
			if websocket.CloseStatus(err) != websocket.StatusNormalClosure {
				t.Errorf("self-test closure = %v", err)
			}
			return
		}
		mu.Lock()
		defer mu.Unlock()
		events = append(events, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("X-EDU-Signature") == "" || r.Header.Get("Pc-Authorization") != "test-session" {
			t.Errorf("missing signed authentication for %s", r.URL.Path)
		}
		for i, game := range []string{"GAME1", "GAME2"} {
			index := i + 1
			prefix := "/api/tasks/" + game
			switch r.URL.Path {
			case prefix + ".json":
				json.NewEncoder(w).Encode(programmingTaskFixture(game, index))
				return
			case prefix + "/rep_content.json":
				filename := "stack.h"
				if strings.HasPrefix(r.URL.Query().Get("path"), "src/") {
					filename = fmt.Sprintf("第%d关.cpp", index)
				}
				if r.URL.Query().Get("homework_common_id") != "99" {
					t.Error("file read missing homework context")
				}
				json.NewEncoder(w).Encode(map[string]any{"filename": filename, "language": "cpp", "file_type": "txt", "content": map[string]any{"content": base64.StdEncoding.EncodeToString([]byte("// 现存代码\n")), "size": 16}})
				return
			case prefix + "/log_output":
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				if body["test_case_content"] != "1\n" || body["shixun_environment_id"] != float64(12) || body["tab_type"] != float64(1) {
					t.Errorf("log_output body = %#v", body)
				}
				json.NewEncoder(w).Encode(map[string]any{"status": 0, "data": map[string]any{"wss_url": strings.Replace(server.URL, "http://", "ws://", 1) + "/runtime"}})
				return
			case prefix + "/game_build.json":
				builds[game]++
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				extras, _ := body["extras"].(map[string]any)
				if body["sec_key"] != "secret-memory-key" || body["first"] != float64(1) || extras["commitID"] != "fixture-commit" || extras["currentUserId"] != float64(7) || extras["challenge_id"] != float64(200+index) {
					t.Errorf("invalid build binding: %#v", body)
				}
				json.NewEncoder(w).Encode(map[string]any{"status": 1, "port": 4321, "res": map[string]any{"success": false, "code": 0}})
				return
			case prefix + "/game_status.json":
				polls[game]++
				if q := r.URL.Query(); q.Get("sec_key") != "secret-memory-key" || q.Get("time_out") != "false" || q.Get("port") != "4321" || q.Get("resubmit") != "resume-fixture" || q.Get("challenge_id") != fmt.Sprint(200+index) || q.Get("homework_common_id") != "99" {
					t.Error("incorrect polling context")
				}
				switch polls[game] {
				case 1:
					w.WriteHeader(http.StatusNotModified)
				case 2:
					fmt.Fprint(w, `{"running_code_status":1}`)
				case 3:
					fmt.Fprint(w, `{"running_code_status":2}`)
				default:
					next := "GAME2"
					json.NewEncoder(w).Encode(map[string]any{"status": 2, "had_done": 0, "test_sets_count": 1, "sets_error_count": 0, "last_compile_output": "compile successfully", "next_game": next, "test_sets": []any{map[string]any{"result": true, "compile_success": 1, "input": "1\n", "output": "YES\n", "actual_output": "YES\n"}}, "sec_key": "omit-response-key"})
				}
				return
			}
		}
		if r.URL.Path == "/api/myshixuns/MY1/update_file.json" {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			extras, _ := body["extras"].(map[string]any)
			if body["content"] != "// edited\n" || body["exercise_id"] != nil || body["homework_common_id"] != "99" || extras["homework_common_id"] != "99" || extras["currentUserId"] != float64(7) {
				t.Errorf("save body mismatch: %#v", body)
			}
			mode := int(body["evaluate"].(float64))
			if mode != 0 && mode != 1 {
				t.Error("invalid evaluation mode")
			}
			key := ""
			if mode == 1 {
				key = "secret-memory-key"
			}
			json.NewEncoder(w).Encode(map[string]any{"content": map[string]any{"commitID": "fixture-commit", "size": 10}, "resubmit": "resume-fixture", "sec_key": key, "content_modified": 0})
			return
		}
		if r.URL.Path == "/api/homework_commons/99/shixun_challenge_data.json" {
			fmt.Fprint(w, `{"status":0,"data":{"passed_count":2,"challenge_settings":[{"challenge_id":201,"challenge_name":"第一关","challenge_st":0,"passed_status":2},{"challenge_id":202,"challenge_name":"第二关","challenge_st":0,"passed_status":2}]}}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	client := New(server.URL)
	client.LoginName = "demo"
	u, _ := url.Parse(server.URL)
	client.HTTP.Jar.SetCookies(u, []*http.Cookie{{Name: "_educoder_session", Value: "test-session"}})
	ctx := context.Background()
	for i, game := range []string{"GAME1", "GAME2"} {
		task, err := client.LoadTask(ctx, "COURSE1", 99, game)
		if err != nil {
			t.Fatal(err)
		}
		question, err := client.ReadProgramming(ctx, task)
		if err != nil || question.Kind != TaskKindProgramming || len(question.Files) != 2 || question.Files[0].Extension != ".cpp" || question.Files[1].Extension != ".h" || question.Files[0].Filename != fmt.Sprintf("第%d关.cpp", i+1) || question.Files[0].Content != "// 现存代码\n" || task.GameCount != 2 {
			t.Fatalf("question=%+v err=%v", question, err)
		}
		expected := "YES\n"
		selfTest, err := client.SelfTest(ctx, task, question.Files[0].Path, "// edited\n", "1\n", SelfTestOptions{ExpectedOutput: &expected, Timeout: 3 * time.Second})
		if err != nil || !selfTest.Completed || selfTest.Output != "YES\r\n" || selfTest.Matches == nil || !*selfTest.Matches {
			t.Fatalf("self-test=%+v err=%v", selfTest, err)
		}
		if builds[game] != 0 {
			t.Fatal("self-test submitted automatically")
		}
		result, err := client.SubmitCode(ctx, task, question.Files[0].Path, "// edited\n", PollOptions{Interval: time.Millisecond, Timeout: 3 * time.Second})
		if err != nil || !result.Completed || !result.Passed || result.HadDone != 0 || builds[game] != 1 || polls[game] != 4 {
			t.Fatalf("evaluation=%+v err=%v builds=%d polls=%d", result, err, builds[game], polls[game])
		}
		data, _ := json.Marshal(result)
		if strings.Contains(string(data), "sec_key") || strings.Contains(string(data), "omit-response-key") {
			t.Fatal("evaluation secret leaked")
		}
		if i == 0 {
			for _, event := range events {
				if strings.Contains(event, "GAME2") {
					t.Fatal("client advanced into next_game automatically")
				}
			}
		}
	}
	progress, err := client.HomeworkProgress(ctx, "COURSE1", 99)
	if err != nil || progress.PassedCount != 2 || len(progress.Challenges) != 2 || progress.Challenges[1].PassedStatus != 2 {
		t.Fatalf("progress=%+v err=%v", progress, err)
	}
}

func TestProgrammingUnknownTypeCannotMutate(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()
	client := New(server.URL)
	for _, change := range []func(map[string]any){
		func(v map[string]any) { delete(v, "st") },
		func(v map[string]any) { v["st"] = 1 },
		func(v map[string]any) { delete(v["challenge"].(map[string]any), "st") },
		func(v map[string]any) { v["challenge"].(map[string]any)["with_code_file"] = false },
	} {
		fixture := programmingTaskFixture("GAME1", 1)
		change(fixture)
		task := decodeTaskFixture(t, fixture)
		if task.Kind() != TaskKindUnknown {
			t.Fatal("unknown task classified as programming")
		}
		if _, err := client.SaveCode(context.Background(), task, "src/第1关.cpp", "", false); err == nil {
			t.Fatal("unknown task allowed a mutation")
		}
	}
	if requests != 0 {
		t.Fatalf("unknown task caused %d requests", requests)
	}
}

func TestEvaluationTimeoutDoesNotRetryMutation(t *testing.T) {
	builds := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "update_file.json"):
			fmt.Fprint(w, `{"content":{"commitID":"commit"},"sec_key":"private-secret"}`)
		case strings.HasSuffix(r.URL.Path, "game_build.json"):
			builds++
			fmt.Fprint(w, `{"status":1,"res":{"success":false}}`)
		case strings.HasSuffix(r.URL.Path, "game_status.json"):
			fmt.Fprint(w, `{"running_code_status":2}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := New(server.URL)
	task := decodeTaskFixture(t, programmingTaskFixture("GAME1", 1))
	_, err := client.SubmitCode(context.Background(), task, "src/第1关.cpp", "", PollOptions{Timeout: 20 * time.Millisecond, Interval: time.Millisecond})
	if !errors.Is(err, context.DeadlineExceeded) || builds != 1 || strings.Contains(err.Error(), "private-secret") {
		t.Fatalf("timeout err=%v builds=%d", err, builds)
	}
}

func TestBuildRejectsSaveForDifferentChallengeOrMode(t *testing.T) {
	client := New("http://127.0.0.1:1")
	task := decodeTaskFixture(t, programmingTaskFixture("GAME1", 1))
	saved := &SavedCode{CommitID: "commit", SecKey: "secret", Evaluate: 0, gameIdentifier: "GAME1", myshixunID: 80, filePath: "src/第1关.cpp"}
	if _, err := client.BuildCode(context.Background(), task, saved); err == nil {
		t.Fatal("evaluate=0 save was accepted for formal submission")
	}
	saved.Evaluate = 1
	task.Game.Identifier = "GAME2"
	if _, err := client.BuildCode(context.Background(), task, saved); err == nil {
		t.Fatal("save for another game was accepted")
	}
	data, _ := json.Marshal(saved)
	if strings.Contains(string(data), "secret") || strings.Contains(string(data), "sec_key") {
		t.Fatal("save secret leaked in JSON")
	}
}

func TestSelfTestStreamBoundaries(t *testing.T) {
	for _, test := range []struct {
		name     string
		payload  string
		finish   bool
		wait     bool
		limit    int64
		expected string
		wantErr  bool
		matches  bool
	}{
		{name: "spaces-remain-significant", payload: "YES \r\n", finish: true, expected: "YES\n", matches: false},
		{name: "finish-must-be-exact", payload: "ProcessFinished\n", finish: true, expected: "ProcessFinished\n", matches: true},
		{name: "closed-before-finish", payload: "YES\n", wantErr: true},
		{name: "output-limit", payload: "12345", finish: true, limit: 4, wantErr: true},
		{name: "bounded-wait", wait: true, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var server *httptest.Server
			builds := 0
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "update_file.json"):
					fmt.Fprint(w, `{"content":{"commitID":"commit"},"sec_key":"memory-key"}`)
				case strings.HasSuffix(r.URL.Path, "game_build.json"):
					builds++
				case strings.HasSuffix(r.URL.Path, "/log_output"):
					json.NewEncoder(w).Encode(map[string]any{"status": 0, "data": map[string]any{"wss_url": strings.Replace(server.URL, "http://", "ws://", 1)}})
				case r.URL.Path == "/log":
					conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
					if err != nil {
						return
					}
					defer conn.CloseNow()
					ctx, cancel := context.WithTimeout(r.Context(), time.Second)
					defer cancel()
					if _, _, err = conn.Read(ctx); err != nil {
						return
					}
					if test.wait {
						conn.Read(ctx)
						return
					}
					if err := conn.Write(ctx, websocket.MessageBinary, []byte(base64.StdEncoding.EncodeToString([]byte(test.payload)))); err != nil {
						return
					}
					if test.finish {
						conn.Write(ctx, websocket.MessageText, []byte(base64.RawStdEncoding.EncodeToString([]byte("ProcessFinished"))))
					}
					conn.Close(websocket.StatusNormalClosure, "")
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client := New(server.URL)
			task := decodeTaskFixture(t, programmingTaskFixture("GAME1", 1))
			result, err := client.SelfTest(context.Background(), task, "src/第1关.cpp", "", "", SelfTestOptions{Timeout: 100 * time.Millisecond, MaxOutputBytes: test.limit, ExpectedOutput: &test.expected})
			if (err != nil) != test.wantErr {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if !test.wantErr && (!result.Completed || result.Matches == nil || *result.Matches != test.matches) {
				t.Fatalf("result=%+v", result)
			}
			if test.wait && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expected bounded context failure: %v", err)
			}
			if builds != 0 {
				t.Fatal("self-test attempted formal submission")
			}
		})
	}
}

func TestEvaluationRequiresObservedPassEvidence(t *testing.T) {
	for _, test := range []struct {
		name     string
		response string
		passed   bool
		wantErr  bool
	}{
		{"passed-with-had-done-zero", `{"status":2,"had_done":0,"test_sets_count":1,"sets_error_count":0,"test_sets":[{"result":true,"compile_success":1}]}`, true, false},
		{"had-done-alone-is-not-pass", `{"status":2,"had_done":1}`, false, false},
		{"compile-failure", `{"status":2,"test_sets_count":1,"sets_error_count":0,"test_sets":[{"result":true,"compile_success":0}]}`, false, false},
		{"wrong-answer", `{"status":2,"test_sets_count":1,"sets_error_count":1,"test_sets":[{"result":false,"compile_success":1}]}`, false, false},
		{"missing-test-result", `{"status":2,"test_sets_count":1,"sets_error_count":0,"test_sets":[]}`, false, false},
		{"api-error", `{"status":-1,"message":"fixture failure"}`, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, test.response) }))
			defer server.Close()
			client := New(server.URL)
			task := decodeTaskFixture(t, programmingTaskFixture("GAME1", 1))
			saved := &SavedCode{CommitID: "commit", SecKey: "secret", Evaluate: 1, gameIdentifier: "GAME1", myshixunID: 80, filePath: "src/第1关.cpp"}
			result, err := client.WaitEvaluation(context.Background(), task, saved, PollOptions{Timeout: time.Second})
			if (err != nil) != test.wantErr {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if !test.wantErr && (!result.Completed || result.Passed != test.passed) {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func TestSaveMutationIsNotRetried(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"message":"fixture save failed"}`)
	}))
	defer server.Close()
	client := New(server.URL)
	task := decodeTaskFixture(t, programmingTaskFixture("GAME1", 1))
	if _, err := client.SubmitCode(context.Background(), task, "src/第1关.cpp", "", PollOptions{}); err == nil || requests != 1 {
		t.Fatalf("save failure err=%v requests=%d", err, requests)
	}
}

type programmingRoundTripFunc func(*http.Request) (*http.Response, error)

func (f programmingRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestEvaluationTransportErrorsDoNotExposeQuerySecrets(t *testing.T) {
	client := New("https://example.invalid")
	client.HTTP.Transport = programmingRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, &url.Error{Op: "GET", URL: r.URL.String(), Err: fmt.Errorf("transport echoed URL: %s", r.URL)}
	})
	task := decodeTaskFixture(t, programmingTaskFixture("GAME1", 1))
	saved := &SavedCode{CommitID: "commit", SecKey: "query-secret-value", Evaluate: 1, gameIdentifier: "GAME1", myshixunID: 80, filePath: "src/第1关.cpp"}
	_, err := client.WaitEvaluation(context.Background(), task, saved, PollOptions{})
	if err == nil || strings.Contains(err.Error(), "query-secret-value") || strings.Contains(err.Error(), "sec_key") || strings.Contains(err.Error(), "?") {
		t.Fatalf("transport error leaked signed query: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.WaitEvaluation(ctx, task, saved, PollOptions{})
	if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "query-secret-value") {
		t.Fatalf("cancellation identity lost or secret leaked: %v", err)
	}
}

func TestPositiveLifecycleStatusesAreEndpointSpecific(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"status":1}`) }))
	defer server.Close()
	client := New(server.URL)
	if err := client.request(context.Background(), http.MethodGet, "/api/other.json", nil, nil, "/", nil); err == nil {
		t.Fatal("generic API request incorrectly accepted status=1")
	}
}

func TestExecutionErrorsDoNotEchoUpstreamCredentialMessages(t *testing.T) {
	for _, code := range []int{http.StatusOK, http.StatusBadRequest} {
		for _, endpoint := range []string{"/api/myshixuns/MY1/update_file.json", "/api/tasks/GAME1/game_build.json", "/api/tasks/GAME1/game_status.json", "/api/tasks/GAME1/log_output"} {
			t.Run(fmt.Sprintf("%d%s", code, endpoint), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(code)
					fmt.Fprint(w, `{"status":-1,"message":"sec_key=credential-canary-value; wss://runtime.invalid/?token=runtime-canary"}`)
				}))
				defer server.Close()
				client := New(server.URL)
				err := client.request(context.Background(), http.MethodPost, endpoint, nil, nil, "/", nil)
				if err == nil || strings.Contains(err.Error(), "canary") || strings.Contains(err.Error(), "sec_key") || strings.Contains(err.Error(), "wss://") || !strings.Contains(err.Error(), endpoint) {
					t.Fatalf("execution error exposed upstream message or lost endpoint: %v", err)
				}
			})
		}
	}
}
