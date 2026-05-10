package ui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ray-x/finsight/internal/config"
	"github.com/ray-x/finsight/internal/portfolio"
	"github.com/ray-x/finsight/internal/yahoo"
)

// newTestPortfolioModel builds a Model in viewPortfolio mode with three
// in-memory positions (no disk, no network).
func newTestPortfolioModel() Model {
	cfg := &config.Config{
		RefreshInterval: 900,
		ChartStyle:      "candlestick_dotted",
		Watchlists: []config.WatchlistGroup{
			{Name: "Default", Symbols: []config.WatchItem{
				{Symbol: "AAPL", Name: "Apple Inc."},
			}},
		},
	}
	pf := &portfolio.File{Positions: []portfolio.Position{
		{Symbol: "AAPL", Position: 10, OpenPrice: 150},
		{Symbol: "MSFT", Position: 5, OpenPrice: 300},
		{Symbol: "NVDA", Position: 2, OpenPrice: 0}, // unset, will auto-fill
	}}
	m := Model{
		cfg:           cfg,
		mode:          viewPortfolio,
		width:         140,
		height:        40,
		relatedFocus:  -1,
		portfolio:     pf,
		portfolioPath: "", // no disk writes
	}
	m.rebuildPortfolioItems()
	return m
}

func TestPortfolioNavigation(t *testing.T) {
	m := newTestPortfolioModel()
	if m.portfolioSelected != 0 {
		t.Fatalf("expected selected=0, got %d", m.portfolioSelected)
	}
	// down
	nm, _ := m.handlePortfolioKey(keyMsg("down"))
	m = asModel(nm)
	if m.portfolioSelected != 1 {
		t.Fatalf("after down want 1, got %d", m.portfolioSelected)
	}
	nm, _ = m.handlePortfolioKey(keyMsg("down"))
	m = asModel(nm)
	nm, _ = m.handlePortfolioKey(keyMsg("down"))
	m = asModel(nm)
	if m.portfolioSelected != 2 {
		t.Fatalf("should clamp at last index 2, got %d", m.portfolioSelected)
	}
	// up
	nm, _ = m.handlePortfolioKey(keyMsg("up"))
	m = asModel(nm)
	if m.portfolioSelected != 1 {
		t.Fatalf("after up want 1, got %d", m.portfolioSelected)
	}
}

func TestPortfolioProfitToggle(t *testing.T) {
	m := newTestPortfolioModel()
	if m.portfolioProfit != ProfitPercent {
		t.Fatalf("default profit mode should be Percent")
	}
	nm, _ := m.handlePortfolioKey(keyMsg("%"))
	m = asModel(nm)
	if m.portfolioProfit != ProfitTotal {
		t.Fatalf("expected ProfitTotal after toggle")
	}
	nm, _ = m.handlePortfolioKey(keyMsg("%"))
	m = asModel(nm)
	if m.portfolioProfit != ProfitPercent {
		t.Fatalf("expected ProfitPercent after second toggle")
	}
}

func TestPortfolioDeleteItem(t *testing.T) {
	m := newTestPortfolioModel()
	if len(m.portfolioItems) != 3 {
		t.Fatalf("expected 3 items, got %d", len(m.portfolioItems))
	}
	m.portfolioSelected = 1 // MSFT
	nm, _ := m.handlePortfolioKey(keyMsg("d"))
	m = asModel(nm)
	if len(m.portfolioItems) != 2 {
		t.Fatalf("expected 2 items after delete, got %d", len(m.portfolioItems))
	}
	for _, it := range m.portfolioItems {
		if it.Symbol == "MSFT" {
			t.Fatalf("MSFT was not removed")
		}
	}
	if m.portfolio.Find("MSFT") != nil {
		t.Fatalf("MSFT still in underlying positions")
	}
}

func TestPortfolioBackToWatchlist(t *testing.T) {
	m := newTestPortfolioModel()
	nm, _ := m.handlePortfolioKey(keyMsg("esc"))
	m = asModel(nm)
	if m.mode != viewWatchlist {
		t.Fatalf("expected viewWatchlist, got %v", m.mode)
	}
}

func TestBeginPortfolioAddDefaultsQuantityAndBoughtDate(t *testing.T) {
	m := newTestPortfolioModel()
	nm, _ := m.beginPortfolioAdd("TSLA", "Tesla")
	m = asModel(nm)

	if !m.portfolioForm.active {
		t.Fatal("expected portfolio form to be active")
	}
	if m.portfolioForm.posField != "10" {
		t.Fatalf("expected default quantity 10, got %q", m.portfolioForm.posField)
	}
	if m.portfolioForm.dateField != time.Now().Format(time.DateOnly) {
		t.Fatalf("expected today's bought date, got %q", m.portfolioForm.dateField)
	}
}

func TestPortfolioFormShiftTabMovesBackward(t *testing.T) {
	m := newTestPortfolioModel()
	m.portfolioForm = portfolioFormState{
		active:   true,
		symbol:   "AAPL",
		posField: "10",
		focus:    0,
	}

	nm, _ := m.handlePortfolioFormKey(keyMsg("shift+tab"))
	m = asModel(nm)
	if m.portfolioForm.focus != 2 {
		t.Fatalf("expected focus to wrap backward to 2, got %d", m.portfolioForm.focus)
	}

	nm, _ = m.handlePortfolioFormKey(keyMsg("shift+tab"))
	m = asModel(nm)
	if m.portfolioForm.focus != 1 {
		t.Fatalf("expected focus to wrap backward to 1, got %d", m.portfolioForm.focus)
	}

	nm, _ = m.handlePortfolioFormKey(keyMsg("shift+tab"))
	m = asModel(nm)
	if m.portfolioForm.focus != 0 {
		t.Fatalf("expected focus to wrap backward to 0, got %d", m.portfolioForm.focus)
	}
}

