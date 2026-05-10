package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ray-x/finsight/internal/config"
	"github.com/ray-x/finsight/internal/llm"
	"github.com/ray-x/finsight/internal/logger"
	"github.com/ray-x/finsight/internal/portfolio"
	"github.com/ray-x/finsight/internal/yahoo"
)

// portfolioAIMsg is emitted when the LLM returns advice for a single
// position ("" symbol = whole portfolio review).
type portfolioAIMsg struct {
	symbol string
	text   string
	err    error
}

// === Entry / switching ===

func (m Model) openPortfolio() (tea.Model, tea.Cmd) {
	m.portfolioPrev = m.mode
	m.mode = viewPortfolio
	if m.portfolioSelected >= len(m.portfolio.Positions) {
		m.portfolioSelected = 0
	}
	m.rebuildPortfolioItems()
	return m, tea.Batch(m.fetchPortfolioQuotes(), m.fetchPortfolioCharts())
}

// rebuildPortfolioItems refreshes m.portfolioItems from m.portfolio, reusing
// any cached WatchlistItem data already held for the same symbol in the
// watchlist to avoid an initial flicker.
func (m *Model) rebuildPortfolioItems() {
	watchlistCache := make(map[string]WatchlistItem, len(m.items))
	for _, it := range m.items {
		watchlistCache[it.Symbol] = it
	}
	m.portfolioItems = BuildPortfolioItems(m.portfolio.Positions, watchlistCache)
}

// === View ===

func (m Model) viewPortfolio() string {
	title := titleStyle.Render(" ◆ Finsight ")
	viewTabs := m.renderViewTabs()

	updateStr := ""
	if !m.lastUpdate.IsZero() {
		updateStr = nameStyle.Render(fmt.Sprintf("Updated: %s", m.lastUpdate.Format("15:04:05")))
	}
	spacer := m.width - lipgloss.Width(title) - lipgloss.Width(viewTabs) - lipgloss.Width(updateStr) - 6
	if spacer < 0 {
		spacer = 0
	}
	titleBar := lipgloss.JoinHorizontal(lipgloss.Center,
		title, "  ", viewTabs, lipgloss.NewStyle().Width(spacer).Render(""), updateStr)

	list := RenderPortfolio(m.portfolioItems, m.portfolioSelected, m.width, 2, m.cfg.ChartStyle, m.portfolioProfit, m.currentTimeframe().Label)

	help := m.renderPortfolioHelp()

	var errStr string
	if m.err != "" {
		errStr = errorStyle.Render(m.err)
	}

	parts := []string{titleBar}
	parts = append(parts, list)
	if errStr != "" {
		parts = append(parts, errStr)
	}
	parts = append(parts, help)
	base := lipgloss.JoinVertical(lipgloss.Left, parts...)

	// Overlay form or AI popup if active
	if m.portfolioForm.active {
		baseLines := strings.Split(base, "\n")
		for len(baseLines) < m.height {
			baseLines = append(baseLines, "")
		}
		base = strings.Join(baseLines, "\n")
		base = m.overlayPopup(base, m.renderPortfolioForm())
	}
	if m.showPortfolioAI {
		baseLines := strings.Split(base, "\n")
		for len(baseLines) < m.height {
			baseLines = append(baseLines, "")
		}
		base = strings.Join(baseLines, "\n")
		base = m.overlayPopup(base, m.renderPortfolioAIPopup())
	}
	return base
}

func (m Model) renderPortfolioHelp() string {
	aiHelp := ""
	if m.llmClient != nil {
		aiHelp = helpKeyStyle.Render("a") + " AI  " + helpKeyStyle.Render("A") + " review  "
	}
	return helpStyle.Render(
		helpKeyStyle.Render("↑↓") + " navigate  " +
			helpKeyStyle.Render("Enter") + " detail  " +
			helpKeyStyle.Render("/") + " add  " +
			hintKey("e", "dit  ") +
			hintKey("d", "elete  ") +
			helpKeyStyle.Render("%") + " profit %/$  " +
			aiHelp +
			hintKey("r", "efresh  ") +
			helpKeyStyle.Render("Esc") + " back")
}

// === Key handling ===

