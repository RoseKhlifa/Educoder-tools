package educoder

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type examFixture struct {
	mu             sync.Mutex
	t              *testing.T
	course         string
	paper          map[string]any
	info           map[string]any
	clock          map[string]any
	submission     map[string]any
	check          map[string]any
	writes         []string
	bodies         []map[string]any
	ack            string
	commitHTTP     int
	commitSucceeds bool
	listOverride   []map[string]any
	notModified    bool
	closedPaper    bool
	overview       map[string]any
}

func newExamFixture(t *testing.T) (*examFixture, *Client, func()) {
	f := &examFixture{t: t, course: "COURSE1", ack: `{"status":0,"alert":[]}`, commitHTTP: 200, commitSucceeds: true}
	f.paper = map[string]any{
		"exercise": map[string]any{"id": 51, "exercise_name": "Synthetic paper", "user_id": 7, "exercise_user_id": 601, "can_start": true, "commit_status": 0, "screen_open": false, "identity_verify": false, "open_camera": false, "open_phone_video_recording": false, "ip_limit": "no", "ip_bind": false, "forbid_screen": false, "exercise_start_at": time.Now().Add(-time.Minute).Format(time.RFC3339), "end_time": time.Now().Add(2 * time.Hour).Format(time.RFC3339)},
		"exercise_question_types": []any{
			map[string]any{"question_type_id": 1, "question_type": 0, "count": 1, "name": "Single", "items": []any{map[string]any{"question_id": 101, "q_position": 1, "question_type": 0, "question_title": "Synthetic single", "question_choices": []any{map[string]any{"choice_id": 1001, "choice_position": 1, "choice_text": "One", "user_answer_boolean": false}, map[string]any{"choice_id": 1002, "choice_position": 2, "choice_text": "Two", "user_answer_boolean": false}}, "user_answer": nil}}},
			map[string]any{"question_type_id": 3, "question_type": 2, "count": 1, "name": "Judgment", "items": []any{map[string]any{"question_id": 102, "q_position": 2, "question_type": 2, "question_title": "Synthetic judgment", "question_choices": []any{map[string]any{"choice_id": 2007, "choice_position": 1, "choice_text": "False", "user_answer_boolean": false}, map[string]any{"choice_id": 2003, "choice_position": 2, "choice_text": "True", "user_answer_boolean": false}}, "user_answer": nil}}},
			map[string]any{"question_type_id": 4, "question_type": 3, "count": 1, "name": "Blank", "items": []any{map[string]any{"question_id": 103, "q_position": 3, "question_type": 3, "question_title": "Keep text case", "multi_count": 1, "user_answer": []any{}}}},
		},
	}
	f.info = map[string]any{"exercise_user_id": 601, "user_is_enter": true, "can_start": true, "is_commit": false, "is_user_locked": false, "start_locked": false, "is_locked": true, "screen_open": false, "check_camera": false, "screen_shot_open": false, "ip_bind": false, "is_ip_limit": false, "ip_limit": "no"}
	f.clock = map[string]any{"has_time": true, "left_time": 600, "student_left_minutes": 10, "user_end_time": time.Now().Add(10 * time.Minute).Format(time.RFC3339), "user_total_end_time": time.Now().Add(20 * time.Minute).Format(time.RFC3339)}
	f.submission = map[string]any{"exercise_user_id": 601, "user_id": 7, "login": "synthetic", "commit_status": 0, "is_force_commit": false, "score": "--", "open_total_score": false}
	f.overview = map[string]any{"id": 51, "exercise_user_id": 601, "exercise_name": "Synthetic paper", "exercise_question_count": 3, "total_score": "100.0", "end_time": time.Now().Add(time.Hour).In(homeworkLocation).Format("2006-01-02 15:04:05"), "screen_key": "synthetic-ignored-extra", "teacher_permission": true, "user_answers": []any{"synthetic-hidden-extra"}}
	for _, flag := range []string{"only_on_client", "net_limit", "use_white_list", "forbid_virtual", "screen_open", "screen_shot_open", "identity_verify", "open_camera", "open_phone_video_recording", "ip_bind", "forbid_screen", "is_user_locked", "off_limits"} {
		f.overview[flag] = false
	}
	f.overview["ip_limit"] = "no"
	f.check = map[string]any{"shixun_undo": 0, "question_undo": 0, "oj_undo": 0, "end_time": time.Now().Add(10 * time.Minute).Format(time.RFC3339)}
	srv := httptest.NewServer(http.HandlerFunc(f.handle))
	c := New(srv.URL)
	c.LoginName = "synthetic"
	return f, c, srv.Close
}
func (f *examFixture) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodPost {
		f.writes = append(f.writes, r.URL.Path)
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			f.t.Errorf("invalid request body")
		}
		f.bodies = append(f.bodies, body)
	}
	switch r.URL.Path {
	case "/api/users/get_user_info.json":
		json.NewEncoder(w).Encode(map[string]any{"user_id": 7, "login": "synthetic"})
	case "/api/v2/courses/COURSE1/exercises.json":
		list := f.listOverride
		if list == nil {
			list = []map[string]any{{"id": 51, "exercise_name": "Synthetic paper", "exercise_user_id": 601}}
		}
		json.NewEncoder(w).Encode(map[string]any{"status": 0, "total_count": len(list), "exercises": list})
	case "/api/exercises/51/common_header.json":
		json.NewEncoder(w).Encode(f.overview)
	case "/api/exercises/51/user_exercise_detail.json":
		if f.closedPaper {
			w.Write([]byte(`{"status":-1,"message":"Teacher disallows post-submission paper view"}`))
			return
		}
		if f.notModified {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		json.NewEncoder(w).Encode(f.paper)
	case "/api/exercises/51/get_exercise_user_info.json":
		json.NewEncoder(w).Encode(map[string]any{"status": 0, "data": f.info})
	case "/api/exercises/51/exercise_time.json":
		json.NewEncoder(w).Encode(f.clock)
	case "/api/exercises/51/begin_commit.json":
		json.NewEncoder(w).Encode(f.check)
	case "/api/exercises/51/exercise_users.json":
		json.NewEncoder(w).Encode(map[string]any{"status": 0, "data": map[string]any{"current_answer_user": f.submission, "total_score": "100.0"}})
	case "/api/exercise_questions/101/exercise_answers.json", "/api/exercise_questions/102/exercise_answers.json", "/api/exercise_questions/103/exercise_answers.json":
		w.Write([]byte(f.ack))
	case "/api/exercises/51/commit_exercise.json":
		if f.commitSucceeds {
			f.submission["commit_status"] = 2
			f.info["is_commit"] = true
		}
		w.WriteHeader(f.commitHTTP)
		if f.commitHTTP == 200 {
			w.Write([]byte(`{"status":0,"data":{"commit_time":"2030-01-01T00:00:00+08:00","user_exercise_time":"10 minutes"}}`))
		} else {
			w.Write([]byte(`{"message":"private signed URL secret must not escape"}`))
		}
	default:
		http.NotFound(w, r)
	}
}
func TestExerciseObservedLifecycle(t *testing.T) {
	f, c, close := newExamFixture(t)
	defer close()
	ctx := context.Background()
	list, err := c.Exercises(ctx, "COURSE1")
	if err != nil || len(list) != 1 || list[0].Name != "Synthetic paper" {
		t.Fatalf("list: %#v %v", list, err)
	}
	p, err := c.ExercisePaper(ctx, "COURSE1", 51)
	if err != nil || len(p.Questions) != 3 || !p.StateKnown {
		t.Fatalf("paper: %#v %v", p, err)
	}
	text := "  MiXeD case  "
	answers := []ExerciseAnswer{{QuestionID: 101, QuestionType: 0, ChoiceID: 1002}, {QuestionID: 102, QuestionType: 2, ChoiceID: 2007}, {QuestionID: 103, QuestionType: 3, ChoiceID: 1, AnswerText: &text}}
	for _, a := range answers {
		ack, err := c.SaveExerciseAnswer(ctx, "COURSE1", 51, a)
		if err != nil || ack == nil || !ack.Saved || ack.ReadbackVerified {
			t.Fatalf("save ack: %#v %v", ack, err)
		}
	}
	if len(f.writes) != 3 {
		t.Fatalf("writes=%d", len(f.writes))
	}
	if len(f.bodies[0]) != 3 || f.bodies[0]["answer_text"] != nil || f.bodies[0]["exercise_choice_id"] != float64(1002) {
		t.Fatalf("choice wire changed: %#v", f.bodies[0])
	}
	if f.bodies[1]["exercise_choice_id"] != float64(2007) {
		t.Fatal("judgment option IDs were replaced by boolean positions")
	}
	if f.bodies[2]["answer_text"] != text || f.bodies[2]["exercise_choice_id"] != float64(1) {
		t.Fatalf("blank text normalized: %#v", f.bodies[2])
	}
	got, err := c.CommitExercise(ctx, "COURSE1", 51)
	if err != nil || !got.Acknowledged || !got.Reconciled || !got.Submission.ConfirmedSubmitted {
		t.Fatalf("commit: %#v %v", got, err)
	}
	if got.Submission.OpenTotalScore || string(got.Submission.Score) != `"--"` {
		t.Fatal("unpublished grade changed into score")
	}
	if len(f.writes) != 4 || f.bodies[3]["categoryId"] != "51" || f.bodies[3]["commit_method"] != float64(1) {
		t.Fatalf("commit wire: %#v", f.bodies[3])
	}
}
func TestExerciseWritesRejectFreshState(t *testing.T) {
	cases := []struct {
		name   string
		change func(*examFixture)
		code   ExerciseCode
	}{
		{"locked", func(f *examFixture) { f.info["is_user_locked"] = true }, ExerciseLocked},
		{"start locked", func(f *examFixture) { f.info["start_locked"] = true }, ExerciseLocked},
		{"submitted", func(f *examFixture) { f.info["is_commit"] = true }, ExerciseAlreadySubmitted},
		{"screen supervision", func(f *examFixture) { f.info["screen_open"] = true }, ExerciseActionRequired},
		{"camera", func(f *examFixture) { f.info["check_camera"] = true }, ExerciseActionRequired},
		{"identity", func(f *examFixture) { f.paper["exercise"].(map[string]any)["identity_verify"] = true }, ExerciseActionRequired},
		{"IP binding", func(f *examFixture) { f.info["ip_bind"] = true }, ExerciseActionRequired},
		{"admission missing", func(f *examFixture) { f.info["user_is_enter"] = false }, ExerciseActionRequired},
		{"missing state", func(f *examFixture) { delete(f.info, "is_user_locked") }, ExerciseActionRequired},
		{"time elapsed", func(f *examFixture) { f.clock["left_time"] = 0 }, ExerciseExpired},
		{"personal deadline", func(f *examFixture) { f.clock["user_end_time"] = time.Now().Add(-time.Second).Format(time.RFC3339) }, ExerciseExpired},
		{"unknown clock", func(f *examFixture) { delete(f.clock, "left_time") }, ExerciseActionRequired},
		{"wrong actor", func(f *examFixture) { f.paper["exercise"].(map[string]any)["user_id"] = 999 }, ExerciseIdentityMismatch},
		{"wrong exercise", func(f *examFixture) { f.paper["exercise"].(map[string]any)["id"] = 999 }, ExerciseIdentityMismatch},
		{"missing exercise", func(f *examFixture) { delete(f.paper["exercise"].(map[string]any), "id") }, ExerciseIdentityMismatch},
		{"changed attempt", func(f *examFixture) { f.info["exercise_user_id"] = 602 }, ExerciseIdentityMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, c, close := newExamFixture(t)
			defer close()
			tc.change(f)
			_, err := c.SaveExerciseAnswer(context.Background(), "COURSE1", 51, ExerciseAnswer{QuestionID: 101, QuestionType: 0, ChoiceID: 1001})
			if ExerciseErrorCode(err) != tc.code {
				t.Fatalf("error=%v code=%s want=%s", err, ExerciseErrorCode(err), tc.code)
			}
			if len(f.writes) != 0 {
				t.Fatalf("protected state caused %d writes", len(f.writes))
			}
		})
	}
}
func TestExerciseAnswerBoundToFreshQuestion(t *testing.T) {
	cases := []struct {
		name   string
		answer ExerciseAnswer
		change func(*examFixture)
		code   ExerciseCode
	}{
		{"wrong question", ExerciseAnswer{QuestionID: 999, QuestionType: 0, ChoiceID: 1001}, nil, ExerciseAnswerInvalid},
		{"cross question option", ExerciseAnswer{QuestionID: 101, QuestionType: 0, ChoiceID: 2007}, nil, ExerciseAnswerInvalid},
		{"changed type", ExerciseAnswer{QuestionID: 101, QuestionType: 2, ChoiceID: 1001}, nil, ExerciseAnswerInvalid},
		{"multi blank", ExerciseAnswer{QuestionID: 103, QuestionType: 3, ChoiceID: 1}, func(f *examFixture) {
			f.paper["exercise_question_types"].([]any)[2].(map[string]any)["items"].([]any)[0].(map[string]any)["multi_count"] = 2
		}, ExerciseUnsupportedQuestion},
		{"duplicate question", ExerciseAnswer{QuestionID: 101, QuestionType: 0, ChoiceID: 1001}, func(f *examFixture) {
			f.paper["exercise_question_types"].([]any)[2].(map[string]any)["items"].([]any)[0].(map[string]any)["question_id"] = 101
		}, ExerciseUnsupportedQuestion},
		{"duplicate option", ExerciseAnswer{QuestionID: 101, QuestionType: 0, ChoiceID: 1001}, func(f *examFixture) {
			f.paper["exercise_question_types"].([]any)[0].(map[string]any)["items"].([]any)[0].(map[string]any)["question_choices"].([]any)[1].(map[string]any)["choice_id"] = 1001
		}, ExerciseUnsupportedQuestion},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, c, close := newExamFixture(t)
			defer close()
			if tc.change != nil {
				tc.change(f)
			}
			_, err := c.SaveExerciseAnswer(context.Background(), "COURSE1", 51, tc.answer)
			if ExerciseErrorCode(err) != tc.code {
				t.Fatalf("error=%v code=%s want=%s", err, ExerciseErrorCode(err), tc.code)
			}
			if len(f.writes) != 0 {
				t.Fatal("invalid answer wrote upstream")
			}
		})
	}
}
func TestExerciseCommitLostAckReconcilesWithoutRepeat(t *testing.T) {
	for _, committed := range []bool{true, false} {
		t.Run(map[bool]string{true: "committed", false: "unknown"}[committed], func(t *testing.T) {
			f, c, close := newExamFixture(t)
			defer close()
			f.commitHTTP = 502
			f.commitSucceeds = committed
			got, err := c.CommitExercise(context.Background(), "COURSE1", 51)
			if committed {
				if err != nil || got.Acknowledged || !got.Reconciled {
					t.Fatalf("reconcile: %#v %v", got, err)
				}
			} else if ExerciseErrorCode(err) != ExerciseSubmissionUncertain || got.Reconciled {
				t.Fatalf("unknown: %#v %v", got, err)
			}
			if len(f.writes) != 1 {
				t.Fatalf("commit replayed %d times", len(f.writes))
			}
		})
	}
}
func TestExerciseMissingSaveAckAndUnansweredCommit(t *testing.T) {
	f, c, close := newExamFixture(t)
	defer close()
	f.ack = `{"alert":[]}`
	if _, err := c.SaveExerciseAnswer(context.Background(), "COURSE1", 51, ExerciseAnswer{QuestionID: 101, QuestionType: 0, ChoiceID: 1001}); ExerciseErrorCode(err) != ExerciseUpstream {
		t.Fatalf("missing ack accepted: %v", err)
	}
	f.writes = nil
	f.check["question_undo"] = 1
	if _, err := c.CommitExercise(context.Background(), "COURSE1", 51); ExerciseErrorCode(err) != ExerciseActionRequired {
		t.Fatalf("unanswered commit accepted: %v", err)
	}
	if len(f.writes) != 0 {
		t.Fatal("unanswered paper was committed")
	}
}
func TestExerciseReadIsPassiveAndNotModifiedIsError(t *testing.T) {
	f, c, close := newExamFixture(t)
	defer close()
	f.info["screen_open"] = true
	f.paper["exercise"].(map[string]any)["screen_open"] = true
	if _, err := c.ExercisePaper(context.Background(), "COURSE1", 51); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ExerciseUserInfo(context.Background(), "COURSE1", 51); err != nil {
		t.Fatal(err)
	}
	if len(f.writes) != 0 {
		t.Fatal("exam read entered the attempt")
	}
	f.notModified = true
	if _, err := c.ExercisePaper(context.Background(), "COURSE1", 51); err == nil {
		t.Fatal("304 accepted as empty successful paper")
	}
}
func TestExerciseCourseMemberAndPrivacy(t *testing.T) {
	f, c, close := newExamFixture(t)
	defer close()
	f.listOverride = []map[string]any{{"id": 52, "exercise_name": "Another paper", "exercise_user_id": 603}}
	if _, err := c.ExercisePaper(context.Background(), "COURSE1", 51); ExerciseErrorCode(err) != ExerciseIdentityMismatch {
		t.Fatalf("foreign exercise read: %v", err)
	}
	if _, err := c.ExercisePaper(context.Background(), "FOREIGN", 51); err == nil || strings.Contains(err.Error(), "signed") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("foreign course error leaked upstream: %v", err)
	}
	f.listOverride = []map[string]any{{"id": 51, "exercise_name": "A", "exercise_user_id": 601}, {"id": 51, "exercise_name": "B", "exercise_user_id": 601}}
	if _, err := c.Exercises(context.Background(), "COURSE1"); ExerciseErrorCode(err) != ExerciseIdentityMismatch {
		t.Fatalf("duplicate list accepted: %v", err)
	}
	if len(f.writes) != 0 {
		t.Fatal("invalid membership wrote upstream")
	}
}

