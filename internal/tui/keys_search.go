package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// Search, SearchResults, and Recent are migrated to declarative registry
// keymaps (REQ-NAV-001). The key sets live here as data and each action has its
// own focused handler, so no screen-local switch dispatches keys.
//
// The legacy handle*Keys methods remain only because update.go still references
// them; they resolve through the same keymaps below, so there is exactly one
// source of truth for what a key does.
//
// The keymaps are registered at package init, after update.go has built the
// registry, so they replace the placeholder entries there. Registration order is
// irrelevant: Register replaces the keymap for a screen in place.

func init() {
	for _, km := range searchKeymaps() {
		keymapRegistry.Register(km)
	}
}

// searchKeymaps returns the declarative keymaps for the Search, SearchResults,
// and Recent screens.
func searchKeymaps() []Keymap {
	return []Keymap{searchScreenKeymap(), searchResultsScreenKeymap(), recentScreenKeymap()}
}

func searchScreenKeymap() Keymap {
	return Keymap{Screen: ScreenSearch, Bindings: []Binding{
		{Key: "enter", Description: "Search", Handler: searchSubmitKey},
		{Key: "tab", Description: "Switch source", Handler: searchSwitchSourceKey},
		{Key: "i", Description: "Edit query", Handler: searchFocusInputKey},
		{Key: "/", Description: "Edit query", Handler: searchFocusInputKey},
		{Key: "p", Description: "Project filter", Handler: cycleFilterKey},
		{Key: "esc", Description: "Back", Handler: searchCancelKey},
		{Key: "q", Description: "Dashboard", Handler: globalDashboardKey},
	}}
}

func searchResultsScreenKeymap() Keymap {
	return Keymap{Screen: ScreenSearchResults, Bindings: combineBindings(
		confirmDeleteBindings(),
		[]Binding{
			{Key: "enter", Description: "Open detail", Handler: searchResultsOpenKey},
			{Key: "t", Description: "Timeline", Handler: searchResultsTimelineKey},
			{Key: "T", Description: "Timeline", Handler: searchResultsTimelineKey},
			{Key: "v", Description: "Toggle preview", Handler: togglePreviewKey},
			{Key: "V", Description: "Toggle preview", Handler: togglePreviewKey},
			{Key: "p", Description: "Project filter", Handler: cycleFilterKey},
			{Key: "d", Description: "Delete", Handler: searchResultsDeleteKey},
			{Key: "/", Description: "New search", Handler: openSearchFromResultsKey},
			{Key: "s", Description: "New search", Handler: openSearchFromResultsKey},
			{Key: "esc", Description: "Back to search", Handler: searchResultsBackKey},
			{Key: "q", Description: "Dashboard", Handler: globalDashboardKey},
		},
		workspaceBindings(), newObservationBindings(), authBindings(),
	)}
}

func recentScreenKeymap() Keymap {
	return Keymap{Screen: ScreenRecent, Bindings: combineBindings(
		confirmDeleteBindings(),
		[]Binding{
			{Key: "enter", Description: "Open detail", Handler: recentOpenKey},
			{Key: "t", Description: "Timeline", Handler: recentTimelineKey},
			{Key: "T", Description: "Timeline", Handler: recentTimelineKey},
			{Key: "g", Description: "Graph", Handler: recentGraphKey},
			{Key: "G", Description: "Graph", Handler: recentGraphKey},
			{Key: "v", Description: "Toggle preview", Handler: togglePreviewKey},
			{Key: "V", Description: "Toggle preview", Handler: togglePreviewKey},
			{Key: "p", Description: "Project filter", Handler: cycleFilterKey},
			{Key: "d", Description: "Delete", Handler: recentDeleteKey},
			{Key: "esc", Description: "Back", Handler: recentBackKey},
			{Key: "q", Description: "Dashboard", Handler: globalDashboardKey},
		},
		workspaceBindings(), newObservationBindings(), authBindings(),
	)}
}

// confirmDeleteBindings are the y/n confirm/cancel keys shared by the list
// screens. They only match while a delete confirmation is pending, so the same
// letters keep their normal meaning otherwise.
func confirmDeleteBindings() []Binding {
	pending := func(m *Model) bool { return m.ConfirmDelete }
	return []Binding{
		{Key: "y", Description: "Confirm delete", When: pending, Handler: confirmDeleteKey},
		{Key: "Y", Description: "Confirm delete", When: pending, Handler: confirmDeleteKey},
		{Key: "n", Description: "Cancel", When: pending, Handler: cancelConfirmDeleteKey},
		{Key: "N", Description: "Cancel", When: pending, Handler: cancelConfirmDeleteKey},
		{Key: "esc", Description: "Cancel", When: pending, Handler: cancelConfirmDeleteKey},
	}
}

// dispatchDeclared routes a key through a declarative keymap. It is the single
// lookup used by the compatibility shims below, so no per-screen switch exists.
func dispatchDeclared(km Keymap, m Model, key string) (tea.Model, tea.Cmd) {
	if b, ok := matchBinding(km.Bindings, &m, key); ok {
		return b.Handler(m, key)
	}
	return m, nil
}

// ─── Search handlers ────────────────────────────────────────────────────────

func searchSubmitKey(m Model, _ string) (tea.Model, tea.Cmd) {
	return m.handleSearchInputKeys(tea.KeyMsg{Type: tea.KeyEnter})
}

