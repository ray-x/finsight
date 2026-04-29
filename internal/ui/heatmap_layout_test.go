package ui

import (
	"math"
	"testing"

	"github.com/ray-x/finsight/internal/yahoo"
)

func sumHeatmapValue(items []HeatmapItem) float64 {
	total := 0.0
	for _, it := range items {
		total += it.Value
	}
	if total <= 0 {
		return 1
	}
	return total
}

func maxAspectRatio(w, h int) float64 {
	if w <= 0 || h <= 0 {
		return math.Inf(1)
	}
	a := float64(w) / float64(h)
	if a < 1 {
		a = 1 / a
	}
	return a
}

func TestSquarifiedTreemapLargestCapIsSquareLike(t *testing.T) {
	m := Model{}
	items := []HeatmapItem{
		{Label: "NVDA", Value: 3400},
		{Label: "AAPL", Value: 3200},
		{Label: "MSFT", Value: 3000},
		{Label: "AMZN", Value: 2100},
		{Label: "META", Value: 1800},
		{Label: "GOOG", Value: 1700},
		{Label: "INTC", Value: 350},
		{Label: "NOK", Value: 120},
	}

	rects := m.computeTreemapLayout(items, 0, 0, 120, 36, sumHeatmapValue(items), 0)
	if len(rects) == 0 {
		t.Fatal("expected non-empty layout")
	}

	var nvda *HeatmapRect
	largestArea := 0
	largestLabel := ""
	for i := range rects {
		r := &rects[i]
		area := r.Width * r.Height
		if area > largestArea {
			largestArea = area
			largestLabel = r.Item.Label
		}
		if r.Item.Label == "NVDA" {
			nvda = r
		}
	}

	if nvda == nil {
		t.Fatal("NVDA rectangle not found")
	}
	if largestLabel != "NVDA" {
		t.Fatalf("expected NVDA to be largest tile, got %s", largestLabel)
	}

	// Check visual (pixel-space) aspect ratio: terminal chars are ~2:1 tall:wide.
	// A tile of w chars × h chars has pixel dimensions w × (h*2).
	const cellAspect = 2.0
	pixelW := float64(nvda.Width)
	pixelH := float64(nvda.Height) * cellAspect
	visualRatio := pixelW / pixelH
	if visualRatio < 1 {
		visualRatio = 1 / visualRatio
	}
	if visualRatio > 2.3 {
		t.Fatalf("expected NVDA tile to be visually square, visual_ratio=%.2f (w=%d h=%d)", visualRatio, nvda.Width, nvda.Height)
	}
}

func TestSquarifiedTreemapUsesTerminalHeightWithoutOverflow(t *testing.T) {
	m := Model{}
	items := []HeatmapItem{
		{Label: "NVDA", Value: 3400}, {Label: "AAPL", Value: 3200}, {Label: "MSFT", Value: 3000},
		{Label: "AMZN", Value: 2100}, {Label: "META", Value: 1800}, {Label: "GOOG", Value: 1700},
		{Label: "TSLA", Value: 1200}, {Label: "AVGO", Value: 950}, {Label: "ORCL", Value: 800},
		{Label: "AMD", Value: 650}, {Label: "ADBE", Value: 600}, {Label: "CRM", Value: 520},
		{Label: "QCOM", Value: 500}, {Label: "CSCO", Value: 460}, {Label: "INTC", Value: 350},
		{Label: "NOK", Value: 120}, {Label: "MU", Value: 180}, {Label: "SNOW", Value: 220},
		{Label: "PLTR", Value: 150}, {Label: "U", Value: 45},
	}

	width := 120
	height := 36
	rects := m.computeTreemapLayout(items, 0, 0, width, height, sumHeatmapValue(items), 0)
	if len(rects) == 0 {
		t.Fatal("expected non-empty layout")
	}

	maxBottom := 0
	tinyRects := 0
	for _, r := range rects {
		if r.X < 0 || r.Y < 0 || r.Width < 0 || r.Height < 0 {
			t.Fatalf("invalid rectangle: %+v", r)
		}
		if r.X+r.Width > width || r.Y+r.Height > height {
			t.Fatalf("rectangle out of bounds: %+v", r)
		}
		if r.Width < 4 || r.Height < 3 {
			tinyRects++
		}
		if r.Y+r.Height > maxBottom {
			maxBottom = r.Y + r.Height
		}
	}

	if maxBottom < int(float64(height)*0.8) {
		t.Fatalf("expected to use most vertical space, used %d of %d", maxBottom, height)
	}
	if tinyRects > len(rects)/3 {
		t.Fatalf("too many tiny rectangles: %d of %d", tinyRects, len(rects))
	}
}

