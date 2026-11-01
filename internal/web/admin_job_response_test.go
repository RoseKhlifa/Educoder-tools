package web

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"educoder-cli/internal/jobs"
	"educoder-cli/internal/platform"
)

func TestAdminTaskContextUsesVerifiedIdentityBeyondRecentUserPageAndStaysPrivate(t *testing.T) {
	f := newPlatformHTTPFixture(t, nil)
	ctx := context.Background()
	for i := int64(4); i < 109; i++ {
		if _, err := f.platform.UpsertLogin(ctx, platform.Identity{UpstreamID: i, Login: fmt.Sprintf("fixture%d", i), Name: "其他同学"}); err != nil {
			t.Fatal(err)
		}
	}
	users, err := f.platform.ListUsers(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range users {
		if u.ID == f.alice.ID {
			t.Fatal("fixture owner unexpectedly on first 100-user page")
		}
	}
	if err := f.platform.RememberCourses(ctx, []platform.CourseLabel{{Identifier: "COURSE_A", Name: "数据结构·测试课堂"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.platform.RememberHomeworks(ctx, "COURSE_A", []platform.HomeworkLabel{{ID: 44, Name: "第1章绪论·测试作业"}}); err != nil {
		t.Fatal(err)
	}
	job, _, err := f.manager.Submit(ctx, f.alice.Owner(), jobs.Request{Course: "COURSE_A", HomeworkID: 44, Scope: "homework", Mode: "submit"}, func(context.Context, jobs.Request, func(jobs.Event)) (jobs.Outcome, error) {
		return jobs.Outcome{Status: jobs.Failed, ErrorCode: "HOMEWORK_BATCH_FAILED", Result: json.RawMessage(`{"status":"verification_unconfirmed","reason_code":"verification_unconfirmed","total_levels":10,"completed_levels":5,"levels":[{"position":6,"title":"绪论测试六","status":"verification_unconfirmed","reason_code":"verification_unconfirmed","score":"0.00","max_score":"0.0"}]}`)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	f.waitJob(t, job.ID)
	for _, path := range []string{"/api/admin/jobs", "/api/admin/jobs/" + job.ID} {
		response := f.request(t, f.adminClient, "GET", path, "")
		if response.StatusCode != 200 {
			t.Fatal(response.Body)
		}
		for _, expected := range []string{f.alice.Name, "数据结构·测试课堂", "第1章绪论·测试作业", "绪论测试六", `"linked":true`} {
			if !strings.Contains(response.Body, expected) {
				t.Fatalf("missing %q in %s", expected, response.Body)
			}
		}
	}
	public := f.request(t, f.aliceClient, "GET", "/api/jobs/"+job.ID, "")
	for _, private := range []string{`"context"`, f.alice.Name, "数据结构·测试课堂", f.alice.ID, `"owner_id"`} {
		if strings.Contains(public.Body, private) {
			t.Fatalf("admin display context leaked into consumer response: %s", public.Body)
		}
	}
	if got := f.request(t, f.bobClient, "GET", "/api/admin/jobs/"+job.ID, ""); got.StatusCode != 403 {
		t.Fatalf("ordinary user read operator context: %d", got.StatusCode)
	}
}

func TestAdminHistoricalLabelsWarmFromResultsWithoutClaimingLegacyOwner(t *testing.T) {
	f := newPlatformHTTPFixture(t, nil)
	ctx := context.Background()
	job, _, err := f.manager.Submit(ctx, accountJobOwner(f.alice.Login), jobs.Request{Course: "HISTORY", Mode: "submit", Scope: "course"}, func(context.Context, jobs.Request, func(jobs.Event)) (jobs.Outcome, error) {
		return jobs.Outcome{Status: jobs.Succeeded, Result: json.RawMessage(`{"status":"passed","total_homeworks":1,"completed_homeworks":1,"homeworks":[{"course":"HISTORY","course_name":"历史课堂","homework_id":88,"title":"历史作业","status":"skipped"}]}`)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	f.waitJob(t, job.ID)
	app := NewServer(DefaultConfig())
	app.AttachPlatform(f.platform)
	app.AttachJobs(f.app.jobs)
	cn, hn, err := f.platform.LookupJobLabels(ctx, "HISTORY", 88)
	if err != nil || cn != "历史课堂" || hn != "历史作业" {
		t.Fatalf("history label warm failed: %q %q %v", cn, hn, err)
	}
	if err := f.platform.RememberCourses(ctx, []platform.CourseLabel{{Identifier: "HISTORY", Name: "当前课堂新名称"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.platform.RememberHomeworks(ctx, "HISTORY", []platform.HomeworkLabel{{ID: 88, Name: "当前作业新名称"}}); err != nil {
		t.Fatal(err)
	}
	app.AttachJobs(f.app.jobs)
	cn, hn, err = f.platform.LookupJobLabels(ctx, "HISTORY", 88)
	if err != nil || cn != "当前课堂新名称" || hn != "当前作业新名称" {
		t.Fatalf("historical warm overwrote current names: %q %q %v", cn, hn, err)
	}
	raw, err := f.manager.AdminGet(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	views, err := app.adminJobResponses(ctx, []jobs.AdminRecord{raw})
	if err != nil || views[0].Context.User.Linked || views[0].Context.User.Name != "历史账号未关联" {
		t.Fatalf("legacy display hash was claimed as a current user: %+v %v", views, err)
	}
}
