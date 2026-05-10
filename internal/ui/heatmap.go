package ui

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/ray-x/finsight/internal/logger"
	"github.com/ray-x/finsight/internal/yahoo"
)

// HeatmapType selects which heatmap visualization is displayed.
type HeatmapType int

const (
	HeatmapPortfolio HeatmapType = iota
	HeatmapMostActive
	HeatmapTrendNow
	HeatmapWatchlist
)

func heatmapTypeName(kind HeatmapType) string {
	switch kind {
	case HeatmapPortfolio:
		return "portfolio"
	case HeatmapMostActive:
		return "most_active"
	case HeatmapTrendNow:
		return "trend_now"
	case HeatmapWatchlist:
		return "watchlist"
	default:
		return fmt.Sprintf("unknown_%d", kind)
	}
}

// SortBy selects the column to sort heatmap items by.
type SortBy int

const (
	SortBySize SortBy = iota
	SortByChange
	SortByName
)

// PortfolioHeatmapScale controls how portfolio tiles are sized.
type PortfolioHeatmapScale int

const (
	PortfolioHeatmapByMarketValue PortfolioHeatmapScale = iota
	PortfolioHeatmapByMarketCap
)

func portfolioHeatmapScaleLabel(scale PortfolioHeatmapScale) string {
	switch scale {
	case PortfolioHeatmapByMarketCap:
		return "Market Cap"
	default:
		return "Market Value"
	}
}

// HeatmapItem represents a single heatmap cell (symbol or sector).
type HeatmapItem struct {
	Label    string  // Symbol or sector name
	Value    float64 // Market value (portfolio), volume, or aggregate value (sector)
	Change   float64 // Price change %
	Quote    *yahoo.Quote
	Selected bool
}

// HeatmapModel encapsulates heatmap state.
type HeatmapModel struct {
	Type           HeatmapType
	Items          []HeatmapItem
	VisibleIdxs    []int // Indices currently rendered on-screen
	Loading        bool
	SortBy         SortBy
	PortfolioScale PortfolioHeatmapScale
	SelectedIdx    int
	SelectedLabel  string // For returning to selected item after mode switch
}

// NewHeatmapModel creates a new heatmap model.
func NewHeatmapModel() HeatmapModel {
	return HeatmapModel{
		Type:           HeatmapPortfolio,
		Items:          []HeatmapItem{},
		VisibleIdxs:    []int{},
		Loading:        false,
		SortBy:         SortBySize,
		PortfolioScale: PortfolioHeatmapByMarketValue,
		SelectedIdx:    0,
		SelectedLabel:  "",
	}
}

func quoteChangePercent(q *yahoo.Quote) float64 {
	if q == nil {
		return 0
	}
	change := q.ChangePercent
	if change == 0 {
		change = q.Change
	}
	if q.Price != 0 && q.PreviousClose != 0 {
		change = ((q.Price - q.PreviousClose) / q.PreviousClose) * 100
	}
	return change
}

func heatmapSizingValueByMarketCap(cap int64, fallbackMinCap float64) float64 {
	value := float64(cap)
	if value <= 0 {
		if fallbackMinCap <= 0 || !isFinite(fallbackMinCap) {
			fallbackMinCap = 1
		}
		value = fallbackMinCap * 0.1
	}
	if value <= 0 {
		value = 1
	}
	return value
}

// BuildPortfolioHeatmap builds heatmap items from portfolio items.
func (hm *HeatmapModel) BuildPortfolioHeatmap(items []PortfolioItem) {
	hm.Type = HeatmapPortfolio
	hm.Loading = false
	hm.Items = []HeatmapItem{}

	for _, pi := range items {
		if pi.Quote == nil {
			continue
		}

		value := pi.Quote.Price * pi.Position
		if hm.PortfolioScale == PortfolioHeatmapByMarketCap {
			value = float64(pi.Quote.MarketCap)
		}
		if value <= 0 {
			continue
		}

		hm.Items = append(hm.Items, HeatmapItem{
			Label:    pi.Symbol,
			Value:    value,
			Change:   quoteChangePercent(pi.Quote),
			Quote:    pi.Quote,
			Selected: pi.Symbol == hm.SelectedLabel,
		})
	}

	hm.applySorting()
	hm.findSelectedIndex()
}

// BuildWatchlistHeatmap builds heatmap items from all watchlist items across all groups,
// sized by market cap and colored by daily change %.
func (hm *HeatmapModel) BuildWatchlistHeatmap(items []WatchlistItem) {
	hm.Type = HeatmapWatchlist
	hm.Loading = false
	hm.Items = []HeatmapItem{}

	// Find minimum non-zero market cap for fallback sizing.
	minCap := math.Inf(1)
	for _, wi := range items {
		if wi.Quote != nil && wi.Quote.MarketCap > 0 && float64(wi.Quote.MarketCap) < minCap {
			minCap = float64(wi.Quote.MarketCap)
		}
	}
	if !isFinite(minCap) || minCap <= 0 {
		minCap = 1
	}

	missingCap := 0
	for _, wi := range items {
		if wi.Quote == nil {
			continue
		}
		value := heatmapSizingValueByMarketCap(wi.Quote.MarketCap, minCap)
		if wi.Quote.MarketCap <= 0 {
			missingCap++
		}

		hm.Items = append(hm.Items, HeatmapItem{
			Label:    wi.Symbol,
			Value:    value,
			Change:   quoteChangePercent(wi.Quote),
			Quote:    wi.Quote,
			Selected: wi.Symbol == hm.SelectedLabel,
		})
	}

	hm.applySorting()
	hm.findSelectedIndex()
	logger.Log("heatmap: build type=watchlist items=%d missing_cap=%d", len(hm.Items), missingCap)
}