func TestSquarifiedTreemapCoverageAndNoOverlap(t *testing.T) {
	m := Model{}
	items := []HeatmapItem{
		{Label: "NVDA", Value: 3400}, {Label: "AAPL", Value: 3200}, {Label: "MSFT", Value: 3000},
		{Label: "AMZN", Value: 2100}, {Label: "META", Value: 1800}, {Label: "GOOG", Value: 1700},
		{Label: "TSLA", Value: 1200}, {Label: "AVGO", Value: 950}, {Label: "ORCL", Value: 800},
		{Label: "AMD", Value: 650}, {Label: "ADBE", Value: 600}, {Label: "CRM", Value: 520},
		{Label: "QCOM", Value: 500}, {Label: "CSCO", Value: 460}, {Label: "INTC", Value: 350},
		{Label: "NOK", Value: 120}, {Label: "MU", Value: 180}, {Label: "SNOW", Value: 220},
		{Label: "PLTR", Value: 150}, {Label: "U", Value: 45},
	}

	width := 100
	height := 32
	rects := m.computeTreemapLayout(items, 0, 0, width, height, sumHeatmapValue(items), 0)
	if len(rects) == 0 {
		t.Fatal("expected non-empty layout")
	}

	occupied := make([][]bool, height)
	for y := 0; y < height; y++ {
		occupied[y] = make([]bool, width)
	}

	cellsUsed := 0
	for _, r := range rects {
		for yy := r.Y; yy < r.Y+r.Height; yy++ {
			for xx := r.X; xx < r.X+r.Width; xx++ {
				if occupied[yy][xx] {
					t.Fatalf("overlap at x=%d y=%d", xx, yy)
				}
				occupied[yy][xx] = true
				cellsUsed++
			}
		}
	}

	coverage := float64(cellsUsed) / float64(width*height)
	if coverage < 0.9 {
		t.Fatalf("expected >=90%% coverage, got %.2f%%", coverage*100)
	}
}

func TestBuildYahooQuoteHeatmapDoesNotUseVolumeForSizingFallback(t *testing.T) {
	hm := NewHeatmapModel()
	hm.BuildYahooQuoteHeatmap(HeatmapMostActive, []yahoo.Quote{
		{Symbol: "NVDA", MarketCap: 3000, Volume: 100},
		{Symbol: "TSLA", MarketCap: 1000, Volume: 999999999},
		{Symbol: "NOCAP", MarketCap: 0, Volume: 999999999},
	})

	if len(hm.Items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(hm.Items))
	}

	vals := map[string]float64{}
	for _, it := range hm.Items {
		vals[it.Label] = it.Value
	}

	if vals["TSLA"] >= vals["NVDA"] {
		t.Fatalf("expected TSLA value < NVDA value, got TSLA=%.2f NVDA=%.2f", vals["TSLA"], vals["NVDA"])
	}
	if vals["NOCAP"] >= vals["TSLA"] {
		t.Fatalf("expected no-cap fallback to remain small, got NOCAP=%.2f TSLA=%.2f", vals["NOCAP"], vals["TSLA"])
	}
}

func TestBuildPortfolioHeatmapDefaultsToMarketValue(t *testing.T) {
	hm := NewHeatmapModel()
	hm.BuildPortfolioHeatmap([]PortfolioItem{
		{
			WatchlistItem: WatchlistItem{
				Symbol: "BIGPOS",
				Quote:  &yahoo.Quote{Price: 10, PreviousClose: 9, MarketCap: 100},
			},
			Position: 100,
		},
		{
			WatchlistItem: WatchlistItem{
				Symbol: "BIGCAP",
				Quote:  &yahoo.Quote{Price: 10, PreviousClose: 9, MarketCap: 1000},
			},
			Position: 10,
		},
	})

	vals := map[string]float64{}
	for _, it := range hm.Items {
		vals[it.Label] = it.Value
	}

	if vals["BIGPOS"] <= vals["BIGCAP"] {
		t.Fatalf("expected market value sizing by default, got BIGPOS=%.2f BIGCAP=%.2f", vals["BIGPOS"], vals["BIGCAP"])
	}
}

func TestBuildPortfolioHeatmapCanUseMarketCap(t *testing.T) {
	hm := NewHeatmapModel()
	hm.PortfolioScale = PortfolioHeatmapByMarketCap
	hm.BuildPortfolioHeatmap([]PortfolioItem{
		{
			WatchlistItem: WatchlistItem{
				Symbol: "BIGPOS",
				Quote:  &yahoo.Quote{Price: 10, PreviousClose: 9, MarketCap: 100},
			},
			Position: 100,
		},
		{
			WatchlistItem: WatchlistItem{
				Symbol: "BIGCAP",
				Quote:  &yahoo.Quote{Price: 10, PreviousClose: 9, MarketCap: 1000},
			},
			Position: 10,
		},
	})

	vals := map[string]float64{}
	for _, it := range hm.Items {
		vals[it.Label] = it.Value
	}

	if vals["BIGCAP"] <= vals["BIGPOS"] {
		t.Fatalf("expected market cap sizing, got BIGCAP=%.2f BIGPOS=%.2f", vals["BIGCAP"], vals["BIGPOS"])
	}
}
