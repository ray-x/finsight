package cache

import (
	"testing"
	"time"

	"github.com/ray-x/finsight/internal/yahoo"
)

func TestCacheQuotesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir)
	if err != nil {
		t.Fatalf("New cache: %v", err)
	}

	// Empty cache returns nil
	if got := c.GetQuotes([]string{"AAPL"}); got != nil {
		t.Error("expected nil for empty cache")
	}

	quotes := []yahoo.Quote{
		{Symbol: "AAPL", Name: "Apple Inc.", Price: 150.0, ChangePercent: 1.5},
		{Symbol: "MSFT", Name: "Microsoft", Price: 300.0, ChangePercent: -0.5},
	}

	c.PutQuotes(quotes)

	got := c.GetQuotes([]string{"AAPL", "MSFT"})
	if got == nil {
		t.Fatal("expected cached quotes, got nil")
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 quotes, got %d", len(got))
	}
	if got[0].Symbol != "AAPL" || got[0].Price != 150.0 {
		t.Errorf("quote mismatch: %+v", got[0])
	}

	subset := c.GetQuotes([]string{"AAPL"})
	if subset == nil || len(subset) != 1 || subset[0].Symbol != "AAPL" {
		t.Fatalf("expected AAPL-only subset, got %+v", subset)
	}

	if got := c.GetQuotes([]string{"AAPL", "GOOG"}); got != nil {
		t.Fatalf("expected nil when one requested quote is missing, got %+v", got)
	}
}

func TestCacheChartRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir)
	if err != nil {
		t.Fatalf("New cache: %v", err)
	}

	// Empty returns nil
	if got := c.GetChart("AAPL", "1d", "5m"); got != nil {
		t.Error("expected nil for empty cache")
	}

	chart := &yahoo.ChartData{
		Timestamps: []int64{1000, 2000, 3000},
		Opens:      []float64{100, 101, 102},
		Highs:      []float64{103, 104, 105},
		Lows:       []float64{99, 100, 101},
		Closes:     []float64{101, 102, 103},
		Volumes:    []int64{1000, 2000, 3000},
	}

	c.PutChart("AAPL", "1d", "5m", chart)

	got := c.GetChart("AAPL", "1d", "5m")
	if got == nil {
		t.Fatal("expected cached chart, got nil")
	}
	if len(got.Closes) != 3 {
		t.Errorf("expected 3 closes, got %d", len(got.Closes))
	}

	// Different range should miss
	if c.GetChart("AAPL", "5d", "15m") != nil {
		t.Error("different range should miss cache")
	}
}

func TestCacheStaleQuotes(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir)
	if err != nil {
		t.Fatalf("New cache: %v", err)
	}

	quotes := []yahoo.Quote{{Symbol: "AAPL", Price: 150.0}}
	c.PutQuotes(quotes)

	// Stale getter should return data even when fresh getter would
	stale := c.GetQuotesStale([]string{"AAPL"})
	if stale == nil {
		t.Error("stale getter should return data")
	}

	c.PutQuotes([]yahoo.Quote{{Symbol: "MSFT", Price: 300.0}})

	subset := c.GetQuotesStale([]string{"AAPL"})
	if subset == nil || len(subset) != 1 || subset[0].Symbol != "AAPL" {
		t.Fatalf("expected stale AAPL-only subset, got %+v", subset)
	}

	if got := c.GetQuotesStale([]string{"AAPL", "GOOG"}); got != nil {
		t.Fatalf("expected stale miss when one requested quote is missing, got %+v", got)
	}
}

func TestCacheInvalidate(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir)
	if err != nil {
		t.Fatalf("New cache: %v", err)
	}

	chart := &yahoo.ChartData{
		Closes: []float64{100, 101, 102},
	}
	c.PutQuotes([]yahoo.Quote{{Symbol: "AAPL", Price: 150}})
	c.PutChart("AAPL", "1d", "5m", chart)

	if c.GetChart("AAPL", "1d", "5m") == nil {
		t.Fatal("expected cached chart before invalidate")
	}
	if got := c.GetQuotesStale([]string{"AAPL"}); len(got) != 1 {
		t.Fatal("expected cached quote before invalidate")
	}

	c.Invalidate("AAPL")

	if c.GetChartStale("AAPL", "1d", "5m") != nil {
		t.Error("expected nil after invalidate")
	}
	if got := c.GetQuotesStale([]string{"AAPL"}); got != nil {
		t.Error("expected quote cache miss after invalidate")
	}
}

