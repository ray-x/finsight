package cache

import (
	"context"
	"path/filepath"
	"time"

	"github.com/ray-x/finsight/internal/db"
	"github.com/ray-x/finsight/internal/logger"
	"github.com/ray-x/finsight/internal/news"
	"github.com/ray-x/finsight/internal/yahoo"
)

// SQLiteCache wraps the SQLite-backed cache used by the live app.
// JSON artifacts remain available only as an external reference store
// for data types that have not yet been migrated to SQLite.
type SQLiteCache struct {
	sqliteDB  *db.DB
	jsonCache *Cache // Reference store for non-SQLite-backed data types
}

// NewSQLiteCache creates a new SQLite-backed cache.
func NewSQLiteCache(dir string) (*SQLiteCache, error) {
	// Initialize SQLite database
	dbPath := filepath.Join(dir, "finsight.db")
	sqliteDB, err := db.Open(dbPath)
	if err != nil {
		return nil, err
	}

	// Keep JSON cache only for data types that are still stored as JSON.
	jsonCache, err := New(dir)
	if err != nil {
		sqliteDB.Close()
		return nil, err
	}

	return &SQLiteCache{
		sqliteDB:  sqliteDB,
		jsonCache: jsonCache,
	}, nil
}

// Close closes the database connection. It first runs a TRUNCATE WAL
// checkpoint so all writes land in the main DB file — otherwise tools
// that inspect `finsight.db` without reading the `-wal` sidecar can
// appear to see stale data even though writes succeeded.
func (sc *SQLiteCache) Close() error {
	if sc == nil || sc.sqliteDB == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := sc.sqliteDB.Exec(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		logger.Log("cache: WAL checkpoint on close failed: %v", err)
	}
	return sc.sqliteDB.Close()
}

// KnowledgeDB returns the underlying SQLite database handle used for
// persistent retrieval/report storage.
func (sc *SQLiteCache) KnowledgeDB() *db.DB {
	return sc.sqliteDB
}

// ── Quotes ──

// GetQuotes returns cached quotes if fresh from SQLite.
func (sc *SQLiteCache) GetQuotes(symbols []string) []yahoo.Quote {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result := make([]yahoo.Quote, 0, len(symbols))
	for _, sym := range symbols {
		quote, err := sc.sqliteDB.GetQuoteLatest(ctx, sym)
		if err != nil || quote == nil || time.Since(quote.SyncedAt) > cacheTTL {
			return nil
		}
		result = append(result, convertDBQuoteToYahoo(quote))
	}
	if len(result) == len(symbols) {
		return result
	}
	return nil
}

// GetQuotesStale returns cached quotes regardless of age from SQLite.
func (sc *SQLiteCache) GetQuotesStale(symbols []string) []yahoo.Quote {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result := make([]yahoo.Quote, 0, len(symbols))
	for _, sym := range symbols {
		quote, err := sc.sqliteDB.GetQuoteLatest(ctx, sym)
		if err != nil || quote == nil {
			return nil
		}
		result = append(result, convertDBQuoteToYahoo(quote))
	}
	if len(result) == len(symbols) {
		return result
	}
	return nil
}

// PutQuotes caches quotes to SQLite.
func (sc *SQLiteCache) PutQuotes(quotes []yahoo.Quote) {
	if sc == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Store in SQLite. Errors are logged but don't block the JSON
	// write, which is the read-path fallback.
	for _, q := range quotes {
		dbQuote := convertYahooQuoteToDBQuote(&q)
		if err := sc.sqliteDB.UpsertQuoteLatest(ctx, dbQuote); err != nil {
			logger.Log("cache: sqlite UpsertQuoteLatest %s failed: %v", q.Symbol, err)
		}
	}
}

// ── Charts ──

