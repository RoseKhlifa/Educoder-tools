package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/jobs"
)

// Recovery is a read-before-write continuation, not permission to replay an
// unconfirmed submission. The original retry capability remains unchanged.
func canRecoverJob(job jobs.Job) bool {
	if !isRestartableRequest(job.Request) || canRetryJob(job) {
		return false
	}
	switch job.Status {
	case jobs.Failed, jobs.Canceled, jobs.Interrupted, jobs.Uncertain:
		return true
	}
	return false
}

type recoveryState struct {
	Status           string          `json:"status"`
	ReasonCode       string          `json:"reason_code"`
	Course           string          `json:"course"`
	HomeworkID       int64           `json:"homework_id"`
	ChallengeID      int64           `json:"challenge_id"`
	Result           *recoveryState  `json:"result"`
	Homeworks        []recoveryState `json:"homeworks"`
	Levels           []recoveryState `json:"levels"`
	RecoveryOriginal *recoveryState  `json:"recovery_original"`
}

type recoveryTarget struct {
	course     string
	homeworkID int64
	whole      bool
	challenges map[int64]bool
}

// Only explicit persisted identities may constrain a continuation. Titles and
// row positions are never used to guess which uncertain challenge was written.
func recoveryTargets(original jobs.Job) ([]recoveryTarget, error) {
	var state recoveryState
	if len(original.Result) != 0 && json.Unmarshal(original.Result, &state) != nil {
		return nil, errors.New("原任务结果格式不完整，无法确认待复核关卡身份")
	}
	for depth := 0; state.RecoveryOriginal != nil; depth++ {
		if depth >= 48 {
			return nil, errors.New("复核记录层级过深")
		}
		state = *state.RecoveryOriginal
	}
	targets := map[string]*recoveryTarget{}
	add := func(node recoveryState, course string, homeworkID int64) error {
		if course == "" || homeworkID <= 0 || educoderIdentifier(course) != nil {
			return errors.New("待复核作业缺少确定的课堂或作业身份")
		}
		if original.Request.Course != "" && course != original.Request.Course || original.Request.Scope == "homework" && homeworkID != original.Request.HomeworkID {
			return errors.New("待复核关卡身份与原任务范围不一致")
		}
		key := fmt.Sprintf("%s:%d", course, homeworkID)
		target := targets[key]
		if target == nil {
			if len(targets) >= 100 {
				return errors.New("待复核作业数量超过上限")
			}
			target = &recoveryTarget{course: course, homeworkID: homeworkID, challenges: map[int64]bool{}}
			targets[key] = target
		}
		if node.ChallengeID > 0 {
			target.challenges[node.ChallengeID] = true
		} else {
			target.whole = true
		}
		return nil
	}
	var addRegion func(recoveryState, string, int64) error
	addRegion = func(node recoveryState, course string, homeworkID int64) error {
		if node.Course != "" {
			course = node.Course
		}
		if node.HomeworkID > 0 {
			homeworkID = node.HomeworkID
		}
		if homeworkID > 0 {
			return add(node, course, homeworkID)
		}
		if len(node.Homeworks) == 0 {
			return errors.New("原任务未保留待复核作业身份，已停止继续执行")
		}
		for _, child := range node.Homeworks {
			if err := addRegion(child, course, homeworkID); err != nil {
				return err
			}
		}
		return nil
	}
	var visit func(recoveryState, string, int64, int) (bool, error)
	visit = func(node recoveryState, course string, homeworkID int64, depth int) (bool, error) {
		if depth > 48 {
			return false, errors.New("原任务结果层级过深")
		}
		if node.Course != "" {
			course = node.Course
		}
		if node.HomeworkID > 0 {
			homeworkID = node.HomeworkID
		}
		foundChild := false
		children := append(append([]recoveryState{}, node.Homeworks...), node.Levels...)
		if node.Result != nil {
			children = append(children, *node.Result)
		}
		for _, child := range children {
			found, err := visit(child, course, homeworkID, depth+1)
			if err != nil {
				return false, err
			}
			foundChild = foundChild || found
		}
		uncertain := uncertainJobState(node.Status) || uncertainJobState(node.ReasonCode)
		if uncertain && !foundChild {
			if err := addRegion(node, course, homeworkID); err != nil {
				return false, err
			}
		}
		return uncertain || foundChild, nil
	}
	found, err := visit(state, original.Request.Course, original.Request.HomeworkID, 0)
	if err != nil {
		return nil, err
	}
	if !found {
		// A process can stop during mutation before a child result is written.
		// With no exact child evidence, require the whole recorded scope full.
		if err := addRegion(state, original.Request.Course, original.Request.HomeworkID); err != nil {
			return nil, err
		}
	}
	out := make([]recoveryTarget, 0, len(targets))
	for _, target := range targets {
		out = append(out, *target)
	}
	return out, nil
}

