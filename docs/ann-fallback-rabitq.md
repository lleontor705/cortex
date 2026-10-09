# ANN Strategy Decision Record: MRL NO-GO, Exact-Scan Retained, RaBitQ Fallback

- **Status**: DECIDED — exact-scan + tuning retained; RaBitQ documented as fallback (not implemented)
- **Change**: [retrieval-improvements-2026-10](../openspec/changes/retrieval-improvements-2026-10/design.md) · board task ret-206 (REQ-RET-104)
- **Gate evidence**: ret-201 MRL recall decision gate (REQ-RET-103), `bench/mrl/`, merged as `9ee9b44` (PR #145)
- **Tracking issue**: #146

## 1. Decision

The halfvec(2048) MRL subvector + full-precision re-rank ANN strategy **failed its recall gate** (NO-GO). The chain of follow-on ANN tasks (ret-202..205) is **voided**. Production retains the **exact pgvector cosine scan with ret-105 config-gated tuning**. The **RaBitQ** quantized-retrieval design (arXiv 2405.12497) is recorded here as the fallback path should the retained strategy ever become latency-infeasible at scale.

## 2. NO-GO evidence (ret-201)

Deterministic offline harness in `bench/mrl/` (`recall.go`, `recall_test.go`, `cmd/mrl`): halfvec(2048) subvector top-50 shortlist + full-precision re-rank vs exact 4096-dim float32 cosine scan, on a 2000-doc synthetic MRL-like corpus with 100 random and 50 hard near-duplicate queries.

| Check | Result | Gate | Verdict |
|---|---|---|---|
| recall@10, combined (subvector + re-rank) | **0.8080** (random 0.7240 / hard 0.9760) | >= 0.95 | **FAIL** |
| recall@10, direct no-re-rank lower bound | 0.5407 | informational | — |
| recall@10, full-4096-dim fp16 quantization-only control | 0.9993 | control | isolates the loss as **truncation**, not halfvec storage noise |
| Latency, projected at 200k rows (ef_search=100, hop 4, K=50) | 810x speedup projected | — | **PASS** |
| Measured harness scan speedup (half-dim linear scan) | 1.97x | linear-scan bound | — |

**Interpretation**: the recall shortfall comes from dropping dims 2048..4095, not from `halfvec` storage or re-rank mechanics. The corpus is a deliberately **pessimistic lower bound** — the 2048-dim prefix carries ~68% of ranking energy on synthetic data, whereas real qwen3 MRL truncation is *trained* to preserve ranking quality. The verdict is therefore a synthetic lower-bound NO-GO, not a measured production regression.

## 3. Retained strategy (LIVE in production)

- **Exact pgvector cosine scan** remains the production vector-search strategy.
- **ret-105 exact-scan tuning is LIVE** (PR #132, `cd25322`): config-gated tuning via `CORTEX_VECTOR_PGVECTOR_MAX_PARALLEL_WORKERS_PER_GATHER` (parallel gather scoped to a dedicated search transaction) and `CORTEX_VECTOR_PGVECTOR_DISTANCE_MODE=ip` (normalized inner-product `<#>` mode), with **top-3 exact-match verification** — delivering a **20-40% scan speedup** while the default SQL stays byte-identical to the pre-tuning exact `<=>` cosine scan when the env vars are unset (pinned by `TestSearchTuning_DefaultSQLByteIdentical`).
- Engine-side similarity and threshold semantics are unchanged (`1 - distance`).
- Migration 114 DDL (documented in the ret-201 report) was **not executed**; no schema change accompanies this decision.

## 4. Fallback design: RaBitQ (arXiv 2405.12497)

**Design**: shortlist via `binary_quantize` at 1 bit per dimension (4096-dim → 512-byte signature) with the provably error-bounded distance estimator from RaBitQ, then **full-precision re-rank** of the top-K shortlist with the stored float32/halfvec vectors. This mirrors the re-rank seam the ret-201 harness already exercises, so the harness is directly reusable for a RaBitQ gate.

**Recall considerations**:

- RaBitQ's error-bounded estimator targets exactly the failure mode ret-201 isolated: recall loss from coarse quantization is bounded, and the full-precision re-rank stage restores ranking quality for the shortlist.
- The fp16 control (0.9993) shows full-dimension quantization noise is negligible; RaBitQ's 1-bit signature is far coarser than fp16, so the shortlist must be sized generously (e.g. top-100..200) before re-rank to hit a 0.95+ gate — this is the parameter the gate must tune, exactly as the ret-201 harness tunes shortlist size.
- The hard near-duplicate slice (0.9760 with MRL truncation) is the discriminator class; a RaBitQ gate must report random/hard/combined recall separately, as ret-201 did.

**Status**: design only. No code, schema, or config change is proposed or authorized by this record.

## 5. Revalidation procedure (cheap path)

Before any RaBitQ implementation, the MRL strategy itself can be cheaply revalidated on real data:

1. Re-run the ret-201 harness (`bench/mrl/cmd/mrl`) with **real embeddings** from the production qwen3 embedding model instead of the synthetic MRL-like corpus. No model, dataset download, or network is needed beyond the embeddings themselves; the harness is deterministic and offline.
2. The same >= 0.95 combined recall@10 gate applies. A **PASS on real embeddings** reopens the MRL subvector strategy (and would supersede this record); a **FAIL** confirms the NO-GO and keeps the fallback as the only ANN path.

## 6. Trigger conditions for revisiting

Revisit this decision (revalidate MRL, or escalate to the RaBitQ fallback) when **any** of the following holds:

1. **Corpus scale**: the vector corpus approaches the latency envelope where exact scan is no longer viable (the ret-201 latency projection was computed at 200k rows; monitor actual corpus growth and p50/p99 scan latency against that envelope).
2. **Nan MRL model availability**: a qwen3-class embedding model with a properly MRL-trained 2048 prefix becomes available in the deployment stack, making real-embedding revalidation worth a re-run.
3. **Real-embedding revalidation pass**: the Section 5 revalidation on real embeddings passes the >= 0.95 gate — in which case the MRL strategy supersedes this fallback.

## 7. Board state and DAG dead-end (educational)

Tasks ret-202..206 on board `retrieval-improvements-2026-10` are **superseded by this record** and intentionally remain in backlog — an operator may administratively close them at will.

ret-206 is a permanent DAG dead-end in cortex-ia v0.5.9: it hard-depends on ret-205, whose premise was voided by the ret-201 NO-GO. FAIL verdicts never mark a task `done`, dependents only unlock when **all** dependencies are `done`, and the CLI has no administrative edge-removal verb — so ret-206 can never become claimable. Lessons for future planners: model gate outcomes as task completions (`done`), and avoid hard dependency edges on GO/NO-GO gate tasks.
