# Cortex Benchmarks

Evaluation suite for Cortex memory retrieval against standard benchmarks.

## Retrieval baseline quick path

The deterministic baseline proves the corpus, report, reproducibility, gate, and
legacy-adapter contracts. It does **not** prove that a candidate improves Cortex,
that external benchmark scores are Cortex-reproduced, or that answer-token
F1/ROUGE/judge results measure labelled stable-ID retrieval relevance.

```bash
# Validate the benchmark-claim documentation contract.
go test -v -count=1 ./bench -run TestRetrievalBaselineDocumentationContract

# Validate the offline baseline contracts and preserved adapters.
go test -v -count=1 ./bench/...
```

These commands preserve LOCOMO, DMR, and LongMemEval without requiring a dataset
download, embedding provider, live judge, or external service. Dataset-scale and
performance runs are opt-in: record corpus/split, evaluator, protocol/profile,
provider/model, build, hardware, uncertainty, resources, licences, limitations,
and report IDs before using their results as evidence. See
[the full methodology](../docs/BENCHMARKS.md) for metric definitions and release
claim rules.

## Benchmarks

| Benchmark | Questions | Focus | Source |
|-----------|-----------|-------|--------|
| **LOCOMO** | 7,512 | Long-term conversational memory (5 question types) | [snap-research/locomo](https://github.com/snap-research/locomo) |
| **DMR** | ~500 | Deep memory retrieval across sessions | [MemGPT/MSC-Self-Instruct](https://huggingface.co/datasets/MemGPT/MSC-Self-Instruct) |
| **LongMemEval** | 500 | Long-term interactive memory (5 abilities) | [xiaowu0162/LongMemEval](https://github.com/xiaowu0162/LongMemEval) |

## Quick Start

```bash
# 1. Download datasets
chmod +x download.sh
./download.sh

# 2. Run benchmarks
cd .. && go test ./bench/... -v -timeout 30m

# 3. Run specific benchmark with reporting
go run ./bench/locomo -data bench/datasets/locomo10.json -report bench/results/locomo.json
```

## Evaluation Methodology

1. **Ingest** — Parse dataset conversations into Cortex sessions + observations
2. **Query** — For each question, run `mem_search` (FTS5 + optional graph boost)
3. **Score** — F1 token overlap (always) + LLM-as-Judge (optional, needs API key)
4. **Aggregate** — Per-type and overall accuracy

### LLM Judge (Optional)

Set an API key to enable LLM-based answer evaluation:

```bash
export OPENAI_API_KEY=sk-...    # Uses gpt-4o
# OR
export ANTHROPIC_API_KEY=sk-... # Uses claude-sonnet
```

Without an API key, scoring falls back to F1 token overlap only.

## Dataset Licenses

- LOCOMO: CC BY-NC 4.0
- MSC-Self-Instruct: Apache 2.0
- LongMemEval: MIT
