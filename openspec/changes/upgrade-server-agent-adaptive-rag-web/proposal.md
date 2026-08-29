# Proposal: Scoped Adaptive RAG and Web Knowledge Experience

## Intent

Upgrade the server-mode conversational agent so the Web uses Cortex's existing lexical, vector, memory-graph, AST-graph, community-summary, reranking and CRAG capabilities through one deep `ScopedAgentRetriever`. Then redesign `/agent` and `/search` to make scope, retrieval tier, progress, degradation and sources understandable without changing the product's read-only knowledge objective.

## Why now

The current agent fuses lexical and vector results and appends AST symbols, but `ExecuteAdaptiveSearch` does not consume its vector callback, multi-hop does not hydrate graph neighbors, architectural mode only boosts pre-existing summary observations, and Web metadata exposes only degradation. Pre-review also found that graph budgets can be applied after materialization and the production `requestOperations` seam does not yet delegate graph snapshots.

## Outcomes

- Every corpus read is bound to the verified principal's tenant, current workspace and authorized public project UUID.
- Adaptive routing selects direct, semantic-hybrid, multi-hop or architectural retrieval.
- All ranking signals are normalized before fusion, reranking, CRAG and answer confidence.
- Memory and AST graphs contribute bounded authorized evidence: budgets are enforced before expansion/materialization, hybrid normalized hits seed PPR, and canonical ordering makes output reproducible.
- Production retrieval delegates graph reads through authenticated `requestOperations`; authorization/identity failures fail closed while availability failures degrade only the affected branch.
- CRAG performs at most one scope-preserving reformulation, then answers or abstains.
- JSON and SSE expose safe retrieval metadata with parity.
- `/agent` and `/search` become accessible, responsive knowledge workspaces with visible scope and source provenance.

## Non-goals

No write-capable agent, autonomous tenant/workspace selection, external Web fallback, raw SQL/filesystem tools, transcript persistence, Python GraphRAG service, vector-provider fallback, or changes to local-mode architecture.

## Rollback

Keep the existing agent service contract behind composition. A server feature flag may select the prior shallow retriever during rollout; schema changes are not required. A tagged PostgreSQL 16 canary gates architectural summaries. Web pages can revert independently because transport additions are backward-compatible.
