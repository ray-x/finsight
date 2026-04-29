package chart

import (
	"math"
	"sort"

	"github.com/ray-x/finsight/internal/logger"
)

// Session breaks
//
// When an intraday series spans more than one trading session, we
// want the rendered chart to show a visible session break where the
// market was
// closed instead of squishing all bars together into a misleading
// continuous line. The helpers in this file insert NaN placeholders
// into a series at every detected session break; the renderers skip any
// column whose value is NaN, producing visible whitespace.
//
// Session-break sizing rationale: if we want the combined separator
// columns to take roughly `sessionBreakFractionTarget` of the output
// width, then with `G` breaks and `N` real bars the per-break
// placeholder count is approximately
//
//	perGap ≈ N * sessionBreakFractionTarget / ((1 - sessionBreakFractionTarget) * G)
//
// We clamp to at least 1 so the smallest watchlist chart still shows
// a visible break.
const sessionBreakFractionTarget = 0.125

// isGapCandle reports whether a Candle is a session-break sentinel
// (Open=NaN).
// Used by renderers to skip the column.
func isGapCandle(c Candle) bool {
	return math.IsNaN(c.Open)
}

// gapCandle returns a session-break sentinel Candle that renderers will
// skip.
func gapCandle() Candle {
	n := math.NaN()
	return Candle{Open: n, Close: n, High: n, Low: n}
}

// DetectSessionBreaks returns the indices in `timestamps` at which a
// session break begins (exported wrapper around the internal break
// detector used by the renderers). Callers can use this to split a
// multi-day intraday series into per-session slices in a timezone-
// agnostic way — the break points are the same ones the break
// renderers will render as whitespace.
func DetectSessionBreaks(timestamps []int64) []int {
	if len(timestamps) < 2 {
		return nil
	}
	iv := inferIntervalSec(timestamps)
	if iv <= 0 {
		return nil
	}
	return detectGaps(timestamps, iv, 2.5)
}

// detectGaps returns the indices in `timestamps` at which a session
// break
// begins (i.e. the delta timestamps[i] - timestamps[i-1] is much
// larger than the typical bar interval). intervalSec is the inferred
// median interval. A break is flagged when the delta exceeds BOTH
// `gapMultiplier * intervalSec` AND an absolute minimum threshold
// that scales with the bar interval. The absolute minimum prevents
// intraday data holes (e.g. a single missing 5m bar in a low-volume
// period) from being rendered as session breaks, while still
// catching genuine overnight/weekend breaks.
func detectGaps(timestamps []int64, intervalSec int64, gapMultiplier float64) []int {
	if len(timestamps) < 2 || intervalSec <= 0 {
		return nil
	}
	relative := int64(float64(intervalSec) * gapMultiplier)
	if relative < intervalSec*2 {
		relative = intervalSec * 2
	}
	// Absolute floor: delta must represent a meaningful non-trading
	// period, not just a bar or two of missing data. Scales with the
	// interval so daily bars require a 3-day break (weekends pass
	// through unflagged) while intraday bars require >= 1 hour.
	var absFloor int64
	switch {
	case intervalSec < 15*60: // sub-15m intraday (1m, 5m)
		absFloor = 60 * 60 // 1 hour
	case intervalSec < 60*60: // 15m .. 30m
		absFloor = 2 * 60 * 60 // 2 hours
	case intervalSec < 24*60*60: // hourly
		absFloor = 4 * 60 * 60 // 4 hours
	default: // daily or coarser
		absFloor = 3 * 24 * 60 * 60 // 3 days — lets weekends pass
	}
	threshold := relative
	if absFloor > threshold {
		threshold = absFloor
	}
	var gaps []int
	for i := 1; i < len(timestamps); i++ {
		if timestamps[i]-timestamps[i-1] > threshold {
			gaps = append(gaps, i)
		}
	}
	return gaps
}

// inferIntervalSec returns the median delta between adjacent
// timestamps, which is a robust estimate of the bar interval even
// when the series contains overnight gaps.
func inferIntervalSec(timestamps []int64) int64 {
	if len(timestamps) < 2 {
		return 0
	}
	deltas := make([]int64, 0, len(timestamps)-1)
	for i := 1; i < len(timestamps); i++ {
		d := timestamps[i] - timestamps[i-1]
		if d > 0 {
			deltas = append(deltas, d)
		}
	}
	if len(deltas) == 0 {
		return 0
	}
	sort.Slice(deltas, func(i, j int) bool { return deltas[i] < deltas[j] })
	return deltas[len(deltas)/2]
}

