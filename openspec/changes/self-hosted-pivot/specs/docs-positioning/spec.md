# Delta for docs-positioning — Self-hosted positioning, config surface, and addendum supersession

Adds the documentation and positioning contract (design decision D6).

## ADDED Requirements

### Requirement: REQ-SH-040: server-saas.md retired in favor of a self-host guide
`docs/server-saas.md` MUST be retired (content replaced by a self-hosted deployment guide, or superseded by an equivalent guide under `docs/`), and `AGENTS.md` server clauses MUST be rewritten for single-tenant self-hosting: the `AuthorizedStore`/`AuthorizedContext` clause survives with an explicit note that the principal is the synthetic configured constant, and the SaaS control-plane pointer to `docs/server-saas.md` MUST be removed.

#### Scenario: no SaaS framing remains (Happy)
- GIVEN the change applied
- WHEN `grep -rn "server-saas" AGENTS.md docs README.md` runs
- THEN no link to the retired document remains and the self-host guide is linked instead

#### Scenario: AuthorizedStore contract survives (Happy)
- GIVEN the rewritten AGENTS.md
- WHEN its server clauses are read
- THEN tenant/workspace still come from verified principal grants, never client input

#### Scenario: self-host guide covers the operator path (Edge)
- GIVEN the replacement guide
- WHEN it is read end to end
- THEN it documents single-tenant configuration keys, the 3-DSN bootstrap, the static bearer, and contains no billing/SSO/multi-tenant provisioning instructions

### Requirement: REQ-SH-041: Positioning sweep and config surface removal
`README.md`, `docs/embedded-web.md`, `docs/capability-matrix.md` (including the `cortex_migration_ledger` misname at line 47), `docs/verification-matrix.md`, `docs/CONFIGURATION.md`, `docs/RAILWAY_DEPLOYMENT.md`, `docs/SERVER.md`, `docs/HTTP-API.md`, `docs/ARCHITECTURE.md`, `docs/INSTALLATION.md`, `internal/platform/mode.go` comments, and `internal/config/config.go` MUST be swept to single-tenant self-hosted positioning; the `ServerConfig.MultiTenant` field MUST be removed together with its test assertions.

#### Scenario: configuration no longer offers multi_tenant (Edge)
- GIVEN `multi_tenant` set in YAML/JSON/TOML configuration
- WHEN `cortex config validate` or configuration loading runs
- THEN the key is unknown/ignored by the schema and no code path branches on it

#### Scenario: capability matrix ledger name fixed (Happy)
- GIVEN `docs/capability-matrix.md:47`
- WHEN the row is read
- THEN it names `cortex_migration_ledger` correctly

#### Scenario: docs contract stays green (Happy)
- GIVEN the sweep applied
- WHEN `go test -v -count=1 ./bench` runs
- THEN the documentation contract passes with no broken pins

### Requirement: REQ-SH-042: Closed addendum receives a supersession pointer only
The LOCAL-MODE PARITY section of `openspec/changes/embedded-web-redesign/specs/quality-gates/coverage-push-90-addendum.md` MUST receive a minimal edit pointing to `openspec/changes/self-hosted-pivot` as the initiative that implements parity (superseding that section's NON-GOAL status). The `embedded-web-redesign` board MUST NOT gain tasks, and no other content of that change may be modified.

#### Scenario: pointer present, board untouched (Happy)
- GIVEN the edit applied
- WHEN `git diff --stat openspec/changes/embedded-web-redesign` runs
- THEN only the addendum file changed and the board task counts are unchanged

#### Scenario: over-edit attempt (Error)
- GIVEN a proposed edit touches other embedded-web-redesign artifacts
- WHEN review evaluates it
- THEN it is rejected as an REQ-SH-042 violation

#### Scenario: pointer target is valid (Edge)
- GIVEN the pointer text
- WHEN it is read
- THEN it names `openspec/changes/self-hosted-pivot` (proposal/design/specs/tasks) as the superseding change and states that the old section creates no task on the closed board
