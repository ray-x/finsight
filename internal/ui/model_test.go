package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ray-x/finsight/internal/cache"
	"github.com/ray-x/finsight/internal/config"
	"github.com/ray-x/finsight/internal/yahoo"
)

// newTestModel creates a minimal Model for testing without network deps.
func newTestModel() Model {
	cfg := &config.Config{
		RefreshInterval: 900,
		ChartStyle:      "candlestick_dotted",
		Watchlists: []config.WatchlistGroup{
			{Name: "Default", Symbols: []config.WatchItem{
				{Symbol: "AAPL", Name: "Apple Inc."},
				{Symbol: "MSFT", Name: "Microsoft"},
				{Symbol: "GOOG", Name: "Alphabet"},
			}},
		},
	}
	items := make([]WatchlistItem, len(cfg.Watchlists[0].Symbols))
	for i, w := range cfg.Watchlists[0].Symbols {
		items[i] = WatchlistItem{Symbol: w.Symbol, Name: w.Name}
	}
	return Model{
		cfg:          cfg,
		items:        items,
		mode:         viewWatchlist,
		width:        120,
		height:       40,
		relatedFocus: -1,
	}
}

func keyMsg(k string) tea.KeyMsg {
	switch k {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEscape}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
}

func asModel(m tea.Model) Model {
	return m.(Model)
}

// === View mode tests ===

func TestOverlayPopupAlignment(t *testing.T) {
	m := newTestModel()
	m.width = 80
	m.height = 20

	// Build a base with some content lines followed by empty padding lines.
	// This simulates the real scenario where the watchlist has fewer lines
	// than the terminal height.
	var baseLines []string
	for i := 0; i < 5; i++ {
		baseLines = append(baseLines, strings.Repeat("X", 80))
	}
	for i := 5; i < 20; i++ {
		baseLines = append(baseLines, "") // empty padding
	}
	base := strings.Join(baseLines, "\n")

	// Simple popup content (no ANSI codes for clarity)
	popup := strings.Repeat("A", 40) + "\n" +
		strings.Repeat("B", 40) + "\n" +
		strings.Repeat("C", 40)

	result := m.overlayPopup(base, popup)
	lines := strings.Split(result, "\n")

	// The popup is 46 chars wide (40 content + 6 for border/padding from lipgloss).
	// Find popup lines by looking for the border character.
	var popupStartCols []int
	for _, line := range lines {
		// Find the first non-space, non-X character position
		idx := strings.IndexAny(line, "╭╰│")
		if idx >= 0 {
			// Count visible chars before the border char
			prefix := line[:idx]
			w := 0
			for _, r := range prefix {
				if r != ' ' && r != 'X' {
					break
				}
				w++
			}
			popupStartCols = append(popupStartCols, w)
		}
	}

	if len(popupStartCols) == 0 {
		t.Fatal("no popup lines found in overlay result")
	}

	// All popup lines must start at the same column (consistent alignment)
	first := popupStartCols[0]
	for i, col := range popupStartCols {
		if col != first {
			t.Errorf("popup line %d starts at col %d, expected %d (misaligned!)", i, col, first)
		}
	}
}

func TestInitialState(t *testing.T) {
	m := newTestModel()
	if m.mode != viewWatchlist {
		t.Errorf("expected viewWatchlist, got %d", m.mode)
	}
	if m.selected != 0 {
		t.Errorf("expected selected=0, got %d", m.selected)
	}
	if len(m.items) != 3 {
		t.Errorf("expected 3 items, got %d", len(m.items))
	}
}

func TestHeatmapWatchlistQuotesRebuildAddsMissingSymbols(t *testing.T) {
	c, err := cache.New(t.TempDir())
	if err != nil {
		t.Fatalf("new cache: %v", err)
	}

	m := newTestModel()
	m.cfg.Watchlists = []config.WatchlistGroup{
		{Name: "Active", Symbols: []config.WatchItem{{Symbol: "AAPL", Name: "Apple"}}},
		{Name: "Other", Symbols: []config.WatchItem{{Symbol: "MSFT", Name: "Microsoft"}}},
	}
	m.items = []WatchlistItem{{
		Symbol: "AAPL",
		Name:   "Apple",
		Quote:  &yahoo.Quote{Symbol: "AAPL", Price: 100, PreviousClose: 95, MarketCap: 1000},
	}}
	m.cache = c
	m.heatmap = NewHeatmapModel()
	m.heatmap.BuildWatchlistHeatmap(m.allWatchlistItems())

	nm, _ := m.handleHeatmapWatchlistQuotes(heatmapWatchlistQuotesMsg{
		quotes: []yahoo.Quote{{Symbol: "MSFT", Price: 200, PreviousClose: 190, MarketCap: 2000}},
	})
	m = asModel(nm)

	if len(m.heatmap.Items) != 2 {
		t.Fatalf("expected 2 heatmap items after rebuild, got %d", len(m.heatmap.Items))
	}
	found := false
	for _, it := range m.heatmap.Items {
		if it.Label == "MSFT" && it.Quote != nil && it.Quote.Symbol == "MSFT" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected fetched inactive-group symbol to be added to heatmap")
	}
}

