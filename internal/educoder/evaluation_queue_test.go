package educoder

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// queueServer serves one save, one game_build reply, a scripted get_rank
// sequence and the queue cancel endpoint, recording what the client sent.
type queueServer struct {
	build      string
	ranks      []string // get_rank bodies in order; the last one repeats
	cancelBody string   // queue_evaluate_cancel reply
	mu         sync.Mutex
	rankCalls  int
	rankQuery  []string
	cancels    int
	cancelSent map[string]any
}

func (q *queueServer) start(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q.mu.Lock()
		defer q.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/update_file.json"):
			w.Write([]byte(`{"content":{"commitID":"c1","size":4},"resubmit":"","sec_key":"k","content_modified":0}`))
		case strings.HasSuffix(r.URL.Path, "/game_build.json"):
			w.Write([]byte(q.build))
		case r.URL.Path == "/api/game_queues/get_rank.json":
			q.rankQuery = append(q.rankQuery, r.URL.Query().Get("tpi_id")+"|"+r.URL.Query().Get("homework_common_id")+"|"+r.URL.Query().Get("exercise_id"))
			i := min(q.rankCalls, len(q.ranks)-1)
			q.rankCalls++
			w.Write([]byte(q.ranks[i]))
		case r.URL.Path == "/api/game_queues/queue_evaluate_cancel.json":
			q.cancels++
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &q.cancelSent)
			w.Write([]byte(q.cancelBody))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func fastQueue(t *testing.T, timeout time.Duration) {
	t.Helper()
	interval, limit := evaluationQueueInterval, evaluationQueueTimeout
	evaluationQueueInterval, evaluationQueueTimeout = time.Millisecond, timeout
	t.Cleanup(func() { evaluationQueueInterval, evaluationQueueTimeout = interval, limit })
}

