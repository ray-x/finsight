package earnings

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDiscoverEarningsWebLinksFromPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`
<!doctype html>
<html><body>
  <a href="/investor/quarterly-results-q1-2026.pdf">Q1 2026 Earnings Release PDF</a>
  <a href="/news/product-launch">Product Launch</a>
</body></html>`))
	}))
	defer srv.Close()

	res, err := DiscoverEarningsWebLinks(context.Background(), "AAPL", DiscoveryOptions{
		PageURL:       srv.URL,
		MaxCandidates: 5,
	})
	if err != nil {
		t.Fatalf("discover failed: %v", err)
	}
	if res == nil || len(res.Candidates) == 0 {
		t.Fatal("expected candidates from investor page")
	}
	if res.Candidates[0].Kind != "pdf" {
		t.Fatalf("expected top candidate to be pdf, got %s", res.Candidates[0].Kind)
	}
	if !strings.Contains(strings.ToLower(res.Candidates[0].URL), "q1-2026") {
		t.Fatalf("unexpected top candidate url: %s", res.Candidates[0].URL)
	}
}

func TestDiscoverEarningsWebLinksFromSearchHint(t *testing.T) {
	originalRSS := googleNewsRSSURL
	defer func() { googleNewsRSSURL = originalRSS }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?>
<rss version="2.0"><channel>
  <item>
    <title>Apple Q1 Earnings Report PDF</title>
    <link>https://example.com/reports/apple-q1-earnings.pdf</link>
  </item>
</channel></rss>`))
	}))
	defer srv.Close()
	googleNewsRSSURL = srv.URL

	res, err := DiscoverEarningsWebLinks(context.Background(), "AAPL", DiscoveryOptions{
		SearchHint:    "Apple earnings report",
		MaxCandidates: 5,
	})
	if err != nil {
		t.Fatalf("discover failed: %v", err)
	}
	if res == nil || len(res.Candidates) == 0 {
		t.Fatal("expected search-hint candidates")
	}
	found := false
	for _, c := range res.Candidates {
		if c.Source == "news_search" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected at least one news_search candidate, got %+v", res.Candidates)
	}
}

func TestDiscoverEarningsWebLinksWithExternalCrawler(t *testing.T) {
	res, err := DiscoverEarningsWebLinks(context.Background(), "AAPL", DiscoveryOptions{
		PageURL:       "https://example.com/investor",
		MaxCandidates: 5,
		CrawlerCmd:    "sh",
		CrawlerArgs: []string{
			"-c",
			`printf '%s' '<html><body><a href="https://investor.apple.com/investor-relations/default.aspx">Apple Investor Relations</a></body></html>'`,
		},
	})
	if err != nil {
		t.Fatalf("discover failed: %v", err)
	}
	if res == nil || len(res.Candidates) == 0 {
		t.Fatal("expected candidates from external crawler output")
	}
	if res.Candidates[0].Source != "ir_page_crawler" {
		t.Fatalf("expected source ir_page_crawler, got %s", res.Candidates[0].Source)
	}
	if !strings.Contains(res.Candidates[0].URL, "investor.apple.com") {
		t.Fatalf("unexpected crawler candidate url: %s", res.Candidates[0].URL)
	}
}

func TestDiscoverEarningsWebLinksCrawlerFallbackToHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><body><a href="/investor/quarterly-results-q1-2026.pdf">Q1 2026 Earnings Release PDF</a></body></html>`))
	}))
	defer srv.Close()

	res, err := DiscoverEarningsWebLinks(context.Background(), "AAPL", DiscoveryOptions{
		PageURL:       srv.URL,
		MaxCandidates: 5,
		CrawlerCmd:    "definitely-not-a-real-command",
	})
	if err != nil {
		t.Fatalf("discover failed: %v", err)
	}
	if res == nil || len(res.Candidates) == 0 {
		t.Fatal("expected candidates from HTTP fallback")
	}
	foundIRPage := false
	for _, c := range res.Candidates {
		if c.Source == "ir_page" {
			foundIRPage = true
			break
		}
	}
	if !foundIRPage {
		t.Fatalf("expected fallback candidates with source ir_page, got %+v", res.Candidates)
	}
}