func searchSwitchSourceKey(m Model, _ string) (tea.Model, tea.Cmd) {
	return m.handleSearchInputKeys(tea.KeyMsg{Type: tea.KeyTab})
}

func searchFocusInputKey(m Model, _ string) (tea.Model, tea.Cmd) {
	m.SearchInput.Focus()
	return m, nil
}

func searchCancelKey(m Model, _ string) (tea.Model, tea.Cmd) {
	m.SearchInput.Blur()
	m.NavReset()
	m.Cursor = 0
	return m, nil
}

// ─── Confirm-delete handlers ────────────────────────────────────────────────

func confirmDeleteKey(m Model, _ string) (tea.Model, tea.Cmd) {
	id := m.ConfirmDeleteID
	m.ConfirmDelete = false
	m.ConfirmDeleteID = 0
	return m, deleteObservationCmd(m.deps, id)
}

func cancelConfirmDeleteKey(m Model, _ string) (tea.Model, tea.Cmd) {
	m.ConfirmDelete = false
	m.ConfirmDeleteID = 0
	return m, nil
}

// ─── Search results handlers ────────────────────────────────────────────────

func searchResultsOpenKey(m Model, _ string) (tea.Model, tea.Cmd) {
	if m.SearchMode == "code" {
		m.PreviewVisible = true
		m.updatePreviewContent()
		return m, nil
	}
	if item, ok := m.SearchListModel.SelectedItem().(searchResultItem); ok {
		m.PrevCursor = m.SearchListModel.Index()
		m.DetailLoading = true
		return m, tea.Batch(m.SetupSpinner.Tick, loadObservationDetail(m.deps, item.result.ID))
	}
	return m, nil
}

func searchResultsTimelineKey(m Model, _ string) (tea.Model, tea.Cmd) {
	if m.SearchMode == "code" {
		return m, nil
	}
	if item, ok := m.SearchListModel.SelectedItem().(searchResultItem); ok {
		m.PrevCursor = m.SearchListModel.Index()
		return m, loadTimeline(m.deps, item.result.ID)
	}
	return m, nil
}

func searchResultsDeleteKey(m Model, _ string) (tea.Model, tea.Cmd) {
	if m.SearchMode == "code" {
		return m, nil
	}
	if item, ok := m.SearchListModel.SelectedItem().(searchResultItem); ok {
		m.ConfirmDelete = true
		m.ConfirmDeleteID = item.result.ID
		m.DeleteTargetTitle = item.result.Title
	}
	return m, nil
}

func openSearchFromResultsKey(m Model, _ string) (tea.Model, tea.Cmd) {
	m.NavPush(ScreenSearch)
	m.SearchInput.Focus()
	return m, nil
}

func searchResultsBackKey(m Model, _ string) (tea.Model, tea.Cmd) {
	m.PreviewVisible = false
	m.NavPop()
	m.Cursor = 0
	m.Scroll = 0
	m.SearchInput.Focus()
	return m, nil
}

// ─── Recent handlers ────────────────────────────────────────────────────────

func recentOpenKey(m Model, _ string) (tea.Model, tea.Cmd) {
	if item, ok := m.RecentList.SelectedItem().(observationItem); ok {
		m.PrevCursor = m.RecentList.Index()
		m.DetailLoading = true
		return m, tea.Batch(m.SetupSpinner.Tick, loadObservationDetail(m.deps, item.obs.ID))
	}
	return m, nil
}

func recentTimelineKey(m Model, _ string) (tea.Model, tea.Cmd) {
	if item, ok := m.RecentList.SelectedItem().(observationItem); ok {
		m.PrevCursor = m.RecentList.Index()
		return m, loadTimeline(m.deps, item.obs.ID)
	}
	return m, nil
}

func recentGraphKey(m Model, _ string) (tea.Model, tea.Cmd) {
	if item, ok := m.RecentList.SelectedItem().(observationItem); ok {
		m.PrevCursor = m.RecentList.Index()
		m.GraphRootID = item.obs.ID
		m.NavPush(ScreenGraph)
		m.ActiveDeckWorkspace = 1
		return m, loadGraphRelated(m.deps, m.GraphRootID)
	}
	return m, nil
}

func recentDeleteKey(m Model, _ string) (tea.Model, tea.Cmd) {
	if item, ok := m.RecentList.SelectedItem().(observationItem); ok {
		m.ConfirmDelete = true
		m.ConfirmDeleteID = item.obs.ID
		m.DeleteTargetTitle = item.obs.Title
	}
	return m, nil
}

func recentBackKey(m Model, _ string) (tea.Model, tea.Cmd) {
	m.PreviewVisible = false
	prev := m.NavPop()
	m.Cursor = 0
	m.Scroll = 0
	return m, m.refreshScreen(prev)
}

// ─── Compatibility shims ────────────────────────────────────────────────────

func (m Model) handleSearchKeys(key string) (tea.Model, tea.Cmd) {
	return dispatchDeclared(searchScreenKeymap(), m, key)
}

func (m Model) handleSearchResultsKeys(key string) (tea.Model, tea.Cmd) {
	return dispatchDeclared(searchResultsScreenKeymap(), m, key)
}

func (m Model) handleRecentKeys(key string) (tea.Model, tea.Cmd) {
	return dispatchDeclared(recentScreenKeymap(), m, key)
}