// BuildYahooQuoteHeatmap builds items from Yahoo quote lists like Most Active and Trend Now.
func (hm *HeatmapModel) BuildYahooQuoteHeatmap(kind HeatmapType, quotes []yahoo.Quote) {
	hm.Type = kind
	hm.Loading = false
	hm.Items = []HeatmapItem{}

	minCap := math.Inf(1)
	capCount := 0
	for _, q := range quotes {
		if q.MarketCap > 0 && float64(q.MarketCap) < minCap {
			minCap = float64(q.MarketCap)
		}
		if q.MarketCap > 0 {
			capCount++
		}
	}
	if !isFinite(minCap) || minCap <= 0 {
		minCap = 1
	}

	missingCapFallback := 0

	for _, q := range quotes {
		if strings.TrimSpace(q.Symbol) == "" {
			continue
		}
		// Use market cap for proportional sizing (larger cap = larger box).
		// If a symbol has no market cap from Yahoo, keep it small instead of
		// switching to volume (which can distort relative sizes badly).
		value := heatmapSizingValueByMarketCap(q.MarketCap, minCap)
		if q.MarketCap <= 0 {
			missingCapFallback++
		}

		hm.Items = append(hm.Items, HeatmapItem{
			Label:    q.Symbol,
			Value:    value,
			Change:   quoteChangePercent(&q),
			Quote:    &q,
			Selected: q.Symbol == hm.SelectedLabel,
		})
	}

	hm.applySorting()
	hm.findSelectedIndex()

	if len(hm.Items) > 0 {
		topN := 3
		if len(hm.Items) < topN {
			topN = len(hm.Items)
		}
		topParts := make([]string, 0, topN)
		for i := 0; i < topN; i++ {
			topParts = append(topParts, fmt.Sprintf("%s=%.3g", hm.Items[i].Label, hm.Items[i].Value))
		}
		ratio := 0.0
		if len(hm.Items) > 1 && hm.Items[1].Value > 0 {
			ratio = hm.Items[0].Value / hm.Items[1].Value
		}
		logger.Log("heatmap: build type=%s items=%d cap_quotes=%d fallback_no_cap=%d top=%s top_ratio_1_2=%.3f",
			heatmapTypeName(kind), len(hm.Items), capCount, missingCapFallback, strings.Join(topParts, ","), ratio)
	}
}

// applySorting sorts heatmap items based on current sort mode.
func (hm *HeatmapModel) applySorting() {
	sort.Slice(hm.Items, func(i, j int) bool {
		switch hm.SortBy {
		case SortBySize:
			return hm.Items[i].Value > hm.Items[j].Value
		case SortByChange:
			return hm.Items[i].Change > hm.Items[j].Change
		case SortByName:
			return hm.Items[i].Label < hm.Items[j].Label
		default:
			return hm.Items[i].Value > hm.Items[j].Value
		}
	})
}

// findSelectedIndex updates SelectedIdx to match SelectedLabel if possible.
func (hm *HeatmapModel) findSelectedIndex() {
	if hm.SelectedLabel == "" && len(hm.Items) > 0 {
		hm.SelectedIdx = 0
		return
	}

	for i, item := range hm.Items {
		if item.Label == hm.SelectedLabel {
			hm.SelectedIdx = i
			return
		}
	}

	if hm.SelectedIdx >= len(hm.Items) {
		hm.SelectedIdx = len(hm.Items) - 1
	}
	if hm.SelectedIdx < 0 {
		hm.SelectedIdx = 0
	}
}

// SelectUp moves selection up in the heatmap.
func (hm *HeatmapModel) SelectUp() {
	if len(hm.VisibleIdxs) == 0 {
		return
	}

	// Find current selection's position within VisibleIdxs.
	pos := -1
	for i, idx := range hm.VisibleIdxs {
		if idx == hm.SelectedIdx {
			pos = i
			break
		}
	}

	// If not found in visible list, snap to first visible.
	if pos < 0 {
		hm.SelectedIdx = hm.VisibleIdxs[0]
		hm.updateSelectedLabel()
		return
	}

	if pos > 0 {
		hm.SelectedIdx = hm.VisibleIdxs[pos-1]
	} else {
		// At first visible: wrap to last visible
		hm.SelectedIdx = hm.VisibleIdxs[len(hm.VisibleIdxs)-1]
	}
	hm.updateSelectedLabel()
}

