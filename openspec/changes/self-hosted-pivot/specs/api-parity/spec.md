# Delta for api-parity — Neutral API handler package and local/server endpoint parity

Adds the extraction and parity contract (design decisions D3, D4) that makes the five web-critical endpoints available on both muxes without violating the architecture gate.

## ADDED Requirements

### Requirement: REQ-SH-010: Neutral handler package over a narrow port
The change MUST add `internal/api` exposing `Me`, `Stats`, `Projects`, `AgentProjects`, and `ProjectGraph` handlers behind a five-method `Port` interface (`CurrentPrincipal`, `ServerStats`, `Projects`, `AgentProjects`, `ProjectGraph`). `internal/api` MUST import only `internal/domain` and the standard library; it MUST NOT import `internal/platform/server`, `internal/store/postgres`, or `internal/http`.

#### Scenario: package compiles in isolation (Happy)
- GIVEN the extracted package
- WHEN `go test ./internal/api` runs
- THEN the handlers, port, and golden JSON contract tests pass with no server-track import in the import graph

#### Scenario: JSON envelope is unchanged (Happy)
- GIVEN a principal, stats, project list, agent project map, and subgraph fixture
- WHEN the extracted handlers serialize them
- THEN the emitted JSON keys and shapes equal the pre-extraction server responses (golden assertions)

#### Scenario: forbidden import attempt (Error)
- GIVEN a change tries to import server packages from `internal/api`
- WHEN review inspects the import block
- THEN the change is rejected as a REQ-SH-010 / architecture-gate violation

### Requirement: REQ-SH-011: Server delegation preserves routes and behavior
`internal/platform/server` MUST serve the five routes (`GET /api/me`, `GET /api/stats`, `GET /api/projects`, `GET /api/agent/projects`, `GET /api/graph/project-graph`) through `internal/api` handlers backed by an `Operations`-derived adapter over `AuthorizedStore`. Route patterns, auth middleware ordering, status codes, and response bodies MUST remain byte-compatible for equivalent states.

#### Scenario: authenticated server call (Happy)
- GIVEN a server-mode request authenticated with the configured bearer
- WHEN it calls each of the five routes
- THEN each returns 200 with the same JSON shape the web client consumed before extraction

#### Scenario: unauthenticated call (Error)
- GIVEN no bearer
- WHEN any of the five routes is called
- THEN the standard 401 envelope is returned by the pre-existing middleware

#### Scenario: port error mapping is preserved (Edge)
- GIVEN the underlying `Operations` method returns an authorization or not-found error
- WHEN the delegated handler responds
- THEN the same status code and error envelope are produced as before extraction (no raw error text leaks)

### Requirement: REQ-SH-012: Local parity endpoints on the serve mux
The local `serve` mux MUST register `GET /api/me`, `GET /api/stats`, `GET /api/projects`, `GET /api/agent/projects`, and `GET /api/graph/project-graph` behind the existing token gate, backed by a bundle implementation of `Port`: `/api/stats` recomputed from bundle Observations/Sessions/Metrics (no SQLite `GetServerStats` exists), `/api/projects` and `/api/agent/projects` derived from sessions + observations + code, `/api/graph/project-graph` computed as a BFS over graphstore edges honoring `depth` and `max_nodes` bounds, and `/api/me` returning a synthetic local principal (owner role, single-user local mode) so the web client no longer observes a null principal.

#### Scenario: local /api/me returns a principal (Happy)
- GIVEN a local `serve` process with a valid local token
- WHEN `GET /api/me` is called with the token
- THEN the response is 200 with a non-null principal carrying owner role and stable identity fields

#### Scenario: local stats match the data (Edge)
- GIVEN a local database with known observation, session, and metric rows
- WHEN `GET /api/stats` is called
- THEN the returned counters equal the counts computed from those rows

#### Scenario: unauthenticated parity call (Error)
- GIVEN no token
- WHEN any of the five parity routes is called
- THEN the request is rejected by `withAuth` with 401

### Requirement: REQ-SH-013: Architecture gate preservation
The extraction MUST NOT weaken `internal/app/arch_test.go`: no local package may import server-track code, `internal/domain` must still compile in isolation, the local build must stay zero-CGO, and `cmd/cortex` remains the only bridge to the server composition.

#### Scenario: arch invariants stay green (Happy)
- GIVEN the full change applied
- WHEN `go test ./internal/app ./internal/platform/server` runs
- THEN every architecture invariant test passes without modification of the invariant list

#### Scenario: import graph audit (Edge)
- GIVEN `internal/api` and `internal/http`
- WHEN their import blocks are inspected
- THEN neither contains `internal/platform/server`, `internal/store/postgres`, `internal/authz`, or `internal/identity`

#### Scenario: bridge unchanged (Happy)
- GIVEN `cmd/cortex/main.go`
- WHEN the mode dispatch is inspected
- THEN `--mode server` still routes through `internal/platform/server` and no new bridge import appears

### Requirement: REQ-SH-014: Deferred parity surface is documented
`/api/audit`, `/api/system/metrics`, and `/api/rag/stats` MUST NOT be registered on the local mux in this change; their absence MUST be recorded as roadmap in the change documentation (no local audit store exists).

#### Scenario: deferred routes stay absent (Edge)
- GIVEN the local mux after the change
- WHEN `GET /api/audit`, `GET /api/system/metrics`, or `GET /api/rag/stats` is called
- THEN the route is not registered (falls through to the SPA handler or 404) and no partial implementation exists

#### Scenario: server-only routes unaffected (Happy)
- GIVEN the server mux
- WHEN the three deferred routes are called in server mode
- THEN they are still served exactly as before (deferred means local-only absence)

#### Scenario: roadmap recorded (Happy)
- GIVEN the change documentation
- WHEN the deferred endpoints are described
- THEN each is listed as roadmap with its prerequisite (local audit store, metrics collector, local RAG stats)
