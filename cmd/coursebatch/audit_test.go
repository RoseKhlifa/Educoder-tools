package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"educoder-cli/internal/questionbank"
)

func TestUsageImportIsIdempotentAndKeepsBilledTruncation(t *testing.T) {
	dir := t.TempDir()
	store, err := questionbank.Open(filepath.Join(dir, "bank.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ledger := `{"model":"deepseek-flash","finish_reason":"stop","usage":{"prompt_tokens":20,"prompt_cache_hit_tokens":5,"completion_tokens":30}}` + "\n" + `{"model":"deepseek-v4-pro","finish_reason":"length","usage":{"prompt_tokens":40,"prompt_cache_hit_tokens":0,"completion_tokens":50}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "model-usage.jsonl"), []byte(ledger), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cost.json"), []byte(`{"currency":"CNY","balance_deduction":"0.02"}`), 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := importUsageAudit(context.Background(), store, "local-owner", dir); err != nil {
			t.Fatal(err)
		}
	}
	var info struct {
		RunID string `json:"run_id"`
	}
	b, err := os.ReadFile(filepath.Join(dir, "bank-usage-run.json"))
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(b, &info) != nil {
		t.Fatal("invalid import receipt")
	}
	rows, err := store.UsageForRun(context.Background(), info.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].InputTokens != 20 || rows[0].CachedInputTokens != 5 || rows[1].FinishReason != "length" || rows[1].OutputTokens != 50 || rows[1].CostMicroUSD <= 0 {
		t.Fatalf("usage missing or double counted: %+v", rows)
	}
}
