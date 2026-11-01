package educoder

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func exercisePath(id int64, suffix string) string {
	return "/api/exercises/" + strconv.FormatInt(id, 10) + suffix
}
func examRef(course string, id int64) string {
	ref := "/classrooms/" + course + "/exercise"
	if id > 0 {
		ref += "/" + strconv.FormatInt(id, 10) + "/detail"
	}
	return ref
}
func examQuery(course string, id int64, login string) url.Values {
	q := url.Values{"coursesId": {course}, "categoryId": {strconv.FormatInt(id, 10)}}
	if login != "" {
		q.Set("login", login)
	}
	return q
}
func validateExam(course string, id int64) error {
	if validIdentifier(course) != nil || id <= 0 {
		return &exerciseError{ExerciseIdentityMismatch, "invalid exercise context"}
	}
	return nil
}
func examErr(code ExerciseCode, msg string) error { return &exerciseError{code, msg} }

// exerciseRequest never exposes an upstream message or signed URL. Messages
// can contain identifiers, student answers, tokens or proxy diagnostics.
func (c *Client) exerciseRequest(ctx context.Context, method, path string, q url.Values, body any, ref string, out any, ack bool) error {
	var raw json.RawMessage
	if err := c.request(ctx, method, path, q, body, ref, &raw); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return examErr(ExerciseUpstream, "exercise upstream request failed")
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return examErr(ExerciseUpstream, "exercise response is not an object")
	}
	if ack {
		var status int
		v, ok := obj["status"]
		if !ok || json.Unmarshal(v, &status) != nil || status != 0 {
			return examErr(ExerciseUpstream, "exercise acknowledgement is missing")
		}
	}
	if out != nil && json.Unmarshal(raw, out) != nil {
		return examErr(ExerciseUpstream, "exercise response fields are invalid")
	}
	return nil
}
func knownFields(raw json.RawMessage, names ...string) bool {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return false
	}
	for _, name := range names {
		if v, ok := obj[name]; !ok || string(v) == "null" {
			return false
		}
	}
	return true
}

// Exercises resolves exams from the current account's course route. It does
// not create an attempt, unlock a paper or enter an exam.
func (c *Client) Exercises(ctx context.Context, course string) ([]Exercise, error) {
	if validateExam(course, 1) != nil || c.LoginName == "" {
		return nil, examErr(ExerciseIdentityMismatch, "authenticated exercise context is required")
	}
	result := []Exercise{}
	seen := map[int64]bool{}
	expectedTotal := -1
	for page := 1; page <= 100; page++ {
		var r struct {
			Total     *int       `json:"total_count"`
			Exercises []Exercise `json:"exercises"`
		}
		q := url.Values{"page": {strconv.Itoa(page)}, "limit": {"20"}}
		if err := c.exerciseRequest(ctx, http.MethodGet, "/api/v2/courses/"+course+"/exercises.json", q, nil, examRef(course, 0), &r, false); err != nil {
			return nil, err
		}
		if r.Total == nil || *r.Total < 0 || (expectedTotal >= 0 && expectedTotal != *r.Total) {
			return nil, examErr(ExerciseIdentityMismatch, "exercise list count is missing or changed")
		}
		expectedTotal = *r.Total
		for _, e := range r.Exercises {
			if e.ID <= 0 || e.Name == "" || seen[e.ID] {
				return nil, examErr(ExerciseIdentityMismatch, "exercise list identity is ambiguous")
			}
			seen[e.ID] = true
			result = append(result, e)
		}
		if len(result) > expectedTotal {
			return nil, examErr(ExerciseIdentityMismatch, "exercise list count is inconsistent")
		}
		if len(result) == expectedTotal {
			return result, nil
		}
		if len(r.Exercises) == 0 {
			return nil, examErr(ExerciseIdentityMismatch, "exercise list is truncated")
		}
	}
	return nil, examErr(ExerciseUpstream, "exercise list exceeded page limit")
}
func (c *Client) ExerciseList(ctx context.Context, course string) ([]Exercise, error) {
	return c.Exercises(ctx, course)
}

