package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lleontor705/cortex/v2/internal/domain"
)

// ─── Graph (Cortex-exclusive) ───────────────────────────────────────────────

// graphRerootKey re-roots the graph on the focused observation (design W12
// "Focus as root"). The r binding keeps the same action so both the enter and r
// hints stay honest, and no graph key opens the detail screen.
func graphRerootKey(m Model, _ string) (tea.Model, tea.Cmd) {
	item, ok := m.GraphListModel.SelectedItem().(graphItem)
	if !ok {
		return m, nil
	}
	m.GraphRootID = item.obs.ID
	m.Cursor = 0
	m.Scroll = 0
	return m, loadGraphRelated(m.deps, item.obs.ID)
}

// graphKeymap declares the Graph screen bindings (design W12). esc/q are not
// declared here: like the other non-list intercepts they resolve through the
// canonical global esc (pop) and q (reset) entries, so the footer never
// duplicates them.
func graphKeymap() Keymap {
	return Keymap{Screen: ScreenGraph, Bindings: []Binding{
		{Key: "enter", Description: "Focus as root", Handler: graphRerootKey},
		{Key: "r", Description: "re-root", Handler: graphRerootKey},
	}}
}

// ─── Archive (Cortex-exclusive) ─────────────────────────────────────────────

func (m Model) handleArchiveKeys(key string) (tea.Model, tea.Cmd) {
	if m.ConfirmDelete {
		switch key {
		case "y", "Y":
			m.ConfirmDelete = false
			return m, deleteObservationCmd(m.deps, m.ConfirmDeleteID)
		case "n", "N", "esc":
			m.ConfirmDelete = false
		}
		return m, nil
	}

	switch key {
	case "enter":
		if item, ok := m.ArchiveList.SelectedItem().(observationItem); ok {
			m.DetailLoading = true
			return m, tea.Batch(m.SetupSpinner.Tick, loadObservationDetail(m.deps, item.obs.ID))
		}
	case "u":
		if item, ok := m.ArchiveList.SelectedItem().(observationItem); ok {
			return m, unarchiveObservationCmd(m.deps, item.obs.ID)
		}
	case "d":
		if item, ok := m.ArchiveList.SelectedItem().(observationItem); ok {
			m.ConfirmDelete = true
			m.ConfirmDeleteID = item.obs.ID
			m.DeleteTargetTitle = item.obs.Title
		}
		return m, nil
	case "f":
		if m.Stats != nil && len(m.Stats.Projects) > 0 {
			projects := append([]string{""}, m.Stats.Projects...)
			currentIdx := 0
			for i, p := range projects {
				if p == m.FilterProject {
					currentIdx = i
					break
				}
			}
			m.FilterProject = projects[(currentIdx+1)%len(projects)]
			return m, loadArchivedObservations(m.deps, m.FilterProject)
		}
	case "esc":
		prev := m.NavPop()
		m.Cursor = 0
		m.Scroll = 0
		return m, m.refreshScreen(prev)
	case "q":
		m.NavReset()
		m.Cursor = 0
		m.Scroll = 0
		return m, loadStats(m.deps)
	}
	return m, nil
}

// ─── Health (Cortex-exclusive) ──────────────────────────────────────────────

// healthReviewStaleKey reviews the stale observations: it opens the highest
// ranked stale entry for inspection (design W13 "Review stale"). The audited
// tab/expand/scroll no-ops are gone; an empty list reports honestly instead of
// silently doing nothing.
func healthReviewStaleKey(m Model, _ string) (tea.Model, tea.Cmd) {
	return m.reviewHealthObservation(m.HealthStale, "No stale observations to review")
}

// healthReviewOrphanedKey reviews the orphaned observations (design W13
// "Review orphaned").
func healthReviewOrphanedKey(m Model, _ string) (tea.Model, tea.Cmd) {
	return m.reviewHealthObservation(m.HealthOrphans, "No orphaned observations to review")
}

func (m Model) reviewHealthObservation(observations []*domain.Observation, emptyMsg string) (tea.Model, tea.Cmd) {
	if len(observations) == 0 {
		m.ToastMessage = emptyMsg
		m.ToastType = "warning"
		return m, nil
	}
	m.PrevCursor = 0
	m.DetailLoading = true
	return m, tea.Batch(m.SetupSpinner.Tick, loadObservationDetail(m.deps, observations[0].ID))
}

// healthKeymap declares the Health screen bindings (design W13). esc/q resolve
// through the canonical globals so the declared footer and the dispatch path
// stay identical.
func healthKeymap() Keymap {
	return Keymap{Screen: ScreenHealth, Bindings: []Binding{
		{Key: "enter", Description: "Review stale", Handler: healthReviewStaleKey},
		{Key: "o", Description: "Review orphaned", Handler: healthReviewOrphanedKey},
	}}
}

// handleGraphKeys and handleHealthKeys are compatibility shims for the leftover
// update.go delegate entries; they resolve through the same declarative keymaps
// above so there is exactly one source of truth per key.
func (m Model) handleGraphKeys(key string) (tea.Model, tea.Cmd) {
	return dispatchDeclared(graphKeymap(), m, key)
}

func (m Model) handleHealthKeys(key string) (tea.Model, tea.Cmd) {
	return dispatchDeclared(healthKeymap(), m, key)
}

// systemFooterHint renders a system screen's footer straight from the registry
// (REQ-NAV-004), so a displayed key can never drift from the handler the
// registry dispatches. The screen is passed explicitly because the view can be
// rendered before the model's Screen field catches up. FooterHints already
// omits bindings whose When predicate is false.
func (m Model) systemFooterHint(screen Screen) string {
	hints := keymapRegistry.FooterHints(screen, &m)
	parts := make([]string, 0, len(hints))
	for _, h := range hints {
		parts = append(parts, footerPair(h))
	}
	return helpStyle.Render("  " + strings.Join(parts, " • "))
}

// footerPair turns a "[key] description" hint into the plain "key description"
// form the W12/W13 footers use.
func footerPair(hint string) string {
	pair := strings.TrimPrefix(hint, "[")
	return strings.Replace(pair, "] ", " ", 1)
}

func init() {
	keymapRegistry.Register(graphKeymap())
	keymapRegistry.Register(healthKeymap())
}
