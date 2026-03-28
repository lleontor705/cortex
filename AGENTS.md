# Cortex — Agent Skills Index

When working on this project, load the relevant skill(s) BEFORE writing any code.

## How to Use

1. Check the trigger column to find skills that match your current task
2. Load the skill by reading the SKILL.md file at the listed path
3. Follow ALL patterns and rules from the loaded skill
4. Multiple skills can apply simultaneously

## Project Overview

Cortex is a next-generation memory server for AI coding assistants, built on Engram's foundation with enhanced features:

- **Core**: SQLite + FTS5 full-text search (Engram-compatible)
- **New**: Knowledge graph, importance scoring, auto-archival, vector search
- **Interface**: MCP server, HTTP API, CLI, TUI
- **Goal**: 100% API compatibility with Engram + advanced memory features

## Architecture Principles

1. **Compatibility First**: All 14 Engram MCP tools must produce identical outputs
2. **Single Binary**: Zero external runtime dependencies
3. **SQLite-First**: All data in SQLite with FTS5, relationships via foreign keys
4. **Configuration Flexibility**: YAML + environment variables with sensible defaults
5. **Test Coverage**: Minimum 70% coverage, integration tests for critical paths
6. **Performance**: Sub-10ms search on 10K+ observations

## Package Responsibilities

| Package | Responsibility | Key Types |
|---------|---------------|-----------|
| `cmd/cortex` | CLI entry point, command routing | CLI flags, subcommands |
| `internal/store` | SQLite storage layer, migrations | Store, Observation, Session |
| `internal/search` | FTS5 + vector search, RRF fusion | Searcher, HybridSearcher |
| `internal/graph` | Knowledge graph, relationships, entities | GraphStore, Relationship, Entity |
| `internal/lifecycle` | Importance scoring, archival | Scorer, Archiver |
| `internal/mcp` | MCP server implementation | Server, Tool handlers |
| `internal/http` | REST API endpoints | Handler, routes |
| `internal/config` | Configuration management | Config, validation |
| `internal/tui` | Terminal UI (BubbleTea) | Model, views |

## Key Patterns

### Storage Layer
- Use `internal/store` for all database operations
- Transaction boundaries: one transaction per business operation
- Soft delete by default (hard delete requires explicit flag)
- FTS5 triggers keep index synchronized automatically

### Search
- FTS5 is primary search mechanism (always available)
- Vector search is optional (requires sqlite-vec compile flag)
- Hybrid search uses Reciprocal Rank Fusion (RRF) with k=60
- Query sanitization: wrap terms in double quotes to prevent FTS5 syntax errors

### Knowledge Graph
- Relationships: `related_to`, `contradicts`, `supersedes`, `depends_on`, `derived_from`
- Entity types: `file`, `package`, `symbol`, `url`, `concept`
- Traversal: depth-limited BFS with visited set to prevent cycles
- Relationships persist even if observations are soft-deleted

### Importance Scoring
- Score = base_weight × time_decay × reference_factor
- Time decay: exponential with configurable half-life (default 30 days)
- Reference counting: increment on search result or direct retrieval
- Scoring is lazy (calculated on demand, not stored)

### MCP Tools
- 14 Engram tools: identical signatures, identical response formats
- 5 Cortex-exclusive tools: graph, scoring, archival, hybrid search
- Tool profiles: `agent` (11 tools), `admin` (3 tools), or custom combinations
- Response format: human-readable text, not JSON

### Configuration
- YAML file at `~/.cortex/config.yaml`
- Environment variables: `CORTEX_<SECTION>_<KEY>` (e.g., `CORTEX_DATABASE_PATH`)
- Validation: fail-fast on invalid config, clear error messages
- Defaults: all values have sensible defaults

## Testing Requirements

### Unit Tests
- Test coverage >= 70%
- Test edge cases: empty results, invalid inputs, boundary conditions
- Use table-driven tests for multiple scenarios

### Integration Tests
- Tagged with `// +build integration`
- Test against real SQLite database
- Cover: migrations, FTS5 triggers, graph relationships, archival

### Benchmark Tests
- Located in `*_test.go` files with `Benchmark` prefix
- Run with `go test -bench=.`
- Focus on: search performance, scoring calculation, graph traversal

### Compatibility Tests
- Compare Cortex vs Engram outputs for identical inputs
- Test all 14 MCP tools with various parameter combinations
- Verify response format matches exactly (including whitespace)

## Documentation Standards

### Code Comments
- Exported types/functions: document purpose, parameters, return values
- Complex algorithms: explain the "why" not just the "what"
- SQL queries: document expected columns and their types

### README.md
- Keep Quick Start section up to date
- Include examples for common use cases
- Link to detailed docs for advanced topics

### API Documentation
- MCP tools: description, parameters, examples in tool schema
- HTTP endpoints: request/response schemas, error codes
- Configuration: all options with types and defaults

### Migration Guides
- Document breaking changes between versions
- Provide step-by-step upgrade instructions
- Include rollback procedures when applicable

## Skills

