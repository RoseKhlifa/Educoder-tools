package educoder

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const mysqlDescribeFixture = `+-------+--------------+------+-----+---------+-------+
| Field | Type         | Null | Key | Default | Extra |
+-------+--------------+------+-----+---------+-------+
| id    | int(11)      | NO   | PRI | NULL    |       |
| name  | varchar(255) | YES  |     |         |       |
+-------+--------------+------+-----+---------+-------+
`

func mysqlTaskFixture(t *testing.T) *Task {
	task := graphicTaskFixture(t)
	task.Shixun.Name = "Public schema experiment"
	task.Challenge.Subject = "Create customer table"
	task.Challenge.Position = 1
	task.Challenge.Description = "Use MySQL to create the table, run desc and save txt output.\n```\nmysql -uroot -psample123 -h127.0.0.1\n```\n答案保存为：/data/workspace/myshixun/answer/step1/result.txt"
	return task
}
func mysqlReferenceFixture() *MySQLArtifactReference {
	return &MySQLArtifactReference{Experiment: "Public schema experiment", Title: "Create customer table", Position: 1, Path: "answer/step1/result.txt", Content: mysqlDescribeFixture, Source: "external:fixture@0123456789abcdef:1"}
}

func TestMySQLArtifactReferenceIsTypedAndExactlyBound(t *testing.T) {
	task := mysqlTaskFixture(t)
	ref := mysqlReferenceFixture()
	plan, err := BuildMySQLArtifactPlan(task, *ref)
	if err != nil {
		t.Fatal(err)
	}
	if !task.IsMySQLArtifactTask() || plan.Path != ref.Path || !strings.Contains(plan.CanonicalSQL(), "CREATE TABLE `educoder_reference`") {
		t.Fatalf("unbound plan %+v", plan)
	}
	serialized, _ := json.Marshal(plan)
	if strings.Contains(string(serialized), "sample123") || strings.Contains(string(serialized), "MYSQL_PWD") {
		t.Fatal("public runtime password persisted")
	}
	for _, tc := range []struct {
		name   string
		change func(*MySQLArtifactReference)
	}{
		{"experiment", func(r *MySQLArtifactReference) { r.Experiment = "Another experiment" }},
		{"title", func(r *MySQLArtifactReference) { r.Title = "Other task" }},
		{"path", func(r *MySQLArtifactReference) { r.Path = "answer/step2/result.txt" }},
		{"position", func(r *MySQLArtifactReference) { r.Position = 2 }},
		{"nonunique alias", func(r *MySQLArtifactReference) { r.Content = strings.Replace(r.Content, "| name  |", "| ID    |", 1) }},
		{"SQL injection type", func(r *MySQLArtifactReference) {
			r.Content = strings.Replace(r.Content, "varchar(255)", "int); DROP x", 1)
		}},
		{"unknown type", func(r *MySQLArtifactReference) {
			r.Content = strings.Replace(r.Content, "varchar(255)", "enum('yes')", 1)
		}},
		{"trailing instruction", func(r *MySQLArtifactReference) { r.Content += "DROP DATABASE test;" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := *ref
			tc.change(&r)
			if _, err := BuildMySQLArtifactPlan(task, r); err == nil {
				t.Fatal("unparsed or unrelated reference accepted")
			}
		})
	}
	changed := *task
	changed.Challenge = task.Challenge
	changed.Challenge.Description += "\nChanged instructions"
	if plan.validate(&changed) == nil {
		t.Fatal("plan reused across changed public task")
	}
	plan.Path = "answer/step2/result.txt"
	if plan.validate(task) == nil {
		t.Fatal("modified plan accepted")
	}
}

