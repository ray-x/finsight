package ui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/ray-x/finsight/internal/cache"
	"github.com/ray-x/finsight/internal/chart"
	"github.com/ray-x/finsight/internal/config"
	"github.com/ray-x/finsight/internal/db"
	"github.com/ray-x/finsight/internal/earnings"
	"github.com/ray-x/finsight/internal/edgar"
	"github.com/ray-x/finsight/internal/llm"
	"github.com/ray-x/finsight/internal/logger"
	"github.com/ray-x/finsight/internal/news"
	"github.com/ray-x/finsight/internal/portfolio"
	"github.com/ray-x/finsight/internal/yahoo"
)

// View modes
type viewMode int

const (
	viewWatchlist viewMode = iota
	viewDetail
	viewSearch
	viewHelp
	viewPortfolio
	viewHeatmap
)

// Timeframe presets
type timeframe struct {
	Label    string
	Range    string
	Interval string
}

var timeframes = []timeframe{
	{Label: "1D", Range: "1d", Interval: "5m"},    // ~78 candles (6.5h × 12)
	{Label: "1W", Range: "5d", Interval: "15m"},   // ~130 candles
	{Label: "1M", Range: "1mo", Interval: "60m"},  // ~154 candles
	{Label: "6M", Range: "6mo", Interval: "1d"},   // ~126 candles
	{Label: "1Y", Range: "1y", Interval: "1d"},    // ~252 candles
	{Label: "3Y", Range: "3y", Interval: "1d"},    // ~756 candles
	{Label: "5Y", Range: "5y", Interval: "1wk"},   // ~260 candles
	{Label: "10Y", Range: "10y", Interval: "1wk"}, // ~520 candles; Yahoo returns max available if the symbol is younger than 10y
}

// Messages
type quotesMsg struct {
	quotes     []yahoo.Quote
	symbols    []string
	err        error
	background bool
}

type chartMsg struct {
	symbol        string
	data          *yahoo.ChartData
	err           error
	chartRange    string
	chartInterval string
	background    bool
}

// indicatorContextMsg carries a long-horizon chart used to warm
// up technical indicators for the primary chart.
type indicatorContextMsg struct {
	symbol   string
	interval string
	data     *yahoo.ChartData
	err      error
}

type searchMsg struct {
	results []yahoo.SearchResult
	err     error
}

type keyStatsMsg struct {
	symbol string
	stats  *yahoo.KeyStats
	err    error
}

type financialsMsg struct {
	symbol     string
	financials *yahoo.FinancialData
	err        error
}

type holdersMsg struct {
	symbol  string
	holders *yahoo.HolderData
	err     error
}

type aiMsg struct {
	symbol string
	text   string
	err    error
}

type earningsMsg struct {
	symbol    string
	report    *llm.EarningsReport
	discovery *earnings.DiscoveryResult
	err       error
}

type summaryMsg struct {
	group int
	text  string
	err   error
}

type heatmapFeedMsg struct {
	kind   HeatmapType
	quotes []yahoo.Quote
	err    error
}

// heatmapWatchlistQuotesMsg carries freshly-fetched quotes for inactive watchlist
// group symbols. On arrival the watchlist heatmap is rebuilt in place.
type heatmapWatchlistQuotesMsg struct {
	quotes []yahoo.Quote
	err    error
}

// Related symbols
type RelatedSymbol struct {
	Symbol    string
	Score     float64
	Quote     *yahoo.Quote
	ChartData *yahoo.ChartData
}

type recommendedSymbolsMsg struct {
	forSymbol string
	symbols   []yahoo.RecommendedSymbol
	err       error
}

type relatedQuotesMsg struct {
	forSymbol string
	quotes    []yahoo.Quote
	err       error
}

type relatedChartMsg struct {
	forSymbol     string
	symbol        string
	data          *yahoo.ChartData
	chartRange    string
	chartInterval string
	err           error
}

type tickMsg time.Time

type startupRefreshMsg struct{}

// MAMode selects which moving-average preset to draw on the detail
// chart and which MACD parameters to use for the MACD panel. Cycled
// via the "M" key in the detail view.
type MAMode int

const (
	// MADayTrading: fast EMAs for intraday / swing setups. MACD 8/17/9.
	MADayTrading MAMode = iota
	// MAMediumTerm: 21/50 EMAs. MACD 12/26/9.
	MAMediumTerm
	// MALongTerm: 50/100 EMAs. MACD 12/26/9.
	MALongTerm
	// MAMulti: 20/50/200 — classic trend ribbon. MACD 12/26/9.
	MAMulti
	maModeCount
)

// Label returns a short human label for the current preset.
func (m MAMode) Label() string {
	switch m {
	case MADayTrading:
		return "EMA 9/21 · MACD 8/17/9 (day)"
	case MAMediumTerm:
		return "EMA 21/50 · MACD 12/26/9 (medium)"
	case MALongTerm:
		return "EMA 50/100 · MACD 12/26/9 (long)"
	case MAMulti:
		return "EMA 20/50/200 · MACD 12/26/9"
	}
	return ""
}

// EMAPeriods returns the ordered list of EMA lookbacks to draw as
// chart overlays for this mode.
func (m MAMode) EMAPeriods() []int {
	switch m {
	case MADayTrading:
		return []int{9, 21}
	case MAMediumTerm:
		return []int{21, 50}
	case MALongTerm:
		return []int{50, 100}
	case MAMulti:
		return []int{20, 50, 200}
	}
	return nil
}

// MACDParams returns (fast, slow, signal) EMAs for the MACD panel.
// Day-trading mode uses the quicker 8/17/9 to match the tighter EMAs.
func (m MAMode) MACDParams() (fast, slow, signal int) {
	if m == MADayTrading {
		return 8, 17, 9
	}
	return 12, 26, 9
}

// Key bindings
type keyMap struct {
	Up          key.Binding
	Down        key.Binding
	Left        key.Binding
	Right       key.Binding
	Enter       key.Binding
	Back        key.Binding
	Search      key.Binding
	Delete      key.Binding
	Sort        key.Binding
	Help        key.Binding
	NextSymbol  key.Binding
	PrevSymbol  key.Binding
	MarketDepth key.Binding
	AI          key.Binding
	Earnings    key.Binding
	Summary     key.Binding
	Quit        key.Binding
	Refresh     key.Binding
	RefreshAll  key.Binding
	ChartStyle  key.Binding
	MAMode      key.Binding
}

var keys = keyMap{
	Up:          key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
	Down:        key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
	Left:        key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("←/h", "prev timeframe")),
	Right:       key.NewBinding(key.WithKeys("right", "l"), key.WithHelp("→/l", "next timeframe")),
	Enter:       key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "detail/select")),
	Back:        key.NewBinding(key.WithKeys("esc", "q"), key.WithHelp("esc/q", "back")),
	Search:      key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
	Delete:      key.NewBinding(key.WithKeys("d", "x"), key.WithHelp("d/x", "remove")),
	Sort:        key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "sort")),
	Help:        key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
	NextSymbol:  key.NewBinding(key.WithKeys("]"), key.WithHelp("]", "next symbol")),
	PrevSymbol:  key.NewBinding(key.WithKeys("["), key.WithHelp("[", "prev symbol")),
	MarketDepth: key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "market depth")),
	AI:          key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "AI analysis")),
	Earnings:    key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "earnings report")),
	Summary:     key.NewBinding(key.WithKeys("S"), key.WithHelp("S", "watchlist summary")),
	Quit:        key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit")),
	Refresh:     key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh symbol")),
	RefreshAll:  key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "refresh all")),
	ChartStyle:  key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "cycle chart style")),
	MAMode:      key.NewBinding(key.WithKeys("M"), key.WithHelp("M", "cycle MA preset")),
}

var portfolioKeys = struct {
	Open         key.Binding
	Watchlist    key.Binding
	ProfitToggle key.Binding
	ReviewAll    key.Binding
	Edit         key.Binding
}{
	Open:         key.NewBinding(key.WithKeys("p", "P"), key.WithHelp("p", "portfolio")),
	Watchlist:    key.NewBinding(key.WithKeys("w", "W"), key.WithHelp("w", "watchlist")),
	ProfitToggle: key.NewBinding(key.WithKeys("%"), key.WithHelp("%", "profit %/$")),
	ReviewAll:    key.NewBinding(key.WithKeys("A"), key.WithHelp("A", "AI portfolio review")),
	Edit:         key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit position")),
}

// Model is the main bubbletea model.
type Model struct {
	cfg     *config.Config
	cfgPath string
	client  *yahoo.Client
	cache   cache.Cacher
	news    news.Provider
	items   []WatchlistItem

	activeGroup     int // index into cfg.Watchlists
	selected        int
	timeframeIdx    int // index into timeframes slice
	sortMode        SortMode
	mode            viewMode
	width           int
	height          int
	lastUpdate      time.Time
	err             string
	quitting        bool
	prevMode        viewMode // for returning from help
	showMarketDepth bool
	popupTab        int // 0=Market, 1=Financials, 2=Holders
	popupScroll     int // scroll offset for popup content

	// Related symbols for detail view
	relatedSymbols   []RelatedSymbol
	relatedForSymbol string         // which symbol the related data is for
	relatedFocus     int            // -1 = no focus, 0..N = focused related symbol index
	compareSymbol    *RelatedSymbol // non-nil when in compare mode

	// AI analysis
	llmClient   *llm.Client
	edgarClient *edgar.Client
	aiText      string // current AI analysis text
	aiSymbol    string // which symbol the AI analysis is for
	aiLoading   bool
	showAI      bool

	// AI provider switcher popup
	showProviderPicker bool
	providerPickerSel  int

	// Earnings report
	earningsText         string
	earningsSymbol       string
	earningsLoading      bool
	earningsFiling       string // e.g. "10-Q (2026-01-31)"
	showEarnings         bool
	pendingEarnings      bool // waiting for financials before fetching earnings
	pendingAI            bool // waiting for financials before fetching AI
	earningsDiscovery    *earnings.DiscoveryResult
	earningsLinkSel      int
	earningsLinkStatus   string
	earningsDB           *db.DB
	earningsPoller       *earnings.Poller
	earningsOrchestrator *earnings.Orchestrator

	// Technical indicators panel (inline in the detail view, toggled with "t").
	showTechnicals bool

	// Active moving-average preset for the chart overlays + MACD panel.
	// Cycled with the "M" key.
	maMode MAMode

	// Watchlist summary
	summaryText    string
	summaryGroup   int // which group the summary is for
	summaryLoading bool
	showSummary    bool

	// Search state
	search SearchView

	// Portfolio state
	portfolio          *portfolio.File
	portfolioPath      string // "" means embed in cfg
	portfolioItems     []PortfolioItem
	portfolioSelected  int
	portfolioProfit    ProfitMode
	portfolioPrev      viewMode // view to return to on Esc from portfolio
	portfolioSearching bool     // true while search was launched from portfolio view

	// Portfolio AI advisor state
	showPortfolioAI    bool
	portfolioAIText    string
	portfolioAISymbol  string // "" = whole-portfolio review
	portfolioAILoading bool
	portfolioAIScroll  int

	// Portfolio in-app add/edit form state
	portfolioForm portfolioFormState

	// Unified interactive AI command window (replaces one-shot AI popups
	// when the user presses `a`). See aicmd.go.
	aicmd AICmdState

	// Earnings orchestrator background context (for graceful shutdown)
	orchestratorCtx    context.Context
	orchestratorCancel context.CancelFunc

	// Heatmap state
	heatmap     HeatmapModel
	heatmapPrev viewMode // view to return to on Esc from heatmap

	// Temporary detail item opened from heatmap when symbol isn't in watchlist.
	tempDetailActive       bool
	tempDetailSymbol       string
	tempDetailPrevSelected int

	// Watchlist-add confirmation popup
	confirmWatchlistAdd bool
	confirmWatchlistSel int // selected group index in multi-group picker
}

// portfolioFormState tracks the inline add/edit form for positions.
type portfolioFormState struct {
	active    bool
	editing   bool   // true = editing existing, false = adding new
	symbol    string // locked symbol being added/edited
	name      string
	posField  string
	openField string
	focus     int // 0 = position, 1 = open price
	err       string
}

func NewModel(cfg *config.Config, cfgPath string) Model {
	ApplyTheme(cfg.ColorScheme)

	var groupItems []config.WatchItem
	if len(cfg.Watchlists) > 0 {
		groupItems = cfg.Watchlists[0].Symbols
	}
	items := make([]WatchlistItem, len(groupItems))
	for i, w := range groupItems {
		items[i] = WatchlistItem{
			Symbol:  w.Symbol,
			Name:    w.Name,
			Loading: true,
		}
	}

	sqliteCache, err := cache.NewSQLiteCache(cache.DefaultDir())
	if err != nil {
		logger.Log("cache: SQLiteCache init failed, running without persistent cache: %v", err)
	}
	var dataCache cache.Cacher
	if sqliteCache != nil {
		dataCache = sqliteCache
	}
	var earningsDB *db.DB
	if sqliteCache != nil {
		earningsDB = sqliteCache.KnowledgeDB()
	}

	var llmClient *llm.Client
	if cfg.LLM.Model != "" {
		client := llm.NewClient(llm.Config{
			Provider:      llm.Provider(cfg.LLM.Provider),
			Endpoint:      cfg.LLM.Endpoint,
			Model:         cfg.LLM.Model,
			APIKey:        cfg.LLM.APIKey,
			Project:       cfg.LLM.Project,
			Location:      cfg.LLM.Location,
			ContextTokens: cfg.LLM.ContextTokens,
		})
		if client.Configured() {
			llmClient = client
		}
	}

	var earningsPoller *earnings.Poller
	if sqliteCache != nil {
		registry := earnings.NewRegistry(cfg.Earnings.IRFeedRegistry)
		if !registry.Empty() {
			earningsPoller = earnings.NewPoller(sqliteCache.KnowledgeDB(), registry)
		}
	}

	var earningsOrchestrator *earnings.Orchestrator
	if sqliteCache != nil && cfg.Earnings.EDGARConfirmSeconds > 0 {
		earningsOrchestrator = earnings.NewOrchestrator(sqliteCache.KnowledgeDB(), &cfg.Earnings)
	}

	// Create context for orchestrator lifecycle management
	orchestratorCtx, orchestratorCancel := context.WithCancel(context.Background())

	// Resolve portfolio storage: prefer a dedicated file (private, 0600)
	// so holdings don't sit in config.yaml by default. Fall back to the
	// embedded cfg.Portfolio list when no file exists.
	pf, pfPath := loadPortfolio(cfg)

	// Pre-warm items from cache synchronously so the very first
	// frame rendered by bubbletea already shows real prices and
	// charts instead of "Loading...". The subsequent async quote
	// + chart commands dispatched by Init() still run and replace
	// this data if it differs, but the initial paint is instant.
	if dataCache != nil {
		prewarmItemsFromCache(items, dataCache)
	}

	return Model{
		cfg:                  cfg,
		cfgPath:              cfgPath,
		client:               yahoo.NewClient(),
		cache:                dataCache,
		news:                 news.NewMulti(news.NewYahoo(), news.NewGoogle()),
		llmClient:            llmClient,
		edgarClient:          edgar.NewClient(cfg.EdgarEmail),
		earningsDB:           earningsDB,
		earningsPoller:       earningsPoller,
		earningsOrchestrator: earningsOrchestrator,
		items:                items,
		mode:                 viewWatchlist,
		timeframeIdx:         0, // default: 1D

		aicmd: AICmdState{History: loadAICmdHistory()},

		portfolio:          pf,
		portfolioPath:      pfPath,
		orchestratorCtx:    orchestratorCtx,
		orchestratorCancel: orchestratorCancel,
		heatmap:            NewHeatmapModel(),
	}
}

// loadPortfolio picks the active portfolio source. Priority:
//  1. ./portfolio.yaml in CWD (useful for dev)
//  2. ~/.config/finsight/portfolio.yaml
//  3. cfg.Portfolio (embedded fallback) — returned with empty path so we
//     save through the config file on mutation.
func loadPortfolio(cfg *config.Config) (*portfolio.File, string) {
	for _, p := range []string{portfolio.LocalPath(), portfolio.DefaultPath()} {
		if portfolio.Exists(p) {
			if f, err := portfolio.Load(p); err == nil {
				return f, p
			}
		}
	}
	return &portfolio.File{Positions: append([]portfolio.Position(nil), cfg.Portfolio...)}, ""
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.fetchQuotes(),
		m.fetchAllCharts(),
		m.fetchAllIndicatorContexts(),
		m.startupRefreshCmd(),
		m.tickCmd(),
		m.startEarningsOrchestrator(),
	)
}

// cleanupTempDetailItem removes the transient detail symbol (if any) and restores
// the previous watchlist selection.
func (m Model) cleanupTempDetailItem() Model {
	if !m.tempDetailActive || m.tempDetailSymbol == "" {
		return m
	}

	tempIdx := -1
	for i := range m.items {
		if m.items[i].Symbol == m.tempDetailSymbol {
			tempIdx = i
			break
		}
	}

	if tempIdx >= 0 {
		m.items = append(m.items[:tempIdx], m.items[tempIdx+1:]...)
	}

	if len(m.items) == 0 {
		m.selected = 0
	} else {
		restore := m.tempDetailPrevSelected
		if restore < 0 {
			restore = 0
		}
		if restore >= len(m.items) {
			restore = len(m.items) - 1
		}
		m.selected = restore
	}

	m.tempDetailActive = false
	m.tempDetailSymbol = ""
	m.tempDetailPrevSelected = 0

	return m
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.heatmap.RebuildVisibleIdxs(m.width-2, m.height-3)
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case quotesMsg:
		return m.handleQuotes(msg)

	case chartMsg:
		return m.handleChart(msg)

	case indicatorContextMsg:
		return m.handleIndicatorContext(msg)

	case searchMsg:
		return m.handleSearchResults(msg)

	case keyStatsMsg:
		return m.handleKeyStats(msg)

	case financialsMsg:
		return m.handleFinancials(msg)

	case holdersMsg:
		return m.handleHolders(msg)

	case recommendedSymbolsMsg:
		return m.handleRecommendedSymbols(msg)

	case relatedQuotesMsg:
		return m.handleRelatedQuotes(msg)

	case relatedChartMsg:
		return m.handleRelatedChart(msg)

	case aiMsg:
		return m.handleAI(msg)

	case earningsMsg:
		return m.handleEarnings(msg)

	case summaryMsg:
		return m.handleSummary(msg)

	case heatmapFeedMsg:
		return m.handleHeatmapFeed(msg)

	case heatmapWatchlistQuotesMsg:
		return m.handleHeatmapWatchlistQuotes(msg)

	case portfolioAIMsg:
		return m.handlePortfolioAI(msg)

	case aiCmdResultMsg:
		return m.handleAICmdResult(msg)

	case aiCmdTickMsg:
		return m.handleAICmdTick()

	case aiCmdBlinkMsg:
		return m.handleAICmdBlink()

	case tickMsg:
		return m, tea.Batch(m.refreshQuotes(true), m.refreshAllCharts(true), m.tickCmd())

	case startupRefreshMsg:
		return m, tea.Batch(m.refreshQuotes(true), m.refreshAllCharts(true))
	}

	return m, nil
}

func (m Model) View() string {
	if m.quitting {
		return ""
	}
	if m.width == 0 {
		return "Loading..."
	}

	var output string
	switch m.mode {
	case viewWatchlist:
		output = m.viewWatchlist()
	case viewDetail:
		output = m.viewDetail()
	case viewSearch:
		output = m.viewSearch()
	case viewHelp:
		output = m.viewHelpScreen()
	case viewPortfolio:
		output = m.viewPortfolio()
	case viewHeatmap:
		output = m.RenderHeatmap(&m.heatmap, m.width, m.height)
	}

	// Truncate each line to terminal width to prevent wrapping on resize.
	if m.width > 0 {
		lines := strings.Split(output, "\n")
		for i, line := range lines {
			if lipgloss.Width(line) > m.width {
				lines[i] = ansi.Truncate(line, m.width, "")
			}
		}
		output = strings.Join(lines, "\n")
	}

	// Overlay the AI command window on top of whatever view is active.
	if m.aicmd.Active {
		baseLines := strings.Split(output, "\n")
		for len(baseLines) < m.height {
			baseLines = append(baseLines, "")
		}
		output = strings.Join(baseLines, "\n")
		output = m.overlayPopup(output, m.renderAICmdPopup())
	}

	// Overlay provider picker popup
	if m.showProviderPicker {
		baseLines := strings.Split(output, "\n")
		for len(baseLines) < m.height {
			baseLines = append(baseLines, "")
		}
		output = strings.Join(baseLines, "\n")
		output = m.overlayPopup(output, m.renderProviderPicker())
	}

	return output
}

// === View Renderers ===

func (m Model) currentTimeframe() timeframe {
	return timeframes[m.timeframeIdx]
}

func (m Model) viewWatchlist() string {
	// Title bar
	title := titleStyle.Render(" ◆ Finsight ")

	// Top-level view switch (Watchlist | Portfolio) inline with title.
	viewTabs := m.renderViewTabs()

	// Watchlist group tabs on their own row.
	groupTabs := m.renderGroupTabs()

	// Timeframe selector
	tfStr := m.renderTimeframeBar()

	updateStr := ""
	if !m.lastUpdate.IsZero() {
		updateStr = nameStyle.Render(fmt.Sprintf("Updated: %s", m.lastUpdate.Format("15:04:05")))
	}

	spacerWidth := m.width - lipgloss.Width(title) - lipgloss.Width(viewTabs) - lipgloss.Width(tfStr) - lipgloss.Width(updateStr) - 6
	if spacerWidth < 0 {
		spacerWidth = 0
	}
	titleBar := lipgloss.JoinHorizontal(lipgloss.Center,
		title, "  ", viewTabs, "  ", tfStr,
		lipgloss.NewStyle().Width(spacerWidth).Render(""),
		updateStr)

	// Watchlist
	list := RenderWatchlist(m.sortedItems(), m.selected, m.width, 2, m.cfg.ChartStyle, m.sortMode, m.currentTimeframe().Label)

	// Scroll
	listHeight := m.height - 4
	lines := strings.Split(list, "\n")
	if len(lines) > listHeight {
		itemLines := 4
		startLine := m.selected * itemLines
		if startLine+listHeight > len(lines) {
			startLine = len(lines) - listHeight
		}
		if startLine < 0 {
			startLine = 0
		}
		endLine := startLine + listHeight
		if endLine > len(lines) {
			endLine = len(lines)
		}
		lines = lines[startLine:endLine]
		list = strings.Join(lines, "\n")
	}

	help := m.renderHelp()
	errStr := ""
	if m.err != "" {
		errStr = errorStyle.Render(m.err)
	}

	parts := []string{titleBar}
	if groupTabs != "" {
		parts = append(parts, groupTabs)
	}
	parts = append(parts, list)
	if errStr != "" {
		parts = append(parts, errStr)
	}
	parts = append(parts, help)

	base := lipgloss.JoinVertical(lipgloss.Left, parts...)

	// Overlay summary popup if active
	if m.showSummary {
		// Pad base to full terminal height so overlay centers correctly
		baseLines := strings.Split(base, "\n")
		if len(baseLines) < m.height {
			for len(baseLines) < m.height {
				baseLines = append(baseLines, "")
			}
			base = strings.Join(baseLines, "\n")
		}
		popup := m.renderSummaryPopup()
		base = m.overlayPopup(base, popup)
	}

	return base
}