// SelectDown moves selection down in the heatmap.
func (hm *HeatmapModel) SelectDown() {
	if len(hm.VisibleIdxs) == 0 {
		return
	}

	// Find current selection's position within VisibleIdxs.
	pos := -1
	for i, idx := range hm.VisibleIdxs {
		if idx == hm.SelectedIdx {
			pos = i
			break
		}
	}

	// If not found in visible list, snap to first visible.
	if pos < 0 {
		hm.SelectedIdx = hm.VisibleIdxs[0]
		hm.updateSelectedLabel()
		return
	}

	if pos < len(hm.VisibleIdxs)-1 {
		hm.SelectedIdx = hm.VisibleIdxs[pos+1]
	} else {
		// At last visible: wrap to first visible
		hm.SelectedIdx = hm.VisibleIdxs[0]
	}
	hm.updateSelectedLabel()
}

func (hm *HeatmapModel) ensureVisibleSelection() {
	if len(hm.VisibleIdxs) == 0 {
		return
	}
	for _, idx := range hm.VisibleIdxs {
		if idx == hm.SelectedIdx {
			return
		}
	}
	hm.SelectedIdx = hm.VisibleIdxs[0]
	hm.updateSelectedLabel()
}

// updateSelectedLabel syncs SelectedLabel with current SelectedIdx.
func (hm *HeatmapModel) updateSelectedLabel() {
	if hm.SelectedIdx >= 0 && hm.SelectedIdx < len(hm.Items) {
		hm.SelectedLabel = hm.Items[hm.SelectedIdx].Label
	}
}

// SelectedItem returns the currently selected heatmap item.
func (hm *HeatmapModel) SelectedItem() *HeatmapItem {
	if hm.SelectedIdx >= 0 && hm.SelectedIdx < len(hm.Items) {
		return &hm.Items[hm.SelectedIdx]
	}
	return nil
}

// ChangeSort changes the sort mode and re-sorts items.
func (hm *HeatmapModel) ChangeSort(sortBy SortBy) {
	hm.SortBy = sortBy
	hm.applySorting()
	hm.findSelectedIndex()
	hm.VisibleIdxs = nil
}

// RebuildVisibleIdxs recomputes which items are visible for the given canvas size.
// This must be called from Update (not View) so the result persists in the real model.
func (hm *HeatmapModel) RebuildVisibleIdxs(width, height int) {
	if len(hm.Items) == 0 || width <= 0 || height <= 0 {
		hm.VisibleIdxs = nil
		return
	}

	const minTileChars = 3
	canvasCharArea := float64(width * height)

	var layoutItems []HeatmapItem
	for _, item := range hm.Items {
		testTotal := 0.0
		for _, li := range layoutItems {
			testTotal += li.Value
		}
		testTotal += item.Value

		minShare := item.Value / testTotal
		projectedArea := minShare * canvasCharArea
		if projectedArea < float64(minTileChars*minTileChars) {
			break
		}
		layoutItems = append(layoutItems, item)
	}
	if len(layoutItems) == 0 && len(hm.Items) > 0 {
		layoutItems = hm.Items[:1]
	}

	hm.VisibleIdxs = hm.VisibleIdxs[:0]
	for i, item := range hm.Items {
		for _, li := range layoutItems {
			if li.Label == item.Label {
				hm.VisibleIdxs = append(hm.VisibleIdxs, i)
				break
			}
		}
	}
}

// RenderHeatmap renders the heatmap view.
func (m Model) RenderHeatmap(hm *HeatmapModel, width, height int) string {
	if hm.Loading {
		return lipgloss.Place(width, height/2,
			lipgloss.Center, lipgloss.Center,
			nameStyle.Render("Loading heatmap feed..."))
	}
	if len(hm.Items) == 0 {
		return lipgloss.Place(width, height/2,
			lipgloss.Center, lipgloss.Center,
			nameStyle.Render("No heatmap data available."))
	}
	// Do not attempt to render the heatmap if there are too few items, as it won't look good and may cause layout issues. Show a message instead.
	if len(hm.Items) < 5 {
		return lipgloss.Place(width, height/2,
			lipgloss.Center, lipgloss.Center,
			nameStyle.Render("Need at least 5 items to render heatmap."))
	}

	// Title bar with mode indicator
	title := titleStyle.Render(" ◆ Finsight ")
	viewTabs := m.renderViewTabs()

	var heatmapTypeLabel string
	switch hm.Type {
	case HeatmapPortfolio:
		heatmapTypeLabel = "Portfolio Allocation · Size: " + portfolioHeatmapScaleLabel(hm.PortfolioScale)
	case HeatmapMostActive:
		heatmapTypeLabel = "Most Active"
	case HeatmapTrendNow:
		heatmapTypeLabel = "Trend Now"
	case HeatmapWatchlist:
		heatmapTypeLabel = "Watchlist (All Groups)"
	default:
		heatmapTypeLabel = "Heatmap"
	}

	subtitle := nameStyle.Render(" " + heatmapTypeLabel)

	// Heatmap treemap rendering - use most of available space
	// Reserve 3 lines: title, subtitle, help footer
	treemapHeight := height - 3
	if treemapHeight < 4 {
		treemapHeight = 4
	}
	treemap := m.renderHeatmapTreemap(hm, width-2, treemapHeight)

	// Help footer
	helpText := renderHeatmapHelp(hm)

	output := lipgloss.JoinVertical(
		lipgloss.Top,
		title+" "+viewTabs,
		subtitle,
		treemap,
		helpText,
	)

	return output
}

