package solver

import (
	"context"
	"errors"
	"testing"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/problem"
)

func hadoopWorkspaceAdapterTask() *educoder.Task {
	zero, yes := 0, true
	t := &educoder.Task{ST: &zero, HomeworkID: 1, CourseIdentifier: "c", Environments: []educoder.ProgrammingEnvironment{{EnvironmentID: 12, TabType: 1}, {EnvironmentID: 12, TabType: 4}}}
	t.CodeEditor.EnvironmentID, t.CodeEditor.Name = 12, "Hadoop"
	t.Game.ID, t.Game.Identifier, t.Game.MyShixunID = 3, "g", 4
	t.MyShixun.ID, t.MyShixun.Identifier = 4, "instance"
	t.User.UserID = 5
	t.Challenge.ID, t.Challenge.ST, t.Challenge.WithCodeFile = 6, &zero, &yes
	t.Challenge.Path, t.Challenge.Description = "src/step1/hint.txt", "请使用命令行完成"
	return t
}

func bindHadoopWorkspaceAdapter(t *testing.T, a *EducoderAdapter, task *educoder.Task) problem.SolutionFile {
	t.Helper()
	identity, err := terminalIdentity(task)
	if err != nil {
		t.Fatal(err)
	}
	loaded := LoadedProblem{Snapshot: problem.Snapshot{Kind: "programming", EditMode: "terminal", Files: []problem.File{{Path: identity.file, Filename: "hint.txt", Language: "shell"}}, PublicTests: []problem.PublicTest{{}}}, target: Target{"c", 1, "g"}, terminalIdentity: &identity}
	a.bindSnapshot(loaded)
	return problem.SolutionFile{Path: identity.file, Content: "printf ready\n"}
}

func TestHadoopWorkspaceAdapterStartsOnceBeforeCandidateAndDoesNotReplayAfterFailure(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		task := hadoopWorkspaceAdapterTask()
		a, err := NewEducoderAdapter(educoder.New("https://fixture.invalid"))
		if err != nil {
			t.Fatal(err)
		}
		var starts, runs int
		a.hadoopStart = func(context.Context, *educoder.Task) error { starts++; return nil }
		file := bindHadoopWorkspaceAdapter(t, a, task)
		a.terminalRun = func(_ context.Context, _ *educoder.Task, script string, _ educoder.TerminalOptions) (*educoder.TerminalResult, error) {
			runs++
			return terminalSuccess(file), nil
		}
		for i := 0; i < 2; i++ {
			st, runErr := a.selfTestTerminal(context.Background(), task, Target{"c", 1, "g"}, file, "", "")
			if runErr != nil || !st.Completed || !st.Matches {
				t.Fatalf("selftest=%+v err=%v", st, runErr)
			}
		}
		if starts != 1 || runs != 1 {
			t.Fatalf("workspace preflight/candidate replayed: starts=%d runs=%d", starts, runs)
		}
	})
	t.Run("preflight-failure", func(t *testing.T) {
		task := hadoopWorkspaceAdapterTask()
		a, err := NewEducoderAdapter(educoder.New("https://fixture.invalid"))
		if err != nil {
			t.Fatal(err)
		}
		var starts, runs int
		a.hadoopStart = func(context.Context, *educoder.Task) error { starts++; return errors.New("private bootstrap detail") }
		file := bindHadoopWorkspaceAdapter(t, a, task)
		a.terminalRun = func(context.Context, *educoder.Task, string, educoder.TerminalOptions) (*educoder.TerminalResult, error) {
			runs++
			return terminalSuccess(file), nil
		}
		for i := 0; i < 2; i++ {
			st, runErr := a.selfTestTerminal(context.Background(), task, Target{"c", 1, "g"}, file, "", "")
			if runErr == nil || st.Completed || st.SavedFilesHash != "" {
				t.Fatalf("failed preflight became candidate evidence: %+v err=%v", st, runErr)
			}
		}
		if starts != 1 || runs != 0 {
			t.Fatalf("failed preflight replayed or candidate ran: starts=%d runs=%d", starts, runs)
		}
	})
}
