package ui

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Markdown rendering styles (rebuilt by ApplyTheme).
var (
	mdHeadingStyle     = lipgloss.NewStyle().Bold(true).Foreground(colorYellow)
	mdBoldStyle        = lipgloss.NewStyle().Bold(true).Foreground(colorWhite)
	mdItalicStyle      = lipgloss.NewStyle().Italic(true).Foreground(colorGray)
	mdCodeStyle        = lipgloss.NewStyle().Foreground(colorBlue)
	mdBulletStyle      = lipgloss.NewStyle().Foreground(colorYellow)
	mdLabelStyle       = lipgloss.NewStyle().Bold(true).Foreground(colorGreen)
	mdHighlightStyle   = lipgloss.NewStyle().Bold(true).Foreground(colorRed)
	mdTableHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(colorBlue)
	mdTableBorderStyle = lipgloss.NewStyle().Foreground(colorDim)
	mdTableBarUpStyle  = lipgloss.NewStyle().Foreground(colorGreen)
	mdTableBarDownStyle = lipgloss.NewStyle().Foreground(colorRed)
	mdTableBarTrackStyle = lipgloss.NewStyle().Foreground(colorDim)
)

var (
	// **bold** or __bold__
	reBold = regexp.MustCompile(`\*\*(.+?)\*\*|__(.+?)__`)
	// ==highlight==
	reHighlight = regexp.MustCompile(`==(.+?)==`)
	// *italic* or _italic_ (but not inside __ pairs)
	reItalic = regexp.MustCompile(`(?:^|[^_*])\*([^*]+?)\*(?:[^_*]|$)|(?:^|[^_*])_([^_]+?)_(?:[^_*]|$)`)
	// `code`
	reCode = regexp.MustCompile("`([^`]+)`")
	// Numeric values (supports commas and decimals, e.g. -1,234.56)
	reNumeric = regexp.MustCompile(`[-+]?\d[\d,]*(?:\.\d+)?|[-+]?\.\d+`)
	// Labels like "Price Action:" or "Verdict:" at start of line
	reLabel = regexp.MustCompile(`^(\s*(?:[A-Z][A-Za-z &/]+):)`)
	// Numbered items like "1. " or "1) "
	reNumbered = regexp.MustCompile(`^(\s*\d+[.)]\s)`)
)

// renderMarkdown converts a markdown-formatted string to ANSI-styled text
// suitable for terminal display. Handles headings, bold, italic, code,
// bullet points, and label patterns.
func renderMarkdown(s string, width int) string {
	var out strings.Builder
	allLines := strings.Split(s, "\n")

	for i := 0; i < len(allLines); i++ {
		trimmed := strings.TrimSpace(allLines[i])

		// Blank line
		if trimmed == "" {
			out.WriteString("\n")
			continue
		}

		// Headings: # ## ###
		if strings.HasPrefix(trimmed, "#") {
			heading := strings.TrimLeft(trimmed, "# ")
			out.WriteString(mdHeadingStyle.Render("  "+heading) + "\n")
			continue
		}

		// Horizontal rules: --- or ***
		if trimmed == "---" || trimmed == "***" || trimmed == "___" {
			if width > 4 {
				out.WriteString(lipgloss.NewStyle().Foreground(colorDim).Render(strings.Repeat("─", width-4)) + "\n")
			}
			continue
		}

		// Markdown table: | col | col |
		if isTableLine(trimmed) {
			tableLines := []string{trimmed}
			for i+1 < len(allLines) {
				next := strings.TrimSpace(allLines[i+1])
				if !isTableLine(next) {
					break
				}
				tableLines = append(tableLines, next)
				i++
			}
			if len(tableLines) >= 2 {
				out.WriteString(renderTable(tableLines, width))
			} else {
				rendered := "  " + renderInline(trimmed)
				wrapped := wrapRendered("  ", rendered, width)
				out.WriteString(wrapped + "\n")
			}
			continue
		}

		// Bullet points: - or *
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			bullet := mdBulletStyle.Render("  •")
			rest := renderInline(trimmed[2:])
			wrapped := wrapRendered("    ", bullet+" "+rest, width)
			out.WriteString(wrapped + "\n")
			continue
		}

		// Numbered items
		if loc := reNumbered.FindStringIndex(trimmed); loc != nil {
			num := mdBulletStyle.Render("  " + strings.TrimSpace(trimmed[:loc[1]]))
			rest := renderInline(trimmed[loc[1]:])
			wrapped := wrapRendered("    ", num+rest, width)
			out.WriteString(wrapped + "\n")
			continue
		}

		// Regular line: apply inline formatting
		rendered := "  " + renderInline(trimmed)
		wrapped := wrapRendered("  ", rendered, width)
		out.WriteString(wrapped + "\n")
	}

	// Safety net: hard-truncate any line that still exceeds width.
	// This catches cases where ANSI-styled text confuses width calculations.
	result := out.String()
	if width <= 0 {
		return result
	}
	var final strings.Builder
	for i, line := range strings.Split(result, "\n") {
		if i > 0 {
			final.WriteString("\n")
		}
		if ansi.StringWidth(line) > width {
			final.WriteString(ansi.Truncate(line, width, ""))
		} else {
			final.WriteString(line)
		}
	}
	return final.String()
}

