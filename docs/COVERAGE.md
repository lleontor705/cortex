# Coverage policy

Cortex validates behavior at three complementary layers. A percentage is a
regression guard, not a substitute for the authorization, migration, and
composition contracts exercised by the integration suites.

## Mandatory CI gates

| Layer | Command | Required evidence |
| --- | --- | --- |
| Go service and persistence | `make test-postgres-coverage` | Whole-project atomic coverage, including PostgreSQL integration tests, is at least 70%. |
| Web client core | `npm --prefix web run test:coverage` | V8 coverage of `web/src/lib/**/*.ts`: statements and lines at least 70%, branches at least 60%, functions at least 55%. |
| Compose boundary | `make test-e2e-docker` | The server, web application, bootstrap credentials, tenant/workspace boundary, search, and restart persistence work together in an isolated Compose project. |

Both GitHub workflows upload the Go profile/log and the web V8 reports as
artifacts. Local reports are deliberately ignored by Git: Go writes to
`coverage/`; Vitest writes to `web/coverage/`.

## Why the web scope is explicit

The web test environment is intentionally Node-based and currently exercises
the security-sensitive, pure TypeScript client core: bearer transport policy,
authentication handshake, agent streaming reducer, API encoding, preferences,
and configuration exporters. React/TSX rendering is not counted as if it were
covered; it remains protected by production build validation and the Compose
boundary test. Add a DOM test environment and component interaction tests
before expanding the V8 include set to `tsx` files.

## Running locally

Run the inexpensive web and Go checks first:

```text
npm --prefix web run test:coverage
make test-coverage
```

The PostgreSQL gate requires all three `CORTEX_TEST_POSTGRES_*` DSNs and the
authorization bootstrap described in `AGENTS.md`; CI is the authoritative
executor when those are unavailable. The Compose boundary check requires
Docker:

```text
make test-postgres-coverage
make test-e2e-docker
```
