package questionbank

import "context"

// UsageSummary is actual ledger evidence, including unsuccessful runs. It is
// not inferred from the bounded recent-job list and is not a customer bill.
type UsageSummary struct {
	ModelCalls          int64 `json:"model_calls"`
	InputTokens         int64 `json:"input_tokens"`
	CachedInputTokens   int64 `json:"cached_input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CostMicroUSD        int64 `json:"cost_micro_usd"`
	ReservedMicroUSD    int64 `json:"reserved_micro_usd"`
	SettledCostMicroUSD int64 `json:"settled_cost_micro_usd"`
	UnknownCalls        int64 `json:"unknown_calls"`
}

func (s *Store) UsageSummary(ctx context.Context) (UsageSummary, error) {
	var out UsageSummary
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(input_tokens),0),COALESCE(SUM(cached_input_tokens),0),COALESCE(SUM(output_tokens),0),COALESCE(SUM(cost_micro_usd),0),COALESCE(SUM(CASE WHEN status!='settled' THEN reserved_micro_usd ELSE 0 END),0),COALESCE(SUM(CASE WHEN status='settled' THEN cost_micro_usd ELSE 0 END),0),COALESCE(SUM(CASE WHEN status!='settled' THEN 1 ELSE 0 END),0) FROM usage_ledger`).Scan(&out.ModelCalls, &out.InputTokens, &out.CachedInputTokens, &out.OutputTokens, &out.CostMicroUSD, &out.ReservedMicroUSD, &out.SettledCostMicroUSD, &out.UnknownCalls)
	return out, err
}
