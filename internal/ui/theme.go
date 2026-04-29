package ui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/ray-x/finsight/internal/chart"
)

// Palette defines all the semantic colors used throughout the UI.
type Palette struct {
	Green   lipgloss.Color
	Red     lipgloss.Color
	Yellow  lipgloss.Color
	Blue    lipgloss.Color
	Gray    lipgloss.Color
	White   lipgloss.Color
	Dim     lipgloss.Color
	Bg      lipgloss.Color
	CardBg  lipgloss.Color
	Accent  lipgloss.Color // highlight / selection background
	Accent2 lipgloss.Color // secondary accent (compare mode, etc.)
}

// GreenRGB returns the ANSI escape for the green color (used for sparklines).
func (p Palette) GreenRGB() string {
	return lipgloss.NewStyle().Foreground(p.Green).Render("")[0:0] +
		"\033[" + colorToSGR(p.Green) + "m"
}

// RedRGB returns the ANSI escape for the red color.
func (p Palette) RedRGB() string {
	return "\033[" + colorToSGR(p.Red) + "m"
}

func colorToSGR(c lipgloss.Color) string {
	// For ANSI 16 colors use standard codes
	switch string(c) {
	// Standard ANSI colors
	case "1":
		return "31"
	case "2":
		return "32"
	case "3":
		return "33"
	case "4":
		return "34"
	case "5":
		return "35"
	case "6":
		return "36"
	case "7":
		return "37"
	case "9":
		return "31;1"
	case "10":
		return "32;1"
	case "11":
		return "33;1"
	case "12":
		return "34;1"
	case "13":
		return "35;1"
	case "14":
		return "36;1"
	}
	// For hex colors, parse and use 24-bit SGR
	hex := string(c)
	if len(hex) == 7 && hex[0] == '#' {
		r := hexByte(hex[1], hex[2])
		g := hexByte(hex[3], hex[4])
		b := hexByte(hex[5], hex[6])
		return "38;2;" + itoa(int(r)) + ";" + itoa(int(g)) + ";" + itoa(int(b))
	}
	return "37" // fallback white
}

func hexByte(hi, lo byte) byte {
	return hexNibble(hi)<<4 | hexNibble(lo)
}

