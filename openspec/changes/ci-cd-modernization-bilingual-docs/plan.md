# Plan: CI/CD Modernization + Bilingual Docs (cortex)

## Intent

Modernize the repository's CI/CD posture and stand up a bilingual documentation site, in one dependency-safe DAG on board `cortex-ci-cd-docs`. Three evidence-backed gap clusters from the completed investigation:

1. **Supply-chain & CI hardening**: no CodeQL, no govulncheck in CI, no dependency-review, no SBOM/attestation/signing, no dependabot, actions major-tag pinned only, coverage uploaded as artifact only (no codecov).
2. **Release correctness**: release.yml auto-bumps a patch tag on every push to main (docs-only merges create releases) and duplicates ~12 CI jobs (~490 LOC); CHANGELOG.md only has 'Unreleased' (12 undocumented tags); missing SECURITY.md/CODEOWNERS/PR+issue templates; git-tracked root binaries (after_server.cov, baseline_server.cov, baseline_server, baseline_tui, app_prof, cov_sandbox, setup_cover_baseline, stale).
3. **Bilingual docs**: 22 English doc pages + README.md is Spanish-only; no docs site generator. Chosen stack: MkDocs Material + mkdocs-static-i18n suffix mode (en default, adjacent .es.md files, EN fallback), Python toolchain isolated from Go/Node builds. Caveat honored: Material blog plugin is incompatible with static-i18n and is not needed.

Hard constraints (apply to every task): zero Go product-code changes (workflows/docs/config only); workload policy flexible (no Go LOC thresholds apply; verification is yaml/tooling exit codes); existing CI gates (coverage >=80% CI, golangci-lint v2.11.4, race, cross-OS) must not be weakened — the CI >=80% vs release 70% coverage discrepancy is reconciled upward/documentationally, never downward; gates that cannot run locally are BLOCKED, never silently skipped; boards cortex-rag-perf and cortex-honest-gains are NOT reused.

Non-goals (explicitly out of scope):
1. No Go product code, MCP surface, retrieval, store, or migration changes.
2. No CI gate is removed or lowered (coverage >=80% in CI stays; release-side 70% is aligned up to the CI policy, not down).
3. No blog plugin for MkDocs Material (incompatible with static-i18n; not required).
4. No JS-based docs generator migration; MkDocs Material + static-i18n is final.
5. No new derivative boards; all decomposition happens in place on `cortex-ci-cd-docs`.
6. No destructive git operations beyond untracking the enumerated root binaries via git rm --cached.

## Requirements

### Requirement: REQ-CI-001 Supply-chain hardening in CI
Requirements: REQ-CI-001

The CI/CD workflows MUST SHA-pin all GitHub Actions (ratchet pattern), run CodeQL static analysis for Go on PRs and a weekly cron, run govulncheck in ci.yml, add dependabot covering gomod, web npm, plugin/opencode npm, docker, and github-actions ecosystems, and gate PRs with dependency-review.

#### Scenario: pull request dependency review (Happy path)
- GIVEN a PR modifies go.mod, package-lock.json, or a Dockerfile
- WHEN dependency-review.yml runs on the PR
- THEN the dependency-review-action reports added vulnerabilities and license issues and fails on configured severity thresholds

#### Scenario: weekly CodeQL scan (Edge case)
- GIVEN codeql.yml declares init plus analyze for the go language on push/PR and a weekly cron schedule
- WHEN the cron tick fires
- THEN analysis results appear in the Security tab without touching release.yml

#### Scenario: unpinned action introduced (Error state)
- GIVEN a workflow edit introduces an action referenced by mutable major tag instead of full commit SHA
- WHEN review runs against the ratchet policy
- THEN the unpinned reference is flagged and corrected before merge

### Requirement: REQ-CI-002 Release correctness and coverage reporting
Requirements: REQ-CI-002

