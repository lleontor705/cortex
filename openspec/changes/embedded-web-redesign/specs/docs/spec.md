# Delta for docs

## ADDED Requirements

### Requirement: REQ-DOC-001: embedded web documentation
README and a new `docs/embedded-web.md` MUST document the single-binary web surface: first-boot key lifecycle, `cortex web key show|regenerate`, runtime endpoint injection, local/server/hybrid mounting, and Makefile `web-build` ordering; AGENTS.md gains the new command surface.

#### Scenario: operator onboards from docs (Happy path)
- GIVEN a fresh user following the docs only
- WHEN they boot serve and open the UI
- THEN every step (key display, entry, regeneration) is covered without reading source

#### Scenario: mode-specific differences (Edge case)
- GIVEN local vs server vs hybrid UI behavior
- WHEN documented
- THEN each mode's web capability is stated precisely and matches REQ-MODE-001 semantics

#### Scenario: stale command docs (Error state)
- GIVEN docs describe a removed web container or non-existent command
- WHEN review greps the documented commands
- THEN the task fails until docs match the shipped CLI

### Requirement: REQ-DOC-002: SaaS control-plane gap review
`docs/server-saas.md` MUST be extended with an architecture review that inventories the existing data plane (multi-tenant verifier, RLS, BOLA, audit, per-token rate tiers) and the deliberate absences (provisioning control plane, billing/entitlements, SSO lifecycle, retention, PITR/WAL archiving, distributed limiter) with bounded hardening recommendations; no billing implementation is introduced.

#### Scenario: gap matrix complete (Happy path)
- GIVEN the review section
- WHEN a stakeholder reads it
- THEN every present/absent capability is traceable to a code or doc reference in-repo

#### Scenario: hardening bounded (Edge case)
- GIVEN a recommendation such as distributed rate limiting
- WHEN scope is stated
- THEN it names the seam (e.g. process-local `agentQuotaLimiter`) rather than inventing new services

#### Scenario: scope creep detection (Error state)
- GIVEN the change adds billing or SSO code paths
- WHEN review compares against this delta
- THEN the work is rejected as violating the proposal non-goals
