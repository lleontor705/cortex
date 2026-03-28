# Cortex CLI Parity Design

**Date:** 2026-03-27

## Goal

Implement a real `cortex` application surface that closes the largest product gap with Engram: a working CLI entrypoint, MCP startup, operational memory commands, agent setup helpers, and Engram data import, while keeping Cortex honest about features it does not yet implement.

## Scope

This phase includes:

- `cortex mcp`
- `cortex search`
- `cortex save`
- `cortex context`
- `cortex stats`
- `cortex timeline`
- `cortex setup`
- `cortex import --from-engram`
- `cortex serve` only if backed by a real HTTP server; otherwise it must fail fast with a clear message

This phase excludes:

- New Cortex-exclusive MCP tools not already implemented
- Full TUI work
- Archival, graph, scoring, and hybrid-search CLI exposure beyond what already exists in code
- Placeholder commands that suggest functionality that does not actually work

## Product Direction

Cortex should behave like an Engram-compatible memory server first, not a broader agent platform. Engram is the behavioral reference for CLI shape, flags, help text, setup flow, and import semantics wherever Cortex already has the necessary backend support.

Where Cortex lacks a backend feature, the command must either:

1. be omitted from this phase, or
2. return a direct, explicit "not implemented yet" error

The phase favors truthful product behavior over wider but misleading surface area.

## Architecture

### Command Layer

`cmd/cortex/main.go` becomes a real command router. It should remain thin: parse arguments, print help, and dispatch to package-level command functions.

### App Bootstrap Layer

A new `internal/app` package should centralize shared startup logic:

- load config
- open database
- apply migrations
- construct stores
- expose cleanup hooks

This replaces ad hoc setup inside every command and gives Cortex a single place to define "ready to serve memory" behavior.

### CLI Layer

A new `internal/cli` package should hold one function per command:

- `RunMCP`
- `RunSearch`
- `RunSave`
- `RunContext`
- `RunStats`
- `RunTimeline`
- `RunServe`
- `RunSetup`
- `RunImport`

These functions own argument parsing after the command name, command-specific usage text, and user-facing output formatting.

### Setup Layer

A new `internal/setup` package should be ported from Engram only as far as needed to install Cortex integrations for supported agents. Paths, binary names, and MCP command lines must reference `cortex`, not `engram`.

### Import Layer

Engram import should live in a dedicated package, separate from SQL migration infrastructure. The import path should read Engram data from either:

- an Engram SQLite database, or
- the same import mechanism already used by Engram if reusable without coupling

Imported data should be translated into Cortex observations, sessions, and prompts through Cortex’s own repositories/stores.

## Compatibility Rules

- Match Engram command names and high-value flags where Cortex has real support.
- Reuse Engram’s user-facing command ergonomics when possible.
- Keep MCP compatibility highest priority.
- Do not introduce new Cortex-specific CLI options in this phase unless required by current internals.

## Error Handling

- Unknown commands print help and exit non-zero.
- Unsupported-but-documented-later commands must print direct status, not silently no-op.
- `serve` must fail clearly if HTTP server support is not yet real.
- `import --from-engram` must report what source it read and what entities were imported.

## Testing Strategy

Add tests at four levels:

1. command dispatch tests for `cmd/cortex`
2. unit tests for CLI command functions
3. integration tests for MCP startup and Engram import
4. comparison checks against local Engram behavior for shared command flows

At minimum, this phase should verify:

- `cortex mcp` builds and starts the MCP server
- `cortex save/search/context/stats/timeline` operate against a real SQLite-backed Cortex instance
- `cortex setup <agent>` writes Cortex-specific integration config
- `cortex import --from-engram` imports real Engram fixtures into Cortex

## Documentation Impact

At the end of the phase:

- `README.md` must be updated to match the commands that actually work
- command help output must align with the README
- any command intentionally deferred must be described as deferred, not available

## Non-Goals

- Achieving full feature parity with all future README claims
- Re-architecting the store layer
- Adding new memory semantics beyond what already exists in the current codebase

## Success Criteria

This phase is successful when a user can install or build Cortex and use it as a real memory CLI/MCP tool instead of a stub binary, with behavior that is recognizably aligned with Engram and documentation that is accurate to the implementation.
