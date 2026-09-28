# Delta for ci-tests — Test and CI matrix rework for the single-tenant server

Adds the test-matrix contract (design decision D5) covering retained security suites, reworked multi-tenant cases, and the coverage gate.

## ADDED Requirements

### Requirement: REQ-SH-030: Retained PostgreSQL security fixtures
The 3-DSN trio (`CORTEX_TEST_POSTGRES_DSN`, `CORTEX_TEST_POSTGRES_MIGRATION_DSN`, `CORTEX_TEST_POSTGRES_AUTHZ_ADMIN_DSN`), `scripts/postgres/bootstrap-authz.sql`, the `postgres_integration` build tag, the fail-don't-skip harness contract, and the RLS-exploit probe suites `internal/store/postgres/authz_negative_integration_test.go` and `internal/store/postgres/workspace_security_integration_test.go` MUST be retained unchanged in intent.

#### Scenario: DSN contract intact (Happy)
- GIVEN the change applied
- WHEN `go test -v -count=1 ./bench` runs
- THEN the PostgreSQL coverage and harness contract assertions still pass with the 3-DSN strings, bootstrap-authz pins, and Makefile integration target present

#### Scenario: missing DSN still fails (Error)
- GIVEN CI runs the tagged suite without a DSN
- WHEN the harness starts
- THEN it fails loudly rather than skipping

#### Scenario: bootstrap role separation preserved (Edge)
- GIVEN `scripts/postgres/bootstrap-authz.sql`
- WHEN `TestPostgresAuthzBootstrapContract` runs
- THEN the migration/app/admin/login role separation assertions still pass byte-for-byte

### Requirement: REQ-SH-031: Multi-tenant test rework is inventory-driven and honest
Cases that exercise only the removed application plane (multi-tenant verifier, per-request workspace selection, per-request tenant derivation, multi-workspace product features) MUST be reworked or deleted, while SQL-level tenant/workspace isolation probes that remain valid under constant-tenant RLS MUST be kept. Test deletions MUST NOT weaken fixtures, add skips, or convert strict assertions into tolerances; files outside a task's `allowed_files` MUST be escalated through in-place decomposition rather than edited covertly.

#### Scenario: removed plane has no covering test (Happy)
- GIVEN the change applied
- WHEN `grep -rn "MultiTenantTokenPrincipalVerifier|allowRequestSelection|cfg.Server.MultiTenant" --include=*.go`
- THEN no matches remain in tests or production code

#### Scenario: RLS probes still run (Happy)
- GIVEN the tagged suite in CI with valid DSNs
- WHEN `authz_negative_integration_test.go` and `workspace_security_integration_test.go` execute
- THEN all their assertions pass unmodified

#### Scenario: scope creep attempt (Error)
- GIVEN a rework task needs a file it does not own
- WHEN the implementer considers editing it
- THEN the task is returned for in-place decomposition instead

### Requirement: REQ-SH-032: Coverage and documentation-contract gates survive
The enforced coverage threshold MUST remain `>= 80.0` with the computed `-coverpkg` list that excludes only vendored `web/node_modules`; no cortex-owned package may be dropped from measurement. `bench/documentation_contract_test.go` pin updates MUST land in this same change if any DSN/role/build-tag text moves, and the wave-7 gate task MUST re-check the total after the test rework.

#### Scenario: gate value unchanged (Happy)
- GIVEN the change applied
- WHEN the coverage enforcement step is inspected
- THEN the threshold is still 80.0 and the exclusion list still contains only `/node_modules/`

#### Scenario: rework drops coverage (Edge)
- GIVEN test deletions reduce the measured total below 80.0
- WHEN `sh-doc-contract-coverage-gate` runs
- THEN the task fails and compensating tests are added in place before the change may close

#### Scenario: pin text moves with the matrix (Edge)
- GIVEN a DSN name, role name, or build tag is renamed anywhere in CI, Makefile, or the harness
- WHEN `bench/documentation_contract_test.go` is reviewed in the same change
- THEN its expectations are updated in this change so `go test ./bench` is green in the same revision
