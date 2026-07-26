package tui

// command_extra_test.go — behavioral coverage for the command palette, screen
// helpers, render helpers, list-item FilterValue contracts, and the delete /
// unarchive confirmation flows. These exercise pure decision logic and message
// dispatch without launching any interactive program or network service.

import (
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/store/session"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ─── typeColor ───────────────────────────────────────────────────────────────

func TestTypeColorMapping(t *testing.T) {
	cases := []struct {
		name string
		want lipgloss.Color
	}{
		{"bugfix", colorRed},
		{"decision", colorCyan},
		{"architecture", colorPurple},
		{"discovery", colorTeal},
		{"pattern", colorBlue},
		{"config", colorAmber},
		{"preference", colorGold},
		{"unknown", colorSubtext}, // default branch
		{"", colorSubtext},        // empty → default
	}
	for _, tc := range cases {
		got := typeColor(tc.name)
		if got != tc.want {
			t.Fatalf("typeColor(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// ─── screenName ──────────────────────────────────────────────────────────────

func TestScreenNameAllScreens(t *testing.T) {
	cases := []struct {
		screen Screen
		want   string
	}{
		{ScreenDashboard, "Dashboard"},
		{ScreenSearch, "Search"},
		{ScreenSearchResults, "Search Results"},
		{ScreenRecent, "Recent"},
		{ScreenObservationDetail, "Detail"},
		{ScreenTimeline, "Timeline"},
		{ScreenSessions, "Sessions"},
		{ScreenSessionDetail, "Session Detail"},
		{ScreenSetup, "Setup"},
		{ScreenGraph, "Graph"},
		{ScreenArchive, "Archive"},
		{ScreenHealth, "Health"},
		{ScreenEmbeddingConfig, "Embedding Settings"},
		{ScreenHelp, "Help"},
	}
	for _, tc := range cases {
		m := Model{Screen: tc.screen}
		if got := m.screenName(); got != tc.want {
			t.Fatalf("screenName(%v) = %q, want %q", tc.screen, got, tc.want)
		}
	}
}

func TestScreenNameUnknownDefault(t *testing.T) {
	m := Model{Screen: Screen(9999)}
	if got := m.screenName(); got != "Cortex" {
		t.Fatalf("screenName(unknown) = %q, want Cortex", got)
	}
}

// ─── truncateStr ─────────────────────────────────────────────────────────────

func TestTruncateStrPreservesShortStrings(t *testing.T) {
	if got := truncateStr("short", 10); got != "short" {
		t.Fatalf("truncateStr = %q, want %q", got, "short")
	}
}

func TestTruncateStrExactLengthUnchanged(t *testing.T) {
	s := "12345"
	if got := truncateStr(s, 5); got != s {
		t.Fatalf("truncateStr at exact length = %q, want %q", got, s)
	}
}

func TestTruncateStrAddsEllipsisWhenExceeded(t *testing.T) {
	if got := truncateStr("abcdefgh", 4); got != "abcd..." {
		t.Fatalf("truncateStr = %q, want %q", got, "abcd...")
	}
}

func TestTruncateStrCollapsesNewlines(t *testing.T) {
	if got := truncateStr("a\nb\nc", 10); strings.Contains(got, "\n") {
		t.Fatalf("truncateStr should collapse newlines, got %q", got)
	}
}

func TestTruncateStrMultibyteRuneBoundary(t *testing.T) {
	// "héllo" has 5 runes; truncating at 3 keeps rune boundaries intact.
	got := truncateStr("héllo", 3)
	if got != "hél..." {
		t.Fatalf("truncateStr multibyte = %q, want %q", got, "hél...")
	}
}

// ─── formatTime ──────────────────────────────────────────────────────────────

func TestFormatTimeZeroReturnsEmDash(t *testing.T) {
	if got := formatTime(time.Time{}); got != "—" {
		t.Fatalf("formatTime(zero) = %q, want —", got)
	}
}

func TestFormatTimeNonZeroFormatted(t *testing.T) {
	ts := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	got := formatTime(ts)
	if !strings.Contains(got, "2026") {
		t.Fatalf("formatTime = %q, want year 2026", got)
	}
	if got == "—" {
		t.Fatal("non-zero time must not render as em dash")
	}
}

// ─── entityIcon ──────────────────────────────────────────────────────────────

func TestEntityIconMapping(t *testing.T) {
	cases := map[string]string{
		"file":    "F",
		"url":     "U",
		"package": "P",
		"symbol":  "S",
		"concept": "*",
		"":        "*",
	}
	for in, want := range cases {
		if got := entityIcon(in); got != want {
			t.Fatalf("entityIcon(%q) = %q, want %q", in, got, want)
		}
	}
}

// ─── renderSparkline ─────────────────────────────────────────────────────────

func TestRenderSparklineAllZero(t *testing.T) {
	out := renderSparkline([]int{0, 0, 0, 0, 0, 0, 0})
	if !strings.Contains(out, "7-day activity:") {
		t.Fatalf("expected label, got %q", out)
	}
	if !strings.Contains(out, "(0 total)") {
		t.Fatalf("expected zero total, got %q", out)
	}
}

func TestRenderSparklineReportsSum(t *testing.T) {
	out := renderSparkline([]int{1, 2, 3, 0, 0, 0, 0})
	if !strings.Contains(out, "(6 total)") {
		t.Fatalf("expected summed total (6), got %q", out)
	}
}

// ─── allCommands / filteredCommands ─────────────────────────────────────────

func TestAllCommandsCountAndShortcuts(t *testing.T) {
	cmds := allCommands()
	if len(cmds) == 0 {
		t.Fatal("expected at least one palette command")
	}
	// Two commands carry shortcuts by design (Search via "/", Help via "?").
	shortcuts := 0
	for _, c := range cmds {
		if c.shortcut != "" {
			shortcuts++
		}
		if c.execute == nil {
			t.Fatalf("command %q has nil execute", c.name)
		}
	}
	if shortcuts < 2 {
		t.Fatalf("expected at least 2 shortcut-bearing commands, got %d", shortcuts)
	}
}

func TestFilteredCommandsEmptyQueryReturnsAll(t *testing.T) {
	m := Model{}
	got := m.filteredCommands()
	if len(got) != len(allCommands()) {
		t.Fatalf("empty query: got %d, want %d", len(got), len(allCommands()))
	}
}

func TestFilteredCommandsSubstringMatch(t *testing.T) {
	m := Model{}
	m.CmdPaletteInput.SetValue("search")
	got := m.filteredCommands()
	if len(got) == 0 {
		t.Fatal("expected matches for 'search'")
	}
	for _, c := range got {
		if !strings.Contains(strings.ToLower(c.name), "search") {
			t.Fatalf("filtered command %q does not match query", c.name)
		}
	}
}

func TestFilteredCommandsCaseInsensitive(t *testing.T) {
	m := Model{}
	m.CmdPaletteInput.SetValue("HELP")
	got := m.filteredCommands()
	found := false
	for _, c := range got {
		if strings.Contains(strings.ToLower(c.name), "help") {
			found = true
		}
	}
	if !found {
		t.Fatal("case-insensitive match for 'HELP' returned no help command")
	}
}

func TestFilteredCommandsNoMatchReturnsEmpty(t *testing.T) {
	m := Model{}
	m.CmdPaletteInput.SetValue("zzz-no-such-command")
	if got := m.filteredCommands(); len(got) != 0 {
		t.Fatalf("expected no matches, got %d", len(got))
	}
}

// ─── command palette key handling ───────────────────────────────────────────

func TestCmdPaletteEscapeCloses(t *testing.T) {
	m := New(&Deps{})
	m.CmdPaletteOpen = true
	m.CmdPaletteInput.Focus()

	updated, _ := m.handleCmdPaletteKeys(tea.KeyMsg{Type: tea.KeyEsc})
	result := updated.(Model)
	if result.CmdPaletteOpen {
		t.Fatal("esc should close the palette")
	}
}

func TestCmdPaletteDownThenUpBoundsCursor(t *testing.T) {
	m := New(&Deps{})
	m.CmdPaletteOpen = true
	total := len(m.filteredCommands())

	// Move down to the last item.
	for i := 0; i < total+5; i++ {
		u, _ := m.handleCmdPaletteKeys(tea.KeyMsg{Type: tea.KeyDown})
		m = u.(Model)
	}
	if m.CmdPaletteCursor != total-1 {
		t.Fatalf("after many downs cursor = %d, want %d", m.CmdPaletteCursor, total-1)
	}

	// Move back up to the first item.
	for i := 0; i < total+5; i++ {
		u, _ := m.handleCmdPaletteKeys(tea.KeyMsg{Type: tea.KeyUp})
		m = u.(Model)
	}
	if m.CmdPaletteCursor != 0 {
		t.Fatalf("after many ups cursor = %d, want 0", m.CmdPaletteCursor)
	}
}

func TestCmdPaletteEnterExecutesSelectedCommand(t *testing.T) {
	m := New(&Deps{})
	m.CmdPaletteOpen = true
	// Default cursor 0 selects the first command ("Search memories").
	updated, _ := m.handleCmdPaletteKeys(tea.KeyMsg{Type: tea.KeyEnter})
	result := updated.(Model)
	if result.CmdPaletteOpen {
		t.Fatal("enter should close the palette after executing")
	}
	if result.Screen != ScreenSearch {
		t.Fatalf("screen = %v, want ScreenSearch", result.Screen)
	}
}

func TestCmdPaletteTypingResetsCursor(t *testing.T) {
	m := New(&Deps{})
	m.CmdPaletteOpen = true
	m.CmdPaletteCursor = 3

	// Typing a rune resets the cursor to 0.
	updated, _ := m.handleCmdPaletteKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	result := updated.(Model)
	if result.CmdPaletteCursor != 0 {
		t.Fatalf("cursor = %d after typing, want 0", result.CmdPaletteCursor)
	}
}

// ─── item FilterValue contracts ──────────────────────────────────────────────

func TestObservationItemFilterValueIncludesFields(t *testing.T) {
	item := observationItem{obs: &domain.Observation{
		Title: "TitleX", Content: "BodyY", Type: "decision", Project: "cortex",
	}}
	fv := item.FilterValue()
	for _, want := range []string{"TitleX", "BodyY", "decision", "cortex"} {
		if !strings.Contains(fv, want) {
			t.Fatalf("FilterValue %q missing %q", fv, want)
		}
	}
}

func TestSearchResultItemFilterValueIncludesFields(t *testing.T) {
	item := searchResultItem{result: &domain.SearchResult{
		Observation: domain.Observation{Title: "STitle", Content: "SBody"},
	}}
	fv := item.FilterValue()
	if !strings.Contains(fv, "STitle") || !strings.Contains(fv, "SBody") {
		t.Fatalf("FilterValue %q missing title/content", fv)
	}
}

func TestSessionItemFilterValueIncludesFields(t *testing.T) {
	item := sessionItem{session: &session.SessionStats{
		Session: &domain.Session{Project: "proj-x", Summary: "summarized"},
	}}
	fv := item.FilterValue()
	if !strings.Contains(fv, "proj-x") || !strings.Contains(fv, "summarized") {
		t.Fatalf("FilterValue %q missing project/summary", fv)
	}
}

func TestGraphItemFilterValueIncludesFields(t *testing.T) {
	item := graphItem{obs: &domain.Observation{Title: "GTitle", Content: "GBody"}}
	fv := item.FilterValue()
	if !strings.Contains(fv, "GTitle") || !strings.Contains(fv, "GBody") {
		t.Fatalf("FilterValue %q missing title/content", fv)
	}
}

// ─── delete confirmation flow (search results) ──────────────────────────────

func TestSearchResultsConfirmDeleteYesDispatchesDeleteCommand(t *testing.T) {
	m := New(&Deps{})
	m.Screen = ScreenSearchResults
	m.ConfirmDelete = true
	m.ConfirmDeleteID = 42

	updated, cmd := m.handleSearchResultsKeys("y")
	result := updated.(Model)
	if result.ConfirmDelete {
		t.Fatal("confirm flag should clear after 'y'")
	}
	if cmd == nil {
		t.Fatal("expected a delete command to be dispatched on 'y'")
	}
	msg := cmd()
	delMsg, ok := msg.(deleteObservationMsg)
	if !ok {
		t.Fatalf("dispatched message type = %T, want deleteObservationMsg", msg)
	}
	if delMsg.id != 42 {
		t.Fatalf("delete message id = %d, want 42", delMsg.id)
	}
}

func TestSearchResultsConfirmDeleteNoCancelsWithoutCommand(t *testing.T) {
	m := New(&Deps{})
	m.Screen = ScreenSearchResults
	m.ConfirmDelete = true
	m.ConfirmDeleteID = 42

	updated, cmd := m.handleSearchResultsKeys("n")
	result := updated.(Model)
	if result.ConfirmDelete {
		t.Fatal("confirm flag should clear after 'n'")
	}
	if cmd != nil {
		t.Fatalf("no command expected on cancel, got %T", cmd)
	}
}

func TestSearchResultsConfirmDeleteEscapeCancels(t *testing.T) {
	m := New(&Deps{})
	m.Screen = ScreenSearchResults
	m.ConfirmDelete = true
	m.ConfirmDeleteID = 42

	updated, cmd := m.handleSearchResultsKeys("esc")
	result := updated.(Model)
	if result.ConfirmDelete {
		t.Fatal("confirm flag should clear after 'esc'")
	}
	if cmd != nil {
		t.Fatalf("no command expected on escape-cancel, got %T", cmd)
	}
}

// ─── archive unarchive key dispatches command ───────────────────────────────

func TestArchiveUnarchiveKeyDispatchesCommand(t *testing.T) {
	m := New(&Deps{})
	m.Screen = ScreenArchive
	m.ArchiveList.SetItems([]list.Item{
		observationItem{obs: &domain.Observation{ID: 77, Title: "Archived", Type: "manual"}},
	})

	updated, cmd := m.handleArchiveKeys("u")
	if cmd == nil {
		t.Fatal("expected an unarchive command to be dispatched on 'u'")
	}
	msg := cmd()
	unarchMsg, ok := msg.(unarchiveObservationMsg)
	if !ok {
		t.Fatalf("dispatched message type = %T, want unarchiveObservationMsg", msg)
	}
	if unarchMsg.id != 77 {
		t.Fatalf("unarchive message id = %d, want 77", unarchMsg.id)
	}

	// The unarchive key dispatches a fire-and-forget command; the synchronous
	// view state must remain stable until the async message arrives: the screen
	// stays on Archive, the list retains its items, and no confirmation flow is
	// triggered.
	result := updated.(Model)
	if result.Screen != ScreenArchive {
		t.Fatalf("screen = %v, want ScreenArchive (unarchive must not navigate)", result.Screen)
	}
	if result.ConfirmDelete {
		t.Fatal("ConfirmDelete must remain false; unarchive is not a confirmation flow")
	}
	if items := result.ArchiveList.Items(); len(items) != 1 {
		t.Fatalf("archive list items = %d, want 1 (no synchronous mutation)", len(items))
	}
}
