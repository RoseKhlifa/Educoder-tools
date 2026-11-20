package web

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"educoder-cli/internal/answer"
)

// An unattended deployment otherwise learns that the model account is empty
// only when every level that needs generation starts failing with HTTP 402.
// The admin overview therefore shows the provider balance, read at most once
// per TTL so polling the overview never turns into provider traffic.
const (
	modelBalanceTTL      = 5 * time.Minute
	modelBalanceRetryTTL = time.Minute
	modelBalanceTimeout  = 5 * time.Second
)

// The warning lines are about one heavy production day of model spend
// (about $1.4 on 2026-11-02), so an operator has a day to top up.
var modelBalanceLowLine = map[string]string{"CNY": "10", "USD": "1.5"}

type modelBalanceView struct {
	Available bool      `json:"available"`
	Currency  string    `json:"currency,omitempty"`
	Total     string    `json:"total,omitempty"`
	Threshold string    `json:"threshold,omitempty"`
	Low       bool      `json:"low"`
	CheckedAt time.Time `json:"checked_at"`
	// Error is a provider error kind only, never a body, key or URL.
	Error string `json:"error,omitempty"`
}

type modelBalance struct {
	read func(context.Context) (answer.Balance, error)
	now  func() time.Time

	mu   sync.Mutex
	last *modelBalanceView
	at   time.Time
}

func newModelBalance(read func(context.Context) (answer.Balance, error)) *modelBalance {
	return &modelBalance{read: read, now: time.Now}
}

// get returns the cached balance, reading it again once the TTL has passed. A
// failed read is retried sooner and never hides the last error from the view.
func (b *modelBalance) get(ctx context.Context) *modelBalanceView {
	if b == nil || b.read == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	if b.last != nil {
		ttl := modelBalanceTTL
		if b.last.Error != "" {
			ttl = modelBalanceRetryTTL
		}
		if now.Sub(b.at) < ttl {
			view := *b.last
			return &view
		}
	}
	// The read completes even if this admin request goes away, so the cache
	// is filled for the next one; it is bounded by its own timeout.
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), modelBalanceTimeout)
	balance, err := b.read(readCtx)
	cancel()
	view := modelBalanceView{CheckedAt: now.UTC()}
	if err != nil {
		view.Error = modelBalanceErrorKind(err)
	} else {
		view.Available = balance.IsAvailable
		view.Low = !balance.IsAvailable
		if len(balance.BalanceInfos) > 0 {
			info := balance.BalanceInfos[0]
			view.Currency, view.Total = info.Currency, info.TotalBalance
			view.Threshold = modelBalanceLowLine[info.Currency]
			view.Low = view.Low || decimalBelow(info.TotalBalance, view.Threshold)
		}
	}
	b.last, b.at = &view, now
	out := view
	return &out
}

func modelBalanceErrorKind(err error) string {
	var e *answer.Error
	if !errors.As(err, &e) || e == nil {
		return "unavailable"
	}
	if e.HTTPStatus != 0 {
		return fmt.Sprintf("%s:%d", e.Kind, e.HTTPStatus)
	}
	return e.Kind
}

// decimalBelow compares decimal strings exactly; an unparseable value or an
// empty line never raises a warning by itself.
func decimalBelow(value, line string) bool {
	v, ok := new(big.Rat).SetString(value)
	if !ok || line == "" {
		return false
	}
	l, ok := new(big.Rat).SetString(line)
	return ok && v.Cmp(l) < 0
}
