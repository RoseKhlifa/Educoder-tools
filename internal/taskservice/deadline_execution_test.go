package taskservice

import (
	"context"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/solver"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestRunCoursesFiltersClosedAssignmentsBeforePlanning(t *testing.T) {
	var forbiddenWork atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/users/student/courses.json":
			fmt.Fprint(w, `{"count":1,"courses":[{"id":1,"name":"Course C","first_category_url":"/classrooms/C/shixun_homework"}]}`)
		case "/api/courses/C/homework_commons.json":
			fmt.Fprint(w, `{"query_total_count":2,"homeworks":[{"homework_id":44,"name":"已截止作业","status":["已截止"],"is_shixun":true,"shixun_identifier":"S44","challenge_count":1},{"homework_id":45,"name":"已过期作业","end_time":"2026-10-23 22:59:00","is_shixun":true,"shixun_identifier":"S45","challenge_count":1}]}`)
		default:
			forbiddenWork.Add(1)
			http.Error(w, "closed assignment must not execute", http.StatusInternalServerError)
		}
	}))
	defer server.Close()
	client := educoder.New(server.URL)
	client.LoginName = "student"
	result, err := New(bank(t), nil, Config{}).RunCourses(context.Background(), client, Request{Owner: "owner", Mode: solver.ModeSubmit}, nil)
	if err != nil {
		t.Fatalf("run courses: %v", err)
	}
	if result.Status != "passed" || result.ReasonCode != "no_active_homework" || result.TotalHomeworks != 0 || len(result.Homeworks) != 0 {
		t.Fatalf("closed result = %+v", result)
	}
	if forbiddenWork.Load() != 0 {
		t.Fatalf("closed assignments reached execution routes: %d", forbiddenWork.Load())
	}
}

func TestRunHomeworkRejectsClosedBeforeRuntimeEntry(t *testing.T) {
	var writesOrEntry atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/courses/C/homework_commons.json":
			fmt.Fprint(w, `{"homeworks":[{"homework_id":44,"name":"已截止作业","status":["closed"],"is_shixun":true,"shixun_identifier":"S44","challenge_count":1}]}`)
		default:
			writesOrEntry.Add(1)
			http.Error(w, "closed assignment reached runtime", http.StatusInternalServerError)
		}
	}))
	defer server.Close()
	provider := &fakeProvider{}
	result, err := New(bank(t), provider, Config{}).RunHomework(context.Background(), educoder.New(server.URL), Request{Owner: "owner", Course: "C", HomeworkID: 44, Mode: solver.ModeSubmit}, nil)
	if err != nil {
		t.Fatalf("closed homework should be a terminal result: %v", err)
	}
	if result == nil || result.Status != "deadline_passed" || result.ReasonCode != "deadline_passed" || provider.objectiveCalls.Load() != 0 || writesOrEntry.Load() != 0 {
		t.Fatalf("closed homework result=%+v model_calls=%d forbidden=%d", result, provider.objectiveCalls.Load(), writesOrEntry.Load())
	}
}

func TestDirectProgrammingRunRejectsClosedBeforeModel(t *testing.T) {
	var modelCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tasks/PROGRAM.json":
			fmt.Fprint(w, `{"st":0,"homework_common_id":44,"game":{"identifier":"PROGRAM","id":8,"myshixun_id":9},"myshixun":{"id":9},"challenge":{"id":11,"st":0,"with_code_file":true,"subject":"program","task_pass":"Return zero","path":"main.cpp"},"code_editor":{"shixun_environment_id":12,"name":"C/C++"},"test_sets":[]}`)
		case "/api/courses/C/homework_commons.json":
			fmt.Fprint(w, `{"homeworks":[{"homework_id":44,"name":"已截止作业","end_time":"2026-10-23 22:59:00","is_shixun":true,"shixun_identifier":"S44","challenge_count":1}]}`)
		default:
			modelCalls.Add(1)
			http.Error(w, "closed assignment reached mutation", http.StatusInternalServerError)
		}
	}))
	defer server.Close()
	provider := &fakeProvider{generate: func(context.Context) error { modelCalls.Add(1); return nil }}
	result, err := New(bank(t), provider, Config{}).Run(context.Background(), educoder.New(server.URL), Request{Owner: "owner", Course: "C", HomeworkID: 44, Game: "PROGRAM", Mode: solver.ModeSubmit}, nil)
	if err != nil {
		t.Fatalf("closed direct programming should be a terminal result: %v", err)
	}
	if result == nil || result.Status != "deadline_passed" || result.Kind != "programming" || modelCalls.Load() != 0 || provider.programmingCalls.Load() != 0 {
		t.Fatalf("direct closed result=%+v model_calls=%d provider_calls=%d", result, modelCalls.Load(), provider.programmingCalls.Load())
	}
}
