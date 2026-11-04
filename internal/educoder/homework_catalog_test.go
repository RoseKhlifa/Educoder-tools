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

func TestHomeworksForDisplayMergesPaginatedTabsWithStableDeduplication(t *testing.T) {
	var calls []string
	pages := map[string]string{
		"4/1": `{"query_total_count":3,"homeworks":[{"homework_id":4101,"name":"first shixun","is_shixun":true,"type":4,"homework_kind":"common"},{"homework_id":4101,"name":"duplicate in page"},{"homework_id":4102,"name":"second shixun","is_shixun":true,"shixun_identifier":"SHIXUN2","type":"4"}]}`,
		"4/2": `{"homeworks":[{"homework_id":4102,"name":"duplicate across pages"},{"homework_id":4103,"name":"third shixun","is_shixun":true}]}`,
		"1/1": `{"query_total_count":3,"homeworks":[{"homework_id":3968749,"name":"text and image homework","status":["提交中"],"type":1,"end_time":"2099-12-31 23:59:59","work_status":["修改作品"],"un_commit_work":false,"work_id":9001,"student_work_id":9001,"homework_kind":"shixun"},{"homework_id":4102,"name":"duplicate across tabs","is_shixun":false,"type":1}]}`,
		"1/2": `{"homeworks":[{"homework_id":4102,"name":"second common duplicate"},{"homework_id":3968750,"name":"second common","is_shixun":true,"type":"1"}]}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/courses/vm6u9z27/homework_commons.json" {
			t.Errorf("unexpected catalog request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		if q.Get("id") != "vm6u9z27" || q.Get("category") != "UNIT1" || q.Get("status") != "0" || q.Get("order") != "0" || q.Get("limit") != "20" {
			t.Errorf("catalog query lost its filters: %v", q)
		}
		key := q.Get("type") + "/" + q.Get("page")
		calls = append(calls, key)
		body, ok := pages[key]
		if !ok {
			t.Errorf("unexpected catalog page: %s", key)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	rows, err := New(server.URL).HomeworksForDisplay(context.Background(), "vm6u9z27", "UNIT1")
	if err != nil {
		t.Fatal(err)
	}
	ids, kinds := []int64{}, []string{}
	for _, row := range rows {
		ids, kinds = append(ids, row.ID), append(kinds, row.Kind)
	}
	if !reflect.DeepEqual(ids, []int64{4101, 4102, 4103, 3968749, 3968750}) || !reflect.DeepEqual(kinds, []string{"shixun", "shixun", "shixun", "common", "common"}) {
		t.Fatalf("merged catalog has unstable order or classification: ids=%v kinds=%v", ids, kinds)
	}
	if !reflect.DeepEqual(calls, []string{"4/1", "4/2", "1/1", "1/2"}) {
		t.Fatalf("catalog did not exhaust each tab once: %v", calls)
	}
	if rows[1].Name != "second shixun" || rows[1].ShixunIdentifier != "SHIXUN2" || string(rows[1].Type) != `"4"` {
		t.Fatalf("duplicate overwrote the original shixun row: %+v", rows[1])
	}
	common := rows[3]
	if common.IsShixun || common.ShixunIdentifier != "" || common.Kind != "common" || string(common.Type) != "1" || string(common.WorkStatus) != `["修改作品"]` || common.StudentWorkID != 9001 || common.WorkID != 9001 || common.CommonSubmissionStatus() != "submitted" || common.IsClosed(time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpired text/image fixture was lost or classified from a missing shixun flag: %+v", common)
	}
	if !rows[4].IsShixun || rows[4].Kind != "common" || string(rows[4].Type) != `"1"` {
		t.Fatalf("common classification did not come from the requested tab: %+v", rows[4])
	}
}

func TestHomeworksExecutionCatalogKeepsType4AndTabClassification(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/courses/C/homework_commons.json" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		q := r.URL.Query()
		calls = append(calls, q.Get("type"))
		if _, present := q["category"]; present {
			t.Errorf("empty category was added: %v", q)
		}
		_, _ = w.Write([]byte(`{"query_total_count":1,"homeworks":[{"homework_id":1,"is_shixun":false,"homework_kind":"common"}]}`))
	}))
	defer server.Close()
	client := New(server.URL)

	rows, err := client.Homeworks(context.Background(), "C", "")
	if err != nil || len(rows) != 1 || rows[0].Kind != "shixun" || rows[0].IsShixun || !reflect.DeepEqual(calls, []string{"4"}) {
		t.Fatalf("execution catalog changed tab or inferred kind from is_shixun: rows=%+v calls=%v err=%v", rows, calls, err)
	}
	rows, err = client.HomeworksByType(context.Background(), "C", "", "")
	if err != nil || len(rows) != 1 || rows[0].Kind != "" || !reflect.DeepEqual(calls, []string{"4", ""}) {
		t.Fatalf("all-tabs read invented a classification: rows=%+v calls=%v err=%v", rows, calls, err)
	}
	encoded, err := json.Marshal(rows[0])
	if err != nil || strings.Contains(string(encoded), `"type":`) || strings.Contains(string(encoded), `"homework_kind":`) {
		t.Fatalf("optional raw type or unclassified kind was emitted: %s err=%v", encoded, err)
	}
}

func TestHomeworksForDisplayReturnsNoPartialCatalogOnTabFailure(t *testing.T) {
	for _, fail := range []string{"4/1", "1/1", "1/2"} {
		t.Run(fail, func(t *testing.T) {
			var calls []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				key := q.Get("type") + "/" + q.Get("page")
				calls = append(calls, key)
				if key == fail {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				switch key {
				case "4/1":
					_, _ = w.Write([]byte(`{"query_total_count":1,"homeworks":[{"homework_id":1}]}`))
				case "1/1":
					_, _ = w.Write([]byte(`{"query_total_count":2,"homeworks":[{"homework_id":3968749}]}`))
				default:
					t.Errorf("request followed a failed catalog: %s", key)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			rows, err := New(server.URL).HomeworksForDisplay(context.Background(), "vm6u9z27", "")
			var readError *MetadataReadError
			if rows != nil || !errors.As(err, &readError) || readError.HTTPStatus != http.StatusForbidden || readError.Attempts != 1 {
				t.Fatalf("tab failure became a partial or empty success: rows=%+v calls=%v err=%v", rows, calls, err)
			}
			if calls[len(calls)-1] != fail {
				t.Fatalf("catalog continued after failure: %v", calls)
			}
		})
	}
}

func TestHomeworkCatalogRejectsIncompleteAndStalledPagination(t *testing.T) {
	cases := []struct {
		name, first, second string
	}{
		{"empty before retained total", `{"query_total_count":2,"homeworks":[{"homework_id":3968749}]}`, `{"homeworks":[]}`},
		{"repeat before retained total", `{"query_total_count":2,"homeworks":[{"homework_id":3968749}]}`, `{"homeworks":[{"homework_id":3968749}]}`},
		{"repeat without advertised total", `{"homeworks":[{"homework_id":3968749}]}`, `{"homeworks":[{"homework_id":3968749}]}`},
		{"missing list", `{"query_total_count":2,"homeworks":[{"homework_id":3968749}]}`, `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					_, _ = w.Write([]byte(tc.first))
				} else {
					_, _ = w.Write([]byte(tc.second))
				}
			}))
			defer server.Close()
			rows, err := New(server.URL).HomeworksByType(context.Background(), "vm6u9z27", "", "1")
			var readError *MetadataReadError
			if rows != nil || !errors.As(err, &readError) || readError.Category != "schema" || readError.Attempts != 1 || calls != 2 {
				t.Fatalf("incomplete pagination became success: rows=%+v calls=%d err=%v", rows, calls, err)
			}
		})
	}
}

