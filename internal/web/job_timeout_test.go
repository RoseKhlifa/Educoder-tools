package web

import (
	"testing"
	"time"
)

func TestLongBatchTimeoutConfiguration(t *testing.T) {
	for _, key := range []string{"JOB_WORKERS", "JOB_QUEUE_SIZE", "JOB_PER_ACCOUNT_PENDING", "JOB_PER_ACCOUNT_RUNNING", "MODEL_CONCURRENCY", "JOB_TIMEOUT", "MODEL_TIMEOUT"} {
		t.Setenv(key, "")
	}
	defaults := DefaultConfig()
	if defaults.JobTimeout != 2*time.Hour || defaults.ModelTimeout != 4*time.Minute {
		t.Fatalf("batch timeout=%v model timeout=%v", defaults.JobTimeout, defaults.ModelTimeout)
	}
	for _, tc := range []struct {
		raw   string
		want  time.Duration
		valid bool
	}{
		{"", 2 * time.Hour, true},
		{"4h", 4 * time.Hour, true},
		{"24h", 24 * time.Hour, true},
		{"24h1s", 0, false},
		{"0s", 0, false},
		{"invalid", 0, false},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			t.Setenv("JOB_TIMEOUT", tc.raw)
			cfg := DefaultConfig()
			err := loadJobConfig(&cfg)
			if (err == nil) != tc.valid {
				t.Fatalf("JOB_TIMEOUT=%q err=%v valid=%t", tc.raw, err, tc.valid)
			}
			if tc.valid && (cfg.JobTimeout != tc.want || cfg.ModelTimeout != 4*time.Minute) {
				t.Fatalf("JOB_TIMEOUT=%q batch=%v want=%v model=%v", tc.raw, cfg.JobTimeout, tc.want, cfg.ModelTimeout)
			}
		})
	}
}
