package ui

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/ray-x/finsight/internal/chart"
	"github.com/ray-x/finsight/internal/yahoo"
)

// Technicals is the full result of evaluating the common trading
// indicators against a ChartData slice. Values are NaN-padded; callers
// should consult Latest* helpers for the most recent readings.
type Technicals struct {
	SMA20, SMA50, SMA200       []float64
	EMA9, EMA26, EMA59, EMA120 []float64
	BBUpper, BBMid, BBLower    []float64
	RSI14                      []float64
	MACD, MACDSignal, MACDHist []float64
	StochK, StochD             []float64
	Pivot                      chart.Pivot

	Closes []float64

	// Derived cross state: EMA9 vs EMA26.
	EMACrossDir     int // +1 bullish, -1 bearish, 0 flat
	EMACrossBarsAgo int // -1 if no recent cross
}

type bollingerStrategy struct {
	label        string
	detail       string
	pctB         float64
	bandwidthPct float64
	bias         int // +1 bullish, -1 bearish, +2 watch, 0 neutral
}

// ComputeTechnicals runs all indicator computations on the given
// ChartData. The caller must ensure cd != nil and len(cd.Closes) > 0.
func ComputeTechnicals(cd *yahoo.ChartData) *Technicals {
	return ComputeTechnicalsWithContext(cd, nil)
}

// ComputeTechnicalsWithContext computes indicators with an optional
// historical context prepended for warmup. Intended for intraday
// charts where the primary slice is too short for a 200-SMA or even a
// 26-EMA to stabilise: by prepending N prior daily closes we treat the
// price series as continuous across sessions, so the MA lines already
// have their full warmup when the intraday window begins.
//
// The returned slices align 1:1 with `primary` — any context bars are
// sliced off the output so callers can zip values against primary
// timestamps without offset arithmetic.
//
// If ctx is nil, or its bars are not strictly older than primary, it is
// ignored (the function degrades gracefully to ComputeTechnicals).
func ComputeTechnicalsWithContext(primary, ctx *yahoo.ChartData) *Technicals {
	if primary == nil || len(primary.Closes) == 0 {
		return nil
	}
	pCloses := primary.Closes
	pHighs := primary.Highs
	pLows := primary.Lows
	if len(pHighs) != len(pCloses) {
		pHighs = pCloses
	}
	if len(pLows) != len(pCloses) {
		pLows = pCloses
	}

	// Merge context → primary when the context exists and predates the
	// primary series. Use timestamps when both are populated; otherwise
	// fall back to "prepend everything".
	closes := pCloses
	highs := pHighs
	lows := pLows
	offset := 0
	if ctx != nil && len(ctx.Closes) > 0 {
		prefixCloses, prefixHighs, prefixLows := sliceCtxPrefix(ctx, primary)
		if len(prefixCloses) > 0 {
			closes = append(append([]float64{}, prefixCloses...), pCloses...)
			highs = append(append([]float64{}, prefixHighs...), pHighs...)
			lows = append(append([]float64{}, prefixLows...), pLows...)
			offset = len(prefixCloses)
		}
	}

	t := &Technicals{
		Closes: pCloses, // keep primary slice so callers see original prices
		SMA20:  trimPrefix(chart.SMA(closes, 20), offset),
		SMA50:  trimPrefix(chart.SMA(closes, 50), offset),
		SMA200: trimPrefix(chart.SMA(closes, 200), offset),
		EMA9:   trimPrefix(chart.EMA(closes, 9), offset),
		EMA26:  trimPrefix(chart.EMA(closes, 26), offset),
		EMA59:  trimPrefix(chart.EMA(closes, 59), offset),
		EMA120: trimPrefix(chart.EMA(closes, 120), offset),
		RSI14:  trimPrefix(chart.RSI(closes, 14), offset),
	}
	bbU, bbM, bbL := chart.BollingerBands(closes, 20, 2)
	t.BBUpper = trimPrefix(bbU, offset)
	t.BBMid = trimPrefix(bbM, offset)
	t.BBLower = trimPrefix(bbL, offset)
	macd, sig, hist := chart.MACD(closes, 12, 26, 9)
	t.MACD = trimPrefix(macd, offset)
	t.MACDSignal = trimPrefix(sig, offset)
	t.MACDHist = trimPrefix(hist, offset)
	stK, stD := chart.Stochastic(highs, lows, closes, 14, 1, 3)
	t.StochK = trimPrefix(stK, offset)
	t.StochD = trimPrefix(stD, offset)
	t.EMACrossDir, t.EMACrossBarsAgo = chart.CrossState(t.EMA9, t.EMA26)

	// Pivot uses the previous completed bar's HLC. Prefer the last
	// context bar (i.e. previous session close) when the primary is
	// intraday and context is available — that matches the trader's
	// intuition of "yesterday's pivot". Fall back to the bar before the
	// most recent primary bar otherwise.
	if ctx != nil && offset > 0 && len(ctx.Closes) > 0 {
		i := len(ctx.Closes) - 1
		ch := ctx.Closes[i]
		hh := ch
		lh := ch
		if len(ctx.Highs) == len(ctx.Closes) {
			hh = ctx.Highs[i]
		}
		if len(ctx.Lows) == len(ctx.Closes) {
			lh = ctx.Lows[i]
		}
		t.Pivot = chart.PivotPoints(hh, lh, ch)
	} else if n := len(pCloses); n >= 2 {
		t.Pivot = chart.PivotPoints(pHighs[n-2], pLows[n-2], pCloses[n-2])
	}
	return t
}