func (m Model) handlePortfolioKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Form (add/edit) has priority
	if m.portfolioForm.active {
		return m.handlePortfolioFormKey(msg)
	}

	// AI popup
	if m.showPortfolioAI {
		switch {
		case key.Matches(msg, keys.Down):
			m.portfolioAIScroll++
		case key.Matches(msg, keys.Up):
			if m.portfolioAIScroll > 0 {
				m.portfolioAIScroll--
			}
		case msg.String() == "R":
			// Force refresh AI
			if m.cache != nil {
				m.cache.DeleteText(m.portfolioCacheKey(m.portfolioAISymbol), "ai-portfolio")
			}
			m.portfolioAILoading = true
			m.portfolioAIText = ""
			if m.portfolioAISymbol == "" {
				return m, m.fetchPortfolioReview()
			}
			return m, m.fetchPortfolioAdvice(m.portfolioAISymbol)
		case key.Matches(msg, keys.Back):
			m.showPortfolioAI = false
			m.portfolioAIScroll = 0
		}
		return m, nil
	}

	switch {
	case key.Matches(msg, keys.Up):
		if m.portfolioSelected > 0 {
			m.portfolioSelected--
		}
	case key.Matches(msg, keys.Down):
		if m.portfolioSelected < len(m.portfolioItems)-1 {
			m.portfolioSelected++
		}
	case key.Matches(msg, keys.Enter):
		if len(m.portfolioItems) > 0 {
			sym := m.portfolioItems[m.portfolioSelected].Symbol
			// Jump to detail view — if the symbol isn't in the current
			// watchlist, temporarily add it to m.items so detail view can
			// render. A refresh will rebuild.
			m.items = append([]WatchlistItem(nil), m.items...)
			found := -1
			for i, it := range m.items {
				if it.Symbol == sym {
					found = i
					break
				}
			}
			if found < 0 {
				m.items = append(m.items, WatchlistItem{
					Symbol:  sym,
					Name:    m.portfolioItems[m.portfolioSelected].Name,
					Quote:   m.portfolioItems[m.portfolioSelected].Quote,
					Loading: m.portfolioItems[m.portfolioSelected].Quote == nil,
				})
				found = len(m.items) - 1
			}
			m.selected = found
			m.mode = viewDetail
			m.relatedFocus = -1
			m.compareSymbol = nil
			return m, tea.Batch(m.fetchChart(sym), m.fetchKeyStats(sym), m.fetchRelatedSymbols(sym))
		}
	case key.Matches(msg, keys.Search):
		m.portfolioForm = portfolioFormState{} // reset
		m.mode = viewSearch
		m.search = SearchView{}
		m.portfolioSearching = true
	case key.Matches(msg, keys.Delete):
		return m.removeSelectedPortfolio()
	case key.Matches(msg, portfolioKeys.Edit):
		return m.beginPortfolioEdit()
	case key.Matches(msg, portfolioKeys.ProfitToggle):
		if m.portfolioProfit == ProfitPercent {
			m.portfolioProfit = ProfitTotal
		} else {
			m.portfolioProfit = ProfitPercent
		}
	case key.Matches(msg, keys.AI):
		if m.llmClient != nil && len(m.portfolioItems) > 0 {
			return m.startPortfolioAdvice(m.portfolioItems[m.portfolioSelected].Symbol)
		}
	case key.Matches(msg, portfolioKeys.ReviewAll):
		if m.llmClient != nil && len(m.portfolioItems) > 0 {
			return m.startPortfolioReview()
		}
	case key.Matches(msg, keys.Refresh):
		return m, tea.Batch(m.forcePortfolioRefresh()...)
	case key.Matches(msg, keys.Back):
		// Return to watchlist
		m.mode = viewWatchlist
		return m, nil
	case key.Matches(msg, portfolioKeys.Watchlist), key.Matches(msg, portfolioKeys.Open):
		// `w`/`W` or `p`/`P` both switch back to watchlist from portfolio.
		m.mode = viewWatchlist
		return m, nil
	case msg.String() == "h" || msg.String() == "H":
		// Switch to heatmap view — show all portfolio positions.
		m.heatmapPrev = viewPortfolio
		m.heatmap = NewHeatmapModel()
		m.mode = viewHeatmap
		return m.setHeatmapType(HeatmapPortfolio)
	}
	return m, nil
}

// === Fetching ===

