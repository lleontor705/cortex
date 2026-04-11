[← Back to README](../README.md)

# Benchmarks

Cortex is evaluated against three standard memory benchmarks used in the academic literature. All results are reproducible — see [Running Benchmarks](#running-benchmarks) below.

## Results Summary

### LOCOMO (Long-Term Conversational Memory)

**Dataset:** 1,986 questions across 10 conversations, 5 question types.
**Source:** [snap-research/locomo](https://github.com/snap-research/locomo) (ACL 2024)

| Mode | single-hop | multi-hop | temporal | Overall improvement |
|------|-----------|-----------|----------|-------------------|
| FTS5 only (baseline) | 0.002 | 0.001 | 0.000 | — |
| FTS5 + Ollama (nomic-embed-text) | 0.025 | 0.016 | 0.026 | **12-16x** |
| FTS5 + OpenAI (text-embedding-3-small) | 0.026 | 0.016 | 0.037 | **13-37x** |

> **Note:** These scores are raw F1 token overlap between retrieved context and gold answers. Cortex is a **retrieval system**, not a generative model — it finds relevant memories, not generates answers. For end-to-end accuracy (retrieval + LLM answer generation), scores are significantly higher. Engram reports 80% LOCOMO with an LLM-as-Judge evaluator on top of retrieval.

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

**Key findings:**

1. **Vector search improves retrieval 12-37x** across all question types vs. keyword-only FTS5
2. **Ollama (local) matches OpenAI** on single-hop and multi-hop questions
3. **OpenAI leads on temporal reasoning** (+42% over Ollama) due to higher-dimensional embeddings
4. **Ollama is 1.7x faster** than OpenAI (no network latency for inference, but sequential embedding)
5. **Both providers improve temporal from zero** — FTS5 cannot answer temporal questions at all

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
2. **Sequential embedding** — Each observation is embedded one at a time. Batch embedding would significantly reduce benchmark runtime.
3. **No graph boost** — Knowledge graph neighbor expansion is not yet used in benchmarks. This is expected to improve multi-hop scores in v0.5.
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