// HeatmapRect represents a positioned rectangle in the treemap.
type HeatmapRect struct {
	Item   HeatmapItem
	X, Y   int
	Width  int
	Height int
	Idx    int
}

type squarifyTile struct {
	item HeatmapItem
	idx  int
	area float64
}

type treemapBounds struct {
	x int
	y int
	w int
	h int
}

// renderHeatmapTreemap renders a proportional treemap layout based on item values.
func (m Model) renderHeatmapTreemap(hm *HeatmapModel, width, height int) string {
	if len(hm.Items) == 0 {
		hm.VisibleIdxs = nil
		return ""
	}

	const minTileChars = 3 // minimum tile dimension in characters

	// Items are expected to be sorted descending by Value (market cap).
	// Pre-filter: only include symbols whose proportional share of the canvas
	// is large enough to get at least a minTileChars×minTileChars tile.
	// We iteratively recompute the threshold as items are added, so that
	// freed space from dropped symbols isn't wasted — it flows back to the
	// remaining larger-cap tiles, making them bigger and squarer.
	canvasCharArea := float64(width * height)
	if canvasCharArea <= 0 {
		return ""
	}

	var layoutItems []HeatmapItem
	runningTotal := 0.0
	for _, item := range hm.Items {
		runningTotal += item.Value
	}

	for _, item := range hm.Items {
		// Compute what share this item would get if we include it.
		// We use the total of items included so far + this item.
		testTotal := 0.0
		for _, li := range layoutItems {
			testTotal += li.Value
		}
		testTotal += item.Value

		// The smallest item in the current set is item itself (sorted desc).
		// If its proportional area < minTileChars², it won't fit cleanly.
		minShare := item.Value / testTotal
		projectedArea := minShare * canvasCharArea
		if projectedArea < float64(minTileChars*minTileChars) {
			// This item and all subsequent (smaller) ones won't fit — stop here.
			break
		}
		layoutItems = append(layoutItems, item)
	}

	// Always show at least 1 item.
	if len(layoutItems) == 0 && len(hm.Items) > 0 {
		layoutItems = hm.Items[:1]
	}

	// Recompute total for only the items we're laying out.
	totalValue := 0.0
	for _, item := range layoutItems {
		totalValue += item.Value
	}
	if totalValue == 0 {
		totalValue = 1
	}

	rects := m.computeTreemapLayout(layoutItems, 0, 0, width, height, totalValue, hm.SelectedIdx)

	// Safety net: drop any tile still too small to render.
	filtered := rects[:0]
	for _, r := range rects {
		if r.Width >= minTileChars && r.Height >= minTileChars {
			filtered = append(filtered, r)
		}
	}

	canvas := m.renderTreemapCanvas(filtered, width, height, hm.SelectedIdx)
	return canvas
}

// computeTreemapLayout arranges items using a squarified treemap layout.
func (m Model) computeTreemapLayout(items []HeatmapItem, x, y, width, height int, totalValue float64, selectedIdx int) []HeatmapRect {
	if len(items) == 0 || width <= 0 || height <= 0 {
		return nil
	}
	_ = selectedIdx

	if totalValue <= 0 {
		for _, item := range items {
			if item.Value > 0 {
				totalValue += item.Value
			}
		}
		if totalValue <= 0 {
			totalValue = float64(len(items))
		}
	}

	// Terminal character cells are approximately 2:1 tall:wide (height:width).
	// All area and aspect-ratio calculations use pixel-space so that tiles
	// that are geometrically square in pixels end up looking square on screen,
	// not as tall thin strips.
	const cellAspect = 2.0
	pixelW := float64(width)
	pixelH := float64(height) * cellAspect
	canvasArea := pixelW * pixelH
	tiles := make([]squarifyTile, 0, len(items))
	for i, item := range items {
		value := item.Value
		if value <= 0 {
			value = 1
		}
		area := (value / totalValue) * canvasArea
		if area <= 0 {
			area = 1
		}
		tiles = append(tiles, squarifyTile{item: item, idx: i, area: area})
	}

	// Squarified treemap assumes descending order for better aspect ratios.
	sort.SliceStable(tiles, func(i, j int) bool {
		return tiles[i].area > tiles[j].area
	})

	var rects []HeatmapRect
	bounds := treemapBounds{x: x, y: y, w: width, h: height}
	row := make([]squarifyTile, 0, 8)

	for len(tiles) > 0 && bounds.w > 0 && bounds.h > 0 {
		next := tiles[0]
		// Use effective pixel dimensions for aspect-ratio decisions.
		side := math.Min(float64(bounds.w), float64(bounds.h)*cellAspect)

		if len(row) == 0 {
			row = append(row, next)
			tiles = tiles[1:]
			continue
		}

		currWorst := m.worstAspectRatio(row, side)
		nextWorst := m.worstAspectRatio(append(row, next), side)
		// Squarify rule with relaxation for small rows:
		// For rows with <2 items, allow slight worsening (1.15x) to group mega-caps together.
		// This avoids tall-thin strips for top symbols (NVDA, AAPL, MSFT).
		// For larger rows (2+ items), use canonical rule (nextWorst <= currWorst).
		allowedWorseningFactor := 1.0
		if len(row) < 2 {
			allowedWorseningFactor = 1.15
		}
		if nextWorst <= currWorst*allowedWorseningFactor {
			row = append(row, next)
			tiles = tiles[1:]
			continue
		}

		var placed []HeatmapRect
		placed, bounds = m.layoutSquarifiedRow(row, bounds)
		rects = append(rects, placed...)
		row = row[:0]
	}

	if len(row) > 0 && bounds.w > 0 && bounds.h > 0 {
		placed, _ := m.layoutSquarifiedRow(row, bounds)
		rects = append(rects, placed...)
	}

	return rects
}