// sliceCtxPrefix returns the portion of `ctx` that is strictly older
// than `primary`. Returns empty slices when the data sets overlap or
// when timestamps are unavailable to determine ordering.
func sliceCtxPrefix(ctx, primary *yahoo.ChartData) (closes, highs, lows []float64) {
	if len(ctx.Closes) == 0 || len(primary.Closes) == 0 {
		return nil, nil, nil
	}
	// Need timestamps on both to decide overlap; otherwise skip
	// prefixing (better no context than duplicated bars).
	if len(ctx.Timestamps) != len(ctx.Closes) || len(primary.Timestamps) == 0 {
		return nil, nil, nil
	}
	primaryStart := primary.Timestamps[0]
	cutoff := 0
	for i, ts := range ctx.Timestamps {
		if ts < primaryStart {
			cutoff = i + 1
		} else {
			break
		}
	}
	if cutoff == 0 {
		return nil, nil, nil
	}
	closes = ctx.Closes[:cutoff]
	if len(ctx.Highs) == len(ctx.Closes) {
		highs = ctx.Highs[:cutoff]
	} else {
		highs = closes
	}
	if len(ctx.Lows) == len(ctx.Closes) {
		lows = ctx.Lows[:cutoff]
	} else {
		lows = closes
	}
	return closes, highs, lows
}

// trimPrefix drops the first `offset` entries from s. Returns s when
// offset <= 0 so callers can chain safely.
func trimPrefix(s []float64, offset int) []float64 {
	if offset <= 0 || offset >= len(s) {
		if offset >= len(s) {
			return []float64{}
		}
		return s
	}
	return s[offset:]
}