func (m Model) viewDetail() string {
	if m.selected >= len(m.items) {
		return ""
	}

	item := m.items[m.selected]

	// Symbol tabs
	tabBar := m.renderSymbolTabs()

	title := titleStyle.Render(fmt.Sprintf(" %s ", item.Symbol))
	tfStr := m.renderTimeframeBar()
	titleBar := lipgloss.JoinHorizontal(lipgloss.Center, title, "  ", tfStr)

	chartHeight := m.height - 13
	if len(m.relatedSymbols) > 0 {
		chartHeight -= 2 // room for related symbols row
	}
	if m.compareSymbol != nil {
		chartHeight -= 2 // room for compare legend
	}
	if m.showTechnicals {
		chartHeight -= 4 // room for technicals panel (MA, BB, RSI/Stoch, Pivot)
	}
	if chartHeight < 5 {
		chartHeight = 5
	}

	var chartView string
	if m.compareSymbol != nil && m.compareSymbol.ChartData != nil && item.ChartData != nil {
		chartView = m.renderCompareChart(item, chartHeight)
	} else {
		chartView = RenderDetailChart(item, m.width, chartHeight, m.currentTimeframe().Label, m.cfg.ChartStyle, m.showTechnicals, m.maMode)
	}

	marketStatus := renderMarketStatus(item.Quote)
	infoBar := renderDetailInfo(item.Quote, m.width)

	relatedBar := m.renderRelatedSymbols()

	helpParts := helpKeyStyle.Render("←→") + " timeframe  " +
		helpKeyStyle.Render("[]") + " prev/next symbol  " +
		hintKey("m", "arket data  ") +
		hintKey("t", "echnicals  ")
	if m.showTechnicals {
		helpParts += helpKeyStyle.Render("M") + " MA preset  "
	}
	if m.tempDetailActive && m.tempDetailSymbol == item.Symbol {
		helpParts += hintKey("w", "atchlist  ")
	}
	if m.llmClient != nil {
		helpParts += helpKeyStyle.Render("a") + " AI  "
		helpParts += hintKey("e", "arnings  ")
	}
	if len(m.relatedSymbols) > 0 {
		helpParts += helpKeyStyle.Render("Tab") + " related  "
		if m.relatedFocus >= 0 {
			helpParts += helpKeyStyle.Render("Enter") + " compare  "
		}
	}
	helpParts += helpKeyStyle.Render("Esc") + " back"
	help := helpStyle.Render(helpParts)

	parts := []string{tabBar, titleBar, "", chartView, marketStatus, infoBar}
	if m.showTechnicals {
		if tech := RenderTechnicalsPanel(item, m.width, m.maMode); tech != "" {
			parts = append(parts, tech)
		}
	}
	if m.compareSymbol != nil {
		// Compare legend
		mainSym := item.Symbol
		cmpSym := m.compareSymbol.Symbol
		legend := fmt.Sprintf("  %s %s   %s %s",
			lipgloss.NewStyle().Foreground(colorBlue).Render("━━"),
			priceStyle.Render(mainSym),
			lipgloss.NewStyle().Foreground(lipgloss.Color("#ff79c6")).Render("━━"),
			lipgloss.NewStyle().Foreground(lipgloss.Color("#ff79c6")).Bold(true).Render(cmpSym))
		parts = append(parts, legend)
	}
	if relatedBar != "" {
		parts = append(parts, relatedBar)
	}
	parts = append(parts, help)
	base := lipgloss.JoinVertical(lipgloss.Left, parts...)

	// Pad base to full terminal height so overlays center correctly
	baseLines := strings.Split(base, "\n")
	if len(baseLines) < m.height {
		for len(baseLines) < m.height {
			baseLines = append(baseLines, "")
		}
		base = strings.Join(baseLines, "\n")
	}

	// Overlay detail info popup if active
	if m.showMarketDepth && item.Quote != nil {
		popup := m.renderDetailPopup(item)
		base = m.overlayPopup(base, popup)
	}

	// Overlay AI analysis if active
	if m.showAI {
		popup := m.renderAIPopup()
		base = m.overlayPopup(base, popup)
	}

	// Overlay earnings report if active
	if m.showEarnings {
		popup := m.renderEarningsPopup()
		base = m.overlayPopup(base, popup)
	}

	// Overlay watchlist-add confirmation / group picker
	if m.confirmWatchlistAdd && m.tempDetailActive {
		var popupContent string
		if len(m.cfg.Watchlists) <= 1 {
			// Single group: simple confirm
			wlName := ""
			if len(m.cfg.Watchlists) == 1 {
				wlName = m.cfg.Watchlists[0].Name
			}
			popupContent = fmt.Sprintf("Add %s to watchlist %q?\n\n%s confirm  %s cancel",
				helpKeyStyle.Render(item.Symbol), wlName,
				helpKeyStyle.Render("Enter"), helpKeyStyle.Render("Esc"))
		} else {
			// Multi-group: show selectable list
			header := fmt.Sprintf("Add %s to watchlist:", helpKeyStyle.Render(item.Symbol))
			var lines []string
			lines = append(lines, header, "")
			for i, wl := range m.cfg.Watchlists {
				num := helpKeyStyle.Render(fmt.Sprintf("%d", i+1))
				prefix := "  "
				if i == m.confirmWatchlistSel {
					prefix = helpKeyStyle.Render("▸ ")
				}
				lines = append(lines, prefix+num+" "+wl.Name)
			}
			lines = append(lines, "",
				helpKeyStyle.Render("↑↓")+"/"+helpKeyStyle.Render("1-9")+" select  "+
					helpKeyStyle.Render("Enter")+" confirm  "+
					helpKeyStyle.Render("Esc")+" cancel")
			popupContent = strings.Join(lines, "\n")
		}
		base = m.overlayPopup(base, popupContent)
	}

	return base
}

func (m Model) renderRelatedSymbols() string {
	if len(m.relatedSymbols) == 0 {
		return ""
	}

	focusStyle := lipgloss.NewStyle().
		Background(ActiveTheme.Accent).
		Foreground(colorWhite).
		Bold(true).
		Padding(0, 1)
	compareActiveStyle := lipgloss.NewStyle().
		Background(ActiveTheme.Accent2).
		Foreground(colorWhite).
		Bold(true).
		Padding(0, 1)

	var parts []string
	for i, rs := range m.relatedSymbols {
		sparkWidth := 10
		sparkStr := strings.Repeat(" ", sparkWidth)

		if rs.ChartData != nil && len(rs.ChartData.Closes) > 0 {
			bars := chart.SparklineBars(rs.ChartData.Closes, sparkWidth)
			var sb strings.Builder
			for _, b := range bars {
				if b.Up {
					sb.WriteString(changeUpStyle.Render(string(b.Char)))
				} else {
					sb.WriteString(changeDownStyle.Render(string(b.Char)))
				}
			}
			sparkStr = sb.String()
		}

		symText := fmt.Sprintf("%-5s", rs.Symbol)
		chgText := ""
		if rs.Quote != nil {
			chg := rs.Quote.ChangePercent
			sign := "+"
			style := changeUpStyle
			if chg < 0 {
				sign = ""
				style = changeDownStyle
			}
			chgText = style.Render(fmt.Sprintf("%s%.1f%%", sign, chg))
		}

		isComparing := m.compareSymbol != nil && m.compareSymbol.Symbol == rs.Symbol
		isFocused := i == m.relatedFocus

		var cell string
		if isComparing {
			sym := lipgloss.NewStyle().Background(ActiveTheme.Accent2).Foreground(colorBg).Bold(true).Render(symText)
			cell = compareActiveStyle.Render(fmt.Sprintf("%s %s %s", sparkStr, sym, chgText))
		} else if isFocused {
			sym := lipgloss.NewStyle().Background(ActiveTheme.Accent).Foreground(colorBg).Bold(true).Render(symText)
			cell = focusStyle.Render(fmt.Sprintf("%s %s %s", sparkStr, sym, chgText))
		} else {
			sym := nameStyle.Render(symText)
			cell = fmt.Sprintf("%s %s %s", sparkStr, sym, chgText)
		}

		parts = append(parts, cell)
	}

	header := nameStyle.Render("  Related: ")
	row := header + strings.Join(parts, " ")
	return row
}

func (m Model) renderCompareChart(item WatchlistItem, height int) string {
	cmp := m.compareSymbol
	if cmp == nil || cmp.ChartData == nil || item.ChartData == nil {
		return ""
	}

	mainData := item.ChartData.Closes
	cmpData := cmp.ChartData.Closes

	chartWidth := m.width - 6
	if chartWidth < 20 {
		chartWidth = 20
	}

	// Header: both symbols with price and change
	mainHeader := lipgloss.NewStyle().Foreground(colorBlue).Bold(true).Render(item.Symbol)
	cmpHeader := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff79c6")).Bold(true).Render(cmp.Symbol)

	mainPriceStr := ""
	if item.Quote != nil {
		chgStyle := changeUpStyle
		sign := "+"
		if item.Quote.ChangePercent < 0 {
			chgStyle = changeDownStyle
			sign = ""
		}
		mainPriceStr = fmt.Sprintf(" %s %s",
			priceStyle.Render(fmt.Sprintf("$%.2f", item.Quote.Price)),
			chgStyle.Render(fmt.Sprintf("%s%.2f%%", sign, item.Quote.ChangePercent)))
	}

	cmpPriceStr := ""
	if cmp.Quote != nil {
		chgStyle := changeUpStyle
		sign := "+"
		if cmp.Quote.ChangePercent < 0 {
			chgStyle = changeDownStyle
			sign = ""
		}
		cmpPriceStr = fmt.Sprintf(" %s %s",
			priceStyle.Render(fmt.Sprintf("$%.2f", cmp.Quote.Price)),
			chgStyle.Render(fmt.Sprintf("%s%.2f%%", sign, cmp.Quote.ChangePercent)))
	}

	headerLine := fmt.Sprintf("  %s%s  vs  %s%s  %s",
		mainHeader, mainPriceStr,
		cmpHeader, cmpPriceStr,
		nameStyle.Render("["+m.currentTimeframe().Label+"]"))

	// Compute percentage change for Y-axis labels
	mainBase := mainData[0]
	cmpBase := cmpData[0]
	if mainBase == 0 {
		mainBase = 1
	}
	if cmpBase == 0 {
		cmpBase = 1
	}

	// Find global pct range
	allPct := make([]float64, 0, len(mainData)+len(cmpData))
	for _, v := range mainData {
		allPct = append(allPct, (v-mainBase)/mainBase*100)
	}
	for _, v := range cmpData {
		allPct = append(allPct, (v-cmpBase)/cmpBase*100)
	}
	pctMin, pctMax := allPct[0], allPct[0]
	for _, v := range allPct {
		if v < pctMin {
			pctMin = v
		}
		if v > pctMax {
			pctMax = v
		}
	}

	// Y-axis labels
	yAxisWidth := 7
	cWidth := chartWidth - yAxisWidth
	if cWidth < 10 {
		cWidth = 10
	}
	chartH := height - 3 // header + bottom padding
	if chartH < 3 {
		chartH = 3
	}

	line1, line2 := chart.RenderCompareLines(mainData, cmpData, cWidth, chartH)

	mainColor := lipgloss.NewStyle().Foreground(colorBlue)
	cmpColor := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff79c6"))
	bothColor := lipgloss.NewStyle().Foreground(lipgloss.Color("#bd93f9"))

	var rows []string
	rows = append(rows, headerLine)

	for r := 0; r < chartH; r++ {
		// Y-axis label: percentage change
		pctVal := pctMax - (pctMax-pctMin)*float64(r)/float64(chartH-1)
		yLabel := nameStyle.Render(fmt.Sprintf("%+6.1f%% ", pctVal))

		var rowBuf strings.Builder
		rowBuf.WriteString("  ")
		rowBuf.WriteString(yLabel)

		for c := 0; c < cWidth; c++ {
			var r1, r2 rune
			if r < len(line1.Grid) && c < len(line1.Grid[r]) {
				r1 = line1.Grid[r][c]
			}
			if r < len(line2.Grid) && c < len(line2.Grid[r]) {
				r2 = line2.Grid[r][c]
			}

			has1 := r1 != 0
			has2 := r2 != 0

			if has1 && has2 {
				// Both series have dots in this cell — merge and color purple
				merged := 0x2800 | (r1 - 0x2800) | (r2 - 0x2800)
				rowBuf.WriteString(bothColor.Render(string(merged)))
			} else if has1 {
				rowBuf.WriteString(mainColor.Render(string(r1)))
			} else if has2 {
				rowBuf.WriteString(cmpColor.Render(string(r2)))
			} else {
				rowBuf.WriteRune(' ')
			}
		}
		rows = append(rows, rowBuf.String())
	}

	// X-axis: zero line indicator
	zeroPct := 0.0
	if pctMin <= zeroPct && zeroPct <= pctMax {
		// already shown via Y-axis labels
	}

	return strings.Join(rows, "\n")
}

func (m Model) renderSymbolTabs() string {
	var tabs []string
	for i, item := range m.items {
		sym := item.Symbol
		chgStr := ""
		if item.Quote != nil {
			chg := item.Quote.ChangePercent
			sign := "+"
			chgStyle := changeUpStyle
			if chg < 0 {
				sign = ""
				chgStyle = changeDownStyle
			}
			chgStr = " " + chgStyle.Render(fmt.Sprintf("%s%.1f%%", sign, chg))
		}
		if i == m.selected {
			tabs = append(tabs, helpKeyStyle.Render(sym)+chgStr)
		} else {
			tabs = append(tabs, nameStyle.Render(sym)+chgStr)
		}
	}
	tabLine := strings.Join(tabs, nameStyle.Render(" │ "))

	tabStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorDim).
		Padding(0, 1).
		Width(m.width - 4)

	return tabStyle.Render(tabLine)
}

func (m Model) renderMarketDepth(item WatchlistItem) string {
	q := item.Quote
	if q == nil {
		return ""
	}

	titleLine := helpKeyStyle.Render(fmt.Sprintf("  %s — Market Depth & Price Detail", q.Symbol))

	row := func(label string, value string) string {
		return fmt.Sprintf("  %s  %s", nameStyle.Render(fmt.Sprintf("%-18s", label)), priceStyle.Render(value))
	}

	// ── Left column: Price, Bid/Ask, Volume ──
	var left []string
	left = append(left, helpKeyStyle.Render("  Price"))
	left = append(left, row("Last Price", formatFloat(q.Price)))
	if q.PreviousClose > 0 {
		left = append(left, row("Previous Close", formatFloat(q.PreviousClose)))
	}
	left = append(left, row("Open", formatFloat(q.Open)))
	left = append(left, row("Day Range", fmt.Sprintf("%s — %s", formatFloat(q.DayLow), formatFloat(q.DayHigh))))
	if q.FiftyTwoWeekLow > 0 || q.FiftyTwoWeekHigh > 0 {
		left = append(left, row("52-Week Range", fmt.Sprintf("%s — %s", formatFloat(q.FiftyTwoWeekLow), formatFloat(q.FiftyTwoWeekHigh))))
	}

	left = append(left, "")
	left = append(left, helpKeyStyle.Render("  Bid / Ask"))
	bidStr := formatFloat(q.Bid)
	if q.BidSize > 0 {
		bidStr += fmt.Sprintf(" × %d", q.BidSize)
	}
	askStr := formatFloat(q.Ask)
	if q.AskSize > 0 {
		askStr += fmt.Sprintf(" × %d", q.AskSize)
	}
	left = append(left, row("Bid", bidStr))
	left = append(left, row("Ask", askStr))
	spread := q.Ask - q.Bid
	if q.Bid > 0 && q.Ask > 0 {
		spreadPct := (spread / q.Ask) * 100
		left = append(left, row("Spread", fmt.Sprintf("%s (%.3f%%)", formatFloat(spread), spreadPct)))
	}

	left = append(left, "")
	left = append(left, helpKeyStyle.Render("  Volume"))
	left = append(left, row("Volume", formatVolume(q.Volume)))
	left = append(left, row("Avg Volume", formatVolume(q.AvgVolume)))

	// ── Right column: Market State, Pre/Post Market, Fundamentals ──
	var right []string

	// Market state
	stateStr := marketClosedStyle.Render(q.MarketState)
	if q.MarketState == "REGULAR" {
		stateStr = marketOpenStyle.Render("OPEN")
	} else if q.MarketState == "PRE" {
		stateStr = helpKeyStyle.Render("PRE-MARKET")
	} else if q.MarketState == "POST" || q.MarketState == "POSTPOST" {
		stateStr = helpKeyStyle.Render("AFTER HOURS")
	}
	right = append(right, helpKeyStyle.Render("  Market State"))
	right = append(right, fmt.Sprintf("  %s  %s", nameStyle.Render("Status           "), stateStr))

	if q.PreMarketPrice > 0 {
		right = append(right, "")
		right = append(right, helpKeyStyle.Render("  Pre-Market"))
		right = append(right, row("Price", formatFloat(q.PreMarketPrice)))
		chgSign := "+"
		if q.PreMarketChange < 0 {
			chgSign = ""
		}
		right = append(right, row("Change", fmt.Sprintf("%s%.2f (%.2f%%)", chgSign, q.PreMarketChange, q.PreMarketChangePercent)))
	}

	if q.PostMarketPrice > 0 {
		right = append(right, "")
		right = append(right, helpKeyStyle.Render("  After Hours"))
		right = append(right, row("Price", formatFloat(q.PostMarketPrice)))
		chgSign := "+"
		if q.PostMarketChange < 0 {
			chgSign = ""
		}
		right = append(right, row("Change", fmt.Sprintf("%s%.2f (%.2f%%)", chgSign, q.PostMarketChange, q.PostMarketChangePercent)))
	}

	right = append(right, "")
	right = append(right, helpKeyStyle.Render("  Fundamentals"))
	if q.PE > 0 {
		right = append(right, row("P/E (TTM)", fmt.Sprintf("%.2f", q.PE)))
	}
	if q.ForwardPE > 0 {
		right = append(right, row("Forward P/E", fmt.Sprintf("%.2f", q.ForwardPE)))
	}
	if q.EPS != 0 {
		right = append(right, row("EPS (TTM)", fmt.Sprintf("%.2f", q.EPS)))
	}
	if q.BookValue > 0 {
		right = append(right, row("Book Value", fmt.Sprintf("%.2f", q.BookValue)))
	}
	if q.PriceToBook > 0 {
		right = append(right, row("Price/Book", fmt.Sprintf("%.2f", q.PriceToBook)))
	}
	if q.DividendYield > 0 {
		right = append(right, row("Dividend Yield", fmt.Sprintf("%.2f%%", q.DividendYield*100)))
	}
	if q.MarketCap > 0 {
		right = append(right, row("Market Cap", formatMarketCap(q.MarketCap)))
	}

	// ── Compose two columns ──
	leftBlock := strings.Join(left, "\n")
	rightBlock := strings.Join(right, "\n")
	columns := lipgloss.JoinHorizontal(lipgloss.Top, leftBlock, "    ", rightBlock)

	var lines []string
	lines = append(lines, titleLine)
	lines = append(lines, separatorStyle.Render("  "+strings.Repeat("─", 70)))
	lines = append(lines, "")
	lines = append(lines, columns)
	lines = append(lines, "")
	lines = append(lines, helpStyle.Render("  Tab/1-3 switch tabs · m/Esc close"))

	return strings.Join(lines, "\n")
}

func (m Model) renderDetailPopup(item WatchlistItem) string {
	tabNames := []string{"Market", "Financials", "Holders"}
	var tabParts []string
	for i, name := range tabNames {
		if i == m.popupTab {
			tabParts = append(tabParts, helpKeyStyle.Render(fmt.Sprintf(" %d·%s ", i+1, name)))
		} else {
			tabParts = append(tabParts, nameStyle.Render(fmt.Sprintf(" %d·%s ", i+1, name)))
		}
	}
	tabBar := strings.Join(tabParts, nameStyle.Render("│"))

	var content string
	switch m.popupTab {
	case 0:
		content = m.renderMarketDepth(item)
	case 1:
		content = m.renderFinancialsTab(item)
	case 2:
		content = m.renderHoldersTab(item)
	}

	return tabBar + "\n" + content
}

