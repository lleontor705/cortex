# Delta for web-ux

## ADDED Requirements

### Requirement: REQ-UX-001: first-boot key entry flow
The web shell MUST offer a first-boot onboarding step: when no stored key exists locally, the user pastes the once-shown web key; on success it is stored client-side (per current AppShell token-storage conventions) and the connection probe turns healthy.

#### Scenario: paste once (Happy path)
- GIVEN serve printed a fresh key at first boot
- WHEN the user pastes it in the key entry screen
- THEN the UI verifies via `/health` + an authenticated probe, stores the key, and never prompts again

#### Scenario: paste with stray whitespace (Edge case)
- GIVEN the pasted value has surrounding whitespace/newlines
- WHEN the UI normalizes it
- THEN verification succeeds and the stored value is trimmed deterministically

#### Scenario: wrong key rejected (Error state)
- GIVEN an incorrect paste
- WHEN verification fails
- THEN the UI shows an actionable error pointing to `cortex web key regenerate` and stores nothing

### Requirement: REQ-UX-002: route boundary components
The App Router MUST provide top-level `error.tsx`, `loading.tsx`, and `not-found.tsx` boundaries using existing design tokens, so client-side route failures render recovery UI instead of white screens.

#### Scenario: slow route transition (Happy path)
- GIVEN a lazily heavy page (e.g. graph)
- WHEN navigation starts
- THEN the loading boundary renders within one frame and the page replaces it on completion

#### Scenario: unmatched URL (Edge case)
- GIVEN a client route that matches no page
- WHEN the SPA fallback serves index.html and routing resolves
- THEN the not-found boundary shows a helpful message with navigation links

#### Scenario: render error (Error state)
- GIVEN a thrown error inside a page tree
- WHEN the error boundary catches it
- THEN it displays the error with a retry action and no stack detail beyond a debug toggle

### Requirement: REQ-UX-003: toast notification system
A lightweight toast provider (context + `useToast` hook, theme-token styled) MUST exist, be mounted in the root layout, and be usable by at least the auth/connection flows.

#### Scenario: success feedback (Happy path)
- GIVEN an action such as key entry success
- WHEN `useToast` fires a success message
- THEN a dismissible toast appears and auto-hides per documented duration

#### Scenario: concurrent toasts (Edge case)
- GIVEN multiple toasts triggered rapidly
- WHEN they stack
- THEN ordering is deterministic, capped, and each remains dismissible

#### Scenario: provider absent (Error state)
- GIVEN a component calls `useToast` outside the provider
- WHEN it renders
- THEN it throws a clear invariant error in development and no-ops safely in production builds

### Requirement: REQ-UX-004: per-domain empty states and a11y lint
The shared `EmptyState` MUST support domain variants (memory, search, graph, projects, sessions) with distinct copy/actions, and `npm run lint` in `web/` MUST run an eslint configuration including jsx-a11y rules as a CI-visible gate.

#### Scenario: empty search results (Happy path)
- GIVEN a query with zero hits
- WHEN the search page renders
- THEN the search-domain empty state suggests refinements instead of a generic blank card

#### Scenario: keyboard navigation (Edge case)
- GIVEN an empty state with action buttons
- WHEN a keyboard-only user tabs through
- THEN focus order is visible and controls announce accessible names

#### Scenario: a11y violation introduced (Error state)
- GIVEN a component with a missing image alt or focusable div
- WHEN `npm run lint` runs
- THEN the lint gate fails naming the rule and file
