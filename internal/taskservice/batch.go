package taskservice

import (
	"context"
	"educoder-cli/internal/answer"
	"educoder-cli/internal/educoder"
	"educoder-cli/internal/solver"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const batchBudgetMicroUSD int64 = 500000 // $0.50 across the whole homework

type BatchLevel struct {
	Position      int    `json:"position"`
	ChallengeID   int64  `json:"challenge_id"`
	Title         string `json:"title"`
	Kind          string `json:"kind"`
	Status        string `json:"status"`
	Score         string `json:"score,omitempty"`
	MaxScore      string `json:"max_score,omitempty"`
	ModelCalls    int    `json:"model_calls"`
	MaxModelCalls int    `json:"max_model_calls"`
	ReasonCode    string `json:"reason_code,omitempty"`
}

type BatchResult struct {
	Status           string       `json:"status"`
	Kind             string       `json:"kind"`
	Scope            string       `json:"scope"`
	TotalLevels      int          `json:"total_levels"`
	CompletedLevels  int          `json:"completed_levels"`
	SkippedLevels    int          `json:"skipped_levels"`
	FailedLevels     int          `json:"failed_levels"`
	CurrentPosition  int          `json:"current_position,omitempty"`
	CurrentTitle     string       `json:"current_title,omitempty"`
	ModelCalls       int          `json:"model_calls"`
	CostMicroUSD     int64        `json:"cost_micro_usd"`
	ReservedMicroUSD int64        `json:"reserved_micro_usd,omitempty"`
	Levels           []BatchLevel `json:"levels"`
	Diagnostics      []string     `json:"diagnostics,omitempty"`
	ReasonCode       string       `json:"reason_code,omitempty"`
}

// RunHomework executes every level in the selected homework in one process.
// It performs a complete, read-only roster preflight before invoking Run; a
// malformed or partial roster therefore cannot cause an accidental write.
func (s *Service) RunHomework(ctx context.Context, client *educoder.Client, req Request, report func(Progress)) (*BatchResult, error) {
	if s == nil || client == nil || s.store == nil {
		return nil, errors.New("task service requires store and authenticated client")
	}
	if req.Owner == "" || req.Course == "" || req.HomeworkID <= 0 {
		return nil, errors.New("task owner and target are required")
	}
	if req.Mode != solver.ModeSubmit {
		return nil, errors.New("homework batch supports submit mode only")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	out := &BatchResult{Kind: "homework", Scope: "homework", Status: "running"}
	emit := func(p BatchProgress, phase, msg string) {
		if report != nil {
			p2 := p
			report(Progress{Phase: phase, Kind: "homework", Message: msg, Batch: &p2})
		}
	}
	emit(BatchProgress{}, "loading", "正在读取作业关卡清单")
	hws, err := client.Homeworks(ctx, req.Course, "")
	if err != nil {
		return nil, err
	}
	var hw *educoder.Homework
	for i := range hws {
		if hws[i].ID == req.HomeworkID {
			x := hws[i]
			hw = &x
			break
		}
	}
	if hw == nil {
		return nil, fmt.Errorf("课堂中没有作业 %d", req.HomeworkID)
	}
	if !hw.IsShixun || hw.ShixunIdentifier == "" {
		out.Status = "unsupported_roster"
		return out, errors.New("selected homework is not a shixun")
	}
	roster, err := client.HomeworkRoster(ctx, req.Course, *hw)
	if errors.Is(err, educoder.ErrHomeworkRuntimeMissing) {
		// Enter is allowed only after the selected homework has been positively
		// resolved from Homeworks; it creates the runtime needed by read APIs.
		t, enterErr := client.Enter(ctx, req.Course, req.HomeworkID, hw.ShixunIdentifier)
		if enterErr != nil {
			return nil, enterErr
		}
		hw.MyShixunIdentifier = t.MyShixun.Identifier
		if hw.MyShixunIdentifier == "" {
			return nil, errors.New("entered homework did not return a runtime")
		}
		roster, err = client.HomeworkRoster(ctx, req.Course, *hw)
	}
	if err != nil {
		return &BatchResult{Kind: "homework", Scope: "homework", Status: "unsupported_roster", Diagnostics: []string{err.Error()}}, err
	}
	if len(roster.Levels) == 0 || len(roster.Levels) > 100 {
		out.Status = "unsupported_roster"
		return out, errors.New("homework roster is empty or exceeds 100 levels")
	}
	sort.Slice(roster.Levels, func(i, j int) bool { return roster.Levels[i].Position < roster.Levels[j].Position })
	out.TotalLevels = len(roster.Levels)
	out.Levels = make([]BatchLevel, 0, len(roster.Levels))
	for _, l := range roster.Levels {
		out.Levels = append(out.Levels, BatchLevel{Position: l.Position, ChallengeID: l.ChallengeID, Title: l.Title, Status: "planned", MaxModelCalls: s.cfg.MaxModelCalls})
	}
	// Load and validate every planned task before the first solver call. This
	// is deliberately separate from the per-level guard: a stale or partial
	// roster must fail atomically before any save/submit mutation can happen.
	for _, l := range roster.Levels {
		t, e := client.CollectionLoadTask(ctx, req.Course, req.HomeworkID, l.Game)
		if e != nil {
			out.Status = "unsupported_roster"
			out.Diagnostics = append(out.Diagnostics, e.Error())
			return out, e
		}
		if e = roster.ValidateTask(t, l); e != nil {
			out.Status = "unsupported_roster"
			out.Diagnostics = append(out.Diagnostics, e.Error())
			return out, e
		}
	}
	// Re-read progress for preflight and use only exact challenge IDs. This is
	// also the baseline for skip decisions and catches stale roster changes.
	progress, err := client.HomeworkProgress(ctx, req.Course, req.HomeworkID)
	if err != nil {
		return nil, err
	}
	full := map[int64]educoder.ChallengeProgress{}
	for _, p := range progress.Challenges {
		if p.HasFullScore() {
			full[p.ChallengeID] = p
		}
	}
	for i, l := range roster.Levels {
		if p, ok := full[l.ChallengeID]; ok {
			out.Levels[i].Status = "skipped"
			out.Levels[i].Kind = "verified"
			out.Levels[i].Score = p.GameScore
			out.Levels[i].MaxScore = p.ChallengeScore
			out.SkippedLevels++
			out.CompletedLevels++
		}
	}
	emit(BatchProgress{TotalLevels: out.TotalLevels, CompletedLevels: out.CompletedLevels, SkippedLevels: out.SkippedLevels}, "preflight", "已确认作业关卡身份和初始满分状态")
	for i, l := range roster.Levels {
		if out.Levels[i].Status == "skipped" {
			continue
		}
		if err := ctx.Err(); err != nil {
			out.Status = "canceled"
			out.Diagnostics = append(out.Diagnostics, err.Error())
			return out, err
		}
		// Skip decisions are rechecked immediately before each mutation.
		fresh, e := client.HomeworkProgress(ctx, req.Course, req.HomeworkID)
		if e != nil {
			out.Status = "verification_unconfirmed"
			out.FailedLevels++
			out.Levels[i].Status = "verification_unconfirmed"
			out.Diagnostics = append(out.Diagnostics, e.Error())
			return out, e
		}
		if p, ok := findFull(fresh, l.ChallengeID); ok {
			out.Levels[i].Status = "skipped"
			out.Levels[i].Kind = "verified"
			out.Levels[i].Score = p.GameScore
			out.Levels[i].MaxScore = p.ChallengeScore
			out.SkippedLevels++
			out.CompletedLevels++
			continue
		}
		out.CurrentPosition = l.Position
		out.CurrentTitle = l.Title
		emit(BatchProgress{TotalLevels: out.TotalLevels, CompletedLevels: out.CompletedLevels, SkippedLevels: out.SkippedLevels, FailedLevels: out.FailedLevels, CurrentPosition: l.Position, CurrentTitle: l.Title}, "running", fmt.Sprintf("正在完成第 %d 关：%s", l.Position, l.Title))
		bound, e := client.BindHomeworkLevel(roster, l)
		if e != nil {
			return out, e
		}
		remaining := batchBudgetMicroUSD - out.CostMicroUSD - out.ReservedMicroUSD
		if remaining <= 0 {
			out.Status = "budget_exhausted"
			out.FailedLevels++
			out.Levels[i].Status = "budget_exhausted"
			return out, nil
		}
		current := BatchProgress{TotalLevels: out.TotalLevels, CompletedLevels: out.CompletedLevels, SkippedLevels: out.SkippedLevels, FailedLevels: out.FailedLevels, CurrentPosition: l.Position, CurrentTitle: l.Title}
		single, runErr := s.Run(ctx, bound, Request{Owner: req.Owner, Course: req.Course, HomeworkID: req.HomeworkID, Game: l.Game, Mode: solver.ModeSubmit, BudgetMicroUSD: remaining, IdempotencyKey: fmt.Sprintf("%s:%d", req.IdempotencyKey, l.ChallengeID)}, func(p Progress) { emit(current, p.Phase, p.Message) })
		if single != nil {
			out.ModelCalls += single.ModelCalls
			out.CostMicroUSD += single.CostMicroUSD
			out.ReservedMicroUSD += single.ReservedMicroUSD
			out.Levels[i].Kind = single.Kind
			out.Levels[i].ModelCalls = single.ModelCalls
			out.Levels[i].Score = single.Score
			out.Levels[i].MaxScore = single.MaxScore
		}
		// A returned child result is authoritative. In particular a completed
		// self-test failure or exhausted repair budget is not an unknown submit.
		// Preserve that reason instead of replacing every non-full score with
		// verification_unconfirmed below.
		if runErr != nil || single == nil || single.Status != "passed" {
			out.FailedLevels++
			out.Levels[i].Status = statusOf(single, runErr)
			out.Status = terminalBatchStatus(single, runErr)
			out.ReasonCode = batchFailureReason(single, runErr)
			out.Levels[i].ReasonCode = out.ReasonCode
			out.Diagnostics = append(out.Diagnostics, errorText(runErr))
			return out, runErr
		}
		out.Levels[i].Score = single.Score
		out.Levels[i].MaxScore = single.MaxScore
		verify, e := client.HomeworkProgress(ctx, req.Course, req.HomeworkID)
		if e != nil {
			out.FailedLevels++
			out.Levels[i].Status = "verification_unconfirmed"
			out.Status = "verification_unconfirmed"
			out.Diagnostics = append(out.Diagnostics, e.Error())
			return out, e
		}
		p, ok := findFull(verify, l.ChallengeID)
		if !ok {
			out.FailedLevels++
			out.Levels[i].Status = "verification_unconfirmed"
			out.Status = "verification_unconfirmed"
			return out, errors.New("level did not reach exact full score after submission")
		}
		out.Levels[i].Status = "passed"
		out.Levels[i].Score = p.GameScore
		out.Levels[i].MaxScore = p.ChallengeScore
		out.CompletedLevels++
		emit(BatchProgress{TotalLevels: out.TotalLevels, CompletedLevels: out.CompletedLevels, SkippedLevels: out.SkippedLevels, FailedLevels: out.FailedLevels}, "verified", fmt.Sprintf("第 %d 关已核验满分", l.Position))
	}
	// A level that was full-score during preflight must still be full-score at
	// the end. This catches upstream progress races or a changed assignment
	// before exposing the batch as completely passed.
	finalProgress, finalErr := client.HomeworkProgress(ctx, req.Course, req.HomeworkID)
	if finalErr != nil {
		out.Status = "verification_unconfirmed"
		out.FailedLevels++
		out.Diagnostics = append(out.Diagnostics, finalErr.Error())
		return out, finalErr
	}
	for i, l := range roster.Levels {
		p, ok := findFull(finalProgress, l.ChallengeID)
		if !ok {
			out.Status = "verification_unconfirmed"
			out.FailedLevels++
			out.Levels[i].Status = "verification_unconfirmed"
			out.Diagnostics = append(out.Diagnostics, fmt.Sprintf("第 %d 关最终满分状态未确认", l.Position))
			return out, errors.New("final homework verification did not confirm every planned level")
		}
		out.Levels[i].Score, out.Levels[i].MaxScore = p.GameScore, p.ChallengeScore
	}
	out.CurrentPosition = 0
	out.CurrentTitle = ""
	if out.CompletedLevels == out.TotalLevels && out.FailedLevels == 0 {
		out.Status = "passed"
	} else {
		out.Status = "verification_unconfirmed"
	}
	return out, nil
}

func findFull(p *educoder.HomeworkProgress, id int64) (educoder.ChallengeProgress, bool) {
	for _, x := range p.Challenges {
		if x.ChallengeID == id && x.HasFullScore() {
			return x, true
		}
	}
	return educoder.ChallengeProgress{}, false
}
func statusOf(r *Result, e error) string {
	if r != nil && r.Status != "" {
		return r.Status
	}
	return "failed"
}
func terminalBatchStatus(r *Result, e error) string {
	if r != nil {
		switch r.Status {
		case "uncertain", "verification_unconfirmed", "canceled", "budget_exhausted", "model_error", "selftest_failed", "attempts_exhausted", "invalid_candidate", "unsupported_multifile", "budget_unavailable", "usage_persistence_failed", "verification_persistence_failed":
			return r.Status
		}
	}
	if errors.Is(e, context.Canceled) {
		return "canceled"
	}
	return "failed"
}

// batchFailureReason emits an enum only. Upstream bodies, model responses,
// source code and execution URLs are never copied into a public reason.
func batchFailureReason(r *Result, err error) string {
	var providerErr *answer.Error
	if errors.As(err, &providerErr) {
		switch providerErr.Kind {
		case "truncated_output":
			return "model_output_truncated"
		case "invalid_output_json", "candidate_contract", "objective_candidate_contract":
			return "model_response_invalid"
		case "timeout":
			return "model_timeout"
		case "network_error":
			return "model_network_error"
		case "upstream_error":
			if providerErr.HTTPStatus == 429 {
				return "model_rate_limited"
			}
		}
		return "model_error"
	}
	if r == nil {
		return "execution_failed"
	}
	diagnostics := append([]string(nil), r.Diagnostics...)
	if r.Programming != nil {
		diagnostics = append(diagnostics, r.Programming.Diagnostics...)
	}
	if err != nil {
		diagnostics = append(diagnostics, err.Error())
	}
	for _, diagnostic := range diagnostics {
		switch {
		case strings.Contains(diagnostic, "WebSocket 连接失败"):
			return "selftest_connection_failed"
		case strings.Contains(diagnostic, "ProcessFinished") || strings.Contains(diagnostic, "自测等待结束"):
			return "selftest_stream_unconfirmed"
		case strings.Contains(diagnostic, "editor content"):
			return "editor_changed"
		}
	}
	switch r.Status {
	case "model_error", "selftest_failed", "attempts_exhausted", "verification_unconfirmed", "budget_exhausted", "canceled":
		return r.Status
	case "invalid_candidate":
		return "model_response_invalid"
	case "uncertain":
		return "submission_uncertain"
	default:
		return "execution_failed"
	}
}
func errorText(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}
