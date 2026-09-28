# Delta for security-hardening — Top-5 fixes plus bearer/principal consolidation

Adds the security contract from the security investigation and the fourth audit's Q1/Q3 additions. Confirmed-clean controls are regression-protected by REQ-SQ-SEC-010.

## ADDED Requirements

### Requirement: REQ-SQ-SEC-001: Static bearer principal carries the database grant_version (HIGH-1)
The static request principal MUST be assembled with the bootstrapped actor's real `grant_version`: `resolveStaticBindProvenance` MUST return both the rendered migration-112 provenance and the grant version it read, `requestConfig.Server.GrantVersion` MUST be set from it before `newStaticBearerVerifier`, and `newStaticBearerVerifier` MUST read `cfg.Server.GrantVersion` instead of the hardcoded `1` (`internal/platform/server/static_verifier.go:70`). Startup MUST fail closed when the resolved grant version is not positive.

#### Scenario: reconcile no longer breaks authentication (Happy)
- GIVEN a database whose canonical grant was reconciled to grant_version 2
- WHEN the server composes `requestConfig` and `newStaticBearerVerifier`
- THEN `VerifyToken` returns a principal with `GrantVersion == 2` and no HTTP 28000 is produced

#### Scenario: unit case at grant_version=2 (Edge)
- GIVEN `static_verifier_test.go` with `cfg.Server.GrantVersion = 2`
- WHEN `go test -v -count=1 ./internal/platform/server` runs
- THEN the suite passes and asserts the propagated version

#### Scenario: missing or invalid version fails closed (Error)
- GIVEN a composition where the resolved grant version is 0 or negative
- WHEN startup runs
- THEN composition aborts with an explicit server error before any handler exists

### Requirement: REQ-SQ-SEC-002: auth logout clears secrets on every configuration format
`cortex auth logout` MUST clear `http.token`, `sync.token`, and `ai.api_key` when the configuration file is YAML, JSON, or TOML: the YAML merge MUST explicitly remove those scalar keys even though `mergeYAMLNode` only visits keys present in the desired document, `config.Save` errors MUST be surfaced instead of discarded at `internal/cli/cli.go:2223`, and `auth status` MUST re-read the configuration after saving.

#### Scenario: YAML token cleared (Happy)
- GIVEN a YAML config containing `http.token: secret`
- WHEN `cortex auth logout` runs
- THEN the file is rewritten without `http.token`, `sync.token`, or `ai.api_key`

#### Scenario: save failure is reported (Error)
- GIVEN an unwritable configuration path
- WHEN `cortex auth logout` runs
- THEN the command exits 1 with the save error on stderr instead of printing success

#### Scenario: JSON and TOML stay correct (Edge)
- GIVEN JSON and TOML configurations with the same secrets
- WHEN logout runs on each
- THEN all three secrets are removed as they are today for those formats

### Requirement: REQ-SQ-SEC-003: Empty http.host is not loopback
`isLoopbackHost("")` MUST return false (`internal/cli/cli.go:1430-1434`) so that an empty host, which binds \":7438\" on all interfaces, is rejected by the existing no-token guard at `cli.go:762`. `localhost`, `127.0.0.1`, `::1`, and other loopback values MUST continue to be treated as loopback, and the default `http.host=localhost` MUST remain servable without a token.

#### Scenario: tokenless all-interfaces bind refused (Happy)
- GIVEN `http.host` empty and `http.token` unset
- WHEN `cortex serve` starts
- THEN the command exits 1 with the non-local-host refusal message and no listener opens

#### Scenario: default configuration unaffected (Edge)
- GIVEN the default `http.host=localhost`
- WHEN `cortex serve` starts
- THEN the server binds normally without requiring `http.token`

#### Scenario: explicit loopback values still accepted (Edge)
- GIVEN `http.host` set to `127.0.0.1` or `::1` without a token
- WHEN `cortex serve` starts
- THEN the bind proceeds as loopback

#### Scenario: non-loopback host without token still refused (Error)
- GIVEN `http.host=0.0.0.0` and no `http.token`
- WHEN `cortex serve` starts
- THEN the command exits 1 with the existing refusal message