func TestExerciseObservedLocalPersonalDeadline(t *testing.T) {
	f, c, close := newExamFixture(t)
	defer close()
	f.clock["user_end_time"] = time.Now().Add(time.Minute).In(homeworkLocation).Format("2006-01-02 15:04:05")
	tm, err := c.ExerciseTime(context.Background(), "COURSE1", 51)
	if err != nil || !tm.StateKnown || tm.LeftTime != 600 {
		t.Fatalf("status-free timer not decoded: %#v %v", tm, err)
	}
	if _, err = c.ExerciseWriteGuard(context.Background(), "COURSE1", 51); err != nil {
		t.Fatal(err)
	}
	f.clock["user_end_time"] = time.Now().Add(-time.Minute).In(homeworkLocation).Format("2006-01-02 15:04:05")
	if _, err = c.ExerciseWriteGuard(context.Background(), "COURSE1", 51); ExerciseErrorCode(err) != ExerciseExpired {
		t.Fatalf("later global deadline overrode personal deadline: %v", err)
	}
}
func TestExerciseMissingTypeIsNotSingleChoice(t *testing.T) {
	f, c, close := newExamFixture(t)
	defer close()
	delete(f.paper["exercise_question_types"].([]any)[0].(map[string]any)["items"].([]any)[0].(map[string]any), "question_type")
	if _, err := c.ExercisePaper(context.Background(), "COURSE1", 51); ExerciseErrorCode(err) != ExerciseUnsupportedQuestion {
		t.Fatalf("missing question type defaulted to single choice: %v", err)
	}
}
func TestExerciseUnsupportedPaperRemainsReadable(t *testing.T) {
	f, c, close := newExamFixture(t)
	defer close()
	group := f.paper["exercise_question_types"].([]any)[0].(map[string]any)
	group["question_type"] = 99
	group["items"].([]any)[0].(map[string]any)["question_type"] = 99
	if _, err := c.ExercisePaper(context.Background(), "COURSE1", 51); err != nil {
		t.Fatalf("read blocked merely due to unsupported type: %v", err)
	}
	if _, err := c.ExerciseWriteGuard(context.Background(), "COURSE1", 51); ExerciseErrorCode(err) != ExerciseUnsupportedQuestion {
		t.Fatalf("unknown type writable: %v", err)
	}
	if len(f.writes) != 0 {
		t.Fatal("unknown type wrote upstream")
	}
}
func TestExerciseTruncatedListCannotBindPaper(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			t.Error("membership used write")
		}
		if r.URL.Query().Get("page") == "1" {
			w.Write([]byte(`{"status":0,"total_count":2,"exercises":[{"id":51,"exercise_name":"Synthetic","exercise_user_id":601}]}`))
			return
		}
		w.Write([]byte(`{"status":0,"total_count":2,"exercises":[]}`))
	}))
	defer srv.Close()
	c := New(srv.URL)
	c.LoginName = "synthetic"
	if _, err := c.ExercisePaper(context.Background(), "COURSE1", 51); ExerciseErrorCode(err) != ExerciseIdentityMismatch {
		t.Fatalf("partially fetched list used as member proof: %v", err)
	}
}

