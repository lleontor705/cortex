# Delta for Agent Operations

## ADDED Requirements

### Requirement: REQ-OPS-001: Provider authority and resource budgets
Provider, model, destination and generation bounds MUST come only from validated server administration configuration. Agent requests MUST obey request-size, output, concurrency and token/request quotas keyed by verified tenant and token tier.

#### Scenario: Within budget
- GIVEN a configured hardened provider and available tenant/token budget
- WHEN an authorized request runs
- THEN it uses the configured provider/model and reserves/releases capacity

#### Scenario: Client supplies provider fields
- GIVEN a request includes model, URL, API key, tools or output-limit fields
- WHEN validation runs
- THEN unknown/forbidden fields are rejected with `400 invalid_request`
- AND no outbound connection is made

#### Scenario: Quota exhausted
- GIVEN the token or tenant budget/concurrency is exhausted
- WHEN a request starts
- THEN it returns `429 quota_exceeded` with bounded retry metadata
- AND no retrieval or provider call occurs

### Requirement: REQ-OPS-002: Content-free audit and telemetry
The system MUST audit agent authorization/outcomes using operational metadata and MUST NOT log or audit question, history, answer, retrieved content, secrets, embeddings or provider URLs.

#### Scenario: Successful answer
- GIVEN an answer completes
- WHEN audit is recorded
- THEN it includes correlation ID, actor, scope, transport, duration, token/source counts, confidence and degraded flags
- AND excludes conversational/evidence content

#### Scenario: Degraded retrieval
- GIVEN code or vector retrieval degrades
- WHEN the answer completes
- THEN the degradation reason and result class are observable as metadata

#### Scenario: Audit sink unavailable
- GIVEN audit persistence fails
- WHEN the mandatory pre-provider authorization audit is attempted
- THEN the request fails closed with `503 audit_unavailable`
- AND no provider call occurs and logs remain content-free
