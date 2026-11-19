package web

import (
	"context"
	"testing"
	"time"
)

func TestCollectionLaneSizeIsConfigurable(t *testing.T) {
	for _, name := range []string{"JOB_WORKERS", "JOB_QUEUE_SIZE", "JOB_PER_ACCOUNT_PENDING", "JOB_PER_ACCOUNT_RUNNING", "MODEL_CONCURRENCY", "JOB_TIMEOUT", "MODEL_TIMEOUT", "APP_DATA_DIR", "DEEPSEEK_API_KEY_FILE", "DEEPSEEK_API_KEY", "DEEPSEEK_BASE_URL", "COLLECTION_WORKERS"} {
		t.Setenv(name, "")
	}
	cfg, err := LoadConfig()
	if err != nil || cfg.CollectionWorkers != 1 {
		t.Fatalf("default collection lane = %d err=%v, want 1", cfg.CollectionWorkers, err)
	}
	t.Setenv("COLLECTION_WORKERS", "3")
	if cfg, err = LoadConfig(); err != nil || cfg.CollectionWorkers != 3 {
		t.Fatalf("configured collection lane = %d err=%v", cfg.CollectionWorkers, err)
	}
	cfg.DataDir = t.TempDir()
	runtime, err := NewJobRuntime(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := runtime.manager.Stats().CollectionWorkers; got != 3 {
		t.Fatalf("manager collection workers = %d", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = runtime.Close(ctx); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"0", "-1", "junk", "17"} {
		t.Setenv("COLLECTION_WORKERS", bad)
		if _, err = LoadConfig(); err == nil {
			t.Fatalf("accepted invalid COLLECTION_WORKERS %q", bad)
		}
	}
}
