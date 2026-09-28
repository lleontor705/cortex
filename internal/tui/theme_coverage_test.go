package tui

import (
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/store/session"

	"github.com/charmbracelet/bubbles/list"
)

// ─── renderStatusBar Branches ────────────────────────────────────────────────

func TestStatusBarDashboardScreen(t *testing.T) {
	m := newTestModel(120, 40)
	m.Screen = ScreenDashboard
	m.Stats = &combinedStats{TotalObservations: 42}
	m.UploadToCortex = true
	m.Version = "v2.0"
	out := m.renderStatusBar()
	for _, want := range []string{"Dashboard", "42 obs", "sync: on", "v2.0", "j/k nav"} {
		if !strings.Contains(out, want) {
			t.Errorf("StatusBar dashboard missing %q", want)
		}
	}
}

func TestStatusBarNonDashboardScreen(t *testing.T) {
	m := newTestModel(120, 40)
	m.Screen = ScreenRecent
	m.Stats = &combinedStats{TotalObservations: 10}
	out := m.renderStatusBar()
	if !strings.Contains(out, "n new") || !strings.Contains(out, "Esc back") {
		t.Error("non-dashboard should show alternate help hint")
	}
}

func TestStatusBarCompact(t *testing.T) {
	m := newTestModel(30, 10)
	m.Screen = ScreenDashboard
	m.Stats = &combinedStats{TotalObservations: 5}
	out := m.renderStatusBar()
	if out == "" {
		t.Error("compact status bar should produce output")
	}
}

func TestStatusBarCompactWidthMin(t *testing.T) {
	m := newTestModel(5, 5)
	m.Screen = ScreenDashboard
	out := m.renderStatusBar()
	if out == "" {
		t.Error("very small terminal should still produce status bar")
	}
}

func TestStatusBarFilterProject(t *testing.T) {
	m := newTestModel(120, 40)
	m.Screen = ScreenDashboard
	m.FilterProject = "my-proj"
	out := m.renderStatusBar()
	if !strings.Contains(out, "my-proj") {
		t.Error("missing filter project label")
	}
}

func TestStatusBarPreviewVisible(t *testing.T) {
	m := newTestModel(120, 40)
	m.Screen = ScreenDashboard
	m.PreviewVisible = true
	out := m.renderStatusBar()
	if !strings.Contains(out, "preview: on") {
		t.Error("missing preview label")
	}
}

func TestStatusBarUploadDisabled(t *testing.T) {
	m := newTestModel(120, 40)
	m.Screen = ScreenDashboard
	m.UploadToCortex = false
	out := m.renderStatusBar()
	if !strings.Contains(out, "local only") {
		t.Error("missing local only label")
	}
}

func TestStatusBarUserBadgeDefaults(t *testing.T) {
	m := newTestModel(120, 40)
	m.Screen = ScreenDashboard
	m.UserRole = ""
	out := m.renderStatusBar()
	if !strings.Contains(out, "MEMBER") {
		t.Error("empty role should default to MEMBER")
	}
}

func TestStatusBarUserRole(t *testing.T) {
	m := newTestModel(120, 40)
	m.Screen = ScreenDashboard
	m.UserRole = "admin"
	out := m.renderStatusBar()
	if !strings.Contains(out, "ADMIN") {
		t.Error("should uppercase role")
	}
}

func TestStatusBarThemeLight(t *testing.T) {
	m := newTestModel(120, 40)
	m.Screen = ScreenDashboard
	m.IsDarkTheme = false
	out := m.renderStatusBar()
	if !strings.Contains(out, "Light") {
		t.Error("expected Light theme badge")
	}
}

func TestStatusBarThemeDark(t *testing.T) {
	m := newTestModel(120, 40)
	m.Screen = ScreenDashboard
	m.IsDarkTheme = true
	out := m.renderStatusBar()
	if !strings.Contains(out, "Dark") {
		t.Error("expected Dark theme badge")
	}
}

func TestStatusBarRecentPosition(t *testing.T) {
	m := newTestModel(120, 40)
	m.Screen = ScreenRecent
	m.RecentObservations = []*domain.Observation{
		{ID: 1, Title: "A"},
		{ID: 2, Title: "B"},
	}
	items := []list.Item{
		observationItem{obs: m.RecentObservations[0]},
		observationItem{obs: m.RecentObservations[1]},
	}
	m.RecentList.SetItems(items)
	m.RecentList.SetSize(116, 30)
	out := m.renderStatusBar()
	if !strings.Contains(out, "[1/2]") {
		t.Error("expected position indicator")
	}
}

