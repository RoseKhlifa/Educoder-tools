package web

import (
	"encoding/json"
	"strings"
	"testing"

	"educoder-cli/internal/educoder"
)

func TestHomeworkDTOPreservesDeadlineAndProgressFields(t *testing.T) {
	var homework educoder.Homework
	if err := json.Unmarshal([]byte(`{"homework_id":3985886,"name":"树结构","challenge_count":12,"finished_challenge_count":4,"end_time":"2026-11-30T23:59:00+08:00"}`), &homework); err != nil {
		t.Fatal(err)
	}
	got := toHomeworkDTO(homework)
	if got.Deadline != "2026-11-30T23:59:00+08:00" || got.ChallengeCount != 12 || got.FinishedCount != 4 {
		t.Fatalf("deadline/progress lost: %#v", got)
	}
	var numeric educoder.Homework
	if err := json.Unmarshal([]byte(`{"homework_id":1,"end_time":1796032740000}`), &numeric); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(toHomeworkDTO(numeric).Deadline, "2026-") {
		t.Fatalf("numeric deadline was not normalized: %#v", toHomeworkDTO(numeric))
	}
}

func TestTaskDTOPreservesQuestionAndLevelDetails(t *testing.T) {
	var task educoder.Task
	if err := json.Unmarshal([]byte(`{"status":"required","game_count":6,"challenge":{"id":9,"subject":"树结构相等","task_pass":"请完成判断并说明边界","path":"2.h;","position":3},"shixun":{"name":"编程题实训","challenges_count":12}}`), &task); err != nil {
		t.Fatal(err)
	}
	got := toTaskDTO(task)
	if got.Challenge.Description == "" || got.Challenge.Path != "2.h;" || got.Challenge.Position != 3 || got.GameCount != 6 || got.Shixun.ChallengesCount != 12 {
		t.Fatalf("task details lost: %#v", got)
	}
}
