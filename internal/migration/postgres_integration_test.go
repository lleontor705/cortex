//go:build postgres_integration

package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lleontor705/cortex/testutil/postgrestest"
)

func TestMain(m *testing.M) {
	code := runPostgresMigrationTests(m)
	os.Exit(code)
}

func runPostgresMigrationTests(m *testing.M) int {
	dsn := os.Getenv("CORTEX_TEST_POSTGRES_MIGRATION_DSN")
	if dsn == "" {
		return m.Run()
	}
	isolatedDSN, cleanup, err := isolatedPostgresDatabase(dsn, "migration")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create isolated PostgreSQL migration database: %v\n", err)
		return 1
	}
	os.Setenv("CORTEX_TEST_POSTGRES_MIGRATION_DSN", isolatedDSN)
	defer cleanup()
	return m.Run()
}

func isolatedPostgresDatabase(dsn, packageName string) (string, func(), error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := postgrestest.EnsureMigrationRoles(ctx, dsn); err != nil {
		return "", nil, err
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return "", nil, err
	}
	database := fmt.Sprintf("cortex_%s_%d", packageName, time.Now().UnixNano())
	admin, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return "", nil, err
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{database}.Sanitize()); err != nil {
		return "", nil, err
	}
	isolatedDSN, err := postgresDSNWithDatabase(dsn, database)
	if err != nil {
		return "", nil, err
	}
	cleanup := func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		connection, connectErr := pgx.ConnectConfig(cleanupCtx, config)
		if connectErr != nil {
			fmt.Fprintf(os.Stderr, "connect to drop isolated PostgreSQL database %s: %v\n", database, connectErr)
			return
		}
		defer connection.Close(cleanupCtx)
		if _, dropErr := connection.Exec(cleanupCtx, `DROP DATABASE `+pgx.Identifier{database}.Sanitize()+` WITH (FORCE)`); dropErr != nil {
			fmt.Fprintf(os.Stderr, "drop isolated PostgreSQL database %s: %v\n", database, dropErr)
		}
	}
	return isolatedDSN, cleanup, nil
}

func postgresDSNWithDatabase(dsn, database string) (string, error) {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return "", fmt.Errorf("PostgreSQL test DSN must use postgres:// or postgresql://")
	}
	parsed.Path = "/" + database
	return parsed.String(), nil
}

