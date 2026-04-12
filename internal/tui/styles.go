package tui

import "github.com/charmbracelet/lipgloss"

// ─── Colors (Cortex Neural palette) ─────────────────────────────────────────

var (
	_            = lipgloss.Color("#16161e") // Deep navy base (reserved for future use)
	_            = lipgloss.Color("#1a1b2e") // Slightly lighter panel bg (reserved for future use)
	colorOverlay = lipgloss.Color("#565f89") // Muted blue-gray borders
	colorText     = lipgloss.Color("#c0caf5") // Soft blue-white text
	colorSubtext  = lipgloss.Color("#565f89") // Dim blue-gray
	colorCyan     = lipgloss.Color("#2ac3de") // Electric cyan — primary accent
	colorBlue     = lipgloss.Color("#7aa2f7") // Bright blue — secondary
	colorPurple   = lipgloss.Color("#bb9af7") // Soft purple — brain/cortex
	colorGreen    = lipgloss.Color("#9ece6a") // Success green
	colorAmber    = lipgloss.Color("#e0af68") // Warning amber
	colorRed      = lipgloss.Color("#f7768e") // Danger red
	colorMauve    = lipgloss.Color("#c0a0f0") // Soft purple-pink
	colorTeal     = lipgloss.Color("#73daca") // Teal accent
	colorGold     = lipgloss.Color("#e0af68") // Gold for scores
)

// ─── Layout Styles ───────────────────────────────────────────────────────────

var (
	appStyle = lipgloss.NewStyle().
			Foreground(colorText).
			Padding(1, 2)

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorCyan).
			BorderStyle(lipgloss.NormalBorder()).
			BorderBottom(true).
			BorderForeground(colorOverlay).
			PaddingBottom(1).
			MarginBottom(1)

	helpStyle = lipgloss.NewStyle().
			Foreground(colorSubtext).
			MarginTop(1)

	errorStyle = lipgloss.NewStyle().
			Foreground(colorRed).
			Bold(true).
			Padding(0, 1)

	updateBannerStyle = lipgloss.NewStyle().
				Foreground(colorAmber).
				Bold(true).
				Padding(0, 1)
)

// ─── Dashboard Styles ────────────────────────────────────────────────────────

var (
	statNumberStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorTeal).
			Width(8).
			Align(lipgloss.Right)

	statLabelStyle = lipgloss.NewStyle().
			Foreground(colorText).
			PaddingLeft(2)

	statCardStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(colorOverlay).
			Padding(1, 2).
			MarginBottom(1)

	menuItemStyle = lipgloss.NewStyle().
			Foreground(colorText).
			PaddingLeft(2)

	menuSelectedStyle = lipgloss.NewStyle().
				Background(colorCyan).
				Foreground(lipgloss.Color("#16161e")).
				Bold(true).
				PaddingLeft(1)

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPurple).
			MarginBottom(1)
)

// ─── List Styles ─────────────────────────────────────────────────────────────

var (
	listItemStyle = lipgloss.NewStyle().
			Foreground(colorText).
			PaddingLeft(2)

	listSelectedStyle = lipgloss.NewStyle().
				Background(colorCyan).
				Foreground(lipgloss.Color("#16161e")).
				Bold(true).
				PaddingLeft(1)

	typeBadgeStyle = lipgloss.NewStyle().
			Foreground(colorAmber).
			Bold(true)

	idStyle = lipgloss.NewStyle().
		Foreground(colorBlue)

	timestampStyle = lipgloss.NewStyle().
			Foreground(colorSubtext).
			Italic(true)

	projectStyle = lipgloss.NewStyle().
			Foreground(colorGold)

	contentPreviewStyle = lipgloss.NewStyle().
				Foreground(colorSubtext).
				PaddingLeft(4)
)

// ─── Detail View Styles ──────────────────────────────────────────────────────

var (
	sectionHeadingStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorPurple).
				MarginTop(1).
				MarginBottom(1)

	detailContentStyle = lipgloss.NewStyle().
				Foreground(colorText).
				PaddingLeft(2)

	detailLabelStyle = lipgloss.NewStyle().
				Foreground(colorSubtext).
				Width(14).
				Align(lipgloss.Right).
				PaddingRight(1)

)

// ─── Timeline Styles ─────────────────────────────────────────────────────────

var (
	timelineFocusStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.NormalBorder()).
				BorderForeground(colorCyan).
				Padding(0, 1)

	timelineItemStyle = lipgloss.NewStyle().
				Foreground(colorSubtext).
				PaddingLeft(2)

	timelineConnectorStyle = lipgloss.NewStyle().
				Foreground(colorOverlay)
)

// ─── Search Styles ───────────────────────────────────────────────────────────

var (
	searchInputStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.NormalBorder()).
				BorderForeground(colorCyan).
				Foreground(colorText).
				Padding(0, 1).
				MarginBottom(1)

	noResultsStyle = lipgloss.NewStyle().
			Foreground(colorSubtext).
			Italic(true).
			PaddingLeft(2).
			MarginTop(1)
)

// ─── Cortex-Exclusive Styles ────────────────────────────────────────────────

var (
	// Graph edge relationship badges
	graphEdgeStyle = lipgloss.NewStyle().
			Foreground(colorMauve).
			Bold(true)

)

// ─── Status Bar & Toast Styles ──────────────────────────────────────────────

var (
	statusBarStyle = lipgloss.NewStyle().
			Foreground(colorText).
			Background(lipgloss.Color("#1a1b2e")).
			Padding(0, 1)

)

// typeColor returns a lipgloss.Color for observation types.
func typeColor(obsType string) lipgloss.Color {
	switch obsType {
	case "bugfix":
		return colorRed
	case "decision":
		return colorCyan
	case "architecture":
		return colorPurple
	case "discovery":
		return colorTeal
	case "pattern":
		return colorBlue
	case "config":
		return colorAmber
	case "preference":
		return colorGold
	default:
		return colorSubtext
	}
}