func (m Model) renderFinancialsTab(item WatchlistItem) string {
	q := item.Quote
	sym := ""
	if q != nil {
		sym = q.Symbol
	}
	titleLine := helpKeyStyle.Render(fmt.Sprintf("  %s — Financials", sym))

	var lines []string
	lines = append(lines, titleLine)
	lines = append(lines, separatorStyle.Render("  "+strings.Repeat("─", 80)))

	fd := item.Financials
	if fd == nil {
		lines = append(lines, "")
		lines = append(lines, nameStyle.Render("  Loading financials..."))
		lines = append(lines, "")
		lines = append(lines, helpStyle.Render("  Tab/1-3 switch tabs · m/Esc close"))
		return strings.Join(lines, "\n")
	}

	row := func(label string, value string) string {
		return fmt.Sprintf("  %s  %s", nameStyle.Render(fmt.Sprintf("%-24s", label)), priceStyle.Render(value))
	}

	fmtAmount := func(v int64) string {
		abs := v
		if abs < 0 {
			abs = -abs
		}
		sign := ""
		if v < 0 {
			sign = "-"
		}
		switch {
		case abs >= 1_000_000_000_000:
			return fmt.Sprintf("%s%.2fT", sign, float64(abs)/1e12)
		case abs >= 1_000_000_000:
			return fmt.Sprintf("%s%.2fB", sign, float64(abs)/1e9)
		case abs >= 1_000_000:
			return fmt.Sprintf("%s%.2fM", sign, float64(abs)/1e6)
		default:
			return fmt.Sprintf("%s%d", sign, abs)
		}
	}

	fmtPct := func(v float64) string {
		style := changeUpStyle
		s := "+"
		if v < 0 {
			style = changeDownStyle
			s = ""
		}
		return style.Render(fmt.Sprintf("%s%.2f%%", s, v*100))
	}

	yoyDelta := func(cur, prev int64) string {
		if prev == 0 {
			return "  —"
		}
		pct := float64(cur-prev) / float64(prev) * 100
		style := changeUpStyle
		s := "+"
		if pct < 0 {
			style = changeDownStyle
			s = ""
		}
		return style.Render(fmt.Sprintf("%s%.1f%%", s, pct))
	}

	// ══════════════════════════════════════════════════
	// LEFT COLUMN: Revenue chart, Key Metrics, Balance, Valuation, EPS
	// ══════════════════════════════════════════════════
	var left []string

	// ── Revenue vs. Earnings Bar Chart ──
	periods := fd.Quarterly
	if len(periods) == 0 {
		periods = fd.Yearly
	}
	if len(periods) > 0 {
		left = append(left, helpKeyStyle.Render("  Revenue vs. Earnings"))

		maxVal := int64(0)
		for _, p := range periods {
			if p.Revenue > maxVal {
				maxVal = p.Revenue
			}
		}

		barWidth := 8
		chartHeight := 8
		if maxVal > 0 {
			for row := chartHeight; row >= 1; row-- {
				threshold := float64(row) / float64(chartHeight)
				var rowStr strings.Builder
				rowStr.WriteString("  ")
				yVal := float64(maxVal) * threshold
				yLabel := fmtAmount(int64(yVal))
				rowStr.WriteString(nameStyle.Render(fmt.Sprintf("%7s ", yLabel)))

				for _, p := range periods {
					revH := float64(p.Revenue) / float64(maxVal) * float64(chartHeight)
					earnH := float64(p.Earnings) / float64(maxVal) * float64(chartHeight)

					revChar := " "
					if float64(row) <= revH {
						revChar = helpKeyStyle.Render("█")
					} else if float64(row)-0.5 <= revH {
						revChar = helpKeyStyle.Render("▄")
					}

					earnChar := " "
					if float64(row) <= earnH {
						earnChar = changeUpStyle.Render("█")
					} else if float64(row)-0.5 <= earnH {
						earnChar = changeUpStyle.Render("▄")
					}

					rowStr.WriteString(strings.Repeat(revChar, 4))
					rowStr.WriteString(" ")
					rowStr.WriteString(strings.Repeat(earnChar, 3))
					_ = barWidth
					rowStr.WriteString("  ")
				}
				left = append(left, rowStr.String())
			}

			var xAxis strings.Builder
			xAxis.WriteString("  ")
			xAxis.WriteString(strings.Repeat(" ", 8))
			for _, p := range periods {
				label := p.Date
				if len(label) > 8 {
					label = label[:8]
				}
				xAxis.WriteString(nameStyle.Render(fmt.Sprintf("%-10s", label)))
			}
			left = append(left, xAxis.String())

			left = append(left, "  "+strings.Repeat(" ", 8)+
				helpKeyStyle.Render("██")+" Revenue  "+
				changeUpStyle.Render("██")+" Earnings")
		}
		left = append(left, "")
	}

	// ── Key Financial Metrics ──
	left = append(left, helpKeyStyle.Render("  Key Metrics"))
	if fd.RevenueTTM > 0 {
		left = append(left, row("Revenue (TTM)", fmtAmount(fd.RevenueTTM)))
	}
	if fd.EBITDA > 0 {
		left = append(left, row("EBITDA", fmtAmount(fd.EBITDA)))
	}
	if fd.ProfitMargin != 0 {
		left = append(left, row("Profit Margin", fmtPct(fd.ProfitMargin)))
	}
	if fd.GrossMargins != 0 {
		left = append(left, row("Gross Margin", fmtPct(fd.GrossMargins)))
	}
	if fd.OperatingMargins != 0 {
		left = append(left, row("Operating Margin", fmtPct(fd.OperatingMargins)))
	}
	if fd.EarningsGrowth != 0 {
		left = append(left, row("Earnings Growth (QoQ)", fmtPct(fd.EarningsGrowth)))
	}
	if fd.RevenueGrowth != 0 {
		left = append(left, row("Revenue Growth (QoQ)", fmtPct(fd.RevenueGrowth)))
	}
	if fd.ReturnOnEquity != 0 {
		left = append(left, row("Return on Equity", fmtPct(fd.ReturnOnEquity)))
	}

	// ── Balance Sheet & Cash Flow ──
	if fd.TotalCash > 0 || fd.TotalDebt > 0 || fd.FreeCashflow != 0 {
		left = append(left, "")
		left = append(left, helpKeyStyle.Render("  Balance Sheet & Cash Flow"))
		if fd.TotalCash > 0 {
			left = append(left, row("Total Cash (mrq)", fmtAmount(fd.TotalCash)))
		}
		if fd.TotalDebt > 0 {
			left = append(left, row("Total Debt (mrq)", fmtAmount(fd.TotalDebt)))
		}
		if fd.TotalCash > 0 && fd.TotalDebt > 0 {
			netCash := fd.TotalCash - fd.TotalDebt
			label := "Net Cash"
			if netCash < 0 {
				label = "Net Debt"
			}
			left = append(left, row(label, fmtAmount(netCash)))
		}
		if fd.DebtToEquity > 0 {
			left = append(left, row("Debt/Equity (mrq)", fmt.Sprintf("%.2f", fd.DebtToEquity)))
		}
		if fd.CurrentRatio > 0 {
			left = append(left, row("Current Ratio (mrq)", fmt.Sprintf("%.2f", fd.CurrentRatio)))
		}
		if fd.FreeCashflow != 0 {
			left = append(left, row("Free Cash Flow", fmtAmount(fd.FreeCashflow)))
		}
		if fd.OperatingCashflow != 0 {
			left = append(left, row("Operating Cash Flow", fmtAmount(fd.OperatingCashflow)))
		}
	}

	// ── Valuation ──
	if fd.PriceToSales > 0 || fd.EnterpriseValue > 0 {
		left = append(left, "")
		left = append(left, helpKeyStyle.Render("  Valuation"))
		if fd.PriceToSales > 0 {
			left = append(left, row("Price/Sales (TTM)", fmt.Sprintf("%.2f", fd.PriceToSales)))
		}
		if fd.EnterpriseValue > 0 {
			left = append(left, row("Enterprise Value", fmtAmount(fd.EnterpriseValue)))
		}
		if fd.EnterpriseToRevenue > 0 {
			left = append(left, row("EV/Revenue", fmt.Sprintf("%.2f", fd.EnterpriseToRevenue)))
		}
		if fd.EnterpriseToEbitda > 0 {
			left = append(left, row("EV/EBITDA", fmt.Sprintf("%.2f", fd.EnterpriseToEbitda)))
		}
	}

	// ── EPS Trend ──
	if len(fd.EPSHistory) > 0 {
		left = append(left, "")
		left = append(left, helpKeyStyle.Render("  EPS Trend (Last 4 Quarters)"))

		hdr := fmt.Sprintf("  %-12s %10s %10s %10s %12s",
			nameStyle.Render("Quarter"),
			nameStyle.Render("Actual"),
			nameStyle.Render("Estimate"),
			nameStyle.Render("Diff"),
			nameStyle.Render("Surprise"))
		left = append(left, hdr)

		var epsVals []float64
		for _, ep := range fd.EPSHistory {
			diff := ep.EPSActual - ep.EPSEstimate
			diffStyle := changeUpStyle
			diffSign := "+"
			if diff < 0 {
				diffStyle = changeDownStyle
				diffSign = ""
			}
			surpStyle := changeUpStyle
			surpSign := "+"
			if ep.SurprisePercent < 0 {
				surpStyle = changeDownStyle
				surpSign = ""
			}

			left = append(left, fmt.Sprintf("  %-12s %10s %10s %10s %12s",
				priceStyle.Render(ep.Quarter),
				priceStyle.Render(fmt.Sprintf("$%.2f", ep.EPSActual)),
				nameStyle.Render(fmt.Sprintf("$%.2f", ep.EPSEstimate)),
				diffStyle.Render(fmt.Sprintf("%s$%.2f", diffSign, diff)),
				surpStyle.Render(fmt.Sprintf("%s%.2f%%", surpSign, ep.SurprisePercent*100))))
			epsVals = append(epsVals, ep.EPSActual)
		}

		if len(epsVals) >= 2 {
			left = append(left, "  "+nameStyle.Render("EPS Trend       ")+renderMiniBar(epsVals))
		}
	}

	// ══════════════════════════════════════════════════
	// RIGHT COLUMN: Annual, Quarterly, Analyst
	// ══════════════════════════════════════════════════
	var right []string

	// ── Annual Revenue & Earnings Table ──
	if len(fd.Yearly) > 0 {
		right = append(right, helpKeyStyle.Render("  Annual"))

		hdr := fmt.Sprintf("  %-10s %12s %12s %12s %12s",
			nameStyle.Render("Year"),
			nameStyle.Render("Revenue"),
			nameStyle.Render("Rev YoY"),
			nameStyle.Render("Earnings"),
			nameStyle.Render("Earn YoY"))
		right = append(right, hdr)

		for i, y := range fd.Yearly {
			revYoY := "  —"
			earnYoY := "  —"
			if i > 0 {
				revYoY = yoyDelta(y.Revenue, fd.Yearly[i-1].Revenue)
				earnYoY = yoyDelta(y.Earnings, fd.Yearly[i-1].Earnings)
			}
			right = append(right, fmt.Sprintf("  %-10s %12s %12s %12s %12s",
				priceStyle.Render(y.Date),
				priceStyle.Render(fmtAmount(y.Revenue)), revYoY,
				priceStyle.Render(fmtAmount(y.Earnings)), earnYoY))
		}
		right = append(right, "")
	}

	// ── Quarterly Revenue & Earnings Table ──
	if len(fd.Quarterly) > 0 {
		right = append(right, helpKeyStyle.Render("  Quarterly"))

		hdr := fmt.Sprintf("  %-10s %12s %12s %12s %12s",
			nameStyle.Render("Quarter"),
			nameStyle.Render("Revenue"),
			nameStyle.Render("Rev QoQ"),
			nameStyle.Render("Earnings"),
			nameStyle.Render("Earn QoQ"))
		right = append(right, hdr)

		for i, q := range fd.Quarterly {
			revQoQ := "  —"
			earnQoQ := "  —"
			if i > 0 {
				revQoQ = yoyDelta(q.Revenue, fd.Quarterly[i-1].Revenue)
				earnQoQ = yoyDelta(q.Earnings, fd.Quarterly[i-1].Earnings)
			}
			right = append(right, fmt.Sprintf("  %-10s %12s %12s %12s %12s",
				priceStyle.Render(q.Date),
				priceStyle.Render(fmtAmount(q.Revenue)), revQoQ,
				priceStyle.Render(fmtAmount(q.Earnings)), earnQoQ))
		}
		right = append(right, "")
	}

	// ── Analyst ──
	hasTargets := fd.TargetMeanPrice > 0
	hasRecs := len(fd.RecommendationTrend) > 0
	if hasTargets || hasRecs {
		right = append(right, helpKeyStyle.Render("  Analyst"))
	}

	if hasTargets {
		right = append(right, "")
		currentPrice := 0.0
		if q != nil {
			currentPrice = q.Price
		}
		right = append(right, row("Current Price", fmt.Sprintf("$%.2f", currentPrice)))
		right = append(right, row("Mean Target", fmt.Sprintf("$%.2f", fd.TargetMeanPrice)))
		right = append(right, row("Target Range",
			fmt.Sprintf("$%.2f — $%.2f", fd.TargetLowPrice, fd.TargetHighPrice)))
		if currentPrice > 0 && fd.TargetMeanPrice > 0 {
			upside := (fd.TargetMeanPrice - currentPrice) / currentPrice * 100
			upsideStr := fmt.Sprintf("%+.1f%%", upside)
			if upside >= 0 {
				right = append(right, fmt.Sprintf("  %s  %s",
					nameStyle.Render(fmt.Sprintf("%-24s", "Upside/Downside")),
					changeUpStyle.Render(upsideStr)))
			} else {
				right = append(right, fmt.Sprintf("  %s  %s",
					nameStyle.Render(fmt.Sprintf("%-24s", "Upside/Downside")),
					changeDownStyle.Render(upsideStr)))
			}
		}
		if fd.NumberOfAnalysts > 0 {
			right = append(right, row("# Analysts", fmt.Sprintf("%d", fd.NumberOfAnalysts)))
		}
		if fd.RecommendationKey != "" {
			rec := strings.ToUpper(strings.ReplaceAll(fd.RecommendationKey, "_", " "))
			right = append(right, row("Consensus", rec))
		}
	}

	if hasRecs {
		right = append(right, "")
		right = append(right, helpKeyStyle.Render("  Analyst Recommendations"))

		strongBuyStyle := lipgloss.NewStyle().Foreground(colorGreen)
		buyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#aad576"))
		holdStyle := lipgloss.NewStyle().Foreground(colorYellow)
		sellStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff8c42"))
		strongSellStyle := lipgloss.NewStyle().Foreground(colorRed)

		now := time.Now()
		monthLabel := func(period string) string {
			offset := 0
			if period == "-1m" {
				offset = -1
			} else if period == "-2m" {
				offset = -2
			} else if period == "-3m" {
				offset = -3
			}
			t := now.AddDate(0, offset, 0)
			return t.Format("Jan")
		}

		maxTotal := 0
		for _, rp := range fd.RecommendationTrend {
			total := rp.StrongBuy + rp.Buy + rp.Hold + rp.Sell + rp.StrongSell
			if total > maxTotal {
				maxTotal = total
			}
		}

		chartHeight := 10
		barWidth := 5

		for row := chartHeight; row >= 1; row-- {
			var rowBuf strings.Builder
			rowBuf.WriteString("    ")
			for i := len(fd.RecommendationTrend) - 1; i >= 0; i-- {
				rp := fd.RecommendationTrend[i]
				total := rp.StrongBuy + rp.Buy + rp.Hold + rp.Sell + rp.StrongSell
				if total == 0 || maxTotal == 0 {
					rowBuf.WriteString(strings.Repeat(" ", barWidth+2))
					continue
				}

				scale := float64(chartHeight) / float64(maxTotal)
				hSS := float64(rp.StrongSell) * scale
				hSell := float64(rp.Sell) * scale
				hHold := float64(rp.Hold) * scale
				hBuy := float64(rp.Buy) * scale

				pos := float64(row)
				var ch string
				if pos <= hSS {
					ch = strongSellStyle.Render("█")
				} else if pos <= hSS+hSell {
					ch = sellStyle.Render("█")
				} else if pos <= hSS+hSell+hHold {
					ch = holdStyle.Render("█")
				} else if pos <= hSS+hSell+hHold+hBuy {
					ch = buyStyle.Render("█")
				} else if pos <= float64(total)*scale {
					ch = strongBuyStyle.Render("█")
				} else {
					ch = " "
				}

				bar := strings.Repeat(ch, barWidth)
				rowBuf.WriteString(" " + bar + " ")
			}
			right = append(right, rowBuf.String())
		}

		var countBuf strings.Builder
		countBuf.WriteString("    ")
		for i := len(fd.RecommendationTrend) - 1; i >= 0; i-- {
			rp := fd.RecommendationTrend[i]
			total := rp.StrongBuy + rp.Buy + rp.Hold + rp.Sell + rp.StrongSell
			label := fmt.Sprintf("%d", total)
			pad := barWidth - len(label)
			lPad := pad / 2
			rPad := pad - lPad
			countBuf.WriteString(" " + strings.Repeat(" ", lPad) + priceStyle.Render(label) + strings.Repeat(" ", rPad) + " ")
		}
		right = append(right, countBuf.String())

		var monthBuf strings.Builder
		monthBuf.WriteString("    ")
		for i := len(fd.RecommendationTrend) - 1; i >= 0; i-- {
			rp := fd.RecommendationTrend[i]
			ml := monthLabel(rp.Period)
			pad := barWidth - len(ml)
			lPad := pad / 2
			rPad := pad - lPad
			monthBuf.WriteString(" " + strings.Repeat(" ", lPad) + nameStyle.Render(ml) + strings.Repeat(" ", rPad) + " ")
		}
		right = append(right, monthBuf.String())

		right = append(right, "")
		right = append(right, fmt.Sprintf("    %s Strong Buy  %s Buy  %s Hold  %s Sell  %s Strong Sell",
			strongBuyStyle.Render("●"),
			buyStyle.Render("●"),
			holdStyle.Render("●"),
			sellStyle.Render("●"),
			strongSellStyle.Render("●")))
	}

	if len(fd.Yearly) == 0 && len(fd.Quarterly) == 0 && fd.RevenueTTM == 0 {
		left = append(left, "")
		left = append(left, nameStyle.Render("  No financial data available"))
	}

	// ── Compose two columns ──
	leftBlock := strings.Join(left, "\n")
	rightBlock := strings.Join(right, "\n")
	columns := lipgloss.JoinHorizontal(lipgloss.Top, leftBlock, "    ", rightBlock)

	lines = append(lines, "")
	lines = append(lines, columns)
	lines = append(lines, "")
	lines = append(lines, helpStyle.Render("  Tab/1-3 switch tabs · m/Esc close"))
	return strings.Join(lines, "\n")
}

func (m Model) renderHoldersTab(item WatchlistItem) string {
	q := item.Quote
	sym := ""
	if q != nil {
		sym = q.Symbol
	}
	titleLine := helpKeyStyle.Render(fmt.Sprintf("  %s — Holders", sym))

	var lines []string
	lines = append(lines, titleLine)
	lines = append(lines, separatorStyle.Render("  "+strings.Repeat("─", 50)))

	hd := item.Holders
	if hd == nil {
		lines = append(lines, "")
		lines = append(lines, nameStyle.Render("  Loading holder data..."))
		lines = append(lines, "")
		lines = append(lines, helpStyle.Render("  Tab/1-3 switch tabs · m/Esc close"))
		return strings.Join(lines, "\n")
	}

	row := func(label string, value string) string {
		return fmt.Sprintf("  %s  %s", nameStyle.Render(fmt.Sprintf("%-22s", label)), priceStyle.Render(value))
	}

	// Ownership breakdown
	lines = append(lines, "")
	lines = append(lines, helpKeyStyle.Render("  Ownership Breakdown"))
	lines = append(lines, row("Insiders", fmt.Sprintf("%.2f%%", hd.InsiderPctHeld*100)))
	lines = append(lines, row("Institutions", fmt.Sprintf("%.2f%%", hd.InstitutionPctHeld*100)))
	if hd.InstitutionCount > 0 {
		lines = append(lines, row("# Institutions", fmt.Sprintf("%d", hd.InstitutionCount)))
	}

	// Visual bar: insiders vs institutions vs public
	insBar := int(hd.InsiderPctHeld * 40)
	instBar := int(hd.InstitutionPctHeld * 40)
	pubBar := 40 - insBar - instBar
	if pubBar < 0 {
		pubBar = 0
	}
	barStr := changeDownStyle.Render(strings.Repeat("█", insBar)) +
		helpKeyStyle.Render(strings.Repeat("█", instBar)) +
		nameStyle.Render(strings.Repeat("░", pubBar))
	lines = append(lines, "  "+barStr)
	lines = append(lines, "  "+changeDownStyle.Render("█")+" Insider  "+
		helpKeyStyle.Render("█")+" Institution  "+
		nameStyle.Render("░")+" Public")

	// Top institutional holders
	if len(hd.TopInstitutions) > 0 {
		lines = append(lines, "")
		lines = append(lines, helpKeyStyle.Render("  Top Institutional Holders"))
		hdr := fmt.Sprintf("  %-26s %12s %10s %12s",
			nameStyle.Render("Name"),
			nameStyle.Render("Shares"),
			nameStyle.Render("% Held"),
			nameStyle.Render("Report Date"))
		lines = append(lines, hdr)

		for _, h := range hd.TopInstitutions {
			name := h.Name
			if len(name) > 24 {
				name = name[:24] + ".."
			}
			lines = append(lines, fmt.Sprintf("  %-26s %12s %9.2f%% %12s",
				priceStyle.Render(name),
				priceStyle.Render(formatHolderShares(h.Shares)),
				h.PctHeld*100,
				nameStyle.Render(h.ReportDate)))
		}
	}

	// Insiders
	if len(hd.Insiders) > 0 {
		lines = append(lines, "")
		lines = append(lines, helpKeyStyle.Render("  Insider Holdings"))
		hdr := fmt.Sprintf("  %-34s %14s %12s",
			nameStyle.Render("Name"),
			nameStyle.Render("Shares"),
			nameStyle.Render("Last Trans"))
		lines = append(lines, hdr)

		for _, h := range hd.Insiders {
			name := h.Name
			if len(name) > 32 {
				name = name[:32] + ".."
			}
			shares := "—"
			if h.Shares > 0 {
				shares = formatHolderShares(h.Shares)
			}
			lines = append(lines, fmt.Sprintf("  %-34s %14s %12s",
				priceStyle.Render(name),
				priceStyle.Render(shares),
				nameStyle.Render(h.ReportDate)))
		}
	}

	if len(hd.TopInstitutions) == 0 && len(hd.Insiders) == 0 {
		lines = append(lines, "")
		lines = append(lines, nameStyle.Render("  No holder data available"))
	}

	lines = append(lines, "")
	lines = append(lines, helpStyle.Render("  Tab/1-3 switch tabs · m/Esc close"))
	return strings.Join(lines, "\n")
}

// renderMiniBar renders a simple bar chart using block characters.
func renderMiniBar(data []float64) string {
	if len(data) == 0 {
		return ""
	}
	maxVal := data[0]
	for _, v := range data {
		if v > maxVal {
			maxVal = v
		}
	}
	if maxVal <= 0 {
		return strings.Repeat("▁", len(data))
	}
	blocks := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	var out strings.Builder
	for _, v := range data {
		idx := int(v / maxVal * float64(len(blocks)-1))
		if idx < 0 {
			idx = 0
		}
		if idx >= len(blocks) {
			idx = len(blocks) - 1
		}
		if v >= 0 {
			out.WriteString(changeUpStyle.Render(string(blocks[idx])))
		} else {
			out.WriteString(changeDownStyle.Render(string(blocks[0])))
		}
	}
	return out.String()
}

func formatHolderShares(shares int64) string {
	switch {
	case shares >= 1_000_000_000:
		return fmt.Sprintf("%.2fB", float64(shares)/1e9)
	case shares >= 1_000_000:
		return fmt.Sprintf("%.2fM", float64(shares)/1e6)
	case shares >= 1_000:
		return fmt.Sprintf("%.1fK", float64(shares)/1e3)
	default:
		return fmt.Sprintf("%d", shares)
	}
}