// RenderTechnicalsPanel returns a stack of rendered rows suitable for
// RenderTechnicalsPanel returns a stack of rendered rows suitable for
// the detail view. Width is the terminal column count; the panel uses
// its full width. The provided maMode controls which EMA periods are
// highlighted in the MA row and which MACD params annotate the MACD
// badge. Returns an empty string when no data is available.
func RenderTechnicalsPanel(item WatchlistItem, width int, maMode MAMode) string {
	if item.ChartData == nil || len(item.ChartData.Closes) == 0 {
		return ""
	}
	// Need either a long enough primary series OR a daily context
	// providing warmup. Otherwise the MA lines are mostly NaN.
	hasCtx := item.IndicatorContext != nil && len(item.IndicatorContext.Closes) >= 30
	if !hasCtx && len(item.ChartData.Closes) < 30 {
		return ""
	}
	t := ComputeTechnicalsWithContext(item.ChartData, item.IndicatorContext)
	if t == nil {
		return ""
	}
	if width < 60 {
		width = 60
	}

	var sb strings.Builder
	sb.WriteString(renderMALine(item, maMode, width))
	sb.WriteString("\n")
	sb.WriteString(renderBollingerLine(t, width))
	sb.WriteString("\n")
	sb.WriteString(renderBollingerStrategyLine(t, width))
	sb.WriteString("\n")
	sb.WriteString(renderOscillatorLine(t, width))
	sb.WriteString("\n")
	sb.WriteString(renderPivotLine(t, item.Quote, width))
	return sb.String()
}

// --- per-indicator row renderers ---

var (
	indLabelStyle = lipgloss.NewStyle().Foreground(colorBlue).Bold(true)
	indValueStyle = lipgloss.NewStyle().Foreground(colorWhite).Bold(true)
	indDimStyle   = lipgloss.NewStyle().Foreground(colorGray)
	indBullStyle  = lipgloss.NewStyle().Foreground(colorGreen).Bold(true)
	indBearStyle  = lipgloss.NewStyle().Foreground(colorRed).Bold(true)
	indWarnStyle  = lipgloss.NewStyle().Foreground(colorYellow).Bold(true)
	indActiveLbl  = lipgloss.NewStyle().Foreground(colorYellow).Bold(true)
	indActiveVal  = lipgloss.NewStyle().Foreground(colorYellow).Bold(true).Underline(true)
	indHintStyle  = lipgloss.NewStyle().Foreground(colorGray).Italic(true)
)

func fmtOrDash(v float64) string {
	if math.IsNaN(v) {
		return "—"
	}
	return fmt.Sprintf("%.2f", v)
}

func lastVal(s []float64) float64 { v, _ := chart.LastValid(s); return v }

// renderMALine renders the MA row. The EMA periods shown depend on
// the active MA preset: the preset's own periods are highlighted (bold
// yellow + underlined value), and a reference set of common periods
// that are NOT in the preset is shown in dim style so the reader can
// still orient themselves. Ends with an "M: …" hint indicating the
// `M` key cycles presets and naming the current one.
func renderMALine(item WatchlistItem, maMode MAMode, width int) string {
	if item.ChartData == nil || len(item.ChartData.Closes) == 0 {
		return ""
	}
	primary := item.ChartData
	closes := primary.Closes
	offset := 0
	if item.IndicatorContext != nil && len(item.IndicatorContext.Closes) > 0 {
		prefix, _, _ := sliceCtxPrefix(item.IndicatorContext, primary)
		if len(prefix) > 0 {
			closes = append(append([]float64{}, prefix...), primary.Closes...)
			offset = len(prefix)
		}
	}

	// Active periods from the preset; highlighted.
	active := maMode.EMAPeriods()
	activeSet := make(map[int]bool, len(active))
	for _, p := range active {
		activeSet[p] = true
	}
	// Reference set: common periods the reader likely wants regardless.
	ref := []int{9, 20, 50, 100, 200}
	// Combine preserving order: active first (in preset order), then
	// any reference periods not already included.
	order := append([]int{}, active...)
	for _, p := range ref {
		if !activeSet[p] {
			order = append(order, p)
		}
	}

	var parts []string
	for _, p := range order {
		v := lastVal(trimPrefix(chart.EMA(closes, p), offset))
		label := fmt.Sprintf("EMA%d ", p)
		val := fmtOrDash(v)
		if activeSet[p] {
			parts = append(parts, indActiveLbl.Render(label)+indActiveVal.Render(val))
		} else {
			parts = append(parts, indDimStyle.Render(label)+indValueStyle.Render(val))
		}
	}

	// 9/26 cross state — always useful context.
	ema9 := trimPrefix(chart.EMA(closes, 9), offset)
	ema26 := trimPrefix(chart.EMA(closes, 26), offset)
	crossDir, crossAgo := chart.CrossState(ema9, ema26)
	crossBadge := indDimStyle.Render("flat")
	switch {
	case crossDir > 0:
		crossBadge = indBullStyle.Render(fmt.Sprintf("▲ 9>26 (%db)", crossAgo))
	case crossDir < 0:
		crossBadge = indBearStyle.Render(fmt.Sprintf("▼ 9<26 (%db)", crossAgo))
	}

	hint := indHintStyle.Render(fmt.Sprintf("  M: %s", maMode.Label()))
	return indLabelStyle.Render("MA ") +
		strings.Join(parts, "  ") + "  " +
		crossBadge + hint
}

