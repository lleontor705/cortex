# Context Mode Features: Phase 1 Sandbox Execution, Auto-Externalization & Token Metrics

## Intent

Context Mode optimizes agent token efficiency and processing safety in Cortex by introducing three core architectural capabilities for Phase 1 (Wave 1):
1. An isolated, Zero-CGO polyglot execution sandbox (`cortex_execute`) using native host runtimes (Python, Node/Bun, Go, PowerShell/Bash) with configurable timeouts, bounded memory buffers, and optional file context injection.
2. An automatic payload externalization engine that intercepts massive tool outputs exceeding 100 KB (102,400 bytes) and persists them to a dedicated SQLite `transient_payloads` table with FTS5 full-text indexing, associated with the active `session_id`, returning a compact snippet pointer instead of saturating LLM context windows.
3. A token savings accounting ledger that tracks prompt tokens saved per session and globally, reporting these metrics transparently in `cortex_stats`.

Non-goals for Phase 1 (Wave 1):
- Wave 2 capabilities (dynamic HippoRAG graph context expansion, externalized payload summarization pipelines, remote Docker container isolation).
- Persisting transient execution artifacts or large outputs to permanent `observations` or modifying HippoRAG knowledge graph nodes/edges.
- Multi-tenant cloud sandbox orchestration or arbitrary network egress virtualization.

## Requirements

### Requirement: REQ-SANDBOX-001 cortex_execute polyglot sandbox execution with context injection

Cortex MUST expose an MCP tool `cortex_execute` enabling execution of ephemeral scripts across supported system runtimes (Python, Node/Bun, Go, PowerShell/Bash) in a Zero-CGO environment using `os/exec.CommandContext`. The runner MUST enforce configurable execution timeouts (default 15s, max 60s), memory-safe output buffer limits (default 5MB, max 20MB), capture stdout, stderr, exit code, and execution duration, and support optional file context injection (`FILE_PATH` and `FILE_CONTENT` when <= 500 KB).

#### Scenario: successful polyglot script execution (Happy path)
- GIVEN an agent requests execution of valid code in a recognized language (Python, Node/Bun, Go, PowerShell, or Bash) whose runtime is present in the host PATH
- WHEN `cortex_execute` invokes the runner within the configured timeout and memory limits
- THEN the subprocess completes with exit code 0, captures complete standard output and error, records execution duration, and returns formatted results without truncation

#### Scenario: execution with memory limit truncation and file context injection (Edge case)
- GIVEN an execution request specifying an existing file path and code that produces streaming output exceeding the configured output byte limit
- WHEN the runner executes the command with `FILE_PATH` and `FILE_CONTENT` injected into the environment
- THEN output collection terminates at the specified byte cap, sets `truncated: true` without memory exhaustion, and reports the partial stdout and exit status safely

#### Scenario: runtime unavailable or execution timeout (Error state)
- GIVEN a script targeting an uninstalled language runtime or a script that loops indefinitely exceeding the specified timeout
- WHEN the runner attempts process dispatch or reaches the deadline
- THEN the execution fails closed, terminates the subprocess tree cleanly without orphan processes, and returns a clear actionable error listing available system runtimes

### Requirement: REQ-PAYLOAD-001 auto-externalization of large payloads (>100KB) to transient SQLite FTS5 store

Cortex MUST automatically intercept tool outputs and execution results exceeding 100 KB (102,400 bytes) and store them in a dedicated `transient_payloads` SQLite table accompanied by a `transient_payloads_fts` full-text index. Externalized payloads MUST be associated with the active `session_id` and project, generate a compact pointer snippet with token savings metadata, and remain isolated from permanent `observations` and HippoRAG graph traversal.

#### Scenario: automatic payload externalization and pointer generation (Happy path)
- GIVEN an execution result or tool payload whose byte length meets or exceeds 102,400 bytes (100 KB)
- WHEN the auto-externalization filter processes the output
- THEN the full raw content is persisted in `transient_payloads`, indexed in `transient_payloads_fts`, and replaced in the MCP response by a structured pointer containing the payload ID, byte count, token savings estimate, and a compact head preview snippet

#### Scenario: exact threshold boundary and multiline snippet formatting (Edge case)
- GIVEN an output exactly at the 100 KB boundary (102,400 bytes) with non-standard line breaks or dense single-line data
- WHEN externalization evaluation and snippet generation occur
- THEN the payload is deterministically externalized without boundary off-by-one errors, and snippet construction yields bounded preview lines with an explicit line and byte omission notice