func (m Model) overlayPopup(base string, popup string) string {
	// Apply scroll: clip popup content lines
	popupLines := strings.Split(popup, "\n")
	maxVisibleH := m.height - 6 // leave room for border + padding
	if maxVisibleH < 5 {
		maxVisibleH = 5
	}

	// Clamp scroll offset
	if m.popupScroll > len(popupLines)-maxVisibleH {
		// handled below after we know total
	}
	scrollEnd := m.popupScroll + maxVisibleH
	if scrollEnd > len(popupLines) {
		scrollEnd = len(popupLines)
	}
	scrollStart := scrollEnd - maxVisibleH
	if scrollStart < 0 {
		scrollStart = 0
	}

	visibleLines := popupLines[scrollStart:scrollEnd]

	// Add scroll indicator if content overflows
	if len(popupLines) > maxVisibleH {
		pct := 0
		if len(popupLines)-maxVisibleH > 0 {
			pct = scrollStart * 100 / (len(popupLines) - maxVisibleH)
		}
		indicator := nameStyle.Render(fmt.Sprintf("  ↑↓ scroll %d%%", pct))
		visibleLines = append(visibleLines, indicator)
	}

	// Re-apply background after every ANSI reset in content lines so that inner
	// style resets don't punch holes in the background. Do this BEFORE border
	// rendering so the border itself is not affected.
	bgSeq := bgAnsiSeq(colorBg)
	for i, vl := range visibleLines {
		visibleLines[i] = strings.ReplaceAll(vl, "\033[0m", "\033[0m"+bgSeq)
	}

	clipped := strings.Join(visibleLines, "\n")

	// Pre-pad content lines to uniform width so the popup border is rectangular.
	// We do this manually instead of using lipgloss Width which miscalculates
	// with pre-styled ANSI content.
	contentW := m.width - 10 // border(2) + padding(4) + margin(4)
	if contentW < 36 {
		contentW = 36
	}
	paddedLines := strings.Split(clipped, "\n")
	for i, pl := range paddedLines {
		visW := ansi.StringWidth(pl)
		if visW < contentW {
			paddedLines[i] = pl + strings.Repeat(" ", contentW-visW)
		} else if visW > contentW {
			paddedLines[i] = ansi.Truncate(pl, contentW, "")
		}
	}
	clipped = strings.Join(paddedLines, "\n")

	popupStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBlue).
		Background(colorBg).
		Padding(1, 2)

	rendered := popupStyle.Render(clipped)

	popupW := lipgloss.Width(rendered)
	popupH := lipgloss.Height(rendered)

	// Center the popup over the base
	baseLines := strings.Split(base, "\n")
	baseH := len(baseLines)

	startY := (baseH - popupH) / 2
	if startY < 0 {
		startY = 0
	}
	startX := (m.width - popupW) / 2
	if startX < 0 {
		startX = 0
	}

	popupLines = strings.Split(rendered, "\n")

	for i, pLine := range popupLines {
		row := startY + i
		if row >= len(baseLines) {
			break
		}
		baseLine := baseLines[row]
		// Build the overlaid line: left padding + popup line, capped to terminal width.
		// The left portion must be exactly startX visible chars wide so that
		// the popup stays aligned even when the base line is shorter (e.g.
		// empty padding lines below the watchlist content).
		left := ""
		if startX > 0 {
			left = truncateAnsi(baseLine, startX)
			if leftW := ansi.StringWidth(left); leftW < startX {
				left += strings.Repeat(" ", startX-leftW)
			}
		}
		combined := left + pLine
		// Ensure combined line doesn't exceed terminal width
		if ansi.StringWidth(combined) > m.width {
			combined = truncateAnsi(combined, m.width)
		}
		baseLines[row] = combined
	}

	return strings.Join(baseLines, "\n")
}

// bgAnsiSeq returns the ANSI escape sequence to set the background color.
func bgAnsiSeq(c lipgloss.Color) string {
	hex := string(c)
	if len(hex) == 7 && hex[0] == '#' {
		r := hexVal(hex[1])<<4 | hexVal(hex[2])
		g := hexVal(hex[3])<<4 | hexVal(hex[4])
		b := hexVal(hex[5])<<4 | hexVal(hex[6])
		return fmt.Sprintf("\033[48;2;%d;%d;%dm", r, g, b)
	}
	// For ANSI 16 colors, use standard background codes
	n := 0
	for _, ch := range hex {
		if ch >= '0' && ch <= '9' {
			n = n*10 + int(ch-'0')
		}
	}
	if n < 8 {
		return fmt.Sprintf("\033[%dm", 40+n)
	}
	return fmt.Sprintf("\033[48;5;%dm", n)
}

func hexVal(b byte) byte {
	switch {
	case b >= '0' && b <= '9':
		return b - '0'
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10
	}
	return 0
}

// truncateAnsi returns the first n visible characters of s, preserving ANSI codes.
// truncateAnsi returns the first n visible-width characters of s,
// correctly handling ANSI escape codes and wide characters (braille, CJK).
func truncateAnsi(s string, n int) string {
	return ansi.Truncate(s, n, "")
}

func (m Model) viewHelpScreen() string {
	title := titleStyle.Render(" ◆ Help — Key Bindings ")

	sections := []struct {
		header string
		keys   []struct{ key, desc string }
	}{
		{
			header: "Navigation",
			keys: []struct{ key, desc string }{
				{"↑ / k", "Move up"},
				{"↓ / j", "Move down"},
				{"← / h", "Previous timeframe"},
				{"→ / l", "Next timeframe"},
				{"Enter", "Open detail view / Select search result"},
				{"Esc / q", "Back / Quit"},
			},
		},
		{
			header: "Actions",
			keys: []struct{ key, desc string }{
				{"/", "Search symbols"},
				{"s", "Cycle sort mode"},
				{"d / x", "Remove symbol from watchlist"},
				{"r", "Refresh current symbol"},
				{"R", "Refresh all symbols"},
				{"c", "Cycle chart style"},
				{"M", "Cycle MA preset (EMA 9/21, 21/50, 50/100, 20/50/200)"},
			},
		},
		{
			header: "Detail View",
			keys: []struct{ key, desc string }{
				{"]", "Next symbol"},
				{"[", "Previous symbol"},
				{"← →", "Switch timeframe"},
				{"m", "Market data / Financials / Holders"},
				{"a", "AI analysis (requires LLM config)"},
				{"e", "Earnings report (SEC + AI)"},
				{"Tab/1-3", "Switch popup tabs"},
			},
		},
		{
			header: "General",
			keys: []struct{ key, desc string }{
				{"?", "Toggle this help screen"},
				{"Ctrl+C", "Quit"},
			},
		},
	}

	var lines []string
	for _, sec := range sections {
		lines = append(lines, "")
		lines = append(lines, helpKeyStyle.Render("  "+sec.header))
		for _, k := range sec.keys {
			keyStr := helpKeyStyle.Render(fmt.Sprintf("  %-14s", k.key))
			lines = append(lines, keyStr+nameStyle.Render(k.desc))
		}
	}

	content := strings.Join(lines, "\n")
	footer := "\n" + helpStyle.Render("  Press any key to return")

	return lipgloss.JoinVertical(lipgloss.Left, title, content, footer)
}

func (m Model) viewSearch() string {
	title := titleStyle.Render(" ◆ Search ")
	searchContent := RenderSearch(m.search, m.width)
	return lipgloss.JoinVertical(lipgloss.Left, title, "", searchContent)
}

// providerOption describes an available LLM provider for the picker.
type providerOption struct {
	id       string // provider key (matches llm.Provider values)
	label    string // display name
	model    string // default model for this provider
	endpoint string // default endpoint (empty = use provider default)
}

// availableProviders returns the list of LLM providers that have
// credentials configured in the environment.
func (m Model) availableProviders() []providerOption {
	var opts []providerOption

	// Always include the currently configured provider first
	if m.llmClient != nil {
		opts = append(opts, providerOption{
			id:    string(m.llmClient.Provider()),
			label: providerLabel(string(m.llmClient.Provider())),
			model: m.llmClient.Model(),
		})
	}

	// Detect available providers from environment
	candidates := []struct {
		id      string
		label   string
		model   string
		envKeys []string
	}{
		{"openai", "OpenAI", "gpt-4o", []string{"OPENAI_API_KEY"}},
		{"copilot", "GitHub Copilot", "gpt-4o", []string{"GITHUB_COPILOT_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"}},
		{"gemini", "Google Gemini", "gemini-2.5-flash", []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}},
		{"anthropic", "Anthropic Claude", "claude-sonnet-4-20250514", []string{"ANTHROPIC_API_KEY"}},
		{"vertex", "Vertex AI", "gemini-2.5-flash", []string{"GOOGLE_ACCESS_TOKEN", "GCLOUD_ACCESS_TOKEN"}},
	}

	for _, c := range candidates {
		// Skip if already added as current
		if m.llmClient != nil && c.id == string(m.llmClient.Provider()) {
			continue
		}
		for _, envKey := range c.envKeys {
			if os.Getenv(envKey) != "" {
				opts = append(opts, providerOption{
					id:    c.id,
					label: c.label,
					model: c.model,
				})
				break
			}
		}
	}

	// Also check copilot apps.json for credential availability
	if m.llmClient == nil || string(m.llmClient.Provider()) != "copilot" {
		hasCopilot := false
		for _, o := range opts {
			if o.id == "copilot" {
				hasCopilot = true
				break
			}
		}
		if !hasCopilot {
			home, _ := os.UserHomeDir()
			if home != "" {
				appsFile := filepath.Join(home, ".config", "github-copilot", "apps.json")
				if _, err := os.Stat(appsFile); err == nil {
					opts = append(opts, providerOption{
						id:    "copilot",
						label: "GitHub Copilot",
						model: "gpt-4o",
					})
				}
			}
		}
	}

	return opts
}

func providerLabel(id string) string {
	switch id {
	case "openai":
		return "OpenAI"
	case "copilot":
		return "GitHub Copilot"
	case "gemini":
		return "Google Gemini"
	case "anthropic":
		return "Anthropic Claude"
	case "vertex":
		return "Vertex AI"
	default:
		return id
	}
}

// renderProviderPicker renders the AI provider selection popup.
func (m Model) renderProviderPicker() string {
	providers := m.availableProviders()
	header := helpKeyStyle.Render("Switch AI Provider")
	curProvider := ""
	if m.llmClient != nil {
		curProvider = string(m.llmClient.Provider()) + " / " + m.llmClient.Model()
	}
	current := nameStyle.Render("Current: ") + helpKeyStyle.Render(curProvider)

	var lines []string
	lines = append(lines, header, current, "")
	for i, p := range providers {
		prefix := "  "
		if i == m.providerPickerSel {
			prefix = helpKeyStyle.Render("▸ ")
		}
		label := p.label
		if p.model != "" {
			label += nameStyle.Render(" (" + p.model + ")")
		}
		lines = append(lines, prefix+label)
	}
	lines = append(lines, "",
		helpKeyStyle.Render("↑↓")+" select  "+
			helpKeyStyle.Render("Enter")+" switch  "+
			helpKeyStyle.Render("Esc")+" cancel")
	return strings.Join(lines, "\n")
}

// handleProviderPickerKey handles keys while the provider picker is open.
func (m Model) handleProviderPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	providers := m.availableProviders()
	switch {
	case key.Matches(msg, keys.Up):
		if m.providerPickerSel > 0 {
			m.providerPickerSel--
		}
	case key.Matches(msg, keys.Down):
		if m.providerPickerSel < len(providers)-1 {
			m.providerPickerSel++
		}
	case key.Matches(msg, keys.Enter):
		if m.providerPickerSel < len(providers) {
			p := providers[m.providerPickerSel]
			// Resolve API key for the selected provider
			apiKey := config.ResolveLLMAPIKey(p.id, m.cfg.LLM.APIKeyEnv)
			model := p.model
			if p.id == string(m.cfg.LLM.Provider) || (p.id == "openai" && m.cfg.LLM.Provider == "") {
				// If switching back to the configured provider, use its config model
				model = m.cfg.LLM.Model
			}
			endpoint := p.endpoint
			if endpoint == "" && p.id == "openai" {
				endpoint = m.cfg.LLM.Endpoint
			}
			client := llm.NewClient(llm.Config{
				Provider:      llm.Provider(p.id),
				Endpoint:      endpoint,
				Model:         model,
				APIKey:        apiKey,
				Project:       m.cfg.LLM.Project,
				Location:      m.cfg.LLM.Location,
				ContextTokens: m.cfg.LLM.ContextTokens,
			})
			if client.Configured() {
				m.llmClient = client
				// Clear cached AI results so next request uses new provider
				m.aiText = ""
				m.aiSymbol = ""
			}
		}
		m.showProviderPicker = false
	case key.Matches(msg, keys.Back):
		m.showProviderPicker = false
	}
	return m, nil
}

func (m Model) renderTimeframeBar() string {
	var parts []string
	for i, tf := range timeframes {
		if i == m.timeframeIdx {
			parts = append(parts, helpKeyStyle.Render("["+tf.Label+"]"))
		} else {
			parts = append(parts, nameStyle.Render(" "+tf.Label+" "))
		}
	}
	return strings.Join(parts, "")
}

func (m Model) renderGroupTabs() string {
	if len(m.cfg.Watchlists) <= 1 || m.mode == viewPortfolio {
		return ""
	}
	var parts []string
	for i, g := range m.cfg.Watchlists {
		num := fmt.Sprintf("%d", i+1)
		if i == m.activeGroup {
			parts = append(parts, helpKeyStyle.Render("["+num+":"+g.Name+"]"))
		} else {
			parts = append(parts, " "+helpKeyStyle.Render(num)+nameStyle.Render(":"+g.Name+" "))
		}
	}
	return "  " + strings.Join(parts, " ")
}

// renderViewTabs renders the top-level Watchlist | Portfolio | Heatmap switch that
// appears inline next to the title on the first line.
//
// The active tab is shown in brackets with bold highlight. The inactive
// tabs are shown dimmed with their shortcut letters underlined so the key hints
// are obvious (P for portfolio, W for watchlist, H for heatmap — case-insensitive).
func (m Model) renderViewTabs() string {
	// Shortcut letter on the inactive tab: use the same yellow/bold as other
	// key hints, plus underline so it is obviously a hotkey even at a glance.
	hintLetter := helpKeyStyle.Underline(true)
	dim := nameStyle
	active := helpKeyStyle

	// Build Watchlist tab
	var wl string
	if m.mode == viewWatchlist {
		wl = active.Render("[Watchlist]")
	} else {
		wl = dim.Render(" ") + hintLetter.Render("W") + dim.Render("atchlist ")
	}

	// Build Portfolio tab
	var pf string
	if m.mode == viewPortfolio {
		pf = active.Render("[Portfolio]")
	} else {
		pf = dim.Render(" ") + hintLetter.Render("P") + dim.Render("ortfolio ")
	}

	// Build Heatmap tab
	var hm string
	if m.mode == viewHeatmap {
		hm = active.Render("[Heatmap]")
	} else {
		hm = dim.Render(" ") + hintLetter.Render("H") + dim.Render("eatmap ")
	}

	sep := dim.Render(" | ")
	return wl + sep + pf + sep + hm
}

// hintKey renders a keybinding hint as `K` + `est` where the shortcut letter
// is highlighted + underlined and the rest of the word inherits the
// surrounding help style. Use this when the hotkey is the first letter of
// the action name (e.g. s -> Sort). Include the trailing spaces in `rest`.
func hintKey(letter, rest string) string {
	return helpKeyStyle.Underline(true).Render(letter) + rest
}

func (m Model) renderHelp() string {
	tabHelp := ""
	if len(m.cfg.Watchlists) > 1 {
		tabHelp = helpKeyStyle.Render("1-9") + " group  "
	}
	aiHelp := ""
	if m.llmClient != nil {
		aiHelp = hintKey("S", "ummary  ")
	}
	return helpStyle.Render(
		helpKeyStyle.Render("↑↓") + " navigate  " +
			helpKeyStyle.Render("←→") + " timeframe  " +
			tabHelp +
			helpKeyStyle.Render("Enter") + " detail  " +
			helpKeyStyle.Render("/") + " search  " +
			hintKey("s", "ort  ") +
			aiHelp +
			hintKey("d", "elete  ") +
			hintKey("r", "efresh symbol  ") +
			helpKeyStyle.Render("R") + " refresh all  " +
			helpKeyStyle.Render("?") + " help  " +
			hintKey("q", "uit"))
}

// === Key Handlers ===

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, keys.Quit) {
		m.quitting = true
		if m.orchestratorCancel != nil {
			m.orchestratorCancel()
		}
		return m, tea.Quit
	}

	// AI command window is modal and captures all keys while open.
	if m.aicmd.Active {
		return m.handleAICmdKey(msg)
	}

	// Help mode: any key returns to previous view
	if m.mode == viewHelp {
		m.mode = m.prevMode
		return m, nil
	}

	// Help key works from any non-search mode
	if key.Matches(msg, keys.Help) && m.mode != viewSearch {
		m.prevMode = m.mode
		m.mode = viewHelp
		return m, nil
	}

	// `a` opens the unified interactive AI command window from any
	// non-search, non-popup view (detail/watchlist/portfolio). When a
	// symbol is in context, the window auto-submits a brief analysis
	// prompt so pressing `a` behaves like the old one-shot AI summary;
	// the user can keep chatting once the response arrives.
	if key.Matches(msg, keys.AI) && m.mode != viewSearch && !m.showSummary && !m.showEarnings && !m.showPortfolioAI && !m.showAI {
		if prompt := m.aiCmdAutoPrompt(); prompt != "" {
			return m.openAICmdAuto(prompt)
		}
		return m.openAICmd(m.aiCmdSeed())
	}

	// Provider picker popup: modal key handling
	if m.showProviderPicker {
		return m.handleProviderPickerKey(msg)
	}

	// Shift-A opens the AI provider picker from any non-search view
	if msg.String() == "A" && m.mode != viewSearch && m.mode != viewPortfolio {
		providers := m.availableProviders()
		if len(providers) > 1 {
			m.showProviderPicker = true
			// Pre-select current provider
			cur := ""
			if m.llmClient != nil {
				cur = string(m.llmClient.Provider())
			}
			for i, p := range providers {
				if p.id == cur {
					m.providerPickerSel = i
					break
				}
			}
			return m, nil
		}
	}

	switch m.mode {
	case viewSearch:
		return m.handleSearchKey(msg)
	case viewDetail:
		return m.handleDetailKey(msg)
	case viewPortfolio:
		return m.handlePortfolioKey(msg)
	case viewHeatmap:
		return m.handleHeatmapKey(msg)
	default:
		return m.handleWatchlistKey(msg)
	}
}

func (m Model) handleWatchlistKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Handle summary popup keys when visible
	if m.showSummary {
		switch {
		case key.Matches(msg, keys.Down):
			m.popupScroll++
		case key.Matches(msg, keys.Up):
			if m.popupScroll > 0 {
				m.popupScroll--
			}
		case key.Matches(msg, keys.Refresh):
			// Force refresh: clear cache and re-fetch
			groupName := m.cfg.Watchlists[m.activeGroup].Name
			logger.Log("manual_refresh: target=summary group=%s", groupName)
			if m.cache != nil {
				m.cache.DeleteText("summary_"+groupName, "summary")
			}
			m.summaryLoading = true
			m.summaryText = ""
			return m, m.fetchSummary()
		case key.Matches(msg, keys.RefreshAll):
			return m.refreshAllSymbols()
		case key.Matches(msg, keys.Back) || key.Matches(msg, keys.Summary):
			m.showSummary = false
		}
		return m, nil
	}

	switch {
	case key.Matches(msg, keys.Up):
		if m.selected > 0 {
			m.selected--
		}
	case key.Matches(msg, keys.Down):
		if m.selected < len(m.items)-1 {
			m.selected++
		}
	case key.Matches(msg, keys.Left):
		return m.prevTimeframe()
	case key.Matches(msg, keys.Right):
		return m.nextTimeframe()
	case key.Matches(msg, keys.Enter):
		if len(m.items) > 0 {
			m.mode = viewDetail
			m.relatedFocus = -1
			m.compareSymbol = nil
			sym := m.items[m.selected].Symbol
			cmds := []tea.Cmd{m.fetchChart(sym), m.fetchKeyStats(sym), m.fetchRelatedSymbols(sym)}
			if c := m.ensureIndicatorContext(m.selected); c != nil {
				cmds = append(cmds, c)
			}
			return m, tea.Batch(cmds...)
		}
	case key.Matches(msg, keys.Search):
		m.mode = viewSearch
		m.search = SearchView{}
	case key.Matches(msg, keys.Delete):
		return m.removeSelected()
	case key.Matches(msg, keys.Sort):
		m.sortMode = (m.sortMode + 1) % sortModeCount
	case key.Matches(msg, keys.Refresh):
		return m.refreshCurrentSymbol()
	case key.Matches(msg, keys.RefreshAll):
		return m.refreshAllSymbols()
	case key.Matches(msg, keys.ChartStyle):
		m.cycleChartStyle()
	case key.Matches(msg, keys.MAMode):
		m.maMode = (m.maMode + 1) % maModeCount
	case key.Matches(msg, portfolioKeys.Open):
		return m.openPortfolio()
	case msg.String() == "h" || msg.String() == "H":
		// Switch to heatmap view — show all symbols across all watchlist groups.
		m.heatmapPrev = viewWatchlist
		m.heatmap = NewHeatmapModel()
		m.mode = viewHeatmap
		return m.setHeatmapType(HeatmapWatchlist)
	case key.Matches(msg, keys.Summary):
		if m.llmClient != nil && len(m.items) > 0 {
			m.showSummary = true
			m.popupScroll = 0
			if m.summaryGroup != m.activeGroup || m.summaryText == "" {
				m.summaryLoading = true
				m.summaryText = ""
				m.summaryGroup = m.activeGroup
				return m, m.fetchSummary()
			}
		}
	case key.Matches(msg, keys.Back):
		m.quitting = true
		if m.orchestratorCancel != nil {
			m.orchestratorCancel()
		}
		return m, tea.Quit
	default:
		// Number keys 1-9 to switch watchlist groups
		if len(m.cfg.Watchlists) > 1 {
			r := msg.String()
			if len(r) == 1 && r[0] >= '1' && r[0] <= '9' {
				idx := int(r[0] - '1')
				if idx < len(m.cfg.Watchlists) {
					return m.switchGroup(idx)
				}
			}
		}
	}
	return m, nil
}

