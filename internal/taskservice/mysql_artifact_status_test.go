package taskservice

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/solver"
)

func TestMySQLFailureStatusIsConservative(t *testing.T) {
	cases := []struct {
		name  string
		stage *educoder.MySQLArtifactStage
		want  string
	}{
		{"lost execution reply", &educoder.MySQLArtifactStage{ExecutionAttempted: true}, "uncertain"},
		{"lost preparation reply", &educoder.MySQLArtifactStage{ExecutionAttempted: true, ExecutionConfirmed: true, PreparationAttempted: true}, "uncertain"},
		{"lost build reply", &educoder.MySQLArtifactStage{ExecutionAttempted: true, ExecutionConfirmed: true, PreparationAttempted: true, PreparationConfirmed: true, BuildAttempted: true}, "uncertain"},
		{"confirmed preflight failure", &educoder.MySQLArtifactStage{ExecutionAttempted: true, ExecutionConfirmed: true, PreparationAttempted: false}, "error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mysqlFailureStatus(&educoder.MySQLArtifactResult{Stage: tc.stage}, errors.New("lost reply"))
			if got != tc.want {
				t.Fatalf("status=%q want %q", got, tc.want)
			}
		})
	}
}

func TestDirectMySQLRunRejectsClosedHomeworkBeforeMutation(t *testing.T) {
	var forbidden int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tasks/MYSQL.json":
			_, _ = w.Write([]byte(mysqlTaskJSON))
		case "/api/courses/C/homework_commons.json":
			_, _ = w.Write([]byte(`{"homeworks":[{"homework_id":44,"name":"closed","status":["closed"],"is_shixun":true,"shixun_identifier":"S"}]}`))
		default:
			forbidden++
			http.Error(w, "closed MySQL homework reached mutation", http.StatusInternalServerError)
		}
	}))
	defer server.Close()
	client := educoder.New(server.URL)
	result, err := New(bank(t), nil, Config{}).Run(context.Background(), client, Request{Owner: "owner", Course: "C", HomeworkID: 44, Game: "MYSQL", Mode: solver.ModeSubmit}, nil)
	if err != nil {
		t.Fatalf("closed MySQL homework returned error: %v", err)
	}
	if result == nil || result.Status != "deadline_passed" || result.Kind != string(educoder.TaskKindMySQLArtifact) || forbidden != 0 {
		t.Fatalf("result=%+v forbidden=%d", result, forbidden)
	}
}

const mysqlTaskJSON = `{"course_identifier":"C","homework_common_id":44,"game":{"identifier":"MYSQL","id":8,"myshixun_id":9,"status":0},"myshixun":{"id":9,"identifier":"MS"},"user":{"user_id":10},"shixun":{"id":20,"identifier":"S","name":"Public schema experiment"},"challenge":{"id":11,"st":0,"with_code_file":false,"subject":"Create customer table","position":1,"task_pass":"Use MySQL to create the table, run desc and save txt output. mysql -uroot -p123123 -h127.0.0.1\n答案保存为：/data/workspace/myshixun/answer/step1/result.txt"},"shixun_environments":[{"shixun_environment_id":23,"tab_type":3,"vnc_url":"https://runtime.educoder.net/desktop"}],"vnc_url":"https://runtime.educoder.net/desktop"}`