// renderInline applies inline markdown formatting (bold, italic, code, highlight, labels).
func renderInline(s string) string {
	// Labels like "Price Action:" at start
	s = reLabel.ReplaceAllStringFunc(s, func(m string) string {
		label := reLabel.FindStringSubmatch(m)
		if len(label) > 1 {
			return mdLabelStyle.Render(label[1])
		}
		return m
	})

	// Highlight: ==text==  (must be before bold to avoid conflicts)
	s = reHighlight.ReplaceAllStringFunc(s, func(m string) string {
		sub := reHighlight.FindStringSubmatch(m)
		return mdHighlightStyle.Render(sub[1])
	})

	// Bold: **text** or __text__
	s = reBold.ReplaceAllStringFunc(s, func(m string) string {
		sub := reBold.FindStringSubmatch(m)
		text := sub[1]
		if text == "" {
			text = sub[2]
		}
		return mdBoldStyle.Render(text)
	})

	// Code: `text`
	s = reCode.ReplaceAllStringFunc(s, func(m string) string {
		sub := reCode.FindStringSubmatch(m)
		return mdCodeStyle.Render(sub[1])
	})

	return s
}

// wrapRendered wraps an already-rendered line, using indent for continuation.
// Uses ANSI-aware width measurement to break on word boundaries.
func wrapRendered(indent, s string, width int) string {
	if width <= 0 {
		return s
	}
	visW := ansi.StringWidth(s)
	if visW <= width {
		return s
	}

	// Split into words (preserving ANSI sequences attached to words).
	// We walk the string character by character, splitting on spaces.
	words := splitWordsAnsi(s)
	if len(words) == 0 {
		return s
	}

	var lines []string
	var cur strings.Builder
	curW := 0
	indentW := ansi.StringWidth(indent)
	first := true

	for _, word := range words {
		ww := ansi.StringWidth(word)
		limit := width
		if !first {
			limit = width // continuation lines also use full width
		}

		if curW == 0 {
			// Start of a new line
			if !first {
				cur.WriteString(indent)
				curW = indentW
			}
			cur.WriteString(word)
			curW += ww
		} else if curW+1+ww <= limit {
			// Fits on current line with a space
			cur.WriteString(" ")
			cur.WriteString(word)
			curW += 1 + ww
		} else {
			// Wrap: finish current line, start new one
			lines = append(lines, cur.String())
			cur.Reset()
			cur.WriteString(indent)
			cur.WriteString(word)
			curW = indentW + ww
			first = false
		}
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}

	return strings.Join(lines, "\n")
}

// splitWordsAnsi splits s on whitespace while keeping ANSI escape sequences
// attached to adjacent non-whitespace characters.
func splitWordsAnsi(s string) []string {
	var words []string
	var cur strings.Builder
	inEsc := false

	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '\033' {
			inEsc = true
			cur.WriteByte(ch)
			continue
		}
		if inEsc {
			cur.WriteByte(ch)
			if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') {
				inEsc = false
			}
			continue
		}
		if ch == ' ' || ch == '\t' {
			if cur.Len() > 0 {
				words = append(words, cur.String())
				cur.Reset()
			}
		} else {
			cur.WriteByte(ch)
		}
	}
	if cur.Len() > 0 {
		words = append(words, cur.String())
	}
	return words
}

// isTableLine returns true if the trimmed line looks like a markdown table row.
func isTableLine(trimmed string) bool {
	return strings.HasPrefix(trimmed, "|") && strings.Count(trimmed, "|") >= 2
}

// parseTableRow splits a markdown table row "|a|b|c|" into trimmed cells.
func parseTableRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	cells := make([]string, len(parts))
	for i, p := range parts {
		cells[i] = strings.TrimSpace(p)
	}
	return cells
}