release.yml MUST trigger only on v* tags and workflow_dispatch; the automatic patch-bump on every push to main MUST be removed; release jobs MUST reuse CI via workflow_call instead of duplicating ~490 LOC; ci.yml MUST upload coverage to codecov; CHANGELOG MUST be automated (release-please recommended) and backfilled for the 12 undocumented tags; .goreleaser.yaml MUST add syft SBOMs and Docker buildx provenance.

#### Scenario: docs-only merge creates no release (Happy path)
- GIVEN a docs-only PR is merged to main
- WHEN CI completes on the main push
- THEN no new tag, no release, and no GoReleaser run occur

#### Scenario: tag push drives release (Edge case)
- GIVEN a maintainer pushes tag v0.x.y or triggers workflow_dispatch
- WHEN release.yml runs
- THEN it reuses the CI workflow via workflow_call for gates and proceeds to GoReleaser publish with checksums, brew tap, and ghcr images

#### Scenario: coverage uploaded to codecov (Error state)
- GIVEN ci.yml produces coverage.out with total coverage >= 80 percent
- WHEN the CI coverage job finishes
- THEN codecov-action v5 uploads the report and CI does not lower the 80 percent threshold

### Requirement: REQ-REPO-001 Repository hygiene and community files
Requirements: REQ-REPO-001

The repository MUST include .github/SECURITY.md, .github/CODEOWNERS, .github/PULL_REQUEST_TEMPLATE.md, and .github/ISSUE_TEMPLATE (bug plus feature); AGENTS.md branch story MUST match actual workflow branches (master->main refs fixed); the enumerated root binaries MUST be untracked (git rm --cached) and .gitignore extended.

#### Scenario: community files present (Happy path)
- GIVEN a contributor opens the repository page
- WHEN they view Security policy, code owners, PR form, or issue forms
- THEN SECURITY.md, CODEOWNERS, PULL_REQUEST_TEMPLATE.md, and ISSUE_TEMPLATE/bug_report.md and feature_request.md exist and are non-empty

#### Scenario: stray binaries untracked (Edge case)
- GIVEN after_server.cov, baseline_server.cov, baseline_server, baseline_tui, app_prof, cov_sandbox, setup_cover_baseline, and stale are currently git-tracked
- WHEN the hygiene task completes
- THEN git ls-files reports none of them and .gitignore patterns prevent re-adding

#### Scenario: branch story consistent (Error state)
- GIVEN AGENTS.md documents PR targets as master in places while CI targets main and release runs on master
- WHEN the hygiene task completes
- THEN the branch narrative in AGENTS.md matches the actual workflow trigger branches with no stale master->main contradictions

### Requirement: REQ-DOCS-001 Bilingual MkDocs Material site with i18n
Requirements: REQ-DOCS-001

The repository MUST build a MkDocs Material site strictly (mkdocs build --strict) over the 22 existing docs pages with dark mode and full nav; mkdocs-static-i18n suffix mode MUST use default_language en with EN fallback for untranslated pages; a language selector MUST be present; the 5 core pages (INSTALLATION, ARCHITECTURE, CONFIGURATION, MCP, AGENT-SETUP) MUST be translated to Spanish first; docs/TRANSLATING.md MUST document the fallback policy and workflow; the remaining ~17 pages MUST be swept to .es.md; README.md MUST become the EN master with README.es.md carrying the current Spanish content plus real badges and the Pages link; scripts/check-doc-i18n.sh MUST list pages lacking .es.md as a non-blocking CI step (exit 0).

#### Scenario: strict build over existing pages (Happy path)
- GIVEN mkdocs.yml with Material theme, dark mode, and nav covering all 22 docs pages
- WHEN mkdocs build --strict runs
- THEN the build exits 0 with zero broken nav links or missing pages

#### Scenario: untranslated page falls back to EN (Edge case)
- GIVEN a docs page has no <name>.es.md sibling
- WHEN the ES locale is browsed
- THEN the EN content is served via static-i18n fallback with no build error

