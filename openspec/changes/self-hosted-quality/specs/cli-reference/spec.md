# Delta for cli-reference — Authoritative CLI reference document

Adds the CLI reference contract that the docs/CLI-TUI investigation found entirely missing (obs 711: every documented gap — `web key`, `backup`, `update`, `ingest`, `watch`, `code` (9 subcommands / 15 flags), exit codes, `--mode server` + reindex, `status --json`, `doctor --server`, bare-TTY -> TUI, env var matrix).

## ADDED Requirements

### Requirement: REQ-SQ-CR-001: docs/CLI-REFERENCE.md exists as the single authoritative CLI contract
`docs/CLI-REFERENCE.md` MUST document the invocation model (mode triad `local`|`hybrid`|`server`, `cortex serve`, `--mode server`, bare-TTY -> TUI), the exit-code contract (0 success, 1 failure, 2 usage/argument error), and one section per top-level command containing every flag with its default value, the environment variable that overrides it, whether authentication is required, and at least one worked example.

#### Scenario: every shipped command has a section (Happy)
- GIVEN the binary's command surface
- WHEN `docs/CLI-REFERENCE.md` is read
- THEN `serve`, `mcp`, `config`, `auth`, `web key`, `backup`, `update`, `ingest`, `watch`, `code`, `status`, `doctor`, `tui`, and `search` each have their own section with flags, defaults, env vars, auth requirement, and an example

#### Scenario: previously undocumented commands are covered (Edge)
- GIVEN `web key show|regenerate`, `backup`, `update`, `ingest`, `watch`, and `code` with its 9 subcommands and 15 flags
- WHEN each section is read
- THEN its subcommands, flags, defaults, and at least one example are present

#### Scenario: exit codes are explicit (Happy)
- GIVEN a shell script consuming cortex output
- WHEN the exit-code table is read
- THEN 0, 1, and 2 are each defined with the conditions that produce them

### Requirement: REQ-SQ-CR-002: Environment-variable index and auth matrix
`docs/CLI-REFERENCE.md` MUST contain an alphabetical environment-variable index covering `CORTEX_HTTP_TOKEN`, `CORTEX_SERVER_*`, `CORTEX_EMBEDDING_*`, `CORTEX_LLM_*`, `CORTEX_VECTOR_*`, `CORTEX_SERVER_BOOTSTRAP_DEVELOPMENT`, `CORTEX_SERVER_PRINT_BOOTSTRAP`, and the sync/config variables, plus an auth matrix stating per command and per transport (local SQLite, local HTTP, server `--mode server`, MCP) which credential applies.

#### Scenario: env index resolves every documented variable (Happy)
- GIVEN the env-var index
- WHEN each variable is cross-checked against its command section
- THEN no variable appears in one place with a different default or meaning in the other

#### Scenario: auth matrix distinguishes the planes (Edge)
- GIVEN the auth matrix
- WHEN it is read
- THEN local zero-auth mode, local `http.token`, server static bearer, and MCP profile behavior are each stated separately

#### Scenario: bootstrap flags documented as opt-in (Edge)
- GIVEN `CORTEX_SERVER_BOOTSTRAP_DEVELOPMENT` and `CORTEX_SERVER_PRINT_BOOTSTRAP`
- WHEN the index entries are read
- THEN both are marked default-off/production-unsafe with the compose files cited as the default source

#### Scenario: undocumented variable rejected (Error)
- GIVEN a `CORTEX_*` variable that exists in `internal/config` but is absent from the index
- WHEN review cross-checks the source
- THEN the task fails until the variable is indexed

### Requirement: REQ-SQ-CR-003: docs/CLI.md demoted to a pointer
`docs/CLI.md` MUST be reduced to a short index that points to `docs/CLI-REFERENCE.md` for command detail; it MUST NOT duplicate flag tables, so no two repository documents can disagree about CLI behavior.

#### Scenario: single source of truth (Happy)
- GIVEN `docs/CLI.md` after the change
- WHEN it is read
- THEN it links to `docs/CLI-REFERENCE.md` and contains no command flag table

#### Scenario: dangling link rejected (Error)
- GIVEN the pointer edit
- WHEN `go test -v -count=1 ./bench` runs
- THEN the documentation gate passes and `docs/CLI-REFERENCE.md` exists at the linked path

#### Scenario: pointer keeps entry value (Edge)
- GIVEN a reader arriving from the docs index
- WHEN `docs/CLI.md` is opened
- THEN it still explains where to start and links to the reference instead of being an empty stub