// isTableSeparator returns true if cells form a separator row (e.g. |---|:---:|).
func isTableSeparator(cells []string) bool {
	for _, c := range cells {
		if !strings.Contains(c, "-") {
			return false
		}
		if strings.Trim(c, ":- ") != "" {
			return false
		}
	}
	return len(cells) > 0
}

type tableRow struct {
	cells    []string
	isHeader bool
}

// renderTable renders a markdown table block as formatted terminal output.
func renderTable(tableLines []string, width int) string {
	// First pass: check if a separator row exists.
	hasSep := false
	for _, line := range tableLines {
		if isTableSeparator(parseTableRow(line)) {
			hasSep = true
			break
		}
	}

	// Second pass: collect rows, marking header rows.
	var rows []tableRow
	seenSep := false
	for _, line := range tableLines {
		cells := parseTableRow(line)
		if isTableSeparator(cells) {
			seenSep = true
			continue
		}
		rows = append(rows, tableRow{cells: cells, isHeader: hasSep && !seenSep})
	}
	if len(rows) == 0 {
		return ""
	}

	colMeta := analyzeTableColumns(rows)

	// Normalise column count.
	numCols := 0
	for _, r := range rows {
		if len(r.cells) > numCols {
			numCols = len(r.cells)
		}
	}

	// Render cells and measure column widths.
	type rCell struct {
		text string
		w    int
	}
	rendered := make([][]rCell, len(rows))
	colW := make([]int, numCols)

	for i, r := range rows {
		rendered[i] = make([]rCell, numCols)
		for j := 0; j < numCols; j++ {
			var raw string
			if j < len(r.cells) {
				raw = r.cells[j]
			}
			var styled string
			if r.isHeader {
				styled = mdTableHeaderStyle.Render(raw)
			} else {
				styled = renderInline(raw)
				if colMeta[j].isNumeric {
					if value, ok := parseNumericCell(raw); ok {
						bar := renderValueBar(value, colMeta[j].maxAbs)
						if bar != "" {
							styled += " " + bar
						}
					}
				}
			}
			w := ansi.StringWidth(styled)
			rendered[i][j] = rCell{text: styled, w: w}
			if w > colW[j] {
				colW[j] = w
			}
		}
	}

	// Minimum column width.
	for j := range colW {
		if colW[j] < 3 {
			colW[j] = 3
		}
	}

	// Shrink widest columns if table exceeds available width.
	const tblIndent = 2
	const sepW = 3 // " │ "
	totalW := tblIndent
	for _, cw := range colW {
		totalW += cw
	}
	totalW += (numCols - 1) * sepW
	if width > 0 && totalW > width {
		excess := totalW - width
		for excess > 0 {
			maxIdx := 0
			for j := 1; j < numCols; j++ {
				if colW[j] > colW[maxIdx] {
					maxIdx = j
				}
			}
			if colW[maxIdx] <= 3 {
				break
			}
			colW[maxIdx]--
			excess--
		}
	}

	// Build output.
	var out strings.Builder
	colSep := " " + mdTableBorderStyle.Render("│") + " "

	for i, row := range rendered {
		out.WriteString(strings.Repeat(" ", tblIndent))
		for j := 0; j < numCols; j++ {
			c := row[j]
			tw := colW[j]
			text := c.text
			vw := c.w
			if vw > tw {
				text = ansi.Truncate(text, tw, "…")
				vw = ansi.StringWidth(text)
			}
			out.WriteString(text)
			if pad := tw - vw; pad > 0 {
				out.WriteString(strings.Repeat(" ", pad))
			}
			if j < numCols-1 {
				out.WriteString(colSep)
			}
		}
		out.WriteString("\n")

		// Draw separator line after the last header row.
		if rows[i].isHeader && (i+1 >= len(rows) || !rows[i+1].isHeader) {
			out.WriteString(strings.Repeat(" ", tblIndent))
			for j := 0; j < numCols; j++ {
				out.WriteString(mdTableBorderStyle.Render(strings.Repeat("─", colW[j])))
				if j < numCols-1 {
					out.WriteString(mdTableBorderStyle.Render("─┼─"))
				}
			}
			out.WriteString("\n")
		}
	}

	return out.String()
}

type tableColMeta struct {
	isNumeric bool
	maxAbs    float64
}

