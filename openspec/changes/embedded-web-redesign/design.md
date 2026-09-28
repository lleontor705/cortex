# Design: Embedded Web Redesign

## Context & Constraints

Single Go binary (zero-CGO local gate in `internal/app/arch_test.go`), Next.js 15/React 19 client-only web app (10 pages, 13 Vitest suites), hybrid spec plane, flexible workload (Go <=700 / TS <=500 source LOC; tests <=1200; modular test files <=250 LOC). Locked decisions D0-D4 are non-negotiable.

## D0/D3 — Web access key (`internal/webkey`, new local-safe package)

- `Store` at `<CortexDir()>/web.key` (override via `web.key_file`): 32-byte `crypto/rand` secret, prefix lookup + HMAC-SHA256 digest at rest, `hmac.Equal` verify, 0600 create/read mode enforcement — same pattern as `internal/identity/apikey.go:70-121` but a **separate credential namespace** (gotcha: sync token falls back to `cfg.HTTP.Token` at `internal/app/app.go:222-224`; web key NEVER joins that chain).
- `EnsureFirstBoot` returns (key, minted bool); serve paths print plaintext once only when minted.
- CLI `cortex web key show|regenerate` in `internal/cli/web.go`, registered in the `cli.go` command table beside config/auth.
- Config: `WebConfig{Enabled, Host, Port, KeyFile}` in `internal/config/web.go` tagged YAML/JSON/TOML as sibling of `HTTPConfig` (config.go:163-170); Validate enforces port range; Zero-Bloat: only user-settable fields serialize.
- Rejected: keyring (absent in-repo), bcrypt/scrypt (none used anywhere), storing key in main config (violates plaintext-at-rest rule and Zero-Bloat).

## D1 — Static export + embedded server (`internal/web`, new local-safe package)

- `web/next.config.ts`: `output:"export"`, `trailingSlash:true`; `web/src/app/health/route.ts` deleted (route handlers are export-incompatible). Build stays green because pages are already 100% `"use client"` with no SSR/next/image/server actions.
- Runtime endpoint: `web/src/lib/server-endpoint.ts` keeps pure `resolveServerEndpoint(config)`; module export now seeds from `window.__CORTEX_WEB_CONFIG__` written by `/config.js` served by Go (fallback `DEFAULT_SERVER_URL`). Existing Vitest contract unchanged.
- `internal/web`: `//go:embed all:dist` (`dist/index.html` is the committed one-line placeholder so `go build ./...` never needs the web toolchain); `NewHandler(cfg, verifier)` = sanitized FileServer + SPA fallback (unknown non-`/api/`, non-asset GET → index.html) + unauthenticated `GET /health` JSON + web-key bearer middleware on the UI surface; CORS same-origin friendly.
- Mounting (D4): local — `internal/cli/cli.go:runServe` composes web handler under the existing mux/refusal rules (cli.go:739-776 preserved); server — `cmd/cortex/main.go` bridge wires the same handler into `internal/platform/server` HTTP composition (local packages must not import server code; server may compose local-safe `internal/web`).
- Rejected: serving from `web/out` at runtime (no single binary); CDN/asset hashing pipeline (no benefit at this scale); keeping Next standalone server (contradicts D1).

## D2 — Mode triad

- `internal/platform/mode.go`: documented triad, strict ParseMode error listing valid modes; hybrid stays `ModeHybrid → cli.Run + CORTEX_MODE=hybrid` with an explicit named composition helper in `internal/app` (SQLite + sync loop, app.go:216-269 behavior unchanged).
- Startup banner (both serve paths): `mode=... store=... sync=... web=... url=...` key=value lines (pipe-safe), plus exit-on-key-store-failure (REQ-MODE-002).

## UX (web/)

First-boot key gate in `AppShell` via new `web/src/lib/web-key.ts` (paste/normalize/verify, tests in plain Vitest); boundaries `error.tsx/loading.tsx/not-found.tsx`; toast context `web/src/lib/toast.tsx` mounted in `app/layout.tsx`; `EmptyState` domain variants; a11y via `web/.eslintrc.json` + eslint-plugin-jsx-a11y on `npm run lint` — **requires `.gitignore` negation `!web/.eslintrc.json` (added in build-orchestration task)** because of the global `.*` rule. Existing CSS-variable tokens extended, not replaced.

## Playwright (`tools/e2e-web/`)

Own package.json (Node >=24) avoids touching `web/package.json`; `webServer` boots `go run ./cmd/cortex serve` against a temp data dir; specs: first-boot key entry, dashboard, memory/search/graph smoke, settings; CI job installs chromium (headless) and is required.

## Sequencing / Data Flow

Browser → Go (`:port/`) → embedded FS (assets, config.js, /health) | web-key middleware → UI surface | existing API mux unchanged. First boot: mint → persist 0600 → print once → paste-once in UI.

## Verification Approach

Per-task Go package tests (`go test -v -count=1 ./...` focused), web Vitest + `next build` export oracle, Makefile `web-build` sync, CI gates (coverage 90, web coverage, Playwright job, docker_e2e updated). Postgres/DSN-dependent gates remain CI-only; local Windows runs report BLOCKED, never silently skipped.

## Risks / Rollback

Embed ordering (placeholder mitigates), `.gitignore` dotfile trap, e2e compose transition (retire only after mounts land), coverage 90 ambition (decompose gate task into per-package batches on failure). Rollback is additive: `web.enabled=false` disables the surface; no schema or migration changes anywhere in this initiative.