func (m Model) worstAspectRatio(row []squarifyTile, side float64) float64 {
	if len(row) == 0 || side <= 0 {
		return math.Inf(1)
	}

	sumArea := 0.0
	minArea := math.Inf(1)
	maxArea := 0.0
	for _, t := range row {
		a := t.area
		if a <= 0 {
			continue
		}
		sumArea += a
		if a < minArea {
			minArea = a
		}
		if a > maxArea {
			maxArea = a
		}
	}
	if sumArea <= 0 || !isFinite(minArea) || minArea <= 0 {
		return math.Inf(1)
	}

	side2 := side * side
	return math.Max((side2*maxArea)/(sumArea*sumArea), (sumArea*sumArea)/(side2*minArea))
}

func (m Model) layoutSquarifiedRow(row []squarifyTile, bounds treemapBounds) ([]HeatmapRect, treemapBounds) {
	if len(row) == 0 || bounds.w <= 0 || bounds.h <= 0 {
		return nil, bounds
	}

	rowArea := 0.0
	for _, t := range row {
		rowArea += t.area
	}
	if rowArea <= 0 {
		return nil, bounds
	}

	// Pick the strip orientation that yields better aspect ratios for this row.
	horizontalSplit := shouldUseHorizontalSplit(row, rowArea, bounds)
	var rects []HeatmapRect

	const cellAspect = 2.0
	if horizontalSplit {
		// rowArea is in pixel^2; strip pixel-height = rowArea/pixelWidth.
		// Convert back to char-height by dividing by cellAspect.
		stripH := int(math.Round(rowArea / (float64(bounds.w) * cellAspect)))
		if stripH < 1 {
			stripH = 1
		}
		if stripH > bounds.h {
			stripH = bounds.h
		}

		widths := allocateByArea(row, rowArea, bounds.w)
		cx := bounds.x
		for i, t := range row {
			w := widths[i]
			if i == len(row)-1 {
				w = bounds.x + bounds.w - cx
			}
			if w > 0 {
				rects = append(rects, HeatmapRect{
					Item:   t.item,
					X:      cx,
					Y:      bounds.y,
					Width:  w,
					Height: stripH,
					Idx:    t.idx,
				})
			}
			cx += w
		}

		bounds.y += stripH
		bounds.h -= stripH
		return rects, bounds
	}

	// rowArea is in pixel^2; strip pixel-width = rowArea/pixelHeight.
	// Pixel-width equals char-width (no cellAspect needed for width).
	stripW := int(math.Round(rowArea / (float64(bounds.h) * cellAspect)))
	if stripW < 1 {
		stripW = 1
	}
	if stripW > bounds.w {
		stripW = bounds.w
	}

	heights := allocateByArea(row, rowArea, bounds.h)
	cy := bounds.y
	for i, t := range row {
		h := heights[i]
		if i == len(row)-1 {
			h = bounds.y + bounds.h - cy
		}
		if h > 0 {
			rects = append(rects, HeatmapRect{
				Item:   t.item,
				X:      bounds.x,
				Y:      cy,
				Width:  stripW,
				Height: h,
				Idx:    t.idx,
			})
		}
		cy += h
	}

	bounds.x += stripW
	bounds.w -= stripW
	return rects, bounds
}

func allocateByArea(row []squarifyTile, rowArea float64, totalLen int) []int {
	parts := make([]int, len(row))
	if len(row) == 0 || rowArea <= 0 || totalLen <= 0 {
		return parts
	}
	if totalLen < len(row) {
		for i := 0; i < totalLen; i++ {
			parts[i] = 1
		}
		return parts
	}

	used := 0
	for i := 0; i < len(row)-1; i++ {
		remainingSlots := len(row) - i - 1
		maxAllowed := totalLen - used - remainingSlots
		if maxAllowed < 1 {
			maxAllowed = 1
		}

		portion := int(math.Round((row[i].area / rowArea) * float64(totalLen)))
		if portion < 1 {
			portion = 1
		}
		if portion > maxAllowed {
			portion = maxAllowed
		}

		parts[i] = portion
		used += portion
	}

	parts[len(row)-1] = totalLen - used
	if parts[len(row)-1] < 1 {
		parts[len(row)-1] = 1
	}

	return parts
}

