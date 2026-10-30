package solver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

// ReconcileClient recovers a completed build whose polling response was
// uncertain. All upstream operations are reads. It never starts a build, saves
// code or invokes the model, and it preserves the original unknown evidence.
func (s *Service) ReconcileClient(ctx context.Context, client *educoder.Client, owner, runID string) (*Result, error) {
	if s == nil || s.store == nil || client == nil {
		return nil, errors.New("reconciliation requires a store and authenticated client")
	}
	record, err := s.store.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	var state runState
	if err := json.Unmarshal(record.Data, &state); err != nil {
		return nil, err
	}
	if owner == "" || ownerKey(owner) != state.Owner {
		return nil, errors.New("reconciliation owner does not match saved run")
	}
	result := &state.Result
	if record.Status == "passed" {
		return result, nil
	}
	eval := result.Evaluation
	// EducoderAdapter uses status=error only after SaveCode and BuildCode
	// succeed and WaitEvaluation fails. save_error/build_error cannot qualify.
	if record.Status != "uncertain" || eval == nil || eval.Status != "error" || result.CandidateID <= 0 || result.CodeHash == "" || eval.SavedFilesHash != result.CodeHash {
		return result, errors.New("run has no saved-code-bound uncertain build to reconcile")
	}
	candidates, err := s.store.Lookup(ctx, state.SnapshotRef)
	if err != nil {
		return result, err
	}
	var candidate *questionbank.Candidate
	for i := range candidates {
		if candidates[i].ID == result.CandidateID {
			candidate = &candidates[i]
			break
		}
	}
	if candidate == nil || candidate.Ref != state.SnapshotRef || candidate.FilesHash != result.CodeHash {
		return result, errors.New("saved run candidate does not match its problem contract and code hash")
	}
	candidateHash, err := problem.HashFiles(candidate.Files)
	if err != nil || candidateHash != result.CodeHash {
		return result, errors.New("candidate source does not match saved code hash")
	}
	boundUnknown := false
	for _, evidence := range candidate.Validations {
		if evidence.RunID == runID && evidence.Kind == "formal" && evidence.Result == "unknown" && evidence.FilesHash == result.CodeHash && evidence.SavedFilesHash == result.CodeHash {
			boundUnknown = true
			break
		}
	}
	if !boundUnknown {
		return result, errors.New("run has no matching uncertain formal verification")
	}
	adapter, _ := NewEducoderAdapter(client)
	discovery, err := adapter.Load(ctx, state.Target)
	if err != nil {
		return result, err
	}
	unlock, err := s.lock(ctx, discovery.Key)
	if err != nil {
		return result, err
	}
	defer unlock()
	loaded, err := adapter.Load(ctx, state.Target)
	if err != nil {
		return result, err
	}
	if loaded.Key != discovery.Key {
		return result, errors.New("runtime changed while acquiring reconciliation lock")
	}
	snapshot, err := problem.Prepare(loaded.Snapshot)
	if err != nil {
		return result, err
	}
	if (questionbank.Ref{RevisionHash: snapshot.RevisionHash, ContractHash: snapshot.ContractHash}) != state.SnapshotRef {
		return result, errors.New("current problem revision or execution contract differs from saved run")
	}
	if err := matchingSnapshotHash(snapshot, result.CodeHash); err != nil {
		return result, err
	}
	task, err := client.LoadTask(ctx, state.Target.Course, state.Target.HomeworkID, state.Target.Game)
	if err != nil {
		return result, err
	}
	if task.Game.Status != 2 || len(task.TestSets) == 0 {
		return result, errors.New("fresh task does not show a completed passing evaluation")
	}
	for _, test := range task.TestSets {
		if test.Result == nil || !*test.Result || test.CompileSuccess == nil || *test.CompileSuccess != 1 {
			return result, errors.New("fresh evaluation does not confirm every returned test compiled and passed")
		}
	}
	progress, err := client.HomeworkProgress(ctx, state.Target.Course, state.Target.HomeworkID)
	if err != nil {
		return result, err
	}
	fullScore := false
	for _, challenge := range progress.Challenges {
		if challenge.ChallengeID == task.Challenge.ID {
			fullScore = challenge.HasFullScore()
			break
		}
	}
	if !fullScore {
		return result, errors.New("current challenge full score is not confirmed")
	}
	// Re-read the exact bytes after score/evaluation reads. A full-score record
	// from older code must never promote a different current editor candidate.
	finalFiles, err := client.ReadProgramming(ctx, task)
	if err != nil {
		return result, err
	}
	files := make([]problem.SolutionFile, 0, len(finalFiles.Files))
	for _, file := range finalFiles.Files {
		files = append(files, problem.SolutionFile{Path: file.Path, Content: file.Content})
	}
	finalHash, err := problem.HashFiles(files)
	if err != nil || finalHash != result.CodeHash {
		return result, errors.New("current source changed during reconciliation")
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	evidence := questionbank.Validation{CandidateID: candidate.ID, Kind: "formal", Result: "passed", FilesHash: finalHash, SavedFilesHash: finalHash, SubmittedFilesHash: finalHash, RevisionHash: state.SnapshotRef.RevisionHash, ContractHash: state.SnapshotRef.ContractHash, Feedback: "Read-only reconciliation confirmed the saved build: exact source, completed passing tests, and full challenge score.", RunID: runID, Terminal: true, Complete: true, TestCount: len(task.TestSets), PassedCount: len(task.TestSets)}
	if err := s.store.RecordValidation(cleanup, evidence); err != nil {
		return result, fmt.Errorf("persist reconciliation evidence: %w", err)
	}
	result.Status = "passed"
	result.Evaluation = &Evaluation{Completed: true, Passed: true, Status: "2", Feedback: evidence.Feedback, TestCount: evidence.TestCount, PassedCount: evidence.PassedCount, SavedFilesHash: finalHash, SubmittedFilesHash: finalHash}
	statuses := make([]int, 0, len(progress.Challenges))
	for _, challenge := range progress.Challenges {
		statuses = append(statuses, challenge.PassedStatus)
	}
	result.Progress = &Progress{PassedCount: progress.PassedCount, ChallengeStatuses: statuses}
	result.Diagnostics = append(result.Diagnostics, "polling uncertainty reconciled with read-only fresh evaluation, full score and matching source")
	state.Feedback = nil
	state.Result = *result
	if err := s.persist(cleanup, runID, state, "passed", record.CreatedAt); err != nil {
		return result, fmt.Errorf("persist reconciled run: %w", err)
	}
	return result, nil
}

func matchingSnapshotHash(snapshot problem.Snapshot, expected string) error {
	files := make([]problem.SolutionFile, 0, len(snapshot.Files))
	for _, file := range snapshot.Files {
		files = append(files, problem.SolutionFile{Path: file.Path, Content: file.CurrentContent})
	}
	hash, err := problem.HashFiles(files)
	if err != nil || hash != expected {
		return errors.New("current editor source does not match the uncertain build candidate")
	}
	return nil
}
