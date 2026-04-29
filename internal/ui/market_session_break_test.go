package ui

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ray-x/finsight/internal/chart"
	"github.com/ray-x/finsight/internal/logger"
	"github.com/ray-x/finsight/internal/yahoo"
)

// Helpers -----------------------------------------------------------------

// mkSession builds a synthetic session of 5m bars running
// [openHourET:openMinuteET, closeHourET:closeMinuteET) on the given ET
// calendar date. Prices increment by 1.0 from basePrice.
func mkSession(t *testing.T, y int, mo time.Month, d, openHr, openMin, closeHr, closeMin int, basePrice float64) *yahoo.ChartData {
	t.Helper()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("loc: %v", err)
	}
	start := time.Date(y, mo, d, openHr, openMin, 0, 0, loc)
	end := time.Date(y, mo, d, closeHr, closeMin, 0, 0, loc)
	var cd yahoo.ChartData
	p := basePrice
	for t0 := start; t0.Before(end); t0 = t0.Add(5 * time.Minute) {
		cd.Timestamps = append(cd.Timestamps, t0.Unix())
		cd.Opens = append(cd.Opens, p)
		cd.Closes = append(cd.Closes, p+0.1)
		cd.Highs = append(cd.Highs, p+0.2)
		cd.Lows = append(cd.Lows, p-0.1)
		cd.Volumes = append(cd.Volumes, 1000)
		p += 0.05
	}
	return &cd
}

// concat merges the bars of multiple ChartData in timestamp order.
func concat(parts ...*yahoo.ChartData) *yahoo.ChartData {
	var out yahoo.ChartData
	for _, p := range parts {
		if p == nil {
			continue
		}
		out.Timestamps = append(out.Timestamps, p.Timestamps...)
		out.Opens = append(out.Opens, p.Opens...)
		out.Closes = append(out.Closes, p.Closes...)
		out.Highs = append(out.Highs, p.Highs...)
		out.Lows = append(out.Lows, p.Lows...)
		out.Volumes = append(out.Volumes, p.Volumes...)
	}
	return &out
}

func countETDates(ts []int64) int {
	loc, _ := time.LoadLocation("America/New_York")
	seen := make(map[[3]int]struct{})
	for _, t := range ts {
		tt := time.Unix(t, 0).In(loc)
		y, m, d := tt.Date()
		seen[[3]int{y, int(m), d}] = struct{}{}
	}
	return len(seen)
}

// Tests -------------------------------------------------------------------

// TestTrimToLastNSessions_KeepsRequestedDates verifies the helper
// retains the requested number of distinct ET calendar dates rather
// than a fixed bar count.
func TestTrimToLastNSessions_KeepsRequestedDates(t *testing.T) {
	day1 := mkSession(t, 2026, 4, 21, 9, 30, 16, 0, 100) // Mon
	day2 := mkSession(t, 2026, 4, 22, 9, 30, 16, 0, 105) // Tue
	day3 := mkSession(t, 2026, 4, 23, 9, 30, 16, 0, 110) // Wed
	cd := concat(day1, day2, day3)

	trimmed1 := trimToLastNSessions(cd, 1)
	if got := countETDates(trimmed1.Timestamps); got != 1 {
		t.Errorf("n=1: expected 1 ET date, got %d (bars=%d)", got, len(trimmed1.Timestamps))
	}
	if len(trimmed1.Timestamps) != len(day3.Timestamps) {
		t.Errorf("n=1: expected %d bars, got %d", len(day3.Timestamps), len(trimmed1.Timestamps))
	}

	trimmed2 := trimToLastNSessions(cd, 2)
	if got := countETDates(trimmed2.Timestamps); got != 2 {
		t.Errorf("n=2: expected 2 ET dates, got %d (bars=%d)", got, len(trimmed2.Timestamps))
	}
	wantBars := len(day2.Timestamps) + len(day3.Timestamps)
	if len(trimmed2.Timestamps) != wantBars {
		t.Errorf("n=2: expected %d bars (day2+day3), got %d", wantBars, len(trimmed2.Timestamps))
	}

	trimmed3 := trimToLastNSessions(cd, 3)
	if got := countETDates(trimmed3.Timestamps); got != 3 {
		t.Errorf("n=3: expected 3 ET dates, got %d", got)
	}

	// n>=available should return the full series.
	trimmed10 := trimToLastNSessions(cd, 10)
	if len(trimmed10.Timestamps) != len(cd.Timestamps) {
		t.Errorf("n=10: expected full series, got %d/%d", len(trimmed10.Timestamps), len(cd.Timestamps))
	}
}

