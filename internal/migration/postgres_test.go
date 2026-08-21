package migration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// postgresHistoricalChecksums pins the canonical (LF-normalized) SHA-256 of
// every historical PostgreSQL migration SQL. Applied databases ledger the
// embedded checksum and fail closed on mismatch, so editing any of these
// files bricks existing databases; these literals make such drift visible in
// plain unit tests on every platform (LF or CRLF checkout).
var postgresHistoricalChecksums = map[int]string{
	100: "e9fe69b75aceab783a44faaba473e31379423e59042a502423319e3ea2e60339",
	101: "ab40aa393397641dc2e7a783cb4227e01528431e67dcd79ad590819d6f159e8b",
	102: "8ada074feeca3d8741287e96eb6f54ee1834e9bb1f35b2678b81d50b6219e107",
	103: "c45c94890e5be93326df36b1c00307af502f0a70f6ab014162e6a4220a1e76e0",
	104: "52c21c6a60b8d3912ad1ca004613f0de762e43307db95774d41f0e184fb82234",
	105: "63ff0c1498387bf42e42728662c7bd89ca65d103c881340bd23357829e682127",
	// 106 is pinned at its reviewed fixed contract (grants-derived trusted
	// principal scope with stale clearing and hardened grant privileges,
	// workspace defaults, authoritative activation pointer, exact same-
	// coordinate replay, least privilege, mediated identity with the
	// token-bound binder proof, the owner/admin-mediated api_tokens
	// lifecycle with in-SQL digest derivation, frozen usage coordinates,
	// and the migration-role-only bootstrap service reconciler with its
	// least-privilege actor_subjects definer prerequisites); baselines
	// 100-105 above must remain byte-identical forever. 106 is still
	// unshipped, so its pin moves with the reviewed bytes until release.
	106: "aa27c931a3241322298acbeb2a823b8c8feab0287deb7f95900f355c13fdef28",
	// 107 is the workspace-safe sync identity closure (SEC-03) of the
	// tools-security-performance-hardening train, created after the T01
	// sibling-workspace exploit proof: prompts and edges are bound to a
	// workspace through their durable chains, edge endpoints must share one
	// workspace, and the tenant-wide observation/prompt/edge client_id
	// unique indexes are swapped for workspace-scoped replacements after
	// fail-closed duplicate/orphan preflights that never merge or drop a
	// collision. Still unshipped, so its pin moves with the reviewed bytes
	// until release.
	107: "821caa597df812cc6e3993f80881878335abb54d3324979b61d782630512896a",
	// 108 is the canonical SRW principal read/write gating of the
	// tools-performance-scalability-r1 train (PG-00/PG-01/PG-02, MIG-01),
	// created after the T01 lock-spike PASS: one canonical
	// cortex_principal_key advisory namespace, shared-gated verify/bind
	// FOR SHARE revalidation, exclusive-gated identity invalidators with
	// lock-free key resolves and locked revalidation, and throttled
	// non-authoritative last_used_at telemetry. Purely additive CREATE OR
	// REPLACE over 100/106 routines. Still unshipped, so its pin moves
	// with the reviewed bytes until release.
	108: "7eb15b2554fb13f3b3f8558455dafa99b568f0c07e94600f6b47f3598893e306",
}

// mustPostgresMigrations loads the full PostgreSQL migration line or fails
// the test. Shared by unit tests and the postgres_integration lifecycle test.
func mustPostgresMigrations(t *testing.T) []*PostgresServerMigration {
	t.Helper()
	migrations, err := NewPostgresServerMigrations()
	if err != nil {
		t.Fatalf("NewPostgresServerMigrations: %v", err)
	}
	return migrations
}

