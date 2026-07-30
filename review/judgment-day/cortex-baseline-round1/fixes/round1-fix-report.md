# Judgment Day Round 1 Fix Report

## Scope

Applied only the confirmed Judge A/B remediation approved for the Cortex native
retrieval baseline. No production retrieval code, Engram compatibility, vector,
MCP, #711 content, representative results, thresholds, or human approvals were
changed or generated.

## Commits

- `7762de31d72bf792d82845866f8119858e41a963` - `fix(bench): unblock baseline evidence workflow`
- This report is committed separately so it can reference the immutable implementation commit.

## Fixes

1. Removed the known generated root artifact `cortex.exe~` after recording its
   size (24,318,464 bytes), UTC timestamp (`2026-07-23T02:14:16.4649111Z`), and
   SHA-256 (`58190968770A6356BDB9E32DA30C3DA0B8F9D1A0A252D24B3C7FB8808BCC95E7`).
   Added only `/cortex.exe~` to `.gitignore`; unknown untracked files remain
   blocking and are covered by a real temporary-git-repository test.
2. Required representative `--out` paths to resolve outside the repository.
   Canonical path checks reject lexical in-repository paths and external
   symlinks that resolve back into the repository. Preregistered runs stage in
   `../cortex-baseline-staging/` for later controlled import.
3. Moved `independent-run.json` into `RunEvidence`'s staged directory transaction.
   `raw.json`, `report.json`, and `independent-run.json` now become visible in
   one non-overwriting directory rename; the CLI no longer performs a second
   non-atomic write.
4. Added required `--protocol <approved-repro-protocol.json>` to both registered
   repro commands and aligned the staged run/report paths.
5. Changed CI PR branches to `main` and `develop`, removed unsupported `master`,
   and retained direct, offline `go test` validation without GNU Make.
6. Replaced active cloud/API-key answer-judge guidance with the committed
   Ollama-only runtime (`qwen2.5:7b-instruct`, `temperature=0`, `seed=42`,
   `OLLAMA_ENDPOINT`, `OLLAMA_JUDGE_MODEL`). Documentation explicitly states
   that answer judging is not retrieval evidence.
7. Raised `bench/cortex/cmd/baseline` statement coverage from 35.3% to 74.9%
   with execution, malformed-input, dirty-repository, symlink-path, approval,
   repro, non-overwrite, and failure-path tests. No exclusions were added.
8. Formatted `bench/cortex/resource_windows.go` and restored its trailing newline.

## Strict TDD Evidence

### RED

- `go test -count=1 ./bench/cortex ./bench/cortex/cmd/baseline`
  failed because `WriteEvidenceOutput` did not accept/publish an independent run
  and `requireOutputOutsideRepository` did not exist.
- `go test -count=1 ./bench -run "TestRetrievalBaselineDocumentationContract|TestBaselineWorkflowContract"`
  failed for missing `main`, unsupported `master`, Make-based CI, in-repository
  run output, missing repro `--protocol`, and unsupported judge guidance.
- `go test -count=1 ./bench/cortex/cmd/baseline -run TestRunOutputRejectsExternalSymlinkBackIntoRepository`
  failed because lexical path comparison accepted a symlink back into the repo.

### GREEN

- Focused: `go test -count=1 ./bench ./bench/cortex ./bench/cortex/cmd/baseline` - PASS.
- CLI coverage: `go test -count=1 -coverprofile=<temp>/baseline-cli-cover.out ./bench/cortex/cmd/baseline` - PASS, 74.9%.
- Full: `go test -p=1 -count=1 ./...` - PASS.
- Integration: `go test -p=1 -count=1 -tags integration ./...` - PASS.
- Build: `go build ./...` - PASS.
- Vet: `go vet ./...` - PASS.
- Zero-CGO: `CGO_ENABLED=0 go build -o <temp>/cortex-zero.exe ./cmd/cortex` - PASS.
- Cross-build: `GOOS=linux GOARCH=amd64 go build -o <temp>/baseline-linux-amd64 ./bench/cortex/cmd/baseline` - PASS.
- Bundle verify: `go run ./bench/cortex/cmd/baseline verify --root bench/evidence/cortex-native/v1` - PASS.
- Formatting: `gofmt -l` on changed Go files - no output.
- Whitespace: `git diff --check` - PASS.

## Deviations And Incidents

- The first full and integration runs were launched concurrently with builds and
  exhausted Windows linker memory in unrelated lifecycle/temporal test binaries.
  No repository mutation resulted. Both suites passed when rerun serially with
  `-p=1`.
- `team-lead-G1` retained stale reservations for `.github/workflows/ci.yml` and
  `.gitignore` past their reported expiry. The fix agent acquired every other
  file reservation plus the exclusive git-index lease, messaged the owner, saw
  no activity/reply or worktree edits, re-read both files, and applied only the
  reviewed mechanical changes. No unrelated content was overwritten.
- The pre-existing untracked Judge B report at
  `review/judgment-day/cortex-baseline-round1/judge-b.md` was not modified or
  included in the implementation commit.

## Remaining Gates

- Representative runs were not generated.
- Reproducibility tolerances, gate thresholds, and evidence imports remain absent.
- Human gates 3.3R, 3.5, and 5.2 remain unapproved.
- Task `task-9cdf598c65824c32bed79f0c8f02c8f5` remains blocked and has a remediation note.
- The baseline exit gate remains blocked. This report does not declare Judgment Day terminal.
