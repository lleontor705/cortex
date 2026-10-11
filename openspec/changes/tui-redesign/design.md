# Design: Cortex TUI Full Redesign

Change: `tui-redesign` · Workflow: sdd-lite · All wireframes are ASCII (user decision).

## 1. Architecture decisions

### D1 — Declarative per-screen keymap registry (VALIDATED against code)

The current global router (`update.go:601-780`) intercepts keys before per-screen handlers using an exclusion list repeated 5 times. This is the root cause of the Timeline-unreachable defect (global `t` at :690 shadows timeline bindings despite 4 `loadTimeline` call sites), the untypeable `f` in search, dead `U`/`1-4`/`p` bindings, and Health no-ops. **Decision: replace it.**

```
// internal/tui/keymap.go

// Binding is one declarative key binding for a screen.
type Binding struct {
    Key         string // e.g. "enter", "T", "ctrl+s"
    Description string // human-readable, shown in footer + help
    When        func(m *Model) bool // optional visibility predicate
    Handler     func(m Model, key string) (tea.Model, tea.Cmd)
}

// Keymap is the declarative binding set for one screen.
type Keymap struct {
    Screen   Screen
    Bindings []Binding // ordered; first match wins
}

// Registry maps screens to keymaps. Dispatch is the ONLY key path:
// handleKeyPress -> registry lookup -> binding handler. No pre-router.
type KeymapRegistry struct { keymaps map[Screen]Keymap }

func (r *KeymapRegistry) Dispatch(m Model, key string) (tea.Model, tea.Cmd, bool)
func (r *KeymapRegistry) FooterHints(s Screen, m *Model) []string // generated footer
func (r *KeymapRegistry) HelpEntries() []HelpEntry              // generated help
```

Rules:
- **Global keys are registry entries too**, limited to `ctrl+k` (palette), `?` (help), `esc` (pop stack), `q` (dashboard). They are evaluated only when no screen binding matches — inversion of the current precedence, so shadowing by construction is impossible.
- Text-input screens declare an `InputMode` flag: when a textinput is focused, only `esc`/`tab`/`enter` escape input mode; all other keys go to the input. This replaces the exclusion-list hack and fixes "cannot type f in search".
- Dead bindings removed: `u` (StatsMode no-op), `P` (upload toggle, not persisted), `gg` (PendingKey never set).
- `t` theme toggle moves to Configuration + command palette (open question Q1).

### D2 — Navigation stack (VALIDATED)

Today back-navigation mixes `PrevScreen` (single-level) with hardcoded `ScreenDashboard` returns (e.g. update.go:1755, 1877, 1979). **Decision: a proper stack.**

```
// internal/tui/nav.go
type NavStack struct{ stack []Screen }
func (n *NavStack) Push(s Screen)  // forward navigation
func (n *NavStack) Pop() Screen    // esc: returns previous screen, never empty
func (n *NavStack) ResetTo(s Screen) // q: clear stack, land on Dashboard
func (n *NavStack) Current() Screen
func (n *NavStack) Depth() int     // rendered as breadcrumb
```

- `esc` always pops (Dashboard is the stack floor; `esc` on Dashboard does nothing).
- `q` from any screen resets to Dashboard.
- `PrevScreen` field is deleted; all navigation flows through the stack.
- Breadcrumb in the header: `Dashboard / Configuration / Embedding`.

### D3 — Unified Configuration screen (primary user goal: SIMPLIFY)

The 3 surfaces (Local Config 6-section/17-field form, Embedding Settings, Auth modal) collapse into **one Configuration screen** with two tabs:

- **Essential**: AI provider (single 8-preset enum — fixes the 8-vs-3 downgrade bug), embedding provider/model, Connect to server (launches the guided flow, D4). Everything else defaults sensibly.
- **Advanced**: storage path/format, HTTP API, MCP, sync interval, embedding vector dim/autostart, theme. Hidden by default; explicit user opt-in.

**One save semantic** (built on `tui-sync-fix-001`): `s` saves -> config written -> `ReloadConfig` hot-reloads everything reloadable (sync loop included, per tui-sync-fix-001). If — and only if — a field genuinely requires process restart (database path, config format), an explicit amber **restart banner** appears. Toasts state exactly what happened: `Saved — changes applied` or `Saved — restart required for: database path`.

### D4 — Connect guided flow

First-class screen replacing the Auth modal: URL -> token -> connectivity test -> enable. Honest states only: `Testing...`, `Connected — sync running`, `Saved — restart required`, `Failed: <reason>` (never a false success). Remote MCP Proxy mode selection no longer silently writes `sync.enabled=false` without saying so.

