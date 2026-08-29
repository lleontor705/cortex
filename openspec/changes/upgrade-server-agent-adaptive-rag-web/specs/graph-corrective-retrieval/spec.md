# Delta for Graph and Corrective Retrieval

## ADDED Requirements

### Requirement: REQ-ADAPT-002: Authorized multi-hop memory and AST graph
Multi-hop retrieval MUST seed PPR from normalized lexical, dense and AST hits. It MUST reserve hop/node/edge/evidence budgets before expansion or materialization, acquire independently bounded visible memory and scoped AST snapshots through authenticated production operations, and canonically order seeds, graph material and evidence before truncation. Authorization, missing authenticated operations and scope/identity uncertainty MUST fail closed; corpus availability MAY degrade only that branch.

#### Scenario: Impact question
- GIVEN authorized memory decisions and AST dependency neighbors
- WHEN a multi-hop impact question is asked
- THEN both graph families may contribute cited evidence
- AND the production `requestOperations` path delegates to the context-bound authorized store

#### Scenario: Budget boundary and deterministic ties
- GIVEN duplicate-order hybrid seeds and corpora larger than configured budgets
- WHEN snapshots and propagation run repeatedly
- THEN acquisition never materializes beyond the pre-reserved bounds
- AND canonical ordering returns the same bounded evidence

#### Scenario: Authorization versus availability
- GIVEN either a cross-scope endpoint, missing authenticated operations, identity mismatch or unavailable authorized AST corpus
- WHEN graph retrieval runs
- THEN authority or identity uncertainty fails closed without evidence
- AND a pure AST availability failure is reported as branch degradation only when bounded authorized memory evidence remains

### Requirement: REQ-ADAPT-003: Checksum-aware architectural summaries
Architectural retrieval MUST derive community summaries from the authorized memory and AST snapshot and MAY cache them only under the exact tenant, workspace, project UUID, memory checksum and AST index checksum.

#### Scenario: Architectural overview
- GIVEN a ready AST index and visible memory graph
- WHEN an architectural question is asked
- THEN relevant community summaries guide retrieval
- AND underlying evidence handles remain available for citations

#### Scenario: Corpus changes
- GIVEN a cached summary and a changed memory or AST checksum
- WHEN the next question runs
- THEN the stale summary is not reused and is recomputed or marked unavailable

#### Scenario: Invalid snapshot
- GIVEN a missing, mismatched or non-ready scoped index identity
- WHEN architectural retrieval starts
- THEN that branch fails closed or degrades with a stable code
- AND no stale summary is returned

### Requirement: REQ-ADAPT-004: One-pass corrective retrieval
CRAG MUST evaluate normalized evidence after reranking. Low confidence MUST trigger at most one server-controlled reformulation under the same immutable scope and remaining budgets; a second low result MUST abstain.

#### Scenario: Successful correction
- GIVEN the first retrieval is low-confidence and reformulation finds evidence
- WHEN CRAG reevaluates once
- THEN the improved authorized evidence is returned with `refinement_count=1`

#### Scenario: Medium confidence
- GIVEN evidence is medium confidence
- WHEN CRAG evaluates it
- THEN no reformulation occurs and uncertainty is preserved

#### Scenario: Still insufficient
- GIVEN the single reformulation remains low or errors
- WHEN correction completes
- THEN the agent returns insufficient evidence or an explicit degradation
- AND MUST NOT make a second reformulation or external-Web search
