package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"testing"
	"time"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/jobs"
	"educoder-cli/internal/platform"
	"educoder-cli/internal/taskservice"
)

// Dev-only visual harness. It is a test, so it never ships in the binary, and
// it contacts no network: a fake catalog plus scripted executors drive the real
// progress tracker, the real job manager and the real HTTP handlers so the
// browser sees exactly the payloads production would produce.
//
//	WEB_DEV_HARNESS=127.0.0.1:11460 go test ./internal/web/ -run TestDevHarness -count=1 -timeout 0
//
// Credentials are fixtures: the fake client accepts any password.
func TestDevHarness(t *testing.T) {
	addr := os.Getenv("WEB_DEV_HARNESS")
	if addr == "" {
		t.Skip("set WEB_DEV_HARNESS=host:port to run the visual harness")
	}
	dir := t.TempDir()
	store, err := platform.Open(filepath.Join(dir, "platform.sqlite"), platform.Options{AdminUserIDs: []int64{1}})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	me, err := store.UpsertLogin(ctx, platform.Identity{UpstreamID: 1, Login: "demo", Name: "示例同学"})
	if err != nil {
		t.Fatal(err)
	}
	peers := make([]platform.User, 0, 3)
	for i, name := range []string{"示例用户 A", "示例用户 B", "示例用户 C"} {
		user, err := store.UpsertLogin(ctx, platform.Identity{UpstreamID: int64(20 + i), Login: fmt.Sprintf("peer%d", i), Name: name})
		if err != nil {
			t.Fatal(err)
		}
		peers = append(peers, user)
	}

	manager, err := jobs.Open(ctx, jobs.Config{Path: filepath.Join(dir, "jobs.sqlite"), Workers: 8, PerOwnerRunning: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = manager.Close(shutdown)
	}()

	root, err := filepath.Abs(filepath.Join("..", "..", "web"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.StaticDir, cfg.CookieSecure, cfg.HTTPAddr = root, false, addr
	app := NewServerWithFactory(cfg, func(string) EducoderClient { return &harnessClient{} })
	app.AttachPlatform(store)
	runtime := &JobRuntime{manager: manager, modelConfigured: true, modelConcurrency: 8}
	runtime.run = func(ctx context.Context, _ EducoderClient, _ string, req jobs.Request, report func(jobs.Event)) (jobs.Outcome, error) {
		return harnessCourseRun(ctx, req, report)
	}
	app.AttachJobs(runtime)

	// Seed one live job per account plus settled records, so the consumer pages,
	// the operator overview and the reason groups all have something to show.
	seedHarnessJobs(t, manager, me, peers)

	server := &http.Server{Addr: addr, Handler: app, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Errorf("harness server: %v", err)
		}
	}()
	t.Logf("dev harness on http://%s/  (login: demo / any password)", addr)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	<-stop
	shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
}

type harnessClient struct{}

func (harnessClient) Login(_ context.Context, login, _ string) (*educoder.User, error) {
	return &educoder.User{UserID: 1, Login: login, Name: "示例同学", Identity: "student"}, nil
}
func (harnessClient) UserInfo(context.Context, string) (*educoder.User, error) {
	return &educoder.User{UserID: 1, Login: "demo", Name: "示例同学", Identity: "student"}, nil
}
func (harnessClient) Courses(context.Context, string) ([]educoder.Course, error) {
	students := func(n int) *int { return &n }
	return []educoder.Course{
		{ID: 1, Identifier: "DS2026", Name: "数据结构与算法", HomeworkCount: 4, TeacherName: "示例教师", StudentCount: students(126)},
		{ID: 2, Identifier: "OS2026", Name: "操作系统原理", HomeworkCount: 3, TeacherName: "示例教师", StudentCount: students(98)},
		{ID: 3, Identifier: "DB2026", Name: "数据库系统", HomeworkCount: 2, TeacherName: "示例教师", StudentCount: students(73)},
	}, nil
}
func (harnessClient) Homeworks(_ context.Context, course, _ string) ([]educoder.Homework, error) {
	rows := []educoder.Homework{}
	for i, name := range harnessHomeworkNames(course) {
		finished := 0
		if i == 0 {
			finished = 3
		}
		rows = append(rows, educoder.Homework{
			ID: int64(100*len(course) + i), Name: name, Status: []string{"正在进行"}, IsShixun: true,
			ShixunIdentifier: fmt.Sprintf("sx%s%d", course, i), ChallengeCount: 3, FinishedCount: finished,
			EnterStatus: 1, Deadline: "2026-12-31 23:59",
		})
	}
	return rows, nil
}
func (harnessClient) Enter(context.Context, string, int64, string) (*educoder.Task, error) {
	return &educoder.Task{CourseIdentifier: "DS2026", HomeworkID: 1}, nil
}

func harnessHomeworkNames(course string) []string {
	switch course {
	case "OS2026":
		return []string{"进程调度实验", "虚拟内存实验", "文件系统实验"}
	case "DB2026":
		return []string{"SQL 查询实验", "事务与隔离级别"}
	default:
		return []string{"顺序表与链表", "栈与队列应用", "二叉树遍历", "图的最短路径"}
	}
}

// harnessCourseRun drives the real HomeworkProgressTracker through the same
// report sequence RunHomework emits, so the browser renders genuine snapshots.
func harnessCourseRun(ctx context.Context, req jobs.Request, report func(jobs.Event)) (jobs.Outcome, error) {
	type plan struct {
		course, courseName, title string
		id                        int64
		levels                    []string
		outcome                   string
		reason                    string
	}
	plans := []plan{
		{"DS2026", "数据结构与算法", "顺序表与链表", 101, []string{"建立顺序表", "插入与删除", "链表逆序"}, taskservice.ProgressSkipped, "already_passed"},
		{"DS2026", "数据结构与算法", "栈与队列应用", 102, []string{"括号匹配", "表达式求值", "循环队列"}, taskservice.ProgressPassed, ""},
		{"DS2026", "数据结构与算法", "二叉树遍历", 103, []string{"前序遍历", "层次遍历", "树的高度"}, taskservice.ProgressPassed, ""},
		{"DS2026", "数据结构与算法", "图的最短路径", 104, []string{"邻接矩阵", "Dijkstra", "路径还原"}, taskservice.ProgressAttention, "resource_unavailable"},
	}
	stages := []string{"loading", "bank_lookup", "generating", "selftesting", "evaluating", "verifying"}
	trackers := make([]*taskservice.HomeworkProgressTracker, len(plans))
	for i, p := range plans {
		trackers[i] = taskservice.NewHomeworkProgressTracker(p.course, p.courseName, p.id, p.title)
	}
	settled := make([]string, len(plans))
	publish := func(phase, message string) {
		items := make([]taskservice.ServiceHomeworkProgress, 0, len(trackers))
		completed, skipped, attention := 0, 0, 0
		var focus *taskservice.ServiceHomeworkProgress
		for i, tracker := range trackers {
			state := tracker.Snapshot()
			items = append(items, state)
			switch settled[i] {
			case taskservice.ProgressPassed:
				completed++
			case taskservice.ProgressSkipped:
				completed, skipped = completed+1, skipped+1
			case taskservice.ProgressAttention:
				attention++
			}
			if focus == nil && tracker.Running() {
				focus = &items[len(items)-1]
			}
		}
		progress := &jobs.BatchProgress{
			TotalHomeworks: len(plans), CompletedHomeworks: completed, SkippedHomeworks: skipped, NeedsServiceHomeworks: attention,
			Homeworks: jobHomeworkProgressList(items),
		}
		if focus != nil {
			progress.CurrentCourse, progress.CurrentHomeworkID, progress.CurrentHomeworkTitle = focus.Course, focus.HomeworkID, focus.Title
			progress.TotalLevels, progress.CompletedLevels = focus.TotalLevels, focus.CompletedLevels
			progress.SkippedLevels, progress.FailedLevels = focus.SkippedLevels, focus.FailedLevels
			progress.CurrentPosition, progress.CurrentTitle, progress.Stage = focus.CurrentPosition, focus.CurrentTitle, focus.Stage
		}
		report(jobs.Event{Phase: phase, Kind: "service", Message: message, Progress: progress})
	}
	pace := 550 * time.Millisecond
	if value, err := time.ParseDuration(os.Getenv("WEB_DEV_HARNESS_PACE")); err == nil && value > 0 && value < time.Minute {
		pace = value
	}
	tick := func() bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(pace):
			return true
		}
	}
	// The first assignment is already complete upstream and is skipped at once.
	trackers[0].Settle(taskservice.ProgressSkipped, "already_passed", &taskservice.BatchResult{TotalLevels: 3, CompletedLevels: 3, SkippedLevels: 3}, time.Now().UTC())
	settled[0] = taskservice.ProgressSkipped
	publish("loading", "正在同步课堂和作业")
	// Two assignments then progress together, as HOMEWORK_PARALLELISM allows.
	for level := 0; level < 3; level++ {
		for _, stage := range stages {
			for i := 1; i < len(plans); i++ {
				if settled[i] != "" {
					continue
				}
				batch := &taskservice.BatchProgress{TotalLevels: 3, CompletedLevels: level, CurrentPosition: level + 1, CurrentTitle: plans[i].levels[level]}
				if stage == "verifying" {
					batch = &taskservice.BatchProgress{TotalLevels: 3, CompletedLevels: level + 1}
				}
				trackers[i].Observe(stage, batch, time.Now().UTC())
			}
			publish(stage, "正在处理作业")
			if !tick() {
				return jobs.Outcome{Status: jobs.Canceled, Message: "已停止"}, nil
			}
		}
		// The last assignment hits an overloaded Educoder runtime on level 2.
		if level == 1 {
			last := len(plans) - 1
			trackers[last].Settle(plans[last].outcome, plans[last].reason, &taskservice.BatchResult{TotalLevels: 3, CompletedLevels: 1, FailedLevels: 1, Levels: []taskservice.BatchLevel{
				{Position: 1, Title: plans[last].levels[0], Status: "passed"},
				{Position: 2, Title: plans[last].levels[1], Status: "resource_unavailable"},
				{Position: 3, Title: plans[last].levels[2], Status: "planned"},
			}}, time.Now().UTC())
			settled[last] = plans[last].outcome
			publish("service_pending", "这份作业稍后可以重新执行")
		}
	}
	for i := 1; i < len(plans); i++ {
		if settled[i] != "" {
			continue
		}
		trackers[i].Settle(taskservice.ProgressPassed, "", &taskservice.BatchResult{TotalLevels: 3, CompletedLevels: 3, Levels: []taskservice.BatchLevel{
			{Position: 1, Title: plans[i].levels[0], Status: "passed"},
			{Position: 2, Title: plans[i].levels[1], Status: "passed"},
			{Position: 3, Title: plans[i].levels[2], Status: "passed"},
		}}, time.Now().UTC())
		settled[i] = taskservice.ProgressPassed
	}
	publish("verified", "本次作业处理进度已更新")
	result := map[string]any{
		"status": "needs_service", "kind": "service", "scope": req.Scope, "reason_code": "service_required",
		"total_courses": 1, "total_homeworks": 4, "completed_homeworks": 3, "skipped_homeworks": 1, "needs_service_homeworks": 1,
		"auto_retry": map[string]any{"next_at": time.Now().Add(9 * time.Minute).UTC().Format(time.RFC3339), "attempt": 1, "max": 3},
		"homeworks": []map[string]any{
			{"course": "DS2026", "course_name": "数据结构与算法", "homework_id": 101, "title": "顺序表与链表", "status": "skipped", "reason_code": "already_passed"},
			{"course": "DS2026", "course_name": "数据结构与算法", "homework_id": 102, "title": "栈与队列应用", "status": "passed"},
			{"course": "DS2026", "course_name": "数据结构与算法", "homework_id": 103, "title": "二叉树遍历", "status": "passed"},
			{"course": "DS2026", "course_name": "数据结构与算法", "homework_id": 104, "title": "图的最短路径", "status": "needs_service", "reason_code": "resource_unavailable",
				"result": map[string]any{"status": "resource_unavailable", "reason_code": "resource_unavailable", "total_levels": 3, "completed_levels": 1, "current_position": 2, "current_title": "Dijkstra",
					"levels": []map[string]any{
						{"position": 1, "title": "邻接矩阵", "status": "passed"},
						{"position": 2, "title": "Dijkstra", "status": "resource_unavailable", "reason_code": "resource_unavailable"},
						{"position": 3, "title": "路径还原", "status": "planned"},
					}}},
		},
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return jobs.Outcome{}, err
	}
	return jobs.Outcome{Status: jobs.Failed, Result: raw, ErrorCode: "SERVICE_REQUIRED", Message: "部分作业需要服务人员处理"}, nil
}