func buildWith(t *testing.T, q *queueServer, ctx context.Context) error {
	t.Helper()
	client := New(q.start(t).URL)
	task := selfTestTaskFixture()
	saved, err := client.SaveCode(context.Background(), task, "main.cpp", "code", true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.BuildCode(ctx, task, saved)
	return err
}

const queuedBuild = `{"status":1,"port":0,"res":{"code":0,"data":{"queueCode":1,"waitNum":3,"ableToCreate":0}}}`

func TestQueuedEvaluationWaitsForStartLikeTheTaskPage(t *testing.T) {
	fastQueue(t, time.Minute)
	q := &queueServer{build: queuedBuild, ranks: []string{
		`{"status":0,"data":{"status":1,"rank":3}}`,
		`{"status":0,"data":{"status":1,"rank":1}}`,
		`{"status":0,"data":{"status":2,"rank":0}}`,
	}}
	if err := buildWith(t, q, context.Background()); err != nil {
		t.Fatalf("queued evaluation that started was rejected: %v", err)
	}
	if q.rankCalls != 3 || q.cancels != 0 {
		t.Fatalf("rank calls=%d cancels=%d", q.rankCalls, q.cancels)
	}
	// tpi_id is the run instance id, as the task page sends it.
	if q.rankQuery[0] != "4|44|" {
		t.Fatalf("get_rank query = %q", q.rankQuery[0])
	}
}

func TestQueuedEvaluationTimeoutCancelsAndIsARefusal(t *testing.T) {
	fastQueue(t, 20*time.Millisecond)
	q := &queueServer{build: queuedBuild, ranks: []string{`{"status":0,"data":{"status":1,"rank":9}}`}, cancelBody: `{"status":0,"message":"ok"}`}
	err := buildWith(t, q, context.Background())
	var refused *EvaluationRefusedError
	if !errors.As(err, &refused) || refused.Kind != "queue_canceled" {
		t.Fatalf("err=%v, want a confirmed-cancel refusal", err)
	}
	if q.cancels != 1 || q.cancelSent["tpi_id"] != float64(4) {
		t.Fatalf("cancels=%d sent=%v", q.cancels, q.cancelSent)
	}
}

func TestUnconfirmedQueueCancelLeavesTheEvaluationUnknown(t *testing.T) {
	fastQueue(t, 20*time.Millisecond)
	q := &queueServer{build: queuedBuild, ranks: []string{`{"status":0,"data":{"status":1}}`}, cancelBody: `{"status":-1,"message":"queued evaluation already running"}`}
	err := buildWith(t, q, context.Background())
	var uncertain *EvaluationUncertainError
	var refused *EvaluationRefusedError
	if !errors.As(err, &uncertain) || errors.As(err, &refused) {
		t.Fatalf("err=%v, want unknown when the cancel is not confirmed", err)
	}
	if strings.Contains(err.Error(), "already running") {
		t.Fatalf("platform text echoed: %q", err.Error())
	}
}

func TestQueueEndingInAnUninterpretedStateIsObservedNotAssumed(t *testing.T) {
	fastQueue(t, time.Minute)
	q := &queueServer{build: queuedBuild, ranks: []string{`{"status":0,"data":{"status":1}}`, `{"status":-1,"message":"not queued"}`}}
	if err := buildWith(t, q, context.Background()); err != nil {
		t.Fatalf("err=%v; an ended queue must be observed through game_status, not assumed refused", err)
	}
	if q.cancels != 0 {
		t.Fatal("an ended queue was canceled")
	}
}

func TestCanceledJobStillWithdrawsItsQueuedEvaluation(t *testing.T) {
	fastQueue(t, time.Minute)
	q := &queueServer{build: queuedBuild, ranks: []string{`{"status":0,"data":{"status":1}}`}, cancelBody: `{"status":0}`}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			q.mu.Lock()
			n := q.rankCalls
			q.mu.Unlock()
			if n >= 2 {
				cancel()
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	err := buildWith(t, q, ctx)
	var refused *EvaluationRefusedError
	if !errors.As(err, &refused) || q.cancels != 1 {
		t.Fatalf("err=%v cancels=%d; a canceled job must withdraw its queued evaluation", err, q.cancels)
	}
}

func TestEnvironmentQuotaIsARefusalBeforeAnyStatusCheck(t *testing.T) {
	for name, build := range map[string]string{
		"status 1":  `{"status":1,"data":{"identifier":"g","data_list":[{"name":"other lab"}]}}`,
		"status -1": `{"status":-1,"data":{"data_list":[{"name":"other lab"}]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			q := &queueServer{build: build, ranks: []string{`{}`}}
			err := buildWith(t, q, context.Background())
			var refused *EvaluationRefusedError
			if !errors.As(err, &refused) || refused.Kind != "environment_quota" || q.rankCalls != 0 {
				t.Fatalf("err=%v rank calls=%d", err, q.rankCalls)
			}
		})
	}
}

func TestBuildStatusOutcomesKeepTheirMeaning(t *testing.T) {
	q := &queueServer{build: `{"status":1,"port":7,"res":{"data":{"queueCode":0}}}`, ranks: []string{`{}`}}
	if err := buildWith(t, q, context.Background()); err != nil || q.rankCalls != 0 {
		t.Fatalf("plain start: err=%v rank calls=%d", err, q.rankCalls)
	}
	q = &queueServer{build: `{"status":0,"message":"x"}`, ranks: []string{`{}`}}
	var status *APIStatusError
	if err := buildWith(t, q, context.Background()); !errors.As(err, &status) || status.Code != 0 {
		t.Fatalf("unexpected status: err=%v", err)
	}
	q = &queueServer{build: `{"port":7}`, ranks: []string{`{}`}}
	if err := buildWith(t, q, context.Background()); err == nil || !strings.Contains(err.Error(), "启动状态") {
		t.Fatalf("missing status: err=%v", err)
	}
	// An unusual data/res shape must not turn an accepted start into a failure.
	q = &queueServer{build: `{"status":1,"data":"text","res":[1]}`, ranks: []string{`{}`}}
	if err := buildWith(t, q, context.Background()); err != nil {
		t.Fatalf("lenient shapes: err=%v", err)
	}
}
