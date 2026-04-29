package earnings

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ray-x/finsight/internal/config"
	"github.com/ray-x/finsight/internal/db"
)

// newTestDB creates a temporary test database and returns it along with a cleanup function.
func newTestDB(t *testing.T) (*db.DB, func()) {
	tmp := t.TempDir()
	database, err := db.Open(filepath.Join(tmp, "test.db"))
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	return database, func() {
		database.Close()
	}
}

func TestOrchestratorTriggersWorkers(t *testing.T) {
	database, cleanup := newTestDB(t)
	defer cleanup()

	cfg := &config.EarningsConfig{
		EDGARConfirmSeconds:       5,
		EDGARConfirmDurationMins:  1,
		YahooBackfillSeconds:      5,
		YahooBackfillDurationMins: 1,
	}

	orch := NewOrchestrator(database, cfg)

	// Create and insert a new IR event
	event := &db.EarningsEvent{
		Symbol:      "AAPL",
		SourceType:  "company_ir_release",
		SourceName:  "test-ir-feed",
		Title:       "Q1 2025 Earnings Release",
		URL:         "https://example.com/ir/aapl-q1-2025",
		Fingerprint: "test-fingerprint",
		ReleasedAt:  time.Now().Add(-5 * time.Minute),
		DetectedAt:  time.Now(),
		Confidence:  "high",
		Status:      "new",
	}

	// Trigger workers for this event
	orch.TriggerEventWorkers(context.Background(), event)

	// Check that workers were spawned
	orch.mu.Lock()
	wg, exists := orch.activeWorkers["AAPL"]
	orch.mu.Unlock()

	if !exists {
		t.Error("Expected workers to be spawned for AAPL, but none were created")
	}
	if wg != nil && (!wg.edgarConfirming || !wg.yahooBackfilling) {
		t.Error("Expected both EDGAR and Yahoo workers to be running")
	}

	// Verify workers are still running
	if wg.cancelCtx.Err() != nil {
		t.Error("Expected workers context to still be active, but it was cancelled")
	}
}

func TestOrchestratorIgnoresNonIREvents(t *testing.T) {
	database, cleanup := newTestDB(t)
	defer cleanup()

	cfg := &config.EarningsConfig{
		EDGARConfirmSeconds:       5,
		EDGARConfirmDurationMins:  1,
		YahooBackfillSeconds:      5,
		YahooBackfillDurationMins: 1,
	}

	orch := NewOrchestrator(database, cfg)

	// Create a non-IR event
	event := &db.EarningsEvent{
		Symbol:      "TSLA",
		SourceType:  "yahoo_backfill",
		SourceName:  "yahoo",
		Title:       "TSLA Q1 2025 Earnings",
		URL:         "https://finance.yahoo.com/quote/TSLA",
		Fingerprint: "test-fingerprint-tsla",
		ReleasedAt:  time.Now().Add(-5 * time.Minute),
		DetectedAt:  time.Now(),
		Confidence:  "medium",
		Status:      "new",
	}

	// Try to trigger workers (should be ignored)
	orch.TriggerEventWorkers(context.Background(), event)

	// Check that no workers were spawned
	orch.mu.Lock()
	_, exists := orch.activeWorkers["TSLA"]
	orch.mu.Unlock()

	if exists {
		t.Error("Expected no workers to be spawned for non-IR events")
	}
}

func TestOrchestratorHandlesNilEvent(t *testing.T) {
	database, cleanup := newTestDB(t)
	defer cleanup()

	cfg := &config.EarningsConfig{
		EDGARConfirmSeconds:       5,
		EDGARConfirmDurationMins:  1,
		YahooBackfillSeconds:      5,
		YahooBackfillDurationMins: 1,
	}

	orch := NewOrchestrator(database, cfg)

	// Try to trigger workers with nil event (should be safe)
	orch.TriggerEventWorkers(context.Background(), nil)

	// Check that no workers were spawned
	orch.mu.Lock()
	defer orch.mu.Unlock()
	if len(orch.activeWorkers) > 0 {
		t.Error("Expected no workers to be created for nil event")
	}
}

func TestOrchestratorCleanupExpiredWorkers(t *testing.T) {
	database, cleanup := newTestDB(t)
	defer cleanup()

	cfg := &config.EarningsConfig{
		EDGARConfirmSeconds:       1,
		EDGARConfirmDurationMins:  1,
		YahooBackfillSeconds:      1,
		YahooBackfillDurationMins: 1,
	}

	orch := NewOrchestrator(database, cfg)

	// Create and trigger an event
	event := &db.EarningsEvent{
		Symbol:      "NVDA",
		SourceType:  "company_ir_release",
		SourceName:  "test-ir-feed",
		Title:       "Q1 2025 Earnings Release",
		URL:         "https://example.com/ir/nvda-q1-2025",
		Fingerprint: "test-fingerprint-nvda",
		ReleasedAt:  time.Now().Add(-5 * time.Minute),
		DetectedAt:  time.Now(),
		Confidence:  "high",
		Status:      "new",
	}
	orch.TriggerEventWorkers(context.Background(), event)

	// Verify workers exist
	orch.mu.Lock()
	wg, exists := orch.activeWorkers["NVDA"]
	orch.mu.Unlock()

	if !exists {
		t.Fatal("Expected workers to be created")
	}

	// Manually set startedAt to past to simulate expiration
	orch.mu.Lock()
	wg.startedAt = time.Now().Add(-2 * time.Minute)
	orch.mu.Unlock()

	// Run checkAndSpawnWorkers to cleanup
	orch.checkAndSpawnWorkers(context.Background())

	// Verify workers were removed
	orch.mu.Lock()
	_, exists = orch.activeWorkers["NVDA"]
	orch.mu.Unlock()

	if exists {
		t.Error("Expected expired workers to be cleaned up")
	}
}

func TestOrchestratorStartAndShutdown(t *testing.T) {
	database, cleanup := newTestDB(t)
	defer cleanup()

	cfg := &config.EarningsConfig{
		EDGARConfirmSeconds:       1,
		EDGARConfirmDurationMins:  1,
		YahooBackfillSeconds:      1,
		YahooBackfillDurationMins: 1,
	}

	orch := NewOrchestrator(database, cfg)

	// Run orchestrator for a short time
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- orch.Start(ctx)
	}()

	// Wait for context to timeout
	select {
	case err := <-errChan:
		if err == nil {
			t.Error("Expected context.Canceled error, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Error("Orchestrator did not respect context cancellation")
	}
}
