package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// ─── Onboarding empty states (REQ-SYNC-005, design.md W19) ──────────────────

// EmptyStateKind classifies which onboarding empty state the Dashboard must
// surface. The zero value means there is content to show (or the counts are not
// known yet), so the normal menu renders.
type EmptyStateKind int

const (
	// EmptyStateNone means content exists or the counts are still unknown.
	EmptyStateNone EmptyStateKind = iota
	// EmptyStateFirstRun is a fresh install whose store has never held memory.
	EmptyStateFirstRun
	// EmptyStateEmptyDB is a store that has been used but currently holds no
	// observations (everything was deleted or archived).
	EmptyStateEmptyDB
)

// String returns the stable identifier used in diagnostics and tests.
func (k EmptyStateKind) String() string {
	switch k {
	case EmptyStateNone:
		return "none"
	case EmptyStateFirstRun:
		return "first-run"
	case EmptyStateEmptyDB:
		return "empty-db"
	default:
		return "invalid"
	}
}

// onboardingAction is one routable action offered by an onboarding empty state.
type onboardingAction int

const (
	onboardingNoAction onboardingAction = iota
	onboardingCreateMemory
	onboardingSetupPlugin
	onboardingConnect
	onboardingViewArchive
	onboardingSkipSync
)

// onboardingSyncPromptDismissed records the unconfigured-sync prompt dismissal
// for the lifetime of the process. A package variable is deliberate: the skip
// lives in memory only and must reappear on the next launch, which is exactly
// the process lifetime (REQ-SYNC-005). model.go is outside this task's file
// lease, so the Model cannot carry the flag.
var onboardingSyncPromptDismissed bool

// resetOnboardingSyncPrompt restores the pre-launch prompt visibility; tests use
// it to stand in for a fresh process.
func resetOnboardingSyncPrompt() { onboardingSyncPromptDismissed = false }

// onboardingEmptyState classifies the Dashboard's onboarding state. Unknown
// counts (Stats not loaded) map to EmptyStateNone so a slow load never flashes
// onboarding at a returning user.
func (m Model) onboardingEmptyState() EmptyStateKind {
	if m.Stats == nil || m.Stats.TotalObservations > 0 {
		return EmptyStateNone
	}
	if m.Stats.TotalSessions > 0 || len(m.ArchivedObservations) > 0 {
		return EmptyStateEmptyDB
	}
	return EmptyStateFirstRun
}

// syncUnconfigured reports whether no sync endpoint exists, reusing the honest
// status vocabulary so the prompt and the header chip can never disagree.
func (m Model) syncUnconfigured() bool {
	return m.dashboardSyncChip().State == SyncNotConfigured
}

// syncPromptVisible is true only while the store counts are known, sync is
// unconfigured, the user has not skipped this launch, and no full empty state
// already offers Connect. Waiting for the counts avoids prompting before the
// dashboard knows the store state.
func (m Model) syncPromptVisible() bool {
	return m.Stats != nil &&
		!onboardingSyncPromptDismissed &&
		m.onboardingEmptyState() == EmptyStateNone &&
		m.syncUnconfigured()
}

// onboardingActionFor maps a key to the action it triggers in the current
// onboarding state. It is the single routing table shared by the Dashboard
// bindings and their tests, so a state exposes exactly its declared actions.
func (m Model) onboardingActionFor(key string) (onboardingAction, bool) {
	switch m.onboardingEmptyState() {
	case EmptyStateFirstRun:
		switch key {
		case "enter", "n", "N":
			return onboardingCreateMemory, true
		case "s", "S", "i", "I":
			return onboardingSetupPlugin, true
		case "c", "C", "L":
			return onboardingConnect, true
		}
	case EmptyStateEmptyDB:
		switch key {
		case "enter", "n", "N":
			return onboardingCreateMemory, true
		case "a", "A":
			return onboardingViewArchive, true
		}
	}
	if m.syncPromptVisible() {
		switch key {
		case "c", "C", "L":
			return onboardingConnect, true
		case "x", "X":
			return onboardingSkipSync, true
		}
	}
	return onboardingNoAction, false
}

// applyOnboardingAction routes one onboarding action through the same entry
// point the rest of the TUI uses, so onboarding never invents a parallel
// navigation path. The single primary action per state is the first result.
func (m Model) applyOnboardingAction(action onboardingAction) (tea.Model, tea.Cmd) {
	switch action {
	case onboardingCreateMemory:
		return openNewObservationKey(m, "n")
	case onboardingSetupPlugin:
		return m.navigateDashboard(ScreenSetup)
	case onboardingConnect:
		return m.openConnectFlow()
	case onboardingViewArchive:
		return m.navigateDashboard(ScreenArchive)
	case onboardingSkipSync:
		onboardingSyncPromptDismissed = true
		return m, nil
	}
	return m, nil
}

// handleOnboardingKey reports whether the key belonged to the active onboarding
// state and, if so, the routed result.
func (m Model) handleOnboardingKey(key string) (tea.Model, tea.Cmd, bool) {
	action, ok := m.onboardingActionFor(key)
	if !ok {
		return m, nil, false
	}
	model, cmd := m.applyOnboardingAction(action)
	return model, cmd, true
}

// onboardingBindingHandler adapts handleOnboardingKey to the registry's
// (tea.Model, tea.Cmd) binding signature; a matched binding always routes.
func onboardingBindingHandler(m Model, key string) (tea.Model, tea.Cmd) {
	model, cmd, _ := m.handleOnboardingKey(key)
	return model, cmd
}

func firstRunActive(m *Model) bool {
	return m != nil && m.onboardingEmptyState() == EmptyStateFirstRun
}

func emptyDBActive(m *Model) bool {
	return m != nil && m.onboardingEmptyState() == EmptyStateEmptyDB
}

func emptyStateActive(m *Model) bool {
	return m != nil && m.onboardingEmptyState() != EmptyStateNone
}

// connectOfferActive is true while either empty state or the unconfigured-sync
// prompt offers the Connect flow.
func connectOfferActive(m *Model) bool {
	return m != nil && (m.onboardingEmptyState() == EmptyStateFirstRun || m.syncPromptVisible())
}

func syncPromptActive(m *Model) bool {
	return m != nil && m.syncPromptVisible()
}

// onboardingBindings declares the Dashboard bindings that surface only while an
// onboarding empty state (or the unconfigured-sync prompt) is active. They are
// ordered before the base Dashboard bindings so an active state's actions win;
// each carries a When predicate, so an inactive state leaves the base menu keys
// (search, configuration, theme) untouched.
func onboardingBindings() []Binding {
	return []Binding{
		{Key: "enter", Description: "Create your first memory", When: emptyStateActive, Handler: onboardingBindingHandler},
		{Key: "s", Description: "Set up an agent plugin", When: firstRunActive, Handler: onboardingBindingHandler},
		{Key: "a", Description: "View archive", When: emptyDBActive, Handler: onboardingBindingHandler},
		{Key: "c", Description: "Connect to a server", When: connectOfferActive, Handler: onboardingBindingHandler},
		{Key: "x", Description: "Skip", When: syncPromptActive, Handler: onboardingBindingHandler},
	}
}