#### Scenario: duplicated flags fail review (Error)
- GIVEN a flag table still present in `docs/CLI.md`
- WHEN review compares it with the reference
- THEN the task fails until the duplicate is removed

### Requirement: REQ-SQ-CR-004: Deprecated and retired surface appendix
`docs/CLI-REFERENCE.md` MUST end with a deprecated appendix that records retired or deprecated surface: `--tools=admin|temporal` (retired per `docs/MCP.md:18`), the retired `cortex-web` container image, `mem_*`/Engram framing, and the v1 root migrations, each with the supported replacement.

#### Scenario: retired flags are not advertised as current (Happy)
- GIVEN the appendix
- WHEN it is read alongside the `mcp` command section
- THEN `admin` and `temporal` appear only in the appendix as retired, and the `mcp` section documents `agent`, `dev`, and `minimal`

#### Scenario: every retired item names its replacement (Edge)
- GIVEN the appendix entries
- WHEN each entry is read
- THEN it states the supported replacement (for example embedded web instead of the `cortex-web` image)

#### Scenario: retired surface still discoverable (Happy)
- GIVEN a user migrating from an older version
- WHEN they search the reference for a retired flag
- THEN the appendix returns the retirement notice instead of silence

#### Scenario: retired item advertised as current (Error)
- GIVEN `admin` or `temporal` listed under a live command section
- WHEN review finds it
- THEN the task fails until it is moved to the appendix

### Requirement: REQ-SQ-CR-005: Reference content is anchored to real behavior
Every flag, default, exit code, and command in `docs/CLI-REFERENCE.md` MUST match the shipped binary: the implementer verifies against `cortex help` output, `internal/cli`, and `internal/config` defaults before completion, and no invented flag may appear.

#### Scenario: no phantom flags (Edge)
- GIVEN the finished document
- WHEN a reviewer cross-checks any documented flag against `internal/cli`
- THEN the flag exists with the documented default

#### Scenario: real flags missing (Error)
- GIVEN a command surface entry absent from the document
- WHEN review finds it
- THEN the task fails until the entry is added

#### Scenario: default value mismatch rejected (Error)
- GIVEN a documented default that differs from `internal/config` defaults
- WHEN the cross-check runs
- THEN the task fails until the document states the effective default

#### Scenario: examples are executable (Happy)
- GIVEN each worked example
- WHEN it is executed against a local instance
- THEN it completes with the documented exit code

### Requirement: REQ-SQ-CR-006: Documentation gate stays green
Authoring the reference MUST NOT break `go test -v -count=1 ./bench`, and the new document MUST be linked from `docs/README.md` index (tracked by the index task).

#### Scenario: gate green (Happy)
- GIVEN the new document
- WHEN `go test -v -count=1 ./bench` runs
- THEN the suite exits 0

#### Scenario: index link present (Edge)
- GIVEN `docs/README.md` after the index task
- WHEN the index is searched
- THEN `CLI-REFERENCE.md` is listed

#### Scenario: gate regression blocks the task (Error)
- GIVEN a red `./bench` suite after the edit
- WHEN the verification command runs
- THEN the task may not transition to `in_review`

### Requirement: REQ-SQ-CR-007: Runtime mode label and doctor probe reflect self-hosted reality
The `Server (PostgreSQL Multi-Tenant)` mode label MUST be replaced with single-tenant self-hosted wording at `internal/cli/cli.go:2189` and `internal/config/config.go:1360,1373`, and the `doctor` web probe MUST stop targeting the retired `:3000` container at `internal/cli/cli.go:1765-1773` and instead probe the configured serve address where the embedded web is actually mounted.

#### Scenario: no multi-tenant label in output (Happy)
- GIVEN server mode selected
- WHEN `cortex status` (or the mode label path) prints
- THEN no `Multi-Tenant` wording appears anywhere in CLI or config output

#### Scenario: doctor probes the real port (Edge)
- GIVEN default configuration (`http.port=7438`)
- WHEN `cortex doctor --server` runs against a live server
- THEN the probe targets `http://localhost:7438` and reports the embedded web status instead of a permanent `:3000` miss

#### Scenario: tests stay green (Happy)
- GIVEN the string and probe edits
- WHEN `go test -v -count=1 ./internal/cli ./internal/config` runs
- THEN both suites pass

#### Scenario: probe failure is non-fatal (Edge)
- GIVEN no server listening during `doctor`
- WHEN the probe fails
- THEN the command still prints the informational not-running line and keeps its existing exit-code semantics