func (m Model) fetchPortfolioQuotes() tea.Cmd {
	symbols := make([]string, 0, len(m.portfolio.Positions))
	for _, p := range m.portfolio.Positions {
		symbols = append(symbols, p.Symbol)
	}
	if len(symbols) == 0 {
		return nil
	}
	if m.cache != nil {
		if cached := m.cache.GetQuotes(symbols); cached != nil {
			cov := make(map[string]bool, len(cached))
			for _, q := range cached {
				cov[q.Symbol] = true
			}
			all := true
			for _, s := range symbols {
				if !cov[s] {
					all = false
					break
				}
			}
			if all {
				return func() tea.Msg { return quotesMsg{quotes: cached} }
			}
		}
	}
	client := m.client
	cacheRef := m.cache
	return func() tea.Msg {
		quotes, err := client.GetQuotes(symbols)
		if err == nil && cacheRef != nil && len(quotes) > 0 {
			// Persist freshly-fetched quotes so the next cold start sees
			// up-to-date data. handleQuotes intentionally does not write
			// back on cache-first hits.
			cacheRef.PutQuotes(quotes)
		}
		if err != nil && cacheRef != nil {
			if stale := cacheRef.GetQuotesStale(symbols); stale != nil {
				return quotesMsg{quotes: stale}
			}
		}
		return quotesMsg{quotes: quotes, err: err}
	}
}

func (m Model) fetchPortfolioCharts() tea.Cmd {
	var cmds []tea.Cmd
	for _, p := range m.portfolio.Positions {
		cmds = append(cmds, m.fetchChart(p.Symbol))
	}
	return tea.Batch(cmds...)
}

func (m Model) forcePortfolioRefresh() []tea.Cmd {
	if m.cache != nil {
		for _, p := range m.portfolio.Positions {
			m.cache.Invalidate(p.Symbol)
		}
	}
	return []tea.Cmd{m.fetchPortfolioQuotes(), m.fetchPortfolioCharts()}
}

// applyQuotesToPortfolio updates portfolio item quotes from a quotesMsg.
// Called from handleQuotes so both views stay in sync.
func (m *Model) applyQuotesToPortfolio(quotes []yahoo.Quote) {
	if len(m.portfolioItems) == 0 {
		return
	}
	byName := make(map[string]int, len(m.portfolioItems))
	for i, it := range m.portfolioItems {
		byName[it.Symbol] = i
	}
	persistDirty := false
	for i := range quotes {
		q := &quotes[i]
		idx, ok := byName[q.Symbol]
		if !ok {
			continue
		}
		item := m.portfolioItems[idx]
		item.Quote = q
		item.Loading = false
		item.Error = ""
		if item.Name == "" || item.Name == item.Symbol {
			if q.Name != "" {
				item.Name = q.Name
			}
		}
		// Auto-fill missing open price from today's open on first fetch,
		// then persist.
		if item.OpenPrice == 0 && q.Open > 0 {
			item.OpenPrice = q.Open
			for j, p := range m.portfolio.Positions {
				if p.Symbol == item.Symbol && p.OpenPrice == 0 {
					m.portfolio.Positions[j].OpenPrice = q.Open
					persistDirty = true
					logger.Log("portfolio: auto-filled open price for %s = %.4f", item.Symbol, q.Open)
					break
				}
			}
		}
		m.portfolioItems[idx] = item
	}
	if persistDirty {
		m.savePortfolio()
	}
}

// applyChartToPortfolio updates portfolio item chart data from chartMsg.
func (m *Model) applyChartToPortfolio(symbol string, data *yahoo.ChartData) {
	for i := range m.portfolioItems {
		if m.portfolioItems[i].Symbol == symbol {
			m.portfolioItems[i].ChartData = data
			return
		}
	}
}

// === Save ===

// savePortfolio writes the in-memory portfolio back to its source (file
// or embedded config).
func (m *Model) savePortfolio() {
	if m.portfolio == nil {
		return
	}
	if m.portfolioPath != "" {
		if err := portfolio.Save(m.portfolioPath, m.portfolio); err != nil {
			logger.Log("portfolio: save failed: %v", err)
		}
		return
	}
	// Embedded in config.yaml
	m.cfg.Portfolio = append(m.cfg.Portfolio[:0], m.portfolio.Positions...)
	if err := config.Save(m.cfgPath, m.cfg); err != nil {
		logger.Log("portfolio: embedded save failed: %v", err)
	}
}

