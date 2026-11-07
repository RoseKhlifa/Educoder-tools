package taskservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/questionbank"
)

const noProgressFuseTTL = 24 * time.Hour

// noProgressPolicyVersion changes only when fuse semantics change. Ordinary
// patch releases must not silently erase a valid no-progress cooldown.
const noProgressPolicyVersion = "scheduler-policy-2"

type homeworkBlock struct {
	Kind         string    `json:"kind"`
	Course       string    `json:"course"`
	HomeworkID   int64     `json:"homework_id"`
	Reason       string    `json:"reason"`
	Fingerprint  string    `json:"fingerprint"`
	Version      string    `json:"version"`
	BankRevision string    `json:"bank_revision"`
	BlockedAt    time.Time `json:"blocked_at"`
	BlockedUntil time.Time `json:"blocked_until"`
}

func homeworkBlockKey(req Request) string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", req.Owner, req.Course, req.HomeworkID)))
	return "homework-fuse-" + hex.EncodeToString(hash[:])
}

func deterministicHomeworkFailure(reason string) bool {
	switch reason {
	case "attempts_exhausted", "generation_stalled", "selftest_failed", "unsupported_multifile", "model_response_invalid":
		return true
	}
	return false
}

func homeworkProgressFingerprint(hw educoder.Homework, progress *educoder.HomeworkProgress) string {
	if _, _, valid := educoder.RequiredHomeworkCounts(hw, progress); !valid {
		return ""
	}
	levels := append([]educoder.ChallengeProgress(nil), progress.Challenges...)
	sort.Slice(levels, func(i, j int) bool { return levels[i].ChallengeID < levels[j].ChallengeID })
	// Failed evaluations increment EvaluateCount without changing completion.
	// It is intentionally excluded: otherwise a failed attempt defeats its own
	// no-progress fuse. All identity, requirement and score fields remain bound.
	for i := range levels {
		levels[i].EvaluateCount = 0
	}
	raw, _ := json.Marshal(struct {
		Shixun string
		Count  int
		Levels []educoder.ChallengeProgress
	}{hw.ShixunIdentifier, hw.ChallengeCount, levels})
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func (s *Service) homeworkFuse(ctx context.Context, req Request, hw educoder.Homework, progress *educoder.HomeworkProgress, now time.Time) (*homeworkBlock, error) {
	fingerprint := homeworkProgressFingerprint(hw, progress)
	if fingerprint == "" {
		return nil, nil // unknown roster is never completion or fuse evidence
	}
	record, err := s.store.FindRunByIdempotency(ctx, ownerHash(req.Owner), homeworkBlockKey(req))
	if errors.Is(err, questionbank.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var block homeworkBlock
	if err = json.Unmarshal(record.Data, &block); err != nil {
		return nil, err
	}
	if block.Kind != "homework_no_progress" || block.Course != req.Course || block.HomeworkID != req.HomeworkID || !deterministicHomeworkFailure(block.Reason) || block.Version != noProgressPolicyVersion || block.Fingerprint != fingerprint || now.Before(block.BlockedAt) || !now.Before(block.BlockedUntil) {
		return nil, nil
	}
	revision, err := s.store.ReusableEvidenceRevisionForHomework(ctx, ownerHash(req.Owner), req.Course, req.HomeworkID)
	if err != nil {
		return nil, err
	}
	if revision != block.BankRevision {
		return nil, nil
	}
	return &block, nil
}

func (s *Service) rememberHomeworkFailure(ctx context.Context, req Request, hw educoder.Homework, before, after *educoder.HomeworkProgress, batch *BatchResult, now time.Time) error {
	if batch == nil || !deterministicHomeworkFailure(batch.ReasonCode) || batch.Status == "passed" {
		return nil
	}
	fingerprint := homeworkProgressFingerprint(hw, before)
	if fingerprint == "" || fingerprint != homeworkProgressFingerprint(hw, after) {
		return nil
	}
	revision, err := s.store.ReusableEvidenceRevisionForHomework(ctx, ownerHash(req.Owner), req.Course, req.HomeworkID)
	if err != nil {
		return err
	}
	block := homeworkBlock{Kind: "homework_no_progress", Course: req.Course, HomeworkID: req.HomeworkID, Reason: batch.ReasonCode, Fingerprint: fingerprint, Version: noProgressPolicyVersion, BankRevision: revision, BlockedAt: now, BlockedUntil: now.Add(noProgressFuseTTL)}
	raw, err := json.Marshal(block)
	if err != nil {
		return err
	}
	key := homeworkBlockKey(req)
	return s.store.PutRun(ctx, questionbank.RunRecord{ID: key, Owner: ownerHash(req.Owner), IdempotencyKey: key, Status: "blocked_deterministic", Data: raw, CreatedAt: now, UpdatedAt: now})
}

// guardHomeworkUnknown applies to direct single-homework runs too. It returns
// only the exact runtime games whose formal write outcome is still unknown.
// Callers must hold those levels while continuing unrelated levels in the same
// homework. Self-test/pre-write failures are intentionally omitted by the
// question-bank evidence classifier and never block a new mutation.
func (s *Service) guardHomeworkUnknown(ctx context.Context, req Request, roster *educoder.HomeworkRoster, progress *educoder.HomeworkProgress) (map[string]bool, error) {
	runs, err := s.store.UnresolvedHomeworkRuns(ctx, ownerHash(req.Owner), req.Course, req.HomeworkID)
	if err != nil {
		return nil, err
	}
	blocked := make(map[string]bool)
	for _, run := range runs {
		if !run.FormalUnknown {
			continue
		}
		resolved := false
		for _, level := range roster.Levels {
			if level.Game != run.Game || level.RosterIssue != "" {
				continue
			}
			if progressOptional(progress, level.ChallengeID) {
				resolved = true
			} else if _, full := findFull(progress, level.ChallengeID); full {
				resolved = true
			}
			break
		}
		if resolved {
			continue
		}
		if run.Game == "" {
			// A formal unknown without a stable game cannot be mapped to one
			// level. Keep the fail-closed whole-homework behavior rather than
			// risking a duplicate write to an unidentified runtime.
			return nil, errors.New("prior formal submission outcome has no stable level identity")
		}
		blocked[run.Game] = true
	}
	return blocked, nil
}

func mayContinueAfterLevel(task *educoder.Task, result *Result, err error) bool {
	if task == nil || !task.CanContinueAfterConfirmedFailure() || result == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if err != nil {
		var providerErr *answer.Error
		if !errors.As(err, &providerErr) && !graphicFailureBeforeWrite(result, err) {
			// An untyped error may represent an unknown formal write or persistence
			// failure. Only confirmed model failures and typed pre-write graphic
			// failures can continue to another independent level.
			return false
		}
	}
	switch batchFailureReason(result, err) {
	case "editor_changed", "submission_uncertain", "verification_unconfirmed", "budget_exhausted", "canceled":
		return false
	}
	if result.Programming != nil {
		p := result.Programming
		if p.Status == "uncertain" || p.Status == "verification_unconfirmed" || (p.Evaluation != nil && !p.Evaluation.Completed) {
			return false
		}
	}
	switch result.Status {
	case "attempts_exhausted", "selftest_failed", "generation_stalled", "model_error", "invalid_candidate", "resource_unavailable", "prewrite_error", "save_unverified", "saved_not_submitted", "save_error":
		return true
	}
	return false
}

// graphicFailureBeforeWrite is deliberately narrow. Quota/resource errors and
// the explicit pre-write/save stages prove that no formal build was sent, so
// another level may run and the failed level can be retried once later. A
// build-sent/accepted mutation remains fail-closed because replaying it could
// duplicate an upstream submission.
func graphicFailureBeforeWrite(result *Result, err error) bool {
	var mutation *educoder.GraphicMutationError
	if errors.As(err, &mutation) {
		switch mutation.Stage {
		case educoder.GraphicStagePrewrite, educoder.GraphicStageSaveUnconfirmed, educoder.GraphicStageSavedNotSubmitted:
			return true
		default:
			return false
		}
	}
	var quota *educoder.GraphicEnvironmentQuotaError
	var resource *educoder.GraphicRuntimeResourceError
	if errors.As(err, &quota) || errors.As(err, &resource) {
		return true
	}
	if result != nil && result.Programming != nil && result.Programming.Evaluation != nil {
		switch result.Programming.Evaluation.Status {
		case "prewrite_error", "resource_unavailable", "saved_not_submitted", "save_unverified", "save_error", "editor_changed":
			return true
		}
	}
	return false
}
