package earnings

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/ray-x/finsight/internal/config"
	"github.com/ray-x/finsight/internal/db"
)

// Orchestrator manages background workers for EDGAR confirmation and Yahoo backfill
// after IR feed events are detected. It uses config-driven timing knobs to control
// worker cadence and duration.
type Orchestrator struct {
	db             *db.DB
	cfg            *config.EarningsConfig
	httpClient     *http.Client
	mu             sync.Mutex
	activeWorkers  map[string]*workerGroup
	lastEventCheck time.Time
}

// workerGroup tracks active workers for a symbol during a confirmation/backfill window.
type workerGroup struct {
	symbol           string
	startedAt        time.Time
	cancelCtx        context.Context
	cancel           context.CancelFunc
	edgarConfirming  bool
	yahooBackfilling bool
	edgarWorkerDone  chan struct{}
	yahooWorkerDone  chan struct{}
}

// NewOrchestrator creates a new background job orchestrator.
func NewOrchestrator(database *db.DB, cfg *config.EarningsConfig) *Orchestrator {
	if cfg == nil {
		cfg = &config.EarningsConfig{}
	}
	return &Orchestrator{
		db:            database,
		cfg:           cfg,
		httpClient:    &http.Client{Timeout: 30 * time.Second},
		activeWorkers: make(map[string]*workerGroup),
	}
}

// Start begins the background job orchestrator. It polls for new IR events
// and spawns EDGAR/Yahoo workers when appropriate. Blocks until ctx is cancelled.
func (o *Orchestrator) Start(ctx context.Context) error {
	if o.cfg == nil || o.db == nil {
		return nil
	}

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			o.shutdownAll()
			return ctx.Err()
		case <-ticker.C:
			o.checkAndSpawnWorkers(ctx)
		}
	}
}

// TriggerEventWorkers spawns EDGAR and Yahoo workers for a detected IR event.
// This is called when a new IR event is detected (typically by the poller).
func (o *Orchestrator) TriggerEventWorkers(ctx context.Context, event *db.EarningsEvent) {
	if o.cfg == nil || o.db == nil || event == nil {
		return
	}
	if event.SourceType != "company_ir_release" {
		return
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	symbol := event.Symbol
	wg, exists := o.activeWorkers[symbol]

	if !exists {
		// New symbol: spawn workers
		workerCtx, cancel := context.WithCancel(context.Background())
		wg = &workerGroup{
			symbol:          symbol,
			startedAt:       time.Now(),
			cancelCtx:       workerCtx,
			cancel:          cancel,
			edgarWorkerDone: make(chan struct{}),
			yahooWorkerDone: make(chan struct{}),
		}
		o.activeWorkers[symbol] = wg

		// Spawn EDGAR confirmation worker
		if o.cfg.EDGARConfirmSeconds > 0 && o.cfg.EDGARConfirmDurationMins > 0 {
			wg.edgarConfirming = true
			go o.runEDGARConfirmationWorker(workerCtx, symbol, event, wg)
		}

		// Spawn Yahoo backfill worker
		if o.cfg.YahooBackfillSeconds > 0 && o.cfg.YahooBackfillDurationMins > 0 {
			wg.yahooBackfilling = true
			go o.runYahooBackfillWorker(workerCtx, symbol, event, wg)
		}
	}
}

// checkAndSpawnWorkers cleans up expired worker groups and periodically
// polls for new events (placeholder for production multi-symbol iteration).
func (o *Orchestrator) checkAndSpawnWorkers(ctx context.Context) {
	o.mu.Lock()
	defer o.mu.Unlock()

	// TODO: In production, we'd need to iterate through registered symbols
	// For now, this is a placeholder that shows the worker lifecycle.
	// The orchestrator will be triggered when IR events are detected
	// through the poller's event insertion.

	// Clean up expired workers
	for symbol, wg := range o.activeWorkers {
		elapsed := time.Since(wg.startedAt)
		maxDuration := time.Duration(max(o.cfg.EDGARConfirmDurationMins, o.cfg.YahooBackfillDurationMins)) * time.Minute

		if elapsed > maxDuration {
			wg.cancel()
			delete(o.activeWorkers, symbol)
		}
	}
}

// runEDGARConfirmationWorker polls EDGAR at configured intervals for earnings filings
// that match the IR event, storing any discovered filings as "edgar_confirmation" events.
// Runs until ctx is cancelled or maxDuration is reached.
func (o *Orchestrator) runEDGARConfirmationWorker(ctx context.Context, symbol string, triggerEvent *db.EarningsEvent, wg *workerGroup) {
	defer close(wg.edgarWorkerDone)

	ticker := time.NewTicker(time.Duration(o.cfg.EDGARConfirmSeconds) * time.Second)
	defer ticker.Stop()

	maxDuration := time.Duration(o.cfg.EDGARConfirmDurationMins) * time.Minute
	startedAt := time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if time.Since(startedAt) > maxDuration {
				return
			}

			// TODO: Implement EDGAR confirmation logic
			// For now, this is a placeholder that could:
			// 1. Query EDGAR API for recent filings for symbol
			// 2. Match against triggerEvent details (title, release date)
			// 3. Store any matches as db.EarningsEvent with source_type="edgar_confirmation"
			_ = o.confirmEarningsFromEDGAR(ctx, symbol, triggerEvent)
		}
	}
}

