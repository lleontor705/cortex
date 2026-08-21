package tui

import (
	"errors"
	"fmt"
	"testing"

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/store/session"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// ─── Dashboard Keys ─────────────────────────────────────────────────────────

func TestHandleDashboardKeysNavigation(t *testing.T) {
	m := New(&Deps{})
	m.Width, m.Height = 120, 40

	// Move down
	updated, _ := m.handleKeyPress("j")
	result := updated.(Model)
	if result.Cursor != 1 {
		t.Fatalf("cursor = %d, want 1", result.Cursor)
	}

	// Move up
	updated, _ = result.handleKeyPress("k")
	result = updated.(Model)
	if result.Cursor != 0 {
		t.Fatalf("cursor = %d, want 0", result.Cursor)
	}

	// Can't go above 0
	updated, _ = result.handleKeyPress("k")
	result = updated.(Model)
	if result.Cursor != 0 {
		t.Fatalf("cursor = %d, want 0", result.Cursor)
	}
}

func TestHandleDashboardSearchShortcut(t *testing.T) {
	m := New(&Deps{})
	m.Width, m.Height = 120, 40

	updated, _ := m.handleKeyPress("s")
	result := updated.(Model)
	if result.Screen != ScreenSearch {
		t.Fatalf("screen = %v, want %v", result.Screen, ScreenSearch)
	}
}

func TestHandleDashboardQuit(t *testing.T) {
	m := New(&Deps{})
	_, cmd := m.handleKeyPress("q")
	if cmd == nil {
		t.Fatal("q should return quit command")
	}
}

// ─── Search Results Keys ────────────────────────────────────────────────────

func TestHandleSearchResultsKeysScrollAndDetail(t *testing.T) {
	m := New(&Deps{})
	m.Screen = ScreenSearchResults
	m.Width, m.Height = 120, 40
	results := []*domain.SearchResult{
		{Observation: domain.Observation{ID: 1, Title: "First"}},
		{Observation: domain.Observation{ID: 2, Title: "Second"}},
		{Observation: domain.Observation{ID: 3, Title: "Third"}},
	}
	m.SearchResults = results
	items := make([]list.Item, len(results))
	for i, r := range results {
		items[i] = searchResultItem{result: r}
	}
	m.SearchListModel.SetItems(items)
	m.SearchListModel.SetSize(116, 30)

	// Move down via Update (list handles navigation)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	result := updated.(Model)
	if result.SearchListModel.Index() != 1 {
		t.Fatalf("list index = %d, want 1", result.SearchListModel.Index())
	}

	// Press enter to load detail
	_, cmd := result.handleSearchResultsKeys("enter")
	if cmd == nil {
		t.Fatal("enter should return a command to load detail")
	}
}

// ─── Recent Keys ────────────────────────────────────────────────────────────

func TestHandleRecentKeysNavigation(t *testing.T) {
	m := New(&Deps{})
	m.Screen = ScreenRecent
	m.Width, m.Height = 120, 40
	obs := []*domain.Observation{
		{ID: 1, Title: "First"},
		{ID: 2, Title: "Second"},
	}
	m.RecentObservations = obs
	items := make([]list.Item, len(obs))
	for i, o := range obs {
		items[i] = observationItem{obs: o}
	}
	m.RecentList.SetItems(items)
	m.RecentList.SetSize(116, 32)

	// Move down via Update
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	result := updated.(Model)
	if result.RecentList.Index() != 1 {
		t.Fatalf("list index = %d, want 1", result.RecentList.Index())
	}

	// Can't go past end
	updated, _ = result.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	result = updated.(Model)
	if result.RecentList.Index() != 1 {
		t.Fatalf("list index = %d, want 1", result.RecentList.Index())
	}
}

// ─── Observation Detail Keys ────────────────────────────────────────────────

func TestHandleObservationDetailKeysScroll(t *testing.T) {
	m := New(&Deps{})
	m.Screen = ScreenObservationDetail
	m.Width, m.Height = 120, 40
	m.SelectedObservation = &domain.Observation{ID: 1, Content: "line1\nline2\nline3"}

	// Set up the viewport with content that can scroll
	longContent := ""
	for i := 0; i < 100; i++ {
		longContent += fmt.Sprintf("line %d\n", i)
	}
	contentStr := buildDetailContent(m.SelectedObservation, nil, nil, nil)
	_ = contentStr // content built from observation
	m.DetailViewport = viewport.New(80, 10)
	m.DetailViewport.SetContent(longContent)

	// Scroll down
	updated, _ := m.handleObservationDetailKeys("j")
	result := updated.(Model)
	if result.DetailViewport.YOffset < 1 {
		t.Fatalf("viewport YOffset = %d, want >= 1", result.DetailViewport.YOffset)
	}

	// Scroll up
	updated, _ = result.handleObservationDetailKeys("k")
	result = updated.(Model)
	if result.DetailViewport.YOffset != 0 {
		t.Fatalf("viewport YOffset = %d, want 0", result.DetailViewport.YOffset)
	}
}

func TestHandleObservationDetailGraphShortcut(t *testing.T) {
	m := New(&Deps{})
	m.Screen = ScreenObservationDetail
	m.SelectedObservation = &domain.Observation{ID: 42}

	updated, cmd := m.handleObservationDetailKeys("g")
	if cmd == nil {
		t.Fatal("g should return a command to load graph")
	}
	result := updated.(Model)
	if result.GraphRootID != 42 {
		t.Fatalf("graph root = %d, want 42", result.GraphRootID)
	}
}

// ─── Sessions Keys ──────────────────────────────────────────────────────────

func TestHandleSessionsKeysNavigation(t *testing.T) {
	m := New(&Deps{})
	m.Screen = ScreenSessions
	m.Width, m.Height = 120, 40
	sessions := []*session.SessionStats{
		{Session: &domain.Session{ID: "s1", Project: "p1"}, ObservationCount: 5},
		{Session: &domain.Session{ID: "s2", Project: "p2"}, ObservationCount: 3},
	}
	m.Sessions = sessions
	items := make([]list.Item, len(sessions))
	for i, s := range sessions {
		items[i] = sessionItem{session: s}
	}
	m.SessionListModel.SetItems(items)
	m.SessionListModel.SetSize(116, 32)

	// Move down via Update
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	result := updated.(Model)
	if result.SessionListModel.Index() != 1 {
		t.Fatalf("list index = %d, want 1", result.SessionListModel.Index())
	}

	// Enter loads session observations
	_, cmd := result.handleSessionsKeys("enter")
	if cmd == nil {
		t.Fatal("enter should return command")
	}
}

// ─── Graph Keys ─────────────────────────────────────────────────────────────

func TestHandleGraphKeysNavigation(t *testing.T) {
	m := New(&Deps{})
	m.Screen = ScreenGraph
	m.Width, m.Height = 120, 40
	obs := []*domain.Observation{
		{ID: 1, Title: "First"},
		{ID: 2, Title: "Second"},
	}
	m.GraphObservations = obs
	items := make([]list.Item, len(obs))
	for i, o := range obs {
		items[i] = graphItem{obs: o}
	}
	m.GraphListModel.SetItems(items)
	m.GraphListModel.SetSize(116, 28)

	// Move down via Update
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	result := updated.(Model)
	if result.GraphListModel.Index() != 1 {
		t.Fatalf("list index = %d, want 1", result.GraphListModel.Index())
	}
}

func TestHandleGraphKeysReroot(t *testing.T) {
	m := New(&Deps{})
	m.Screen = ScreenGraph
	m.Width, m.Height = 120, 40
	obs := []*domain.Observation{
		{ID: 5, Title: "Related"},
	}
	m.GraphObservations = obs
	items := []list.Item{graphItem{obs: obs[0]}}
	m.GraphListModel.SetItems(items)
	m.GraphListModel.SetSize(116, 28)

	updated, cmd := m.handleGraphKeys("r")
	if cmd == nil {
		t.Fatal("r should return a command to re-root graph")
	}
	result := updated.(Model)
	if result.GraphRootID != 5 {
		t.Fatalf("graph root = %d, want 5", result.GraphRootID)
	}
}

// ─── Archive Keys ───────────────────────────────────────────────────────────

func TestHandleArchiveKeysNavigation(t *testing.T) {
	m := New(&Deps{})
	m.Screen = ScreenArchive
	m.Width, m.Height = 120, 40
	obs := []*domain.Observation{
		{ID: 1, Title: "Archived 1"},
		{ID: 2, Title: "Archived 2"},
	}
	m.ArchivedObservations = obs
	items := make([]list.Item, len(obs))
	for i, o := range obs {
		items[i] = observationItem{obs: o}
	}
	m.ArchiveList.SetItems(items)
	m.ArchiveList.SetSize(116, 32)

	// Move down via Update
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	result := updated.(Model)
	if result.ArchiveList.Index() != 1 {
		t.Fatalf("list index = %d, want 1", result.ArchiveList.Index())
	}
}

// ─── Setup Keys ─────────────────────────────────────────────────────────────

func TestHandleSetupKeysBlocked(t *testing.T) {
	m := New(&Deps{})
	m.Screen = ScreenSetup
	m.SetupInstalling = true

	// All keys blocked during install
	updated, _ := m.handleSetupKeys("enter")
	result := updated.(Model)
	if !result.SetupInstalling {
		t.Fatal("keys should be blocked during install")
	}
}

func TestHandleSetupKeysDoneGoesBack(t *testing.T) {
	m := New(&Deps{})
	m.Screen = ScreenSetup
	m.SetupDone = true

	updated, _ := m.handleSetupKeys("enter")
	result := updated.(Model)
	if result.Screen != ScreenDashboard {
		t.Fatalf("screen = %v, want %v", result.Screen, ScreenDashboard)
	}
}

// ─── Empty List Safety ──────────────────────────────────────────────────────

func TestHandleRecentKeysEmptyList(t *testing.T) {
	m := New(&Deps{})
	m.Screen = ScreenRecent
	m.Width, m.Height = 120, 40
	m.RecentObservations = nil

	// j should not panic on empty list
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	result := updated.(Model)
	if result.RecentList.Index() != 0 {
		t.Fatalf("list index = %d, want 0", result.RecentList.Index())
	}

	// enter should not panic
	_, cmd := result.handleRecentKeys("enter")
	if cmd != nil {
		t.Fatal("enter on empty list should not return command")
	}
}

func TestHandleSearchResultsKeysEmptyList(t *testing.T) {
	m := New(&Deps{})
	m.Screen = ScreenSearchResults
	m.Width, m.Height = 120, 40
	m.SearchResults = nil

	// j should not panic on empty list
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	result := updated.(Model)
	if result.SearchListModel.Index() != 0 {
		t.Fatalf("list index = %d", result.SearchListModel.Index())
	}

	_, cmd := result.handleSearchResultsKeys("enter")
	if cmd != nil {
		t.Fatal("enter on empty list should not return command")
	}
}

func TestHandleGraphKeysEmptyList(t *testing.T) {
	m := New(&Deps{})
	m.Screen = ScreenGraph
	m.Width, m.Height = 120, 40
	m.GraphObservations = nil

	// j should not panic on empty list
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	result := updated.(Model)
	if result.GraphListModel.Index() != 0 {
		t.Fatalf("list index = %d", result.GraphListModel.Index())
	}

	_, cmd := result.handleGraphKeys("r")
	if cmd != nil {
		t.Fatal("r on empty list should not return command")
	}
}

func TestHandleArchiveKeysEmptyList(t *testing.T) {
	m := New(&Deps{})
	m.Screen = ScreenArchive
	m.Width, m.Height = 120, 40
	m.ArchivedObservations = nil

	// j should not panic on empty list
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	result := updated.(Model)
	if result.ArchiveList.Index() != 0 {
		t.Fatalf("list index = %d", result.ArchiveList.Index())
	}

	_, cmd := result.handleArchiveKeys("enter")
	if cmd != nil {
		t.Fatal("enter on empty list should not return command")
	}
}

func TestHandleSessionsKeysEmptyList(t *testing.T) {
	m := New(&Deps{})
	m.Screen = ScreenSessions
	m.Width, m.Height = 120, 40
	m.Sessions = nil

	// j should not panic on empty list
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	result := updated.(Model)
	if result.SessionListModel.Index() != 0 {
		t.Fatalf("list index = %d", result.SessionListModel.Index())
	}

	_, cmd := result.handleSessionsKeys("enter")
	if cmd != nil {
		t.Fatal("enter on empty list should not return command")
	}
}

// ─── Allowlist Prompt Flow ───────────────────────────────────────────────────

func TestHandleSetupAllowlistPromptYes(t *testing.T) {
	original := addClaudeCodeAllowlistFn
	t.Cleanup(func() { addClaudeCodeAllowlistFn = original })
	addClaudeCodeAllowlistFn = func() error { return nil }

	m := New(&Deps{})
	m.Screen = ScreenSetup
	m.SetupAllowlistPrompt = true

	updated, _ := m.handleSetupKeys("y")
	result := updated.(Model)
	if result.SetupAllowlistPrompt {
		t.Fatal("prompt should be dismissed")
	}
	if !result.SetupDone {
		t.Fatal("should be done after y")
	}
	if !result.SetupAllowlistApplied {
		t.Fatal("allowlist should be applied")
	}
}

func TestHandleSetupAllowlistPromptNo(t *testing.T) {
	m := New(&Deps{})
	m.Screen = ScreenSetup
	m.SetupAllowlistPrompt = true

	updated, _ := m.handleSetupKeys("n")
	result := updated.(Model)
	if result.SetupAllowlistPrompt {
		t.Fatal("prompt should be dismissed")
	}
	if !result.SetupDone {
		t.Fatal("should be done after n")
	}
	if result.SetupAllowlistApplied {
		t.Fatal("allowlist should NOT be applied")
	}
}

func TestHandleSetupAllowlistPromptError(t *testing.T) {
	original := addClaudeCodeAllowlistFn
	t.Cleanup(func() { addClaudeCodeAllowlistFn = original })
	addClaudeCodeAllowlistFn = func() error { return errors.New("write failed") }

	m := New(&Deps{})
	m.Screen = ScreenSetup
	m.SetupAllowlistPrompt = true

	updated, _ := m.handleSetupKeys("y")
	result := updated.(Model)
	if result.SetupAllowlistApplied {
		t.Fatal("should NOT be applied on error")
	}
	if result.SetupAllowlistError != "write failed" {
		t.Fatalf("error = %q", result.SetupAllowlistError)
	}
}

// ─── Search Input Keys ──────────────────────────────────────────────────────

func TestHandleSearchInputKeysEsc(t *testing.T) {
	m := New(&Deps{})
	m.Screen = ScreenSearch
	m.SearchInput.Focus()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	result := updated.(Model)
	if result.Screen != ScreenDashboard {
		t.Fatalf("screen = %v, want %v", result.Screen, ScreenDashboard)
	}
}

func TestQuickObservationModalOpenCloseAndSubmit(t *testing.T) {
	m := New(&Deps{})
	m.Screen = ScreenDashboard

	// Press 'n' to open quick memory modal
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = updated.(Model)
	if !m.NewObsModalOpen {
		t.Fatal("expected NewObsModalOpen to be true after pressing 'n'")
	}
	if m.NewObsFocusField != 0 {
		t.Fatalf("expected focus on title (0), got %d", m.NewObsFocusField)
	}

	// Press 'tab' to cycle focus field
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	if m.NewObsFocusField != 1 {
		t.Fatalf("expected focus on content (1), got %d", m.NewObsFocusField)
	}

	// Press 'esc' to cancel modal
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.NewObsModalOpen {
		t.Fatal("expected NewObsModalOpen to be false after pressing 'esc'")
	}

	// Reopen and submit observation
	m.NewObsModalOpen = true
	m.NewObsTitleInput.SetValue("New Architectural Decision")
	m.NewObsContentInput.SetValue("Detailed memory content")
	m.NewObsFocusField = 4 // Save button

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.NewObsModalOpen {
		t.Fatal("expected modal to close on submit")
	}
	if cmd == nil {
		t.Fatal("expected non-nil command on submit")
	}
}

func TestCycleProjectFilter(t *testing.T) {
	m := New(&Deps{})
	m.Screen = ScreenDashboard
	m.Stats = &combinedStats{
		Projects: []string{"proj-a", "proj-b"},
	}

	// First 'p' cycles to proj-a
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	m = updated.(Model)
	if m.FilterProject != "proj-a" {
		t.Fatalf("expected filter proj-a, got %q", m.FilterProject)
	}

	// Second 'p' cycles to proj-b
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	m = updated.(Model)
	if m.FilterProject != "proj-b" {
		t.Fatalf("expected filter proj-b, got %q", m.FilterProject)
	}

	// Third 'p' cycles back to all (empty)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	m = updated.(Model)
	if m.FilterProject != "" {
		t.Fatalf("expected empty filter, got %q", m.FilterProject)
	}
}

func TestSplitPreviewToggle(t *testing.T) {
	m := New(&Deps{})
	m.Screen = ScreenRecent

	// Press 'v' to toggle split preview on
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m = updated.(Model)
	if !m.PreviewVisible {
		t.Fatal("expected PreviewVisible to be true after pressing 'v'")
	}

	// Press 'v' again to toggle split preview off
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m = updated.(Model)
	if m.PreviewVisible {
		t.Fatal("expected PreviewVisible to be false after second 'v'")
	}
}