// promotePortfolioToFile switches a first-time user to the dedicated
// ~/.config/finsight/portfolio.yaml file so positions never land in
// config.yaml. Called lazily from addPortfolio when m.portfolioPath == "".
func (m *Model) promotePortfolioToFile() {
	if m.portfolioPath != "" {
		return
	}
	m.portfolioPath = portfolio.DefaultPath()
	// Clear embedded copy; subsequent save() writes to the new file only.
	m.cfg.Portfolio = nil
}

// === Add / Edit / Remove ===

func (m Model) removeSelectedPortfolio() (tea.Model, tea.Cmd) {
	if len(m.portfolioItems) == 0 || m.portfolioSelected >= len(m.portfolioItems) {
		return m, nil
	}
	sym := m.portfolioItems[m.portfolioSelected].Symbol
	if m.portfolio.Remove(sym) {
		m.savePortfolio()
	}
	if m.cache != nil {
		m.cache.DeleteText(m.portfolioCacheKey(sym), "ai-portfolio")
	}
	m.rebuildPortfolioItems()
	if m.portfolioSelected >= len(m.portfolioItems) && m.portfolioSelected > 0 {
		m.portfolioSelected--
	}
	return m, nil
}

func (m Model) beginPortfolioAdd(symbol, name string) (tea.Model, tea.Cmd) {
	m.portfolioForm = portfolioFormState{
		active: true, editing: false,
		symbol: symbol, name: name,
		posField:  "10",
		dateField: time.Now().Format(time.DateOnly),
	}
	m.mode = viewPortfolio
	return m, nil
}

func (m Model) beginPortfolioEdit() (tea.Model, tea.Cmd) {
	if len(m.portfolioItems) == 0 {
		return m, nil
	}
	it := m.portfolioItems[m.portfolioSelected]
	m.portfolioForm = portfolioFormState{
		active: true, editing: true,
		symbol:   it.Symbol,
		name:     it.Name,
		posField: strconv.FormatFloat(it.Position, 'f', -1, 64),
		dateField: func() string {
			if p := m.portfolio.Find(it.Symbol); p != nil && strings.TrimSpace(p.BoughtAt) != "" {
				return p.BoughtAt
			}
			return time.Now().Format(time.DateOnly)
		}(),
	}
	if it.OpenPrice > 0 {
		m.portfolioForm.openField = strconv.FormatFloat(it.OpenPrice, 'f', -1, 64)
	}
	return m, nil
}

func (m Model) handlePortfolioFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := msg.String()
	switch s {
	case "esc":
		m.portfolioForm = portfolioFormState{}
		return m, nil
	case "tab", "down":
		m.portfolioForm.focus = (m.portfolioForm.focus + 1) % 3
		return m, nil
	case "shift+tab", "up":
		m.portfolioForm.focus = (m.portfolioForm.focus + 3 - 1) % 3
		return m, nil
	case "enter":
		return m.submitPortfolioForm()
	case "backspace":
		if m.portfolioForm.focus == 0 && len(m.portfolioForm.posField) > 0 {
			m.portfolioForm.posField = m.portfolioForm.posField[:len(m.portfolioForm.posField)-1]
		} else if m.portfolioForm.focus == 1 && len(m.portfolioForm.openField) > 0 {
			m.portfolioForm.openField = m.portfolioForm.openField[:len(m.portfolioForm.openField)-1]
		} else if m.portfolioForm.focus == 2 && len(m.portfolioForm.dateField) > 0 {
			m.portfolioForm.dateField = m.portfolioForm.dateField[:len(m.portfolioForm.dateField)-1]
		}
		return m, nil
	}
	if len(s) == 1 {
		c := s[0]
		switch m.portfolioForm.focus {
		case 0, 1:
			if (c >= '0' && c <= '9') || c == '.' {
				if m.portfolioForm.focus == 0 {
					m.portfolioForm.posField += s
				} else {
					m.portfolioForm.openField += s
				}
			}
		case 2:
			if (c >= '0' && c <= '9') || c == '-' {
				m.portfolioForm.dateField += s
			}
		}
	}
	return m, nil
}

