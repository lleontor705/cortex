# Proposal: Embedded Web Redesign

**Change ID:** embedded-web-redesign | **Workflow:** sdd-full | **Spec plane:** hybrid | **Board:** embedded-web-redesign

## Why

Cortex ships a single Go binary plus a separate Next.js web app (`web/`) that today requires its own build, its own runtime (standalone `server.js` / Docker image `cortex-web`), and a build-time-baked API endpoint (`NEXT_PUBLIC_CORTEX_SERVER_URL` in `web/src/lib/server-endpoint.ts`). This fragments the product: two artifacts, two ports, Docker-only UI testing, and no first-boot authentication story. This initiative makes the web UI a first-class capability of the single `cortex` binary with a generated access key, a formalized mode triad, hardened UX, and quality gates.

## What Changes

- **D0/D3 — Web access key:** A unique key is generated at first boot, shown once, persisted as prefix + HMAC-SHA256 digest in a dedicated 0600 `web.key` file under the Cortex config/data directory, and regenerable via `cortex web key regenerate|show`. The web key namespace is isolated from `http.token` and the sync credential (known fallback trap at `internal/app/app.go` sync token → `cfg.HTTP.Token`).
- **D1 — Static export + go:embed:** Next.js switches from `output: "standalone"` to `output: "export"`; built assets are synced into `internal/web/dist` and embedded via `go:embed`, served by a Go FileServer with SPA fallback. `/health` moves from `web/src/app/health/route.ts` to the Go servers. The API endpoint is injected at runtime (served `config.js` /`window` global) instead of baked at build time.
- **D4 — Mount everywhere:** Both the local `serve` path (`internal/cli`) and `cortex --mode server` (`cmd/cortex` bridge) mount the embedded web UI; the `cortex-web` Docker image, its compose services, and release publishing are retired.
- **D2 — Mode triad:** local/server/hybrid get explicit composition, guards, and startup/status reporting; hybrid KEEPS its existing meaning (local SQLite + remote sync/replication). No new semantics are invented.
- **UX:** first-boot key entry flow, `error.tsx`/`loading.tsx`/`not-found.tsx` boundaries, a toast system, per-domain empty states, and an a11y lint pass.
- **Quality:** Go global coverage gate raised from 70% to 90%; Playwright introduced (`tools/e2e-web/`) with first-boot, dashboard, memory/search/graph smoke, and settings flows, wired into a CI job (Node 24, headless chromium).
- **Hygiene & release:** corroborated dead files purged; GoReleaser/CI ordering builds web assets before Go compilation; docs updated (README, docs/, AGENTS.md).

## Impact

- **Specs:** web-config, web-key, web-embedding, mode-triad, web-ux, quality-gates, release-packaging, repo-hygiene, docs.
- **Code:** `internal/config`, new `internal/webkey`, new `internal/web`, `internal/cli`, `internal/http`, `internal/platform` (+`server`), `cmd/cortex`, `web/`, Makefile, `.goreleaser.yaml`, `.github/workflows/*`, compose files, docs.
- **Users:** single-binary web UI at first boot with a console-shown key; new `cortex web key` commands; no standalone web container.
- **Architecture gate:** `internal/web` and `internal/webkey` must remain local-safe (zero-CGO, no PostgreSQL/authz/server imports) per `internal/app/arch_test.go`; `cmd/cortex` stays the sole server bridge.

## Non-Goals

- No billing, entitlements, SSO lifecycle, or provisioning control-plane implementation; the SaaS review is bounded to control-plane gap **documentation** plus explicitly scoped hardening notes.
- **Protected files:** `migrations/v2/001_init.sql` and ALL retired root v1 SQL files `migrations/001-014*.sql` are NEVER edited, moved, or deleted by any task in this change, despite being unreferenced prose-only history.
- No SSR, server actions, or `next/image` adoption; the web surface stays 100% client-rendered for export viability.
- No replacement of the existing CSS-variable theme/design-token system; extend only.
- No rewrite of `bench/**`, `plugin/claude-code`, `plugin/opencode`, or root `package.json` (Husky).
- No successor boards; blocked tasks decompose in place on this board.

## Risks

- `go:embed` requires build output at compile time → mitigated by committed `internal/web/dist/index.html` placeholder + Makefile/CI ordering (web build before Go build).
- `.gitignore` global `.*` rule silently ignores new dotfile configs (e.g. `web/.eslintrc.json`) → explicit negation required.
- Static export is incompatible with the existing Next `/health` route handler → route retirement and Go `/health` must land in the same wave.
- Raising coverage 70%→90% in one initiative is aggressive; if the gate task fails review, it is decomposed in place into per-package batches.
