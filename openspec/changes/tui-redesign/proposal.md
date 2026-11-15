# Proposal: Cortex TUI Full Redesign

## Change ID
`tui-redesign`

## Workflow
- **Workflow**: sdd-lite (integrated)
- **Spec plane**: hybrid (this directory is authoritative)
- **Workload policy**: flexible (source logic <= 700 LOC per task)
- **Board**: `tui-redesign`

## Problem

A full TUI audit (Cortex observations #700, #697, `gotchas/tui-full-audit`) found that the Cortex TUI's information architecture fights the user instead of helping them. Root causes, all verified against code:

1. **Global key router shadowing (architecture defect).** `internal/tui/update.go:601-780` runs a single global key router *before* per-screen handlers, with an ad-hoc exclusion list repeated 5 times (`m.Screen != ScreenSearch && m.Screen != ScreenEmbeddingConfig && ...`). Every global key silently shadows screen-local keys. Consequences: the Timeline screen is unreachable in practice (global `t` theme toggle at update.go:690 beats the 4 `loadTimeline` call sites at update.go:1085/1194/1280/1388), dead bindings (`U`, `1-4` on excluded screens, `p`), and Health-screen controls that do nothing.

2. **Configuration is fragmented across 3 surfaces with 4 save semantics.** 17 config keys are split across: Local Config (6-section form, update.go:1749-1925), Embedding Settings (reachable only via the undocumented `ctrl+k` palette), and the Auth modal. Save/reload behavior differs per surface (reload+save / save-only+restart-tip / save-only+false-success-toast / in-memory-only). The embedding provider/model is editable on 2 surfaces with divergent enums (8 presets vs 3) — saving Embedding Settings can *downgrade* the configured provider. The server URL is writable from both the Auth modal and Local Config with conflicting side effects (Remote MCP Proxy mode writes `sync.enabled=false`, update.go ~2477).

3. **Sync/connection flows lie.** The TUI cannot start the sync loop in the running process (App.ReloadConfig never restarted it — being fixed by in-flight task `tui-sync-fix-001`), yet showed "✔ Connected in Hybrid Mode". Saving config without a token could wipe a pre-existing `http.token` via `clearAbsentSecrets`. Sync defaults to disabled with zero feedback.

4. **No-ops presented as features.** `u` StatsMode toggle, `P` upload toggle (not persisted), sparkline data (7 queries per launch, never rendered), `gg` PendingKey (never set).

5. **No onboarding.** First run, empty database, and unconfigured sync all present raw empty lists with no guidance.

6. **Inconsistent navigation.** Back-navigation mixes `PrevScreen` with hardcoded Dashboard returns; the command palette is undocumented; jargon-heavy labels ("Neural Graph", "Command Deck").

## User value

- **Simplified configuration (primary user goal):** one Configuration screen, sensible defaults, advanced knobs hidden behind progressive disclosure, one save semantic.
- **Trustworthy connection flow:** guided connect with a real connectivity test and honest state (running / restart-required / error).
- **Predictable keys:** every key visible on a screen does what it says; help is generated from the same registry that dispatches keys.
- **Guided first use:** empty states route to Setup, Connect, or Create-first-memory.

## What changes

1. **Declarative per-screen keymap registry** replaces the global key router. Each screen declares its bindings (key, description, handler); dispatch is registry-driven; the help screen and per-screen footer hints are generated from the registry. Global keys shrink to `ctrl+k` (palette), `?` (help), `esc` (back), `q` (dashboard).
2. **Navigation stack** replaces `PrevScreen`/hardcoded returns: forward navigation pushes, `esc` pops, `q` returns to Dashboard.
3. **Unified Configuration screen** with Essential/Advanced progressive disclosure, one provider enum (8-preset superset), one save semantic (save -> hot-reload where possible; explicit restart banner only when a process restart is truly required), built on the `tui-sync-fix-001` ReloadConfig semantics.
4. **Connect guided flow** as a first-class screen: URL -> token -> connectivity test -> enable sync, with honest state and restart banner. The Auth modal is dissolved into it.
5. **Onboarding empty states** for first-run, empty DB, and unconfigured sync, each routing to Setup / Connect / Create-first-memory.
6. **Removals:** `u` StatsMode, `P` upload toggle, `gg` dead binding, sparkline data loading (7 queries/launch). Timeline is **kept** and made reachable (`T` from Detail/Results/Recent). Embedding Settings is **merged** into Configuration. Dashboard menu is de-duplicated (one Configuration entry, one Connect entry).

## Scope

- **In scope:** `internal/tui/**` (screens, keymaps, navigation, config UI, connect flow, onboarding, help), plus test files. ASCII wireframes for every redesigned screen/modal embedded in `design.md`.
- **Out of scope (non-goals):**
  - No changes to the sync wire protocol, server endpoints, or `internal/platform/server/**`.
  - No changes to `internal/app` or `internal/config` semantics beyond consuming the `tui-sync-fix-001` contract (the config-format split-brain fix — cycler renames the config file without deleting the old one — is a documented follow-up requiring `internal/config/config.go`).
  - No HTML prototypes (user decision: ASCII wireframes only).
  - No new features for the removed no-ops (StatsMode/upload toggle are removed, not implemented).

## Risks

| Risk | Mitigation |
|---|---|
| `update.go` (~2500+ LOC) is a single lease bottleneck; in-flight `tui-sync-fix-001` holds leases on `update.go`, `store.go` | Wave 1 tasks use only new files; extraction waves are sequential chains on `update.go`; DAG tasks touching `update.go`/`store.go` start only after `tui-sync-fix-001` completes |
| Key-removal breaks muscle memory | Help screen and per-screen footers generated from registry; removed keys documented in migration notes |
| Parallel screen migrations conflict on shared files | Mechanical extraction wave first (per-screen `keys_*.go` / `view_*.go` files), then disjoint-file parallel migrations |
| Config unification regressions (token wiping, provider downgrade) | Single save path with golden tests; provider enum unified to the 8-preset superset; token-preservation tests inherited from `tui-sync-fix-001` semantics |
| `view_knowledge.go` holds 8 screens | Mechanical view split into `view_search.go` / `view_detail.go` / `view_sessions.go` before parallel migration |

## Open questions requiring user sign-off

1. **Theme toggle placement:** proposal moves `t` from a global key into Configuration + command palette. Acceptable, or keep a global key (registry-scoped so it can never shadow a screen binding)?
2. **Timeline fate:** proposal keeps and fixes it. If Timeline is not valued, removal would simplify Detail further.
3. **`1-4` workspace switchers:** proposal keeps them as registry-declared bindings (no longer shadowing). Keep or remove?
4. **Config-format cycler:** keep in Advanced with an explicit "old file must be deleted manually" warning now, and file the `internal/config` split-brain fix as a follow-up change?
