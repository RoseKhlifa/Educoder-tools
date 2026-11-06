package web

import (
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
