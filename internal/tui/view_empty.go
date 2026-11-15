package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ─── Onboarding empty-state views (design.md W19) ───────────────────────────

// viewOnboardingEmptyState renders the state that replaces the Dashboard menu
// when the store holds no observations. Each state exposes exactly one primary
// action (marked with ▸) and at most one secondary path.
func (m Model) viewOnboardingEmptyState(kind EmptyStateKind) string {
	title := lipgloss.NewStyle().Foreground(colorCyan).Bold(true)
	primary := lipgloss.NewStyle().Foreground(colorCyan).Bold(true)
	secondary := lipgloss.NewStyle().Foreground(colorText)
	dim := lipgloss.NewStyle().Foreground(colorSubtext)

	var lines []string
	switch kind {
	case EmptyStateFirstRun:
		lines = append(lines,
			title.Render("  Welcome to Cortex."),
			dim.Render("  Your memory store is empty. Start with one of:"),
			"",
			primary.Render("  ▸ [n] Create your first memory")+"   "+dim.Render("[enter]"),
			secondary.Render("    [s] Set up an agent plugin"),
			secondary.Render("    [c] Connect to a server"),
		)
	case EmptyStateEmptyDB:
		lines = append(lines,
			title.Render("  No observations yet."),
			"",
			primary.Render("  ▸ [n] Create your first memory")+"   "+dim.Render("[enter]"),
			secondary.Render("    [a] View archive"),
		)
	}
	return strings.Join(lines, "\n")
}

// viewUnconfiguredSyncPrompt renders the dismissible prompt that offers the
// guided Connect flow while no sync endpoint exists (REQ-SYNC-005). It returns
// an empty string whenever the prompt must stay hidden.
func (m Model) viewUnconfiguredSyncPrompt() string {
	if !m.syncPromptVisible() {
		return ""
	}
	title := lipgloss.NewStyle().Foreground(colorAmber).Bold(true)
	action := lipgloss.NewStyle().Foreground(colorCyan).Bold(true)
	dim := lipgloss.NewStyle().Foreground(colorSubtext)
	return strings.Join([]string{
		title.Render("  ⚠ Sync: not configured"),
		action.Render("    [c] Connect to a server") + dim.Render(" — guided, 3 steps, tests before saving"),
		dim.Render("    [x] Skip — stay local-only"),
	}, "\n")
}

// onboardingDashboardKeymap extends the Dashboard keymap with the guarded
// onboarding bindings from empty_state.go. Registering it here — after
// keys_dashboard.go's init in file order — keeps the extension inside this
// task's file lease and leaves the registry's dispatch algorithm untouched
// (REQ-NAV-001/002): screen bindings still precede the canonical globals, and an
// inactive onboarding state simply hides every added binding.
func onboardingDashboardKeymap() Keymap {
	base := dashboardKeymap()
	base.Bindings = append(onboardingBindings(), base.Bindings...)
	return base
}

func init() {
	keymapRegistry.Register(onboardingDashboardKeymap())
}