func shouldUseHorizontalSplit(row []squarifyTile, rowArea float64, bounds treemapBounds) bool {
	if bounds.w <= 0 || bounds.h <= 0 || rowArea <= 0 {
		return bounds.w <= bounds.h
	}

	// Pass pixel-space dimensions: width stays same, height scaled by cellAspect.
	const cellAspect = 2.0
	hWorst := projectedWorstAspect(row, rowArea, float64(bounds.w), true)
	vWorst := projectedWorstAspect(row, rowArea, float64(bounds.h)*cellAspect, false)

	if isFinite(hWorst) && isFinite(vWorst) {
		if hWorst < vWorst {
			return true
		}
		if vWorst < hWorst {
			return false
		}
	}

	// Tie-breaker: split along shorter side.
	return bounds.w <= bounds.h
}

func projectedWorstAspect(row []squarifyTile, rowArea, fixedLen float64, horizontal bool) float64 {
	if len(row) == 0 || rowArea <= 0 || fixedLen <= 0 {
		return math.Inf(1)
	}

	stripThickness := rowArea / fixedLen
	if stripThickness <= 0 {
		return math.Inf(1)
	}

	worst := 0.0
	for _, t := range row {
		a := t.area
		if a <= 0 {
			continue
		}
		var w, h float64
		if horizontal {
			h = stripThickness
			w = a / h
		} else {
			w = stripThickness
			h = a / w
		}
		if w <= 0 || h <= 0 {
			continue
		}
		aspect := w / h
		if aspect < 1 {
			aspect = 1 / aspect
		}
		if aspect > worst {
			worst = aspect
		}
	}
	if worst == 0 {
		return math.Inf(1)
	}
	return worst
}

func isFinite(v float64) bool {
	return !math.IsInf(v, 0) && !math.IsNaN(v)
}

// renderTreemapCanvas renders all treemap rectangles onto a canvas-like structure.
func (m Model) renderTreemapCanvas(rects []HeatmapRect, canvasWidth, canvasHeight int, selectedIdx int) string {
	if len(rects) == 0 {
		return ""
	}

	if canvasWidth <= 0 || canvasHeight <= 0 {
		return ""
	}

	type styledCell struct {
		ch       rune
		style    lipgloss.Style
		hasStyle bool
	}

	canvas := make([][]styledCell, canvasHeight)
	for y := 0; y < canvasHeight; y++ {
		row := make([]styledCell, canvasWidth)
		for x := 0; x < canvasWidth; x++ {
			row[x] = styledCell{ch: ' '}
		}
		canvas[y] = row
	}

	buildBoxLines := func(r *HeatmapRect, selected bool) []string {
		var textLine1, textLine2 string

		if r.Width >= 18 && r.Height >= 5 {
			textLine1 = r.Item.Label
			textLine2 = fmt.Sprintf("%.2f%%", r.Item.Change)
		} else if r.Width >= 12 && r.Height >= 4 {
			textLine1 = r.Item.Label
			textLine2 = fmt.Sprintf("%.1f%%", r.Item.Change)
		} else if r.Width >= 8 && r.Height >= 3 {
			textLine1 = r.Item.Label
			if len(textLine1) > r.Width-2 {
				textLine1 = textLine1[:r.Width-2]
			}
		} else if r.Width >= 4 {
			abbr := r.Item.Label
			if len(abbr) > r.Width-2 {
				abbr = abbr[:r.Width-2]
			}
			textLine1 = abbr
		}

		content := m.renderBoxContent(textLine1, textLine2, r.Width, r.Height, selected)
		return strings.Split(content, "\n")
	}

	drawRect := func(r *HeatmapRect, drawX, drawY int, selected bool) {
		if r.Width <= 0 || r.Height <= 0 {
			return
		}
		if drawX >= canvasWidth || drawY >= canvasHeight || drawX+r.Width <= 0 || drawY+r.Height <= 0 {
			return
		}

		lines := buildBoxLines(r, selected)
		style := getCellBackgroundStyle(r.Item.Change, selected)

		startX := drawX
		if startX < 0 {
			startX = 0
		}
		startY := drawY
		if startY < 0 {
			startY = 0
		}
		endX := drawX + r.Width
		if endX > canvasWidth {
			endX = canvasWidth
		}
		endY := drawY + r.Height
		if endY > canvasHeight {
			endY = canvasHeight
		}

		for yy := startY; yy < endY; yy++ {
			srcY := yy - drawY
			if srcY < 0 || srcY >= len(lines) {
				continue
			}
			lineRunes := []rune(lines[srcY])
			for xx := startX; xx < endX; xx++ {
				srcX := xx - drawX
				if srcX < 0 || srcX >= len(lineRunes) {
					continue
				}
				canvas[yy][xx] = styledCell{ch: lineRunes[srcX], style: style, hasStyle: true}
			}
		}
	}

	var selectedRect *HeatmapRect
	for i := range rects {
		if rects[i].Idx == selectedIdx {
			selectedRect = &rects[i]
			break
		}
	}

	selectedHas3D := selectedRect != nil && selectedRect.Width >= 5

	// Base pass: draw non-selected tiles only.
	for i := range rects {
		r := &rects[i]
		if selectedRect != nil && r.Idx == selectedRect.Idx {
			continue
		}
		drawRect(r, r.X, r.Y, false)
	}

	if selectedRect != nil {
		if !selectedHas3D {
			drawRect(selectedRect, selectedRect.X, selectedRect.Y, true)
		} else {
			// Always lift selected tile by one cell; drawRect clips overflow,
			// so top/left overflow is intentionally trimmed at canvas edges.
			dx, dy := -1, -1

			shadowStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#1a1a1a")).Background(lipgloss.Color("#1a1a1a"))

			// Draw shadow where the tile moved out: right edge if shifted left.
			if dx < 0 {
				shadowX := selectedRect.X + selectedRect.Width - 1
				if shadowX >= 0 && shadowX < canvasWidth {
					for yy := selectedRect.Y; yy < selectedRect.Y+selectedRect.Height; yy++ {
						if yy < 0 || yy >= canvasHeight {
							continue
						}
						canvas[yy][shadowX] = styledCell{ch: ' ', style: shadowStyle, hasStyle: true}
					}
				}
			}

			// Draw shadow where the tile moved out: bottom edge if shifted up.
			if dy < 0 {
				shadowY := selectedRect.Y + selectedRect.Height - 1
				if shadowY >= 0 && shadowY < canvasHeight {
					for xx := selectedRect.X; xx < selectedRect.X+selectedRect.Width; xx++ {
						if xx < 0 || xx >= canvasWidth {
							continue
						}
						canvas[shadowY][xx] = styledCell{ch: ' ', style: shadowStyle, hasStyle: true}
					}
				}
			}

			// Draw lifted selected tile last for correct overlay precedence.
			drawRect(selectedRect, selectedRect.X+dx, selectedRect.Y+dy, true)
		}
	}

	var lines []string
	for y := 0; y < canvasHeight; y++ {
		nonEmpty := false
		for x := 0; x < canvasWidth; x++ {
			c := canvas[y][x]
			if c.hasStyle || c.ch != ' ' {
				nonEmpty = true
				break
			}
		}
		if !nonEmpty {
			continue
		}

		var b strings.Builder
		for x := 0; x < canvasWidth; x++ {
			c := canvas[y][x]
			if c.hasStyle {
				b.WriteString(c.style.Render(string(c.ch)))
			} else {
				b.WriteRune(c.ch)
			}
		}
		lines = append(lines, b.String())
	}

	return strings.Join(lines, "\n")
}

