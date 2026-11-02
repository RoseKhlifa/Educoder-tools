package solver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

func terminalAdapterFixture() map[string]any {
	fixture := adapterFixture(0)
	fixture["code_editor"] = map[string]any{"shixun_environment_id": 0, "name": ""}
	fixture["shixun_environments"] = []any{map[string]any{"shixun_environment_id": 15, "tab_type": 4, "name": "Linux"}}
	fixture["challenge"].(map[string]any)["path"] = "step3/placeholder.py"
	fixture["test_sets"] = []any{map[string]any{"is_public": true, "input": "", "output": "placeholder stdout"}}
	return fixture
}

func terminalTestAdapter(t *testing.T) (*EducoderAdapter, map[string]any) {
	t.Helper()
	fixture := terminalAdapterFixture()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/tasks/g.json" {
			t.Errorf("terminal adapter unexpectedly accessed editor/build endpoint: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected protocol route", 500)
			return
		}
		_ = json.NewEncoder(w).Encode(fixture)
	}))
	t.Cleanup(server.Close)
	a, err := NewEducoderAdapter(educoder.New(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	return a, fixture
}

func terminalCandidate() problem.SolutionFile {
	return problem.SolutionFile{Path: "step3/placeholder.py", Content: "#!/bin/sh\nset -eu\nmkdir -p experiment\nprintf '%s\\n' data > experiment/result.txt\n"}
}

func terminalSuccess(file problem.SolutionFile) *educoder.TerminalResult {
	exit := 0
	return &educoder.TerminalResult{Completed: true, Output: "workspace operation completed\n", ExitCode: &exit, ScriptHash: problem.HashContent(file.Content)}
}

func terminalPass() *educoder.EvaluationResult {
	status, count, compile := 2, 1, 1
	passed := true
	return &educoder.EvaluationResult{Completed: true, Passed: true, Status: &status, TestSetsCount: &count, TestSets: []educoder.TestSet{{Result: &passed, CompileSuccess: &compile}}}
}

func bindTerminalTest(t *testing.T, a *EducoderAdapter) LoadedProblem {
	t.Helper()
	loaded, err := a.Load(context.Background(), Target{"c", 1, "g"})
	if err != nil {
		t.Fatal(err)
	}
	a.bindSnapshot(loaded)
	return loaded
}

func TestTerminalSnapshotExcludesPlaceholderAndStdoutOracle(t *testing.T) {
	a, _ := terminalTestAdapter(t)
	loaded := bindTerminalTest(t, a)
	snapshot, err := problem.Prepare(loaded.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.EditMode != "terminal" || snapshot.Files[0].Language != "shell" || snapshot.Files[0].CurrentContent != "" || snapshot.Files[0].Path != terminalCandidate().Path || len(snapshot.PublicTests) != 1 || snapshot.PublicTests[0].Stdin != "" || snapshot.PublicTests[0].ExpectedStdout != "" {
		t.Fatalf("terminal script contract mixed with editor source: %+v", snapshot)
	}
	ordinary := snapshot
	ordinary.EditMode = "single_file"
	ordinary.EnvironmentContract = []string{"editor:python", "language:python"}
	code, err := problem.Prepare(ordinary)
	if err != nil {
		t.Fatal(err)
	}
	if code.ContractHash == snapshot.ContractHash {
		t.Fatal("terminal bank contract is not separated from editor submissions")
	}
	encoded, _ := json.Marshal(snapshot)
	if strings.Contains(string(encoded), "placeholder stdout") || strings.Contains(string(encoded), "private-input") || strings.Contains(string(encoded), "instance") {
		t.Fatalf("runtime/private/placeholder data leaked into snapshot: %s", encoded)
	}
}

func TestTerminalExecuteExactlyOnceThenConsumeFormalReceipt(t *testing.T) {
	a, _ := terminalTestAdapter(t)
	bindTerminalTest(t, a)
	file := terminalCandidate()
	var runs, submits int
	var issued *educoder.TerminalResult
	a.terminalRun = func(_ context.Context, _ *educoder.Task, script string, _ educoder.TerminalOptions) (*educoder.TerminalResult, error) {
		runs++
		if script != file.Content {
			t.Fatal("script bytes changed before execution")
		}
		issued = terminalSuccess(file)
		return issued, nil
	}
	a.terminalSubmit = func(_ context.Context, _ *educoder.Task, receipt *educoder.TerminalResult, _ educoder.PollOptions) (*educoder.EvaluationResult, error) {
		submits++
		if receipt != issued {
			t.Fatal("formal submission did not use the exact execution receipt")
		}
		return terminalPass(), nil
	}
	target := Target{"c", 1, "g"}
	if _, err := a.Submit(context.Background(), target, file); err == nil || submits != 0 {
		t.Fatal("formal submit was allowed without executing the script")
	}
	hash, _ := problem.HashFiles([]problem.SolutionFile{file})
	for i := 0; i < 2; i++ {
		st, err := a.SelfTest(context.Background(), target, file, "", "")
		if err != nil || !st.Completed || !st.Matches || st.ExitCode == nil || *st.ExitCode != 0 || st.SavedFilesHash != hash || st.Output != "workspace operation completed\n" {
			t.Fatalf("execution evidence missing: %+v err=%v", st, err)
		}
	}
	changed := file
	changed.Content += "printf 'different'\n"
	if _, err := a.Submit(context.Background(), target, changed); err == nil || submits != 0 {
		t.Fatal("receipt accepted a different exact candidate hash")
	}
	eval, err := a.Submit(context.Background(), target, file)
	if err != nil || !eval.Completed || !eval.Passed || eval.SavedFilesHash != hash || eval.SubmittedFilesHash != hash || eval.PassedCount != 1 {
		t.Fatalf("formal evidence missing: %+v err=%v", eval, err)
	}
	if _, err := a.Submit(context.Background(), target, file); err == nil {
		t.Fatal("formal receipt replay was allowed")
	}
	if runs != 1 || submits != 1 {
		t.Fatalf("script or formal mutation replayed: runs=%d submits=%d", runs, submits)
	}
}

func TestTerminalUnknownOrFailedExecutionNeverReplaysOrSubmits(t *testing.T) {
	for _, scenario := range []string{"disconnect", "incomplete", "wrong_hash", "nonzero"} {
		t.Run(scenario, func(t *testing.T) {
			a, _ := terminalTestAdapter(t)
			bindTerminalTest(t, a)
			file := terminalCandidate()
			runs, submits := 0, 0
			a.terminalRun = func(context.Context, *educoder.Task, string, educoder.TerminalOptions) (*educoder.TerminalResult, error) {
				runs++
				r := terminalSuccess(file)
				switch scenario {
				case "disconnect":
					return r, errors.New("terminal disconnected after sending script")
				case "incomplete":
					r.Completed = false
				case "wrong_hash":
					r.ScriptHash = "different-script"
				case "nonzero":
					exit := 7
					r.ExitCode, r.Output = &exit, "mkdir failed\n"
				}
				return r, nil
			}
			a.terminalSubmit = func(context.Context, *educoder.Task, *educoder.TerminalResult, educoder.PollOptions) (*educoder.EvaluationResult, error) {
				submits++
				return terminalPass(), nil
			}
			for i := 0; i < 2; i++ {
				st, err := a.SelfTest(context.Background(), Target{"c", 1, "g"}, file, "", "")
				if scenario == "nonzero" {
					if err != nil || !st.Completed || st.Matches || st.ExitCode == nil || *st.ExitCode != 7 || st.SavedFilesHash == "" {
						t.Fatalf("confirmed nonzero result not preserved: %+v %v", st, err)
					}
				} else if err == nil || st.Matches || st.Completed || st.SavedFilesHash != "" {
					t.Fatalf("unknown execution promoted to evidence: %+v %v", st, err)
				}
			}
			if _, err := a.Submit(context.Background(), Target{"c", 1, "g"}, file); err == nil {
				t.Fatal("unconfirmed/failed execution was formally submitted")
			}
			if runs != 1 || submits != 0 {
				t.Fatalf("uncertain mutation replayed: runs=%d submits=%d", runs, submits)
			}
		})
	}
}

func TestTerminalUncertainFormalSubmissionDoesNotReplay(t *testing.T) {
	a, _ := terminalTestAdapter(t)
	bindTerminalTest(t, a)
	file := terminalCandidate()
	runs, submits := 0, 0
	a.terminalRun = func(context.Context, *educoder.Task, string, educoder.TerminalOptions) (*educoder.TerminalResult, error) {
		runs++
		return terminalSuccess(file), nil
	}
	a.terminalSubmit = func(context.Context, *educoder.Task, *educoder.TerminalResult, educoder.PollOptions) (*educoder.EvaluationResult, error) {
		submits++
		return nil, errors.New("formal response disconnected")
	}
	target := Target{"c", 1, "g"}
	if _, err := a.SelfTest(context.Background(), target, file, "", ""); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := a.Submit(context.Background(), target, file); err == nil {
			t.Fatal("uncertain formal submission was treated as successful")
		}
	}
	if runs != 1 || submits != 1 {
		t.Fatalf("uncertain formal mutation replayed: runs=%d submits=%d", runs, submits)
	}
}

func TestTerminalIdentityChangesStopBeforeExecutionOrFormalMutation(t *testing.T) {
	mutations := map[string]func(map[string]any){
		"user":      func(f map[string]any) { f["user"].(map[string]any)["user_id"] = 999 },
		"game":      func(f map[string]any) { f["game"].(map[string]any)["id"] = 999 },
		"runtime":   func(f map[string]any) { f["myshixun"].(map[string]any)["identifier"] = "other" },
		"challenge": func(f map[string]any) { f["challenge"].(map[string]any)["id"] = 999 },
		"path":      func(f map[string]any) { f["challenge"].(map[string]any)["path"] = "other.py" },
		"environment": func(f map[string]any) {
			f["shixun_environments"].([]any)[0].(map[string]any)["shixun_environment_id"] = 999
		},
		"editor_layout": func(f map[string]any) { f["code_editor"].(map[string]any)["shixun_environment_id"] = 15 },
	}
	for name, mutate := range mutations {
		for _, afterExecution := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "/before_script", true: "/before_formal"}[afterExecution], func(t *testing.T) {
				a, fixture := terminalTestAdapter(t)
				bindTerminalTest(t, a)
				file := terminalCandidate()
				runs, submits := 0, 0
				a.terminalRun = func(context.Context, *educoder.Task, string, educoder.TerminalOptions) (*educoder.TerminalResult, error) {
					runs++
					return terminalSuccess(file), nil
				}
				a.terminalSubmit = func(context.Context, *educoder.Task, *educoder.TerminalResult, educoder.PollOptions) (*educoder.EvaluationResult, error) {
					submits++
					return terminalPass(), nil
				}
				if afterExecution {
					if _, err := a.SelfTest(context.Background(), Target{"c", 1, "g"}, file, "", ""); err != nil {
						t.Fatal(err)
					}
				}
				mutate(fixture)
				if _, err := a.SelfTest(context.Background(), Target{"c", 1, "g"}, file, "", ""); err == nil {
					t.Fatal("changed task allowed script execution")
				}
				if _, err := a.Submit(context.Background(), Target{"c", 1, "g"}, file); err == nil {
					t.Fatal("changed task allowed formal submission")
				}
				if runs != map[bool]int{false: 0, true: 1}[afterExecution] || submits != 0 {
					t.Fatalf("task change caused mutation: runs=%d submits=%d", runs, submits)
				}
			})
		}
	}
}

