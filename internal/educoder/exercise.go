package educoder

// The exercise client contains the small, typed portion of the online-exam
// protocol observed from the browser.  It deliberately keeps exam reads
// separate from the shixun/answer protocol: reading a paper never starts an
// attempt and a successful answer ACK is not evidence that the answer was
// graded.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

type ExerciseCode string

const (
	ExerciseActionRequired      ExerciseCode = "action_required"
	ExerciseLocked              ExerciseCode = "locked"
	ExerciseExpired             ExerciseCode = "expired"
	ExerciseAlreadySubmitted    ExerciseCode = "already_submitted"
	ExerciseUnsupportedQuestion ExerciseCode = "unsupported_question"
	ExerciseIdentityMismatch    ExerciseCode = "identity_mismatch"
	ExerciseAnswerInvalid       ExerciseCode = "answer_invalid"
	ExerciseSubmissionUncertain ExerciseCode = "submission_uncertain"
	ExerciseUpstream            ExerciseCode = "upstream_error"
)

type exerciseError struct {
	code ExerciseCode
	msg  string
}

func (e *exerciseError) Error() string { return e.msg }
func ExerciseErrorCode(err error) ExerciseCode {
	if err == nil {
		return ""
	}
	var e *exerciseError
	if errors.As(err, &e) {
		return e.code
	}
	return ExerciseUpstream
}

// ExerciseErrorCodeOf is an alias for callers that also use error-code types.
func ExerciseErrorCodeOf(err error) ExerciseCode { return ExerciseErrorCode(err) }

type Exercise struct {
	ID                 int64  `json:"id"`
	Name               string `json:"exercise_name"`
	ExerciseUserID     int64  `json:"exercise_user_id"`
	CurrentStatus      int    `json:"current_status"`
	ExerciseStatus     int    `json:"exercise_status"`
	WholeStatus        int    `json:"whole_exercise_status"`
	ExerciseLeftTime   string `json:"exercise_left_time,omitempty"`
	Time               int    `json:"time"`
	ScreenOpen         bool   `json:"screen_open"`
	ScreenNum          int    `json:"screen_num"`
	IsLocked           bool   `json:"is_locked"`
	IsRandom           bool   `json:"is_random"`
	IPLimit            string `json:"ip_limit,omitempty"`
	IPBind             bool   `json:"ip_bind"`
	IdentityVerify     bool   `json:"identity_verify"`
	CurrentUserCreated bool   `json:"current_user_created"`
	CommitStatus       int    `json:"commit_status"`
}

type ExerciseSettings struct {
	TotalScore              json.RawMessage `json:"total_score,omitempty"`
	ID                      int64           `json:"id"`
	UserID                  int64           `json:"user_id"`
	ExerciseUserID          int64           `json:"exercise_user_id"`
	Name                    string          `json:"exercise_name"`
	EndTime                 string          `json:"end_time"`
	ExerciseStartAt         string          `json:"exercise_start_at"`
	CommitStatus            int             `json:"commit_status"`
	UserExerciseStatus      int             `json:"user_exercise_status"`
	StudentCommitStatus     int             `json:"student_commit_status"`
	CanStart                bool            `json:"can_start"`
	ScreenOpen              bool            `json:"screen_open"`
	IdentityVerify          bool            `json:"identity_verify"`
	OpenCamera              bool            `json:"open_camera"`
	OpenPhoneVideoRecording bool            `json:"open_phone_video_recording"`
	IPBind                  bool            `json:"ip_bind"`
	ForbidScreen            bool            `json:"forbid_screen"`
	IPLimit                 string          `json:"ip_limit"`
	OpenScore               bool            `json:"open_score"`
	OpenTotalScore          bool            `json:"open_total_score"`
}
type ExerciseChoice struct {
	ID         int64  `json:"choice_id"`
	Position   int    `json:"choice_position"`
	Text       string `json:"choice_text"`
	UserAnswer bool   `json:"user_answer_boolean"`
}
type ExerciseQuestion struct {
	typeKnown  bool
	ID         int64            `json:"question_id"`
	Position   int              `json:"q_position"`
	Type       int              `json:"question_type"`
	Title      string           `json:"question_title"`
	BlankCount int              `json:"multi_count"`
	Choices    []ExerciseChoice `json:"question_choices"`
	UserAnswer json.RawMessage  `json:"user_answer,omitempty"`
}
type ExerciseQuestionGroup struct {
	typeKnown  bool
	countKnown bool
	ID         int                `json:"question_type_id"`
	Type       int                `json:"question_type"`
	Count      int                `json:"count"`
	Name       string             `json:"name"`
	Questions  []ExerciseQuestion `json:"items"`
}
type ExercisePaper struct {
	Course        string                  `json:"course"`
	Exercise      ExerciseSettings        `json:"exercise"`
	QuestionTypes []ExerciseQuestionGroup `json:"exercise_question_types"`
	Questions     []ExerciseQuestion      `json:"questions,omitempty"`
	StateKnown    bool                    `json:"-"`
}
type ExerciseUserInfo struct {
	StateKnown     bool   `json:"-"`
	ExerciseUserID int64  `json:"exercise_user_id"`
	UserIsEnter    bool   `json:"user_is_enter"`
	CanStart       bool   `json:"can_start"`
	IsCommit       bool   `json:"is_commit"`
	IsUserLocked   bool   `json:"is_user_locked"`
	StartLocked    bool   `json:"start_locked"`
	IsLocked       bool   `json:"is_locked"`
	ScreenOpen     bool   `json:"screen_open"`
	CheckCamera    bool   `json:"check_camera"`
	ScreenShotOpen bool   `json:"screen_shot_open"`
	IPBind         bool   `json:"ip_bind"`
	IsIPLimit      bool   `json:"is_ip_limit"`
	IPLimit        string `json:"ip_limit"`
}
type ExerciseTime struct {
	StateKnown         bool   `json:"-"`
	HasTime            bool   `json:"has_time"`
	LeftTime           int    `json:"left_time"`
	StudentLeftMinutes int    `json:"student_left_minutes"`
	UserEndTime        string `json:"user_end_time"`
	UserTotalEndTime   string `json:"user_total_end_time"`
}
type ExerciseCommitCheck struct {
	StateKnown   bool   `json:"-"`
	ShixunUndo   int    `json:"shixun_undo"`
	QuestionUndo int    `json:"question_undo"`
	OJUndo       int    `json:"oj_undo"`
	EndTime      string `json:"end_time"`
}
type ExerciseSubmission struct {
	UserID         int64           `json:"user_id"`
	ExerciseUserID int64           `json:"exercise_user_id"`
	Login          string          `json:"login"`
	CommitStatus   int             `json:"commit_status"`
	IsForceCommit  bool            `json:"is_force_commit"`
	OpenTotalScore bool            `json:"open_total_score"`
	Score          json.RawMessage `json:"score"`
	// MaxScore is the paper's possible score, never this student's result.
	MaxScore           json.RawMessage `json:"max_score,omitempty"`
	ConfirmedSubmitted bool            `json:"confirmed_submitted"`
}

