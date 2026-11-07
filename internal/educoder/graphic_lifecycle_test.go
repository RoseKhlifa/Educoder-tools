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

type lifecycleGraphicSession struct {
	fakeGraphicSession
	reads, closes             int
	readFailure               int
	saveFailure, closeFailure bool
}

func TestGraphicQuotaDataListRequiresNonEmptyArray(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{`null`, false}, {`[]`, false}, {`{}`, false}, {`[{}]`, true}, {`"busy"`, false},
	} {
		if got := graphicQuotaDataList(json.RawMessage(tc.raw)); got != tc.want {
			t.Fatalf("graphicQuotaDataList(%s)=%v want %v", tc.raw, got, tc.want)
		}
	}
}

func (s *lifecycleGraphicSession) ReadFile(ctx context.Context, p string) ([]byte, bool, error) {
	s.reads++
	if s.reads == s.readFailure {
		return nil, false, errors.New("private SSH error")
	}
	return s.fakeGraphicSession.ReadFile(ctx, p)
}
func (s *lifecycleGraphicSession) SaveAndCommit(ctx context.Context, p, h string, e bool, b []byte) (string, error) {
	if s.saveFailure {
		return "", errors.New("private save error")
	}
	return s.fakeGraphicSession.SaveAndCommit(ctx, p, h, e, b)
}
func (s *lifecycleGraphicSession) Close() error {
	s.closes++
	if s.closeFailure {
		return errors.New("private close error")
	}
	return nil
}

type lifecycleGraphicTransport struct {
	session *lifecycleGraphicSession
	fail    bool
	opens   int
}

func (f *lifecycleGraphicTransport) Open(context.Context, GraphicConnection) (GraphicSession, error) {
	f.opens++
	if f.fail {
		return nil, errors.New("private transport error")
	}
	return f.session, nil
}

func TestGraphicSingleLeaseCleansEveryAcquiredMutationBoundary(t *testing.T) {
	for _, phase := range []string{"pass", "read failure", "save failure", "prepare failure", "build unknown", "failed verdict", "postpass read failure", "close failure", "release failure", "preexisting", "ownership unknown"} {
		t.Run(phase, func(t *testing.T) {
			task := graphicTaskFixture(t)
			if phase == "preexisting" {
				started := true
				task.Environments[0].IsStart = &started
			}
			if phase == "ownership unknown" {
				task.Environments[0].IsStart = nil
			}
			session := &lifecycleGraphicSession{}
			if phase == "read failure" {
				session.readFailure = 1
			}
			if phase == "save failure" {
				session.saveFailure = true
			}
			if phase == "postpass read failure" {
				session.readFailure = 3
			}
			if phase == "close failure" {
				session.closeFailure = true
			}
			starts, releases, builds, prepares := 0, 0, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/GRAPHIC1.json"):
					clone := *task
					if builds > 0 {
						clone.Game.Status = 2
					}
					_ = json.NewEncoder(w).Encode(clone)
				case strings.HasSuffix(r.URL.Path, "/start.json"):
					starts++
					ioJSON(w, `{"status":0,"data":{"ssh_address":"fixture","port":22,"username":"fixture","password":"private","myshixun_id":80}}`)
				case strings.HasSuffix(r.URL.Path, "/exit_delete_pod.json"):
					releases++
					if phase == "release failure" {
						w.WriteHeader(502)
					} else {
						ioJSON(w, `{"status":0}`)
					}
				case strings.HasSuffix(r.URL.Path, "/update_file.json"):
					prepares++
					if phase == "prepare failure" {
						w.WriteHeader(502)
					} else {
						ioJSON(w, `{"sec_key":"fixture","resubmit":"fixture"}`)
					}
				case strings.HasSuffix(r.URL.Path, "/game_build.json"):
					builds++
					if phase == "build unknown" {
						w.WriteHeader(502)
					} else {
						ioJSON(w, `{"status":1,"port":1}`)
					}
				case strings.HasSuffix(r.URL.Path, "/game_status.json"):
					if builds == 0 {
						ioJSON(w, `{"status":0}`)
					} else if phase == "failed verdict" {
						ioJSON(w, `{"status":0,"test_sets_count":1,"sets_error_count":1,"test_sets":[{"result":false,"compile_success":1}]}`)
					} else {
						ioJSON(w, `{"status":2,"test_sets_count":1,"sets_error_count":0,"test_sets":[{"result":true,"compile_success":1}]}`)
					}
				default:
					t.Errorf("unexpected endpoint %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			protocol := NewGraphicProtocol(New(server.URL), &lifecycleGraphicTransport{session: session})
			doc, err := protocol.Describe(task)
			if err != nil {
				t.Fatal(err)
			}
			if starts != 0 || session.reads != 0 {
				t.Fatal("metadata load opened or read a runtime")
			}
			result, err := protocol.Submit(context.Background(), task, doc, graphicProject, PollOptions{Interval: time.Millisecond, Timeout: time.Second})
			wantRelease := 1
			if phase == "preexisting" || phase == "ownership unknown" {
				wantRelease = 0
			}
			if starts != 1 || releases != wantRelease || session.closes != 1 || builds > 1 || prepares > 1 {
				t.Fatalf("lifecycle starts=%d releases=%d close=%d builds=%d prepares=%d", starts, releases, session.closes, builds, prepares)
			}
			if result == nil {
				t.Fatal("mutation boundary lost")
			}
			switch phase {
			case "read failure":
				if err == nil || result.Stage != GraphicStagePrewrite || builds != 0 {
					t.Fatalf("prewrite %#v %v", result, err)
				}
			case "save failure":
				if err == nil || result.Stage != GraphicStageSaveUnconfirmed || builds != 0 {
					t.Fatalf("save boundary %#v %v", result, err)
				}
			case "prepare failure":
				var boundary *GraphicMutationError
				if !errors.As(err, &boundary) || boundary.Stage != GraphicStageSavedNotSubmitted || !result.Saved || result.SubmissionAttempted || builds != 0 {
					t.Fatalf("saved not submitted %#v %v", result, err)
				}
			case "build unknown":
				var boundary *GraphicMutationError
				if !errors.As(err, &boundary) || boundary.Stage != GraphicStageBuildSent || !result.SubmissionAttempted || builds != 1 {
					t.Fatalf("build boundary %#v %v", result, err)
				}
			case "failed verdict":
				if err != nil || result.Evaluation == nil || !result.Evaluation.Completed || result.Evaluation.Passed {
					t.Fatalf("failure %#v %v", result, err)
				}
			default:
				if err != nil || !result.Confirmed || !result.Evaluation.Passed {
					t.Fatalf("known pass overridden: %#v %v", result, err)
				}
				if (phase == "close failure" || phase == "release failure") && result.CleanupError == "" {
					t.Fatal("cleanup diagnostic missing")
				}
			}
			if err != nil && strings.Contains(err.Error(), "private") {
				t.Fatal("transport detail leaked")
			}
		})
	}
}

func TestGraphicSSHFailureReleasesOwnedAcceptedStart(t *testing.T) {
	task := graphicTaskFixture(t)
	starts, releases := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/start.json") {
			starts++
			ioJSON(w, `{"status":0,"data":{"ssh_address":"fixture","port":22,"username":"fixture","password":"private","myshixun_id":80}}`)
		} else if strings.HasSuffix(r.URL.Path, "/exit_delete_pod.json") {
			releases++
			ioJSON(w, `{"status":0}`)
		} else {
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	protocol := NewGraphicProtocol(New(server.URL), &lifecycleGraphicTransport{fail: true})
	var waits []time.Duration
	protocol.retryWait = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
	if _, err := protocol.Read(context.Background(), task); err == nil {
		t.Fatal("SSH failure accepted")
	}
	if starts != 6 || releases != 1 || !reflect.DeepEqual(waits, graphicReadinessDelays) {
		t.Fatalf("starts=%d releases=%d waits=%v", starts, releases, waits)
	}
}

func TestGraphicResourceCooldownIsBoundedAndSanitized(t *testing.T) {
	task := graphicTaskFixture(t)
	starts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		starts++
		ioJSON(w, `{"status":-1,"message":"private runtime credential"}`)
	}))
	defer server.Close()
	protocol := NewGraphicProtocol(New(server.URL), &fakeGraphicTransport{})
	var waits []time.Duration
	protocol.retryWait = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
	_, err := protocol.Read(context.Background(), task)
	var resource *GraphicRuntimeResourceError
	if !errors.As(err, &resource) || starts != 4 || !reflect.DeepEqual(waits, []time.Duration{15 * time.Second, 45 * time.Second, 120 * time.Second}) || strings.Contains(err.Error(), "private") {
		t.Fatalf("starts=%d waits=%v error=%v", starts, waits, err)
	}
}

