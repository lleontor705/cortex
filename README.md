# Cortex

Cortex is a local-first memory system for coding agents. It stores observations, sessions, search indexes, knowledge-graph relationships, temporal data, and importance scores.

## Runtime Modes

| Mode | Storage | Transport | Use case |
|---|---|---|---|
| Local | SQLite | MCP stdio, local HTTP, TUI, CLI | Personal development and offline use |
| Server | PostgreSQL | Authenticated HTTP and Streamable HTTP MCP | Shared, tenant-scoped workspace |

The local and server runtimes share the `cortex_*` naming convention but do not expose the same MCP catalog. See [docs/MCP.md](docs/MCP.md) and [docs/HTTP-API.md](docs/HTTP-API.md).

## Quick Start

### Local

Install Cortex with Homebrew:

```bash
brew install lleontor705/tap/cortex
```

Alternatively, install it directly with Go:

```bash
go install github.com/lleontor705/cortex/cmd/cortex@latest
```

Then configure Cortex for your coding agent:

```bash
cortex setup claude-code
cortex search "database decision"
cortex tui
```

The local database defaults to `~/.cortex/cortex.db`. The normal build uses the degraded vector adapter; use the release artifact or build with `-tags cortex_vectors` for functional local BLOB vector search.

### Server with Docker

```bash
docker compose up --build
```

This starts PostgreSQL and the authenticated server on port `7438`. The development token is configured in `docker-compose.yml`; do not use it outside local development.

The web Control Room is built separately:

```bash
docker build -t cortex-web ./web
docker run --rm -p 5173:80 cortex-web
```

Open `http://localhost:5173`, then enter the server URL and bearer token.

## CLI

```text
cortex setup <agent>        Install an agent integration
cortex search <query>       Search observations
cortex save <title> <text>  Save an observation
cortex context              Show recent context
cortex stats                Show local statistics
cortex timeline             Show observation timeline
cortex revisions            Show revision history
cortex tui                  Launch the terminal UI
cortex serve                Start the local SQLite HTTP server
cortex mcp                  Start local MCP stdio
cortex doctor               Check database and indexes
cortex reindex              Rebuild local embeddings/indexes
cortex gc                   Garbage collect archived observations
cortex export               Export observations
cortex import               Import observations
cortex sync                Synchronize configured chunks
cortex merge-projects       Merge project names
cortex migrate status       Inspect migration state
```

Local v2 migrations are forward-only. `migrate down` is not a supported local upgrade path.

## Documentation

- [Installation](docs/INSTALLATION.md)
- [Configuration](docs/CONFIGURATION.md)
- [CLI reference](docs/CLI.md)
- [MCP tools](docs/MCP.md)
- [HTTP API](docs/HTTP-API.md)
- [Server deployment](docs/SERVER.md)
- [Architecture](docs/ARCHITECTURE.md)
- [Agent setup](docs/AGENT-SETUP.md)
- [Plugins](docs/PLUGINS.md)
- [Obsidian export](docs/OBSIDIAN_EXPORT.md)
- [Benchmarks](docs/BENCHMARKS.md)

## Development

This is a Go repository. Use Go 1.26.5 and the pinned lint version from CI.

```bash
go mod download
make build
```

The frontend lives in `web/` and uses its own npm lockfile. The root `package.json` only installs Husky hooks; `npm test` is intentionally not a project test command.
