# Contributing to Cortex

## Quick Start

1. Fork and clone the repository
2. `go mod download`
3. `make test` to verify everything works
4. Create a branch, make changes, submit a PR

## Development

```bash
make build            # Build binary
make test             # Run all tests
make lint             # Run linter
make fmt              # Format code
make test-coverage    # Coverage report
```

## Issue-First Workflow

1. **Open an issue** before starting work — describe what and why
2. **Get approval** — wait for maintainer to add `status:approved` label
3. **Open a PR** linking the issue: `Closes #N` in the PR body
4. **Add a type label**: `type:bug`, `type:feature`, `type:docs`, `type:refactor`, `type:chore`

## Commit Format

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```
feat(graph): add BFS path finding between observations
fix(search): handle empty FTS5 query gracefully
refactor(store): extract common scan helpers
docs(readme): update installation instructions
```

## Code Style

- Follow existing patterns in the codebase
- Table-driven tests with `t.Run()` subtests
- Domain services depend on repository interfaces, stores implement them
- Use `testutil.NewTestDBWithMigrations()` for store tests
- Error wrapping: `fmt.Errorf("package: operation: %w", err)`

## Testing

- Target 70%+ coverage
- Store tests: use in-memory SQLite with inline migration SQL
- Domain tests: use mocks or real stores
- CLI tests: call `Run()` with args, capture stdout/stderr
- MCP tests: call handler functions directly with test `Stores`

## Labels

| Category | Labels |
|----------|--------|
| Type (required on PR) | `type:bug`, `type:feature`, `type:docs`, `type:refactor`, `type:chore`, `type:breaking-change` |
| Status | `status:needs-review`, `status:approved`, `status:in-progress`, `status:blocked` |
| Priority | `priority:high`, `priority:medium`, `priority:low` |

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