#### Scenario: database write failure or corrupted session handling (Error state)
- GIVEN an invalid database handle or disk write failure during transient payload persistence
- WHEN `transient_payloads` insert or FTS5 indexing fails
- THEN the operation returns a structured database error, avoids corrupting the permanent `observations` table, and does not leak partial unindexed transient records

### Requirement: REQ-METRICS-001 token savings tracking and reporting in cortex_stats

Cortex MUST track estimated tokens saved through auto-externalization per session and globally across all sessions using character-to-token heuristics (~4 characters per token). The system statistics tool `cortex_stats` MUST expose aggregated externalized payload counts, total externalized bytes, and cumulative tokens saved alongside existing session and observation metrics.

#### Scenario: reporting accumulated token savings in memory stats (Happy path)
- GIVEN multiple large payloads have been externalized during active or past sessions
- WHEN an agent or operator calls `cortex_stats`
- THEN the returned system statistics include total externalized payload count, total externalized data size in human-readable format, and total estimated tokens saved

#### Scenario: zero externalized payloads cold start (Edge case)
- GIVEN a freshly initialized database or session with zero externalized payloads
- WHEN `cortex_stats` queries transient payload metrics
- THEN the stats report gracefully displays 0 externalized payloads and 0 tokens saved without null dereference or division-by-zero errors

#### Scenario: transient store degradation during stats query (Error state)
- GIVEN a temporary SQLite read lock or table error specific to the transient store
- WHEN `cortex_stats` executes
- THEN the tool returns core session and observation statistics while appending an informational degradation notice for transient metrics, preserving primary tool availability

## Design

### Package Architecture & Seams

The Phase 1 architecture strictly respects the Zero-CGO and local-only dependency boundaries enforced by `internal/app/arch_test.go`:

1. `internal/domain/sandbox`:
   - `Runner` interface:
     ```go
     type Runner interface {
         Execute(ctx context.Context, req ExecutionRequest) (*ExecutionResult, error)
         AvailableRuntimes() map[Language]string
     }
     ```
   - Request/Result models: `ExecutionRequest`, `ExecutionResult`, `Language`.
   - Implementation: `DefaultRunner` using `os/exec.CommandContext`, temporary execution directory, limited writers (`limitedWriter`), input injection (`FILE_PATH`, `FILE_CONTENT`), process timeout and signal handling.
   - Clean boundaries: No dependencies on SQLite or MCP. Zero-CGO pure Go.

2. `internal/domain/payload`:
   - `TransientPayload` model:
     `ID`, `SessionID`, `Project`, `SourceTool`, `ContentType`, `ByteCount`, `TokensSaved`, `Snippet`, `Content`, `CreatedAt`.
   - Core functions: `EstimateTokens(string) int`, `ShouldExternalize(byteCount int, threshold ...int) bool`, `NewTransientPayload(...)`.
   - `Store` interface:
     ```go
     type Store interface {
         Save(ctx context.Context, p *TransientPayload) error
         Get(ctx context.Context, id string) (*TransientPayload, error)
         Search(ctx context.Context, id string, query string, limit int) ([]SearchResult, error)
         PurgeSession(ctx context.Context, sessionID string) error
         Stats(ctx context.Context, sessionID string) (totalSavedTokens int, totalBytes int, count int, err error)
         GlobalStats(ctx context.Context) (totalSavedTokens int, totalBytes int, count int, err error)
     }
     ```

3. `internal/store/sqlite`:
   - Dedicated migration `004_transient_payloads.sql`:
     Creates table `transient_payloads` and virtual table `transient_payloads_fts USING fts5(id UNINDEXED, content, tokenize='unicode61')`.
   - `TransientPayloadStore`:
     Implements `payload.Store` on SQLite with transaction-safe inserts into `transient_payloads` and `transient_payloads_fts`, snippet generation via FTS5 `snippet()`, and global/session aggregation queries.

4. `internal/store/bundle`:
   - `Stores` struct updated to include:
     - `TransientPayloads payload.Store`
     - `SandboxRunner sandbox.Runner`
   - Initialized in `bundle.go` for local composition roots.

5. `internal/mcp`:
   - `ProfileAgent` in `server.go` registers `"cortex_execute": true`.
   - `internal/mcp/tools_sandbox.go` implements `handleExecute`:
     - Parses parameters (`language`, `code`, `file_path`, `timeout`, `args`).
     - Invokes `stores.SandboxRunner.Execute()`.
     - Evaluates output length using `payload.ShouldExternalize()`.
     - Externalizes stdout/stderr if >= 100 KB, saving to `stores.TransientPayloads`, computing tokens saved, and returning the pointer snippet.
   - `internal/mcp/tools_memory.go` updates `handleStats`:
     - Calls `stores.TransientPayloads.GlobalStats(ctx)` and reports externalized payload statistics.