func TestAllWatchlistItemsWithQuotesPrefersFreshQuotesOverStaleCache(t *testing.T) {
	c, err := cache.New(t.TempDir())
	if err != nil {
		t.Fatalf("new cache: %v", err)
	}
	c.PutQuotes([]yahoo.Quote{{Symbol: "MSFT", Price: 150, PreviousClose: 145, MarketCap: 1000}})

	m := newTestModel()
	m.cfg.Watchlists = []config.WatchlistGroup{
		{Name: "Active", Symbols: []config.WatchItem{{Symbol: "AAPL", Name: "Apple"}}},
		{Name: "Other", Symbols: []config.WatchItem{{Symbol: "MSFT", Name: "Microsoft"}}},
	}
	m.items = []WatchlistItem{{
		Symbol: "AAPL",
		Name:   "Apple",
		Quote:  &yahoo.Quote{Symbol: "AAPL", Price: 100, PreviousClose: 95, MarketCap: 2000},
	}}
	m.cache = c

	items := m.allWatchlistItemsWithQuotes(map[string]yahoo.Quote{
		"MSFT": {Symbol: "MSFT", Price: 300, PreviousClose: 290, MarketCap: 2500},
	})

	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	for _, it := range items {
		if it.Symbol != "MSFT" {
			continue
		}
		if it.Quote == nil {
			t.Fatal("expected MSFT quote to be populated")
		}
		if it.Quote.Price != 300 {
			t.Fatalf("expected fresh quote to win over stale cache, got %.2f", it.Quote.Price)
		}
		return
	}

	t.Fatal("expected MSFT item in merged watchlist")
}

func TestQuitKey(t *testing.T) {
	m := newTestModel()
	updated, cmd := m.Update(keyMsg("ctrl+c"))
	model := asModel(updated)
	if !model.quitting {
		t.Error("expected quitting=true after ctrl+c")
	}
	if cmd == nil {
		t.Error("expected tea.Quit command")
	}
}

func TestHelpToggle(t *testing.T) {
	m := newTestModel()

	// Enter help
	updated, _ := m.Update(keyMsg("?"))
	model := asModel(updated)
	if model.mode != viewHelp {
		t.Errorf("expected viewHelp, got %d", model.mode)
	}
	if model.prevMode != viewWatchlist {
		t.Errorf("expected prevMode=viewWatchlist, got %d", model.prevMode)
	}

	// Any key returns from help
	updated, _ = model.Update(keyMsg("esc"))
	model = asModel(updated)
	if model.mode != viewWatchlist {
		t.Errorf("expected viewWatchlist after help dismiss, got %d", model.mode)
	}
}

// === Watchlist key handling ===

func TestWatchlistNavigation(t *testing.T) {
	m := newTestModel()

	// Down moves selection
	updated, _ := m.Update(keyMsg("down"))
	model := asModel(updated)
	if model.selected != 1 {
		t.Errorf("expected selected=1, got %d", model.selected)
	}

	// Down again
	updated, _ = model.Update(keyMsg("down"))
	model = asModel(updated)
	if model.selected != 2 {
		t.Errorf("expected selected=2, got %d", model.selected)
	}

	// Down past end stays at last
	updated, _ = model.Update(keyMsg("down"))
	model = asModel(updated)
	if model.selected != 2 {
		t.Errorf("expected selected=2 (clamped), got %d", model.selected)
	}

	// Up moves back
	updated, _ = model.Update(keyMsg("up"))
	model = asModel(updated)
	if model.selected != 1 {
		t.Errorf("expected selected=1, got %d", model.selected)
	}

	// Up past start stays at 0
	updated, _ = model.Update(keyMsg("up"))
	model = asModel(updated)
	updated, _ = model.Update(keyMsg("up"))
	model = asModel(updated)
	if model.selected != 0 {
		t.Errorf("expected selected=0 (clamped), got %d", model.selected)
	}
}

func TestWatchlistSortCycle(t *testing.T) {
	m := newTestModel()
	if m.sortMode != SortDefault {
		t.Errorf("expected SortDefault, got %d", m.sortMode)
	}

	updated, _ := m.Update(keyMsg("s"))
	model := asModel(updated)
	if model.sortMode != SortBySymbol {
		t.Errorf("expected SortBySymbol, got %d", model.sortMode)
	}

	// Press s repeatedly to cycle
	for i := 0; i < int(sortModeCount)-1; i++ {
		updated, _ = model.Update(keyMsg("s"))
		model = asModel(updated)
	}
	if model.sortMode != SortDefault {
		t.Errorf("expected sort to wrap back to SortDefault, got %d", model.sortMode)
	}
}