func TestPostgresMigration104RealLifecycle(t *testing.T) {
	dsn := os.Getenv("CORTEX_TEST_POSTGRES_MIGRATION_DSN")
	if dsn == "" {
		t.Fatal("CORTEX_TEST_POSTGRES_MIGRATION_DSN is required for postgres_integration tests")
	}
	ctx := context.Background()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	var version, database string
	if err := db.QueryRowContext(ctx, `SELECT current_setting('server_version'), current_database()`).Scan(&version, &database); err != nil {
		t.Fatal(err)
	}
	t.Logf("real PostgreSQL server_version=%s database=%s; config=postgres_integration DSNs with scripts/postgres/bootstrap-authz.sql", version, database)

	migrations := mustPostgresMigrations(t)
	for _, migration := range migrations[:4] {
		if err := migration.Apply(ctx, db); err != nil {
			t.Fatalf("apply historical migration %d: %v", migration.Version(), err)
		}
	}
	tenant := "00000000-0000-0000-0000-000000000104"
	var observationID int64
	if err := db.QueryRowContext(ctx, `
		WITH organization AS (
			INSERT INTO organizations(tenant_id,name) VALUES($1,'upgrade fixture')
			ON CONFLICT (tenant_id) DO UPDATE SET name=EXCLUDED.name RETURNING id
		), w AS (
			INSERT INTO workspaces(tenant_id,organization_id,name)
			SELECT $1,id,'upgrade workspace' FROM organization RETURNING id
		), s AS (
			INSERT INTO sessions(tenant_id,workspace_id) SELECT $1,id FROM w RETURNING id
		)
		INSERT INTO observations(tenant_id,session_id,type,title,content)
		SELECT $1,id,'manual','pre-104','preserved' FROM s RETURNING id`, tenant).Scan(&observationID); err != nil {
		t.Fatalf("seed upgrade data: %v", err)
	}
	if err := migrations[4].Apply(ctx, db); err != nil {
		t.Fatalf("upgrade to 104: %v", err)
	}
	var content string
	if err := db.QueryRowContext(ctx, `SELECT content FROM observations WHERE tenant_id=$1 AND id=$2`, tenant, observationID).Scan(&content); err != nil || content != "preserved" {
		t.Fatalf("upgrade data content=%q err=%v", content, err)
	}
	if err := ApplyPostgresServerMigrations(ctx, db); err != nil {
		t.Fatalf("reapply: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close before restart: %v", err)
	}
	db, err = sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("reopen after restart: %v", err)
	}
	if err := ApplyPostgresServerMigrations(ctx, db); err != nil {
		t.Fatalf("restart reapply: %v", err)
	}

	if _, err := db.ExecContext(ctx, `UPDATE cortex_server_migrations SET checksum='tampered' WHERE version=104`); err != nil {
		t.Fatal(err)
	}
	if err := migrations[4].Apply(ctx, db); err == nil || !strings.Contains(err.Error(), "104 checksum mismatch") {
		t.Fatalf("checksum mismatch error=%v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE cortex_server_migrations SET checksum=$1 WHERE version=104`, migrations[4].Checksum()); err != nil {
		t.Fatal(err)
	}

	failing := &PostgresServerMigration{version: 105, name: "injected_failure", sql: `CREATE TABLE migration_105_rolled_back(id integer); SELECT 1/0`, checksum: "injected"}
	if err := failing.Apply(ctx, db); err == nil {
		t.Fatal("failed DDL unexpectedly succeeded")
	}
	var tableExists, ledgerExists bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('public.migration_105_rolled_back') IS NOT NULL`).Scan(&tableExists); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cortex_server_migrations WHERE version=105)`).Scan(&ledgerExists); err != nil {
		t.Fatal(err)
	}
	if tableExists || ledgerExists {
		t.Fatalf("failed DDL leaked table=%v ledger=%v", tableExists, ledgerExists)
	}
	for version, checksum := range postgresHistoricalChecksums {
		var got string
		if err := db.QueryRowContext(ctx, `SELECT checksum FROM cortex_server_migrations WHERE version=$1`, version).Scan(&got); err != nil || got != checksum {
			t.Fatalf("historical migration %d checksum=%q want=%q err=%v", version, got, checksum, err)
		}
	}
	var receiptColumns string
	if err := db.QueryRowContext(ctx, `SELECT string_agg(column_name, ',' ORDER BY ordinal_position) FROM information_schema.columns WHERE table_schema='public' AND table_name='handoff_receipts'`).Scan(&receiptColumns); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(receiptColumns, "tenant_id") {
		t.Fatal(fmt.Sprintf("104 receipt columns=%s", receiptColumns))
	}

	t.Run("down is forward-only for every ledgered migration 100-104 with zero mutation", func(t *testing.T) {
		before := postgresMigrationSnapshot(t, ctx, db)
		for _, migration := range migrations {
			if err := migration.Down(ctx, db); err == nil || !errors.Is(err, ErrForwardOnly) {
				t.Fatalf("Down(%d) err=%v; want errors.Is ErrForwardOnly", migration.Version(), err)
			}
		}
		if after := postgresMigrationSnapshot(t, ctx, db); after != before {
			t.Fatal("Down executed DDL/DML: schema, ledger, or data snapshot changed")
		}
	})

	t.Run("unledgered Down is forward-only and mutates nothing", func(t *testing.T) {
		if _, err := db.ExecContext(ctx, `DELETE FROM cortex_server_migrations WHERE version=104`); err != nil {
			t.Fatal(err)
		}
		before := postgresMigrationSnapshot(t, ctx, db)
		if err := migrations[4].Down(ctx, db); err == nil || !errors.Is(err, ErrForwardOnly) {
			t.Fatalf("unledgered Down(104) err=%v; want errors.Is ErrForwardOnly", err)
		}
		if after := postgresMigrationSnapshot(t, ctx, db); after != before {
			t.Fatal("unledgered Down(104) mutated the database")
		}
		// A stale unledgered artifact must fail closed on re-apply: 104 has no
		// IF NOT EXISTS, so the pre-existing table rejects the DDL.
		if err := migrations[4].Apply(ctx, db); err == nil {
			t.Fatal("re-apply over a stale unledgered artifact unexpectedly succeeded")
		}
		// Reviewed compensating recovery for the fixture: restore the ledger
		// row matching the exact applied 104 artifacts so the remaining
		// matrix stays consistent (never a destructive Down).
		if _, err := db.ExecContext(ctx,
			`INSERT INTO cortex_server_migrations (version, name, checksum) VALUES ($1, $2, $3)`,
			migrations[4].Version(), migrations[4].Name(), migrations[4].Checksum()); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("future ledger version fails closed on Apply", func(t *testing.T) {
		if _, err := db.ExecContext(ctx, `INSERT INTO cortex_server_migrations(version,name,checksum) VALUES(105,'future','future')`); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = db.ExecContext(ctx, `DELETE FROM cortex_server_migrations WHERE version=105`) })
		if err := migrations[0].Apply(ctx, db); err == nil || !errors.Is(err, ErrFutureMigration) {
			t.Fatalf("Apply(100) with future ledger version 105 err=%v; want errors.Is ErrFutureMigration", err)
		}
		if err := ApplyPostgresServerMigrations(ctx, db); err == nil || !errors.Is(err, ErrFutureMigration) {
			t.Fatalf("ApplyPostgresServerMigrations with future ledger version 105 err=%v; want errors.Is ErrFutureMigration", err)
		}
	})
}

