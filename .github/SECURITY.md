# Security Policy

## Supported Versions

Security fixes are applied to the latest published release only. Older tags are
not backported.

| Version                       | Supported |
| ----------------------------- | --------- |
| Latest release (see [tags](https://github.com/lleontor705/cortex/tags)) | ✅ |
| Older releases                | ❌ |

Report against the newest tag (or the `main` commit if the issue is not yet
released) so the report can be reproduced on supported code.

## Reporting a Vulnerability

Report vulnerabilities privately through **GitHub Security Advisories**:

https://github.com/lleontor705/cortex/security/advisories/new

- Do not open a public issue, discussion, or pull request for an exploitable
  vulnerability — GitHub's private advisory flow keeps the report undisclosed
  until a fix ships.
- This repository has no dedicated security mailbox; the advisory workflow is
  the supported private channel and routes to the repository owner.
- Include as much of the following as possible:
  - Affected component: the `cortex` binary (CLI/TUI), the MCP server
    (local `serve` or `--mode server`), or the web frontend (`web/`).
  - Version tag or commit hash.
  - Reproduction steps, proof-of-concept, and the impact you observed
    (for example data exposure, unauthorized access, or command execution).

## Response Expectations

- **Acknowledgement**: within 7 days of filing the advisory.
- **Triage update**: a severity assessment and whether the report is in scope
  within 14 days.
- **Status cadence**: updates at least every 14 days until resolution.
- **Fix and disclosure**: a patched release and coordinated disclosure follow
  the fix; if a report is declined, the reasoning is stated in the advisory.
  Target full disclosure within 90 days of the initial report, adjusted by
  severity and fix complexity.

## Scope

In scope:

- The `cortex` binary: CLI commands, TUI, and local SQLite composition.
- The MCP server: local MCP tool surface and the authenticated server mode
  (`cortex --mode server`), including its bearer authentication boundary.
- The web frontend under `web/`, including authorization (BOLA) and session
  handling.

Out of scope:

- Vulnerabilities in third-party dependencies with no Cortex-specific
  exposure — report them upstream; an advisory here is still welcome for
  tracking once a fix is available.
- Social engineering, phishing, and physical attacks against contributors or
  infrastructure.
- Denial-of-service through pure network volumetry or attacks requiring
  attacker control of infrastructure outside the affected component.
- Reports against unsupported releases (see Supported Versions).
