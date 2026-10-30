package solver

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/problem"
)

// EducoderAdapter binds the generic solver to the already authenticated
// protocol client. It deliberately exposes no session or execution key to the
// solver's persisted state.
type EducoderAdapter struct {
	Client   *educoder.Client
	mu       sync.Mutex
	expected map[string]map[string]string
}

func NewEducoderAdapter(client *educoder.Client) (*EducoderAdapter, error) {
	if client == nil {
		return nil, errors.New("educoder client is required")
	}
	return &EducoderAdapter{Client: client}, nil
}

// RunClient is a convenience for callers that already own an authenticated
// protocol client and do not need to customize the execution adapter.
func (s *Service) RunClient(ctx context.Context, client *educoder.Client, req Request) (*Result, error) {
	adapter, err := NewEducoderAdapter(client)
	if err != nil {
		return nil, err
	}
	return s.Run(ctx, adapter, req)
}

func (a *EducoderAdapter) Load(ctx context.Context, target Target) (LoadedProblem, error) {
	t, err := a.Client.LoadTask(ctx, target.Course, target.HomeworkID, target.Game)
	if err != nil {
		return LoadedProblem{}, err
	}
	pt, err := a.Client.ReadProgramming(ctx, t)
	if err != nil {
		return LoadedProblem{}, err
	}
	files := make([]problem.File, 0, len(pt.Files))
	for _, f := range pt.Files {
		files = append(files, problem.File{Path: f.Path, Filename: f.Filename, Language: f.Language, Extension: f.Extension, CurrentContent: f.Content, StarterProvenance: "unknown"})
	}
	public := make([]problem.PublicTest, 0)
	for _, test := range t.TestSets {
		if test.IsPublic {
			public = append(public, problem.PublicTest{Stdin: test.Input, ExpectedStdout: test.Output})
		}
	}
	environment := []string{"editor:" + strings.ToLower(strings.TrimSpace(t.CodeEditor.Name))}
	seen := map[string]bool{}
	for _, file := range files {
		if !seen[file.Language] {
			environment = append(environment, "language:"+file.Language)
			seen[file.Language] = true
		}
	}
	snap := problem.Snapshot{SchemaVersion: problem.SchemaVersion, Kind: "programming", Title: t.Challenge.Subject, StatementMarkdown: pt.Markdown, Files: files, PublicTests: public, EditMode: "single_file", SnapshotCompleteness: "observed", EnvironmentContract: environment}
	return LoadedProblem{Snapshot: snap, Key: strconv.FormatInt(t.MyShixun.ID, 10), target: target}, nil
}

