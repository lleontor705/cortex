# Delta for Scoped Adaptive Retrieval

## ADDED Requirements

### Requirement: REQ-SCOPE-001: Canonical corpus visibility
Every retrieval branch MUST bind tenant and current workspace from the verified principal, authorize the public project UUID, and apply identical classification-clearance and personal-owner visibility during lexical reads, vector hydration and graph expansion.

#### Scenario: Authorized hydration
- GIVEN a vector candidate in the authorized project and visible classification
- WHEN it is hydrated
- THEN its evidence is eligible for ranking
- AND the project label is presentation-only

#### Scenario: Duplicate labels
- GIVEN equal labels across sibling projects or workspaces
- WHEN retrieval runs for one public project UUID
- THEN only that exact project contributes evidence

#### Scenario: Hidden candidate
- GIVEN a restricted candidate without clearance or another actor's personal observation
- WHEN vector or graph hydration is attempted
- THEN it is rejected as not found with a stable authorization-safe code
- AND no metadata or content is emitted

### Requirement: REQ-RANK-001: Comparable deterministic scores
The retriever MUST normalize lexical, dense, MaxSim, PPR and summary signals to `[0,1]` before fusion, CRAG and confidence; invalid values MUST NOT receive a neutral default.

#### Scenario: Mixed signals
- GIVEN lexical, vector and graph candidates with different native scales
- WHEN scores are combined
- THEN every component is normalized and the final order is deterministic

#### Scenario: Equal scores
- GIVEN candidates with equal normalized scores
- WHEN ranking completes
- THEN stable source-kind and public-handle tie breakers are applied

#### Scenario: Invalid score
- GIVEN NaN, infinity, negative or structurally missing score input
- WHEN normalization runs
- THEN it is clamped or rejected by documented signal policy
- AND MUST NOT be promoted to medium confidence

### Requirement: REQ-ADAPT-001: Tiered semantic tracer
The agent MUST route `auto` queries to direct-factual, semantic-hybrid, multi-hop-graph or architectural-global retrieval. Semantic-hybrid MUST execute lexical and filtered dense retrieval, fuse with RRF and rerank with MaxSim within fixed budgets.

#### Scenario: Conceptual question
- GIVEN a conceptual project question and healthy lexical/vector corpora
- WHEN auto retrieval runs
- THEN semantic-hybrid uses both branches and reports their contribution

#### Scenario: Vector degradation
- GIVEN embeddings or the configured vector index is unavailable
- WHEN semantic retrieval runs
- THEN lexical evidence may continue and the vector branch is marked degraded

#### Scenario: Scope rejection
- GIVEN an unauthorized project UUID or absent current workspace
- WHEN any tier is requested
- THEN retrieval fails closed before embedding, vector, graph or model calls
