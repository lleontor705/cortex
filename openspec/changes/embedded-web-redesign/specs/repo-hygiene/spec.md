# Delta for repo-hygiene

## ADDED Requirements

### Requirement: REQ-HYG-001: corroborated dead file purge
Corroborated unreferenced files MUST be deleted: `llms.txt`, `llms-full.txt`, the two `review/judgment-day/**` scratch reports, and `docker/nginx-ollama/**`, with prose references updated. Retired root v1 SQL files (`migrations/001-014*.sql`) and `migrations/v2/001_init.sql` are explicitly OUT OF SCOPE (protected non-goals) and MUST remain untouched.

#### Scenario: purge lands (Happy path)
- GIVEN the cleanup wave
- WHEN the deletions are committed
- THEN `git ls-files` lists no tracked path for the purged set and the repo still builds

#### Scenario: prose-only references (Edge case)
- GIVEN documentation mentions a purged file
- WHEN the wave completes
- THEN the mention is removed or reworded without touching protected migration prose

#### Scenario: protected file touched (Error state)
- GIVEN a diff hunk modifies or deletes any protected migration SQL
- WHEN review runs
- THEN the task fails immediately and is reverted (hard non-goal)

### Requirement: REQ-HYG-002: untracked artifact guidance
`.gitignore` MUST carry explicit guidance entries for recurring untracked workstations artifacts (local binaries/caches such as `/cortex.exe~`, `.codex/`, `web/.next`, tsbuildinfo) leveraging the existing dotfile rule where already covered, documented by comments rather than redundant broad patterns.

#### Scenario: new clone is clean (Happy path)
- GIVEN a developer builds locally on Windows
- WHEN `git status` runs
- THEN no routine build artifact appears as untracked noise

#### Scenario: negation interplay (Edge case)
- GIVEN an intentional tracked dotfile config (e.g. `web/.eslintrc.json`)
- WHEN ignore rules evaluate
- THEN an explicit negation keeps it trackable despite the global dotfile ignore

#### Scenario: over-broad ignore (Error state)
- GIVEN a new ignore pattern would hide a needed source or config file
- WHEN `git check-ignore` is exercised in review
- THEN the pattern is corrected before merge
