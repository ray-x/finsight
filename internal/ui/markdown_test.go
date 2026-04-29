package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// ANSI helpers to simulate production lipgloss output (tests run without TTY,
// so lipgloss.Render strips codes). These let us test with real escape sequences.
func ansiBold(s string) string   { return "\033[1m" + s + "\033[0m" }
func ansiRed(s string) string    { return "\033[31m" + s + "\033[0m" }
func ansiYellow(s string) string { return "\033[33m" + s + "\033[0m" }
func ansiGreen(s string) string  { return "\033[32m" + s + "\033[0m" }

// assertLineWidths checks every non-empty line is ≤ maxW visible chars.
func assertLineWidths(t *testing.T, text string, maxW int, label string) {
	t.Helper()
	for i, line := range strings.Split(text, "\n") {
		if line == "" {
			continue
		}
		w := ansi.StringWidth(line)
		if w > maxW {
			t.Errorf("%s: line %d exceeds width %d: visible width %d, raw %q", label, i, maxW, w, line)
		}
	}
}

func TestWrapRendered_ShortLine(t *testing.T) {
	s := "  hello world"
	got := wrapRendered("  ", s, 40)
	if got != s {
		t.Errorf("short line should not be wrapped, got %q", got)
	}
}

func TestWrapRendered_LongPlainText(t *testing.T) {
	s := "  Biggest Gainer: QQQ led the group today, rising 0.48%. This confirms continued investor interest in large-cap technology and growth stocks."
	got := wrapRendered("  ", s, 60)
	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected wrapping into multiple lines, got %d line(s): %q", len(lines), got)
	}
	assertLineWidths(t, got, 60, "plain")
}

func TestWrapRendered_ContinuationIndent(t *testing.T) {
	s := "  word1 word2 word3 word4 word5 word6 word7 word8 word9 word10"
	got := wrapRendered("    ", s, 30)
	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected multiple lines, got %d", len(lines))
	}
	for i := 1; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], "    ") {
			t.Errorf("continuation line %d should start with indent, got %q", i, lines[i])
		}
	}
}

func TestWrapRendered_WithANSI_Bold(t *testing.T) {
	// Simulate lipgloss bold output: \033[1mBiggest Gainer:\033[0m (space inside styled text)
	s := "  " + ansiBold("Biggest Gainer:") + " QQQ led the group today, rising " + ansiRed("0.48%") + ". This confirms continued investor interest in large-cap technology and growth stocks."
	got := wrapRendered("  ", s, 60)
	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected wrapping with ANSI bold, got %d line(s)", len(lines))
	}
	assertLineWidths(t, got, 60, "ansi-bold")
}

func TestWrapRendered_WithANSI_MultiStyle(t *testing.T) {
	// Multiple styles: bullet + bold label + highlighted number
	bullet := ansiYellow("•")
	label := ansiBold("Biggest Gainer:")
	highlight := ansiRed("-0.04%")
	s := fmt.Sprintf("  %s %s IKO.AX underperformed, dropping %s. This ETF is significantly trailing its 52-week high, suggesting specific sector headwinds or lack of recent momentum.", bullet, label, highlight)
	got := wrapRendered("    ", s, 80)
	assertLineWidths(t, got, 80, "multi-style")
}

func TestWrapRendered_WithANSI_SpaceInsideStyle(t *testing.T) {
	// The real production case: lipgloss wraps multi-word text in ONE escape sequence
	// e.g. **Biggest Gainer:** → \033[1mBiggest Gainer:\033[0m (space INSIDE escape)
	styled := "\033[1mBiggest Gainer:\033[0m"
	s := "  " + styled + " QQQ led the group today, rising 0.48%. This confirms continued investor interest in large-cap technology."
	got := wrapRendered("  ", s, 50)
	assertLineWidths(t, got, 50, "space-inside-style")
}

func TestWrapRendered_ZeroWidth(t *testing.T) {
	s := "hello world"
	got := wrapRendered("  ", s, 0)
	if got != s {
		t.Errorf("zero width should return input unchanged, got %q", got)
	}
}

func TestSplitWordsAnsi_Plain(t *testing.T) {
	words := splitWordsAnsi("hello world foo")
	if len(words) != 3 || words[0] != "hello" || words[1] != "world" || words[2] != "foo" {
		t.Errorf("unexpected split: %v", words)
	}
}

func TestSplitWordsAnsi_WithEscapes(t *testing.T) {
	s := "\033[1mBold\033[0m text"
	words := splitWordsAnsi(s)
	if len(words) != 2 {
		t.Fatalf("expected 2 words, got %d: %v", len(words), words)
	}
	if words[0] != "\033[1mBold\033[0m" {
		t.Errorf("word 0 should contain ANSI codes, got %q", words[0])
	}
	if words[1] != "text" {
		t.Errorf("word 1 should be plain, got %q", words[1])
	}
}