| Skill | Trigger | Path |
|-------|---------|------|
| `cortex-architecture-guardrails` | Any change affecting system boundaries, ownership, state flow, or cross-package responsibilities | [`skills/architecture-guardrails/SKILL.md`](skills/architecture-guardrails/SKILL.md) |
| `cortex-mcp-parity` | Changes to MCP tool signatures, response formats, or behavior | [`skills/mcp-parity/SKILL.md`](skills/mcp-parity/SKILL.md) |
| `cortex-knowledge-graph` | Working with relationships, entities, or graph traversal | [`skills/knowledge-graph/SKILL.md`](skills/knowledge-graph/SKILL.md) |
| `cortex-search-engine` | FTS5 queries, vector search, hybrid search, or result ranking | [`skills/search-engine/SKILL.md`](skills/search-engine/SKILL.md) |
| `cortex-lifecycle` | Importance scoring, auto-archival, or reference counting | [`skills/lifecycle/SKILL.md`](skills/lifecycle/SKILL.md) |
| `cortex-storage-layer` | SQLite schema changes, migrations, or store methods | [`skills/storage-layer/SKILL.md`](skills/storage-layer/SKILL.md) |
| `cortex-configuration` | Config file changes, environment variables, or validation | [`skills/configuration/SKILL.md`](skills/configuration/SKILL.md) |
| `cortex-testing` | Writing or modifying tests, improving coverage | [`skills/testing/SKILL.md`](skills/testing/SKILL.md) |
| `cortex-migration` | Engram migration logic, data transformation, or compatibility | [`skills/migration/SKILL.md`](skills/migration/SKILL.md) |
| `cortex-http-api` | HTTP routes, handlers, request/response formats | [`skills/http-api/SKILL.md`](skills/http-api/SKILL.md) |
| `cortex-cli` | CLI commands, flags, or help text | [`skills/cli/SKILL.md`](skills/cli/SKILL.md) |
| `cortex-tui` | Terminal UI screens, navigation, or rendering | [`skills/tui/SKILL.md`](skills/tui/SKILL.md) |

## Common Workflows

### Adding a New MCP Tool
1. Load `cortex-mcp-parity` skill
2. Define tool schema in `internal/mcp/tools.go`
3. Implement handler in `internal/mcp/handlers.go`
4. Add to appropriate tool profile (agent/admin/custom)
5. Write unit tests for handler
6. Update README.md MCP tools table
7. Add integration test for full workflow

### Modifying Storage Schema
1. Load `cortex-storage-layer` skill
2. Create new migration file in `migrations/`
3. Implement Up() and Down() SQL
4. Update `internal/store` types if needed
5. Add migration test case
6. Update REQ-032/REQ-033 specs if structure changes

### Adding Knowledge Graph Feature
1. Load `cortex-knowledge-graph` skill
2. Add methods to `internal/graph` package
3. Ensure FTS5 triggers updated if needed
4. Add graph-specific MCP tool if user-facing
5. Write integration tests with graph fixtures
6. Update graph traversal documentation

### Optimizing Search Performance
1. Load `cortex-search-engine` skill
2. Write benchmark for current performance
3. Implement optimization
4. Verify benchmark improvement
5. Ensure FTS5 triggers still work
6. Test with large dataset (10K+ observations)
7. Document performance characteristics

### Adding Configuration Option
1. Load `cortex-configuration` skill
2. Add field to `internal/config` Config struct
3. Set default value in defaults
4. Add YAML parsing support
5. Add environment variable mapping
6. Add validation logic
7. Write unit tests for config loading
8. Update docs/CONFIGURATION.md

## Debugging Tips

### FTS5 Issues
- Check triggers are installed: `SELECT * FROM sqlite_master WHERE type='trigger'`
- Verify FTS5 table exists: `.tables` in sqlite3 CLI
- Test query directly: `SELECT * FROM observations_fts WHERE observations_fts MATCH '"term"'`

### Knowledge Graph Issues
- Check relationship exists: `SELECT * FROM observation_relationships WHERE source_id = X`
- Verify entity extraction: `SELECT * FROM observation_entities WHERE observation_id = X`
- Test traversal: use `mem_graph` tool with small depth first

### Importance Scoring Issues
- Calculate manually: base_weight × 0.5^(age/30) × (1 + log(1 + ref_count))
- Check reference count: `SELECT id, reference_count FROM observations WHERE id = X`
- Verify time decay formula matches REQ-021

### Migration Issues
- Check current version: `SELECT * FROM _cortex_migrations ORDER BY version DESC LIMIT 1`
- Verify migration checksums match expected SHA-256
- Test rollback with `--down` flag on test database first

## Performance Guidelines

- **Batch Operations**: Use transactions for multiple inserts/updates
- **Search**: Limit queries to MaxSearchResults (default 100)
- **Graph Traversal**: Limit depth to prevent exponential blowup
- **Importance Scoring**: Calculate lazily, don't pre-compute for all observations
- **Archival**: Run during low-usage periods (configurable interval)

## Security Considerations

- **Private Data**: Content with `<private>...</private>` tags is redacted before storage
- **SQL Injection**: Use parameterized queries exclusively
- **File Paths**: Validate database path is absolute and accessible
- **Auth Token**: HTTP API supports optional token-based auth
- **Input Validation**: Truncate content to MaxObservationLength, validate types/scopes

## Getting Help

1. Check relevant skill file for patterns and rules
2. Review REQ specifications in delta-specs.md
3. Look at existing implementations in similar packages
4. Run tests to understand expected behavior
5. Check Engram codebase for reference implementation (when adding Engram-compatible features)

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for:
- Code style guidelines
- Commit message format
- Pull request process
- Testing requirements
- Documentation standards