// canonicalChecksum hashes SQL with CRLF normalized to LF so pinned literals
// are byte-identical on LF and CRLF checkouts.
func canonicalChecksum(sql string) string {
	normalized := strings.ReplaceAll(sql, "\r\n", "\n")
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

func TestPostgresServerMigrationMetadata(t *testing.T) {
	m, err := NewPostgresServerMigration()
	if err != nil {
		t.Fatalf("NewPostgresServerMigration: %v", err)
	}
	if m.Version() != 100 || m.Name() != "server" {
		t.Fatalf("migration identity = %d/%q, want 100/server", m.Version(), m.Name())
	}
	if len(m.SQL()) < 1000 || m.Checksum() == "" {
		t.Fatal("embedded server migration or checksum is empty")
	}
}

func TestPostgresServerMigrationSequence(t *testing.T) {
	migrations := mustPostgresMigrations(t)
	if len(migrations) != 9 {
		t.Fatalf("migration count = %d, want 9", len(migrations))
	}
	for i, want := range []int{100, 101, 102, 103, 104, 105, 106, 107, 108} {
		if migrations[i].Version() != want {
			t.Fatalf("migration %d version = %d, want %d", i, migrations[i].Version(), want)
		}
	}
	for _, token := range []string{"principal_grants", "assertion_kind", "normalized_value"} {
		if !strings.Contains(migrations[1].SQL(), token) {
			t.Errorf("migration 101 missing %q", token)
		}
	}
	for _, token := range []string{"sync_changes", "client_id", "cortex_record_sync_change"} {
		if !strings.Contains(migrations[2].SQL(), token) {
			t.Errorf("migration 102 missing %q", token)
		}
	}
	for _, token := range []string{"cortex_default_sync_client_id", "BEFORE INSERT"} {
		if !strings.Contains(migrations[3].SQL(), token) {
			t.Errorf("migration 103 missing %q", token)
		}
	}
	for _, token := range []string{"handoff_receipts", "tenant_id", "cortex_current_tenant()"} {
		if !strings.Contains(migrations[4].SQL(), token) {
			t.Errorf("migration 104 missing %q", token)
		}
	}
	for _, token := range []string{"workspace_id", "workspaces(tenant_id, id)", "handoff_receipts"} {
		if !strings.Contains(migrations[5].SQL(), token) {
			t.Errorf("migration 105 missing %q", token)
		}
	}
	for _, token := range []string{"project_artifacts", "cortex_project_isolation", "project_storage_usage"} {
		if !strings.Contains(migrations[6].SQL(), token) {
			t.Errorf("migration 106 missing %q", token)
		}
	}
	for _, token := range []string{"cortex_bind_prompt_workspace", "cortex_bind_edge_workspace", "workspace_id, client_id"} {
		if !strings.Contains(migrations[7].SQL(), token) {
			t.Errorf("migration 107 missing %q", token)
		}
	}
	for _, token := range []string{"cortex_principal_key", "pg_advisory_xact_lock_shared", "pg_try_advisory_xact_lock"} {
		if !strings.Contains(migrations[8].SQL(), token) {
			t.Errorf("migration 108 missing %q", token)
		}
	}
}

// TestPostgresHistoricalMigrationsAreImmutable pins the canonical SHA-256 of
// the historical server SQL (100-105). They MUST remain byte-identical:
// applied databases refuse checksum mismatches, and rewriting history would
// be a destructive schema change (REM-MIG-001).
func TestPostgresHistoricalMigrationsAreImmutable(t *testing.T) {
	migrations := mustPostgresMigrations(t)
	for version, pinned := range postgresHistoricalChecksums {
		var found bool
		var sql string
		for _, migration := range migrations {
			if migration.Version() == version {
				found, sql = true, migration.SQL()
				break
			}
		}
		if !found {
			t.Fatalf("historical migration %d missing from the migration line", version)
		}
		if got := canonicalChecksum(sql); got != pinned {
			t.Errorf("historical migration %d SQL changed: canonical sha256=%s, pinned=%s", version, got, pinned)
		}
	}
}

// TestPostgresServerMigration104HandoffReceipts verifies the additive 104
// follow-up: tenant-scoped receipts, composite tenant key, RESTRICT FK, and
// RLS isolation aligned with the 100 baseline policy.
func TestPostgresServerMigration104HandoffReceipts(t *testing.T) {
	migrations := mustPostgresMigrations(t)
	var subject *PostgresServerMigration
	for _, migration := range migrations {
		if migration.Version() == 104 {
			subject = migration
			break
		}
	}
	if subject == nil {
		t.Fatal("migration 104 (handoff receipts) is not registered")
	}
	if subject.Name() != "handoff_receipts" {
		t.Errorf("migration 104 name = %q, want handoff_receipts", subject.Name())
	}
	if subject.Checksum() == "" {
		t.Error("migration 104 checksum is empty")
	}
	for _, token := range []string{
		"CREATE TABLE handoff_receipts",
		"tenant_id uuid NOT NULL",
		"PRIMARY KEY (tenant_id, scope, key)",
		"REFERENCES observations(tenant_id, id) ON DELETE RESTRICT",
		"FORCE ROW LEVEL SECURITY",
		"cortex_current_tenant()",
	} {
		if !strings.Contains(subject.SQL(), token) {
			t.Errorf("migration 104 missing %q", token)
		}
	}
	if strings.Contains(strings.ToLower(subject.SQL()), "drop table") {
		t.Error("migration 104 contains DROP TABLE; follow-ups must be additive")
	}
	if strings.Contains(subject.SQL(), "CREATE TABLE IF NOT EXISTS handoff_receipts") {
		t.Error("migration 104 uses IF NOT EXISTS; a stale unledgered artifact must fail closed, not be adopted")
	}
}

// TestPostgresServerMigration105WorkspaceBinding verifies the workspace
// binding follow-up: fail-closed backfills, NOT NULL composite tenant/workspace
// foreign keys, the workspace-scoped topic uniqueness swap, and the
// workspace-scoped handoff receipt primary key (AMD-MIG-105).
func TestPostgresServerMigration105WorkspaceBinding(t *testing.T) {
	migrations := mustPostgresMigrations(t)
	var subject *PostgresServerMigration
	for _, migration := range migrations {
		if migration.Version() == 105 {
			subject = migration
			break
		}
	}
	if subject == nil {
		t.Fatal("migration 105 (workspace binding) is not registered")
	}
	if subject.Name() != "workspace_binding" {
		t.Errorf("migration 105 name = %q, want workspace_binding", subject.Name())
	}
	if subject.Checksum() == "" {
		t.Error("migration 105 checksum is empty")
	}
	for _, token := range []string{
		// Column adds before the fail-closed backfills.
		"ALTER TABLE observations ADD COLUMN workspace_id bigint",
		"ALTER TABLE handoff_receipts ADD COLUMN workspace_id bigint",
		// Backfills derive workspace exactly from the durable chain
		// session -> observation -> receipt; leftovers abort the migration.
		"UPDATE observations o",
		"SET workspace_id = s.workspace_id",
		"UPDATE handoff_receipts r",
		"SET workspace_id = o.workspace_id",
		"RAISE EXCEPTION",
		// 104-era observation DML keeps working: the BEFORE trigger derives
		// the workspace from the session and rejects explicit mismatches.
		"cortex_bind_observation_workspace",
		"BEFORE INSERT OR UPDATE OF session_id, workspace_id ON observations",
		// Hard constraints after the backfill.
		"ALTER TABLE observations ALTER COLUMN workspace_id SET NOT NULL",
		"ALTER TABLE handoff_receipts ALTER COLUMN workspace_id SET NOT NULL",
		"REFERENCES workspaces(tenant_id, id)",
		// Durable pending receipts cannot resolve a workspace and must fail
		// closed instead of guessing a tenant-wide default.
		"state = 'pending'",
		// Workspace-scoped idempotency namespaces.
		"ADD PRIMARY KEY (tenant_id, workspace_id, scope, key)",
		// Workspace-scoped active topic uniqueness: create the new partial
		// index, validate duplicates, retire the tenant-wide index, rename.
		"CREATE UNIQUE INDEX observations_topic_key_active_ws_uq",
		"(tenant_id, workspace_id, project_key, topic_key)",
		"WHERE topic_key IS NOT NULL AND deleted_at IS NULL",
		"DROP INDEX observations_topic_key_active_uq",
		"ALTER INDEX observations_topic_key_active_ws_uq RENAME TO observations_topic_key_active_uq",
	} {
		if !strings.Contains(subject.SQL(), token) {
			t.Errorf("migration 105 missing %q", token)
		}
	}
	for _, banned := range []string{"DROP TABLE", "TRUNCATE", "DELETE FROM observations", "DELETE FROM handoff_receipts", "DROP DATABASE"} {
		if strings.Contains(strings.ToUpper(subject.SQL()), banned) {
			t.Errorf("migration 105 contains destructive statement %q", banned)
		}
	}
}

// TestPostgresServerMigration107WorkspaceSync verifies the additive 107
// follow-up: fail-closed preflights before any mutation, session- and
// from-observation-derived workspace backfills for prompts and edges, the
// binding triggers with explicit-mismatch rejection, composite tenant/
// workspace foreign keys, and the workspace-scoped client_id uniqueness swap
// for observations, prompts, and edges (SEC-03 schema closure).
func TestPostgresServerMigration107WorkspaceSync(t *testing.T) {
	migrations := mustPostgresMigrations(t)
	var subject *PostgresServerMigration
	for _, migration := range migrations {
		if migration.Version() == 107 {
			subject = migration
			break
		}
	}
	if subject == nil {
		t.Fatal("migration 107 (workspace sync) is not registered")
	}
	if subject.Name() != "workspace_sync" {
		t.Errorf("migration 107 name = %q, want workspace_sync", subject.Name())
	}
	if subject.Checksum() == "" {
		t.Error("migration 107 checksum is empty")
	}
	for _, token := range []string{
		// Preflights run before any mutating statement: orphans,
		// cross-workspace edges, and duplicate client_id groups abort.
		"prompt(s) reference no session",
		"edge(s) reference no observation",
		"edge(s) cross workspaces",
		"observation client_id group(s) collide inside a workspace",
		"prompt client_id group(s) collide inside a workspace",
		"edge client_id group(s) collide inside a workspace",
		// Column adds before the fail-closed backfills.
		"ALTER TABLE prompts ADD COLUMN workspace_id bigint",
		"ALTER TABLE edges ADD COLUMN workspace_id bigint",
		// Backfills derive the workspace exactly from the durable chains.
		"UPDATE prompts p",
		"SET workspace_id = s.workspace_id",
		"UPDATE edges e",
		"SET workspace_id = o.workspace_id",
		"RAISE EXCEPTION",
		// Legacy DML keeps working: BEFORE triggers derive the workspace
		// and reject explicit mismatches.
		"cortex_bind_prompt_workspace",
		"BEFORE INSERT OR UPDATE OF session_id, workspace_id ON prompts",
		"prompt workspace % conflicts with session workspace %",
		"cortex_bind_edge_workspace",
		"BEFORE INSERT OR UPDATE OF from_observation_id, to_observation_id, workspace_id ON edges",
		"edge endpoints must share one workspace (from %, to %)",
		"edge workspace % conflicts with from-observation workspace %",
		// Hard constraints after the backfill.
		"ALTER TABLE prompts ALTER COLUMN workspace_id SET NOT NULL",
		"ALTER TABLE edges ALTER COLUMN workspace_id SET NOT NULL",
		"ADD CONSTRAINT prompts_tenant_workspace_fkey",
		"ADD CONSTRAINT edges_tenant_workspace_fkey",
		"REFERENCES workspaces(tenant_id, id)",
		// Workspace-scoped client_id uniqueness: create replacements,
		// validate, retire the tenant-wide indexes, rename into place.
		"CREATE UNIQUE INDEX observations_client_id_ws_uq",
		"CREATE UNIQUE INDEX prompts_client_id_ws_uq",
		"CREATE UNIQUE INDEX edges_client_id_ws_uq",
		"ON observations(tenant_id, workspace_id, client_id)",
		"ON prompts(tenant_id, workspace_id, client_id)",
		"ON edges(tenant_id, workspace_id, client_id)",
		"WHERE client_id IS NOT NULL",
		"DROP INDEX observations_client_id_uq",
		"DROP INDEX prompts_client_id_uq",
		"DROP INDEX edges_client_id_uq",
		"ALTER INDEX observations_client_id_ws_uq RENAME TO observations_client_id_uq",
		"ALTER INDEX prompts_client_id_ws_uq RENAME TO prompts_client_id_uq",
		"ALTER INDEX edges_client_id_ws_uq RENAME TO edges_client_id_uq",
		// Workspace-scoped feed predicates for list/search and hydration.
		"CREATE INDEX observations_tenant_workspace_idx",
		"CREATE INDEX prompts_tenant_workspace_idx",
		"CREATE INDEX edges_tenant_workspace_idx",
	} {
		if !strings.Contains(subject.SQL(), token) {
			t.Errorf("migration 107 missing %q", token)
		}
	}
	for _, banned := range []string{"DROP TABLE", "TRUNCATE", "DELETE FROM", "DROP DATABASE", "ON DELETE CASCADE", "ON DELETE SET NULL"} {
		if strings.Contains(strings.ToUpper(subject.SQL()), banned) {
			t.Errorf("migration 107 contains destructive statement %q", banned)
		}
	}
	if strings.Contains(subject.SQL(), "CREATE UNIQUE INDEX IF NOT EXISTS") {
		t.Error("migration 107 uses IF NOT EXISTS; a stale unledgered artifact must fail closed, not be adopted")
	}
}

// TestPostgresServerMigrationHeadIs108 pins the runtime head: every migration
// in the line must refuse ledgers recording versions beyond 108 so a head 108
// binary fails closed (ErrFutureMigration) against a newer database, and a
// head 107 binary fails closed against a 108 ledger.
func TestPostgresServerMigrationHeadIs108(t *testing.T) {
	migrations := mustPostgresMigrations(t)
	for _, migration := range migrations {
		if migration.maxKnownVersion != 108 {
			t.Errorf("migration %d maxKnownVersion = %d, want 108", migration.Version(), migration.maxKnownVersion)
		}
		if err := migration.Down(context.Background(), (*sql.DB)(nil)); err == nil || !strings.Contains(err.Error(), "nil") {
			// Down(nil) must fail on the nil connection before any DDL; the
			// forward-only error for real connections is proven in the
			// postgres_integration behavioral matrix.
			t.Errorf("Down(nil) for migration %d err=%v, want nil-connection failure", migration.Version(), err)
		}
	}
}

func TestPostgresServerMigrationIsServerOnly(t *testing.T) {
	m, err := NewPostgresServerMigration()
	if err != nil {
		t.Fatalf("NewPostgresServerMigration: %v", err)
	}
	for _, table := range []string{"organizations", "workspaces", "projects", "app_users", "service_accounts", "api_tokens", "memberships", "sessions", "observations", "prompts", "edges", "entities", "index_outbox", "audit_events"} {
		if !strings.Contains(m.SQL(), "CREATE TABLE IF NOT EXISTS "+table) {
			t.Errorf("server migration does not define %s", table)
		}
	}
	if strings.Contains(strings.ToLower(m.SQL()), "sqlite") {
		t.Fatal("server migration contains SQLite-only SQL")
	}
	if !strings.Contains(m.SQL(), "FORCE ROW LEVEL SECURITY") || !strings.Contains(m.SQL(), "SECURITY DEFINER") {
		t.Fatal("server migration is missing forced RLS or SECURITY DEFINER context binding")
	}
}

func TestPostgresServerMigratorRejectsNilDB(t *testing.T) {
	m, err := NewPostgresServerMigration()
	if err != nil {
		t.Fatalf("NewPostgresServerMigration: %v", err)
	}
	if err := m.Apply(context.Background(), (*sql.DB)(nil)); err == nil {
		t.Fatal("Apply(nil) unexpectedly succeeded")
	}
	if err := m.Down(context.Background(), (*sql.DB)(nil)); err == nil {
		t.Fatal("Down(nil) unexpectedly succeeded")
	}
}

func TestPostgresServerMigrationHasRecoveryAndIsolationSchema(t *testing.T) {
	m, err := NewPostgresServerMigration()
	if err != nil {
		t.Fatalf("NewPostgresServerMigration: %v", err)
	}
	for _, token := range []string{"actor_subjects", "lease_owner", "leased_until", "affected_rows", "completed_at", "project_key", "scope", "source", "edges_valid_range"} {
		if !strings.Contains(m.SQL(), token) {
			t.Errorf("server migration missing %q", token)
		}
	}
}

// TestPostgresDownPolicyIsForwardOnly guards the Down policy at the source
// level: the destructive shared-table cascade, the RLS-function drops, and
// the ledger erase are gone. Applied (ledgered) migrations MUST get a
// fail-closed forward-only error instead of any DDL/DML (REM-MIG-001). The
// behavioral matrix runs in postgres_integration with real snapshots.
func TestPostgresDownPolicyIsForwardOnly(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("postgres.go"))
	if err != nil {
		t.Fatalf("read postgres.go source: %v", err)
	}
	src := string(source)
	for _, banned := range []string{
		// Shared tenant-data cascade of the old Down.
		"DROP TABLE IF EXISTS audit_events",
		"DROP TABLE IF EXISTS observations",
		"DROP TABLE IF EXISTS organizations",
		// Principal-binding helpers belong to migration 100 only.
		"DROP FUNCTION IF EXISTS cortex_current_tenant()",
		"DROP FUNCTION IF EXISTS cortex_bind_principal",
		// Rolling back must never rewrite the migration ledger.
		"DELETE FROM cortex_server_migrations WHERE version=$1",
	} {
		if strings.Contains(src, banned) {
			t.Errorf("postgres.go still contains destructive statement %q", banned)
		}
	}
	if !strings.Contains(src, "ErrForwardOnly") {
		t.Error("postgres.go Down path no longer references ErrForwardOnly")
	}
}

// TestPostgresDownHasNoArtifactCleanupPath pins at the source level that
// Down executes NO DDL/DML under any ledger state: the unledgered artifact
// cleanup allowlist and its DROP path are gone (R1F review). Every version
// 100-104 is unconditionally ErrForwardOnly; the behavioral matrix with real
// schema/data snapshots runs in postgres_integration.
func TestPostgresDownHasNoArtifactCleanupPath(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("postgres.go"))
	if err != nil {
		t.Fatalf("read postgres.go source: %v", err)
	}
	src := string(source)
	for _, banned := range []string{
		// The unledgered artifact cleanup allowlist is removed entirely.
		"postgresUnledgeredArtifactCleanup",
		// No DDL in the Down path, under any ledger state.
		"DROP TABLE IF EXISTS",
		// Rolling back must never rewrite the migration ledger.
		"DELETE FROM cortex_server_migrations",
	} {
		if strings.Contains(src, banned) {
			t.Errorf("postgres.go still contains %q", banned)
		}
	}
}