### Requirement: REQ-SQ-SEC-004: Compose defaults respect the distinct-role boundary (MEDIUM-6)
Both `docker-compose.yml:33` and `docker/docker-compose.prod.yml:41` MUST default `CORTEX_SERVER_BOOTSTRAP_DEVELOPMENT` to `false`, and `docker/server-entrypoint.sh:82` MUST NOT echo `tenant_owner_bearer=$CORTEX_HTTP_TOKEN` into retained logs unless `CORTEX_SERVER_PRINT_BOOTSTRAP=true` is set; the bearer MUST be written only to the state file. A Go contract test MUST parse both compose files and assert the `false` default.

#### Scenario: development mode is opt-in (Happy)
- GIVEN a fresh `docker compose up` with no environment overrides
- WHEN the stack boots
- THEN `server.bootstrap_development` is false and `server.go:706-708` distinct-role validation runs

#### Scenario: bearer never reaches logs (Edge)
- GIVEN default entrypoint execution
- WHEN bootstrap completes
- THEN stdout/retained logs contain no `tenant_owner_bearer=` line and the state file still holds the credential

#### Scenario: opt-in still works (Edge)
- GIVEN `CORTEX_SERVER_PRINT_BOOTSTRAP=true` and `CORTEX_SERVER_BOOTSTRAP_DEVELOPMENT=true`
- WHEN the entrypoint runs
- THEN the diagnostic echo and development bootstrap behave as before

### Requirement: REQ-SQ-SEC-005: Project-graph BFS never discloses out-of-project observations (MEDIUM-5)
`ProjectGraph` expansion (`internal/http/parity_graph.go:159-174`) MUST skip any observation whose `Project` differs from the root project whenever the root is not `all_projects`, MUST NOT emit such nodes' labels or titles into the returned subgraph, and MUST keep the seeded filtered set (`:65`) consistent with the expansion set.

#### Scenario: cross-project leak closed (Happy)
- GIVEN observations A (project alpha) and B (project beta) sharing an edge
- WHEN the project graph for alpha is requested
- THEN B is absent from nodes and no title/label of B appears anywhere in the response

#### Scenario: all_projects root unchanged (Edge)
- GIVEN the root `all_projects`
- WHEN the project graph is requested
- THEN expansion behavior for other projects is unchanged

#### Scenario: edge-only neighbor pruned (Edge)
- GIVEN an in-project node linked only to an out-of-project node
- WHEN the graph is requested
- THEN the in-project node survives and the boundary edge is dropped

#### Scenario: label still leaked (Error)
- GIVEN a response containing an out-of-project title after the fix
- WHEN the response is inspected
- THEN the task fails until the node and its payload are removed

### Requirement: REQ-SQ-SEC-006: Git hygiene and a reviewable committed baseline (LOW-10 + fourth audit)
`.gitignore` MUST gain `tools/*/node_modules/`, `tools/e2e-web/playwright-report/`, and `tools/e2e-web/test-results/` before any `git add tools/`, and the 122 landed dirty paths (51 modified / 65 untracked / 6 deleted) from `embedded-web-redesign` and `self-hosted-pivot` MUST be committed in three logical commits (embedded-web-redesign, self-hosted-pivot, self-hosted-quality planning artifacts) using non-destructive git operations only.

#### Scenario: ignore rules verified (Happy)
- GIVEN the .gitignore edit
- WHEN `git check-ignore -v tools/e2e-web/node_modules tools/e2e-web/playwright-report tools/e2e-web/test-results` runs
- THEN all three paths resolve to a pattern (exit 0)

#### Scenario: clean tree after Wave 0 (Happy)
- GIVEN the three commits
- WHEN `git status --porcelain` runs
- THEN no path from the two landed initiatives remains dirty

#### Scenario: no destructive operation (Error)
- GIVEN any proposed git command
- WHEN it includes `reset --hard`, `clean -fd`, `push --force`, or history rewriting
- THEN it is rejected and the task is escalated

### Requirement: REQ-SQ-SEC-007: One stdlib-neutral bearer primitive for both planes (LOW-9 + arch debt #13)
A new `internal/httpauth` package MUST provide bearer extraction (`Authorization: Bearer ` scheme and `X-API-Key`, byte-exact secret), a SHA-256 fixed-length constant-time comparison that removes the raw-length oracle of `subtle.ConstantTimeCompare`, and one canonical 401 writer emitting `WWW-Authenticate: Bearer realm="cortex"` with the nested `{"error":{"code","message"}}` envelope. `internal/http.withAuth` and `platform/server` requestAuthenticator MUST both consume it, `X-API-Key` MUST be accepted on both planes (making `docs/HTTP-API.md:6` true everywhere), and `requestAuthenticator`'s struct fields MUST NOT change.

