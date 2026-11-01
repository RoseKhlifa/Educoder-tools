package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/jobs"
	"educoder-cli/internal/taskservice"
)

type exerciseWebFixture struct {
	*fakeClient
	paperReads atomic.Int64
	listReads  atomic.Int64
	submitted  bool
}

func (f *exerciseWebFixture) Exercises(context.Context, string) ([]educoder.Exercise, error) {
	f.listReads.Add(1)
	return []educoder.Exercise{{ID: 11, Name: "示例期中考试", ExerciseUserID: 22}}, nil
}
func (f *exerciseWebFixture) ExerciseOverview(context.Context, string, int64) (*educoder.ExerciseOverview, error) {
	return &educoder.ExerciseOverview{ID: 11, ExerciseUserID: 22, Name: "示例期中考试", TotalQuestions: 50, MaxScore: json.RawMessage(`"100.00"`), EndTime: "2099-01-01T00:00:00+08:00"}, nil
}
func (f *exerciseWebFixture) ExercisePaper(context.Context, string, int64) (*educoder.ExercisePaper, error) {
	f.paperReads.Add(1)
	if f.submitted {
		return nil, errors.New("teacher disabled paper after submission")
	}
	return &educoder.ExercisePaper{Course: "COURSE", Exercise: educoder.ExerciseSettings{ID: 11, ExerciseUserID: 22, Name: "示例期中考试", ExerciseStartAt: "2099-01-01T00:00:00+08:00"}}, nil
}
func (f *exerciseWebFixture) ExerciseUserInfo(context.Context, string, int64) (*educoder.ExerciseUserInfo, error) {
	return &educoder.ExerciseUserInfo{ExerciseUserID: 22, StateKnown: true, UserIsEnter: true, CanStart: true, IsCommit: f.submitted}, nil
}
func (f *exerciseWebFixture) ExerciseSubmission(context.Context, string, int64) (*educoder.ExerciseSubmission, error) {
	return &educoder.ExerciseSubmission{ExerciseUserID: 22, ConfirmedSubmitted: f.submitted, Score: json.RawMessage(`"--"`)}, nil
}
func (f *exerciseWebFixture) ExerciseTime(context.Context, string, int64) (*educoder.ExerciseTime, error) {
	return &educoder.ExerciseTime{StateKnown: true, HasTime: true, LeftTime: 300, UserEndTime: "2099-01-01 00:00:00"}, nil
}
func (f *exerciseWebFixture) ExerciseWriteGuard(context.Context, string, int64) (*educoder.ExercisePaper, error) {
	return nil, errors.New("browser action required")
}

func TestExerciseDirectoryKeepsSubmittedVisibleWithoutPaperAndChecksMembership(t *testing.T) {
	f := &exerciseWebFixture{fakeClient: &fakeClient{courses: []educoder.Course{{Identifier: "COURSE", Name: "示例课程"}}}, submitted: true}
	cfg := DefaultConfig()
	cfg.StaticDir = ""
	srv := httptest.NewServer(NewServerWithFactory(cfg, func(string) EducoderClient { return f }))
	defer srv.Close()
	unauth := doJSON(t, http.DefaultClient, "GET", srv.URL+"/api/courses/COURSE/exercises", "")
	if unauth.StatusCode != 401 || f.listReads.Load() != 0 {
		t.Fatal("anonymous exam directory read")
	}
	client := jobAccount(t, srv, "sample-student")
	missing := doJSON(t, client, "GET", srv.URL+"/api/courses/OTHER/exercises", "")
	if missing.StatusCode != 404 || f.listReads.Load() != 0 {
		t.Fatal("foreign course read was not rejected before upstream")
	}
	got := doJSON(t, client, "GET", srv.URL+"/api/courses/COURSE/exercises", "")
	if got.StatusCode != 200 || !strings.Contains(got.Body, `"status":"submitted"`) || !strings.Contains(got.Body, `"submitted":true`) || !strings.Contains(got.Body, `"available":false`) || !strings.Contains(got.Body, `"totalQuestions":50`) || f.paperReads.Load() != 0 {
		t.Fatalf("submitted exam disappeared behind forbidden paper: %s paperReads=%d", got.Body, f.paperReads.Load())
	}
	if strings.Contains(got.Body, "teacher disabled") || strings.Contains(got.Body, "question_title") || strings.Contains(got.Body, "user_answer") || strings.Contains(got.Body, `"score":"0"`) {
		t.Fatalf("private paper or invented score leaked: %s", got.Body)
	}
}

