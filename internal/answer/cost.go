package answer

import (
	"errors"
	"math"
)

// DefaultPrices is the conservative Flash peak price profile recorded in the
// implementation plan. Unknown models require an explicit configured price.
func DefaultPrices() map[string]Pricing {
	return map[string]Pricing{
		"deepseek-flash": {
			InputMicroUSDPerMillion: 300000, CacheHitMicroUSDPerMillion: 6000,
			OutputMicroUSDPerMillion: 1200000, Version: "deepseek-flash-peak-2026-10-31",
		},
		"deepseek-v4-pro": {
			InputMicroUSDPerMillion: 1320000, CacheHitMicroUSDPerMillion: 44000,
			OutputMicroUSDPerMillion: 3960000, Version: "deepseek-v4-pro-peak-2026-10-31",
		},
	}
}

func validPricing(p Pricing) bool {
	return p.InputMicroUSDPerMillion > 0 && p.CacheHitMicroUSDPerMillion >= 0 && p.OutputMicroUSDPerMillion > 0 && p.Version != ""
}

func costMicroUSD(miss, hit, output int64, p Pricing) (int64, error) {
	var numerator int64
	for _, v := range [][2]int64{{miss, p.InputMicroUSDPerMillion}, {hit, p.CacheHitMicroUSDPerMillion}, {output, p.OutputMicroUSDPerMillion}} {
		if v[0] < 0 || v[1] < 0 || (v[1] != 0 && v[0] > (math.MaxInt64-numerator)/v[1]) {
			return 0, errors.New("model token cost exceeds supported range")
		}
		numerator += v[0] * v[1]
	}
	// Round the total upward once; do not use floating point or round free
	// fractional requests down to zero.
	cost := numerator / 1000000
	if numerator%1000000 != 0 {
		cost++
	}
	return cost, nil
}

func (d *DeepSeek) Estimate(req Request) (Estimate, error) {
	wire, err := buildRequest(req)
	if err != nil {
		return Estimate{}, err
	}
	p, ok := d.prices[wire.Model]
	if !ok || !validPricing(p) {
		return Estimate{}, &Error{Kind: "price_unavailable"}
	}
	// A byte upper bound is intentionally more conservative than the typical
	// characters-per-token estimate, including Chinese and arbitrary source.
	// The additional framing allowance covers role and message delimiters.
	input := int64(256)
	for _, m := range wire.Messages {
		input += int64(len(m.Content.textOnly()) + len(m.Role) + 64)
	}
	peakInputPrice := p.InputMicroUSDPerMillion
	if p.CacheHitMicroUSDPerMillion > peakInputPrice {
		peakInputPrice = p.CacheHitMicroUSDPerMillion
	}
	peak := p
	peak.InputMicroUSDPerMillion = peakInputPrice
	cost, err := costMicroUSD(input, 0, int64(wire.MaxTokens), peak)
	if err != nil {
		return Estimate{}, err
	}
	return Estimate{InputTokens: input, OutputTokens: int64(wire.MaxTokens), CostMicroUSD: cost, Model: wire.Model, PriceVersion: p.Version}, nil
}
