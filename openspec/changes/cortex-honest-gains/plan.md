# Plan: cortex-honest-gains Honest Gains Roadmap

## Intent

Convert the honest weaknesses surfaced by the rag-perf audit (W1-W7) into one evidence-first improvement initiative for the cortex Go repository. The prior board cortex-rag-perf (20/20 done) landed HippoRAG 2 end-to-end, SkewRoute routing, MRL/int8 vector scan, the filtered-search cardinality switch, the zero-CGO sqlite-vec port, the fixed-judge eval harness, and Chain-of-Note. Measured today: LOCOMO 0.35 (temporal 0.692 strongest, multi-hop 0.189 weakest), LongMemEval 0.08, deltas 0.000. Because the first-eval baseline postdates every rag-t* change, actual lift is unproven; this initiative makes claims honest before making them better.

Hard constraints (apply to every task): Zero-CGO and no external services in local mode (enforced by internal/app/arch_test.go); workload policy flexible (source <= 700 LOC Go, tests <= 1200 LOC, modular test files <= 250 LOC); the eval no-fabrication contract and pinned migration checksums stay intact; the completed cortex-rag-perf board is never edited; deprecating a retrieval engine is an orchestrator decision gate, never a unilateral removal.

Non-goals (explicitly out of scope):
1. No new retrieval engines and no rewrite of the adaptive tier.
2. No weakening of the eval no-fabrication contract, the pinned migration checksums, or the arch gate.
3. No CI/release workflow changes; packaging work is additive formula plus docs only.
4. Operator-side actions (Ollama model pull, heartbeat/auto-renew, report signing secret) are captured as ops/runbook tasks or documented prerequisites, never as silent code assumptions.
5. No fix implementation for LongMemEval inside this initiative beyond what the diagnosis evidence justifies; fixes are re-proposed after honest-03 lands.

## Requirements

### Requirement: REQ-GAINS-001 Pre-change lift baseline (W1)
Requirements: REQ-GAINS-001

The eval harness SHALL be run against the product state at the pre-initiative commit (expected 3bf0f411 — last main commit before the 2026-10-02 rag-perf wave; MUST be re-verified via git log at execution time before pinning). Both baseline reports SHALL publish under bench/reports/ as eval-comparison/v1 with baseline_status not_recorded and no invented baseline numbers.