func TestTerminalConcurrentDuplicateWaitsForSingleExecution(t *testing.T) {
	a, _ := terminalTestAdapter(t)
	bindTerminalTest(t, a)
	file := terminalCandidate()
	var runs atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	a.terminalRun = func(context.Context, *educoder.Task, string, educoder.TerminalOptions) (*educoder.TerminalResult, error) {
		runs.Add(1)
		close(started)
		<-release
		return terminalSuccess(file), nil
	}
	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			r, err := a.SelfTest(context.Background(), Target{"c", 1, "g"}, file, "", "")
			if err != nil || !r.Completed {
				t.Errorf("duplicate execution result: %+v %v", r, err)
			}
		}()
	}
	<-started
	close(release)
	wg.Wait()
	if runs.Load() != 1 {
		t.Fatalf("concurrent calls ran %d scripts", runs.Load())
	}
}

func TestTerminalReceiptCannotEvaluateWorkspaceChangedByAnotherCandidate(t *testing.T) {
	a, _ := terminalTestAdapter(t)
	bindTerminalTest(t, a)
	first := terminalCandidate()
	second := first
	second.Content += "printf 'second' > experiment/second.txt\n"
	runs, submits := 0, 0
	a.terminalRun = func(_ context.Context, _ *educoder.Task, script string, _ educoder.TerminalOptions) (*educoder.TerminalResult, error) {
		runs++
		return terminalSuccess(problem.SolutionFile{Path: first.Path, Content: script}), nil
	}
	a.terminalSubmit = func(context.Context, *educoder.Task, *educoder.TerminalResult, educoder.PollOptions) (*educoder.EvaluationResult, error) {
		submits++
		return terminalPass(), nil
	}
	target := Target{"c", 1, "g"}
	for _, file := range []problem.SolutionFile{first, second} {
		if _, err := a.SelfTest(context.Background(), target, file, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.Submit(context.Background(), target, first); err == nil {
		t.Fatal("old candidate receipt evaluated another candidate's workspace")
	}
	if _, err := a.SelfTest(context.Background(), target, first, "", ""); err == nil {
		t.Fatal("old candidate was silently replayed or restored as current")
	}
	if _, err := a.Submit(context.Background(), target, second); err != nil {
		t.Fatal(err)
	}
	if runs != 2 || submits != 1 {
		t.Fatalf("stale receipt caused mutation: runs=%d submits=%d", runs, submits)
	}
}

func TestSolverTerminalStdoutDoesNotTriggerRuntimeStartupReplay(t *testing.T) {
	a, _ := terminalTestAdapter(t)
	file := terminalCandidate()
	runs := 0
	a.terminalRun = func(context.Context, *educoder.Task, string, educoder.TerminalOptions) (*educoder.TerminalResult, error) {
		runs++
		r := terminalSuccess(file)
		r.Output = runtimeStartupFailureMessage
		return r, nil
	}
	provider := &fakeProvider{candidates: []answer.Candidate{{Kind: "programming", Language: "shell", Files: []problem.SolutionFile{file}}}}
	service := New(&fakeStore{}, provider, Config{Timeout: time.Second})
	result, err := service.Run(context.Background(), &terminalProgressAdapter{a}, Request{Owner: "account", Course: "c", HomeworkID: 1, Game: "g", Mode: ModeSelfTest})
	if err != nil || result.Status != "selftest_passed" || runs != 1 {
		t.Fatalf("terminal stdout was treated as a replayable environment error: result=%+v err=%v runs=%d", result, err, runs)
	}
}

type terminalProgressAdapter struct{ *EducoderAdapter }

func (*terminalProgressAdapter) Progress(context.Context, Target) (Progress, error) {
	return Progress{PassedCount: 1}, nil
}

func TestSolverTerminalRunsScriptInSelfTestAndSubmitModes(t *testing.T) {
	for _, mode := range []Mode{ModeSelfTest, ModeSubmit} {
		t.Run(string(mode), func(t *testing.T) {
			a, _ := terminalTestAdapter(t)
			file := terminalCandidate()
			runs, submits := 0, 0
			a.terminalRun = func(context.Context, *educoder.Task, string, educoder.TerminalOptions) (*educoder.TerminalResult, error) {
				runs++
				return terminalSuccess(file), nil
			}
			a.terminalSubmit = func(context.Context, *educoder.Task, *educoder.TerminalResult, educoder.PollOptions) (*educoder.EvaluationResult, error) {
				submits++
				return terminalPass(), nil
			}
			provider := &fakeProvider{candidates: []answer.Candidate{{Kind: "programming", Language: "shell", Files: []problem.SolutionFile{file}}}}
			store := &fakeStore{}
			service := New(store, provider, Config{Timeout: time.Second})
			result, err := service.Run(context.Background(), &terminalProgressAdapter{a}, Request{Owner: "account", Course: "c", HomeworkID: 1, Game: "g", Mode: mode})
			wantStatus, wantSubmits := "selftest_passed", 0
			if mode == ModeSubmit {
				wantStatus, wantSubmits = "passed", 1
			}
			if err != nil || result.Status != wantStatus || result.SelfTestStatus != "passed" || runs != 1 || submits != wantSubmits {
				t.Fatalf("terminal flow skipped execution or repeated mutations: result=%+v err=%v runs=%d submits=%d", result, err, runs, submits)
			}
			if mode == ModeSelfTest && store.candidates[0].Status == "upstream_verified" {
				t.Fatal("exit zero was recorded as formal pass")
			}
			if mode == ModeSubmit && (store.candidates[0].Status != "workspace_passed" || result.Evaluation == nil || !result.Evaluation.ReuseUnproven || store.validations[len(store.validations)-1].Kind != "formal_workspace") {
				t.Fatal("workspace pass was lost or promoted as an independently reusable script")
			}
		})
	}
}

type terminalExternalStore struct {
	fakeStore
	binds int
}

func (s *terminalExternalStore) BindExternalCandidates(context.Context, questionbank.Ref, questionbank.ExternalIdentity, problem.Snapshot) ([]questionbank.Candidate, error) {
	s.binds++
	return nil, nil
}
func (*terminalExternalStore) IsExternalCandidate(context.Context, int64) (bool, error) {
	return true, nil
}

func TestTerminalContractDoesNotImportEditorPlaceholderFromExternalBank(t *testing.T) {
	store := &terminalExternalStore{}
	service := New(store, nil, Config{})
	loaded := LoadedProblem{Snapshot: problem.Snapshot{EditMode: "terminal"}, ExternalIdentity: questionbank.ExternalIdentity{Experiment: "Linux", Title: "Create files"}}
	if err := service.bindExternalCandidates(context.Background(), questionbank.Ref{}, loaded); err != nil || store.binds != 0 {
		t.Fatalf("terminal imported ordinary source: binds=%d err=%v", store.binds, err)
	}
	loaded.Snapshot.EditMode = "single_file"
	if err := service.bindExternalCandidates(context.Background(), questionbank.Ref{}, loaded); err != nil || store.binds != 1 {
		t.Fatalf("ordinary external import regressed: binds=%d err=%v", store.binds, err)
	}
}
