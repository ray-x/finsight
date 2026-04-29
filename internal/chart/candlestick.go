package chart

import (
	"math"
	"strings"
)

// Candlestick renders an inline candlestick chart using Unicode block characters.
// Each column = 1 candle. Green (open < close), Red (open > close).
// Only uses open and close values.
//
// Block characters used:
//   █ full block     ▇ 7/8    ▆ 3/4    ▅ 5/8
//   ▄ 1/2            ▃ 3/8    ▂ 1/4    ▁ 1/8
//   ╷ wick (thin line above/below body)

var (
	// GreenFg and RedFg are ANSI escape sequences for candlestick colors.
	// They default to bright green/red but can be overridden by the UI theme.
	GreenFg = "\033[38;2;0;255;135m"
	RedFg   = "\033[38;2;255;95;135m"
)

const resetFg = "\033[0m"

var bodyBlocks = []rune{' ', '▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// ovPt is one sample of an overlay line at a char column / row.
type ovPt struct {
	row   int
	valid bool
}

// pickLineGlyph chooses a glyph based on the slope arriving from
// pts[i-1] and leaving toward pts[i+1]. Row indices grow DOWNWARD.
//
//	─   flat        ╱ rising through   ╲ falling through
//	╯   flat→rise   ╮ flat→fall
//	╭   rise→flat   ╰ fall→flat
func pickLineGlyph(pts []ovPt, i int) rune {
	in, out := 0, 0
	if i > 0 && pts[i-1].valid {
		switch {
		case pts[i].row < pts[i-1].row:
			in = -1
		case pts[i].row > pts[i-1].row:
			in = +1
		}
	}
	if i+1 < len(pts) && pts[i+1].valid {
		switch {
		case pts[i+1].row < pts[i].row:
			out = -1
		case pts[i+1].row > pts[i].row:
			out = +1
		}
	}
	switch {
	case in == -1 && out == -1:
		return '╱'
	case in == +1 && out == +1:
		return '╲'
	case in == 0 && out == -1:
		return '╯'
	case in == 0 && out == +1:
		return '╮'
	case in == -1 && out == 0:
		return '╭'
	case in == +1 && out == 0:
		return '╰'
	case in == -1 && out == +1:
		return '╮' // peak
	case in == +1 && out == -1:
		return '╰' // valley
	}
	return '─'
}

// Candle represents a single candlestick.
type Candle struct {
	Open  float64
	Close float64
	High  float64
	Low   float64
}

// LineOverlay is a line series (typically a moving average) drawn on
// top of a candlestick chart. Values must have the same length as the
// candles slice passed to the renderer. NaN entries are skipped.
type LineOverlay struct {
	Values []float64
	Color  string // ANSI foreground escape, e.g. "\033[38;2;...m"
	Label  string // for optional legend / debugging
}

// RenderCandlestick renders a candlestick chart for the table inline view.
// width = number of terminal columns, height = number of terminal rows.
func RenderCandlestick(candles []Candle, width, height int) string {
	return RenderCandlestickWithOverlays(candles, nil, width, height)
}

// RenderCandlestickWithOverlays renders a candlestick chart with any
// number of line overlays (moving averages, Bollinger bands, etc.)
// painted in gaps where the candle grid is empty. Candles always win
// over overlays visually.
func RenderCandlestickWithOverlays(candles []Candle, overlays []LineOverlay, width, height int) string {
	if len(candles) == 0 || width <= 0 || height <= 0 {
		return ""
	}

	// Resample candles to fit width
	resampled := resampleCandles(candles, width)

	// Price range: start from candle highs/lows, then EXPAND to
	// include overlay values so slow MAs (EMA100, EMA200) that trail
	// below the current candle range aren't clamped to the chart's
	// bottom row — producing the spurious diagonal ramp artifact.
	minVal := math.MaxFloat64
	maxVal := -math.MaxFloat64
	for _, c := range resampled {
		if isGapCandle(c) {
			continue
		}
		lo := math.Min(c.Open, c.Close)
		hi := math.Max(c.Open, c.Close)
		if lo < minVal {
			minVal = lo
		}
		if hi > maxVal {
			maxVal = hi
		}
	}
	for _, raw := range overlays {
		for _, v := range raw.Values {
			if math.IsNaN(v) {
				continue
			}
			if v < minVal {
				minVal = v
			}
			if v > maxVal {
				maxVal = v
			}
		}
	}

	priceRange := maxVal - minVal
	if priceRange == 0 {
		priceRange = 1
	}

	totalSubRows := height * 8 // 8 sub-rows per character row

	// Build a grid: grid[row][col] = character to display
	grid := make([][]rune, height)
	colors := make([][]string, height) // color per cell
	for r := 0; r < height; r++ {
		grid[r] = make([]rune, width)
		colors[r] = make([]string, width)
		for c := 0; c < width; c++ {
			grid[r][c] = ' '
		}
	}

	// Paint line overlays (e.g. MAs) as a dotted_line: braille dots at
	// 2 dots/col × 4 dots/row resolution with Bresenham smoothing
	// between adjacent points. Rendered to a separate overlay grid so
	// candles painted afterwards mask them — standard trading chart
	// z-order. We keep a parallel overlay layer and blit it last where
	// the candle cell is empty.
	overlayGlyphBlock := make([][]rune, height)
	overlayColorBlock := make([][]string, height)
	for r := 0; r < height; r++ {
		overlayGlyphBlock[r] = make([]rune, width)
		overlayColorBlock[r] = make([]string, width)
	}
	{
		ovCols := width * 2
		ovRows := height * 4
		for _, raw := range overlays {
			if len(raw.Values) == 0 {
				continue
			}
			vals := resampleValues(raw.Values, ovCols)
			setDot := func(x, y int) {
				if x < 0 || x >= ovCols || y < 0 || y >= ovRows {
					return
				}
				charCol := x / 2
				dotCol := x % 2
				charRow := y / 4
				dotRowInChar := y % 4
				if overlayGlyphBlock[charRow][charCol] == 0 {
					overlayGlyphBlock[charRow][charCol] = 0x2800
				}
				overlayGlyphBlock[charRow][charCol] |= brailleBlocks[dotRowInChar][dotCol]
				overlayColorBlock[charRow][charCol] = raw.Color
			}
			prevX, prevY := -1, -1
			for col := 0; col < ovCols && col < len(vals); col++ {
				v := vals[col]
				if math.IsNaN(v) {
					prevX = -1
					continue
				}
				dotRow := ovRows - 1 - int(math.Round((v-minVal)/priceRange*float64(ovRows-1)))
				if dotRow < 0 {
					dotRow = 0
				}
				if dotRow >= ovRows {
					dotRow = ovRows - 1
				}
				if prevX < 0 {
					setDot(col, dotRow)
				} else {
					bresenhamLine(prevX, prevY, col, dotRow, setDot)
				}
				prevX, prevY = col, dotRow
			}
		}
	}

	for col, candle := range resampled {
		if col >= width {
			break
		}
		if isGapCandle(candle) {
			// Session gap — leave this column empty.
			continue
		}

		isGreen := candle.Close >= candle.Open
		color := RedFg
		if isGreen {
			color = GreenFg
		}

		bodyLow := math.Min(candle.Open, candle.Close)
		bodyHigh := math.Max(candle.Open, candle.Close)

		// Normalize to sub-row positions (0 = top, totalSubRows-1 = bottom)
		topSubRow := totalSubRows - 1 - int(math.Round((bodyHigh-minVal)/priceRange*float64(totalSubRows-1)))
		botSubRow := totalSubRows - 1 - int(math.Round((bodyLow-minVal)/priceRange*float64(totalSubRows-1)))

		if topSubRow < 0 {
			topSubRow = 0
		}
		if botSubRow >= totalSubRows {
			botSubRow = totalSubRows - 1
		}

		// Fill character rows that the candle body spans
		for subRow := topSubRow; subRow <= botSubRow; subRow++ {
			charRow := subRow / 8
			if charRow >= height {
				charRow = height - 1
			}
			grid[charRow][col] = '█'
			colors[charRow][col] = color
		}

		// For partial fills at top and bottom edges
		topCharRow := topSubRow / 8
		botCharRow := botSubRow / 8
		if topCharRow >= height {
			topCharRow = height - 1
		}
		if botCharRow >= height {
			botCharRow = height - 1
		}

		// If body is very thin (same sub-row), at least draw a thin bar
		if topSubRow == botSubRow {
			charRow := topSubRow / 8
			if charRow < height {
				subInChar := topSubRow % 8
				idx := 8 - subInChar
				if idx < 1 {
					idx = 1
				}
				if idx > 8 {
					idx = 8
				}
				grid[charRow][col] = bodyBlocks[idx]
				colors[charRow][col] = color
			}
		}
	}

	// Render to string. Candles take priority; overlay dots fill cells
	// where the candle grid is empty.
	var sb strings.Builder
	for r := 0; r < height; r++ {
		if r > 0 {
			sb.WriteRune('\n')
		}
		for c := 0; c < width; c++ {
			if grid[r][c] != ' ' {
				if colors[r][c] != "" {
					sb.WriteString(colors[r][c])
					sb.WriteRune(grid[r][c])
					sb.WriteString(resetFg)
				} else {
					sb.WriteRune(grid[r][c])
				}
				continue
			}
			if overlayGlyphBlock[r][c] != 0 {
				sb.WriteString(overlayColorBlock[r][c])
				sb.WriteRune(overlayGlyphBlock[r][c])
				sb.WriteString(resetFg)
				continue
			}
			sb.WriteRune(' ')
		}
	}

	return sb.String()
}

// RenderCandlestickSimple renders a single-row candlestick bar for the table view.
// Each character is one candle, using ▁▂▃▄▅▆▇█ to show body height, colored green/red.
func RenderCandlestickSimple(candles []Candle, width int) string {
	if len(candles) == 0 || width <= 0 {
		return ""
	}

	resampled := resampleCandles(candles, width)

	// Find overall min/max
	minVal := math.MaxFloat64
	maxVal := -math.MaxFloat64
	for _, c := range resampled {
		lo := math.Min(c.Open, c.Close)
		hi := math.Max(c.Open, c.Close)
		if lo < minVal {
			minVal = lo
		}
		if hi > maxVal {
			maxVal = hi
		}
	}

	priceRange := maxVal - minVal
	if priceRange == 0 {
		priceRange = 1
	}

	var sb strings.Builder
	for _, c := range resampled {
		isGreen := c.Close >= c.Open
		color := RedFg
		if isGreen {
			color = GreenFg
		}

		// Map the midpoint of the body to a block height
		bodyMid := (c.Open + c.Close) / 2
		normalized := (bodyMid - minVal) / priceRange
		idx := int(math.Round(normalized * float64(len(bodyBlocks)-1)))
		if idx < 1 {
			idx = 1
		}
		if idx >= len(bodyBlocks) {
			idx = len(bodyBlocks) - 1
		}

		sb.WriteString(color)
		sb.WriteRune(bodyBlocks[idx])
		sb.WriteString(resetFg)
	}

	return sb.String()
}

func resampleCandles(candles []Candle, targetLen int) []Candle {
	if len(candles) == 0 {
		return nil
	}
	if len(candles) == targetLen {
		return candles
	}

	result := make([]Candle, targetLen)
	ratio := float64(len(candles)) / float64(targetLen)

	for i := 0; i < targetLen; i++ {
		pos := ratio * float64(i)
		idx := int(pos)
		if idx >= len(candles) {
			idx = len(candles) - 1
		}

		if len(candles) > targetLen {
			// Downsample: aggregate range
			end := int(ratio * float64(i+1))
			if end > len(candles) {
				end = len(candles)
			}
			if idx >= end {
				idx = end - 1
			}
			if idx < 0 {
				idx = 0
			}
			// If ALL candles in this aggregation range are gaps, or if a
			// gap sits inside a mixed bucket, keep a gap sentinel so
			// downsampling doesn't connect pre/post-session bars into a
			// synthetic spike candle.
			allGap := true
			hasGap := false
			for k := idx; k < end; k++ {
				if isGapCandle(candles[k]) {
					hasGap = true
					continue
				}
				if !isGapCandle(candles[k]) {
					allGap = false
				}
			}
			if allGap || hasGap {
				result[i] = gapCandle()
				continue
			}
			high := math.Inf(-1)
			low := math.Inf(1)
			var firstOpen, lastClose float64
			firstSet, lastSet := false, false
			for k := idx; k < end; k++ {
				if isGapCandle(candles[k]) {
					continue
				}
				if candles[k].High > high {
					high = candles[k].High
				}
				if candles[k].Low > 0 && candles[k].Low < low {
					low = candles[k].Low
				}
				if !firstSet {
					firstOpen = candles[k].Open
					firstSet = true
				}
				lastClose = candles[k].Close
				lastSet = true
			}
			if !firstSet {
				firstOpen = candles[idx].Open
			}
			if !lastSet {
				lastClose = candles[end-1].Close
			}
			result[i] = Candle{
				Open:  firstOpen,
				Close: lastClose,
				High:  high,
				Low:   low,
			}
		} else {
			// Upsample: repeat candles to fill width (may include gap
			// sentinels, which renderers skip).
			result[i] = candles[idx]
		}
	}

	return result
}

// resampleValues downsamples/upsamples a float series using the same
// index mapping as resampleCandles, taking the LAST value of each
// source window (to match candle Close) and propagating NaN when the
// window contains only NaNs.
func resampleValues(values []float64, targetLen int) []float64 {
	if len(values) == 0 || targetLen <= 0 {
		return nil
	}
	if len(values) == targetLen {
		out := make([]float64, targetLen)
		copy(out, values)
		return out
	}
	out := make([]float64, targetLen)
	ratio := float64(len(values)) / float64(targetLen)
	denom := float64(targetLen - 1)
	if denom < 1 {
		denom = 1
	}
	for i := 0; i < targetLen; i++ {
		idx := int(ratio * float64(i))
		if len(values) > targetLen {
			end := int(ratio * float64(i+1))
			if end > len(values) {
				end = len(values)
			}
			if end <= idx {
				end = idx + 1
			}
			// Prefer the last non-NaN value in the window.
			v := math.NaN()
			for k := end - 1; k >= idx; k-- {
				if !math.IsNaN(values[k]) {
					v = values[k]
					break
				}
			}
			out[i] = v
		} else {
			// Upsample: linearly interpolate between the two nearest
			// source points so smooth curves (e.g. moving averages)
			// render as diagonals instead of staircases in the dot
			// grid. NaN on either side propagates so warm-up gaps
			// stay as gaps.
			pos := float64(i) * float64(len(values)-1) / denom
			lo := int(math.Floor(pos))
			hi := lo + 1
			if hi >= len(values) {
				hi = len(values) - 1
			}
			if lo < 0 {
				lo = 0
			}
			a := values[lo]
			b := values[hi]
			if math.IsNaN(a) || math.IsNaN(b) {
				if !math.IsNaN(a) {
					out[i] = a
				} else if !math.IsNaN(b) {
					out[i] = b
				} else {
					out[i] = math.NaN()
				}
				continue
			}
			frac := pos - float64(lo)
			out[i] = a + (b-a)*frac
		}
	}
	return out
}

// RenderCandlestickBraille renders candlestick bars using braille dots.
// When showWicks is false: 2 candles per terminal char (1 dot col each), body only.
// When showWicks is true: 1 candle per 2 terminal chars. Body uses all 4 dot columns
// (4 dots wide), wicks use 2 center dot columns (2 dots wide) for visual distinction.
// Green when close >= open, red when close < open. Per-candle coloring.
// width = terminal columns, height = terminal rows.
func RenderCandlestickBraille(candles []Candle, width, height int, showWicks bool) string {
	return RenderCandlestickBrailleWithOverlays(candles, nil, width, height, showWicks)
}

// RenderCandlestickBrailleWithOverlays is the overlay-aware variant of
// RenderCandlestickBraille. Overlays are painted on top of the braille
// grid as dot glyphs so MA lines stay visible even over solid candle
// bodies.
func RenderCandlestickBrailleWithOverlays(candles []Candle, overlays []LineOverlay, width, height int, showWicks bool) string {
	if len(candles) == 0 || width <= 0 || height <= 0 {
		return ""
	}

	var numCandles int
	if showWicks {
		numCandles = width / 2 // 1 candle per 2 char columns
		if numCandles < 1 {
			numCandles = 1
		}
	} else {
		numCandles = width * 2 // 2 candles per char column
	}
	resampled := resampleCandles(candles, numCandles)

	// Find global min/max using high/low (for wicks)
	minVal := math.MaxFloat64
	maxVal := -math.MaxFloat64
	for _, c := range resampled {
		if isGapCandle(c) {
			continue
		}
		hi := c.High
		lo := c.Low
		if hi == 0 {
			hi = math.Max(c.Open, c.Close)
		}
		if lo == 0 {
			lo = math.Min(c.Open, c.Close)
		}
		if lo > 0 && lo < minVal {
			minVal = lo
		}
		if hi > maxVal {
			maxVal = hi
		}
	}
	// Expand price range to include overlay values so slow MAs
	// (EMA100/200) that trail below the current candle range don't
	// get clamped to the chart bottom.
	for _, raw := range overlays {
		for _, v := range raw.Values {
			if math.IsNaN(v) {
				continue
			}
			if v < minVal {
				minVal = v
			}
			if v > maxVal {
				maxVal = v
			}
		}
	}
	priceRange := maxVal - minVal
	if priceRange == 0 {
		priceRange = 1
	}

	totalDotRows := height * 4 // 4 dot rows per braille character row

	// braille dot patterns: [dotRow 0..3][dotCol 0..1]
	braille := [4][2]rune{
		{0x2801, 0x2808},
		{0x2802, 0x2810},
		{0x2804, 0x2820},
		{0x2840, 0x2880},
	}

	grid := make([][]rune, height)
	colColors := make([]string, numCandles)

	for r := 0; r < height; r++ {
		grid[r] = make([]rune, width)
		for c := 0; c < width; c++ {
			grid[r][c] = 0x2800 // empty braille
		}
	}

	for col, candle := range resampled {
		if col >= numCandles {
			break
		}
		if isGapCandle(candle) {
			// Session gap — leave columns empty.
			continue
		}

		isGreen := candle.Close >= candle.Open
		if isGreen {
			colColors[col] = GreenFg
		} else {
			colColors[col] = RedFg
		}

		bodyLow := math.Min(candle.Open, candle.Close)
		bodyHigh := math.Max(candle.Open, candle.Close)

		wickHigh := candle.High
		wickLow := candle.Low
		if wickHigh == 0 {
			wickHigh = bodyHigh
		}
		if wickLow == 0 {
			wickLow = bodyLow
		}

		topDotRow := totalDotRows - 1 - int(math.Round((bodyHigh-minVal)/priceRange*float64(totalDotRows-1)))
		botDotRow := totalDotRows - 1 - int(math.Round((bodyLow-minVal)/priceRange*float64(totalDotRows-1)))

		wickTopDotRow := totalDotRows - 1 - int(math.Round((wickHigh-minVal)/priceRange*float64(totalDotRows-1)))
		wickBotDotRow := totalDotRows - 1 - int(math.Round((wickLow-minVal)/priceRange*float64(totalDotRows-1)))

		if topDotRow < 0 {
			topDotRow = 0
		}
		if botDotRow >= totalDotRows {
			botDotRow = totalDotRows - 1
		}
		if wickTopDotRow < 0 {
			wickTopDotRow = 0
		}
		if wickBotDotRow >= totalDotRows {
			wickBotDotRow = totalDotRows - 1
		}

		if showWicks {
			// 1 candle per 2 chars: body = all 4 dot cols, wick = center 2 dot cols
			leftChar := col * 2
			rightChar := col*2 + 1
			if rightChar >= width {
				continue
			}

			// Draw body with all 4 dot columns (4 dots wide)
			for dr := topDotRow; dr <= botDotRow; dr++ {
				charRow := dr / 4
				dotRowInChar := dr % 4
				if charRow < height {
					grid[charRow][leftChar] |= braille[dotRowInChar][0] | braille[dotRowInChar][1]
					grid[charRow][rightChar] |= braille[dotRowInChar][0] | braille[dotRowInChar][1]
				}
			}

			// Draw upper wick with center 2 dot cols (right col of left char + left col of right char)
			for dr := wickTopDotRow; dr < topDotRow; dr++ {
				charRow := dr / 4
				dotRowInChar := dr % 4
				if charRow < height {
					grid[charRow][leftChar] |= braille[dotRowInChar][1]
					grid[charRow][rightChar] |= braille[dotRowInChar][0]
				}
			}

			// Draw lower wick with center 2 dot cols
			for dr := botDotRow + 1; dr <= wickBotDotRow; dr++ {
				charRow := dr / 4
				dotRowInChar := dr % 4
				if charRow < height {
					grid[charRow][leftChar] |= braille[dotRowInChar][1]
					grid[charRow][rightChar] |= braille[dotRowInChar][0]
				}
			}
		} else {
			// 2 candles per char: each candle gets 1 dot column
			charCol := col / 2
			dotCol := col % 2
			if charCol >= width {
				continue
			}

			for dr := topDotRow; dr <= botDotRow; dr++ {
				charRow := dr / 4
				dotRowInChar := dr % 4
				if charRow < height {
					grid[charRow][charCol] |= braille[dotRowInChar][dotCol]
				}
			}
		}
	}

	// Paint overlays as dotted_line braille dots with Bresenham
	// smoothing between adjacent points, at 2 dots/col × 4 dots/row
	// resolution. Overlay wins over empty cells; candle cells stay
	// untouched so prices read on top of MA lines.
	overlayGlyph := make([][]rune, height)
	overlayColor := make([][]string, height)
	for r := 0; r < height; r++ {
		overlayGlyph[r] = make([]rune, width)
		overlayColor[r] = make([]string, width)
	}
	{
		ovCols := width * 2
		for _, raw := range overlays {
			if len(raw.Values) == 0 {
				continue
			}
			vals := resampleValues(raw.Values, ovCols)
			setDot := func(x, y int) {
				if x < 0 || x >= ovCols || y < 0 || y >= totalDotRows {
					return
				}
				charCol := x / 2
				dotCol := x % 2
				charRow := y / 4
				dotRowInChar := y % 4
				if overlayGlyph[charRow][charCol] == 0 {
					overlayGlyph[charRow][charCol] = 0x2800
				}
				overlayGlyph[charRow][charCol] |= brailleBlocks[dotRowInChar][dotCol]
				overlayColor[charRow][charCol] = raw.Color
			}
			prevX, prevY := -1, -1
			for col := 0; col < ovCols && col < len(vals); col++ {
				v := vals[col]
				if math.IsNaN(v) {
					prevX = -1
					continue
				}
				dotRow := totalDotRows - 1 - int(math.Round((v-minVal)/priceRange*float64(totalDotRows-1)))
				if dotRow < 0 {
					dotRow = 0
				}
				if dotRow >= totalDotRows {
					dotRow = totalDotRows - 1
				}
				if prevX < 0 {
					setDot(col, dotRow)
				} else {
					bresenhamLine(prevX, prevY, col, dotRow, setDot)
				}
				prevX, prevY = col, dotRow
			}
		}
	}

	// Render with color
	var sb strings.Builder
	for r := 0; r < height; r++ {
		if r > 0 {
			sb.WriteRune('\n')
		}
		for c := 0; c < width; c++ {
			// Overlay only fills cells the candle grid left empty,
			// so prices always read on top of the MA line.
			if overlayGlyph[r][c] != 0 && grid[r][c] == 0x2800 {
				sb.WriteString(overlayColor[r][c])
				sb.WriteRune(overlayGlyph[r][c])
				sb.WriteString(resetFg)
				continue
			}
			ch := grid[r][c]
			if ch == 0x2800 {
				sb.WriteRune(' ')
				continue
			}

			if showWicks {
				// 1 candle per 2 chars — map char index to candle index
				candleIdx := c / 2
				color := ""
				if candleIdx < len(colColors) {
					color = colColors[candleIdx]
				}
				if color != "" {
					sb.WriteString(color)
				}
				sb.WriteRune(ch)
				if color != "" {
					sb.WriteString(resetFg)
				}
			} else {
				// 2 candles per char — pick dominant color
				leftCol := c * 2
				rightCol := c*2 + 1
				leftColor := ""
				rightColor := ""
				if leftCol < len(colColors) {
					leftColor = colColors[leftCol]
				}
				if rightCol < len(colColors) {
					rightColor = colColors[rightCol]
				}

				if leftColor == rightColor || rightColor == "" {
					if leftColor != "" {
						sb.WriteString(leftColor)
					}
					sb.WriteRune(ch)
					if leftColor != "" {
						sb.WriteString(resetFg)
					}
				} else if leftColor == "" {
					sb.WriteString(rightColor)
					sb.WriteRune(ch)
					sb.WriteString(resetFg)
				} else {
					sb.WriteString(rightColor)
					sb.WriteRune(ch)
					sb.WriteString(resetFg)
				}
			}
		}
	}

	return sb.String()
}

// RenderVolumeBars renders volume bars aligned to candle positions.
// Each bar is colored green/red based on whether close >= open.
// width = terminal columns, height = terminal rows (typically 2-4).
// Uses block characters ▁▂▃▄▅▆▇█ for each column.
func RenderVolumeBars(candles []Candle, volumes []int64, width, height int) string {
	if len(volumes) == 0 || width <= 0 || height <= 0 {
		return ""
	}

	numBars := width
	resampledVol := resampleVolumes(volumes, numBars)
	resampledCandles := resampleCandles(candles, numBars)
	for i := 0; i < len(resampledVol) && i < len(resampledCandles); i++ {
		if isGapCandle(resampledCandles[i]) {
			resampledVol[i] = 0
		}
	}

	var maxVol int64
	for _, v := range resampledVol {
		if v > maxVol {
			maxVol = v
		}
	}
	if maxVol == 0 {
		return ""
	}

	blocks := []rune{' ', '▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	totalLevels := height * 8

	var sb2 strings.Builder
	for row := 0; row < height; row++ {
		if row > 0 {
			sb2.WriteRune('\n')
		}
		for col := 0; col < numBars; col++ {
			vol := resampledVol[col]
			level := int(math.Round(float64(vol) / float64(maxVol) * float64(totalLevels)))
			if level > totalLevels {
				level = totalLevels
			}

			rowFromBottom := height - 1 - row
			rowBase := rowFromBottom * 8
			rowTop := rowBase + 8

			var blockIdx int
			if level >= rowTop {
				blockIdx = 8
			} else if level > rowBase {
				blockIdx = level - rowBase
			} else {
				blockIdx = 0
			}

			if blockIdx > 0 && col < len(resampledCandles) {
				isGreen := resampledCandles[col].Close >= resampledCandles[col].Open
				if isGreen {
					sb2.WriteString(GreenFg)
				} else {
					sb2.WriteString(RedFg)
				}
				sb2.WriteRune(blocks[blockIdx])
				sb2.WriteString(resetFg)
			} else {
				sb2.WriteRune(blocks[blockIdx])
			}
		}
	}

	return sb2.String()
}

func resampleVolumes(volumes []int64, targetLen int) []int64 {
	if len(volumes) == 0 || targetLen <= 0 {
		return nil
	}
	if len(volumes) == targetLen {
		return volumes
	}

	result := make([]int64, targetLen)
	ratio := float64(len(volumes)) / float64(targetLen)

	for i := 0; i < targetLen; i++ {
		pos := ratio * float64(i)
		idx := int(pos)
		if idx >= len(volumes) {
			idx = len(volumes) - 1
		}

		if len(volumes) > targetLen {
			end := int(ratio * float64(i+1))
			if end > len(volumes) {
				end = len(volumes)
			}
			if idx >= end {
				idx = end - 1
			}
			var sum int64
			count := 0
			for k := idx; k < end; k++ {
				sum += volumes[k]
				count++
			}
			if count > 0 {
				result[i] = sum / int64(count)
			}
		} else {
			result[i] = volumes[idx]
		}
	}

	return result
}