func renderBollingerLine(t *Technicals, width int) string {
	u, m, l := lastVal(t.BBUpper), lastVal(t.BBMid), lastVal(t.BBLower)
	close := 0.0
	if len(t.Closes) > 0 {
		close = t.Closes[len(t.Closes)-1]
	}
	// %B = (close - lower) / (upper - lower); 0=lower, 1=upper, >1 breakout up.
	pctB := math.NaN()
	if !math.IsNaN(u) && !math.IsNaN(l) && u != l {
		pctB = (close - l) / (u - l)
	}
	badge := indDimStyle.Render("mid")
	switch {
	case !math.IsNaN(pctB) && pctB >= 1:
		badge = indWarnStyle.Render("⚠ UPPER BREAK")
	case !math.IsNaN(pctB) && pctB <= 0:
		badge = indWarnStyle.Render("⚠ LOWER BREAK")
	case !math.IsNaN(pctB) && pctB >= 0.8:
		badge = indBullStyle.Render("upper zone")
	case !math.IsNaN(pctB) && pctB <= 0.2:
		badge = indBearStyle.Render("lower zone")
	}
	pctStr := "—"
	if !math.IsNaN(pctB) {
		pctStr = fmt.Sprintf("%.2f", pctB)
	}
	return indLabelStyle.Render("BB20 ") +
		indDimStyle.Render("upper ") + indValueStyle.Render(fmtOrDash(u)) + "  " +
		indDimStyle.Render("sma20 ") + indValueStyle.Render(fmtOrDash(m)) + "  " +
		indDimStyle.Render("lower ") + indValueStyle.Render(fmtOrDash(l)) + "  " +
		indDimStyle.Render("%B ") + indValueStyle.Render(pctStr) + "  " +
		badge
}

func renderBollingerStrategyLine(t *Technicals, width int) string {
	s := deriveBollingerStrategy(t)
	signalStyle := indDimStyle
	switch s.bias {
	case 1:
		signalStyle = indBullStyle
	case -1:
		signalStyle = indBearStyle
	case 2:
		signalStyle = indWarnStyle
	}

	pctStr := "—"
	if !math.IsNaN(s.pctB) {
		pctStr = fmt.Sprintf("%.2f", s.pctB)
	}
	bandwidthStr := "—"
	if !math.IsNaN(s.bandwidthPct) {
		bandwidthStr = fmt.Sprintf("%.1f%%", s.bandwidthPct)
	}

	return indLabelStyle.Render("BB Strat ") +
		signalStyle.Render(s.label) + "  " +
		indDimStyle.Render(s.detail) + "  " +
		indDimStyle.Render("%B ") + indValueStyle.Render(pctStr) + "  " +
		indDimStyle.Render("BW ") + indValueStyle.Render(bandwidthStr) + "  " +
		indHintStyle.Render("20-SMA basis")
}