// TestPostgresApplyGuardsFutureLedgerVersions pins at the source level that
// Apply rejects ledgers recording versions beyond the runtime head (a
// database created by a NEWER runtime) instead of silently applying below a
// newer head. Behavioral proof (a 105 ledger row) runs in postgres_integration.
func TestPostgresApplyGuardsFutureLedgerVersions(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("postgres.go"))
	if err != nil {
		t.Fatalf("read postgres.go source: %v", err)
	}
	src := string(source)
	if !strings.Contains(src, "ErrFutureMigration") {
		t.Error("postgres.go Apply path does not reference ErrFutureMigration")
	}
	if !strings.Contains(src, "SELECT max(version) FROM cortex_server_migrations WHERE version > $1") {
		t.Error("postgres.go Apply path lacks the future-version ledger guard query")
	}
}

// TestPostgresServerMigration106ProjectArtifacts verifies the additive 106
// follow-up: tenant/workspace/project-scoped artifacts, immutable
// revisions/events, one activation pointer, durable idempotency receipts,
// transactional usage counters, ENABLE+FORCE RLS with tenant/workspace/
// project policies, and grants aligned with the server baseline.
func TestPostgresServerMigration106ProjectArtifacts(t *testing.T) {
	migrations := mustPostgresMigrations(t)
	var subject *PostgresServerMigration
	for _, migration := range migrations {
		if migration.Version() == 106 {
			subject = migration
			break
		}
	}
	if subject == nil {
		t.Fatal("migration 106 (project artifacts) is not registered")
	}
	if subject.Name() != "project_artifacts" {
		t.Errorf("migration 106 name = %q, want project_artifacts", subject.Name())
	}
	if subject.Checksum() == "" {
		t.Error("migration 106 checksum is empty")
	}
	for _, token := range []string{
		// Six tenant/workspace/project-scoped artifact tables.
		"CREATE TABLE project_artifacts (",
		"CREATE TABLE project_artifact_revisions (",
		"CREATE TABLE project_artifact_events (",
		"CREATE TABLE project_artifact_activations (",
		"CREATE TABLE project_artifact_idempotency (",
		"CREATE TABLE project_storage_usage (",
		"tenant_id uuid NOT NULL",
		"workspace_id bigint NOT NULL",
		// Workspace defaults are first-class rows with an absent project.
		"project_id bigint",
		"CHECK (\n        (source_scope = 'project' AND project_id IS NOT NULL)\n        OR (source_scope = 'workspace_default' AND project_id IS NULL)\n    )",
		"CREATE UNIQUE INDEX project_artifacts_project_active_key_uq",
		"CREATE UNIQUE INDEX project_artifacts_workspace_default_active_key_uq",
		"WHERE source_scope = 'workspace_default' AND status = 'active'",
		// Scope stays consistent with the durable hierarchy.
		"cortex_validate_project_scope",
		// Immutable history and one activation pointer guarded by the
		// monotonic activation CAS token.
		"cortex_forbid_project_history_mutation",
		"activation_revision integer NOT NULL CHECK (activation_revision >= 1)",
		"activation_revision integer NOT NULL DEFAULT 0 CHECK (activation_revision >= 0)",
		"cortex_project_activation_cas_guard",
		"activation_revision must increase to move the activation pointer",
		"PRIMARY KEY (tenant_id, artifact_id)",
		// Exact idempotency replay: the committed receipt stores the exact
		// immutable result revision of the SAME coordinate's artifact,
		// commits exactly once, and cannot be fabricated by direct INSERT.
		"result_revision integer",
		"AND result_revision >= 1",
		"cortex_project_idempotency_commit_guard",
		"cortex_project_idempotency_result_guard_on_insert",
		"committed receipts must reference the exact revision of the same coordinate",
		"CREATE UNIQUE INDEX project_artifact_idempotency_project_uq",
		"CREATE UNIQUE INDEX project_artifact_idempotency_workspace_default_uq",
		"state IN ('pending', 'committed')",
		// Activation pointer is authoritative: mirror sync + no drift.
		"cortex_project_sync_activation_mirror",
		"cortex_project_activation_mirror_authoritative",
		"activation_revision must equal the activation pointer token",
		// Quota counters can only grow: monotonic guard, no removal.
		"cortex_project_usage_monotonic",
		"storage usage counters never decrease",
		"cortex_forbid_project_ledger_deletion",
		// Trusted principal-derived workspace/project scope binding:
		// grants-derived, actor-persisting, stale-scope-clearing.
		"ALTER TABLE cortex_tenant_context ADD COLUMN workspace_id bigint",
		"ALTER TABLE cortex_tenant_context ADD COLUMN actor_public_id uuid",
		"CREATE OR REPLACE FUNCTION cortex_bind_principal(p_actor_public_id uuid, p_grant_digest text, p_grant_version bigint)",
		"actor_public_id = EXCLUDED.actor_public_id",
		"workspace_id = NULL,",
		"cortex_bind_project_scope",
		"grant_type = 'workspace'",
		"grant_value = p_workspace_public_id::text",
		"g.grant_value IN ('*', p_project_public_id::text)",
		"g.grant_value IN ('project:*', 'project:' || p_project_public_id::text)",
		"principal is not granted workspace",
		"principal is not granted project",
		// Identity tables are fully revoked from the application role; only
		// the non-sensitive actor label columns stay directly readable and
		// api_tokens keeps a column read that excludes token_digest.
		"REVOKE ALL ON public.actor_subjects, public.principal_grants FROM cortex_app",
		"GRANT SELECT (tenant_id, public_id, subject) ON public.actor_subjects TO cortex_app",
		"REVOKE SELECT ON public.api_tokens FROM cortex_app",
		// Hard-delete/restrict everywhere; no cascade.
		"REFERENCES workspaces(tenant_id, id) ON DELETE RESTRICT",
		"REFERENCES projects(tenant_id, id) ON DELETE RESTRICT",
		"REFERENCES project_artifacts(tenant_id, id) ON DELETE RESTRICT",
		"REFERENCES project_artifact_revisions(tenant_id, artifact_id, revision) ON DELETE RESTRICT",
		"FOREIGN KEY (tenant_id, artifact_id, result_revision) REFERENCES project_artifact_revisions(tenant_id, artifact_id, revision) ON DELETE RESTRICT",
		// Soft-delete provenance triple.
		"deleted_at timestamptz",
		"deleted_by uuid",
		"delete_reason text",
		// RLS: enable + force + tenant/workspace/project policies + grants.
		"ENABLE ROW LEVEL SECURITY",
		"FORCE ROW LEVEL SECURITY",
		"cortex_project_isolation",
		"cortex_current_tenant()",
		"workspace_id = public.cortex_current_workspace()",
		"REVOKE ALL ON project_artifacts, project_artifact_revisions, project_artifact_events, project_artifact_activations, project_artifact_idempotency, project_storage_usage FROM PUBLIC",
		// Least privilege: no DELETE anywhere, no UPDATE on immutable
		// history, admin read-only.
		"GRANT SELECT, INSERT, UPDATE ON project_artifacts TO cortex_app",
		"GRANT SELECT, INSERT ON project_artifact_revisions, project_artifact_events TO cortex_app",
		"GRANT SELECT, INSERT, UPDATE ON project_artifact_activations TO cortex_app",
		"GRANT SELECT, INSERT, UPDATE ON project_artifact_idempotency TO cortex_app",
		"GRANT SELECT, INSERT, UPDATE ON project_storage_usage TO cortex_app",
		"GRANT SELECT ON project_artifacts, project_artifact_revisions, project_artifact_events, project_artifact_activations, project_artifact_idempotency, project_storage_usage TO cortex_admin",
	} {
		if !strings.Contains(subject.SQL(), token) {
			t.Errorf("migration 106 missing %q", token)
		}
	}
	upper := strings.ToUpper(subject.SQL())
	// Exactly two pinned DELETE statements exist in this migration, both
	// verbatim and neither touching artifact/history/ledger data: (1) the
	// replaced cortex_bind_principal's migration 100 housekeeping, removing
	// stale context rows from EARLIER transactions of the same backend, and
	// (2) the bootstrap reconciler's scoped principal_grants replace, which
	// rewrites exactly one actor's mutable grant set inside the reconciled
	// transition (grant rows are mutable authorization state, not retained
	// evidence). Every other delete/destroy path is banned.
	cleanup := strings.ToUpper("DELETE FROM public.cortex_tenant_context\n     WHERE backend_pid = pg_backend_pid() AND transaction_id <> txid_current();")
	grantReplace := strings.ToUpper("DELETE FROM public.principal_grants WHERE tenant_id = p_tenant_id AND actor_public_id = p_actor_public_id;")
	if got := strings.Count(upper, cleanup); got != 1 {
		t.Fatalf("migration 106 must contain exactly one pinned tenant-context housekeeping DELETE (found %d)", got)
	}
	if got := strings.Count(upper, grantReplace); got != 1 {
		t.Fatalf("migration 106 must contain exactly one pinned scoped principal_grants reconcile DELETE (found %d)", got)
	}
	upper = strings.ReplaceAll(upper, cleanup, "")
	upper = strings.ReplaceAll(upper, grantReplace, "")
	// DROP POLICY IF EXISTS + CREATE POLICY is the baseline idempotent-policy
	// convention (see 104); every other drop/destroy path is banned.
	for _, banned := range []string{
		"DROP TABLE", "DROP INDEX", "DROP FUNCTION", "DROP TRIGGER", "DROP SCHEMA",
		"TRUNCATE", "DELETE FROM",
		"ON DELETE CASCADE", "ON DELETE SET NULL", "ON DELETE SET DEFAULT",
	} {
		if strings.Contains(upper, banned) {
			t.Errorf("migration 106 contains destructive statement %q", banned)
		}
	}
	if strings.Contains(subject.SQL(), "CREATE TABLE IF NOT EXISTS project_artifacts") {
		t.Error("migration 106 uses IF NOT EXISTS; a stale unledgered artifact must fail closed, not be adopted")
	}
	// Every artifact table enables AND forces RLS.
	rlsTables := []string{"project_artifacts", "project_artifact_revisions", "project_artifact_events", "project_artifact_activations", "project_artifact_idempotency", "project_storage_usage"}
	for _, table := range rlsTables {
		if !strings.Contains(subject.SQL(), "ALTER TABLE "+table+" ENABLE ROW LEVEL SECURITY") {
			t.Errorf("migration 106 does not enable RLS on %s", table)
		}
		if !strings.Contains(subject.SQL(), "ALTER TABLE "+table+" FORCE ROW LEVEL SECURITY") {
			t.Errorf("migration 106 does not force RLS on %s", table)
		}
		if !strings.Contains(subject.SQL(), "CREATE POLICY cortex_project_isolation ON "+table) {
			t.Errorf("migration 106 has no cortex_project_isolation policy on %s", table)
		}
	}
}

