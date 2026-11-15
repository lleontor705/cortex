package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
)

// ─── Command Palette ──────────────────────────────────────────────────────

func (m Model) viewCmdPalette() string {
	var b strings.Builder

	filtered := m.filteredCommands()

	b.WriteString("  " + m.CmdPaletteInput.View())
	b.WriteString("\n\n")

	for i, cmd := range filtered {
		cursor := "  "
		style := lipgloss.NewStyle().Foreground(colorText)
		if i == m.CmdPaletteCursor {
			cursor = "\u25b8 "
			style = lipgloss.NewStyle().Foreground(lipgloss.Color("#16161e")).Background(colorCyan).Bold(true)
		}

		shortcut := ""
		if cmd.shortcut != "" {
			shortcut = lipgloss.NewStyle().Foreground(colorSubtext).Render("  " + cmd.shortcut)
		}

		b.WriteString(cursor + style.Render(cmd.name) + shortcut + "\n")
	}

	modalContent := b.String()
	modal := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorCyan).
		Padding(1, 2).
		Width(50).
		Render(modalContent)

	return lipgloss.Place(m.Width-4, m.Height-2,
		lipgloss.Center, lipgloss.Center,
		modal)
}

// ─── Quick Create Observation Modal ────────────────────────────────────────

func (m Model) renderNewObsModal() string {
	var panel strings.Builder
	titleStyle := lipgloss.NewStyle().Foreground(colorCyan).Bold(true)
	labelStyle := lipgloss.NewStyle().Foreground(colorPurple).Width(12)
	dim := lipgloss.NewStyle().Foreground(colorSubtext)

	panel.WriteString(titleStyle.Render("✦ Quick Create Memory") + "\n")
	panel.WriteString(dim.Render("Create and persist a new observation directly into Cortex memory.") + "\n\n")

	f := func(field int, name string, input textinput.Model) {
		cursor := "  "
		if m.NewObsFocusField == field {
			cursor = listSelectedStyle.Render("▸ ")
		}
		panel.WriteString(cursor + labelStyle.Render(name) + input.View() + "\n")
	}

	f(0, "Title:", m.NewObsTitleInput)
	f(1, "Content:", m.NewObsContentInput)
	f(2, "Type:", m.NewObsTypeInput)
	f(3, "Project:", m.NewObsProjectInput)

	panel.WriteString("\n  ")
	btnStyle := lipgloss.NewStyle().Padding(0, 2)
	if m.NewObsFocusField == 4 {
		btnStyle = btnStyle.Background(colorCyan).Foreground(lipgloss.Color("#16161e")).Bold(true)
	} else {
		btnStyle = btnStyle.Border(lipgloss.NormalBorder()).BorderForeground(colorOverlay)
	}
	panel.WriteString(btnStyle.Render("Save Observation [Enter]"))
	panel.WriteString("   " + dim.Render("[Tab] Next Field  •  [Esc] Cancel  •  [Ctrl+S] Save"))

	modal := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorCyan).
		Padding(1, 2).
		Width(68).
		Render(panel.String())

	width := m.Width - 4
	if width < 20 {
		width = 72
	}
	height := m.Height - 2
	if height < 10 {
		height = 20
	}

	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
}

// ─── Keyboard Reference Help View ──────────────────────────────────────────

// helpOriginScreen is the screen whose bindings the generated Help lists. The
// help opener pushes ScreenHelp onto the live stack, so the origin is the level
// directly below it; Help reached without that push assumes the Dashboard floor.
func (m Model) helpOriginScreen() Screen {
	return m.NavParent()
}

// helpEntries returns exactly the rows the Help screen shows: the canonical
// global keys followed by the origin screen's own bindings. Both come from the
// registry that dispatches keys, so Help can never advertise a key that has no
// handler (REQ-NAV-004).
func (m Model) helpEntries() []HelpEntry {
	origin := m.helpOriginScreen()
	all := keymapRegistry.HelpEntries(&m)
	entries := make([]HelpEntry, 0, len(all))
	for _, entry := range all {
		if entry.Global || entry.Screen == origin {
			entries = append(entries, entry)
		}
	}
	return entries
}