func deriveBollingerStrategy(t *Technicals) bollingerStrategy {
	s := bollingerStrategy{
		label:        "wait",
		detail:       "insufficient BB20 data",
		pctB:         math.NaN(),
		bandwidthPct: math.NaN(),
	}
	if t == nil || len(t.Closes) == 0 || len(t.BBUpper) == 0 || len(t.BBMid) == 0 || len(t.BBLower) == 0 {
		return s
	}

	lastIdx := len(t.Closes) - 1
	close := t.Closes[lastIdx]
	upper := t.BBUpper[lastIdx]
	mid := t.BBMid[lastIdx]
	lower := t.BBLower[lastIdx]
	if math.IsNaN(upper) || math.IsNaN(mid) || math.IsNaN(lower) {
		return s
	}

	if upper != lower {
		s.pctB = (close - lower) / (upper - lower)
	}
	if mid != 0 {
		s.bandwidthPct = ((upper - lower) / mid) * 100
	}

	prevValid := false
	prevClose, prevUpper, prevMid, prevLower := 0.0, math.NaN(), math.NaN(), math.NaN()
	if lastIdx > 0 {
		prevClose = t.Closes[lastIdx-1]
		prevUpper = t.BBUpper[lastIdx-1]
		prevMid = t.BBMid[lastIdx-1]
		prevLower = t.BBLower[lastIdx-1]
		prevValid = !math.IsNaN(prevUpper) && !math.IsNaN(prevMid) && !math.IsNaN(prevLower)
	}

	switch {
	case prevValid && prevClose <= prevUpper && close > upper:
		s.label = "breakout long"
		s.detail = "fresh close above upper band"
		s.bias = 1
	case prevValid && prevClose >= prevLower && close < lower:
		s.label = "breakdown short"
		s.detail = "fresh close below lower band"
		s.bias = -1
	case close > upper:
		s.label = "upper-band trend"
		s.detail = "price riding the upper band"
		s.bias = 1
	case close < lower:
		s.label = "lower-band trend"
		s.detail = "price riding the lower band"
		s.bias = -1
	case !math.IsNaN(s.pctB) && s.pctB >= 0.9:
		s.label = "reversion short watch"
		s.detail = "inside-band stretch near upper extreme"
		s.bias = 2
	case !math.IsNaN(s.pctB) && s.pctB <= 0.1:
		s.label = "reversion long watch"
		s.detail = "inside-band stretch near lower extreme"
		s.bias = 2
	case prevValid && prevClose < prevMid && close > mid:
		s.label = "mid-band reclaim"
		s.detail = "bullish regain of SMA20"
		s.bias = 1
	case prevValid && prevClose > prevMid && close < mid:
		s.label = "mid-band failure"
		s.detail = "bearish loss of SMA20"
		s.bias = -1
	case !math.IsNaN(s.bandwidthPct) && s.bandwidthPct <= 5:
		s.label = "squeeze watch"
		s.detail = "bands tight around SMA20"
		s.bias = 2
	default:
		s.label = "mid-band balance"
		s.detail = "price rotating around SMA20"
		s.bias = 0
	}

	return s
}

// renderOscillatorLine packs RSI(14) and Stochastic (%K/%D) side-by-side
// on a single row since neither needs a full-width bar. Layout:
//
//	RSI 74.4 [█████░░] OB  │  Stoch K 98 D 97 [█████░] OB
//
// Each half gets roughly half the available width.
func renderOscillatorLine(t *Technicals, width int) string {
	r := lastVal(t.RSI14)
	k := lastVal(t.StochK)
	d := lastVal(t.StochD)

	rsiBadge := compactBadge(r, 70, 30, 50)
	stochBadge := compactBadge(k, 80, 20, 50)

	// Half width each, minus the separator.
	half := (width - 4) / 2
	if half < 40 {
		half = 40
	}
	// Reserve ~36 cols for label + values + badge (badges now read
	// "overbought"/"oversold"/"bullish"/"bearish"); rest is the bar.
	rsiBarW := half - 36
	if rsiBarW < 8 {
		rsiBarW = 8
	}
	stochBarW := half - 40
	if stochBarW < 8 {
		stochBarW = 8
	}

	left := indLabelStyle.Render("RSI ") +
		indValueStyle.Render(fmtOrDash(r)) + " " +
		rsiBar(r, rsiBarW) + " " + rsiBadge
	right := indLabelStyle.Render("Stoch ") +
		indDimStyle.Render("K ") + indValueStyle.Render(fmtOrDash(k)) + " " +
		indDimStyle.Render("D ") + indValueStyle.Render(fmtOrDash(d)) + " " +
		stochBar(k, stochBarW) + " " + stochBadge

	sep := indDimStyle.Render("  │  ")
	return left + sep + right
}