// TestPostgresServerMigration106IdentityMediation pins the mediated
// identity/privilege contract added to the unshipped 106 (REQ-IDP-002..009,
// REQ-QUOTA-001): SECURITY DEFINER provisioning/activation/verification/
// grant-read routines with a fixed search path and least EXECUTE, a bound
// owner/admin authorization helper the application role can never execute,
// the proof-bound three-argument binder for legacy empty-digest actors, the
// full direct-privilege revocation on the identity tables (token_digest and
// grant state stay unreadable, graph label columns stay readable), and the
// frozen PostgreSQL usage coordinates. Ownership itself (cortex_migration,
// the role that runs this file) is asserted against a real database in the
// postgres_integration suite.
func TestPostgresServerMigration106IdentityMediation(t *testing.T) {
	migrations := mustPostgresMigrations(t)
	var subject *PostgresServerMigration
	for _, migration := range migrations {
		if migration.Version() == 106 {
			subject = migration
			break
		}
	}
	if subject == nil {
		t.Fatal("migration 106 (project artifacts) is not registered")
	}
	sqlText := subject.SQL()
	for _, token := range []string{
		// Private bound-caller authorization helper: derived only from the
		// installed principal context, an active non-revoked actor, and a
		// durable same-tenant owner/admin role grant; never executable by
		// the application or admin roles.
		"CREATE FUNCTION cortex_actor_admin_caller()",
		"RETURNS TABLE(tenant_id uuid, caller_public_id uuid)",
		"AND g.grant_type = 'role'",
		"AND g.grant_value IN ('owner', 'admin')",
		"caller is not an owner or admin",
		"REVOKE ALL ON FUNCTION cortex_actor_admin_caller() FROM PUBLIC",
		"REVOKE ALL ON FUNCTION cortex_actor_admin_caller() FROM cortex_app",
		"REVOKE ALL ON FUNCTION cortex_actor_admin_caller() FROM cortex_admin",
		// Provisioning contract: exact signature, allowlist validation,
		// in-SQL canonical SHA-256 grant digest, atomic actor+grants+audit.
		"CREATE FUNCTION cortex_provision_actor(p_actor_public_id uuid, p_subject text, p_actor_type text, p_grants jsonb, p_reason text)",
		"RETURNS TABLE(grant_version bigint, grant_digest text)",
		"'role', 'workspace', 'project', 'classification', 'scope'",
		"encode(public.digest(convert_to(v_canonical, 'UTF8'), 'sha256'), 'hex')",
		"identity.actor.provision",
		"REVOKE ALL ON FUNCTION cortex_provision_actor(uuid,text,text,jsonb,text) FROM PUBLIC",
		"GRANT EXECUTE ON FUNCTION cortex_provision_actor(uuid,text,text,jsonb,text) TO cortex_app",
		// Activation contract: a true no-op keeps the version, transitions
		// bump it exactly once, synchronize the subject row, revoke live
		// tokens when disabling, and audit in the same transaction.
		"CREATE FUNCTION cortex_set_actor_active(p_target_actor_public_id uuid, p_active boolean, p_reason text)",
		"RETURNS bigint",
		"IF v_current IS NOT DISTINCT FROM p_active THEN",
		"identity.actor.active_changed",
		"REVOKE ALL ON FUNCTION cortex_set_actor_active(uuid,boolean,text) FROM PUBLIC",
		"GRANT EXECUTE ON FUNCTION cortex_set_actor_active(uuid,boolean,text) TO cortex_app",
		// Verification contract: exact signature and row shape, token lock,
		// actor/subject liveness, revocation/expiry/scope enforcement,
		// last_used_at fold, and provenance minting over tenant, actor,
		// grant version, and token keyed by the caller-supplied digest.
		"CREATE FUNCTION cortex_verify_token_principal(p_token_prefix text, p_token_digest bytea, p_required_scope text)",
		"token_scopes text[],",
		"grant_version bigint,",
		"binding_provenance text",
		"FOR UPDATE OF t",
		"token is revoked",
		"token is expired",
		"token is missing required scope",
		"'v1:' || v_token_id::text || ':'",
		"REVOKE ALL ON FUNCTION cortex_verify_token_principal(text,bytea,text) FROM PUBLIC",
		"GRANT EXECUTE ON FUNCTION cortex_verify_token_principal(text,bytea,text) TO cortex_app",
		// Grant read-back for user listing, ordered and owner/admin-gated.
		"CREATE FUNCTION cortex_read_actor_grants(p_actor_public_id uuid)",
		"RETURNS TABLE(grant_type text, grant_value text)",
		"ORDER BY g.grant_type, g.grant_value",
		"REVOKE ALL ON FUNCTION cortex_read_actor_grants(uuid) FROM PUBLIC",
		"GRANT EXECUTE ON FUNCTION cortex_read_actor_grants(uuid) TO cortex_app",
		// The binder stays three-argument; EVERY actor — non-empty and
		// legacy empty stored digests alike — binds ONLY with the strict
		// versioned token-bound proof minted by verification.
		"CREATE OR REPLACE FUNCTION cortex_bind_principal(p_actor_public_id uuid, p_grant_digest text, p_grant_version bigint)",
		"'^v1:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}:[0-9a-f]{64}$'",
		"principal binding requires token-bound provenance",
		"v_token_public_id := substring(p_grant_digest FROM 4 FOR 36)::uuid",
		"v_mac := substring(p_grant_digest FROM 41)",
		"v_subject IS DISTINCT FROM p_actor_public_id",
		"OR v_token_revoked IS NOT NULL",
		"OR (v_token_expires IS NOT NULL AND v_token_expires <= clock_timestamp())",
		"public.hmac(",
		"convert_to(",
		"v_tenant::text || ':' || p_actor_public_id::text || ':' || v_version::text || ':' || v_token_public_id::text",
		"principal binding proof is stale, revoked, or foreign",
		"IF v_mac <> v_expected THEN",
		"principal binding proof is invalid",
		// Privilege matrix: the application role loses every direct table
		// privilege on the identity tables; labels keep a column read.
		"REVOKE ALL ON public.actor_subjects, public.principal_grants FROM cortex_app",
		"GRANT SELECT (tenant_id, public_id, subject) ON public.actor_subjects TO cortex_app",
		"REVOKE SELECT ON public.api_tokens FROM cortex_app",
		// Frozen usage coordinates: the guard fires before the monotonic
		// fold guard (alphabetical trigger order: coordinate_immutable sorts
		// before monotonic) and leaves legal nondecreasing folds alone.
		"CREATE FUNCTION cortex_project_usage_coordinate_immutable()",
		"NEW.project_id IS DISTINCT FROM OLD.project_id",
		"storage usage coordinates are immutable after insert",
		"CREATE TRIGGER project_storage_usage_coordinate_immutable",
	} {
		if !strings.Contains(sqlText, token) {
			t.Errorf("migration 106 identity mediation missing %q", token)
		}
	}
	// Every definer routine pins the fixed search path: the binder, the
	// project scope binder, the current workspace/project readers, the
	// five identity routines, the four token-lifecycle/grant-version
	// additions pinned by TestPostgresServerMigration106TokenLifecycleMediation,
	// and the migration-role bootstrap reconciler pinned by
	// TestPostgresServerMigration106BootstrapReconciler.
	if got := strings.Count(sqlText, "SECURITY DEFINER"); got != 14 {
		t.Errorf("migration 106 SECURITY DEFINER count = %d, want 14", got)
	}
	if got := strings.Count(sqlText, "SET search_path = pg_catalog, public"); got != 14 {
		t.Errorf("migration 106 fixed search_path count = %d, want 14", got)
	}
	// The api_tokens column grant must never expose token_digest, and no
	// column grant may expose sensitive grant state.
	tokenGrant := "GRANT SELECT (id, public_id, tenant_id, token_prefix, subject_user_id, subject_service_account_id, scopes, workspace_ids, rate_limit_tier, expires_at, revoked_at, last_used_at, created_at, updated_at, name, created_by) ON public.api_tokens TO cortex_app;"
	if !strings.Contains(sqlText, tokenGrant) {
		t.Error("migration 106 missing the exact non-sensitive api_tokens column grant")
	}
	for _, line := range strings.Split(sqlText, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "GRANT SELECT (") {
			for _, banned := range []string{"token_digest", "grant_digest", "grant_version"} {
				if strings.Contains(trimmed, banned) {
					t.Errorf("column grant exposes sensitive column %q: %s", banned, trimmed)
				}
			}
			if strings.Contains(trimmed, "actor_subjects") && trimmed != "GRANT SELECT (tenant_id, public_id, subject) ON public.actor_subjects TO cortex_app;" {
				t.Errorf("actor_subjects column grant is not the pinned label triple: %s", trimmed)
			}
		}
		if strings.HasPrefix(trimmed, "GRANT EXECUTE") && strings.Contains(trimmed, "cortex_actor_admin_caller") {
			t.Errorf("private authorization helper must never be granted EXECUTE: %s", trimmed)
		}
	}
	// The hardened binder keeps migration 100's baseline privilege contract.
	for _, token := range []string{
		"REVOKE ALL ON FUNCTION cortex_bind_principal(uuid,text,bigint) FROM PUBLIC",
		"REVOKE ALL ON FUNCTION cortex_bind_principal(uuid,text,bigint) FROM cortex_admin",
		"GRANT EXECUTE ON FUNCTION cortex_bind_principal(uuid,text,bigint) TO cortex_app",
	} {
		if !strings.Contains(sqlText, token) {
			t.Errorf("migration 106 binder privilege contract missing %q", token)
		}
	}
}