// viewHelp renders the Keyboard Reference straight from the keymap registry
// (design.md W17/W18): the global keys, the origin screen's bindings, and the
// command palette's entries. Hand-written rows let the reference drift from the
// dispatch path, so nothing here is authored by hand.
func (m Model) viewHelp() string {
	var b strings.Builder

	origin := m.helpOriginScreen()
	globals, screenEntries := splitHelpEntries(m.helpEntries())

	b.WriteString(headerStyle.Render("  Cortex TUI — Keyboard Reference"))
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  Generated from the live keymap registry — every key here is dispatched."))
	b.WriteString("\n\n")

	writeHelpSection(&b, "Global", globals)
	writeHelpSection(&b, (Model{Screen: origin}).screenName(), screenEntries)

	b.WriteString(sectionHeadingStyle.Render("  Command palette (ctrl+k)"))
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("  Reachable from every screen without a focused text input."))
	b.WriteString("\n")
	commandStyle := lipgloss.NewStyle().Foreground(colorText)
	for _, command := range allCommands() {
		label := command.name
		if command.shortcut != "" {
			label += "  (" + command.shortcut + ")"
		}
		fmt.Fprintf(&b, "  %s\n", commandStyle.Render(label))
	}
	b.WriteString("\n")

	b.WriteString(m.helpFooter())

	return b.String()
}

func splitHelpEntries(entries []HelpEntry) (globals, screen []HelpEntry) {
	for _, entry := range entries {
		if entry.Global {
			globals = append(globals, entry)
			continue
		}
		screen = append(screen, entry)
	}
	return globals, screen
}

func writeHelpSection(b *strings.Builder, title string, rows []HelpEntry) {
	b.WriteString(sectionHeadingStyle.Render("  " + title))
	b.WriteString("\n")
	writeHelpRows(b, rows)
	b.WriteString("\n")
}

// writeHelpRows renders one binding per line, folding single-letter case pairs
// (T/t) into one row so a shared action is stated once. The alias note records
// that the lowercase form is accepted wherever the paired uppercase key is
// shown, which the handlers already honor by matching both cases.
func writeHelpRows(b *strings.Builder, rows []HelpEntry) {
	keyStyle := lipgloss.NewStyle().Foreground(colorCyan).Bold(true).Width(16)
	descStyle := lipgloss.NewStyle().Foreground(colorText)
	folded, aliased := foldCaseAliases(rows)
	for _, entry := range folded {
		description := entry.Description
		if description == "" {
			description = "—"
		}
		fmt.Fprintf(b, "  %s %s\n", keyStyle.Render(entry.Key), descStyle.Render(description))
	}
	if aliased {
		b.WriteString(helpStyle.Render("  Lowercase aliases are accepted wherever a paired uppercase key is shown."))
		b.WriteString("\n")
	}
}

// foldCaseAliases merges single-letter bindings that share a description and
// differ only in case, labelling the pair "T/t". It reports whether any pair was
// folded so the caller can surface the alias note.
func foldCaseAliases(entries []HelpEntry) ([]HelpEntry, bool) {
	byKey := make(map[string]int, len(entries))
	for i, entry := range entries {
		byKey[entry.Key] = i
	}
	skip := make(map[int]bool, len(entries))
	folded := make([]HelpEntry, 0, len(entries))
	aliased := false
	for i, entry := range entries {
		if skip[i] || len([]rune(entry.Key)) != 1 {
			folded = append(folded, entry)
			continue
		}
		upper := strings.ToUpper(entry.Key)
		j, ok := byKey[upper]
		if upper == entry.Key || !ok || skip[j] || entries[j].Description != entry.Description {
			folded = append(folded, entry)
			continue
		}
		skip[j] = true
		folded = append(folded, HelpEntry{
			Key:         upper + "/" + entry.Key,
			Description: entry.Description,
			Screen:      entry.Screen,
			Global:      entry.Global,
		})
		aliased = true
	}
	return folded, aliased
}

// helpFooter renders the Help screen's own footer from its declared registry
// bindings, so the displayed keys are exactly the dispatchable ones (its globals
// are already listed in the body). Screen bindings win over the identically
// keyed global so the footer states the on-screen behaviour.
func (m Model) helpFooter() string {
	declared := make(map[string]bool)
	for _, binding := range helpKeymap().Bindings {
		declared[binding.Key] = true
	}
	order := make([]string, 0, len(declared))
	byKey := make(map[string]string)
	for _, hint := range keymapRegistry.FooterHints(ScreenHelp, &m) {
		key := hintKey(hint)
		if !declared[key] {
			continue
		}
		if _, seen := byKey[key]; !seen {
			order = append(order, key)
		}
		byKey[key] = hint
	}
	parts := make([]string, 0, len(order))
	for _, key := range order {
		parts = append(parts, footerPair(byKey[key]))
	}
	return helpStyle.Render("  " + strings.Join(parts, " • "))
}

// hintKey extracts the key from a "[key] description" registry hint.
func hintKey(hint string) string {
	trimmed := strings.TrimPrefix(hint, "[")
	if end := strings.Index(trimmed, "]"); end >= 0 {
		return trimmed[:end]
	}
	return trimmed
}