func (m Model) handleDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// If watchlist-add confirmation popup is showing
	if m.confirmWatchlistAdd {
		switch {
		case key.Matches(msg, keys.Up):
			if m.confirmWatchlistSel > 0 {
				m.confirmWatchlistSel--
			}
		case key.Matches(msg, keys.Down):
			if m.confirmWatchlistSel < len(m.cfg.Watchlists)-1 {
				m.confirmWatchlistSel++
			}
		case key.Matches(msg, keys.Enter) || msg.String() == "y" || msg.String() == "Y":
			// Confirm: add symbol to selected watchlist group
			target := m.confirmWatchlistSel
			if len(m.cfg.Watchlists) <= 1 {
				target = 0
			}
			if m.tempDetailActive && m.tempDetailSymbol != "" && m.selected < len(m.items) {
				sym := m.items[m.selected].Symbol
				if target < len(m.cfg.Watchlists) {
					wl := &m.cfg.Watchlists[target]
					alreadyExists := false
					for _, existing := range wl.Symbols {
						if existing.Symbol == sym {
							alreadyExists = true
							break
						}
					}
					if !alreadyExists {
						wl.Symbols = append(wl.Symbols, config.WatchItem{Symbol: sym, Name: sym})
						if config.Save(m.cfgPath, m.cfg) == nil {
							m.tempDetailActive = false
							m.tempDetailSymbol = ""
						}
					}
				}
			}
			m.confirmWatchlistAdd = false
		case key.Matches(msg, keys.Back) || msg.String() == "n" || msg.String() == "N":
			m.confirmWatchlistAdd = false
		default:
			// Number keys 1-9 to jump to a group
			r := msg.String()
			if len(r) == 1 && r[0] >= '1' && r[0] <= '9' {
				idx := int(r[0] - '1')
				if idx < len(m.cfg.Watchlists) {
					m.confirmWatchlistSel = idx
				}
			}
		}
		return m, nil
	}

	// If market depth popup is showing, handle tab switching or dismiss
	if m.showMarketDepth {
		switch {
		case key.Matches(msg, keys.Down):
			m.popupScroll++
		case key.Matches(msg, keys.Up):
			if m.popupScroll > 0 {
				m.popupScroll--
			}
		case msg.String() == "tab":
			m.popupTab = (m.popupTab + 1) % 3
			m.popupScroll = 0
		case msg.String() == "shift+tab":
			m.popupTab = (m.popupTab + 2) % 3
			m.popupScroll = 0
		case msg.String() == "1":
			m.popupTab = 0
			m.popupScroll = 0
		case msg.String() == "2":
			m.popupTab = 1
			m.popupScroll = 0
		case msg.String() == "3":
			m.popupTab = 2
			m.popupScroll = 0
		case key.Matches(msg, keys.MarketDepth) || key.Matches(msg, keys.Back):
			m.showMarketDepth = false
			m.popupScroll = 0
		}
		return m, nil
	}

	// If AI popup is showing, scroll or dismiss
	if m.showAI {
		switch {
		case key.Matches(msg, keys.Down):
			m.popupScroll++
		case key.Matches(msg, keys.Up):
			if m.popupScroll > 0 {
				m.popupScroll--
			}
		case key.Matches(msg, keys.Refresh):
			// Force refresh: clear cache and re-fetch
			if m.selected < len(m.items) {
				item := m.items[m.selected]
				logger.Log("manual_refresh: target=ai symbol=%s", item.Symbol)
				if m.cache != nil {
					m.cache.PutText(item.Symbol, "ai", "")
				}
				m.aiLoading = true
				m.aiText = ""
				m.aiSymbol = item.Symbol
				m.popupScroll = 0
				if item.Financials == nil {
					m.pendingAI = true
					return m, m.fetchFinancials(item.Symbol)
				}
				return m, m.fetchAIAnalysis(item)
			}
		case key.Matches(msg, keys.RefreshAll):
			return m.refreshAllSymbols()
		case key.Matches(msg, keys.Back) || key.Matches(msg, keys.AI):
			m.showAI = false
			m.popupScroll = 0
		}
		return m, nil
	}

	// If earnings popup is showing, scroll or dismiss
	if m.showEarnings {
		switch {
		case key.Matches(msg, keys.Down):
			m.popupScroll++
			m.earningsLinkStatus = ""
		case key.Matches(msg, keys.Up):
			if m.popupScroll > 0 {
				m.popupScroll--
			}
			m.earningsLinkStatus = ""
		case msg.String() == "n":
			m.stepEarningsLinkSelection(1)
		case msg.String() == "p":
			m.stepEarningsLinkSelection(-1)
		case msg.String() == "c" || key.Matches(msg, keys.Enter):
			m.copySelectedEarningsLink()
		case key.Matches(msg, keys.Refresh):
			// Force refresh: clear cache and re-fetch
			if m.selected < len(m.items) {
				item := m.items[m.selected]
				logger.Log("manual_refresh: target=earnings symbol=%s", item.Symbol)
				if m.cache != nil {
					m.cache.PutText(item.Symbol, "earnings", "")
				}
				m.earningsLoading = true
				m.earningsText = ""
				m.earningsSymbol = item.Symbol
				m.earningsDiscovery = nil
				m.earningsLinkSel = 0
				m.earningsLinkStatus = ""
				m.popupScroll = 0
				if item.Financials == nil {
					m.pendingEarnings = true
					return m, m.fetchFinancials(item.Symbol)
				}
				return m, m.fetchEarningsReport(item)
			}
		case key.Matches(msg, keys.RefreshAll):
			return m.refreshAllSymbols()
		case key.Matches(msg, keys.Back) || key.Matches(msg, keys.Earnings):
			m.showEarnings = false
			m.popupScroll = 0
			m.earningsLinkStatus = ""
		}
		return m, nil
	}

	switch {
	case key.Matches(msg, keys.Back):
		// Esc: exit compare → exit focus → exit detail
		if m.compareSymbol != nil {
			m.compareSymbol = nil
			return m, nil
		}
		if m.relatedFocus >= 0 {
			m.relatedFocus = -1
			return m, nil
		}
		m = m.cleanupTempDetailItem()
		m.mode = viewWatchlist
	case msg.String() == "tab":
		// Tab: cycle focus through related symbols
		if len(m.relatedSymbols) > 0 {
			m.relatedFocus = (m.relatedFocus + 1) % len(m.relatedSymbols)
		}
		return m, nil
	case msg.String() == "shift+tab":
		if len(m.relatedSymbols) > 0 {
			if m.relatedFocus <= 0 {
				m.relatedFocus = len(m.relatedSymbols) - 1
			} else {
				m.relatedFocus--
			}
		}
		return m, nil
	case key.Matches(msg, keys.Enter):
		// Enter: toggle compare with focused related symbol
		if m.relatedFocus >= 0 && m.relatedFocus < len(m.relatedSymbols) {
			rs := m.relatedSymbols[m.relatedFocus]
			if m.compareSymbol != nil && m.compareSymbol.Symbol == rs.Symbol {
				m.compareSymbol = nil // toggle off
			} else {
				m.compareSymbol = &rs
			}
		}
		return m, nil
	case key.Matches(msg, keys.Left):
		return m.prevTimeframe()
	case key.Matches(msg, keys.Right):
		return m.nextTimeframe()
	case key.Matches(msg, keys.NextSymbol):
		if m.selected < len(m.items)-1 {
			m.selected++
		} else {
			m.selected = 0
		}
		m.relatedFocus = -1
		m.compareSymbol = nil
		sym := m.items[m.selected].Symbol
		cmds := []tea.Cmd{m.fetchChart(sym), m.fetchKeyStats(sym), m.fetchRelatedSymbols(sym)}
		if c := m.ensureIndicatorContext(m.selected); c != nil {
			cmds = append(cmds, c)
		}
		return m, tea.Batch(cmds...)
	case key.Matches(msg, keys.PrevSymbol):
		if m.selected > 0 {
			m.selected--
		} else {
			m.selected = len(m.items) - 1
		}
		m.relatedFocus = -1
		m.compareSymbol = nil
		sym := m.items[m.selected].Symbol
		cmds2 := []tea.Cmd{m.fetchChart(sym), m.fetchKeyStats(sym), m.fetchRelatedSymbols(sym)}
		if c := m.ensureIndicatorContext(m.selected); c != nil {
			cmds2 = append(cmds2, c)
		}
		return m, tea.Batch(cmds2...)
	case key.Matches(msg, keys.MarketDepth):
		m.showMarketDepth = true
		m.popupTab = 0
		m.popupScroll = 0
		sym := m.items[m.selected].Symbol
		var cmds []tea.Cmd
		if m.items[m.selected].Financials == nil {
			cmds = append(cmds, m.fetchFinancials(sym))
		}
		if m.items[m.selected].Holders == nil {
			cmds = append(cmds, m.fetchHolders(sym))
		}
		if len(cmds) > 0 {
			return m, tea.Batch(cmds...)
		}
	case key.Matches(msg, keys.ChartStyle):
		m.cycleChartStyle()
	case key.Matches(msg, keys.MAMode):
		m.maMode = (m.maMode + 1) % maModeCount
	case msg.String() == "t":
		m.showTechnicals = !m.showTechnicals
		// Lazy-fetch a same-resolution backfill so intraday indicators
		// (MACD / MAs) warm up properly across prior sessions. Refetch
		// when the cached context was fetched at a different interval.
		if m.showTechnicals && m.selected < len(m.items) {
			if cmd := m.ensureIndicatorContext(m.selected); cmd != nil {
				return m, cmd
			}
		}
	case msg.String() == "w" || msg.String() == "W":
		if m.tempDetailActive && m.tempDetailSymbol != "" && m.selected < len(m.items) {
			// Show watchlist-add confirmation popup
			m.confirmWatchlistAdd = true
			m.confirmWatchlistSel = m.activeGroup
		} else {
			// Navigate to watchlist view
			m = m.cleanupTempDetailItem()
			m.mode = viewWatchlist
		}
	case key.Matches(msg, keys.Refresh):
		return m.refreshCurrentSymbol()
	case key.Matches(msg, keys.RefreshAll):
		return m.refreshAllSymbols()
	case key.Matches(msg, keys.AI):
		if m.llmClient != nil && m.selected < len(m.items) {
			m.showAI = true
			m.popupScroll = 0
			sym := m.items[m.selected].Symbol
			if m.aiSymbol != sym || m.aiText == "" {
				m.aiLoading = true
				m.aiText = ""
				m.aiSymbol = sym
				if m.items[m.selected].Financials == nil {
					m.pendingAI = true
					return m, m.fetchFinancials(sym)
				}
				return m, m.fetchAIAnalysis(m.items[m.selected])
			}
		}
	case key.Matches(msg, keys.Earnings):
		if m.llmClient != nil && m.selected < len(m.items) {
			m.showEarnings = true
			m.popupScroll = 0
			sym := m.items[m.selected].Symbol
			if m.earningsSymbol != sym || m.earningsText == "" {
				m.earningsLoading = true
				m.earningsText = ""
				m.earningsSymbol = sym
				if m.items[m.selected].Financials == nil {
					m.pendingEarnings = true
					return m, m.fetchFinancials(sym)
				}
				return m, m.fetchEarningsReport(m.items[m.selected])
			}
		}
	}
	return m, nil
}

func (m Model) handleHeatmapKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Back):
		m.mode = m.heatmapPrev
		if m.mode == 0 {
			m.mode = viewWatchlist
		}
	case msg.String() == "w" || msg.String() == "W":
		m.heatmapPrev = viewHeatmap
		m.mode = viewWatchlist
	case msg.String() == "p" || msg.String() == "P":
		m.heatmapPrev = viewHeatmap
		m.mode = viewPortfolio
	case msg.String() == "t" || msg.String() == "T":
		return m.setHeatmapType(nextHeatmapType(m.heatmap.Type))
	case msg.String() == "r" || msg.String() == "R":
		return m.setHeatmapType(m.heatmap.Type)
	case msg.String() == "s" || msg.String() == "S":
		// Cycle through sort modes
		m.heatmap.SortBy = (m.heatmap.SortBy + 1) % 3
		m.heatmap.applySorting()
		m.heatmap.findSelectedIndex()
	case msg.String() == "v" || msg.String() == "V":
		if m.heatmap.Type == HeatmapPortfolio {
			if m.heatmap.PortfolioScale == PortfolioHeatmapByMarketValue {
				m.heatmap.PortfolioScale = PortfolioHeatmapByMarketCap
			} else {
				m.heatmap.PortfolioScale = PortfolioHeatmapByMarketValue
			}
			return m.setHeatmapType(HeatmapPortfolio)
		}
	case key.Matches(msg, keys.Up):
		m.heatmap.SelectUp()
	case key.Matches(msg, keys.Down):
		m.heatmap.SelectDown()
	case key.Matches(msg, keys.Enter):
		// Select the heatmap item and navigate to detail view
		if item := m.heatmap.SelectedItem(); item != nil {
			// Extract symbol from label (handle "Sector" suffix)
			label := item.Label
			sym := label
			if strings.HasSuffix(label, " Sector") {
				// For sector heatmaps, just go back to watchlist
				m.mode = viewWatchlist
				break
			}
			// Find the symbol in the watchlist and switch to detail.
			found := false
			for i, wi := range m.items {
				if wi.Symbol == sym {
					// If a temporary symbol was active from a prior heatmap open,
					// remove it before entering a regular watchlist-backed detail.
					m = m.cleanupTempDetailItem()
					m.selected = i
					m.mode = viewDetail
					m.relatedFocus = -1
					m.compareSymbol = nil
					cmds := []tea.Cmd{m.fetchChart(sym), m.fetchKeyStats(sym), m.fetchRelatedSymbols(sym)}
					if c := m.ensureIndicatorContext(m.selected); c != nil {
						cmds = append(cmds, c)
					}
					found = true
					return m, tea.Batch(cmds...)
				}
			}

			if !found {
				// Open a transient detail item when the symbol is not in watchlist.
				m = m.cleanupTempDetailItem()
				prevSelected := m.selected
				temp := WatchlistItem{
					Symbol:  sym,
					Name:    sym,
					Quote:   item.Quote,
					Loading: true,
				}
				m.items = append(m.items, temp)
				m.selected = len(m.items) - 1
				m.tempDetailActive = true
				m.tempDetailSymbol = sym
				m.tempDetailPrevSelected = prevSelected
				m.mode = viewDetail
				m.relatedFocus = -1
				m.compareSymbol = nil

				cmds := []tea.Cmd{m.fetchChart(sym), m.fetchKeyStats(sym), m.fetchRelatedSymbols(sym)}
				if c := m.ensureIndicatorContext(m.selected); c != nil {
					cmds = append(cmds, c)
				}
				return m, tea.Batch(cmds...)
			}
		}
	case key.Matches(msg, keys.Help):
		m.prevMode = viewHeatmap
		m.mode = viewHelp
	}
	return m, nil
}

func (m Model) setHeatmapType(kind HeatmapType) (tea.Model, tea.Cmd) {
	m.heatmap.Type = kind
	logger.Log("heatmap: set type=%s", heatmapTypeName(kind))
	switch kind {
	case HeatmapWatchlist:
		all := m.allWatchlistItems()
		m.heatmap.BuildWatchlistHeatmap(all)
		if len(m.heatmap.Items) == 0 {
			logger.Log("heatmap: watchlist has no renderable items, fallback to most_active")
			return m.setHeatmapType(HeatmapMostActive)
		}
		m.heatmap.RebuildVisibleIdxs(m.width-2, m.height-3)
		// Trigger a background fetch for symbols from inactive groups that
		// have no cached quote data so the heatmap fills in shortly.
		var missing []string
		for _, it := range all {
			if it.Quote == nil {
				missing = append(missing, it.Symbol)
			}
		}
		var cmd tea.Cmd
		if len(missing) > 0 {
			cmd = m.fetchHeatmapWatchlistQuotes(missing)
		}
		return m, cmd
	case HeatmapPortfolio:
		// Keep portfolio heatmap in sync even if user enters heatmap
		// without opening portfolio view first.
		m.rebuildPortfolioItems()
		m.heatmap.BuildPortfolioHeatmap(m.portfolioItems)
		if len(m.heatmap.Items) == 0 {
			logger.Log("heatmap: portfolio has no renderable items, fallback to most_active")
			return m.setHeatmapType(HeatmapMostActive)
		}
		m.heatmap.RebuildVisibleIdxs(m.width-2, m.height-3)
		return m, nil
	// case HeatmapVolume:
	// 	m.heatmap.BuildVolumeHeatmap(m.items)
	// 	return m, nil
	// case HeatmapSector:
	// 	m.heatmap.BuildSectorHeatmap(m.items)
	// 	return m, nil
	case HeatmapMostActive, HeatmapTrendNow:
		return m, m.fetchHeatmapFeed(kind)
	default:
		return m, nil
	}
}

func nextHeatmapType(curr HeatmapType) HeatmapType {
	switch curr {
	case HeatmapWatchlist:
		return HeatmapPortfolio
	case HeatmapPortfolio:
		return HeatmapMostActive
	case HeatmapMostActive:
		return HeatmapTrendNow
	default:
		return HeatmapWatchlist
	}
}

func (m Model) handleHeatmapFeed(msg heatmapFeedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		logger.Log("heatmap: feed error type=%s err=%v", heatmapTypeName(msg.kind), msg.err)
		m.err = fmt.Sprintf("Heatmap feed error: %v", msg.err)
		return m, nil
	}
	if len(msg.quotes) == 0 {
		logger.Log("heatmap: feed empty type=%s", heatmapTypeName(msg.kind))
		m.err = "Heatmap feed returned no symbols"
		m.heatmap.Items = nil
		return m, nil
	}
	capCount := 0
	for _, q := range msg.quotes {
		if q.MarketCap > 0 {
			capCount++
		}
	}
	logger.Log("heatmap: feed ok type=%s quotes=%d with_market_cap=%d", heatmapTypeName(msg.kind), len(msg.quotes), capCount)
	m.err = ""
	m.heatmap.BuildYahooQuoteHeatmap(msg.kind, msg.quotes)
	m.heatmap.RebuildVisibleIdxs(m.width-2, m.height-3)
	return m, nil
}

// handleHeatmapWatchlistQuotes merges freshly-fetched quotes for inactive watchlist
// group symbols into the current watchlist heatmap and re-renders it.
func (m Model) handleHeatmapWatchlistQuotes(msg heatmapWatchlistQuotesMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil || len(msg.quotes) == 0 || m.heatmap.Type != HeatmapWatchlist {
		return m, nil
	}
	qmap := make(map[string]yahoo.Quote, len(msg.quotes))
	for _, q := range msg.quotes {
		qmap[q.Symbol] = q
	}
	for i := range m.heatmap.Items {
		sym := m.heatmap.Items[i].Label
		if q, ok := qmap[sym]; ok {
			if q.Price != 0 && q.PreviousClose != 0 {
				m.heatmap.Items[i].Change = ((q.Price - q.PreviousClose) / q.PreviousClose) * 100
			} else {
				m.heatmap.Items[i].Change = q.Change
			}
			qCopy := q
			m.heatmap.Items[i].Quote = &qCopy
		}
	}
	m.heatmap.applySorting()
	m.heatmap.findSelectedIndex()
	m.heatmap.RebuildVisibleIdxs(m.width-2, m.height-3)
	return m, nil
}

// allWatchlistItems collects WatchlistItems from all watchlist groups.
// The active group's items (m.items) are used as-is since they carry live quotes.
// For inactive groups, the per-symbol stale cache is consulted; symbols not yet in
// the cache will appear without quote data until the background fetch completes.
// Each symbol is included at most once (active group data wins on duplicates).
func (m Model) allWatchlistItems() []WatchlistItem {
	seen := make(map[string]bool)
	all := make([]WatchlistItem, 0)

	// Active group — already has live/recent quotes.
	for _, it := range m.items {
		seen[it.Symbol] = true
		all = append(all, it)
	}

	// Inactive groups — look up per-symbol from stale cache.
	for i, wl := range m.cfg.Watchlists {
		if i == m.activeGroup {
			continue
		}
		for _, w := range wl.Symbols {
			if seen[w.Symbol] {
				continue
			}
			seen[w.Symbol] = true
			it := WatchlistItem{Symbol: w.Symbol, Name: w.Name}
			if m.cache != nil {
				if stale := m.cache.GetQuotesStale([]string{w.Symbol}); len(stale) > 0 {
					q := stale[0]
					it.Quote = &q
				}
			}
			all = append(all, it)
		}
	}
	return all
}

// fetchHeatmapWatchlistQuotes fetches live quotes for inactive-group symbols and
// returns them as a heatmapWatchlistQuotesMsg to update the watchlist heatmap.
func (m Model) fetchHeatmapWatchlistQuotes(symbols []string) tea.Cmd {
	client := m.client
	cacheRef := m.cache
	return func() tea.Msg {
		quotes, err := client.GetQuotes(symbols)
		if err == nil && cacheRef != nil && len(quotes) > 0 {
			cacheRef.PutQuotes(quotes)
		}
		if err != nil && cacheRef != nil {
			if stale := cacheRef.GetQuotesStale(symbols); stale != nil {
				return heatmapWatchlistQuotesMsg{quotes: stale}
			}
		}
		return heatmapWatchlistQuotesMsg{quotes: quotes, err: err}
	}
}

func (m Model) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		if m.portfolioSearching {
			m.portfolioSearching = false
			m.mode = viewPortfolio
		} else {
			m.mode = viewWatchlist
		}
		return m, nil

	case tea.KeyTab:
		// Toggle between suggestion and watchlist sections
		if m.search.Section == SectionSuggestions && len(m.search.WatchlistMatches) > 0 {
			m.search.Section = SectionWatchlist
			m.search.WatchSelected = 0
		} else if m.search.Section == SectionWatchlist && len(m.search.Results) > 0 {
			m.search.Section = SectionSuggestions
		}
		return m, nil

	case tea.KeyEnter:
		if m.search.Section == SectionWatchlist && len(m.search.WatchlistMatches) > 0 && m.search.WatchSelected < len(m.search.WatchlistMatches) {
			// Navigate to detail view for the matched watchlist item
			matchSymbol := m.search.WatchlistMatches[m.search.WatchSelected].Symbol
			for i, item := range m.items {
				if item.Symbol == matchSymbol {
					m.selected = i
					break
				}
			}
			m.mode = viewDetail
			m.relatedFocus = -1
			m.compareSymbol = nil
			return m, tea.Batch(m.fetchKeyStats(matchSymbol), m.fetchRelatedSymbols(matchSymbol))
		}
		if m.search.Section == SectionSuggestions && len(m.search.Results) > 0 && m.search.Selected < len(m.search.Results) {
			r := m.search.Results[m.search.Selected]
			if m.portfolioSearching {
				m.portfolioSearching = false
				return m.addPortfolioFromSearch(r.Symbol, r.Name)
			}
			return m.addSymbol(r.Symbol, r.Name)
		}
		return m, nil

	case tea.KeyUp:
		if m.search.Section == SectionSuggestions {
			if m.search.Selected > 0 {
				m.search.Selected--
			}
		} else {
			if m.search.WatchSelected > 0 {
				m.search.WatchSelected--
			}
		}
		return m, nil

	case tea.KeyDown:
		if m.search.Section == SectionSuggestions {
			if m.search.Selected < len(m.search.Results)-1 {
				m.search.Selected++
			}
		} else {
			if m.search.WatchSelected < len(m.search.WatchlistMatches)-1 {
				m.search.WatchSelected++
			}
		}
		return m, nil

	case tea.KeyBackspace:
		if len(m.search.Query) > 0 {
			m.search.Query = m.search.Query[:len(m.search.Query)-1]
			m.search.WatchlistMatches = m.filterWatchlist(m.search.Query)
			m.search.WatchSelected = 0
			if len(m.search.Query) >= 1 {
				m.search.Loading = true
				return m, m.doSearch(m.search.Query)
			}
			m.search.Results = nil
		}
		return m, nil

	case tea.KeyRunes:
		m.search.Query += string(msg.Runes)
		m.search.WatchlistMatches = m.filterWatchlist(m.search.Query)
		m.search.WatchSelected = 0
		if len(m.search.Query) >= 1 {
			m.search.Loading = true
			m.search.Selected = 0
			return m, m.doSearch(m.search.Query)
		}
		return m, nil
	}

	return m, nil
}

