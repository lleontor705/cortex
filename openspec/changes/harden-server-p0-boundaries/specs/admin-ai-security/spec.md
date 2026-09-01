# Delta for Admin AI Security

## ADDED Requirements

### Requirement: REQ-AIADMIN-001: Management Authorization Gate
Every `/api/admin/ai/*` operation MUST obtain an audited `ResourceAdmin`/`ActionManage` authorization decision from request-scoped server operations before revealing configuration or invoking a provider.

#### Scenario: Authorized administrator
- GIVEN an authenticated principal with admin management permission
- WHEN it requests AI status or a probe
- THEN authorization is recorded before the operation executes
- AND the existing response contract is returned.

#### Scenario: Authenticated non-administrator
- GIVEN a member or viewer token
- WHEN it requests any `/api/admin/ai/*` route
- THEN the server returns `403` with code `forbidden`
- AND no configuration or provider call is observable.

#### Scenario: Authorization audit failure
- GIVEN the management decision cannot be durably audited
- WHEN an AI administration operation is attempted
- THEN the operation fails closed with a sanitized server error
- AND the probe is not invoked.

### Requirement: REQ-AIADMIN-002: Production-Composed Safe Probes
AI probes MUST be injected from production-composed LLM and embedding dependencies and MUST NOT construct ad-hoc clients, read secrets in handlers, or accept caller-controlled destinations, models or prompts.

#### Scenario: Configured dependency
- GIVEN an authorized administrator and a configured production dependency
- WHEN the corresponding probe runs
- THEN it uses the configured transport policy, timeout and credential source
- AND returns bounded status metadata and latency.

#### Scenario: Provider not configured
- GIVEN an authorized administrator and an absent provider dependency
- WHEN the probe runs
- THEN it returns `not_configured` without outbound network activity.

#### Scenario: Upstream failure
- GIVEN the configured provider times out or returns malformed data
- WHEN the probe runs
- THEN it returns a bounded sanitized failure
- AND MUST NOT expose credentials, raw bodies or internal network details.

