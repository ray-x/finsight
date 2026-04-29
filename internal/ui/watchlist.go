package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/ray-x/finsight/internal/chart"
	"github.com/ray-x/finsight/internal/logger"
	"github.com/ray-x/finsight/internal/news"
	"github.com/ray-x/finsight/internal/yahoo"
)

// SortMode defines how the watchlist is sorted.
type SortMode int

const (
	SortDefault SortMode = iota
	SortBySymbol
	SortBySymbolDesc
	SortByChangeAsc
	SortByChangeDesc
	SortByMarketCapAsc
	SortByMarketCapDesc
	sortModeCount
)

func (s SortMode) Label() string {
	switch s {
	case SortBySymbol:
		return "Symbol ↑"
	case SortBySymbolDesc:
		return "Symbol ↓"
	case SortByChangeAsc:
		return "Change% ↑"
	case SortByChangeDesc:
		return "Change% ↓"
	case SortByMarketCapAsc:
		return "Mkt Cap ↑"
	case SortByMarketCapDesc:
		return "Mkt Cap ↓"
	default:
		return "Default"
	}
}

// WatchlistItem represents one row in the watchlist view.
type WatchlistItem struct {
	Symbol    string
	Name      string
	Quote     *yahoo.Quote
	ChartData *yahoo.ChartData
	// IndicatorContext holds prior bars used to warm up technical
	// indicators when ChartData is too short on its own. It is
	// fetched lazily when the detail view's technicals panel needs
	// it. Nil means "not yet fetched" or "not needed".
	IndicatorContext *yahoo.ChartData
	// IndicatorContextInterval is the interval (e.g. "5m", "1d") the
	// context was fetched at. Used to invalidate stale context when
	// the user switches to a different-resolution timeframe.
	IndicatorContextInterval string
	Financials               *yahoo.FinancialData
	Holders                  *yahoo.HolderData
	News                     []news.Item
	Loading                  bool
	Error                    string
}

// Column widths
const (
	colSymbol    = 14
	colPrice     = 12
	colChange    = 12
	colChangePct = 10
	colMarketCap = 12
	colOpen      = 12
	colLow       = 12
	colHigh      = 12
	colVolume    = 10
	colAvgVol    = 10
)

var (
	tableHeaderStyle = lipgloss.NewStyle().
				Foreground(colorGray).
				Bold(true).
				PaddingLeft(1)

	rowStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderBottom(true).
			BorderTop(false).
			BorderLeft(false).
			BorderRight(false).
			BorderForeground(colorDim)

	selectedRowStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.NormalBorder()).
				BorderBottom(true).
				BorderTop(false).
				BorderLeft(false).
				BorderRight(false).
				BorderForeground(colorBlue).
				Background(lipgloss.Color("#1e2030"))

	cellStyle = lipgloss.NewStyle()
)

// RenderWatchlist renders the full watchlist as a table with charts.
func RenderWatchlist(items []WatchlistItem, selected int, width, _ int, chartStyle string, sortMode SortMode, timeframe string) string {
	if len(items) == 0 {
		return lipgloss.Place(width, 3,
			lipgloss.Center, lipgloss.Center,
			nameStyle.Render("No symbols in watchlist. Press / to search and add symbols."))
	}

	chartW := calcChartWidth(width)

	header := renderTableHeader(width, chartW, sortMode)
	sep := separatorStyle.Render(strings.Repeat("─", width-2))

	var rows []string
	rows = append(rows, header, sep)
	for i, item := range items {
		isSelected := i == selected
		row := renderTableRow(item, isSelected, width, chartW, chartStyle, timeframe)
		rows = append(rows, row)
	}

	return strings.Join(rows, "\n")
}

// visibleCols determines which optional columns fit within the given width.
// Returns bools for: open, low, high, change, volume, avgVol, marketCap
type colVis struct {
	open, low, high, change, volume, avgVol, marketCap bool
}

func calcVisibleCols(width, chartW int) colVis {
	// Mandatory: symbol + trend + price + change%
	used := colSymbol + chartW + colPrice + colChangePct
	remaining := width - used

	// Add columns in priority order; stop when no more room
	v := colVis{}
	if remaining >= colChange {
		v.change = true
		remaining -= colChange
	}
	if remaining >= colMarketCap {
		v.marketCap = true
		remaining -= colMarketCap
	}
	if remaining >= colOpen {
		v.open = true
		remaining -= colOpen
	}
	if remaining >= colLow {
		v.low = true
		remaining -= colLow
	}
	if remaining >= colHigh {
		v.high = true
		remaining -= colHigh
	}
	if remaining >= colVolume {
		v.volume = true
		remaining -= colVolume
	}
	if remaining >= colAvgVol {
		v.avgVol = true
	}
	return v
}

func calcChartWidth(totalWidth int) int {
	// Start with a base allocation and clamp
	fixedCols := colSymbol + colPrice + colChangePct + 4
	w := totalWidth - fixedCols
	// Reserve room for the other columns that might show
	w -= colChange + colMarketCap + colOpen + colLow + colHigh + colVolume + colAvgVol
	if w < 6 {
		w = 6
	}
	if w > 30 {
		w = 30
	}
	return w
}