// === Timeframe ===

// ensureIndicatorContext returns a command to fetch the indicator
// backfill series for the item at idx when the current timeframe
// needs one and the cached context (if any) is at a different
// resolution. Returns nil when no fetch is needed.
func (m Model) ensureIndicatorContext(idx int) tea.Cmd {
	if idx < 0 || idx >= len(m.items) {
		return nil
	}
	tf := m.currentTimeframe()
	rangeStr, interval, ok := indicatorContextParams(tf.Interval)
	if !ok {
		return nil
	}
	it := m.items[idx]
	if it.IndicatorContext != nil && it.IndicatorContextInterval == interval {
		return nil
	}
	return m.fetchIndicatorContext(it.Symbol, rangeStr, interval)
}

func (m Model) prevTimeframe() (tea.Model, tea.Cmd) {
	if m.timeframeIdx > 0 {
		m.timeframeIdx--
		cmds := []tea.Cmd{m.fetchAllCharts()}
		if m.mode == viewDetail {
			cmds = append(cmds, m.fetchAllRelatedCharts())
		}
		// Refresh indicator backfill for ALL items so inline MA
		// overlays (watchlist + detail + related) have enough
		// same-resolution lookback (e.g. 199 bars for EMA200) from
		// the first visible bar onward at the new timeframe.
		if c := m.fetchAllIndicatorContexts(); c != nil {
			cmds = append(cmds, c)
		}
		return m, tea.Batch(cmds...)
	}
	return m, nil
}

func (m Model) nextTimeframe() (tea.Model, tea.Cmd) {
	if m.timeframeIdx < len(timeframes)-1 {
		m.timeframeIdx++
		cmds := []tea.Cmd{m.fetchAllCharts()}
		if m.mode == viewDetail {
			cmds = append(cmds, m.fetchAllRelatedCharts())
		}
		if c := m.fetchAllIndicatorContexts(); c != nil {
			cmds = append(cmds, c)
		}
		return m, tea.Batch(cmds...)
	}
	return m, nil
}

// === Data Operations ===

func (m Model) handleQuotes(msg quotesMsg) (tea.Model, tea.Cmd) {
	targets := make(map[string]struct{}, len(msg.symbols))
	for _, symbol := range msg.symbols {
		targets[symbol] = struct{}{}
	}

	if msg.err != nil {
		if msg.background && m.hasLoadedQuotes() {
			return m, nil
		}
		m.err = msg.err.Error()
		for i := range m.items {
			if len(targets) > 0 {
				if _, ok := targets[m.items[i].Symbol]; !ok {
					continue
				}
			}
			m.items[i].Loading = false
			if m.items[i].Quote == nil {
				m.items[i].Error = "Failed to fetch"
			}
		}
		return m, nil
	}

	m.err = ""
	m.lastUpdate = time.Now()

	// NOTE: Cache writes happen in the network-fetch goroutine so that
	// cache-first hits don't re-stamp the cache with the same stale data
	// (which would keep stale entries "fresh" across restarts).

	quoteMap := make(map[string]yahoo.Quote)
	for _, q := range msg.quotes {
		quoteMap[q.Symbol] = q
	}

	for i := range m.items {
		if len(targets) > 0 {
			if _, ok := targets[m.items[i].Symbol]; !ok {
				continue
			}
		}
		if q, ok := quoteMap[m.items[i].Symbol]; ok {
			m.items[i].Quote = &q
			m.items[i].Loading = false
			m.items[i].Error = ""
			if m.items[i].Name == "" {
				m.items[i].Name = q.Name
			}
		} else {
			// Symbol not in response — stop loading, show as available without quote
			m.items[i].Loading = false
		}
	}

	m.applyQuotesToPortfolio(msg.quotes)

	return m, nil
}

func (m Model) handleChart(msg chartMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil && msg.background && m.hasLoadedChart(msg.symbol) {
		return m, nil
	}
	for i := range m.items {
		if m.items[i].Symbol == msg.symbol {
			m.items[i].Loading = false
			if msg.err != nil {
				m.items[i].Error = "Chart: " + msg.err.Error()
			} else {
				// If the background refresh produced the same data that
				// the cache already painted, skip the reassignment so
				// we don't trigger a flicker-inducing repaint.
				if msg.background && chartDataEqual(m.items[i].ChartData, msg.data) {
					break
				}
				m.items[i].ChartData = msg.data
				// NOTE: Cache writes happen in the network-fetch goroutine
				// so that cache-first hits don't re-stamp the cache with the
				// same stale data (which would keep stale entries "fresh"
				// across restarts).
			}
			break
		}
	}
	if msg.err == nil {
		m.applyChartToPortfolio(msg.symbol, msg.data)
	}
	return m, nil
}

// chartDataEqual reports whether two ChartData snapshots have the
// same timestamps and closes. Used to elide redundant repaints when
// a background refresh returns the exact same bars already painted
// from cache. When only the last bar's close ticks (sub-bar updates
// during live trading), we still repaint — but because timestamps
// are identical, the chart updates the last bar in place without
// shifting older bars.
func chartDataEqual(a, b *yahoo.ChartData) bool {
	if a == nil || b == nil {
		return a == b
	}
	if len(a.Closes) != len(b.Closes) || len(a.Timestamps) != len(b.Timestamps) {
		return false
	}
	for i := range a.Timestamps {
		if a.Timestamps[i] != b.Timestamps[i] {
			return false
		}
	}
	for i := range a.Closes {
		if a.Closes[i] != b.Closes[i] {
			return false
		}
	}
	return true
}

// cycleChartStyle rotates between the supported chart renderings:
// candlestick_dotted → candlestick → dotted_line → candlestick_dotted.
func (m *Model) cycleChartStyle() {
	if m.cfg == nil {
		return
	}
	switch m.cfg.ChartStyle {
	case "candlestick_dotted":
		m.cfg.ChartStyle = "candlestick"
	case "candlestick":
		m.cfg.ChartStyle = "dotted_line"
	default: // dotted_line or anything unexpected
		m.cfg.ChartStyle = "candlestick_dotted"
	}
}

// handleIndicatorContext stores the backfill series used by the
// technicals panel. The stored interval lets callers invalidate
// stale context on timeframe changes.
func (m Model) handleIndicatorContext(msg indicatorContextMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil || msg.data == nil || len(msg.data.Closes) == 0 {
		return m, nil
	}
	for i := range m.items {
		if m.items[i].Symbol == msg.symbol {
			m.items[i].IndicatorContext = msg.data
			m.items[i].IndicatorContextInterval = msg.interval
			break
		}
	}
	return m, nil
}

func (m Model) handleSearchResults(msg searchMsg) (tea.Model, tea.Cmd) {
	m.search.Loading = false
	if msg.err != nil {
		m.search.Error = msg.err.Error()
	} else {
		m.search.Results = msg.results
		m.search.Error = ""
	}
	return m, nil
}

func (m Model) handleKeyStats(msg keyStatsMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil || msg.stats == nil {
		return m, nil
	}
	for i := range m.items {
		if m.items[i].Symbol == msg.symbol && m.items[i].Quote != nil {
			peg := msg.stats.PEGRatio
			// If Yahoo doesn't provide PEG, compute: PEG = Trailing PE / (Annual EPS Growth × 100)
			if peg == 0 && msg.stats.AnnualGrowth > 0 && m.items[i].Quote.PE > 0 {
				growthPct := msg.stats.AnnualGrowth * 100 // e.g. 0.7389 -> 73.89
				peg = m.items[i].Quote.PE / growthPct
			}
			m.items[i].Quote.PEG = peg
			if msg.stats.Beta > 0 {
				m.items[i].Quote.Beta = msg.stats.Beta
			}
			break
		}
	}
	return m, nil
}

func (m Model) handleFinancials(msg financialsMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		// If financials failed and we were waiting, clear pending flags and show error
		if m.pendingEarnings {
			m.pendingEarnings = false
			m.earningsLoading = false
			m.earningsText = "Error loading financial data: " + msg.err.Error()
		}
		if m.pendingAI {
			m.pendingAI = false
			m.aiLoading = false
			m.aiText = "Error loading financial data: " + msg.err.Error()
		}
		return m, nil
	}
	for i := range m.items {
		if m.items[i].Symbol == msg.symbol {
			m.items[i].Financials = msg.financials
			if m.cache != nil && msg.financials != nil {
				m.cache.PutFinancials(msg.symbol, msg.financials)
			}
			break
		}
	}

	// If AI or earnings was waiting for financials, trigger them now
	var cmds []tea.Cmd
	if m.pendingAI && m.selected < len(m.items) && m.items[m.selected].Symbol == msg.symbol {
		m.pendingAI = false
		cmds = append(cmds, m.fetchAIAnalysis(m.items[m.selected]))
	}
	if m.pendingEarnings && m.selected < len(m.items) && m.items[m.selected].Symbol == msg.symbol {
		m.pendingEarnings = false
		cmds = append(cmds, m.fetchEarningsReport(m.items[m.selected]))
	}
	if len(cmds) > 0 {
		return m, tea.Batch(cmds...)
	}
	return m, nil
}

func (m Model) handleHolders(msg holdersMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m, nil
	}
	for i := range m.items {
		if m.items[i].Symbol == msg.symbol {
			m.items[i].Holders = msg.holders
			if m.cache != nil && msg.holders != nil {
				m.cache.PutHolders(msg.symbol, msg.holders)
			}
			break
		}
	}
	return m, nil
}

func (m Model) filterWatchlist(query string) []WatchlistItem {
	if query == "" {
		return nil
	}
	q := strings.ToUpper(query)
	var matches []WatchlistItem
	for _, item := range m.items {
		if strings.Contains(strings.ToUpper(item.Symbol), q) || strings.Contains(strings.ToUpper(item.Name), q) {
			matches = append(matches, item)
		}
	}
	return matches
}

// === AI Analysis ===

func (m Model) handleAI(msg aiMsg) (tea.Model, tea.Cmd) {
	m.aiLoading = false
	if msg.err != nil {
		m.aiText = "Error: " + msg.err.Error()
	} else {
		m.aiText = msg.text
		m.aiSymbol = msg.symbol
	}
	return m, nil
}

func (m Model) fetchAIAnalysis(item WatchlistItem) tea.Cmd {
	client := m.llmClient
	if client == nil {
		return nil
	}

	symbol := item.Symbol
	dataCache := m.cache

	// Check cache first
	if dataCache != nil {
		if cached := dataCache.GetText(symbol, "ai"); cached != "" {
			return func() tea.Msg {
				return aiMsg{symbol: symbol, text: cached}
			}
		}
	}

	q := llm.QuoteData{Symbol: item.Symbol, Name: item.Name}
	if item.Quote != nil {
		q.Price = item.Quote.Price
		q.Change = item.Quote.Change
		q.ChangePercent = item.Quote.ChangePercent
		q.MarketCap = item.Quote.MarketCap
		q.PE = item.Quote.PE
		q.ForwardPE = item.Quote.ForwardPE
		q.EPS = item.Quote.EPS
		q.PEG = item.Quote.PEG
		q.Beta = item.Quote.Beta
		q.FiftyTwoWeekHigh = item.Quote.FiftyTwoWeekHigh
		q.FiftyTwoWeekLow = item.Quote.FiftyTwoWeekLow
		q.DividendYield = item.Quote.DividendYield
		q.Volume = item.Quote.Volume
		q.AvgVolume = item.Quote.AvgVolume
		q.DayHigh = item.Quote.DayHigh
		q.DayLow = item.Quote.DayLow
		q.Open = item.Quote.Open
		q.PreviousClose = item.Quote.PreviousClose
	}

	var fin *llm.FinancialSummary
	if item.Financials != nil {
		fin = &llm.FinancialSummary{
			ProfitMargin:      item.Financials.ProfitMargin,
			RevenueGrowth:     item.Financials.RevenueGrowth,
			EarningsGrowth:    item.Financials.EarningsGrowth,
			DebtToEquity:      item.Financials.DebtToEquity,
			CurrentRatio:      item.Financials.CurrentRatio,
			FreeCashflow:      item.Financials.FreeCashflow,
			GrossMargins:      item.Financials.GrossMargins,
			OperatingMargins:  item.Financials.OperatingMargins,
			ReturnOnEquity:    item.Financials.ReturnOnEquity,
			RecommendationKey: item.Financials.RecommendationKey,
			TargetMeanPrice:   item.Financials.TargetMeanPrice,
			TargetHighPrice:   item.Financials.TargetHighPrice,
			TargetLowPrice:    item.Financials.TargetLowPrice,
			NumberOfAnalysts:  item.Financials.NumberOfAnalysts,
		}
	}

	userPrompt := llm.BuildUserPrompt(q, fin)

	return func() tea.Msg {
		ctx := context.Background()
		text, err := client.Chat(ctx, llm.SystemPrompt(), userPrompt)
		if err == nil && text != "" && dataCache != nil {
			dataCache.PutText(symbol, "ai", text)
		}
		return aiMsg{symbol: symbol, text: text, err: err}
	}
}

func (m Model) renderAIPopup() string {
	title := helpKeyStyle.Render(fmt.Sprintf("  ◆ AI Analysis — %s", m.aiSymbol))

	var content string
	if m.aiLoading {
		content = nameStyle.Render("  Analyzing... (waiting for LLM response)")
	} else {
		maxWidth := m.width - 12
		if maxWidth < 40 {
			maxWidth = 40
		}
		content = renderMarkdown(m.aiText, maxWidth)
	}

	footer := helpStyle.Render("  a/Esc close · R refresh")

	return title + "\n\n" + content + "\n\n" + footer
}

// === Watchlist Summary ===

func (m Model) handleSummary(msg summaryMsg) (tea.Model, tea.Cmd) {
	m.summaryLoading = false
	if msg.err != nil {
		m.summaryText = "Error: " + msg.err.Error()
	} else {
		m.summaryText = msg.text
		m.summaryGroup = msg.group
	}
	return m, nil
}

func (m Model) fetchSummary() tea.Cmd {
	client := m.llmClient
	if client == nil {
		return nil
	}

	group := m.activeGroup
	groupName := m.cfg.Watchlists[group].Name
	dataCache := m.cache

	// Check cache first
	if dataCache != nil {
		if cached := dataCache.GetText("summary_"+groupName, "summary"); cached != "" {
			return func() tea.Msg {
				return summaryMsg{group: group, text: cached}
			}
		}
	}

	// Build quote data for all items
	items := make([]llm.QuoteData, 0, len(m.items))
	symbols := make([]string, 0, len(m.items))
	for _, item := range m.items {
		q := llm.QuoteData{Symbol: item.Symbol, Name: item.Name}
		if item.Quote != nil {
			q.Price = item.Quote.Price
			q.Change = item.Quote.Change
			q.ChangePercent = item.Quote.ChangePercent
			q.MarketCap = item.Quote.MarketCap
			q.PE = item.Quote.PE
			q.ForwardPE = item.Quote.ForwardPE
			q.EPS = item.Quote.EPS
			q.PEG = item.Quote.PEG
			q.Beta = item.Quote.Beta
			q.FiftyTwoWeekHigh = item.Quote.FiftyTwoWeekHigh
			q.FiftyTwoWeekLow = item.Quote.FiftyTwoWeekLow
			q.DividendYield = item.Quote.DividendYield
			q.Volume = item.Quote.Volume
			q.AvgVolume = item.Quote.AvgVolume
			q.DayHigh = item.Quote.DayHigh
			q.DayLow = item.Quote.DayLow
			q.Open = item.Quote.Open
			q.PreviousClose = item.Quote.PreviousClose
		}
		items = append(items, q)
		symbols = append(symbols, item.Symbol)
	}

	yahooClient := m.client

	return func() tea.Msg {
		// Fetch 5d charts for weekly summary data
		weekData := make(map[string]*llm.WeekSummary, len(symbols))
		for _, sym := range symbols {
			cd, err := yahooClient.GetChart(sym, "5d", "15m")
			if err != nil || cd == nil || len(cd.Closes) == 0 {
				continue
			}
			w := &llm.WeekSummary{
				WeekClose: cd.Closes[len(cd.Closes)-1],
			}
			if len(cd.Opens) > 0 {
				w.WeekOpen = cd.Opens[0]
			}
			// Compute week high and low
			w.WeekHigh = cd.Closes[0]
			w.WeekLow = cd.Closes[0]
			for _, h := range cd.Highs {
				if h > w.WeekHigh {
					w.WeekHigh = h
				}
			}
			for _, l := range cd.Lows {
				if l > 0 && l < w.WeekLow {
					w.WeekLow = l
				}
			}
			if w.WeekOpen > 0 {
				w.WeekChangePct = (w.WeekClose - w.WeekOpen) / w.WeekOpen * 100
			}

			// Build per-day open/close from intraday bars
			if len(cd.Timestamps) == len(cd.Closes) && len(cd.Opens) == len(cd.Closes) {
				type dayAcc struct {
					date      string
					firstOpen float64
					lastClose float64
					high, low float64
					volume    int64
				}
				var days []dayAcc
				prevDate := ""
				for j := range cd.Timestamps {
					t := time.Unix(cd.Timestamps[j], 0)
					d := t.Format("Mon 01/02")
					var barVol int64
					if j < len(cd.Volumes) {
						barVol = cd.Volumes[j]
					}
					if d != prevDate {
						days = append(days, dayAcc{
							date:      d,
							firstOpen: cd.Opens[j],
							lastClose: cd.Closes[j],
							high:      cd.Highs[j],
							low:       cd.Lows[j],
							volume:    barVol,
						})
						prevDate = d
					} else {
						cur := &days[len(days)-1]
						cur.lastClose = cd.Closes[j]
						cur.volume += barVol
						if cd.Highs[j] > cur.high {
							cur.high = cd.Highs[j]
						}
						if cd.Lows[j] > 0 && cd.Lows[j] < cur.low {
							cur.low = cd.Lows[j]
						}
					}
				}
				var totalVol int64
				for _, da := range days {
					totalVol += da.volume
					w.Days = append(w.Days, llm.DaySummary{
						Date:   da.date,
						Open:   da.firstOpen,
						Close:  da.lastClose,
						High:   da.high,
						Low:    da.low,
						Volume: da.volume,
					})
				}
				w.WeekVolume = totalVol
			}

			weekData[sym] = w
		}

		userPrompt := llm.BuildSummaryPrompt(groupName, items, weekData)

		ctx := context.Background()
		text, err := client.Chat(ctx, llm.SummarySystemPrompt(), userPrompt)
		if err == nil && text != "" && dataCache != nil {
			dataCache.PutText("summary_"+groupName, "summary", text)
		}
		return summaryMsg{group: group, text: text, err: err}
	}
}

func (m Model) renderSummaryPopup() string {
	groupName := ""
	if m.activeGroup < len(m.cfg.Watchlists) {
		groupName = m.cfg.Watchlists[m.activeGroup].Name
	}
	title := helpKeyStyle.Render(fmt.Sprintf("  ◆ Watchlist Summary — %s", groupName))

	var content string
	if m.summaryLoading {
		content = nameStyle.Render("  Analyzing watchlist... (waiting for LLM response)")
	} else {
		maxWidth := m.width - 12
		if maxWidth < 40 {
			maxWidth = 40
		}
		content = renderMarkdown(m.summaryText, maxWidth)
	}

	footer := helpStyle.Render("  S/Esc close · R refresh")

	return title + "\n\n" + content + "\n\n" + footer
}

// wrapText wraps s to the given width, preserving existing newlines.
func wrapText(s string, width int) string {
	var out strings.Builder
	for _, para := range strings.Split(s, "\n") {
		if para == "" {
			out.WriteString("\n")
			continue
		}
		col := 0
		for _, word := range strings.Fields(para) {
			wLen := len(word)
			if col > 0 && col+1+wLen > width {
				out.WriteString("\n")
				col = 0
			}
			if col > 0 {
				out.WriteString(" ")
				col++
			}
			out.WriteString(word)
			col += wLen
		}
		out.WriteString("\n")
	}
	return out.String()
}

// === Earnings Report ===

func (m Model) handleEarnings(msg earningsMsg) (tea.Model, tea.Cmd) {
	m.earningsLoading = false
	m.earningsDiscovery = msg.discovery
	m.earningsLinkSel = 0
	m.earningsLinkStatus = ""
	if msg.err != nil {
		m.earningsText = "Error: " + msg.err.Error()
	} else if msg.report != nil {
		m.earningsText = msg.report.Analysis
		m.earningsSymbol = msg.report.Symbol
		if msg.report.FilingForm != "" {
			m.earningsFiling = fmt.Sprintf("%s (%s)", msg.report.FilingForm, msg.report.FilingDate)
		} else {
			m.earningsFiling = "Yahoo Finance data"
		}
	}
	return m, nil
}

