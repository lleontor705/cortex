[← Back to README](../README.md)

# Benchmarks

## What the baseline proves

The committed baseline contracts prove that Cortex can validate a versioned,
labelled retrieval corpus; preserve per-query ranked stable IDs; calculate
retrieval-quality, correctness, latency, throughput, and resource fields; compare
independent runs; and preregister immutable release gates. The corpus contract is
`cortex.retrieval-corpus/v1`, the report contract is
`retrieval-evidence-report/v1`, and each run must record its exact corpus,
protocol, profile, build, and hardware versions. Universal correctness gates
require zero isolation violations and an exact match with the authoritative
eligible stable-ID set.

This is a **methodology and current-path baseline**, not evidence that a future
retrieval candidate is better. A result supports a Cortex claim only when its
report and referenced corpus are committed, complete, independently reproduced,
and approved under a gate registered before candidate results are observed.

## What the baseline does not prove

- It does not establish a new quality, latency, throughput, CPU, RSS, storage,
  or index threshold. Numeric thresholds remain unset until representative
  baseline runs, variance analysis, corpus/hardware approval, and reviewer
  sign-off exist.
- It does not turn external LOCOMO, DMR, or LongMemEval results into
  Cortex-reproduced performance.
- It does not make answer-token F1, ROUGE-L, or judge correctness equivalent to
  labelled stable-ID retrieval relevance. Those metrics evaluate answer text or
  answer acceptability; release retrieval evidence requires relevant episode or
  fact IDs, or immutable evidence spans.
- It does not prove portability, vector-provider parity, or production-scale
  performance. Those claims need their own versioned build and evidence matrix.

The legacy suite tables and commands below are preserved for continuity. Unless
a row links a complete Cortex evidence report, treat it as historical or
exploratory evidence, not as a release gate or a cross-system comparison.

## Versioned retrieval protocol

| Evidence field | Required record |
|---|---|
| Corpus | Schema and corpus version; immutable query IDs; profile/query classes; hard negatives; no-answer, temporal, isolation, privacy, classification, and lifecycle labels |
| Relevance | Relevant episode/fact stable IDs or half-open byte spans; missing labels make release evidence incomplete |
| Execution | Protocol and profile ID/version, build commit and dirty state, provider/model where used, and traceable current ranked outputs |
| Environment | Named hardware profile, OS, architecture, CPU, memory, and explicit measurement units |
| Report | Per-query and per-class results, metric definitions, limitations, and independently retained run IDs |

### Splits and evaluator

Splits are assigned by immutable query ID under a versioned strategy. The
development/calibration split may select protocol details and gates; held-out
decision evidence must not be reused after candidate results are observed. The
evaluator is the committed Go implementation in `bench/common`: it validates
stable-ID/span labels and computes deterministic aggregates. Optional answer
judges belong only to the legacy answer-evaluation suites and must disclose the
judge model, endpoint class, prompt/protocol version, and failure/abstention
policy. They do not supply missing retrieval labels.

### Metrics and uncertainty

| Metric | Definition and interpretation |
|---|---|
| Recall@k | Fraction of labelled relevant stable IDs returned in the first `k` results |
| MRR | Reciprocal rank of the first labelled relevant stable ID |
| nDCG | Discounted cumulative gain normalized by the ideal ordering; supports graded relevance |
| Evidence recall | Fraction of labelled episode/fact IDs or evidence spans covered |
| No-answer/abstention | Whether the profile correctly returns no supported answer for labelled no-answer queries |
| Isolation violations | Count of unauthorized or otherwise ineligible returned IDs; any non-zero value blocks release |
| Filter correctness | Exact equality between returned eligibility and the authoritative filtered stable-ID set |
| Latency/throughput | p50/p95/p99 duration and completed queries per second, with units and sample size |
| Resources | CPU seconds, peak RSS bytes, corpus storage bytes, and retrieval-index bytes |