func renderTableHeader(width, chartW int, sortMode SortMode) string {
	symLabel := "Symbol"
	pctLabel := "Change%"
	capLabel := "Mkt Cap"
	switch sortMode {
	case SortBySymbol:
		symLabel = "Symbol ↑"
	case SortBySymbolDesc:
		symLabel = "Symbol ↓"
	case SortByChangeAsc:
		pctLabel = "Change% ↑"
	case SortByChangeDesc:
		pctLabel = "Change% ↓"
	case SortByMarketCapAsc:
		capLabel = "Mkt Cap ↑"
	case SortByMarketCapDesc:
		capLabel = "Mkt Cap ↓"
	}

	vis := calcVisibleCols(width, chartW)

	parts := []string{
		tableHeaderStyle.Width(colSymbol).Render(symLabel),
		tableHeaderStyle.Width(chartW).Render("Trend"),
		tableHeaderStyle.Width(colPrice).Align(lipgloss.Right).Render("Last"),
	}
	if vis.open {
		parts = append(parts, tableHeaderStyle.Width(colOpen).Align(lipgloss.Right).Render("Open"))
	}
	if vis.low {
		parts = append(parts, tableHeaderStyle.Width(colLow).Align(lipgloss.Right).Render("Low"))
	}
	if vis.high {
		parts = append(parts, tableHeaderStyle.Width(colHigh).Align(lipgloss.Right).Render("High"))
	}
	if vis.change {
		parts = append(parts, tableHeaderStyle.Width(colChange).Align(lipgloss.Right).Render("Change"))
	}
	parts = append(parts, tableHeaderStyle.Width(colChangePct).Align(lipgloss.Right).Render(pctLabel))
	if vis.volume {
		parts = append(parts, tableHeaderStyle.Width(colVolume).Align(lipgloss.Right).Render("Volume"))
	}
	if vis.avgVol {
		parts = append(parts, tableHeaderStyle.Width(colAvgVol).Align(lipgloss.Right).Render("AvgVol"))
	}
	if vis.marketCap {
		parts = append(parts, tableHeaderStyle.Width(colMarketCap).Align(lipgloss.Right).Render(capLabel))
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

func renderTableRow(item WatchlistItem, selected bool, width, chartW int, chartStyle string, timeframe string) string {
	vis := calcVisibleCols(width, chartW)

	applyRowStyle := func(line string) string {
		// Truncate the joined line to width before applying row style to
		// prevent lipgloss from wrapping content that exceeds terminal width.
		joined := strings.Split(line, "\n")
		for i, l := range joined {
			if lipgloss.Width(l) > width-2 {
				joined[i] = ansi.Truncate(l, width-2, "")
			}
		}
		line = strings.Join(joined, "\n")
		if selected {
			return selectedRowStyle.Width(width - 2).Render(line)
		}
		return rowStyle.Width(width - 2).Render(line)
	}

	symCol := cellStyle.Width(colSymbol).Render(
		symbolStyle.Render(item.Symbol) + "\n" + nameStyle.Render(truncate(item.Name, colSymbol-2)))

	if item.Loading {
		return applyRowStyle(lipgloss.JoinHorizontal(lipgloss.Top, symCol, nameStyle.Render("  Loading...")))
	}
	if item.Error != "" {
		return applyRowStyle(lipgloss.JoinHorizontal(lipgloss.Top, symCol, errorStyle.Render("  "+truncate(item.Error, 40))))
	}

	q := item.Quote
	if q == nil {
		return applyRowStyle(symCol)
	}

	positive := q.ChangePercent >= 0

	// Render chart based on style
	chartStr := renderInlineChart(item, chartW-2, 2, chartStyle, positive, timeframe)
	chartCol := cellStyle.Width(chartW).Render(chartStr)

	// Price column
	priceStr := formatPrice(q.Price, q.Currency)
	priceCol := cellStyle.Width(colPrice).Align(lipgloss.Right).Render(priceStyle.Render(priceStr))

	parts := []string{symCol, chartCol, priceCol}

	// Open column
	if vis.open {
		openStr := formatPriceNum(q.Open, q.Currency)
		openCol := cellStyle.Width(colOpen).Align(lipgloss.Right).Render(nameStyle.Render(openStr))
		parts = append(parts, openCol)
	}

	// Low column
	if vis.low {
		lowStr := formatPriceNum(q.DayLow, q.Currency)
		lowCol := cellStyle.Width(colLow).Align(lipgloss.Right).Render(nameStyle.Render(lowStr))
		parts = append(parts, lowCol)
	}

	// High column
	if vis.high {
		highStr := formatPriceNum(q.DayHigh, q.Currency)
		highCol := cellStyle.Width(colHigh).Align(lipgloss.Right).Render(nameStyle.Render(highStr))
		parts = append(parts, highCol)
	}

	// Change column
	if vis.change {
		arrow := changeArrow(positive)
		var changeStr string
		if isZeroDecimalCurrency(q.Currency) {
			changeStr = fmt.Sprintf("%s%s %s", map[bool]string{true: "+", false: ""}[q.Change >= 0], formatIntWithCommas(int64(math.Round(q.Change))), arrow)
		} else {
			changeStr = fmt.Sprintf("%+.2f %s", q.Change, arrow)
		}
		changeCol := cellStyle.Width(colChange).Align(lipgloss.Right).Render(changeStyle(positive).Render(changeStr))
		parts = append(parts, changeCol)
	}

	// Change% column (always visible)
	pctStr := fmt.Sprintf("%+.2f%%", q.ChangePercent)
	pctCol := cellStyle.Width(colChangePct).Align(lipgloss.Right).Render(changeStyle(positive).Render(pctStr))
	parts = append(parts, pctCol)

	// Volume column
	if vis.volume {
		volStr := formatVolume(q.Volume)
		volCol := cellStyle.Width(colVolume).Align(lipgloss.Right).Render(nameStyle.Render(volStr))
		parts = append(parts, volCol)
	}

	// AvgVol column
	if vis.avgVol {
		avgVolStr := formatVolume(q.AvgVolume)
		avgVolCol := cellStyle.Width(colAvgVol).Align(lipgloss.Right).Render(nameStyle.Render(avgVolStr))
		parts = append(parts, avgVolCol)
	}

	// Market cap column
	if vis.marketCap {
		capStr := formatMarketCap(q.MarketCap)
		capCol := cellStyle.Width(colMarketCap).Align(lipgloss.Right).Render(nameStyle.Render(capStr))
		parts = append(parts, capCol)
	}

	line := lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	return applyRowStyle(line)
}

type oneDRenderMode int

var nowFunc = time.Now

const (
	oneDRenderCurrentOnly oneDRenderMode = iota
	oneDRenderCurrentWithTail
	oneDRenderPrevSessionBreakCurrent
)

type oneDRenderLayout struct {
	mode            oneDRenderMode
	currStart       int
	prevStart       int
	prevKeep        int
	sessionBreakPad int
	tailPad         int
}

func closedTailPad(numBars int) int {
	if numBars <= 0 {
		return 0
	}
	pad := int(math.Round(float64(numBars) * 0.125 / 0.875))
	if pad < 1 {
		pad = 1
	}
	return pad
}

func inferredSessionIntervalSec(ts []int64, currStart int) int64 {
	if currStart >= 0 && currStart+1 < len(ts) {
		if delta := ts[currStart+1] - ts[currStart]; delta > 0 {
			return delta
		}
	}
	if currStart >= 2 {
		if delta := ts[currStart-1] - ts[currStart-2]; delta > 0 {
			return delta
		}
	}
	for i := 1; i < len(ts); i++ {
		if delta := ts[i] - ts[i-1]; delta > 0 {
			return delta
		}
	}
	return 0
}

func sessionBreakFractionForDuration(breakDurationSec int64) float64 {
	const (
		minFraction    = 0.125
		maxFraction    = 1.0 / 6.0
		referenceMin   = int64(12 * 60 * 60)
		referenceRange = int64(6 * 60 * 60)
	)
	if breakDurationSec <= referenceMin {
		return minFraction
	}
	if breakDurationSec >= referenceMin+referenceRange {
		return maxFraction
	}
	normalized := float64(breakDurationSec-referenceMin) / float64(referenceRange)
	return minFraction + normalized*(maxFraction-minFraction)
}

func sessionBreakFractionForSeries(ts []int64, currStart int) float64 {
	if currStart <= 0 || currStart >= len(ts) {
		return 0.125
	}
	intervalSec := inferredSessionIntervalSec(ts, currStart)
	if intervalSec <= 0 {
		return 0.125
	}
	breakDurationSec := ts[currStart] - ts[currStart-1] - intervalSec
	if breakDurationSec < intervalSec {
		breakDurationSec = intervalSec
	}
	return sessionBreakFractionForDuration(breakDurationSec)
}

func sessionBreakPadForSeries(ts []int64, currStart, currentBars int) int {
	const currentFraction = 0.5
	if currentBars <= 0 {
		return 0
	}
	if currStart <= 0 || currStart >= len(ts) {
		return closedTailPad(currentBars)
	}
	breakFraction := sessionBreakFractionForSeries(ts, currStart)
	pad := int(math.Round(float64(currentBars) * breakFraction / currentFraction))
	if pad < 1 {
		pad = 1
	}
	return pad
}

func isSessionLikelyActive(ts []int64, currStart int) bool {
	if len(ts) == 0 {
		return false
	}
	intervalSec := inferredSessionIntervalSec(ts, currStart)
	if intervalSec <= 0 {
		return false
	}
	ageSec := nowFunc().Unix() - ts[len(ts)-1]
	if ageSec < 0 {
		return true
	}
	freshWindowSec := int64(12 * intervalSec)
	if freshWindowSec < 20*60 {
		freshWindowSec = 20 * 60
	}
	return ageSec <= freshWindowSec
}

func latestQuoteTimestamp(q *yahoo.Quote) int64 {
	if q == nil {
		return 0
	}
	latest := q.RegularMarketTime
	if q.PreMarketTime > latest {
		latest = q.PreMarketTime
	}
	if q.PostMarketTime > latest {
		latest = q.PostMarketTime
	}
	return latest
}

func isQuoteLikelyActive(q *yahoo.Quote) bool {
	latest := latestQuoteTimestamp(q)
	if latest <= 0 {
		return false
	}
	ageSec := nowFunc().Unix() - latest
	if ageSec < 0 {
		return true
	}
	return ageSec <= 20*60
}

func isLikelyTradingByExchangeClock(symbol string) bool {
	now := nowFunc()
	upper := strings.ToUpper(symbol)

	locName := "America/New_York"
	openHour, openMin := 9, 30
	closeHour, closeMin := 16, 0

	switch {
	case strings.HasSuffix(upper, ".AX"):
		locName = "Australia/Sydney"
		openHour, openMin = 10, 0
		closeHour, closeMin = 16, 0
	case strings.HasSuffix(upper, ".T"):
		locName = "Asia/Tokyo"
		openHour, openMin = 9, 0
		closeHour, closeMin = 15, 0
	}

	loc, err := time.LoadLocation(locName)
	if err != nil {
		return false
	}
	localNow := now.In(loc)
	wd := localNow.Weekday()
	if wd == time.Saturday || wd == time.Sunday {
		return false
	}
	minutes := localNow.Hour()*60 + localNow.Minute()
	openMinutes := openHour*60 + openMin
	closeMinutes := closeHour*60 + closeMin
	return minutes >= openMinutes && minutes < closeMinutes
}

func computeOneDRenderLayout(item WatchlistItem) oneDRenderLayout {
	if item.ChartData == nil || len(item.ChartData.Timestamps) == 0 {
		logger.Log("one_d_layout: symbol=%s mode=empty", item.Symbol)
		return oneDRenderLayout{}
	}
	ts := item.ChartData.Timestamps
	breaks := chart.DetectSessionBreaks(ts)
	currStart := 0
	if len(breaks) > 0 {
		currStart = breaks[len(breaks)-1]
	}
	prevStart := 0
	hasPrev := false
	if currStart > 0 {
		hasPrev = true
		if len(breaks) > 1 {
			prevStart = breaks[len(breaks)-2]
		}
	}
	prevBars := currStart - prevStart
	currentBars := len(ts) - currStart
	if currentBars <= 0 {
		currentBars = len(ts)
		currStart = 0
		hasPrev = false
		prevStart = 0
		prevBars = 0
	}
	state := ""
	if item.Quote != nil {
		state = strings.ToUpper(item.Quote.MarketState)
	}
	isRegular := state == "REGULAR"
	isPremarket := strings.HasPrefix(state, "PRE")
	isExplicitClosed := state != "" && !isRegular && !isPremarket
	isLikelyActiveFromQuote := state == "" && isQuoteLikelyActive(item.Quote)
	isLikelyActiveFromBars := state == "" && hasPrev && isSessionLikelyActive(ts, currStart)
	isLikelyActiveFromClock := false
	if state == "" && !isLikelyActiveFromQuote && !isLikelyActiveFromBars && isLikelyTradingByExchangeClock(item.Symbol) {
		// Guard against stale two-session snapshots: when currentBars is
		// already close to a full prior session and we only have clock
		// evidence (no fresh quote/bar timestamps), treat as not-active.
		if !(hasPrev && prevBars > 0 && currentBars >= int(math.Round(float64(prevBars)*0.8))) {
			isLikelyActiveFromClock = true
		}
	}
	isTradingNow := isRegular || isPremarket || isLikelyActiveFromQuote || isLikelyActiveFromBars || isLikelyActiveFromClock

	if isExplicitClosed || (!isTradingNow && hasPrev) {
		tailPad := closedTailPad(currentBars)
		logger.Log("one_d_layout: symbol=%s state=%q mode=current_with_tail hasPrev=%v currStart=%d currentBars=%d tailPad=%d quoteActive=%v barsActive=%v clockActive=%v",
			item.Symbol, state, hasPrev, currStart, currentBars, tailPad, isLikelyActiveFromQuote, isLikelyActiveFromBars, isLikelyActiveFromClock)
		return oneDRenderLayout{
			mode:      oneDRenderCurrentWithTail,
			currStart: currStart,
			tailPad:   tailPad,
		}
	}

	// For multi-session data, always show prev+session_break+current
	// regardless of market state. This ensures IKO.AX and other non-US
	// exchanges show overnight session breaks even when
	// MarketState is empty.
	if hasPrev {
		currentFraction := 0.5
		if isTradingNow && prevBars > 0 {
			progress := float64(currentBars) / float64(prevBars)
			if progress < 0.12 {
				progress = 0.12
			}
			if progress > 0.75 {
				progress = 0.75
			}
			currentFraction = progress
		}
		breakFraction := sessionBreakFractionForSeries(ts, currStart)
		prevFraction := 1 - currentFraction - breakFraction
		if prevFraction < 0.1 {
			prevFraction = 0.1
			currentFraction = 1 - breakFraction - prevFraction
		}
		if currentFraction <= 0 {
			currentFraction = 0.5
		}
		prevKeep := int(math.Round(float64(currentBars) * prevFraction / currentFraction))
		if prevKeep < 1 {
			prevKeep = 1
		}
		if prevKeep > prevBars {
			prevKeep = prevBars
		}
		sessionBreakPad := sessionBreakPadForSeries(ts, currStart, currentBars)
		layout := oneDRenderLayout{
			mode:            oneDRenderPrevSessionBreakCurrent,
			currStart:       currStart,
			prevStart:       prevStart,
			prevKeep:        prevKeep,
			sessionBreakPad: sessionBreakPad,
		}
		logger.Log("one_d_layout: symbol=%s state=%q mode=prev_session_break_current hasPrev=%v currStart=%d prevStart=%d currentBars=%d prevKeep=%d sessionBreakPad=%d quoteActive=%v barsActive=%v clockActive=%v",
			item.Symbol, state, hasPrev, currStart, prevStart, currentBars, prevKeep, sessionBreakPad, isLikelyActiveFromQuote, isLikelyActiveFromBars, isLikelyActiveFromClock)
		return layout
	}
	logger.Log("one_d_layout: symbol=%s state=%q mode=current_only hasPrev=%v currStart=%d currentBars=%d quoteActive=%v barsActive=%v clockActive=%v",
		item.Symbol, state, hasPrev, currStart, currentBars, isLikelyActiveFromQuote, isLikelyActiveFromBars, isLikelyActiveFromClock)
	return oneDRenderLayout{mode: oneDRenderCurrentOnly, currStart: currStart}
}

func applyOneDLayoutFloat64(values []float64, layout oneDRenderLayout) []float64 {
	if len(values) == 0 {
		return values
	}
	if layout.currStart < 0 || layout.currStart > len(values) {
		layout.currStart = 0
	}
	switch layout.mode {
	case oneDRenderPrevSessionBreakCurrent:
		prevEnd := layout.currStart
		prevStart := prevEnd - layout.prevKeep
		if prevStart < layout.prevStart {
			prevStart = layout.prevStart
		}
		if prevStart < 0 {
			prevStart = 0
		}
		out := make([]float64, 0, len(values[prevStart:])+layout.sessionBreakPad)
		out = append(out, values[prevStart:prevEnd]...)
		nan := math.NaN()
		for i := 0; i < layout.sessionBreakPad; i++ {
			out = append(out, nan)
		}
		out = append(out, values[layout.currStart:]...)
		return out
	case oneDRenderCurrentWithTail:
		out := make([]float64, 0, len(values[layout.currStart:])+layout.tailPad)
		out = append(out, values[layout.currStart:]...)
		nan := math.NaN()
		for i := 0; i < layout.tailPad; i++ {
			out = append(out, nan)
		}
		return out
	default:
		return values[layout.currStart:]
	}
}

func applyOneDLayoutInt64(values []int64, layout oneDRenderLayout) []int64 {
	if len(values) == 0 {
		return values
	}
	if layout.currStart < 0 || layout.currStart > len(values) {
		layout.currStart = 0
	}
	switch layout.mode {
	case oneDRenderPrevSessionBreakCurrent:
		prevEnd := layout.currStart
		prevStart := prevEnd - layout.prevKeep
		if prevStart < layout.prevStart {
			prevStart = layout.prevStart
		}
		if prevStart < 0 {
			prevStart = 0
		}
		out := make([]int64, 0, len(values[prevStart:])+layout.sessionBreakPad)
		out = append(out, values[prevStart:prevEnd]...)
		for i := 0; i < layout.sessionBreakPad; i++ {
			out = append(out, 0)
		}
		out = append(out, values[layout.currStart:]...)
		return out
	case oneDRenderCurrentWithTail:
		last := values[len(values)-1]
		out := make([]int64, 0, len(values[layout.currStart:])+layout.tailPad)
		out = append(out, values[layout.currStart:]...)
		for i := 0; i < layout.tailPad; i++ {
			out = append(out, last)
		}
		return out
	default:
		return values[layout.currStart:]
	}
}

func applyOneDLayoutVolumes(values []int64, layout oneDRenderLayout) []int64 {
	if len(values) == 0 {
		return values
	}
	if layout.currStart < 0 || layout.currStart > len(values) {
		layout.currStart = 0
	}
	switch layout.mode {
	case oneDRenderPrevSessionBreakCurrent:
		prevEnd := layout.currStart
		prevStart := prevEnd - layout.prevKeep
		if prevStart < layout.prevStart {
			prevStart = layout.prevStart
		}
		if prevStart < 0 {
			prevStart = 0
		}
		out := make([]int64, 0, len(values[prevStart:])+layout.sessionBreakPad)
		out = append(out, values[prevStart:prevEnd]...)
		for i := 0; i < layout.sessionBreakPad; i++ {
			out = append(out, 0)
		}
		out = append(out, values[layout.currStart:]...)
		return out
	case oneDRenderCurrentWithTail:
		out := make([]int64, 0, len(values[layout.currStart:])+layout.tailPad)
		out = append(out, values[layout.currStart:]...)
		for i := 0; i < layout.tailPad; i++ {
			out = append(out, 0)
		}
		return out
	default:
		return values[layout.currStart:]
	}
}

func applyOneDLayoutCandles(values []chart.Candle, layout oneDRenderLayout) []chart.Candle {
	if len(values) == 0 {
		return values
	}
	if layout.currStart < 0 || layout.currStart > len(values) {
		layout.currStart = 0
	}
	sessionBreak := func() chart.Candle {
		n := math.NaN()
		return chart.Candle{Open: n, Close: n, High: n, Low: n}
	}
	switch layout.mode {
	case oneDRenderPrevSessionBreakCurrent:
		prevEnd := layout.currStart
		prevStart := prevEnd - layout.prevKeep
		if prevStart < layout.prevStart {
			prevStart = layout.prevStart
		}
		if prevStart < 0 {
			prevStart = 0
		}
		out := make([]chart.Candle, 0, len(values[prevStart:])+layout.sessionBreakPad)
		out = append(out, values[prevStart:prevEnd]...)
		for i := 0; i < layout.sessionBreakPad; i++ {
			out = append(out, sessionBreak())
		}
		out = append(out, values[layout.currStart:]...)
		return out
	case oneDRenderCurrentWithTail:
		out := make([]chart.Candle, 0, len(values[layout.currStart:])+layout.tailPad)
		out = append(out, values[layout.currStart:]...)
		for i := 0; i < layout.tailPad; i++ {
			out = append(out, sessionBreak())
		}
		return out
	default:
		return values[layout.currStart:]
	}
}

func shapeOneDChartData(item WatchlistItem) *yahoo.ChartData {
	if item.ChartData == nil {
		return nil
	}
	layout := computeOneDRenderLayout(item)
	cd := item.ChartData
	return &yahoo.ChartData{
		Timestamps: applyOneDLayoutInt64(cd.Timestamps, layout),
		Closes:     applyOneDLayoutFloat64(cd.Closes, layout),
		Opens:      applyOneDLayoutFloat64(cd.Opens, layout),
		Highs:      applyOneDLayoutFloat64(cd.Highs, layout),
		Lows:       applyOneDLayoutFloat64(cd.Lows, layout),
		Volumes:    applyOneDLayoutVolumes(cd.Volumes, layout),
	}
}

func sanitizeOneDOutlierWicks(symbol string, cd *yahoo.ChartData) *yahoo.ChartData {
	if cd == nil {
		return nil
	}
	n := len(cd.Closes)
	if n < 3 || len(cd.Opens) < n || len(cd.Highs) < n || len(cd.Lows) < n {
		return cd
	}
	out := &yahoo.ChartData{
		Timestamps: append([]int64(nil), cd.Timestamps...),
		Closes:     append([]float64(nil), cd.Closes...),
		Opens:      append([]float64(nil), cd.Opens...),
		Highs:      append([]float64(nil), cd.Highs...),
		Lows:       append([]float64(nil), cd.Lows...),
		Volumes:    append([]int64(nil), cd.Volumes...),
	}
	const spikePct = 0.03
	fixedHigh := 0
	fixedLow := 0
	for i := 1; i < n-1; i++ {
		if math.IsNaN(out.Opens[i]) || math.IsNaN(out.Closes[i]) || math.IsNaN(out.Highs[i]) || math.IsNaN(out.Lows[i]) {
			continue
		}
		bodyHigh := math.Max(out.Opens[i], out.Closes[i])
		bodyLow := math.Min(out.Opens[i], out.Closes[i])
		if bodyHigh <= 0 || bodyLow <= 0 {
			continue
		}
		neighborHigh := math.Max(bodyHigh, math.Max(out.Closes[i-1], out.Closes[i+1]))
		neighborLow := math.Min(bodyLow, math.Min(out.Closes[i-1], out.Closes[i+1]))

		if out.Highs[i] > bodyHigh*(1+spikePct) && out.Highs[i] > neighborHigh*(1+spikePct) {
			out.Highs[i] = bodyHigh
			fixedHigh++
		}
		if out.Lows[i] < bodyLow*(1-spikePct) && out.Lows[i] < neighborLow*(1-spikePct) {
			out.Lows[i] = bodyLow
			fixedLow++
		}
	}
	if fixedHigh > 0 || fixedLow > 0 {
		logger.Log("one_d_sanitize: symbol=%s bars=%d fixedHigh=%d fixedLow=%d thresholdPct=%.2f",
			symbol, n, fixedHigh, fixedLow, spikePct*100)
	}
	return out
}

func renderInlineChart(item WatchlistItem, w, h int, style string, positive bool, timeframe string) string {
	if item.ChartData == nil {
		return ""
	}
	data := item.ChartData
	if timeframe == "1D" {
		data = shapeOneDChartData(item)
		data = sanitizeOneDOutlierWicks(item.Symbol, data)
	}
	switch style {
	case "dotted_line":
		if len(data.Closes) == 0 {
			return ""
		}
		sparkline := chart.RenderSparklineLine(data.Closes, w, h)
		color := changeColor(positive)
		lines := strings.Split(sparkline, "\n")
		for i, line := range lines {
			lines[i] = color + line + "\033[0m"
		}
		return strings.Join(lines, "\n")
	case "candlestick":
		if len(data.Opens) == 0 || len(data.Closes) == 0 {
			return ""
		}
		candles := buildCandles(data)
		return chart.RenderCandlestick(candles, w, h)
	default: // "candlestick_dotted"
		if len(data.Opens) == 0 || len(data.Closes) == 0 {
			return ""
		}
		candles := buildCandles(data)
		return chart.RenderCandlestickBraille(candles, w, h, false)
	}
}

// RenderDetailChart renders a large chart for the detail view.
func RenderDetailChart(item WatchlistItem, width, height int, timeframe string, chartStyle string, showMAOverlay bool, maMode MAMode) string {
	if item.ChartData == nil || len(item.ChartData.Opens) == 0 {
		return chartBorderStyle.Width(width - 4).Render("No chart data available")
	}
	renderData := item.ChartData
	if timeframe == "1D" {
		renderData = shapeOneDChartData(item)
		renderData = sanitizeOneDOutlierWicks(item.Symbol, renderData)
	}

	q := item.Quote
	positive := q != nil && q.ChangePercent >= 0

	// Header line
	header := symbolStyle.Render(item.Symbol)
	if q != nil {
		priceStr := formatPrice(q.Price, q.Currency)
		changeStr := fmt.Sprintf("%+.2f%%", q.ChangePercent)
		header = fmt.Sprintf("%s  %s  %s  %s",
			symbolStyle.Render(item.Symbol),
			priceStyle.Render(priceStr),
			changeStyle(positive).Render(changeStr),
			nameStyle.Render("["+timeframe+"]"))
	}

	// Compute price range
	opens := renderData.Opens
	closes := renderData.Closes
	allPrices := append([]float64{}, opens...)
	allPrices = append(allPrices, closes...)
	if len(renderData.Highs) > 0 {
		allPrices = append(allPrices, renderData.Highs...)
	}
	if len(renderData.Lows) > 0 {
		allPrices = append(allPrices, renderData.Lows...)
	}
	minPrice, maxPrice := allPrices[0], allPrices[0]
	for _, v := range allPrices {
		if v > 0 && v < minPrice {
			minPrice = v
		}
		if v > maxPrice {
			maxPrice = v
		}
	}

	// Y-axis label width — currency-aware
	currency := ""
	if q != nil {
		currency = q.Currency
	}
	yFmt := func(price float64) string {
		return formatPriceNum(price, currency)
	}
	yAxisWidth := len(yFmt(maxPrice))
	priceLabelW := len(yFmt(minPrice))
	if priceLabelW > yAxisWidth {
		yAxisWidth = priceLabelW
	}
	yAxisWidth += 1 // padding

	// Layout dimensions
	volHeight := 3                        // rows for volume bars
	chartHeight := height - 5 - volHeight // header + x-axis row + borders + volume
	if chartHeight < 3 {
		chartHeight = 3
	}
	chartWidth := width - 6 - yAxisWidth
	if chartWidth < 10 {
		chartWidth = 10
	}

	chartStr := renderDetailChartContent(item, renderData, chartWidth, chartHeight, chartStyle, positive, showMAOverlay, maMode, timeframe)

	// Render volume bars
	volStr := ""
	if len(renderData.Volumes) > 0 {
		candles := buildCandles(renderData)
		volStr = chart.RenderVolumeBars(candles, renderData.Volumes, chartWidth, volHeight)
	}

	// Build Y-axis labels (right side)
	numYLabels := chartHeight / 2
	if numYLabels < 2 {
		numYLabels = 2
	}
	if numYLabels > 8 {
		numYLabels = 8
	}

	chartLines := strings.Split(chartStr, "\n")
	// Pad chart lines to chartHeight
	for len(chartLines) < chartHeight {
		chartLines = append(chartLines, strings.Repeat(" ", chartWidth))
	}

	priceRange := maxPrice - minPrice
	if priceRange == 0 {
		priceRange = 1
	}

	// Pre-compute which chart rows get a Y-axis label
	yLabelRows := make(map[int]string)
	for i := 0; i < numYLabels; i++ {
		frac := float64(i) / float64(numYLabels-1)
		price := maxPrice - frac*priceRange
		row := int(math.Round(frac * float64(chartHeight-1)))
		formatted := yFmt(price)
		label := fmt.Sprintf("%*s", yAxisWidth, formatted)
		yLabelRows[row] = label
	}

	// Combine chart lines with Y-axis
	var bodyLines []string
	for i, line := range chartLines {
		if label, ok := yLabelRows[i]; ok {
			bodyLines = append(bodyLines, line+nameStyle.Render(label))
		} else {
			bodyLines = append(bodyLines, line+strings.Repeat(" ", yAxisWidth))
		}
	}

	// Build X-axis (time labels)
	xAxis := buildTimeAxis(renderData.Timestamps, chartWidth, timeframe)

	// Volume label
	volLabel := ""
	if volStr != "" {
		volLabel = nameStyle.Render("Vol")
	}

	parts := []string{
		header,
		strings.Join(bodyLines, "\n"),
		volLabel,
		volStr,
	}
	if showMAOverlay {
		if macd := buildMACDSection(item, chartWidth, chartStyle, maMode); macd != "" {
			parts = append(parts, macd)
		}
	}
	parts = append(parts, nameStyle.Render(xAxis))

	chartWithAxis := lipgloss.JoinVertical(lipgloss.Left, parts...)

	return chartBorderStyle.Width(width - 4).Render(chartWithAxis)
}

// buildMACDSection renders a 3-line MACD panel (header + 2-row
// histogram) sized to chartWidth so bars align with the candles. Bar
// width is picked based on chartStyle so each histogram bar sits under
// exactly one candle.
func buildMACDSection(item WatchlistItem, chartWidth int, chartStyle string, maMode MAMode) string {
	fast, slow, signal := maMode.MACDParams()
	if item.ChartData == nil || len(item.ChartData.Closes) < slow+signal {
		return ""
	}
	macd, sig, hist := macdWithContext(item, fast, slow, signal)
	if len(hist) == 0 {
		return ""
	}
	mVal := lastVal(macd)
	sVal := lastVal(sig)
	hVal := lastVal(hist)
	crossDir, crossAgo := chart.CrossState(macd, sig)
	badge := indDimStyle.Render("—")
	switch {
	case crossDir > 0:
		badge = indBullStyle.Render(fmt.Sprintf("▲ bull cross (%db)", crossAgo))
	case crossDir < 0:
		badge = indBearStyle.Render(fmt.Sprintf("▼ bear cross (%db)", crossAgo))
	}
	histStyle := indDimStyle
	if !math.IsNaN(hVal) {
		if hVal > 0 {
			histStyle = indBullStyle
		} else if hVal < 0 {
			histStyle = indBearStyle
		}
	}
	header := indLabelStyle.Render(fmt.Sprintf("MACD(%d-%d-%d) ", fast, slow, signal)) +
		indDimStyle.Render("line ") + indValueStyle.Render(fmtOrDash(mVal)) + "  " +
		indDimStyle.Render("sig ") + indValueStyle.Render(fmtOrDash(sVal)) + "  " +
		indDimStyle.Render("hist ") + histStyle.Render(fmtOrDash(hVal)) + "  " +
		badge

	barW := 1
	if chartStyle == "candlestick_dotted" {
		barW = 2
	}
	histStr := chart.RenderMACDHistogram(hist, chartWidth, barW)
	if histStr == "" {
		return header
	}
	return header + "\n" + histStr
}

// buildTimeAxis creates a time label row for the X-axis.
func buildTimeAxis(timestamps []int64, width int, timeframe string) string {
	if len(timestamps) == 0 {
		return ""
	}

	// Determine time format based on timeframe
	var timeFmt string
	switch timeframe {
	case "1D":
		timeFmt = "15:04"
	case "1W":
		timeFmt = "Mon 15:04"
	case "1M":
		timeFmt = "Jan 02"
	default: // 6M, 1Y, 3Y, 5Y
		timeFmt = "Jan '06"
	}

	// Number of labels that fit
	sampleLabel := time.Unix(timestamps[0], 0).Local().Format(timeFmt)
	labelWidth := len(sampleLabel) + 2 // label + padding
	numLabels := width / labelWidth
	if numLabels < 2 {
		numLabels = 2
	}
	if numLabels > 10 {
		numLabels = 10
	}

	// Build axis line
	axis := make([]byte, width)
	for i := range axis {
		axis[i] = ' '
	}

	for i := 0; i < numLabels; i++ {
		// Map label index to timestamp index
		tsIdx := int(float64(i) / float64(numLabels-1) * float64(len(timestamps)-1))
		if tsIdx >= len(timestamps) {
			tsIdx = len(timestamps) - 1
		}
		t := time.Unix(timestamps[tsIdx], 0).Local()
		label := t.Format(timeFmt)

		// Position in the axis
		pos := int(float64(i) / float64(numLabels-1) * float64(width-len(label)))
		if pos < 0 {
			pos = 0
		}
		if pos+len(label) > width {
			pos = width - len(label)
		}

		// Write label if it doesn't overlap previous
		for j := 0; j < len(label) && pos+j < width; j++ {
			axis[pos+j] = label[j]
		}
	}

	return string(axis)
}

// renderDetailInfo renders a compact info bar with fundamentals for the detail view.
func renderDetailInfo(q *yahoo.Quote, width int) string {
	if q == nil {
		return ""
	}
	sep := nameStyle.Render("  │  ")
	var parts []string

	if q.PE > 0 {
		parts = append(parts, nameStyle.Render("PE ")+priceStyle.Render(fmt.Sprintf("%.2f", q.PE)))
	}
	if q.ForwardPE > 0 {
		parts = append(parts, nameStyle.Render("Fwd PE ")+priceStyle.Render(fmt.Sprintf("%.2f", q.ForwardPE)))
	}
	if q.PEG > 0 {
		parts = append(parts, nameStyle.Render("PEG ")+priceStyle.Render(fmt.Sprintf("%.2f", q.PEG)))
	}
	if q.EPS != 0 {
		parts = append(parts, nameStyle.Render("EPS ")+priceStyle.Render(fmt.Sprintf("%.2f", q.EPS)))
	}
	if q.Beta > 0 {
		parts = append(parts, nameStyle.Render("Beta ")+priceStyle.Render(fmt.Sprintf("%.2f", q.Beta)))
	}
	if q.FiftyTwoWeekLow > 0 || q.FiftyTwoWeekHigh > 0 {
		parts = append(parts, nameStyle.Render("52w ")+
			priceStyle.Render(formatPriceNum(q.FiftyTwoWeekLow, q.Currency))+
			nameStyle.Render(" — ")+
			priceStyle.Render(formatPriceNum(q.FiftyTwoWeekHigh, q.Currency)))
	}
	if q.DividendYield >= 0 {
		parts = append(parts, nameStyle.Render("Yield ")+priceStyle.Render(fmt.Sprintf("%.2f%%", q.DividendYield*100)))
	}
	if q.MarketCap > 0 {
		parts = append(parts, nameStyle.Render("Mkt Cap ")+priceStyle.Render(formatMarketCap(q.MarketCap)))
	}

	if len(parts) == 0 {
		return ""
	}
	return "  " + strings.Join(parts, sep)
}

func renderMarketStatus(q *yahoo.Quote) string {
	if q == nil {
		return ""
	}

	var parts []string

	// Market state indicator
	switch q.MarketState {
	case "REGULAR":
		parts = append(parts, marketOpenStyle.Render("● MARKET OPEN"))
	case "PRE":
		parts = append(parts, helpKeyStyle.Render("● PRE-MARKET"))
	case "POST", "POSTPOST":
		parts = append(parts, helpKeyStyle.Render("● AFTER HOURS"))
	default:
		parts = append(parts, marketClosedStyle.Render("● MARKET CLOSED"))
	}

	if q.ExchangeTimezone != "" {
		parts = append(parts, nameStyle.Render(q.ExchangeTimezone))
	}

	sep := nameStyle.Render("  │  ")

	// Pre-market data
	if q.PreMarketPrice > 0 {
		chg := q.PreMarketChangePercent
		sign := "+"
		chgStyle := changeUpStyle
		if chg < 0 {
			sign = ""
			chgStyle = changeDownStyle
		}
		preStr := helpKeyStyle.Render("Pre: ") +
			priceStyle.Render(formatPriceNum(q.PreMarketPrice, q.Currency)) + " " +
			chgStyle.Render(fmt.Sprintf("%s%.2f%%", sign, chg))
		if q.PreMarketTime > 0 {
			t := time.Unix(q.PreMarketTime, 0)
			preStr += nameStyle.Render(fmt.Sprintf(" (%s)", t.Format("15:04")))
		}
		parts = append(parts, preStr)
	}

	// Post-market data
	if q.PostMarketPrice > 0 {
		chg := q.PostMarketChangePercent
		sign := "+"
		chgStyle := changeUpStyle
		if chg < 0 {
			sign = ""
			chgStyle = changeDownStyle
		}
		postStr := helpKeyStyle.Render("Post: ") +
			priceStyle.Render(formatPriceNum(q.PostMarketPrice, q.Currency)) + " " +
			chgStyle.Render(fmt.Sprintf("%s%.2f%%", sign, chg))
		if q.PostMarketTime > 0 {
			t := time.Unix(q.PostMarketTime, 0)
			postStr += nameStyle.Render(fmt.Sprintf(" (%s)", t.Format("15:04")))
		}
		parts = append(parts, postStr)
	}

	if len(parts) == 0 {
		return ""
	}
	return "  " + strings.Join(parts, sep)
}

func renderDetailChartContent(item WatchlistItem, data *yahoo.ChartData, w, h int, style string, positive bool, showMAOverlay bool, maMode MAMode, timeframe string) string {
	if data == nil {
		return ""
	}
	layout := oneDRenderLayout{}
	if timeframe == "1D" {
		layout = computeOneDRenderLayout(item)
	}
	shapeOverlay := func(values []float64) []float64 {
		if timeframe == "1D" {
			return applyOneDLayoutFloat64(values, layout)
		}
		return values
	}
	switch style {
	case "dotted_line":
		if len(data.Closes) == 0 {
			return ""
		}
		closes := data.Closes
		if showMAOverlay {
			overlays := buildMAOverlays(item, maMode)
			for i := range overlays {
				overlays[i].Values = shapeOverlay(overlays[i].Values)
			}
			if len(overlays) > 0 {
				return chart.RenderSparklineLineWithOverlays(closes, overlays, w, h, changeColor(positive))
			}
		}
		return chart.RenderSparklineLineWithOverlays(closes, nil, w, h, changeColor(positive))
	case "candlestick_dotted":
		if len(data.Opens) == 0 || len(data.Closes) == 0 {
			return ""
		}
		candles := buildCandles(data)
		if showMAOverlay {
			overlays := buildMAOverlays(item, maMode)
			for i := range overlays {
				overlays[i].Values = shapeOverlay(overlays[i].Values)
			}
			if len(overlays) > 0 {
				return chart.RenderCandlestickBrailleWithOverlays(candles, overlays, w, h, true)
			}
		}
		return chart.RenderCandlestickBraille(candles, w, h, true)
	default: // "candlestick"
		if len(data.Opens) == 0 || len(data.Closes) == 0 {
			return ""
		}
		candles := buildCandles(data)
		if showMAOverlay {
			overlays := buildMAOverlays(item, maMode)
			for i := range overlays {
				overlays[i].Values = shapeOverlay(overlays[i].Values)
			}
			if len(overlays) > 0 {
				return chart.RenderCandlestickWithOverlays(candles, overlays, w, h)
			}
		}
		return chart.RenderCandlestick(candles, w, h)
	}
}

// buildMAOverlays computes the EMA overlays for the given MA preset
// from the item's primary Closes (with indicator context if available)
// and returns them as colored line overlays aligned 1:1 with the
// primary bars.
func buildMAOverlays(item WatchlistItem, maMode MAMode) []chart.LineOverlay {
	if item.ChartData == nil || len(item.ChartData.Closes) < 10 {
		return nil
	}
	periods := maMode.EMAPeriods()
	if len(periods) == 0 {
		return nil
	}
	// Merge context → primary for warmup (same strategy as
	// ComputeTechnicalsWithContext). When ctx is present and strictly
	// older, prepend its closes so longer EMAs (50, 100, 200) stabilise
	// before the primary window begins.
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
	colors := []string{
		"\033[38;2;125;211;252m", // cyan — fastest
		"\033[38;2;251;191;36m",  // amber — middle
		"\033[38;2;192;132;252m", // violet — slowest
	}
	var out []chart.LineOverlay
	for i, p := range periods {
		vals := trimPrefix(chart.EMA(closes, p), offset)
		if len(vals) != len(primary.Closes) {
			continue
		}
		out = append(out, chart.LineOverlay{
			Values: vals,
			Color:  colors[i%len(colors)],
			Label:  fmt.Sprintf("EMA%d", p),
		})
	}
	return out
}

// macdWithContext computes MACD(fast,slow,signal) against the item's
// closes, prepending any available indicator context for warmup, then
// trims the result back to the primary window.
func macdWithContext(item WatchlistItem, fast, slow, signal int) (macd, sig, hist []float64) {
	if item.ChartData == nil {
		return nil, nil, nil
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
	m, s, h := chart.MACD(closes, fast, slow, signal)
	return trimPrefix(m, offset), trimPrefix(s, offset), trimPrefix(h, offset)
}

func buildCandles(cd *yahoo.ChartData) []chart.Candle {
	n := len(cd.Opens)
	if len(cd.Closes) < n {
		n = len(cd.Closes)
	}
	hasHL := len(cd.Highs) >= n && len(cd.Lows) >= n
	candles := make([]chart.Candle, n)
	for i := 0; i < n; i++ {
		c := chart.Candle{
			Open:  cd.Opens[i],
			Close: cd.Closes[i],
		}
		if hasHL {
			c.High = cd.Highs[i]
			c.Low = cd.Lows[i]
		} else {
			c.High = math.Max(c.Open, c.Close)
			c.Low = math.Min(c.Open, c.Close)
		}
		candles[i] = c
	}
	return candles
}

func displayName(item WatchlistItem, q *yahoo.Quote) string {
	if item.Name != "" {
		return item.Name
	}
	if q != nil && q.Name != "" {
		return q.Name
	}
	return item.Symbol
}

// isZeroDecimalCurrency returns true for currencies where fractional units
// are not meaningful for stock prices (e.g. JPY, KRW).
func isZeroDecimalCurrency(currency string) bool {
	switch currency {
	case "JPY", "KRW", "VND", "IDR", "TWD":
		return true
	}
	return false
}

// formatPriceNum formats a price number with commas, dropping decimals for
// zero-decimal currencies.
func formatPriceNum(price float64, currency string) string {
	if isZeroDecimalCurrency(currency) {
		return formatIntWithCommas(int64(math.Round(price)))
	}
	return formatFloat(price)
}

// formatIntWithCommas formats an integer with comma separators.
func formatIntWithCommas(n int64) string {
	negative := n < 0
	if negative {
		n = -n
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		if negative {
			return "-" + s
		}
		return s
	}
	var buf strings.Builder
	remainder := len(s) % 3
	if remainder > 0 {
		buf.WriteString(s[:remainder])
	}
	for i := remainder; i < len(s); i += 3 {
		if buf.Len() > 0 {
			buf.WriteByte(',')
		}
		buf.WriteString(s[i : i+3])
	}
	if negative {
		return "-" + buf.String()
	}
	return buf.String()
}

func formatPrice(price float64, currency string) string {
	sym := "$"
	switch currency {
	case "KRW":
		sym = "₩"
	case "JPY":
		sym = "¥"
	case "EUR":
		sym = "€"
	case "GBP", "GBp":
		sym = "£"
	case "CNY", "CNH":
		sym = "¥"
	case "HKD":
		sym = "HK$"
	case "TWD":
		sym = "NT$"
	case "INR":
		sym = "₹"
	case "AUD":
		sym = "A$"
	case "CAD":
		sym = "C$"
	}

	return sym + formatPriceNum(price, currency)
}

func formatMarketCap(cap int64) string {
	if cap <= 0 {
		return "—"
	}
	switch {
	case cap >= 1_000_000_000_000:
		return fmt.Sprintf("%.2fT", float64(cap)/1e12)
	case cap >= 1_000_000_000:
		return fmt.Sprintf("%.2fB", float64(cap)/1e9)
	case cap >= 1_000_000:
		return fmt.Sprintf("%.2fM", float64(cap)/1e6)
	default:
		return fmt.Sprintf("%d", cap)
	}
}

func formatVolume(vol int64) string {
	if vol <= 0 {
		return "—"
	}
	switch {
	case vol >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(vol)/1e9)
	case vol >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(vol)/1e6)
	case vol >= 1_000:
		return fmt.Sprintf("%.1fK", float64(vol)/1e3)
	default:
		return fmt.Sprintf("%d", vol)
	}
}

func formatFloat(f float64) string {
	s := fmt.Sprintf("%.2f", f)
	parts := strings.SplitN(s, ".", 2)
	intPart := parts[0]
	decPart := parts[1]

	negative := false
	if intPart[0] == '-' {
		negative = true
		intPart = intPart[1:]
	}

	// Insert commas from right
	n := len(intPart)
	if n <= 3 {
		if negative {
			return "-" + intPart + "." + decPart
		}
		return intPart + "." + decPart
	}

	var buf strings.Builder
	remainder := n % 3
	if remainder > 0 {
		buf.WriteString(intPart[:remainder])
	}
	for i := remainder; i < n; i += 3 {
		if buf.Len() > 0 {
			buf.WriteByte(',')
		}
		buf.WriteString(intPart[i : i+3])
	}

	result := buf.String() + "." + decPart
	if negative {
		return "-" + result
	}
	return result
}

func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return "…"
	}
	return string(runes[:maxLen-1]) + "…"
}