func (m Model) fetchEarningsReport(item WatchlistItem) tea.Cmd {
	client := m.llmClient
	edgarCl := m.edgarClient
	if client == nil {
		return nil
	}

	symbol := item.Symbol
	dataCache := m.cache

	// Check cache first
	if dataCache != nil {
		maxAge := m.earningsReportCacheMaxAge(item)
		if cached := dataCache.GetTextFresh(symbol, "earnings", maxAge); cached != "" {
			return func() tea.Msg {
				res, _ := m.buildEarningsWebContext(context.Background(), symbol)
				if res != nil {
					_, _ = m.persistEarningsWebDiscoveryEvents(context.Background(), symbol, res)
				}
				return earningsMsg{symbol: symbol, report: &llm.EarningsReport{
					Symbol:   symbol,
					Analysis: cached,
				}, discovery: res}
			}
		}
	}

	if !m.shouldUseEDGARForEarnings() {
		edgarCl = nil
	}

	// Build Yahoo Finance data string
	q := llm.QuoteData{Symbol: item.Symbol, Name: item.Name}
	if item.Quote != nil {
		q.Price = item.Quote.Price
		q.Change = item.Quote.Change
		q.ChangePercent = item.Quote.ChangePercent
		q.MarketCap = item.Quote.MarketCap
		q.PE = item.Quote.PE
		q.ForwardPE = item.Quote.ForwardPE
		q.EPS = item.Quote.EPS
		q.PEG = item.Quote.PEG
		q.Beta = item.Quote.Beta
		q.FiftyTwoWeekHigh = item.Quote.FiftyTwoWeekHigh
		q.FiftyTwoWeekLow = item.Quote.FiftyTwoWeekLow
		q.DividendYield = item.Quote.DividendYield
		q.Volume = item.Quote.Volume
		q.AvgVolume = item.Quote.AvgVolume
	}
	var fin *llm.FinancialSummary
	if item.Financials != nil {
		fin = &llm.FinancialSummary{
			ProfitMargin:      item.Financials.ProfitMargin,
			RevenueGrowth:     item.Financials.RevenueGrowth,
			EarningsGrowth:    item.Financials.EarningsGrowth,
			DebtToEquity:      item.Financials.DebtToEquity,
			CurrentRatio:      item.Financials.CurrentRatio,
			FreeCashflow:      item.Financials.FreeCashflow,
			GrossMargins:      item.Financials.GrossMargins,
			OperatingMargins:  item.Financials.OperatingMargins,
			ReturnOnEquity:    item.Financials.ReturnOnEquity,
			RecommendationKey: item.Financials.RecommendationKey,
			TargetMeanPrice:   item.Financials.TargetMeanPrice,
			TargetHighPrice:   item.Financials.TargetHighPrice,
			TargetLowPrice:    item.Financials.TargetLowPrice,
			NumberOfAnalysts:  item.Financials.NumberOfAnalysts,
		}
	}
	yahooData := llm.BuildUserPrompt(q, fin)

	// Append historical revenue/earnings and EPS data for earnings analysis
	if item.Financials != nil {
		var quarterly, yearly []llm.HistoricalPeriod
		for _, p := range item.Financials.Quarterly {
			quarterly = append(quarterly, llm.HistoricalPeriod{Date: p.Date, Revenue: p.Revenue, Earnings: p.Earnings})
		}
		for _, p := range item.Financials.Yearly {
			yearly = append(yearly, llm.HistoricalPeriod{Date: p.Date, Revenue: p.Revenue, Earnings: p.Earnings})
		}
		var eps []llm.EPSQuarter
		for _, e := range item.Financials.EPSHistory {
			eps = append(eps, llm.EPSQuarter{Quarter: e.Quarter, EPSActual: e.EPSActual, EPSEstimate: e.EPSEstimate, SurprisePercent: e.SurprisePercent})
		}
		logger.Log("Earnings data for %s: quarterly=%d, yearly=%d, eps=%d", symbol, len(quarterly), len(yearly), len(eps))
		yahooData = llm.BuildEarningsData(yahooData, quarterly, yearly, eps)
	} else {
		logger.Log("Earnings data for %s: Financials is nil", symbol)
	}
	logger.Log("Earnings yahooData for %s (%d chars):\n%s", symbol, len(yahooData), yahooData)

	return func() tea.Msg {
		ctx := context.Background()
		promptData := yahooData
		var discovery *earnings.DiscoveryResult
		if m.earningsPoller != nil {
			pollCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			if n, err := m.earningsPoller.PollSymbol(pollCtx, symbol); err != nil {
				logger.Log("earnings: IR poll failed for %s: %v", symbol, err)
			} else if n > 0 {
				logger.Log("earnings: IR poll discovered %d new release event(s) for %s", n, symbol)
			}
			cancel()
		}
		if res, webCtx := m.buildEarningsWebContext(ctx, symbol); webCtx != "" {
			discovery = res
			promptData += "\n\n" + webCtx
		}
		if discovery != nil {
			if n, err := m.persistEarningsWebDiscoveryEvents(ctx, symbol, discovery); err != nil {
				logger.Log("earnings: failed to persist discovery links for %s: %v", symbol, err)
			} else if n > 0 {
				logger.Log("earnings: stored %d web discovery event(s) for %s", n, symbol)
			}
		}
		report, err := client.AnalyzeEarnings(ctx, edgarCl, symbol, promptData)
		if err == nil && report != nil && report.Analysis != "" && dataCache != nil {
			dataCache.PutText(symbol, "earnings", report.Analysis)
		}
		return earningsMsg{symbol: symbol, report: report, discovery: discovery, err: err}
	}
}

func (m Model) buildEarningsWebContext(ctx context.Context, symbol string) (*earnings.DiscoveryResult, string) {
	if m.cfg == nil {
		return nil, ""
	}
	key := strings.ToUpper(strings.TrimSpace(symbol))
	pageURL := strings.TrimSpace(m.cfg.Earnings.IRPageRegistry[key])
	searchHint := strings.TrimSpace(m.cfg.Earnings.SearchHints[key])
	// Fall back to ticker-based hint so web search always runs
	if searchHint == "" {
		searchHint = key + " earnings results quarterly"
	}

	discoverCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	res, err := earnings.DiscoverEarningsWebLinks(discoverCtx, key, earnings.DiscoveryOptions{
		PageURL:       pageURL,
		SearchHint:    searchHint,
		MaxCandidates: 6,
		CrawlerCmd:    strings.TrimSpace(m.cfg.Earnings.WebCrawlerCommand),
		CrawlerArgs:   m.cfg.Earnings.WebCrawlerArgs,
	})
	if err != nil {
		logger.Log("earnings: web discovery failed for %s: %v", key, err)
		return nil, ""
	}
	block := res.PromptBlock()
	if block != "" {
		logger.Log("earnings: web discovery found %d candidate links for %s", len(res.Candidates), key)
	}
	return res, block
}

func (m Model) persistEarningsWebDiscoveryEvents(ctx context.Context, symbol string, res *earnings.DiscoveryResult) (int, error) {
	if m.earningsDB == nil || res == nil || len(res.Candidates) == 0 {
		return 0, nil
	}
	now := time.Now().UTC()
	stored := 0
	sym := strings.ToUpper(strings.TrimSpace(symbol))
	for _, c := range res.Candidates {
		url := strings.TrimSpace(c.URL)
		if url == "" {
			continue
		}
		title := strings.TrimSpace(c.Title)
		if title == "" {
			title = "Investor relations discovery link"
		}
		meta, _ := json.Marshal(map[string]any{
			"discovery_source": c.Source,
			"link_kind":        c.Kind,
			"score":            c.Score,
			"ir_page":          strings.TrimSpace(res.PageURL),
		})
		payload := sym + "|company_ir_web|" + url + "|" + strings.ToLower(strings.TrimSpace(c.Kind))
		h := sha256.Sum256([]byte(payload))
		fp := hex.EncodeToString(h[:16])

		ev := &db.EarningsEvent{
			Symbol:       sym,
			SourceType:   "company_ir_web",
			SourceName:   c.Source,
			Title:        title,
			URL:          url,
			DetectedAt:   now,
			IngestedAt:   now,
			Fingerprint:  fp,
			Confidence:   "medium",
			Status:       "new",
			MetadataJSON: string(meta),
		}
		inserted, err := m.earningsDB.InsertEarningsEventIfNew(ctx, ev)
		if err != nil {
			return stored, err
		}
		if inserted {
			stored++
		}
	}
	return stored, nil
}

func (m Model) earningsReportCacheMaxAge(item WatchlistItem) time.Duration {
	cfg := m.cfg.Earnings
	now := time.Now()

	if m.isWithinEarningsHighFreqWindow(item, now) {
		return time.Duration(cfg.HighFreqPollingSeconds) * time.Second
	}

	hour := now.Hour()
	if hour >= 8 && hour < 20 {
		return time.Duration(cfg.NormalPollingSeconds) * time.Second
	}

	return time.Duration(cfg.OffHoursPollingSeconds) * time.Second
}

func (m Model) isWithinEarningsHighFreqWindow(item WatchlistItem, now time.Time) bool {
	if item.Financials == nil || item.Financials.NextEarningsDate == "" {
		return false
	}
	baseDate, err := time.Parse("2006-01-02", item.Financials.NextEarningsDate)
	if err != nil {
		return false
	}
	locNow := now.Local()
	releaseDay := time.Date(baseDate.Year(), baseDate.Month(), baseDate.Day(), 0, 0, 0, 0, locNow.Location())
	start := releaseDay.Add(-time.Duration(m.cfg.Earnings.WindowBeforeMinutes) * time.Minute)
	end := releaseDay.Add(time.Duration(24*60+m.cfg.Earnings.WindowAfterMinutes) * time.Minute)
	return !locNow.Before(start) && !locNow.After(end)
}

func (m Model) shouldUseEDGARForEarnings() bool {
	priority := m.cfg.Earnings.SourcePriority
	if len(priority) == 0 {
		return true
	}
	first := strings.ToLower(strings.TrimSpace(priority[0]))
	if first == "yahoo" {
		logger.Log("earnings: source_priority starts with yahoo; skipping EDGAR for earnings report")
		return false
	}
	for _, src := range priority {
		if strings.ToLower(strings.TrimSpace(src)) == "edgar" {
			return true
		}
	}
	logger.Log("earnings: edgar not listed in source_priority; skipping EDGAR for earnings report")
	return false
}

func (m Model) renderEarningsPopup() string {
	titleText := fmt.Sprintf("  ◆ Earnings Report — %s", m.earningsSymbol)
	if m.earningsFiling != "" {
		titleText += fmt.Sprintf(" [%s]", m.earningsFiling)
	}
	title := helpKeyStyle.Render(titleText)

	var content string
	if m.earningsLoading {
		content = nameStyle.Render("  Fetching SEC filing & analyzing... (this may take a moment)")
	} else {
		maxWidth := m.width - 12
		if maxWidth < 40 {
			maxWidth = 40
		}
		content = renderMarkdown(m.earningsText, maxWidth)
		if links := m.renderEarningsDiscoveryLinks(maxWidth); links != "" {
			content += "\n\n" + links
		}
	}

	footerText := "  e/Esc close · R refresh · n/p select link · c/Enter copy"
	if strings.TrimSpace(m.earningsLinkStatus) != "" {
		footerText += " · " + strings.TrimSpace(m.earningsLinkStatus)
	}
	footer := helpStyle.Render(footerText)

	return title + "\n\n" + content + "\n\n" + footer
}

func (m Model) renderEarningsDiscoveryLinks(width int) string {
	if m.earningsDiscovery == nil || len(m.earningsDiscovery.Candidates) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("### Discovered Report Links\n")
	for i, c := range m.earningsDiscovery.Candidates {
		if i >= 5 {
			break
		}
		kind := "page"
		if strings.EqualFold(strings.TrimSpace(c.Kind), "pdf") {
			kind = "pdf"
		}
		label := strings.TrimSpace(c.Title)
		if label == "" {
			label = c.URL
		}
		prefix := "- "
		if i == m.earningsLinkSel {
			prefix = "> "
		}
		b.WriteString(prefix)
		b.WriteString("[")
		b.WriteString(kind)
		b.WriteString("] ")
		b.WriteString(label)
		b.WriteString(" -> ")
		b.WriteString(c.URL)
		b.WriteString("\n")
	}
	return renderMarkdown(b.String(), width)
}

func (m *Model) stepEarningsLinkSelection(step int) {
	maxLinks := m.earningsDiscoveryLinkCount()
	if maxLinks == 0 {
		m.earningsLinkStatus = "no discovered links"
		return
	}
	m.earningsLinkSel += step
	if m.earningsLinkSel < 0 {
		m.earningsLinkSel = maxLinks - 1
	}
	if m.earningsLinkSel >= maxLinks {
		m.earningsLinkSel = 0
	}
	m.earningsLinkStatus = ""
}

func (m *Model) copySelectedEarningsLink() {
	maxLinks := m.earningsDiscoveryLinkCount()
	if maxLinks == 0 {
		m.earningsLinkStatus = "no discovered links"
		return
	}
	if m.earningsLinkSel < 0 || m.earningsLinkSel >= maxLinks {
		m.earningsLinkSel = 0
	}
	selected := m.earningsDiscovery.Candidates[m.earningsLinkSel]
	url := strings.TrimSpace(selected.URL)
	if url == "" {
		m.earningsLinkStatus = "selected link is empty"
		return
	}
	copyText := url
	label := strings.TrimSpace(selected.Title)
	if label != "" {
		label = strings.ReplaceAll(label, "[", "\\[")
		label = strings.ReplaceAll(label, "]", "\\]")
		copyText = "[" + label + "](" + url + ")"
	}
	helper, err := copyToClipboard(copyText)
	if err != nil {
		m.earningsLinkStatus = "copy failed"
		return
	}
	m.earningsLinkStatus = "copied via " + helper
}

func (m Model) earningsDiscoveryLinkCount() int {
	if m.earningsDiscovery == nil {
		return 0
	}
	n := len(m.earningsDiscovery.Candidates)
	if n > 5 {
		n = 5
	}
	return n
}

// === Commands ===

func (m Model) fetchQuotes() tea.Cmd {
	symbols := make([]string, len(m.items))
	for i, item := range m.items {
		symbols[i] = item.Symbol
	}
	return m.fetchQuotesForSymbols(symbols, true, false)
}

func (m Model) refreshQuotes(background bool) tea.Cmd {
	symbols := make([]string, len(m.items))
	for i, item := range m.items {
		symbols[i] = item.Symbol
	}
	return m.fetchQuotesForSymbols(symbols, false, background)
}

func (m Model) refreshQuote(symbol string, background bool) tea.Cmd {
	return m.fetchQuotesForSymbols([]string{symbol}, false, background)
}

func (m Model) fetchQuotesForSymbols(symbols []string, useCacheFirst, background bool) tea.Cmd {
	if len(symbols) == 0 {
		return nil
	}

	// Try cache first — but only if it covers ALL requested symbols.
	if useCacheFirst && m.cache != nil {
		if cached := m.cache.GetQuotes(symbols); cached != nil {
			cachedSet := make(map[string]bool, len(cached))
			for _, q := range cached {
				cachedSet[q.Symbol] = true
			}
			allCovered := true
			for _, s := range symbols {
				if !cachedSet[s] {
					allCovered = false
					break
				}
			}
			if allCovered {
				return func() tea.Msg {
					return quotesMsg{quotes: cached, symbols: symbols}
				}
			}
		}
	}

	client := m.client
	cacheRef := m.cache
	return func() tea.Msg {
		quotes, err := client.GetQuotes(symbols)
		if err == nil && cacheRef != nil && len(quotes) > 0 {
			// Persist freshly-fetched quotes so the next cold start and
			// the next cache hit both see up-to-date data.
			cacheRef.PutQuotes(quotes)
		}
		if err != nil && !background && cacheRef != nil {
			// Network error: fall back to stale cache
			if stale := cacheRef.GetQuotesStale(symbols); stale != nil {
				return quotesMsg{quotes: stale, symbols: symbols}
			}
		}
		return quotesMsg{quotes: quotes, symbols: symbols, err: err, background: background}
	}
}

func (m Model) fetchHeatmapFeed(kind HeatmapType) tea.Cmd {
	client := m.client
	if client == nil {
		logger.Log("heatmap: feed request type=%s client=nil", heatmapTypeName(kind))
		return func() tea.Msg {
			return heatmapFeedMsg{kind: kind, err: fmt.Errorf("yahoo client not initialized")}
		}
	}
	logger.Log("heatmap: feed request type=%s", heatmapTypeName(kind))

	return func() tea.Msg {
		var (
			symbols []string
			err     error
		)
		switch kind {
		case HeatmapMostActive:
			symbols, err = client.GetMostActiveSymbols(60)
		case HeatmapTrendNow:
			symbols, err = client.GetTrendingSymbols("US", 60)
		default:
			return heatmapFeedMsg{kind: kind, err: fmt.Errorf("unsupported heatmap feed type")}
		}
		if err != nil {
			logger.Log("heatmap: symbol fetch error type=%s err=%v", heatmapTypeName(kind), err)
			return heatmapFeedMsg{kind: kind, err: err}
		}
		if len(symbols) == 0 {
			logger.Log("heatmap: symbol fetch empty type=%s", heatmapTypeName(kind))
			return heatmapFeedMsg{kind: kind, quotes: nil}
		}
		logger.Log("heatmap: symbol fetch ok type=%s symbols=%d", heatmapTypeName(kind), len(symbols))

		quotes, err := client.GetQuotes(symbols)
		if err != nil {
			logger.Log("heatmap: quote fetch error type=%s symbols=%d err=%v", heatmapTypeName(kind), len(symbols), err)
			return heatmapFeedMsg{kind: kind, err: err}
		}
		logger.Log("heatmap: quote fetch ok type=%s symbols=%d quotes=%d", heatmapTypeName(kind), len(symbols), len(quotes))
		return heatmapFeedMsg{kind: kind, quotes: quotes}
	}
}

func (m Model) fetchChart(symbol string) tea.Cmd {
	return m.fetchChartWithPolicy(symbol, true, false)
}

func (m Model) refreshChart(symbol string, background bool) tea.Cmd {
	return m.fetchChartWithPolicy(symbol, false, background)
}

func (m Model) fetchChartWithPolicy(symbol string, useCacheFirst, background bool) tea.Cmd {
	tf := m.currentTimeframe()

	// Determine if we need pre/post market data
	includePrePost := false
	chartRange := tf.Range
	chartInterval := tf.Interval

	// Check market state from quote data
	var marketState string
	for _, item := range m.items {
		if item.Symbol == symbol && item.Quote != nil {
			marketState = item.Quote.MarketState
			break
		}
	}

	const maxBars1D = 96 // 8 hours of 5m bars

	if tf.Label == "1D" {
		if marketState == "REGULAR" {
			// Market is open: use 2d to get yesterday+today data
			// so early-day charts aren't stretched with too few bars
			chartRange = "2d"
		} else {
			// Market closed, pre/post, unknown, or quote not loaded yet:
			// use 5d range to ensure we get the last trading day's data.
			// Always request pre/post so the response covers the same bar
			// set we may have cached during a previous POST session —
			// otherwise the network trimmed-96 ends at the regular-hours
			// close while the cache trimmed-96 ends at post-market close,
			// producing a visible flicker on every cold start.
			chartRange = "5d"
			chartInterval = "5m"
			includePrePost = true
		}

		// If within 30 min of market open (9:30 AM ET), show premarket
		if m.isNearMarketOpen(30) {
			includePrePost = true
		}
	}

	// Determine the trim budget for the 1D panel. The 1D view trims a
	// multi-day network response down to the latest session(s) so the
	// sparkline focuses on the most recent session shape. We trim by
	// "same ET calendar date as the newest bar" instead of a fixed
	// bar count so cache-first and network paths converge to the
	// same window once no new bars are arriving.
	//
	// When the market is not in REGULAR session (closed/pre/post) we
	// keep the previous session too so the overnight/closed period
	// renders as a visible gap between the two sessions.
	trimSession := tf.Label == "1D" && (chartRange == "2d" || chartRange == "5d")
	// Keep the last two sessions in memory for the 1D view. The
	// renderer decides whether to show only the latest session,
	// latest+trailing closed gap, or previous-tail + overnight gap +
	// current session depending on market state and how far into the
	// current session we are.
	trimSessions := 2
	// Upper bound just so we don't pass 2000 bars to the sparkline
	// when session trimming can't apply (e.g. odd data).
	trimTo := 0
	if trimSession {
		trimTo = maxBars1D * 3 * trimSessions // ~full extended session safety cap
	}
	logger.Log("fetchChart: policy %s tf=%s range=%s interval=%s marketState=%q trimSession=%v trimSessions=%d",
		symbol, tf.Label, chartRange, chartInterval, marketState, trimSession, trimSessions)

	// Try cache first. For intraday (1D) views the cached window and
	// the fresh network window can differ by a few minutes, which
	// causes a brief repaint ~1s after launch when the background
	// refresh lands — that's intentional: show cached data
	// immediately, then swap in fresh data when it arrives.
	if useCacheFirst && m.cache != nil {
		if cached := m.cache.GetChart(symbol, chartRange, chartInterval); cached != nil {
			rawN := len(cached.Closes)
			if trimSession {
				cached = trimToLastNSessions(cached, trimSessions)
			} else if trimTo > 0 && len(cached.Closes) > trimTo {
				cached = trimChartData(cached, trimTo)
			}
			logger.Log("fetchChart: cache-first HIT %s %s/%s rawBars=%d trimmedBars=%d",
				symbol, chartRange, chartInterval, rawN, len(cached.Closes))
			return func() tea.Msg {
				return chartMsg{symbol: symbol, data: cached, chartRange: chartRange, chartInterval: chartInterval}
			}
		}
		logger.Log("fetchChart: cache-first MISS %s %s/%s -> falling through to network",
			symbol, chartRange, chartInterval)
	} else if useCacheFirst {
		logger.Log("fetchChart: cache-first SKIP %s %s/%s (no cache configured)",
			symbol, chartRange, chartInterval)
	}

	client := m.client
	cacheRef := m.cache
	is1D := tf.Label == "1D"
	return func() tea.Msg {
		t0 := time.Now()
		data, err := client.GetChartWithOpts(symbol, chartRange, chartInterval, includePrePost)
		rawBars := 0
		if data != nil {
			rawBars = len(data.Closes)
		}
		logger.Log("fetchChart: network %s %s/%s bg=%v bars=%d err=%v in %dms",
			symbol, chartRange, chartInterval, background, rawBars, err, time.Since(t0).Milliseconds())
		if err != nil && !background && cacheRef != nil {
			// Network error: fall back to stale cache
			if stale := cacheRef.GetChartStale(symbol, chartRange, chartInterval); stale != nil {
				if trimSession {
					stale = trimToLastNSessions(stale, trimSessions)
				} else if trimTo > 0 && len(stale.Closes) > trimTo {
					stale = trimChartData(stale, trimTo)
				}
				return chartMsg{symbol: symbol, data: stale, chartRange: chartRange, chartInterval: chartInterval}
			}
		}
		// If 1D chart returned empty (e.g. non-US market hasn't opened yet), retry with 5d
		if err == nil && data != nil && is1D && len(data.Closes) == 0 {
			data2, err2 := client.GetChartWithOpts(symbol, "5d", "5m", false)
			if err2 == nil && data2 != nil && len(data2.Closes) > 0 {
				data = data2
				chartRange = "5d"
				chartInterval = "5m"
			}
		}
		if err == nil && data != nil && trimSession {
			data = trimToLastNSessions(data, trimSessions)
		} else if err == nil && data != nil && trimTo > 0 && len(data.Closes) > trimTo {
			data = trimChartData(data, trimTo)
		}
		// Persist freshly-fetched chart data so the next cold start sees
		// the up-to-date trend. We only write here (not in handleChart)
		// so cache-first hits don't re-stamp stale entries with a new
		// FetchedAt timestamp.
		if err == nil && cacheRef != nil && data != nil && len(data.Closes) > 0 {
			logger.Log("fetchChart: writing cache %s %s/%s bars=%d lastTs=%d",
				symbol, chartRange, chartInterval, len(data.Closes),
				func() int64 {
					if len(data.Timestamps) == 0 {
						return 0
					}
					return data.Timestamps[len(data.Timestamps)-1]
				}())
			cacheRef.PutChart(symbol, chartRange, chartInterval, data)
			// The 1D watchlist path decides between 2d and 5d before
			// quotes are loaded. Mirror the write under the startup-read
			// key so the next cold start sees the fresh trend even when
			// the market was open at write time.
			if is1D && chartInterval == "5m" && chartRange == "2d" {
				cacheRef.PutChart(symbol, "5d", "5m", data)
			}
		}
		return chartMsg{symbol: symbol, data: data, err: err, chartRange: chartRange, chartInterval: chartInterval, background: background}
	}
}