// renderRectForLine renders a single line of a heatmap rectangle.
func (m Model) renderRectForLine(r *HeatmapRect, lineY int, selectedIdx int) string {
	if lineY < r.Y || lineY >= r.Y+r.Height {
		return ""
	}

	// Determine what text to render based on available space
	var textLine1, textLine2 string

	if r.Width >= 18 && r.Height >= 5 {
		// Large box: show symbol and change
		textLine1 = r.Item.Label
		textLine2 = fmt.Sprintf("%.2f%%", r.Item.Change)
	} else if r.Width >= 12 && r.Height >= 4 {
		// Medium box: show symbol and change
		textLine1 = r.Item.Label
		textLine2 = fmt.Sprintf("%.1f%%", r.Item.Change)
	} else if r.Width >= 8 && r.Height >= 3 {
		// Small box: show symbol only
		textLine1 = r.Item.Label
		if len(textLine1) > r.Width-2 {
			textLine1 = textLine1[:r.Width-2]
		}
	} else if r.Width >= 4 {
		// Tiny box: abbreviated symbol
		abbr := r.Item.Label
		if len(abbr) > r.Width-2 {
			abbr = abbr[:r.Width-2]
		}
		textLine1 = abbr
	}

	// Build the rendered content for this entire rect
	content := m.renderBoxContent(textLine1, textLine2, r.Width, r.Height, r.Idx == selectedIdx)

	// Apply style
	style := getCellBackgroundStyle(r.Item.Change, r.Idx == selectedIdx)
	styledContent := style.Render(content)

	// Extract the line at lineY relative to the rect
	lines := strings.Split(styledContent, "\n")
	lineIdx := lineY - r.Y
	if lineIdx >= 0 && lineIdx < len(lines) {
		return lines[lineIdx]
	}

	return ""
}

