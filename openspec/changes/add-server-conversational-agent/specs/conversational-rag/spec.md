# Delta for Conversational RAG

## ADDED Requirements

### Requirement: REQ-AGENT-001: Read-only grounded answer service
The server MUST answer project questions from authorized memory and scoped AST metadata through one read-only service. It MUST NOT expose tools or write memories, code, project artifacts or conversation transcripts.

#### Scenario: Grounded project answer
- GIVEN an authorized question and supporting memory/symbol evidence
- WHEN the service produces an answer
- THEN it returns explanatory text, confidence, degradation status and validated sources
- AND each factual project claim is supported or marked uncertain

#### Scenario: One corpus unavailable
- GIVEN one authorized corpus is unavailable but another has sufficient evidence
- WHEN the question is answered
- THEN the service may answer from the available corpus
- AND identifies the unavailable branch in `retrieval.degraded`

#### Scenario: No authorized evidence
- GIVEN no authorized corpus is available or retrieval has no supporting evidence
- WHEN an answer is requested
- THEN the service returns `503 retrieval_unavailable` or an explicit insufficient-evidence result
- AND MUST NOT call the model to invent an answer

### Requirement: REQ-AGENT-002: Untrusted bounded conversation context
The server MUST accept only user/assistant history of at most six turn pairs, 12 messages, 4 KiB per message and 24 KiB aggregate; history and question MUST be treated as untrusted data beneath immutable system policy.

#### Scenario: Contextual follow-up
- GIVEN six or fewer valid turn pairs
- WHEN the user asks a follow-up
- THEN bounded history may resolve references
- AND it cannot change tenant, project, provider or policy

#### Scenario: Client sends a system role
- GIVEN history contains a `system`, `tool` or unknown role
- WHEN validation runs
- THEN the request is rejected with `400 invalid_history_role`

#### Scenario: History exceeds a limit
- GIVEN history exceeds any message, count or aggregate limit
- WHEN submitted
- THEN the request is rejected with `413 history_too_large`
- AND no retrieval/provider call occurs

### Requirement: REQ-AGENT-003: Server-resolved citations
The model MAY reference only opaque handles issued for the current request; the server MUST resolve, authorize and render citations, and MUST NOT return model-invented paths, IDs or URLs as sources.

#### Scenario: Valid mixed sources
- GIVEN the model references issued memory and AST handles
- WHEN the response is finalized
- THEN sources contain sanitized title/type/path/position metadata
- AND every source remains within the verified project scope

#### Scenario: Evidence was truncated
- GIVEN context budgeting excludes a retrieved source
- WHEN the model references that source
- THEN the handle is invalid for the prompt and is removed
- AND confidence is reduced

#### Scenario: Forged or cross-request handle
- GIVEN output contains an unknown or prior-request handle
- WHEN citations are resolved
- THEN it is discarded and audited as invalid metadata
- AND no underlying object is fetched or disclosed