func TestMySQLArtifactTargetAndShellScope(t *testing.T) {
	for _, bad := range []string{"../../res.txt", "/etc/passwd.txt", "answer/../res.txt", ".git/config.txt", "pingce/result.txt", "secret/result.txt", "answer/a\n.txt", "answer/res.txt;touch x"} {
		if _, err := normalizeMySQLArtifactPath(bad); err == nil {
			t.Errorf("unsafe target %q accepted", bad)
		}
	}
	task := mysqlTaskFixture(t)
	plan, err := BuildMySQLArtifactPlan(task, *mysqlReferenceFixture())
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := mysqlArtifactCommand(plan, "root", "quoted'fixture", "educoder_schema_0123456789abcdef0123456789abcdef", "answer/step1/generated.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"CREATE DATABASE `educoder_schema_0123456789abcdef0123456789abcdef`", "DESCRIBE `educoder_reference`", "--table", "--protocol=TCP", "--host=127.0.0.1", "set -C", "'quoted'\"'\"'fixture'"} {
		if !strings.Contains(cmd, required) {
			t.Errorf("command missing %s", required)
		}
	}
	for _, forbidden := range []string{"DROP DATABASE", "DROP TABLE", "IF NOT EXISTS", "Field |", "tee ", "pingce", "mysql -p"} {
		if strings.Contains(cmd, forbidden) {
			t.Errorf("unsafe or fabricated command %s", forbidden)
		}
	}
	if _, err := mysqlArtifactCommand(plan, "root", "x", "existing_user_db", "answer/generated.txt"); err == nil {
		t.Fatal("unscoped database accepted")
	}
	task.Challenge.Description += "\n/data/workspace/myshixun/other/result.txt"
	if task.IsMySQLArtifactTask() {
		t.Fatal("ambiguous result target accepted")
	}
}

type mysqlFakeSession struct {
	fakeGraphicSession
	execs   int
	execErr error
	result  string
}

func (s *mysqlFakeSession) ReadMySQLArtifact(context.Context, string) ([]byte, bool, error) {
	return []byte(s.result), s.result != "", nil
}
func (s *mysqlFakeSession) ExecuteMySQLArtifact(_ context.Context, p *MySQLArtifactPlan, u, password string) (string, error) {
	s.execs++
	if u != "root" || password != "sample123" {
		return "", errors.New("wrong public login")
	}
	if s.execErr != nil {
		return "", s.execErr
	}
	s.result = mysqlDescribeFixture
	return graphicCommitFixture, nil
}

type mysqlFakeTransport struct {
	s     *mysqlFakeSession
	opens int
}

func (f *mysqlFakeTransport) Open(context.Context, GraphicConnection) (GraphicSession, error) {
	f.opens++
	return f.s, nil
}