func TestHandleQuotesTargetsRequestedSymbols(t *testing.T) {
	m := newTestModel()
	m.items[0].Quote = &yahoo.Quote{Symbol: "AAPL", Price: 100}
	m.items[1].Quote = &yahoo.Quote{Symbol: "MSFT", Price: 200}

	updated, _ := m.handleQuotes(quotesMsg{
		quotes:  []yahoo.Quote{{Symbol: "AAPL", Price: 111}},
		symbols: []string{"AAPL"},
	})
	model := asModel(updated)

	if model.items[0].Quote == nil || model.items[0].Quote.Price != 111 {
		t.Fatalf("expected AAPL quote to update, got %+v", model.items[0].Quote)
	}
	if model.items[1].Quote == nil || model.items[1].Quote.Price != 200 {
		t.Fatalf("expected MSFT quote to remain unchanged, got %+v", model.items[1].Quote)
	}
}

func TestRefreshCurrentSymbolDoesNotInvalidateCache(t *testing.T) {
	m := newTestModel()
	c, err := cache.New(t.TempDir())
	if err != nil {
		t.Fatalf("new cache: %v", err)
	}
	m.cache = c
	m.selected = 1
	c.PutChart("MSFT", "5d", "5m", &yahoo.ChartData{Closes: []float64{1, 2, 3}})

	_, cmd := m.refreshCurrentSymbol()
	if cmd == nil {
		t.Fatal("expected refresh command")
	}
	if got := c.GetChartStale("MSFT", "5d", "5m"); got == nil {
		t.Fatal("expected cached chart to remain after current refresh trigger")
	}
}

func TestRefreshAllSymbolsDoesNotInvalidateCache(t *testing.T) {
	m := newTestModel()
	c, err := cache.New(t.TempDir())
	if err != nil {
		t.Fatalf("new cache: %v", err)
	}
	m.cache = c
	c.PutChart("AAPL", "5d", "5m", &yahoo.ChartData{Closes: []float64{1, 2, 3}})
	c.PutChart("MSFT", "5d", "5m", &yahoo.ChartData{Closes: []float64{4, 5, 6}})

	_, cmd := m.refreshAllSymbols()
	if cmd == nil {
		t.Fatal("expected refresh command")
	}
	if got := c.GetChartStale("AAPL", "5d", "5m"); got == nil {
		t.Fatal("expected AAPL cached chart to remain after all refresh trigger")
	}
	if got := c.GetChartStale("MSFT", "5d", "5m"); got == nil {
		t.Fatal("expected MSFT cached chart to remain after all refresh trigger")
	}
}

func TestShouldUseEDGARForEarnings(t *testing.T) {
	m := newTestModel()

	m.cfg.Earnings.SourcePriority = []string{"company_ir", "edgar", "yahoo"}
	if !m.shouldUseEDGARForEarnings() {
		t.Fatalf("expected EDGAR enabled when present in source_priority")
	}

	m.cfg.Earnings.SourcePriority = []string{"yahoo", "edgar"}
	if m.shouldUseEDGARForEarnings() {
		t.Fatalf("expected EDGAR disabled when yahoo is the primary source")
	}

	m.cfg.Earnings.SourcePriority = []string{"company_ir", "yahoo"}
	if m.shouldUseEDGARForEarnings() {
		t.Fatalf("expected EDGAR disabled when not listed in source_priority")
	}
}

func TestIsWithinEarningsHighFreqWindow(t *testing.T) {
	m := newTestModel()
	m.cfg.Earnings.WindowBeforeMinutes = 120
	m.cfg.Earnings.WindowAfterMinutes = 120

	today := time.Now().Local().Format("2006-01-02")
	item := WatchlistItem{Financials: &yahoo.FinancialData{NextEarningsDate: today}}

	now := time.Now().Local().Add(-30 * time.Minute)
	if !m.isWithinEarningsHighFreqWindow(item, now) {
		t.Fatalf("expected to be inside high-frequency window for today's earnings date")
	}

	farFuture := WatchlistItem{Financials: &yahoo.FinancialData{NextEarningsDate: time.Now().AddDate(0, 0, 10).Format("2006-01-02")}}
	if m.isWithinEarningsHighFreqWindow(farFuture, time.Now()) {
		t.Fatalf("expected to be outside high-frequency window for far-future earnings date")
	}
}

func TestWatchlistSearchMode(t *testing.T) {
	m := newTestModel()

	updated, _ := m.Update(keyMsg("/"))
	model := asModel(updated)
	if model.mode != viewSearch {
		t.Errorf("expected viewSearch, got %d", model.mode)
	}

	// Esc returns to watchlist
	updated, _ = model.Update(keyMsg("esc"))
	model = asModel(updated)
	if model.mode != viewWatchlist {
		t.Errorf("expected viewWatchlist after esc, got %d", model.mode)
	}
}

func TestSummaryPopupRefreshLowercaseR(t *testing.T) {
	m := newTestModel()
	m.showSummary = true
	m.summaryText = "stale"
	m.summaryLoading = false

	updated, _ := m.Update(keyMsg("r"))
	model := asModel(updated)

	if !model.showSummary {
		t.Fatal("expected summary popup to remain open")
	}
	if !model.summaryLoading {
		t.Fatal("expected summary refresh to start on lowercase r")
	}
	if model.summaryText != "" {
		t.Fatalf("expected summary text to be cleared, got %q", model.summaryText)
	}
}

