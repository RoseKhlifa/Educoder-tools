package web

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestDeploymentJobTimeoutDefaultsAgree(t *testing.T) {
	for file, expected := range map[string]string{
		"../../compose.yaml":        `JOB_TIMEOUT: "${JOB_TIMEOUT:-2h}"`,
		"../../deploy/.env.example": "JOB_TIMEOUT=2h",
		"../../README.md":           "| `JOB_TIMEOUT` | `2h` |",
		"../../deploy/README.md":    "`JOB_TIMEOUT` 默认 2 小时",
	} {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), expected) {
			t.Errorf("%s missing %q", file, expected)
		}
	}
}

// compose.yaml lists the container environment explicitly. A setting the
// process reads but compose does not pass is silently ignored in production.
func TestComposePassesEveryJobSettingWithCodeDefaults(t *testing.T) {
	b, err := os.ReadFile("../../compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	d := DefaultConfig()
	for name, value := range map[string]int{
		"JOB_WORKERS": d.JobWorkers, "JOB_QUEUE_SIZE": d.JobQueueSize,
		"JOB_PER_ACCOUNT_PENDING": d.JobPerOwner, "JOB_PER_ACCOUNT_RUNNING": d.JobPerOwnerRunning,
		"MODEL_CONCURRENCY":            d.ModelConcurrency,
		"UPSTREAM_ACCOUNT_CONCURRENCY": d.UpstreamAccountConcurrency, "UPSTREAM_GLOBAL_CONCURRENCY": d.UpstreamGlobalConcurrency,
		"HOMEWORK_PARALLELISM": d.HomeworkParallelism, "COLLECTION_WORKERS": d.CollectionWorkers,
	} {
		want := fmt.Sprintf(`%s: "${%s:-%d}"`, name, name, value)
		if !strings.Contains(string(b), want) {
			t.Errorf("compose.yaml must pass %s with the code default: %s", name, want)
		}
	}
}

func TestProFreshSolveDefaultsOnAndIsConfigurable(t *testing.T) {
	t.Setenv("MODEL_PRO_FRESH_SOLVE", "")
	cfg, err := LoadConfig()
	if err != nil || !cfg.ProFreshSolve {
		t.Fatalf("default ProFreshSolve=%v err=%v", cfg.ProFreshSolve, err)
	}
	t.Setenv("MODEL_PRO_FRESH_SOLVE", "false")
	if cfg, err = LoadConfig(); err != nil || cfg.ProFreshSolve {
		t.Fatalf("disabled ProFreshSolve=%v err=%v", cfg.ProFreshSolve, err)
	}
	t.Setenv("MODEL_PRO_FRESH_SOLVE", "sometimes")
	if _, err = LoadConfig(); err == nil {
		t.Fatal("accepted an invalid MODEL_PRO_FRESH_SOLVE")
	}
	b, err := os.ReadFile("../../compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `MODEL_PRO_FRESH_SOLVE: "${MODEL_PRO_FRESH_SOLVE:-true}"`) {
		t.Fatal("compose.yaml must pass MODEL_PRO_FRESH_SOLVE with the code default")
	}
}