func TestGraphicQuotaVariantsDoNotStartOrDeleteOtherPods(t *testing.T) {
	for _, body := range []string{`{"status":-3}`, `{"status":-5}`, `{"status":0,"data":{"code":-3}}`, `{"status":0,"data":{"data_list":[{"id":"private"}]}}`} {
		t.Run(body, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if !strings.HasSuffix(r.URL.Path, "/start.json") {
					t.Error("quota caused deletion")
				}
				ioJSON(w, body)
			}))
			defer server.Close()
			_, err := NewGraphicProtocol(New(server.URL), &fakeGraphicTransport{}).Read(context.Background(), graphicTaskFixture(t))
			var quota *GraphicEnvironmentQuotaError
			if !errors.As(err, &quota) || requests != 1 || strings.Contains(err.Error(), "private") {
				t.Fatalf("quota=%v requests=%d", err, requests)
			}
		})
	}
}

func TestGraphicAccountLeaseSerializesSeparateClientsAndCancellation(t *testing.T) {
	task := graphicTaskFixture(t)
	a := NewGraphicProtocol(New("http://same-upstream"), nil)
	b := NewGraphicProtocol(New("http://same-upstream/"), nil)
	unlock, err := a.lockAccount(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if second, err := b.lockAccount(ctx, task); !errors.Is(err, context.DeadlineExceeded) {
		if second != nil {
			second()
		}
		t.Fatalf("same account lease overlapped: %v", err)
	}
	other := *task
	other.User.UserID++
	releaseOther, err := b.lockAccount(context.Background(), &other)
	if err != nil {
		t.Fatal(err)
	}
	releaseOther()
	unlock()
	release, err := b.lockAccount(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	release()
	graphicAccountLocks.Lock()
	left := len(graphicAccountLocks.entries)
	graphicAccountLocks.Unlock()
	if left != 0 {
		t.Fatalf("leaked account lock entries=%d", left)
	}
}

func TestGraphicUnicodeNameAndAmbiguity(t *testing.T) {
	task := graphicTaskFixture(t)
	task.Challenge.Description = "保存在“workspace/myshixun/用例图/提交”目录下，命名为学生选课图，格式为mdj。"
	if got := task.GraphicPaths(); !reflect.DeepEqual(got, []string{"用例图/提交/学生选课图.mdj"}) {
		t.Fatalf("unicode target lost %v", got)
	}
	task.Challenge.Description += " 另一份命名为教师选课图。"
	if got := task.GraphicPaths(); len(got) != 0 {
		t.Fatalf("ambiguous names guessed %v", got)
	}
}
