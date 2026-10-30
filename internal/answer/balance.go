package answer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// Balance contains only the documented non-secret account balance fields.
// Amounts remain decimal strings so callers can calculate exact deltas without
// introducing floating-point currency rounding or assuming a currency.
type Balance struct {
	IsAvailable  bool          `json:"is_available"`
	BalanceInfos []BalanceInfo `json:"balance_infos"`
}

type BalanceInfo struct {
	Currency        string `json:"currency"`
	TotalBalance    string `json:"total_balance"`
	GrantedBalance  string `json:"granted_balance"`
	ToppedUpBalance string `json:"topped_up_balance"`
}

// ReadBalance performs the documented GET /user/balance request. It does not
// generate model output, and errors never include provider bodies, keys or URLs.
func (d *DeepSeek) ReadBalance(ctx context.Context) (Balance, error) {
	callCtx := ctx
	var cancel context.CancelFunc
	if d.timeout > 0 {
		callCtx, cancel = context.WithTimeout(ctx, d.timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, d.baseURL+"/user/balance", nil)
	if err != nil {
		return Balance{}, &Error{Kind: "request_build_failed"}
	}
	req.Header.Set("Authorization", "Bearer "+d.apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := d.client.Do(req)
	if err != nil {
		return Balance{}, providerNetworkError(callCtx, err, false)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Balance{}, &Error{Kind: "upstream_error", HTTPStatus: resp.StatusCode, Retryable: resp.StatusCode == 429 || resp.StatusCode >= 500}
	}
	const maxBalanceBytes = 1 << 20
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBalanceBytes+1))
	if err != nil {
		return Balance{}, &Error{Kind: "response_read_failed", HTTPStatus: resp.StatusCode}
	}
	var balance Balance
	if len(b) > maxBalanceBytes || json.Unmarshal(b, &balance) != nil || len(balance.BalanceInfos) == 0 || len(balance.BalanceInfos) > 8 {
		return Balance{}, &Error{Kind: "invalid_provider_response"}
	}
	for _, info := range balance.BalanceInfos {
		if (info.Currency != "CNY" && info.Currency != "USD") || !validBalanceAmount(info.TotalBalance) || !validBalanceAmount(info.GrantedBalance) || !validBalanceAmount(info.ToppedUpBalance) {
			return Balance{}, &Error{Kind: "invalid_provider_response"}
		}
	}
	return balance, nil
}

func validBalanceAmount(s string) bool {
	if len(s) == 0 || len(s) > 48 {
		return false
	}
	if s[0] == '-' {
		s = s[1:]
	}
	digits, dot := 0, false
	for i, c := range s {
		if c == '.' && !dot && i > 0 && i < len(s)-1 {
			dot = true
		} else if c >= '0' && c <= '9' {
			digits++
		} else {
			return false
		}
	}
	return digits > 0
}

func providerNetworkError(ctx context.Context, err error, billedUnknown bool) *Error {
	result := &Error{Kind: "network_error", Retryable: true, BilledUnknown: billedUnknown}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		result.Kind = "timeout"
	} else if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		result.Kind = "canceled"
		result.Retryable = false
	}
	return result
}