// TestDiscoverFromDDGWebSearch verifies that DuckDuckGo HTML result links are
// parsed and scored correctly. The package-level ddgSearchURL is overridden to
// point at a local test server that returns a fake DDG HTML page.
func TestDiscoverFromDDGWebSearch(t *testing.T) {
	originalDDG := ddgSearchURL
	defer func() { ddgSearchURL = originalDDG }()

	// Simulate the DuckDuckGo HTML result page structure.
	// Real DDG uses class="result__a" on result anchors.
	fakeDDGHTML := `<!doctype html><html><body>
<div class="results">
  <a class="result__a" href="https://investor.example.com/earnings/q1-2026-earnings-release.pdf">Apple Q1 2026 Earnings Release PDF</a>
  <a class="result__a" href="https://investor.example.com/earnings/results-page">Quarterly Results</a>
  <a class="result__a" href="https://example.com/unrelated-news-article">Unrelated Article</a>
</div>
</body></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(fakeDDGHTML))
	}))
	defer srv.Close()
	ddgSearchURL = srv.URL

	candidates, err := discoverFromWebSearch(context.Background(), &http.Client{}, "AAPL", "Apple", "Apple earnings report investor relations")
	if err != nil {
		t.Fatalf("discoverFromWebSearch failed: %v", err)
	}
	if len(candidates) == 0 {
		t.Fatal("expected candidates from DDG search results")
	}
	// The PDF link should score highest
	if candidates[0].Kind != "pdf" {
		t.Errorf("expected top candidate to be pdf, got %q (url=%s)", candidates[0].Kind, candidates[0].URL)
	}
	for _, c := range candidates {
		if c.Source != "web_search" {
			t.Errorf("expected source=web_search, got %q for %s", c.Source, c.URL)
		}
	}
}

func TestDiscoverFromDDGWebSearchPrefersOfficialInvestorHost(t *testing.T) {
	originalDDG := ddgSearchURL
	defer func() { ddgSearchURL = originalDDG }()

	fakeDDGHTML := `<!doctype html><html><body>
<div class="results">
  <a class="result__a" href="https://example.com/apple-q1-2026-earnings.pdf">Apple Q1 2026 Earnings Report PDF</a>
  <a class="result__a" href="https://investor.apple.com/investor-relations/default.aspx">Apple Investor Relations</a>
  <a class="result__a" href="https://news.example.com/apple-quarterly-results">Apple quarterly results recap</a>
</div>
</body></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(fakeDDGHTML))
	}))
	defer srv.Close()
	ddgSearchURL = srv.URL

	candidates, err := discoverFromWebSearch(context.Background(), &http.Client{}, "AAPL", "Apple", "Apple earnings report investor relations")
	if err != nil {
		t.Fatalf("discoverFromWebSearch failed: %v", err)
	}
	if len(candidates) < 2 {
		t.Fatalf("expected multiple candidates, got %+v", candidates)
	}
	if candidates[0].URL != "https://investor.apple.com/investor-relations/default.aspx" {
		t.Fatalf("expected official Apple investor relations page to rank first, got %+v", candidates)
	}
}

