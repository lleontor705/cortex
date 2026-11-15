package tui

import (
	"strings"
	"testing"
)

// onboardingModel builds a Dashboard model with the given observation/session
// counts and a nil Config (sync unconfigured) unless overridden by the caller.
func onboardingModel(obs, sessions int) Model {
	m := New(&Deps{Version: "test"})
	m.Width, m.Height = 120, 40
	m.Stats = &combinedStats{TotalObservations: obs, TotalSessions: sessions}
	return m
}

// TestOnboardingEmptyStates covers REQ-SYNC-005: first-run and empty-DB states
// render with their routed actions, and the unconfigured-sync prompt appears
// only when sync is unconfigured, routes to Connect, and stays hidden after a
// skip until the next launch.
func TestOnboardingEmptyStates(t *testing.T) {
	defer resetOnboardingSyncPrompt()

	t.Run("first run renders welcome with three routed actions", func(t *testing.T) {
		resetOnboardingSyncPrompt()
		m := onboardingModel(0, 0)
		if got := m.onboardingEmptyState(); got != EmptyStateFirstRun {
			t.Fatalf("onboardingEmptyState() = %v, want first-run", got)
		}
		view := m.viewDashboard()
		for _, want := range []string{
			"Welcome to Cortex", "Create your first memory", "Set up an agent plugin", "Connect to a server",
		} {
			if !strings.Contains(view, want) {
				t.Fatalf("first-run view missing %q:\n%s", want, view)
			}
		}
		if n := strings.Count(view, "▸"); n != 1 {
			t.Fatalf("first-run must expose exactly one primary action, found %d", n)
		}

		// Each action routes through the registry's real dispatch path.
		create := routeOnboarding(t, m, "enter")
		if !create.NewObsModalOpen {
			t.Fatal("primary enter did not open the create-memory modal")
		}
		if got := routeOnboarding(t, m, "s"); got.Screen != ScreenSetup {
			t.Fatalf("s routed to %v, want Setup", got.Screen)
		}
		if got := routeOnboarding(t, m, "c"); got.Screen != ScreenConnect {
			t.Fatalf("c routed to %v, want Connect", got.Screen)
		}
	})

	t.Run("empty DB renders create and archive", func(t *testing.T) {
		resetOnboardingSyncPrompt()
		m := onboardingModel(0, 3)
		if got := m.onboardingEmptyState(); got != EmptyStateEmptyDB {
			t.Fatalf("onboardingEmptyState() = %v, want empty-db", got)
		}
		view := m.viewDashboard()
		for _, want := range []string{"No observations yet", "Create your first memory", "View archive"} {
			if !strings.Contains(view, want) {
				t.Fatalf("empty-db view missing %q:\n%s", want, view)
			}
		}
		if n := strings.Count(view, "▸"); n != 1 {
			t.Fatalf("empty-db must expose exactly one primary action, found %d", n)
		}
		if got := routeOnboarding(t, m, "n"); !got.NewObsModalOpen {
			t.Fatal("n did not open the create-memory modal")
		}
		if got := routeOnboarding(t, m, "a"); got.Screen != ScreenArchive {
			t.Fatalf("a routed to %v, want Archive", got.Screen)
		}
	})

	t.Run("unconfigured sync prompt routes to connect then skip hides it", func(t *testing.T) {
		resetOnboardingSyncPrompt()
		m := onboardingModel(5, 1)
		if !m.syncPromptVisible() {
			t.Fatal("unconfigured sync should show the prompt")
		}
		view := m.viewDashboard()
		for _, want := range []string{"Sync: not configured", "[c] Connect to a server", "[x] Skip — stay local-only"} {
			if !strings.Contains(view, want) {
				t.Fatalf("sync prompt missing %q:\n%s", want, view)
			}
		}
		if got := routeOnboarding(t, m, "c"); got.Screen != ScreenConnect {
			t.Fatalf("c routed to %v, want Connect", got.Screen)
		}
		skipped := routeOnboarding(t, m, "x")
		if skipped.syncPromptVisible() {
			t.Fatal("skip did not hide the prompt")
		}
		if strings.Contains(skipped.viewDashboard(), "stay local-only") {
			t.Fatal("skipped prompt still rendered")
		}
		resetOnboardingSyncPrompt()
		if !m.syncPromptVisible() {
			t.Fatal("prompt must return on the next launch")
		}
	})

	t.Run("no prompt when sync is configured", func(t *testing.T) {
		resetOnboardingSyncPrompt()
		cfg := testLocalConfig(t)
		cfg.Sync.Enabled, cfg.Sync.URL = true, "https://sync.example.test"
		m := New(&Deps{Config: cfg})
		m.Width, m.Height = 120, 40
		m.Stats = &combinedStats{TotalObservations: 5}
		if m.syncUnconfigured() {
			t.Fatal("configured sync reported as unconfigured")
		}
		if m.syncPromptVisible() {
			t.Fatal("prompt visible although sync is configured")
		}
		if strings.Contains(m.viewDashboard(), "stay local-only") {
			t.Fatal("prompt rendered although sync is configured")
		}
		// The guarded onboarding bindings stay hidden, so the base Dashboard
		// keys (search/configuration) are not shadowed.
		if _, _, handled := keymapRegistry.Dispatch(m, "x"); handled {
			t.Fatal("skip binding dispatched although the prompt is hidden")
		}
	})
}

// routeOnboarding drives one key through the live registry and returns the
// resulting model, failing fast if the key is not dispatchable on the Dashboard.
func routeOnboarding(t *testing.T, m Model, key string) Model {
	t.Helper()
	out, _, handled := keymapRegistry.Dispatch(m, key)
	if !handled {
		t.Fatalf("key %q is not dispatchable on the Dashboard", key)
	}
	res, ok := out.(Model)
	if !ok {
		t.Fatalf("key %q did not return a Model", key)
	}
	return res
}
