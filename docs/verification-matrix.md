# Cortex v2 Verification Matrix

## Overview

This document defines the verification gates, execution commands, prerequisites, pass criteria, source traceability, and environmental status for Cortex v2.

### Verification Authority & Hierarchy

1. **Authoritative CI (`.github/workflows/ci.yml`)**: GitHub Actions workflows running on `ubuntu-latest`, `windows-latest`, and `macos-latest` provide the final and authoritative pass/fail record.
2. **Local Pre-Push Verification**: Local gate sequence enforced prior to push (`golangci-lint run ./...` followed by `go test -v ./...`).
3. **Environment-Gated Checks**: Gates requiring specific infrastructure (PostgreSQL 16 with bootstrap credentials, Docker daemon, or CGO/gcc) that report `BLOCKED` locally when prerequisites are absent.

> [!IMPORTANT]
> **Truthful Reporting & Session Constraints**:
> Per dispatch instructions for this maintenance session (*"no test/build/install, no source/config edits, no commits"*), **no test suites were executed during this documentation session**. All gate statuses below reflect documented repository contracts, pipeline definitions, and toolchain dependencies. Every gate status is documented as `Unexecuted in Session (Authority: CI)` or `ND/pending`. No unrun runtime `PASS` claims are made.

---

## 1. Verification Gates & Execution Matrix

