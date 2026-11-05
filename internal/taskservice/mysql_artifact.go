package taskservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/questionbank"
	"educoder-cli/internal/solver"
)

type mysqlArtifactState struct {
	Result       Result        `json:"result"`
	Target       solver.Target `json:"target"`
	Mode         solver.Mode   `json:"mode"`
	PlanSource   string        `json:"plan_source"`
	SchemaSHA256 string        `json:"schema_sha256"`
}

func (s *Service) runMySQLArtifact(ctx context.Context, client *educoder.Client, req Request, task *educoder.Task, emit func(string, string)) (*Result, error) {
	if task == nil || !task.IsMySQLArtifactTask() {
		return nil, errors.New("not a supported MySQL artifact task")
	}
	if req.Mode != solver.ModeSubmit {
		return &Result{Status: "unsupported_mode", Kind: "mysql_artifact", Diagnostics: []string{"mysql_artifact_requires_submit"}}, nil
	}
	target := solver.Target{Course: req.Course, HomeworkID: req.HomeworkID, Game: req.Game}
	if req.IdempotencyKey != "" {
		old, err := s.store.FindRunByIdempotency(ctx, ownerHash(req.Owner), req.IdempotencyKey)
		if err == nil {
			var state mysqlArtifactState
			if e := json.Unmarshal(old.Data, &state); e != nil {
				return nil, e
			}
			if state.Target != target || state.Mode != req.Mode {
				return nil, errors.New("idempotency key belongs to another MySQL target")
			}
			return &state.Result, nil
		}
		if !errors.Is(err, questionbank.ErrNotFound) {
			return nil, err
		}
	}
	emit("loading_resources", "正在读取已核验的 MySQL 结构参考")
	targetPath, err := educoder.MySQLArtifactTarget(task)
	if err != nil {
		return nil, err
	}
	entries, err := s.store.ExternalEntries(ctx)
	if err != nil {
		return nil, err
	}
	var refs []educoder.MySQLArtifactReference
	for _, entry := range entries {
		if entry.Kind != "diagram" || entry.Status != "reference" || strings.TrimSpace(entry.Experiment) != strings.TrimSpace(task.Shixun.Name) || strings.TrimSpace(entry.Title) != strings.TrimSpace(task.Challenge.Subject) {
			continue
		}
		for _, file := range entry.Files {
			if file.Path == targetPath {
				refs = append(refs, educoder.MySQLArtifactReference{Experiment: entry.Experiment, Title: entry.Title, Position: entry.Position, Path: file.Path, Content: file.Content, Source: fmt.Sprintf("reference:%d", entry.ID)})
			}
		}
	}
	if len(refs) != 1 {
		return &Result{Status: "reference_unavailable", Kind: "mysql_artifact", Diagnostics: []string{"exact_mysql_reference_not_unique"}}, errors.New("没有唯一匹配的 MySQL 结构参考")
	}
	plan, err := educoder.BuildMySQLArtifactPlan(task, refs[0])
	if err != nil {
		return nil, err
	}
	initialArtifact := &educoder.MySQLArtifactResult{SchemaSHA256: plan.SchemaSHA256, Source: plan.Source, Stage: &educoder.MySQLArtifactStage{}}
	result := &Result{RunID: runID(), Status: "running", Kind: "mysql_artifact", Source: plan.Source, MySQLArtifact: initialArtifact}
	state := mysqlArtifactState{Result: *result, Target: target, Mode: req.Mode, PlanSource: plan.Source, SchemaSHA256: plan.SchemaSHA256}
	raw, _ := json.Marshal(state)
	created := time.Now().UTC()
	if err = s.store.PutRun(ctx, questionbank.RunRecord{ID: result.RunID, Owner: ownerHash(req.Owner), IdempotencyKey: req.IdempotencyKey, Status: "running", Data: raw, CreatedAt: created, UpdatedAt: created}); err != nil {
		return result, err
	}
	emit("running", "正在独立创建 MySQL 表并执行 DESCRIBE")
	protocol := educoder.NewGraphicProtocol(client, nil)
	artifact, runErr := protocol.RunMySQLArtifact(ctx, task, plan, educoder.PollOptions{})
	if artifact != nil {
		result.MySQLArtifact = artifact
	}
	if artifact != nil && artifact.Evaluation != nil && artifact.Evaluation.Completed && !artifact.Evaluation.Passed {
		result.Status = "failed"
		result.Diagnostics = []string{"mysql_formal_evaluation_failed"}
	} else if artifact != nil && artifact.Evaluation != nil && artifact.Evaluation.Passed && artifact.Confirmed && runErr == nil {
		result.Status = "passed"
	} else if runErr != nil {
		result.Status = mysqlFailureStatus(artifact, runErr)
		result.Diagnostics = []string{runErr.Error()}
	} else {
		result.Status = "verification_unconfirmed"
		result.Diagnostics = []string{"mysql_formal_result_not_confirmed"}
	}
	state.Result = *result
	raw, _ = json.Marshal(state)
	persistErr := s.store.PutRun(ctx, questionbank.RunRecord{ID: result.RunID, Owner: ownerHash(req.Owner), IdempotencyKey: req.IdempotencyKey, Status: result.Status, Data: raw, CreatedAt: created, UpdatedAt: time.Now().UTC()})
	if persistErr != nil {
		return result, errors.Join(runErr, persistErr)
	}
	return result, runErr
}

// mysqlFailureStatus is deliberately conservative. A request that may have
// reached the platform remains uncertain even when its response was lost.
func mysqlFailureStatus(artifact *educoder.MySQLArtifactResult, runErr error) string {
	if runErr == nil {
		return "failed"
	}
	if artifact == nil || artifact.Stage == nil {
		return "error"
	}
	s := artifact.Stage
	if s.ExecutionAttempted && !s.ExecutionConfirmed || s.PreparationAttempted && !s.PreparationConfirmed || s.BuildAttempted && !s.BuildAccepted || s.EvaluationAttempted && !s.EvaluationObserved {
		return "uncertain"
	}
	return "error"
}
