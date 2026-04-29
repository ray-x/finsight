package db

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestOpenDatabase tests database initialization and schema creation.
func TestOpenDatabase(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Verify database file was created
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Fatalf("Database file was not created at %s", dbPath)
	}

	// Test health check
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.Health(ctx); err != nil {
		t.Fatalf("Health check failed: %v", err)
	}
}

// TestQuoteLatestUpsert tests inserting and retrieving quotes.
func TestQuoteLatestUpsert(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Insert a quote
	quote := &QuoteLatest{
		Symbol:           "AAPL",
		Price:            150.25,
		Change:           2.50,
		ChangePct:        1.69,
		PERatio:          28.5,
		ForwardPE:        26.2,
		FiftyTwoWeekHigh: 200.0,
		FiftyTwoWeekLow:  120.0,
		QuoteSource:      "yahoo",
		QuoteUpdatedAt:   time.Now(),
		SyncedAt:         time.Now(),
	}

	if err := db.UpsertQuoteLatest(ctx, quote); err != nil {
		t.Fatalf("Failed to upsert quote: %v", err)
	}

	// Retrieve the quote
	retrieved, err := db.GetQuoteLatest(ctx, "AAPL")
	if err != nil {
		t.Fatalf("Failed to retrieve quote: %v", err)
	}

	if retrieved == nil {
		t.Fatal("Retrieved quote is nil")
	}

	if retrieved.Symbol != "AAPL" || retrieved.Price != 150.25 {
		t.Fatalf("Quote data mismatch: got %+v", retrieved)
	}

	// Test stale check
	stale, err := db.IsQuoteStale(ctx, "AAPL", 1*time.Minute)
	if err != nil {
		t.Fatalf("Failed to check staleness: %v", err)
	}
	if stale {
		t.Error("Quote should not be stale yet")
	}
}

// TestPriceBarUpsert tests inserting and retrieving price bars with expiry.
func TestPriceBarUpsert(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	now := time.Now()

	// Insert intraday bar (15min expiry)
	bar := &PriceBar{
		Symbol:        "AAPL",
		Interval:      "5m",
		OpenTime:      now,
		Open:          150.0,
		High:          152.0,
		Low:           149.5,
		Close:         151.5,
		Volume:        1000000,
		ExpirySeconds: IntradayBarTTL,
		SyncedAt:      now,
	}

	if err := db.UpsertPriceBar(ctx, bar); err != nil {
		t.Fatalf("Failed to upsert bar: %v", err)
	}

	// Retrieve bars
	bars, err := db.GetPriceBars(ctx, "AAPL", "5m", 10)
	if err != nil {
		t.Fatalf("Failed to retrieve bars: %v", err)
	}

	if len(bars) != 1 {
		t.Fatalf("Expected 1 bar, got %d", len(bars))
	}

	if bars[0].Close != 151.5 {
		t.Fatalf("Bar data mismatch: got %+v", bars[0])
	}

	// Check freshness
	fresh, err := db.HasFreshBars(ctx, "AAPL", "5m")
	if err != nil {
		t.Fatalf("Failed to check fresh bars: %v", err)
	}
	if !fresh {
		t.Error("Bars should be fresh")
	}
}

