# Delta for migrations — Frozen embedded SQL set with dead-but-embedded migration 111

Adds the documentation contract for the code-only migration path (design decision D2).

## ADDED Requirements

### Requirement: REQ-SH-020: Embedded migration set is frozen and 111 is documented as dead-but-embedded
No task in this change may edit, move, or delete any file under `migrations/v2/`, any ledger entry, any checksum pin, or the retired root `migrations/001-014*.sql` history. Migration 111 (`ServerMultiTenantVerifierSQL`) stays embedded and becomes dead code with no Go caller after `MultiTenantTokenPrincipalVerifier` is deleted; migration 110 (`cortex_verify_token_principal_v2`) remains the live single-tenant verifier; migrations 100-105 stay immutable. The `ErrFutureMigration` class of failures on ledgered databases MUST be documented in `internal/migration/postgres.go` comments and the migration runbook, and a compensating migration 112 that physically drops 111 MUST be recorded as roadmap only (never created in this change).

#### Scenario: embedded SQL bytes unchanged (Happy)
- GIVEN the change applied
- WHEN `go test -v -count=1 ./internal/migration` runs
- THEN every pinned checksum assertion for 001/002 and 100-111 still passes with identical digests

#### Scenario: 111 has no Go caller (Edge)
- GIVEN the multi-tenant verifier is deleted
- WHEN `grep -r ServerMultiTenantVerifierSQL` runs
- THEN the only hits are the embed declaration and migration plumbing/documentation

#### Scenario: attempted SQL edit (Error)
- GIVEN a task proposes changing `migrations/v2/*.sql`
- WHEN review evaluates it
- THEN it is rejected as an REQ-SH-020 violation and the embedded set stays byte-identical

#### Scenario: ledger landmine documented (Edge)
- GIVEN a database with a recorded ledger row for version 111
- WHEN an operator reads `internal/migration/postgres.go` comments or the runbook
- THEN the failure mode and the forward-only, non-destructive posture are described explicitly