// Only the solver calls bindSnapshot, after acquiring the runtime lock. A
// competing discovery Load therefore cannot replace the active run's baseline.
func (a *EducoderAdapter) bindSnapshot(loaded LoadedProblem) {
	hashes := make(map[string]string, len(loaded.Snapshot.Files))
	for _, file := range loaded.Snapshot.Files {
		hashes[file.Path] = problem.HashContent(file.CurrentContent)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.expected == nil {
		a.expected = map[string]map[string]string{}
	}
	a.expected[loaded.target.Game] = hashes
}

func (a *EducoderAdapter) guardEditor(ctx context.Context, t *educoder.Task, file problem.SolutionFile) error {
	a.mu.Lock()
	expected, bound := a.expected[t.Game.Identifier][file.Path]
	a.mu.Unlock()
	if !bound {
		return errors.New("editor baseline is not bound to the active solver run")
	}
	current, err := a.Client.ReadCodeFile(ctx, t, file.Path)
	if err != nil {
		return err
	}
	if problem.HashContent(current.Content) != expected {
		return errors.New("editor content changed concurrently; reload before writing")
	}
	return nil
}

func (a *EducoderAdapter) confirmEditor(ctx context.Context, t *educoder.Task, file problem.SolutionFile) (string, error) {
	current, err := a.Client.ReadCodeFile(ctx, t, file.Path)
	if err != nil {
		return "", err
	}
	if current.Content != file.Content {
		return "", errors.New("saved editor content differs from candidate; evaluation remains unverified")
	}
	hash, err := problem.HashFiles([]problem.SolutionFile{file})
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	if a.expected[t.Game.Identifier] == nil {
		a.expected[t.Game.Identifier] = map[string]string{}
	}
	a.expected[t.Game.Identifier][file.Path] = problem.HashContent(file.Content)
	a.mu.Unlock()
	return hash, nil
}

func (a *EducoderAdapter) task(ctx context.Context, target Target) (*educoder.Task, error) {
	if target.Game == "" {
		return nil, errors.New("game is required for task execution")
	}
	return a.Client.LoadTask(ctx, target.Course, target.HomeworkID, target.Game)
}

func (a *EducoderAdapter) SelfTest(ctx context.Context, target Target, file problem.SolutionFile, input, expected string) (SelfTest, error) {
	t, err := a.task(ctx, target)
	if err != nil {
		return SelfTest{}, err
	}
	if err := a.guardEditor(ctx, t, file); err != nil {
		return SelfTest{}, err
	}
	want := expected
	r, err := a.Client.SelfTest(ctx, t, file.Path, file.Content, input, educoder.SelfTestOptions{ExpectedOutput: &want})
	if err != nil {
		return SelfTest{Completed: r != nil && r.Completed, Output: outputOf(r), Matches: r != nil && r.Matches != nil && *r.Matches}, err
	}
	hash, err := a.confirmEditor(ctx, t, file)
	if err != nil {
		return SelfTest{Completed: r.Completed, Output: r.Output}, err
	}
	return SelfTest{Completed: r.Completed, Output: r.Output, Matches: r.Matches != nil && *r.Matches, SavedFilesHash: hash}, nil
}

func outputOf(r *educoder.SelfTestResult) string {
	if r == nil {
		return ""
	}
	return r.Output
}

func (a *EducoderAdapter) Submit(ctx context.Context, target Target, file problem.SolutionFile) (Evaluation, error) {
	t, err := a.task(ctx, target)
	if err != nil {
		return Evaluation{}, err
	}
	if err := a.guardEditor(ctx, t, file); err != nil {
		return Evaluation{}, err
	}
	saved, err := a.Client.SaveCode(ctx, t, file.Path, file.Content, true)
	if err != nil {
		return Evaluation{Status: "save_error", Feedback: err.Error()}, err
	}
	hash, err := a.confirmEditor(ctx, t, file)
	if err != nil {
		return Evaluation{Status: "save_unverified", Feedback: err.Error()}, err
	}
	if _, err := a.Client.BuildCode(ctx, t, saved); err != nil {
		return Evaluation{Status: "build_error", Feedback: err.Error(), SavedFilesHash: hash}, err
	}
	r, err := a.Client.WaitEvaluation(ctx, t, saved, educoder.PollOptions{})
	if err != nil {
		return Evaluation{Status: "error", Feedback: err.Error(), SavedFilesHash: hash}, err
	}
	if r == nil {
		return Evaluation{Status: "unknown", Feedback: "empty evaluation result"}, errors.New("empty evaluation result")
	}
	if _, err := a.confirmEditor(ctx, t, file); err != nil {
		return Evaluation{Status: "concurrent_edit", Feedback: err.Error(), SavedFilesHash: hash}, err
	}
	status := "failed"
	if r.Status != nil {
		status = strconv.Itoa(*r.Status)
	}
	feedback := r.LastCompileOutput
	if feedback == "" && r.SetsErrorCount != nil && *r.SetsErrorCount > 0 {
		feedback = fmt.Sprintf("evaluation reported %d failing test sets", *r.SetsErrorCount)
	}
	count, passed := 0, 0
	if r.TestSetsCount != nil {
		count = *r.TestSetsCount
	}
	if count == 0 {
		count = len(r.TestSets)
	}
	for _, test := range r.TestSets {
		if test.Result != nil && *test.Result {
			passed++
		}
	}
	for _, test := range r.TestSets {
		if test.IsPublic && test.Result != nil && !*test.Result {
			actual := ""
			if test.ActualOutput != nil {
				actual = *test.ActualOutput
			}
			feedback += fmt.Sprintf("\nPublic test: input=%q expected=%q actual=%q", test.Input, test.Output, actual)
		}
	}
	return Evaluation{Completed: r.Completed, Passed: r.Passed, Status: status, Feedback: feedback, TestCount: count, PassedCount: passed, SavedFilesHash: hash, SubmittedFilesHash: hash}, nil
}

func (a *EducoderAdapter) Progress(ctx context.Context, target Target) (Progress, error) {
	p, err := a.Client.HomeworkProgress(ctx, target.Course, target.HomeworkID)
	if err != nil {
		return Progress{}, err
	}
	statuses := make([]int, 0, len(p.Challenges))
	for _, c := range p.Challenges {
		statuses = append(statuses, c.PassedStatus)
	}
	return Progress{PassedCount: p.PassedCount, ChallengeStatuses: statuses}, nil
}