// perSessionBreakPlaceholders returns how many NaN placeholders to insert at
// each detected break so that the total separator space approximates
// `sessionBreakFractionTarget` of the output. `numBars` is the number
// of real bars, `numGaps` the number of detected breaks. Always at least 1
// so even tiny charts show a visible separator.
func perSessionBreakPlaceholders(numBars, numGaps int) int {
	if numGaps <= 0 || numBars <= 0 {
		return 0
	}
	frac := sessionBreakFractionTarget
	// Solve: P*numGaps / (numBars + P*numGaps) = frac
	// P = numBars * frac / ((1-frac) * numGaps)
	p := int(math.Round(float64(numBars) * frac / ((1 - frac) * float64(numGaps))))
	if p < 1 {
		p = 1
	}
	return p
}

// WithLineSessionBreaks returns a new series that preserves the ordering of
// the input but with NaN placeholders inserted at every detected
// session break. Renderers treat NaN columns as empty whitespace.
// Timestamps are used only to detect breaks and are not returned —
// callers render data by index, so the renderer doesn't need them.
//
// When timestamps and values lengths differ (common with Yahoo
// responses where Opens/Closes can lag Timestamps by one element)
// the helper aligns to the shorter of the two and still inserts
// separators based on the shared prefix.
func WithLineSessionBreaks(timestamps []int64, values []float64) []float64 {
	return withLineSessionBreaksTagged("", timestamps, values)
}

// WithLineSessionBreaksTagged is the logging-aware variant; tag is typically
// the symbol so log lines can be correlated per ticker.
func WithLineSessionBreaksTagged(tag string, timestamps []int64, values []float64) []float64 {
	return withLineSessionBreaksTagged(tag, timestamps, values)
}

func withLineSessionBreaksTagged(tag string, timestamps []int64, values []float64) []float64 {
	n := len(values)
	if len(timestamps) < n {
		n = len(timestamps)
	}
	if n == 0 {
		logger.Log("session_break[%s] line: skip (empty)", tag)
		return values
	}
	ts := timestamps[:n]
	intervalSec := inferIntervalSec(ts)
	if intervalSec <= 0 {
		logger.Log("session_break[%s] line: skip (no interval) n=%d firstTs=%d lastTs=%d", tag, n, ts[0], ts[n-1])
		return values
	}
	gaps := detectGaps(ts, intervalSec, 2.5)
	if len(gaps) == 0 {
		logger.Log("session_break[%s] line: none n=%d interval=%ds span=%ds", tag, n, intervalSec, ts[n-1]-ts[0])
		return values
	}
	perGap := perSessionBreakPlaceholders(n, len(gaps))
	if perGap <= 0 {
		return values
	}
	firstBreak := gaps[0]
	gapDelta := int64(0)
	if firstBreak > 0 && firstBreak < n {
		gapDelta = ts[firstBreak] - ts[firstBreak-1]
	}
	logger.Log("session_break[%s] line: insert n=%d interval=%ds breaks=%d perBreak=%d firstAt=%d firstDelta=%ds",
		tag, n, intervalSec, len(gaps), perGap, firstBreak, gapDelta)
	out := make([]float64, 0, len(values)+perGap*len(gaps))
	nan := math.NaN()
	gi := 0
	for i := 0; i < len(values); i++ {
		if gi < len(gaps) && gaps[gi] == i {
			for k := 0; k < perGap; k++ {
				out = append(out, nan)
			}
			gi++
		}
		out = append(out, values[i])
	}
	return out
}

// WithCandleSessionBreaks is the candlestick analogue of
// WithLineSessionBreaks. It
// inserts `gapCandle()` sentinels at each detected session break so
// the renderer skips those columns.
//
// Aligns to min(len(timestamps), len(candles)) to tolerate the
// length skew that occurs when buildCandles trims to the shorter of
// len(Opens)/len(Closes) while Timestamps may be one element longer.
func WithCandleSessionBreaks(timestamps []int64, candles []Candle) []Candle {
	return withCandleSessionBreaksTagged("", timestamps, candles)
}

// WithCandleSessionBreaksTagged is the logging-aware variant.
func WithCandleSessionBreaksTagged(tag string, timestamps []int64, candles []Candle) []Candle {
	return withCandleSessionBreaksTagged(tag, timestamps, candles)
}