func (c *Client) ExerciseOverview(ctx context.Context, course string, id int64) (*ExerciseOverview, error) {
	target, err := c.exerciseTarget(ctx, course, id)
	if err != nil {
		return nil, err
	}
	if _, err := c.exerciseActor(ctx, course); err != nil {
		return nil, err
	}
	var header ExerciseOverview
	if err := c.exerciseRequest(ctx, http.MethodGet, exercisePath(id, "/common_header.json"), examQuery(course, id, ""), nil, examRef(course, id), &header, false); err != nil {
		return nil, err
	}
	if header.ID != id || header.ExerciseUserID != target.ExerciseUserID || header.Name != target.Name {
		return nil, examErr(ExerciseIdentityMismatch, "exercise overview identity mismatch")
	}
	if header.TotalQuestions <= 0 || !positiveExerciseScore(header.MaxScore) {
		return nil, examErr(ExerciseUpstream, "exercise overview metadata is incomplete")
	}
	if _, known := exercisePersonalDeadline(header.EndTime); !known {
		return nil, examErr(ExerciseUpstream, "exercise overview deadline is invalid")
	}
	return &header, nil
}

func positiveExerciseScore(raw json.RawMessage) bool {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		value = string(raw)
	}
	parsed, err := strconv.ParseFloat(value, 64)
	return err == nil && parsed > 0 && parsed < 1e9
}

// These flags are intentionally private. A common_header contains many other
// fields, including monitoring credentials, which never enter the public DTO.
type exerciseHeaderProtection struct {
	ID                      int64  `json:"id"`
	ExerciseUserID          int64  `json:"exercise_user_id"`
	OnlyOnClient            bool   `json:"only_on_client"`
	NetLimit                bool   `json:"net_limit"`
	UseWhiteList            bool   `json:"use_white_list"`
	ForbidVirtual           bool   `json:"forbid_virtual"`
	ScreenOpen              bool   `json:"screen_open"`
	ScreenShotOpen          bool   `json:"screen_shot_open"`
	IdentityVerify          bool   `json:"identity_verify"`
	OpenCamera              bool   `json:"open_camera"`
	OpenPhoneVideoRecording bool   `json:"open_phone_video_recording"`
	IPBind                  bool   `json:"ip_bind"`
	ForbidScreen            bool   `json:"forbid_screen"`
	IsUserLocked            bool   `json:"is_user_locked"`
	OffLimits               bool   `json:"off_limits"`
	IPLimit                 string `json:"ip_limit"`
}

func (c *Client) exerciseCheckHeaderProtection(ctx context.Context, course string, id, attempt int64) error {
	var raw json.RawMessage
	if err := c.exerciseRequest(ctx, http.MethodGet, exercisePath(id, "/common_header.json"), examQuery(course, id, ""), nil, examRef(course, id), &raw, false); err != nil {
		return err
	}
	var p exerciseHeaderProtection
	if json.Unmarshal(raw, &p) != nil {
		return examErr(ExerciseUpstream, "exercise header protection is invalid")
	}
	if p.ID != id || p.ExerciseUserID != attempt {
		return examErr(ExerciseIdentityMismatch, "exercise header attempt mismatch")
	}
	if !knownFields(raw, "only_on_client", "net_limit", "use_white_list", "forbid_virtual", "screen_open", "screen_shot_open", "identity_verify", "open_camera", "open_phone_video_recording", "ip_bind", "forbid_screen", "is_user_locked", "off_limits", "ip_limit") {
		return examErr(ExerciseActionRequired, "exercise browser requirements are incomplete")
	}
	if p.IsUserLocked {
		return examErr(ExerciseLocked, "exercise requires teacher unlock")
	}
	if p.OnlyOnClient || p.NetLimit || p.UseWhiteList || p.ForbidVirtual || p.ScreenOpen || p.ScreenShotOpen || p.IdentityVerify || p.OpenCamera || p.OpenPhoneVideoRecording || p.IPBind || p.ForbidScreen || p.OffLimits || restrictedExamIP(p.IPLimit) {
		return examErr(ExerciseActionRequired, "exercise requires the original supervised browser session")
	}
	return nil
}

