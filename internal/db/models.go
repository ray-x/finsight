package db

import "time"

// QuoteLatest represents the latest quote and key stats for a symbol.
type QuoteLatest struct {
	Symbol                    string
	Price                     float64
	Change                    float64
	ChangePct                 float64
	Open                      float64
	High                      float64
	Low                       float64
	Volume                    int64
	MarketCap                 int64
	PERatio                   float64
	ForwardPE                 float64
	DividendYield             float64
	FiftyTwoWeekHigh          float64
	FiftyTwoWeekLow           float64
	FiftyTwoWeekChange        float64
	FiftyTwoWeekChangePct     float64
	TwoHundredDayAvg          float64
	TwoHundredDayAvgChange    float64
	TwoHundredDayAvgChangePct float64
	FiftyDayAvg               float64
	FiftyDayAvgChange         float64
	FiftyDayAvgChangePct      float64
	Beta                      float64
	TrailingEPS               float64
	ForwardEPS                float64
	PEGRatio                  float64
	EPSSurprise               float64
	EPSSurprisePct            float64
	TargetMeanPrice           float64
	QuoteSource               string
	QuoteUpdatedAt            time.Time
	PEChangedAt               time.Time
	ForwardPEChangedAt        time.Time
	StatsChangedAt            time.Time
	SyncedAt                  time.Time
}

// QuoteHistory represents a historical snapshot of a quote.
type QuoteHistory struct {
	ID               int64
	Symbol           string
	Price            float64
	Change           float64
	ChangePct        float64
	PERatio          float64
	ForwardPE        float64
	DividendYield    float64
	FiftyTwoWeekHigh float64
	FiftyTwoWeekLow  float64
	TrailingEPS      float64
	ForwardEPS       float64
	TargetMeanPrice  float64
	RecordedAt       time.Time
}

// PriceBar represents a single OHLCV bar.
type PriceBar struct {
	ID            int64
	Symbol        string
	Interval      string // e.g., "1m", "5m", "15m", "1d", "1wk"
	OpenTime      time.Time
	Open          float64
	High          float64
	Low           float64
	Close         float64
	Volume        int64
	ExpirySeconds int
	ExpiresAt     time.Time
	SyncedAt      time.Time
}

// IntervalDuration returns the nominal duration of a bar for the given
// interval string (e.g. "5m" → 5min, "1d" → 24h). Returns 0 for unknown
// intervals, which callers should treat as "cannot determine if closed".
func IntervalDuration(interval string) time.Duration {
	switch interval {
	case "1m":
		return time.Minute
	case "2m":
		return 2 * time.Minute
	case "5m":
		return 5 * time.Minute
	case "15m":
		return 15 * time.Minute
	case "30m":
		return 30 * time.Minute
	case "60m", "1h":
		return time.Hour
	case "90m":
		return 90 * time.Minute
	case "1d":
		return 24 * time.Hour
	case "5d":
		return 5 * 24 * time.Hour
	case "1wk":
		return 7 * 24 * time.Hour
	case "1mo":
		return 30 * 24 * time.Hour
	case "3mo":
		return 90 * 24 * time.Hour
	}
	return 0
}

// RangeDuration returns the nominal lookback duration for a Yahoo-style
// chart range string (e.g. "1d", "5d", "1mo", "1y"). Returns 0 for
// unknown ranges. Used to window reads from the bar store so that a 1D
// view doesn't pull back months of accumulated intraday bars.
func RangeDuration(chartRange string) time.Duration {
	const day = 24 * time.Hour
	switch chartRange {
	case "1d":
		return day
	case "2d":
		return 2 * day
	case "5d":
		return 5 * day
	case "1mo":
		return 31 * day
	case "3mo":
		return 93 * day
	case "6mo":
		return 186 * day
	case "ytd":
		return 366 * day
	case "1y":
		return 366 * day
	case "2y":
		return 2 * 366 * day
	case "3y":
		return 3 * 366 * day
	case "5y":
		return 5 * 366 * day
	case "10y":
		return 10 * 366 * day
	case "max":
		return 50 * 366 * day
	}
	return 0
}

// IsStale checks if a bar has expired.
//
// A bar is immutable once its interval window has fully closed
// (OpenTime + IntervalDuration is in the past). Historical bars never
// go stale — their OHLCV cannot change. Only the currently-open bar
// (whose window has not yet ended) uses the expires_at TTL fence.
func (pb *PriceBar) IsStale() bool {
	if d := IntervalDuration(pb.Interval); d > 0 {
		if time.Now().After(pb.OpenTime.Add(d)) {
			return false
		}
	}
	if pb.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(pb.ExpiresAt)
}

// EarningsRecord represents a single earnings report.
type EarningsRecord struct {
	ID               int64
	Symbol           string
	FiscalPeriod     string // e.g., "2025Q1", "FY2025"
	PeriodType       string // "quarterly" or "annual"
	AnnouncementDate time.Time
	EPSActual        float64
	EPSEstimate      float64
	RevenueActual    float64
	RevenueEstimate  float64
	GuidanceHigh     float64
	GuidanceLow      float64
	SurprisePct      float64
	RawJSON          string
	SyncedAt         time.Time
}

// Document represents a filing, transcript, or report text.
type Document struct {
	ID           int64
	Symbol       string
	DocumentType string // "10-K", "10-Q", "8-K", "transcript", etc.
	FiscalPeriod string
	Title        string
	Source       string
	URL          string
	Content      string
	ContentHash  string
	MetadataJSON string
	SyncedAt     time.Time
}

// LLMReport represents an AI-generated analysis report.
type LLMReport struct {
	ID           int64
	Symbol       string
	FiscalPeriod string
	Title        string
	QuestionHash string
	Markdown     string
	JSON         string
	CreatedAt    time.Time
	SyncedAt     time.Time
}

// LLMReportSection represents an extracted section from a report (## or ### heading).
type LLMReportSection struct {
	ID             int64
	ReportID       int64
	HeadingLevel   int // 2 for ##, 3 for ###
	HeadingTitle   string
	SectionContent string
	SectionHash    string
	SyncedAt       time.Time
}

// FTSSearchResult represents a full-text search result.
type FTSSearchResult struct {
	ID      int64
	Rank    float64 // FTS5 rank (negative, closer to 0 is better)
	Content string
	Context map[string]string // e.g., {"symbol": "AAPL", "title": "..."}
}

// EarningsEvent is a normalized, source-tagged earnings release event
// discovered from IR feeds, SEC filings, or aggregators.
type EarningsEvent struct {
	ID           int64
	Symbol       string
	SourceType   string // company_ir_release | edgar_filing | yahoo_aggregate
	SourceName   string
	Title        string
	URL          string
	ReleasedAt   time.Time
	DetectedAt   time.Time
	IngestedAt   time.Time
	Fingerprint  string
	Confidence   string // high | highest | medium
	Status       string // new | confirmed | reconciled | stale
	MetadataJSON string
	Content      string
	UpdatedAt    time.Time
}

// IRFeedState stores conditional-request metadata for one IR feed URL.
type IRFeedState struct {
	FeedURL       string
	ETag          string
	LastModified  string
	LastCheckedAt time.Time
	LastStatus    int
	LastError     string
	UpdatedAt     time.Time
}