// TestTrimToLastNSessions_TwoSessionsProducesDetectableGap is the key
// regression test: after keeping 2 ET sessions of 5m bars with an
// overnight close between them, chart.WithLineSessionBreaks must
// detect at least one session break (the overnight market-close
// window) and insert NaN
// placeholders for it. If this fails, the detail/watchlist chart
// won't show a market-close session break even though the data
// supports one.
func TestTrimToLastNSessions_TwoSessionsProducesDetectableGap(t *testing.T) {
	// Two consecutive trading days, regular hours only.
	day1 := mkSession(t, 2026, 4, 22, 9, 30, 16, 0, 100)
	day2 := mkSession(t, 2026, 4, 23, 9, 30, 16, 0, 105)
	cd := concat(day1, day2)

	trimmed := trimToLastNSessions(cd, 2)
	if countETDates(trimmed.Timestamps) != 2 {
		t.Fatalf("expected 2 ET dates in trimmed data, got %d", countETDates(trimmed.Timestamps))
	}

	out := chart.WithLineSessionBreaks(trimmed.Timestamps, trimmed.Closes)
	if len(out) <= len(trimmed.Closes) {
		t.Fatalf("expected WithLineSessionBreaks to insert placeholders (in=%d out=%d)",
			len(trimmed.Closes), len(out))
	}

	// The inserted session break(s) should be NaN runs; there must be at least
	// one NaN representing the overnight close.
	nanCount := 0
	for _, v := range out {
		if math.IsNaN(v) {
			nanCount++
		}
	}
	if nanCount == 0 {
		t.Fatalf("expected at least one NaN placeholder, got none")
	}
}

// TestTrimToLastNSessions_OneSessionProducesNoGap documents the
// reason the old single-session trim hid the market-close period:
// with only one ET date in the series there is no overnight delta to
// detect, so WithLineSessionBreaks inserts nothing. This is the behaviour we
// intentionally changed for non-REGULAR market states.
func TestTrimToLastNSessions_OneSessionProducesNoGap(t *testing.T) {
	day1 := mkSession(t, 2026, 4, 22, 9, 30, 16, 0, 100)
	day2 := mkSession(t, 2026, 4, 23, 9, 30, 16, 0, 105)
	cd := concat(day1, day2)

	trimmed := trimToLastNSessions(cd, 1)
	if countETDates(trimmed.Timestamps) != 1 {
		t.Fatalf("expected 1 ET date, got %d", countETDates(trimmed.Timestamps))
	}
	out := chart.WithLineSessionBreaks(trimmed.Timestamps, trimmed.Closes)
	if len(out) != len(trimmed.Closes) {
		t.Errorf("expected no session-break insertion for single-session data (in=%d out=%d)",
			len(trimmed.Closes), len(out))
	}
}

