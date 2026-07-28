# W10 Obsidian export remediation

Implemented on top of `b7a4b40` in commit `efdfded`.

## Findings addressed

- Transactional staging with backup/rollback, atomic same-filesystem renames,
  manifest-last commit, and best-effort file/directory sync.
- Symlink and traversal rejection during vault discovery, reads, and writes.
- Checksum/ownership validation for renamed notes and edited-rename conflicts.
- Injectable clock and byte-stable no-op manifests.
- Duplicate `cortex_id` diagnostics with both paths.
- Unicode-normalized, Windows-safe, bounded path components.
- YAML escaping, deterministic wikilinks, privacy filtering, and a committed
  golden fixture.

## Verification

- `go test ./internal/projection/obsidian -count=5`
- `go test ./...`
- `go vet ./...`
- `CGO_ENABLED=0 go test ./internal/projection/obsidian`
- `CGO_ENABLED=0 go build ./cmd/cortex`
- Obsidian package coverage: 84.0%.