### D5 — Fates of audited items

| Item | Fate | Rationale |
|---|---|---|
| Timeline screen | **KEEP, fix reachability** | 4 real call sites + full handler exist; registry makes `T` work from Detail/Results/Recent |
| Embedding Settings | **MERGE** into Configuration/Advanced | removes duplicate surface + enum divergence |
| `u` StatsMode | **REMOVE** | no-op presented as feature |
| `P` upload toggle | **REMOVE** | not persisted, misleading |
| `gg` PendingKey | **REMOVE** | dead code |
| Sparkline | **DROP data loading** | 7 queries/launch for data never rendered; dashboard keeps simple counts |
| Dashboard duplicates | **DEDUPE** | one Configuration entry, one Connect entry |
| Auth modal | **DISSOLVE** into Connect flow | single write path for server URL/token |

### D6 — Onboarding empty states

Three empty states, each with one primary action: first-run -> Setup agent plugin; empty DB -> Create first memory (`n`); unconfigured sync -> Connect flow.

## 2. ASCII wireframes

### W1 — Dashboard (redesigned)

```
  Cortex Memory                    [Hybrid sync: running]      1/4
  ────────────────────────────────────────────────────────────────
  Observations 1,204 · Sessions 87 · Edges 3,410 · Projects 5

  MAIN
    > Recent observations        (1)
      Search memories            (/)
      Knowledge graph            (2)
      Memory health              (3)
      Sessions                   (4)
      Archived observations
      Timeline

  SYSTEM
      Connect to server...       (honest state, guided)
      Configuration...           (single entry — Essential/Advanced)
      Setup agent plugin...
      Help                       (?)
      Quit                       (ctrl+c)

  [ctrl+k] palette · [n] new memory · [t] theme · [?] help
  ────────────────────────────────────────────────────────────────
  Dashboard /  · esc back · q here
```

Changes: menu grouped MAIN/SYSTEM; duplicate config entries removed; sync state chip in header is honest (running / disabled / restart required / error); sparkline queries removed; labels de-jargoned ("Neural Graph" -> "Knowledge graph", "Command Deck" dropped).

### W2 — Configuration · Essential tab

```
  Configuration                    [Essential]  Advanced        
  ────────────────────────────────────────────────────────────────
  AI PROVIDER
    Provider      [ Ollama      ]  <-  (left/right cycles 8 presets)
    Model         [ nomic-embed-text ]
    Base URL      [ http://localhost:11434 ]

  EMBEDDING
    Provider      [ Ollama      ]  <- single enum, same 8 presets
    Model         [ nomic-embed-text ]

  SERVER
    Status        ● Not connected          [ Connect... -> ]  (c)

  [s] Save & apply     [r] Reload from disk     [esc] Back
  ────────────────────────────────────────────────────────────────
  Dashboard / Configuration / Essential · unsaved changes: 2
```

### W3 — Configuration · Advanced tab

```
  Configuration                    Essential  [Advanced]      
  ────────────────────────────────────────────────────────────────
  STORAGE
    Database path [ /Users/me/.cortex/cortex.db ]
    Config format [ yaml ]   ⚠ changing format renames the file;
                             delete the old file manually (follow-up fix)

  HTTP API            MCP                     SYNC
    Enabled [x]        Profile  [ dev ]       Enabled [ ]
    Host    [127...]   Remote   [ ]           Interval [ 30s ]
    Port    [8080]     URL      [ ]           Token env [ CORTEX_REMOTE_TOKEN ]
                       Token env[ ]

  EMBEDDING ADVANCED
    Vector dim [ 768 ]   Autostart Ollama [x]   Theme [ dark ]

  [s] Save & apply     [r] Reload from disk     [esc] Back
  ────────────────────────────────────────────────────────────────
  Dashboard / Configuration / Advanced · unsaved changes: 0
```

### W4 — Restart banner (only when truly required)

```
  ┌──────────────────────────────────────────────────────────────┐
  │ ⚠ Saved. Restart required for: database path.               │
│ All other changes are already applied.                       │
  └──────────────────────────────────────────────────────────────┘
```

### W5 — Connect flow · step 1 (URL)

```
  Connect to server                               step 1 of 3
  ────────────────────────────────────────────────────────────────
  Server URL
  [ https://your-cortex.up.railway.app_ ]

  Mode after connect:
    ( ) Hybrid sync — local DB + background replication  [recommended]
    ( ) Remote MCP proxy — no local replication
        note: proxy mode disables background sync (stated, not silent)

  [enter] Continue    [esc] Cancel
  ────────────────────────────────────────────────────────────────
  Dashboard / Connect · step 1 of 3
```

