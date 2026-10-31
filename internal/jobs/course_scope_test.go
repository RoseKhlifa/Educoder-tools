package jobs

import (
	"context"
	"fmt"
	"testing"
)

func TestCourseAndAccountScopeContracts(t *testing.T) {
	for _, tc := range []struct {
		name  string
		req   Request
		valid bool
	}{
		{"course", Request{Course: "C", Scope: "course", Mode: "submit"}, true},
		{"all", Request{Scope: "all", Mode: "submit"}, true},
		{"course requires course", Request{Scope: "course", Mode: "submit"}, false},
		{"course cannot specify homework", Request{Course: "C", Scope: "course", HomeworkID: 1, Mode: "submit"}, false},
		{"course cannot specify game", Request{Course: "C", Scope: "course", Game: "G", Mode: "submit"}, false},
		{"course requires submit", Request{Course: "C", Scope: "course", Mode: "generate_only"}, false},
		{"all cannot specify course", Request{Scope: "all", Course: "C", Mode: "submit"}, false},
		{"all cannot specify homework", Request{Scope: "all", HomeworkID: 1, Mode: "submit"}, false},
		{"all cannot specify game", Request{Scope: "all", Game: "G", Mode: "submit"}, false},
		{"all requires submit", Request{Scope: "all", Mode: "selftest"}, false},
		{"collection independent", Request{Mode: "collect_verified"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, payload, target, err := normalize(tc.req)
			if (err == nil) != tc.valid || (tc.valid && (payload == "" || target == "")) {
				t.Fatalf("valid=%v normalize err=%v target=%q", tc.valid, err, target)
			}
		})
	}
}

func TestOverlappingBroadScopesSerializeWithoutCoalescingOtherTargets(t *testing.T) {
	m := testManager(t, Config{Workers: 4, PerOwnerRunning: 4, PerOwnerPending: 10})
	started := make(chan string, 8)
	gates := map[string]chan struct{}{}
	for _, name := range []string{"homework", "course", "other", "all"} {
		gates[name] = make(chan struct{})
	}
	t.Cleanup(func() {
		for _, gate := range gates {
			select {
			case <-gate:
			default:
				close(gate)
			}
		}
	})
	exec := func(name string) Executor {
		return func(ctx context.Context, _ Request, _ func(Event)) (Outcome, error) {
			started <- name
			select {
			case <-gates[name]:
				return Outcome{}, nil
			case <-ctx.Done():
				return Outcome{}, ctx.Err()
			}
		}
	}
	homework := submit(t, m, "o", Request{Scope: "homework", Course: "C", HomeworkID: 44, Mode: "submit"}, exec("homework"))
	if receive(t, started) != "homework" {
		t.Fatal("homework did not start")
	}
	courseReq := Request{Scope: "course", Course: "C", Mode: "submit", IdempotencyKey: "course-key"}
	course := submit(t, m, "o", courseReq, exec("course"))
	if course.ID == homework.ID || course.Kind != "service" {
		t.Fatalf("different targets coalesced: %+v", course)
	}
	other := submit(t, m, "o", Request{Scope: "course", Course: "D", Mode: "submit"}, exec("other"))
	if receive(t, started) != "other" {
		t.Fatal("independent course blocked by queued broad scope")
	}
	all := submit(t, m, "o", Request{Scope: "all", Mode: "submit"}, exec("all"))
	for _, id := range []string{course.ID, all.ID} {
		j, err := m.Get(context.Background(), "o", id)
		if err != nil || j.Status != Queued {
			t.Fatalf("overlap was not queued: %+v %v", j, err)
		}
	}
	again, reused, err := m.Submit(context.Background(), "o", courseReq, exec("course"))
	if err != nil || !reused || again.ID != course.ID {
		t.Fatalf("exact target not deduplicated: %+v reused=%v err=%v", again, reused, err)
	}
	_, _, err = m.Submit(context.Background(), "o", Request{Scope: "all", Mode: "submit", IdempotencyKey: courseReq.IdempotencyKey}, exec("all"))
	if !IsCode(err, "idempotency_conflict") {
		t.Fatalf("different payload reused idempotency key: %v", err)
	}
	close(gates["homework"])
	waitJob(t, m, "o", homework.ID, Succeeded)
	if receive(t, started) != "course" {
		t.Fatal("course did not start after overlapping homework finished")
	}
	close(gates["other"])
	waitJob(t, m, "o", other.ID, Succeeded)
	j, _ := m.Get(context.Background(), "o", all.ID)
	if j.Status != Queued {
		t.Fatalf("all job overlapped course: %+v", j)
	}
	close(gates["course"])
	waitJob(t, m, "o", course.ID, Succeeded)
	if receive(t, started) != "all" {
		t.Fatal("all did not resume")
	}
	close(gates["all"])
	waitJob(t, m, "o", all.ID, Succeeded)
}

func TestExecutionScopeOverlapMatrix(t *testing.T) {
	hw := Request{Scope: "homework", Course: "C", HomeworkID: 44, Mode: "submit"}
	for i, tc := range []struct {
		a, b Request
		want bool
	}{
		{hw, Request{Scope: "all", Mode: "submit"}, true},
		{hw, Request{Scope: "course", Course: "C", Mode: "submit"}, true},
		{hw, Request{Scope: "course", Course: "D", Mode: "submit"}, false},
		{hw, Request{Course: "C", HomeworkID: 44, Game: "G", Mode: "selftest"}, true},
		{hw, Request{Course: "C", HomeworkID: 45, Game: "G", Mode: "selftest"}, false},
		{Request{Scope: "all", Mode: "submit"}, Request{Mode: "collect_verified"}, false},
		{Request{Course: "C", HomeworkID: 44, Game: "G1"}, Request{Course: "C", HomeworkID: 44, Game: "G2"}, false},
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			if requestsOverlap(tc.a, tc.b) != tc.want || requestsOverlap(tc.b, tc.a) != tc.want {
				t.Fatal("unexpected asymmetric execution overlap")
			}
		})
	}
}

func TestServiceProgressWithoutCurrentLevel(t *testing.T) {
	p := publicProgress(&BatchProgress{TotalHomeworks: 4, CompletedHomeworks: 1, SkippedHomeworks: 1, NeedsServiceHomeworks: 1, CurrentCourse: "C", CurrentHomeworkID: 42, CurrentHomeworkTitle: "Homework"})
	if p == nil || p.CompletedHomeworks != 1 || p.CurrentHomeworkID != 42 {
		t.Fatalf("service progress lost: %+v", p)
	}
	if publicProgress(&BatchProgress{TotalHomeworks: 1, CompletedHomeworks: 1, NeedsServiceHomeworks: 1}) != nil {
		t.Fatal("impossible service counters accepted")
	}
}