func TestAIPopupRefreshLowercaseR(t *testing.T) {
	m := newTestModel()
	m.mode = viewDetail
	m.showAI = true
	m.aiText = "stale"
	m.aiLoading = false

	updated, _ := m.Update(keyMsg("r"))
	model := asModel(updated)

	if !model.showAI {
		t.Fatal("expected AI popup to remain open")
	}
	if !model.aiLoading {
		t.Fatal("expected AI refresh to start on lowercase r")
	}
	if model.aiText != "" {
		t.Fatalf("expected AI text to be cleared, got %q", model.aiText)
	}
}

func TestEarningsPopupRefreshLowercaseR(t *testing.T) {
	m := newTestModel()
	m.mode = viewDetail
	m.showEarnings = true
	m.earningsText = "stale"
	m.earningsLoading = false

	updated, _ := m.Update(keyMsg("r"))
	model := asModel(updated)

	if !model.showEarnings {
		t.Fatal("expected earnings popup to remain open")
	}
	if !model.earningsLoading {
		t.Fatal("expected earnings refresh to start on lowercase r")
	}
	if model.earningsText != "" {
		t.Fatalf("expected earnings text to be cleared, got %q", model.earningsText)
	}
}

func TestWatchlistDeleteItem(t *testing.T) {
	m := newTestModel()
	m.selected = 1 // MSFT

	updated, _ := m.Update(keyMsg("d"))
	model := asModel(updated)
	if len(model.items) != 2 {
		t.Errorf("expected 2 items after delete, got %d", len(model.items))
	}
	// MSFT should be gone
	for _, item := range model.items {
		if item.Symbol == "MSFT" {
			t.Error("MSFT should have been deleted")
		}
	}
}

func TestWatchlistQuitOnEsc(t *testing.T) {
	m := newTestModel()

	updated, cmd := m.Update(keyMsg("esc"))
	model := asModel(updated)
	if !model.quitting {
		t.Error("expected quitting=true after esc in watchlist")
	}
	if cmd == nil {
		t.Error("expected tea.Quit command")
	}
}

// === Detail view key handling ===

func TestDetailEscLayered(t *testing.T) {
	m := newTestModel()
	m.mode = viewDetail
	m.relatedSymbols = []RelatedSymbol{
		{Symbol: "NVDA"},
		{Symbol: "TSLA"},
	}

	// Set focus and compare
	m.relatedFocus = 0
	m.compareSymbol = &RelatedSymbol{Symbol: "NVDA"}

	// First Esc: clears compare
	updated, _ := m.Update(keyMsg("esc"))
	model := asModel(updated)
	if model.compareSymbol != nil {
		t.Error("expected compareSymbol=nil after first esc")
	}
	if model.relatedFocus != 0 {
		t.Error("focus should still be active after clearing compare")
	}
	if model.mode != viewDetail {
		t.Error("should still be in detail view")
	}

	// Second Esc: clears focus
	updated, _ = model.Update(keyMsg("esc"))
	model = asModel(updated)
	if model.relatedFocus != -1 {
		t.Errorf("expected relatedFocus=-1, got %d", model.relatedFocus)
	}
	if model.mode != viewDetail {
		t.Error("should still be in detail view")
	}

	// Third Esc: back to watchlist
	updated, _ = model.Update(keyMsg("esc"))
	model = asModel(updated)
	if model.mode != viewWatchlist {
		t.Errorf("expected viewWatchlist, got %d", model.mode)
	}
}

func TestDetailTabFocusCycle(t *testing.T) {
	m := newTestModel()
	m.mode = viewDetail
	m.relatedSymbols = []RelatedSymbol{
		{Symbol: "NVDA"},
		{Symbol: "TSLA"},
		{Symbol: "AMZN"},
	}

	// Tab from -1 goes to 0
	updated, _ := m.Update(keyMsg("tab"))
	model := asModel(updated)
	if model.relatedFocus != 0 {
		t.Errorf("expected relatedFocus=0, got %d", model.relatedFocus)
	}

	// Tab cycles forward
	updated, _ = model.Update(keyMsg("tab"))
	model = asModel(updated)
	if model.relatedFocus != 1 {
		t.Errorf("expected relatedFocus=1, got %d", model.relatedFocus)
	}

	updated, _ = model.Update(keyMsg("tab"))
	model = asModel(updated)
	if model.relatedFocus != 2 {
		t.Errorf("expected relatedFocus=2, got %d", model.relatedFocus)
	}

	// Tab wraps to 0
	updated, _ = model.Update(keyMsg("tab"))
	model = asModel(updated)
	if model.relatedFocus != 0 {
		t.Errorf("expected relatedFocus=0 (wrapped), got %d", model.relatedFocus)
	}
}