// GetChart returns cached chart data if fresh from SQLite.
func (sc *SQLiteCache) GetChart(symbol, chartRange, interval string) *yahoo.ChartData {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	bars, err := sc.readBarsForRange(ctx, symbol, chartRange, interval)
	if err != nil || len(bars) == 0 {
		logger.Log("cache.GetChart: %s %s/%s sqlite MISS (err=%v len=%d)",
			symbol, chartRange, interval, err, len(bars))
		return nil
	}
	// Check if ANY bar is fresh
	freshCount := 0
	for _, bar := range bars {
		if !bar.IsStale() {
			freshCount++
		}
	}
	if freshCount > 0 {
		logger.Log("cache.GetChart: %s %s/%s sqlite HIT bars=%d fresh=%d",
			symbol, chartRange, interval, len(bars), freshCount)
		return convertDBBarsToChartData(bars, interval)
	}
	logger.Log("cache.GetChart: %s %s/%s sqlite STALE bars=%d",
		symbol, chartRange, interval, len(bars))
	return nil
}

// readBarsForRange loads the bars relevant to (chartRange, interval).
// When the range maps to a known lookback window it queries by time so
// we don't pull back every accumulated intraday bar for a 1D view.
// Falls back to a flat 500-row LIMIT for unknown ranges.
func (sc *SQLiteCache) readBarsForRange(ctx context.Context, symbol, chartRange, interval string) ([]*db.PriceBar, error) {
	if d := db.RangeDuration(chartRange); d > 0 {
		// Small buffer so a range boundary doesn't drop the edge bar.
		start := time.Now().Add(-(d + 6*time.Hour))
		end := time.Now().Add(24 * time.Hour)
		return sc.sqliteDB.GetPriceBarsInRange(ctx, symbol, interval, start, end)
	}
	return sc.sqliteDB.GetPriceBars(ctx, symbol, interval, 500)
}

// GetChartStale returns cached chart data regardless of age from SQLite.
func (sc *SQLiteCache) GetChartStale(symbol, chartRange, interval string) *yahoo.ChartData {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	bars, err := sc.readBarsForRange(ctx, symbol, chartRange, interval)
	if err == nil && len(bars) > 0 {
		return convertDBBarsToChartData(bars, interval)
	}
	return nil
}

// PutChart caches chart data to SQLite.
func (sc *SQLiteCache) PutChart(symbol, chartRange, interval string, data *yahoo.ChartData) {
	if data == nil || len(data.Closes) == 0 {
		return
	}

	// Generous timeout: large indicator-context fetches can produce
	// 2000+ bars and the old 5s ceiling silently truncated writes.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Determine TTL based on interval
	var ttlSeconds int
	switch interval {
	case "1m", "5m", "15m", "30m", "60m":
		ttlSeconds = db.IntradayBarTTL // 15 minutes
	case "1d":
		ttlSeconds = db.DailyBarTTL // Never expire (0)
	case "1wk":
		ttlSeconds = db.WeeklyBarTTL // Never expire (0)
	case "1mo":
		ttlSeconds = db.MonthlyBarTTL // Never expire (0)
	default:
		ttlSeconds = 0
	}

	now := time.Now()
	bars := make([]*db.PriceBar, 0, len(data.Timestamps))
	for i, timestamp := range data.Timestamps {
		if i >= len(data.Opens) || i >= len(data.Closes) {
			break
		}
		var vol int64
		if i < len(data.Volumes) {
			vol = data.Volumes[i]
		}
		var hi, lo float64
		if i < len(data.Highs) {
			hi = data.Highs[i]
		}
		if i < len(data.Lows) {
			lo = data.Lows[i]
		}
		bars = append(bars, &db.PriceBar{
			Symbol:        symbol,
			Interval:      interval,
			OpenTime:      time.Unix(timestamp, 0),
			Open:          data.Opens[i],
			High:          hi,
			Low:           lo,
			Close:         data.Closes[i],
			Volume:        vol,
			ExpirySeconds: ttlSeconds,
			SyncedAt:      now,
		})
	}

	// Batch upsert in a single transaction. Atomic and orders of
	// magnitude faster than the previous per-bar loop — critical for
	// large fetches that used to silently truncate on the 5s deadline.
	if err := sc.sqliteDB.UpsertPriceBars(ctx, bars); err != nil {
		logger.Log("cache: sqlite UpsertPriceBars %s %s/%s (%d bars) failed: %v",
			symbol, chartRange, interval, len(bars), err)
	} else {
		logger.Log("cache.PutChart: sqlite wrote %s %s/%s bars=%d ttl=%ds",
			symbol, chartRange, interval, len(bars), ttlSeconds)
	}
}