#### Scenario: length oracle closed (Happy)
- GIVEN secrets of different lengths presented to `EqualSecret`
- WHEN compared
- THEN the comparison runs over two fixed 32-byte digests and returns false without a length-dependent early exit

#### Scenario: envelopes converge (Happy)
- GIVEN an unauthenticated `/api/*` request on each plane
- WHEN the 401 response is inspected
- THEN both return the same nested error envelope and the same `WWW-Authenticate` header

#### Scenario: web 401 contract survives (Edge)
- GIVEN local mode serving the embedded web
- WHEN `npm --prefix web run test` runs after the local adoption
- THEN the web suite passes with the canonical envelope

### Requirement: REQ-SQ-SEC-008: Single domain-level synthetic-principal constructor (arch debt #14)
`internal/domain` MUST expose one stdlib-only constructor that builds the synthetic principal shape currently written as three literals (`static_verifier.go:60-75`, `internal/http/me_local.go:37-54`, `internal/http/parity_ops.go:48-68`); all three sites MUST call it, and the resulting principal MUST be field-for-field identical to today's output so existing assertions pass unmodified.

#### Scenario: three literals collapse to one call site (Happy)
- GIVEN the constructor landed and all three adoptions
- WHEN the three files are searched for inline principal literals
- THEN no duplicated literal assembly remains

#### Scenario: behavior-preserving (Edge)
- GIVEN the adoptions
- WHEN `go test -v -count=1 ./internal/platform/server ./internal/http ./internal/domain` runs
- THEN all suites pass without weakening assertions

#### Scenario: constructor is stdlib-only (Edge)
- GIVEN `internal/domain` after the addition
- WHEN its import list is inspected
- THEN only standard-library packages are imported (arch gate invariant 3)

#### Scenario: field divergence introduced (Error)
- GIVEN an adoption producing a principal that differs from today's shape
- WHEN the existing assertions run
- THEN the task fails until the constructor reproduces the exact shape

### Requirement: REQ-SQ-SEC-009: Static-bearer revocation contract documented (MEDIUM-2, sized M)
`docs/HTTP-API.md` MUST state the landed revocation contract explicitly — the static bearer is configuration-derived and unrevocable at runtime, so revocation is `rotate the configured bearer + restart` — and MUST record TTL re-verification against durable principal state as roadmap-deferred, not implemented in this change.

#### Scenario: operator contract explicit (Happy)
- GIVEN the security wave complete
- WHEN the authentication section of `docs/HTTP-API.md` is read
- THEN the rotate-and-restart procedure and the deferred TTL item are both stated

#### Scenario: no silent behavior change (Error)
- GIVEN the documentation task
- WHEN its diff includes changes under `internal/`
- THEN the task is rejected as out of scope

#### Scenario: doc matches landed verifier (Edge)
- GIVEN the fixed static verifier from REQ-SQ-SEC-001
- WHEN the documented procedure is executed
- THEN rotating `http.token` and restarting invalidates the old bearer

#### Scenario: TTL documented as deferred (Edge)
- GIVEN the roadmap note in `docs/HTTP-API.md`
- WHEN it is read
- THEN it names TTL re-verification as future work with the reason it was not implemented here

### Requirement: REQ-SQ-SEC-010: Landed security controls are regression-protected
No task in this change may weaken fail-closed bearer validation, the migration-112 bind contract (FOR SHARE re-read before MAC), mux precedence of `/api/*` over the web fallback, export/import authentication, web-key 0600 + HMAC at-rest protection, CORS wildcard rejection, or secrets redaction; each remains covered by its existing tests, and the 80.0 coverage gate is never lowered.

#### Scenario: controls stay green (Happy)
- GIVEN the security wave complete
- WHEN `go test -v -count=1 ./internal/platform/server ./internal/http ./internal/config ./internal/cli ./internal/webkey` runs
- THEN every suite passes with assertions intact

#### Scenario: gate untouched (Edge)
- GIVEN this change's diff
- WHEN `.github/workflows/ci.yml` coverage logic is inspected
- THEN the 80.0 threshold and `-coverpkg` scope are unchanged

#### Scenario: assertion weakened (Error)
- GIVEN a diff that deletes or relaxes an existing security assertion
- WHEN review compares it with the pre-change test
- THEN the task fails unless an equivalent or stronger assertion replaces it

#### Scenario: migration SQL untouched (Error)
- GIVEN the change diff
- WHEN `migrations/v2/` is inspected
- THEN no SQL byte changed
