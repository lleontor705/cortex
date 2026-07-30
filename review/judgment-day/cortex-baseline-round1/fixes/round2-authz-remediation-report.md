# Authorization Remediation Round 2

## Scope

Remediation of the three HIGH findings identified against `5e3f5e0`. The
change is limited to server authorization boundaries, lifecycle capabilities,
audit persistence, PostgreSQL resource predicates, deterministic v2 fixtures,
and their tests.

## Implemented

- Removed exported raw repository accessors and all PostgreSQL repository fields
  from `server.Runtime`; added architecture and mutation-pin tests.
- Added a private, capability-limited `SystemService` for lifecycle work. Its
  principal and permissions are derived internally from the verified server
  principal and cannot be client supplied.
- Point operations load tenant/workspace/project/owner/classification metadata
  before authorization. Graph operations validate both endpoints. List/search
  and repository point SQL include tenant, project, classification, and personal
  ownership predicates.
- Added mandatory PostgreSQL `AuditSink` wiring at server startup. Audit records
  persist correlation, actor, action, resource, reason, and allow/deny without
  content or secret material. Privileged writes fail closed when persistence
  fails.
- Replaced hand-built embedding/outbox fixtures with the embedded v2 baseline
  migration, eliminating the missing-`index_outbox` race and preserving the
  intentional rollback test by dropping the migrated table explicitly.

## Final Verification Evidence

- Default: `go test ./...` PASS
- Race: `go test -race ./...` PASS
- Fuzz: `go test ./internal/authz -run=^$ -fuzz=Fuzz -fuzztime=10s` PASS; PostgreSQL fuzz target equivalent PASS
- Vet: `go vet ./...` PASS
- Build: `go build ./...` PASS
- Zero-CGO: `CGO_ENABLED=0 go build ./cmd/cortex` PASS
- Lint: `golangci-lint run ./...` PASS
- Docker non-superuser PostgreSQL 16 (`cortex_app`, no superuser):
  `go test -tags "integration postgres_integration" -count=1 ./...` PASS
- Tagged coverage: same Docker setup with
  `-coverprofile coverage-tagged-final.txt`; `go tool cover -func coverage-tagged-final.txt`
  reported **71.5% total**.
- Focused architecture/authz/audit/system tests: PASS.
- Mutation pins explicitly detect synthetic `AuthorizedStore.Observations()` and
  `Runtime.Repositories` additions: PASS.

## Commit

## Commit

Focused implementation commit: `8b15f7a`.
