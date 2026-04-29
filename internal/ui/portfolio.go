package ui

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/ray-x/finsight/internal/portfolio"
)

// ProfitMode selects which profit column is displayed in the portfolio
// view. It is toggled by the `%` key.
type ProfitMode int

const (
	ProfitPercent ProfitMode = iota
	ProfitTotal
)

// PortfolioItem represents one row in the portfolio view. It embeds the
// live WatchlistItem so quote/chart fetching can be shared.
type PortfolioItem struct {
	WatchlistItem
	Position  float64
	OpenPrice float64
	Note      string
}

// PortfolioMetrics summarises the whole portfolio.
type PortfolioMetrics struct {
	TotalMarketValue     float64
	TotalCostBasis       float64 // positions with known OpenPrice only
	TotalDailyPL         float64
	TotalDailyPLPct      float64
	TotalUnrealizedPL    float64 // across positions with known OpenPrice
	TotalUnrealizedPLPct float64
	Known                int // count of positions with known open price
	Count                int
}

// ComputePortfolioMetrics returns aggregate P/L across the items.
func ComputePortfolioMetrics(items []PortfolioItem) PortfolioMetrics {
	var m PortfolioMetrics
	var prevValue float64
	for _, it := range items {
		m.Count++
		if it.Quote == nil {
			continue
		}
		mv := it.Quote.Price * it.Position
		m.TotalMarketValue += mv
		prev := it.Quote.PreviousClose
		if prev == 0 {
			prev = it.Quote.Price - it.Quote.Change
		}
		if prev > 0 {
			prevValue += prev * it.Position
		}
		if it.OpenPrice > 0 {
			m.Known++
			m.TotalCostBasis += it.OpenPrice * it.Position
			m.TotalUnrealizedPL += (it.Quote.Price - it.OpenPrice) * it.Position
		}
	}
	if prevValue > 0 {
		m.TotalDailyPL = m.TotalMarketValue - prevValue
		m.TotalDailyPLPct = (m.TotalDailyPL / prevValue) * 100
	}
	if m.TotalCostBasis > 0 {
		m.TotalUnrealizedPLPct = (m.TotalUnrealizedPL / m.TotalCostBasis) * 100
	}
	return m
}

// portfolio column widths
const (
	colPos    = 10
	colOpen2  = 12
	colValue  = 14
	colProfit = 14
	colNote   = 16
)

// RenderPortfolio renders the portfolio table.
func RenderPortfolio(items []PortfolioItem, selected, width, _ int, chartStyle string, profitMode ProfitMode, timeframe string) string {
	if len(items) == 0 {
		return lipgloss.Place(width, 3,
			lipgloss.Center, lipgloss.Center,
			nameStyle.Render("No positions. Press / to search and add positions."))
	}

	chartW := calcPortfolioChartWidth(width)
	vis := calcPortfolioCols(width, chartW)

	header := renderPortfolioHeader(width, chartW, vis, profitMode)
	sep := separatorStyle.Render(strings.Repeat("─", width-2))

	rows := []string{header, sep}
	for i, it := range items {
		rows = append(rows, renderPortfolioRow(it, i == selected, width, chartW, chartStyle, vis, profitMode, timeframe))
	}
	// Footer with totals
	metrics := ComputePortfolioMetrics(items)
	rows = append(rows, sep, renderPortfolioFooter(metrics, width, profitMode))
	return strings.Join(rows, "\n")
}

type portfolioColVis struct {
	open, change, value, profit, note bool
}

func calcPortfolioChartWidth(total int) int {
	fixed := colSymbol + colPos + colPrice + colChangePct + 4
	w := total - fixed - colOpen2 - colChange - colValue - colProfit - colNote
	if w < 6 {
		w = 6
	}
	if w > 24 {
		w = 24
	}
	return w
}

func calcPortfolioCols(width, chartW int) portfolioColVis {
	// Mandatory: symbol + position + trend + last + change%
	used := colSymbol + colPos + chartW + colPrice + colChangePct
	rem := width - used
	v := portfolioColVis{}
	if rem >= colValue {
		v.value = true
		rem -= colValue
	}
	if rem >= colProfit {
		v.profit = true
		rem -= colProfit
	}
	if rem >= colOpen2 {
		v.open = true
		rem -= colOpen2
	}
	if rem >= colChange {
		v.change = true
		rem -= colChange
	}
	if rem >= colNote {
		v.note = true
	}
	return v
}