// isNearMarketOpen checks if we're within `minutes` of US market open (9:30 AM ET).
func (m Model) isNearMarketOpen(minutes int) bool {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return false
	}
	now := time.Now().In(loc)
	wd := now.Weekday()
	if wd == time.Saturday || wd == time.Sunday {
		return false
	}
	marketOpen := time.Date(now.Year(), now.Month(), now.Day(), 9, 30, 0, 0, loc)
	diff := marketOpen.Sub(now)
	return diff > 0 && diff <= time.Duration(minutes)*time.Minute
}

// trimChartData keeps the last n data points from chart data.
func trimChartData(cd *yahoo.ChartData, n int) *yahoo.ChartData {
	total := len(cd.Closes)
	if total <= n {
		return cd
	}
	start := total - n
	result := &yahoo.ChartData{
		Closes: cd.Closes[start:],
	}
	if len(cd.Timestamps) >= total {
		result.Timestamps = cd.Timestamps[start:]
	}
	if len(cd.Opens) >= total {
		result.Opens = cd.Opens[start:]
	}
	if len(cd.Highs) >= total {
		result.Highs = cd.Highs[start:]
	}
	if len(cd.Lows) >= total {
		result.Lows = cd.Lows[start:]
	}
	if len(cd.Volumes) >= total {
		result.Volumes = cd.Volumes[start:]
	}
	return result
}

// trimToLastSession keeps only bars that fall on the same ET calendar
// date as the most recent bar. This gives a stable "1D" view: both
// cache-first and network paths converge to the same session bars
// (rather than a sliding last-N window that drifts whenever a new
// bar arrives), so a background refresh with no material change
// won't visibly redraw the chart.
func trimToLastSession(cd *yahoo.ChartData) *yahoo.ChartData {
	return trimToLastNSessions(cd, 1)
}

// trimToLastNSessions keeps the bars of the last `n` distinct
// trading sessions ending at the most recent bar. n=1 is the classic
// single-session 1D view. n=2 keeps today plus the previous trading
// session so the overnight market-close period renders as a visible
// session break via chart.WithLineSessionBreaks /
// chart.WithCandleSessionBreaks. Session boundaries are detected by
// chart.DetectSessionBreaks (the same logic the session-break
// renderers use), which is timezone-agnostic — this works for US
// symbols, ASX (.AX), Tokyo (.T), etc. without needing to know each
// exchange's local calendar. If there aren't `n` distinct sessions
// in the data, returns everything available.
func trimToLastNSessions(cd *yahoo.ChartData, n int) *yahoo.ChartData {
	if cd == nil || len(cd.Timestamps) == 0 || n <= 0 {
		return cd
	}
	breaks := chart.DetectSessionBreaks(cd.Timestamps)
	if len(breaks) == 0 {
		return cd
	}
	// breaks[i] is the first index of a new session. To keep the
	// last n sessions, start from breaks[len(breaks)-n+1-1] i.e.
	// breaks[len(breaks)-(n-1)-1] when n>=2; for n=1 start from the
	// last break. Guard against fewer breaks than requested.
	// Equivalent math: keep sessions indexed by breaks plus the
	// current tail, so to retain `n` sessions we slice from the
	// (len(breaks)-n+1)'th break.
	//
	// Examples (k = len(breaks)):
	//   n=1 -> start = breaks[k-1]                (last session only)
	//   n=2 -> start = breaks[k-2] if k>=2 else 0 (prev + current)
	idx := len(breaks) - n + 1 - 1
	if idx < 0 {
		return cd
	}
	start := breaks[idx]
	if start <= 0 {
		return cd
	}
	out := &yahoo.ChartData{
		Timestamps: cd.Timestamps[start:],
		Closes:     cd.Closes[start:],
	}
	if len(cd.Opens) >= len(cd.Timestamps) {
		out.Opens = cd.Opens[start:]
	}
	if len(cd.Highs) >= len(cd.Timestamps) {
		out.Highs = cd.Highs[start:]
	}
	if len(cd.Lows) >= len(cd.Timestamps) {
		out.Lows = cd.Lows[start:]
	}
	if len(cd.Volumes) >= len(cd.Timestamps) {
		out.Volumes = cd.Volumes[start:]
	}
	return out
}

// prewarmItemsFromCache synchronously fills in each watchlist item's
// Quote and ChartData from the persistent cache so the first frame
// bubbletea paints already shows real data. We accept stale entries
// here — the in-flight refresh commands from Init() will replace
// anything out of date shortly after launch. This removes the
// "Loading..." blank-screen flash that was previously unavoidable
// while waiting for the first network round-trip.
func prewarmItemsFromCache(items []WatchlistItem, c cache.Cacher) {
	if len(items) == 0 || c == nil {
		return
	}
	symbols := make([]string, len(items))
	for i := range items {
		symbols[i] = items[i].Symbol
	}
	// Prefer fresh quotes; fall back to stale so the first paint
	// still shows *something* after a long gap between sessions.
	quotes := c.GetQuotes(symbols)
	if len(quotes) < len(symbols) {
		quotes = c.GetQuotesStale(symbols)
	}
	quoteBySymbol := make(map[string]*yahoo.Quote, len(quotes))
	for i := range quotes {
		q := quotes[i]
		quoteBySymbol[q.Symbol] = &q
	}
	// 1D default: cache key is 5d/5m (set by fetchChartWithPolicy
	// whenever marketState != REGULAR or quote not yet loaded).
	// We fall back to 2d/5m and 1d/1d for completeness.
	keys := [][2]string{{"5d", "5m"}, {"2d", "5m"}, {"1d", "1d"}}
	hits := 0
	for i := range items {
		if q, ok := quoteBySymbol[items[i].Symbol]; ok {
			items[i].Quote = q
		}
		// Keep the last two sessions; the 1D renderer decides how much
		// of each to show based on market state and session progress.
		for _, k := range keys {
			if cd := c.GetChartStale(items[i].Symbol, k[0], k[1]); cd != nil && len(cd.Closes) > 0 {
				items[i].ChartData = trimToLastNSessions(cd, 2)
				hits++
				break
			}
		}
		// If we have either a quote or chart, the row won't render
		// as "Loading" — suppress the spinner so the first frame
		// is clean.
		if items[i].Quote != nil || items[i].ChartData != nil {
			items[i].Loading = false
		}
	}
	logger.Log("prewarm: items=%d quotes=%d charts=%d", len(items), len(quoteBySymbol), hits)
}

func (m Model) fetchAllCharts() tea.Cmd {
	var cmds []tea.Cmd
	for i, item := range m.items {
		cmds = append(cmds, staggerCmd(i, m.fetchChart(item.Symbol)))
	}
	return tea.Batch(cmds...)
}

func (m Model) refreshAllCharts(background bool) tea.Cmd {
	var cmds []tea.Cmd
	for i, item := range m.items {
		cmds = append(cmds, staggerCmd(i, m.refreshChart(item.Symbol, background)))
	}
	return tea.Batch(cmds...)
}

func (m Model) currentSymbol() string {
	if m.selected < 0 || m.selected >= len(m.items) {
		return ""
	}
	return m.items[m.selected].Symbol
}

// staggerCmd delays a fan-out command by i*stepMs so parallel network
// requests don't all fire simultaneously and trip Yahoo's rate limiter.
// The first item (i==0) runs immediately; each subsequent item waits
// an extra 150ms. For a typical 4-symbol watchlist that spreads the
// burst over ~450ms — plenty to avoid the crumb-endpoint 429 storm
// while still feeling instant to the user.
func staggerCmd(i int, cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	if i == 0 {
		return cmd
	}
	delay := time.Duration(i) * 150 * time.Millisecond
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return cmd()
	})
}

// fetchAllIndicatorContexts dispatches indicator-backfill fetches for
// every watchlist item at the current timeframe. Cached responses
// short-circuit instantly, so this is cheap to call at startup and
// whenever the timeframe changes. Guarantees MA/MACD overlays have a
// fully warmed-up series from the first visible bar — including the
// inline watchlist charts (not just the selected item's detail view).
func (m Model) fetchAllIndicatorContexts() tea.Cmd {
	tf := m.currentTimeframe()
	rangeStr, interval, ok := indicatorContextParams(tf.Interval)
	if !ok {
		return nil
	}
	var cmds []tea.Cmd
	for _, item := range m.items {
		if item.IndicatorContext != nil && item.IndicatorContextInterval == interval {
			continue
		}
		cmds = append(cmds, m.fetchIndicatorContext(item.Symbol, rangeStr, interval))
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

func (m Model) refreshCurrentSymbol() (tea.Model, tea.Cmd) {
	symbol := m.currentSymbol()
	if symbol == "" {
		return m, nil
	}
	logger.Log("manual_refresh: target=current symbol=%s timeframe=%s", symbol, m.currentTimeframe().Label)
	return m, tea.Batch(m.refreshQuote(symbol, false), m.refreshChart(symbol, false))
}

func (m Model) refreshAllSymbols() (tea.Model, tea.Cmd) {
	groupName := ""
	if m.cfg != nil && m.activeGroup >= 0 && m.activeGroup < len(m.cfg.Watchlists) {
		groupName = m.cfg.Watchlists[m.activeGroup].Name
	}
	logger.Log("manual_refresh: target=all group=%s symbols=%d timeframe=%s", groupName, len(m.items), m.currentTimeframe().Label)
	return m, tea.Batch(m.refreshQuotes(false), m.refreshAllCharts(false))
}

func (m Model) startupRefreshCmd() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(time.Time) tea.Msg {
		return startupRefreshMsg{}
	})
}

func (m Model) hasLoadedQuotes() bool {
	for _, item := range m.items {
		if item.Quote != nil {
			return true
		}
	}
	return false
}

func (m Model) hasLoadedChart(symbol string) bool {
	for _, item := range m.items {
		if item.Symbol == symbol && item.ChartData != nil {
			return true
		}
	}
	return false
}

// Close releases background resources held by the model. Most
// importantly it checkpoints and closes the SQLite cache so the
// on-disk `finsight.db` reflects every write from the session —
// without this, the WAL sidecar can hold writes that never land in
// the main DB file between runs.
func (m Model) Close() error {
	if m.orchestratorCancel != nil {
		m.orchestratorCancel()
	}
	if sc, ok := m.cache.(*cache.SQLiteCache); ok && sc != nil {
		return sc.Close()
	}
	return nil
}

func (m Model) doSearch(query string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		results, err := client.Search(query)
		return searchMsg{results: results, err: err}
	}
}

// fetchIndicatorContext fetches prior bars at the given resolution
// and stashes them on the WatchlistItem so intraday technical
// indicators can warm up across prior sessions. Cached under the
// (range, interval) key so repeated calls are cheap.
func (m Model) fetchIndicatorContext(symbol, rangeStr, interval string) tea.Cmd {
	if m.cache != nil {
		if cached := m.cache.GetChart(symbol, rangeStr, interval); cached != nil && len(cached.Closes) > 0 {
			return func() tea.Msg {
				return indicatorContextMsg{symbol: symbol, interval: interval, data: cached}
			}
		}
	}
	client := m.client
	cacheRef := m.cache
	return func() tea.Msg {
		data, err := client.GetChart(symbol, rangeStr, interval)
		if err == nil && data != nil && len(data.Closes) > 0 && cacheRef != nil {
			cacheRef.PutChart(symbol, rangeStr, interval, data)
		}
		return indicatorContextMsg{symbol: symbol, interval: interval, data: data, err: err}
	}
}

// indicatorContextParams picks a (range, interval) pair that provides
// enough SAME-resolution history to warm up moving averages (EMA100,
// EMA200, MACD, etc.) for the given primary interval.
//
// Yahoo's v8 chart API enforces per-interval history caps that must
// not be exceeded (otherwise it returns an error and no data):
//
//	1m           ≤ 7d
//	2m/5m/15m/30m ≤ 60d
//	60m/90m/1h   ≤ 730d
//	daily+       unlimited
//
// Each window below stays within those limits and targets ≥ 250 prior
// bars after `sliceCtxPrefix` trims the overlap, so a 200-period EMA
// is already warm at the first visible bar. Returns ("", "", false)
// when the primary series doesn't need backfill.
func indicatorContextParams(primaryInterval string) (rangeStr, interval string, ok bool) {
	switch primaryInterval {
	case "1m":
		return "7d", "1m", true // max Yahoo allows — ~2700 bars
	case "2m":
		return "1mo", "2m", true // ~5800 bars (30d × 195/d)
	case "5m":
		return "1mo", "5m", true // ~2300 bars (30d × 78/d)
	case "15m":
		return "1mo", "15m", true // ~780 bars
	case "30m":
		return "1mo", "30m", true // ~390 bars — tight but > 200
	case "60m", "90m", "1h":
		return "2y", "60m", true // ~3500 bars
	case "1d":
		// Daily primaries: 10y gives ~2500 bars so even the 3Y
		// timeframe (~756 primary) still leaves ~1700 bars of prefix
		// — comfortably above the 200-EMA warmup floor.
		return "10y", "1d", true
	case "1wk":
		return "max", "1wk", true
	default:
		return "", "", false
	}
}

func (m Model) fetchKeyStats(symbol string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		stats, err := client.GetKeyStats(symbol)
		return keyStatsMsg{symbol: symbol, stats: stats, err: err}
	}
}

func (m Model) fetchFinancials(symbol string) tea.Cmd {
	client := m.client
	cacheRef := m.cache

	// Try fresh cache first
	if cacheRef != nil {
		if cached := cacheRef.GetFinancials(symbol); cached != nil {
			return func() tea.Msg {
				return financialsMsg{symbol: symbol, financials: cached}
			}
		}
	}

	return func() tea.Msg {
		fin, err := client.GetFinancials(symbol)
		if err != nil && cacheRef != nil {
			if stale := cacheRef.GetFinancialsStale(symbol); stale != nil {
				return financialsMsg{symbol: symbol, financials: stale}
			}
		}
		return financialsMsg{symbol: symbol, financials: fin, err: err}
	}
}

func (m Model) fetchHolders(symbol string) tea.Cmd {
	client := m.client
	cacheRef := m.cache

	// Try fresh cache first
	if cacheRef != nil {
		if cached := cacheRef.GetHolders(symbol); cached != nil {
			return func() tea.Msg {
				return holdersMsg{symbol: symbol, holders: cached}
			}
		}
	}

	return func() tea.Msg {
		hld, err := client.GetHolders(symbol)
		if err != nil && cacheRef != nil {
			if stale := cacheRef.GetHoldersStale(symbol); stale != nil {
				return holdersMsg{symbol: symbol, holders: stale}
			}
		}
		return holdersMsg{symbol: symbol, holders: hld, err: err}
	}
}

func (m Model) tickCmd() tea.Cmd {
	// Refresh every 15 minutes
	return tea.Tick(15*time.Minute, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m Model) startEarningsOrchestrator() tea.Cmd {
	// Start background earnings orchestrator for EDGAR confirmation and Yahoo backfill
	if m.earningsOrchestrator == nil {
		return nil
	}

	return func() tea.Msg {
		// Run orchestrator in background; it will block until context is cancelled
		go func() {
			_ = m.earningsOrchestrator.Start(m.orchestratorCtx)
		}()
		return nil
	}
}

func (m Model) addSymbol(symbol, name string) (tea.Model, tea.Cmd) {
	for _, item := range m.items {
		if item.Symbol == symbol {
			m.mode = viewWatchlist
			return m, nil
		}
	}

	m.cfg.AddSymbol(m.activeGroup, symbol, name)
	_ = config.Save(m.cfgPath, m.cfg)

	m.items = append(m.items, WatchlistItem{
		Symbol:  symbol,
		Name:    name,
		Loading: true,
	})

	m.mode = viewWatchlist
	return m, tea.Batch(m.fetchQuotes(), m.fetchChart(symbol))
}

func (m Model) removeSelected() (tea.Model, tea.Cmd) {
	if len(m.items) == 0 || m.selected >= len(m.items) {
		return m, nil
	}

	symbol := m.items[m.selected].Symbol
	m.cfg.RemoveSymbol(m.activeGroup, symbol)
	_ = config.Save(m.cfgPath, m.cfg)

	// Invalidate cache
	if m.cache != nil {
		m.cache.Invalidate(symbol)
	}

	m.items = append(m.items[:m.selected], m.items[m.selected+1:]...)
	if m.selected >= len(m.items) && m.selected > 0 {
		m.selected--
	}

	return m, nil
}

func (m Model) switchGroup(idx int) (tea.Model, tea.Cmd) {
	if idx < 0 || idx >= len(m.cfg.Watchlists) || idx == m.activeGroup {
		return m, nil
	}
	m.activeGroup = idx
	m.selected = 0
	m.sortMode = SortDefault

	group := m.cfg.Watchlists[idx]
	m.items = make([]WatchlistItem, len(group.Symbols))
	for i, w := range group.Symbols {
		m.items[i] = WatchlistItem{
			Symbol:  w.Symbol,
			Name:    w.Name,
			Loading: true,
		}
	}

	return m, tea.Batch(m.fetchQuotes(), m.fetchAllCharts())
}

func (m Model) sortedItems() []WatchlistItem {
	if m.sortMode == SortDefault {
		return m.items
	}
	sorted := make([]WatchlistItem, len(m.items))
	copy(sorted, m.items)
	sort.SliceStable(sorted, func(i, j int) bool {
		switch m.sortMode {
		case SortBySymbol:
			return sorted[i].Symbol < sorted[j].Symbol
		case SortBySymbolDesc:
			return sorted[i].Symbol > sorted[j].Symbol
		case SortByChangeAsc:
			ci, cj := getChangePct(sorted[i]), getChangePct(sorted[j])
			return ci < cj
		case SortByChangeDesc:
			ci, cj := getChangePct(sorted[i]), getChangePct(sorted[j])
			return ci > cj
		case SortByMarketCapAsc:
			mi, mj := getMarketCap(sorted[i]), getMarketCap(sorted[j])
			return mi < mj
		case SortByMarketCapDesc:
			mi, mj := getMarketCap(sorted[i]), getMarketCap(sorted[j])
			return mi > mj
		}
		return false
	})
	return sorted
}

func getChangePct(item WatchlistItem) float64 {
	if item.Quote != nil {
		return item.Quote.ChangePercent
	}
	return 0
}

func getMarketCap(item WatchlistItem) float64 {
	if item.Quote != nil {
		return float64(item.Quote.MarketCap)
	}
	return 0
}

// === Related Symbols ===

func (m Model) fetchRelatedSymbols(symbol string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		syms, err := client.GetRecommendedSymbols(symbol)
		return recommendedSymbolsMsg{forSymbol: symbol, symbols: syms, err: err}
	}
}

func (m Model) handleRecommendedSymbols(msg recommendedSymbolsMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil || len(msg.symbols) == 0 {
		m.relatedSymbols = nil
		m.relatedForSymbol = msg.forSymbol
		return m, nil
	}

	m.relatedForSymbol = msg.forSymbol
	m.relatedSymbols = make([]RelatedSymbol, len(msg.symbols))
	for i, s := range msg.symbols {
		m.relatedSymbols[i] = RelatedSymbol{Symbol: s.Symbol, Score: s.Score}
	}

	// Fetch quotes and charts for all related symbols
	symbols := make([]string, len(msg.symbols))
	for i, s := range msg.symbols {
		symbols[i] = s.Symbol
	}

	cmds := []tea.Cmd{m.fetchRelatedQuotes(msg.forSymbol, symbols)}
	for _, sym := range symbols {
		cmds = append(cmds, m.fetchRelatedChart(msg.forSymbol, sym))
	}
	return m, tea.Batch(cmds...)
}

func (m Model) fetchRelatedQuotes(forSymbol string, symbols []string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		quotes, err := client.GetQuotes(symbols)
		return relatedQuotesMsg{forSymbol: forSymbol, quotes: quotes, err: err}
	}
}

func (m Model) handleRelatedQuotes(msg relatedQuotesMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil || msg.forSymbol != m.relatedForSymbol {
		return m, nil
	}
	quoteMap := make(map[string]*yahoo.Quote)
	for i := range msg.quotes {
		quoteMap[msg.quotes[i].Symbol] = &msg.quotes[i]
	}
	for i := range m.relatedSymbols {
		if q, ok := quoteMap[m.relatedSymbols[i].Symbol]; ok {
			m.relatedSymbols[i].Quote = q
		}
	}
	return m, nil
}

func (m Model) fetchRelatedChart(forSymbol, symbol string) tea.Cmd {
	tf := m.currentTimeframe()
	client := m.client
	return func() tea.Msg {
		data, err := client.GetChartWithOpts(symbol, tf.Range, tf.Interval, false)
		return relatedChartMsg{
			forSymbol:     forSymbol,
			symbol:        symbol,
			data:          data,
			chartRange:    tf.Range,
			chartInterval: tf.Interval,
			err:           err,
		}
	}
}

func (m Model) handleRelatedChart(msg relatedChartMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil || msg.forSymbol != m.relatedForSymbol {
		return m, nil
	}
	for i := range m.relatedSymbols {
		if m.relatedSymbols[i].Symbol == msg.symbol {
			m.relatedSymbols[i].ChartData = msg.data
			break
		}
	}
	return m, nil
}

func (m Model) fetchAllRelatedCharts() tea.Cmd {
	if len(m.relatedSymbols) == 0 {
		return nil
	}
	var cmds []tea.Cmd
	for _, rs := range m.relatedSymbols {
		cmds = append(cmds, m.fetchRelatedChart(m.relatedForSymbol, rs.Symbol))
	}
	return tea.Batch(cmds...)
}
