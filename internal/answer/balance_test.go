package answer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadBalanceReturnsOnlyDocumentedDecimalFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/user/balance" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("balance request method/path/authentication incorrect")
		}
		_, _ = w.Write([]byte(`{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"15.123456","granted_balance":"0.00","topped_up_balance":"15.123456"}],"undocumented":"secret"}`))
	}))
	defer server.Close()
	p, _ := NewDeepSeek(Config{APIKey: "test-key", BaseURL: server.URL})
	balance, err := p.ReadBalance(context.Background())
	if err != nil || !balance.IsAvailable || len(balance.BalanceInfos) != 1 || balance.BalanceInfos[0].Currency != "CNY" || balance.BalanceInfos[0].TotalBalance != "15.123456" {
		t.Fatal("documented decimal balance was not preserved")
	}
}

func TestProviderTransportErrorsDoNotExposeRequestDetails(t *testing.T) {
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("private-key and https://provider.invalid/chat?secret=private-query")
	})
	p, _ := NewDeepSeek(Config{APIKey: "private-key", BaseURL: "https://provider.invalid", HTTPClient: &http.Client{Transport: rt}})
	_, balanceErr := p.ReadBalance(context.Background())
	_, objectiveErr := p.GenerateObjective(context.Background(), ObjectiveRequest{Problem: objectiveFixture()})
	_, programmingErr := p.Generate(context.Background(), Request{Problem: testSnapshot()})
	for _, err := range []error{balanceErr, objectiveErr, programmingErr} {
		if err == nil || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "http") {
			t.Fatal("provider network error was not opaque")
		}
	}
	if balanceErr.(*Error).BilledUnknown || !objectiveErr.(*Error).BilledUnknown || !programmingErr.(*Error).BilledUnknown {
		t.Fatal("paid generation vs unbilled balance error accounting incorrect")
	}
}

func TestReadBalanceRejectsUntrustedAmountAndRedactsHTTPBody(t *testing.T) {
	for _, body := range []string{
		`{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"secret-key","granted_balance":"0","topped_up_balance":"1"}]}`,
		`{"is_available":true,"balance_infos":[{"currency":"not-a-currency","total_balance":"1","granted_balance":"0","topped_up_balance":"1"}]}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		p, _ := NewDeepSeek(Config{APIKey: "test-key", BaseURL: server.URL})
		_, err := p.ReadBalance(context.Background())
		server.Close()
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("balance accepted non-decimal amount or leaked response")
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "private-key", 401) }))
	defer server.Close()
	p, _ := NewDeepSeek(Config{APIKey: "test-key", BaseURL: server.URL})
	_, err := p.ReadBalance(context.Background())
	if err == nil || strings.Contains(err.Error(), "private-key") || err.(*Error).HTTPStatus != 401 {
		t.Fatal("balance HTTP error not sanitized")
	}
}

func TestDefaultProPeakCachePriceAndIntegerRounding(t *testing.T) {
	pro := DefaultPrices()["deepseek-v4-pro"]
	if pro.CacheHitMicroUSDPerMillion != 44000 {
		t.Fatal("Pro peak cache price differs from published $0.044 per million tokens")
	}
	for _, tc := range []struct{ miss, hit, output, want int64 }{
		{0, 1000000, 0, 44000},
		{1000000, 1000000, 1000000, 5324000},
		{1, 1, 1, 6},
	} {
		got, err := costMicroUSD(tc.miss, tc.hit, tc.output, pro)
		if err != nil || got != tc.want {
			t.Fatalf("integer cost=%d want=%d err=%v", got, tc.want, err)
		}
	}
}
