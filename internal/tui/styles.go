package tui

import "github.com/charmbracelet/lipgloss"

// ─── Color Palette & Theme Engine ──────────────────────────────────────────

type ThemePalette struct {
	BaseBg       lipgloss.Color
	PanelBg      lipgloss.Color
	Overlay      lipgloss.Color
	Text         lipgloss.Color
	Subtext      lipgloss.Color
	Cyan         lipgloss.Color
	Blue         lipgloss.Color
	Purple       lipgloss.Color
	Green        lipgloss.Color
	Amber        lipgloss.Color
	Red          lipgloss.Color
	Mauve        lipgloss.Color
	Teal         lipgloss.Color
	Gold         lipgloss.Color
	HighlightBg  lipgloss.Color
	HighlightTxt lipgloss.Color
}

var (
	darkPalette = ThemePalette{
		BaseBg:       lipgloss.Color("#16161e"),
		PanelBg:      lipgloss.Color("#1a1b2e"),
		Overlay:      lipgloss.Color("#565f89"),
		Text:         lipgloss.Color("#c0caf5"),
		Subtext:      lipgloss.Color("#7aa2f7"),
		Cyan:         lipgloss.Color("#2ac3de"),
		Blue:         lipgloss.Color("#7aa2f7"),
		Purple:       lipgloss.Color("#bb9af7"),
		Green:        lipgloss.Color("#9ece6a"),
		Amber:        lipgloss.Color("#e0af68"),
		Red:          lipgloss.Color("#f7768e"),
		Mauve:        lipgloss.Color("#c0a0f0"),
		Teal:         lipgloss.Color("#73daca"),
		Gold:         lipgloss.Color("#e0af68"),
		HighlightBg:  lipgloss.Color("#2ac3de"),
		HighlightTxt: lipgloss.Color("#16161e"),
	}

	lightPalette = ThemePalette{
		BaseBg:       lipgloss.Color("#f8fafc"),
		PanelBg:      lipgloss.Color("#ffffff"),
		Overlay:      lipgloss.Color("#cbd5e1"),
		Text:         lipgloss.Color("#0f172a"),
		Subtext:      lipgloss.Color("#475569"),
		Cyan:         lipgloss.Color("#0284c7"),
		Blue:         lipgloss.Color("#2563eb"),
		Purple:       lipgloss.Color("#7c3aed"),
		Green:        lipgloss.Color("#16a34a"),
		Amber:        lipgloss.Color("#d97706"),
		Red:          lipgloss.Color("#dc2626"),
		Mauve:        lipgloss.Color("#9333ea"),
		Teal:         lipgloss.Color("#0d9488"),
		Gold:         lipgloss.Color("#b45309"),
		HighlightBg:  lipgloss.Color("#0284c7"),
		HighlightTxt: lipgloss.Color("#ffffff"),
	}

	currentIsDark = true
	activePalette = darkPalette

	// Colors exported / updated dynamically
	colorOverlay = activePalette.Overlay
	colorText    = activePalette.Text
	colorSubtext = activePalette.Subtext
	colorCyan    = activePalette.Cyan
	colorBlue    = activePalette.Blue
	colorPurple  = activePalette.Purple
	colorGreen   = activePalette.Green
	colorAmber   = activePalette.Amber
	colorRed     = activePalette.Red
	colorMauve   = activePalette.Mauve
	colorTeal    = activePalette.Teal
	colorGold    = activePalette.Gold
)

// ApplyTheme switches between Dark and Light mode and rebuilds styles.
func ApplyTheme(dark bool) {
	currentIsDark = dark
	if dark {
		activePalette = darkPalette
	} else {
		activePalette = lightPalette
	}
	colorOverlay = activePalette.Overlay
	colorText = activePalette.Text
	colorSubtext = activePalette.Subtext
	colorCyan = activePalette.Cyan
	colorBlue = activePalette.Blue
	colorPurple = activePalette.Purple
	colorGreen = activePalette.Green
	colorAmber = activePalette.Amber
	colorRed = activePalette.Red
	colorMauve = activePalette.Mauve
	colorTeal = activePalette.Teal
	colorGold = activePalette.Gold

	rebuildStyles()
}

// IsDark reports whether dark mode is currently active.
func IsDark() bool {
	return currentIsDark
}

// ToggleTheme switches between dark and light mode.
func ToggleTheme() bool {
	ApplyTheme(!currentIsDark)
	return currentIsDark
}

// ─── Layout Styles ───────────────────────────────────────────────────────────

var (
	appStyle          lipgloss.Style
	headerStyle       lipgloss.Style
	helpStyle         lipgloss.Style
	errorStyle        lipgloss.Style
	updateBannerStyle lipgloss.Style

	// Dashboard
	statNumberStyle   lipgloss.Style
	statLabelStyle    lipgloss.Style
	statCardStyle     lipgloss.Style
	menuItemStyle     lipgloss.Style
	menuSelectedStyle lipgloss.Style
	titleStyle        lipgloss.Style

	// List
	listItemStyle       lipgloss.Style
	listSelectedStyle   lipgloss.Style
	typeBadgeStyle      lipgloss.Style
	idStyle             lipgloss.Style
	timestampStyle      lipgloss.Style
	projectStyle        lipgloss.Style
	contentPreviewStyle lipgloss.Style

	// Detail
	sectionHeadingStyle lipgloss.Style
	detailContentStyle  lipgloss.Style
	detailLabelStyle    lipgloss.Style

	// Timeline
	timelineFocusStyle     lipgloss.Style
	timelineItemStyle      lipgloss.Style
	timelineConnectorStyle lipgloss.Style

	// Search
	searchInputStyle lipgloss.Style
	noResultsStyle   lipgloss.Style

	// Cortex-Exclusive
	graphEdgeStyle lipgloss.Style
	statusBarStyle lipgloss.Style
)

func init() {
	rebuildStyles()
}

func rebuildStyles() {
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
		Background(activePalette.HighlightBg).
		Foreground(activePalette.HighlightTxt).
		Bold(true).
		PaddingLeft(1)

	titleStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(colorPurple).
		MarginBottom(1)

	listItemStyle = lipgloss.NewStyle().
		Foreground(colorText).
		PaddingLeft(2)

	listSelectedStyle = lipgloss.NewStyle().
		Background(activePalette.HighlightBg).
		Foreground(activePalette.HighlightTxt).
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

	timelineFocusStyle = lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(colorCyan).
		Padding(0, 1)

	timelineItemStyle = lipgloss.NewStyle().
		Foreground(colorSubtext).
		PaddingLeft(2)

	timelineConnectorStyle = lipgloss.NewStyle().
		Foreground(colorOverlay)

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

	graphEdgeStyle = lipgloss.NewStyle().
		Foreground(colorMauve).
		Bold(true)

	statusBarStyle = lipgloss.NewStyle().
		Foreground(colorText).
		Background(activePalette.PanelBg).
		Padding(0, 1)
}

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


