# Design: Self-Hosted Quality

**Change ID:** self-hosted-quality | **Workflow:** sdd-full | **Spec plane:** hybrid

This design records decisions D1-D7 with their measured basis, the component interfaces the tasks implement, the contested-file ledger, the verification strategy, and the dependency-safe task DAG. Evidence sources: docs/CLI-TUI investigation (obs 711), security investigation, performance investigation, and the fourth post-pivot architecture audit (ses_f16c72d96).

## 1. Decisions and measured basis

### D1 — Wave 0: gitignore hardening, then commit the landed work

122 paths (51 modified / 65 untracked / 6 deleted) from `embedded-web-redesign` and `self-hosted-pivot` are uncommitted, including `internal/api/`, `internal/web/`, `internal/webkey/`, `static_verifier*.go`, `migrations/v2/112_static_bind_contract.sql`, `tools/e2e-web/`, and `docs/embedded-web.md`. Every task diff produced before this lands would be unreviewable, so Wave 0 executes first:

1. `sh-gitignore-hardening` appends `tools/*/node_modules/`, `tools/e2e-web/playwright-report/`, `tools/e2e-web/test-results/` to `.gitignore` (the current file has only `/node_modules/`, `web/node_modules/`, `plugin/*/node_modules/` — `git check-ignore` exits 1 for all three targets). This absorbs the security investigation's LOW-10 so `git add tools/` can never sweep credential-capable Playwright artifacts.
2. `sh-git-commit-landed-pivot` then stages and commits in three logical commits: embedded-web-redesign, self-hosted-pivot, self-hosted-quality planning artifacts. `allowed_files` is intentionally omitted for this task: it mutates no file content (git index + commit only), the single sanctioned exception recorded in tasks.md.

### D2 — Two canonical documents, docs/CLI.md demoted to a pointer

`docs/CLI-REFERENCE.md` (~900-1200 lines) becomes the single authoritative CLI contract: invocation model (mode triad, bare-TTY -> TUI), exit codes 0/1/2, one section per command with flags, defaults, environment variables, auth requirements and worked examples, an environment-variable index, an auth matrix, and a deprecated appendix (including `--tools admin/temporal` per `docs/MCP.md:18`). `docs/CLI.md` shrinks to a pointer so two documents never compete. `docs/TUI-GUIDE.md` (~350-500 lines) is authored from the investigation's screen inventory (15 screens, 5 overlays, global and per-screen key tables, command palette, theme engine `ApplyTheme`/`ToggleTheme` with persistence, status bar anatomy, known quirks: `p` shadowed at `internal/tui/update.go:734`, unbound digit 10, inaccurate `f`/`p` in help view).

**Accepted naming:** `docs/server-saas.md` keeps its filename (orchestrator decision); AGENTS.md already points operators to `docs/SERVER.md`. No rename task exists.

### D3 — Security chain order and interfaces

Order is dictated by blast radius: HIGH-1 first.

**HIGH-1 grant-version propagation.** Today `resolveStaticBindProvenance` (`server.go:548-567`) already SELECTs `grant_version` but returns only the rendered MAC string, while `newStaticBearerVerifier` (`static_verifier.go:43-76`) hardcodes `GrantVersion: 1`. Migration 112's `cortex_bind_principal` recomputes the static MAC over `tenant:actor:grant version` at the row's real, sticky `grant_version` (bumped by `cortex_bootstrap_service_principal` on any canonical-grant change), so every authenticated request fails `28000` after a reconcile.

```go
// server.go
staticProvenance, staticGrantVersion, err := resolveStaticBindProvenance(ctx, migrationDB, cfg)
requestConfig := cfg
requestConfig.Server.GrantDigest = staticProvenance
requestConfig.Server.GrantVersion = staticGrantVersion // transient copy, never persisted
requestVerifier, err := newStaticBearerVerifier(requestConfig)

// static_verifier.go
GrantVersion: cfg.Server.GrantVersion, // previously hardcoded 1
```

A startup assertion fails closed when `staticGrantVersion <= 0`; `internal/platform/server/static_verifier_test.go` gains a `grant_version=2` case proving `VerifyToken` returns a principal whose `GrantVersion == 2`. Local-only literals (`internal/http/me_local.go:47`, `parity_ops.go:61`) are untouched by this task (no migration-112 database exists locally).