// TestEarningsRecordUpsert tests earnings record persistence.
func TestEarningsRecordUpsert(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Insert earnings record
	record := &EarningsRecord{
		Symbol:           "AAPL",
		FiscalPeriod:     "2025Q1",
		PeriodType:       "quarterly",
		AnnouncementDate: time.Now(),
		EPSActual:        1.95,
		EPSEstimate:      1.90,
		RevenueActual:    100.2e9,
		RevenueEstimate:  99.5e9,
		SurprisePct:      2.63,
		SyncedAt:         time.Now(),
	}

	if err := db.UpsertEarningsRecord(ctx, record); err != nil {
		t.Fatalf("Failed to upsert earnings: %v", err)
	}

	// Retrieve record
	retrieved, err := db.GetEarningsRecord(ctx, "AAPL", "2025Q1")
	if err != nil {
		t.Fatalf("Failed to retrieve earnings: %v", err)
	}

	if retrieved == nil || retrieved.EPSActual != 1.95 {
		t.Fatalf("Earnings data mismatch: got %+v", retrieved)
	}

	// Retrieve records list
	records, err := db.GetEarningsRecords(ctx, "AAPL", 10)
	if err != nil {
		t.Fatalf("Failed to retrieve earnings list: %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("Expected 1 record, got %d", len(records))
	}
}

// TestDocumentStorage tests document persistence and FTS search.
func TestDocumentStorage(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Insert document
	doc := &Document{
		Symbol:       "AAPL",
		DocumentType: "10-K",
		FiscalPeriod: "FY2024",
		Title:        "Annual Report 2024",
		Source:       "SEC",
		Content:      "Apple Inc. reported record revenue in fiscal 2024...",
		SyncedAt:     time.Now(),
	}

	if err := db.UpsertDocument(ctx, doc); err != nil {
		t.Fatalf("Failed to upsert document: %v", err)
	}

	// Retrieve document
	retrieved, err := db.GetDocuments(ctx, "AAPL", "10-K", 10)
	if err != nil {
		t.Fatalf("Failed to retrieve documents: %v", err)
	}

	if len(retrieved) != 1 || retrieved[0].Title != "Annual Report 2024" {
		t.Fatalf("Document data mismatch: got %+v", retrieved)
	}

	// Test FTS search
	results, err := db.SearchDocuments(ctx, "revenue", 5)
	if err != nil {
		t.Fatalf("Failed to search documents: %v", err)
	}

	if len(results) == 0 {
		t.Error("Expected to find document with 'revenue' keyword")
	}
}

// TestLLMReportStorage tests LLM report persistence and section extraction.
func TestLLMReportStorage(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Insert report with sections
	markdown := `# Apple Inc. Analysis

## Financial Overview
Apple reported strong revenue growth in Q1 2025.

### Revenue Trends
Revenue increased by 15% year-over-year.

## Market Sentiment
Analyst ratings remain bullish for Apple stock.
`

	report := &LLMReport{
		Symbol:       "AAPL",
		FiscalPeriod: "2025Q1",
		Title:        "Q1 2025 Analysis",
		CreatedAt:    time.Now(),
		SyncedAt:     time.Now(),
	}

	if err := db.UpsertLLMReport(ctx, report, markdown); err != nil {
		t.Fatalf("Failed to upsert report: %v", err)
	}

	// Retrieve report
	reports, err := db.GetLLMReports(ctx, "AAPL", 10)
	if err != nil {
		t.Fatalf("Failed to retrieve reports: %v", err)
	}

	if len(reports) != 1 {
		t.Fatalf("Expected 1 report, got %d", len(reports))
	}

	// Check sections were extracted
	sections, err := db.GetLLMReportSections(ctx, reports[0].ID)
	if err != nil {
		t.Fatalf("Failed to retrieve sections: %v", err)
	}

	if len(sections) < 2 {
		t.Errorf("Expected at least 2 sections, got %d", len(sections))
	}

	// Verify section content
	for _, s := range sections {
		if s.HeadingLevel != 2 && s.HeadingLevel != 3 {
			t.Errorf("Invalid heading level: %d", s.HeadingLevel)
		}
	}

	// Test FTS search
	results, err := db.SearchLLMReports(ctx, "revenue", 5)
	if err != nil {
		t.Fatalf("Failed to search reports: %v", err)
	}

	if len(results) == 0 {
		t.Error("Expected to find report section with 'revenue' keyword")
	}
}

func TestEarningsEventAndFeedStateStorage(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Now().UTC()

	state := &IRFeedState{
		FeedURL:       "https://example.com/rss.xml",
		ETag:          "\"abc123\"",
		LastModified:  now.Format(time.RFC1123),
		LastCheckedAt: now,
		LastStatus:    200,
	}
	if err := db.UpsertIRFeedState(ctx, state); err != nil {
		t.Fatalf("failed to upsert feed state: %v", err)
	}

	gotState, err := db.GetIRFeedState(ctx, state.FeedURL)
	if err != nil {
		t.Fatalf("failed to get feed state: %v", err)
	}
	if gotState == nil || gotState.ETag != state.ETag {
		t.Fatalf("feed state mismatch: %+v", gotState)
	}

	ev := &EarningsEvent{
		Symbol:      "AAPL",
		SourceType:  "company_ir_release",
		SourceName:  "Apple Investor Relations",
		Title:       "Apple reports quarterly results",
		URL:         "https://investor.apple.com/news",
		ReleasedAt:  now,
		DetectedAt:  now,
		IngestedAt:  now,
		Fingerprint: "fp-1",
		Confidence:  "high",
		Status:      "new",
	}
	inserted, err := db.InsertEarningsEventIfNew(ctx, ev)
	if err != nil {
		t.Fatalf("failed to insert event: %v", err)
	}
	if !inserted {
		t.Fatal("expected event to be newly inserted")
	}

	inserted, err = db.InsertEarningsEventIfNew(ctx, ev)
	if err != nil {
		t.Fatalf("failed to re-insert event: %v", err)
	}
	if inserted {
		t.Fatal("expected duplicate event to be ignored")
	}

	events, err := db.GetRecentEarningsEvents(ctx, "AAPL", 10)
	if err != nil {
		t.Fatalf("failed to list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
}

// BenchmarkQuoteUpsert benchmarks quote insertion/update.
func BenchmarkQuoteUpsert(b *testing.B) {
	tmpDir := b.TempDir()
	dbPath := filepath.Join(tmpDir, "bench.db")

	db, err := Open(dbPath)
	if err != nil {
		b.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	quote := &QuoteLatest{
		Symbol:   "AAPL",
		Price:    150.25,
		PERatio:  28.5,
		SyncedAt: time.Now(),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		db.UpsertQuoteLatest(ctx, quote)
	}
}
