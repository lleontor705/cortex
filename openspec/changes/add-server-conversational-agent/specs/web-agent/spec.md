# Delta for Web Agent

## ADDED Requirements

### Requirement: REQ-WEB-001: Granted-project conversational UI
The Web MUST provide `/agent` only to authenticated users and MUST populate its selector exclusively from server-returned eligible projects; it MUST NOT infer access from role names or accept arbitrary local project values.

#### Scenario: Ask about granted project
- GIVEN the server returns one or more eligible projects
- WHEN the user selects one and asks a question
- THEN the UI submits its canonical project identifier and renders answer, confidence, degradation and sources

#### Scenario: No eligible projects
- GIVEN the authenticated principal has no eligible project
- WHEN `/agent` loads
- THEN the composer is disabled with an accessible explanation
- AND no fallback/default project is invented

#### Scenario: Stale project grant
- GIVEN a previously selected project is removed from the refreshed eligible list
- WHEN the next request is attempted
- THEN selection and conversation are cleared
- AND the UI requires a currently granted project

### Requirement: REQ-WEB-002: Ephemeral accessible chat lifecycle
The Web MUST keep at most six turn pairs in memory, persist no transcript, cancel work on Stop/logout/unmount, and provide keyboard- and screen-reader-accessible status and source navigation.

#### Scenario: Progressive answer
- GIVEN SSE is available
- WHEN response deltas arrive
- THEN an `aria-live` region announces bounded status without stealing focus
- AND source cards become keyboard reachable after completion

#### Scenario: JSON compatibility
- GIVEN streaming is unavailable before response start
- WHEN the user retries in JSON mode
- THEN the same canonical result is rendered without duplicating the user turn

#### Scenario: Logout during generation
- GIVEN an answer is in flight with conversation state present
- WHEN authentication is invalidated or the user logs out
- THEN the request is aborted and transcript/source state is cleared
- AND nothing is written to localStorage, sessionStorage or IndexedDB