func (c *Client) exerciseTarget(ctx context.Context, course string, id int64) (*Exercise, error) {
	if err := validateExam(course, id); err != nil {
		return nil, err
	}
	list, err := c.Exercises(ctx, course)
	if err != nil {
		return nil, err
	}
	for _, e := range list {
		if e.ID == id {
			if e.ExerciseUserID <= 0 {
				return nil, examErr(ExerciseIdentityMismatch, "exercise member identity is missing")
			}
			return &e, nil
		}
	}
	return nil, examErr(ExerciseIdentityMismatch, "exercise does not belong to the selected course")
}
func (c *Client) exerciseActor(ctx context.Context, course string) (*User, error) {
	u, err := c.UserInfo(ctx, course)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, examErr(ExerciseUpstream, "exercise account lookup failed")
	}
	if u.UserID <= 0 || u.Login == "" || u.Login != c.LoginName {
		return nil, examErr(ExerciseIdentityMismatch, "exercise account identity mismatch")
	}
	return u, nil
}

func (c *Client) ExercisePaper(ctx context.Context, course string, id int64) (*ExercisePaper, error) {
	target, err := c.exerciseTarget(ctx, course, id)
	if err != nil {
		return nil, err
	}
	actor, err := c.exerciseActor(ctx, course)
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	// The paper reader deliberately omits login. The original frontend uses
	// the cookie's current attempt here; supplying login selects a review route.
	if err := c.exerciseRequest(ctx, http.MethodGet, exercisePath(id, "/user_exercise_detail.json"), examQuery(course, id, ""), nil, examRef(course, id), &raw, false); err != nil {
		return nil, err
	}
	var root struct {
		Exercise json.RawMessage `json:"exercise"`
		Data     json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &root) != nil {
		return nil, examErr(ExerciseUpstream, "exercise paper response is invalid")
	}
	// The recorded endpoint returns exercise/exercise_question_types at root.
	// An explicit data object is accepted only when it contains the same keys.
	if len(root.Exercise) == 0 && len(root.Data) > 0 {
		raw = root.Data
		_ = json.Unmarshal(raw, &root)
	}
	var p ExercisePaper
	if json.Unmarshal(raw, &p) != nil {
		return nil, examErr(ExerciseUpstream, "exercise paper fields are invalid")
	}
	if p.Exercise.ID != id || p.Exercise.UserID != actor.UserID || p.Exercise.ExerciseUserID != target.ExerciseUserID || (p.Course != "" && p.Course != course) {
		return nil, examErr(ExerciseIdentityMismatch, "exercise paper identity mismatch")
	}
	p.Course = course
	p.Questions = nil
	seen := map[int64]bool{}
	positions := map[int]bool{}
	for _, g := range p.QuestionTypes {
		if !g.typeKnown || !g.countKnown || g.Count != len(g.Questions) {
			return nil, examErr(ExerciseUnsupportedQuestion, "exercise question count is inconsistent")
		}
		for _, q := range g.Questions {
			if !q.typeKnown || q.ID <= 0 || q.Position <= 0 || q.Title == "" || seen[q.ID] || positions[q.Position] || q.Type != g.Type {
				return nil, examErr(ExerciseUnsupportedQuestion, "exercise question identity is ambiguous")
			}
			seen[q.ID] = true
			positions[q.Position] = true
			p.Questions = append(p.Questions, q)
		}
	}
	if len(p.Questions) == 0 {
		return nil, examErr(ExerciseUnsupportedQuestion, "exercise paper has no readable questions")
	}
	p.StateKnown = knownFields(root.Exercise, "id", "user_id", "exercise_user_id", "can_start", "commit_status", "screen_open", "identity_verify", "open_camera", "open_phone_video_recording", "ip_limit", "ip_bind", "forbid_screen", "exercise_start_at")
	return &p, nil
}
func (c *Client) ExerciseUserInfo(ctx context.Context, course string, id int64) (*ExerciseUserInfo, error) {
	target, err := c.exerciseTarget(ctx, course, id)
	if err != nil {
		return nil, err
	}
	var r struct {
		Data json.RawMessage `json:"data"`
	}
	if err := c.exerciseRequest(ctx, http.MethodGet, exercisePath(id, "/get_exercise_user_info.json"), examQuery(course, id, c.LoginName), nil, examRef(course, id), &r, false); err != nil {
		return nil, err
	}
	var u ExerciseUserInfo
	if json.Unmarshal(r.Data, &u) != nil {
		return nil, examErr(ExerciseUpstream, "exercise user state is invalid")
	}
	if u.ExerciseUserID != target.ExerciseUserID {
		return nil, examErr(ExerciseIdentityMismatch, "exercise user identity mismatch")
	}
	u.StateKnown = knownFields(r.Data, "exercise_user_id", "user_is_enter", "can_start", "is_commit", "is_user_locked", "start_locked", "screen_open", "check_camera", "screen_shot_open", "ip_bind", "is_ip_limit", "ip_limit")
	return &u, nil
}
func (c *Client) ExerciseTime(ctx context.Context, course string, id int64) (*ExerciseTime, error) {
	if _, err := c.exerciseTarget(ctx, course, id); err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err := c.exerciseRequest(ctx, http.MethodGet, exercisePath(id, "/exercise_time.json"), examQuery(course, id, c.LoginName), nil, examRef(course, id), &raw, false); err != nil {
		return nil, err
	}
	var t ExerciseTime
	if json.Unmarshal(raw, &t) != nil {
		return nil, examErr(ExerciseUpstream, "exercise time is invalid")
	}
	t.StateKnown = knownFields(raw, "has_time", "left_time", "user_end_time")
	return &t, nil
}
func (c *Client) BeginExerciseCommit(ctx context.Context, course string, id int64) (*ExerciseCommitCheck, error) {
	if _, err := c.exerciseTarget(ctx, course, id); err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err := c.exerciseRequest(ctx, http.MethodGet, exercisePath(id, "/begin_commit.json"), url.Values{"id": {strconv.FormatInt(id, 10)}}, nil, examRef(course, id), &raw, false); err != nil {
		return nil, err
	}
	var check ExerciseCommitCheck
	if json.Unmarshal(raw, &check) != nil {
		return nil, examErr(ExerciseUpstream, "exercise commit check is invalid")
	}
	check.StateKnown = knownFields(raw, "shixun_undo", "question_undo", "oj_undo", "end_time")
	if !check.StateKnown || check.ShixunUndo < 0 || check.QuestionUndo < 0 || check.OJUndo < 0 {
		return nil, examErr(ExerciseActionRequired, "exercise unanswered state is not confirmed")
	}
	return &check, nil
}
func (c *Client) ExerciseSubmission(ctx context.Context, course string, id int64) (*ExerciseSubmission, error) {
	target, err := c.exerciseTarget(ctx, course, id)
	if err != nil {
		return nil, err
	}
	actor, err := c.exerciseActor(ctx, course)
	if err != nil {
		return nil, err
	}
	var r struct {
		Data struct {
			Current  json.RawMessage `json:"current_answer_user"`
			MaxScore json.RawMessage `json:"total_score"`
		} `json:"data"`
	}
	q := examQuery(course, id, "")
	q.Set("page", "1")
	q.Set("limit", "20")
	if err := c.exerciseRequest(ctx, http.MethodGet, exercisePath(id, "/exercise_users.json"), q, nil, examRef(course, id), &r, false); err != nil {
		return nil, err
	}
	var s ExerciseSubmission
	if json.Unmarshal(r.Data.Current, &s) != nil {
		return nil, examErr(ExerciseUpstream, "exercise submission fields are invalid")
	}
	if s.ExerciseUserID != target.ExerciseUserID || s.UserID != actor.UserID || s.Login != actor.Login || !knownFields(r.Data.Current, "commit_status", "is_force_commit", "open_total_score", "score") {
		return nil, examErr(ExerciseIdentityMismatch, "exercise submission identity mismatch")
	}
	s.ConfirmedSubmitted = s.CommitStatus == 2
	if positiveExerciseScore(r.Data.MaxScore) {
		s.MaxScore = append(json.RawMessage(nil), r.Data.MaxScore...)
	}
	return &s, nil
}

