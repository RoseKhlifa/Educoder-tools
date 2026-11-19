package taskservice

import (
	"errors"
	"testing"

	"educoder-cli/internal/educoder"
)

func TestSelfTestRefusalIsAContinuableResourceFailure(t *testing.T) {
	refusal := &educoder.SelfTestRefusedError{Kind: "resource_busy"}
	result := &Result{Status: "resource_unavailable", Kind: "programming"}
	if got := batchFailureReason(result, refusal); got != "resource_unavailable" {
		t.Fatalf("reason=%q", got)
	}
	if !runtimeFailureBeforeFormal(result, refusal) {
		t.Fatal("a refused self-test wrote nothing formal and must be deferrable")
	}
	yes := true
	task := &educoder.Task{SkipLevel: &yes}
	task.Shixun.TaskPass = &yes
	if !mayContinueAfterLevel(task, result, refusal) {
		t.Fatal("a refused self-test must not stop the remaining levels")
	}
	if mayContinueAfterLevel(task, &Result{Status: "uncertain"}, errors.New("unknown write")) {
		t.Fatal("unknown writes must still stop the homework")
	}
}