func (m Model) submitPortfolioForm() (tea.Model, tea.Cmd) {
	pos, err := strconv.ParseFloat(strings.TrimSpace(m.portfolioForm.posField), 64)
	if err != nil || pos <= 0 {
		m.portfolioForm.err = "quantity must be > 0"
		return m, nil
	}
	var openPrice float64
	if s := strings.TrimSpace(m.portfolioForm.openField); s != "" {
		openPrice, err = strconv.ParseFloat(s, 64)
		if err != nil || openPrice < 0 {
			m.portfolioForm.err = "open price must be numeric"
			return m, nil
		}
	}
	boughtAt := strings.TrimSpace(m.portfolioForm.dateField)
	if boughtAt == "" {
		boughtAt = time.Now().Format(time.DateOnly)
	}
	parsedDate, err := time.Parse(time.DateOnly, boughtAt)
	if err != nil {
		m.portfolioForm.err = "bought date must be YYYY-MM-DD"
		return m, nil
	}

	m.promotePortfolioToFile()
	p := portfolio.Position{
		Symbol:    m.portfolioForm.symbol,
		Position:  pos,
		OpenPrice: openPrice,
		BoughtAt:  parsedDate.Format(time.DateOnly),
	}
	if existing := m.portfolio.Find(p.Symbol); existing != nil {
		p.Note = existing.Note
	}
	m.portfolio.Add(p)
	m.savePortfolio()

	m.portfolioForm = portfolioFormState{}
	m.rebuildPortfolioItems()
	// Point selection at the edited/added symbol
	for i, it := range m.portfolioItems {
		if it.Symbol == p.Symbol {
			m.portfolioSelected = i
			break
		}
	}
	return m, tea.Batch(m.fetchPortfolioQuotes(), m.fetchChart(p.Symbol))
}

// addPortfolioFromSearch is called by the search-result handler when the
// user is in portfolio-add flow.
func (m Model) addPortfolioFromSearch(symbol, name string) (tea.Model, tea.Cmd) {
	m.mode = viewPortfolio
	return m.beginPortfolioAdd(symbol, name)
}

// === Form rendering ===

func (m Model) renderPortfolioForm() string {
	verb := "Add"
	if m.portfolioForm.editing {
		verb = "Edit"
	}
	title := helpKeyStyle.Render(fmt.Sprintf("  ◆ %s position — %s (%s)", verb, m.portfolioForm.symbol, truncate(m.portfolioForm.name, 40)))

	cursor := "█"
	posLine := "  Quantity: " + m.portfolioForm.posField
	if m.portfolioForm.focus == 0 {
		posLine += cursor
	}
	openLine := "  Open price (blank = use today's open): " + m.portfolioForm.openField
	if m.portfolioForm.focus == 1 {
		openLine += cursor
	}
	dateLine := "  Bought date (YYYY-MM-DD): " + m.portfolioForm.dateField
	if m.portfolioForm.focus == 2 {
		dateLine += cursor
	}

	errLine := ""
	if m.portfolioForm.err != "" {
		errLine = "\n" + errorStyle.Render("  "+m.portfolioForm.err)
	}
	footer := helpStyle.Render("  Tab switch  Enter save  Esc cancel")
	return title + "\n\n" + nameStyle.Render(posLine) + "\n" + nameStyle.Render(openLine) + "\n" + nameStyle.Render(dateLine) + errLine + "\n\n" + footer
}

// === AI advisor ===

func (m Model) portfolioCacheKey(symbol string) string {
	if symbol == "" {
		return "__review__"
	}
	return symbol
}

func (m Model) startPortfolioAdvice(symbol string) (tea.Model, tea.Cmd) {
	m.showPortfolioAI = true
	m.portfolioAISymbol = symbol
	m.portfolioAIScroll = 0
	if m.cache != nil {
		if cached := m.cache.GetText(m.portfolioCacheKey(symbol), "ai-portfolio"); cached != "" {
			m.portfolioAIText = cached
			m.portfolioAILoading = false
			return m, nil
		}
	}
	m.portfolioAILoading = true
	m.portfolioAIText = ""
	return m, m.fetchPortfolioAdvice(symbol)
}

func (m Model) startPortfolioReview() (tea.Model, tea.Cmd) {
	m.showPortfolioAI = true
	m.portfolioAISymbol = ""
	m.portfolioAIScroll = 0
	if m.cache != nil {
		if cached := m.cache.GetText(m.portfolioCacheKey(""), "ai-portfolio"); cached != "" {
			m.portfolioAIText = cached
			m.portfolioAILoading = false
			return m, nil
		}
	}
	m.portfolioAILoading = true
	m.portfolioAIText = ""
	return m, m.fetchPortfolioReview()
}