func TestDetailShiftTabFocusCycle(t *testing.T) {
	m := newTestModel()
	m.mode = viewDetail
	m.relatedSymbols = []RelatedSymbol{
		{Symbol: "NVDA"},
		{Symbol: "TSLA"},
	}

	// Shift+Tab from -1 goes to last
	updated, _ := m.Update(keyMsg("shift+tab"))
	model := asModel(updated)
	if model.relatedFocus != 1 {
		t.Errorf("expected relatedFocus=1, got %d", model.relatedFocus)
	}

	// Shift+Tab goes backwards
	updated, _ = model.Update(keyMsg("shift+tab"))
	model = asModel(updated)
	if model.relatedFocus != 0 {
		t.Errorf("expected relatedFocus=0, got %d", model.relatedFocus)
	}

	// Shift+Tab wraps to last
	updated, _ = model.Update(keyMsg("shift+tab"))
	model = asModel(updated)
	if model.relatedFocus != 1 {
		t.Errorf("expected relatedFocus=1 (wrapped), got %d", model.relatedFocus)
	}
}

func TestDetailEnterToggleCompare(t *testing.T) {
	m := newTestModel()
	m.mode = viewDetail
	m.relatedSymbols = []RelatedSymbol{
		{Symbol: "NVDA", Quote: &yahoo.Quote{Symbol: "NVDA", Price: 100}},
		{Symbol: "TSLA", Quote: &yahoo.Quote{Symbol: "TSLA", Price: 200}},
	}
	m.relatedFocus = 0

	// Enter: sets compare
	updated, _ := m.Update(keyMsg("enter"))
	model := asModel(updated)
	if model.compareSymbol == nil {
		t.Fatal("expected compareSymbol to be set")
	}
	if model.compareSymbol.Symbol != "NVDA" {
		t.Errorf("expected compare=NVDA, got %s", model.compareSymbol.Symbol)
	}

	// Enter again on same: toggles off
	updated, _ = model.Update(keyMsg("enter"))
	model = asModel(updated)
	if model.compareSymbol != nil {
		t.Error("expected compareSymbol=nil after toggle off")
	}

	// Enter on different focus: sets new compare
	model.relatedFocus = 1
	updated, _ = model.Update(keyMsg("enter"))
	model = asModel(updated)
	if model.compareSymbol == nil || model.compareSymbol.Symbol != "TSLA" {
		t.Errorf("expected compare=TSLA, got %v", model.compareSymbol)
	}
}

func TestDetailEnterNoFocusNoOp(t *testing.T) {
	m := newTestModel()
	m.mode = viewDetail
	m.relatedFocus = -1

	updated, _ := m.Update(keyMsg("enter"))
	model := asModel(updated)
	if model.compareSymbol != nil {
		t.Error("enter with no focus should not set compare")
	}
}

func TestDetailTabNoRelatedNoOp(t *testing.T) {
	m := newTestModel()
	m.mode = viewDetail
	m.relatedSymbols = nil

	updated, _ := m.Update(keyMsg("tab"))
	model := asModel(updated)
	if model.relatedFocus != -1 {
		t.Errorf("tab with no related symbols should keep focus at -1, got %d", model.relatedFocus)
	}
}

func TestDetailNextPrevSymbolResetsCompare(t *testing.T) {
	m := newTestModel()
	m.mode = viewDetail
	m.relatedFocus = 1
	m.compareSymbol = &RelatedSymbol{Symbol: "NVDA"}

	// ] next symbol
	updated, _ := m.Update(keyMsg("]"))
	model := asModel(updated)
	if model.relatedFocus != -1 {
		t.Errorf("expected relatedFocus reset to -1, got %d", model.relatedFocus)
	}
	if model.compareSymbol != nil {
		t.Error("expected compareSymbol=nil after symbol switch")
	}
	if model.selected != 1 {
		t.Errorf("expected selected=1, got %d", model.selected)
	}

	// [ prev symbol
	model.relatedFocus = 0
	model.compareSymbol = &RelatedSymbol{Symbol: "TSLA"}
	updated, _ = model.Update(keyMsg("["))
	model = asModel(updated)
	if model.relatedFocus != -1 {
		t.Errorf("expected relatedFocus reset to -1, got %d", model.relatedFocus)
	}
	if model.compareSymbol != nil {
		t.Error("expected compareSymbol=nil after symbol switch")
	}
}

func TestDetailNextSymbolWraps(t *testing.T) {
	m := newTestModel()
	m.mode = viewDetail
	m.selected = 2 // last item

	updated, _ := m.Update(keyMsg("]"))
	model := asModel(updated)
	if model.selected != 0 {
		t.Errorf("expected selected=0 (wrapped), got %d", model.selected)
	}
}

func TestDetailPrevSymbolWraps(t *testing.T) {
	m := newTestModel()
	m.mode = viewDetail
	m.selected = 0 // first item

	updated, _ := m.Update(keyMsg("["))
	model := asModel(updated)
	if model.selected != 2 {
		t.Errorf("expected selected=2 (wrapped), got %d", model.selected)
	}
}

