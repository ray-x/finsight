package chart

import (
	"math"
	"strings"
)

// Braille-based sparkline chart rendering similar to btop+.
// Uses Unicode braille characters for high-resolution terminal charts.

// Braille dot patterns for 2x4 grid per character:
//   ⡀⡁⡂⡃⡄⡅⡆⡇⠈⠉⠊⠋⠌⠍⠎⠏ etc.
//   Each braille character is a 2-column, 4-row dot matrix.

var brailleBlocks = [4][2]rune{
	{0x2801, 0x2808}, // row 0 (top)
	{0x2802, 0x2810},
	{0x2804, 0x2820},
	{0x2840, 0x2880}, // row 3 (bottom)
}

// bresenhamLine invokes set at every integer point along the line
// from (x0,y0) to (x1,y1), endpoints included. Used to smooth overlay
// lines across braille dot cells so staircase gaps are filled with
// connecting dots.
func bresenhamLine(x0, y0, x1, y1 int, set func(x, y int)) {
	dx := x1 - x0
	if dx < 0 {
		dx = -dx
	}
	dy := y1 - y0
	if dy < 0 {
		dy = -dy
	}
	sx := 1
	if x0 >= x1 {
		sx = -1
	}
	sy := 1
	if y0 >= y1 {
		sy = -1
	}
	err := dx - dy
	for {
		set(x0, y0)
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 > -dy {
			err -= dy
			x0 += sx
		}
		if e2 < dx {
			err += dx
			y0 += sy
		}
	}
}

// RenderSparkline renders a mini sparkline using braille characters.
// width is the number of terminal columns for the chart.
// height is the number of terminal rows.
func RenderSparkline(data []float64, width, height int, color string) string {
	return RenderSparklineWithOverlays(data, nil, width, height, color)
}

