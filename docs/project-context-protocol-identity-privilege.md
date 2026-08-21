# Project Context Protocol — Identity, Privilege, and Rollout Runbook (IDP-T05)

Scope: the paired Project Context artifact migrations that carry the
identity/privilege mediation contract — SQLite follow-up **003** (registry
version **2003**, `migrations/v2/003_project_artifacts.sql`) and PostgreSQL
**106** (`migrations/v2/106_project_artifacts.sql`). This runbook defines the
checksum preflight, apply, post-apply checks, release ordering, and the
non-destructive rollback policy. It is operator procedure, not application
code: nothing here mutates a ledger or overrides a checksum.

## 1. Checksum identity model

- SQLite follow-ups are ledgered in `cortex_v2_migrations`
  (`version`, `name`, `checksum`); the 001 baseline identity stays frozen in
  `cortex_meta` and is never touched by follow-ups.
- PostgreSQL server migrations 100-106 are ledgered in
  `cortex_server_migrations` (`version`, `name`, `checksum`).
- The ledger stores the exact SHA-256 of the embedded SQL at apply time
  (PostgreSQL hashes LF-normalized SQL). A ledgered checksum is immutable
  evidence: `Apply` treats a mismatching recorded checksum as
  `ErrSchemaTampered` and refuses; nothing ever rewrites a ledger row.
- Immutability pins: `v2_test.go` pins 2001/2002 (shipped; byte-identical
  forever) and 2003 (unshipped; the pin moves with the reviewed bytes until
  its release freezes it). `postgres_test.go` pins 100-105 (immutable
  forever) and 106 (unshipped, moves until release). Editing shipped SQL
  bricks every applied database — the pins make that drift fail loudly in
  unit tests on every platform.

## 2. Read-only ledger preflight

API (in `internal/migration`):

- SQLite 003: `(*V2Baseline).PreflightFollowUp(ctx, db, 2003)`
- PostgreSQL 106: `(*PostgresServerMigration).Preflight(ctx, db)` on the
  version-106 migration from `NewPostgresServerMigrations()`

Both return a `LedgerPreflight` snapshot and its `Verdict()`. The preflight
is strictly read-only: it probes ledger presence (`sqlite_master` /
`to_regclass`), reads at most two rows, never creates the ledger, never
writes, and takes no advisory locks. Unit tests prove zero mutation via
schema snapshots; behavioral PostgreSQL coverage runs in the
`postgres_integration` suite.

**Expected state before rollout: UNLEDGERED.** The verdict table:

| Preflight state | Verdict | Action |
| --- | --- | --- |
| No ledger row for 2003/106 (ledger table absent or row absent) | `nil` | Proceed to apply |
| Row records the CURRENT embedded checksum | `ErrPreflightStop` (already applied) | Stop. Run the post-apply check instead of applying |
| Row records ANY other (prior/pre-release) checksum | `ErrPreflightStop` + `ErrSchemaTampered` | Stop and escalate. Applying would fail closed; never reconcile by editing the ledger |
| Ledger records any version beyond the runtime head (e.g. 2004/107) | `ErrPreflightStop` + `ErrFutureMigration` | Stop. Database was created by a newer runtime; do not run below its head |

Escalation rule: **any recorded checksum for the target version stops the
rollout.** There is no checksum override, no `--force`, and no ledger
mutation path; a prior checksum is reconciled only by a human decision
documented as a new reviewed migration.

### 2.1 Copy/paste executable preflight SQL (operators)

Every query below is read-only. Run them exactly as written; none of them
writes, creates, or locks anything. Each block includes the expected output
and the stop condition for every result shape.

#### SQLite (target 2003)

Open the database with ordinary read-only mode so a hot write-ahead log is
OBSERVED (`mode=ro` forbids writes yet still reads through an existing
`-wal`). Do not use immutable=1 against the live database: immutable tells
SQLite the file cannot change, so a live WAL database can return a stale,
pre-WAL state and misjudge the ledger. immutable=1 is only safe against a
snapshot copy taken after a clean writer shutdown (WAL fully checkpointed
into the copy):

```sh
DB="$HOME/.cortex/v2/cortex.db"
sqlite3 "file:${DB}?mode=ro" "SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'cortex_v2_migrations';"
```

