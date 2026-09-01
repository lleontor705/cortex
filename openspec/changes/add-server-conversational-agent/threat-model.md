# Threat Model

| Threat | Boundary / control | Required oracle |
|---|---|---|
| Cross-tenant/workspace AST disclosure | Composite scope, forced RLS, principal-derived session variables | Sibling-scope rows never appear in query, prompt or citations |
| Arbitrary project/BOLA | Capability check plus exact project grant before retrieval | Unknown and forbidden project return the same sanitized 403 |
| Role-based privilege shortcut | Authorize resources/actions; never test for `RoleAgent` | Viewer with grants succeeds; agent role without grants fails |
| Prompt injection from question/history/evidence | Fixed hierarchy, role rejection, delimiters, no tools | Injection fixture cannot alter policy or retrieve extra scope |
| Citation forgery | Per-request opaque handles resolved server-side | Unknown/foreign handle is discarded and confidence reduced |
| Provider SSRF/secret exposure | Reuse P0 hardened configured client; no request-controlled model/URL/key | Request fields cannot alter provider and errors contain no destination/key |
| Resource exhaustion / spend abuse | Body/context/output caps, tier quotas, concurrency, deadlines, cancellation | Oversize 413, quota 429, timeout 504, disconnect cancels upstream |
| Transcript/privacy leakage | Ephemeral Web state; content-free logs/audit | Logout/new chat clears state; canary content absent from logs/audit |
| Legacy AST contamination | New scoped tables; legacy tables inaccessible; trusted reindex only | Unscoped row cannot rank or cite; incomplete corpus reports degradation |
| Streaming confusion/cache leakage | Auth before headers, no-store, stable event schema, sanitized terminal error | No pre-auth SSE; proxy/client tests preserve event boundaries |
| Indirect writes or tool execution | Completion port has no tools; service ports are read-only except audit | Fakes prove no write method is reachable during either transport |

## Trust Assumptions

Bearer verification, principal grants, P0 CORS/SSRF controls, PostgreSQL migration-role separation and vector tenant/workspace sealing remain authoritative. Reverse proxies must preserve cancellation and disable buffering for the SSE route. Project checkout ingestion is an administrative trusted operation outside the conversational request.

## Abuse and Privacy Policy

Questions, histories, retrieved evidence and model output are sensitive content. They may exist in bounded request memory only and must be released after completion/cancellation. Operational telemetry is metadata-only. The service must prefer “insufficient authorized evidence” over inference when evidence is missing, degraded or conflicting.
