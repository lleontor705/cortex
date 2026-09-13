package tui

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	beaconColors  = []lipgloss.Color{colorGreen, colorTeal, colorCyan, colorBlue}
)

const (
	BrailleLogo = `     ⣠⣶⣿⣿⣿⣿⣶⣤⡀       ⢀⣤⣶⣿⣿⣿⣿⣶⣄
  ⢰⣿⣿⠟⠉   ⠹⣿⣿⣄⣠⣿⣿⠏   ⠈⠻⣿⣿⡆
  ⣾⣿⠃  ⢰⣶⣶⡄ ⠹⣿⣿⣿⣿⠏ ⢰⣶⣶⡄  ⠘⣿⣷
 ⢸⣿⡟   ⠸⣿⣿⣧  ⠹⣿⣿⠏  ⣼⣿⣿⠇   ⢻⣿⡇
  ⠹⣿⣧⡀   ⠉⠛⠿⣿⣿⣿⣿⣿⣿⠿⠛⠉   ⢀⣼⣿⠏
   ⠙⢿⣿⣦⣄      C O R T E X      ⣠⣴⣿⡿⠋
     ⠉⠛⠿⠿⣿⣶⣤⣤⣤⣤⣤⣤⣶⣿⠿⠿⠛⠉`
)

// hexToRGB parses a hex string "#RRGGBB" into RGB integers.
func hexToRGB(hexStr string) (r, g, b int) {
	if strings.HasPrefix(hexStr, "#") && len(hexStr) == 7 {
		_, _ = fmt.Sscanf(hexStr[1:], "%02x%02x%02x", &r, &g, &b)
		return
	}
	return 124, 58, 237 // fallback to purple
}

// ShimmerLogo renders the Braille Logo with a dynamic, wave-interpolated color gradient
// smoothly moving between Primary (Purple) and Secondary (Cyan) across columns based on frame index.
func ShimmerLogo(frame int) string {
	r1, g1, b1 := hexToRGB(string(colorPurple))
	r2, g2, b2 := hexToRGB(string(colorCyan))

	lines := strings.Split(BrailleLogo, "\n")
	renderedLines := make([]string, 0, len(lines))

	for _, line := range lines {
		var sb strings.Builder
		for col, ch := range []rune(line) {
			if ch == ' ' {
				sb.WriteRune(' ')
				continue
			}
			wave := float64(col)*0.16 - float64(frame)*0.22
			t := 0.5 + 0.5*math.Sin(wave)

			r := int(float64(r1)*(1.0-t) + float64(r2)*t)
			g := int(float64(g1)*(1.0-t) + float64(g2)*t)
			b := int(float64(b1)*(1.0-t) + float64(b2)*t)

			hex := fmt.Sprintf("#%02x%02x%02x", r, g, b)
			styled := lipgloss.NewStyle().Foreground(lipgloss.Color(hex)).Bold(true).Render(string(ch))
			sb.WriteString(styled)
		}
		renderedLines = append(renderedLines, sb.String())
	}
	return strings.Join(renderedLines, "\n")
}

// renderPulsingBeacon returns a breathing status beacon.
func renderPulsingBeacon(frame int) string {
	c := beaconColors[frame%len(beaconColors)]
	dot := lipgloss.NewStyle().Foreground(c).Bold(true).Render("●")
	text := lipgloss.NewStyle().Foreground(colorText).Bold(true).Render(" NEURAL LINK ACTIVE")
	return dot + text
}

// renderSpinner returns an animated Braille spinner frame.
func renderSpinner(frame int) string {
	frameChar := spinnerFrames[frame%len(spinnerFrames)]
	return lipgloss.NewStyle().Foreground(colorCyan).Bold(true).Render(frameChar)
}

// renderProgressBar renders a sleek horizontal progress bar.
func renderProgressBar(percent float64, width int) string {
	if percent < 0 {
		percent = 0
	}
	if percent > 1.0 {
		percent = 1.0
	}
	if width < 5 {
		width = 10
	}

	filledLen := int(percent * float64(width))
	if filledLen > width {
		filledLen = width
	}
	emptyLen := width - filledLen

	filled := progressBarFilledStyle.Render(strings.Repeat("■", filledLen))
	empty := progressBarEmptyStyle.Render(strings.Repeat("□", emptyLen))
	pctLabel := fmt.Sprintf(" %3.0f%%", percent*100)

	return filled + empty + lipgloss.NewStyle().Foreground(colorSubtext).Render(pctLabel)
}

// renderLogo renders the official Cortex Shimmer Logo or compact title.
func (m Model) renderLogo() string {
	if m.Width > 0 && (m.Width < 60 || m.Height < 20) {
		brand := lipgloss.NewStyle().Bold(true).Foreground(colorCyan).Render("cortex")
		ver := lipgloss.NewStyle().Foreground(colorSubtext).Render(" " + m.Version)
		tagline := lipgloss.NewStyle().Foreground(colorSubtext).Render(" — configuration center")
		return fmt.Sprintf("  %s%s%s\n", brand, ver, tagline)
	}

	logo := ShimmerLogo(m.AnimFrame)
	tagline := lipgloss.NewStyle().Foreground(colorSubtext).Render("     cortex " + m.Version + " — configuration & persistent memory engine")
	return logo + "\n" + tagline + "\n"
}