func (m Model) fetchPortfolioAdvice(symbol string) tea.Cmd {
	client := m.llmClient
	if client == nil {
		return nil
	}
	snapshots := m.portfolioSnapshots()
	cacheRef := m.cache
	cacheKey := m.portfolioCacheKey(symbol)
	return func() tea.Msg {
		ctx := context.Background()
		user := llm.BuildPortfolioAdvicePrompt(symbol, snapshots)
		text, err := client.Chat(ctx, llm.PortfolioSystemPrompt(), user)
		if err == nil && text != "" && cacheRef != nil {
			cacheRef.PutText(cacheKey, "ai-portfolio", text)
		}
		return portfolioAIMsg{symbol: symbol, text: text, err: err}
	}
}

func (m Model) fetchPortfolioReview() tea.Cmd {
	client := m.llmClient
	if client == nil {
		return nil
	}
	snapshots := m.portfolioSnapshots()
	cacheRef := m.cache
	cacheKey := m.portfolioCacheKey("")
	return func() tea.Msg {
		ctx := context.Background()
		user := llm.BuildPortfolioReviewPrompt(snapshots)
		text, err := client.Chat(ctx, llm.PortfolioSystemPrompt(), user)
		if err == nil && text != "" && cacheRef != nil {
			cacheRef.PutText(cacheKey, "ai-portfolio", text)
		}
		return portfolioAIMsg{symbol: "", text: text, err: err}
	}
}

func (m Model) portfolioSnapshots() []llm.PortfolioSnapshot {
	metrics := ComputePortfolioMetrics(m.portfolioItems)
	snaps := make([]llm.PortfolioSnapshot, 0, len(m.portfolioItems))
	totalValue := metrics.TotalMarketValue
	for _, it := range m.portfolioItems {
		var price, change, changePct, dayPnL, unrealPnL, unrealPct, weight float64
		var prevClose, open float64
		var currency string
		if it.Quote != nil {
			price = it.Quote.Price
			change = it.Quote.Change
			changePct = it.Quote.ChangePercent
			prevClose = it.Quote.PreviousClose
			open = it.Quote.Open
			currency = it.Quote.Currency
			dayPnL = change * it.Position
		}
		mv := price * it.Position
		if totalValue > 0 {
			weight = (mv / totalValue) * 100
		}
		if it.OpenPrice > 0 && price > 0 {
			unrealPnL = (price - it.OpenPrice) * it.Position
			unrealPct = ((price / it.OpenPrice) - 1) * 100
		}
		snaps = append(snaps, llm.PortfolioSnapshot{
			Symbol:          it.Symbol,
			Name:            it.Name,
			Position:        it.Position,
			OpenPrice:       it.OpenPrice,
			Price:           price,
			PreviousClose:   prevClose,
			DayOpen:         open,
			DailyChange:     change,
			DailyChangePct:  changePct,
			MarketValue:     mv,
			DailyPL:         dayPnL,
			UnrealizedPL:    unrealPnL,
			UnrealizedPLPct: unrealPct,
			WeightPct:       weight,
			Currency:        currency,
			Note:            it.Note,
		})
	}
	return snaps
}

func (m Model) handlePortfolioAI(msg portfolioAIMsg) (tea.Model, tea.Cmd) {
	m.portfolioAILoading = false
	if msg.err != nil {
		m.portfolioAIText = "Error: " + msg.err.Error()
	} else {
		m.portfolioAIText = msg.text
	}
	return m, nil
}

// renderPortfolioAIPopup renders a popup with AI advice for a single
// position or the whole portfolio.
func (m Model) renderPortfolioAIPopup() string {
	var header string
	if m.portfolioAISymbol == "" {
		header = "  ◆ Portfolio Review"
	} else {
		header = fmt.Sprintf("  ◆ Position Advice — %s", m.portfolioAISymbol)
	}
	title := helpKeyStyle.Render(header)

	var content string
	if m.portfolioAILoading {
		content = nameStyle.Render("  Analysing... (waiting for LLM response)")
	} else {
		maxWidth := m.width - 12
		if maxWidth < 40 {
			maxWidth = 40
		}
		content = renderMarkdown(m.portfolioAIText, maxWidth)
	}
	footer := helpStyle.Render("  Esc close · R refresh")
	return title + "\n\n" + content + "\n\n" + footer
}