// analyzeTableColumns detects numeric body columns and captures max abs value per column.
func analyzeTableColumns(rows []tableRow) []tableColMeta {
	numCols := 0
	for _, r := range rows {
		if len(r.cells) > numCols {
			numCols = len(r.cells)
		}
	}
	meta := make([]tableColMeta, numCols)
	if numCols == 0 {
		return meta
	}

	bodyRows := 0
	counts := make([]int, numCols)
	maxAbs := make([]float64, numCols)
	headers := make([]string, numCols)

	for _, r := range rows {
		if r.isHeader {
			for j := 0; j < numCols && j < len(r.cells); j++ {
				if headers[j] == "" {
					headers[j] = strings.ToLower(strings.TrimSpace(r.cells[j]))
				}
			}
			continue
		}
		bodyRows++
		for j := 0; j < numCols && j < len(r.cells); j++ {
			v, ok := parseNumericCell(r.cells[j])
			if !ok {
				continue
			}
			counts[j]++
			absV := math.Abs(v)
			if absV > maxAbs[j] {
				maxAbs[j] = absV
			}
		}
	}

	if bodyRows == 0 {
		return meta
	}

	for j := 0; j < numCols; j++ {
		// Require mostly-numeric values in a column, and skip date-like columns.
		if counts[j]*10 < bodyRows*6 {
			continue
		}
		h := headers[j]
		if strings.Contains(h, "date") || strings.Contains(h, "quarter") || strings.Contains(h, "period") || strings.Contains(h, "year") {
			continue
		}
		meta[j] = tableColMeta{isNumeric: true, maxAbs: maxAbs[j]}
	}

	return meta
}

// parseNumericCell parses a numeric value from a markdown table cell.
func parseNumericCell(cell string) (float64, bool) {
	s := strings.TrimSpace(cell)
	if s == "" {
		return 0, false
	}
	lower := strings.ToLower(s)
	if lower == "n/a" || lower == "na" || lower == "--" || lower == "-" {
		return 0, false
	}

	negParen := strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")")
	if negParen {
		s = strings.TrimPrefix(strings.TrimSuffix(s, ")"), "(")
	}

	// Keep common unit suffixes and strip punctuation/symbols around values.
	unit := 1.0
	if len(s) > 0 {
		sfx := s[len(s)-1]
		switch sfx {
		case 'k', 'K':
			unit = 1e3
			s = s[:len(s)-1]
		case 'm', 'M':
			unit = 1e6
			s = s[:len(s)-1]
		case 'b', 'B':
			unit = 1e9
			s = s[:len(s)-1]
		case 't', 'T':
			unit = 1e12
			s = s[:len(s)-1]
		}
	}

	match := reNumeric.FindString(s)
	if match == "" {
		return 0, false
	}
	num := strings.ReplaceAll(match, ",", "")
	v, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, false
	}
	v *= unit
	if negParen {
		v = -v
	}
	return v, true
}

// renderValueBar returns a compact fixed-width spark bar for table cells.
func renderValueBar(value, maxAbs float64) string {
	if maxAbs <= 0 {
		return ""
	}
	const barWidth = 6
	ratio := math.Abs(value) / maxAbs
	filled := int(math.Round(ratio * barWidth))
	if filled == 0 && value != 0 {
		filled = 1
	}
	if filled > barWidth {
		filled = barWidth
	}
	if filled < 0 {
		filled = 0
	}
	full := strings.Repeat("█", filled)
	empty := strings.Repeat("░", barWidth-filled)
	if value < 0 {
		return "[" + mdTableBarDownStyle.Render(full) + mdTableBarTrackStyle.Render(empty) + "]"
	}
	return "[" + mdTableBarUpStyle.Render(full) + mdTableBarTrackStyle.Render(empty) + "]"
}

// rebuildMarkdownStyles updates markdown styles after a theme change.
func rebuildMarkdownStyles(p Palette) {
	mdHeadingStyle = lipgloss.NewStyle().Bold(true).Foreground(p.Yellow)
	mdBoldStyle = lipgloss.NewStyle().Bold(true).Foreground(p.White)
	mdItalicStyle = lipgloss.NewStyle().Italic(true).Foreground(p.Gray)
	mdCodeStyle = lipgloss.NewStyle().Foreground(p.Blue)
	mdBulletStyle = lipgloss.NewStyle().Foreground(p.Yellow)
	mdLabelStyle = lipgloss.NewStyle().Bold(true).Foreground(p.Green)
	mdHighlightStyle = lipgloss.NewStyle().Bold(true).Foreground(p.Red)
	mdTableHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(p.Blue)
	mdTableBorderStyle = lipgloss.NewStyle().Foreground(p.Dim)
	mdTableBarUpStyle = lipgloss.NewStyle().Foreground(p.Green)
	mdTableBarDownStyle = lipgloss.NewStyle().Foreground(p.Red)
	mdTableBarTrackStyle = lipgloss.NewStyle().Foreground(p.Dim)
}