// TestRenderInlineChart_GapsOnlyOn1D confirms that the timeframe
// parameter gates market-close gap insertion. For 1D the rendered
// sparkline width changes when gaps are inserted; for 1W/1M/etc.
// the chart must render contiguously from the raw bars with no
// NaN placeholders — session/overnight breaks are expected on
// longer timeframes and shouldn't be rendered as whitespace.
func TestRenderInlineChart_GapsOnlyOn1D(t *testing.T) {
	day1 := mkSession(t, 2026, 4, 22, 9, 30, 16, 0, 100)
	day2 := mkSession(t, 2026, 4, 23, 9, 30, 16, 0, 105)
	cd := concat(day1, day2)
	q := &yahoo.Quote{Symbol: "QQQ", ChangePercent: 0.5}
	item := WatchlistItem{Symbol: "QQQ", Quote: q, ChartData: cd}

	// "1D" should produce a chart whose rendered output differs
	// from a non-1D render (because NaN placeholders are inserted).
	out1D := renderInlineChart(item, 60, 2, "dotted_line", true, "1D")
	out1W := renderInlineChart(item, 60, 2, "dotted_line", true, "1W")
	if out1D == "" || out1W == "" {
		t.Fatalf("expected both renders to produce output, got 1D=%q 1W=%q", out1D, out1W)
	}
	if out1D == out1W {
		t.Errorf("expected 1D render to differ from 1W render (gaps only on 1D)")
	}

	// Sanity: a single-session data set should render identically
	// regardless of timeframe (no gap to insert anywhere).
	singleItem := WatchlistItem{Symbol: "QQQ", Quote: q, ChartData: day2}
	s1D := renderInlineChart(singleItem, 60, 2, "dotted_line", true, "1D")
	s1W := renderInlineChart(singleItem, 60, 2, "dotted_line", true, "1W")
	if s1D != s1W {
		t.Errorf("single-session render should match across timeframes; 1D=%q 1W=%q",
			s1D, s1W)
	}
}

// TestRenderInlineChart_MarketClosedTrailingGap verifies that when
// the market is not currently REGULAR, a 1D render of a single
// session appends a trailing NaN run so the closed-market period is
// visible as whitespace on the right of the chart. When the market
// is REGULAR there must be no trailing gap.
func TestRenderInlineChart_MarketClosedTrailingGap(t *testing.T) {
	day := mkSession(t, 2026, 4, 23, 9, 30, 16, 0, 100)

	// Helper: count NaN tail directly on the output of the gap
	// pipeline (we can't easily diff rendered strings for tail
	// presence, so exercise the chart helpers the renderer uses).
	qClosed := &yahoo.Quote{Symbol: "QQQ", MarketState: "POST"}
	qOpen := &yahoo.Quote{Symbol: "QQQ", MarketState: "REGULAR"}

	itemClosed := WatchlistItem{Symbol: "QQQ", Quote: qClosed, ChartData: day}
	itemOpen := WatchlistItem{Symbol: "QQQ", Quote: qOpen, ChartData: day}

	outClosed := renderInlineChart(itemClosed, 60, 2, "dotted_line", true, "1D")
	outOpen := renderInlineChart(itemOpen, 60, 2, "dotted_line", true, "1D")
	if outClosed == "" || outOpen == "" {
		t.Fatalf("expected renders to produce output")
	}
	if outClosed == outOpen {
		t.Errorf("expected closed-market render to differ from open (trailing gap missing)")
	}

	// And directly verify the chart helper pads the series.
	padded := chart.WithClosedMarketTail(day.Closes)
	if len(padded) <= len(day.Closes) {
		t.Fatalf("WithClosedMarketTail did not append placeholders: in=%d out=%d",
			len(day.Closes), len(padded))
	}
	tailNaN := 0
	for i := len(padded) - 1; i >= 0; i-- {
		if math.IsNaN(padded[i]) {
			tailNaN++
			continue
		}
		break
	}
	if tailNaN == 0 {
		t.Fatalf("expected trailing NaN tail, got none")
	}
}

func TestShapeOneDChartData_EarlyRegularUsesPrevSessionBreakCurrent(t *testing.T) {
	prev := mkSession(t, 2026, 4, 22, 9, 30, 16, 0, 100)
	// 2 hours of today's session: show yesterday tail + gap + today.
	// This is the standard multi-session layout, independent of elapsed time.
	curr := mkSession(t, 2026, 4, 23, 9, 30, 11, 30, 105)
	cd := concat(prev, curr)
	item := WatchlistItem{
		Symbol:    "QQQ",
		Quote:     &yahoo.Quote{Symbol: "QQQ", MarketState: "REGULAR"},
		ChartData: cd,
	}

	layout := computeOneDRenderLayout(item)
	if layout.mode != oneDRenderPrevSessionBreakCurrent {
		t.Fatalf("expected prev-session_break-current layout, got %v", layout.mode)
	}
	shaped := shapeOneDChartData(item)
	if len(shaped.Closes) <= len(curr.Closes) {
		t.Fatalf("expected shaped series to include prev tail + session break + current; curr=%d shaped=%d",
			len(curr.Closes), len(shaped.Closes))
	}
	nanCount := 0
	for _, v := range shaped.Closes {
		if math.IsNaN(v) {
			nanCount++
		}
	}
	if nanCount == 0 {
		t.Fatalf("expected an overnight NaN session break in early-session composite layout")
	}
	if math.IsNaN(shaped.Closes[len(shaped.Closes)-1]) {
		t.Fatalf("latest bar should be on the right edge, not inside the session-break tail")
	}
}

