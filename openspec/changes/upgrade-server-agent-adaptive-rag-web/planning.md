# Planning Join

## Evidence

- `internal/platform/server/agent.go` separately runs lexical/vector memory retrieval and AST lookup.
- `internal/retrieval/adaptive.go` declares four tiers but ignores `vectorSearch`; multi-hop only scores lexical candidates.
- `internal/store/postgres/authorized_operations.go:GetAgentObservationByID` verifies tenant/workspace/project but omits `appendObservationVisibilityPredicate`.
- `internal/retrieval/late_interaction.go`, `crag.go`, `domain/graph/ppr.go` and `domain/graph/community_summaries.go` provide reusable pure-Go engines.
- Pre-review of 2.2 found post-materialization limiting, lexical-only memory seeds and no production `requestOperations` delegation for graph snapshots.
- `web/src/app/agent/page.tsx` and `search/page.tsx` have separate scope/status/source presentations.

## Contract-to-task join

| Requirements | Tasks |
|---|---|
| REQ-SCOPE-001, REQ-RANK-001 | 1.1–1.2, 5.1 |
| REQ-ADAPT-001..002 | 2.1–2.2, usarw-2.2i, 5.1 |
| REQ-ADAPT-003..004 | 2.3–2.4, 5.1 |
| REQ-TRACE-001 | 3.1, 4.1, 5.1 |
| REQ-WEB-KNOW-001..003 | 4.1–4.3, 5.1 |

## DAG

Critical path: `1.1 → 1.2 → 2.1 → 2.2 → usarw-2.2i → 2.3 → 2.4 → 3.1 → 4.1 → 4.2/4.3 → 5.1`. The orchestrator manually routes `usarw-2.2i` after 2.2 review; 2.3 MUST remain blocked until its tagged PostgreSQL 16 oracle passes. Only `4.2` and `4.3` are parallel; their writable files are disjoint. All Go tasks forecast ≤500 changed lines and TypeScript tasks ≤350.