func TestMySQLArtifactBuildHasRealExecutionAndFreshVerdict(t *testing.T) {
	for _, phase := range []string{"pass", "execution uncertain", "prepare uncertain", "build uncertain", "cached verdict"} {
		t.Run(phase, func(t *testing.T) {
			task := mysqlTaskFixture(t)
			plan, err := BuildMySQLArtifactPlan(task, *mysqlReferenceFixture())
			if err != nil {
				t.Fatal(err)
			}
			session := &mysqlFakeSession{}
			if phase == "execution uncertain" {
				session.execErr = errors.New("lost SSH reply")
			}
			transport := &mysqlFakeTransport{s: session}
			prepares, builds, polls, reads := 0, 0, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasSuffix(r.URL.Path, "/GRAPHIC1.json"):
					reads++
					clone := *task
					if builds > 0 {
						clone.Game.Status = 2
					}
					_ = json.NewEncoder(w).Encode(clone)
				case strings.HasSuffix(r.URL.Path, "/start.json"):
					if r.URL.Query().Get("tab_type") != "3" {
						t.Error("MySQL used an invented environment")
					}
					ioJSON(w, `{"status":0,"data":{"ssh_address":"ssh.educoder.net","port":2222,"username":"headless","password":"ephemeral","myshixun_id":80}}`)
				case strings.HasSuffix(r.URL.Path, "/update_file.json"):
					prepares++
					var body map[string]any
					_ = json.NewDecoder(r.Body).Decode(&body)
					if _, ok := body["content"]; ok {
						t.Error("reference descriptor copied through editor")
					}
					if _, ok := body["path"]; ok {
						t.Error("placeholder editor path overwritten")
					}
					if phase == "prepare uncertain" {
						http.Error(w, "lost reply", http.StatusBadGateway)
						return
					}
					ioJSON(w, `{"sec_key":"public-fixture-key","resubmit":"r1","content_modified":1}`)
				case strings.HasSuffix(r.URL.Path, "/game_build.json"):
					builds++
					if phase == "build uncertain" {
						http.Error(w, "lost reply", http.StatusBadGateway)
						return
					}
					ioJSON(w, `{"status":1,"port":55}`)
				case strings.HasSuffix(r.URL.Path, "/exit_delete_pod.json"):
					ioJSON(w, `{"status":0,"message":"success"}`)
				case strings.HasSuffix(r.URL.Path, "/game_status.json"):
					if builds == 0 || phase == "cached verdict" {
						ioJSON(w, `{"status":0}`)
						return
					}
					polls++
					if polls == 1 {
						ioJSON(w, `{"status":0,"running_code_status":1}`)
					} else {
						ioJSON(w, `{"status":2,"test_sets_count":1,"sets_error_count":0,"test_sets":[{"result":true,"compile_success":1}]}`)
					}
				default:
					t.Errorf("unexpected endpoint %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			protocol := NewGraphicProtocol(New(server.URL), transport)
			result, err := protocol.RunMySQLArtifact(context.Background(), task, plan, PollOptions{Interval: time.Millisecond, Timeout: 30 * time.Millisecond})
			if session.execs != 1 || prepares > 1 || builds > 1 {
				t.Fatalf("uncertain mutation replayed: executes=%d prepare=%d builds=%d", session.execs, prepares, builds)
			}
			if phase == "pass" {
				if err != nil || result == nil || !result.Saved || !result.Submitted || !result.Confirmed || !result.Evaluation.Passed || reads != 2 {
					t.Fatalf("no fresh source-bound verdict: %+v err=%v reads=%d", result, err, reads)
				}
			} else {
				if err == nil || result == nil || !result.ExecutionAttempted {
					t.Fatalf("uncertainty lost: %+v %v", result, err)
				}
			}
			if phase == "execution uncertain" && prepares != 0 {
				t.Fatal("uncertain SQL execution caused formal write")
			}
		})
	}
}

func TestMySQLArtifactCancelledOrChangedTaskNeverWrites(t *testing.T) {
	task := mysqlTaskFixture(t)
	plan, err := BuildMySQLArtifactPlan(task, *mysqlReferenceFixture())
	if err != nil {
		t.Fatal(err)
	}
	for _, cancelled := range []bool{true, false} {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			changed := *task
			changed.User.UserID++
			_ = json.NewEncoder(w).Encode(changed)
		}))
		session := &mysqlFakeSession{}
		transport := &mysqlFakeTransport{s: session}
		p := NewGraphicProtocol(New(server.URL), transport)
		ctx, cancel := context.WithCancel(context.Background())
		if cancelled {
			cancel()
		}
		_, err := p.RunMySQLArtifact(ctx, task, plan, PollOptions{})
		cancel()
		server.Close()
		if err == nil || transport.opens != 0 || session.execs != 0 {
			t.Fatal("cancelled or changed task mutated runtime")
		}
		if cancelled && calls != 0 {
			t.Fatal("cancelled context reached upstream")
		}
	}
}

func TestMySQLRemoteLockUsesKernelFlock(t *testing.T) {
	command := mysqlRemoteLockCommand(GraphicWorkspace + "/.educoder-mysql-lock")
	for _, required := range []string{"exec 9>", "flock -n 9", "printf 'locked", "trap 'exit 0' HUP INT TERM", "while :; do sleep 3600"} {
		if !strings.Contains(command, required) {
			t.Fatalf("lock command missing %q: %s", required, command)
		}
	}
	if strings.Contains(command, "mkdir --") || strings.Contains(command, "rmdir --") {
		t.Fatalf("lock command still relies on stale mkdir lock: %s", command)
	}
}

func TestMySQLArtifactStageEvidenceDistinguishesMutationBoundaries(t *testing.T) {
	pass := &MySQLArtifactResult{Stage: &MySQLArtifactStage{
		ExecutionAttempted: true, ExecutionConfirmed: true,
		PreparationAttempted: true, PreparationConfirmed: true,
		BuildAttempted: true, BuildAccepted: true,
		EvaluationAttempted: true, EvaluationObserved: true,
	}}
	if !pass.Stage.ExecutionConfirmed || !pass.Stage.BuildAccepted || !pass.Stage.EvaluationObserved {
		t.Fatal("successful stage evidence lost")
	}
	unknownBuild := &MySQLArtifactResult{Stage: &MySQLArtifactStage{ExecutionAttempted: true, ExecutionConfirmed: true, BuildAttempted: true}}
	if unknownBuild.Stage.BuildAccepted || unknownBuild.Stage.EvaluationObserved {
		t.Fatal("uncertain build was recorded as accepted")
	}
}
