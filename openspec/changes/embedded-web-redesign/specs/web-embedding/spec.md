# Delta for web-embedding

## ADDED Requirements

### Requirement: REQ-WEB-001: static export build
The web app MUST build with `output: "export"` (Next.js static export), replacing `output: "standalone"`; all ten pages remain client-rendered and the Vitest suite MUST stay green.

#### Scenario: export succeeds (Happy path)
- GIVEN the migrated `next.config.ts`
- WHEN `npm run build` runs in `web/`
- THEN a fully static `out/` tree is produced with zero server-runtime files
- AND the build fails if any route handler or server feature is reintroduced

#### Scenario: deep-link asset paths (Edge case)
- GIVEN exported pages at nested routes
- WHEN assets are requested via relative base paths
- THEN all references resolve inside the exported tree served under `/internal/web/dist`

#### Scenario: unsupported feature during export (Error state)
- GIVEN a server action, SSR-only API, or `route.ts` handler is present
- WHEN the export build runs
- THEN the build errors deterministically naming the offending file

### Requirement: REQ-WEB-002: runtime API endpoint injection
The client MUST resolve the Cortex API base URL at runtime (injected `/config.js` or `window.__CORTEX_WEB__` written by the Go server) instead of baking `NEXT_PUBLIC_CORTEX_SERVER_URL` at build time; `resolveServerEndpoint(config)` keeps its pure signature.

#### Scenario: same-origin default (Happy path)
- GIVEN the web UI served by the embedded Go server
- WHEN the app boots without explicit configuration
- THEN API calls target the same origin and work without any environment variable

#### Scenario: detached deployment override (Edge case)
- GIVEN a Go server configured with a different public API origin in `/config.js`
- WHEN the browser loads the injected config before app scripts
- THEN `serverEndpoint` resolves to the configured URL and `resolveServerEndpoint` unit behavior is unchanged

#### Scenario: injection script unavailable (Error state)
- GIVEN the config script fails to load
- WHEN the app initializes
- THEN it falls back to the documented default `http://localhost:7438` and reports the fallback in the connection health check

### Requirement: REQ-WEB-003: Go-served /health
`/health` MUST be served by the Go servers (local `internal/http` and server composition) unauthenticated and readiness-bearing; the Next.js `app/health/route.ts` MUST be deleted.

#### Scenario: health from web shell (Happy path)
- GIVEN the embedded server running
- WHEN the web shell probes `GET /health`
- THEN it receives a JSON status document without needing a web key

#### Scenario: health during degraded store (Edge case)
- GIVEN the backing store is unavailable
- WHEN `/health` is requested
- THEN the response reflects degradation with a deterministic shape (no hang)

#### Scenario: health is not on protected surface (Error state)
- GIVEN a request to `/health` with an invalid web key header
- WHEN routing decides auth
- THEN `/health` is still served (it is exempt), while data endpoints reject

### Requirement: REQ-WEB-004: embedded assets with SPA fallback
A local-safe `internal/web` package MUST embed the built assets via `go:embed` (with a committed placeholder so `go build ./...` always compiles) and serve them with a FileServer plus SPA fallback to `index.html` for unknown non-API, non-asset routes.

#### Scenario: dashboard served (Happy path)
- GIVEN embedded assets built from the export
- WHEN a browser requests `/` or `/graph/`
- THEN the matching static document is returned with correct content types and cache headers

#### Scenario: unknown deep link (Edge case)
- GIVEN a request path that matches no file but is not under `/api/`
- WHEN the handler runs
- THEN `index.html` is served with status 200 (client routing takes over)

#### Scenario: path traversal attempt (Error state)
- GIVEN a request containing `..` or encoded traversal sequences
- WHEN the handler sanitizes the path
- THEN the request is rejected and nothing outside the embedded FS is served

### Requirement: REQ-WEB-005: local serve mount
The local `cortex serve` path MUST mount the embedded web surface guarded by the web key, keeping the existing API mux, `http.token` behavior, non-loopback refusal, and CORS semantics on `/api/*` intact (same-origin web allowed).

#### Scenario: browser flow end to end (Happy path)
- GIVEN local serve with a minted web key and a browser session carrying it
- WHEN the user logs in and loads data pages
- THEN the UI and API both respond over one origin and one port

#### Scenario: loopback-only policy preserved (Edge case)
- GIVEN a non-loopback host without `http.token`
- WHEN serve boots
- THEN it refuses exactly as today, with web mount adding no exception

#### Scenario: web disabled (Error state)
- GIVEN `web.enabled=false`
- WHEN a browser requests `/`
- THEN a documented minimal response is returned and `/api/*` continues to work

### Requirement: REQ-WEB-006: server mode mount
`cortex --mode server` MUST mount the same embedded web surface (via the `cmd/cortex` composition bridge), while tenant/workspace authorization for data remains enforced by the server's authorized stores; the local arch gate imports MUST NOT be violated.

#### Scenario: server boots with UI (Happy path)
- GIVEN server mode with a web key
- WHEN the operator opens the UI URL
- THEN the static surface loads and authenticated API usage respects existing principal grants

#### Scenario: UI behind proxy prefix (Edge case)
- GIVEN the server is exposed behind a reverse proxy
- WHEN asset and API requests flow through the proxy
- THEN runtime-injected endpoint config keeps the UI pointed at the correct API base

#### Scenario: arch gate breach (Error state)
- GIVEN a proposed change makes a local package import server-only code to mount the web UI
- WHEN `internal/app/arch_test.go` gates run
- THEN the change is rejected; mounting happens at composition roots only

### Requirement: REQ-WEB-007: build orchestration for embedded assets
The Makefile MUST provide a `web-build` target that installs, builds, and syncs `web/out` into `internal/web/dist`; `.gitignore` MUST ignore synced output while keeping the committed placeholder, and dotfile negations for new web configs MUST be added.

#### Scenario: make web-build (Happy path)
- GIVEN a clean checkout with Node >= 24
- WHEN `make web-build` runs before `make build`
- THEN `internal/web/dist` holds fresh export output and the Go binary embeds it

#### Scenario: placeholder keeps go build green (Edge case)
- GIVEN no web build has ever run locally
- WHEN `go build ./...` runs
- THEN compilation succeeds against the committed placeholder asset

#### Scenario: synced assets never tracked (Error state)
- GIVEN a developer runs `git add internal/web/dist` after syncing
- WHEN ignore rules apply
- THEN only the committed placeholder is trackable and bulk output stays ignored
