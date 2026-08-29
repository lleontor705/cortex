# Installation

## Requirements

- Go 1.26.5 for source builds.
- PostgreSQL 16 or newer for server mode.
- Docker for the reproducible server stack.
- `curl` and `jq` only when using the Claude Code hook integration.

## Homebrew (macOS / Linux)

```bash
brew install lleontor705/tap/cortex
```

## Go Install

```bash
go install github.com/lleontor705/cortex/v2/cmd/cortex@latest
```

## Release Binary

Download the archive for your operating system from the project [Releases](https://github.com/lleontor705/cortex/releases) page. Release artifacts enable the `cortex_vectors` build tag.

## Source Build

```bash
git clone https://github.com/lleontor705/cortex.git
cd cortex
make build
```

This installs the default zero-CGO vector-stub build. For a local functional SQLite BLOB vector adapter, build with the `cortex_vectors` build tag:

```bash
go build -tags cortex_vectors ./cmd/cortex
```

## Local Setup

```bash
cortex setup claude-code
cortex setup opencode
cortex doctor
cortex search "example query"
```

The local database defaults to `~/.cortex/cortex.db`. Configuration is read from `~/.cortex/cortex.yaml` when present.

`cortex setup opencode` installs its TypeScript event plugin from content embedded in the binary. Claude Code's native lifecycle plugin is installed separately through its marketplace; `cortex setup claude-code` configures MCP and tool permissions.

## Server Docker

```bash
docker compose up --build
```

The development server listens on `http://localhost:7438`. On its first Compose start, the container creates random UUIDs for the tenant, workspace and owner subject, plus a random owner Bearer. Copy it immediately from `docker compose logs cortex-server`; it is emitted once and stored in the private `cortex-server-state` volume so restarts retain the same authority. Docker logs may persist the Bearer, so do not share them. To supply a fixed development identity, set all four values (`CORTEX_TENANT_ID`, `CORTEX_WORKSPACE_ID`, `CORTEX_PRINCIPAL_SUBJECT`, and `CORTEX_HTTP_TOKEN`) together; partial input fails closed. For production, provide separate migration/runtime DSNs and an external secret/identity system; see [SERVER.md](SERVER.md).

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
