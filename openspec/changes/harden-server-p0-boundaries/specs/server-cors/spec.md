# Delta for Server CORS

## ADDED Requirements

### Requirement: REQ-CORS-001: Exact Fail-Closed Origin Policy
Server mode MUST emit CORS approval only when the request origin exactly equals a configured HTTP(S) origin. Empty configuration MUST allow no cross-origin request, and wildcard or malformed server origins MUST be rejected during validation.

#### Scenario: Exact configured origin
- GIVEN `https://console.example` is configured
- WHEN a request carries exactly that `Origin`
- THEN the response includes that exact `Access-Control-Allow-Origin`
- AND an allowed preflight receives `204` with bounded methods and headers.

#### Scenario: Similar but different origin
- GIVEN `https://console.example` is configured
- WHEN the origin differs by scheme, host, port or suffix
- THEN no CORS approval header is emitted
- AND the request is not treated as an allowed preflight.

#### Scenario: Unsafe configuration
- GIVEN server configuration contains `*`, userinfo, a path, query, fragment or a non-HTTP(S) origin
- WHEN server configuration is validated
- THEN startup is rejected with a deterministic configuration error
- AND the invalid value is never converted into allow-all behavior.

### Requirement: REQ-CORS-002: Authentication Preservation
CORS processing MUST NOT weaken authentication or authorize an application request.

#### Scenario: Allowed preflight
- GIVEN an allowed browser origin
- WHEN it sends a valid `OPTIONS` preflight
- THEN the server returns only preflight approval
- AND no protected operation executes.

#### Scenario: Allowed origin without credentials
- GIVEN an allowed origin
- WHEN it sends a protected non-OPTIONS request without valid credentials
- THEN the server returns `401`
- AND may retain the exact CORS response header.

#### Scenario: Disallowed preflight
- GIVEN an unlisted origin
- WHEN it sends `OPTIONS` to a protected route
- THEN the server emits no CORS approval
- AND MUST NOT bypass the protected router.

