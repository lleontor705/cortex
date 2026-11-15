package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/code"
	"github.com/lleontor705/cortex/v2/internal/store/session"

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

func TestHandleDashboardDirectShortcuts(t *testing.T) {
	m := New(&Deps{})
	m.Width, m.Height = 120, 40

	// 'r' -> ScreenRecent
	updated, _ := m.handleKeyPress("r")
	res := updated.(Model)
	if res.Screen != ScreenRecent {
		t.Fatalf("r shortcut: screen = %v, want %v", res.Screen, ScreenRecent)
	}

	// 'g' -> ScreenGraph
	m.Screen = ScreenDashboard
	updated, _ = m.handleKeyPress("g")
	res = updated.(Model)
	if res.Screen != ScreenGraph {
		t.Fatalf("g shortcut: screen = %v, want %v", res.Screen, ScreenGraph)
	}

	// 'c' -> ScreenLocalConfig
	m.Screen = ScreenDashboard
	updated, _ = m.handleKeyPress("c")
	res = updated.(Model)
	if res.Screen != ScreenLocalConfig {
		t.Fatalf("c shortcut: screen = %v, want %v", res.Screen, ScreenLocalConfig)
	}

	// 'i' -> ScreenSetup
	m.Screen = ScreenDashboard
	updated, _ = m.handleKeyPress("i")
	res = updated.(Model)
	if res.Screen != ScreenSetup {
		t.Fatalf("i shortcut: screen = %v, want %v", res.Screen, ScreenSetup)
	}
}

func TestHandleLocalConfigTestConnection(t *testing.T) {
	m := New(&Deps{})
	m.Width, m.Height = 120, 40
	m.Screen = ScreenLocalConfig
	m.LocalCfgFocusField = 3 // AI section
	m.LocalCfgLLMBaseURL.SetValue("http://127.0.0.1:11434")

	updated, cmd := m.handleLocalConfigKeys("t")
	res := updated.(Model)
	if !res.TestConnTesting {
		t.Error("expected TestConnTesting to be true")
	}
	if cmd == nil {
		t.Fatal("expected test connection cmd to be non-nil")
	}
}

func TestHandleWorkspaceSwitchers(t *testing.T) {
	m := New(&Deps{})
	m.Width, m.Height = 120, 40
	m.Screen = ScreenDashboard

	// Press '1' -> ScreenRecent (Workspace 0)
	updated, _ := m.handleKeyPress("1")
	res := updated.(Model)
	if res.Screen != ScreenRecent || res.ActiveDeckWorkspace != 0 {
		t.Fatalf("key 1: screen=%v ws=%d, want ScreenRecent, ws=0", res.Screen, res.ActiveDeckWorkspace)
	}

	// Press '2' -> ScreenGraph (Workspace 1)
	m.Screen = ScreenDashboard
	updated, _ = m.handleKeyPress("2")
	res = updated.(Model)
	if res.Screen != ScreenGraph || res.ActiveDeckWorkspace != 1 {
		t.Fatalf("key 2: screen=%v ws=%d, want ScreenGraph, ws=1", res.Screen, res.ActiveDeckWorkspace)
	}

	// Press '3' -> ScreenHealth (Workspace 2)
	m.Screen = ScreenDashboard
	updated, _ = m.handleKeyPress("3")
	res = updated.(Model)
	if res.Screen != ScreenHealth || res.ActiveDeckWorkspace != 2 {
		t.Fatalf("key 3: screen=%v ws=%d, want ScreenHealth, ws=2", res.Screen, res.ActiveDeckWorkspace)
	}

	// Press '4' -> ScreenSetup (Workspace 3)
	m.Screen = ScreenDashboard
	updated, _ = m.handleKeyPress("4")
	res = updated.(Model)
	if res.Screen != ScreenSetup || res.ActiveDeckWorkspace != 3 {
		t.Fatalf("key 4: screen=%v ws=%d, want ScreenSetup, ws=3", res.Screen, res.ActiveDeckWorkspace)
	}
}

