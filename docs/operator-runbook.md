# Operator Runbook — Cortex-IA Maintenance Prerequisites

Audience: the human operator maintaining this OpenCode + Cortex-IA workstation.
Every command below was verified against the installed CLI (`cortex-ia v0.5.3`) with
read-only `--help`/`doctor`/`status` probes. Do not treat remembered syntax as authority:
re-run `cortex-ia help` and update this runbook whenever the CLI surface changes.

Secrets policy: signing keys are operator-provided inputs. Never write a secret value into
any repository file; reference it only as an environment input or a value passed to the CLI.

---

## 1. Host heartbeat / auto-renew (claim and lease maintenance)

### What runs
The OpenCode plugin `~/.config/opencode/plugins/cortex-work.ts` (installed by `cortex-ia`)
starts a maintenance loop the moment `cortex_ia_work_claim` succeeds:

- cadence: `interval_ms: 30000`, host-status probe timeout: `status_timeout_ms: 5000`,
  stale-progress cutoff: `stale_progress_ms: 900000`, renewed TTL: `ttl: "15m"`
- each tick proves the host session is still busy, then executes
  `cortex-ia work controller-renew <task-id> --owner <controller> --authority @stdin --ttl 15m`
  with the claim token and full lease set on stdin.

There is **no** `cortex-ia` subcommand named `host`, `daemon`, or `maintenance`
(verified: `cortex-ia host --help` → `Error: unknown command`). The heartbeat is
plugin-internal and cannot be switched on from the CLI; it is enabled by having the
installed plugin loaded in a live, workspace-bound OpenCode session.

### Symptom
- Task auto-transitions to `blocked` while the claim TTL (15 min) lapses mid-run.
- Claim receipt reports
  `"maintenance":{"active":false,"reason":"host_status_unavailable_manual_renewal_required", ...}`.
- `cortex_ia_work_status` bridge authority shows `write_usable: false` and
  `action: "STOP_WRITING_AND_RECONCILE"`.

### Diagnosis (read-only)
```bash
cortex-ia work status <task-id>          # claim.expires_at, live leases, status, revision
cortex-ia doctor                         # plugin/TUI wiring presence + reporting warnings
cortex-ia --help                         # confirm the installed command surface
```
Via MCP: `cortex_ia_work_status({ "task_id": "<task-id>" })` → `bridge_authority.maintenance`
returns `{active, reason, interval_ms, status_timeout_ms, stale_progress_ms, ttl}`.

Maintenance stop reasons and their meaning:

| reason | meaning |
|---|---|
| `host_status_unavailable_manual_renewal_required` | The plugin could not call the host `session.status` API or the session has no workspace directory — typically a stale/missing plugin versus OpenCode host version mismatch. |
| `host_idle_or_unknown` | Host reported the session idle/unknown; renewal intentionally stops. |
| `stale_progress` | No new host message parts for `stale_progress_ms` (15 min); renewal stops. |
| `maintenance_failed_manual_reconciliation_required` | A `controller-renew` tick failed or timed out; manual reconciliation is required. |

### Remediation
1. Verify the plugin is installed and current in the OpenCode root:
   ```bash
   cortex-ia doctor
   cortex-ia sync --target opencode --dry-run   # preview drift (read-only)
   cortex-ia sync --target opencode             # apply: re-register the plugin and theme
   ```
   (`cortex-ia install --target opencode --overwrite` is the equivalent first-install path;
   it prompts for confirmation and captures a backup.)
2. Keep the OpenCode server/session alive and bound to the workspace directory — the
   heartbeat only starts at claim time, so re-claim after fixing.
3. Confirm the next claim receipt reports `"maintenance":{"active":true}` (no `reason`).

### Minion-side mitigation (dual renewal cadence < 10 min)
Independently of the host loop, the executing minion renews on a dual cadence strictly
below the 15-minute TTL:

- `cortex_ia_work_renew({ "task_id": ..., "ttl": "15m" })`
- `cortex_ia_work_lease_renew({ "task_id": ..., "path": ..., "ttl": "15m" })`

CLI equivalents (claim/lease tokens are hidden process memory — never print them):
```bash
cortex-ia work renew <task-id> --claim-token <token> [--ttl <duration>]
cortex-ia work lease-renew --path <file> --lease-token <token>
cortex-ia work controller-renew <task-id> --owner <owner> --authority @stdin   # claim + all leases in one call
```
Any renewal failure is authority loss: stop writing immediately, preserve the diff, and
transition the task to `blocked`.

---

## 2. Error-report signing secret

### Symptom
- `cortex_ia_report_error` fails with
  `authenticated reporting requires a configured signing secret: run "cortex-ia report config --secret <KEY>" or install a release binary, which embeds it`
  (every unsigned report is rejected by the hub with HTTP 401).
- `cortex-ia report status` shows the signing secret as not configured.
- `cortex-ia doctor` emits: `error reporting has no signing secret ... run "cortex-ia report config --secret <KEY>"`.

### Command
```bash
cortex-ia report config [--endpoint <url>] [--secret <key>] [--enable|--disable]
cortex-ia report status       # current endpoint/enablement/signing configuration
cortex-ia report flush        # retry queued reports after fixing the secret
```

### Key generation and delivery
The key is an HMAC signing secret shared with the report hub; it is operator-provided input:
```bash
openssl rand -hex 32
cortex-ia report config --secret <KEY>          # <KEY> is the generated value, never committed
```
Alternative (source builds persist it on first configuration): export
`CORTEX_REPORT_SECRET` (and optionally `CORTEX_REPORT_ENDPOINT`) in the operator
environment; the CLI reports `error reporting signing secret persisted from CORTEX_REPORT_SECRET`
when it bootstraps `~/.cortex-ia/telemetry.json` from the environment.