func TestShapeOneDChartData_LateRegularAlsoShowsSessionBreak(t *testing.T) {
	prev := mkSession(t, 2026, 4, 22, 9, 30, 16, 0, 100)
	// 5 hours of today's session: with the threshold removed, we now
	// always show prev+session_break+current when multiple sessions are detected.
	curr := mkSession(t, 2026, 4, 23, 9, 30, 14, 30, 105)
	cd := concat(prev, curr)
	item := WatchlistItem{
		Symbol:    "QQQ",
		Quote:     &yahoo.Quote{Symbol: "QQQ", MarketState: "REGULAR"},
		ChartData: cd,
	}

	layout := computeOneDRenderLayout(item)
	if layout.mode != oneDRenderPrevSessionBreakCurrent {
		t.Fatalf("expected prev-session_break-current layout (threshold removed), got %v", layout.mode)
	}
	shaped := shapeOneDChartData(item)
	if len(shaped.Closes) <= len(curr.Closes) {
		t.Fatalf("expected shaped series to include prev tail + session break + current; curr=%d shaped=%d",
			len(curr.Closes), len(shaped.Closes))
	}
	nanCount := 0
	for _, v := range shaped.Closes {
		if math.IsNaN(v) {
			nanCount++
		}
	}
	if nanCount == 0 {
		t.Fatalf("expected an overnight NaN session break in multi-session layout")
	}
	if math.IsNaN(shaped.Closes[len(shaped.Closes)-1]) {
		t.Fatalf("latest bar should be on the right edge, not inside the session-break tail")
	}
}

func TestComputeOneDRenderLayout_LogsModeDecision(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "ui-layout.log")
	if err := logger.Init(logPath); err != nil {
		t.Fatalf("logger.Init: %v", err)
	}
	t.Cleanup(logger.Close)

	t.Run("multi-session logs prev_session_break_current", func(t *testing.T) {
		prev := mkSession(t, 2026, 4, 22, 9, 30, 16, 0, 100)
		curr := mkSession(t, 2026, 4, 23, 9, 30, 11, 30, 105)
		item := WatchlistItem{
			Symbol:    "QQQ",
			Quote:     &yahoo.Quote{Symbol: "QQQ", MarketState: "REGULAR"},
			ChartData: concat(prev, curr),
		}

		_ = computeOneDRenderLayout(item)
		b, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatalf("read log: %v", err)
		}
		got := string(b)
		if !strings.Contains(got, "one_d_layout: symbol=QQQ") || !strings.Contains(got, "mode=prev_session_break_current") {
			t.Fatalf("expected prev_session_break_current layout log, got: %s", got)
		}
	})

	t.Run("single-session closed logs current_with_tail", func(t *testing.T) {
		day := mkSession(t, 2026, 4, 23, 9, 30, 16, 0, 100)
		item := WatchlistItem{
			Symbol:    "QQQ",
			Quote:     &yahoo.Quote{Symbol: "QQQ", MarketState: "POST"},
			ChartData: day,
		}

		_ = computeOneDRenderLayout(item)
		b, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatalf("read log: %v", err)
		}
		got := string(b)
		if !strings.Contains(got, "one_d_layout: symbol=QQQ") || !strings.Contains(got, "mode=current_with_tail") {
			t.Fatalf("expected current_with_tail layout log, got: %s", got)
		}
	})
}