func TestHandleControlHubSubTabSwitching(t *testing.T) {
	m := New(&Deps{})
	m.Width, m.Height = 120, 40

	// From Setup, press 'c' -> ScreenLocalConfig
	m.Screen = ScreenSetup
	updated, _ := m.handleSetupKeys("c")
	res := updated.(Model)
	if res.Screen != ScreenLocalConfig {
		t.Fatalf("c from Setup: screen=%v, want ScreenLocalConfig", res.Screen)
	}

	// From LocalConfig, press 'i' -> ScreenSetup
	m.Screen = ScreenLocalConfig
	updated, _ = m.handleLocalConfigKeys("i")
	res = updated.(Model)
	if res.Screen != ScreenSetup {
		t.Fatalf("i from LocalConfig: screen=%v, want ScreenSetup", res.Screen)
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

func TestHandleSearchInputTabSwitchesSource(t *testing.T) {
	m := New(&Deps{})
	m.Screen = ScreenSearch
	m.SearchInput.Focus()

	updated, _ := m.handleSearchInputKeys(tea.KeyMsg{Type: tea.KeyTab})
	result := updated.(Model)
	if result.SearchMode != "code" {
		t.Fatalf("search mode = %q, want code", result.SearchMode)
	}
	if !strings.Contains(result.SearchInput.Placeholder, "file path") {
		t.Fatalf("placeholder = %q", result.SearchInput.Placeholder)
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

func TestSetupProfileCycle(t *testing.T) {
	m := New(&Deps{})
	m.Screen = ScreenSetup
	m.SetupProfile = "agent"

	// Press 'p' -> dev
	updated, _ := m.handleSetupKeys("p")
	m = updated.(Model)
	if m.SetupProfile != "dev" {
		t.Fatalf("expected profile dev, got %q", m.SetupProfile)
	}

	// Press 'p' -> minimal
	updated, _ = m.handleSetupKeys("p")
	m = updated.(Model)
	if m.SetupProfile != "minimal" {
		t.Fatalf("expected profile minimal, got %q", m.SetupProfile)
	}

	// Press 'p' -> agent
	updated, _ = m.handleSetupKeys("p")
	m = updated.(Model)
	if m.SetupProfile != "agent" {
		t.Fatalf("expected profile agent, got %q", m.SetupProfile)
	}
}

// ─── Registry Dispatch (REQ-NAV-001 / REQ-NAV-002) ──────────────────────────

func TestRegistryDispatch(t *testing.T) {
	t.Run("screen bindings precede globals", func(t *testing.T) {
		// "?" on Help must reach handleHelpKeys, not the global help opener.
		help := newTestModel(120, 40)
		seedNavFrom(&help, ScreenHelp, ScreenRecent)
		if out, _ := help.handleKeyPress("?"); out.(Model).Screen != ScreenRecent {
			t.Fatalf("? on Help: screen = %v, want %v (global ran first)", out.(Model).Screen, ScreenRecent)
		}

		// "esc" on Search must run the Search handler, not the global pop.
		search := newTestModel(120, 40)
		seedNavFrom(&search, ScreenSearch, ScreenHelp)
		if out, _ := search.handleKeyPress("esc"); out.(Model).Screen != ScreenDashboard {
			t.Fatalf("esc on Search: screen = %v, want %v (global ran first)", out.(Model).Screen, ScreenDashboard)
		}

		// "q" on Dashboard must quit via the screen binding, not reset the view.
		dash := newTestModel(120, 40)
		dash.Screen = ScreenDashboard
		_, cmd := dash.handleKeyPress("q")
		if cmd == nil {
			t.Fatal("q on Dashboard: no command returned")
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatalf("q on Dashboard: cmd produced %T, want tea.QuitMsg", cmd())
		}
	})

	t.Run("T opens timeline from detail results and recent", func(t *testing.T) {
		obs := newTestObs(1042, "decision", "Fix JWT")
		cases := []struct {
			name   string
			screen Screen
			seed   func(*Model)
		}{
			{"detail", ScreenObservationDetail, func(m *Model) { m.SelectedObservation = obs }},
			{"results", ScreenSearchResults, func(m *Model) {
				m.SearchResults = []*domain.SearchResult{{Observation: *obs}}
				m.SearchListModel.SetItems([]list.Item{searchResultItem{result: m.SearchResults[0]}})
			}},
			{"recent", ScreenRecent, func(m *Model) {
				m.RecentObservations = []*domain.Observation{obs}
				m.RecentList.SetItems(observationItems(obs))
			}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				m := newTestModel(120, 40)
				m.Screen = tc.screen
				tc.seed(&m)
				_, cmd := m.handleKeyPress("T")
				if cmd == nil {
					t.Fatalf("T on %s returned no command", tc.name)
				}
				if _, ok := cmd().(timelineMsg); !ok {
					t.Fatalf("T on %s produced %T, want timelineMsg", tc.name, cmd())
				}
			})
		}
	})

	t.Run("f types into the focused search input", func(t *testing.T) {
		m := newTestModel(120, 40)
		m.Screen = ScreenSearch
		m.SearchInput.Focus()
		m.SearchInput.SetValue("")
		out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
		got := out.(Model)
		if got.SearchInput.Value() != "f" {
			t.Fatalf("search input = %q, want %q", got.SearchInput.Value(), "f")
		}
		if got.Screen != ScreenSearch {
			t.Fatalf("screen = %v, want %v", got.Screen, ScreenSearch)
		}
	})

	t.Run("removed u P and gg bindings are inert", func(t *testing.T) {
		m := newTestModel(120, 40)
		m.Screen = ScreenHealth
		for _, key := range []string{"u", "U", "P", "g", "g"} {
			out, cmd := m.handleKeyPress(key)
			m = out.(Model)
			if cmd != nil {
				t.Fatalf("key %q: cmd = %v, want nil", key, cmd)
			}
			if m.ToastMessage != "" {
				t.Fatalf("key %q produced toast %q", key, m.ToastMessage)
			}
		}
		if m.Screen != ScreenHealth {
			t.Fatalf("inert keys mutated state: screen=%v", m.Screen)
		}

		dash := newTestModel(120, 40)
		dash.Screen = ScreenDashboard
		for _, key := range []string{"u", "P"} {
			out, cmd := dash.handleKeyPress(key)
			dash = out.(Model)
			if cmd != nil || dash.ToastMessage != "" {
				t.Fatalf("dashboard key %q: cmd=%v toast=%q", key, cmd, dash.ToastMessage)
			}
		}
	})

	t.Run("global keys dispatch on every non-input screen", func(t *testing.T) {
		screens := []Screen{
			ScreenDashboard, ScreenSearch, ScreenSearchResults, ScreenRecent, ScreenObservationDetail,
			ScreenTimeline, ScreenSessions, ScreenSessionDetail, ScreenSetup, ScreenGraph, ScreenArchive,
			ScreenHealth, ScreenEmbeddingConfig, ScreenLocalConfig, ScreenHelp,
		}
		for _, screen := range screens {
			m := newTestModel(120, 40)
			m.Screen = screen
			for _, key := range []string{"ctrl+k", "?", "esc", "q"} {
				if _, _, handled := keymapRegistry.Dispatch(m, key); !handled {
					t.Fatalf("global key %q not dispatchable on screen %v", key, screen)
				}
			}
			if out, _ := m.handleKeyPress("ctrl+k"); !out.(Model).CmdPaletteOpen {
				t.Fatalf("ctrl+k did not open the palette on screen %v", screen)
			}
		}

		help := newTestModel(120, 40)
		help.Screen = ScreenRecent
		if out, _ := help.handleKeyPress("?"); out.(Model).Screen != ScreenHelp {
			t.Fatalf("? did not open Help, screen = %v", out.(Model).Screen)
		}
	})
}

// TestUpdateMessageRoutingAndIntercepts covers the non-key Update() message
// routing and the input-mode intercepts, which lie outside the registry's
// declarative key coverage.
func TestUpdateMessageRoutingAndIntercepts(t *testing.T) {
	errSample := errors.New("boom")
	obs := newTestObs(1, "decision", "T")
	msgs := []tea.Msg{
		tea.WindowSizeMsg{Width: 120, Height: 40}, tea.WindowSizeMsg{Width: 60, Height: 15}, updateCheckMsg{}, animTickMsg(time.Now()),
		setupAgentsDetectedMsg{}, testConnResultMsg{success: true, message: "ok"}, testConnResultMsg{success: false, message: "bad"},
		statsLoadedMsg{stats: &combinedStats{Projects: []string{"a"}}}, statsLoadedMsg{err: errSample},
		searchResultsMsg{results: []*domain.SearchResult{{Observation: domain.Observation{ID: 1}}}, query: "q"}, searchResultsMsg{err: errSample},
		codeSearchResultsMsg{results: []code.Symbol{{Name: "F"}}, query: "q"}, codeSearchResultsMsg{err: errSample},
		recentObservationsMsg{observations: []*domain.Observation{obs}}, recentObservationsMsg{err: errSample},
		observationDetailMsg{observation: obs}, observationDetailMsg{err: errSample}, timelineMsg{focus: obs}, timelineMsg{err: errSample},
		recentSessionsMsg{sessions: []*session.SessionStats{{Session: &domain.Session{ID: "s"}}}}, recentSessionsMsg{err: errSample},
		sessionObservationsMsg{observations: []*domain.Observation{obs}}, sessionObservationsMsg{err: errSample},
		graphLoadedMsg{related: []*domain.Observation{obs}}, graphLoadedMsg{err: errSample},
		archiveLoadedMsg{observations: []*domain.Observation{obs}}, archiveLoadedMsg{err: errSample},
		healthLoadedMsg{obsCount: 3, edgeCount: 2}, healthLoadedMsg{err: errSample},
		ollamaStatusMsg{running: true, hasModel: true}, ollamaStartMsg{}, ollamaPullMsg{done: true}, configSavedMsg{}, configReloadedMsg{},
		localConfigSavedMsg{}, reindexProgressMsg{done: true, total: 4, indexed: 4}, activityDataMsg{daily: []int{1, 2, 3}},
		deleteObservationMsg{id: 1}, unarchiveObservationMsg{id: 1}, observationCreatedMsg{observation: obs},
	}
	for i, msg := range msgs {
		m := newTestModel(120, 40)
		if out, _ := m.Update(msg); !isModel(out) {
			t.Fatalf("msg %d (%T): %T", i, msg, out)
		}
	}
	intercepts := []func(*Model){
		func(m *Model) { m.CmdPaletteOpen = true }, func(m *Model) { m.NewObsModalOpen = true },
		func(m *Model) { m.Screen = ScreenSearch; m.SearchInput.Focus() },
		func(m *Model) { m.Screen = ScreenLocalConfig; m.LocalCfgFocusField = 0; m.LocalCfgLLMModel.Focus() },
		func(m *Model) { m.Screen = ScreenLocalConfig; m.LocalCfgDatabasePath.Focus() },
	}
	for i, set := range intercepts {
		m := newTestModel(120, 40)
		set(&m)
		if out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc}); !isModel(out) {
			t.Fatalf("intercept %d: %T", i, out)
		}
	}
	for _, s := range []Screen{ScreenSearchResults, ScreenRecent, ScreenSessions, ScreenGraph, ScreenArchive} {
		m := newTestModel(120, 40)
		m.Screen = s
		if out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")}); !isModel(out) {
			t.Fatalf("list nav screen %d", s)
		}
	}
	quit := newTestModel(120, 40)
	if _, cmd := quit.Update(tea.KeyMsg{Type: tea.KeyCtrlC}); cmd == nil {
		t.Fatal("ctrl+c should quit")
	}
}