#### Scenario: core pages translated (Error state)
- GIVEN INSTALLATION, ARCHITECTURE, CONFIGURATION, MCP, and AGENT-SETUP have .es.md siblings
- WHEN the language selector is switched to Spanish
- THEN the five core pages render Spanish content and TRANSLATING.md documents the fallback policy

### Requirement: REQ-DOCS-002 GitHub Pages deployment workflow
Requirements: REQ-DOCS-002

.github/workflows/docs.yml MUST build the site in a Dockerized mkdocs-material environment, deploy the artifact to GitHub Pages, filter triggers to docs/** plus mkdocs.yml, guard with a pages concurrency group, and publish a preview artifact for PRs.

#### Scenario: docs change deploys site (Happy path)
- GIVEN a merged PR touching docs/** or mkdocs.yml
- WHEN docs.yml runs
- THEN the Dockerized mkdocs-material build succeeds and deploy-pages publishes the artifact with the pages concurrency guard serializing runs

#### Scenario: unrelated change skips deploy (Edge case)
- GIVEN a PR or push touching only Go code under internal/
- WHEN path filters are evaluated
- THEN docs.yml jobs are skipped entirely

#### Scenario: broken docs build blocks deploy (Error state)
- GIVEN a docs change introduces a broken markdown link or invalid mkdocs.yml key
- WHEN the Dockerized mkdocs build runs with strict mode
- THEN the workflow fails closed and no artifact is deployed to Pages

## Design

- **Supply chain**: SHA-pin every action reference in ci.yml, release.yml, and new workflows; add codeql.yml (codeql-action init+analyze, go, PR + weekly cron), dependency-review.yml (dependency-review-action@v4 on PR), dependabot.yml (gomod; npm web/; npm plugin/opencode; docker; github-actions), and a govulncheck job (govulncheck-action@v1) in ci.yml.
- **Release**: release.yml on push tags v* + workflow_dispatch only; jobs call ci.yml via workflow_call; codecov-action@v5 upload in ci.yml; release-please-action@v4 for CHANGELOG automation (PR flow already enforces type:* + Closes #N) with a backfilled CHANGELOG.md; .goreleaser.yaml gains syft SBOMs and buildx provenance; T10 adds keyless cosign signing (id-token: write) and SBOM publication to the release page.
- **Docs stack**: root mkdocs.yml (Material, dark mode, nav for 22 pages), docs/index.md landing, Makefile docs-build target, docs/requirements.txt; mkdocs-static-i18n suffix mode, default_language en, language selector; translations as adjacent <name>.es.md files; docs/TRANSLATING.md policy; scripts/check-doc-i18n.sh non-blocking gate wired as one ci.yml step.
- **README**: README.md becomes EN master; current Spanish content moves verbatim to README.es.md; real badges + Pages link added.
- **File-conflict discipline**: ci.yml is written by T3 then T4 then T7 (serialized by DAG edges); mkdocs.yml by T5 then T6 then T8 reads only; no two in-flight tasks share a writable path.

## Tasks

- [ ] ci-docs-t01 [github] Add community hygiene files and fix AGENTS.md branch story
  Requirements: REQ-REPO-001
  Allowed files target: .github/SECURITY.md; .github/CODEOWNERS; .github/PULL_REQUEST_TEMPLATE.md; .github/ISSUE_TEMPLATE/bug_report.md; .github/ISSUE_TEMPLATE/feature_request.md; AGENTS.md
  Verification: test -f .github/SECURITY.md && test -f .github/CODEOWNERS && test -f .github/PULL_REQUEST_TEMPLATE.md && test -f .github/ISSUE_TEMPLATE/bug_report.md && test -f .github/ISSUE_TEMPLATE/feature_request.md
  Forecast: ~150 lines markdown/config, no Go LOC.

- [ ] ci-docs-t02 [repo] Untrack stray root binaries and extend .gitignore
  Requirements: REQ-REPO-001
  Allowed files target: .gitignore; after_server.cov; baseline_server.cov
  Verification: test -z "$(git ls-files -- '*.cov' baseline_server baseline_tui app_prof cov_sandbox setup_cover_baseline stale)"
  Forecast: git rm --cached bridge pattern on 8 binaries; .gitignore patterns; no Go LOC.

- [ ] ci-docs-t03 [ci] SHA-pin actions and add CodeQL, govulncheck, dependabot, dependency-review
  Requirements: REQ-CI-001
  Allowed files target: .github/workflows/ci.yml; .github/workflows/codeql.yml; .github/dependabot.yml
  Verification: actionlint && python3 -c "import yaml; yaml.safe_load(open('.github/dependabot.yml'))"
  Forecast: ~200 lines yaml; dependency-review.yml optional follow-up if 3-file budget binds.

- [ ] ci-docs-t04 [release] Tag-only release trigger, workflow_call reuse, codecov, changelog automation
  Requirements: REQ-CI-002
  Allowed files target: .github/workflows/release.yml; .github/workflows/ci.yml; .goreleaser.yaml
  Verification: actionlint && goreleaser check
  Forecast: ~250 lines yaml + backfilled CHANGELOG.md entry; depends on T3 for SHA-pinning baseline.

- [ ] ci-docs-t05 [docs] MkDocs Material scaffold with nav, dark mode, and strict build
  Requirements: REQ-DOCS-001
  Allowed files target: mkdocs.yml; docs/index.md; Makefile
  Verification: mkdocs build --strict
  Forecast: ~150 lines yaml/md/Make; Python toolchain isolated in docs/requirements.txt.

- [ ] ci-docs-t06 [docs] mkdocs-static-i18n suffix mode with 5 core Spanish pages and translating guide
  Requirements: REQ-DOCS-001
  Allowed files target: mkdocs.yml; docs/TRANSLATING.md; docs/INSTALLATION.es.md
  Verification: mkdocs build --strict && test -f docs/TRANSLATING.md && test -f docs/ARCHITECTURE.es.md && test -f docs/CONFIGURATION.es.md && test -f docs/MCP.es.md && test -f docs/AGENT-SETUP.es.md
  Forecast: 5 translations (~700 lines md total) + plugin config; depends on T5.

- [ ] ci-docs-t07 [docs] Bilingual README split with real badges and non-blocking i18n check gate
  Requirements: REQ-DOCS-001
  Allowed files target: README.md; README.es.md; scripts/check-doc-i18n.sh
  Verification: bash scripts/check-doc-i18n.sh
  Forecast: ~120 lines md/sh; one ci.yml step added under T4's serialized ci.yml window; depends on T4 and T6.

- [ ] ci-docs-t08 [docs] GitHub Pages deploy workflow with path filter and concurrency guard
  Requirements: REQ-DOCS-002
  Allowed files target: .github/workflows/docs.yml
  Verification: actionlint .github/workflows/docs.yml
  Forecast: ~80 lines yaml; depends on T5 and T6 so the deployed site includes i18n.

- [ ] ci-docs-t09 [docs] Spanish translation sweep over remaining docs pages
  Requirements: REQ-DOCS-001
  Allowed files target: docs/BENCHMARKS.es.md; docs/CLI-REFERENCE.es.md; docs/CLI.es.md
  Verification: bash scripts/check-doc-i18n.sh && mkdocs build --strict
  Forecast: ~17 .es.md files (~2400 lines md); depends on T6 and T7 for the check script.

- [ ] ci-docs-t10 [release] Keyless cosign signing and SBOM publication on releases
  Requirements: REQ-CI-002
  Allowed files target: .goreleaser.yaml; .github/workflows/release.yml
  Verification: goreleaser check && actionlint .github/workflows/release.yml
  Forecast: ~60 lines yaml; depends on T4.

DAG edges: t01 (none); t02 (none); t03 (none); t04<-t03; t05 (none); t06<-t05; t07<-t04,t06; t08<-t05,t06; t09<-t06,t07; t10<-t04.
