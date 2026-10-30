package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"educoder-cli/internal/answer"
	"educoder-cli/internal/problem"
	"educoder-cli/internal/questionbank"
)

// The HTTP audit is the one batch-wide accounting view. Solver run ledgers are
// per-run views of some of the same calls and must not be added a second time.
func importUsageAudit(ctx context.Context, store *questionbank.Store, owner, out string) error {
	ledger, err := os.ReadFile(filepath.Join(out, "model-usage.jsonl"))
	if err != nil {
		return err
	}
	cost, err := os.ReadFile(filepath.Join(out, "cost.json"))
	if err != nil {
		return err
	}
	if !json.Valid(cost) {
		return errors.New("invalid cost report")
	}
	lines := strings.Split(strings.TrimSpace(string(ledger)), "\n")
	id := "batch-audit-" + problem.HashContent(owner+":"+string(ledger))
	data, _ := json.Marshal(map[string]any{"source": "http_batch_audit", "cost": json.RawMessage(cost), "usage_records": len(lines), "pricing_note": "USD costs in this ledger are peak-price estimates; actual CNY balance deduction is in the cost report"})
	created := time.Now().UTC()
	if old, e := store.GetRun(ctx, id); e == nil {
		created = old.CreatedAt
	} else if !errors.Is(e, questionbank.ErrNotFound) {
		return e
	}
	if err := store.PutRun(ctx, questionbank.RunRecord{ID: id, Owner: problem.HashContent(owner), Status: "importing", Data: data, CreatedAt: created, UpdatedAt: time.Now().UTC()}); err != nil {
		return err
	}
	existing, err := store.UsageForRun(ctx, id)
	if err != nil {
		return err
	}
	if len(existing) > len(lines) {
		return errors.New("usage audit record count is inconsistent")
	}
	for index, line := range lines {
		if index < len(existing) {
			continue
		}
		var row struct {
			Model  string `json:"model"`
			Finish string `json:"finish_reason"`
			Usage  struct {
				Prompt int64 `json:"prompt_tokens"`
				Hit    int64 `json:"prompt_cache_hit_tokens"`
				Miss   int64 `json:"prompt_cache_miss_tokens"`
				Output int64 `json:"completion_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal([]byte(line), &row) != nil {
			return errors.New("invalid model usage audit row")
		}
		price, ok := answer.DefaultPrices()[row.Model]
		if !ok {
			return errors.New("missing model audit price")
		}
		miss := row.Usage.Miss
		if miss == 0 && row.Usage.Prompt > row.Usage.Hit {
			miss = row.Usage.Prompt - row.Usage.Hit
		}
		numerator := miss*price.InputMicroUSDPerMillion + row.Usage.Hit*price.CacheHitMicroUSDPerMillion + row.Usage.Output*price.OutputMicroUSDPerMillion
		microUSD := (numerator + 999999) / 1000000
		if err := store.RecordUsage(ctx, questionbank.Usage{RunID: id, Provider: "deepseek", Model: row.Model, PricingVersion: price.Version, InputTokens: row.Usage.Prompt, CachedInputTokens: row.Usage.Hit, OutputTokens: row.Usage.Output, CostMicroUSD: microUSD, Status: "audit_peak_estimate", FinishReason: row.Finish}); err != nil {
			return err
		}
	}
	if err := store.PutRun(ctx, questionbank.RunRecord{ID: id, Owner: problem.HashContent(owner), Status: "completed", Data: data, CreatedAt: created, UpdatedAt: time.Now().UTC()}); err != nil {
		return err
	}
	fmt.Printf("BATCH_USAGE_PERSISTED calls=%d run=%s\n", len(lines), id)
	return writeJSON(filepath.Join(out, "bank-usage-run.json"), map[string]any{"run_id": id, "calls": len(lines)})
}