// ExerciseOverview remains available when a teacher disallows viewing the
// paper after submission. Only non-answer metadata is copied from the header.
type ExerciseOverview struct {
	ID             int64           `json:"id"`
	ExerciseUserID int64           `json:"exercise_user_id"`
	Name           string          `json:"exercise_name"`
	TotalQuestions int             `json:"exercise_question_count"`
	MaxScore       json.RawMessage `json:"total_score"`
	EndTime        string          `json:"end_time"`
}
type ExerciseAnswer struct {
	QuestionID   int64   `json:"questionId"`
	QuestionType int     `json:"-"`
	ChoiceID     int64   `json:"exercise_choice_id"`
	AnswerText   *string `json:"answer_text"`
	// Automatic callers bind a candidate to the exact question they solved.
	// This local contract never becomes part of the upstream request body.
	ExpectedQuestionHash string `json:"-"`
}

// ExerciseQuestionHash fingerprints only the question contract, keeping
// answer/save state out so a successful previous answer does not change it.
// Choice order, IDs, positions and text are preserved as supplied to the model.
func ExerciseQuestionHash(q ExerciseQuestion) string {
	type choice struct {
		ID       int64  `json:"id"`
		Position int    `json:"position"`
		Text     string `json:"text"`
	}
	options := make([]choice, 0, len(q.Choices))
	for _, o := range q.Choices {
		options = append(options, choice{o.ID, o.Position, o.Text})
	}
	contract := struct {
		ID         int64    `json:"id"`
		Position   int      `json:"position"`
		Type       int      `json:"type"`
		BlankCount int      `json:"blank_count"`
		Title      string   `json:"title"`
		Choices    []choice `json:"choices"`
	}{q.ID, q.Position, q.Type, q.BlankCount, q.Title, options}
	raw, _ := json.Marshal(contract)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

type ExerciseAnswerAck struct {
	Saved            bool `json:"saved"`
	ReadbackVerified bool `json:"readback_verified"`
}
type ExerciseCommitResult struct {
	Acknowledged     bool                `json:"acknowledged"`
	Reconciled       bool                `json:"reconciled"`
	CommitTime       string              `json:"commit_time"`
	UserExerciseTime string              `json:"user_exercise_time"`
	Submission       *ExerciseSubmission `json:"submission,omitempty"`
}

func (q *ExerciseQuestion) UnmarshalJSON(data []byte) error {
	type plain ExerciseQuestion
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*q = ExerciseQuestion(decoded)
	q.typeKnown = knownFields(data, "question_type")
	return nil
}

func (g *ExerciseQuestionGroup) UnmarshalJSON(data []byte) error {
	type plain ExerciseQuestionGroup
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*g = ExerciseQuestionGroup(decoded)
	g.typeKnown = knownFields(data, "question_type")
	g.countKnown = knownFields(data, "count")
	return nil
}
