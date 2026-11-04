package solver

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"sort"
	"strconv"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

// The editor file is only a contract label for terminal tasks. Candidate
// content is a workspace script, never replacement source for that editor.
type terminalTaskIdentity struct {
	course, game, instance, file, environments                  string
	homework, gameID, myshixunID, userID, challengeID, editorID int64
}

type terminalBinding struct {
	identity      terminalTaskIdentity
	executions    map[string]*terminalExecution
	activeHash    string
	running       string
	hadoopStarted bool
	hadoopErr     error
}

type terminalExecution struct {
	done      chan struct{}
	result    SelfTest
	receipt   *educoder.TerminalResult
	err       error
	submitted bool
}

func terminalIdentity(t *educoder.Task) (terminalTaskIdentity, error) {
	if t == nil || !t.IsTerminalTask() && !t.IsHadoopWorkspaceTask() || len(t.CodePaths()) != 1 || t.HomeworkID <= 0 || t.CourseIdentifier == "" || t.Game.Identifier == "" || t.Game.ID <= 0 || t.MyShixun.Identifier == "" || t.MyShixun.ID <= 0 || t.Game.MyShixunID != t.MyShixun.ID || t.User.UserID <= 0 || t.Challenge.ID <= 0 {
		return terminalTaskIdentity{}, errors.New("terminal task identity or file contract is incomplete")
	}
	// Runtime identifiers remain private. Sorting avoids treating response order
	// as an environment change while still binding every observed field.
	envs := make([]string, 0, len(t.Environments))
	for _, env := range t.Environments {
		encoded, _ := json.Marshal(env)
		envs = append(envs, string(encoded))
	}
	sort.Strings(envs)
	encoded, _ := json.Marshal(envs)
	return terminalTaskIdentity{course: t.CourseIdentifier, game: t.Game.Identifier, instance: t.MyShixun.Identifier, file: t.CodePaths()[0], environments: problem.HashContent(string(encoded)), homework: t.HomeworkID, gameID: t.Game.ID, myshixunID: t.MyShixun.ID, userID: t.User.UserID, challengeID: t.Challenge.ID, editorID: t.CodeEditor.EnvironmentID}, nil
}

func (a *EducoderAdapter) loadTerminal(t *educoder.Task, target Target) (LoadedProblem, error) {
	identity, err := terminalIdentity(t)
	if err != nil {
		return LoadedProblem{}, err
	}
	snapshot := problem.Snapshot{
		SchemaVersion: problem.SchemaVersion, Kind: "programming", Title: t.Challenge.Subject,
		StatementMarkdown: t.Challenge.Description,
		Files:             []problem.File{{Path: identity.file, Filename: path.Base(identity.file), Extension: path.Ext(identity.file), Language: "shell", CurrentContent: "", StarterProvenance: "unknown"}},
		// This single empty sample schedules one execution. The adapter checks
		// its real exit status; it never compares placeholder expected stdout.
		PublicTests:         []problem.PublicTest{{Stdin: "", ExpectedStdout: ""}},
		ObservedConstraints: []string{"terminal exit status only; formal evaluation validates workspace state", "candidate content is a POSIX shell script executed in the terminal; the named editor file is not rewritten"},
		EnvironmentContract: []string{"terminal:posix-shell/v1", "execution:workspace-state", "validation:exit-status-then-formal-evaluation", "language:shell"},
		EditMode:            "terminal", SnapshotCompleteness: "observed",
	}
	if _, err := problem.Prepare(snapshot); err != nil {
		return LoadedProblem{}, err
	}
	return LoadedProblem{Snapshot: snapshot, ExternalIdentity: questionbank.ExternalIdentity{Experiment: t.Shixun.Name, Title: t.Challenge.Subject, Position: t.Challenge.Position}, Key: strconv.FormatInt(t.MyShixun.ID, 10), target: target, terminalIdentity: &identity}, nil
}

func (a *EducoderAdapter) hasTerminalBinding(game string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.terminalTasks[game] != nil
}

// Caller holds a.mu. Re-loading the same game is insufficient by itself: a
// changed account, challenge, runtime, environment or path must stop the run.
func (a *EducoderAdapter) terminalGuard(t *educoder.Task, target Target, file problem.SolutionFile) (*terminalBinding, string, error) {
	identity, err := terminalIdentity(t)
	if err != nil {
		return nil, "", err
	}
	bound := a.terminalTasks[target.Game]
	if bound == nil || bound.identity != identity || target.Course != identity.course || target.HomeworkID != identity.homework || target.Game != identity.game || file.Path != identity.file {
		return nil, "", errors.New("terminal task changed or is not bound to the active solver run")
	}
	hash, err := problem.HashFiles([]problem.SolutionFile{file})
	return bound, hash, err
}

