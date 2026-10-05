# Cortex

**Cortex** is a self-hosted platform for **autonomous episodic memory, project governance,
and a code knowledge graph** built for AI coding agents (Cursor, Claude Code, Cline,
Windsurf, OpenCode) and development teams.

It combines Zero-CGO static AST extraction, community clustering, blast-radius analysis,
project governance, hybrid retrieval (BM25 + vectors), and secure persistence backed by
local SQLite or a single-tenant self-hosted PostgreSQL server.

## Capabilities

### Code and knowledge graph

- **Polyglot AST extractor (Zero-CGO)**: analyzes .NET, Java/Kotlin, Rust, C/C++, PHP,
  Ruby, Swift, Go, TypeScript/JavaScript, Python, and SQL in pure Go, without sending
  code to external LLMs.
- **Community detection and god nodes**: groups functional subsystems, labels
  architectural hubs, and reports dependency cycles (Tarjan SCC).
- **Blast radius**: measures the impact of changing any component and lists the affected
  files and functions.
- **Obsidian export**: writes an interconnected Markdown vault with `[[WikiLinks]]`.

### Episodic memory and continuous learning

- Records architecture decisions, bug fixes, patterns, and technical discoveries.
- **Hybrid retrieval**: combines full-text BM25 with vector similarity (OpenAI, Ollama,
  pgvector, Qdrant) and freshness/importance weighting.
- **Autonomous background workers**: periodic graph reorganization, contradiction
  detection, and conflict resolution (`supersedes`).

### Governance and self-hosting

- **Single-tenant PostgreSQL with forced RLS**: tenant and workspace identifiers are
  configuration constants, never client input.
- **Project rules and system prompts**: dynamic injection of corporate rules and agent
  skills into every query.

## Installation

```bash
# Homebrew (macOS / Linux)
brew install lleontor705/tap/cortex

# Go toolchain
go install github.com/lleontor705/cortex/v2/cmd/cortex@latest

# From source
git clone https://github.com/lleontor705/cortex.git
cd cortex
make build
```

Prebuilt binaries for Windows, macOS, and Linux are available on the
[GitHub Releases page](https://github.com/lleontor705/cortex/releases).

## Quick start

```bash
# Wire agent integrations with modular profiles (dev, minimal, agent)
cortex setup claude-code --profile=dev
cortex setup opencode --profile=agent

# Inspect the operating mode and run diagnostics
cortex status
cortex doctor

# Adaptive search with complexity classification
cortex search "architecture decision" --mode=auto

# Index a repository once, then keep it fresh with the watcher
cortex ingest ./internal --project cortex
cortex watch . --project cortex

# Interactive terminal UI
cortex tui
```

Server mode (embedded web UI served from the same binary and port as the HTTP API):

```bash
cortex serve
```

## Documentation

- **[Architecture](ARCHITECTURE.md)**, **[Configuration](CONFIGURATION.md)**,
  **[Installation](INSTALLATION.md)**, **[Distribution](distribution.md)**, and
  **[Graph intelligence](GRAPH_INTELLIGENCE.md)** explain the runtime boundaries,
  storage, migrations, and code-graph analysis.
- **Interfaces**: **[MCP](MCP.md)** profiles and tools, **[HTTP API](HTTP-API.md)**
  endpoints, **[Agent setup](AGENT-SETUP.md)**, **[Plugins](PLUGINS.md)**, and the
  **[Embedded Web UI](embedded-web.md)**.
- **CLI and TUI**: the **[CLI index](CLI.md)**, the full
  **[CLI reference](CLI-REFERENCE.md)**, and the **[TUI guide](TUI-GUIDE.md)**.
- **Deployment and operations**: **[Server deployment](SERVER.md)**,
  **[Self-hosted server identity](server-saas.md)**,
  **[Railway deployment](RAILWAY_DEPLOYMENT.md)**, and the
  **[project context rollout runbook](project-context-protocol-identity-privilege.md)**.
- **Data and evaluation**: **[Obsidian export](OBSIDIAN_EXPORT.md)**,
  **[Benchmarks](BENCHMARKS.md)**, **[Coverage](COVERAGE.md)**,
  **[Capability matrix](capability-matrix.md)**, and the
  **[Verification matrix](verification-matrix.md)**.
- **Specifications**: [Plugin alignment](specs/plugin-alignment.md) and
  [E2E audit remediation](specs/e2e-audit-remediation.md).
- **[Documentation index](README.md)**: the same pages grouped as a reading list.
