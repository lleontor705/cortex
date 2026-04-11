package tui

import (
	"errors"
	"testing"

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/store/session"

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
	m.SearchResults = []*domain.SearchResult{
		{Observation: domain.Observation{ID: 1, Title: "First"}},
		{Observation: domain.Observation{ID: 2, Title: "Second"}},
		{Observation: domain.Observation{ID: 3, Title: "Third"}},
	}

	// Move down
	updated, _ := m.handleSearchResultsKeys("j")
	result := updated.(Model)
	if result.Cursor != 1 {
		t.Fatalf("cursor = %d, want 1", result.Cursor)
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
	m.RecentObservations = []*domain.Observation{
		{ID: 1, Title: "First"},
		{ID: 2, Title: "Second"},
	}

	updated, _ := m.handleRecentKeys("j")
	result := updated.(Model)
	if result.Cursor != 1 {
		t.Fatalf("cursor = %d, want 1", result.Cursor)
	}

	// Can't go past end
	updated, _ = result.handleRecentKeys("j")
	result = updated.(Model)
	if result.Cursor != 1 {
		t.Fatalf("cursor = %d, want 1", result.Cursor)
	}
}

// ─── Observation Detail Keys ────────────────────────────────────────────────

func TestHandleObservationDetailKeysScroll(t *testing.T) {
	m := New(&Deps{})
	m.Screen = ScreenObservationDetail
	m.SelectedObservation = &domain.Observation{ID: 1}

	// Scroll down
	updated, _ := m.handleObservationDetailKeys("j")
	result := updated.(Model)
	if result.DetailScroll != 1 {
		t.Fatalf("scroll = %d, want 1", result.DetailScroll)
	}

	// Scroll up
	updated, _ = result.handleObservationDetailKeys("k")
	result = updated.(Model)
	if result.DetailScroll != 0 {
		t.Fatalf("scroll = %d, want 0", result.DetailScroll)
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
	m.Sessions = []*session.SessionStats{
		{Session: &domain.Session{ID: "s1", Project: "p1"}, ObservationCount: 5},
		{Session: &domain.Session{ID: "s2", Project: "p2"}, ObservationCount: 3},
	}

	updated, _ := m.handleSessionsKeys("j")
	result := updated.(Model)
	if result.Cursor != 1 {
		t.Fatalf("cursor = %d, want 1", result.Cursor)
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
	m.GraphObservations = []*domain.Observation{
		{ID: 1, Title: "First"},
		{ID: 2, Title: "Second"},
	}

	updated, _ := m.handleGraphKeys("j")
	result := updated.(Model)
	if result.Cursor != 1 {
		t.Fatalf("cursor = %d, want 1", result.Cursor)
	}
}

func TestHandleGraphKeysReroot(t *testing.T) {
	m := New(&Deps{})
	m.Screen = ScreenGraph
	m.Width, m.Height = 120, 40
	m.GraphObservations = []*domain.Observation{
		{ID: 5, Title: "Related"},
	}

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
	m.ArchivedObservations = []*domain.Observation{
		{ID: 1, Title: "Archived 1"},
		{ID: 2, Title: "Archived 2"},
	}

	updated, _ := m.handleArchiveKeys("j")
	result := updated.(Model)
	if result.Cursor != 1 {
		t.Fatalf("cursor = %d, want 1", result.Cursor)
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

	// j/k should not panic
	updated, _ := m.handleRecentKeys("j")
	result := updated.(Model)
	if result.Cursor != 0 {
		t.Fatalf("cursor = %d, want 0", result.Cursor)
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

	updated, _ := m.handleSearchResultsKeys("j")
	result := updated.(Model)
	if result.Cursor != 0 {
		t.Fatalf("cursor = %d", result.Cursor)
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

	updated, _ := m.handleGraphKeys("j")
	result := updated.(Model)
	if result.Cursor != 0 {
		t.Fatalf("cursor = %d", result.Cursor)
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

	updated, _ := m.handleArchiveKeys("j")
	result := updated.(Model)
	if result.Cursor != 0 {
		t.Fatalf("cursor = %d", result.Cursor)
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

	updated, _ := m.handleSessionsKeys("j")
	result := updated.(Model)
	if result.Cursor != 0 {
		t.Fatalf("cursor = %d", result.Cursor)
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
