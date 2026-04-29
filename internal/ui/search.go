package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/ray-x/finsight/internal/chart"
	"github.com/ray-x/finsight/internal/yahoo"
)

// SearchSection indicates which section the cursor is in
type SearchSection int

const (
	SectionSuggestions SearchSection = iota
	SectionWatchlist
)

// SearchView renders the search overlay.
type SearchView struct {
	Query    string
	Results  []yahoo.SearchResult
	Selected int
	Loading  bool
	Error    string

	// Watchlist matches
	WatchlistMatches []WatchlistItem
	Section          SearchSection
	WatchSelected    int
}

func RenderSearch(sv SearchView, width int) string {
	// Search input box
	prompt := helpKeyStyle.Render("Search: ")
	cursor := "█"
	input := fmt.Sprintf("%s%s%s", prompt, sv.Query, cursor)
	inputBox := searchBoxStyle.Width(width - 4).Render(input)

	var parts []string
	parts = append(parts, inputBox)

	// Section 1: Yahoo search suggestions
	var suggestContent string
	if sv.Loading {
		suggestContent = nameStyle.Render("  Searching...")
	} else if sv.Error != "" {
		suggestContent = errorStyle.Render("  " + sv.Error)
	} else if len(sv.Results) > 0 {
		suggestHeader := helpKeyStyle.Render("  Suggestions")
		suggestSep := separatorStyle.Render("  " + strings.Repeat("─", width-8))
		var rows []string
		for i, r := range sv.Results {
			selected := sv.Section == SectionSuggestions && i == sv.Selected
			row := renderSearchResult(r, selected, width-6)
			rows = append(rows, row)
		}
		suggestContent = suggestHeader + "\n" + suggestSep + "\n" + strings.Join(rows, "\n")
	} else if sv.Query != "" {
		suggestContent = nameStyle.Render("  No results found")
	} else {
		suggestContent = nameStyle.Render("  Type to search for stocks, indices, ETFs...")
	}
	parts = append(parts, suggestContent)

	// Section 2: Watchlist matches
	if len(sv.WatchlistMatches) > 0 {
		watchHeader := helpKeyStyle.Render("  Watchlist")
		watchSep := separatorStyle.Render("  " + strings.Repeat("─", width-8))
		var rows []string
		for i, item := range sv.WatchlistMatches {
			selected := sv.Section == SectionWatchlist && i == sv.WatchSelected
			row := renderWatchlistMatch(item, selected, width-6)
			rows = append(rows, row)
		}
		separator := separatorStyle.Render(strings.Repeat("─", width-6))
		watchContent := separator + "\n" + watchHeader + "\n" + watchSep + "\n" + strings.Join(rows, "\n")
		parts = append(parts, watchContent)
	}

	help := helpStyle.Render(
		helpKeyStyle.Render("Enter") + " add/select  " +
			helpKeyStyle.Render("Tab") + " switch section  " +
			helpKeyStyle.Render("Esc") + " cancel  " +
			helpKeyStyle.Render("↑↓") + " navigate")

	parts = append(parts, "", help)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func renderSearchResult(r yahoo.SearchResult, selected bool, width int) string {
	style := searchResultStyle
	if selected {
		style = selectedResultStyle
	}

	symbol := symbolStyle.Render(fmt.Sprintf("%-12s", r.Symbol))
	name := truncate(r.Name, 30)
	typeStr := nameStyle.Render(fmt.Sprintf("[%s]", r.Type))
	exchange := nameStyle.Render(r.Exchange)

	line := fmt.Sprintf("%s %s  %s  %s", symbol, name, typeStr, exchange)
	return style.Width(width).Render(line)
}

func renderWatchlistMatch(item WatchlistItem, selected bool, width int) string {
	style := searchResultStyle
	if selected {
		style = selectedResultStyle
	}

	symbol := symbolStyle.Render(fmt.Sprintf("%-12s", item.Symbol))
	name := truncate(item.Name, 20)

	// Trend sparkline — per-bar coloring: green for up, red for down
	trendWidth := 12
	trendStr := ""
	if item.ChartData != nil && len(item.ChartData.Closes) > 0 {
		bars := chart.SparklineBars(item.ChartData.Closes, trendWidth)
		var sb strings.Builder
		for _, b := range bars {
			if b.Up {
				sb.WriteString(changeUpStyle.Render(string(b.Char)))
			} else {
				sb.WriteString(changeDownStyle.Render(string(b.Char)))
			}
		}
		trendStr = sb.String()
	}

	priceStr := ""
	changeStr := ""
	if item.Quote != nil {
		priceStr = priceStyle.Render(formatFloat(item.Quote.Price))
		chg := item.Quote.ChangePercent
		sign := "+"
		chgStyle := changeUpStyle
		if chg < 0 {
			sign = ""
			chgStyle = changeDownStyle
		}
		changeStr = chgStyle.Render(fmt.Sprintf("%s%.2f%%", sign, chg))
	}

	line := fmt.Sprintf("%s %s  %s  %s  %s", symbol, name, trendStr, priceStr, changeStr)
	return style.Width(width).Render(line)
}