// postgresMigrationSnapshot builds a deterministic digest of the schema
// (public columns), the migration ledger, and tenant row counts for
// zero-mutation proofs of the Down forward-only policy.
func postgresMigrationSnapshot(t *testing.T, ctx context.Context, db *sql.DB) string {
	t.Helper()
	var b strings.Builder

	columns, err := db.QueryContext(ctx, `
		SELECT table_name, column_name, data_type
		FROM information_schema.columns
		WHERE table_schema = 'public'
		ORDER BY table_name, column_name`)
	if err != nil {
		t.Fatalf("snapshot columns: %v", err)
	}
	for columns.Next() {
		var table, column, dataType string
		if err := columns.Scan(&table, &column, &dataType); err != nil {
			t.Fatalf("scan columns: %v", err)
		}
		fmt.Fprintf(&b, "col|%s|%s|%s\n", table, column, dataType)
	}
	columns.Close()
	if err := columns.Err(); err != nil {
		t.Fatalf("iterate columns: %v", err)
	}

	ledger, err := db.QueryContext(ctx, `SELECT version, name, checksum FROM cortex_server_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("snapshot ledger: %v", err)
	}
	for ledger.Next() {
		var version int
		var name, checksum string
		if err := ledger.Scan(&version, &name, &checksum); err != nil {
			t.Fatalf("scan ledger: %v", err)
		}
		fmt.Fprintf(&b, "ledger|%d|%s|%s\n", version, name, checksum)
	}
	ledger.Close()
	if err := ledger.Err(); err != nil {
		t.Fatalf("iterate ledger: %v", err)
	}

	for _, table := range []string{"organizations", "workspaces", "sessions", "observations", "handoff_receipts", "audit_events"} {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil {
			t.Fatalf("snapshot count(%s): %v", table, err)
		}
		fmt.Fprintf(&b, "count|%s|%d\n", table, count)
	}
	return b.String()
}
