# Cortex TUI Full Redesign - Integrated Plan (SDD-Lite)

- Change ID: tui-redesign
- Workflow: sdd-lite | Phase: integrated | Spec plane: hybrid (this artifact is the authoritative plan)
- Workload policy: flexible | Execution mode: auto
- Board: tui-redesign (single durable initiative board, no derivative boards)
- Date: 2026-10-10
- Requirements: REQ-CFG-001, REQ-CFG-002, REQ-CFG-003, REQ-CFG-004, REQ-CFG-005, REQ-CFG-006, REQ-NAV-001, REQ-NAV-002, REQ-NAV-003, REQ-NAV-004, REQ-NAV-005, REQ-SYNC-001, REQ-SYNC-002, REQ-SYNC-003, REQ-SYNC-004, REQ-SYNC-005
- Research basis: Cortex observations #700 and #697 (gotchas/tui-full-audit); anchors re-verified against internal/tui on 2026-10-10 (update.go:601-780 global router, update.go:1749-1925 LocalConfig form, view_knowledge.go 8 views, view_control.go 3 config views). RFC 2119 keywords.

## 1. Intent & Non-Goals

Requirements: REQ-CFG-001, REQ-CFG-002, REQ-CFG-003, REQ-CFG-004, REQ-CFG-005, REQ-CFG-006, REQ-NAV-001, REQ-NAV-002, REQ-NAV-003, REQ-NAV-004, REQ-NAV-005, REQ-SYNC-001, REQ-SYNC-002, REQ-SYNC-003, REQ-SYNC-004, REQ-SYNC-005

### Intent

Redesign the Cortex TUI end to end so the interface stops fighting the user: replace the shadowing global key router (update.go:601-780) with a declarative per-screen keymap registry, collapse the 3 fragmented configuration surfaces (Local Config 6-section form, Embedding Settings reachable only via the undocumented ctrl+k palette, Auth modal - 17 keys, 4 distinct save semantics) into ONE Configuration screen with Essential/Advanced progressive disclosure and a single honest save semantic, make connection/sync a first-class guided flow with a real connectivity test, add onboarding empty states, and adopt a navigation stack with uniform esc/q semantics. Primary user goal: SIMPLIFY configuration options - sensible defaults, advanced knobs hidden. Prototypes are ASCII wireframes embedded in design.md (user decision).

### Non-Goals