func withCandleSessionBreaksTagged(tag string, timestamps []int64, candles []Candle) []Candle {
	n := len(candles)
	if len(timestamps) < n {
		n = len(timestamps)
	}
	if n == 0 {
		logger.Log("session_break[%s] cndl: skip (empty)", tag)
		return candles
	}
	ts := timestamps[:n]
	intervalSec := inferIntervalSec(ts)
	if intervalSec <= 0 {
		logger.Log("session_break[%s] cndl: skip (no interval) n=%d", tag, n)
		return candles
	}
	gaps := detectGaps(ts, intervalSec, 2.5)
	if len(gaps) == 0 {
		logger.Log("session_break[%s] cndl: none n=%d interval=%ds span=%ds tsLen=%d candlesLen=%d",
			tag, n, intervalSec, ts[n-1]-ts[0], len(timestamps), len(candles))
		return candles
	}
	perGap := perSessionBreakPlaceholders(n, len(gaps))
	if perGap <= 0 {
		return candles
	}
	firstBreak := gaps[0]
	gapDelta := int64(0)
	if firstBreak > 0 && firstBreak < n {
		gapDelta = ts[firstBreak] - ts[firstBreak-1]
	}
	logger.Log("session_break[%s] cndl: insert n=%d interval=%ds breaks=%d perBreak=%d firstAt=%d firstDelta=%ds tsLen=%d candlesLen=%d",
		tag, n, intervalSec, len(gaps), perGap, firstBreak, gapDelta, len(timestamps), len(candles))
	out := make([]Candle, 0, len(candles)+perGap*len(gaps))
	gi := 0
	for i := 0; i < len(candles); i++ {
		if gi < len(gaps) && gaps[gi] == i {
			for k := 0; k < perGap; k++ {
				out = append(out, gapCandle())
			}
			gi++
		}
		out = append(out, candles[i])
	}
	return out
}

// WithOverlaySessionBreaks aligns a line overlay (e.g. a moving average) to a
// inserts NaN placeholders at the same break positions so overlay
// values line up with the primary series. Uses the shared prefix
// when lengths differ.
func WithOverlaySessionBreaks(timestamps []int64, values []float64) []float64 {
	n := len(values)
	if len(timestamps) < n {
		n = len(timestamps)
	}
	if n == 0 {
		return values
	}
	ts := timestamps[:n]
	intervalSec := inferIntervalSec(ts)
	if intervalSec <= 0 {
		return values
	}
	gaps := detectGaps(ts, intervalSec, 2.5)
	if len(gaps) == 0 {
		return values
	}
	perGap := perSessionBreakPlaceholders(n, len(gaps))
	if perGap <= 0 {
		return values
	}
	out := make([]float64, 0, len(values)+perGap*len(gaps))
	nan := math.NaN()
	gi := 0
	for i := 0; i < len(values); i++ {
		if gi < len(gaps) && gaps[gi] == i {
			for k := 0; k < perGap; k++ {
				out = append(out, nan)
			}
			gi++
		}
		out = append(out, values[i])
	}
	return out
}

// closedTailPlaceholders returns how many NaN placeholders to append
// to a 1-session series so that the trailing separator approximates
// `sessionBreakFractionTarget` of the output width. Analogous to
// perSessionBreakPlaceholders but for a single trailing run rather than
// between-session breaks.
func closedTailPlaceholders(numBars int) int {
	if numBars <= 0 {
		return 0
	}
	frac := sessionBreakFractionTarget
	// pad / (numBars + pad) = frac  =>  pad = numBars * frac / (1-frac)
	p := int(math.Round(float64(numBars) * frac / (1 - frac)))
	if p < 1 {
		p = 1
	}
	return p
}

// WithClosedMarketTail appends a trailing NaN run to a line series
// so the renderer leaves whitespace on the right, representing the
// currently-closed market period. Use this instead of (or in
// addition to) WithLineSessionBreaks when the displayed window is a
// single
// trading session and the market is not currently in a REGULAR
// state. Pass-through (no change) when values is empty.
func WithClosedMarketTail(values []float64) []float64 {
	if len(values) == 0 {
		return values
	}
	pad := closedTailPlaceholders(len(values))
	if pad <= 0 {
		return values
	}
	out := make([]float64, 0, len(values)+pad)
	out = append(out, values...)
	nan := math.NaN()
	for i := 0; i < pad; i++ {
		out = append(out, nan)
	}
	return out
}

// WithCandleClosedMarketTail is the candlestick analogue of
// WithClosedMarketTail.
func WithCandleClosedMarketTail(candles []Candle) []Candle {
	if len(candles) == 0 {
		return candles
	}
	pad := closedTailPlaceholders(len(candles))
	if pad <= 0 {
		return candles
	}
	out := make([]Candle, 0, len(candles)+pad)
	out = append(out, candles...)
	for i := 0; i < pad; i++ {
		out = append(out, gapCandle())
	}
	return out
}