```sql
-- Q1: does the follow-up ledger exist at all?
SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'cortex_v2_migrations';
-- Expected: no row  → ledger ABSENT. TERMINATE here with the UNLEDGERED
--                 verdict: 2003 is unledgered; proceed to apply. Do NOT run
--                 Q2/Q3 — they fail with
--                 "no such table: cortex_v2_migrations" on the absent table.
--        OR: cortex_v2_migrations  (ledger exists — run Q2 and Q3 now)

-- Q2 (only when Q1 returned cortex_v2_migrations): what is recorded for the
-- target 2003?
SELECT checksum FROM cortex_v2_migrations WHERE version = 2003;
-- Expected: <no row>  → UNLEDGERED. This is the required state: proceed to apply.
--        OR: <64-hex equal to the embedded checksum>  → STOP, already applied:
--                 run the post-apply check instead of applying.
--        OR: <any other value, e.g. an older pre-release checksum>
--                 → STOP AND ESCALATE (tamper-class). Applying would fail
--                 closed; never reconcile by editing the ledger.
--
-- The embedded (current) checksum is pinned in
-- internal/migration/v2_test.go (v2HistoricalChecksums[2003]) and is the
-- SHA-256 of migrations/v2/003_project_artifacts.sql with LF endings
-- (CRLF checkouts: `tr -d '\r' < file | sha256sum`). The pin moves with the
-- reviewed bytes until 003 ships; after release it is frozen.

-- Q3 (only when Q1 returned cortex_v2_migrations): did a newer runtime
-- ledger anything beyond this train's head (2003)?
SELECT max(version) FROM cortex_v2_migrations WHERE version > 2003;
-- Expected: NULL (or no row) → proceed.
--        OR: any value (e.g. 2004) → STOP, newer runtime: this database was
--                 created by a NEWER binary; do not run below its head.
```

#### PostgreSQL (target 106)

Connect with `psql` and open a read-only transaction so a stray paste cannot
write (`BEGIN TRANSACTION READ ONLY;` — alternatively use a role that holds
only SELECT on the ledger):

```sql
BEGIN TRANSACTION READ ONLY;

-- Q1: does the server migration ledger exist at all?
SELECT to_regclass('cortex_server_migrations');
-- Expected: NULL → ledger ABSENT. TERMINATE here with the UNLEDGERED
--                 verdict: 106 is unledgered; COMMIT and proceed to apply
--                 (apply runs the full 100-106 line). Do NOT run Q2/Q3 in
--                 this transaction: querying the absent relation raises
--                 ERROR: relation "cortex_server_migrations" does not exist
--                 and ABORTS the read-only transaction.
--        OR: cortex_server_migrations (ledger exists — run Q2 and Q3 now)

-- Q2 (only when Q1 returned cortex_server_migrations): what is recorded for
-- the target 106?
SELECT checksum FROM cortex_server_migrations WHERE version = 106;
-- Expected: <no row>  → UNLEDGERED. This is the required state: proceed to apply.
--        OR: <64-hex equal to the embedded checksum>  → STOP, already applied:
--                 run the post-apply check instead of applying.
--        OR: <any other value> → STOP AND ESCALATE (tamper-class). Applying
--                 would fail closed; never reconcile by editing the ledger.
--
-- The embedded (current) checksum is pinned in
-- internal/migration/postgres_test.go (postgresHistoricalChecksums[106]) and
-- is the SHA-256 of migrations/v2/106_project_artifacts.sql with LF
-- endings. The pin moves with the reviewed bytes until 106 ships.

-- Q3 (only when Q1 returned cortex_server_migrations): did a newer runtime
-- ledger anything beyond this train's head (106)?
SELECT max(version) FROM cortex_server_migrations WHERE version > 106;
-- Expected: NULL → proceed.
--        OR: any value (e.g. 107) → STOP, newer runtime: do not run below
--                 its head.

COMMIT;
```

These operator queries mirror the ledger probes the code-level preflight
executes (`V2Baseline.PreflightFollowUp` probes `sqlite_master` and the
ledger over its open connection; `PostgresServerMigration.Preflight` probes
`to_regclass('cortex_server_migrations')` and the ledger), so a green
manual run and a green code run carry the same meaning. The operator SQLite
DSN deliberately uses plain `mode=ro` (not the code probe's
`immutable=1` file probe): operators preflight a LIVE database whose WAL
must be observed.

## 3. Apply and post-apply checks

Order per database:

1. **Preflight** (section 2). Only proceed on the unledgered verdict.
2. **Apply**:
   - SQLite: startup applies pending follow-ups inside one transaction
     (`V2Baseline.Apply`); 2002 and 2003 DDL and ledger rows commit together
     or not at all.
   - PostgreSQL: `ApplyPostgresServerMigrations` applies 100-106 in order
     under the advisory lock; each migration is one transaction with DDL and
     its ledger row committed together.
3. **Post-apply check**:
   - SQLite 003: `(*V2Baseline).VerifyFollowUpApplied(ctx, db, 2003)` — the
     ledger must record the exact embedded checksum; then
     `VerifyIntegrity` must pass.
   - PostgreSQL 106: `(*PostgresServerMigration).VerifyApplied(ctx, db)` —
     the ledger must record a checksum matching the embedded SQL.
   - A missing row means the apply did not complete; a drifted checksum is
     `ErrSchemaTampered` (SQLite) or a mismatch error (PostgreSQL).
4. **Re-preflight after apply** (optional confirmation): the preflight now
   reports the already-applied stop — that is the correct post-rollout
   state, not a failure.

## 4. Release ordering

1. **Freeze pins before tagging.** 003 and 106 are unshipped: their
   checksum pins in `v2_test.go`/`postgres_test.go` intentionally move with
   reviewed bytes. At release, the tagged bytes become immutable and any
   later edit fails the pin tests.
2. **Ship the pair together.** The SQLite 003 and PostgreSQL 106 trains ride
   the same release as the code that requires them; runtimes never ship a
   migration without the code path that uses it.
3. **Apply order is version order.** SQLite: 2001 → 2002 → 2003.
   PostgreSQL: 100 → … → 106. The runner enforces this; never apply 003/106
   out of order or partially.
4. **No 107 / no 2004 in this train.** IDP-T05 explicitly excludes any next
   migration; `TestPostgresPreflight106ChecksumMatchesPin` and the sequence
   tests pin the heads (106 / 2003).
5. **Gates before release** (CI is authoritative):
   `go test -v -count=1 ./internal/migration`,
   `go test -v -count=1 ./...`, and the tagged suite
   `go test -v -count=1 -tags "integration postgres_integration" ./...`
   with the three `CORTEX_TEST_POSTGRES_*` DSNs.

## 5. Non-destructive rollback

- `Down` is unconditionally forward-only on both lines
  (`ErrForwardOnly`): no DDL, no DML, no ledger erase, no artifact cleanup
  exception. Rollback must never destroy artifact history, receipts, or
  ledger evidence.
- **Before apply** (preflight stop or pre-deploy doubt): simply do not
  apply; the database is untouched by the preflight and by a refused apply
  (transactional all-or-nothing, proven by zero-mutation tests).
- **After apply**: the schema is additive, but an OLDER runtime refuses a
  database ledgered beyond its head (`ErrFutureMigration`, fail closed).
  Therefore rollback after 003/106 is ledgered means *keeping the current
  binary* (or a newer one) — never redeploying an older one over an
  upgraded database. Corrective schema changes go through a NEW reviewed
  migration (the 107+ train), never through ledger edits, checksum
  overrides, or destructive SQL.

## 6. Prohibited operator actions

- Do not edit any shipped migration SQL (001, 002, 100-105) — checksum pins
  and applied databases make this a breaking change.
- Do not `INSERT`/`UPDATE`/`DELETE` against `cortex_v2_migrations` or
  `cortex_server_migrations`. The ledger is append-only evidence written
  only by the migration runner inside its transaction.
- Do not override or bypass checksums. No flag exists; adding one is a
  policy violation, not a feature.
- Do not hand-run 003/106 DDL outside the runner (stale unledgered
  artifacts fail closed by design), and do not register or apply a 107/2004
  migration as part of this train.

## 7. Verification matrix

| Gate | Command | Notes |
| --- | --- | --- |
| Migration package | `go test -v -count=1 ./internal/migration` | Preflight/post-apply/zero-mutation/rejection/pin tests |
| Default suite | `go test -v -count=1 ./...` | CI unit gate |
| Tagged suite | `go test -v -count=1 -tags "integration postgres_integration" ./...` | Needs the three PG DSNs; CI authoritative |
| Pinned lint | golangci-lint v2.11.4 per CI | `--build-tags postgres_integration` for tagged files |