func TestExerciseSubmittedOverviewDoesNotNeedClosedPaper(t *testing.T) {
	f, c, close := newExamFixture(t)
	defer close()
	f.closedPaper = true
	f.submission["commit_status"] = 2
	f.info["is_commit"] = true
	f.info["is_user_locked"] = true
	if _, err := c.ExercisePaper(context.Background(), "COURSE1", 51); err == nil {
		t.Fatal("closed paper unexpectedly readable")
	}
	overview, err := c.ExerciseOverview(context.Background(), "COURSE1", 51)
	if err != nil || overview.ID != 51 || overview.TotalQuestions != 3 || string(overview.MaxScore) != `"100.0"` {
		t.Fatalf("overview: %#v %v", overview, err)
	}
	raw, err := json.Marshal(overview)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 6 {
		t.Fatalf("overview is not six-field allowlist: %s", raw)
	}
	if strings.Contains(string(raw), "synthetic-ignored-extra") || strings.Contains(string(raw), "synthetic-hidden-extra") {
		t.Fatal("overview exposed non-metadata")
	}
	summary, err := c.ExerciseSubmission(context.Background(), "COURSE1", 51)
	if err != nil || !summary.ConfirmedSubmitted || summary.OpenTotalScore || string(summary.Score) != `"--"` || string(summary.MaxScore) != `"100.0"` {
		t.Fatalf("submission grade separation: %#v %v", summary, err)
	}
	if len(f.writes) != 0 {
		t.Fatal("submitted read-only metadata triggered writes")
	}
}
func TestExerciseOverviewRejectsForeignIdentity(t *testing.T) {
	for _, field := range []string{"id", "exercise_user_id", "exercise_name"} {
		t.Run(field, func(t *testing.T) {
			f, c, close := newExamFixture(t)
			defer close()
			if field == "exercise_name" {
				f.overview[field] = "Foreign paper"
			} else {
				f.overview[field] = 999
			}
			if _, err := c.ExerciseOverview(context.Background(), "COURSE1", 51); ExerciseErrorCode(err) != ExerciseIdentityMismatch {
				t.Fatalf("foreign overview accepted: %v", err)
			}
			if len(f.writes) != 0 {
				t.Fatal("overview used a write")
			}
		})
	}
}