// ── Financials ──

// GetFinancials returns cached financials if fresh from JSON (future: SQLite).
func (sc *SQLiteCache) GetFinancials(symbol string) *yahoo.FinancialData {
	// For now, use JSON cache; will migrate to SQLite in next phase
	return sc.jsonCache.GetFinancials(symbol)
}

// GetFinancialsStale returns cached financials regardless of age.
func (sc *SQLiteCache) GetFinancialsStale(symbol string) *yahoo.FinancialData {
	return sc.jsonCache.GetFinancialsStale(symbol)
}

// PutFinancials caches financial data to JSON.
func (sc *SQLiteCache) PutFinancials(symbol string, data *yahoo.FinancialData) {
	sc.jsonCache.PutFinancials(symbol, data)
}

// ── Holders ──

// GetHolders returns cached holders if fresh from JSON (future: SQLite).
func (sc *SQLiteCache) GetHolders(symbol string) *yahoo.HolderData {
	return sc.jsonCache.GetHolders(symbol)
}

// GetHoldersStale returns cached holders regardless of age.
func (sc *SQLiteCache) GetHoldersStale(symbol string) *yahoo.HolderData {
	return sc.jsonCache.GetHoldersStale(symbol)
}

// PutHolders caches holder data to JSON.
func (sc *SQLiteCache) PutHolders(symbol string, data *yahoo.HolderData) {
	sc.jsonCache.PutHolders(symbol, data)
}

// ── News ──

// GetNews returns cached news if fresh from JSON (future: SQLite).
func (sc *SQLiteCache) GetNews(symbol string) []news.Item {
	return sc.jsonCache.GetNews(symbol)
}

// GetNewsStale returns cached news regardless of age.
func (sc *SQLiteCache) GetNewsStale(symbol string) []news.Item {
	return sc.jsonCache.GetNewsStale(symbol)
}

// PutNews caches news items to JSON.
func (sc *SQLiteCache) PutNews(symbol string, items []news.Item) {
	sc.jsonCache.PutNews(symbol, items)
}

// ── Text (LLM, earnings notes, etc.) ──

// GetText returns cached text if fresh from JSON (future: SQLite).
func (sc *SQLiteCache) GetText(symbol, kind string) string {
	return sc.jsonCache.GetText(symbol, kind)
}

// GetTextFresh returns cached text if newer than maxAge.
func (sc *SQLiteCache) GetTextFresh(symbol, kind string, maxAge time.Duration) string {
	return sc.jsonCache.GetTextFresh(symbol, kind, maxAge)
}

// PutText caches text to JSON (future: SQLite).
func (sc *SQLiteCache) PutText(symbol, kind, text string) {
	sc.jsonCache.PutText(symbol, kind, text)
}

// DeleteText removes cached text.
func (sc *SQLiteCache) DeleteText(symbol, kind string) {
	sc.jsonCache.DeleteText(symbol, kind)
}

// ── Invalidation ──