func (a *EducoderAdapter) selfTestTerminal(ctx context.Context, t *educoder.Task, target Target, file problem.SolutionFile, input, expected string) (SelfTest, error) {
	if input != "" || expected != "" {
		return SelfTest{}, errors.New("terminal execution accepts an exit-status contract, not stdout samples")
	}
	a.mu.Lock()
	bound, hash, err := a.terminalGuard(t, target, file)
	if err != nil {
		a.mu.Unlock()
		return SelfTest{}, err
	}
	if t.IsHadoopWorkspaceTask() && !bound.hadoopStarted {
		bound.hadoopStarted = true
		start := a.hadoopStart
		if start == nil {
			start = func(ctx context.Context, task *educoder.Task) error { return a.Client.StartHadoop(ctx, task) }
		}
		if startErr := start(ctx, t); startErr != nil {
			bound.hadoopErr = errors.New("Hadoop 运行环境未就绪")
		}
	}
	if bound.hadoopErr != nil {
		err := bound.hadoopErr
		a.mu.Unlock()
		return SelfTest{}, err
	}
	if prior := bound.executions[hash]; prior != nil {
		if bound.activeHash != hash {
			a.mu.Unlock()
			return SelfTest{}, errors.New("terminal workspace was changed by another candidate; the earlier script will not be replayed")
		}
		a.mu.Unlock()
		select {
		case <-prior.done:
			return prior.result, prior.err
		case <-ctx.Done():
			return SelfTest{}, ctx.Err()
		}
	}
	if bound.running != "" {
		a.mu.Unlock()
		return SelfTest{}, errors.New("another terminal candidate is still executing in this workspace")
	}
	// Reserve before any mutation, including failures with uncertain execution.
	// A duplicate sample/call observes this result and cannot replay the script.
	execution := &terminalExecution{done: make(chan struct{})}
	bound.executions[hash] = execution
	bound.activeHash, bound.running = hash, hash
	a.mu.Unlock()
	run := a.terminalRun
	if run == nil {
		run = a.Client.RunTerminalCommand
	}
	receipt, runErr := run(ctx, t, file.Content, educoder.TerminalOptions{})
	result := SelfTest{}
	if receipt != nil {
		result.Output = receipt.Output
	}
	if runErr == nil {
		if receipt == nil || !receipt.Completed || receipt.ExitCode == nil || receipt.ScriptHash != problem.HashContent(file.Content) {
			runErr = errors.New("terminal script has no complete source-bound exit result; execution will not be replayed")
		} else {
			result.Completed = true
			result.Matches = *receipt.ExitCode == 0
			exit := *receipt.ExitCode
			result.ExitCode = &exit
			// For terminal mode this is the executed candidate hash, not a
			// claim that script bytes were saved into the placeholder editor.
			result.SavedFilesHash = hash
		}
	}
	a.mu.Lock()
	if a.terminalTasks[target.Game] != bound {
		result.Completed, result.Matches, result.SavedFilesHash = false, false, ""
		runErr = errors.New("terminal binding changed during execution; result remains unverified")
	}
	bound.running = ""
	execution.result, execution.err = result, runErr
	if runErr == nil && result.Completed && result.Matches {
		execution.receipt = receipt
	}
	close(execution.done)
	a.mu.Unlock()
	return result, runErr
}

func (a *EducoderAdapter) submitTerminal(ctx context.Context, t *educoder.Task, target Target, file problem.SolutionFile) (Evaluation, error) {
	a.mu.Lock()
	bound, hash, err := a.terminalGuard(t, target, file)
	if err != nil {
		a.mu.Unlock()
		return Evaluation{Status: "terminal_unverified"}, err
	}
	execution := bound.executions[hash]
	if execution == nil {
		a.mu.Unlock()
		return Evaluation{Status: "terminal_unverified"}, errors.New("terminal submission requires this candidate's completed execution")
	}
	select {
	case <-execution.done:
	default:
		a.mu.Unlock()
		return Evaluation{Status: "terminal_unverified"}, errors.New("terminal execution has not completed")
	}
	if execution.err != nil || execution.receipt == nil || !execution.result.Completed || !execution.result.Matches || execution.submitted || bound.activeHash != hash || bound.running != "" {
		a.mu.Unlock()
		return Evaluation{Status: "terminal_unverified"}, errors.New("terminal receipt is missing, failed, uncertain or already submitted")
	}
	execution.submitted = true
	// Formal evaluation observes the workspace left by this script. Another
	// candidate must not mutate that workspace while evaluation is running.
	bound.running = hash
	receipt := execution.receipt
	a.mu.Unlock()
	submit := a.terminalSubmit
	if submit == nil {
		submit = a.Client.SubmitTerminal
	}
	r, err := submit(ctx, t, receipt, educoder.PollOptions{})
	a.mu.Lock()
	bound.running = ""
	a.mu.Unlock()
	// A formal pass observes this existing workspace, not a clean baseline.
	// Earlier candidates/jobs may have supplied necessary files, even when this
	// adapter only executed one script. Do not promote it as a reusable answer.
	result := Evaluation{Status: "error", SavedFilesHash: hash, ReuseUnproven: true}
	if r != nil {
		result.Completed, result.Passed = r.Completed, r.Passed
		result.SubmittedFilesHash = hash
		result.Feedback = r.LastCompileOutput
		if r.Status != nil {
			result.Status = strconv.Itoa(*r.Status)
		}
		if r.TestSetsCount != nil {
			result.TestCount = *r.TestSetsCount
		} else {
			result.TestCount = len(r.TestSets)
		}
		for _, test := range r.TestSets {
			if test.Result != nil && *test.Result && test.CompileSuccess != nil && *test.CompileSuccess == 1 {
				result.PassedCount++
			}
		}
	}
	if err != nil {
		return result, err
	}
	if r == nil {
		return result, errors.New("terminal evaluation returned no source-bound result")
	}
	return result, nil
}