// TestPostgresServerMigration106TokenLifecycleMediation pins the mediated
// api_tokens lifecycle and grant-version read-back added to the unshipped
// 106 after the adversarial token-impersonation review: the application
// role holds no direct api_tokens write at all, issue/rotate/revoke run
// only through migration-owned definer routines that authorize the bound
// owner/admin caller, hash the one-time secret inside SQL, never return
// token_digest, and audit atomically in the same transaction. Routine
// ownership (cortex_migration) and the real EXECUTE/privilege matrix are
// asserted against a live database in the postgres_integration suite.
func TestPostgresServerMigration106TokenLifecycleMediation(t *testing.T) {
	migrations := mustPostgresMigrations(t)
	var subject *PostgresServerMigration
	for _, migration := range migrations {
		if migration.Version() == 106 {
			subject = migration
			break
		}
	}
	if subject == nil {
		t.Fatal("migration 106 (project artifacts) is not registered")
	}
	sqlText := subject.SQL()
	for _, token := range []string{
		// Direct identity-bearing writes on api_tokens are gone from the
		// application role; only the audited definer lifecycle below can
		// mint, rewrite, or revoke a token, so a compromised query path
		// can no longer forge a victim token with an attacker-known
		// digest and ride verification into a bound principal.
		"REVOKE INSERT, UPDATE, DELETE ON public.api_tokens FROM cortex_app",
		// Owner/admin-gated grant-version read-back (same authorization
		// surface as the grant read-back) so user listing can pin stale
		// binders without any direct actor_subjects state read.
		"CREATE FUNCTION cortex_actor_grant_version(p_actor_public_id uuid)",
		"RETURNS bigint",
		"actor does not exist in tenant",
		"REVOKE ALL ON FUNCTION cortex_actor_grant_version(uuid) FROM PUBLIC",
		"REVOKE ALL ON FUNCTION cortex_actor_grant_version(uuid) FROM cortex_admin",
		"GRANT EXECUTE ON FUNCTION cortex_actor_grant_version(uuid) TO cortex_app",
		// Issue contract: exact signature, live same-tenant subject of
		// either type, digest derived ONLY inside SQL from the presented
		// one-time secret, non-secret audit, digest-free return shape.
		"CREATE FUNCTION cortex_issue_api_token(p_subject_public_id uuid, p_name text, p_secret text, p_scopes text[], p_workspace_ids uuid[], p_expires_at timestamptz, p_reason text)",
		"RETURNS TABLE(token_public_id uuid, token_prefix text, expires_at timestamptz)",
		"token subject does not exist in tenant",
		"identity.token.issued",
		"REVOKE ALL ON FUNCTION cortex_issue_api_token(uuid,text,text,text[],uuid[],timestamptz,text) FROM PUBLIC",
		"REVOKE ALL ON FUNCTION cortex_issue_api_token(uuid,text,text,text[],uuid[],timestamptz,text) FROM cortex_admin",
		"GRANT EXECUTE ON FUNCTION cortex_issue_api_token(uuid,text,text,text[],uuid[],timestamptz,text) TO cortex_app",
		// Rotate contract: locks the live token, revokes it, and re-issues
		// an exact copy with a fresh in-SQL digest; the caller learns
		// everything except the stored digest.
		"CREATE FUNCTION cortex_rotate_api_token(p_token_public_id uuid, p_secret text, p_reason text)",
		"RETURNS TABLE(token_public_id uuid, token_prefix text, token_name text, subject_public_id uuid, principal_type text, token_scopes text[], token_workspace_ids uuid[], expires_at timestamptz)",
		"token does not exist in tenant or is revoked",
		"identity.token.rotated",
		"'rotated_from', p_token_public_id::text",
		"REVOKE ALL ON FUNCTION cortex_rotate_api_token(uuid,text,text) FROM PUBLIC",
		"REVOKE ALL ON FUNCTION cortex_rotate_api_token(uuid,text,text) FROM cortex_admin",
		"GRANT EXECUTE ON FUNCTION cortex_rotate_api_token(uuid,text,text) TO cortex_app",
		// Revoke contract: idempotent per-call boolean, missing tokens fail
		// closed, and only an actual transition appends the audit row.
		"CREATE FUNCTION cortex_revoke_api_token(p_token_public_id uuid, p_reason text)",
		"RETURNS boolean",
		"identity.token.revoked",
		"REVOKE ALL ON FUNCTION cortex_revoke_api_token(uuid,text) FROM PUBLIC",
		"REVOKE ALL ON FUNCTION cortex_revoke_api_token(uuid,text) FROM cortex_admin",
		"GRANT EXECUTE ON FUNCTION cortex_revoke_api_token(uuid,text) TO cortex_app",
	} {
		if !strings.Contains(sqlText, token) {
			t.Errorf("migration 106 token lifecycle mediation missing %q", token)
		}
	}
	// Every privileged mutation resolves its caller solely through the
	// private bound owner/admin helper: the three pre-existing identity
	// routines plus grant-version, issue, rotate, and revoke.
	if got := strings.Count(sqlText, "FROM public.cortex_actor_admin_caller() d"); got != 7 {
		t.Errorf("admin-caller authorization count = %d, want 7", got)
	}
	// The stored digest is always the tenant-keyed HMAC-SHA256 of the
	// presented secret computed inside SQL, for issue and rotation alike.
	if got := strings.Count(sqlText, "public.hmac(convert_to(p_secret, 'UTF8'), convert_to(v_tenant::text, 'UTF8'), 'sha256')"); got != 2 {
		t.Errorf("in-SQL token digest derivations = %d, want 2 (issue and rotate)", got)
	}
	// No definer return shape may ever expose the stored digest or the
	// one-time secret.
	for _, line := range strings.Split(sqlText, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "RETURNS") {
			for _, banned := range []string{"token_digest", "p_secret"} {
				if strings.Contains(trimmed, banned) {
					t.Errorf("routine return shape exposes %q: %s", banned, trimmed)
				}
			}
		}
	}
	// The token lifecycle never weakens the immutable-history or retention
	// posture: no routine deletes evidence.
	for _, banned := range []string{"DELETE FROM public.api_tokens", "DELETE FROM api_tokens"} {
		if strings.Contains(strings.ToUpper(sqlText), strings.ToUpper(banned)) {
			t.Errorf("migration 106 deletes token rows directly (%q)", banned)
		}
	}
}

// TestPostgresServerMigration106BinderProvenanceUnforgeable pins the
// negative binder contract after the deterministic-digest impersonation
// review: NO cortex_app binder path accepts actor+digest+version alone.
// The deterministic grant digest is integrity metadata only, so an
// attacker who can read or recompute another actor's digest (identical
// grants produce the identical digest; owner/admin can read grants
// through the mediated read-back) still cannot bind that actor. Every
// bind requires the token-bound v1 provenance whose MAC is keyed by the
// stored token digest and binds tenant, actor, and grant version.
func TestPostgresServerMigration106BinderProvenanceUnforgeable(t *testing.T) {
	migrations := mustPostgresMigrations(t)
	var subject *PostgresServerMigration
	for _, migration := range migrations {
		if migration.Version() == 106 {
			subject = migration
			break
		}
	}
	if subject == nil {
		t.Fatal("migration 106 (project artifacts) is not registered")
	}
	sqlText := subject.SQL()
	start := strings.Index(sqlText, "CREATE OR REPLACE FUNCTION cortex_bind_principal(")
	end := strings.Index(sqlText, "REVOKE ALL ON FUNCTION cortex_bind_principal(uuid,text,bigint) FROM PUBLIC")
	if start < 0 || end <= start {
		t.Fatal("migration 106 binder definition not found")
	}
	body := sqlText[start:end]
	// The proof gate applies unconditionally, and the token/actor/tenant/
	// version revalidation plus MAC recheck run under lock for every bind.
	for _, token := range []string{
		"IF p_grant_digest !~ '^v1:",
		"RAISE EXCEPTION 'principal binding requires token-bound provenance' USING ERRCODE = '28000';",
		"AND grant_version = p_grant_version",
		"v_subject IS DISTINCT FROM p_actor_public_id",
		"OR v_token_revoked IS NOT NULL",
		"public.hmac(",
		"IF v_mac <> v_expected THEN",
	} {
		if !strings.Contains(body, token) {
			t.Errorf("migration 106 binder provenance contract missing %q", token)
		}
	}
	// The deterministic stored digest must never authenticate: the old
	// digest-equality acceptance branch (and any stored-digest read in the
	// binder) is gone, so a digest recomputed from a victim's readable
	// grants — including the identical digest held by a different actor
	// with identical grants — cannot satisfy the binder.
	for _, banned := range []string{
		"v_digest",
		"grant_digest INTO",
		"p_grant_digest = v_digest",
		"v_digest = p_grant_digest",
		"IF v_digest <> ''",
	} {
		if strings.Contains(body, banned) {
			t.Errorf("migration 106 binder still treats the deterministic digest as an authenticator (%q)", banned)
		}
	}
	// Exactly one binder definition exists in 106 (the additive 3-arg
	// replacement), and it stays executable ONLY by the application role:
	// since the proof is mandatory, no callable path remains that accepts
	// actor+digest+version alone.
	if got := strings.Count(sqlText, "CREATE OR REPLACE FUNCTION cortex_bind_principal("); got != 1 {
		t.Errorf("migration 106 binder definition count = %d, want 1", got)
	}
	if got := strings.Count(sqlText, "GRANT EXECUTE ON FUNCTION cortex_bind_principal"); got != 1 {
		t.Errorf("migration 106 binder EXECUTE grants = %d, want exactly one (cortex_app)", got)
	}
	if strings.Contains(sqlText, "GRANT EXECUTE ON FUNCTION cortex_bind_principal(uuid,text,bigint) TO cortex_admin") {
		t.Error("migration 106 grants binder EXECUTE to cortex_admin")
	}
}