### W6 — Connect flow · step 2 (token)

```
  Connect to server                               step 2 of 3
  ────────────────────────────────────────────────────────────────
  Bearer token
  [ ctx_•••••••••••••••••••••••• ]
  Stored in config as http.token. A token set via `cortex auth login`
  is preserved if you leave this empty.

  [enter] Test connection    [esc] Back
  ────────────────────────────────────────────────────────────────
  Dashboard / Connect · step 2 of 3
```

### W7 — Connect flow · step 3 (test + honest result)

```
  Connect to server                               step 3 of 3
  ────────────────────────────────────────────────────────────────
  Testing https://your-cortex.up.railway.app ...
    ✓ server reachable (142 ms)
    ✓ token accepted
    ✓ sync endpoint /api/sync/changes OK

  Result: Connected — background sync running (interval 30s).

  [enter] Done    [esc] Back

  -- failure variant --
  ✗ token rejected (401): check the bearer token.
  Result: Not connected. Nothing was saved.
```

### W8 — Search

```
  Search memories
  ────────────────────────────────────────────────────────────────
  > fix jwt token expiry_          <- typing 'f' works (InputMode)

  [enter] Search    [esc] Cancel    [ctrl+k] palette
  ────────────────────────────────────────────────────────────────
  Dashboard / Search
```

### W9 — Search Results + split preview

```
  Results for "fix jwt token expiry" — 12 matches      [p] proj: all
  ────────────────────────────────────────────────────────────────
  > 1. Fix JWT bearer validation            score 0.91   #1042
    2. Token refresh edge case              score 0.84   #987
    3. Auth middleware refactor             score 0.77   #933
  ───────────────────────────────── preview (v) ──────────────────
  Fix JWT bearer validation
  Validated exp/nbf claims and moved clock-skew tolerance...

  [enter] Open detail   [T] Timeline   [v] Preview   [n] New
  [p] Project filter   [esc] Back to search
  ────────────────────────────────────────────────────────────────
  Dashboard / Search / Results
```

### W10 — Observation Detail

```
  Observation #1042 · decision · project: cortex        score 0.91
  ────────────────────────────────────────────────────────────────
  Fix JWT bearer validation

  Validated exp/nbf claims and moved clock-skew tolerance into the
  token validator. Related: #987, #933.

  LINKS
    references  #987 Token refresh edge case
    relates_to  #933 Auth middleware refactor

  [T] Timeline   [g] Graph from here   [d] Delete (confirm)   [esc] Back
  ────────────────────────────────────────────────────────────────
  Dashboard / Search / Results / #1042
```

### W11 — Timeline (kept; now reachable via T)

```
  Timeline from #1042 Fix JWT bearer validation
  ────────────────────────────────────────────────────────────────
    BEFORE
      #933  Auth middleware refactor            2d ago
      #987  Token refresh edge case             1d ago
  ►   FOCUS
      #1042 Fix JWT bearer validation           4h ago
    AFTER
      #1051 Rotation job for refresh tokens     1h ago

  [enter] Open   [esc] Back to detail
  ────────────────────────────────────────────────────────────────
  Dashboard / Search / Results / #1042 / Timeline
```

### W12 — Knowledge Graph

```
  Knowledge graph — root #1042                    [p] proj: cortex
  ────────────────────────────────────────────────────────────────
        #987 ──── #1042 ──── #1051
                    │
                  #933

  [enter] Focus as root   [esc] Back
  ────────────────────────────────────────────────────────────────
  Dashboard / Knowledge graph
```

### W13 — Memory Health (controls now real)

```
  Memory health
  ────────────────────────────────────────────────────────────────
  Observations by type      Stale (>90d)   Orphaned   Broken links
    decision   412          38             12         3
    bugfix     288          21             5          1
    pattern    197          9              2          0

  [enter] Review stale      <- performs the action (was a no-op)
  [o] Review orphaned       <- performs the action (was a no-op)
  [esc] Back
  ────────────────────────────────────────────────────────────────
  Dashboard / Memory health
```

### W14 — Archive

```
  Archived observations — 54                       [p] proj: all
  ────────────────────────────────────────────────────────────────
  > 1. Legacy v1 migration notes            archived 12d ago
    2. Old embedding benchmark              archived 30d ago

  [enter] View   [v] Preview   [u] Restore   [esc] Back
  ────────────────────────────────────────────────────────────────
  Dashboard / Archive
```

### W15 — Sessions / Session detail

