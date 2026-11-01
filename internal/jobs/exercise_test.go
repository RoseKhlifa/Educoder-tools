package jobs

import (
	"context"
	"testing"
)

func TestExerciseRequestIdentityAndProgressContracts(t *testing.T) {
	good := Request{Scope: "exercise", Course: "COURSE", ExerciseID: 11, Mode: "submit"}
	_, payload, target, err := normalize(good)
	if err != nil {
		t.Fatal(err)
	}
	other := good
	other.ExerciseID++
	_, otherPayload, otherTarget, _ := normalize(other)
	if payload == otherPayload || target == otherTarget {
		t.Fatal("different exams share an idempotency or target identity")
	}
	for _, bad := range []Request{
		{Scope: "exercise", Course: "COURSE", Mode: "submit"},
		{Scope: "exercise", Course: "COURSE", ExerciseID: -1, Mode: "submit"},
		{Scope: "exercise", Course: "COURSE", ExerciseID: 11, HomeworkID: 11, Mode: "submit"},
		{Scope: "exercise", Course: "COURSE", ExerciseID: 11, Game: "GAME", Mode: "submit"},
		{Scope: "exercise", Course: "COURSE", ExerciseID: 11, Mode: "generate_only"},
		{Scope: "course", Course: "COURSE", ExerciseID: 11, Mode: "submit"},
		{Scope: "all", ExerciseID: 11, Mode: "submit"},
		{Mode: "collect_verified", ExerciseID: 11},
	} {
		if _, _, _, err := normalize(bad); err == nil {
			t.Fatalf("invalid mixed exam target accepted: %+v", bad)
		}
	}
	if p := publicProgress(&BatchProgress{CurrentCourse: "COURSE", CurrentExerciseID: 11, CurrentExerciseTitle: "示例考试", TotalQuestions: 150, CompletedQuestions: 125}); p == nil || p.TotalQuestions != 150 || p.CompletedQuestions != 125 {
		t.Fatalf("exam questions must not be truncated by level count limit: %+v", p)
	}
	if publicProgress(&BatchProgress{TotalQuestions: 20, CompletedQuestions: 21}) != nil {
		t.Fatal("invalid question progress accepted")
	}
}

func TestExerciseOverlapAndIdempotencyIsolation(t *testing.T) {
	a := Request{Scope: "exercise", Course: "C", ExerciseID: 11, Mode: "submit", IdempotencyKey: "exam-key"}
	b := a
	b.ExerciseID++
	if !requestsOverlap(a, a) || requestsOverlap(a, b) || requestsOverlap(a, Request{Scope: "homework", Course: "C", HomeworkID: 11, Mode: "submit"}) || requestsOverlap(a, Request{Scope: "all", Mode: "submit"}) {
		t.Fatal("exam and classroom homework overlap contracts mixed")
	}
	m := testManager(t, Config{Workers: 2, PerOwnerRunning: 2, PerOwnerPending: 4})
	started := make(chan int64, 2)
	gate := make(chan struct{})
	defer close(gate)
	exec := func(ctx context.Context, req Request, _ func(Event)) (Outcome, error) {
		started <- req.ExerciseID
		select {
		case <-gate:
			return Outcome{}, nil
		case <-ctx.Done():
			return Outcome{}, ctx.Err()
		}
	}
	first := submit(t, m, "owner", a, exec)
	if receive(t, started) != 11 || first.Kind != "exercise" {
		t.Fatal("exam execution not started with its own kind")
	}
	again, reused, err := m.Submit(context.Background(), "owner", a, exec)
	if err != nil || !reused || again.ID != first.ID {
		t.Fatalf("same exam was not deduplicated: %+v %v", again, err)
	}
	if _, _, err := m.Submit(context.Background(), "owner", b, exec); !IsCode(err, "idempotency_conflict") {
		t.Fatalf("same key can select another exam: %v", err)
	}
	b.IdempotencyKey = "other-exam"
	second := submit(t, m, "owner", b, exec)
	if receive(t, started) != 12 || second.ID == first.ID {
		t.Fatal("independent exams did not retain separate execution")
	}
}