func TestComputeOneDRenderLayout_SessionBreakPadVariesByBreakDuration(t *testing.T) {
	prev := mkSession(t, 2026, 4, 22, 9, 30, 16, 0, 100)
	shortBreakCurr := mkSession(t, 2026, 4, 23, 4, 0, 10, 0, 105)
	longBreakCurr := mkSession(t, 2026, 4, 23, 10, 0, 16, 0, 105)

	shortLayout := computeOneDRenderLayout(WatchlistItem{
		Symbol:    "QQQ",
		Quote:     &yahoo.Quote{Symbol: "QQQ", MarketState: "PRE"},
		ChartData: concat(prev, shortBreakCurr),
	})
	longLayout := computeOneDRenderLayout(WatchlistItem{
		Symbol:    "IKO.AX",
		Quote:     &yahoo.Quote{Symbol: "IKO.AX", MarketState: "PRE"},
		ChartData: concat(prev, longBreakCurr),
	})

	if shortLayout.mode != oneDRenderPrevSessionBreakCurrent {
		t.Fatalf("expected short-break layout to be prev_session_break_current, got %v", shortLayout.mode)
	}
	if longLayout.mode != oneDRenderPrevSessionBreakCurrent {
		t.Fatalf("expected long-break layout to be prev_session_break_current, got %v", longLayout.mode)
	}
	if longLayout.sessionBreakPad <= shortLayout.sessionBreakPad {
		t.Fatalf("expected longer closed period to produce larger sessionBreakPad, short=%d long=%d",
			shortLayout.sessionBreakPad, longLayout.sessionBreakPad)
	}
	if longLayout.prevKeep >= shortLayout.prevKeep {
		t.Fatalf("expected longer closed period to shift session break left via smaller prevKeep, short=%d long=%d",
			shortLayout.prevKeep, longLayout.prevKeep)
	}
	if shortLayout.prevKeep < 1 || longLayout.prevKeep < 1 {
		t.Fatalf("expected prevKeep to stay positive, short=%d long=%d",
			shortLayout.prevKeep, longLayout.prevKeep)
	}

	shortShaped := shapeOneDChartData(WatchlistItem{
		Symbol:    "QQQ",
		Quote:     &yahoo.Quote{Symbol: "QQQ", MarketState: "PRE"},
		ChartData: concat(prev, shortBreakCurr),
	})
	longShaped := shapeOneDChartData(WatchlistItem{
		Symbol:    "IKO.AX",
		Quote:     &yahoo.Quote{Symbol: "IKO.AX", MarketState: "PRE"},
		ChartData: concat(prev, longBreakCurr),
	})
	shortBreakStart := -1
	for i, v := range shortShaped.Closes {
		if math.IsNaN(v) {
			shortBreakStart = i
			break
		}
	}
	longBreakStart := -1
	for i, v := range longShaped.Closes {
		if math.IsNaN(v) {
			longBreakStart = i
			break
		}
	}
	if shortBreakStart < 0 || longBreakStart < 0 {
		t.Fatalf("expected shaped output to include session break placeholders, short=%d long=%d",
			shortBreakStart, longBreakStart)
	}
	if longBreakStart >= shortBreakStart {
		t.Fatalf("expected longer break to start earlier in shaped output, short=%d long=%d",
			shortBreakStart, longBreakStart)
	}
}