```
  Sessions — 87                                    [p] proj: all
  ────────────────────────────────────────────────────────────────
  > 1. ses_8f3a…  TUI redesign planning       42 obs  2h ago
    2. ses_1c9d…  Sync debugging              17 obs  1d ago

  [enter] Open session   [esc] Back
  ────────────────────────────────────────────────────────────────
  Dashboard / Sessions

  -- session detail --
  Session ses_8f3a… · 42 observations
  ────────────────────────────────────────────────────────────────
  > 1. Keymap registry contract              2h ago
    2. Navigation stack model                2h ago
  [enter] Open observation   [esc] Back to sessions
```

### W16 — Setup agent plugin

```
  Setup agent plugin
  ────────────────────────────────────────────────────────────────
  Detected agents:
    [x] OpenCode      plugin installed   [enter] Reinstall
    [ ] Claude Code   not installed      [enter] Install

  [i] Re-detect   [esc] Back
  ────────────────────────────────────────────────────────────────
  Dashboard / Setup
```

### W17 — Help (generated from the keymap registry)

```
  Help — keys are generated from the live keymap registry
  ────────────────────────────────────────────────────────────────
  GLOBAL
    ctrl+k  Command palette          ?  Help
    esc     Back (pop)               q  Dashboard

  <current screen: e.g. Search Results>
    enter   Open detail              T  Timeline
    v       Toggle preview           p  Project filter
    n       New memory

  [esc] Back
```

### W18 — Command palette (documented, reachable everywhere)

```
  Command palette                                    ctrl+k
  ────────────────────────────────────────────────────────────────
  > the_
    theme toggle
    test connection (server)
  ────────────────────────────────────────────────────────────────
```

### W19 — Onboarding empty states

```
  -- first run (no DB content, fresh install) --
  Welcome to Cortex.
  Your memory store is empty. Start with one of:
    [n] Create your first memory        [enter]
    [s] Set up an agent plugin          [tab]
    [c] Connect to a server             [o]

  -- empty DB after use (all observations deleted/archived) --
  No observations yet.
    [n] Create your first memory        [enter]
    [a] View archive

  -- unconfigured sync (dashboard header chip variant) --
  Sync: not configured
    [c] Connect to a server — guided, 3 steps, tests before saving
    [x] Skip — stay local-only
```

## 3. Migration notes (model.go / update.go)

1. **Extraction before behavior change.** `update.go` (~2500 LOC) holds all 15 `handle*Keys` funcs; `view_knowledge.go` holds 8 views; `view_control.go` holds 3 config views. Mechanical, behavior-preserving extraction first: `keys_dashboard.go`, `keys_search.go`, `keys_detail.go`, `keys_sessions.go`, `keys_system.go`, `keys_config.go`; `view_search.go`, `view_detail.go`, `view_sessions.go`. Only then does the registry replace the router.
2. **Router removal is one atomic task** (last of the extraction chain): delete the global pre-router block (update.go:601-745), install `registry.Dispatch`, add `InputMode` gating, delete `u`/`P`/`gg` bindings, wire `T` timeline bindings into Detail/Results/Recent keymaps.
3. **Model state.** `PrevScreen` deleted (replaced by `NavStack`); `StatsMode`, `UploadToCortex`, `PendingKey`, sparkline fields deleted; new `ConfigTab`, `ConnectStep`, `RestartRequired []string`, `EmptyStateKind` fields added.
4. **Coordination with in-flight work.** `tui-sync-fix-001` (board `default`, in progress) leases `internal/tui/update.go`, `store.go`, `update_test.go`, `store_test.go` and defines the ReloadConfig/save semantics this design consumes. No task in this DAG touches those files until it completes; Wave 1 tasks use only new files.
5. **Test strategy.** Each `keys_*.go` gets a focused `keys_*_test.go` (<= 250 LOC) asserting registry dispatch, InputMode gating, and no-shadowing (a table test asserting every screen binding fires its own handler). `keys_coverage_test.go` is regenerated from the registry at the end.
6. **Follow-up (out of DAG):** `internal/config/config.go` split-brain fix (format cycler leaves the old file behind) — filed as an open question in proposal.md.

## 4. Verification strategy

- Per-task: `go test -count=1 ./internal/tui/... -run <focused>` plus `go build ./...`.
- Wave gates: after the router-removal task, the full TUI suite must pass: `go test -count=1 ./internal/tui/...`.
- Final integration: `go test -count=1 ./internal/tui/...` + `go vet ./internal/tui/...` + `golangci-lint run internal/tui/...`.
- No-shadowing oracle: table-driven test enumerating every (screen, binding) pair from the registry and asserting the handler is reached — this is the regression test for the original defect class.