// RenderSparklineWithOverlays renders a filled braille sparkline plus
// one or more line overlays (e.g. moving averages) drawn on top using
// slope-aware glyphs. Overlays share the same Y axis as `data`; values
// outside the data's min/max are clamped. NaN entries are skipped.
func RenderSparklineWithOverlays(data []float64, overlays []LineOverlay, width, height int, color string) string {
	if len(data) == 0 || width <= 0 || height <= 0 {
		return ""
	}

	// Resample data to fit width * 2 points (2 dots per braille column)
	cols := width * 2
	resampled := resample(data, cols)

	// Find min/max for scaling. Include overlays so trailing slow MAs
	// (EMA100/200) don't get clamped to the chart's bottom row.
	// Skip NaN entries (used as session-gap placeholders).
	minVal, maxVal := math.Inf(1), math.Inf(-1)
	for _, v := range resampled {
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
	for _, ov := range overlays {
		for _, v := range ov.Values {
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

	// Add small padding to range
	rangeVal := maxVal - minVal
	if rangeVal == 0 {
		rangeVal = 1
	}
	if math.IsInf(minVal, 0) || math.IsInf(maxVal, 0) {
		// All values were NaN — nothing to draw.
		return ""
	}

	// Total rows in braille grid (4 dots per character row)
	totalRows := height * 4

	// Create braille grid
	grid := make([][]rune, height)
	for i := range grid {
		grid[i] = make([]rune, width)
		for j := range grid[i] {
			grid[i][j] = 0x2800 // empty braille
		}
	}

	// Plot area fill (filled from bottom to data point)
	for col := 0; col < cols && col < len(resampled); col++ {
		if math.IsNaN(resampled[col]) {
			// Session-gap placeholder: leave column empty so the
			// chart shows visible whitespace between sessions.
			continue
		}
		normalized := (resampled[col] - minVal) / rangeVal
		dotRow := int(math.Round(normalized * float64(totalRows-1)))
		dotRow = totalRows - 1 - dotRow // invert (0 = top)

		charCol := col / 2
		dotCol := col % 2

		if charCol >= width {
			continue
		}

		// Fill from data point down to bottom
		for r := dotRow; r < totalRows; r++ {
			charRow := r / 4
			dotRowInChar := r % 4
			if charRow < height {
				grid[charRow][charCol] |= brailleBlocks[dotRowInChar][dotCol]
			}
		}
	}

	// Overlay glyphs + per-cell colors laid on top of the fill.
	overlayGlyph := make([][]rune, height)
	overlayColor := make([][]string, height)
	for r := 0; r < height; r++ {
		overlayGlyph[r] = make([]rune, width)
		overlayColor[r] = make([]string, width)
	}
	for _, ov := range overlays {
		if len(ov.Values) == 0 {
			continue
		}
		vals := resampleValues(ov.Values, cols)
		setDot := func(x, y int) {
			charCol := x / 2
			dotCol := x % 2
			charRow := y / 4
			dotRowInChar := y % 4
			if charCol < 0 || charCol >= width || charRow < 0 || charRow >= height {
				return
			}
			if overlayGlyph[charRow][charCol] == 0 {
				overlayGlyph[charRow][charCol] = 0x2800
			}
			overlayGlyph[charRow][charCol] |= brailleBlocks[dotRowInChar][dotCol]
			overlayColor[charRow][charCol] = ov.Color
		}
		prevX, prevY := -1, -1
		for col := 0; col < cols && col < len(vals); col++ {
			v := vals[col]
			if math.IsNaN(v) {
				prevX = -1
				continue
			}
			n := (v - minVal) / rangeVal
			if n < 0 {
				n = 0
			}
			if n > 1 {
				n = 1
			}
			dotRow := int(math.Round(n * float64(totalRows-1)))
			dotRow = totalRows - 1 - dotRow
			if prevX < 0 {
				setDot(col, dotRow)
			} else {
				bresenhamLine(prevX, prevY, col, dotRow, setDot)
			}
			prevX, prevY = col, dotRow
		}
	}

	// Render to string with optional color
	var sb strings.Builder
	for i, row := range grid {
		if i > 0 {
			sb.WriteRune('\n')
		}
		for c, ch := range row {
			if overlayGlyph[i][c] != 0 {
				sb.WriteString(overlayColor[i][c])
				sb.WriteRune(overlayGlyph[i][c])
				sb.WriteString("\033[0m")
				continue
			}
			if color != "" {
				sb.WriteString(color)
			}
			sb.WriteRune(ch)
			if color != "" {
				sb.WriteString("\033[0m")
			}
		}
	}

	return sb.String()
}

// RenderSparklineLine renders just the line (no fill) using braille.
func RenderSparklineLine(data []float64, width, height int) string {
	return RenderSparklineLineWithOverlays(data, nil, width, height, "")
}

// RenderSparklineLineWithOverlays renders a line-only braille chart
// (no fill) plus optional line overlays. The primary line uses
// `color`; overlays use their own colors. Values outside the primary's
// min/max are clamped.
func RenderSparklineLineWithOverlays(data []float64, overlays []LineOverlay, width, height int, color string) string {
	if len(data) == 0 || width <= 0 || height <= 0 {
		return ""
	}

	cols := width * 2
	resampled := resample(data, cols)

	minVal, maxVal := math.Inf(1), math.Inf(-1)
	for _, v := range resampled {
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
	for _, ov := range overlays {
		for _, v := range ov.Values {
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

	rangeVal := maxVal - minVal
	if rangeVal == 0 {
		rangeVal = 1
	}
	if math.IsInf(minVal, 0) || math.IsInf(maxVal, 0) {
		return ""
	}

	totalRows := height * 4

	grid := make([][]rune, height)
	for i := range grid {
		grid[i] = make([]rune, width)
		for j := range grid[i] {
			grid[i][j] = 0x2800
		}
	}

	// Plot only the line
	for col := 0; col < cols && col < len(resampled); col++ {
		if math.IsNaN(resampled[col]) {
			// Session gap: leave the column empty.
			continue
		}
		normalized := (resampled[col] - minVal) / rangeVal
		dotRow := int(math.Round(normalized * float64(totalRows-1)))
		dotRow = totalRows - 1 - dotRow

		charCol := col / 2
		dotCol := col % 2

		if charCol >= width {
			continue
		}

		charRow := dotRow / 4
		dotRowInChar := dotRow % 4
		if charRow < height {
			grid[charRow][charCol] |= brailleBlocks[dotRowInChar][dotCol]
		}
	}

	// Overlay glyphs drawn as braille dots at the same resolution as
	// the primary line.
	overlayGlyph := make([][]rune, height)
	overlayColor := make([][]string, height)
	for r := 0; r < height; r++ {
		overlayGlyph[r] = make([]rune, width)
		overlayColor[r] = make([]string, width)
	}
	for _, ov := range overlays {
		if len(ov.Values) == 0 {
			continue
		}
		vals := resampleValues(ov.Values, cols)
		setDot := func(x, y int) {
			charCol := x / 2
			dotCol := x % 2
			charRow := y / 4
			dotRowInChar := y % 4
			if charCol < 0 || charCol >= width || charRow < 0 || charRow >= height {
				return
			}
			if overlayGlyph[charRow][charCol] == 0 {
				overlayGlyph[charRow][charCol] = 0x2800
			}
			overlayGlyph[charRow][charCol] |= brailleBlocks[dotRowInChar][dotCol]
			overlayColor[charRow][charCol] = ov.Color
		}
		prevX, prevY := -1, -1
		for col := 0; col < cols && col < len(vals); col++ {
			v := vals[col]
			if math.IsNaN(v) {
				prevX = -1
				continue
			}
			n := (v - minVal) / rangeVal
			if n < 0 {
				n = 0
			}
			if n > 1 {
				n = 1
			}
			dotRow := int(math.Round(n * float64(totalRows-1)))
			dotRow = totalRows - 1 - dotRow
			if prevX < 0 {
				setDot(col, dotRow)
			} else {
				bresenhamLine(prevX, prevY, col, dotRow, setDot)
			}
			prevX, prevY = col, dotRow
		}
	}

	var sb strings.Builder
	for i, row := range grid {
		if i > 0 {
			sb.WriteRune('\n')
		}
		for c, ch := range row {
			if overlayGlyph[i][c] != 0 {
				sb.WriteString(overlayColor[i][c])
				sb.WriteRune(overlayGlyph[i][c])
				sb.WriteString("\033[0m")
				continue
			}
			if color != "" {
				sb.WriteString(color)
			}
			sb.WriteRune(ch)
			if color != "" {
				sb.WriteString("\033[0m")
			}
		}
	}
	return sb.String()
}

// SimpleSparkline renders a single-row sparkline using block characters.
func SimpleSparkline(data []float64, width int) string {
	if len(data) == 0 || width <= 0 {
		return ""
	}

	bars := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	resampled := resample(data, width)

	minVal, maxVal := resampled[0], resampled[0]
	for _, v := range resampled {
		if v < minVal {
			minVal = v
		}
		if v > maxVal {
			maxVal = v
		}
	}

	rangeVal := maxVal - minVal
	if rangeVal == 0 {
		rangeVal = 1
	}

	var sb strings.Builder
	for _, v := range resampled {
		idx := int((v - minVal) / rangeVal * float64(len(bars)-1))
		if idx >= len(bars) {
			idx = len(bars) - 1
		}
		sb.WriteRune(bars[idx])
	}
	return sb.String()
}

// ColoredSparkline returns a sparkline where each bar is individually colored
// green (up) or red (down) based on direction vs the previous bar.
// greenColor and redColor are ANSI color strings (e.g. from lipgloss).
func ColoredSparkline(data []float64, width int, greenColor, redColor string) string {
	if len(data) == 0 || width <= 0 {
		return ""
	}

	bars := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	resampled := resample(data, width)

	minVal, maxVal := resampled[0], resampled[0]
	for _, v := range resampled {
		if v < minVal {
			minVal = v
		}
		if v > maxVal {
			maxVal = v
		}
	}

	rangeVal := maxVal - minVal
	if rangeVal == 0 {
		rangeVal = 1
	}

	var sb strings.Builder
	for i, v := range resampled {
		idx := int((v - minVal) / rangeVal * float64(len(bars)-1))
		if idx >= len(bars) {
			idx = len(bars) - 1
		}

		// Determine color: compare to previous value
		color := greenColor
		if i > 0 && v < resampled[i-1] {
			color = redColor
		}
		sb.WriteString(color)
		sb.WriteRune(bars[idx])
		sb.WriteString("\033[0m")
	}
	return sb.String()
}

// CompareLine holds braille grid data for one series in a comparison chart.
type CompareLine struct {
	Grid [][]rune
}

// RenderCompareLines renders two data series as overlaid braille line charts.
// Returns per-row rune grids so the caller can apply lipgloss styling per-cell.
func RenderCompareLines(data1, data2 []float64, width, height int) (CompareLine, CompareLine) {
	empty := CompareLine{}
	if width <= 0 || height <= 0 {
		return empty, empty
	}

	cols := width * 2
	totalRows := height * 4

	// Build both on a shared percentage-change scale
	r1 := resample(data1, cols)
	r2 := resample(data2, cols)

	// Compute shared min/max of percentage changes
	pctChange := func(data []float64) []float64 {
		if len(data) == 0 {
			return nil
		}
		base := data[0]
		if base == 0 {
			base = 1
		}
		out := make([]float64, len(data))
		for i, v := range data {
			out[i] = (v - base) / base
		}
		return out
	}

	pct1 := pctChange(r1)
	pct2 := pctChange(r2)

	allPct := append(pct1, pct2...)
	globalMin, globalMax := allPct[0], allPct[0]
	for _, v := range allPct {
		if v < globalMin {
			globalMin = v
		}
		if v > globalMax {
			globalMax = v
		}
	}
	globalRange := globalMax - globalMin
	if globalRange == 0 {
		globalRange = 1
	}

	// Build grids with shared scale
	buildGridShared := func(pct []float64) [][]rune {
		grid := make([][]rune, height)
		for i := range grid {
			grid[i] = make([]rune, width)
		}
		for col := 0; col < cols && col < len(pct); col++ {
			frac := (pct[col] - globalMin) / globalRange
			dotRow := int(math.Round(frac * float64(totalRows-1)))
			dotRow = totalRows - 1 - dotRow

			charCol := col / 2
			dotCol := col % 2
			if charCol >= width {
				continue
			}
			charRow := dotRow / 4
			dotRowInChar := dotRow % 4
			if charRow < height {
				grid[charRow][charCol] |= brailleBlocks[dotRowInChar][dotCol]
			}
		}
		return grid
	}

	g1 := buildGridShared(pct1)
	g2 := buildGridShared(pct2)

	return CompareLine{Grid: g1}, CompareLine{Grid: g2}
}

// SparkBar represents a single bar in a sparkline with direction info.
type SparkBar struct {
	Char rune
	Up   bool // true if value >= previous value
}

// SparklineBars returns individual bars with direction for per-bar styling.
func SparklineBars(data []float64, width int) []SparkBar {
	if len(data) == 0 || width <= 0 {
		return nil
	}

	barChars := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	resampled := resample(data, width)

	minVal, maxVal := resampled[0], resampled[0]
	for _, v := range resampled {
		if v < minVal {
			minVal = v
		}
		if v > maxVal {
			maxVal = v
		}
	}

	rangeVal := maxVal - minVal
	if rangeVal == 0 {
		rangeVal = 1
	}

	result := make([]SparkBar, len(resampled))
	for i, v := range resampled {
		idx := int((v - minVal) / rangeVal * float64(len(barChars)-1))
		if idx >= len(barChars) {
			idx = len(barChars) - 1
		}
		up := true
		if i > 0 && v < resampled[i-1] {
			up = false
		}
		result[i] = SparkBar{Char: barChars[idx], Up: up}
	}
	return result
}

func resample(data []float64, targetLen int) []float64 {
	if len(data) == 0 {
		return nil
	}
	if len(data) == targetLen {
		return data
	}

	result := make([]float64, targetLen)
	ratio := float64(len(data)-1) / float64(targetLen-1)

	for i := 0; i < targetLen; i++ {
		pos := ratio * float64(i)
		low := int(math.Floor(pos))
		high := int(math.Ceil(pos))

		if high >= len(data) {
			high = len(data) - 1
		}
		if low >= len(data) {
			low = len(data) - 1
		}

		frac := pos - float64(low)
		result[i] = data[low]*(1-frac) + data[high]*frac
	}

	return result
}
