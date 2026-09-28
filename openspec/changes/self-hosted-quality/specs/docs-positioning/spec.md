# Delta for docs-positioning — Staleness waves, matrix truth, profile coherence

Adds the documentation and positioning contract for the docs/CLI-TUI investigation (obs 711) and the fourth audit's coverage-drift finding. `docs/server-saas.md` naming is accepted (no rename task) per the orchestrator decision recorded in REQ-SQ-DOC-007.

## ADDED Requirements

### Requirement: REQ-SQ-DOC-001: HIGH-severity deployment staleness fixed (staleness wave 1)
`docs/INSTALLATION.md` (:57-66, :70, :90-97, :300), `docs/SERVER.md` (:9, :15, :19), and `docs/RAILWAY_DEPLOYMENT.md` (:20, :39, :53, :74, :76, :127) MUST be rewritten to the embedded-web reality: no `cortex-web` container image, no `NEXT_PUBLIC_*` build-time injection, no `:3000` UI port, the web UI served by the single binary on `:7438` (or the configured `http.port`), and single-tenant self-hosted framing instead of multi-tenant-era instructions.

#### Scenario: retired image gone (Happy)
- GIVEN the three documents after the wave
- WHEN they are searched for `cortex-web`, `:3000`, and `NEXT_PUBLIC_`
- THEN no retired instruction remains

#### Scenario: operator path works (Edge)
- GIVEN a new operator following `docs/INSTALLATION.md`
- WHEN they complete the steps
- THEN they reach a single container on `:7438` with the embedded UI and the documented bootstrap credentials flow

#### Scenario: cross-document port consistency (Edge)
- GIVEN the three documents
- WHEN their port references are compared
- THEN they all agree with the compose files (`7438:7438`) and no document mentions a second UI container

#### Scenario: stale instruction survives (Error)
- GIVEN a remaining `:3000` or `NEXT_PUBLIC_` reference found by review
- WHEN the search runs again
- THEN the task fails until the line is rewritten

### Requirement: REQ-SQ-DOC-002: Matrix documents match observed gates and surfaces
`docs/verification-matrix.md:38` MUST stop advertising `:3000` (the compose smoke gate uses `:7438` for the UI as line 39 already states), and `docs/capability-matrix.md:90-98` MUST gain the missing CLI rows (`web`, `backup`, `update`, `ingest`, `migrate`, `code`), a complete TUI row (not only `t`/`L`), the current `Next.js 15` label, and rows for Embedded-Web, `web key`, parity endpoints, and `playwright-e2e`.

#### Scenario: no contradictory port (Happy)
- GIVEN `docs/verification-matrix.md:38`
- WHEN the compose smoke row is read
- THEN only `:7438` health endpoints are asserted for the UI

#### Scenario: capability rows complete (Edge)
- GIVEN `docs/capability-matrix.md`
- WHEN the CLI and TUI sections are read
- THEN every command group and every listed TUI capability maps to a documented row

#### Scenario: gate row still matches CI (Edge)
- GIVEN the edited verification matrix
- WHEN its command cells are compared with `.github/workflows/ci.yml`
- THEN each command string still matches the workflow

#### Scenario: matrix row invented (Error)
- GIVEN a capability row with no shipped command or test behind it
- WHEN review checks it against the binary or CI
- THEN the task fails until the row is removed

### Requirement: REQ-SQ-DOC-003: Documentation indexes are complete and link-clean
`docs/README.md` MUST list every document under `docs/` (the investigation counted 8 missing entries) and MUST remove or correct the dead `docs/remediation/` pointer; `README.md:180` MUST advertise only supported profiles (`agent`, `dev`, `minimal`) instead of the retired `admin`/`temporal` set.

#### Scenario: index round-trips (Happy)
- GIVEN `docs/README.md`
- WHEN each `.md` file in `docs/` is looked up in the index
- THEN it is listed exactly once

#### Scenario: dead pointer removed (Edge)
- GIVEN `docs/README.md:23`
- WHEN the remediation sentence is checked
- THEN no reference to a nonexistent `docs/remediation/` directory remains

#### Scenario: new documents indexed (Edge)
- GIVEN `docs/CLI-REFERENCE.md` and `docs/TUI-GUIDE.md`
- WHEN the index is read
- THEN both are listed with one-line descriptions

#### Scenario: broken link found (Error)
- GIVEN an index entry whose target file does not exist
- WHEN the link is followed
- THEN the task fails until the entry is corrected