| Verification Gate | Scope & Purpose | Command / Invocation | Prerequisites & Toolchain | Pass Criteria & Invariants | Source Traceability | Gate Classification | Session Status |
|---|---|---|---|---|---|---|---|
| <a id="gate-go-compilation"></a>**Go Compilation**<br>`gate_id: go-compilation` | Compiles all packages in zero-CGO default mode | `go build ./...`<br>*(or `make build`)* | Go 1.26.5 | Clean compilation with exit code 0; produces `bin/cortex`. | [`.github/workflows/ci.yml:35`](../.github/workflows/ci.yml#L35), [`Makefile:60`](../Makefile#L60) | CI Mandatory / Local Default | Unexecuted in Session (Authority: CI) |
| <a id="gate-architecture-boundary"></a>**Architecture Boundary**<br>`gate_id: architecture-boundary` | Enforces zero-CGO and import isolation for local packages | `go test -v -count=1 ./internal/app -run '^TestArchitectureBoundaries$'` | Go 1.26.5 | Zero-CGO; no imports of PostgreSQL, authz/identity, Qdrant/pgvector, or server composition in local packages. | [`internal/app/arch_test.go`](../internal/app/arch_test.go), [`AGENTS.md`](../AGENTS.md) | CI Mandatory / Local Default | Unexecuted in Session (Authority: CI) |
| <a id="gate-go-unit-tests"></a>**Go Unit Tests**<br>`gate_id: go-unit-tests` | Validates core business logic, domain services, and SQLite persistence | `go test -v -count=1 ./...`<br>*(or `make test`)* | Go 1.26.5 | All unit tests pass with exit code 0; no panics or test failures. | [`.github/workflows/ci.yml:53`](../.github/workflows/ci.yml#L53), [`Makefile:71`](../Makefile#L71) | CI Mandatory / Local Default | Unexecuted in Session (Authority: CI) |
| <a id="gate-vector-enabled-tests"></a>**Vector-Enabled Tests**<br>`gate_id: vector-enabled-tests` | Exercises SQLite BLOB cosine-scan vector implementation | `go test -v -count=1 -tags cortex_vectors ./...` | Go 1.26.5; tag `cortex_vectors` | SQLite BLOB vector tests pass without regression; release-equivalent vector path verified. | [`.github/workflows/ci.yml:76`](../.github/workflows/ci.yml#L76), [`internal/vector/sqlite_blob/adapter.go`](../internal/vector/sqlite_blob/adapter.go) | CI Mandatory / Local Tagged | Unexecuted in Session (Authority: CI) |
| <a id="gate-schema-ledger-migration"></a>**Schema & Ledger Migration**<br>`gate_id: schema-ledger-migration` | Validates SQLite baseline checksums and PostgreSQL server ledger | `go test -v -count=1 ./internal/migration` | Go 1.26.5 | SQLite baseline checksum matches `cortex_meta`; Postgres migrations 100–105 immutable checksums verified; preflights pass. | [`internal/migration/v2_test.go`](../internal/migration/v2_test.go), [`internal/migration/postgres_test.go`](../internal/migration/postgres_test.go) | CI Mandatory / Local Default | Unexecuted in Session (Authority: CI) |
| <a id="gate-obsidian-path-portability"></a>**Obsidian Path Portability**<br>`gate_id: obsidian-path-portability` | Validates filesystem safety across operating systems | `go test -v -count=1 ./internal/projection/obsidian -run 'Test(SafeSlug\|WindowsDeviceNameNearMisses\|CanonicalPathKey\|ExportCanonicalCollision\|ExportRejectsCaseInsensitiveCollision)'` | Go 1.26.5; runs on `windows-latest` and `macos-latest` runners | Rejects Windows device names (`CON`, `PRN`, `AUX`, `NUL`, `COM1-9`, `LPT1-9`); detects case-insensitive collisions. | [`.github/workflows/ci.yml:145`](../.github/workflows/ci.yml#L145), [`internal/projection/obsidian/writer_test.go`](../internal/projection/obsidian/writer_test.go) | CI Matrix (Windows/macOS) | Unexecuted in Session (Authority: CI) |
| <a id="gate-offline-retrieval-baseline"></a>**Offline Retrieval Baseline**<br>`gate_id: offline-retrieval-baseline` | Validates deterministic retrieval math and isolation contracts | `go test -v -count=1 ./bench ./bench/common ./bench/cortex ./bench/fixtures/cortex-native ./bench/cortex/cmd/baseline`<br>*(or `make test-baseline`)* | Go 1.26.5; zero external network, embeddings, or dataset downloads | Verifies `cortex.retrieval-corpus/v1` and `retrieval-evidence-report/v1`; zero isolation violations; exact filter match. | [`.github/workflows/ci.yml:228`](../.github/workflows/ci.yml#L228), [`Makefile:90`](../Makefile#L90), [`docs/BENCHMARKS.md`](BENCHMARKS.md) | CI Mandatory / Offline Gate | Unexecuted in Session (Authority: CI) |
| <a id="gate-static-analysis-lint"></a>**Static Analysis (Lint)**<br>`gate_id: static-analysis-lint` | Checks code style, bug patterns, and conventions | `golangci-lint run ./...`<br>*(or `make lint`)* | `golangci-lint` pinned to v2.11.4 in CI | Zero linter issues or warnings reported. | [`.github/workflows/ci.yml:295`](../.github/workflows/ci.yml#L295), [`Makefile:119`](../Makefile#L119) | CI Mandatory / Pre-Push Gate | Unexecuted in Session (Authority: CI) |
| <a id="gate-race-detector"></a>**Race Detector**<br>`gate_id: race-detector` | Detects data races in concurrent stores and MCP runtime | `go test -race -count=1 ./internal/store/search ./internal/store/bundle ./internal/mcp` | Go 1.26.5, CGO enabled with working `gcc` | Zero data races reported under concurrent execution. | [`.github/workflows/ci.yml:312`](../.github/workflows/ci.yml#L312), [`AGENTS.md`](../AGENTS.md) | CI Mandatory / Environment-Gated | Unexecuted in Session (Authority: CI) |
| <a id="gate-postgresql-integration"></a>**PostgreSQL Integration**<br>`gate_id: postgresql-integration` | Tests server runtime, migrations 100–109, and multi-tenant RLS | `go test -v -count=1 -tags "integration postgres_integration" ./...`<br>*(or `make test-integration`)* | PostgreSQL 16+, `bootstrap-authz.sql` applied, 3 distinct DSNs configured | All database operations pass under tenant context; RLS prevents cross-tenant access. Fails if DSNs missing. | [`.github/workflows/ci.yml:271`](../.github/workflows/ci.yml#L271), [`Makefile:82`](../Makefile#L82), [`AGENTS.md`](../AGENTS.md) | CI Mandatory / Environment-Gated | Unexecuted in Session (Authority: CI) |
| <a id="gate-whole-project-go-coverage"></a>**Whole-Project Go Coverage**<br>`gate_id: whole-project-go-coverage` | Enforces minimum code coverage threshold for Go codebase | `go test -tags postgres_integration -covermode=atomic -coverpkg=./... -coverprofile=coverage.out ./...`<br>*(or `make test-postgres-coverage`)* | PostgreSQL 16+, Go 1.26.5 | Whole-project atomic coverage is at least **70.0%**; fails build if `< 70.0%`. | [`.github/workflows/ci.yml:188`](../.github/workflows/ci.yml#L188), [`docs/COVERAGE.md:11`](COVERAGE.md#L11) | CI Mandatory / Environment-Gated | Unexecuted in Session (Authority: CI) |
| <a id="gate-web-client-core-v8-coverage"></a>**Web Client-Core V8 Coverage**<br>`gate_id: web-client-core-v8-coverage` | Verifies pure TypeScript client logic in Web control room | `npm --prefix web run test:coverage` | Node >= 24, npm | Coverage for `web/src/lib/**/*.ts`: statements $\ge 70\%$, lines $\ge 70\%$, branches $\ge 60\%$, functions $\ge 55\%$. | [`.github/workflows/ci.yml:107`](../.github/workflows/ci.yml#L107), [`docs/COVERAGE.md:12`](COVERAGE.md#L12) | CI Mandatory / Local Default | Unexecuted in Session (Authority: CI) |
| <a id="gate-web-production-build"></a>**Web Production Build**<br>`gate_id: web-production-build` | Compiles Next.js frontend application | `npm --prefix web run build` | Node >= 24, npm | Clean Next.js production build without TypeScript or bundling errors. | [`.github/workflows/ci.yml:117`](../.github/workflows/ci.yml#L117) | CI Mandatory / Local Default | Unexecuted in Session (Authority: CI) |
| <a id="gate-opencode-plugin-contract"></a>**OpenCode Plugin Contract**<br>`gate_id: opencode-plugin-contract` | Tests OpenCode TypeScript plugin event hooks and logic | `npm --prefix plugin/opencode ci && npm --prefix plugin/opencode test` | Node >= 24, npm, Vitest 4.1.10 | All Vitest contract tests pass; verifies session lifecycle, subagent suppression, and prompt capture. | [`.github/workflows/ci.yml:335`](../.github/workflows/ci.yml#L335), [`plugin/opencode`](../plugin/opencode) | CI Mandatory / Local Default | Unexecuted in Session (Authority: CI) |
| <a id="gate-claude-code-plugin-harness"></a>**Claude Code Plugin Harness**<br>`gate_id: claude-code-plugin-harness` | Tests Claude Code lifecycle hooks using deterministic stubs | `bash plugin/claude-code/scripts/hooks_test.sh` | bash, jq, python3, coreutils timeout | Contract harness verifies 5 lifecycle hooks; exits 127 if any required harness tool is missing. | [`.github/workflows/ci.yml:359`](../.github/workflows/ci.yml#L359), [`docs/PLUGINS.md:114`](PLUGINS.md#L114) | CI Mandatory / Environment-Gated | Unexecuted in Session (Authority: CI) |
| <a id="gate-docker-compose-smoke-test"></a>**Docker Compose Smoke Test**<br>`gate_id: docker-compose-smoke-test` | Validates containerized stack orchestration and health | `docker compose up --build -d`<br>`curl -fsS http://localhost:7438/health`<br>`curl -fsS http://localhost:3000/health` | Docker daemon, Docker Compose | Server, Postgres, and UI containers become healthy within 3 minutes; `/health` returns 200 OK. | [`.github/workflows/ci.yml:368`](../.github/workflows/ci.yml#L368) | CI Mandatory / Environment-Gated | Unexecuted in Session (Authority: CI) |
| <a id="gate-docker-compose-e2e-boundary"></a>**Docker Compose E2E Boundary**<br>`gate_id: docker-compose-e2e-boundary` | Tests multi-tenant persistence and search over Docker stack | `go test -v -count=1 -tags docker_e2e ./e2e`<br>*(or `make test-e2e-docker`)* | Running Docker stack, Go 1.26.5 | End-to-end multi-tenant isolation, search, and restart persistence validated. | [`.github/workflows/ci.yml:280`](../.github/workflows/ci.yml#L280), [`docs/COVERAGE.md:13`](COVERAGE.md#L13) | CI Mandatory / Environment-Gated | Unexecuted in Session (Authority: CI) |
| <a id="gate-qdrant-vector-integration"></a>**Qdrant Vector Integration**<br>`gate_id: qdrant-vector-integration` | Validates external Qdrant adapter operations | `go test -v -count=1 -tags qdrant_integration ./internal/vector/qdrant` | Running Qdrant service, Go 1.26.5 | Qdrant vector indexing and search pass within tenant cell boundary. | [`internal/vector/qdrant`](../internal/vector/qdrant), [`AGENTS.md`](../AGENTS.md) | Opt-In / Environment-Gated | Unexecuted in Session (Authority: CI) |
| <a id="gate-pgvector-integration"></a>**pgvector Integration**<br>`gate_id: pgvector-integration` | Validates external PostgreSQL pgvector adapter | `go test -v -count=1 -tags pgvector_integration ./internal/vector/pgvector` | PostgreSQL with `vector` extension, Go 1.26.5 | pgvector indexing and cosine distance queries pass within tenant cell boundary. | [`internal/vector/pgvector`](../internal/vector/pgvector), [`AGENTS.md`](../AGENTS.md) | Opt-In / Environment-Gated | Unexecuted in Session (Authority: CI) |
| <a id="gate-build-identity-provenance"></a>**Build Identity & Provenance Gate**<br>`gate_id: build-identity-provenance` | Validates statistical binary identity and publication bindings | `go test -v -count=1 ./bench/vectorhydration -run '^Test(IdentityValidation\|PublicationBinding)'` | Go 1.26.5 | Enforces `binary-identity/v1`, `ApprovedBuildIdentity = "go-test-c-trimpath-v1"`, and SHA-256 binary/tree digest sealing. | [`bench/vectorhydration/provenance_test.go`](../bench/vectorhydration/provenance_test.go) | CI Mandatory / Offline Gate | Unexecuted in Session (Authority: CI) |
| <a id="gate-privacy-domain-component"></a>**Privacy Domain & Component Gate**<br>`gate_id: privacy-domain-component` | Validates private marker parsing, redaction, and AST non-interference | `go test -v -count=1 ./internal/domain/privacy` | Go 1.26.5 | Rejects malformed markers and empty residual content; replaces `<private>...</private>` with `[REDACTED]`; AST is never mutated (`REQ-PRIV-007`). | [`internal/domain/privacy/privacy_test.go`](../internal/domain/privacy/privacy_test.go), [`internal/domain/privacy/privacy.go`](../internal/domain/privacy/privacy.go) | CI Mandatory / Local Default | Unexecuted in Session (Authority: CI) |
| <a id="gate-log-ledger-retention"></a>**Log & Ledger Retention Gate**<br>`gate_id: log-ledger-retention` | Enforces immutable ledger history and non-destructive soft deletes | `go test -v -count=1 ./internal/domain/projectprotocol -run 'TestProjectProtocolStoreDefinesNoDestructivePort'` | Go 1.26.5 | Store defines NO hard-delete/purge methods; migration ledgers enforce indefinite retention (`REQ-RET-001`). | [`internal/domain/projectprotocol/ports_test.go`](../internal/domain/projectprotocol/ports_test.go), [`internal/domain/projectprotocol/doc.go`](../internal/domain/projectprotocol/doc.go) | CI Mandatory / Local Default | Unexecuted in Session (Authority: CI) |
| <a id="gate-synthetic-fixture-safety"></a>**Synthetic Fixture Safety Gate**<br>`gate_id: synthetic-fixture-safety` | Verifies synthetic corpus identity, zero-PII guarantee, and isolation | `go test -v -count=1 ./bench/common -run '^TestCorpus'` | Go 1.26.5 | Corpus specifies `cortex-authored-synthetic` origin; privacy review confirms zero real prompts/vendor rows/PII. | [`bench/common/corpus_test.go`](../bench/common/corpus_test.go), [`bench/evidence/cortex-native/v1/corpus.json`](../bench/evidence/cortex-native/v1/corpus.json) | CI Mandatory / Offline Gate | Unexecuted in Session (Authority: CI) |
| <a id="gate-retrieval-evidence-preregistration"></a>**Retrieval Evidence & Preregistration Gate**<br>`gate_id: retrieval-evidence-preregistration` | Validates release gate registration invariants and zero isolation leaks | `go test -v -count=1 ./bench/common -run '^TestGateRegistry'` | Go 1.26.5 | Zero isolation violations; gates preregistered before candidate results; held-out splits never reused. | [`bench/common/gates_test.go`](../bench/common/gates_test.go), [`docs/BENCHMARKS.md`](BENCHMARKS.md) | CI Mandatory / Offline Gate | Unexecuted in Session (Authority: CI) |
| <a id="gate-test-run-record-evidence"></a>**Test-Run Record & Evidence Gate**<br>`gate_id: test-run-record-evidence` | Enforces auditable test-execution run records, schema conformance, source/binary provenance, and NOT_RUN placeholder integrity | `git diff --check`<br>*(schema & record validation)* | Toolchain agnostic / Test-Run Schema v1.0 | Valid test-run record schema (`schema_version: "test-run-record/v1"`); explicit source inputs, binary identity, run metadata, artifacts, retention, test counts, and no-match detection; unexecuted gates strictly recorded as `NOT_RUN` with null unknowns; zero fake PASS; zero secret leakage; clean diff check. | [`bench/vectorhydration/provenance.go`](../bench/vectorhydration/provenance.go), [`docs/verification-matrix.md`](verification-matrix.md#4-product-test-execution-run-record-specification) | Quality Mandatory / Local Gate | Unexecuted in Session (Authority: CI) |

---

## 2. Environmental Prerequisites & Non-Skipping Policy

### Mandatory CI vs Local Workstation Availability

| Prerequisite | Canonical CI Requirement | Workstation Status | Missing Action Policy |
|---|---|---|---|
| **Go Toolchain** | `go1.26.5 linux/amd64` | `go1.26.5 windows/amd64` | Fail build if toolchain mismatch. |
| **golangci-lint** | `v2.11.4` (pinned in CI) | Available (`v2.12.2`) | Do not claim gate equivalence when local versions diverge; CI is authoritative. |
| **Node.js & npm** | `node v24`, `npm` with locked files | `v24.20.0`, `npm 11.19.0` | Local execution allowed for `web` and `plugin/opencode`. |
| **PostgreSQL 16** | Service container in CI with RLS bootstrap | Not present locally | Report `BLOCKED`; do not silently skip PostgreSQL integration suites. |
| **PostgreSQL DSNs** | 3 distinct DSNs: `CORTEX_TEST_POSTGRES_DSN`, `CORTEX_TEST_POSTGRES_MIGRATION_DSN`, `CORTEX_TEST_POSTGRES_AUTHZ_ADMIN_DSN` | Not configured | Missing DSNs fail explicitly rather than skip (`AGENTS.md`). |
| **CGO / GCC** | `gcc` installed on Linux runners | Not found | Race detector cannot run locally; gate is reported `BLOCKED` locally and enforced in CI. |
| **Bash, jq, Python3** | Preinstalled on Linux runners | Partial / unconfirmed | Claude Code hook harness exits 127 when any tool is missing; reported `BLOCKED` locally. |
| **Docker Daemon** | Available in CI runners | Available | Required for `docker compose` smoke tests and E2E boundary suites. |

### Diagnostic & Non-Skipping Invariants

1. **No Silent Skipping**: Tests depending on external fixtures or services MUST fail or report `BLOCKED` when prerequisites are unavailable. Silently passing when an engine is missing is strictly prohibited.
2. **Credential Sanitization**: Verification commands must NEVER log credentials, DSN connection strings with passwords, authorization tokens, or encryption keys.
3. **Deterministic Baselines**: Retrieval baseline contracts (`./bench/...`) are fully offline and deterministic; they execute without external models, network connections, or dataset downloads.

---

## 3. Build Identity, Retention, Privacy & Evidence Invariants

### 3.1 Build Identity & Reproducibility (`build-identity`)
- **Schema & Pinned Flags**: Pinned by `BinaryIdentity` (`binary-identity/v1`) in [`bench/vectorhydration/provenance.go`](../bench/vectorhydration/provenance.go). Approved build configuration is `ApprovedBuildIdentity = "go-test-c-trimpath-v1"`.
- **Digest Sealing**: Binary SHA-256, source commit SHA-1, tree SHA-256, tool SHA-256, and argv SHA-256 must be valid lowercase non-zero hex strings.
- **Publication Binding**: Binds binary identity to protocol identity; unmarshaling enforces `DisallowUnknownFields` and rejects duplicate JSON keys.

### 3.2 Log & Ledger Retention Policy (`log-retention`)
- **Indefinite Retention (`REQ-RET-001`)**: Ledgered records (skill and rule revisions, activations, audit events) are immutable and retained indefinitely in SQLite (`003_project_artifacts.sql`) and PostgreSQL (`106_project_artifacts.sql`).
- **Non-Destructive Deletion**: Deletion is exclusively a soft-delete state transition. No hard-delete, cascade, or purge SQL statements are exposed on artifact tables.
- **Zero-Secret Logging**: In accordance with [`internal/config/config.go`](../internal/config/config.go) and [`docs/ARCHITECTURE.md`](ARCHITECTURE.md), DSN passwords, bearer tokens, API secrets, and raw `<private>` tag contents are scrubbed before stdout or log emission.

### 3.3 Privacy Matrix & Component Enforcement (`privacy`)
- **Marker Syntax**: Exact or case-insensitive `<private>...</private>` tags parsed by [`internal/domain/privacy/privacy.go`](../internal/domain/privacy/privacy.go).
- **Public Residual Requirement**: If redaction leaves public residual text empty, operations fail closed with `ErrCodeRequiredEmpty` (`privacy_required_empty`).
- **Sanitized Errors**: Error structs never leak the private payload; they only convey error code, position, and marker count.
- **AST Non-Interference (`REQ-PRIV-007`)**: AST structures, doc summaries, relationships, and reasoning are structural codebase components and must NEVER be mutated or redacted by privacy routines.

### 3.4 Safe & Synthetic Fixtures (`synthetic-fixture` / `safe-fixture`)
- **Synthetic Integrity**: Governed by [`bench/evidence/cortex-native/v1/corpus.json`](../bench/evidence/cortex-native/v1/corpus.json) and validated by [`bench/common/corpus_test.go`](../bench/common/corpus_test.go).
- **Zero-Leakage Contract**: `privacy_review` asserts `synthetic-only-no-real-prompts-vendor-rows-private-data-or-secrets`. All fixtures contain synthetic data only; no real prompts, no vendor proprietary rows, no credentials.
- **Offline Determinism**: Fixtures validate cross-project isolation collisions, temporal filters, and ranking decoys without external network or LLM dependencies.

### 3.5 Evidence Limits & Gate Bounds (`evidence-limit`)
- **Universal Release Blockers**: Any cross-tenant or cross-project isolation leakage (non-zero isolation violations) blocks release immediately.
- **Ground Truth Grounding**: Relevant episode and fact stable-IDs are required for retrieval evaluation; answer-level metrics (F1, ROUGE-L) cannot substitute for stable-ID relevance.
- **Preregistration Discipline**: Release gates, sample sizes, metrics, and variance thresholds must be registered before candidate evaluation runs; held-out test splits cannot be reused for calibration.

### 3.6 Product Test-Execution Run Records & Audit Invariants (`test-run-record`)
- **Canonical Schema (`schema_version: "test-run-record/v1"`)**: Verification runs for product gates generate structured, auditable test-execution run records. Every record binds the matrix row link, gate ID, source inputs, binary provenance, run metadata, execution metrics, test counts, no-match status, generated artifacts, and retention policy.
- **Truthful Status & NOT_RUN Discipline**: Unexecuted gates must be reported as `status: "NOT_RUN"`. Every runtime-dependent unknown (exit code, timestamps, duration, test counts, no-match flag, binary digest, log artifacts) must be explicitly `null`. Synthesizing fake `PASS` verdicts or fabricating execution history for unrun suites is strictly prohibited.
- **Source Inputs & Binary Sealing**: Executed runs bind the exact source commit SHA, source tree SHA-256, tool version (`go1.26.5`), and build identity (`ApprovedBuildIdentity = "go-test-c-trimpath-v1"` per [`bench/vectorhydration/provenance.go`](../bench/vectorhydration/provenance.go)).
- **Test Counts & No-Match Detection**: Run records explicitly track discrete test counts (`total`, `passed`, `failed`, `skipped`, `blocked`). The `no_match` boolean explicitly indicates whether test filters or package patterns matched zero runnable test targets, preventing silent false-green suite passes.
- **Artifact Sealing & Indefinite Retention**: Output artifacts (test logs, coverage profiles, benchmark evidence reports) are recorded with their relative paths and SHA-256 digests, governed by non-destructive indefinite retention (`REQ-RET-001`).
- **Zero-Secret Scrubbing**: Test-run records and attached logs must never contain PostgreSQL DSN credentials, bearer tokens, API keys, or raw `<private>...</private>` marker payloads.
- **Matrix Row Linkage**: Every test-run record must explicitly link to its parent gate row in [Section 1](#1-verification-gates--execution-matrix).

---

## 4. Product Test-Execution Run Record Specification

### 4.1 Canonical Test-Run Record Contract (`test-run-record/v1`)

Each verification gate execution produces or references an immutable test-execution run record complying with the `test-run-record/v1` specification. The schema establishes end-to-end provenance between the gate definition in Section 1 and runtime verification evidence.

#### Top-Level Specification Fields

| Field Name | Type | Allowed Values / Format | Description & Invariants |
|---|---|---|---|
| `schema_version` | string | `"test-run-record/v1"` | Canonical test-run record schema identifier. |
| `gate_id` | string | Gate slug matching [Section 1](#1-verification-gates--execution-matrix) | Unique identifier of the verification gate (e.g. `offline-retrieval-baseline`). |
| `matrix_row_link` | string | Relative markdown anchor link | Explicit link to the corresponding gate row in the verification matrix (e.g. `verification-matrix.md#gate-offline-retrieval-baseline`). |
| `run_id` | string \| null | UUID / timestamped run string or `null` | Unique run identifier assigned when executed; strictly `null` for `NOT_RUN`. |
| `status` | string | `"PASS"`, `"FAIL"`, `"BLOCKED"`, `"NOT_RUN"` | High-level execution status of the gate. |
| `command` | string | Non-empty CLI command string | Exact command line invocation specified by the verification matrix. |
| `execution` | object \| null | Execution metadata object or `null` | Detailed runtime execution metadata; strictly `null` when `status` is `"NOT_RUN"`. |
| `source_inputs` | object | Source input metadata object | Sealing git commit SHA, source tree digest, manifest digest, and input files. |
| `binary_identity` | object \| null | Binary identity object or `null` | Binary and compiler provenance per `bench/vectorhydration/provenance.go`; `null` when `status` is `"NOT_RUN"`. |
| `counts` | object \| null | Test count breakdown object or `null` | Discrete counts of test outcomes; strictly `null` when `status` is `"NOT_RUN"`. |
| `no_match` | boolean \| null | `true`, `false`, or `null` | `true` if test filter matched zero test targets; `false` if matches occurred; `null` for `NOT_RUN`. |
| `artifacts` | array of objects \| null | List of artifact objects or `null` | Paths, artifact types, and SHA-256 digests of generated logs/reports; `null` for `NOT_RUN`. |
| `retention` | object | Retention contract object | Policy governing record durability, immutability, storage location, and secret scrubbing. |
| `notes` | string | Descriptive text | Operational notes, block reason, or justification for unexecuted status. |

#### Detailed Object Definitions

##### 1. `execution` Object (Runtime Execution Metadata)
- `timestamp_start` (string | null): ISO-8601 UTC timestamp when command execution began.
- `timestamp_end` (string | null): ISO-8601 UTC timestamp when command execution ended.
- `duration_ms` (integer | null): Total execution duration in milliseconds.
- `exit_code` (integer | null): Process exit code returned by the command (`0` for success).
- `environment` (object): Execution host metadata containing `os` (e.g. `"linux"`, `"windows"`), `arch` (e.g. `"amd64"`, `"arm64"`), and `runner` (e.g. `"github-actions-ubuntu-latest"`, `"local-workstation"`).

##### 2. `source_inputs` Object (Input Sealing)
- `source_commit` (string): 40-character hexadecimal Git commit SHA or `"HEAD"`.
- `source_tree_sha256` (string | null): 64-character lowercase hexadecimal SHA-256 digest of the source tree.
- `manifest_sha256` (string | null): 64-character SHA-256 digest of input test configuration or manifest.
- `input_files` (array of strings): Explicit relative paths of primary source and fixture files scoped to the gate.

##### 3. `binary_identity` Object (Binary Provenance)
- `binary_sha256` (string | null): 64-character SHA-256 digest of the compiled test or product binary.
- `build_identity` (string): Pinned build identity configuration (`"go-test-c-trimpath-v1"` per [`bench/vectorhydration/provenance.go`](../bench/vectorhydration/provenance.go)).
- `tool_version` (string): Toolchain version string (e.g. `"go1.26.5"`).
- `tool_sha256` (string | null): 64-character SHA-256 digest of the compiler toolchain binary.
- `argv_sha256` (string | null): 64-character SHA-256 digest of the exact argument array passed to the builder.

##### 4. `counts` Object (Test Execution Breakdown)
- `total` (integer | null): Total number of test cases evaluated.
- `passed` (integer | null): Number of test cases that passed.
- `failed` (integer | null): Number of test cases that failed.
- `skipped` (integer | null): Number of test cases intentionally skipped by runner.
- `blocked` (integer | null): Number of test cases blocked by missing prerequisites.

##### 5. `no_match` Boolean Field (Zero-Target Detection)
- `true`: The command filter, regex, or package path matched zero executable test functions (e.g. `[no tests to run]` or `no test files`). Such runs MUST NOT be marked `status: "PASS"` unless the gate definition explicitly permits empty test targets.
- `false`: The filter matched one or more executable test functions.
- `null`: The gate was not executed (`status: "NOT_RUN"`).

##### 6. `artifacts` Array of Objects (Evidence Outputs)
- `path` (string): Relative workspace path to the generated artifact (e.g. `coverage/coverage.out`).
- `artifact_type` (string): Semantic type (`"stdout_log"`, `"coverage_profile"`, `"evidence_report"`).
- `sha256` (string): 64-character lowercase hexadecimal SHA-256 digest sealing artifact contents.

##### 7. `retention` Object (Durability and Storage Policy)
- `policy` (string): Retention lifecycle policy (`"indefinite"` per `REQ-RET-001`).
- `immutable` (boolean): `true` indicating append-only persistence without in-place mutation.
- `storage` (string): Authoritative persistence ledger (`"cortex_meta / migration_ledger"`).
- `secret_scrubbed` (boolean): `true` confirming verification output has undergone automated credential scrubbing.

---

### 4.2 Auditable NOT_RUN Record Requirements

When a verification gate is unexecuted during a maintenance, documentation, or constrained session, its run record must adhere to strict auditability rules:

1. **Explicit NOT_RUN Status**: The status MUST be set to `"NOT_RUN"`. Ambiguous states such as `"PENDING_PASS"`, `"ASSUMED_OK"`, or empty strings are strictly disallowed.
2. **Mandatory Null Unknowns**: All runtime-dependent fields that require actual process execution MUST be explicitly `null`. This includes:
   - `run_id`: `null` (no execution identifier assigned).
   - `execution`: `null` (no start/end timestamps, duration, or exit code).
   - `binary_identity`: `null` (no compiled test binary or runtime toolchain probe).
   - `counts`: `null` (counts must NEVER be populated with `0` or placeholder integers, which falsely implies zero tests ran and passed).
   - `no_match`: `null` (filter matching cannot be evaluated without execution).
   - `artifacts`: `null` (no output logs, profiles, or reports exist).
3. **Prohibition of Fictional PASS Claims & History Reuse**:
   - Unexecuted suites must never claim `status: "PASS"`.
   - Records must not reuse or copy timestamps, hashes, or results from previous commits or past CI jobs to simulate current verification.
   - Code inspection, static type checking, or doc reviews cannot substitute for deterministic test execution.
4. **Preservation of Static Provenance**: Even when unexecuted, the record must capture static context:
   - `schema_version: "test-run-record/v1"`
   - `gate_id` and `matrix_row_link` linking directly to the verification matrix row.
   - `command` stating the exact reproducible invocation.
   - `source_inputs` identifying the active commit and primary scoped files.
   - `retention` confirming governance policy.
   - `notes` documenting why the gate was unexecuted (e.g. session constraints, environment prerequisites).

---

### 4.3 Secret Sanitization & Privacy Discipline

Test-execution records, artifacts, and attached logs are subject to automated credential and privacy scrubbing:

1. **Credential Sanitization**: DSN connection strings with passwords, authentication bearer tokens, API secrets, and private keys must be scrubbed or replaced with `[REDACTED]`.
2. **Private Tag Scrubbing**: Any sensitive payload enclosed in `<private>...</private>` tags parsed per [`internal/domain/privacy/privacy.go`](../internal/domain/privacy/privacy.go) must be replaced with `[REDACTED]` prior to artifact serialization.
3. **AST Non-Interference (`REQ-PRIV-007`)**: AST structures, doc summaries, code relationships, and graph reasoning are structural codebase components and must NEVER be mutated or redacted by privacy routines.

---

### 4.4 Placeholder-Only NOT_RUN Record Example

The following record demonstrates an auditable placeholder-only `NOT_RUN` record for the [Offline Retrieval Baseline](#gate-offline-retrieval-baseline) gate. It complies with all null-unknown requirements, contains no fabricated execution history, and explicitly links to its matrix row:

```json
{
  "schema_version": "test-run-record/v1",
  "gate_id": "offline-retrieval-baseline",
  "matrix_row_link": "verification-matrix.md#gate-offline-retrieval-baseline",
  "run_id": null,
  "status": "NOT_RUN",
  "command": "go test -v -count=1 ./bench ./bench/common ./bench/cortex ./bench/fixtures/cortex-native ./bench/cortex/cmd/baseline",
  "execution": null,
  "source_inputs": {
    "source_commit": "03d74b2dff145c373eb3d6f4c22f0045685c9091",
    "source_tree_sha256": null,
    "manifest_sha256": null,
    "input_files": [
      "bench/evidence/cortex-native/v1/corpus.json",
      "bench/common/report.go",
      "bench/cortex/cmd/baseline/main.go"
    ]
  },
  "binary_identity": null,
  "counts": null,
  "no_match": null,
  "artifacts": null,
  "retention": {
    "policy": "indefinite",
    "immutable": true,
    "storage": "cortex_meta / migration_ledger",
    "secret_scrubbed": true
  },
  "notes": "Gate not executed in current session; session constrained to documentation maintenance without test/build/install execution. Placeholder record with null unknowns and no historical run reuse."
}
```
