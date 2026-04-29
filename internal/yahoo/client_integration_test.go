//go:build integration
// +build integration

package yahoo

import (
	"testing"
)

// Integration tests that hit the real Yahoo Finance API.
// Run with: go test -tags=integration ./internal/yahoo/
// Skipped in normal test runs.

func TestIntegrationGetQuotes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	client := NewClient()

	quotes, err := client.GetQuotes([]string{"AAPL"})
	if err != nil {
		t.Fatalf("GetQuotes failed: %v", err)
	}

	if len(quotes) == 0 {
		t.Fatal("expected at least 1 quote")
	}

	q := quotes[0]
	if q.Symbol != "AAPL" {
		t.Errorf("expected symbol AAPL, got %s", q.Symbol)
	}
	if q.Price <= 0 {
		t.Errorf("expected positive price, got %f", q.Price)
	}
	if q.Name == "" {
		t.Error("expected non-empty name")
	}
	if q.MarketCap <= 0 {
		t.Error("expected positive market cap")
	}
}

func TestIntegrationGetChart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	client := NewClient()

	data, err := client.GetChart("AAPL", "5d", "1d")
	if err != nil {
		t.Fatalf("GetChart failed: %v", err)
	}

	if data == nil {
		t.Fatal("expected chart data, got nil")
	}

	if len(data.Closes) == 0 {
		t.Error("expected non-empty closes")
	}
	if len(data.Timestamps) == 0 {
		t.Error("expected non-empty timestamps")
	}
	if len(data.Closes) != len(data.Timestamps) {
		t.Errorf("closes(%d) and timestamps(%d) length mismatch",
			len(data.Closes), len(data.Timestamps))
	}

	// Prices should be positive
	for i, c := range data.Closes {
		if c <= 0 {
			t.Errorf("close[%d] = %f, expected positive", i, c)
			break
		}
	}
}

func TestIntegrationGetKeyStats(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	client := NewClient()

	stats, err := client.GetKeyStats("AAPL")
	if err != nil {
		t.Fatalf("GetKeyStats failed: %v", err)
	}

	if stats == nil {
		t.Fatal("expected key stats, got nil")
	}

	// AAPL should have a positive beta
	if stats.Beta <= 0 {
		t.Logf("warning: Beta=%f (may be 0 for some periods)", stats.Beta)
	}
}

func TestIntegrationGetFinancials(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	client := NewClient()

	fd, err := client.GetFinancials("AAPL")
	if err != nil {
		t.Fatalf("GetFinancials failed: %v", err)
	}

	if fd == nil {
		t.Fatal("expected financial data, got nil")
	}

	if fd.RevenueTTM <= 0 {
		t.Errorf("expected positive RevenueTTM, got %d", fd.RevenueTTM)
	}
	if fd.ProfitMargin == 0 {
		t.Error("expected non-zero ProfitMargin for AAPL")
	}
	if fd.EBITDA <= 0 {
		t.Errorf("expected positive EBITDA, got %d", fd.EBITDA)
	}
	if fd.EnterpriseValue <= 0 {
		t.Errorf("expected positive EnterpriseValue, got %d", fd.EnterpriseValue)
	}
	if len(fd.RecommendationTrend) == 0 {
		t.Error("expected non-empty recommendation trend")
	}
}

func TestIntegrationGetRecommendedSymbols(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	client := NewClient()

	syms, err := client.GetRecommendedSymbols("AAPL")
	if err != nil {
		t.Fatalf("GetRecommendedSymbols failed: %v", err)
	}

	if len(syms) == 0 {
		t.Fatal("expected at least 1 recommended symbol")
	}

	for _, s := range syms {
		if s.Symbol == "" {
			t.Error("expected non-empty symbol")
		}
		if s.Score <= 0 {
			t.Errorf("expected positive score for %s, got %f", s.Symbol, s.Score)
		}
	}
}

func TestIntegrationSearch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	client := NewClient()

	results, err := client.Search("Apple")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected search results for 'Apple'")
	}

	foundAAPL := false
	for _, r := range results {
		if r.Symbol == "AAPL" {
			foundAAPL = true
			break
		}
	}
	if !foundAAPL {
		t.Error("expected AAPL in search results for 'Apple'")
	}
}