func TestSplitWordsAnsi_SpaceInsideEscape(t *testing.T) {
	// \033[1mBiggest Gainer:\033[0m — space inside styled text splits into 2 words
	s := "\033[1mBiggest Gainer:\033[0m rest"
	words := splitWordsAnsi(s)
	// The space inside the styled text causes a split: "\033[1mBiggest" and "Gainer:\033[0m"
	if len(words) != 3 {
		t.Fatalf("expected 3 words, got %d: %v", len(words), words)
	}
	// Verify width of each word is correct
	for _, w := range words {
		sw := ansi.StringWidth(w)
		if sw == 0 && len(w) > 0 {
			// Zero-width ANSI-only fragment — this is OK but wrapping must handle it
			t.Logf("zero-width word: %q", w)
		}
	}
}

func TestSplitWordsAnsi_Empty(t *testing.T) {
	words := splitWordsAnsi("")
	if len(words) != 0 {
		t.Errorf("empty string should produce no words, got %v", words)
	}
}

// Tests for renderMarkdown with real ANSI codes injected.

func TestRenderMarkdown_LongBulletWraps(t *testing.T) {
	md := "- Biggest Gainer: QQQ led the group today, rising 0.48%. This confirms continued investor interest in large-cap technology and growth stocks."
	got := renderMarkdown(md, 60)
	assertLineWidths(t, got, 60, "bullet")
}

func TestRenderMarkdown_LongParagraphWraps(t *testing.T) {
	md := "This is a very long paragraph that should be wrapped because it exceeds the specified width of sixty characters in the rendering output."
	got := renderMarkdown(md, 60)
	assertLineWidths(t, got, 60, "paragraph")
}

func TestRenderMarkdown_BoldBulletWithHighlight(t *testing.T) {
	// This is the exact pattern from the failing screenshot
	md := "- **Biggest Gainer:** QQQ led the group today, rising ==0.48%==. This confirms continued investor interest in large-cap technology and growth stocks."
	got := renderMarkdown(md, 80)
	assertLineWidths(t, got, 80, "bold-bullet-highlight")
}

func TestRenderMarkdown_MultipleBullets(t *testing.T) {
	md := `- **Biggest Gainer:** QQQ led the group today, rising ==0.48%==. This confirms continued investor interest in large-cap technology and growth stocks.
- **Biggest Laggard:** IKO.AX underperformed, dropping ==-0.04%==. This ETF is significantly trailing its 52-week high, suggesting specific sector headwinds or lack of recent momentum.`
	got := renderMarkdown(md, 80)
	assertLineWidths(t, got, 80, "multi-bullet")
}

func TestRenderMarkdown_HeadingNotWrapped(t *testing.T) {
	md := "## Short Heading"
	got := renderMarkdown(md, 80)
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 1 {
		t.Errorf("short heading should be one line, got %d", len(lines))
	}
}

func TestRenderMarkdown_NumberedItemWraps(t *testing.T) {
	md := "1. This is a very long numbered item that should be wrapped properly when it exceeds the specified width of the rendering."
	got := renderMarkdown(md, 50)
	assertLineWidths(t, got, 50, "numbered")
}

func TestRenderMarkdown_SafetyTruncation(t *testing.T) {
	// Even if wrapping misses something, the safety truncation catches it.
	// Inject a raw long line with ANSI that might confuse the wrapper.
	long := "  " + ansiGreen("Label:") + " " + strings.Repeat("x", 200)
	// renderMarkdown doesn't see markdown, treats as regular line
	got := renderMarkdown(long, 80)
	assertLineWidths(t, got, 80, "safety-truncation")
}

func TestRenderMarkdown_TableNumericBars(t *testing.T) {
	md := `| Metric | Value | YoY % |
|---|---:|---:|
| Revenue | 60.9B | 26.0% |
| EPS | 6.12 | 39.0% |
| Gross Margin | 78.4% | 1.2% |`

	got := renderMarkdown(md, 100)
	if !strings.Contains(got, "█") {
		t.Fatalf("expected numeric table bars in output, got: %q", got)
	}
	assertLineWidths(t, got, 100, "table-bars")
}

func TestRenderMarkdown_TableSkipsDateColumnBars(t *testing.T) {
	md := `| Quarter | Revenue |
|---|---:|
| 2025-Q3 | 30.1B |
| 2025-Q4 | 32.4B |`

	got := renderMarkdown(md, 100)
	if strings.Contains(got, "2025-Q3 [") || strings.Contains(got, "2025-Q4 [") {
		t.Fatalf("date-like quarter values should not include bars, got: %q", got)
	}
}