func renderPortfolioHeader(width, chartW int, vis portfolioColVis, mode ProfitMode) string {
	profitLabel := "P/L %"
	if mode == ProfitTotal {
		profitLabel = "P/L $"
	}
	parts := []string{
		tableHeaderStyle.Width(colSymbol).Render("Symbol"),
		tableHeaderStyle.Width(colPos).Align(lipgloss.Right).Render("Position"),
		tableHeaderStyle.Width(chartW).Render("Trend"),
		tableHeaderStyle.Width(colPrice).Align(lipgloss.Right).Render("Last"),
	}
	if vis.open {
		parts = append(parts, tableHeaderStyle.Width(colOpen2).Align(lipgloss.Right).Render("Open"))
	}
	if vis.value {
		parts = append(parts, tableHeaderStyle.Width(colValue).Align(lipgloss.Right).Render("Mkt Value"))
	}
	if vis.change {
		parts = append(parts, tableHeaderStyle.Width(colChange).Align(lipgloss.Right).Render("Day Δ"))
	}
	parts = append(parts, tableHeaderStyle.Width(colChangePct).Align(lipgloss.Right).Render("Day %"))
	if vis.profit {
		parts = append(parts, tableHeaderStyle.Width(colProfit).Align(lipgloss.Right).Render(profitLabel))
	}
	if vis.note {
		parts = append(parts, tableHeaderStyle.Width(colNote).Render("Note"))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

func renderPortfolioRow(it PortfolioItem, selected bool, width, chartW int, chartStyle string, vis portfolioColVis, mode ProfitMode, timeframe string) string {
	applyRowStyle := func(line string) string {
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
		symbolStyle.Render(it.Symbol) + "\n" + nameStyle.Render(truncate(it.Name, colSymbol-2)))
	posCol := cellStyle.Width(colPos).Align(lipgloss.Right).Render(nameStyle.Render(formatPosition(it.Position)))

	if it.Loading {
		return applyRowStyle(lipgloss.JoinHorizontal(lipgloss.Top, symCol, posCol, nameStyle.Render("  Loading...")))
	}
	if it.Error != "" {
		return applyRowStyle(lipgloss.JoinHorizontal(lipgloss.Top, symCol, posCol, errorStyle.Render("  "+truncate(it.Error, 40))))
	}
	q := it.Quote
	if q == nil {
		return applyRowStyle(lipgloss.JoinHorizontal(lipgloss.Top, symCol, posCol))
	}

	positive := q.ChangePercent >= 0
	chartStr := renderInlineChart(it.WatchlistItem, chartW-2, 2, chartStyle, positive, timeframe)
	chartCol := cellStyle.Width(chartW).Render(chartStr)

	priceCol := cellStyle.Width(colPrice).Align(lipgloss.Right).Render(priceStyle.Render(formatPrice(q.Price, q.Currency)))

	parts := []string{symCol, posCol, chartCol, priceCol}

	if vis.open {
		openStr := "—"
		if it.OpenPrice > 0 {
			openStr = formatPriceNum(it.OpenPrice, q.Currency)
		}
		parts = append(parts, cellStyle.Width(colOpen2).Align(lipgloss.Right).Render(nameStyle.Render(openStr)))
	}
	if vis.value {
		mv := q.Price * it.Position
		parts = append(parts, cellStyle.Width(colValue).Align(lipgloss.Right).Render(priceStyle.Render(formatPrice(mv, q.Currency))))
	}
	if vis.change {
		dayDelta := q.Change * it.Position
		var s string
		if isZeroDecimalCurrency(q.Currency) {
			s = fmt.Sprintf("%s%s", signPrefix(dayDelta), formatIntWithCommas(int64(math.Round(dayDelta))))
		} else {
			s = fmt.Sprintf("%+.2f", dayDelta)
		}
		parts = append(parts, cellStyle.Width(colChange).Align(lipgloss.Right).Render(changeStyle(dayDelta >= 0).Render(s)))
	}
	pctStr := fmt.Sprintf("%+.2f%%", q.ChangePercent)
	parts = append(parts, cellStyle.Width(colChangePct).Align(lipgloss.Right).Render(changeStyle(positive).Render(pctStr)))

	if vis.profit {
		parts = append(parts, cellStyle.Width(colProfit).Align(lipgloss.Right).Render(formatProfitCell(it, q.Currency, mode)))
	}
	if vis.note {
		note := truncate(it.Note, colNote-1)
		parts = append(parts, cellStyle.Width(colNote).Render(nameStyle.Render(note)))
	}

	return applyRowStyle(lipgloss.JoinHorizontal(lipgloss.Top, parts...))
}

func renderPortfolioFooter(m PortfolioMetrics, width int, mode ProfitMode) string {
	label := nameStyle.Render(fmt.Sprintf(" %d position(s)", m.Count))
	dayStr := fmt.Sprintf("Day: %s (%+.2f%%)", signFmt(m.TotalDailyPL, 2), m.TotalDailyPLPct)
	day := changeStyle(m.TotalDailyPL >= 0).Render(dayStr)

	var profit string
	if m.Known > 0 {
		if mode == ProfitTotal {
			profit = changeStyle(m.TotalUnrealizedPL >= 0).Render(
				fmt.Sprintf("P/L: %s (%+.2f%%)", signFmt(m.TotalUnrealizedPL, 2), m.TotalUnrealizedPLPct))
		} else {
			profit = changeStyle(m.TotalUnrealizedPL >= 0).Render(
				fmt.Sprintf("P/L: %+.2f%% (%s)", m.TotalUnrealizedPLPct, signFmt(m.TotalUnrealizedPL, 2)))
		}
	}
	mv := priceStyle.Render(fmt.Sprintf("Mkt Value: $%s", formatFloatWithCommas(m.TotalMarketValue)))

	segments := []string{label, mv, day}
	if profit != "" {
		segments = append(segments, profit)
	}
	return " " + strings.Join(segments, "   ")
}

func formatProfitCell(it PortfolioItem, currency string, mode ProfitMode) string {
	if it.OpenPrice <= 0 || it.Quote == nil {
		return nameStyle.Render("—")
	}
	pnl := (it.Quote.Price - it.OpenPrice) * it.Position
	pct := ((it.Quote.Price / it.OpenPrice) - 1) * 100
	switch mode {
	case ProfitTotal:
		var s string
		if isZeroDecimalCurrency(currency) {
			s = fmt.Sprintf("%s%s", signPrefix(pnl), formatIntWithCommas(int64(math.Round(pnl))))
		} else {
			s = fmt.Sprintf("%+.2f", pnl)
		}
		return changeStyle(pnl >= 0).Render(s)
	default:
		return changeStyle(pct >= 0).Render(fmt.Sprintf("%+.2f%%", pct))
	}
}

func formatPosition(p float64) string {
	if p == math.Trunc(p) {
		return formatIntWithCommas(int64(p))
	}
	return fmt.Sprintf("%.4f", p)
}

func signPrefix(v float64) string {
	if v >= 0 {
		return "+"
	}
	return ""
}

func signFmt(v float64, decimals int) string {
	return fmt.Sprintf("%+.*f", decimals, v)
}

func formatFloatWithCommas(v float64) string {
	neg := v < 0
	if neg {
		v = -v
	}
	whole := int64(v)
	frac := v - float64(whole)
	s := formatIntWithCommas(whole)
	cents := int(math.Round(frac * 100))
	if cents == 100 {
		cents = 0
		// handle rounding that carries; add 1 to whole
		s = formatIntWithCommas(whole + 1)
	}
	out := fmt.Sprintf("%s.%02d", s, cents)
	if neg {
		out = "-" + out
	}
	return out
}

// BuildPortfolioItems converts positions + in-memory watchlist items to
// PortfolioItem rows (reusing quote/chart data when available).
func BuildPortfolioItems(positions []portfolio.Position, cache map[string]WatchlistItem) []PortfolioItem {
	out := make([]PortfolioItem, len(positions))
	for i, p := range positions {
		wi := WatchlistItem{Symbol: p.Symbol, Name: p.Symbol, Loading: true}
		if cached, ok := cache[p.Symbol]; ok {
			wi = cached
			if wi.Symbol == "" {
				wi.Symbol = p.Symbol
			}
			if wi.Name == "" {
				wi.Name = p.Symbol
			}
		}
		out[i] = PortfolioItem{
			WatchlistItem: wi,
			Position:      p.Position,
			OpenPrice:     p.OpenPrice,
			Note:          p.Note,
		}
	}
	return out
}
