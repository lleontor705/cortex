# Embedded Web UI

Cortex ships the operator web UI inside the `cortex` binary. The same process that
serves the HTTP API (`/api/*`) and the readiness probe (`/health`) also serves the
compiled Next.js export at `/`, so there is **no separate web container and no
second port**. The Go handler in `internal/web` reads the assets embedded with
`go:embed`, gates the surface with a dedicated **web access key**, and injects the
runtime API endpoint through `/config.js`.

> The web access key is a credential in its own namespace (`internal/webkey`). It
> is never used as, defaulted to, or derived from `http.token` or the replication
> token, and it is never accepted by the `/api/*` endpoints.

## Quick start

```bash
# 1. Boot the local SQLite server. The embedded UI is mounted on the HTTP listener.
cortex serve
```

On the very first boot Cortex mints a web key and prints it exactly once:

```text
Web access key generated on first boot.
<key>
Store it in a safe place; it will not be shown again.
mode=local
store=~/.cortex/cortex.db
sync=disabled
web=mounted
key_file=~/.cortex/web.key
listen=localhost:7438
```

```text
# 2. Open the UI and paste the key once.
http://localhost:7438/
```

The key entry screen stores the key in the browser (localStorage key
`cortex_web_key`), probes `/health` and then the guarded surface, and never
prompts again. An incorrect paste is rejected with an actionable error that points
at `cortex web key regenerate`.

After the first boot the plaintext is never printed again: later boots reuse the
persisted key silently. The default listener is `http://localhost:7438`
(`http.host`/`http.port`); the web surface inherits that host and port.

## Key lifecycle

### Commands

```bash
cortex web key show        # where the key lives, whether it exists, its prefix
cortex web key regenerate  # mint a replacement and print the new plaintext once
```

Both commands accept an explicit store path:

```bash
cortex web key show --key-file /path/to/web.key
cortex web key regenerate -k /path/to/web.key
```

`show` reports the key file path, whether a key exists, and the public prefix; it
**never** prints the plaintext. When no key exists it reports the absence and the
first-boot behavior without creating one.

`regenerate` mints a fresh key, persists it atomically, and prints the new
plaintext exactly once:

```text
Web access key regenerated.
Key file:       ~/.cortex/web.key

New web access key (shown once):
<key>

Store it in a safe place; it will not be shown again.
The previous key is no longer valid.
```

### At-rest format

The key store persists only a non-secret record — never the plaintext — in
`web.key` (default: `~/.cortex/web.key`):

```text
prefix=ctx_XXXXXXXX
digest=<base64url-hmac-sha256>
```

- The plaintext secret is 32 random bytes from `crypto/rand`, encoded as
  `ctx_` + base64 (raw URL alphabet). The `prefix` line is the first 12
  characters of that secret; `digest` is an HMAC-SHA256 over the secret keyed by
  a value derived from the prefix.
- Verification uses prefix lookup followed by a constant-time comparison
  (`hmac.Equal`), so a tampered digest rejects deterministically.
- The file is created with mode `0600` inside a `0700` directory. On POSIX
  systems a key file that is group/other readable fails closed on load
  (permissions are not silently repaired); Windows skips the POSIX mode check.
- A failed first-boot write never leaves a partial file behind.

### Rotation semantics

- First boot uses an exclusive create (`O_EXCL`), so concurrent boots mint exactly
  one key: the winner prints the plaintext and the loser loads the winner's key
  without printing anything.
- `regenerate` writes a temporary file in the same directory, fsyncs it, and
  atomically renames it over `web.key`. If any step fails, the previous key stays
  valid and unchanged and the command exits non-zero.

## Modes

The mode triad is a closed set: `local`, `hybrid`, `server`
(`internal/platform/mode.go`). The embedded web surface is mounted in all three;
what differs is the backing store, the API authorization, and how the surface is
wired.

| | `--mode local` (default) | `--mode hybrid` | `--mode server` |
|---|---|---|---|
| Backing store | SQLite (`~/.cortex/cortex.db`) | SQLite (identical composition) | PostgreSQL (self-hosted single-tenant) |
| Web surface | Mounted at `/` on the local HTTP listener | Mounted at `/` on the local HTTP listener | Mounted at `/` on the server HTTP listener |
| API auth | Static `http.token` bearer | Static `http.token` bearer | Static `http.token` bearer + synthetic principal + RLS/authorized stores |
| Data endpoints | `/api/*` (SQLite), including the five parity routes below | `/api/*` (SQLite) + replication loop | Same five parity routes via `internal/api` + Streamable HTTP MCP `/mcp` |
| Web key file | `~/.cortex/web.key` (or `--key-file`) | Same as local | Same default path |
| Status banner | `mode=local store=<path> sync=<enabled\|disabled> web=mounted` | `mode=local … sync=enabled` (hybrid sets `CORTEX_MODE=hybrid`) | `cortex: web <baseURL>/`, `cortex: web key file <path>` |