Reports disclose sample size, per-class results, the uncertainty method and
confidence level, dispersion/confidence intervals where applicable, and
outliers. Independent runs are compared only when corpus, build, hardware, and
protocol identities match. Deterministic fields must match exactly; measured
fields use a preregistered tolerance derived from baseline variance, never a
post-result tolerance.

### Hardware and resources

Record a reproducible hardware envelope rather than a machine nickname: profile
ID, OS/version, architecture, CPU model/count, available memory, and relevant
provider/model versions. Reports must state whether CPU time, RSS, storage, index
size, latency, and throughput were measured. Zero placeholders from a legacy
adapter mean **unmeasured**, not free or instantaneous.

### Release gates

The gate registry is versioned and immutable. Correctness gates (zero isolation
leakage and exact filter eligibility) are universal and non-relaxable. Quality,
latency, throughput, and resource gates are profile- and query-class-specific.
They record metric direction, sample size, corpus/hardware versions, approval,
and blocking policy before a candidate run. Changing a gate after results
requires a new protocol version, written rationale, approval, and fresh held-out
evaluation that does not reuse the decision evidence.

## External and Cortex-reproduced evidence

| Suite/evidence | Classification | Allowed claim |
|---|---|---|
| Versioned Cortex corpus + complete evidence report | Cortex-reproduced only when run by the documented protocol on the disclosed build/hardware | Retrieval and resource claims limited to that exact profile, corpus, protocol, and environment |
| LOCOMO adapter | External/legacy answer evidence; CC BY-NC 4.0 dataset | Preserve source, split, categories, upstream evidence locators, F1, and optional judge output; do not claim stable-ID retrieval relevance without Cortex labels |
| DMR / MSC-Self-Instruct adapter | External/legacy answer evidence; Apache-2.0 dataset | Preserve F1/ROUGE-L and optional judge semantics; the source answers do not supply Cortex relevant IDs/spans |
| LongMemEval adapter | External/legacy answer evidence; upstream distribution carries the applicable licence notice | Preserve ability categories, temporal locators, abstention, F1, and judge behavior; temporal locators are not Cortex relevance labels |

The adapters are intentionally reporting-only and preserve the existing runners
and scoring behavior. `cortex_reproduction: false`, incomplete retrieval labels,
or a false release-eligibility flag blocks wording that presents an adapted
result as Cortex retrieval performance.

## Reproduce the baseline

Run from the repository root on a clean committed build. The first command is
the executable documentation contract; the second validates deterministic
baseline contracts and all preserved benchmark adapters without requiring a
dataset download, embeddings, a judge, or an external service.

```bash
go test -v -count=1 ./bench -run TestRetrievalBaselineDocumentationContract
go test -v -count=1 ./bench/...
```

Dataset-scale, live-judge, embedding-provider, and performance runs are separate
opt-in evidence. Record the exact command, dataset artifact/version and split,
profile/provider/model versions, build commit/dirty state, hardware profile,
report IDs, and output paths. Do not compare reports whose identity fields differ
without a separately approved normalization protocol.

## Licences and limitations

- Cortex code and generated Cortex evidence remain governed by this repository's
  licence; benchmark datasets retain their upstream licences and attribution.
- LOCOMO is documented as CC BY-NC 4.0, MSC-Self-Instruct as Apache-2.0, and
  LongMemEval with the licence notice shipped by the exact upstream distribution.
  Verify terms against the downloaded version before redistribution.
- Dataset downloads, embedding services, and live judges are not required for
  deterministic contract validation and may impose separate terms, cost, network,
  nondeterminism, or privacy constraints.
- The committed miniature corpus validates contracts and adversarial isolation;
  it is not representative evidence for production scale or domain coverage.
- Legacy suite scores remain useful for continuity, but their answer-oriented
  labels, evaluators, and splits are not interchangeable with the Cortex
  stable-ID/span relevance protocol.

The legacy Cortex suites below exercise three standard memory benchmarks. Their
results are reproducible only when the exact dataset, split, evaluator, build,
profile/provider, hardware, and protocol are disclosed; the tables alone do not
constitute Cortex release evidence.