func TestSubmitPortfolioFormSavesBoughtDateAndDefaults(t *testing.T) {
	m := newTestPortfolioModel()
	m.portfolioPath = filepath.Join(t.TempDir(), "portfolio.yaml")
	m.portfolioForm = portfolioFormState{
		active:    true,
		symbol:    "TSLA",
		name:      "Tesla",
		posField:  "10",
		dateField: "2026-04-30",
	}

	nm, _ := m.submitPortfolioForm()
	m = asModel(nm)

	p := m.portfolio.Find("TSLA")
	if p == nil {
		t.Fatal("expected TSLA position to be added")
	}
	if p.Position != 10 {
		t.Fatalf("expected quantity 10, got %.2f", p.Position)
	}
	if p.BoughtAt != "2026-04-30" {
		t.Fatalf("expected bought date to persist, got %q", p.BoughtAt)
	}
	if p.OpenPrice != 0 {
		t.Fatalf("expected blank open price to stay 0, got %.2f", p.OpenPrice)
	}
}

func TestSubmitPortfolioFormRejectsInvalidBoughtDate(t *testing.T) {
	m := newTestPortfolioModel()
	m.portfolioPath = filepath.Join(t.TempDir(), "portfolio.yaml")
	m.portfolioForm = portfolioFormState{
		active:    true,
		symbol:    "TSLA",
		name:      "Tesla",
		posField:  "10",
		dateField: "04/30/2026",
	}

	nm, _ := m.submitPortfolioForm()
	m = asModel(nm)

	if m.portfolioForm.err == "" {
		t.Fatal("expected validation error for invalid bought date")
	}
	if got := m.portfolio.Find("TSLA"); got != nil {
		t.Fatalf("expected TSLA not to be added on invalid date, got %+v", got)
	}
}

func TestPortfolioMetricsAndRender(t *testing.T) {
	m := newTestPortfolioModel()
	// Inject quotes to exercise compute + render
	aapl := &yahoo.Quote{Symbol: "AAPL", Price: 180, PreviousClose: 175, Change: 5, ChangePercent: 2.86, Open: 176}
	msft := &yahoo.Quote{Symbol: "MSFT", Price: 310, PreviousClose: 308, Change: 2, ChangePercent: 0.65, Open: 309}
	nvda := &yahoo.Quote{Symbol: "NVDA", Price: 900, PreviousClose: 880, Change: 20, ChangePercent: 2.27, Open: 885}
	for i := range m.portfolioItems {
		switch m.portfolioItems[i].Symbol {
		case "AAPL":
			m.portfolioItems[i].Quote = aapl
		case "MSFT":
			m.portfolioItems[i].Quote = msft
		case "NVDA":
			m.portfolioItems[i].Quote = nvda
		}
	}
	metrics := ComputePortfolioMetrics(m.portfolioItems)
	if metrics.Count != 3 {
		t.Fatalf("count=%d", metrics.Count)
	}
	if metrics.Known != 2 {
		t.Fatalf("known positions should be 2 (AAPL+MSFT), got %d", metrics.Known)
	}
	wantMV := 180.0*10 + 310.0*5 + 900.0*2
	if metrics.TotalMarketValue != wantMV {
		t.Fatalf("total market value want %.2f got %.2f", wantMV, metrics.TotalMarketValue)
	}
	// Unrealized P/L only covers known-open positions (AAPL + MSFT)
	wantUnreal := (180.0-150)*10 + (310.0-300)*5
	if metrics.TotalUnrealizedPL != wantUnreal {
		t.Fatalf("unrealized want %.2f got %.2f", wantUnreal, metrics.TotalUnrealizedPL)
	}
	// Render should not panic and include symbols
	out := RenderPortfolio(m.portfolioItems, 0, m.width, 2, m.cfg.ChartStyle, m.portfolioProfit, "1D")
	for _, s := range []string{"AAPL", "MSFT", "NVDA"} {
		if !strings.Contains(out, s) {
			t.Fatalf("render missing symbol %s", s)
		}
	}
}

func TestPortfolioAutoFillOpenPrice(t *testing.T) {
	m := newTestPortfolioModel()
	// NVDA has OpenPrice 0 — applyQuotesToPortfolio should auto-fill.
	quotes := []yahoo.Quote{
		{Symbol: "NVDA", Price: 900, Open: 885, PreviousClose: 880},
	}
	m.applyQuotesToPortfolio(quotes)
	found := false
	for _, it := range m.portfolioItems {
		if it.Symbol == "NVDA" {
			if it.OpenPrice != 885 {
				t.Fatalf("expected NVDA OpenPrice=885 after auto-fill, got %.2f", it.OpenPrice)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("NVDA not present in items")
	}
	p := m.portfolio.Find("NVDA")
	if p == nil || p.OpenPrice != 885 {
		t.Fatalf("underlying position not updated")
	}
	if p.BoughtAt != "" {
		t.Fatalf("expected auto-fill to leave bought date unchanged, got %q", p.BoughtAt)
	}
}