func TestExerciseConsumerAndAdminProjectionUseExamIdentityAndSubmissionEvidence(t *testing.T) {
	j := jobs.Job{Status: jobs.Succeeded, Request: jobs.Request{Scope: "exercise", Course: "COURSE", ExerciseID: 11, Mode: "submit"}, Result: json.RawMessage(`{"title":"示例考试","exercise_id":11,"status":"awaiting_review","total_questions":50,"completed_questions":50,"submitted":true,"score_known":false,"score":"99","max_score":"100","model_calls":9,"candidate":{"answers":["private"]}}`)}
	public := toConsumerJob(j)
	if public.Status != "submitted" || public.Kind != "exercise" || public.Result.Title != "示例考试" || public.Result.TotalQuestions != 50 || !public.Result.Submitted || public.Result.ScoreKnown || public.Result.Score != "" {
		t.Fatalf("submission and grade were mixed: %+v", public)
	}
	raw, _ := json.Marshal(public)
	assertConsumerBoundary(t, string(raw))
	admin := deriveJobContext(j)
	if admin.Exercise == nil || admin.Exercise.ID != 11 || admin.Exercise.Name != "示例考试" || admin.Homework.ID != 0 || admin.Level.Title != "" || admin.ScopeLabel != "在线考试" || admin.Summary.ProgressLabel != "已交卷 · 共 50 题" {
		t.Fatalf("exam was presented as a homework or level: %+v", admin)
	}
	j.Result = json.RawMessage(`{"title":"示例考试","submitted":false,"score_known":false}`)
	if got := toConsumerJob(j); got.Status == "completed" || got.Status == "submitted" {
		t.Fatalf("succeeded wrapper manufactured a submitted exam: %+v", got)
	}
	j.Result = json.RawMessage(`{"title":"示例考试","submitted":true,"score_known":true,"score":"80.50","max_score":"100.00"}`)
	if got := toConsumerJob(j); got.Status != "graded" || got.Result.Score != "80.50" || got.Result.MaxScore != "100.00" {
		t.Fatalf("published grade lost exact decimals: %+v", got)
	}
}

func TestExerciseJobHTTPUsesBackgroundExecutorAndOwnerIsolation(t *testing.T) {
	f := newPlatformHTTPFixture(t, func(_ context.Context, _ EducoderClient, _ string, req jobs.Request, report func(jobs.Event)) (jobs.Outcome, error) {
		if req.Scope != "exercise" || req.ExerciseID != 11 || req.HomeworkID != 0 {
			t.Errorf("exam target not preserved: %+v", req)
		}
		report(jobs.Event{Phase: "saving", Progress: &jobs.BatchProgress{CurrentExerciseID: 11, CurrentCourse: "COURSE", TotalQuestions: 50, CompletedQuestions: 20}})
		return exerciseOutcome(&taskservice.ExerciseResult{Kind: "exercise", Status: "awaiting_review", ExerciseID: 11, Title: "示例期中考试", TotalQuestions: 50, CompletedQuestions: 50, Submitted: true}, nil)
	})
	// Reading/reconciling an already submitted exam does not require model
	// credentials. The service itself decides if an unsubmitted task needs one.
	f.app.jobs.modelConfigured = false
	created := f.request(t, f.aliceClient, "POST", "/api/jobs", `{"scope":"exercise","mode":"submit","course":"COURSE","exercise_id":11,"idempotency_key":"fixture-exam"}`)
	if created.StatusCode != http.StatusAccepted {
		t.Fatal(created.Body)
	}
	id := decodeJob(t, created).ID
	f.waitJob(t, id)
	got := f.request(t, f.aliceClient, "GET", "/api/jobs/"+id, "")
	assertConsumerBoundary(t, got.Body)
	if got.StatusCode != 200 || !strings.Contains(got.Body, `"status":"submitted"`) || !strings.Contains(got.Body, `"total_questions":50`) {
		t.Fatal(got.Body)
	}
	if other := f.request(t, f.bobClient, "GET", "/api/jobs/"+id, ""); other.StatusCode != 404 {
		t.Fatal("other tenant could access exam job")
	}
}

func TestExerciseScoreDecoderRequiresFiniteNonnegativeDecimal(t *testing.T) {
	for _, raw := range []string{`"--"`, `null`, `"-1"`, `"NaN"`, `"1/2"`, `"1e99999999"`, `"` + strings.Repeat("1", 1000) + `"`} {
		var score any
		if decodeExerciseNumber([]byte(raw), &score) == nil {
			t.Fatalf("non-public or unsupported grade accepted: %s", raw[:min(40, len(raw))])
		}
	}
	var score any
	if decodeExerciseNumber([]byte(`"80.50"`), &score) != nil || score != "80.50" {
		t.Fatal("published decimal formatting was lost")
	}
}
