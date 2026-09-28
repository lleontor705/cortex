# Delta for web-config

## ADDED Requirements

### Requirement: REQ-CFG-001: web.* configuration namespace
The local configuration system MUST expose a `web` namespace (sibling of `http` in `internal/config`) with YAML/JSON/TOML tags for `enabled` (default true), `port`/`host` overrides that default to the `http` listener, and a `key_file` override, while enforcing the Zero-Bloat rule: local config files MUST NOT serialize server-only or multi-tenant web parameters.

#### Scenario: web defaults round-trip (Happy path)
- GIVEN a default configuration saved as YAML
- WHEN the config is serialized and reloaded
- THEN the `web` section persists only non-default user-settable fields and reloads identically
- AND no server-specific or pragma-only fields appear in the saved file

#### Scenario: key_file override outside config dir (Edge case)
- GIVEN `web.key_file` points to an existing writable path
- WHEN the web key store resolves its storage location
- THEN the override path is used instead of the default `web.key` in the Cortex config directory

#### Scenario: invalid web values rejected (Error state)
- GIVEN a config with port outside 1-65535 or a negative-hostile value
- WHEN `config.Validate` runs
- THEN validation fails with a deterministic field error and the file is not mutated