## Results Summary

> **Evidence classification:** Historical, unverified repository snapshot.
> **Evidence identity:** The retained rows do not identify a result artifact,
> run ID, build commit/dirty state, exact dataset artifact and split, hardware
> profile, or uncertainty. They are not reproducible Cortex release evidence.
> **Evaluator classification:** The numeric scores use legacy answer-token F1
> (and, where stated, ROUGE-L or an optional answer judge), not labelled
> stable-ID/span retrieval relevance.
> **Comparability:** Values are preserved for historical continuity only. Do not
> use them for cross-provider, cross-system, performance, or causal conclusions.

### LOCOMO (Long-Term Conversational Memory)

**Dataset:** 1,986 questions across 10 conversations, 5 question types.
**Source:** [snap-research/locomo](https://github.com/snap-research/locomo) (ACL 2024)

| Mode | single-hop | multi-hop | temporal | Legacy ratio shown in snapshot |
|------|-----------|-----------|----------|-------------------|
| FTS5 only (baseline) | 0.002 | 0.001 | 0.000 | — |
| FTS5 + Ollama (nomic-embed-text) | 0.025 | 0.016 | 0.026 | **12-16x** |
| FTS5 + OpenAI (text-embedding-3-small) | 0.026 | 0.016 | 0.037 | **13-37x** |

> **Note:** These scores are raw answer-token F1 between retrieved context and
> gold answers. They do not measure answer-generation accuracy, do not contain
> Cortex stable-ID/span relevance labels, and do not substantiate an external
> system's accuracy. The ratios are retained data from the legacy snapshot, not
> verified retrieval-improvement claims.

### DMR (Deep Memory Retrieval)

**Dataset:** 500 multi-session conversations from MSC-Self-Instruct.
**Source:** [MemGPT/MSC-Self-Instruct](https://huggingface.co/datasets/MemGPT/MSC-Self-Instruct) (arXiv:2310.08560)

| Mode | Avg Score (F1 + ROUGE-L) |
|------|--------------------------|
| FTS5 only | 0.000 |
| FTS5 + Ollama vectors | Pending full run |
| FTS5 + OpenAI vectors | Pending full run |

### Embedding Provider Comparison

Tested on LOCOMO (50 questions subset):

| Provider | Model | Dimensions | single-hop | multi-hop | temporal | Time | Cost |
|----------|-------|-----------|-----------|-----------|----------|------|------|
| **Ollama** | nomic-embed-text | 768 | 0.025 | 0.016 | 0.026 | 12 min | $0 |
| **OpenAI** | text-embedding-3-small | 1536 | 0.026 | 0.016 | 0.037 | 21 min | ~$0.02 |
| None (FTS5) | — | — | 0.002 | 0.001 | 0.000 | 4 min | $0 |

**Interpretation limit:** The table preserves answer-token scores, elapsed times,
and reported costs from an unidentified 50-question legacy run. Without the
required evidence identity and repeated-run uncertainty, these rows do not
support provider equivalence, temporal-reasoning conclusions, embedding-dimension
causality, provider speed or network-cause claims, retrieval multipliers, or an
absolute statement about FTS5 temporal capability.

## Methodology

### Pipeline

For each benchmark question:

1. **Ingest** — Parse dataset conversations into Cortex sessions + observations (stored in in-memory SQLite)
2. **Embed** — When vector search is enabled, generate embeddings for each observation via Ollama or OpenAI
3. **Search** — Run FTS5 keyword search + optional vector cosine similarity search
4. **Fuse** — Combine FTS5 and vector results using Reciprocal Rank Fusion (k=60)
5. **Score** — Compare top-5 retrieved results against gold answer using F1 token overlap
6. **Judge** — Optionally evaluate with LLM-as-Judge (requires API key)

### Scoring

- **F1 Token Overlap:** Tokenize prediction and reference, compute precision/recall/F1 on token sets
- **ROUGE-L:** Longest Common Subsequence F1 between prediction and reference
- **LLM-as-Judge:** GPT-4o or Claude evaluates if the retrieved context contains the correct answer (optional, requires API key)
- **Correct threshold:** F1 >= 0.3 for LOCOMO, (F1 + ROUGE-L) / 2 >= 0.3 for DMR

### Reciprocal Rank Fusion (RRF)

When both FTS5 and vector results are available, they are combined using RRF:

```
RRF_score(doc) = Σ 1/(k + rank_i)  where k=60
```

Each ranking system (FTS5 by BM25, vectors by cosine similarity) contributes independently. Documents appearing in both rankings get a combined score higher than either alone.

### Limitations

1. **Retrieval-only evaluation** — Cortex retrieves relevant memories but does not generate answers. End-to-end evaluation requires an LLM layer on top.
2. **Sequential embedding** — Each observation is embedded one at a time. The effect of batch embedding on runtime was not evaluated by this snapshot.
3. **No graph boost** — Knowledge graph neighbor expansion is not used in these benchmarks; its effect on multi-hop scores was not evaluated.
4. **F1 is conservative** — Token overlap penalizes retrieval systems that return contextually correct but lexically different content.

## Running Benchmarks

### Prerequisites

```bash
# Build with vector search support
go build -tags cortex_vectors ./cmd/cortex

# For local embeddings (recommended)
# Install Ollama: https://ollama.com
ollama pull nomic-embed-text

# Download datasets
cd bench
chmod +x download.sh
./download.sh
```

### Run

```bash
# LOCOMO — FTS5 only (fast, no dependencies)
go test ./bench/locomo/ -run TestRunFullDataset -v -timeout 30m

# LOCOMO — with Ollama embeddings (requires Ollama running)
go test -tags cortex_vectors ./bench/locomo/ -run TestRunWithOllamaEmbeddings -v -timeout 30m

# LOCOMO — with OpenAI embeddings (requires API key)
OPENAI_API_KEY=sk-... go test -tags cortex_vectors ./bench/locomo/ -run TestRunWithOpenAIEmbeddings -v -timeout 30m

# DMR
go test ./bench/dmr/ -run TestRunFullDataset -v -timeout 30m

# All benchmarks (short mode — uses subsets)
go test ./bench/... -short -v -timeout 10m
```

### Results

Results are saved as JSON in `bench/results/`:

```bash
ls bench/results/
# locomo.json              — Full LOCOMO (FTS5 only)
# locomo_ollama_50.json    — LOCOMO subset with Ollama
# locomo_openai_50.json    — LOCOMO subset with OpenAI
# dmr.json                 — Full DMR
```

## Dataset Licenses

| Dataset | License | Source |
|---------|---------|--------|
| LOCOMO | CC BY-NC 4.0 | [snap-research/locomo](https://github.com/snap-research/locomo) |
| MSC-Self-Instruct (DMR) | Apache 2.0 | [MemGPT/MSC-Self-Instruct](https://huggingface.co/datasets/MemGPT/MSC-Self-Instruct) |
| LongMemEval | MIT | [xiaowu0162/LongMemEval](https://github.com/xiaowu0162/LongMemEval) |

## References

- LOCOMO: "Evaluating Very Long-Term Conversational Memory of LLM Agents" (ACL 2024, [arXiv:2402.17753](https://arxiv.org/abs/2402.17753))
- DMR/MemGPT: "MemGPT: Towards LLMs as Operating Systems" ([arXiv:2310.08560](https://arxiv.org/abs/2310.08560))
- LongMemEval: "Benchmarking Chat Assistants on Long-Term Interactive Memory" (ICLR 2025, [arXiv:2410.10813](https://arxiv.org/abs/2410.10813))
- Engram: "Effective, Lightweight Memory Orchestration for Conversational Agents" ([arXiv:2511.12960](https://arxiv.org/abs/2511.12960))
- Zep/Graphiti: "A Temporal Knowledge Graph Architecture for Agent Memory" ([arXiv:2501.13956](https://arxiv.org/abs/2501.13956))