// TestPostgresServerMigration106BootstrapReconciler pins the migration-role
// bootstrap reconciler added to the unshipped 106 (REQ-BPR-003, REQ-BPR-004,
// REQ-BPR-008): the exact nine-argument migration-only signature and return
// shape, SECURITY DEFINER with the fixed search path, transaction-scoped
// tenant/actor serialization, canonical grant validation including the exact
// configured workspace grant and the owner/admin role, the in-SQL
// tenant-keyed HMAC digest derivation for the reserved-name bootstrap token,
// the four lifecycle actions with their non-secret audit evidence, the
// revoked-bearer fail-closed rule that never resurrects a token, and the
// EXECUTE matrix limited to cortex_migration (PUBLIC, cortex_app, and
// cortex_admin are all revoked). Real-database atomicity, idempotence,
// concurrency, rotation, recovery, and privilege behavior run in the
// postgres_integration suite.
func TestPostgresServerMigration106BootstrapReconciler(t *testing.T) {
	migrations := mustPostgresMigrations(t)
	var subject *PostgresServerMigration
	for _, migration := range migrations {
		if migration.Version() == 106 {
			subject = migration
			break
		}
	}
	if subject == nil {
		t.Fatal("migration 106 (project artifacts) is not registered")
	}
	sqlText := subject.SQL()
	start := strings.Index(sqlText, "CREATE FUNCTION public.cortex_bootstrap_service_principal(")
	end := strings.Index(sqlText, "REVOKE ALL ON FUNCTION public.cortex_bootstrap_service_principal(uuid,uuid,uuid,text,text,jsonb,text,text,text) FROM PUBLIC")
	if start < 0 || end <= start {
		t.Fatal("migration 106 bootstrap reconciler definition not found")
	}
	body := sqlText[start:end]
	for _, token := range []string{
		// Exact migration-only signature, schema-qualified in public: nine
		// arguments, three-column return, definer context, and the pinned
		// search path.
		"CREATE FUNCTION public.cortex_bootstrap_service_principal(p_tenant_id uuid, p_workspace_public_id uuid, p_actor_public_id uuid, p_subject text, p_service_name text, p_grants jsonb, p_token_name text, p_token_secret text, p_reason text)",
		"RETURNS TABLE(token_public_id uuid, grant_version bigint, bootstrap_action text)",
		"SECURITY DEFINER",
		"SET search_path = pg_catalog, public",
		// Concurrency: one reconciler per tenant/actor pair, serialized by
		// a transaction-scoped advisory lock taken before any identity row
		// is read or inserted.
		"pg_advisory_xact_lock(hashtextextended(p_tenant_id::text || ':' || p_actor_public_id::text, 0))",
		// Argument and grant validation mirrors the provision allowlist and
		// additionally requires the exact configured workspace grant and an
		// owner/admin role; the repository minimum secret length applies.
		"length(p_token_secret) < 12",
		"bootstrap grants must be non-empty allowlisted type/value objects",
		"bootstrap grants must be unique",
		"g->>'value' = p_workspace_public_id::text",
		"bootstrap grants must include the configured workspace grant",
		"g->>'value' IN ('owner', 'admin')",
		"bootstrap grants must include the owner or admin role",
		"bootstrap tenant does not exist",
		"bootstrap workspace does not exist in tenant",
		// Grant integrity digest: identical canonicalization and in-SQL
		// SHA-256 as provisioning; stored digest is metadata, never an
		// authenticator.
		"encode(public.digest(convert_to(v_canonical, 'UTF8'), 'sha256'), 'hex')",
		// The stored bootstrap token digest is derived ONLY inside SQL from
		// the plaintext bearer with the existing tenant-keyed HMAC.
		"public.hmac(convert_to(p_token_secret, 'UTF8'), convert_to(p_tenant_id::text, 'UTF8'), 'sha256')",
		// An existing service account row is validated, never adopted
		// blindly: the stored name must match the configured service.
		"v_existing_name <> v_service_name",
		"bootstrap service account name conflicts with the configured service",
		// Identity conflicts fail closed instead of guessing.
		"bootstrap actor conflicts with an existing subject",
		"bootstrap actor is revoked or inactive' USING ERRCODE = '28000'",
		"multiple active bootstrap tokens exist for the service actor",
		// The reserved bootstrap token name is bound to exactly one
		// bootstrap identity: a row held by any other subject is a
		// fail-closed conflict.
		"OR t.subject_service_account_id IS DISTINCT FROM v_service_id",
		"bootstrap token name is reserved to another subject",
		// Deterministic secret-derived prefix: the textual head plus the
		// first 16 hex characters of the tenant-keyed digest, so bearers
		// sharing a textual head stay unique while verification keeps
		// matching the head plus exact digest equality.
		"v_prefix := left(p_token_secret, 12) || ':' || substring(encode(v_token_digest, 'hex') FROM 1 FOR 16);",
		// A configured bearer matching a revoked reserved bootstrap token
		// fails closed and never clears revoked_at.
		"configured bootstrap bearer matches a revoked bootstrap token' USING ERRCODE = '28000'",
		// Grant reconciliation replaces exactly this actor's grants, folds
		// the integrity digest, and bumps grant_version exactly once; the
		// scoped replace DELETE is pinned verbatim by the artifact test.
		"DELETE FROM public.principal_grants WHERE tenant_id = p_tenant_id AND actor_public_id = p_actor_public_id;",
		"grant_version = v_version\n          WHERE tenant_id = p_tenant_id AND public_id = p_actor_public_id;",
		// The four lifecycle actions and their exact audit evidence names;
		// unchanged restarts emit no audit at all.
		"'identity.bootstrap.provisioned'",
		"'identity.bootstrap.reconciled'",
		"'identity.bootstrap.token_rotated'",
		"'unchanged'",
		// Audit metadata stays non-secret: action, reason, grant count, and
		// public IDs only.
		"jsonb_build_object('action', 'provisioned', 'reason', v_reason, 'allowed', true, 'grant_count', v_count, 'token', v_token_id::text)",
		// Rotation revokes the prior active reserved token and mints exactly
		// one replacement in the same transaction.
		"SET revoked_at = clock_timestamp(), updated_at = now()",
	} {
		if !strings.Contains(body, token) {
			t.Errorf("migration 106 bootstrap reconciler missing %q", token)
		}
	}
	// Privilege and ownership matrix: the routine is created in public,
	// ownership is transferred explicitly to the migration role, EXECUTE
	// is revoked from every runtime role, and only the migration role
	// that owns privileged startup keeps it.
	for _, token := range []string{
		"ALTER FUNCTION public.cortex_bootstrap_service_principal(uuid,uuid,uuid,text,text,jsonb,text,text,text) OWNER TO cortex_migration;",
		"REVOKE ALL ON FUNCTION public.cortex_bootstrap_service_principal(uuid,uuid,uuid,text,text,jsonb,text,text,text) FROM PUBLIC",
		"REVOKE ALL ON FUNCTION public.cortex_bootstrap_service_principal(uuid,uuid,uuid,text,text,jsonb,text,text,text) FROM cortex_app",
		"REVOKE ALL ON FUNCTION public.cortex_bootstrap_service_principal(uuid,uuid,uuid,text,text,jsonb,text,text,text) FROM cortex_admin",
		"GRANT EXECUTE ON FUNCTION public.cortex_bootstrap_service_principal(uuid,uuid,uuid,text,text,jsonb,text,text,text) TO cortex_migration",
		// Least-privilege definer prerequisites: the definer executes as
		// its owner cortex_migration, which baselines 100/101 leave with
		// no direct actor_subjects access, so the file must grant exactly
		// the table privileges the body exercises (row-locked reads,
		// fresh-actor inserts, digest/version folds) and nothing broader.
		"GRANT SELECT, INSERT, UPDATE ON public.actor_subjects TO cortex_migration;",
	} {
		if !strings.Contains(sqlText, token) {
			t.Errorf("migration 106 bootstrap reconciler privilege contract missing %q", token)
		}
	}
	for _, banned := range []string{
		"GRANT EXECUTE ON FUNCTION public.cortex_bootstrap_service_principal(uuid,uuid,uuid,text,text,jsonb,text,text,text) TO cortex_app",
		"GRANT EXECUTE ON FUNCTION public.cortex_bootstrap_service_principal(uuid,uuid,uuid,text,text,jsonb,text,text,text) TO cortex_admin",
		// The raw collision-prone textual prefix must survive only inside
		// the deterministic derivation, never as a stored column value.
		"VALUES\n            (p_tenant_id, v_token_name, left(p_token_secret, 12), v_token_digest",
	} {
		if strings.Contains(sqlText, banned) {
			t.Errorf("migration 106 bootstrap reconciler contract violated (%q)", banned)
		}
	}
	// The only two actor_subjects grants in the file are the pinned
	// least-privilege pair: the application role's label-triple column
	// read and the migration role's definer prerequisites. Any other
	// grant line naming actor_subjects -- broader migration-role
	// authority, new runtime-role access, or a widened column set --
	// fails here.
	actorGrantLines := 0
	for _, line := range strings.Split(sqlText, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "GRANT") || !strings.Contains(trimmed, "actor_subjects") {
			continue
		}
		actorGrantLines++
		if trimmed != "GRANT SELECT (tenant_id, public_id, subject) ON public.actor_subjects TO cortex_app;" &&
			trimmed != "GRANT SELECT, INSERT, UPDATE ON public.actor_subjects TO cortex_migration;" {
			t.Errorf("actor_subjects grant is not pinned least privilege: %s", trimmed)
		}
	}
	if actorGrantLines != 2 {
		t.Errorf("actor_subjects grant lines = %d, want exactly 2 (app label-triple column read + migration definer prerequisites)", actorGrantLines)
	}
	// Exactly one schema-qualified definition; no unqualified create and
	// no owner drift: the textual head appears exactly once, inside the
	// deterministic prefix derivation.
	if got := strings.Count(sqlText, "CREATE FUNCTION public.cortex_bootstrap_service_principal("); got != 1 {
		t.Errorf("migration 106 bootstrap reconciler definition count = %d, want 1", got)
	}
	if strings.Contains(sqlText, "CREATE FUNCTION cortex_bootstrap_service_principal(") {
		t.Error("migration 106 bootstrap reconciler must be schema-qualified in public")
	}
	if got := strings.Count(body, "left(p_token_secret, 12)"); got != 1 {
		t.Errorf("migration 106 textual bearer head occurrences = %d, want exactly one (inside the deterministic prefix derivation)", got)
	}
	// Verification compatibility: the token lookup matches the stored
	// prefix's 12-character head plus exact digest equality, so callers
	// presenting the plain textual head (the existing repository contract)
	// resolve both ordinary and digest-suffixed bootstrap tokens.
	if !strings.Contains(sqlText, "WHERE left(t.token_prefix, 12) = p_token_prefix") {
		t.Error("migration 106 token verification does not match the prefix head plus exact digest")
	}
	for _, banned := range []string{"p_token_secret, ", "token_digest,", "grant_digest,", "binding_provenance"} {
		first := strings.Index(body, "RETURNS TABLE")
		second := strings.Index(body[first:], "\n")
		if second < 0 {
			second = len(body) - first
		}
		returnLine := body[first : first+second]
		if strings.Contains(returnLine, banned) {
			t.Errorf("bootstrap return shape exposes %q: %s", banned, returnLine)
		}
	}
	// Three in-SQL digest derivations exist now (issue, rotate, bootstrap),
	// all keyed by the tenant.
	if got := strings.Count(sqlText, "public.hmac(convert_to("); got != 3 {
		t.Errorf("migration 106 in-SQL token digest derivations = %d, want 3 (issue, rotate, bootstrap)", got)
	}
}

// TestLedgerPreflightVerdictMatrix is the pure oracle for the read-only
// rollout preflight verdict shared by the SQLite 2003 and PostgreSQL 106
// paths (IDP-T05). Behavioral PostgreSQL preflights run in
// postgres_integration; this matrix pins the decision table itself.
func TestLedgerPreflightVerdictMatrix(t *testing.T) {
	const version = 106
	const expected = "d9dc2c36815ca9563922374e78c4cefd96a38ad66f4c8597af63c4a0a0eacc76"

	t.Run("unledgered is the expected state", func(t *testing.T) {
		p := LedgerPreflight{Version: version, ExpectedChecksum: expected, Head: version}
		if err := p.Verdict(); err != nil {
			t.Errorf("unledgered verdict = %v; want nil", err)
		}
		// A ledger table without the target row is still unledgered.
		p.LedgerTable = true
		if err := p.Verdict(); err != nil {
			t.Errorf("ledger-table-without-row verdict = %v; want nil", err)
		}
	})

	t.Run("current checksum stops as already applied", func(t *testing.T) {
		p := LedgerPreflight{Version: version, LedgerTable: true, Ledgered: true,
			RecordedChecksum: expected, ExpectedChecksum: expected, Head: version}
		err := p.Verdict()
		if err == nil || !errors.Is(err, ErrPreflightStop) {
			t.Fatalf("already-applied verdict = %v; want errors.Is ErrPreflightStop", err)
		}
		if errors.Is(err, ErrSchemaTampered) {
			t.Errorf("already-applied stop must not be tamper-class: %v", err)
		}
	})

	t.Run("prior checksum stops and escalates", func(t *testing.T) {
		p := LedgerPreflight{Version: version, LedgerTable: true, Ledgered: true,
			RecordedChecksum: "old-prerelease-checksum", ExpectedChecksum: expected, Head: version}
		err := p.Verdict()
		if err == nil || !errors.Is(err, ErrPreflightStop) || !errors.Is(err, ErrSchemaTampered) {
			t.Fatalf("prior-checksum verdict = %v; want errors.Is ErrPreflightStop AND ErrSchemaTampered", err)
		}
	})

	t.Run("future ledger version stops", func(t *testing.T) {
		p := LedgerPreflight{Version: version, LedgerTable: true, Ledgered: true,
			RecordedChecksum: expected, ExpectedChecksum: expected, Head: version, FutureLedgerVersion: 107}
		err := p.Verdict()
		if err == nil || !errors.Is(err, ErrPreflightStop) || !errors.Is(err, ErrFutureMigration) {
			t.Fatalf("future-version verdict = %v; want errors.Is ErrPreflightStop AND ErrFutureMigration", err)
		}
	})
}

