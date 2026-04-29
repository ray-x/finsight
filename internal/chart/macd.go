package chart

import (
	"math"
	"strings"
)

// RenderMACDHistogram renders a two-row MACD histogram spanning `width`
// character columns. Each bar occupies `barWidthChars` columns (1 for
// block candles, 2 for braille-wicks candles) so the histogram aligns
// 1:1 with the candles above it.
//
// Row 0 holds positive bars growing UP from the shared zero line;
// row 1 holds negative bars hanging DOWN from the zero line.
// Positive bars are green, negative red. NaN values render as blank.
//
// Returns a two-line string (rows separated by '\n'); callers may
// concatenate with other panel rows.
func RenderMACDHistogram(hist []float64, width, barWidthChars int) string {
	if width <= 0 || len(hist) == 0 {
		return ""
	}
	if barWidthChars < 1 {
		barWidthChars = 1
	}
	numBars := width / barWidthChars
	if numBars < 1 {
		return ""
	}
	sub := resampleValues(hist, numBars)

	var maxAbs float64
	for _, v := range sub {
		if math.IsNaN(v) {
			continue
		}
		if a := math.Abs(v); a > maxAbs {
			maxAbs = a
		}
	}
	if maxAbs == 0 {
		maxAbs = 1
	}

	// Bottom-aligned glyphs (grow UP from the row floor).
	upGlyphs := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	// Top-aligned glyphs (grow DOWN from the row ceiling).
	downGlyphs := []rune{'🮂', '🮃', '▀', '🮄', '🮅', '🮆', '█', '█'}

	var topSB, botSB strings.Builder
	for _, v := range sub {
		var gTop, gBot rune = ' ', ' '
		color := ""
		if !math.IsNaN(v) && v != 0 {
			norm := math.Abs(v) / maxAbs
			idx := int(norm * float64(len(upGlyphs)-1))
			if idx < 0 {
				idx = 0
			}
			if idx >= len(upGlyphs) {
				idx = len(upGlyphs) - 1
			}
			if v > 0 {
				gTop = upGlyphs[idx]
				color = GreenFg
			} else {
				gBot = downGlyphs[idx]
				color = RedFg
			}
		}
		topCell := string(gTop)
		botCell := string(gBot)
		if color != "" {
			topCell = color + topCell + resetFg
			botCell = color + botCell + resetFg
		}
		for i := 0; i < barWidthChars; i++ {
			topSB.WriteString(topCell)
			botSB.WriteString(botCell)
		}
	}
	// Pad remainder columns (if width not exactly divisible) with spaces.
	for i := numBars * barWidthChars; i < width; i++ {
		topSB.WriteRune(' ')
		botSB.WriteRune(' ')
	}
	return topSB.String() + "\n" + botSB.String()
}