func TestDetailMarketDepthPopup(t *testing.T) {
	m := newTestModel()
	m.mode = viewDetail

	// Open popup
	updated, _ := m.Update(keyMsg("m"))
	model := asModel(updated)
	if !model.showMarketDepth {
		t.Error("expected showMarketDepth=true")
	}
	if model.popupTab != 0 {
		t.Errorf("expected popupTab=0, got %d", model.popupTab)
	}

	// Tab cycles popup tabs
	updated, _ = model.Update(keyMsg("tab"))
	model = asModel(updated)
	if model.popupTab != 1 {
		t.Errorf("expected popupTab=1, got %d", model.popupTab)
	}

	updated, _ = model.Update(keyMsg("tab"))
	model = asModel(updated)
	if model.popupTab != 2 {
		t.Errorf("expected popupTab=2, got %d", model.popupTab)
	}

	updated, _ = model.Update(keyMsg("tab"))
	model = asModel(updated)
	if model.popupTab != 0 {
		t.Errorf("expected popupTab=0 (wrapped), got %d", model.popupTab)
	}

	// Numeric keys jump to tab
	updated, _ = model.Update(keyMsg("2"))
	model = asModel(updated)
	if model.popupTab != 1 {
		t.Errorf("expected popupTab=1 for key '2', got %d", model.popupTab)
	}

	// Esc closes popup
	updated, _ = model.Update(keyMsg("esc"))
	model = asModel(updated)
	if model.showMarketDepth {
		t.Error("expected showMarketDepth=false after esc")
	}
}

func TestDetailBollingerToggleSwitchesOverlayMode(t *testing.T) {
	m := newTestModel()
	m.mode = viewDetail
	m.showTechnicals = true
	m.technicalOverlayMode = TechnicalOverlayMA

	updated, _ := m.Update(keyMsg("B"))
	model := asModel(updated)
	if model.technicalOverlayMode != TechnicalOverlayBollinger {
		t.Fatalf("expected Bollinger overlay mode, got %v", model.technicalOverlayMode)
	}

	updated, _ = model.Update(keyMsg("B"))
	model = asModel(updated)
	if model.technicalOverlayMode != TechnicalOverlayMA {
		t.Fatalf("expected MA overlay mode after second toggle, got %v", model.technicalOverlayMode)
	}
}

func TestDetailBollingerToggleIgnoredWhenTechnicalsHidden(t *testing.T) {
	m := newTestModel()
	m.mode = viewDetail
	m.showTechnicals = false
	m.technicalOverlayMode = TechnicalOverlayMA

	updated, _ := m.Update(keyMsg("B"))
	model := asModel(updated)
	if model.technicalOverlayMode != TechnicalOverlayMA {
		t.Fatalf("expected overlay mode unchanged when technicals hidden, got %v", model.technicalOverlayMode)
	}
}

// === Sort tests ===

func TestSortedItems(t *testing.T) {
	m := newTestModel()
	m.items[0].Quote = &yahoo.Quote{Symbol: "AAPL", ChangePercent: 2.5, MarketCap: 3000000000000}
	m.items[1].Quote = &yahoo.Quote{Symbol: "MSFT", ChangePercent: -1.0, MarketCap: 2500000000000}
	m.items[2].Quote = &yahoo.Quote{Symbol: "GOOG", ChangePercent: 0.5, MarketCap: 2000000000000}

	// Default: original order
	sorted := m.sortedItems()
	if sorted[0].Symbol != "AAPL" {
		t.Errorf("default sort: expected AAPL first, got %s", sorted[0].Symbol)
	}

	// By symbol ascending
	m.sortMode = SortBySymbol
	sorted = m.sortedItems()
	if sorted[0].Symbol != "AAPL" || sorted[1].Symbol != "GOOG" || sorted[2].Symbol != "MSFT" {
		t.Errorf("symbol sort: got %s %s %s", sorted[0].Symbol, sorted[1].Symbol, sorted[2].Symbol)
	}

	// By symbol descending
	m.sortMode = SortBySymbolDesc
	sorted = m.sortedItems()
	if sorted[0].Symbol != "MSFT" || sorted[2].Symbol != "AAPL" {
		t.Errorf("symbol desc sort: got %s first, %s last", sorted[0].Symbol, sorted[2].Symbol)
	}

	// By change ascending (MSFT -1.0 < GOOG 0.5 < AAPL 2.5)
	m.sortMode = SortByChangeAsc
	sorted = m.sortedItems()
	if sorted[0].Symbol != "MSFT" || sorted[2].Symbol != "AAPL" {
		t.Errorf("change asc: got %s first, %s last", sorted[0].Symbol, sorted[2].Symbol)
	}

	// By change descending
	m.sortMode = SortByChangeDesc
	sorted = m.sortedItems()
	if sorted[0].Symbol != "AAPL" || sorted[2].Symbol != "MSFT" {
		t.Errorf("change desc: got %s first, %s last", sorted[0].Symbol, sorted[2].Symbol)
	}
}

// === View output tests ===

func TestViewLoadingState(t *testing.T) {
	m := newTestModel()
	m.width = 0

	out := m.View()
	if out != "Loading..." {
		t.Errorf("expected 'Loading...' for zero width, got %q", out)
	}
}

func TestViewQuittingState(t *testing.T) {
	m := newTestModel()
	m.quitting = true

	out := m.View()
	if out != "" {
		t.Errorf("expected empty string when quitting, got %q", out)
	}
}

