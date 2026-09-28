# Delta for quality-gates

## ADDED Requirements

### Requirement: REQ-QA-001: Go global coverage raised to 90 percent
The CI Global Coverage gate MUST raise its enforced threshold from 70.0% to 90.0% total line coverage under `go test -tags postgres_integration -covermode=atomic -coverpkg=./...`, with test batches targeting the lightest packages (`internal/ollama`, `internal/update`, `internal/transportpolicy`, `internal/project`) and the new `internal/web`/`internal/webkey` packages.

#### Scenario: gate passes at target (Happy path)
- GIVEN all planned test batches merged
- WHEN the CI coverage job computes total
- THEN total coverage is at least 90.0% and the gate passes

#### Scenario: modular test sizing (Edge case)
- GIVEN new test suites are added for coverage
- WHEN files are allocated
- THEN each modular test file stays <= 250 LOC and no suite is appended to an existing test file over 300 LOC

#### Scenario: threshold regression (Error state)
- GIVEN a merge drops total coverage below 90.0%
- WHEN the gate runs
- THEN CI fails with the computed percentage and the PR cannot land

### Requirement: REQ-QA-002: web coverage gate preserved
The web Vitest V8 coverage gate (`npm run test:coverage`) MUST remain enforced in CI across the export migration and UX additions, with new web modules (endpoint injection, toast, web-key helpers) covered.

#### Scenario: suites green after export migration (Happy path)
- GIVEN the static-export change set
- WHEN `cd web && npm test` runs
- THEN all thirteen pre-existing suites plus new suites pass

#### Scenario: new module coverage (Edge case)
- GIVEN toast and web-key client helpers
- WHEN coverage is computed
- THEN their branches (fallback, normalize, reject) are exercised

#### Scenario: suite deleted to pass (Error state)
- GIVEN a PR removes or skips an existing Vitest suite
- WHEN CI runs
- THEN the coverage gate detects the regression and fails review expectations

### Requirement: REQ-QA-003: Playwright end-to-end adoption
A Playwright harness MUST live in `tools/e2e-web/` (own package.json, Node 24) covering first-boot key entry, dashboard load, memory/search/graph smoke, and settings, executed by a dedicated CI job that boots the embedded server and uses headless chromium.

#### Scenario: first-boot journey (Happy path)
- GIVEN a fresh server boot with a console-minted key
- WHEN the spec enters the key and opens the dashboard
- THEN data renders and the journey completes without manual steps

#### Scenario: graph render (Edge case)
- GIVEN seeded observations
- WHEN the graph smoke spec loads `/graph`
- THEN canvas mount is asserted without pixel-level flakiness (deterministic waits)

#### Scenario: browser missing (Error state)
- GIVEN chromium is not installed in an environment
- WHEN `npx playwright test` runs there
- THEN it fails fast with the installation hint and CI reports the job red (never silently skipped)
