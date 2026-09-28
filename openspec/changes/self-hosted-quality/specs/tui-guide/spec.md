# Delta for tui-guide — First TUI user guide

Adds the TUI documentation contract; the investigation found **no TUI document exists** despite 15 screens, 5 overlays, a dynamic theme engine, and per-screen key tables.

## ADDED Requirements

### Requirement: REQ-SQ-TG-001: docs/TUI-GUIDE.md documents launch, screen map, and navigation
`docs/TUI-GUIDE.md` MUST document how the TUI launches (bare invocation from an interactive terminal, `cortex tui`), a screen map with a mermaid diagram covering all 15 screens and 5 overlays, and the navigation model between them.

#### Scenario: screen inventory complete (Happy)
- GIVEN the TUI source (`internal/tui`)
- WHEN the screen map is read
- THEN dashboard, search, SearchResults, detail, graph, archive, health, embedding, localconfig, setup, models, usage stats and every remaining screen and overlay are named with their entry path

#### Scenario: mermaid renders (Edge)
- GIVEN the mermaid block
- WHEN it is validated by review
- THEN it parses as a flowchart with no orphan node references

#### Scenario: launch paths documented (Happy)
- GIVEN a new user with the binary installed
- WHEN the launch section is read
- THEN both bare-TTY startup and `cortex tui` are documented, including the non-interactive fallback

#### Scenario: screen present in code but missing from the map (Error)
- GIVEN `internal/tui` screens not represented in the diagram
- WHEN review diffs the inventory
- THEN the task fails until the map is completed

### Requirement: REQ-SQ-TG-002: Keybinding matrix for global and per-screen keys
`docs/TUI-GUIDE.md` MUST contain a global key table (`?`, `ctrl+k`, `1-4`, `n`, `t`, `L`, `u`, `P`, `p`, `v`, `ctrl+c`) and per-screen tables covering at minimum dashboard (`1-9`, `s`, `r`, `g`, `c`, `i`, `u`), search (`tab` = code mode, `f`, history), SearchResults (`t`, `f`, `d`, `p`), detail (page up/down), graph (`enter`, `r`), archive (`enter`, `u`, `d`, `f`), health (`tab`), embedding (`h`, `l`, `space`, `r`, `x`), localconfig (`tab`, `t`, `i`, `s`), and setup (`enter`, `p`, `c`).

#### Scenario: keys match the bindings (Happy)
- GIVEN each documented key
- WHEN it is cross-checked against `internal/tui/update.go` and `keys_coverage_test.go`
- THEN every listed key is bound on the documented screen

#### Scenario: shadowed keys are disclosed (Edge)
- GIVEN `p` on the update screen (`internal/tui/update.go:734`)
- WHEN the table and quirks section are read
- THEN the shadowing is stated explicitly rather than documented as the primary action

#### Scenario: navigation keys documented (Edge)
- GIVEN the global table
- WHEN it is read
- THEN `1-4` screen switching and `?` help are documented alongside `ctrl+c` exit

#### Scenario: key documented but unbound (Error)
- GIVEN a table entry with no matching binding in `update.go`
- WHEN review checks it
- THEN the task fails until the row is corrected or removed

### Requirement: REQ-SQ-TG-003: Theme engine, status bar, and command palette
`docs/TUI-GUIDE.md` MUST describe the theme engine (`ApplyTheme`/`ToggleTheme` with `t`, persistence behavior and how to verify it), the status bar anatomy, the Command Deck header, and the command palette contents, including what each indicator means.

#### Scenario: theme persistence verifiable (Happy)
- GIVEN the theming section
- WHEN the documented verification steps are followed
- THEN a user can toggle the theme, restart, and observe the persisted selection

#### Scenario: status bar segments enumerated (Edge)
- GIVEN the status bar anatomy section
- WHEN it is read
- THEN every segment the model renders is named with its meaning

#### Scenario: palette contents match the implementation (Edge)
- GIVEN the command palette list
- WHEN it is compared with the palette source
- THEN every entry exists with the documented action

#### Scenario: theme claim contradicted by behavior (Error)
- GIVEN a documented persistence step that does not persist the theme
- WHEN the verification is executed
- THEN the task fails until the documentation matches observed behavior

### Requirement: REQ-SQ-TG-004: Workflows, CLI-TUI-MCP interplay, and known quirks
`docs/TUI-GUIDE.md` MUST include task-oriented workflows (first-run setup, search -> detail -> graph, embedding configuration, local config editing), a section on how the TUI, CLI, and MCP profiles interact over the same SQLite store, and a Known quirks section listing the unbound digit 10 and the inaccurate `f`/`p` help-view entries.

#### Scenario: quirks are actionable (Edge)
- GIVEN the quirks section
- WHEN each quirk is read
- THEN it names the screen and the expected vs actual behavior

#### Scenario: guide stays gated (Happy)
- GIVEN the finished document
- WHEN `go test -v -count=1 ./bench` runs
- THEN the documentation gate passes

#### Scenario: interplay explains shared state (Happy)
- GIVEN the interplay section
- WHEN it is read
- THEN it states that TUI, CLI, and MCP operate on the same store and warns about concurrent sessions

#### Scenario: quirk fixed but still documented (Error)
- GIVEN a quirk that no longer reproduces
- WHEN verification runs against the current binary
- THEN the row must be removed or re-scoped before the task completes