// runYahooBackfillWorker polls Yahoo Finance at configured intervals for earnings data
// that matches the IR event, storing any discovered data as "yahoo_backfill" events.
// Runs until ctx is cancelled or maxDuration is reached.
func (o *Orchestrator) runYahooBackfillWorker(ctx context.Context, symbol string, triggerEvent *db.EarningsEvent, wg *workerGroup) {
	defer close(wg.yahooWorkerDone)

	ticker := time.NewTicker(time.Duration(o.cfg.YahooBackfillSeconds) * time.Second)
	defer ticker.Stop()

	maxDuration := time.Duration(o.cfg.YahooBackfillDurationMins) * time.Minute
	startedAt := time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if time.Since(startedAt) > maxDuration {
				return
			}

			// TODO: Implement Yahoo backfill logic
			// For now, this is a placeholder that could:
			// 1. Query Yahoo Finance API for earnings data for symbol
			// 2. Extract EPS, revenue, guidance if available
			// 3. Normalize timestamps to match IR event release date
			// 4. Store as db.EarningsEvent with source_type="yahoo_backfill"
			_ = o.backfillEarningsFromYahoo(ctx, symbol, triggerEvent)
		}
	}
}

// confirmEarningsFromEDGAR attempts to fetch and store earnings confirmation from EDGAR.
// Returns true if a new event was stored, false otherwise.
func (o *Orchestrator) confirmEarningsFromEDGAR(ctx context.Context, symbol string, triggerEvent *db.EarningsEvent) bool {
	if o.db == nil {
		return false
	}

	// Placeholder: In a full implementation, this would:
	// 1. Call EDGAR API (e.g., via edgar package client)
	// 2. Look for recent 8-K or 10-Q/10-K filings
	// 3. Extract earnings data and store as EarningsEvent

	// For now, we just record that we attempted confirmation
	_ = ctx
	_ = symbol
	_ = triggerEvent

	return false
}

// backfillEarningsFromYahoo attempts to fetch and store earnings data from Yahoo.
// Returns true if a new event was stored, false otherwise.
func (o *Orchestrator) backfillEarningsFromYahoo(ctx context.Context, symbol string, triggerEvent *db.EarningsEvent) bool {
	if o.db == nil {
		return false
	}

	// Placeholder: In a full implementation, this would:
	// 1. Call Yahoo Finance API (e.g., via yahoo package client)
	// 2. Look for recent earnings in the earnings calendar
	// 3. Extract EPS actual, estimate, revenue, etc.
	// 4. Normalize dates to match trigger event
	// 5. Store as EarningsEvent

	// For now, we just record that we attempted backfill
	_ = ctx
	_ = symbol
	_ = triggerEvent

	return false
}

// shutdownAll gracefully cancels all active worker groups.
func (o *Orchestrator) shutdownAll() {
	o.mu.Lock()
	defer o.mu.Unlock()

	for _, wg := range o.activeWorkers {
		if wg.cancel != nil {
			wg.cancel()
		}
	}
	o.activeWorkers = make(map[string]*workerGroup)
}

// max is a helper to return the maximum of two integers.
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