func TestComputeOneDRenderLayout_EmptyMarketStateUsesRecency(t *testing.T) {
	now := time.Now().Unix()
	iv := int64(5 * 60)

	staleDay1 := &yahoo.ChartData{}
	staleDay2 := &yahoo.ChartData{}
	for i := 0; i < 20; i++ {
		staleDay1.Timestamps = append(staleDay1.Timestamps, now-36*3600+int64(i)*iv)
		staleDay1.Closes = append(staleDay1.Closes, 100+float64(i))
		staleDay1.Opens = append(staleDay1.Opens, 100+float64(i))
		staleDay1.Highs = append(staleDay1.Highs, 101+float64(i))
		staleDay1.Lows = append(staleDay1.Lows, 99+float64(i))
		staleDay1.Volumes = append(staleDay1.Volumes, 1000)

		staleDay2.Timestamps = append(staleDay2.Timestamps, now-18*3600+int64(i)*iv)
		staleDay2.Closes = append(staleDay2.Closes, 120+float64(i))
		staleDay2.Opens = append(staleDay2.Opens, 120+float64(i))
		staleDay2.Highs = append(staleDay2.Highs, 121+float64(i))
		staleDay2.Lows = append(staleDay2.Lows, 119+float64(i))
		staleDay2.Volumes = append(staleDay2.Volumes, 1000)
	}
	stale := concat(staleDay1, staleDay2)
	staleLayout := computeOneDRenderLayout(WatchlistItem{Symbol: "QQQ", Quote: &yahoo.Quote{Symbol: "QQQ", MarketState: ""}, ChartData: stale})
	if staleLayout.mode != oneDRenderCurrentWithTail {
		t.Fatalf("expected stale empty-state series to route to trailing tail, got %v", staleLayout.mode)
	}

	freshDay1 := &yahoo.ChartData{}
	freshDay2 := &yahoo.ChartData{}
	for i := 0; i < 20; i++ {
		freshDay1.Timestamps = append(freshDay1.Timestamps, now-26*3600+int64(i)*iv)
		freshDay1.Closes = append(freshDay1.Closes, 100+float64(i))
		freshDay1.Opens = append(freshDay1.Opens, 100+float64(i))
		freshDay1.Highs = append(freshDay1.Highs, 101+float64(i))
		freshDay1.Lows = append(freshDay1.Lows, 99+float64(i))
		freshDay1.Volumes = append(freshDay1.Volumes, 1000)
	}
	for i := 0; i < 3; i++ {
		freshDay2.Timestamps = append(freshDay2.Timestamps, now-int64((2-i))*iv)
		freshDay2.Closes = append(freshDay2.Closes, 130+float64(i))
		freshDay2.Opens = append(freshDay2.Opens, 130+float64(i))
		freshDay2.Highs = append(freshDay2.Highs, 131+float64(i))
		freshDay2.Lows = append(freshDay2.Lows, 129+float64(i))
		freshDay2.Volumes = append(freshDay2.Volumes, 1000)
	}
	fresh := concat(freshDay1, freshDay2)
	freshLayout := computeOneDRenderLayout(WatchlistItem{Symbol: "IKO.AX", Quote: &yahoo.Quote{Symbol: "IKO.AX", MarketState: ""}, ChartData: fresh})
	if freshLayout.mode != oneDRenderPrevSessionBreakCurrent {
		t.Fatalf("expected fresh empty-state series to route to prev_session_break_current, got %v", freshLayout.mode)
	}
}

func TestComputeOneDRenderLayout_EmptyStateUsesQuoteTimestamp(t *testing.T) {
	now := time.Now().Unix()
	prev := mkSession(t, 2026, 4, 22, 9, 30, 16, 0, 100)
	curr := mkSession(t, 2026, 4, 23, 9, 30, 16, 0, 105)
	cd := concat(prev, curr)

	activeByQuote := computeOneDRenderLayout(WatchlistItem{
		Symbol: "IKO.AX",
		Quote: &yahoo.Quote{
			Symbol:            "IKO.AX",
			MarketState:       "",
			RegularMarketTime: now - 2*60,
		},
		ChartData: cd,
	})
	if activeByQuote.mode != oneDRenderPrevSessionBreakCurrent {
		t.Fatalf("expected recent quote timestamp to route empty-state symbol as active, got %v", activeByQuote.mode)
	}

	staleByQuote := computeOneDRenderLayout(WatchlistItem{
		Symbol: "QQQ",
		Quote: &yahoo.Quote{
			Symbol:            "QQQ",
			MarketState:       "",
			RegularMarketTime: now - 3*60*60,
		},
		ChartData: cd,
	})
	if staleByQuote.mode != oneDRenderCurrentWithTail {
		t.Fatalf("expected stale quote timestamp to route empty-state symbol as closed tail, got %v", staleByQuote.mode)
	}
}