func recoveryPreflight(ctx context.Context, client EducoderClient, original jobs.Job) error {
	targets, err := recoveryTargets(original)
	if err != nil {
		return err
	}
	reader, ok := client.(homeworkProgressReader)
	if !ok {
		return errors.New("当前登录连接不支持只读成绩复核")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	courses, err := client.Courses(ctx, "processing")
	if err != nil {
		return errors.New("重新读取当前账号课堂失败，未继续执行")
	}
	member := map[string]bool{}
	for _, course := range courses {
		if course.Identifier == "" || member[course.Identifier] {
			return errors.New("当前账号课堂身份缺失或重复")
		}
		member[course.Identifier] = true
	}
	resolved := map[string]map[int64]educoder.Homework{}
	for _, target := range targets {
		if !member[target.course] {
			return errors.New("待复核课堂不属于当前账号的有效课堂")
		}
		if resolved[target.course] == nil {
			homeworks, err := client.Homeworks(ctx, target.course, "")
			if err != nil {
				return errors.New("重新读取课堂作业失败，未继续执行")
			}
			resolved[target.course] = map[int64]educoder.Homework{}
			for _, homework := range homeworks {
				if homework.ID <= 0 || resolved[target.course][homework.ID].ID != 0 {
					return errors.New("当前课堂作业身份缺失或重复")
				}
				resolved[target.course][homework.ID] = homework
			}
		}
		homework, exists := resolved[target.course][target.homeworkID]
		if !exists {
			return fmt.Errorf("待复核作业 %d 已不在原课堂，未继续执行", target.homeworkID)
		}
		if homework.IsClosed(time.Now()) {
			continue
		}
		progress, err := reader.HomeworkProgress(ctx, target.course, target.homeworkID)
		if err != nil {
			return fmt.Errorf("作业 %d 最新成绩读取失败，未继续执行", target.homeworkID)
		}
		dto := toHomeworkDTO(homework)
		applyRequiredProgress(&dto, homework, progress)
		if !dto.RequiredKnown {
			return fmt.Errorf("作业 %d 成绩名单不完整或身份不明确，未继续执行", target.homeworkID)
		}
		if target.whole {
			if *dto.RequiredCount != *dto.FinishedRequired {
				return fmt.Errorf("作业 %d 缺少原关卡身份且尚未确认全部必做关卡满分", target.homeworkID)
			}
			continue
		}
		for challengeID := range target.challenges {
			confirmed := false
			for _, challenge := range progress.Challenges {
				if challenge.ChallengeID == challengeID {
					confirmed = challenge.IsOptional() || challenge.HasFullScore()
					break
				}
			}
			if !confirmed {
				return fmt.Errorf("作业 %d 关卡 %d 的提交结果仍未确认满分，未继续执行", target.homeworkID, challengeID)
			}
		}
	}
	return ctx.Err()
}

func recoveryBlocked(original jobs.Job, err error) jobs.Outcome {
	fields := map[string]json.RawMessage{}
	_ = json.Unmarshal(original.Result, &fields)
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	if json.Valid(original.Result) && len(fields["recovery_original"]) == 0 {
		fields["recovery_original"] = original.Result
	}
	fields["status"] = json.RawMessage(`"verification_unconfirmed"`)
	fields["reason_code"] = json.RawMessage(`"recovery_recheck_required"`)
	fields["diagnostics"], _ = json.Marshal([]string{"只读复核未通过；未调用模型或发送新的作业提交。", err.Error()})
	result, _ := json.Marshal(fields)
	return jobs.Outcome{Status: jobs.Failed, ErrorCode: "RECOVERY_RECHECK_REQUIRED", Message: "只读复核尚未通过，保留已完成进度，可再次复核", Result: result}
}

func (s *Server) handleJobRecover(w http.ResponseWriter, r *http.Request, requestID string, session Session, id string) {
	original, err := s.jobs.manager.Get(r.Context(), sessionJobOwner(session), id)
	if err != nil {
		s.jobError(w, requestID, err)
		return
	}
	s.submitJobRecovery(w, r, requestID, session, original)
}

func (s *Server) handleAdminJobRecover(w http.ResponseWriter, r *http.Request, requestID, actor, id string) {
	if s.jobs == nil {
		s.writeError(w, requestID, 503, "SERVICE_UNAVAILABLE", "后台任务服务尚未启动")
		return
	}
	original, err := s.jobs.manager.AdminGet(r.Context(), id)
	if err != nil {
		s.jobError(w, requestID, err)
		return
	}
	session, ok := s.store.sessionForOwner(original.OwnerID)
	if !ok {
		s.writeError(w, requestID, http.StatusConflict, "OWNER_LOGIN_REQUIRED", "请该用户重新登录后再复核继续")
		return
	}
	if err := s.platform.RecordAudit(r.Context(), actor, "job.recover", id, map[string]string{"status": "requested"}); err != nil {
		s.adminError(w, requestID, err)
		return
	}
	s.submitJobRecovery(w, r, requestID, session, original.Job)
}

func (s *Server) submitJobRecovery(w http.ResponseWriter, r *http.Request, requestID string, session Session, original jobs.Job) {
	if err := emptyRetryBody(r); err != nil {
		s.writeError(w, requestID, 400, "INVALID_REQUEST", "复核继续无需提供任务参数")
		return
	}
	if !canRecoverJob(original) {
		s.writeError(w, requestID, 409, "JOB_NOT_RECOVERABLE", "当前任务无需复核继续，请刷新进度")
		return
	}
	if !s.jobs.modelConfigured {
		s.writeError(w, requestID, 503, "SERVICE_UNAVAILABLE", "服务暂时不可用，请稍后再试")
		return
	}
	owner := sessionJobOwner(session)
	req := original.Request
	req.IdempotencyKey = "recover-job:" + original.ID
	job, reused, err := s.jobs.manager.Submit(r.Context(), owner, req, func(ctx context.Context, request jobs.Request, report func(jobs.Event)) (jobs.Outcome, error) {
		return s.runAuthorized(ctx, owner, func(ctx context.Context) (jobs.Outcome, error) {
			report(jobs.Event{Phase: "reconciling", Message: "正在只读复核上次提交结果，尚未开始新的提交"})
			if !session.ExpiresAt.After(s.store.now()) {
				return recoveryBlocked(original, errors.New("用户登录已过期，请重新登录后再次复核")), nil
			}
			if err := recoveryPreflight(ctx, session.Client, original); err != nil {
				return recoveryBlocked(original, err), nil
			}
			report(jobs.Event{Phase: "loading", Message: "已确认上次不确定关卡满分或无需安排，正在继续剩余作业"})
			out, err := s.jobs.run(ctx, session.Client, owner, request, func(event jobs.Event) {
				if p := event.Progress; p != nil && p.CurrentCourse != "" && p.CurrentHomeworkID > 0 && strings.TrimSpace(p.CurrentHomeworkTitle) != "" {
					s.rememberHomeworkLabels(ctx, p.CurrentCourse, []educoder.Homework{{ID: p.CurrentHomeworkID, Name: p.CurrentHomeworkTitle}})
				}
				report(event)
			})
			s.rememberResultLabels(ctx, request, out.Result, false)
			return out, err
		})
	})
	if err != nil {
		s.jobError(w, requestID, err)
		return
	}
	w.Header().Set("Location", "/api/jobs/"+job.ID)
	if reused {
		w.Header().Set("X-Job-Reused", "true")
	}
	s.writeJSON(w, requestID, http.StatusAccepted, toConsumerJob(job))
}
