package tui

import (
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/lleontor705/cortex/v2/internal/app"
	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/transportpolicy"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// connectProbeTimeout bounds every connectivity probe so a dead endpoint fails
// in bounded time instead of hanging the wizard.
var connectProbeTimeout = 4 * time.Second

// connectCheck is one line of the step-3 connectivity report (design.md W7).
type connectCheck struct {
	Name   string
	OK     bool
	Detail string
}

// connectTestResult is the complete outcome of the step-3 connectivity test.
// OK is true only when every check passed, so a single failure can never be
// mistaken for a successful connection.
type connectTestResult struct {
	URL     string
	Checks  []connectCheck
	OK      bool
	Elapsed time.Duration
}

// connectInputEscapeKeys are the only keys that leave a focused wizard input;
// every other key is typed into the field.
var connectInputEscapeKeys = map[string]bool{"esc": true, "enter": true, "tab": true, "shift+tab": true}

// openConnectFlow enters the guided Connect flow on step 1. It seeds the URL
// from the configured sync/MCP endpoint, never pre-fills the bearer token (an
// empty field preserves the existing http.token per REQ-CFG-004), and selects
// the mode the saved configuration already implies so the stated choice matches
// what will be written.
func (m Model) openConnectFlow() (tea.Model, tea.Cmd) {
	m.ConnectStep = ConnectStepURL
	m.ConnectFocus = 0
	m.ConnectResult = nil
	m.ConnectCommitted = false
	m.ConnectSummary = ""
	m.ConnectError = ""
	m.ConnectURL.Blur()
	m.ConnectToken.Blur()
	m.ConnectToken.SetValue("")
	if m.deps != nil && m.deps.Config != nil {
		cfg := m.deps.Config
		switch {
		case strings.TrimSpace(cfg.Sync.URL) != "":
			m.ConnectURL.SetValue(cfg.Sync.URL)
		case strings.TrimSpace(cfg.MCP.Remote.URL) != "":
			m.ConnectURL.SetValue(strings.TrimSuffix(cfg.MCP.Remote.URL, "/mcp"))
		}
		m.ConnectProxyMode = cfg.MCP.Remote.Enabled
	}
	m.Screen = ScreenConnect
	m.focusConnectField()
	return m, textinput.Blink
}

// handleConnectKeys drives the three steps. While a step input owns the
// keyboard every other key is typed into it; esc walks back a step (cancelling
// on step 1) per design.md W5/W6/W7.
func (m Model) handleConnectKeys(key string) (tea.Model, tea.Cmd) {
	if m.connectInputFocused() && !connectInputEscapeKeys[key] {
		return m.typeConnectKey(key)
	}
	switch key {
	case "esc", "q":
		return m.connectBack()
	case "tab", "shift+tab", "up", "down", "k", "j":
		if m.ConnectStep == ConnectStepURL {
			m.ConnectFocus = (m.ConnectFocus + 1) % 2
			m.focusConnectField()
			return m, textinput.Blink
		}
		return m, nil
	case "left", "right", "h", "l", " ":
		if m.ConnectStep == ConnectStepURL && m.ConnectFocus == 1 {
			m.ConnectProxyMode = !m.ConnectProxyMode
		}
		return m, nil
	case "enter":
		return m.advanceConnect()
	}
	return m, nil
}

func (m Model) advanceConnect() (tea.Model, tea.Cmd) {
	switch m.ConnectStep {
	case ConnectStepURL:
		if strings.TrimSpace(m.ConnectURL.Value()) == "" {
			m.ConnectError = "Server URL is required"
			return m, nil
		}
		m.ConnectError = ""
		m.ConnectStep = ConnectStepToken
		m.ConnectFocus = 0
		m.focusConnectField()
		return m, textinput.Blink
	case ConnectStepToken:
		m.ConnectStep = ConnectStepTest
		m.ConnectURL.Blur()
		m.ConnectToken.Blur()
		return m.runConnectTest(), nil
	case ConnectStepTest:
		if m.ConnectCommitted {
			return m.closeConnect()
		}
		if m.ConnectResult != nil && m.ConnectResult.OK {
			return m.commitConnect()
		}
		// A failed test re-runs instead of committing: nothing is ever written
		// from a non-passing connectivity result (REQ-SYNC-001).
		return m.runConnectTest(), nil
	}
	return m, nil
}

func (m Model) connectBack() (tea.Model, tea.Cmd) {
	switch m.ConnectStep {
	case ConnectStepURL:
		return m.closeConnect()
	case ConnectStepToken:
		m.ConnectStep = ConnectStepURL
		m.ConnectFocus = 0
		m.ConnectError = ""
	case ConnectStepTest:
		m.ConnectStep = ConnectStepToken
		m.ConnectResult = nil
		m.ConnectSummary = ""
		m.ConnectCommitted = false
		m.ConnectError = ""
	}
	m.focusConnectField()
	return m, nil
}

func (m Model) closeConnect() (tea.Model, tea.Cmd) {
	m.Screen = ScreenDashboard
	m.ConnectStep = ConnectStepURL
	m.ConnectFocus = 0
	m.ConnectResult = nil
	m.ConnectCommitted = false
	m.ConnectSummary = ""
	m.ConnectError = ""
	m.ConnectURL.Blur()
	m.ConnectToken.Blur()
	return m, loadStats(m.deps)
}

func (m *Model) focusConnectField() {
	m.ConnectURL.Blur()
	m.ConnectToken.Blur()
	switch m.ConnectStep {
	case ConnectStepURL:
		if m.ConnectFocus == 0 {
			m.ConnectURL.Focus()
		}
	case ConnectStepToken:
		m.ConnectToken.Focus()
	}
}

func (m Model) connectInputFocused() bool {
	return m.ConnectURL.Focused() || m.ConnectToken.Focused()
}

func (m Model) typeConnectKey(key string) (tea.Model, tea.Cmd) {
	switch m.ConnectStep {
	case ConnectStepURL:
		applyConnectKey(&m.ConnectURL, key)
	case ConnectStepToken:
		applyConnectKey(&m.ConnectToken, key)
	}
	return m, nil
}

func applyConnectKey(input *textinput.Model, key string) {
	switch key {
	case "backspace":
		if runes := []rune(input.Value()); len(runes) > 0 {
			input.SetValue(string(runes[:len(runes)-1]))
		}
	case "delete":
		input.SetValue("")
	default:
		if len([]rune(key)) == 1 {
			input.SetValue(input.Value() + key)
		}
	}
}

// effectiveConnectToken is the bearer the connectivity test authenticates with:
// the entered token, else the already-configured http.token so leaving the
// field empty still tests and preserves the existing credential (REQ-CFG-004).
func (m Model) effectiveConnectToken() string {
	if t := strings.TrimSpace(m.ConnectToken.Value()); t != "" {
		return t
	}
	if m.deps != nil && m.deps.Config != nil {
		return strings.TrimSpace(m.deps.Config.HTTP.Token)
	}
	return ""
}

func (m Model) runConnectTest() Model {
	m.ConnectError = ""
	m.ConnectCommitted = false
	m.ConnectSummary = ""
	res := runConnectivityTest(strings.TrimSpace(m.ConnectURL.Value()), m.effectiveConnectToken())
	m.ConnectResult = &res
	if !res.OK {
		m.ConnectSummary = "Result: Not connected. Nothing was saved."
	}
	return m
}

// commitConnect writes the verified connection only after a passing test. The
// proxy choice is written exactly as stated in step 1: proxy disables local
// replication (sync.enabled=false, sync.url cleared) and points MCP at the
// server; hybrid enables the replication loop.
func (m Model) commitConnect() (tea.Model, tea.Cmd) {
	outcome := connectCommit(m.deps, m.ConnectURL.Value(), m.ConnectToken.Value(), m.ConnectProxyMode)
	if outcome.Err != nil {
		m.ConnectError = outcome.Err.Error()
		m.ConnectSummary = "Result: Not connected. Nothing was saved."
		m.ToastMessage = "Connect failed: " + outcome.Err.Error()
		m.ToastType = "error"
		return m, nil
	}
	m.ConnectCommitted = true
	m.ConnectError = ""
	chip := m.connectSyncChip()
	m.ConnectSummary = "Result: " + connectResultLine(chip, m.ConnectProxyMode)
	m.ToastMessage = m.ConnectSummary
	m.ToastType = connectToastType(chip, m.ConnectProxyMode)
	m.UploadToCortex = !m.ConnectProxyMode
	return m, nil
}

// connectCommit is the pure write path shared by the wizard and its tests. It
// returns a SaveResult so the caller reports the honest outcome and never a
// false success (REQ-CFG-003).
func connectCommit(d *Deps, rawURL, rawToken string, proxyMode bool) SaveResult {
	if d == nil || d.Config == nil {
		return SaveResult{Err: fmt.Errorf("config not available")}
	}
	base := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	if base == "" {
		return SaveResult{Err: fmt.Errorf("server URL is required")}
	}
	prev := *d.Config
	next := prev // carries http.token forward unless a new one is supplied

	if token := strings.TrimSpace(rawToken); token != "" {
		next.HTTP.Token = token
	}
	if proxyMode {
		next.Sync.Enabled, next.Sync.URL = false, ""
		next.MCP.Remote.Enabled, next.MCP.Remote.URL = true, base+"/mcp"
	} else {
		next.Sync.Enabled, next.Sync.URL = true, base
		next.MCP.Remote.Enabled = false
	}

	targetPath := prev.LoadedFrom
	if targetPath == "" {
		targetPath = filepath.Join(config.CortexDir(), "cortex.yaml")
	}
	if err := config.Validate(&next); err != nil {
		return SaveResult{Err: err}
	}
	if err := config.Save(&next, targetPath); err != nil {
		return SaveResult{Err: err}
	}
	next.LoadedFrom = targetPath
	*d.Config = next
	d.Config = &next

	if d.App != nil {
		if err := d.App.ReloadConfig(); err != nil {
			return SaveResult{Err: fmt.Errorf("config saved but reload failed: %w", err)}
		}
		d.Config = d.App.Config
	}
	return SaveResult{}
}

// connectSyncChip derives the honest post-commit state from the live sync
// subsystem when an App is wired, else from the configuration the save just
// wrote. Only a genuinely running loop yields the running chip.
func (m Model) connectSyncChip() SyncChip {
	hybrid := !m.ConnectProxyMode
	hasEndpoint := false
	var interval time.Duration
	if m.deps != nil && m.deps.Config != nil {
		cfg := m.deps.Config
		hasEndpoint = strings.TrimSpace(cfg.Sync.URL) != "" || strings.TrimSpace(cfg.MCP.Remote.URL) != ""
		interval = cfg.Sync.Interval
	}
	var status app.SyncStatus
	if m.deps != nil && m.deps.App != nil {
		status = m.deps.App.SyncStatus()
	} else {
		var cfg *config.Config
		if m.deps != nil {
			cfg = m.deps.Config
		}
		status = app.SyncStatusFor(cfg, hybrid)
	}
	return dashboardSyncChipFor(status, hasEndpoint, interval)
}

// connectResultLine renders the honest post-commit result in the status.go
// vocabulary (design.md W7). A non-running loop is never described as running.
func connectResultLine(chip SyncChip, proxyMode bool) string {
	if proxyMode {
		return "Remote MCP proxy configured — background sync disabled."
	}
	switch chip.State {
	case SyncRunning:
		if chip.Interval > 0 {
			return "Connected — background sync running (interval " + chip.Interval.String() + ")."
		}
		return "Connected — background sync running."
	case SyncRestartRequired:
		return "Saved — restart required to start the background sync loop."
	case SyncError:
		if reason := strings.TrimSpace(chip.Reason); reason != "" {
			return "Saved, but sync cannot start: " + reason + "."
		}
		return "Saved, but sync cannot start."
	default:
		return "Not connected. Nothing was saved."
	}
}

func connectToastType(chip SyncChip, proxyMode bool) string {
	if chip.State == SyncError {
		return "warning"
	}
	return "success"
}

// runConnectivityTest performs the real three-check probe of design.md W7:
// server reachable, token accepted, and the sync endpoint responding. Each
// check is reported independently and a failure short-circuits the remainder.
func runConnectivityTest(rawURL, token string) connectTestResult {
	base := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	res := connectTestResult{URL: base}
	client := &http.Client{Timeout: connectProbeTimeout, CheckRedirect: transportpolicy.CheckBearerRedirect}
	started := time.Now()

	ok, _, detail := probeConnect(client, http.MethodGet, base+"/health", "")
	res.Checks = append(res.Checks, connectCheck{Name: "server reachable", OK: ok, Detail: detail})
	if !ok {
		res.Elapsed = time.Since(started)
		return res
	}

	ok, status, detail := probeConnect(client, http.MethodGet, base+"/api/me", token)
	if !ok {
		if status == http.StatusUnauthorized {
			detail = "(401): check the bearer token"
		}
		res.Checks = append(res.Checks, connectCheck{Name: "token rejected", OK: false, Detail: detail})
		res.Elapsed = time.Since(started)
		return res
	}
	res.Checks = append(res.Checks, connectCheck{Name: "token accepted", OK: true})

	ok, _, detail = probeConnect(client, http.MethodGet, base+"/api/sync/changes?limit=1", token)
	syncDetail := detail
	if ok {
		syncDetail = "OK"
	}
	res.Checks = append(res.Checks, connectCheck{Name: "sync endpoint /api/sync/changes", OK: ok, Detail: syncDetail})
	res.OK = ok
	res.Elapsed = time.Since(started)
	return res
}

func probeConnect(client *http.Client, method, endpoint, token string) (bool, int, string) {
	req, err := http.NewRequest(method, endpoint, nil)
	if err != nil {
		return false, 0, "invalid endpoint"
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	started := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return false, 0, truncateStr(err.Error(), 60)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true, resp.StatusCode, fmt.Sprintf("(%d ms)", time.Since(started).Milliseconds())
	}
	return false, resp.StatusCode, fmt.Sprintf("(HTTP %d)", resp.StatusCode)
}

// connectKeymap declares the wizard's own bindings so Help and footers are
// generated from the same registry that dispatches the keys (REQ-NAV-004).
func connectKeymap() Keymap {
	handler := Model.handleConnectKeys
	bind := func(key, description string) Binding {
		return Binding{Key: key, Description: description, Handler: handler}
	}
	return Keymap{Screen: ScreenConnect, Bindings: []Binding{
		bind("esc", "Back"),
		bind("enter", "Continue"),
		bind("tab", "Next field"),
		bind("shift+tab", "Previous field"),
		bind("up", "Move up"),
		bind("k", "Move up"),
		bind("down", "Move down"),
		bind("j", "Move down"),
		bind("left", "Previous mode"),
		bind("h", "Previous mode"),
		bind("right", "Next mode"),
		bind("l", "Next mode"),
		bind(" ", "Select mode"),
	}}
}

func init() {
	keymapRegistry.Register(connectKeymap())
}