// TestPostgresPreflightAndVerifyAppliedContract pins the read-only rollout
// preflight and post-apply check for PostgreSQL 106 at the source level: the
// Preflight probe uses to_regclass (presence without DDL), issues no writes,
// takes no advisory lock, and defers to the shared LedgerPreflight verdict;
// the post-apply VerifyApplied check reads the ledger and matches the
// embedded checksum. Behavioral coverage runs in postgres_integration.
func TestPostgresPreflightAndVerifyAppliedContract(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("postgres.go"))
	if err != nil {
		t.Fatalf("read postgres.go source: %v", err)
	}
	src := string(source)

	functionBody := func(name string) string {
		start := strings.Index(src, "func (m *PostgresServerMigration) "+name+"(")
		if start < 0 {
			t.Fatalf("postgres.go does not define %s", name)
		}
		rest := src[start:]
		if end := strings.Index(rest, "\nfunc "); end >= 0 {
			rest = rest[:end]
		}
		return rest
	}

	preflight := functionBody("Preflight")
	if !strings.Contains(preflight, "to_regclass") {
		t.Error("Preflight must probe ledger presence with to_regclass (read-only), never DDL")
	}
	if !strings.Contains(preflight, "Verdict()") {
		t.Error("Preflight must defer to the shared LedgerPreflight verdict")
	}
	for _, banned := range []string{
		"INSERT INTO", "UPDATE cortex", "DELETE FROM", "CREATE TABLE", "ALTER TABLE", "DROP",
		"pg_advisory",
	} {
		if strings.Contains(preflight, banned) {
			t.Errorf("Preflight contains write/lock token %q; it must stay strictly read-only", banned)
		}
	}

	verify := functionBody("VerifyApplied")
	if !strings.Contains(verify, "MatchesChecksum") {
		t.Error("VerifyApplied must accept exactly the checksums Apply's idempotent path accepts (MatchesChecksum)")
	}
	for _, banned := range []string{"INSERT INTO", "UPDATE cortex", "DELETE FROM", "CREATE TABLE", "ALTER TABLE", "DROP"} {
		if strings.Contains(verify, banned) {
			t.Errorf("VerifyApplied contains write token %q; it must stay strictly read-only", banned)
		}
	}
}

// TestPostgresPreflightHeadChecksumsMatchPins ties the rollout preflights to
// the reviewed pins: the checksum a preflight expects for 106, 107, and 108
// is exactly each version's pinned reviewed-bytes checksum, and no migration
// beyond the 108 head exists in the runtime line. (The IDP-T05 train
// originally capped the line at 106; the tools-security-performance-hardening
// train moved the head to 107; the tools-performance-scalability-r1 train
// moved it to 108, each with its own reviewed pin.)
func TestPostgresPreflightHeadChecksumsMatchPins(t *testing.T) {
	migrations := mustPostgresMigrations(t)
	for _, migration := range migrations {
		if migration.Version() > 108 {
			t.Errorf("migration %d registered beyond head 108; 109+ is not part of this train", migration.Version())
		}
		if migration.Version() == 106 && migration.Checksum() != postgresHistoricalChecksums[106] {
			t.Errorf("migration 106 checksum %s does not match the reviewed pin %s", migration.Checksum(), postgresHistoricalChecksums[106])
		}
		if migration.Version() == 107 && migration.Checksum() != postgresHistoricalChecksums[107] {
			t.Errorf("migration 107 checksum %s does not match the reviewed pin %s", migration.Checksum(), postgresHistoricalChecksums[107])
		}
		if migration.Version() == 108 && migration.Checksum() != postgresHistoricalChecksums[108] {
			t.Errorf("migration 108 checksum %s does not match the reviewed pin %s", migration.Checksum(), postgresHistoricalChecksums[108])
		}
	}
}

