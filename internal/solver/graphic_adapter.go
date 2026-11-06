package solver

import (
	"context"
	"errors"
	"path"
	"strconv"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

// Graphic files share the bank's versioned file contract, but use a separate
// runtime adapter. There is no stdout self-test for a StarUML document.
func (a *EducoderAdapter) loadGraphic(ctx context.Context, task *educoder.Task, target Target) (LoadedProblem, error) {
	doc, err := a.graphic.Describe(task)
	if err != nil {
		return LoadedProblem{}, err
	}
	snapshot := problem.Snapshot{
		Kind: "programming", Title: task.Challenge.Subject,
		StatementMarkdown: task.Challenge.Description,
		Files:             []problem.File{{Path: doc.Path, Filename: path.Base(doc.Path), Language: "json", Extension: ".mdj", CurrentContent: doc.Content, StarterProvenance: "unknown"}},
		EditMode:          "graphic", SnapshotCompleteness: "observed",
		EnvironmentContract: []string{"graphic:staruml", "language:json"},
	}
	return LoadedProblem{Snapshot: snapshot, ExternalIdentity: questionbank.ExternalIdentity{Experiment: task.Shixun.Name, Title: task.Challenge.Subject, Position: task.Challenge.Position}, Key: strconv.FormatInt(task.MyShixun.ID, 10), target: target, graphicDocument: doc}, nil
}

func (a *EducoderAdapter) submitGraphic(ctx context.Context, task *educoder.Task, target Target, file problem.SolutionFile) (Evaluation, error) {
	a.mu.Lock()
	doc := a.graphicDocuments[target.Game]
	a.mu.Unlock()
	if doc == nil || doc.Path != file.Path {
		return Evaluation{Status: "save_error"}, errors.New("graphic baseline is not bound to the active solver run")
	}
	if err := educoder.ValidateGraphicContent(file.Content); err != nil {
		return Evaluation{Status: "save_error"}, err
	}
	r, err := a.graphic.Submit(ctx, task, doc, file.Content, educoder.PollOptions{})
	hash, hashErr := problem.HashFiles([]problem.SolutionFile{file})
	if hashErr != nil {
		return Evaluation{Status: "save_error"}, hashErr
	}
	result := Evaluation{Status: "prewrite_error"}
	if r != nil && r.Saved && r.ContentHash == problem.HashContent(file.Content) {
		result.SavedFilesHash = hash
		if r.SubmissionAttempted {
			result.SubmittedFilesHash = hash
		}
	}
	if r != nil {
		switch r.Stage {
		case educoder.GraphicStageSavedNotSubmitted:
			result.Status = "saved_not_submitted"
		case educoder.GraphicStageSaveUnconfirmed:
			result.Status = "save_unverified"
		case educoder.GraphicStageBuildSent, educoder.GraphicStageBuildAccepted, educoder.GraphicStageCompleted:
			result.Status = "error"
		}
	}
	var quota *educoder.GraphicEnvironmentQuotaError
	var resource *educoder.GraphicRuntimeResourceError
	if errors.As(err, &quota) || errors.As(err, &resource) {
		if r == nil || !r.SubmissionAttempted {
			result.Status = "resource_unavailable"
		}
	}
	if err != nil {
		if errors.Is(err, educoder.ErrGraphicChanged) {
			result.Status = "editor_changed"
		}
		return result, err
	}
	if r == nil || r.Evaluation == nil || result.SavedFilesHash == "" {
		return result, errors.New("graphic evaluation has no source-bound result")
	}
	eval := r.Evaluation
	result.Completed = eval.Completed
	result.Passed = eval.Passed && r.Confirmed
	result.Feedback = eval.LastCompileOutput
	if eval.Status != nil {
		result.Status = strconv.Itoa(*eval.Status)
	}
	if eval.TestSetsCount != nil {
		result.TestCount = *eval.TestSetsCount
	}
	for _, test := range eval.TestSets {
		if test.Result != nil && *test.Result && test.CompileSuccess != nil && *test.CompileSuccess == 1 {
			result.PassedCount++
		}
	}
	if eval.Passed && !r.Confirmed {
		result.Completed = false
		return result, errors.New("graphic build has not been confirmed as completed by the task and classroom progress")
	}
	return result, nil
}

// graphicBuildNotSent depends on explicit adapter status, not empty hashes.
// Unknown build delivery deliberately remains status=error and fails closed.
func graphicBuildNotSent(snapshot problem.Snapshot, evaluation Evaluation) bool {
	if snapshot.EditMode != "graphic" {
		return false
	}
	switch evaluation.Status {
	case "prewrite_error", "resource_unavailable", "saved_not_submitted", "save_unverified", "save_error", "editor_changed":
		return true
	}
	return false
}
