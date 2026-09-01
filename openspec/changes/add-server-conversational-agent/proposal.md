# Proposal: Server Conversational Project Agent

## Why

Cortex server exposes authorized memory, hybrid retrieval and AST metadata, but the Web has no conversational surface that can explain what a project contains or what was developed. Building chat directly over the current code store would be unsafe: PostgreSQL `code_symbols` and `code_relations` are process-created, unscoped tables and their `AuthorizedStore` methods perform no code-specific authorization.

## What Changes

- Add a read-only, server-side RAG service shared by `POST /api/agent/answer` (JSON) and `POST /api/agent/stream` (SSE).
- Authorize by verified capabilities and project grants, never by requiring the `agent` role. Memory, search and code retrieval remain independently authorized.
- Introduce `ResourceCode`/`ActionRead` and a forward-only PostgreSQL 109 migration for tenant/workspace/project-scoped AST symbols and relations with forced RLS.
- Reindex legacy AST metadata into the scoped schema from trusted project checkouts. Ambiguous unscoped rows are never assigned to a tenant and never queried.
- Ground answers only in authorized memories and AST metadata (symbol, signature, path and relationship; no source bodies), with server-resolved citations, confidence and degradation status.
- Accept at most six ephemeral conversation turns from the browser as untrusted data. Cortex stores no conversation transcript.
- Add `/agent` to Web with an accessible project selector, chat, source panel, cancellation and JSON-to-SSE progressive enhancement.
- Apply server-configured model/provider, quotas, timeouts, cancellation and content-free audit metadata.

## Success Criteria

- A principal with required capabilities sees only granted projects and sources from its verified tenant/workspace/project boundary.
- Sibling tenants/workspaces and unscoped legacy AST rows cannot affect retrieval, ranking, prompts or citations.
- Prompt injection in questions, history or retrieved content cannot change system policy or trigger tools/writes.
- JSON and SSE use one application service and produce equivalent answer/citation/degradation semantics.
- Missing AI/vector/code dependencies fail closed or return an explicit safe degraded answer; no fabricated citation is emitted.

## Compatibility / Non-Goals

Existing search, MCP, TUI and local SQLite behavior remain unchanged. This MVP does not persist conversations, index code bodies, execute tools, modify memories/code, expose provider selection, or create autonomous/background agents. It depends on the existing P0 SSRF-safe provider composition, tenant/workspace vector sealing, explicit CORS and migration-role separation.