func TestStatusBarSearchResultsPosition(t *testing.T) {
	m := newTestModel(120, 40)
	m.Screen = ScreenSearchResults
	m.SearchResults = []*domain.SearchResult{
		{Observation: domain.Observation{ID: 1}},
		{Observation: domain.Observation{ID: 2}},
	}
	items := []list.Item{
		searchResultItem{result: m.SearchResults[0]},
		searchResultItem{result: m.SearchResults[1]},
	}
	m.SearchListModel.SetItems(items)
	m.SearchListModel.SetSize(116, 30)
	out := m.renderStatusBar()
	if !strings.Contains(out, "[1/2]") {
		t.Error("expected position indicator for search results")
	}
}

func TestStatusBarSessionsPosition(t *testing.T) {
	m := newTestModel(120, 40)
	m.Screen = ScreenSessions
	m.Sessions = []*session.SessionStats{
		{Session: &domain.Session{ID: "s1"}, ObservationCount: 1},
		{Session: &domain.Session{ID: "s2"}, ObservationCount: 2},
	}
	items := []list.Item{
		sessionItem{session: m.Sessions[0]},
		sessionItem{session: m.Sessions[1]},
	}
	m.SessionListModel.SetItems(items)
	m.SessionListModel.SetSize(116, 30)
	out := m.renderStatusBar()
	if !strings.Contains(out, "[1/2]") {
		t.Error("expected position indicator for sessions")
	}
}

func TestStatusBarGraphPosition(t *testing.T) {
	m := newTestModel(120, 40)
	m.Screen = ScreenGraph
	m.GraphObservations = []*domain.Observation{
		{ID: 1, Title: "A"},
	}
	items := []list.Item{graphItem{obs: m.GraphObservations[0]}}
	m.GraphListModel.SetItems(items)
	m.GraphListModel.SetSize(116, 28)
	out := m.renderStatusBar()
	if !strings.Contains(out, "[1/1]") {
		t.Error("expected position indicator for graph")
	}
}

func TestStatusBarArchivePosition(t *testing.T) {
	m := newTestModel(120, 40)
	m.Screen = ScreenArchive
	m.ArchivedObservations = []*domain.Observation{{ID: 1}}
	items := []list.Item{observationItem{obs: m.ArchivedObservations[0]}}
	m.ArchiveList.SetItems(items)
	m.ArchiveList.SetSize(116, 30)
	out := m.renderStatusBar()
	if !strings.Contains(out, "[1/1]") {
		t.Error("expected position indicator for archive")
	}
}

// ─── Theme Toggle Visual Branches ────────────────────────────────────────────

func TestDarkThemeDashboard(t *testing.T) {
	m := newTestModel(120, 40)
	m.IsDarkTheme = true
	m.Stats = &combinedStats{TotalObservations: 10}
	output := m.View()
	if output == "" {
		t.Error("dark theme view should produce output")
	}
}

func TestLightThemeDashboard(t *testing.T) {
	m := newTestModel(120, 40)
	m.IsDarkTheme = false
	m.Stats = &combinedStats{TotalObservations: 10}
	output := m.View()
	if output == "" {
		t.Error("light theme view should produce output")
	}
}

func TestThemeToggleKeyBinding(t *testing.T) {
	ApplyTheme(true)
	m := newTestModel(120, 40)
	m.Screen = ScreenDashboard
	m.IsDarkTheme = true

	updated, _ := m.handleKeyPress("t")
	result := updated.(Model)
	if result.IsDarkTheme {
		t.Error("expected light after toggle")
	}
	if result.ToastMessage == "" {
		t.Error("expected toast message")
	}
}

// ─── renderTypeBadge Coverage ────────────────────────────────────────────────

func TestRenderTypeBadgeAllTypes(t *testing.T) {
	types := []string{"bugfix", "decision", "discovery", "architecture", "pattern", "config", "learning", "unknown"}
	for _, obsType := range types {
		out := renderTypeBadge(obsType)
		if out == "" {
			t.Errorf("renderTypeBadge(%q) returned empty", obsType)
		}
	}
}

func TestRenderTypeBadgeEmpty(t *testing.T) {
	out := renderTypeBadge("")
	if !strings.Contains(out, "NOTE") {
		t.Error("empty type should show NOTE")
	}
}