// compactBadge renders an overbought/oversold/bullish/bearish tag.
// `hi` and `lo` are the overbought / oversold thresholds; `mid`
// splits bullish vs bearish within the neutral zone.
func compactBadge(v, hi, lo, mid float64) string {
	if math.IsNaN(v) {
		return indDimStyle.Render("—")
	}
	switch {
	case v >= hi:
		return indWarnStyle.Render("⚠ overbought")
	case v <= lo:
		return indWarnStyle.Render("⚠ oversold")
	case v >= mid:
		return indBullStyle.Render("bullish")
	default:
		return indBearStyle.Render("bearish")
	}
}

// rsiBar renders a 0..100 horizontal bar with 30/70 guide marks. The
// filled portion is coloured by zone (green >50, red <50, yellow in
// over-b/s zones).
func rsiBar(r float64, width int) string {
	if width < 8 {
		width = 8
	}
	if math.IsNaN(r) {
		return indDimStyle.Render(strings.Repeat("·", width))
	}
	filled := int(r / 100 * float64(width))
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	style := indBullStyle
	switch {
	case r >= 70 || r <= 30:
		style = indWarnStyle
	case r < 50:
		style = indBearStyle
	}
	bar := style.Render(strings.Repeat("█", filled)) +
		indDimStyle.Render(strings.Repeat("░", width-filled))
	return "[" + bar + "]"
}

// stochBar is visually identical to rsiBar but uses 20/80 thresholds.
func stochBar(v float64, width int) string {
	if width < 8 {
		width = 8
	}
	if math.IsNaN(v) {
		return indDimStyle.Render(strings.Repeat("·", width))
	}
	filled := int(v / 100 * float64(width))
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	style := indBullStyle
	switch {
	case v >= 80 || v <= 20:
		style = indWarnStyle
	case v < 50:
		style = indBearStyle
	}
	return "[" + style.Render(strings.Repeat("█", filled)) +
		indDimStyle.Render(strings.Repeat("░", width-filled)) + "]"
}

func renderPivotLine(t *Technicals, q *yahoo.Quote, width int) string {
	p := t.Pivot
	if p.P == 0 {
		return ""
	}
	price := 0.0
	if q != nil {
		price = q.Price
	}
	// Highlight the two nearest levels (one above, one below).
	levels := []struct {
		name  string
		val   float64
		style lipgloss.Style
	}{
		{"R3", p.R3, indBearStyle},
		{"R2", p.R2, indBearStyle},
		{"R1", p.R1, indBearStyle},
		{"P", p.P, indWarnStyle},
		{"S1", p.S1, indBullStyle},
		{"S2", p.S2, indBullStyle},
		{"S3", p.S3, indBullStyle},
	}
	parts := make([]string, 0, len(levels))
	for _, l := range levels {
		tag := l.style.Render(l.name) + " " + indValueStyle.Render(fmt.Sprintf("%.2f", l.val))
		parts = append(parts, tag)
	}
	head := indLabelStyle.Render("Pivot ")
	if price > 0 {
		head += indDimStyle.Render(fmt.Sprintf("(px %.2f) ", price))
	}
	return head + strings.Join(parts, "  ·  ")
}
