package migration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
	if len(migrations) != 6 {
		t.Fatalf("migration count = %d, want 6", len(migrations))
	}
	for i, want := range []int{100, 101, 102, 103, 104, 105} {
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

// TestPostgresServerMigrationHeadIs105 pins the runtime head: every migration
// in the line must refuse ledgers recording versions beyond 105 so a head 105
// binary fails closed (ErrFutureMigration) against a newer database, and a
// head 104 binary fails closed against a 105 ledger.
func TestPostgresServerMigrationHeadIs105(t *testing.T) {
	migrations := mustPostgresMigrations(t)
	for _, migration := range migrations {
		if migration.maxKnownVersion != 105 {
			t.Errorf("migration %d maxKnownVersion = %d, want 105", migration.Version(), migration.maxKnownVersion)
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
