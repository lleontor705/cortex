# Delta for web-key

## ADDED Requirements

### Requirement: REQ-KEY-001: first-boot web access key generation
The system MUST generate a unique web access key (32 bytes from `crypto/rand`, prefixed secret in the style of `internal/identity/apikey.go`) exactly once at first serve-boot when no key file exists, and MUST print the plaintext to the console only at generation or regeneration time.

#### Scenario: key minted on first serve (Happy path)
- GIVEN no web key file exists in the Cortex config directory
- WHEN `cortex serve` (or `--mode server`) boots
- THEN a key is generated, persisted, and the plaintext is displayed once with an instruction that it will not be shown again

#### Scenario: concurrent first boot (Edge case)
- GIVEN two processes race to create the key file
- WHEN both attempt first-boot generation
- THEN exactly one key file wins with 0600 creation, the loser loads the winner's key, and no plaintext is double-printed

#### Scenario: key file directory unwritable (Error state)
- GIVEN the data directory cannot be written
- WHEN first-boot generation is attempted
- THEN serve fails closed with a clear error before accepting any connection, and no partial file remains

### Requirement: REQ-KEY-002: at-rest key file format
The key store MUST persist only a key identifier prefix and an HMAC-SHA256 digest of the secret in a dedicated file (`web.key` by default) with 0600 permissions; the plaintext MUST NOT be stored in the config file or logs, and verification MUST use constant-time comparison (`hmac.Equal`).

#### Scenario: verify valid key (Happy path)
- GIVEN a persisted key record
- WHEN verification is called with the original plaintext
- THEN it succeeds via prefix lookup plus constant-time digest compare
- AND the file content alone never reveals a usable secret

#### Scenario: permissions tightened on read (Edge case)
- GIVEN a key file found with group/other-readable modes
- WHEN the store loads it
- THEN the load fails closed or the mode is repaired to 0600 per documented behavior, deterministically

#### Scenario: tampered digest (Error state)
- GIVEN the stored digest was modified
- WHEN verification runs with any secret
- THEN verification rejects with a deterministic invalid-key error

### Requirement: REQ-KEY-003: CLI key commands
The CLI MUST expose `cortex web key show` (prints where the key lives and whether one exists; never prints the plaintext) and `cortex web key regenerate` (mints a replacement, persists it, and prints the new plaintext exactly once), fitting the existing scriptable command standards.

#### Scenario: regenerate rotates atomically (Happy path)
- GIVEN a running installation with an existing key
- WHEN `cortex web key regenerate` runs
- THEN the old key stops verifying, the new plaintext is shown once, and exit code is 0

#### Scenario: show without key (Edge case)
- GIVEN no key file exists yet
- WHEN `cortex web key show` runs
- THEN it reports the absence and the first-boot generation behavior without creating a key

#### Scenario: regenerate on unwritable store (Error state)
- GIVEN the key file cannot be written
- WHEN regenerate runs
- THEN the command fails with a non-zero exit and the previous key remains valid and unchanged

### Requirement: REQ-KEY-004: credential namespace isolation
The web key MUST live in its own namespace and MUST NEVER be used as, defaulted to, or fall back to `http.token` or the replication/sync token; the web surface MUST authenticate with the web key and API endpoints keep their existing bearer semantics.

#### Scenario: sync fallback unchanged (Happy path)
- GIVEN hybrid mode with sync enabled and `sync.token_env` unset (current fallback to `cfg.HTTP.Token` at `internal/app/app.go`)
- WHEN sync starts
- THEN it uses the same credential as today and the web key is not consulted anywhere in the sync path

#### Scenario: web key never accepted by API auth (Edge case)
- GIVEN a valid web key
- WHEN it is presented to a protected `/api/*` endpoint requiring `http.token`/principal tokens
- THEN the API rejects it independently of web-surface acceptance

#### Scenario: missing key on web surface (Error state)
- GIVEN serve running with web mounted and no key provided by the client
- WHEN a non-asset, non-`/health` web request arrives
- THEN the request is rejected with an unauthenticated response before any UI data is exposed
