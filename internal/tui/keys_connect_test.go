package tui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// connectTestServer is a real HTTP server exposing the three probed endpoints,
// so the connectivity test performs genuine round trips rather than mocks.
func connectTestServer(t *testing.T, tokenOK bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/api/me", func(w http.ResponseWriter, _ *http.Request) {
		if !tokenOK {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/sync/changes", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func driveConnect(t *testing.T, m Model, keys ...string) Model {
	t.Helper()
	for _, key := range keys {
		updated, _ := m.handleConnectKeys(key)
		next, ok := updated.(Model)
		if !ok {
			t.Fatalf("handleConnectKeys(%q) did not return a Model", key)
		}
		m = next
	}
	return m
}

func openConnect(t *testing.T, cfgDeps *Deps) Model {
	t.Helper()
	updated, _ := New(cfgDeps).openConnectFlow()
	m, ok := updated.(Model)
	if !ok {
		t.Fatal("openConnectFlow did not return a Model")
	}
	if m.Screen != ScreenConnect || m.ConnectStep != ConnectStepURL {
		t.Fatalf("flow opened at screen=%v step=%v", m.Screen, m.ConnectStep)
	}
	return m
}

// TestConnectFlowStepsAndBackNav covers acceptance 1: the three wireframe steps
// render and esc walks back a step.
func TestConnectFlowStepsAndBackNav(t *testing.T) {
	srv := connectTestServer(t, true)
	m := openConnect(t, &Deps{Config: testLocalConfig(t)})
	m.ConnectURL.SetValue(srv.URL)

	step1 := m.viewConnect()
	for _, want := range []string{
		"Connect to server", "step 1 of 3", "Server URL", "Mode after connect",
		"Hybrid sync", "Remote MCP proxy", "disables background sync",
		"[enter] Continue", "[esc] Cancel",
	} {
		if !strings.Contains(step1, want) {
			t.Fatalf("step 1 missing %q:\n%s", want, step1)
		}
	}

	m = driveConnect(t, m, "enter")
	step2 := m.viewConnect()
	for _, want := range []string{"step 2 of 3", "Bearer token", "http.token", "leave this empty"} {
		if !strings.Contains(step2, want) {
			t.Fatalf("step 2 missing %q:\n%s", want, step2)
		}
	}

	m = driveConnect(t, m, "esc")
	if m.ConnectStep != ConnectStepURL {
		t.Fatalf("esc did not return to step 1 (step=%v)", m.ConnectStep)
	}
	m = driveConnect(t, m, "enter", "enter")
	if m.ConnectStep != ConnectStepTest {
		t.Fatalf("advance did not reach step 3 (step=%v)", m.ConnectStep)
	}
	step3 := m.viewConnect()
	for _, want := range []string{
		"step 3 of 3", "✓ server reachable", "✓ token accepted",
		"sync endpoint /api/sync/changes", "[enter] Save & connect", "[esc] Back",
	} {
		if !strings.Contains(step3, want) {
			t.Fatalf("step 3 missing %q:\n%s", want, step3)
		}
	}
	if m = driveConnect(t, m, "esc"); m.ConnectStep != ConnectStepToken {
		t.Fatalf("esc did not return to step 2 (step=%v)", m.ConnectStep)
	}
}

// TestConnectFlowProxyModeStatesConsequence covers acceptance 2: proxy mode
// states its consequence and the written config matches the stated choice.
func TestConnectFlowProxyModeStatesConsequence(t *testing.T) {
	srv := connectTestServer(t, true)
	cfg := testLocalConfig(t)
	m := openConnect(t, &Deps{Config: cfg})
	m.ConnectURL.SetValue(srv.URL)
	m.ConnectFocus = 1
	m.focusConnectField()

	m = driveConnect(t, m, " ")
	if !m.ConnectProxyMode {
		t.Fatal("space did not select Remote MCP proxy")
	}
	if view := m.viewConnect(); !strings.Contains(view, "disables background sync") {
		t.Fatalf("proxy consequence not stated:\n%s", view)
	}

	m.ConnectToken.SetValue("ctx_proxy")
	m = driveConnect(t, m, "enter", "enter", "enter")
	if !m.ConnectCommitted {
		t.Fatal("proxy connect was not committed")
	}
	if cfg.Sync.Enabled || cfg.Sync.URL != "" {
		t.Fatalf("proxy wrote sync config (enabled=%v url=%q)", cfg.Sync.Enabled, cfg.Sync.URL)
	}
	if !cfg.MCP.Remote.Enabled || cfg.MCP.Remote.URL != srv.URL+"/mcp" {
		t.Fatalf("proxy MCP config = enabled:%v url:%q", cfg.MCP.Remote.Enabled, cfg.MCP.Remote.URL)
	}
	if !strings.Contains(m.ConnectSummary, "background sync disabled") {
		t.Fatalf("summary not honest about proxy: %q", m.ConnectSummary)
	}
}

// TestConnectFlowFailedTestWritesNothing covers acceptance 3: a failing test
// commits nothing and saves no sync configuration.
func TestConnectFlowFailedTestWritesNothing(t *testing.T) {
	srv := connectTestServer(t, false)
	cfg := testLocalConfig(t)
	path := cfg.LoadedFrom
	m := openConnect(t, &Deps{Config: cfg})
	m.ConnectURL.SetValue(srv.URL)
	m.ConnectToken.SetValue("ctx_bad")

	m = driveConnect(t, m, "enter", "enter")
	if m.ConnectResult == nil || m.ConnectResult.OK {
		t.Fatalf("failing server reported ok=%v", m.ConnectResult)
	}
	view := m.viewConnect()
	for _, want := range []string{"✗ token rejected", "(401)", "Nothing was saved"} {
		if !strings.Contains(view, want) {
			t.Fatalf("failure view missing %q:\n%s", want, view)
		}
	}
	// enter retests rather than committing a non-passing result.
	m = driveConnect(t, m, "enter")
	if m.ConnectCommitted {
		t.Fatal("a failed test was committed")
	}
	if cfg.Sync.Enabled || cfg.Sync.URL != "" || cfg.MCP.Remote.Enabled {
		t.Fatalf("failed test wrote sync config: enabled=%v url=%q mcp=%v",
			cfg.Sync.Enabled, cfg.Sync.URL, cfg.MCP.Remote.Enabled)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sync:") || strings.Contains(string(raw), "mcp:") {
		t.Fatalf("failed test wrote to disk:\n%s", raw)
	}
}

// TestConnectFlowSuccessSavesAndReportsLoopState covers acceptance 4: a passing
// commit saves config, preserves an untouched token, and reports the real loop
// state.
func TestConnectFlowSuccessSavesAndReportsLoopState(t *testing.T) {
	srv := connectTestServer(t, true)
	cfg := testLocalConfig(t)
	cfg.HTTP.Token = "ctx_existing"
	m := openConnect(t, &Deps{Config: cfg})
	m.ConnectURL.SetValue(srv.URL)

	m = driveConnect(t, m, "enter", "enter", "enter")
	if !m.ConnectCommitted {
		t.Fatalf("hybrid connect not committed (error=%q)", m.ConnectError)
	}
	if !cfg.Sync.Enabled || cfg.Sync.URL != srv.URL {
		t.Fatalf("hybrid sync config = enabled:%v url:%q", cfg.Sync.Enabled, cfg.Sync.URL)
	}
	if cfg.HTTP.Token != "ctx_existing" {
		t.Fatalf("empty token field erased http.token: %q", cfg.HTTP.Token)
	}
	if chip := m.connectSyncChip(); chip.State != SyncRestartRequired {
		t.Fatalf("post-commit chip = %v, want restart-required", chip.State)
	}
	if !strings.Contains(m.ConnectSummary, "restart required") {
		t.Fatalf("summary not honest about loop state: %q", m.ConnectSummary)
	}

	cfg2 := testLocalConfig(t)
	cfg2.HTTP.Token = "ctx_old"
	m2 := openConnect(t, &Deps{Config: cfg2})
	m2.ConnectURL.SetValue(srv.URL)
	m2.ConnectToken.SetValue("ctx_new")
	m2 = driveConnect(t, m2, "enter", "enter", "enter")
	if cfg2.HTTP.Token != "ctx_new" {
		t.Fatalf("entered token not applied: %q", cfg2.HTTP.Token)
	}
}

// TestConnectFlowResultLineStates proves both honest post-commit outcomes and
// the proxy variant render the status.go vocabulary.
func TestConnectFlowResultLineStates(t *testing.T) {
	if got := connectResultLine(SyncChip{State: SyncRunning, Interval: 30 * time.Second}, false); !strings.Contains(got, "background sync running (interval 30s)") {
		t.Fatalf("running line = %q", got)
	}
	if got := connectResultLine(SyncChip{State: SyncRestartRequired}, false); !strings.Contains(got, "restart required") {
		t.Fatalf("restart line = %q", got)
	}
	if got := connectResultLine(SyncChip{State: SyncError, Reason: "no sync token configured"}, false); !strings.Contains(got, "cannot start") {
		t.Fatalf("error line = %q", got)
	}
	if got := connectResultLine(SyncChip{State: SyncDisabled}, true); !strings.Contains(got, "background sync disabled") {
		t.Fatalf("proxy line = %q", got)
	}
}

// TestConnectFlowKeymapRegistered keeps the registry and the declared wizard
// keymap in agreement (REQ-NAV-001/004).
func TestConnectFlowKeymapRegistered(t *testing.T) {
	declared := connectKeymap()
	reg, ok := keymapRegistry.keymaps[ScreenConnect]
	if !ok {
		t.Fatal("ScreenConnect has no registered keymap")
	}
	if len(reg.Bindings) != len(declared.Bindings) {
		t.Fatalf("registry has %d bindings, declared %d", len(reg.Bindings), len(declared.Bindings))
	}
	m := openConnect(t, &Deps{Config: testLocalConfig(t)})
	for _, binding := range declared.Bindings {
		if _, _, handled := keymapRegistry.Dispatch(m, binding.Key); !handled {
			t.Fatalf("binding %q is not dispatchable on ScreenConnect", binding.Key)
		}
	}
}