// renderBoxContent creates the text content for a box given its size.
func (m Model) renderBoxContent(line1, line2 string, width, height int, selected bool) string {
	if width < 3 || height < 1 {
		return ""
	}

	// Choose border characters.
	var topLeft, topRight, botLeft, botRight, horiz, vert string
	if selected {
		topLeft, topRight, botLeft, botRight = "╔", "╗", "╚", "╝"
		horiz, vert = "═", "║"
	} else {
		topLeft, topRight, botLeft, botRight = "┌", "┐", "└", "┘"
		horiz, vert = "─", "│"
	}

	// Collect text lines (only non-empty ones that fit).
	var textLines []string
	innerH := height - 2 // rows available between top and bottom border
	if innerH < 0 {
		innerH = 0
	}
	if line1 != "" {
		textLines = append(textLines, lipgloss.PlaceHorizontal(width-2, lipgloss.Center, line1))
	}
	if line2 != "" && len(textLines) < innerH {
		textLines = append(textLines, lipgloss.PlaceHorizontal(width-2, lipgloss.Center, line2))
	}

	// Vertically center the text lines within innerH rows.
	padTop := 0
	if innerH > len(textLines) {
		padTop = (innerH - len(textLines)) / 2
	}

	var content []string
	content = append(content, topLeft+strings.Repeat(horiz, width-2)+topRight)

	blank := vert + strings.Repeat(" ", width-2) + vert
	for i := 0; i < innerH; i++ {
		textIdx := i - padTop
		if textIdx >= 0 && textIdx < len(textLines) {
			content = append(content, vert+textLines[textIdx]+vert)
		} else {
			content = append(content, blank)
		}
	}

	content = append(content, botLeft+strings.Repeat(horiz, width-2)+botRight)

	return strings.Join(content, "\n")
}

// renderHeatmapRow and renderHeatmapCell are deprecated; use treemap layout instead.
func (m Model) renderHeatmapRow(items []HeatmapItem, width, height int, totalValue float64, selectedIdx int) string {
	// Deprecated - kept for compatibility
	return ""
}

// renderHeatmapCell is deprecated.
func (m Model) renderHeatmapCell(item HeatmapItem, width, height int, selected bool) string {
	// Deprecated - kept for compatibility
	return ""
}

// getCellBackgroundStyle returns a style with background color based on change percentage.
// Positive changes use green gradient, negative use red gradient.
// The intensity increases with the magnitude of change.
func getCellBackgroundStyle(change float64, selected bool) lipgloss.Style {
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff"))

	if selected {
		// Selected: keep change-based background and color the double-line border
		// by move direction so selection remains high-contrast without white fill.
		outline := "#cfffc0"
		if change < 0 {
			outline = "#4DF765"
		} else if change > 0 {
			outline = "#ADF527"
		}
		style = style.Foreground(lipgloss.Color(outline)).Bold(true)
		// Fall through to apply change-based background below.
	}

	// Determine background color based on change magnitude and direction
	// Clamp change to -100..+100 for color intensity mapping
	intensity := math.Abs(change)
	if intensity > 100 {
		intensity = 100
	}

	if change > 0 {
		// Positive: green gradient
		// Intensity 0-10% -> light green (#00d700)
		// Intensity 10-30% -> medium green (#00ff00)
		// Intensity 30%+ -> bright green (#87ff87)
		var bgColor string
		if intensity < 5 {
			bgColor = "#004400" // Very dark green
		} else if intensity < 10 {
			bgColor = "#005500" // Dark green
		} else if intensity < 20 {
			bgColor = "#007700" // Medium-dark green
		} else if intensity < 30 {
			bgColor = "#00aa00" // Medium green
		} else if intensity < 50 {
			bgColor = "#00dd00" // Bright green
		} else {
			bgColor = "#00ff87" // Very bright green
		}
		style = style.Background(lipgloss.Color(bgColor))
	} else if change < 0 {
		// Negative: red gradient
		// Intensity 0-10% -> light red (#550000)
		// Intensity 10-30% -> medium red (#770000)
		// Intensity 30%+ -> bright red (#ff5f87)
		var bgColor string
		if intensity < 5 {
			bgColor = "#330000" // Very dark red
		} else if intensity < 10 {
			bgColor = "#550000" // Dark red
		} else if intensity < 20 {
			bgColor = "#770000" // Medium-dark red
		} else if intensity < 30 {
			bgColor = "#aa0000" // Medium red
		} else if intensity < 50 {
			bgColor = "#dd0000" // Bright red
		} else {
			bgColor = "#ff5f87" // Very bright red
		}
		style = style.Background(lipgloss.Color(bgColor))
	} else {
		// No change: neutral gray
		style = style.Background(lipgloss.Color("#333333"))
	}

	return style
}

// formatValue formats large numbers in K/M/B notation.
func formatValue(value float64) string {
	absVal := math.Abs(value)
	if absVal >= 1e9 {
		return fmt.Sprintf("%.1fB", value/1e9)
	} else if absVal >= 1e6 {
		return fmt.Sprintf("%.1fM", value/1e6)
	} else if absVal >= 1e3 {
		return fmt.Sprintf("%.1fK", value/1e3)
	}
	return fmt.Sprintf("%.0f", value)
}

// renderHeatmapHelp renders the help text for heatmap controls.
func renderHeatmapHelp(hm *HeatmapModel) string {
	help := ""
	help += helpKeyStyle.Render("↑↓") + " nav  "
	help += hintKey("T", "ype  ")
	help += hintKey("R", "eload  ")
	help += hintKey("S", "ort  ")
	if hm != nil && hm.Type == HeatmapPortfolio {
		help += hintKey("V", "alue mode  ")
	}
	help += hintKey("E", "nter  ")
	help += hintKey("W", "atchlist  ")
	help += hintKey("P", "ortfolio  ")
	help += hintKey("?", " help")

	return helpKeyStyle.Render(help)
}