- **local** — the single-binary SQLite composition with the local stdio MCP
  server. `cortex serve` boots the HTTP API and mounts the embedded UI on the same
  listener. Hybrid is *not* a distinct store: it is this composition with the
  remote sync/replication loop layered on. If `--mode hybrid` is selected but sync
  is disabled or unconfigured, startup degrades to local behavior with an explicit
  warning naming the missing sync configuration.
- **server** — the self-hosted single-tenant PostgreSQL composition. `cmd/cortex`
  (the only permitted local→server bridge) builds the same `internal/web` handler
  and mounts it ahead of the authenticated `/api/` and `/mcp` routes. The request
  plane authenticates one configured static bearer (`http.token`) against a
  synthetic constant service principal assembled from `server.tenant_id`,
  `server.workspace_id`, and `server.principal_subject`; there is no per-request
  tenant or workspace derivation. The credential is minted only after the server
  composition opens, so a failed bootstrap never creates a key file or claims a
  surface. Tenant and workspace remain configuration constants enforced through the
  server's authorized stores and forced RLS; the web key is an independent UI
  credential.

The five web-critical endpoints are served by the local `serve` mux behind the
same `withAuth` token gate, backed by a bundle port over the SQLite composition
(REQ-SH-012):

- `GET /api/me` — synthetic local principal (owner role, single-user local mode).
- `GET /api/stats` — counters recomputed from the local bundle.
- `GET /api/projects` and `GET /api/agent/projects` — derived from sessions,
  observations, and code.
- `GET /api/graph/project-graph` — BFS over graph edges honoring `depth` and
  `max_nodes`.

`/api/audit`, `/api/system/metrics`, and `/api/rag/stats` remain deferred roadmap
items and are **not** registered on the local mux (no local audit store exists).

Because hybrid and local mount the surface identically, a browser session that
works against a local server also works against a hybrid one; the UI simply sees
the replication state through the API.

## Runtime endpoint injection

The Go server serves the API origin to the browser as a script, so the UI is not
pinned to a build-time URL:

- `GET /config.js` — unauthenticated, `application/javascript`, `no-cache`;
  defines `window.__CORTEX_WEB_CONFIG__`.
- Default (shipped compositions) resolves the page origin, keeping the surface
  same-origin: `window.__CORTEX_WEB_CONFIG__ = { "serverUrl": window.location.origin };`.
- The client (`web/src/lib/server-endpoint.ts`, `resolveServerEndpoint`) reads
  `window.__CORTEX_WEB_CONFIG__.serverUrl` when present and otherwise falls back to
  the documented default `http://localhost:7438`.
- `/health` is served unauthenticated so the shell can render before a key is
  entered. `/api/*` is never answered by the web handler — the API mux owns that
  namespace and keeps its own bearer semantics.

## For contributors

`web/` is a Next.js app exported statically (`output: "export"`), not run as a
Node server. The build pipeline is ordered so the Go binary always compiles, even
in a checkout where the web toolchain never ran:

```bash
# Run this BEFORE make build / release builds.
make web-build
```

`make web-build`:

1. runs `npm ci` only when `web/node_modules` is absent,
2. runs `npm run build` in `web/` (producing `web/out/`),
3. mirrors `web/out/` into `internal/web/dist/`, removing prior export output but
   **preserving the committed placeholder** `internal/web/dist/index.html`.

The placeholder keeps `go:embed` and `go build ./...` green before any web build.
`go:embed` uses the `all:` prefix so the `_next/` asset tree is embedded intact.
`.gitignore` ignores the synced export (`/internal/web/dist/*`) while re-including
the placeholder (`!/internal/web/dist/index.html`), so bulk output is never
tracked. For the full gate, run `make web-build` then `make build`; release builds
follow the same ordering.

## Troubleshooting

- **Lost the key.** Run `cortex web key regenerate` on the host, then paste the new
  key in the UI. The previous key stops verifying immediately after the atomic
  replace; `cortex web key show` confirms where the file lives and its prefix.
- **Server refuses to start.** `cortex serve` refuses to expose the HTTP API (and
  therefore the web surface) on a non-loopback host unless `http.token` is
  configured. Bind `localhost` for local use, or set `http.token` intentionally.
- **UI reports an invalid key.** The presented key did not verify (HTTP 401). The
  inline error points at `cortex web key regenerate`; the stale value is not
  stored.
- **Permission error on load.** On POSIX the key file must not be group/other
  readable. Move it to a private directory and `chmod 600`; the store fails closed
  rather than repairing the mode.
- **Boot fails with "web access key unavailable".** The key store could not be
  created or read (for example an unwritable data directory). `serve` and
  `--mode server` abort non-zero instead of silently serving an API-only surface.