// TestRunbookDocumentsExecutablePreflightSQL pins the operator-facing
// runbook (docs/project-context-protocol-identity-privilege.md) to the exact
// copy/paste read-only preflight SQL (IDP-T05 acceptance): the SQLite
// ledger-existence, target-2003-checksum, and future-version queries with
// their read-only DSN guidance, and the PostgreSQL to_regclass, target-106,
// and future-version queries — plus the expected-output/stop-condition
// semantics for each result shape. Editing the runbook SQL away from these
// executable forms (or drifting it from the code's query semantics) fails
// here.
func TestRunbookDocumentsExecutablePreflightSQL(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "docs", "project-context-protocol-identity-privilege.md"))
	if err != nil {
		t.Fatalf("read rollout runbook: %v", err)
	}
	runbook := string(source)

	// SQLite: read-only connection guidance and the three probe queries.
	for _, token := range []string{
		"mode=ro",
		"immutable=1",
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'cortex_v2_migrations';`,
		`SELECT checksum FROM cortex_v2_migrations WHERE version = 2003;`,
		`SELECT max(version) FROM cortex_v2_migrations WHERE version > 2003;`,
	} {
		if !strings.Contains(runbook, token) {
			t.Errorf("runbook SQLite preflight section missing executable element %q", token)
		}
	}

	// PostgreSQL: the three probe queries.
	for _, token := range []string{
		`SELECT to_regclass('cortex_server_migrations');`,
		`SELECT checksum FROM cortex_server_migrations WHERE version = 106;`,
		`SELECT max(version) FROM cortex_server_migrations WHERE version > 106;`,
		"BEGIN TRANSACTION READ ONLY",
	} {
		if !strings.Contains(runbook, token) {
			t.Errorf("runbook PostgreSQL preflight section missing executable element %q", token)
		}
	}

	// Expected outputs and stop conditions must be stated for every result
	// shape: unledgered (proceed), current checksum (already applied), any
	// other checksum (tamper-class stop), future version (newer runtime).
	for _, token := range []string{
		"no row",
		"already applied",
		"prior",
		"newer runtime",
		"unledgered",
	} {
		if !strings.Contains(runbook, token) {
			t.Errorf("runbook preflight section missing expected-output/stop-condition token %q", token)
		}
	}
}

// TestRunbookPreflightFlowConditionalAndWALSafe pins the second-review
// runbook fixes (IDP-T05): the SQLite live procedure must use ordinary
// mode=ro (a live WAL database is observed, never frozen with immutable=1,
// which can read a stale pre-WAL state), both engines must make the
// Q1-absent result an explicit TERMINATE with the unledgered verdict (Q2/Q3
// run only when the ledger exists), and the PostgreSQL flow must warn that
// querying the absent relation aborts the read-only transaction.
func TestRunbookPreflightFlowConditionalAndWALSafe(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "docs", "project-context-protocol-identity-privilege.md"))
	if err != nil {
		t.Fatalf("read rollout runbook: %v", err)
	}
	runbook := string(source)

	// Live SQLite procedure: plain read-only mode, immutable=1 nowhere in a
	// live command, an explicit live-database warning, and the snapshot
	// alternative documented as checkpointed-only.
	if strings.Contains(runbook, "mode=ro&immutable=1") {
		t.Error("runbook live SQLite procedure still uses immutable=1; live WAL databases can read stale data")
	}
	if !strings.Contains(runbook, `file:${DB}?mode=ro`) {
		t.Error("runbook SQLite live command does not use ordinary mode=ro")
	}
	for _, token := range []string{
		"Do not use immutable=1 against the live database",
		"stale",
		"checkpointed",
	} {
		if !strings.Contains(runbook, token) {
			t.Errorf("runbook SQLite WAL-safety guidance missing %q", token)
		}
	}

	// Conditional flow: Q1-absent TERMINATES with the unledgered verdict in
	// BOTH engines, Q2/Q3 are gated on the ledger existing, the absent-table
	// error is documented for SQLite, and the absent-relation transaction
	// abort is documented for PostgreSQL.
	if got := strings.Count(runbook, "TERMINATE here with the UNLEDGERED"); got != 2 {
		t.Errorf("runbook has %d explicit Q1-absent TERMINATE verdicts, want 2 (SQLite and PostgreSQL)", got)
	}
	if got := strings.Count(runbook, "run Q2 and Q3 now"); got != 2 {
		t.Errorf("runbook has %d ledger-exists gates for Q2/Q3, want 2 (SQLite and PostgreSQL)", got)
	}
	for _, token := range []string{
		"no such table: cortex_v2_migrations",
		`relation "cortex_server_migrations" does not exist`,
		"ABORTS the read-only transaction",
	} {
		if !strings.Contains(runbook, token) {
			t.Errorf("runbook conditional-flow guidance missing %q", token)
		}
	}
}

// sqlFunctionDefinition returns the text of one function definition from a
// migration's SQL, from its CREATE marker through the terminating line-leading
// $$; so lock-ordering assertions stay scoped to a single routine. An empty
// result means the definition is absent.
func sqlFunctionDefinition(sqlText, marker string) string {
	start := strings.Index(sqlText, marker)
	if start < 0 {
		return ""
	}
	rest := sqlText[start:]
	end := strings.Index(rest, "\n$$;")
	if end < 0 {
		return rest
	}
	return rest[:end]
}

// TestPostgresServerMigration108PrincipalRWGating pins the canonical SRW
// principal gating contract of migration 108 (PG-00/PG-01/PG-02, MIG-01):
// ONE canonical transaction-scoped tenant+actor advisory key derived by
// cortex_principal_key, verify/bind readers taking the SHARED gate before
// any FOR SHARE identity re-read, EVERY identity invalidator taking the
// EXCLUSIVE gate before any identity row lock, token writers revalidating
// under the gate after a lock-free key resolve, throttled non-authoritative
// last_used_at telemetry that can never wait on a peer verifier or fail
// authentication, and zero session-scope advisory usage anywhere.
func TestPostgresServerMigration108PrincipalRWGating(t *testing.T) {
	migrations := mustPostgresMigrations(t)
	var subject *PostgresServerMigration
	for _, migration := range migrations {
		if migration.Version() == 108 {
			subject = migration
			break
		}
	}
	if subject == nil {
		t.Fatal("migration 108 (principal rw gating) is not registered")
	}
	if subject.Name() != "principal_rw_gating" {
		t.Errorf("migration 108 name = %q, want principal_rw_gating", subject.Name())
	}
	if subject.Checksum() == "" {
		t.Error("migration 108 checksum is empty")
	}
	sqlText := subject.SQL()

	// The canonical key helper: one 64-bit namespace over the domain tag
	// plus tenant plus actor public UUID, byte-identical to the T01 spike
	// selection, immutable and strict so every caller derives the same key.
	keyBody := sqlFunctionDefinition(sqlText, "CREATE OR REPLACE FUNCTION public.cortex_principal_key(p_tenant uuid, p_actor uuid)")
	if keyBody == "" {
		t.Fatal("migration 108 missing the canonical cortex_principal_key helper")
	}
	for _, token := range []string{
		"RETURNS bigint LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT AS $fn$",
		"SELECT hashtextextended('cortex:principal:' || p_tenant::text || ':' || p_actor::text, 0)",
	} {
		if !strings.Contains(keyBody, token) {
			t.Errorf("canonical key helper missing %q", token)
		}
	}
	// The helper is owned by the migration role and executable ONLY by it.
	for _, token := range []string{
		"ALTER FUNCTION public.cortex_principal_key(uuid, uuid) OWNER TO cortex_migration",
		"REVOKE ALL ON FUNCTION public.cortex_principal_key(uuid, uuid) FROM PUBLIC",
		"REVOKE ALL ON FUNCTION public.cortex_principal_key(uuid, uuid) FROM cortex_app",
		"REVOKE ALL ON FUNCTION public.cortex_principal_key(uuid, uuid) FROM cortex_admin",
		"GRANT EXECUTE ON FUNCTION public.cortex_principal_key(uuid, uuid) TO cortex_migration",
	} {
		if !strings.Contains(sqlText, token) {
			t.Errorf("canonical key helper privilege contract missing %q", token)
		}
	}
	if strings.Contains(sqlText, "GRANT EXECUTE ON FUNCTION public.cortex_principal_key(uuid, uuid) TO cortex_app") ||
		strings.Contains(sqlText, "GRANT EXECUTE ON FUNCTION public.cortex_principal_key(uuid, uuid) TO cortex_admin") {
		t.Error("canonical key helper must stay least-privilege (cortex_migration only)")
	}

	// Transaction-scoped advisory usage ONLY: no session advisory locks and
	// no SET-based session state anywhere in the migration.
	for _, banned := range []string{
		"pg_advisory_lock(",
		"pg_try_advisory_lock(",
		"pg_advisory_unlock",
		"pg_advisory_session_shared",
		"SET LOCAL",
		"SET SESSION",
	} {
		if strings.Contains(sqlText, banned) {
			t.Errorf("migration 108 contains session-scope advisory/SET usage %q", banned)
		}
	}

	// Every gate must route through the canonical helper (no parallel
	// hand-rolled key derivations inside the routine bodies).
	if got := strings.Count(sqlText, "pg_advisory_xact_lock_shared("); got != 2 {
		t.Errorf("shared advisory gate count = %d, want exactly 2 (verify and bind)", got)
	}
	if got := strings.Count(sqlText, "pg_advisory_xact_lock_shared(public.cortex_principal_key("); got != 2 {
		t.Errorf("shared gates not derived from the canonical helper: canonical count = %d, want 2", got)
	}
	if got := strings.Count(sqlText, "pg_advisory_xact_lock(public.cortex_principal_key("); got != 6 {
		t.Errorf("exclusive canonical gates = %d, want 6 (provision, activation, issue, rotate, revoke, bootstrap)", got)
	}
	if strings.Contains(sqlText, "hashtextextended(p_tenant_id::text || ':' || p_actor_public_id::text, 0)") {
		t.Error("migration 108 bootstrap still uses the legacy non-canonical advisory key")
	}

	// Lock ordering: the advisory gate precedes every lock-taking identity
	// statement in every gated routine.
	firstLockKeyword := func(body string) int {
		best := -1
		for _, keyword := range []string{
			"FOR UPDATE", "FOR SHARE",
			"INSERT INTO public.actor_subjects", "INSERT INTO public.principal_grants",
			"UPDATE public.actor_subjects", "UPDATE public.app_users", "UPDATE public.service_accounts",
			"INSERT INTO public.api_tokens", "UPDATE public.api_tokens",
			"DELETE FROM public.principal_grants",
		} {
			if idx := strings.Index(body, keyword); idx >= 0 && (best < 0 || idx < best) {
				best = idx
			}
		}
		return best
	}
	for _, gate := range []struct {
		name   string
		marker string
		shared bool
	}{
		{"verify", "CREATE OR REPLACE FUNCTION cortex_verify_token_principal(", true},
		{"bind", "CREATE OR REPLACE FUNCTION cortex_bind_principal(", true},
		{"provision_actor", "CREATE OR REPLACE FUNCTION cortex_provision_actor(", false},
		{"set_actor_active", "CREATE OR REPLACE FUNCTION cortex_set_actor_active(", false},
		{"issue_api_token", "CREATE OR REPLACE FUNCTION cortex_issue_api_token(", false},
		{"rotate_api_token", "CREATE OR REPLACE FUNCTION cortex_rotate_api_token(", false},
		{"revoke_api_token", "CREATE OR REPLACE FUNCTION cortex_revoke_api_token(", false},
		{"bootstrap", "CREATE OR REPLACE FUNCTION public.cortex_bootstrap_service_principal(", false},
	} {
		body := sqlFunctionDefinition(sqlText, gate.marker)
		if body == "" {
			t.Fatalf("migration 108 missing the replaced %s definition", gate.name)
		}
		gateToken := "pg_advisory_xact_lock(public.cortex_principal_key("
		if gate.shared {
			gateToken = "pg_advisory_xact_lock_shared(public.cortex_principal_key("
		}
		gateIdx := strings.Index(body, gateToken)
		if gateIdx < 0 {
			t.Errorf("%s does not acquire its %s canonical advisory gate", gate.name, map[bool]string{true: "shared", false: "exclusive"}[gate.shared])
			continue
		}
		if lockIdx := firstLockKeyword(body); lockIdx >= 0 && gateIdx > lockIdx {
			t.Errorf("%s acquires its advisory gate AFTER its first identity row lock", gate.name)
		}
	}

	// Verify: lock-free token resolve, shared gate, lock-free token re-read
	// plus FOR SHARE identity revalidation, and throttled best-effort
	// telemetry. The old same-token FOR UPDATE serialization is gone: the
	// verify path takes NO token-row lock at all (PG-02: concurrent
	// verifies of one token must never wait on any token-row lock, and no
	// verifier may wait on another verifier's telemetry row lock).
	verifyBody := sqlFunctionDefinition(sqlText, "CREATE OR REPLACE FUNCTION cortex_verify_token_principal(")
	if got := strings.Count(verifyBody, "FOR UPDATE"); got != 0 {
		t.Errorf("verify FOR UPDATE count = %d, want 0 (no token-row lock waits)", got)
	}
	if strings.Contains(verifyBody, "FOR SHARE OF t") {
		t.Error("verify still locks the token row FOR SHARE; the token re-read must stay lock-free under the shared gate")
	}
	for _, token := range []string{
		"FOR SHARE OF a",
		"pg_try_advisory_xact_lock(hashtextextended('cortex:principal-usage:'",
		"WHEN OTHERS THEN NULL",
		"last_used_at <= clock_timestamp() - interval '30 seconds'",
		"SET last_used_at = clock_timestamp()",
	} {
		if !strings.Contains(verifyBody, token) {
			t.Errorf("verify telemetry/revalidation contract missing %q", token)
		}
	}

	// Bind: lock-free actor resolve, shared gate, FOR SHARE revalidation of
	// grant version and token; no exclusive row locks remain.
	bindBody := sqlFunctionDefinition(sqlText, "CREATE OR REPLACE FUNCTION cortex_bind_principal(")
	if strings.Contains(bindBody, "FOR UPDATE") {
		t.Error("replaced bind still takes exclusive row locks; readers use FOR SHARE under the shared gate")
	}
	if !strings.Contains(bindBody, "FOR SHARE") {
		t.Error("replaced bind does not revalidate FOR SHARE under the shared gate")
	}

	// Token-ID writers must re-read and revalidate the locked token and
	// subject under the exclusive gate (lookup -> gate -> locked re-read).
	for _, name := range []string{"rotate_api_token", "revoke_api_token"} {
		body := sqlFunctionDefinition(sqlText, "CREATE OR REPLACE FUNCTION cortex_"+name+"(")
		if !strings.Contains(body, "FOR UPDATE OF t") {
			t.Errorf("%s does not re-read the token row FOR UPDATE under the exclusive gate", name)
		}
	}

	// Each replaced routine appears exactly once and stays additive.
	for _, marker := range []string{
		"CREATE OR REPLACE FUNCTION public.cortex_principal_key(",
		"CREATE OR REPLACE FUNCTION cortex_verify_token_principal(",
		"CREATE OR REPLACE FUNCTION cortex_bind_principal(",
		"CREATE OR REPLACE FUNCTION cortex_provision_actor(",
		"CREATE OR REPLACE FUNCTION cortex_set_actor_active(",
		"CREATE OR REPLACE FUNCTION cortex_issue_api_token(",
		"CREATE OR REPLACE FUNCTION cortex_rotate_api_token(",
		"CREATE OR REPLACE FUNCTION cortex_revoke_api_token(",
		"CREATE OR REPLACE FUNCTION public.cortex_bootstrap_service_principal(",
	} {
		if got := strings.Count(sqlText, marker); got != 1 {
			t.Errorf("migration 108 defines %s %d times, want exactly one additive replacement", marker, got)
		}
	}

	// The application-role EXECUTE contract is reasserted for every
	// replaced cortex_app routine.
	for _, token := range []string{
		"REVOKE ALL ON FUNCTION cortex_verify_token_principal(text,bytea,text) FROM PUBLIC",
		"GRANT EXECUTE ON FUNCTION cortex_verify_token_principal(text,bytea,text) TO cortex_app",
		"REVOKE ALL ON FUNCTION cortex_bind_principal(uuid,text,bigint) FROM PUBLIC",
		"GRANT EXECUTE ON FUNCTION cortex_bind_principal(uuid,text,bigint) TO cortex_app",
		"REVOKE ALL ON FUNCTION cortex_provision_actor(uuid,text,text,jsonb,text) FROM PUBLIC",
		"GRANT EXECUTE ON FUNCTION cortex_provision_actor(uuid,text,text,jsonb,text) TO cortex_app",
		"REVOKE ALL ON FUNCTION cortex_set_actor_active(uuid,boolean,text) FROM PUBLIC",
		"GRANT EXECUTE ON FUNCTION cortex_set_actor_active(uuid,boolean,text) TO cortex_app",
		"REVOKE ALL ON FUNCTION cortex_issue_api_token(uuid,text,text,text[],uuid[],timestamptz,text) FROM PUBLIC",
		"GRANT EXECUTE ON FUNCTION cortex_issue_api_token(uuid,text,text,text[],uuid[],timestamptz,text) TO cortex_app",
		"REVOKE ALL ON FUNCTION cortex_rotate_api_token(uuid,text,text) FROM PUBLIC",
		"GRANT EXECUTE ON FUNCTION cortex_rotate_api_token(uuid,text,text) TO cortex_app",
		"REVOKE ALL ON FUNCTION cortex_revoke_api_token(uuid,text) FROM PUBLIC",
		"GRANT EXECUTE ON FUNCTION cortex_revoke_api_token(uuid,text) TO cortex_app",
		"ALTER FUNCTION public.cortex_bootstrap_service_principal(uuid,uuid,uuid,text,text,jsonb,text,text,text) OWNER TO cortex_migration",
		"GRANT EXECUTE ON FUNCTION public.cortex_bootstrap_service_principal(uuid,uuid,uuid,text,text,jsonb,text,text,text) TO cortex_migration",
	} {
		if !strings.Contains(sqlText, token) {
			t.Errorf("migration 108 privilege reassertion missing %q", token)
		}
	}

	// Forward-only additive posture: no destructive statement at all. The
	// only two DELETE statements are the exact pinned ones carried over
	// verbatim from migration 106's replaced routines: the binder's stale
	// tenant-context housekeeping (rows of EARLIER transactions of the same
	// backend) and the bootstrap reconciler's scoped mutable-grant-set
	// rewrite. Every other delete/destroy path is banned.
	upper := strings.ToUpper(sqlText)
	contextCleanup := strings.ToUpper("DELETE FROM public.cortex_tenant_context\n     WHERE backend_pid = pg_backend_pid() AND transaction_id <> txid_current();")
	grantReplace := strings.ToUpper("DELETE FROM public.principal_grants WHERE tenant_id = p_tenant_id AND actor_public_id = p_actor_public_id;")
	if got := strings.Count(upper, contextCleanup); got != 1 {
		t.Fatalf("migration 108 must contain exactly one pinned tenant-context housekeeping DELETE (found %d)", got)
	}
	if got := strings.Count(upper, grantReplace); got != 1 {
		t.Fatalf("migration 108 must contain exactly one pinned scoped principal_grants reconcile DELETE (found %d)", got)
	}
	upper = strings.ReplaceAll(upper, contextCleanup, "")
	upper = strings.ReplaceAll(upper, grantReplace, "")
	for _, banned := range []string{
		"DROP TABLE", "DROP INDEX", "DROP FUNCTION", "DROP TRIGGER", "DROP SCHEMA",
		"TRUNCATE", "DELETE FROM", "ON DELETE CASCADE", "ON DELETE SET NULL",
	} {
		if strings.Contains(upper, banned) {
			t.Errorf("migration 108 contains destructive statement %q", banned)
		}
	}
}
