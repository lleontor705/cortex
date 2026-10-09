# Design: Retrieval Pipeline Improvements (design.md)

- Change ID: retrieval-improvements-2026-10 | Workflow: sdd-lite | Plane: hybrid

## D1. Cache mechanism decision (P1) - ONE mechanism

Decision: extend the PR #128 call-site cache pattern, do NOT wrap NewSecure with the LRU.

- New generic helper internal/retrieval/query_embedding_cache.go: type QueryEmbeddingCache wrapping
  retrieval.ScopedCache[[]float32], key = model id + "|" + strings.Join(strings.Fields(strings.ToLower(q)), " ")
  (same normalization as PR #128), atomic hits/misses, errors never cached.
- internal/platform/server/queryEmbeddingCache refactors to delegate to the helper (behavior preserved;
  existing tests stay green).
- Wiring: agent_retriever.go:507 and internal/mcp tools_cortex.go:813 / tools_memory.go:1054 use the
  helper. internal/mcp must NOT import internal/platform/server (architecture gate
  internal/app/arch_test.go), hence the helper lives in internal/retrieval.
- NewSecure LRU wrap is explicitly rejected: it would also cache reindex/document batch embeddings and
  duplicate the PR #128 mechanism. Interface change (service.Service) risk avoided.

Rollback: per-surface wiring is individually revertible; cache miss is always a correct fallback.

## D2. pgvector exact-scan tuning (P1)

- adapter.go search: after opening the tx, optionally SET LOCAL max_parallel_workers_per_gather = N.
- Optional normalized inner-product mode ('<#>') behind a config flag (default off): requires
  normalized stored vectors and normalized query vector; similarity mapping stays 1 - distance so
  engine-side threshold semantics are unchanged.
- Config surfaced via CORTEX_VECTOR_PGVECTOR_* env (authorized_operations.go:1124 pattern) +
  /api/settings (internal/platform/server/settings.go) + docs/CLI-REFERENCE(.es).md.
- Rollback: config-only (unset flag).

## D3. Structural ANN (P2) - spike first, checksum discipline

- Spike (bench/mrl): truncate qwen3-embedding vectors to 2048 (MRL prefix), measure recall@k vs 4096
  full precision on the bench corpus. Gate: agreed recall threshold. Fallback: RaBitQ/binary_quantize
  decision record (arXiv 2405.12497); binary_quantize expression index supports far higher dims.
- Implementation: dimension mode 2048 via CORTEX_VECTOR_PGVECTOR_DIMENSION (validated in adapter +
  authorized_operations.go + reported in settings.go). New migration 114 (migrations/v2/
  114_pgvector_ann_index.sql): halfvec(2048) HNSW expression index + iterative_scan session setup;
  registered in migrations/v2/embed.go; checksum pinned in internal/migration/postgres_test.go.
- Checksum-pinned identity coordination (HARD): pins for 001/002/100-113 are immutable; the 114 pin
  moves with reviewed bytes until release. Rollout uses PostgresServerMigration.Preflight/
  VerifyApplied: expected state unledgered; ANY recorded checksum for the target version stops rollout;
  prior checksums escalate tamper-class; ledger append-only; rollback forward-only/non-destructive.
- Adapter search path: halfvec ANN shortlist (hnsw.ef_search via SET LOCAL) then full-precision cosine
  re-rank of the shortlist; scores returned are full-precision 1 - cosine_distance.
- Rollback: config-only (dimension env back to 4096 + DROP INDEX CONCURRENTLY + iterative scan off);
  no shipped-byte edits ever.

## D4. Ingestion (P3)

- worker.go: make LeaseBatch configurable at composition (default remains 1; server composition sets a
  batch >1) so processBatch becomes live; MaxBacklog saturation still fails closed (REQ-EMB-001).
- adapter.go Upsert: replace per-point Exec with chunked multi-row INSERT ... ON CONFLICT (id) DO UPDATE
  (chunk size = existing maxBatchSize), preserving statement_timeout and pointArgs validation.

## D5. Fusion A/B harness (P3) - decision gate, not a flip

- bench/fusion: fixed corpus + both strategies (RRF k=60 as-is; calibrated convex combination with
  weights fitted on a tuning split), emits recall/precision/latency report through bench/common/metrics.
- REQ-RET-002 score-as-rank contract is untouched in production code; harness is read-only over
  retrieval inputs. Decision gate: explicit approval required before any production change; otherwise
  supersede REQ-RET-002 via a reviewed spec change.

## D6. Quality layers (P4) - tier-gated, flag-guarded

- Pacer: replace the global mutex+sleep pacer (reranker.go:321-349) with a per-query budget limiter
  enforcing provider 60 rpm across concurrent callers (token-bucket; injectable clock; tests never
  wall-sleep).
- Listwise rerank: sliding-window listwise prompting over the existing openai-compatible rerank seam;
  gate on retrieval tiers TierSemanticHybrid/TierMultiHopGraph (adaptive.go classification already
  exists); agent tiers already mapped in agent_retriever.go:669-672.
- HyDE: internal/retrieval/hyde.go behind a default-off flag; ScopedCache for generated hypothetical
  docs; tier-gated like listwise.
- Result-level semantic cache: agent_retriever already holds sharedAgentRetrievalCache
  (ScopedCache[RetrievalResult], 512/5min, exact-key). Extend with embedding-similarity matching for
  agent loops + PurgePrefix invalidation on scope-affecting writes; trace gains a cache-hit marker.

## D7. Interfaces & boundaries

- No public API changes except /api/settings reporting additional pgvector tuning/ANN fields.
- internal/mcp must not import internal/platform/server (shared code goes in internal/retrieval).
- Server persistence stays behind AuthorizedStore/AuthorizedContext.
- Local-mode paths (sqlite_blob, FTS5) untouched.

## D8. Rollback summary

- P1: config-only / per-surface wiring revert.
- P2: env revert + DROP INDEX CONCURRENTLY; DDL never edits shipped bytes.
- P3: LeaseBatch default unchanged (composition-only); multi-row upsert revertible per commit.
- P4: all four layers individually flag-guarded; defaults preserve today's behavior.

## D9. Decision record — ANN strategy (post-gate)

The ret-201 MRL recall gate (REQ-RET-103) returned NO-GO (recall@10 0.8080 < 0.95), voiding the ANN chain ret-202..205. Decision: retain exact-scan + ret-105 tuning (LIVE, top-3 exact-match verification, 20-40% scan speedup); RaBitQ (arXiv 2405.12497, binary_quantize + full-precision re-rank) is documented as fallback in [docs/ann-fallback-rabitq.md](../../docs/ann-fallback-rabitq.md) (issue #146), with a cheap real-embedding revalidation path. ret-202..206 stay in backlog, superseded by the record.
