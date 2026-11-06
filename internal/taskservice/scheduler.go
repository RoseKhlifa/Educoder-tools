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

	buildinfo "educoder-cli"
	"educoder-cli/internal/answer"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/questionbank"
)

const noProgressFuseTTL = 24 * time.Hour

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
	if block.Kind != "homework_no_progress" || block.Course != req.Course || block.HomeworkID != req.HomeworkID || !deterministicHomeworkFailure(block.Reason) || block.Version != buildinfo.Current() || block.Fingerprint != fingerprint || now.Before(block.BlockedAt) || !now.Before(block.BlockedUntil) {
		return nil, nil
	}
	revision, err := s.store.ReusableEvidenceRevision(ctx)
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
	revision, err := s.store.ReusableEvidenceRevision(ctx)
	if err != nil {
		return err
	}
	block := homeworkBlock{Kind: "homework_no_progress", Course: req.Course, HomeworkID: req.HomeworkID, Reason: batch.ReasonCode, Fingerprint: fingerprint, Version: buildinfo.Current(), BankRevision: revision, BlockedAt: now, BlockedUntil: now.Add(noProgressFuseTTL)}
	raw, err := json.Marshal(block)
	if err != nil {
		return err
	}
	key := homeworkBlockKey(req)
	return s.store.PutRun(ctx, questionbank.RunRecord{ID: key, Owner: ownerHash(req.Owner), IdempotencyKey: key, Status: "blocked_deterministic", Data: raw, CreatedAt: now, UpdatedAt: now})
}

// guardHomeworkUnknown applies to direct single-homework runs too. They bypass
// deterministic no-progress cooldowns, never unresolved mutations. A fresh
// full-score read for the exact target or a durable reconciliation changing
// the old run status is required before a new submit can be admitted.
func (s *Service) guardHomeworkUnknown(ctx context.Context, req Request, roster *educoder.HomeworkRoster, progress *educoder.HomeworkProgress) error {
	runs, err := s.store.UnresolvedHomeworkRuns(ctx, ownerHash(req.Owner), req.Course, req.HomeworkID)
	if err != nil {
		return err
	}
	for _, run := range runs {
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
		if !resolved {
			return errors.New("prior submission outcome requires read-only reconciliation before another homework execution")
		}
	}
	return nil
}

// guardPriorUnknownRuns runs before HomeworkRoster may create a lazy runtime.
// This keeps an unresolved write from being followed by even an incidental
// upstream mutation; the explicit read-only reconciliation endpoint must clear
// the durable run first.
func (s *Service) guardPriorUnknownRuns(ctx context.Context, req Request) error {
	runs, err := s.store.UnresolvedHomeworkRuns(ctx, ownerHash(req.Owner), req.Course, req.HomeworkID)
	if err != nil {
		return err
	}
	if len(runs) > 0 {
		return errors.New("prior submission outcome requires read-only reconciliation before another homework execution")
	}
	return nil
}

func mayContinueAfterLevel(task *educoder.Task, result *Result, err error) bool {
	if !task.CanContinueAfterConfirmedFailure() || result == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if err != nil {
		var providerErr *answer.Error
		if !errors.As(err, &providerErr) {
			// A non-provider error may represent an unknown write or persistence
			// failure. Only typed, confirmed model failures can continue.
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
	case "attempts_exhausted", "selftest_failed", "generation_stalled", "model_error", "invalid_candidate", "resource_unavailable":
		return true
	}
	return false
}
