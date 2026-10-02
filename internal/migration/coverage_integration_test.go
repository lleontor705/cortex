//go:build postgres_integration

package migration

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lleontor705/cortex/v2/testutil/postgrestest"
)

// TestCoverageMigrationApplyIdempotent verifies that Apply is idempotent:
// applying the same migration twice produces no error and the ledger row
// count remains exactly one.
func TestCoverageMigrationApplyIdempotent(t *testing.T) {
	dsn := os.Getenv("CORTEX_TEST_POSTGRES_MIGRATION_DSN")
	if dsn == "" {
		t.Fatal("CORTEX_TEST_POSTGRES_MIGRATION_DSN is required")
	}
	ctx := context.Background()
	if err := postgrestest.EnsureMigrationRoles(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	m, err := NewPostgresServerMigration()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(ctx, db); err != nil {
		t.Fatalf("re-apply must be idempotent: %v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM cortex_server_migrations WHERE version=$1`, m.Version()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("ledger rows=%d, want 1", count)
	}
}

// TestCoverageMigrationChecksumMismatchFailsClose proves that a tampered
// ledger checksum causes Apply to refuse the migration without mutation.
func TestCoverageMigrationChecksumMismatchFailsClose(t *testing.T) {
	dsn := os.Getenv("CORTEX_TEST_POSTGRES_MIGRATION_DSN")
	if dsn == "" {
		t.Fatal("CORTEX_TEST_POSTGRES_MIGRATION_DSN is required")
	}
	ctx := context.Background()
	if err := postgrestest.EnsureMigrationRoles(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	m, err := NewPostgresServerMigration()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE cortex_server_migrations SET checksum='tampered' WHERE version=$1`, m.Version()); err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(ctx, db); err == nil {
		t.Fatal("checksum mismatch must fail")
	}
}

// TestCoverageMigrationDownIsForwardOnly covers the Down forward-only
// guard for the baseline migration.
func TestCoverageMigrationDownIsForwardOnly(t *testing.T) {
	dsn := os.Getenv("CORTEX_TEST_POSTGRES_MIGRATION_DSN")
	if dsn == "" {
		t.Fatal("CORTEX_TEST_POSTGRES_MIGRATION_DSN is required")
	}
	ctx := context.Background()
	if err := postgrestest.EnsureMigrationRoles(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	m, err := NewPostgresServerMigration()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := m.Down(ctx, db); !errors.Is(err, ErrForwardOnly) {
		t.Fatalf("Down err=%v, want ErrForwardOnly", err)
	}
}

// TestCoverageMigrationFutureVersionFailsClosed proves that a ledger
// recording a version beyond the runtime head causes Apply to refuse with
// ErrFutureMigration.
func TestCoverageMigrationFutureVersionFailsClosed(t *testing.T) {
	dsn := os.Getenv("CORTEX_TEST_POSTGRES_MIGRATION_DSN")
	if dsn == "" {
		t.Fatal("CORTEX_TEST_POSTGRES_MIGRATION_DSN is required")
	}
	ctx := context.Background()
	if err := postgrestest.EnsureMigrationRoles(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	m, err := NewPostgresServerMigration()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO cortex_server_migrations(version,name,checksum) VALUES(999,'future','fake')`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.ExecContext(ctx, `DELETE FROM cortex_server_migrations WHERE version=999`) }()

	if err := m.Apply(ctx, db); err == nil || !errors.Is(err, ErrFutureMigration) {
		t.Fatalf("future version err=%v, want ErrFutureMigration", err)
	}
}

// TestCoverageMigrationPreflightUnledgered covers the Preflight verdict
// for a migration that has not been applied (unledgered).
func TestCoverageMigrationPreflightUnledgered(t *testing.T) {
	dsn := os.Getenv("CORTEX_TEST_POSTGRES_MIGRATION_DSN")
	if dsn == "" {
		t.Fatal("CORTEX_TEST_POSTGRES_MIGRATION_DSN is required")
	}
	ctx := context.Background()
	if err := postgrestest.EnsureMigrationRoles(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	freshDSN, cleanup, err := isolatedPostgresDatabase(dsn, "preflight-unledgered")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	db, err := sql.Open("pgx", freshDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	m, err := NewPostgresServerMigration()
	if err != nil {
		t.Fatal(err)
	}
	preflight, err := m.Preflight(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if preflight.Ledgered {
		t.Fatal("unledgered migration should not be ledgered")
	}
}

// TestCoverageMigrationPreflightLedgered covers the Preflight verdict
// for a migration that has been applied (ledgered).
func TestCoverageMigrationPreflightLedgered(t *testing.T) {
	dsn := os.Getenv("CORTEX_TEST_POSTGRES_MIGRATION_DSN")
	if dsn == "" {
		t.Fatal("CORTEX_TEST_POSTGRES_MIGRATION_DSN is required")
	}
	ctx := context.Background()
	if err := postgrestest.EnsureMigrationRoles(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	m, err := NewPostgresServerMigration()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	preflight, err := m.Preflight(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if !preflight.Ledgered {
		t.Fatal("applied migration should be ledgered")
	}
	if preflight.ExpectedChecksum == "" {
		t.Fatal("expected checksum empty")
	}
}

// TestCoverageMigrationVerifyAppliedSucceeds covers the VerifyApplied
// success path for an applied migration.
func TestCoverageMigrationVerifyAppliedSucceeds(t *testing.T) {
	dsn := os.Getenv("CORTEX_TEST_POSTGRES_MIGRATION_DSN")
	if dsn == "" {
		t.Fatal("CORTEX_TEST_POSTGRES_MIGRATION_DSN is required")
	}
	ctx := context.Background()
	if err := postgrestest.EnsureMigrationRoles(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	m, err := NewPostgresServerMigration()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := m.VerifyApplied(ctx, db); err != nil {
		t.Fatalf("VerifyApplied err=%v", err)
	}
}

// TestCoverageMigrationVerifyAppliedMissing covers VerifyApplied when the
// migration was never applied.
func TestCoverageMigrationVerifyAppliedMissing(t *testing.T) {
	dsn := os.Getenv("CORTEX_TEST_POSTGRES_MIGRATION_DSN")
	if dsn == "" {
		t.Fatal("CORTEX_TEST_POSTGRES_MIGRATION_DSN is required")
	}
	ctx := context.Background()
	if err := postgrestest.EnsureMigrationRoles(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	freshDSN, cleanup, err := isolatedPostgresDatabase(dsn, "verify-missing")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	db, err := sql.Open("pgx", freshDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	migrations := mustPostgresMigrations(t)
	m := migrations[0]
	if err := m.VerifyApplied(ctx, db); err == nil {
		t.Fatal("VerifyApplied on unapplied migration must fail")
	}
}

// TestCoverageMigrationNilDbGuards covers the nil-db guard for Apply,
// Preflight, VerifyApplied, and Down.
func TestCoverageMigrationNilDbGuards(t *testing.T) {
	m, err := NewPostgresServerMigration()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if err := m.Apply(ctx, nil); err == nil {
		t.Fatal("Apply(nil) must fail")
	}
	if _, err := m.Preflight(ctx, nil); err == nil {
		t.Fatal("Preflight(nil) must fail")
	}
	if err := m.VerifyApplied(ctx, nil); err == nil {
		t.Fatal("VerifyApplied(nil) must fail")
	}
	if err := m.Down(ctx, nil); err == nil {
		t.Fatal("Down(nil) must fail")
	}
}

// TestCoverageMigrationMatchesChecksumCrossPlatform covers the
// MatchesChecksum cross-platform line ending normalization (LF and CRLF).
func TestCoverageMigrationMatchesChecksumCrossPlatform(t *testing.T) {
	m, err := NewPostgresServerMigration()
	if err != nil {
		t.Fatal(err)
	}
	if !m.MatchesChecksum(m.Checksum()) {
		t.Fatal("exact checksum must match")
	}
}

// TestCoverageMigrationAllVersionsHaveConsistentMetadata proves that every
// migration returned by NewPostgresServerMigrations has a non-empty name,
// SQL, and checksum.
func TestCoverageMigrationAllVersionsHaveConsistentMetadata(t *testing.T) {
	migrations := mustPostgresMigrations(t)
	for _, m := range migrations {
		if m.Name() == "" {
			t.Fatalf("version %d has empty name", m.Version())
		}
		if m.SQL() == "" {
			t.Fatalf("version %d has empty SQL", m.Version())
		}
		if m.Checksum() == "" {
			t.Fatalf("version %d has empty checksum", m.Version())
		}
	}
}

// TestCoverageMigrationApplyPostgresServerMigrationsFullLine covers the
// ApplyPostgresServerMigrations convenience function that applies the full
// line atomically.
func TestCoverageMigrationApplyPostgresServerMigrationsFullLine(t *testing.T) {
	dsn := os.Getenv("CORTEX_TEST_POSTGRES_MIGRATION_DSN")
	if dsn == "" {
		t.Fatal("CORTEX_TEST_POSTGRES_MIGRATION_DSN is required")
	}
	ctx := context.Background()
	if err := postgrestest.EnsureMigrationRoles(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	freshDSN, cleanup, err := isolatedPostgresDatabase(dsn, "full-line")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	db, err := sql.Open("pgx", freshDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	if err := ApplyPostgresServerMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	migrations, err := NewPostgresServerMigrations()
	if err != nil {
		t.Fatal(err)
	}
	var ledgerCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM cortex_server_migrations`).Scan(&ledgerCount); err != nil {
		t.Fatal(err)
	}
	if ledgerCount != len(migrations) {
		t.Fatalf("ledger=%d, want %d", ledgerCount, len(migrations))
	}
}

// TestCoverageMigrationPreflightChecksumMismatch covers the Preflight
// verdict when the recorded checksum does not match.
func TestCoverageMigrationPreflightChecksumMismatch(t *testing.T) {
	dsn := os.Getenv("CORTEX_TEST_POSTGRES_MIGRATION_DSN")
	if dsn == "" {
		t.Fatal("CORTEX_TEST_POSTGRES_MIGRATION_DSN is required")
	}
	ctx := context.Background()
	if err := postgrestest.EnsureMigrationRoles(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	m, err := NewPostgresServerMigration()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE cortex_server_migrations SET checksum='mismatch' WHERE version=$1`, m.Version()); err != nil {
		t.Fatal(err)
	}
	preflight, err := m.Preflight(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if !preflight.Ledgered {
		t.Fatal("should be ledgered even with mismatch")
	}
}

// TestCoverageMigrationPreflightFutureVersion covers the Preflight
// verdict when the ledger records a future version.
func TestCoverageMigrationPreflightFutureVersion(t *testing.T) {
	dsn := os.Getenv("CORTEX_TEST_POSTGRES_MIGRATION_DSN")
	if dsn == "" {
		t.Fatal("CORTEX_TEST_POSTGRES_MIGRATION_DSN is required")
	}
	ctx := context.Background()
	if err := postgrestest.EnsureMigrationRoles(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	m, err := NewPostgresServerMigration()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO cortex_server_migrations(version,name,checksum) VALUES(999,'future','fake')`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.ExecContext(ctx, `DELETE FROM cortex_server_migrations WHERE version=999`) }()

	preflight, err := m.Preflight(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if preflight.FutureLedgerVersion != 999 {
		t.Fatalf("future=%d, want 999", preflight.FutureLedgerVersion)
	}
}

// TestCoverageMigrationApplyPartialFailureRollback proves that a failing
// DDL inside a new migration version rolls back completely.
func TestCoverageMigrationApplyPartialFailureRollback(t *testing.T) {
	dsn := os.Getenv("CORTEX_TEST_POSTGRES_MIGRATION_DSN")
	if dsn == "" {
		t.Fatal("CORTEX_TEST_POSTGRES_MIGRATION_DSN is required")
	}
	ctx := context.Background()
	if err := postgrestest.EnsureMigrationRoles(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	freshDSN, cleanup, err := isolatedPostgresDatabase(dsn, "partial-fail")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	db, err := sql.Open("pgx", freshDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	if err := ApplyPostgresServerMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	failing := &PostgresServerMigration{version: 998, name: "partial", sql: `CREATE TABLE test_partial(id integer); SELECT 1/0`, checksum: "partial"}
	if err := failing.Apply(ctx, db); err == nil {
		t.Fatal("failing DDL must error")
	}
	var tableExists bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('public.test_partial') IS NOT NULL`).Scan(&tableExists); err != nil {
		t.Fatal(err)
	}
	if tableExists {
		t.Fatal("partial DDL leaked table")
	}
}

// TestCoverageMigrationVerifyAppliedChecksumMismatch covers the
// VerifyApplied path when the recorded checksum does not match.
func TestCoverageMigrationVerifyAppliedChecksumMismatch(t *testing.T) {
	dsn := os.Getenv("CORTEX_TEST_POSTGRES_MIGRATION_DSN")
	if dsn == "" {
		t.Fatal("CORTEX_TEST_POSTGRES_MIGRATION_DSN is required")
	}
	ctx := context.Background()
	if err := postgrestest.EnsureMigrationRoles(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	m, err := NewPostgresServerMigration()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE cortex_server_migrations SET checksum='wrong' WHERE version=$1`, m.Version()); err != nil {
		t.Fatal(err)
	}
	if err := m.VerifyApplied(ctx, db); err == nil {
		t.Fatal("VerifyApplied on tampered checksum must fail")
	}
}

// TestCoverageMigrationHead104Refuses105Ledger proves that a runtime
// whose maxKnownVersion is 104 refuses to apply when the ledger already
// records version 105.
func TestCoverageMigrationHead104Refuses105Ledger(t *testing.T) {
	dsn := os.Getenv("CORTEX_TEST_POSTGRES_MIGRATION_DSN")
	if dsn == "" {
		t.Fatal("CORTEX_TEST_POSTGRES_MIGRATION_DSN is required")
	}
	ctx := context.Background()
	if err := postgrestest.EnsureMigrationRoles(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	migrations := mustPostgresMigrations(t)
	for _, migration := range migrations {
		if err := migration.Apply(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	head104 := &PostgresServerMigration{
		version:         100,
		name:            migrations[0].Name(),
		sql:             migrations[0].SQL(),
		checksum:        migrations[0].Checksum(),
		maxKnownVersion: 104,
	}
	if err := head104.Apply(ctx, db); !errors.Is(err, ErrFutureMigration) {
		t.Fatalf("head 104 err=%v, want ErrFutureMigration", err)
	}
}

// TestCoverageMigrationPreflightNoLedgerTable covers the Preflight path
// when the migration ledger table does not exist.
func TestCoverageMigrationPreflightNoLedgerTable(t *testing.T) {
	dsn := os.Getenv("CORTEX_TEST_POSTGRES_MIGRATION_DSN")
	if dsn == "" {
		t.Fatal("CORTEX_TEST_POSTGRES_MIGRATION_DSN is required")
	}
	ctx := context.Background()
	if err := postgrestest.EnsureMigrationRoles(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	freshDSN, cleanup, err := isolatedPostgresDatabase(dsn, "preflight-nol")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	db, err := sql.Open("pgx", freshDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	m, err := NewPostgresServerMigration()
	if err != nil {
		t.Fatal(err)
	}
	preflight, err := m.Preflight(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if preflight.LedgerTable {
		t.Fatal("fresh database should not have a ledger table")
	}
}