func hexNibble(b byte) byte {
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

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [3]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// ── Theme definitions ──

var themes = map[string]Palette{
	"default": { // TokyoNight-inspired (original)
		Green:   "#00ff87",
		Red:     "#ff5f87",
		Yellow:  "#ffff00",
		Blue:    "#5fafff",
		Gray:    "#6c6c6c",
		White:   "#ffffff",
		Dim:     "#4e4e4e",
		Bg:      "#1a1b26",
		CardBg:  "#24283b",
		Accent:  "#3d59a1",
		Accent2: "#ff79c6",
	},
	"tokyonight": {
		Green:   "#9ece6a",
		Red:     "#f7768e",
		Yellow:  "#e0af68",
		Blue:    "#7aa2f7",
		Gray:    "#565f89",
		White:   "#c0caf5",
		Dim:     "#3b4261",
		Bg:      "#1a1b26",
		CardBg:  "#24283b",
		Accent:  "#3d59a1",
		Accent2: "#bb9af7",
	},
	"catppuccin": { // Mocha
		Green:   "#a6e3a1",
		Red:     "#f38ba8",
		Yellow:  "#f9e2af",
		Blue:    "#89b4fa",
		Gray:    "#6c7086",
		White:   "#cdd6f4",
		Dim:     "#45475a",
		Bg:      "#1e1e2e",
		CardBg:  "#313244",
		Accent:  "#89b4fa",
		Accent2: "#f5c2e7",
	},
	"dracula": {
		Green:   "#50fa7b",
		Red:     "#ff5555",
		Yellow:  "#f1fa8c",
		Blue:    "#8be9fd",
		Gray:    "#6272a4",
		White:   "#f8f8f2",
		Dim:     "#44475a",
		Bg:      "#282a36",
		CardBg:  "#44475a",
		Accent:  "#bd93f9",
		Accent2: "#ff79c6",
	},
	"nord": {
		Green:   "#a3be8c",
		Red:     "#bf616a",
		Yellow:  "#ebcb8b",
		Blue:    "#81a1c1",
		Gray:    "#4c566a",
		White:   "#eceff4",
		Dim:     "#434c5e",
		Bg:      "#2e3440",
		CardBg:  "#3b4252",
		Accent:  "#5e81ac",
		Accent2: "#b48ead",
	},
	"gruvbox": {
		Green:   "#b8bb26",
		Red:     "#fb4934",
		Yellow:  "#fabd2f",
		Blue:    "#83a598",
		Gray:    "#928374",
		White:   "#ebdbb2",
		Dim:     "#504945",
		Bg:      "#282828",
		CardBg:  "#3c3836",
		Accent:  "#458588",
		Accent2: "#d3869b",
	},
	"solarized": { // Solarized Dark
		Green:   "#859900",
		Red:     "#dc322f",
		Yellow:  "#b58900",
		Blue:    "#268bd2",
		Gray:    "#586e75",
		White:   "#eee8d5",
		Dim:     "#073642",
		Bg:      "#002b36",
		CardBg:  "#073642",
		Accent:  "#268bd2",
		Accent2: "#d33682",
	},
	"ansi": { // 16-color ANSI palette — works in any terminal
		Green:   "2",
		Red:     "1",
		Yellow:  "3",
		Blue:    "4",
		Gray:    "7",
		White:   "15",
		Dim:     "8",
		Bg:      "0",
		CardBg:  "0",
		Accent:  "4",
		Accent2: "5",
	},
}

// ActiveTheme is the palette currently in use. It defaults to "default" and is
// set during initialisation via ApplyTheme.
var ActiveTheme = themes["default"]

// ApplyTheme selects a theme by name and rebuilds all lipgloss styles.
func ApplyTheme(name string) {
	p, ok := themes[name]
	if !ok {
		p = themes["default"]
	}
	ActiveTheme = p

	// Rebuild colour aliases used by styles.go
	colorGreen = p.Green
	colorRed = p.Red
	colorYellow = p.Yellow
	colorBlue = p.Blue
	colorGray = p.Gray
	colorWhite = p.White
	colorDim = p.Dim
	colorBg = p.Bg
	colorCardBg = p.CardBg

	// Rebuild styles
	titleStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(p.White).
		Background(p.Accent).
		Padding(0, 2)

	headerStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(p.Blue).
		BorderStyle(lipgloss.NormalBorder()).
		BorderBottom(true).
		BorderForeground(p.Dim)

	cardStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(p.Dim).
		Padding(0, 1)

	selectedCardStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(p.Blue).
		Padding(0, 1)

	priceStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(p.White)

	changeUpStyle = lipgloss.NewStyle().
		Foreground(p.Green)

	changeDownStyle = lipgloss.NewStyle().
		Foreground(p.Red)

	symbolStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(p.White)

	nameStyle = lipgloss.NewStyle().
		Foreground(p.Gray)

	statusStyle = lipgloss.NewStyle().
		Foreground(p.Gray).
		Padding(0, 1)

	searchBoxStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(p.Yellow).
		Padding(0, 1)

	searchResultStyle = lipgloss.NewStyle().
		Padding(0, 2)

	selectedResultStyle = lipgloss.NewStyle().
		Background(p.Accent).
		Foreground(p.White).
		Padding(0, 2)

	helpStyle = lipgloss.NewStyle().
		Foreground(p.Gray)

	helpKeyStyle = lipgloss.NewStyle().
		Foreground(p.Yellow).
		Bold(true)

	chartBorderStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(p.Dim).
		Padding(0, 1)

	marketOpenStyle = lipgloss.NewStyle().
		Foreground(p.Green).
		Bold(true)

	marketClosedStyle = lipgloss.NewStyle().
		Foreground(p.Red)

	errorStyle = lipgloss.NewStyle().
		Foreground(p.Red).
		Bold(true)

	separatorStyle = lipgloss.NewStyle().
		Foreground(p.Dim)

	// Rebuild watchlist table styles
	tableHeaderStyle = lipgloss.NewStyle().
		Foreground(p.Gray).
		Bold(true).
		PaddingLeft(1)

	rowStyle = lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderBottom(true).
		BorderTop(false).
		BorderLeft(false).
		BorderRight(false).
		BorderForeground(p.Dim)

	selectedRowStyle = lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderBottom(true).
		BorderTop(false).
		BorderLeft(false).
		BorderRight(false).
		BorderForeground(p.Blue).
		Background(p.CardBg)

	// Update chart ANSI colors
	chart.GreenFg = p.GreenRGB()
	chart.RedFg = p.RedRGB()

	// Update markdown rendering styles
	rebuildMarkdownStyles(p)
}

// ThemeNames returns the sorted list of available theme names.
func ThemeNames() []string {
	return []string{
		"ansi",
		"catppuccin",
		"default",
		"dracula",
		"gruvbox",
		"nord",
		"solarized",
		"tokyonight",
	}
}