### Where the key lives
- `~/.cortex-ia/telemetry.json` with fields `endpoint`, `secret`, `enabled`;
  file mode `0600` inside a `0700` directory.
- Release binaries embed the canonical secret at link time, so an embedded secret is
  never written to that file.
- Precedence: `CORTEX_REPORT_ENDPOINT` (environment) overrides the file endpoint; secret
  resolution order is environment (`CORTEX_REPORT_SECRET`) → file → embedded release secret.
- Never store the value in the repository, in task contracts, or in Cortex observations.

### Verification
```bash
cortex-ia report status
cortex-ia doctor
```
Manual probe (sends a real report to the configured endpoint — use only when intentionally
testing delivery):
```bash
cortex-ia report error --code <code> --message <msg> [--details <text|@stdin>] [--task <id>] [--job <id>] [--source <source>]
```

---

## 3. Ollama wedged state

### Symptom
- Nothing listening on `127.0.0.1:11434`; `ollama version` / `ollama list` hangs;
  the Ollama Electron app is inert; model-backed tools time out.

### Diagnosis (read-only)
```bash
lsof -nP -iTCP:11434 -sTCP:LISTEN
pgrep -fl ollama
curl -sS -m 5 http://127.0.0.1:11434/api/tags
```

### Remediation
```bash
pkill -f ollama                 # terminate the wedged serve process
open -a Ollama                  # macOS relaunch (or: ollama serve)
curl -sS -m 5 http://127.0.0.1:11434/api/tags   # must return a JSON model list
ollama list                     # confirms models are visible again
```

### macOS notes
- A relaunch after `pkill` can trigger a TCC prompt (files/network); approve it, or grant
  it under System Settings → Privacy & Security.
- Binaries or app bundles downloaded outside the App Store may be Gatekeeper-quarantined;
  approve them under System Settings → Privacy & Security → Open Anyway.

---

## 4. Claim-TTL recovery runbook (orchestrator reconciliation)

Sequence for a task that lost authority because the claim TTL lapsed:

1. **Read state** (read-only):
   ```bash
   cortex-ia work status <task-id>    # status, revision, claim.expires_at, leases
   ```
   MCP: `cortex_ia_work_status({ "task_id": "<task-id>" })` → `bridge_authority.action`.
2. **Sweep expired authority**:
   ```bash
   cortex-ia work recover
   ```
   Moves `in_progress` tasks with an expired claim to `blocked` (event `claim_expired`)
   and deletes expired leases. Note: `in_review` tasks stay approvable — only abandoned
   reviews older than the staleness window are recovered.
3. **Release an orphaned but still-live claim** (owner session is dead):
   ```bash
   cortex-ia work reconcile <task-id> --reason <text> --session <host-session-id> --revision <n> [--to ready] [--owner-session-inactive <true|false>]
   ```
   Fails when the claim is held by a live session; use it only for orphans.
4. **Retry with CAS revision**:
   ```bash
   cortex-ia work retry <task-id> --revision <n>
   ```
   Preconditions: status `blocked`, exact current revision (mismatch → `stale retry revision`
   / `task is blocked at revision X, not Y`), all dependencies `done`, no atomic decomposition,
   attempt budget remaining, and fewer than two consecutive review `FAIL` verdicts.
   Effect: clears claims/leases/reviews and sets the task to `ready`.
5. **Re-dispatch**: the new minion claims fresh authority with a 15-minute TTL and resumes
   the dual renewal cadence (< 10 min).
6. **When to use a BLOCKED-approve**:
   ```bash
   cortex-ia work approve <task-id> --reviewer <id> [--revision <n>] --verdict <PASS|FAIL|BLOCKED|INCONCLUSIVE> [--evidence <ref>]
   ```
   - The task must be `in_review`; any non-`PASS` verdict moves it to `blocked` and releases
     its claims/leases; `PASS` requires `--evidence` and moves it to `done`.
   - Use `BLOCKED` when review cannot legitimately conclude `PASS` or `FAIL` (authority loss
     mid-review, missing evidence, unverifiable submission). `BLOCKED` and `INCONCLUSIVE` are
     skipped by the two-consecutive-`FAIL` decomposition breaker, so a blocked-approve records
     the outcome without consuming the review-failure streak.
   - After a `BLOCKED`-approve the task is `blocked` again: fix the cause, then resume at
     step 4 (`retry --revision <n>` with the new revision).

Dual-renewal discipline throughout: renew the claim and every file lease together on a
< 10-minute cadence; treat any failed renewal as immediate authority loss.

---

## 5. `cortex_save` degraded — "write could not be persisted"

### Symptom
`cortex_save` returns the contract failure `write could not be persisted`
(`internal/mcp/memorycontract/memorycontract.go`).

### Probable cause
Concurrent sessions contending for the Cortex SQLite database (lock/busy on the writer)
or a store write error while another session holds the DB.

### Remediation
- Retry the same `cortex_save` once after a short pause; saves are upserts keyed by
  `topic_key`, so a retry is idempotent.
- If it recurs, stagger writes: serialize memory saves through a single session or schedule
  them instead of firing from parallel subagents at the same moment.
- Check for a stuck `cortex watch` daemon or another session holding the DB before
  escalating; never retry in a tight loop.

---

## Maintenance note
This runbook mirrors the installed CLI surface (verified: `cortex-ia --help`,
`cortex-ia work --help`, `cortex-ia report --help`, `cortex-ia doctor`,
`cortex-ia work status`, `cortex-ia report status`). If a future `cortex-ia` release renames
or removes a command, update this file in the same change; stale instructions must never be
treated as authority over `--help`.