// TestDiscoverFromGoogleNewsRSS verifies Google News RSS parsing and scoring.
func TestDiscoverFromGoogleNewsRSS(t *testing.T) {
	originalRSS := googleNewsRSSURL
	defer func() { googleNewsRSSURL = originalRSS }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify query param is passed
		if !strings.Contains(r.URL.RawQuery, "q=") {
			t.Errorf("expected q= param in Google News RSS request, got: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?>
<rss version="2.0"><channel>
  <item>
    <title>NVIDIA Q1 2026 Earnings Report – Investor Relations</title>
    <link>https://investor.nvidia.com/reports/q1-2026-earnings-report.pdf</link>
  </item>
  <item>
    <title>NVIDIA CEO on quarterly results</title>
    <link>https://news.example.com/nvidia-ceo-interview</link>
  </item>
</channel></rss>`))
	}))
	defer srv.Close()
	googleNewsRSSURL = srv.URL

	candidates, err := discoverFromGoogleNewsRSS(context.Background(), &http.Client{}, "NVIDIA earnings press release")
	if err != nil {
		t.Fatalf("discoverFromGoogleNewsRSS failed: %v", err)
	}
	if len(candidates) == 0 {
		t.Fatal("expected candidates from Google News RSS")
	}
	// PDF report should rank first
	if candidates[0].Kind != "pdf" {
		t.Errorf("expected top candidate to be pdf, got %q (url=%s)", candidates[0].Kind, candidates[0].URL)
	}
	for _, c := range candidates {
		if c.Source != "news_search" {
			t.Errorf("expected source=news_search, got %q for %s", c.Source, c.URL)
		}
	}
}

// TestDiscoverFromDDGWebSearchIntegration performs a live DuckDuckGo HTML
// search for well-known companies to validate end-to-end parsing behavior.
//
// Run explicitly (network required):
// GOEXPERIMENT=jsonv2 go test ./internal/earnings -run TestDiscoverFromDDGWebSearchIntegration -v
func TestDiscoverFromDDGWebSearchIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	client := &http.Client{Timeout: 15 * time.Second}
	tests := []struct {
		name    string
		symbol  string
		company string
		hint    string
	}{
		{
			name:    "apple",
			symbol:  "AAPL",
			company: "Apple",
			hint:    "Apple earnings press release investor relations",
		},
		{
			name:    "nvidia",
			symbol:  "NVDA",
			company: "NVIDIA",
			hint:    "NVIDIA earnings press release investor relations",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			candidates, err := discoverFromWebSearch(context.Background(), client, tc.symbol, tc.company, tc.hint)
			if err != nil {
				t.Skipf("live DDG request failed (network/rate-limit/endpoint issue): %v", err)
			}
			if len(candidates) == 0 {
				t.Fatalf("expected live DDG candidates for %s", tc.symbol)
			}

			for i, c := range candidates {
				if c.Source != "web_search" {
					t.Fatalf("candidate %d has unexpected source %q", i, c.Source)
				}
				if !strings.HasPrefix(c.URL, "http://") && !strings.HasPrefix(c.URL, "https://") {
					t.Fatalf("candidate %d has non-http(s) URL: %s", i, c.URL)
				}
			}

			// Sanity: at least one result should look earnings-related.
			foundEarningsLike := false
			for _, c := range candidates {
				text := strings.ToLower(c.Title + " " + c.URL)
				if strings.Contains(text, "earnings") || strings.Contains(text, "results") || strings.Contains(text, "quarter") {
					foundEarningsLike = true
					break
				}
			}
			if !foundEarningsLike {
				t.Fatalf("no earnings-like candidate found for %s; got %+v", tc.symbol, candidates)
			}
		})
	}
}

// TestDiscoverEarningsWebLinksIntegration performs a live end-to-end discovery
// through DiscoverEarningsWebLinks, including web search and RSS hints.
//
// Run explicitly (network required):
// GOEXPERIMENT=jsonv2 go test ./internal/earnings -run TestDiscoverEarningsWebLinksIntegration -v
func TestDiscoverEarningsWebLinksIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	res, err := DiscoverEarningsWebLinks(ctx, "AAPL", DiscoveryOptions{
		CompanyName:   "Apple",
		SearchHint:    "Apple earnings press release investor relations",
		MaxCandidates: 8,
		HTTP:          &http.Client{Timeout: 15 * time.Second},
	})
	if err != nil {
		t.Skipf("live discovery failed (network/rate-limit/endpoint issue): %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil discovery result")
	}
	if len(res.Candidates) == 0 {
		t.Fatalf("expected at least one candidate, got %+v", res)
	}

	foundExpectedSource := false
	foundEarningsLike := false
	for i, c := range res.Candidates {
		if c.Source == "web_search" || c.Source == "news_search" || c.Source == "ir_page" {
			foundExpectedSource = true
		}
		if !strings.HasPrefix(c.URL, "http://") && !strings.HasPrefix(c.URL, "https://") {
			t.Fatalf("candidate %d has non-http(s) URL: %s", i, c.URL)
		}
		text := strings.ToLower(c.Title + " " + c.URL)
		if strings.Contains(text, "earnings") || strings.Contains(text, "result") || strings.Contains(text, "quarter") || strings.Contains(text, "financial") {
			foundEarningsLike = true
		}
	}
	if !foundExpectedSource {
		t.Fatalf("expected at least one candidate from recognized discovery source, got %+v", res.Candidates)
	}
	if !foundEarningsLike {
		t.Fatalf("expected at least one earnings-like candidate, got %+v", res.Candidates)
	}
}