// ExerciseWriteGuard fetches the current paper, ownership, admission state and
// personal clock immediately before a write. Passive exam configuration such
// as is_locked is never confused with this student's active lock. Required
// browser/teacher actions are preserved rather than simulated by this client.
func (c *Client) ExerciseWriteGuard(ctx context.Context, course string, id int64) (*ExercisePaper, error) {
	p, err := c.ExercisePaper(ctx, course, id)
	if err != nil {
		return nil, err
	}
	u, err := c.ExerciseUserInfo(ctx, course, id)
	if err != nil {
		return nil, err
	}
	if u.ExerciseUserID != p.Exercise.ExerciseUserID {
		return nil, examErr(ExerciseIdentityMismatch, "exercise attempt changed")
	}
	if u.IsCommit || p.Exercise.CommitStatus == 2 {
		return nil, examErr(ExerciseAlreadySubmitted, "exercise is already submitted")
	}
	if !p.StateKnown || !u.StateKnown {
		return nil, examErr(ExerciseActionRequired, "exercise admission state is incomplete")
	}
	if u.IsUserLocked || u.StartLocked {
		return nil, examErr(ExerciseLocked, "exercise requires teacher unlock")
	}
	if !u.CanStart || !p.Exercise.CanStart || !u.UserIsEnter || p.Exercise.ExerciseStartAt == "" {
		return nil, examErr(ExerciseActionRequired, "exercise requires browser admission")
	}
	s := p.Exercise
	if s.ScreenOpen || s.IdentityVerify || s.OpenCamera || s.OpenPhoneVideoRecording || s.IPBind || s.ForbidScreen || restrictedExamIP(s.IPLimit) || u.ScreenOpen || u.CheckCamera || u.ScreenShotOpen || u.IPBind || u.IsIPLimit || restrictedExamIP(u.IPLimit) {
		return nil, examErr(ExerciseActionRequired, "exercise requires the original supervised browser session")
	}
	if s.CommitStatus != 0 {
		return nil, examErr(ExerciseActionRequired, "exercise submission state is not writable")
	}
	if err := c.exerciseCheckHeaderProtection(ctx, course, id, p.Exercise.ExerciseUserID); err != nil {
		return nil, err
	}
	if err := validateExerciseQuestions(p); err != nil {
		return nil, err
	}
	clock, err := c.ExerciseTime(ctx, course, id)
	if err != nil {
		return nil, err
	}
	if !clock.StateKnown {
		return nil, examErr(ExerciseActionRequired, "personal exercise clock is incomplete")
	}
	if clock.LeftTime <= 0 {
		return nil, examErr(ExerciseExpired, "personal exercise time has expired")
	}
	deadline, known := exercisePersonalDeadline(clock.UserEndTime)
	if !known {
		return nil, examErr(ExerciseActionRequired, "personal exercise deadline is unknown")
	}
	if !time.Now().Before(deadline) {
		return nil, examErr(ExerciseExpired, "personal exercise deadline has passed")
	}
	// user_total_end_time and the paper's global end_time are intentionally not
	// used to extend the shorter per-student deadline.
	return p, nil
}