func TestViewWatchlistContainsSymbols(t *testing.T) {
	m := newTestModel()
	m.items[0].Quote = &yahoo.Quote{Symbol: "AAPL", Name: "Apple Inc.", Price: 150.0, ChangePercent: 1.5}

	out := m.View()
	if !strings.Contains(out, "AAPL") {
		t.Error("watchlist view should contain AAPL")
	}
	if !strings.Contains(out, "Finsight") {
		t.Error("watchlist view should contain title")
	}
}

func TestCurrentTimeframe(t *testing.T) {
	m := newTestModel()
	tf := m.currentTimeframe()
	if tf.Label != "1D" {
		t.Errorf("expected 1D, got %s", tf.Label)
	}

	m.timeframeIdx = 3
	tf = m.currentTimeframe()
	if tf.Label != "6M" {
		t.Errorf("expected 6M, got %s", tf.Label)
	}
}

func TestTimeframeNavigation(t *testing.T) {
	m := newTestModel()

	// Right moves forward
	updated, _ := m.Update(keyMsg("right"))
	model := asModel(updated)
	if model.timeframeIdx != 1 {
		t.Errorf("expected timeframeIdx=1, got %d", model.timeframeIdx)
	}

	// Navigate to end, verify clamping
	for i := 0; i < 10; i++ {
		updated, _ = model.Update(keyMsg("right"))
		model = asModel(updated)
	}
	if model.timeframeIdx != len(timeframes)-1 {
		t.Errorf("expected timeframeIdx=%d (clamped), got %d", len(timeframes)-1, model.timeframeIdx)
	}

	// Left moves back
	updated, _ = model.Update(keyMsg("left"))
	model = asModel(updated)
	if model.timeframeIdx != len(timeframes)-2 {
		t.Errorf("expected timeframeIdx=%d, got %d", len(timeframes)-2, model.timeframeIdx)
	}
}

// === Message handling ===

func TestHandleQuotesMsg(t *testing.T) {
	m := newTestModel()

	msg := quotesMsg{
		quotes: []yahoo.Quote{
			{Symbol: "AAPL", Price: 175.5, ChangePercent: 2.3, Name: "Apple Inc."},
			{Symbol: "MSFT", Price: 410.0, ChangePercent: -0.5, Name: "Microsoft"},
		},
	}

	updated, _ := m.Update(msg)
	model := asModel(updated)

	// Quotes should be populated
	for _, item := range model.items {
		if item.Symbol == "AAPL" && item.Quote != nil {
			if item.Quote.Price != 175.5 {
				t.Errorf("expected AAPL price=175.5, got %f", item.Quote.Price)
			}
			return
		}
	}
	t.Error("AAPL quote was not populated")
}

func TestHandleQuotesMsgError(t *testing.T) {
	m := newTestModel()

	msg := quotesMsg{
		err: fmt.Errorf("network error"),
	}

	updated, _ := m.Update(msg)
	model := asModel(updated)
	if model.err == "" {
		t.Error("expected error to be set on model")
	}
}

func TestHandleQuotesMsgBackgroundErrorPreservesExistingData(t *testing.T) {
	m := newTestModel()
	m.items[0].Quote = &yahoo.Quote{Symbol: "AAPL", Price: 175.5}

	updated, _ := m.Update(quotesMsg{
		err:        fmt.Errorf("temporary refresh error"),
		background: true,
	})
	model := asModel(updated)

	if model.items[0].Quote == nil {
		t.Fatal("expected existing quote to be preserved")
	}
	if model.items[0].Quote.Price != 175.5 {
		t.Fatalf("expected preserved quote price 175.5, got %f", model.items[0].Quote.Price)
	}
	if model.err != "" {
		t.Fatalf("expected background refresh error to stay hidden when data exists, got %q", model.err)
	}
}

func TestHandleChartMsg(t *testing.T) {
	m := newTestModel()
	m.items[0].Loading = true

	msg := chartMsg{
		symbol: "AAPL",
		data: &yahoo.ChartData{
			Closes: []float64{150, 151, 152, 153},
		},
	}

	updated, _ := m.Update(msg)
	model := asModel(updated)

	for _, item := range model.items {
		if item.Symbol == "AAPL" {
			if item.ChartData == nil {
				t.Error("expected chart data to be set for AAPL")
			}
			return
		}
	}
}

func TestHandleChartMsgBackgroundErrorPreservesExistingData(t *testing.T) {
	m := newTestModel()
	m.items[0].ChartData = &yahoo.ChartData{Closes: []float64{150, 151}}

	updated, _ := m.Update(chartMsg{
		symbol:     "AAPL",
		err:        fmt.Errorf("temporary chart refresh error"),
		background: true,
	})
	model := asModel(updated)

	if model.items[0].ChartData == nil {
		t.Fatal("expected existing chart data to be preserved")
	}
	if len(model.items[0].ChartData.Closes) != 2 {
		t.Fatalf("expected preserved chart data, got %+v", model.items[0].ChartData.Closes)
	}
	if model.items[0].Error != "" {
		t.Fatalf("expected no row error for background chart refresh failure, got %q", model.items[0].Error)
	}
}