// Invalidate removes all cached data for a symbol.
func (sc *SQLiteCache) Invalidate(symbol string) {
	if sc == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if sc.sqliteDB != nil {
		if _, err := sc.sqliteDB.Exec(ctx, "DELETE FROM price_bars WHERE symbol = ?", symbol); err != nil {
			logger.Log("cache: sqlite invalidate price_bars %s failed: %v", symbol, err)
		}
		if _, err := sc.sqliteDB.Exec(ctx, "DELETE FROM quotes_latest WHERE symbol = ?", symbol); err != nil {
			logger.Log("cache: sqlite invalidate quotes_latest %s failed: %v", symbol, err)
		}
		if _, err := sc.sqliteDB.Exec(ctx, "DELETE FROM quotes_history WHERE symbol = ?", symbol); err != nil {
			logger.Log("cache: sqlite invalidate quotes_history %s failed: %v", symbol, err)
		}
	}
	sc.jsonCache.Invalidate(symbol)
}

// ── Conversion helpers ──

// ── Conversion helpers ──

func convertYahooQuoteToDBQuote(q *yahoo.Quote) *db.QuoteLatest {
	return &db.QuoteLatest{
		Symbol:           q.Symbol,
		Price:            q.Price,
		Change:           q.Change,
		ChangePct:        q.ChangePercent,
		Open:             q.Open,
		High:             q.DayHigh,
		Low:              q.DayLow,
		Volume:           q.Volume,
		MarketCap:        q.MarketCap,
		PERatio:          q.PE,
		ForwardPE:        q.ForwardPE,
		DividendYield:    q.DividendYield,
		FiftyTwoWeekHigh: q.FiftyTwoWeekHigh,
		FiftyTwoWeekLow:  q.FiftyTwoWeekLow,
		Beta:             q.Beta,
		TrailingEPS:      q.EPS,
		ForwardEPS:       0, // Not in Quote struct
		PEGRatio:         q.PEG,
		TargetMeanPrice:  0, // Not available in Quote struct
		QuoteSource:      "yahoo",
		QuoteUpdatedAt:   time.Now(),
		SyncedAt:         time.Now(),
	}
}

func convertDBQuoteToYahoo(dbQuote *db.QuoteLatest) yahoo.Quote {
	return yahoo.Quote{
		Symbol:           dbQuote.Symbol,
		Price:            dbQuote.Price,
		Change:           dbQuote.Change,
		ChangePercent:    dbQuote.ChangePct,
		Open:             dbQuote.Open,
		DayHigh:          dbQuote.High,
		DayLow:           dbQuote.Low,
		Volume:           dbQuote.Volume,
		MarketCap:        dbQuote.MarketCap,
		PE:               dbQuote.PERatio,
		ForwardPE:        dbQuote.ForwardPE,
		DividendYield:    dbQuote.DividendYield,
		FiftyTwoWeekHigh: dbQuote.FiftyTwoWeekHigh,
		FiftyTwoWeekLow:  dbQuote.FiftyTwoWeekLow,
		Beta:             dbQuote.Beta,
		EPS:              dbQuote.TrailingEPS,
		PEG:              dbQuote.PEGRatio,
	}
}

func convertDBBarsToChartData(bars []*db.PriceBar, interval string) *yahoo.ChartData {
	if len(bars) == 0 {
		return nil
	}

	// Initialize arrays
	timestamps := make([]int64, len(bars))
	opens := make([]float64, len(bars))
	highs := make([]float64, len(bars))
	lows := make([]float64, len(bars))
	closes := make([]float64, len(bars))
	volumes := make([]int64, len(bars))

	// Fill arrays (bars are in descending order, so reverse)
	for i, bar := range bars {
		idx := len(bars) - 1 - i
		timestamps[idx] = bar.OpenTime.Unix()
		opens[idx] = bar.Open
		highs[idx] = bar.High
		lows[idx] = bar.Low
		closes[idx] = bar.Close
		volumes[idx] = bar.Volume
	}

	return &yahoo.ChartData{
		Timestamps: timestamps,
		Opens:      opens,
		Highs:      highs,
		Lows:       lows,
		Closes:     closes,
		Volumes:    volumes,
	}
}
