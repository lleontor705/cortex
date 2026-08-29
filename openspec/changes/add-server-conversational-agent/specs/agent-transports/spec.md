# Delta for Agent Transports

## ADDED Requirements

### Requirement: REQ-TRANSPORT-001: Equivalent JSON and SSE contracts
`POST /api/agent/answer` and `POST /api/agent/stream` MUST invoke the same application service and preserve identical authorization, answer, source, confidence and degradation semantics.

#### Scenario: Complete JSON response
- GIVEN a valid authenticated request
- WHEN `/api/agent/answer` completes
- THEN it returns one canonical `AgentAnswer` JSON object
- AND sets `Cache-Control: no-store`

#### Scenario: Progressive SSE response
- GIVEN the same request is sent to `/api/agent/stream`
- WHEN generation progresses
- THEN ordered `meta`, `delta`, `sources`, and `done` events are flushed
- AND `done` contains canonical metadata equivalent to JSON

#### Scenario: Failure before authorization
- GIVEN missing, revoked or insufficient credentials
- WHEN either endpoint is called
- THEN it returns the same sanitized HTTP authorization error
- AND no SSE headers, retrieval or provider call are started

### Requirement: REQ-TRANSPORT-002: Cancellation and stable failure behavior
Both transports MUST propagate request cancellation and deadlines through retrieval and provider calls and MUST expose stable error classifications without upstream bodies, secrets or provider destinations.

#### Scenario: Client cancels stream
- GIVEN an active SSE response
- WHEN the client disconnects or presses Stop
- THEN server context cancellation reaches all in-flight work
- AND no further event or audit content is emitted beyond terminal metadata where possible

#### Scenario: Provider fails after SSE starts
- GIVEN authorized sources were retrieved and streaming began
- WHEN the provider fails
- THEN a sanitized `error` event terminates the stream
- AND no partial answer is represented as complete

#### Scenario: Deadline exceeded
- GIVEN the configured JSON or SSE deadline expires
- WHEN work remains in flight
- THEN it is canceled and classified `504 agent_timeout`
- AND capacity/quota reservations are released
