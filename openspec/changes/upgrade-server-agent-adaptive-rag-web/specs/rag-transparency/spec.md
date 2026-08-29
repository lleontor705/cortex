# Delta for RAG Transport Transparency

## ADDED Requirements

### Requirement: REQ-TRACE-001: Safe JSON and SSE retrieval trace
JSON and SSE MUST expose equivalent safe metadata: selected tier, completed stages, branch status, refinement count, bounded result/node counts, corpus generation/checksum tokens, degradation codes and final confidence. They MUST NOT expose queries, prompts, evidence content, internal database IDs, secrets or authorization internals.

#### Scenario: Successful stream
- GIVEN a semantic or graph answer
- WHEN SSE events are emitted
- THEN ordered metadata updates describe active/completed stages
- AND `done` equals the canonical JSON result

#### Scenario: Partial degradation
- GIVEN one corpus fails after another succeeds
- WHEN metadata is returned
- THEN only stable branch/degradation codes and bounded counts are exposed

#### Scenario: Sensitive diagnostic
- GIVEN an internal error contains SQL, content, token or principal details
- WHEN transport serialization runs
- THEN details are replaced by a stable public code
- AND JSON/SSE contain no sensitive value