func TestExerciseHeaderSecurityRequirementsRemainEnforced(t *testing.T) {
	for _, field := range []string{"only_on_client", "net_limit", "use_white_list", "forbid_virtual", "screen_open", "screen_shot_open", "identity_verify", "open_camera", "open_phone_video_recording", "ip_bind", "forbid_screen", "off_limits"} {
		t.Run(field, func(t *testing.T) {
			f, c, close := newExamFixture(t)
			defer close()
			f.overview[field] = true
			if _, err := c.SaveExerciseAnswer(context.Background(), "COURSE1", 51, ExerciseAnswer{QuestionID: 101, QuestionType: 0, ChoiceID: 1001}); ExerciseErrorCode(err) != ExerciseActionRequired {
				t.Fatalf("header requirement %s bypassed: %v", field, err)
			}
			if len(f.writes) != 0 {
				t.Fatal("protected header caused a write")
			}
		})
	}
	t.Run("missing client state", func(t *testing.T) {
		f, c, close := newExamFixture(t)
		defer close()
		delete(f.overview, "only_on_client")
		if _, err := c.ExerciseWriteGuard(context.Background(), "COURSE1", 51); ExerciseErrorCode(err) != ExerciseActionRequired {
			t.Fatalf("unknown client requirement accepted: %v", err)
		}
	})
}

