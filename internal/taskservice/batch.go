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
// It validates the canonical assignment and every existing runtime before Run.
// Upstream can defer creating later games; those stable planned identities are
// resolved only when reached, then fully validated before any answer write.
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
	budget := batchBudgetMicroUSD
	if req.BudgetMicroUSD > 0 && req.BudgetMicroUSD < budget {
		budget = req.BudgetMicroUSD
	}
	out := &BatchResult{Kind: "homework", Scope: "homework", Status: "running"}
	emit := func(p BatchProgress, phase, msg string) {
		if report != nil {
			p2 := p
			report(Progress{Phase: phase, Kind: "homework", Message: msg, Batch: &p2})
		}
	}
	emit(BatchProgress{}, "loading", "正在读取作业关卡清单")
	var hw *educoder.Homework
	if req.Homework != nil {
		// Course batches already resolved this exact homework from the
		// authenticated list. Copy it so later roster/enter code cannot mutate
		// the caller's plan.
		copy := *req.Homework
		hw = &copy
	} else {
		hws, err := client.Homeworks(ctx, req.Course, "")
		if err != nil {
			return nil, err
		}
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
	}
	// The assignment can still be present in the upstream history list after
	// its deadline. Do this check before roster resolution or Enter so a direct
	// homework request cannot create a runtime or reach a mutating operation.
	if hw.IsClosed(time.Now()) {
		return &BatchResult{Kind: "homework", Scope: "homework", Status: "deadline_passed", ReasonCode: "deadline_passed"}, nil
	}
	if !hw.IsShixun || hw.ShixunIdentifier == "" {
		out.Status = "unsupported_roster"
		return out, errors.New("selected homework is not a shixun")
	}
	roster, err := client.HomeworkRoster(ctx, req.Course, *hw)
	if errors.Is(err, educoder.ErrHomeworkRuntimeMissing) {
		// A purely elective assignment does not require a runtime at all.
		// Read its canonical requirement flags before Enter creates one.
		progress, progressErr := client.HomeworkProgress(ctx, req.Course, req.HomeworkID)
		if progressErr != nil {
			return nil, progressErr
		}
		if count, valid := requiredHomeworkCount(*hw, progress); valid && count == 0 {
			return &BatchResult{Kind: "homework", Scope: "homework", Status: "passed", ReasonCode: "no_required_levels", Levels: []BatchLevel{}}, nil
		}
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
	var partialRoster *educoder.HomeworkRosterPartialError
	if err != nil && (!errors.As(err, &partialRoster) || roster == nil) {
		return &BatchResult{Kind: "homework", Scope: "homework", Status: "unsupported_roster", Diagnostics: []string{err.Error()}}, err
	}
	if partialRoster != nil {
		for _, diagnostic := range partialRoster.Diagnostics {
			if strings.Contains(diagnostic, "homework runtime mapping is incomplete") {
				return &BatchResult{Kind: "homework", Scope: "homework", Status: "unsupported_roster", Diagnostics: partialRoster.Diagnostics}, err
			}
		}
	}
	if len(roster.Levels) == 0 || len(roster.Levels) > 100 {
		out.Status = "unsupported_roster"
		return out, errors.New("homework roster is empty or exceeds 100 levels")
	}
	sort.Slice(roster.Levels, func(i, j int) bool { return roster.Levels[i].Position < roster.Levels[j].Position })
	levels := make([]educoder.HomeworkLevel, 0, len(roster.Levels))
	tasks := map[int64]*educoder.Task{}
	out.Levels = []BatchLevel{}
	// A read-only precheck failure excludes only this exact target. It never
	// binds a solver client or chooses an ambiguous runtime identity.
	for _, l := range roster.Levels {
		if l.Optional {
			continue
		}
		entry := BatchLevel{Position: l.Position, ChallengeID: l.ChallengeID, Title: l.Title, Status: "planned", MaxModelCalls: s.cfg.MaxModelCalls}
		precheckErr := error(nil)
		if l.RosterIssue != "" {
			entry.Status, entry.ReasonCode = "precheck_failed", "runtime_roster_ambiguous"
			precheckErr = errors.New(l.RosterIssue)
		} else if !l.RuntimePending {
			t, readErr := client.CollectionLoadTask(ctx, req.Course, req.HomeworkID, l.Game)
			precheckErr = readErr
			if precheckErr == nil {
				precheckErr = roster.ValidateTask(t, l)
			}
			if errors.Is(precheckErr, educoder.ErrOptionalTask) {
				continue
			}
			if precheckErr == nil {
				tasks[l.ChallengeID] = t
			} else {
				entry.Status, entry.ReasonCode = "precheck_failed", "task_precheck_failed"
				if errors.Is(precheckErr, educoder.ErrHomeworkTaskIdentity) {
					entry.ReasonCode = "task_identity_mismatch"
				} else if errors.Is(precheckErr, educoder.ErrHomeworkTaskKind) {
					entry.ReasonCode = "unsupported_question_kind"
				}
			}
		}
		if precheckErr != nil {
			if ctx.Err() != nil {
				out.Status, out.ReasonCode = "canceled", "canceled"
				return out, ctx.Err()
			}
			out.Status, out.ReasonCode = "unsupported_roster", entry.ReasonCode
			out.Diagnostics = append(out.Diagnostics, fmt.Sprintf("第 %d 关预检未通过：%s", l.Position, precheckErr.Error()))
		}
		levels = append(levels, l)
		out.Levels = append(out.Levels, entry)
	}
	out.TotalLevels = len(out.Levels)
	if out.TotalLevels == 0 {
		out.Status, out.ReasonCode = "passed", "no_required_levels"
		return out, nil
	}
	// Re-read progress for preflight and use only exact challenge IDs. This is
	// also the baseline for skip decisions and catches stale roster changes.
	progress, err := client.HomeworkProgress(ctx, req.Course, req.HomeworkID)
	if err != nil {
		return nil, err
	}
	blockedGames, guardErr := s.guardHomeworkUnknown(ctx, req, roster, progress)
	if guardErr != nil {
		out.Status, out.ReasonCode = "verification_unconfirmed", "submission_uncertain"
		out.Diagnostics = append(out.Diagnostics, guardErr.Error())
		return out, guardErr
	}
	full := map[int64]educoder.ChallengeProgress{}
	for _, p := range progress.Challenges {
		if p.HasFullScore() {
			full[p.ChallengeID] = p
		}
	}
	for _, l := range levels {
		i := batchLevelIndex(out, l.ChallengeID)
		if i < 0 {
			continue
		}
		if blockedGames[l.Game] {
			out.Levels[i].Status = "verification_unconfirmed"
			out.Levels[i].ReasonCode = "submission_uncertain"
			out.FailedLevels++
			out.Diagnostics = append(out.Diagnostics, fmt.Sprintf("第 %d 关存在未核验的正式提交结果，已跳过该关并继续其他关卡", l.Position))
			continue
		}
		if out.Levels[i].Status == "precheck_failed" {
			continue
		}
		if progressOptional(progress, l.ChallengeID) {
			omitBatchLevel(out, i)
			continue
		}
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
	// Keep pre-write graphical failures in a bounded retry queue. Unrelated
	// levels continue immediately; the failed level is retried once after the
	// rest of the batch instead of aborting the homework.
	pendingLevels := append([]educoder.HomeworkLevel(nil), levels...)
	deferredGraphic := make(map[int64]bool)
	for cursor := 0; cursor < len(pendingLevels); cursor++ {
		l := pendingLevels[cursor]
		i := batchLevelIndex(out, l.ChallengeID)
		if i < 0 {
			continue
		}
		if out.Levels[i].Status == "skipped" || out.Levels[i].Status == "precheck_failed" || (blockedGames[l.Game] && out.Levels[i].ReasonCode == "submission_uncertain") {
			continue
		}
		if err := ctx.Err(); err != nil {
			out.Status = "canceled"
			out.Diagnostics = append(out.Diagnostics, err.Error())
			return out, err
		}
		// A long-running batch may cross the deadline between levels. The local
		// deadline is authoritative for admission; stop before loading the next
		// task or issuing another model/write request.
		if hw.IsClosed(time.Now()) {
			out.Status = "deadline_passed"
			out.ReasonCode = "deadline_passed"
			return out, nil
		}
		// Reuse the latest progress snapshot. It is the preflight read for the
		// first level and the post-submit verification read for later levels;
		// this avoids an identical GET immediately before every mutation while
		// the final reconciliation below remains authoritative.
		if progressOptional(progress, l.ChallengeID) {
			omitBatchLevel(out, i)
			continue
		}
		if p, ok := findFull(progress, l.ChallengeID); ok {
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
		if l.RuntimePending {
			emit(BatchProgress{TotalLevels: out.TotalLevels, CompletedLevels: out.CompletedLevels, SkippedLevels: out.SkippedLevels, CurrentPosition: l.Position, CurrentTitle: l.Title}, "initializing", "正在准备尚未创建的关卡")
			resolved, task, resolveErr := client.ResolveHomeworkLevel(ctx, roster, l)
			if errors.Is(resolveErr, educoder.ErrOptionalTask) {
				omitBatchLevel(out, i)
				continue
			}
			if resolveErr != nil {
				out.Status, out.ReasonCode = "runtime_initialization_failed", "runtime_initialization_failed"
				out.FailedLevels++
				out.Levels[i].Status, out.Levels[i].ReasonCode = out.Status, out.ReasonCode
				out.Diagnostics = append(out.Diagnostics, resolveErr.Error())
				return out, resolveErr
			}
			l = resolved
			tasks[l.ChallengeID] = task
		}
		bound, e := client.BindHomeworkLevel(roster, l)
		if e != nil {
			if errors.Is(e, educoder.ErrOptionalTask) {
				omitBatchLevel(out, i)
				continue
			}
			return out, e
		}
		remaining := budget - out.CostMicroUSD - out.ReservedMicroUSD
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
			if single.Kind == "programming" {
				out.Levels[i].MaxModelCalls = s.cfg.MaxModelCalls
			}
			out.Levels[i].ModelCalls = single.ModelCalls
			out.Levels[i].Score = single.Score
			out.Levels[i].MaxScore = single.MaxScore
		}
		if errors.Is(runErr, educoder.ErrOptionalTask) || (single != nil && single.Status == "skipped_optional") {
			omitBatchLevel(out, i)
			continue
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
			appendDiagnostic := func(message string) {
				if strings.TrimSpace(message) == "" {
					return
				}
				for _, existing := range out.Diagnostics {
					if existing == message {
						return
					}
				}
				out.Diagnostics = append(out.Diagnostics, message)
			}
			if single != nil {
				for _, diagnostic := range single.Diagnostics {
					appendDiagnostic(diagnostic)
				}
			}
			if runErr != nil {
				appendDiagnostic(runErr.Error())
			}
			if mayContinueAfterLevel(tasks[l.ChallengeID], single, runErr) {
				// Re-read explicit capabilities after a known failure. A changed
				// or locked task cannot inherit stale preflight permission.
				freshTask, readErr := client.CollectionLoadTask(ctx, req.Course, req.HomeworkID, l.Game)
				if readErr == nil && roster.ValidateTask(freshTask, l) == nil && mayContinueAfterLevel(freshTask, single, runErr) {
					if graphicFailureBeforeWrite(single, runErr) && !deferredGraphic[l.ChallengeID] {
						deferredGraphic[l.ChallengeID] = true
						pendingLevels = append(pendingLevels, l)
						emit(BatchProgress{TotalLevels: out.TotalLevels, CompletedLevels: out.CompletedLevels, SkippedLevels: out.SkippedLevels, FailedLevels: out.FailedLevels}, "service_pending", fmt.Sprintf("第 %d 关图形环境未完成写入，已延后重试并继续后续关卡", l.Position))
					} else {
						emit(BatchProgress{TotalLevels: out.TotalLevels, CompletedLevels: out.CompletedLevels, SkippedLevels: out.SkippedLevels, FailedLevels: out.FailedLevels}, "service_pending", fmt.Sprintf("第 %d 关已记录失败，继续处理后续可访问关卡", l.Position))
					}
					continue
				}
			}
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
		if progressOptional(verify, l.ChallengeID) {
			omitBatchLevel(out, i)
			continue
		}
		p, ok := findFull(verify, l.ChallengeID)
		if !ok {
			out.FailedLevels++
			out.Levels[i].Status = "verification_unconfirmed"
			out.Status = "verification_unconfirmed"
			return out, errors.New("level did not reach exact full score after submission")
		}
		progress = verify
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
	// Reconcile every required row; do not return at the first missing score.
	// Counters are derived exactly once from the final per-level states. This
	// prevents a failed level that is later revisited (or an optional level
	// omitted at the final read) from being counted twice.
	unconfirmed := false
	byChallenge := make(map[int64]BatchLevel, len(out.Levels))
	for _, entry := range out.Levels {
		byChallenge[entry.ChallengeID] = entry
	}
	finalLevels := make([]BatchLevel, 0, len(levels))
	for _, l := range levels {
		entry, ok := byChallenge[l.ChallengeID]
		if !ok || progressOptional(finalProgress, l.ChallengeID) {
			continue
		}
		if entry.Status == "precheck_failed" {
			finalLevels = append(finalLevels, entry)
			continue
		}
		if p, full := findFull(finalProgress, l.ChallengeID); full {
			entry.Score, entry.MaxScore = p.GameScore, p.ChallengeScore
			if entry.Status != "skipped" {
				entry.Status, entry.ReasonCode = "passed", ""
			}
			finalLevels = append(finalLevels, entry)
			continue
		}
		if entry.Status == "passed" || entry.Status == "planned" || entry.Status == "skipped" || entry.Status == "verification_unconfirmed" {
			entry.Status, entry.ReasonCode = "verification_unconfirmed", "verification_unconfirmed"
			unconfirmed = true
			out.Diagnostics = append(out.Diagnostics, fmt.Sprintf("第 %d 关最终满分状态未确认", l.Position))
		}
		finalLevels = append(finalLevels, entry)
	}
	out.Levels = finalLevels
	out.TotalLevels = len(finalLevels)
	out.CompletedLevels, out.SkippedLevels, out.FailedLevels = 0, 0, 0
	for _, entry := range out.Levels {
		switch entry.Status {
		case "passed":
			out.CompletedLevels++
		case "skipped":
			out.CompletedLevels++
			out.SkippedLevels++
		default:
			out.FailedLevels++
		}
	}
	out.CurrentPosition, out.CurrentTitle = 0, ""
	if out.CompletedLevels == out.TotalLevels && out.FailedLevels == 0 {
		out.Status, out.ReasonCode = "passed", ""
		if out.TotalLevels == 0 {
			out.ReasonCode = "no_required_levels"
		}
	} else if unconfirmed {
		out.Status, out.ReasonCode = "verification_unconfirmed", "verification_unconfirmed"
		return out, errors.New("final homework verification did not confirm every planned level")
	} else if out.Status == "running" {
		out.Status, out.ReasonCode = "needs_service", "service_required"
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
		case "uncertain", "verification_unconfirmed", "canceled", "budget_exhausted", "model_error", "generation_stalled", "selftest_failed", "attempts_exhausted", "invalid_candidate", "unsupported_multifile", "budget_unavailable", "usage_persistence_failed", "verification_persistence_failed", "resource_unavailable", "prewrite_error", "saved_not_submitted", "save_unverified", "save_error", "editor_changed":
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
	if r != nil {
		switch r.Status {
		case "generation_stalled":
			return r.Status
		case "prewrite_error", "saved_not_submitted", "save_unverified", "save_error", "editor_changed":
			// Explicit adapter stages are stronger evidence than diagnostic
			// wording: these failures never started a formal evaluation.
			return r.Status
		}
	}
	var imageErr *educoder.QuestionImageError
	if errors.As(err, &imageErr) {
		return "objective_image_unavailable"
	}
	var quotaErr *educoder.GraphicEnvironmentQuotaError
	if errors.As(err, &quotaErr) {
		return "environment_quota_exhausted"
	}
	var resourceErr *educoder.GraphicRuntimeResourceError
	if errors.As(err, &resourceErr) {
		return "resource_unavailable"
	}
	var providerErr *answer.Error
	if errors.As(err, &providerErr) {
		switch providerErr.Kind {
		case "provider_payment_required":
			return "model_provider_payment_required"
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
	hasFormalEvaluation := r.Programming != nil && r.Programming.Evaluation != nil
	for _, diagnostic := range diagnostics {
		switch {
		case !hasFormalEvaluation && strings.Contains(diagnostic, "WebSocket 连接失败"):
			return "selftest_connection_failed"
		case !hasFormalEvaluation && (strings.Contains(diagnostic, "ProcessFinished") || strings.Contains(diagnostic, "自测等待结束")):
			return "selftest_stream_unconfirmed"
		case strings.Contains(diagnostic, "editor content"):
			return "editor_changed"
		}
	}
	// Self-test saves and runs code without starting a formal evaluation. Its
	// unconfirmed stream must not be described as an uncertain submission,
	// regardless of how the protocol error is worded.
	if r.Programming != nil && r.Programming.SelfTestStatus == "unknown" && !hasFormalEvaluation {
		return "selftest_stream_unconfirmed"
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