func TestHandleChartMsgDoesNotReCacheOnCacheHit(t *testing.T) {
	// Regression: handleChart used to write msg.data back to cache on
	// every invocation, including cache-first hits. That refreshed the
	// JSON cache's FetchedAt timestamp, keeping stale entries "fresh"
	// indefinitely across restarts. Cache writes now live in the
	// network-fetch goroutine so cache-first hits don't re-stamp
	// stale data.
	m := newTestModel()
	m.timeframeIdx = 0
	c, err := cache.New(t.TempDir())
	if err != nil {
		t.Fatalf("new cache: %v", err)
	}
	m.cache = c

	// Seed the cache as if it were written long ago (simulated by not
	// writing anything: GetChart will return nil for a missing key).
	updated, _ := m.Update(chartMsg{
		symbol:        "AAPL",
		data:          &yahoo.ChartData{Closes: []float64{101, 102, 103}},
		chartRange:    "2d",
		chartInterval: "5m",
	})
	model := asModel(updated)

	if got := model.cache.GetChart("AAPL", "2d", "5m"); got != nil {
		t.Fatal("handleChart must not write to cache; cache writes belong to the network-fetch goroutine")
	}
	if got := model.cache.GetChart("AAPL", "5d", "5m"); got != nil {
		t.Fatal("handleChart must not mirror-write to the 5d/5m key either")
	}
}

func TestHandleKeyStatsMsg(t *testing.T) {
	m := newTestModel()
	m.items[0].Quote = &yahoo.Quote{Symbol: "AAPL", PE: 28.5}

	msg := keyStatsMsg{
		symbol: "AAPL",
		stats: &yahoo.KeyStats{
			PEGRatio:     1.5,
			Beta:         1.2,
			AnnualGrowth: 0.15,
		},
	}

	updated, _ := m.Update(msg)
	model := asModel(updated)

	for _, item := range model.items {
		if item.Symbol == "AAPL" {
			if item.Quote.PEG != 1.5 {
				t.Errorf("expected PEG=1.5, got %f", item.Quote.PEG)
			}
			if item.Quote.Beta != 1.2 {
				t.Errorf("expected Beta=1.2, got %f", item.Quote.Beta)
			}
			return
		}
	}
	t.Error("AAPL stats were not populated")
}

// === Render tests ===

func TestRenderRelatedSymbolsEmpty(t *testing.T) {
	m := newTestModel()
	m.relatedSymbols = nil

	out := m.renderRelatedSymbols()
	if out != "" {
		t.Errorf("expected empty for no related symbols, got %q", out)
	}
}

func TestRenderRelatedSymbolsContent(t *testing.T) {
	m := newTestModel()
	m.relatedSymbols = []RelatedSymbol{
		{Symbol: "NVDA", Quote: &yahoo.Quote{ChangePercent: 3.5}},
		{Symbol: "TSLA", Quote: &yahoo.Quote{ChangePercent: -1.2}},
	}

	out := m.renderRelatedSymbols()
	if !strings.Contains(out, "Related") {
		t.Error("expected 'Related' header")
	}
	if !strings.Contains(out, "NVDA") {
		t.Error("expected NVDA in output")
	}
	if !strings.Contains(out, "TSLA") {
		t.Error("expected TSLA in output")
	}
}

func TestRenderCompareChartNil(t *testing.T) {
	m := newTestModel()
	m.compareSymbol = nil

	out := m.renderCompareChart(WatchlistItem{Symbol: "AAPL"}, 20)
	if out != "" {
		t.Errorf("expected empty for nil compare, got %q", out)
	}
}

func TestRenderCompareChartOutput(t *testing.T) {
	m := newTestModel()
	m.compareSymbol = &RelatedSymbol{
		Symbol: "NVDA",
		Quote:  &yahoo.Quote{Symbol: "NVDA", Price: 800, ChangePercent: 2.1},
		ChartData: &yahoo.ChartData{
			Closes: []float64{780, 785, 790, 795, 800},
		},
	}

	item := WatchlistItem{
		Symbol: "AAPL",
		Quote:  &yahoo.Quote{Symbol: "AAPL", Price: 175, ChangePercent: 1.5},
		ChartData: &yahoo.ChartData{
			Closes: []float64{170, 172, 173, 174, 175},
		},
	}

	out := m.renderCompareChart(item, 15)
	if out == "" {
		t.Fatal("expected non-empty compare chart")
	}
	if !strings.Contains(out, "AAPL") {
		t.Error("compare chart should contain main symbol AAPL")
	}
	if !strings.Contains(out, "NVDA") {
		t.Error("compare chart should contain compare symbol NVDA")
	}
	if !strings.Contains(out, "vs") {
		t.Error("compare chart should contain 'vs' separator")
	}
}

func TestRenderTimeframeBar(t *testing.T) {
	m := newTestModel()

	out := m.renderTimeframeBar()
	if !strings.Contains(out, "1D") {
		t.Error("timeframe bar should contain 1D")
	}
}

// === Window size ===

func TestWindowSizeMsg(t *testing.T) {
	m := newTestModel()

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 60})
	model := asModel(updated)
	if model.width != 200 {
		t.Errorf("expected width=200, got %d", model.width)
	}
	if model.height != 60 {
		t.Errorf("expected height=60, got %d", model.height)
	}
}