// TestLocalConfigKeyAndInputBranches exercises the Configuration handler's
// field-cycling and text-input branches, which are internal to the handler and
// therefore outside the registry's declarative coverage.
func TestLocalConfigKeyAndInputBranches(t *testing.T) {
	for field := 0; field <= 16; field++ {
		for _, key := range []string{"left", "right", "h", "l", "enter", " ", "tab", "shift+tab", "up", "down"} {
			m := newTestModel(120, 40)
			m.Screen, m.LocalCfgFocusField = ScreenLocalConfig, field
			if out, _ := m.handleLocalConfigKeys(key); !isModel(out) {
				t.Fatalf("field %d key %q: %T", field, key, out)
			}
		}
	}
	for _, set := range []func(*Model){func(m *Model) { m.LocalCfgSaving = true }, func(m *Model) { m.LocalCfgSaved = true }} {
		for _, key := range []string{"esc", "q", "enter", "z"} {
			m := newTestModel(120, 40)
			set(&m)
			if out, _ := m.handleLocalConfigKeys(key); !isModel(out) {
				t.Fatalf("state key %q: %T", key, out)
			}
		}
	}
	for _, field := range []int{0, 3, 4, 6, 7, 10, 11, 13, 14, 15, 99} {
		for _, msg := range []tea.KeyMsg{{Type: tea.KeyEnter}, {Type: tea.KeyRunes, Runes: []rune("a")}} {
			m := newTestModel(120, 40)
			m.Screen, m.LocalCfgFocusField = ScreenLocalConfig, field
			if out, _ := m.handleLocalConfigInput(msg); !isModel(out) {
				t.Fatalf("input field %d: %T", field, out)
			}
		}
	}
}
