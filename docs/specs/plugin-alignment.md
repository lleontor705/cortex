# Plugin Alignment Specification

## Goal

Keep Claude Code and OpenCode integrations compatible with Cortex local-first storage, bidirectional sync, current HTTP configuration, and local/server MCP identity contracts.

## Scope

- Claude hooks use `CORTEX_HTTP_PORT`, defaulting to `7438`.
- Local HTTP exposes `GET /api/sessions/{id}` and `POST /api/prompts`.
- Local browser access applies exact origins from `http.allowed_origins` without weakening API authentication.
- OpenCode stores user messages in `user_prompts`, ends primary sessions on deletion, and preserves passive observations.
- `cortex setup opencode` installs MCP configuration and the TypeScript event plugin from every Cortex binary.
- Local numeric observation/graph IDs, opaque local session IDs, and server UUIDs are documented as distinct contracts.

## Design

- Reuse `session.Store.GetByID` and `prompt.Store.Save`; no direct SQL or schema changes.
- CORS wraps local auth so allowed `OPTIONS` preflights return `204`, while real `/api/*` calls remain authenticated.
- `plugin/opencode/cortex.ts` remains the single TypeScript source. `plugin/opencode/embed.go` embeds that file directly for release installation.
- Setup requires exactly one binary fallback marker, patches it with the resolved executable, and fails instead of reporting partial success.
- OpenCode session and prompt calls remain best-effort and retain private-tag redaction and truncation.

## Acceptance Criteria

1. All five Claude scripts contain `CORTEX_HTTP_PORT` and no obsolete `CORTEX_PORT` reference.
2. Existing local sessions return `200`; missing sessions return `404`; configured auth remains required.
3. Valid prompts return `201`, persist in `user_prompts`, and do not create observations.
4. Invalid prompt JSON/fields return `400`; missing sessions return `404`.
5. OpenCode uses `/api/prompts`, keeps Task passive capture on `/api/observations`, and ends known primary sessions before clearing state.
6. A binary-only OpenCode setup writes exactly two files and contains no unresolved `return "cortex"` fallback.
7. Allowed CORS preflight returns exact-origin headers; disallowed origins receive none; authenticated real requests remain protected.
8. Documentation distinguishes local integers/session strings from server UUIDs and describes deterministic plugin installation.
9. No migration changes are required.
10. Default zero-CGO build, full tests, lint, and plugin contract tests pass.

## Verification

```bash
go test -count=1 ./internal/http ./internal/setup ./plugin/opencode ./plugin/claude-code
go test -count=1 ./...
golangci-lint run ./...
go build ./cmd/cortex
cortex setup opencode
```

The installed plugin must exist at `~/.config/opencode/plugins/cortex.ts` and contain the current prompt/session contracts.