### Requirement: REQ-SQ-DOC-004: Profile documentation is internally consistent
`docs/ARCHITECTURE.md:216-235` MUST list all MCP profiles including `dev` and `minimal` and mention the embedded web UI; `AGENTS.md` profile clauses MUST match (`agent`, `dev`, `minimal` supported; `admin`, `temporal` retired); `docs/MCP.md` remains the retirement source of truth and the three documents MUST not contradict each other.

#### Scenario: profile sets agree (Happy)
- GIVEN the three documents
- WHEN their profile lists are compared
- THEN each names the same supported set and the same retired set

#### Scenario: embedded UI acknowledged (Edge)
- GIVEN `docs/ARCHITECTURE.md`
- WHEN the runtime composition section is read
- THEN the embedded web surface is mentioned alongside profiles

#### Scenario: architecture section covers mode triad (Edge)
- GIVEN `docs/ARCHITECTURE.md:216-235`
- WHEN it is read
- THEN local/hybrid/server composition and the profile set are both described consistently with `internal/platform/mode.go`

#### Scenario: contradiction detected (Error)
- GIVEN two documents listing different supported profiles
- WHEN the comparison runs
- THEN the task fails until they converge on `docs/MCP.md` as source of truth

### Requirement: REQ-SQ-DOC-005: Configuration and graph-intelligence gaps closed
`docs/CONFIGURATION.md` MUST document the `web.*` keys (`web.enabled`, `web.host`, `web.port`, `web.key_file`) with their environment variables and defaults, and `docs/GRAPH_INTELLIGENCE.md:59-80` MUST document `cortex code` usage (its subcommands and flags) so the graph-intelligence story covers code intelligence.

#### Scenario: web keys discoverable (Happy)
- GIVEN `docs/CONFIGURATION.md`
- WHEN the configuration table is searched
- THEN every `web.*` key appears with default, env var, and purpose

#### Scenario: code command documented (Edge)
- GIVEN `docs/GRAPH_INTELLIGENCE.md:59-80`
- WHEN the section is read
- THEN `cortex code` subcommands and flags match `docs/CLI-REFERENCE.md`

#### Scenario: defaults match the schema (Edge)
- GIVEN each documented `web.*` default
- WHEN it is compared with `internal/config`
- THEN the values agree

#### Scenario: key missing from docs (Error)
- GIVEN a `web.*` key defined in `internal/config` but absent from the table
- WHEN the cross-check runs
- THEN the task fails until the row is added

### Requirement: REQ-SQ-DOC-006: Diagram assets carry current labels
`docs/assets/architecture.svg:104` and `docs/assets/memory_lifecycle.svg:62` MUST replace their Multi-Tenant labels with single-tenant self-hosted wording, and MUST remain valid SVG (viewBox and element structure untouched beyond the text nodes).

#### Scenario: labels updated (Happy)
- GIVEN both SVG files
- WHEN they are searched for `Multi-Tenant`
- THEN no match remains

#### Scenario: assets still render (Edge)
- GIVEN the edited SVGs
- WHEN they are opened by the documentation renderer
- THEN they parse without XML errors

#### Scenario: other stale labels swept (Edge)
- GIVEN neighboring text nodes in the same diagrams
- WHEN they are read
- THEN SaaS-era labels such as tenant provisioning flows are corrected consistently

#### Scenario: structural damage (Error)
- GIVEN an edit that changes viewBox or element structure
- WHEN the SVG is parsed
- THEN the task fails until only text nodes differ

### Requirement: REQ-SQ-DOC-007: Coverage threshold truth and accepted naming
`docs/COVERAGE.md:11` and the `AGENTS.md` command table MUST state the enforced truth — CI global gate 80.0% (`.github/workflows/ci.yml:199`), web V8 gates 70/60/55, initiative floor 80.0% never lowered — replacing the stale 70% claim; and `docs/server-saas.md` MUST keep its filename as an accepted name with no rename task in this change.

#### Scenario: docs match CI (Happy)
- GIVEN the edited coverage documentation
- WHEN `ci.yml` threshold logic is compared with `docs/COVERAGE.md`
- THEN both state 80.0 as the global Go floor

#### Scenario: no rename task exists (Edge)
- GIVEN the task list
- WHEN it is searched for a server-saas rename
- THEN no such task exists and `docs/server-saas.md` remains at its path

#### Scenario: two-tier statement explicit (Edge)
- GIVEN the coverage policy text
- WHEN it is read
- THEN the CI floor and the initiative floor are distinguished from the web V8 gates

#### Scenario: gate silently lowered (Error)
- GIVEN any edit reducing a threshold in docs or CI
- WHEN the diff is reviewed
- THEN the change is rejected under REQ-SQ-SEC-010