#### Scenario: clean worktree run (Happy path)
- GIVEN git log confirms the pre-initiative commit and the worktree at that commit contains the eval harness
- WHEN both LOCOMO and LongMemEval runners execute with the fixed judge (OLLAMA_ENDPOINT=http://127.0.0.1:11434, OLLAMA_JUDGE_MODEL=qwen2.5:7b-instruct)
- THEN bench/reports/prechange-baseline-locomo.json and bench/reports/prechange-baseline-longmemeval.json publish as eval-comparison/v1 with baseline_status not_recorded and judge.enabled true

#### Scenario: harness absent at pinned commit (Edge case)
- GIVEN the committed harness (bench/**) postdates the pre-initiative commit
- WHEN the worktree at the pinned commit lacks the eval harness
- THEN the run reuses the current measurement harness against the pre-change product tree and records the provenance path (commit hash plus harness decision) inside the published report JSON

#### Scenario: baseline numbers missing (Error state)
- GIVEN the runner cannot produce scores for one benchmark at the pre-change state
- WHEN publication executes
- THEN the run fails closed without inventing baseline numbers, and the missing report is documented as BLOCKED evidence rather than a fabricated artifact

### Requirement: REQ-GAINS-002 Embeddings enablement (W2)
Requirements: REQ-GAINS-002

A local Ollama embedding model (e.g. nomic-embed-text or mxbai-embed-large) SHALL be pulled and verified via /api/embed (currently 501), and both evals SHALL re-run embeddings-enabled using the existing EmbeddingCfg runner support (bench/locomo/runner_embed_test.go), with the embedding model recorded in report provenance.

#### Scenario: embeddings-on eval run (Happy path)
- GIVEN a verified Ollama embedding model serves /api/embed
- WHEN both LOCOMO and LongMemEval runners execute with EmbeddingCfg configured
- THEN reports publish as eval-comparison/v1 with the embedding model in provenance and total_questions greater than zero

#### Scenario: embedding model unavailable (Edge case)
- GIVEN the chosen embedding model name is not pulled or /api/embed returns an error
- WHEN the run starts
- THEN the runner fails fast with an actionable embedding error instead of silently scoring lexical-only while claiming vector coverage

#### Scenario: report provenance honesty (Error state)
- GIVEN an eval run completed without a reachable embedding endpoint
- WHEN the report publishes
- THEN the report does not claim embedding-enabled retrieval and the absent vector row is disclosed exactly as in prior honest runs

### Requirement: REQ-GAINS-003 Multi-hop tuning gated on evidence (W3)
Requirements: REQ-GAINS-003

HippoRAG 2 multi-hop tuning (PassageSeed fusion weights, recognition-filter TopK, Chain-of-Note reading stage in executeMultiHopTier) SHALL be gated on REQ-GAINS-001 and REQ-GAINS-002 evidence, regression-chained via Config.Baseline and MinDelta, targeting multi-hop 0.189 to measured improvement with no overall regression.

#### Scenario: tuning improves multi-hop slice (Happy path)
- GIVEN the pre-change baseline and embeddings-on reports exist as comparison anchors
- WHEN a tuning change lands in internal/retrieval/adaptive.go and the eval comparison executes
- THEN the multi-hop slice improves by at least MinDelta and no other slice regresses below its gate

#### Scenario: tuning regresses another slice (Edge case)
- GIVEN a candidate tuning improves multi-hop but drops temporal or single-hop below the gate
- WHEN the comparison executes
- THEN the change is rejected by the MinDelta gate with the offending task delta named, and it does not transition to review

#### Scenario: anchors missing (Error state)
- GIVEN the evidence tasks have not produced the baseline and embeddings reports
- WHEN the tuning task is dispatched
- THEN it remains blocked on its declared dependencies and no tuning change is attempted without anchors

### Requirement: REQ-GAINS-004 LongMemEval root-cause diagnosis (W4)
Requirements: REQ-GAINS-004

LongMemEval 0.08 SHALL receive a root-cause diagnosis decomposing the score into retrieval relevance, judge acceptance, ingestion coverage, and dataset fit, published as a ranked evidence-referenced JSON report before any fix is written. This requirement produces evidence, not fixes.

#### Scenario: diagnosis decomposes the score (Happy path)
- GIVEN the LongMemEval eval reports and runner instrumentation
- WHEN the diagnosis decomposes the 0.08 score per ability and per failure class
- THEN bench/reports/longmemeval-diagnosis.json publishes with at least one root cause, each carrying concrete evidence references and a falsifiable fix hypothesis

#### Scenario: hypothesis is falsifiable (Edge case)
- GIVEN a candidate root cause such as judge prompt over-strictness versus ingestion coverage gaps
- WHEN the diagnosis designs the discriminating measurement
- THEN the report names the exact probe that would confirm or refute the hypothesis with expected observable outcomes

#### Scenario: no fix proposed without evidence (Error state)
- GIVEN the diagnosis cannot isolate a cause for some failure class
- WHEN the report publishes
- THEN that class is recorded as undetermined with the missing instrumentation named, and no speculative fix task is created from it

### Requirement: REQ-GAINS-005 Engine contribution audit with decision gate (W5)
Requirements: REQ-GAINS-005

Every retrieval engine (FTS5 lexical fusion, dense vector, HippoRAG 2 multi-hop, LightRAG community summaries, CRAG gating, ColBERT late interaction plus Chain-of-Note reading) SHALL be measured for marginal contribution ablation-style with the fixed judge and embeddings enabled, producing a keep, tune, deprecate_candidate, or undetermined recommendation per engine. Engine removal SHALL execute only after the orchestrator approves the audit gate decision.

#### Scenario: ablation measures marginal contribution (Happy path)
- GIVEN the embeddings-enabled harness from REQ-GAINS-002
- WHEN each engine is toggled off in wrapper code and the LOCOMO overall plus multi-hop deltas are measured
- THEN bench/reports/engine-ablation-audit.json publishes with at least six engine entries and a recommendation per engine

#### Scenario: engine lacks a toggle (Edge case)
- GIVEN an engine lacks a runtime toggle for ablation
- WHEN the audit cannot isolate its contribution without product edits
- THEN the engine is recorded as undetermined with the concrete missing seam documented and no product code is edited by the audit task

#### Scenario: unapproved removal rejected (Error state)
- GIVEN the audit recommends deprecate_candidate for some engine
- WHEN the removal task executes without orchestrator approval of the gate decision
- THEN no engine wiring is removed; the task reduces to documenting the gate rationale or is blocked pending approval

### Requirement: REQ-GAINS-006 Distribution hardening (W6)
Requirements: REQ-GAINS-006

Distribution artifacts (Homebrew formula, distribution documentation covering plugin/opencode and plugin/claude-code, honest benchmarks page in docs/BENCHMARKS.md) SHALL respect repo conventions (PRs target develop/master; Husky pre-push runs lint plus go test).

#### Scenario: benchmarks page stays honest (Happy path)
- GIVEN measured LOCOMO 0.35, LongMemEval 0.08, deltas 0.000, and the pre-change comparison
- WHEN docs/BENCHMARKS.md is updated
- THEN published numbers match the bench report artifacts and TestRetrievalBaselineDocumentationContract passes

#### Scenario: formula validity (Edge case)
- GIVEN the Homebrew formula builds from the repository with Go 1.26.5 and the cortex_vectors tag
- WHEN the formula is checked
- THEN ruby syntax validation passes and the documented install path matches the Makefile build target

#### Scenario: conventions preserved (Error state)
- GIVEN a distribution change would alter PR branch targets or the Husky pre-push chain
- WHEN the change is proposed
- THEN it is rejected as out of scope and the docs record the existing conventions instead

### Requirement: REQ-GAINS-007 Operator prerequisites runbook (W7)
Requirements: REQ-GAINS-007

Operator prerequisites (host heartbeat/auto-renew deactivation causing about 8 authority-loss incidents; unconfigured report signing secret) SHALL be captured as a documented runbook (docs/OPERATOR-RUNBOOK.md), not as code, listing exact commands, failure symptoms, and recovery steps.

#### Scenario: runbook is actionable (Happy path)
- GIVEN an operator with host-level access
- WHEN following the runbook for heartbeat activation or the cortex-ia report config secret setup
- THEN each prerequisite lists the exact command or setting, its failure symptom, and its recovery

#### Scenario: secret handling (Edge case)
- GIVEN the report signing secret must never be committed
- WHEN the runbook documents the configuration step
- THEN the secret value is referenced as an operator-provided environment input and no credential is written into any repository file

#### Scenario: runbook stays current (Error state)
- GIVEN the cortex-ia CLI surface changes the heartbeat or report commands
- WHEN the runbook no longer matches the active CLI
- THEN a follow-up task updates the runbook rather than leaving stale instructions as authority

## Design

Seam map: pre-change baseline targets bench/reports/prechange-baseline-locomo.json plus bench/reports/prechange-baseline-longmemeval.json; embeddings targets bench/reports/eval-embeddings-locomo.json plus bench/reports/eval-embeddings-longmemeval.json; the diagnosis targets bench/reports/longmemeval-diagnosis.json; ablation targets bench/reports/engine-ablation-audit.json; multi-hop tuning targets internal/retrieval/adaptive.go plus internal/retrieval/adaptive_test.go; engine simplification targets internal/retrieval/crag.go, internal/retrieval/late_interaction.go, and internal/domain/graph/community_summaries.go; distribution targets packaging/homebrew/cortex.rb plus docs/DISTRIBUTION.md and docs/BENCHMARKS.md; the runbook targets docs/OPERATOR-RUNBOOK.md.

Wave structure: Wave A runs evidence tasks in parallel (honest-01, honest-02, honest-03 with disjoint artifacts). Wave A' runs the ablation (honest-04) after honest-02 because the dense and graph legs need embeddings for fair measurement. Wave B runs multi-hop tuning (honest-05) after honest-01 and honest-02 because it chains those baselines. Wave C runs the decision-gated simplification (honest-06) after honest-04 and honest-05 to avoid mutating the same retrieval seam while tuning is measured. Wave D runs distribution and ops work (honest-07, honest-08, honest-09) where honest-08 waits on honest-01 for honest pre-change numbers.

Measurement mechanics: runners are Config-struct driven; evidence runs use temporary wrapper mains outside the repository (the F7/F8 pattern) so no product code changes; common.EvalBaselineFromReport chains prior reports into Config.Baseline; the pre-change baseline uses first-publication semantics (baseline_status not_recorded); the fixed judge protocol (temperature 0, seed 42) is unchanged.

## Tasks

- [ ] honest-01-prechange-baseline [bench] Publish pre-change LOCOMO/LongMemEval baseline from the pre-initiative commit
  Requirements: REQ-GAINS-001
  Allowed files target: bench/reports/prechange-baseline-locomo.json; bench/reports/prechange-baseline-longmemeval.json
  Verification: jq -e '.schema_version == "eval-comparison/v1" and .baseline_status == "not_recorded" and .judge.enabled == true' bench/reports/prechange-baseline-locomo.json
  Forecast: ops probe, no product code; provenance records the verified commit (expected 3bf0f411, re-verify via git log) plus the harness-reuse decision.

- [ ] honest-02-embeddings-enable [bench] Enable local Ollama embeddings and publish embeddings-on eval reports
  Requirements: REQ-GAINS-002
  Allowed files target: bench/reports/eval-embeddings-locomo.json; bench/reports/eval-embeddings-longmemeval.json
  Verification: jq -e '.schema_version == "eval-comparison/v1" and .score.total_questions > 0' bench/reports/eval-embeddings-locomo.json
  Forecast: operator model pull plus wrapper run; no product code.

- [ ] honest-03-longmemeval-diagnosis [bench] Diagnose LongMemEval 0.08 root causes into a ranked evidence report
  Requirements: REQ-GAINS-004
  Allowed files target: bench/reports/longmemeval-diagnosis.json
  Verification: jq -e '.root_causes | length >= 1' bench/reports/longmemeval-diagnosis.json
  Forecast: investigation artifact; every root cause carries evidence refs and a falsifiable fix hypothesis.

- [ ] honest-04-engine-ablation [retrieval] Measure per-engine marginal contribution via ablation harness runs
  Requirements: REQ-GAINS-005
  Allowed files target: bench/reports/engine-ablation-audit.json
  Verification: jq -e '.engines | length >= 6' bench/reports/engine-ablation-audit.json
  Dependencies: honest-02-embeddings-enable
  Forecast: wrapper-only engine toggles; undetermined entries document the missing seam; no product edits.

- [ ] honest-05-multihop-tuning [retrieval] Tune multi-hop tier with embeddings and prove gain against the pre-change baseline
  Requirements: REQ-GAINS-003
  Allowed files target: internal/retrieval/adaptive.go; internal/retrieval/adaptive_test.go
  Verification: go test -v -count=1 ./internal/retrieval
  Dependencies: honest-01-prechange-baseline, honest-02-embeddings-enable
  Forecast: <= 300 changed lines Go, <= 700 Go cap; MinDelta gate on the multi-hop slice.

- [ ] honest-06-engine-simplification [retrieval] Apply the orchestrator-approved engine deprecation/simplification gate
  Requirements: REQ-GAINS-005
  Allowed files target: internal/retrieval/crag.go; internal/retrieval/late_interaction.go; internal/domain/graph/community_summaries.go
  Verification: go test -count=1 ./internal/retrieval ./internal/domain/graph
  Dependencies: honest-04-engine-ablation, honest-05-multihop-tuning
  Forecast: decision-gated; without orchestrator approval the task reduces to documenting the gate rationale in the audit appendix with no product edits; engines outside the allowed files require a new task.

- [ ] honest-07-distribution-packaging [packaging] Add Homebrew formula and distribution documentation
  Requirements: REQ-GAINS-006
  Allowed files target: packaging/homebrew/cortex.rb; docs/DISTRIBUTION.md
  Verification: ruby -c packaging/homebrew/cortex.rb
  Forecast: additive docs plus formula; no CI or release workflow changes.

- [ ] honest-08-benchmarks-doc [docs] Publish honest benchmark results with the pre-change comparison
  Requirements: REQ-GAINS-006
  Allowed files target: docs/BENCHMARKS.md
  Verification: go test -v -count=1 ./bench -run TestRetrievalBaselineDocumentationContract
  Dependencies: honest-01-prechange-baseline
  Forecast: numbers must match published artifacts; refresh after honest-05 lands when applicable.

- [ ] honest-09-operator-runbook [ops] Document operator prerequisites: authority heartbeat and report signing secret
  Requirements: REQ-GAINS-007
  Allowed files target: docs/OPERATOR-RUNBOOK.md
  Verification: grep -c "cortex-ia report config" docs/OPERATOR-RUNBOOK.md
  Forecast: documented runbook only; secret values stay operator-provided and out of the repository.
