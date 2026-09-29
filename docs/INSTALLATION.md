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

## Server Docker (GHCR Images)

Cortex provides one official, multi-architecture (`linux/amd64`, `linux/arm64`) container image hosted on **GitHub Container Registry (`ghcr.io`)**:

- **Cortex Server (API + embedded web UI)**: `ghcr.io/lleontor705/cortex:latest`

The same binary serves the operator web UI at `/` and the HTTP API at `/api/*`, so there is no separate web image and no second UI port.

### Running with Docker Compose

```bash
# 1. Configure environment variables (optional overrides)
cp .env.example .env

# 2. Pull and start PostgreSQL and Cortex Server
docker compose up -d
```

The server listens on `http://localhost:7438` and serves the embedded web UI from the same origin at `http://localhost:7438/`. Server mode is single-tenant: on first start the Docker entrypoint provisions and persists one stable deployment identity — the constants `CORTEX_SERVER_TENANT_ID`, `CORTEX_SERVER_WORKSPACE_ID`, and `CORTEX_SERVER_PRINCIPAL_SUBJECT`, plus the static `CORTEX_HTTP_TOKEN` bearer — generating any value you did not configure. The first boot also mints one web access key and prints it exactly once. Copy both from:

```bash
docker compose logs cortex-server
```

Open `http://localhost:7438/` and paste the web access key once; the browser stores it and never prompts again. The key has its own namespace and is never accepted by `/api/*`, which keeps using the `CORTEX_HTTP_TOKEN` bearer. See [embedded-web.md](embedded-web.md) for the full key lifecycle.

### Running a Standalone Container

```bash
docker run -d \
  --name cortex-server \
  -p 7438:7438 \
  -v cortex-server-state:/home/cortex/.cortex \
  -e CORTEX_SERVER_AUTO_BOOTSTRAP=true \
  -e CORTEX_SERVER_STORAGE_DRIVER=postgres \
  -e CORTEX_SERVER_STORAGE_DSN="postgres://user:pass@host:5432/cortex?sslmode=disable" \
  -e CORTEX_SERVER_STORAGE_MIGRATION_DSN="postgres://admin:pass@host:5432/cortex?sslmode=disable" \
  ghcr.io/lleontor705/cortex:latest
```

Open `http://localhost:7438/` and paste the web access key printed once on first boot. The `cortex-server-state` volume mounted at `/home/cortex/.cortex` persists the key file (`web.key`) across restarts, so the browser does not need it re-entered. Inspect or rotate the key from inside the container:

```bash
docker exec cortex-server cortex web key show
docker exec cortex-server cortex web key regenerate
```

## Verification

```bash
go test -v -count=1 -tags "integration postgres_integration" ./...
```

PostgreSQL integration tests require the DSNs documented in `AGENTS.md` and the authz bootstrap roles. Do not point integration tests at a shared production database.
