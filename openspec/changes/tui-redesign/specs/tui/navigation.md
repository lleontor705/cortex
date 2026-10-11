# Specification Delta: TUI Navigation & Keymaps

Change: `tui-redesign` · Requirements use RFC 2119 keywords (MUST/SHOULD/MAY).

## REQ-NAV-001: Declarative per-screen keymap registry

Key dispatch MUST be driven by a declarative per-screen keymap registry. There MUST be no global key router that runs before screen handlers. A key declared by the focused screen's keymap MUST always reach that screen's handler.

```gherkin
Scenario: No shadowing by construction
  Given any screen with a declared binding for key K
  When the user presses K on that screen
  Then the screen's handler for K runs
  And no global handler intercepts K first

Scenario: Timeline reachable
  Given the user is on Observation Detail, Search Results, or Recent
  When they press "T"
  Then the Timeline screen opens for the focused observation
  And the theme toggle does not intercept the key

Scenario: Typing in search
  Given the search input is focused (InputMode)
  When the user types "f"
  Then the character "f" is inserted into the input
  And no binding fires
```

## REQ-NAV-002: Global keys are minimal and registry-declared

The set of global keys MUST be exactly: `ctrl+k` (command palette), `?` (help), `esc` (back/pop), `q` (dashboard). Global keys MUST be evaluated only when no screen binding matches. The keys `u` (StatsMode), `P` (upload toggle), and `gg` MUST be removed entirely.

```gherkin
Scenario: Removed no-op keys do nothing
  Given the user is on the Dashboard
  When they press "u", then "P", then "g" twice
  Then no state changes and no toast claims an action occurred
```

## REQ-NAV-003: Navigation stack

Forward navigation MUST push onto a navigation stack. `esc` MUST pop the stack (Dashboard is the floor; `esc` on Dashboard does nothing). `q` MUST reset the stack to Dashboard. The `PrevScreen` field MUST be deleted. The header MUST render a breadcrumb derived from the stack.

```gherkin
Scenario: Deep navigation round trip
  Given the user navigates Dashboard -> Search -> Results -> Detail -> Timeline
  When they press "esc" four times
  Then they return Detail -> Results -> Search -> Dashboard in reverse order
  And pressing "esc" once more on Dashboard changes nothing

Scenario: q resets to Dashboard
  Given the user is on Timeline at stack depth 5
  When they press "q"
  Then the stack resets and the Dashboard is shown
```

## REQ-NAV-004: Help and footers generated from the registry

The Help screen and per-screen footer hints MUST be generated from the same keymap registry that dispatches keys. A binding that exists in the registry MUST appear in help; a binding shown in a footer MUST exist in the registry. Health-screen controls MUST perform their described actions or not be shown.

```gherkin
Scenario: Help matches reality
  Given the registry declares bindings B for screen S
  When the user opens Help from screen S
  Then the help lists exactly the global keys plus B for S
  And no key is listed that has no handler

Scenario: Health controls are real
  Given the user is on the Memory Health screen
  When they press a key shown in the footer
  Then the described action executes
```

## REQ-NAV-005: Dashboard information architecture

The Dashboard menu MUST be grouped (MAIN content screens, SYSTEM operations), MUST contain exactly one Configuration entry and one Connect entry, and MUST NOT contain duplicate entries routing to the same screen. Labels MUST avoid internal jargon.

```gherkin
Scenario: No duplicate destinations
  Given the Dashboard menu is rendered
  When the user inspects the entries
  Then no two entries navigate to the same screen
  And exactly one entry opens Configuration and one opens the Connect flow
```

## Non-goals
- No mouse-driven navigation model; mouse wheel scroll behavior is preserved as-is.
- No remappable user keybindings (registry is developer-declared; user remapping is a future change).
