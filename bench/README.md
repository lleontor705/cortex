# Cortex Benchmarks

Evaluation suite for Cortex memory retrieval against standard benchmarks.

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
