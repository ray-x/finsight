package ui

import "github.com/charmbracelet/lipgloss"

var (
	// Colors
	colorGreen  = lipgloss.Color("#00ff87")
	colorRed    = lipgloss.Color("#ff5f87")
	colorYellow = lipgloss.Color("#ffff00")
	colorBlue   = lipgloss.Color("#5fafff")
	colorGray   = lipgloss.Color("#6c6c6c")
	colorWhite  = lipgloss.Color("#ffffff")
	colorDim    = lipgloss.Color("#4e4e4e")
	colorBg     = lipgloss.Color("#1a1b26")
	colorCardBg = lipgloss.Color("#24283b")

	// Base styles
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorWhite).
			Background(lipgloss.Color("#3d59a1")).
			Padding(0, 2)

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorBlue).
			BorderStyle(lipgloss.NormalBorder()).
			BorderBottom(true).
			BorderForeground(colorDim)

	// Card styles for watchlist items
	cardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorDim).
			Padding(0, 1)

	selectedCardStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorBlue).
				Padding(0, 1)

	// Price styles
	priceStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorWhite)

	changeUpStyle = lipgloss.NewStyle().
			Foreground(colorGreen)

	changeDownStyle = lipgloss.NewStyle().
			Foreground(colorRed)

	// Symbol styles
	symbolStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorWhite)

	nameStyle = lipgloss.NewStyle().
			Foreground(colorGray)

	// Status bar
	statusStyle = lipgloss.NewStyle().
			Foreground(colorGray).
			Padding(0, 1)

	// Search styles
	searchBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorYellow).
			Padding(0, 1)

	searchResultStyle = lipgloss.NewStyle().
				Padding(0, 2)

	selectedResultStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("#3d59a1")).
				Foreground(colorWhite).
				Padding(0, 2)

	// Help bar
	helpStyle = lipgloss.NewStyle().
			Foreground(colorGray)

	helpKeyStyle = lipgloss.NewStyle().
			Foreground(colorYellow).
			Bold(true)

	// Detail chart view
	chartBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorDim).
				Padding(0, 1)

	// Market state
	marketOpenStyle = lipgloss.NewStyle().
			Foreground(colorGreen).
			Bold(true)

	marketClosedStyle = lipgloss.NewStyle().
				Foreground(colorRed)

	// Error style
	errorStyle = lipgloss.NewStyle().
			Foreground(colorRed).
			Bold(true)

	// Separator
	separatorStyle = lipgloss.NewStyle().
			Foreground(colorDim)
)

func changeStyle(positive bool) lipgloss.Style {
	if positive {
		return changeUpStyle
	}
	return changeDownStyle
}

func changeArrow(positive bool) string {
	if positive {
		return "↑"
	}
	return "↓"
}

func changeColor(positive bool) string {
	if positive {
		return ActiveTheme.GreenRGB()
	}
	return ActiveTheme.RedRGB()
}