- NG-1: No Go behavior changes outside internal/tui/**.
- NG-2: No sync wire protocol, server endpoint, or internal/platform/server/** changes.
- NG-3: No internal/app or internal/config semantic changes beyond consuming the in-flight tui-sync-fix-001 contract (ReloadConfig restarts the sync loop; token persistence without wiping http.token; honest toasts). The config-format split-brain fix (cycler renames the file without deleting the old one) is a documented follow-up requiring internal/config/config.go.
- NG-4: Removed no-ops (u StatsMode, P upload toggle, gg PendingKey, sparkline data loading) are removed, not implemented.
- NG-5: No HTML prototypes; no user-remappable keybindings; no mouse-driven navigation model.
- NG-6: No implementation, claims, or dispatch in the planning phase; all tasks materialize in backlog.

## 2. Requirements (RFC 2119) with Scenarios

Requirements: REQ-CFG-001, REQ-CFG-002, REQ-CFG-003, REQ-CFG-004, REQ-CFG-005, REQ-CFG-006, REQ-NAV-001, REQ-NAV-002, REQ-NAV-003, REQ-NAV-004, REQ-NAV-005, REQ-SYNC-001, REQ-SYNC-002, REQ-SYNC-003, REQ-SYNC-004, REQ-SYNC-005

Full Gherkin deltas: specs/tui/configuration.md, specs/tui/navigation.md, specs/tui/sync-flows.md. Technical design: design.md (D1-D6, 19 ASCII wireframes).

### Requirement: REQ-CFG-001 Single configuration surface

Requirements: REQ-CFG-001

The TUI MUST present exactly one Configuration screen. The Embedding Settings screen and the Auth modal MUST be dissolved as separate configuration surfaces, and no configuration capability MAY require the command palette to reach.

#### Scenario: all configuration reachable from one screen

Requirements: REQ-CFG-001

GIVEN the user opens Dashboard
WHEN they select Configuration
THEN the unified Configuration screen opens with Essential and Advanced tabs
AND all 17 previously scattered config keys are editable from one of the two tabs

#### Scenario: embedding settings merged

Requirements: REQ-CFG-001

GIVEN the user is on the Configuration screen
WHEN they switch to the Advanced tab
THEN embedding provider, model, vector dimension, and Ollama autostart are editable there
AND the standalone Embedding Settings screen no longer exists

#### Scenario: palette is never the only path

Requirements: REQ-CFG-001

GIVEN the command palette is closed
WHEN the user reaches any configuration capability
THEN a direct menu or key path exists that does not involve the palette

### Requirement: REQ-CFG-002 Single provider enum, no cross-surface downgrade

Requirements: REQ-CFG-002

The AI/LLM and embedding provider enums MUST be the same 8-preset superset (None, Ollama, OpenAI, Anthropic, OpenRouter, Groq, DeepSeek, Custom). Saving one surface MUST NOT downgrade a provider set on another.

#### Scenario: no provider downgrade on save

Requirements: REQ-CFG-002

GIVEN the embedding provider is Anthropic
WHEN the user saves Configuration without changing it
THEN the persisted embedding provider remains Anthropic

#### Scenario: enums are identical

Requirements: REQ-CFG-002

GIVEN the user cycles the AI provider and the embedding provider
WHEN comparing the offered values
THEN both cycles offer exactly the same 8 presets in the same order

#### Scenario: custom provider preserved

Requirements: REQ-CFG-002

GIVEN the embedding provider is Custom with a base URL
WHEN the user saves Configuration
THEN Custom and its base URL persist unchanged

### Requirement: REQ-CFG-003 One save semantic with honest feedback

Requirements: REQ-CFG-003

Saving MUST write config then hot-reload everything reloadable in the running process (including the sync loop, per tui-sync-fix-001). A restart banner MUST appear if and only if a changed field genuinely requires process restart (database path, config format). The TUI MUST NOT show success for an action that did not take effect.

#### Scenario: hot-reloadable save

Requirements: REQ-CFG-003

GIVEN the user changes the embedding model
WHEN they save
THEN the config file is written, the setting is applied without restart, and the toast reads Saved - changes applied

#### Scenario: restart-required save

Requirements: REQ-CFG-003

GIVEN the user changes the database path
WHEN they save
THEN an amber restart banner lists exactly the fields requiring restart and states all other changes are already applied

#### Scenario: failed save shows no success

Requirements: REQ-CFG-003

GIVEN the config file cannot be written
WHEN the save attempt completes
THEN an error state with the reason is shown and no success toast appears

### Requirement: REQ-CFG-004 Token preservation on save

Requirements: REQ-CFG-004

Saving from the TUI MUST NOT erase an existing http.token when the token field is left empty (tui-sync-fix-001 contract).

#### Scenario: empty token field preserves existing token

Requirements: REQ-CFG-004

GIVEN http.token is set in the config file
WHEN the user saves leaving the token field empty
THEN http.token is unchanged

#### Scenario: entered token replaces existing token

Requirements: REQ-CFG-004

GIVEN http.token is set
WHEN the user saves with a new token entered
THEN http.token is updated to the new value

#### Scenario: token resolves after restart

Requirements: REQ-CFG-004

GIVEN the user saved a token via the TUI
WHEN a new process starts and resolveSyncToken runs
THEN the token resolves via the documented fallback chain

### Requirement: REQ-CFG-005 Progressive disclosure with working defaults

Requirements: REQ-CFG-005

The Essential tab MUST expose only AI provider/model/base URL, embedding provider/model, and server connection status with a Connect entry. All other knobs MUST live on Advanced. Defaults MUST yield a working setup without opening Advanced.

#### Scenario: defaults work without Advanced

Requirements: REQ-CFG-005

GIVEN a fresh install with no config file
WHEN the user saves the Essential tab unchanged
THEN the config uses documented defaults (Ollama, nomic-embed-text, sync disabled) and the TUI operates correctly

#### Scenario: essential tab is minimal

Requirements: REQ-CFG-005

GIVEN the Essential tab is rendered
WHEN the user inspects its fields
THEN only AI provider/model/base URL, embedding provider/model, and server status with Connect are present

#### Scenario: advanced is opt-in

Requirements: REQ-CFG-005

GIVEN the user never opens the Advanced tab
WHEN they use the TUI normally
THEN no Advanced-only knob blocks any Essential flow

### Requirement: REQ-CFG-006 Config format change warning

Requirements: REQ-CFG-006

Until the internal/config split-brain follow-up ships, changing the config format MUST warn that the old config file is not deleted and must be removed manually.

#### Scenario: format cycler warns about split-brain

Requirements: REQ-CFG-006

GIVEN the user is on Advanced
WHEN they change the config format from yaml to json
THEN a visible warning states the old file must be deleted manually

#### Scenario: warning persists until saved or reverted

Requirements: REQ-CFG-006

GIVEN the format was changed but not saved
WHEN the user navigates fields
THEN the warning remains visible

#### Scenario: revert clears the warning

Requirements: REQ-CFG-006

GIVEN the format was changed
WHEN the user cycles back to the original format
THEN the warning disappears

### Requirement: REQ-NAV-001 Declarative per-screen keymap registry

Requirements: REQ-NAV-001

Key dispatch MUST be driven by a declarative per-screen keymap registry. There MUST be no global router running before screen handlers. A key declared by the focused screen MUST always reach that screen's handler.

#### Scenario: no shadowing by construction

Requirements: REQ-NAV-001

GIVEN any screen with a declared binding for key K
WHEN the user presses K
THEN the screen handler for K runs and no global handler intercepts first

#### Scenario: timeline reachable

Requirements: REQ-NAV-001

GIVEN the user is on Detail, Results, or Recent
WHEN they press T
THEN Timeline opens for the focused observation and the theme toggle does not intercept

#### Scenario: typing in search

Requirements: REQ-NAV-001

GIVEN the search input is focused (InputMode)
WHEN the user types f
THEN f is inserted into the input and no binding fires

### Requirement: REQ-NAV-002 Minimal global keys; dead bindings removed

Requirements: REQ-NAV-002

Global keys MUST be exactly ctrl+k (palette), ? (help), esc (back), q (dashboard), evaluated only when no screen binding matches. u, P, and gg MUST be removed entirely.

#### Scenario: removed no-op keys do nothing

Requirements: REQ-NAV-002

GIVEN the user is on Dashboard
WHEN they press u, then P, then g twice
THEN no state changes and no toast claims an action occurred

#### Scenario: global keys work everywhere non-input

Requirements: REQ-NAV-002

GIVEN the user is on any screen without a focused text input
WHEN they press ctrl+k, ?, esc, or q
THEN the corresponding global action fires

#### Scenario: screen binding beats global

Requirements: REQ-NAV-002

GIVEN a screen declares a binding for a key that is also global
WHEN the user presses it on that screen
THEN the screen binding wins

### Requirement: REQ-NAV-003 Navigation stack

Requirements: REQ-NAV-003

Forward navigation MUST push onto a stack; esc MUST pop (Dashboard is the floor); q MUST reset to Dashboard. PrevScreen MUST be deleted. The header MUST render a breadcrumb from the stack.

#### Scenario: deep navigation round trip

Requirements: REQ-NAV-003

GIVEN the user navigated Dashboard - Search - Results - Detail - Timeline
WHEN they press esc four times
THEN they return in reverse order and a further esc on Dashboard changes nothing

#### Scenario: q resets to dashboard

Requirements: REQ-NAV-003

GIVEN the user is on Timeline at stack depth 5
WHEN they press q
THEN the stack resets and Dashboard is shown

#### Scenario: breadcrumb reflects depth

Requirements: REQ-NAV-003

GIVEN the user is at Dashboard - Configuration - Advanced
WHEN the header renders
THEN the breadcrumb reads Dashboard / Configuration / Advanced

### Requirement: REQ-NAV-004 Help and footers generated from the registry

Requirements: REQ-NAV-004

Help and per-screen footer hints MUST be generated from the same registry that dispatches keys. A binding shown in a footer MUST exist in the registry; Health-screen controls MUST perform their described action or not be shown.

#### Scenario: help matches reality

Requirements: REQ-NAV-004

GIVEN the registry declares bindings B for screen S
WHEN the user opens Help from S
THEN help lists exactly the global keys plus B and no key without a handler

#### Scenario: health controls are real

Requirements: REQ-NAV-004

GIVEN the user is on Memory Health
WHEN they press a key shown in the footer
THEN the described action executes

#### Scenario: footer omits hidden bindings

Requirements: REQ-NAV-004

GIVEN a binding's When predicate is false
WHEN the footer renders
THEN that binding is omitted

### Requirement: REQ-NAV-005 Dashboard information architecture

Requirements: REQ-NAV-005

The Dashboard menu MUST be grouped (MAIN/SYSTEM), MUST contain exactly one Configuration and one Connect entry, MUST NOT contain duplicate destinations, and labels MUST avoid internal jargon.

#### Scenario: no duplicate destinations

Requirements: REQ-NAV-005

GIVEN the Dashboard menu is rendered
WHEN the user inspects entries
THEN no two entries navigate to the same screen

#### Scenario: grouped menu

Requirements: REQ-NAV-005

GIVEN the Dashboard menu is rendered
WHEN the user inspects entries
THEN content screens are grouped under MAIN and operations under SYSTEM

#### Scenario: de-jargoned labels

Requirements: REQ-NAV-005

GIVEN the Dashboard menu is rendered
WHEN the user reads labels
THEN no label uses internal jargon such as Neural Graph or Command Deck

### Requirement: REQ-SYNC-001 Guided connect flow with real test

Requirements: REQ-SYNC-001

Connecting MUST be a first-class 3-step guided flow (URL + mode, token, connectivity test) that tests before saving and shows the result before commit. The Auth modal MUST be dissolved; the server URL MUST have exactly one writable TUI surface.

#### Scenario: failed connect saves nothing

Requirements: REQ-SYNC-001

GIVEN the connectivity test fails
WHEN the test completes
THEN the failing check and reason are shown and no sync configuration is written

#### Scenario: successful connect starts sync

Requirements: REQ-SYNC-001

GIVEN the user completes all 3 steps with passing checks
WHEN they commit
THEN config is saved, the sync loop starts or hot-reloads, and the state reads Connected - background sync running

#### Scenario: single writable URL surface

Requirements: REQ-SYNC-001

GIVEN the user wants to change the server URL
WHEN they search the TUI
THEN exactly one surface (the Connect flow) accepts a new URL

### Requirement: REQ-SYNC-002 Honest mode selection

Requirements: REQ-SYNC-002

Selecting Remote MCP Proxy mode MUST visibly state that it disables background sync; the TUI MUST NOT silently write sync.enabled=false as an unstated side effect.

#### Scenario: proxy mode states its consequence

Requirements: REQ-SYNC-002

GIVEN the user is on Connect step 1
WHEN they select Remote MCP proxy
THEN the UI states proxy mode disables background sync

#### Scenario: hybrid mode keeps sync available

Requirements: REQ-SYNC-002

GIVEN the user selects Hybrid sync
WHEN they complete the flow
THEN sync is enabled per the flow result and no silent disable occurs

#### Scenario: saved config matches stated choice

Requirements: REQ-SYNC-002

GIVEN the user selected proxy mode
WHEN the config is written
THEN sync.enabled=false matches what the UI stated

### Requirement: REQ-SYNC-003 Honest sync state everywhere

Requirements: REQ-SYNC-003

The Dashboard header chip and Configuration server status MUST reflect the real sync state (running, disabled, not configured, restart required, error). The TUI MUST NOT display a success state the sync subsystem does not have.

#### Scenario: header chip matches reality

Requirements: REQ-SYNC-003

GIVEN sync is configured but the loop failed to start
WHEN the Dashboard renders
THEN the chip shows error with reason and no green connected indicator

#### Scenario: unconfigured sync stated

Requirements: REQ-SYNC-003

GIVEN sync.enabled is false and no URL is set
WHEN the Dashboard renders
THEN the chip shows not configured

#### Scenario: running state only when running

Requirements: REQ-SYNC-003

GIVEN the sync loop is running
WHEN the Dashboard renders
THEN the chip shows running with the configured interval

### Requirement: REQ-SYNC-004 Restart banner semantics for sync saves

Requirements: REQ-SYNC-004

Where a sync-related save cannot hot-reload (per tui-sync-fix-001), the TUI MUST show an explicit restart banner naming affected fields; otherwise it MUST hot-reload and MUST NOT mention restart.

#### Scenario: sync interval change hot-reloads

Requirements: REQ-SYNC-004

GIVEN the sync loop is running
WHEN the user changes the interval and saves
THEN the loop restarts with the new interval without a process restart and no banner

#### Scenario: banner names affected fields

Requirements: REQ-SYNC-004

GIVEN a save requires restart
WHEN the banner renders
THEN it lists exactly the fields requiring restart

#### Scenario: no banner on pure hot-reload

Requirements: REQ-SYNC-004

GIVEN all changed fields are hot-reloadable
WHEN the save completes
THEN no restart banner or restart mention appears

### Requirement: REQ-SYNC-005 Unconfigured-sync onboarding prompt

Requirements: REQ-SYNC-005

When sync is unconfigured, the TUI MUST surface a dismissible prompt offering the Connect flow; skipping hides it until the next launch.

#### Scenario: dismissible sync prompt

Requirements: REQ-SYNC-005

GIVEN sync is unconfigured
WHEN the Dashboard renders
THEN a prompt offers Connect with a skip option and skip hides it until next launch

#### Scenario: prompt routes to connect

Requirements: REQ-SYNC-005

GIVEN the unconfigured-sync prompt is visible
WHEN the user accepts it
THEN the Connect guided flow opens

#### Scenario: prompt absent when configured

Requirements: REQ-SYNC-005

GIVEN sync is configured
WHEN the Dashboard renders
THEN no unconfigured-sync prompt appears

## 3. Design Summary

Requirements: REQ-CFG-001, REQ-CFG-002, REQ-CFG-003, REQ-CFG-004, REQ-CFG-005, REQ-CFG-006, REQ-NAV-001, REQ-NAV-002, REQ-NAV-003, REQ-NAV-004, REQ-NAV-005, REQ-SYNC-001, REQ-SYNC-002, REQ-SYNC-003, REQ-SYNC-004, REQ-SYNC-005

Authoritative design: design.md - 19 ASCII wireframes (Dashboard, Configuration Essential/Advanced, restart banner, Connect steps 1-3, Search, Results+preview, Detail, Timeline, Graph, Health, Archive, Sessions, Setup, Help, command palette, 3 onboarding empty states), the keymap registry Go contract, the navigation stack model, and migration notes from the current model.go/update.go structure.

- D1 Keymap registry replaces the global router (validated: update.go:601-780 has 5 repeated exclusion lists; global t at :690 shadows Timeline despite 4 loadTimeline call sites). Global keys become lowest-priority registry entries; InputMode gating replaces exclusion lists; u/P/gg deleted; t moves to Configuration + palette (open question Q1).
- D2 NavStack replaces PrevScreen and hardcoded Dashboard returns (update.go:1755, 1877, 1979); breadcrumb in header.
- D3 Unified Configuration: Essential/Advanced tabs, single 8-preset enum (fixes the 8-vs-3 provider downgrade), single save semantic on status.go vocabulary (hot-reload + restart banner), token preservation.
- D4 Connect guided flow dissolves the Auth modal; single writable server-URL surface; proxy-mode consequence stated; honest results only.
- D5 Fates: Timeline KEPT and made reachable via T; Embedding Settings MERGED; u/P/gg REMOVED; sparkline loading DROPPED (7 queries/launch); dashboard DEDUPED.
- D6 Onboarding empty states: first-run - Setup; empty DB - Create first memory; unconfigured sync - Connect (dismissible).

## 4. Task DAG (20 tasks, 5 waves + wave 2')

Requirements: REQ-CFG-001, REQ-CFG-002, REQ-CFG-003, REQ-CFG-004, REQ-CFG-005, REQ-CFG-006, REQ-NAV-001, REQ-NAV-002, REQ-NAV-003, REQ-NAV-004, REQ-NAV-005, REQ-SYNC-001, REQ-SYNC-002, REQ-SYNC-003, REQ-SYNC-004, REQ-SYNC-005

Detailed contract: tasks.md (ids, files, verification commands, dependencies). Wave order: W1 -> {W2, W2'} -> W3 -> W4 -> W5; W2 is a sequential chain on update.go (single lease); W2' is a sequential chain on view_knowledge.go parallel to W2; W3/W4 tasks are file-disjoint internally.

- Wave 1 (parallel contracts, new files only): tui-redesign-001 keymap registry, tui-redesign-002 navigation stack, tui-redesign-003 honest status vocabulary
- Wave 2 (sequential extraction + router removal): tui-redesign-004 -> 005 -> 006 -> 007 -> 008
- Wave 2' (parallel to W2): tui-redesign-009 -> 010 (view_knowledge.go split)
- Wave 3 (parallel migrations): tui-redesign-011 Dashboard IA, 012 Search/Results, 013 Detail/Timeline/Archive, 014 Graph/Health, 015 Sessions
- Wave 4: tui-redesign-016 unified Configuration, 017 Connect flow, 018 onboarding empty states, 019 registry-generated Help
- Wave 5: tui-redesign-020 integration regression + registry-generated coverage oracle

Coordination: tasks touching update.go, store.go, or update_test.go (004-008, 011, 020) wait for in-flight tui-sync-fix-001 (board default) to reach done. Tasks 001-003, 009-010, 012-019 use only unleased files.

## 5. Risks & Rollback

Requirements: REQ-CFG-002, REQ-CFG-003, REQ-CFG-004, REQ-NAV-001, REQ-NAV-002

- R-1 update.go (~2500 LOC) is a single lease bottleneck and tui-sync-fix-001 holds leases on update.go/store.go: Wave 1 uses only new files; extraction is a sequential chain; leased-file tasks gate on tui-sync-fix-001 done. Rollback: per-task git revert; extraction tasks are behavior-preserving.
- R-2 muscle-memory breakage from removed keys: help + footers generated from the registry; removed keys documented in design.md migration notes.
- R-3 config unification regressions (token wiping, provider downgrade): single save path with golden tests; enum unified to the 8-preset superset; REQ-CFG-004 inherits tui-sync-fix-001 tests.
- R-4 parallel migration file conflicts: mechanical extraction wave first; W3/W4 allowed_files are mutually disjoint.
- R-5 view_knowledge.go holds 8 screens: split into view_search/view_detail/view_sessions before parallel migration (009-010).
