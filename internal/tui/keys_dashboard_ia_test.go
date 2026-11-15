package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/app"
)

// TestDashboardIAMenuDestinationsUnique enforces REQ-NAV-005: no two menu
// entries may navigate to the same screen.
func TestDashboardIAMenuDestinationsUnique(t *testing.T) {
	seen := map[Screen]string{}
	for _, item := range flattenDashboardItems() {
		if other, ok := seen[item.dest]; ok {
			t.Fatalf("entries %q and %q share destination %v", other, item.label, item.dest)
		}
		seen[item.dest] = item.label
	}
}

// TestDashboardIAExactlyOneConfigAndConnect enforces the dedupe half of
// REQ-NAV-005: exactly one Configuration entry and one Connect entry.
func TestDashboardIAExactlyOneConfigAndConnect(t *testing.T) {
	var config, connect int
	for _, item := range flattenDashboardItems() {
		switch {
		case item.action == dashboardConnect:
			connect++
		case item.dest == ScreenLocalConfig:
			config++
		}
	}
	if config != 1 {
		t.Fatalf("Configuration entries = %d, want 1", config)
	}
	if connect != 1 {
		t.Fatalf("Connect entries = %d, want 1", connect)
	}
}

// TestDashboardIAGroupedSections enforces the MAIN/SYSTEM grouping invariant.
func TestDashboardIAGroupedSections(t *testing.T) {
	sections := dashboardMenu()
	if len(sections) != 2 || sections[0].title != "MAIN" || sections[1].title != "SYSTEM" {
		t.Fatalf("expected MAIN/SYSTEM sections, got %+v", sections)
	}
	for _, item := range sections[0].items {
		if item.action != dashboardNavigate {
			t.Fatalf("MAIN entry %q is not a content screen", item.label)
		}
	}
}

// TestDashboardIALabelsAvoidJargon enforces the de-jargoned label rule.
func TestDashboardIALabelsAvoidJargon(t *testing.T) {
	banned := []string{
		"Neural", "Command Deck", "Deck", "HippoRAG", "PageRank", "AST",
		"Modular Settings", "Control Hub", "Control &", "Zero-CGO", "SQLite", "WAL",
	}
	for _, item := range flattenDashboardItems() {
		text := item.label + " " + item.hint
		for _, term := range banned {
			if strings.Contains(text, term) {
				t.Fatalf("menu entry %q contains internal jargon %q", item.label, term)
			}
		}
	}
}

// TestDashboardIASyncChipStates asserts the header chip text matches the real
// sync state for all five honest states (REQ-SYNC-003).
func TestDashboardIASyncChipStates(t *testing.T) {
	cases := []struct {
		name        string
		status      app.SyncStatus
		hasEndpoint bool
		interval    time.Duration
		want        string
	}{
		{"running", app.SyncStatus{Configured: true, Running: true, TokenSet: true}, true, 30 * time.Second, "Sync: running · every 30s"},
		{"disabled", app.SyncStatus{}, true, 0, "Sync: disabled"},
		{"not-configured", app.SyncStatus{}, false, 0, "Sync: not configured"},
		{"restart-required", app.SyncStatus{Configured: true, TokenSet: true}, true, 0, "Sync: restart required"},
		{"error", app.SyncStatus{Configured: true}, true, 0, "Sync: error — no sync token configured"},
	}
	for _, tc := range cases {
		chip := dashboardSyncChipFor(tc.status, tc.hasEndpoint, tc.interval)
		if got := chip.Text(); got != tc.want {
			t.Fatalf("%s: chip text = %q, want %q", tc.name, got, tc.want)
		}
		if chip.IsSuccess() != tc.status.Running {
			t.Fatalf("%s: IsSuccess() = %v, want %v", tc.name, chip.IsSuccess(), tc.status.Running)
		}
	}
}

// TestDashboardIARendersHonestChip verifies the dashboard header carries the
// chip and never claims success for an unconfigured subsystem.
func TestDashboardIARendersHonestChip(t *testing.T) {
	m := newTestModel(120, 40)
	out := m.viewDashboard()
	if !strings.Contains(out, "Sync: not configured") {
		t.Fatalf("dashboard header should show the honest chip, got %q", out)
	}
	if strings.Contains(out, "Sync: running") {
		t.Fatalf("dashboard must not claim a running loop it does not have, got %q", out)
	}
}

// TestDashboardIARendersGroupedMenu verifies the grouped, de-jargoned menu is
// actually rendered.
func TestDashboardIARendersGroupedMenu(t *testing.T) {
	m := newTestModel(120, 40)
	out := m.viewDashboard()
	for _, want := range []string{"MAIN", "SYSTEM", "Knowledge graph", "Configuration", "Connect to server"} {
		if !strings.Contains(out, want) {
			t.Fatalf("dashboard menu missing %q", want)
		}
	}
}

// TestDashboardIACountsStillRender protects the counts line after the sparkline
// data loading was dropped (design D5).
func TestDashboardIACountsStillRender(t *testing.T) {
	m := newTestModel(120, 40)
	m.Stats = &combinedStats{TotalObservations: 1204, TotalSessions: 87, TotalEdges: 3410, Projects: []string{"cortex"}}
	out := m.viewDashboard()
	for _, want := range []string{"1204", "87", "3410", "knowledge links"} {
		if !strings.Contains(out, want) {
			t.Fatalf("dashboard counts missing %q", want)
		}
	}
}

// TestDashboardIASparklineQueriesRemoved asserts the sparkline command no longer
// issues store queries.
func TestDashboardIASparklineQueriesRemoved(t *testing.T) {
	msg, ok := loadActivityData(nil)().(activityDataMsg)
	if !ok {
		t.Fatalf("loadActivityData returned %T, want activityDataMsg", msg)
	}
	if len(msg.daily) != 7 {
		t.Fatalf("daily length = %d, want 7", len(msg.daily))
	}
	for i, v := range msg.daily {
		if v != 0 {
			t.Fatalf("daily[%d] = %d, want 0 (no queries)", i, v)
		}
	}
}

// TestDashboardIAInertKeys asserts u and P produce no state change or toast
// unless an update is genuinely pending (REQ-NAV-002).
func TestDashboardIAInertKeys(t *testing.T) {
	m := newTestModel(120, 40)
	m.Screen = ScreenDashboard
	for _, key := range []string{"P", "u", "U"} {
		out, cmd := m.handleKeyPress(key)
		res := out.(Model)
		if cmd != nil {
			t.Fatalf("key %q returned a command without a pending update", key)
		}
		if res.Screen != ScreenDashboard || res.ToastMessage != "" || res.Cursor != m.Cursor {
			t.Fatalf("key %q mutated state: screen=%v toast=%q cursor=%d", key, res.Screen, res.ToastMessage, res.Cursor)
		}
	}
}

// TestDashboardIAUpdateKeyActsWhenPending preserves the accepted self-update
// path: u/U revive performSelfUpdateCmd only when an update is pending.
func TestDashboardIAUpdateKeyActsWhenPending(t *testing.T) {
	m := newTestModel(120, 40)
	m.Screen = ScreenDashboard
	m.UpdateResult = &updateResult{Latest: "v9.9.9"}

	out, cmd := m.handleKeyPress("u")
	if cmd == nil {
		t.Fatal("u with a pending update should arm the self-update command")
	}
	if res := out.(Model); !strings.Contains(res.ToastMessage, "v9.9.9") {
		t.Fatalf("expected install toast naming the version, got %q", res.ToastMessage)
	}
}