**httpauth primitive (LOW-9 + arch debt #13).** The two planes disagree today:

| Aspect | `internal/http.withAuth` (server.go:980-1012) | `platform/server` requestAuthenticator (authentication.go:92-132) |
|---|---|---|
| Header trim | `TrimSpace` on header and secret | byte-exact, rejects whitespace-only |
| `X-API-Key` | accepted (advertised in CORS) | ignored (although `docs/HTTP-API.md:6` documents it) |
| Compare | `subtle.ConstantTimeCompare` over raw bytes (length oracle) | same length oracle inside `VerifyToken` |
| 401 envelope | `{"error":"...","code":"..."}` flat | `{"error":{"code","message"}}` nested |

```go
// package internal/httpauth — stdlib only (net/http, crypto/sha256, crypto/subtle)
func ExtractSecret(r *http.Request) (string, bool) // "Bearer " (case-sensitive) or X-API-Key, byte-exact secret
func EqualSecret(a, b string) bool                  // SHA-256 both sides, subtle.ConstantTimeCompare on 32-byte digests
func WriteUnauthorized(w http.ResponseWriter)       // WWW-Authenticate: Bearer realm="cortex" + nested canonical envelope
```

Adoption rules: `requestAuthenticator` struct fields do not change (10+ test files construct it literally); `internal/http.requestAuthorized` and `staticBearerVerifier.VerifyToken` delegate to `EqualSecret`; the canonical 401 envelope is the **nested** shape (the server contract post-pivot), adopted by local `withAuth`; `X-API-Key` becomes accepted in both planes, which makes `docs/HTTP-API.md:6` true everywhere. The local adoption task must run `npm --prefix web run test` because `web/` is the only 401 consumer in local mode.

**Domain synthetic-principal constructor (arch debt #14).** Three literal copies exist (`static_verifier.go:60-75`, `internal/http/me_local.go:37-54`, `internal/http/parity_ops.go:48-68`). One stdlib-only constructor in `internal/domain` takes explicit parameters (subject, tenant, workspace, grant digest, grant version, roles, project ids, clearance) and returns the exact principal shape the three sites produce today; adoption is behavior-preserving (existing assertions in `saas_authentication_test.go`, `parity_routes_test.go`, `parity_ops_test.go` must pass unmodified). Contract task first, then server adoption (after HIGH-1), then local adoption — sequential over `static_verifier.go`.

**Hygiene:** compose defaults flip to `${CORTEX_SERVER_BOOTSTRAP_DEVELOPMENT:-false}` in `docker-compose.yml:33` and `docker/docker-compose.prod.yml:41` with a new `internal/config/compose_defaults_test.go` contract test as the local declarative oracle (docs/CONFIGURATION.md:107 already documents `false`, so docs become true); `docker/server-entrypoint.sh:82` echoes `tenant_owner_bearer=$CORTEX_HTTP_TOKEN` into retained logs and is gated behind `CORTEX_SERVER_PRINT_BOOTSTRAP=true`, asserted by the same contract test file (dependency edge serializes the two tasks). `internal/http/parity_graph.go:159-174` skips nodes whose `Project` differs from the root (unless the root is `all_projects`) and drops scope-restricted labels.

**MEDIUM-2 (sized M):** TTL re-verification against the durable principal state requires a DB handle plus a TTL gate on the request path; it is documented instead — `docs/HTTP-API.md` gains the explicit contract *revocation = rotate the configured bearer + restart*, with TTL re-verification on the roadmap register. Task lands after HIGH-1 so the documented behavior matches the fixed verifier.

### D4 — Performance: measure first, quick wins second, proposals 1/3/4 roadmaped

Wave 5 establishes the baseline before anything is optimized: Step-1 micro-benchmarks already exist (`internal/store/search/bench_test.go`, `internal/store/sqlite` vector benches, `internal/store/graph/path_batch_benchmark_test.go`, `internal/store/bundle/latency_test.go`, `internal/retrieval` benches) and are captured first; then the five missing oracles are added — `BenchmarkProjectGraph` + `BenchmarkStatsHandler` (`internal/http`), `BenchmarkGraphTools` with a query-count assertion (`internal/mcp`), `BenchmarkOpen` (`internal/app`), `BenchmarkView` (`internal/tui`). Each quick win depends on the oracle that measures it where one exists. Only proposal 2 (MCP graph batch, 500 -> 2 queries) is sized S/M and gets an implementation task (local `tools_cortex.go:1266-1297` N+1 over `GetEdgesForObservation`, server `platform/server/http.go:2193-2320`); proposals 1, 3, 4 and the remaining top-10 are carried by `docs/PERFORMANCE-ROADMAP.md` with the measurement plan (baseline -> optimize -> profile -> CI pg_stat_statements diff for the server track).

### D5 — Coverage: two-tier truth

`.github/workflows/ci.yml:199` enforces `coverage < 80.0` as failure; `docs/COVERAGE.md:11` and the AGENTS.md command table still say 70%. The documentation is corrected to the two-tier statement: **CI global floor 80.0% (authoritative), initiative floor 80.0% (never lowered), web V8 gates unchanged (70/60/55)**. This change never edits `ci.yml`, never lowers a threshold, and never excludes a cortex-owned package from `-coverpkg`.

### D6 — CLI string and probe fixes

`internal/cli/cli.go:2189` and `internal/config/config.go:1360,1373` still print `Server (PostgreSQL Multi-Tenant)`; both become single-tenant self-hosted wording. `internal/cli/cli.go:1765-1773` probes `http://localhost:3000` (retired container era) and prints `[INFO] ... not detected` forever; it now probes the configured serve address (`http://<cfg.HTTP.Host>:<cfg.HTTP.Port>`, default `:7438`) which is where both `serve` and `--mode server` mount the embedded web. One task, sequenced after the logout/loopback tasks because it shares `internal/cli/cli.go`.

### D7 — Roadmap register (no tasks)

Recorded in tasks.md as deferred: `internal/platform/server/http.go` decomposition (2,574 LOC, degree 393), `internal/http` parity sub-package split, Port capability interfaces before endpoints grow (debt #12/#17: `/api/graph/{id}/subgraph` and `/api/code/*` called by `web/src/lib/api.ts` but 404 locally), migration 111 physical drop as 113, test-suite consolidation (28 server test files, `multitenant_branch_removal_test.go` overlaps `saas_authentication_test.go`, debt #7), webkey Windows ACL hardening, `cortex_save` memory-plane writer reconciliation (obs #710/#668), static-bearer TTL revocation, and perf proposals 1/3/4 plus search round-trip reduction and watch batching.

## 2. Contested-file ledger (dependency edges exist for each)

| File | Tasks (in order) | Edge |
|---|---|---|
| `internal/cli/cli.go` | logout-clear-list -> loopback -> mode/doctor strings | chain |
| `internal/platform/server/static_verifier.go` | HIGH-1 -> httpauth adoption -> principal adoption | chain |
| `internal/config/config.go` | logout-clear-list -> mode strings | chain |
| `internal/http/parity_graph.go` | BFS filter -> sort hoist | chain |
| `internal/http/parity_ops.go` | principal adoption local -> stats TTL | chain |
| `internal/mcp/tools_cortex.go` | label projection -> MCP batch local | chain |
| `AGENTS.md` | profile conflicts -> coverage drift | chain |
| `internal/config/compose_defaults_test.go` | compose defaults -> entrypoint echo | chain |
| `docs/README.md`, `README.md` | index task after the two new documents | chain |

Everything else is file-disjoint inside its wave; the orchestrator dispatches at most 3 concurrent writers.

## 3. Verification strategy

- **Docs tasks:** `go test -v -count=1 ./bench` (documentation contract gate; `bench/documentation_contract_test.go` pins only `docs/BENCHMARKS.md`/`bench/README.md`, so the edited files are contract-free but the gate stays green).
- **Go tasks:** package-scoped `go test -v -count=1 ./internal/<pkg>`; fast-TDD tasks (HIGH-1, logout, loopback, BFS filter, httpauth, principal, quick wins, MCP batch) carry the Mutation Evidence Gate of `cortex-work-protocol.md` §4/§8 — a `SURVIVED` mutant blocks `in_review`.
- **Declarative tasks:** `internal/config/compose_defaults_test.go` is a Go/YAML-parser oracle for compose values (no ad-hoc lexers); `bash -n docker/server-entrypoint.sh` is only a secondary check, never the primary gate.
- **Git tasks:** `git check-ignore -v ...` and `git status --porcelain` as exit oracles; no destructive git command is ever permitted.
- **BLOCKED policy:** PostgreSQL-DSN suites, Docker compose/e2e, and Playwright/Chromium gates that cannot execute on this Windows workstation are reported BLOCKED with compile-level ceilings (`go vet -tags docker_e2e ./e2e`), with `.github/workflows/ci.yml` as the authoritative executor.
- **Workload:** flexible policy (source <= 700 LOC Go, tests <= 1200 LOC per task, docs exempt); pure-test tasks (the five benchmark oracles, the compose contract test) are anti-decomposition — a failing assertion is simplified, never split into more tests.

## 4. Task DAG

Wave order: **0 -> 1 -> 2 -> 3 -> 4 -> 5 -> 6 -> 7 -> 8** (orchestrator-enforced; dependency edges additionally serialize every contested file).

| Wave | Tasks | Writers | Concurrent-safe? |
|---|---|---|---|
| 0 git hygiene | gitignore hardening, landed-work commit | 1 + 1 | sequential (commit depends on gitignore) |
| 1 docs A | cli-reference, tui-guide, staleness wave 1 | 3 | yes, file-disjoint |
| 2 docs B | matrices, indexes, profiles, coverage drift, config/graph, SVGs | 6 (max 3 at once) | yes, file-disjoint (AGENTS.md edge inside) |
| 3 security | HIGH-1, logout, loopback, compose, entrypoint, BFS, httpauth x3, principal x3, revocation docs | 13 | chains over 4 contested files |
| 4 code strings | mode label + doctor probe | 1 | after logout/loopback |
| 5 perf baseline | two oracle tasks (graph/stats+mcp, open/view) + Step-1 capture | 2 | yes |
| 6 perf quick wins | tick gating, stats TTL, sort hoist, ETag/gz, quota prune, label projection | 6 (max 3 at once) | yes, file-disjoint |
| 7 perf MCP batch | local batch, server batch | 2 | local after label projection |
| 8 perf roadmap | PERFORMANCE-ROADMAP | 1 | fan-in on all perf tasks |