func seedHarnessJobs(t *testing.T, manager *jobs.Manager, me platform.User, peers []platform.User) {
	t.Helper()
	ctx := context.Background()
	live := func(ctx context.Context, req jobs.Request, report func(jobs.Event)) (jobs.Outcome, error) {
		return harnessCourseRun(ctx, req, report)
	}
	settled := func(status, code, message, result string) jobs.Executor {
		return func(context.Context, jobs.Request, func(jobs.Event)) (jobs.Outcome, error) {
			return jobs.Outcome{Status: status, ErrorCode: code, Message: message, Result: json.RawMessage(result)}, nil
		}
	}
	submit := func(owner string, req jobs.Request, exec jobs.Executor) {
		if _, _, err := manager.Submit(ctx, owner, req, exec); err != nil {
			t.Fatalf("seed job: %v", err)
		}
	}
	passed := `{"status":"passed","kind":"service","scope":"course","total_homeworks":2,"completed_homeworks":2,"homeworks":[{"course":"DB2026","course_name":"数据库系统","homework_id":301,"title":"SQL 查询实验","status":"passed"},{"course":"DB2026","course_name":"数据库系统","homework_id":302,"title":"事务与隔离级别","status":"passed"}]}`
	busy := `{"status":"needs_service","kind":"service","scope":"course","reason_code":"resource_unavailable","total_homeworks":2,"completed_homeworks":1,"needs_service_homeworks":1,"auto_retry":{"next_at":"%s","attempt":2,"max":3},"homeworks":[{"course":"OS2026","course_name":"操作系统原理","homework_id":201,"title":"进程调度实验","status":"passed"},{"course":"OS2026","course_name":"操作系统原理","homework_id":202,"title":"虚拟内存实验","status":"needs_service","reason_code":"resource_unavailable","result":{"status":"resource_unavailable","reason_code":"resource_unavailable","total_levels":4,"completed_levels":2,"levels":[{"position":1,"title":"页表初始化","status":"passed"},{"position":2,"title":"缺页中断","status":"passed"},{"position":3,"title":"置换算法","status":"resource_unavailable"},{"position":4,"title":"性能统计","status":"planned"}]}}]}`
	selftest := `{"status":"needs_service","kind":"service","scope":"course","reason_code":"selftest_failed","total_homeworks":1,"completed_homeworks":0,"needs_service_homeworks":1,"homeworks":[{"course":"DS2026","course_name":"数据结构与算法","homework_id":103,"title":"二叉树遍历","status":"needs_service","reason_code":"selftest_failed","result":{"status":"selftest_failed","reason_code":"selftest_failed","total_levels":3,"completed_levels":1,"levels":[{"position":1,"title":"前序遍历","status":"passed"},{"position":2,"title":"层次遍历","status":"selftest_failed"},{"position":3,"title":"树的高度","status":"planned"}]}}]}`

	submit(me.Owner(), jobs.Request{Mode: "submit", Scope: "all"}, live)
	submit(me.Owner(), jobs.Request{Mode: "submit", Scope: "course", Course: "DB2026"}, settled(jobs.Succeeded, "", "全部作业已完成", passed))
	submit(me.Owner(), jobs.Request{Mode: "submit", Scope: "course", Course: "OS2026"},
		settled(jobs.Failed, "SERVICE_REQUIRED", "部分作业需要服务人员处理", fmt.Sprintf(busy, time.Now().Add(7*time.Minute).UTC().Format(time.RFC3339))))
	if len(peers) > 0 {
		submit(peers[0].Owner(), jobs.Request{Mode: "submit", Scope: "course", Course: "DS2026"}, live)
	}
	if len(peers) > 1 {
		submit(peers[1].Owner(), jobs.Request{Mode: "submit", Scope: "all"}, live)
		submit(peers[1].Owner(), jobs.Request{Mode: "submit", Scope: "course", Course: "DS2026"}, settled(jobs.Failed, "SERVICE_REQUIRED", "部分作业需要服务人员处理", selftest))
	}
	if len(peers) > 2 {
		submit(peers[2].Owner(), jobs.Request{Mode: "submit", Scope: "course", Course: "OS2026"},
			settled(jobs.Failed, "SERVICE_REQUIRED", "部分作业需要服务人员处理", fmt.Sprintf(busy, time.Now().Add(4*time.Minute).UTC().Format(time.RFC3339))))
		submit(peers[2].Owner(), jobs.Request{Mode: "submit", Scope: "homework", Course: "DS2026", HomeworkID: 104}, settled(jobs.Uncertain, "RESULT_UNCONFIRMED", "平台结果待复核", `{"status":"verification_unconfirmed","reason_code":"verification_unconfirmed","total_levels":3,"completed_levels":2}`))
	}
}
