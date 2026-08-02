# Installation

## Requirements

- Go 1.26.5 for source builds.
- PostgreSQL 16 or newer for server mode.
- Docker for the reproducible server stack.
- `curl` and `jq` only when using the Claude Code hook integration.

## Release Binary

Download the archive for your operating system from the project Releases page. Release artifacts enable the `cortex_vectors` build tag.

## Source Build

```bash
go install github.com/lleontor705/cortex/cmd/cortex@latest
```

This installs the default zero-CGO vector-stub build. For a local functional SQLite BLOB vector adapter:

```bash
git clone https://github.com/lleontor705/cortex.git
cd cortex
```

## Local Setup

```bash
cortex setup claude-code
cortex doctor
cortex search "example query"
```

The local database defaults to `~/.cortex/cortex.db`. Configuration is read from `~/.cortex/cortex.yaml` when present.

## Server Docker

```bash
```

The development server listens on `http://localhost:7438`. The Compose token is intentionally for development only. For production, provide separate migration/runtime DSNs and an external secret/identity system; see [SERVER.md](SERVER.md).

## Web Control Room

```bash
cd web
npm ci
npm run dev
```

Set `VITE_API_URL` if the server is not at `http://localhost:7438`. The browser token is entered interactively and stored only in browser local storage.

## Verification

```bash
go test -v -count=1 -tags "integration postgres_integration" ./...
```

PostgreSQL integration tests require the DSNs documented in `AGENTS.md` and the authz bootstrap roles. Do not point integration tests at a shared production database.
