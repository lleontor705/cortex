# Specification Delta: TUI Sync & Connection Flows

Change: `tui-redesign` · Requirements use RFC 2119 keywords (MUST/SHOULD/MAY). Builds on the in-flight `tui-sync-fix-001` semantics (ReloadConfig restarts the sync loop; token persistence without wiping http.token; honest toasts).

## REQ-SYNC-001: Guided connect flow

Connecting to a server MUST be a first-class guided flow with three steps: server URL (+ mode), token, connectivity test. The flow MUST perform a real connectivity test before saving and MUST present the result before the user commits. The Auth modal MUST be dissolved into this flow; the server URL MUST have exactly one writable TUI surface.

```gherkin
Scenario: Successful connect
  Given the user starts the Connect flow from the Dashboard or Configuration
  When they enter a valid HTTPS URL and token and run the test
  Then the flow shows per-check results (reachable, token accepted, sync endpoint OK)
  And on commit the config is saved and the sync loop starts or hot-reloads
  And the final state reads "Connected — background sync running"

Scenario: Failed connect saves nothing
  Given the connectivity test fails (unreachable, 401, or sync endpoint error)
  When the test completes
  Then the flow shows the failing check with the reason
  And no sync configuration is written to the config file
```

## REQ-SYNC-002: Honest mode selection

Selecting Remote MCP Proxy mode MUST visibly state that it disables background sync. The TUI MUST NOT silently write `sync.enabled=false` as a side effect of a mode selection without telling the user.

```gherkin
Scenario: Proxy mode states its consequence
  Given the user is on Connect step 1
  When they select "Remote MCP proxy"
  Then the UI states that proxy mode disables background sync
  And the saved config reflects that choice explicitly
```

## REQ-SYNC-003: Honest sync state everywhere

The Dashboard header chip and Configuration server status MUST reflect the real sync state: `running`, `disabled`, `not configured`, `restart required`, or `error: <reason>`. The TUI MUST NOT display a success state that the sync subsystem does not actually have. Sync defaults to disabled and the UI MUST say so rather than implying an active connection.

```gherkin
Scenario: Header chip matches reality
  Given sync is configured but the loop failed to start
  When the Dashboard renders
  Then the header chip shows "error: <reason>"
  And no green "connected" indicator is shown

Scenario: Unconfigured sync
  Given sync.enabled is false and no URL is set
  When the Dashboard renders
  Then the header chip shows "not configured"
  And the unconfigured-sync empty state offers the Connect flow
```

## REQ-SYNC-004: Restart banner semantics

When a sync-related save requires a process restart (only where hot-reload is genuinely impossible per the `tui-sync-fix-001` contract), the TUI MUST show an explicit restart banner naming the affected fields. Otherwise the save MUST hot-reload and MUST NOT mention restart.

```gherkin
Scenario: Sync interval change hot-reloads
  Given the sync loop is running
  When the user changes the sync interval and saves
  Then the loop restarts with the new interval without a process restart
  And no restart banner is shown
```

## REQ-SYNC-005: Onboarding routing for unconfigured sync

When sync is unconfigured, the TUI MUST surface a non-intrusive empty-state prompt offering the Connect flow, dismissible for the session.

```gherkin
Scenario: Dismissible sync prompt
  Given sync is unconfigured
  When the Dashboard renders
  Then a prompt offers "Connect to a server" with a skip option
  And choosing skip hides the prompt until the next TUI launch
```

## Non-goals
- No changes to the sync wire protocol, transport policy, or server endpoints.
- No changes to `internal/app` ReloadConfig internals beyond consuming the `tui-sync-fix-001` contract.
- No automatic sync enablement; the user must explicitly opt in via the Connect flow.
