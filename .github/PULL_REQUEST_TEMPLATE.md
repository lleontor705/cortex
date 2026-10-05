## Summary

<!-- What changed and why, in a few sentences. Reference design decisions only when non-obvious. -->

## Linked Issue

<!-- PR validation fails without an issue reference, and every referenced issue must carry the `status:approved` label (added by a maintainer). -->

Closes #N

## Type Label

<!-- PR validation requires exactly one `type:*` label on the PR. Tick after adding it. -->

- [ ] Exactly one type label added: `type:bug`, `type:feature`, `type:docs`, `type:refactor`, `type:chore`, or `type:breaking-change`

## Verification

<!-- List the commands you actually ran and their exit status. Do not leave rows empty. -->

| Command | Result |
| ------- | ------ |
| `go build ./...` | |
| `golangci-lint run ./...` | |
| `go test -v -count=1 ./...` | |

## Go Changes — Zero-CGO / Architecture Gate

<!-- Required for PRs touching Go code; PR validation does not check this, reviewers do. -->

- [ ] Not applicable (no Go code changed)
- [ ] `internal/app/arch_test.go` passes: local code stays zero-CGO and does not import PostgreSQL, authz/identity, Qdrant/pgvector, or `internal/platform/server`

## Checklist

- [ ] `make fmt` applied
- [ ] Husky pre-push hooks pass (`golangci-lint`, then `go test -v ./...`)
- [ ] Tests cover the changed behavior (target ≥ 70% coverage)
- [ ] Docs/README updated where behavior changed
