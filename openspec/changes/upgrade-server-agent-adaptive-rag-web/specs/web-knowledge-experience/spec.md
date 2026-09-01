# Delta for Web Knowledge Experience

## ADDED Requirements

### Requirement: REQ-WEB-KNOW-001: Scoped agent workspace
`/agent` MUST present the authorized project as a persistent scope boundary and show answer progress, selected tier, confidence, degradation and cited sources without implying that model prose is source truth.

#### Scenario: Adaptive answer
- GIVEN an eligible project and valid question
- WHEN retrieval streams
- THEN the UI shows concise stage progress and renders the final answer beside its sources

#### Scenario: Narrow viewport
- GIVEN a 320 CSS-pixel viewport
- WHEN chat and sources render
- THEN content reflows without horizontal scrolling and the composer remains reachable

#### Scenario: Grant revoked
- GIVEN the selected project becomes unauthorized
- WHEN a request returns the stable denial
- THEN conversation and scope are cleared and no stale evidence remains visible

### Requirement: REQ-WEB-KNOW-002: Transparent search workspace
`/search` MUST preserve direct knowledge exploration while clearly identifying selected project scope, memory/code source type, active retrieval capabilities, index state and result provenance.

#### Scenario: Mixed search
- GIVEN a selected eligible project and query
- WHEN memory and code results return
- THEN grouped results disclose source type, project and available status metadata

#### Scenario: Empty or degraded corpus
- GIVEN one corpus is empty, stale or unavailable
- WHEN search completes
- THEN the UI distinguishes no matches from unavailable indexing and offers an actionable recovery message

#### Scenario: Invalid local scope
- GIVEN a stale or arbitrary local project value
- WHEN search is attempted
- THEN the client clears it and requires a server-returned eligible project

### Requirement: REQ-WEB-KNOW-003: Accessible resilient interaction
Both pages MUST support keyboard operation, visible focus, semantic headings/landmarks, non-disruptive `aria-live` status, reduced motion, light/dark variables, cancellation and retry. They MUST NOT persist questions, answers or sources in browser storage.

#### Scenario: Keyboard workflow
- GIVEN keyboard-only navigation
- WHEN scope, mode, submit, stop and sources are used
- THEN focus order is logical and every action has an accessible name

#### Scenario: Reduced motion
- GIVEN `prefers-reduced-motion: reduce`
- WHEN progress or panels update
- THEN nonessential animation is disabled without hiding state changes

#### Scenario: Logout during work
- GIVEN either page has in-flight work or rendered sensitive state
- WHEN authentication ends
- THEN requests abort and all scoped knowledge state is cleared
