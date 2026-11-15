# Specification Delta: TUI Configuration

Change: `tui-redesign` · Requirements use RFC 2119 keywords (MUST/SHOULD/MAY).

## REQ-CFG-001: Single configuration surface

The TUI MUST present exactly one Configuration screen. The Embedding Settings screen and the Auth modal MUST be removed as separate configuration surfaces. The command palette MUST NOT be the only path to any configuration capability.

```gherkin
Scenario: All configuration reachable from one screen
  Given the user opens the Dashboard
  When they select "Configuration..."
  Then the unified Configuration screen opens with Essential and Advanced tabs
  And every one of the 17 previously-scattered config keys is editable from one of these two tabs
  And no configuration capability requires the command palette to reach

Scenario: Embedding settings merged
  Given the user is on the Configuration screen
  When they switch to the Advanced tab
  Then embedding provider, model, vector dimension, and Ollama autostart are editable there
  And the standalone Embedding Settings screen no longer exists
```

## REQ-CFG-002: Single provider enum

The AI/LLM provider enum and the embedding provider enum MUST be the same 8-preset superset (None, Ollama, OpenAI, Anthropic, OpenRouter, Groq, DeepSeek, Custom). Saving one surface MUST NOT downgrade a provider value set on another surface.

```gherkin
Scenario: No provider downgrade on save
  Given the embedding provider is configured as "Anthropic"
  When the user saves the Configuration screen without changing the embedding provider
  Then the persisted embedding provider remains "Anthropic"
  And no save path rewrites it to a value from a smaller enum
```

## REQ-CFG-003: One save semantic with honest feedback

Saving MUST follow exactly one semantic: write config, then hot-reload everything reloadable in the running process (including the sync loop, per the `tui-sync-fix-001` ReloadConfig contract). A restart banner MUST appear if and only if at least one changed field genuinely requires process restart (database path, config format). Success toasts MUST state exactly what was applied; the TUI MUST NOT show a success toast for an action that did not take effect.

```gherkin
Scenario: Hot-reloadable save
  Given the user changes the embedding model on the Configuration screen
  When they press "s" to save
  Then the config file is written
  And the running process reloads the changed setting without restart
  And the toast reads "Saved — changes applied"
  And no restart banner is shown

Scenario: Restart-required save
  Given the user changes the database path on the Advanced tab
  When they save
  Then the config file is written
  And an amber restart banner lists exactly the fields requiring restart
  And the banner states that all other changes are already applied

Scenario: Failed save
  Given the config file cannot be written
  When the save attempt completes
  Then the TUI shows an error state with the reason
  And no success toast is shown
```

## REQ-CFG-004: Token preservation on save

Saving configuration from the TUI MUST NOT erase an existing `http.token` when the token field is left empty. This inherits the `tui-sync-fix-001` contract.

```gherkin
Scenario: Empty token field preserves existing token
  Given http.token is set in the config file
  When the user saves the Configuration screen leaving the token field empty
  Then http.token in the config file is unchanged
```

## REQ-CFG-005: Progressive disclosure

The Essential tab MUST expose only: AI provider/model/base URL, embedding provider/model, and server connection status with a Connect entry. All other knobs (storage, HTTP API, MCP, sync interval/token env, vector dim, autostart, theme, config format) MUST live on the Advanced tab. Sensible defaults MUST be pre-filled so a user who never opens Advanced gets a working setup.

```gherkin
Scenario: Defaults work without Advanced
  Given a fresh install with no config file
  When the user opens Configuration and saves the Essential tab unchanged
  Then the resulting config uses the documented defaults (Ollama, nomic-embed-text, sync disabled)
  And the TUI operates correctly with those defaults
```

## REQ-CFG-006: Config format change warning

Until the `internal/config` split-brain follow-up ships, changing the config format on the Advanced tab MUST display a warning that the old config file is not deleted and must be removed manually.

```gherkin
Scenario: Format cycler warns about split-brain
  Given the user is on the Advanced tab
  When they change the config format from yaml to json
  Then a visible warning states the old file must be deleted manually
```

## Non-goals
- No changes to config file schema, `internal/config` loading semantics, or secret-clearing rules beyond consuming `tui-sync-fix-001`.
- No re-implementation of the removed no-op toggles (StatsMode, upload toggle).