func TestHomeworkCatalogWithoutTotalExhaustsAtEmptyPage(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			_, _ = w.Write([]byte(`{"homeworks":[{"homework_id":3968749},{"homework_id":3968749}]}`))
		} else {
			_, _ = w.Write([]byte(`{"homeworks":[]}`))
		}
	}))
	defer server.Close()
	rows, err := New(server.URL).HomeworksByType(context.Background(), "vm6u9z27", "", "1")
	if err != nil || len(rows) != 1 || rows[0].Kind != "common" || calls != 2 {
		t.Fatalf("unknown total did not exhaust safely: rows=%+v calls=%d err=%v", rows, calls, err)
	}
}

func TestCommonSubmissionStatusRequiresExplicitPersonalFlag(t *testing.T) {
	for _, tc := range []struct {
		name, raw, kind, want string
	}{
		{"missing", `{}`, "common", "unknown"},
		{"null", `{"un_commit_work":null}`, "common", "unknown"},
		{"submitted without work ids", `{"un_commit_work":false}`, "common", "submitted"},
		{"unsubmitted without work ids", `{"un_commit_work":true}`, "common", "not_submitted"},
		{"positive work ids alone", `{"student_work_id":304751696,"work_id":304751696}`, "common", "unknown"},
		{"generic submitted status alone", `{"status":["已提交"],"work_status":["修改作品"],"student_work_id":304751696,"work_id":304751696}`, "common", "unknown"},
		{"unsubmitted despite ids and action", `{"un_commit_work":true,"status":["提交中"],"work_status":["修改作品"],"student_work_id":304751696,"work_id":304751696}`, "common", "not_submitted"},
		{"submitted despite empty actions", `{"un_commit_work":false,"status":["提交中"],"work_status":[]}`, "common", "submitted"},
		{"shixun flag is not common proof", `{"un_commit_work":false}`, "shixun", "unknown"},
		{"unclassified flag is not common proof", `{"un_commit_work":false}`, "", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var homework Homework
			if err := json.Unmarshal([]byte(tc.raw), &homework); err != nil {
				t.Fatal(err)
			}
			homework.Kind = tc.kind
			if got := homework.CommonSubmissionStatus(); got != tc.want {
				t.Fatalf("submission status=%q, want %q for %+v", got, tc.want, homework)
			}
		})
	}
}

func TestHomeworkCatalogRejectsInvalidPersonalSubmissionFlag(t *testing.T) {
	for _, flag := range []string{`"false"`, `0`, `[]`, `{}`} {
		t.Run(flag, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"query_total_count":1,"homeworks":[{"homework_id":3968749,"un_commit_work":` + flag + `}]}`))
			}))
			defer server.Close()
			rows, err := New(server.URL).HomeworksByType(context.Background(), "vm6u9z27", "", "1")
			var readError *MetadataReadError
			if rows != nil || !errors.As(err, &readError) || readError.Category != "schema" || readError.Attempts != 1 {
				t.Fatalf("invalid submission flag became a catalog row: rows=%+v err=%v", rows, err)
			}
		})
	}
}