### SQLite Schema Migration (004_transient_payloads.sql)

```sql
CREATE TABLE IF NOT EXISTS transient_payloads (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    project TEXT NOT NULL,
    source_tool TEXT NOT NULL,
    content_type TEXT NOT NULL DEFAULT 'text/plain',
    byte_count INTEGER NOT NULL,
    tokens_saved INTEGER NOT NULL,
    snippet TEXT NOT NULL,
    content TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_transient_payloads_session
    ON transient_payloads(session_id);
CREATE INDEX IF NOT EXISTS idx_transient_payloads_project
    ON transient_payloads(project);

CREATE VIRTUAL TABLE IF NOT EXISTS transient_payloads_fts USING fts5(
    id UNINDEXED,
    content,
    tokenize='unicode61'
);
```

## Tasks

- [ ] context-mode-1 [migration] Define SQLite schema for transient_payloads and FTS5 indexing
  Requirements: REQ-PAYLOAD-001
  Allowed files target: migrations/v2/004_transient_payloads.sql; migrations/v2/embed.go; internal/migration/v2.go; internal/migration/v2_test.go
  Verification: go test -v -count=1 ./internal/migration -run TestV2Migrations
  Forecast: <= 200 changed lines Go/SQL, <= 350 Go cap.

- [ ] context-mode-2 [store/sqlite] Implement TransientPayloadStore with FTS5 and aggregate stats
  Requirements: REQ-PAYLOAD-001, REQ-METRICS-001
  Allowed files target: internal/store/sqlite/transient_payload_store.go; internal/store/sqlite/transient_payload_store_test.go
  Verification: go test -v -count=1 ./internal/store/sqlite -run TestTransientPayloadStore
  Forecast: <= 280 changed lines Go, <= 350 Go cap.

- [ ] context-mode-3 [bundle] Wire TransientPayloadStore and sandbox.Runner into bundle.Stores
  Requirements: REQ-SANDBOX-001, REQ-PAYLOAD-001, REQ-METRICS-001
  Allowed files target: internal/store/bundle/bundle.go; internal/store/bundle/bundle_test.go; internal/store/bundle/transient_bundle_test.go
  Verification: go test -v -count=1 ./internal/store/bundle -run TestStoresInitialization
  Forecast: <= 120 changed lines Go, <= 350 Go cap.

- [ ] context-mode-4 [domain/sandbox] Enhance DefaultRunner with process isolation, buffer limits, and injection
  Requirements: REQ-SANDBOX-001
  Allowed files target: internal/domain/sandbox/process.go; internal/domain/sandbox/process_test.go; internal/domain/sandbox/runner.go; internal/domain/sandbox/runner_test.go
  Verification: go test -v -count=1 ./internal/domain/sandbox -run TestProcessExecution
  Forecast: <= 220 changed lines Go, <= 350 Go cap.

- [ ] context-mode-5 [mcp] Register cortex_execute tool with auto-externalization for >100KB payloads
  Requirements: REQ-SANDBOX-001, REQ-PAYLOAD-001
  Allowed files target: internal/mcp/server.go; internal/mcp/tools_sandbox.go; internal/mcp/tools_sandbox_test.go
  Verification: go test -v -count=1 ./internal/mcp -run TestExecuteTool
  Forecast: <= 290 changed lines Go, <= 350 Go cap.

- [ ] context-mode-6 [mcp/metrics] Expose saved token metrics in cortex_stats
  Requirements: REQ-METRICS-001
  Allowed files target: internal/mcp/tools_memory.go; internal/mcp/tools_memory_stats_test.go
  Verification: go test -v -count=1 ./internal/mcp -run TestStatsPayloadMetrics
  Forecast: <= 140 changed lines Go, <= 350 Go cap.

- [ ] context-mode-7 [integration] End-to-end integration test of sandbox execution, auto-externalization, and stats reporting
  Requirements: REQ-SANDBOX-001, REQ-PAYLOAD-001, REQ-METRICS-001
  Allowed files target: internal/mcp/integration_sandbox_test.go
  Verification: go test -v -count=1 ./internal/mcp -run TestIntegrationSandboxAndExternalization
  Forecast: <= 200 changed lines Go, <= 350 Go cap.
