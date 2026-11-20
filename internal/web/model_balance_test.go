package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"educoder-cli/internal/answer"
)

func cny(total string, available bool) answer.Balance {
	return answer.Balance{IsAvailable: available, BalanceInfos: []answer.BalanceInfo{{Currency: "CNY", TotalBalance: total, GrantedBalance: "0.00", ToppedUpBalance: total}}}
}

// The overview reads the provider balance at most once per TTL, warns below
// about one heavy day of spend, and retries a failed read sooner.
func TestModelBalanceIsCachedAndWarnsWhenLow(t *testing.T) {
	now := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	reads := 0
	next := func(context.Context) (answer.Balance, error) { return cny("7.20", true), nil }
	b := newModelBalance(func(ctx context.Context) (answer.Balance, error) { reads++; return next(ctx) })
	b.now = func() time.Time { return now }

	view := b.get(context.Background())
	if reads != 1 || view == nil || !view.Available || !view.Low || view.Currency != "CNY" || view.Total != "7.20" || view.Threshold != "10" || view.Error != "" {
		t.Fatalf("first read: reads=%d view=%+v", reads, view)
	}
	now = now.Add(modelBalanceTTL - time.Second)
	if view = b.get(context.Background()); reads != 1 || view.Total != "7.20" {
		t.Fatalf("read again inside the TTL: reads=%d view=%+v", reads, view)
	}
	next = func(context.Context) (answer.Balance, error) { return cny("25.00", true), nil }
	now = now.Add(time.Second)
	if view = b.get(context.Background()); reads != 2 || view.Low || view.Total != "25.00" {
		t.Fatalf("expired balance not refreshed: reads=%d view=%+v", reads, view)
	}

	next = func(context.Context) (answer.Balance, error) {
		return answer.Balance{}, &answer.Error{Kind: "upstream_error", HTTPStatus: 401}
	}
	now = now.Add(modelBalanceTTL)
	if view = b.get(context.Background()); reads != 3 || view.Error != "upstream_error:401" || view.Low || view.Total != "" {
		t.Fatalf("failed read: reads=%d view=%+v", reads, view)
	}
	now = now.Add(modelBalanceRetryTTL - time.Second)
	if b.get(context.Background()); reads != 3 {
		t.Fatalf("failed read retried too soon: reads=%d", reads)
	}
	next = func(context.Context) (answer.Balance, error) { return cny("50.00", false), nil }
	now = now.Add(time.Second)
	if view = b.get(context.Background()); reads != 4 || !view.Low || view.Available || view.Error != "" {
		t.Fatalf("unavailable account must warn: reads=%d view=%+v", reads, view)
	}

	for _, tc := range []struct {
		currency, total string
		low             bool
	}{{"USD", "1.49", true}, {"USD", "1.50", false}, {"CNY", "9.99", true}, {"CNY", "10", false}, {"EUR", "0.01", false}, {"CNY", "bad", false}} {
		c := newModelBalance(func(context.Context) (answer.Balance, error) {
			return answer.Balance{IsAvailable: true, BalanceInfos: []answer.BalanceInfo{{Currency: tc.currency, TotalBalance: tc.total}}}, nil
		})
		if got := c.get(context.Background()); got.Low != tc.low {
			t.Errorf("%s %s: low=%v, want %v", tc.currency, tc.total, got.Low, tc.low)
		}
	}
	if (*modelBalance)(nil).get(context.Background()) != nil || newModelBalance(nil).get(context.Background()) != nil {
		t.Fatal("an unconfigured model must report no balance")
	}
	if got := modelBalanceErrorKind(errors.New("dial tcp: secret.example")); got != "unavailable" {
		t.Fatalf("non-provider error leaked detail: %q", got)
	}
}

func TestAdminOverviewReportsModelBalance(t *testing.T) {
	f := newPlatformHTTPFixture(t, nil)
	f.app.jobs.balance = newModelBalance(func(context.Context) (answer.Balance, error) { return cny("7.20", true), nil })
	got := f.request(t, f.adminClient, "GET", "/api/admin/overview", "")
	if got.StatusCode != http.StatusOK {
		t.Fatalf("overview: %d %s", got.StatusCode, got.Body)
	}
	var body struct {
		Data struct {
			ModelBalance *modelBalanceView `json:"model_balance"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(got.Body), &body); err != nil {
		t.Fatal(err)
	}
	if b := body.Data.ModelBalance; b == nil || !b.Low || b.Total != "7.20" || b.Currency != "CNY" {
		t.Fatalf("overview balance: %s", got.Body)
	}
	if user := f.request(t, f.aliceClient, "GET", "/api/admin/overview", ""); user.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin read the balance: %d", user.StatusCode)
	}
}
