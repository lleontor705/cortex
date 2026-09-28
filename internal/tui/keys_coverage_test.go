package tui

import (
	"errors"
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

func isModel(v tea.Model) bool {
	_, ok := v.(Model)
	return ok
}

func TestScreenKeyHandlerBattery(t *testing.T) {
	cases := []struct {
		name string
		keys []string
		fn   func(Model, string) (tea.Model, tea.Cmd)
		seed func(*Model)
	}{
		{"search", []string{"i", "/", "esc", "q", "z"}, Model.handleSearchKeys, nil},
		{"searchResults", []string{"j", "enter", "t", "d", "p", "/", "s", "f", "esc", "g", "z"}, Model.handleSearchResultsKeys, func(m *Model) { seedSearchItems(m); m.Stats = &combinedStats{Projects: []string{"a", "b"}} }},
		{"searchResultsConfirm", []string{"y", "n", "esc"}, Model.handleSearchResultsKeys, func(m *Model) { seedSearchItems(m); m.ConfirmDelete = true }},
		{"recent", []string{"j", "k", "enter", "d", "esc", "q", "z"}, Model.handleRecentKeys, seedRecentItems},
		{"recentConfirm", []string{"y", "n"}, Model.handleRecentKeys, func(m *Model) { m.ConfirmDelete = true }},
		{"observationDetail", []string{"up", "down", "pgup", "pgdown", "t", "g", "s", "d", "esc", "q"}, Model.handleObservationDetailKeys, func(m *Model) {
			m.SelectedObservation = &domain.Observation{ID: 9, SessionID: "sess-1", Title: "T", Content: "c"}
			m.DetailViewport = viewport.New(80, 10)
			m.DetailViewport.SetContent(strings.Repeat("line\n", 40))
		}},
		{"timeline", []string{"up", "down", "k", "j", "esc", "q"}, Model.handleTimelineKeys, nil},
		{"sessions", []string{"enter", "esc", "q"}, Model.handleSessionsKeys, seedSessionItems},
		{"sessionDetail", []string{"up", "down", "k", "j", "enter", "t", "esc", "q"}, Model.handleSessionDetailKeys, func(m *Model) {
			seedSessionItems(m)
			m.SelectedSessionIdx = 0
			m.SessionObservations = []*domain.Observation{newTestObs(1, "decision", "a"), newTestObs(2, "bugfix", "b")}
			m.Cursor = 1
		}},
		{"graph", []string{"enter", "r", "esc", "q"}, Model.handleGraphKeys, seedGraphItems},
		{"archive", []string{"enter", "u", "d", "f", "esc", "q", "z"}, Model.handleArchiveKeys, func(m *Model) { seedArchiveItems(m); m.Stats = &combinedStats{Projects: []string{"a"}} }},
		{"archiveConfirm", []string{"y", "n"}, Model.handleArchiveKeys, func(m *Model) { m.ConfirmDelete = true }},
		{"health", []string{"up", "down", "k", "j", "tab", "enter", "esc", "q"}, Model.handleHealthKeys, nil},
		{"help", []string{"esc", "q", "?"}, Model.handleHelpKeys, nil},
		{"embedding", []string{"up", "down", "k", "j", "left", "h", "right", "l", "enter", " ", "esc", "q", "r", "z"}, Model.handleEmbeddingConfigKeys, nil},
		{"localConfig", []string{"up", "down", "tab", "shift+tab", "left", "right", "h", "l", "enter", " ", "esc", "z"}, Model.handleLocalConfigKeys, nil},
		{"dashboard", []string{"up", "down", "k", "j", "enter", " ", "d", "g", "esc", "q", "z"}, Model.handleDashboardKeys, nil},
	}
	for _, tc := range cases {
		for _, key := range tc.keys {
			m := newTestModel(120, 40)
			if tc.seed != nil {
				tc.seed(&m)
			}
			if out, _ := tc.fn(m, key); !isModel(out) {
				t.Fatalf("%s key %q: %T", tc.name, key, out)
			}
		}
	}
}

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

func TestEmbeddingConfigKeyBranches(t *testing.T) {
	states := []func(*Model){
		func(m *Model) {}, func(m *Model) { m.EmbCfgFocusField = 1 }, func(m *Model) { m.EmbCfgFocusField = 4 },
		func(m *Model) { m.EmbCfgPulling = true }, func(m *Model) { m.EmbCfgStarting = true },
		func(m *Model) { m.EmbCfgSaving = true }, func(m *Model) { m.EmbCfgReindexing = true },
		func(m *Model) { m.EmbCfgSaved, m.EmbCfgReindexWarning = true, true },
		func(m *Model) { m.EmbCfgSaved, m.EmbCfgProvider, m.EmbCfgOllamaChecked = true, 1, true },
		func(m *Model) {
			m.EmbCfgSaved, m.EmbCfgProvider, m.EmbCfgOllamaChecked, m.EmbCfgOllamaRunning = true, 1, true, true
		},
		func(m *Model) { m.EmbCfgSaved = true },
	}
	for si, set := range states {
		for _, key := range []string{"s", "p", "x", "r", "up", "down", "left", "right", "h", "l", " ", "enter", "esc", "q", "z"} {
			m := newTestModel(120, 40)
			set(&m)
			if out, _ := m.handleEmbeddingConfigKeys(key); !isModel(out) {
				t.Fatalf("state %d key %q: %T", si, key, out)
			}
		}
	}
	for _, msg := range []tea.KeyMsg{{Type: tea.KeyEnter}, {Type: tea.KeyEsc}, {Type: tea.KeyRunes, Runes: []rune("m")}} {
		m := newTestModel(120, 40)
		if out, _ := m.handleEmbeddingModelInput(msg); !isModel(out) {
			t.Fatalf("embedding model input %q: %T", msg.String(), out)
		}
	}
}

func TestModalKeyFlows(t *testing.T) {
	keyMsgs := []tea.KeyMsg{
		{Type: tea.KeyEsc}, {Type: tea.KeyTab}, {Type: tea.KeyShiftTab}, {Type: tea.KeyEnter}, {Type: tea.KeySpace}, {Type: tea.KeyLeft},
		{Type: tea.KeyRight}, {Type: tea.KeyCtrlS}, {Type: tea.KeyCtrlP}, {Type: tea.KeyCtrlN}, {Type: tea.KeyUp}, {Type: tea.KeyDown},
		{Type: tea.KeyRunes, Runes: []rune("u")},
	}
	for field := 0; field <= 2; field++ {
		for _, msg := range keyMsgs {
			m := newTestModel(120, 40)
			m.openAuthModal()
			m.AuthFocusField = field
			if out, _ := m.handleAuthModalKeys(msg); !isModel(out) {
				t.Fatalf("auth field %d msg %q: %T", field, msg.String(), out)
			}
		}
	}
	for field := 0; field <= 4; field++ {
		for _, msg := range keyMsgs {
			m := newTestModel(120, 40)
			m.NewObsModalOpen, m.NewObsFocusField = true, field
			if out, _ := m.handleNewObsModalKeys(msg); !isModel(out) {
				t.Fatalf("newobs field %d msg %q: %T", field, msg.String(), out)
			}
		}
	}
	for _, msg := range keyMsgs {
		m := newTestModel(120, 40)
		m.CmdPaletteOpen = true
		if out, _ := m.handleCmdPaletteKeys(msg); !isModel(out) {
			t.Fatalf("palette msg %q: %T", msg.String(), out)
		}
	}
}

func TestGlobalRefreshPreviewAndSearchRouting(t *testing.T) {
	for _, key := range []string{"?", "ctrl+k", "1", "2", "3", "4", "n", "N", "t", "T", "L", "u", "U", "P", "p", "v", "V", "z"} {
		m := newTestModel(120, 40)
		m.Screen, m.Stats = ScreenDashboard, &combinedStats{Projects: []string{"a"}}
		if out, _ := m.handleKeyPress(key); !isModel(out) {
			t.Fatalf("global key %q: %T", key, out)
		}
	}
	for _, s := range []Screen{ScreenDashboard, ScreenRecent, ScreenSearchResults, ScreenSessions, ScreenArchive, ScreenTimeline} {
		m := newTestModel(120, 40)
		m.SearchQuery, m.SearchMode = "q", "code"
		m.refreshScreen(s)
	}
	filter := newTestModel(120, 40)
	filter.Screen = ScreenDashboard
	if _, cmd := filter.cycleProjectFilter(); cmd != nil {
		t.Fatal("no projects should return nil command")
	}
	filter.Stats, filter.FilterProject = &combinedStats{Projects: []string{"a", "b"}}, "b"
	filter.cycleProjectFilter()
	for _, s := range []Screen{ScreenSearchResults, ScreenRecent, ScreenArchive} {
		filter.Screen, filter.SearchQuery, filter.SearchMode = s, "q", "code"
		filter.cycleProjectFilter()
	}
	preview := newTestModel(120, 40)
	preview.Screen = ScreenSearchResults
	preview.SearchListModel.SetItems([]list.Item{searchResultItem{result: &domain.SearchResult{Observation: domain.Observation{ID: 1, Title: "R", Content: "c"}}}})
	preview.updatePreviewContent()
	msgs := []tea.KeyMsg{{Type: tea.KeyEnter}, {Type: tea.KeyTab}, {Type: tea.KeyRunes, Runes: []rune("f")}, {Type: tea.KeyUp}, {Type: tea.KeyDown}, {Type: tea.KeyEsc}, {Type: tea.KeyRunes, Runes: []rune("x")}}
	for _, msg := range msgs {
		m := newTestModel(120, 40)
		m.Screen = ScreenSearch
		m.SearchInput.Focus()
		m.SearchInput.SetValue("query")
		m.SearchHistory, m.SearchHistoryIdx = []string{"a", "b"}, 1
		m.Stats = &combinedStats{Projects: []string{"p"}}
		if out, _ := m.handleSearchInputKeys(msg); !isModel(out) {
			t.Fatalf("search input %q: %T", msg.String(), out)
		}
	}
}

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
		func(m *Model) { m.CmdPaletteOpen = true }, func(m *Model) { m.NewObsModalOpen = true }, func(m *Model) { m.AuthModalOpen = true },
		func(m *Model) { m.Screen = ScreenSearch; m.SearchInput.Focus() },
		func(m *Model) { m.Screen = ScreenEmbeddingConfig; m.EmbCfgModel.Focus() },
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
