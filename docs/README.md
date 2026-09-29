# Cortex Documentation

## Core

- [Architecture](ARCHITECTURE.md): runtime boundaries, storage, migration, and authorization.
- [Configuration](CONFIGURATION.md): YAML keys, defaults, and environment variables.
- [Installation](INSTALLATION.md): source, release, Docker, and agent setup prerequisites.
- [Graph intelligence](GRAPH_INTELLIGENCE.md): AST extraction, communities, blast radius, and cycle detection.

## Interfaces

- [MCP](MCP.md): local profiles, server tools, schemas, and transport differences.
- [HTTP API](HTTP-API.md): local SQLite and server PostgreSQL endpoints.
- [Agent setup](AGENT-SETUP.md): supported agent integrations.
- [Plugins](PLUGINS.md): hooks, privacy markers, and plugin limitations.
- [Embedded Web UI](embedded-web.md): in-binary UI, access-key lifecycle, and per-mode capabilities.

## CLI and TUI

- [CLI](CLI.md): short index into the CLI contract and migration limitations.
- [CLI reference](CLI-REFERENCE.md): every command with flags, defaults, environment overrides, authentication, and examples.
- [TUI guide](TUI-GUIDE.md): interactive terminal screens, keys, and workflows.

## Deployment and operations

- [Server deployment](SERVER.md): PostgreSQL, authentication, Docker, and operations.
- [Self-hosted server identity](server-saas.md): single-tenant identity and privilege configuration constants.
- [Railway deployment](RAILWAY_DEPLOYMENT.md): deploying the server image and PostgreSQL on Railway.
- [Project context rollout runbook](project-context-protocol-identity-privilege.md): migration checksum preflight, apply, and rollback policy.

## Data and evaluation

- [Obsidian export](OBSIDIAN_EXPORT.md): read-only projection behavior.
- [Benchmarks](BENCHMARKS.md): reproducible evaluation protocol and offline gates.
- [Coverage](COVERAGE.md): coverage policy, CI gates, and reproduction steps.
- [Capability matrix](capability-matrix.md): shipped capabilities mapped to implementation and tests.
- [Verification matrix](verification-matrix.md): verification gates, commands, and pass criteria.
