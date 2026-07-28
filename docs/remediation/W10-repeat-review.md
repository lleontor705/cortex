# W10 repeat-review remediation report

## Changes

- Export transaction cleanup now reports `RemoveAll` and handle `Close` failures,
  while preserving the primary operation error when both fail.
- Added runtime coverage for symlink files/directories, traversal boundaries,
  case-insensitive collisions, bounded/hash-stable paths, golden bytes, and
  source immutability.
- Retry policy now has an injected sleeper seam; tests assert exact attempts,
  backoff sequence, and stable BUSY errors without wall-clock correctness.
- Symlink containment now treats Windows reparse points (including directory
  junctions) as unsafe, while retaining ordinary symlink checks on Unix.
- The file and directory guard branches also have deterministic metadata-seam
  coverage, so an unavailable OS primitive cannot silently remove branch
  coverage.

## Verification

- Focused Obsidian and bundle tests: `-count=2` PASS.
- Windows focused guard tests: verbose file symlink and directory junction
  cases PASS without skips; deterministic injected file/directory reparse
  guard tests PASS.
- Ubuntu unit CI continues to run the native symlink cases through the existing
  `go test -v -count=1 ./...` unit job; no extra selection is required.
- Full tests: `go test ./... -count=1` PASS.
- Integration-tagged tests: PASS.
- `go vet ./...` PASS.
- `CGO_ENABLED=0 go build ./cmd/cortex` and full zero-CGO tests PASS.
- Fresh package coverage remains above 70% overall; Obsidian 84.5%, bundle 82.1%.
- `gofmt -l` is clean, including the normalized `internal/app/arch_test.go`
  blob; `git diff --check` PASS.

Research files were preserved; generated `$profile` coverage output was removed.