func TestComputeOneDRenderLayout_EmptyStateUsesExchangeClockFallback(t *testing.T) {
	origNow := nowFunc
	nowFunc = func() time.Time {
		loc, _ := time.LoadLocation("Australia/Sydney")
		return time.Date(2026, 4, 24, 10, 5, 0, 0, loc)
	}
	t.Cleanup(func() { nowFunc = origNow })

	now := nowFunc().Unix()
	prev := &yahoo.ChartData{}
	curr := &yahoo.ChartData{}
	iv := int64(5 * 60)
	for i := 0; i < 20; i++ {
		prev.Timestamps = append(prev.Timestamps, now-40*3600+int64(i)*iv)
		prev.Closes = append(prev.Closes, 100+float64(i))
		prev.Opens = append(prev.Opens, 100+float64(i))
		prev.Highs = append(prev.Highs, 101+float64(i))
		prev.Lows = append(prev.Lows, 99+float64(i))
		prev.Volumes = append(prev.Volumes, 1000)

		curr.Timestamps = append(curr.Timestamps, now-20*3600+int64(i)*iv)
		curr.Closes = append(curr.Closes, 120+float64(i))
		curr.Opens = append(curr.Opens, 120+float64(i))
		curr.Highs = append(curr.Highs, 121+float64(i))
		curr.Lows = append(curr.Lows, 119+float64(i))
		curr.Volumes = append(curr.Volumes, 1000)
	}
	cd := concat(prev, curr)

	iko := computeOneDRenderLayout(WatchlistItem{
		Symbol:    "IKO.AX",
		Quote:     &yahoo.Quote{Symbol: "IKO.AX", MarketState: "", RegularMarketTime: 0},
		ChartData: cd,
	})
	if iko.mode != oneDRenderCurrentWithTail {
		t.Fatalf("expected stale full-session IKO.AX data to route as closed tail, got %v", iko.mode)
	}

	qqq := computeOneDRenderLayout(WatchlistItem{
		Symbol:    "QQQ",
		Quote:     &yahoo.Quote{Symbol: "QQQ", MarketState: "", RegularMarketTime: 0},
		ChartData: cd,
	})
	if qqq.mode != oneDRenderCurrentWithTail {
		t.Fatalf("expected QQQ to route as closed by US clock fallback, got %v", qqq.mode)
	}

	// With only a few bars in the current session, Sydney clock fallback
	// should still route IKO.AX as active and show prev+session_break+current.
	freshCurr := &yahoo.ChartData{}
	for i := 0; i < 3; i++ {
		freshCurr.Timestamps = append(freshCurr.Timestamps, now-int64((2-i))*iv)
		freshCurr.Closes = append(freshCurr.Closes, 140+float64(i))
		freshCurr.Opens = append(freshCurr.Opens, 140+float64(i))
		freshCurr.Highs = append(freshCurr.Highs, 141+float64(i))
		freshCurr.Lows = append(freshCurr.Lows, 139+float64(i))
		freshCurr.Volumes = append(freshCurr.Volumes, 1000)
	}
	ikoFresh := computeOneDRenderLayout(WatchlistItem{
		Symbol:    "IKO.AX",
		Quote:     &yahoo.Quote{Symbol: "IKO.AX", MarketState: "", RegularMarketTime: 0},
		ChartData: concat(prev, freshCurr),
	})
	if ikoFresh.mode != oneDRenderPrevSessionBreakCurrent {
		t.Fatalf("expected fresh low-progress IKO.AX data to route as active by Sydney clock fallback, got %v", ikoFresh.mode)
	}
}

func TestSanitizeOneDOutlierWicks_ClampsIsolatedSpike(t *testing.T) {
	cd := &yahoo.ChartData{
		Timestamps: []int64{1, 2, 3, 4, 5},
		Opens:      []float64{651, 652, 652.3, 652.4, 652.5},
		Closes:     []float64{651.2, 652.1, 652.8, 652.6, 652.7},
		Highs:      []float64{651.4, 652.4, 681.274, 652.9, 653.0},
		Lows:       []float64{650.8, 651.8, 652.2, 652.2, 652.3},
		Volumes:    []int64{100, 110, 120, 130, 140},
	}
	out := sanitizeOneDOutlierWicks("QQQ", cd)
	if out.Highs[2] >= 680 {
		t.Fatalf("expected isolated spike to be clamped, got high=%f", out.Highs[2])
	}
	if out.Highs[2] != math.Max(out.Opens[2], out.Closes[2]) {
		t.Fatalf("expected clamped high to equal candle body high, got %f", out.Highs[2])
	}
	if out.Highs[1] != cd.Highs[1] || out.Highs[3] != cd.Highs[3] {
		t.Fatalf("expected non-outlier bars unchanged")
	}
}