func exercisePersonalDeadline(raw string) (time.Time, bool) {
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return parsed, true
	}
	// exercise_time in the capture uses a Chinese local timestamp here,
	// although user_total_end_time is RFC3339. Never inherit the host's zone.
	parsed, err := time.ParseInLocation("2006-01-02 15:04:05", raw, homeworkLocation)
	return parsed, err == nil
}
func restrictedExamIP(s string) bool { return s != "no" && s != "" }
func validateExerciseQuestions(p *ExercisePaper) error {
	options := map[int64]bool{}
	for _, q := range p.Questions {
		switch q.Type {
		case 0, 2:
			if q.BlankCount > 0 || len(q.Choices) < 2 || (q.Type == 2 && len(q.Choices) != 2) {
				return examErr(ExerciseUnsupportedQuestion, "exercise choice shape is unsupported")
			}
			positions := map[int]bool{}
			for _, o := range q.Choices {
				if o.ID <= 0 || o.Position <= 0 || o.Text == "" || options[o.ID] || positions[o.Position] {
					return examErr(ExerciseUnsupportedQuestion, "exercise option identity is ambiguous")
				}
				options[o.ID] = true
				positions[o.Position] = true
			}
		case 3:
			if q.BlankCount != 1 || len(q.Choices) > 0 {
				return examErr(ExerciseUnsupportedQuestion, "only the observed single-blank shape is supported")
			}
		default:
			return examErr(ExerciseUnsupportedQuestion, "exercise question type is unsupported")
		}
	}
	return nil
}
func (c *Client) SaveExerciseAnswer(ctx context.Context, course string, id int64, a ExerciseAnswer) (*ExerciseAnswerAck, error) {
	p, err := c.ExerciseWriteGuard(ctx, course, id)
	if err != nil {
		return nil, err
	}
	var question *ExerciseQuestion
	for i := range p.Questions {
		if p.Questions[i].ID == a.QuestionID {
			question = &p.Questions[i]
			break
		}
	}
	if question == nil {
		return nil, examErr(ExerciseAnswerInvalid, "answer question does not match the current paper")
	}
	if a.ExpectedQuestionHash != "" && a.ExpectedQuestionHash != ExerciseQuestionHash(*question) {
		return nil, examErr(ExerciseIdentityMismatch, "exercise question changed after answer generation")
	}
	if a.QuestionType != question.Type {
		return nil, examErr(ExerciseAnswerInvalid, "answer question type does not match the current paper")
	}
	if question.Type == 0 || question.Type == 2 {
		if a.AnswerText != nil {
			return nil, examErr(ExerciseAnswerInvalid, "choice answer_text must be null")
		}
		found := false
		for _, o := range question.Choices {
			if o.ID == a.ChoiceID {
				found = true
			}
		}
		if !found {
			return nil, examErr(ExerciseAnswerInvalid, "answer option does not belong to the question")
		}
	} else if a.ChoiceID != 1 || a.AnswerText == nil || strings.TrimSpace(*a.AnswerText) == "" || len(*a.AnswerText) > 65536 {
		return nil, examErr(ExerciseAnswerInvalid, "single blank requires slot one and nonempty text")
	}
	// Preserve the supplied text verbatim, including case and whitespace.
	if err := c.exerciseRequest(ctx, http.MethodPost, "/api/exercise_questions/"+strconv.FormatInt(a.QuestionID, 10)+"/exercise_answers.json", nil, a, examRef(course, id), nil, true); err != nil {
		return nil, err
	}
	// The capture acknowledges save but does not echo persisted answer values.
	return &ExerciseAnswerAck{Saved: true, ReadbackVerified: false}, nil
}
func (c *Client) CommitExercise(ctx context.Context, course string, id int64) (*ExerciseCommitResult, error) {
	if _, err := c.ExerciseWriteGuard(ctx, course, id); err != nil {
		return nil, err
	}
	check, err := c.BeginExerciseCommit(ctx, course, id)
	if err != nil {
		return nil, err
	}
	if check.ShixunUndo != 0 || check.QuestionUndo != 0 || check.OJUndo != 0 {
		return nil, examErr(ExerciseActionRequired, "exercise still has unanswered items")
	}
	var r struct {
		Data struct {
			CommitTime       string `json:"commit_time"`
			UserExerciseTime string `json:"user_exercise_time"`
		} `json:"data"`
	}
	err = c.exerciseRequest(ctx, http.MethodPost, exercisePath(id, "/commit_exercise.json"), nil, map[string]any{"categoryId": strconv.FormatInt(id, 10), "commit_method": 1}, examRef(course, id), &r, true)
	out := &ExerciseCommitResult{Acknowledged: err == nil, CommitTime: r.Data.CommitTime, UserExerciseTime: r.Data.UserExerciseTime}
	// A lost acknowledgement can still mean the server committed the attempt.
	// Reconcile once using GET only, and never repeat POST automatically.
	readCtx := ctx
	var cancel context.CancelFunc
	if ctx.Err() != nil {
		readCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
	}
	submission, readErr := c.ExerciseSubmission(readCtx, course, id)
	if readErr == nil {
		out.Submission = submission
		out.Reconciled = submission.ConfirmedSubmitted
	}
	if !out.Reconciled {
		return out, examErr(ExerciseSubmissionUncertain, "exercise submission requires read-only reconciliation")
	}
	return out, nil
}

// Start/unlock and proctoring event writes are intentionally absent: their
// admission actions remain with the original student/teacher browser flow.