func TestExerciseQuestionHashTracksOnlyQuestionContract(t *testing.T) {
	base := ExerciseQuestion{ID: 101, Position: 1, Type: 0, Title: "Synthetic title", Choices: []ExerciseChoice{{ID: 1001, Position: 1, Text: "One"}, {ID: 1002, Position: 2, Text: "Two"}}}
	hash := ExerciseQuestionHash(base)
	if len(hash) != 64 || hash != ExerciseQuestionHash(base) {
		t.Fatal("question fingerprint is not stable SHA256")
	}
	state := base
	state.Choices = append([]ExerciseChoice(nil), base.Choices...)
	state.UserAnswer = json.RawMessage(`[1001]`)
	state.Choices[0].UserAnswer = true
	if ExerciseQuestionHash(state) != hash {
		t.Fatal("answer/save state changed the question contract")
	}
	cases := []struct {
		name   string
		change func(*ExerciseQuestion)
	}{
		{"question id", func(q *ExerciseQuestion) { q.ID++ }},
		{"position", func(q *ExerciseQuestion) { q.Position++ }},
		{"type", func(q *ExerciseQuestion) { q.Type = 2 }},
		{"blank count", func(q *ExerciseQuestion) { q.BlankCount = 1 }},
		{"title", func(q *ExerciseQuestion) { q.Title += " changed" }},
		{"choice id", func(q *ExerciseQuestion) { q.Choices[0].ID++ }},
		{"choice position", func(q *ExerciseQuestion) { q.Choices[0].Position++ }},
		{"choice text", func(q *ExerciseQuestion) { q.Choices[0].Text += " changed" }},
		{"choice order", func(q *ExerciseQuestion) { q.Choices[0], q.Choices[1] = q.Choices[1], q.Choices[0] }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := base
			q.Choices = append([]ExerciseChoice(nil), base.Choices...)
			tc.change(&q)
			if ExerciseQuestionHash(q) == hash {
				t.Fatal("changed contract retained old fingerprint")
			}
		})
	}
}
func TestExerciseSaveBindsGeneratedQuestionBeforePost(t *testing.T) {
	for _, change := range []string{"title", "option text"} {
		t.Run(change, func(t *testing.T) {
			f, c, close := newExamFixture(t)
			defer close()
			before, err := c.ExercisePaper(context.Background(), "COURSE1", 51)
			if err != nil {
				t.Fatal(err)
			}
			hash := ExerciseQuestionHash(before.Questions[0])
			question := f.paper["exercise_question_types"].([]any)[0].(map[string]any)["items"].([]any)[0].(map[string]any)
			if change == "title" {
				question["question_title"] = "Different question, same id"
			} else {
				question["question_choices"].([]any)[0].(map[string]any)["choice_text"] = "Different option, same id"
			}
			_, err = c.SaveExerciseAnswer(context.Background(), "COURSE1", 51, ExerciseAnswer{QuestionID: 101, QuestionType: 0, ChoiceID: 1001, ExpectedQuestionHash: hash})
			if ExerciseErrorCode(err) != ExerciseIdentityMismatch {
				t.Fatalf("changed question accepted: %v", err)
			}
			if len(f.writes) != 0 {
				t.Fatal("question changed between generation and save but POST occurred")
			}
		})
	}
	t.Run("unchanged hash is local only", func(t *testing.T) {
		f, c, close := newExamFixture(t)
		defer close()
		paper, err := c.ExercisePaper(context.Background(), "COURSE1", 51)
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.SaveExerciseAnswer(context.Background(), "COURSE1", 51, ExerciseAnswer{QuestionID: 101, QuestionType: 0, ChoiceID: 1001, ExpectedQuestionHash: ExerciseQuestionHash(paper.Questions[0])})
		if err != nil {
			t.Fatal(err)
		}
		if len(f.bodies) != 1 || len(f.bodies[0]) != 3 {
			t.Fatalf("local question hash leaked into upstream wire: %#v", f.bodies)
		}
	})
}