func TestSQLiteCacheInvalidate(t *testing.T) {
	dir := t.TempDir()
	sc, err := NewSQLiteCache(dir)
	if err != nil {
		t.Fatalf("NewSQLiteCache: %v", err)
	}
	defer sc.Close()
	now := time.Now()

	sc.PutQuotes([]yahoo.Quote{{Symbol: "AAPL", Price: 150}})
	sc.PutChart("AAPL", "5d", "5m", &yahoo.ChartData{
		Timestamps: []int64{now.Add(-10 * time.Minute).Unix(), now.Add(-5 * time.Minute).Unix(), now.Unix()},
		Opens:      []float64{100, 101, 102},
		Highs:      []float64{101, 102, 103},
		Lows:       []float64{99, 100, 101},
		Closes:     []float64{100, 101, 102},
		Volumes:    []int64{10, 20, 30},
	})

	if got := sc.GetQuotesStale([]string{"AAPL"}); len(got) != 1 {
		t.Fatalf("expected stale quote before invalidate, got %d", len(got))
	}
	if got := sc.GetChartStale("AAPL", "5d", "5m"); got == nil || len(got.Closes) == 0 {
		t.Fatal("expected stale chart before invalidate")
	}

	sc.Invalidate("AAPL")

	if got := sc.GetQuotesStale([]string{"AAPL"}); got != nil {
		t.Fatalf("expected nil stale quotes after invalidate, got %d", len(got))
	}
	if got := sc.GetChartStale("AAPL", "5d", "5m"); got != nil {
		t.Fatal("expected nil stale chart after invalidate")
	}
}

func TestSQLiteCacheIgnoresJSONQuoteFallback(t *testing.T) {
	dir := t.TempDir()
	sc, err := NewSQLiteCache(dir)
	if err != nil {
		t.Fatalf("NewSQLiteCache: %v", err)
	}
	defer sc.Close()

	sc.jsonCache.PutQuotes([]yahoo.Quote{{Symbol: "AAPL", Price: 123}})

	if got := sc.GetQuotes([]string{"AAPL"}); got != nil {
		t.Fatalf("expected sqlite-only GetQuotes miss, got %d quote(s)", len(got))
	}
	if got := sc.GetQuotesStale([]string{"AAPL"}); got != nil {
		t.Fatalf("expected sqlite-only GetQuotesStale miss, got %d quote(s)", len(got))
	}
}

func TestSQLiteCacheIgnoresJSONChartFallback(t *testing.T) {
	dir := t.TempDir()
	sc, err := NewSQLiteCache(dir)
	if err != nil {
		t.Fatalf("NewSQLiteCache: %v", err)
	}
	defer sc.Close()
	now := time.Now()

	sc.jsonCache.PutChart("AAPL", "5d", "5m", &yahoo.ChartData{
		Timestamps: []int64{now.Add(-10 * time.Minute).Unix(), now.Add(-5 * time.Minute).Unix(), now.Unix()},
		Opens:      []float64{100, 101, 102},
		Highs:      []float64{101, 102, 103},
		Lows:       []float64{99, 100, 101},
		Closes:     []float64{100, 101, 102},
		Volumes:    []int64{10, 20, 30},
	})

	if got := sc.GetChart("AAPL", "5d", "5m"); got != nil {
		t.Fatal("expected sqlite-only GetChart miss")
	}
	if got := sc.GetChartStale("AAPL", "5d", "5m"); got != nil {
		t.Fatal("expected sqlite-only GetChartStale miss")
	}
}

func TestCacheFinancialsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir)
	if err != nil {
		t.Fatalf("New cache: %v", err)
	}

	fd := &yahoo.FinancialData{
		RevenueTTM:   1000000,
		ProfitMargin: 0.25,
	}

	c.PutFinancials("AAPL", fd)

	got := c.GetFinancials("AAPL")
	if got == nil {
		t.Fatal("expected cached financials")
	}
	if got.RevenueTTM != 1000000 {
		t.Errorf("expected RevenueTTM=1000000, got %d", got.RevenueTTM)
	}

	// Stale should work too
	stale := c.GetFinancialsStale("AAPL")
	if stale == nil {
		t.Error("stale financials should return data")
	}
}

func TestSanitizeKey(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"AAPL", "AAPL"},
		{"^GSPC", "_GSPC"},
		{"BRK.B", "BRK.B"},
		{"BTC-USD", "BTC-USD"},
		{"a b c", "a_b_c"},
	}

	for _, tt := range tests {
		got := sanitizeKey(tt.input)
		if got != tt.expected {
			t.Errorf("sanitizeKey(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// Verify TTL constants are sensible.
func TestCacheTTLValues(t *testing.T) {
	if cacheTTL != 15*time.Minute {
		t.Errorf("expected cacheTTL=15m, got %v", cacheTTL)
	}
	if dailyCacheTTL != 24*time.Hour {
		t.Errorf("expected dailyCacheTTL=24h, got %v", dailyCacheTTL)
	}
}
