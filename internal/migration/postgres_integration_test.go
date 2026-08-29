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
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lleontor705/cortex/v2/testutil/postgrestest"
)

func TestMain(m *testing.M) {
	code := runPostgresMigrationTests(m)
	os.Exit(code)
}

func runPostgresMigrationTests(m *testing.M) int {
	return runIsolatedMigrationEnv(os.Getenv("CORTEX_TEST_POSTGRES_MIGRATION_DSN"), isolatedPostgresDatabase, os.Setenv, m.Run)
}

// runIsolatedMigrationEnv wires TestMain to an isolated migration database.
// The createDB/setenv seams exist so tests can prove the cleanup ordering
// contract without PostgreSQL or process-environment mutation.
func runIsolatedMigrationEnv(
	dsn string,
	createDB func(dsn, packageName string) (string, func(), error),
	setenv func(key, value string) error,
	runTests func() int,
) int {
	if dsn == "" {
		return runTests()
	}
	isolatedDSN, cleanup, err := createDB(dsn, "migration")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create isolated PostgreSQL migration database: %v\n", err)
		return 1
	}
	// The cleanup defer is registered immediately after isolated database
	// creation, before the Setenv attempt, so every later failure path
	// still drops the isolated database instead of leaking it.
	defer cleanup()
	if err := setenv("CORTEX_TEST_POSTGRES_MIGRATION_DSN", isolatedDSN); err != nil {
		fmt.Fprintf(os.Stderr, "set CORTEX_TEST_POSTGRES_MIGRATION_DSN to isolated database: %v\n", err)
		return 1
	}
	return runTests()
}

// TestIsolatedMigrationEnvCleanupOrder is the bounded oracle for the
// IDP-T03B-LINT-FIX isolated-database leak. It drives runIsolatedMigrationEnv
// through fakes (no PostgreSQL, no environment mutation) and proves the
// cleanup ordering contract on every path.
func TestIsolatedMigrationEnvCleanupOrder(t *testing.T) {
	const baseDSN = "postgres://oracle:oracle@127.0.0.1:5432/oracle"
	const isolatedDSN = baseDSN + "/isolated"

	t.Run("setenv failure drops the isolated database without running tests", func(t *testing.T) {
		var events []string
		code := runIsolatedMigrationEnv(
			baseDSN,
			func(dsn, packageName string) (string, func(), error) {
				events = append(events, "create")
				if dsn != baseDSN || packageName != "migration" {
					t.Fatalf("create dsn=%q package=%q; want base DSN and package migration", dsn, packageName)
				}
				return isolatedDSN, func() { events = append(events, "cleanup") }, nil
			},
			func(key, value string) error {
				events = append(events, "setenv")
				if key != "CORTEX_TEST_POSTGRES_MIGRATION_DSN" {
					t.Fatalf("setenv key=%q; want CORTEX_TEST_POSTGRES_MIGRATION_DSN", key)
				}
				if value != isolatedDSN {
					t.Fatalf("setenv value=%q; want the isolated DSN", value)
				}
				return errors.New("injected setenv failure")
			},
			func() int {
				t.Fatal("tests must not run when Setenv fails")
				return 0
			},
		)
		if code != 1 {
			t.Fatalf("exit code=%d; want 1 on Setenv failure", code)
		}
		if got, want := strings.Join(events, ","), "create,setenv,cleanup"; got != want {
			t.Fatalf("events=%s; want %s (cleanup must fire on the Setenv failure path or the database leaks)", got, want)
		}
	})

	t.Run("success runs tests exactly once before exactly one cleanup", func(t *testing.T) {
		var events []string
		code := runIsolatedMigrationEnv(
			baseDSN,
			func(dsn, packageName string) (string, func(), error) {
				events = append(events, "create")
				return isolatedDSN, func() { events = append(events, "cleanup") }, nil
			},
			func(key, value string) error {
				events = append(events, "setenv")
				return nil
			},
			func() int {
				events = append(events, "runTests")
				return 7
			},
		)
		if code != 7 {
			t.Fatalf("exit code=%d; want the runTests result 7", code)
		}
		if got, want := strings.Join(events, ","), "create,setenv,runTests,cleanup"; got != want {
			t.Fatalf("events=%s; want %s (tests run once, cleanup deferred to the end)", got, want)
		}
	})

	t.Run("creation failure skips setenv, cleanup and tests", func(t *testing.T) {
		var events []string
		code := runIsolatedMigrationEnv(
			baseDSN,
			func(dsn, packageName string) (string, func(), error) {
				events = append(events, "create")
				return "", nil, errors.New("injected creation failure")
			},
			func(key, value string) error {
				events = append(events, "setenv")
				return nil
			},
			func() int {
				events = append(events, "runTests")
				return 0
			},
		)
		if code != 1 {
			t.Fatalf("exit code=%d; want 1 on creation failure", code)
		}
		if got, want := strings.Join(events, ","), "create"; got != want {
			t.Fatalf("events=%s; want %s (nothing was created, so nothing may run or drop)", got, want)
		}
	})

	t.Run("empty DSN runs tests directly", func(t *testing.T) {
		var events []string
		code := runIsolatedMigrationEnv("", nil, nil, func() int {
			events = append(events, "runTests")
			return 3
		})
		if code != 3 {
			t.Fatalf("exit code=%d; want the runTests result 3", code)
		}
		if got, want := strings.Join(events, ","), "runTests"; got != want {
			t.Fatalf("events=%s; want %s (no DSN means no isolated database at all)", got, want)
		}
	})
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
	defer func() {
		if closeErr := admin.Close(ctx); closeErr != nil {
			fmt.Fprintf(os.Stderr, "close isolated PostgreSQL admin connection: %v\n", closeErr)
		}
	}()
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
		defer func() {
			if closeErr := connection.Close(cleanupCtx); closeErr != nil {
				fmt.Fprintf(os.Stderr, "close isolated PostgreSQL cleanup connection: %v\n", closeErr)
			}
		}()
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
	// The reapply crossed into 105: the pre-105 observation must now carry
	// its session's workspace through the fail-closed backfill.
	var workspaceMatched, workspaceBound bool
	if err := db.QueryRowContext(ctx, `
		SELECT o.workspace_id = s.workspace_id, o.workspace_id IS NOT NULL
		  FROM observations o JOIN sessions s ON s.tenant_id=o.tenant_id AND s.id=o.session_id
		 WHERE o.tenant_id=$1 AND o.id=$2`, tenant, observationID).Scan(&workspaceMatched, &workspaceBound); err != nil {
		t.Fatalf("verify 105 backfill: %v", err)
	}
	if !workspaceMatched || !workspaceBound {
		t.Fatalf("105 backfill matched=%v bound=%v; want true/true", workspaceMatched, workspaceBound)
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

	// 199 is a synthetic beyond-head version (the real line now ends at
	// 107), so the failing DDL proves transactional rollback of a fresh
	// version's partial work.
	failing := &PostgresServerMigration{version: 199, name: "injected_failure", sql: `CREATE TABLE migration_199_rolled_back(id integer); SELECT 1/0`, checksum: "injected"}
	if err := failing.Apply(ctx, db); err == nil {
		t.Fatal("failed DDL unexpectedly succeeded")
	}
	var tableExists, ledgerExists bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('public.migration_199_rolled_back') IS NOT NULL`).Scan(&tableExists); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cortex_server_migrations WHERE version=199)`).Scan(&ledgerExists); err != nil {
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
		t.Fatalf("104 receipt columns=%s", receiptColumns)
	}

	t.Run("down is forward-only for every ledgered migration 100-105 with zero mutation", func(t *testing.T) {
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
		if _, err := db.ExecContext(ctx, `INSERT INTO cortex_server_migrations(version,name,checksum) VALUES(199,'future','future')`); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = db.ExecContext(ctx, `DELETE FROM cortex_server_migrations WHERE version=199`) })
		if err := migrations[0].Apply(ctx, db); err == nil || !errors.Is(err, ErrFutureMigration) {
			t.Fatalf("Apply(100) with future ledger version 199 err=%v; want errors.Is ErrFutureMigration", err)
		}
		if err := ApplyPostgresServerMigrations(ctx, db); err == nil || !errors.Is(err, ErrFutureMigration) {
			t.Fatalf("ApplyPostgresServerMigrations with future ledger version 199 err=%v; want errors.Is ErrFutureMigration", err)
		}
	})

	t.Run("head 104 runtime refuses a 105 ledger without mutation", func(t *testing.T) {
		before := postgresMigrationSnapshot(t, ctx, db)
		head104 := &PostgresServerMigration{
			version:         104,
			name:            migrations[4].Name(),
			sql:             migrations[4].SQL(),
			checksum:        migrations[4].Checksum(),
			maxKnownVersion: 104,
		}
		if err := head104.Apply(ctx, db); err == nil || !errors.Is(err, ErrFutureMigration) {
			t.Fatalf("head 104 binary over ledger 105 err=%v; want errors.Is ErrFutureMigration", err)
		}
		if after := postgresMigrationSnapshot(t, ctx, db); after != before {
			t.Fatal("refused startup mutated the database")
		}
	})
}

// TestPostgresMigration105WorkspaceBinding exercises migration 105 against
// real PostgreSQL 16: fresh apply with schema hardening, 104-era observation
// DML compatibility through the binding trigger, workspace-scoped topic and
// receipt uniqueness across two workspaces of one tenant, the fail-closed
// abort paths for durable pending receipts, duplicate active topic keys, and
// orphaned observations, and the composite tenant/workspace foreign keys
// (AMD-MIG-105 oracle).
func TestPostgresMigration105WorkspaceBinding(t *testing.T) {
	dsn := os.Getenv("CORTEX_TEST_POSTGRES_MIGRATION_DSN")
	if dsn == "" {
		t.Fatal("CORTEX_TEST_POSTGRES_MIGRATION_DSN is required for postgres_integration tests")
	}
	ctx := context.Background()

	freshDSN, cleanupFresh, err := isolatedPostgresDatabase(dsn, "migration105")
	if err != nil {
		t.Fatalf("create isolated 105 database: %v", err)
	}
	defer cleanupFresh()
	db, err := sql.Open("pgx", freshDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	migrations := mustPostgresMigrations(t)
	if err := ApplyPostgresServerMigrations(ctx, db); err != nil {
		t.Fatalf("fresh apply 100-106: %v", err)
	}
	var ledgerCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM cortex_server_migrations WHERE version BETWEEN 100 AND 106`).Scan(&ledgerCount); err != nil || ledgerCount != 7 {
		t.Fatalf("ledger count=%d err=%v; want 7", ledgerCount, err)
	}

	t.Run("schema is workspace hardened", func(t *testing.T) {
		for _, table := range []string{"observations", "handoff_receipts"} {
			var nullable string
			if err := db.QueryRowContext(ctx, `SELECT is_nullable FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 AND column_name='workspace_id'`, table).Scan(&nullable); err != nil {
				t.Fatalf("%s.workspace_id missing: %v", table, err)
			}
			if nullable != "NO" {
				t.Errorf("%s.workspace_id is_nullable=%s; want NO", table, nullable)
			}
		}
		var receiptPK string
		if err := db.QueryRowContext(ctx, `
			SELECT string_agg(a.attname, ',' ORDER BY k.ord)
			  FROM pg_index i
			  JOIN unnest(i.indkey) WITH ORDINALITY AS k(attnum, ord) ON true
			  JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
			 WHERE i.indrelid = 'public.handoff_receipts'::regclass AND i.indisprimary`).Scan(&receiptPK); err != nil {
			t.Fatal(err)
		}
		if receiptPK != "tenant_id,workspace_id,scope,key" {
			t.Fatalf("handoff_receipts PK=%q; want tenant_id,workspace_id,scope,key", receiptPK)
		}
		// The topic index oracle is catalog-based and formatting
		// independent: PostgreSQL renders the very same partial predicate
		// with or without per-conjunct parentheses ("((a IS NOT NULL) AND
		// (b IS NULL))" vs "a IS NOT NULL AND b IS NULL"), so the key
		// columns come from pg_index.indkey and the predicate's semantic
		// conjuncts from pg_get_expr over the stored parse tree — never
		// from a raw indexdef text comparison.
		var topicKeyColumns string
		if err := db.QueryRowContext(ctx, `
			SELECT COALESCE(string_agg(a.attname, ',' ORDER BY k.ord), '')
			  FROM pg_index i
			  JOIN pg_class ci ON ci.oid = i.indexrelid
			  JOIN unnest(i.indkey) WITH ORDINALITY AS k(attnum, ord) ON true
			  JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
			 WHERE i.indrelid = 'public.observations'::regclass
			   AND ci.relname = 'observations_topic_key_active_uq'`).Scan(&topicKeyColumns); err != nil {
			t.Fatalf("topic index missing from the catalog: %v", err)
		}
		var topicPredicate sql.NullString
		if err := db.QueryRowContext(ctx, `
			SELECT pg_get_expr(i.indpred, i.indrelid)
			  FROM pg_index i
			  JOIN pg_class ci ON ci.oid = i.indexrelid
			 WHERE i.indrelid = 'public.observations'::regclass
			   AND ci.relname = 'observations_topic_key_active_uq'`).Scan(&topicPredicate); err != nil {
			t.Fatalf("topic index predicate missing from the catalog: %v", err)
		}
		if topicKeyColumns != "tenant_id,workspace_id,project_key,topic_key" {
			t.Fatalf("topic index key columns=%q; want tenant_id,workspace_id,project_key,topic_key", topicKeyColumns)
		}
		if !topicPredicate.Valid {
			t.Fatal("topic index is not a partial index; the active-topic predicate is missing")
		}
		if got := indexPredicateConjuncts(topicPredicate.String); !equalStringSets(got, []string{"deleted_at IS NULL", "topic_key IS NOT NULL"}) {
			t.Fatalf("topic index predicate=%q; conjuncts=%v; want the semantic conjunction of deleted_at IS NULL and topic_key IS NOT NULL under any equivalent parenthesization", topicPredicate.String, got)
		}
		var staleIndex int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_indexes WHERE indexname='observations_topic_key_active_ws_uq'`).Scan(&staleIndex); err != nil || staleIndex != 0 {
			t.Fatalf("temporary topic index still present count=%d err=%v", staleIndex, err)
		}
		var triggerCount int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.triggers WHERE event_object_table='observations' AND trigger_name='observations_bind_workspace'`).Scan(&triggerCount); err != nil || triggerCount == 0 {
			t.Fatalf("binding trigger missing count=%d err=%v", triggerCount, err)
		}
	})

	tenant := "00000000-0000-0000-0000-000000000105"
	var ws1, ws2, session1, session2 int64
	if err := db.QueryRowContext(ctx, `
		WITH organization AS (
			INSERT INTO organizations(tenant_id,name) VALUES($1,'105 fixture') RETURNING id
		), w1 AS (
			INSERT INTO workspaces(tenant_id,organization_id,name)
			SELECT $1,id,'workspace one' FROM organization RETURNING id
		), w2 AS (
			INSERT INTO workspaces(tenant_id,organization_id,name)
			SELECT $1,id,'workspace two' FROM organization RETURNING id
		), s1 AS (
			INSERT INTO sessions(tenant_id,workspace_id) SELECT $1,id FROM w1 RETURNING id
		)
		INSERT INTO sessions(tenant_id,workspace_id) SELECT $1,id FROM w2 RETURNING id`, tenant).Scan(&session2); err != nil {
		t.Fatalf("seed second session: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM workspaces WHERE tenant_id=$1 AND name='workspace one'`, tenant).Scan(&ws1); err != nil {
		t.Fatalf("resolve workspace one: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM sessions WHERE tenant_id=$1 AND workspace_id=$2 ORDER BY id LIMIT 1`, tenant, ws1).Scan(&session1); err != nil {
		t.Fatalf("resolve session one: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT workspace_id FROM sessions WHERE tenant_id=$1 AND id=$2`, tenant, session2).Scan(&ws2); err != nil || ws2 == ws1 {
		t.Fatalf("workspace two resolve ws=%d err=%v", ws2, err)
	}

	t.Run("104-era observation DML still works", func(t *testing.T) {
		// 104-style INSERT omits workspace_id entirely.
		var derived int64
		if err := db.QueryRowContext(ctx, `
			INSERT INTO observations(tenant_id,session_id,type,title,content)
			VALUES($1,$2,'manual','old style','inserted without workspace') RETURNING workspace_id`, tenant, session1).Scan(&derived); err != nil {
			t.Fatalf("old-style insert: %v", err)
		}
		if derived != ws1 {
			t.Fatalf("old-style insert derived workspace=%d; want %d", derived, ws1)
		}
		// Explicit workspace matching the session is accepted.
		var explicit int64
		if err := db.QueryRowContext(ctx, `
			INSERT INTO observations(tenant_id,session_id,workspace_id,type,title,content)
			VALUES($1,$2,$3,'manual','new style','explicit workspace') RETURNING workspace_id`, tenant, session2, ws2).Scan(&explicit); err != nil || explicit != ws2 {
			t.Fatalf("explicit insert ws=%d err=%v; want %d", explicit, err, ws2)
		}
		// An explicit workspace that disagrees with the session is rejected.
		if _, err := db.ExecContext(ctx, `
			INSERT INTO observations(tenant_id,session_id,workspace_id,type,title,content)
			VALUES($1,$2,$3,'manual','mismatch','rejected')`, tenant, session1, ws2); err == nil || !strings.Contains(err.Error(), "conflicts with session workspace") {
			t.Fatalf("mismatched workspace err=%v; want session conflict", err)
		}
		// 104-style UPDATE that touches neither session nor workspace is untouched by the trigger.
		if _, err := db.ExecContext(ctx, `UPDATE observations SET title='old style updated' WHERE tenant_id=$1 AND title='old style'`, tenant); err != nil {
			t.Fatalf("old-style update: %v", err)
		}
	})

	t.Run("topic keys are unique per workspace only", func(t *testing.T) {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO observations(tenant_id,session_id,project_key,topic_key,type,title,content)
			VALUES($1,$2,'p','alpha','manual','topic one','first')`, tenant, session1); err != nil {
			t.Fatalf("first topic insert: %v", err)
		}
		// The same active topic in a sibling workspace is allowed since 105.
		if _, err := db.ExecContext(ctx, `
			INSERT INTO observations(tenant_id,session_id,project_key,topic_key,type,title,content)
			VALUES($1,$2,'p','alpha','manual','topic sibling','second')`, tenant, session2); err != nil {
			t.Fatalf("sibling-workspace topic insert: %v", err)
		}
		// Duplicates inside one workspace still fail closed.
		if _, err := db.ExecContext(ctx, `
			INSERT INTO observations(tenant_id,session_id,project_key,topic_key,type,title,content)
			VALUES($1,$2,'p','alpha','manual','topic duplicate','third')`, tenant, session1); err == nil || !strings.Contains(err.Error(), "observations_topic_key_active_uq") {
			t.Fatalf("same-workspace duplicate err=%v; want unique violation on the topic index", err)
		}
	})

	t.Run("receipt namespaces are isolated per workspace", func(t *testing.T) {
		var obs1, obs2 int64
		if err := db.QueryRowContext(ctx, `INSERT INTO observations(tenant_id,session_id,type,title,content) VALUES($1,$2,'manual','receipt base','one') RETURNING id`, tenant, session1).Scan(&obs1); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(ctx, `INSERT INTO observations(tenant_id,session_id,type,title,content) VALUES($1,$2,'manual','receipt base','two') RETURNING id`, tenant, session2).Scan(&obs2); err != nil {
			t.Fatal(err)
		}
		const committedReceipt = `INSERT INTO handoff_receipts(tenant_id,workspace_id,scope,key,payload_hash,canonical_payload,state,observation_id,initial_status,committed_at)
			VALUES($1,$2,'save','shared-key',gen_random_bytes(32),gen_random_bytes(16),'committed',$3,'created',now())`
		if _, err := db.ExecContext(ctx, committedReceipt, tenant, ws1, obs1); err != nil {
			t.Fatalf("first workspace receipt: %v", err)
		}
		// The same (scope,key) in a sibling workspace owns an independent namespace.
		if _, err := db.ExecContext(ctx, committedReceipt, tenant, ws2, obs2); err != nil {
			t.Fatalf("sibling-workspace receipt: %v", err)
		}
		// Reusing the namespace inside one workspace still fails closed.
		if _, err := db.ExecContext(ctx, committedReceipt, tenant, ws1, obs1); err == nil || !strings.Contains(err.Error(), "handoff_receipts_pkey") {
			t.Fatalf("same-workspace receipt duplicate err=%v; want PK violation", err)
		}
		// 104-era receipt DML omits workspace and must fail closed: a
		// tenant-wide default guess would be insecure (AD3).
		if _, err := db.ExecContext(ctx, `
			INSERT INTO handoff_receipts(tenant_id,scope,key,payload_hash,canonical_payload,state)
			VALUES($1,'save','legacy-key',gen_random_bytes(32),gen_random_bytes(16),'pending')`, tenant); err == nil || !strings.Contains(err.Error(), "workspace_id") {
			t.Fatalf("104-era pending receipt err=%v; want not-null failure on workspace_id", err)
		}
	})

	t.Run("durable pending receipt aborts 105 without partial state", func(t *testing.T) {
		abortDSN, cleanupAbort, err := isolatedPostgresDatabase(dsn, "migration105pending")
		if err != nil {
			t.Fatalf("create pending-abort database: %v", err)
		}
		defer cleanupAbort()
		abortDB, err := sql.Open("pgx", abortDSN)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if closeErr := abortDB.Close(); closeErr != nil {
				t.Errorf("close abort database: %v", closeErr)
			}
		}()
		for _, migration := range migrations[:5] {
			if err := migration.Apply(ctx, abortDB); err != nil {
				t.Fatalf("apply historical migration %d: %v", migration.Version(), err)
			}
		}
		if _, err := abortDB.ExecContext(ctx, `
			INSERT INTO handoff_receipts(tenant_id,scope,key,payload_hash,canonical_payload,state)
			VALUES($1,'save','stuck',gen_random_bytes(32),gen_random_bytes(16),'pending')`, tenant); err != nil {
			t.Fatalf("seed durable pending receipt: %v", err)
		}
		if err := migrations[5].Apply(ctx, abortDB); err == nil || !strings.Contains(err.Error(), "pending handoff receipt") {
			t.Fatalf("apply 105 over pending receipt err=%v; want fail-closed abort", err)
		}
		assertNoPartial105(t, ctx, abortDB)
		// Reviewed fixture recovery only: drain the synthetic pending
		// receipt, then the same migration applies cleanly.
		if _, err := abortDB.ExecContext(ctx, `DELETE FROM handoff_receipts WHERE tenant_id=$1 AND key='stuck'`, tenant); err != nil {
			t.Fatal(err)
		}
		if err := migrations[5].Apply(ctx, abortDB); err != nil {
			t.Fatalf("re-apply 105 after draining: %v", err)
		}
	})

	t.Run("duplicate active topic keys abort 105 without partial state", func(t *testing.T) {
		abortDSN, cleanupAbort, err := isolatedPostgresDatabase(dsn, "migration105dup")
		if err != nil {
			t.Fatalf("create duplicate-abort database: %v", err)
		}
		defer cleanupAbort()
		abortDB, err := sql.Open("pgx", abortDSN)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if closeErr := abortDB.Close(); closeErr != nil {
				t.Errorf("close abort database: %v", closeErr)
			}
		}()
		for _, migration := range migrations[:5] {
			if err := migration.Apply(ctx, abortDB); err != nil {
				t.Fatalf("apply historical migration %d: %v", migration.Version(), err)
			}
		}
		var dupSession int64
		if err := abortDB.QueryRowContext(ctx, `
			WITH organization AS (
				INSERT INTO organizations(tenant_id,name) VALUES($1,'dup fixture') RETURNING id
			), w AS (
				INSERT INTO workspaces(tenant_id,organization_id,name)
				SELECT $1,id,'dup workspace' FROM organization RETURNING id
			)
			INSERT INTO sessions(tenant_id,workspace_id) SELECT $1,id FROM w RETURNING id`, tenant).Scan(&dupSession); err != nil {
			t.Fatalf("seed duplicate fixture session: %v", err)
		}
		duplicate := `INSERT INTO observations(tenant_id,session_id,project_key,topic_key,type,title,content)
			VALUES($1,$2,'p','dup','manual',$3,$4)`
		if _, err := abortDB.ExecContext(ctx, duplicate, tenant, dupSession, "dup one", "first"); err != nil {
			t.Fatalf("seed first duplicate: %v", err)
		}
		// Simulate a drifted database: the tenant-wide guard is gone so the
		// second row lands in the SAME workspace, and 105 must abort instead
		// of silently keeping an ambiguous duplicate.
		if _, err := abortDB.ExecContext(ctx, `DROP INDEX observations_topic_key_active_uq`); err != nil {
			t.Fatal(err)
		}
		if _, err := abortDB.ExecContext(ctx, duplicate, tenant, dupSession, "dup two", "second"); err != nil {
			t.Fatalf("seed second duplicate: %v", err)
		}
		if err := migrations[5].Apply(ctx, abortDB); err == nil || !strings.Contains(err.Error(), "collide") {
			t.Fatalf("apply 105 over duplicate topics err=%v; want fail-closed abort", err)
		}
		assertNoPartial105(t, ctx, abortDB)
		// Reviewed fixture recovery only: remove one synthetic duplicate and
		// restore the retired guard so the line can continue forward.
		if _, err := abortDB.ExecContext(ctx, `DELETE FROM observations WHERE tenant_id=$1 AND title='dup two'`, tenant); err != nil {
			t.Fatal(err)
		}
		if _, err := abortDB.ExecContext(ctx, `CREATE UNIQUE INDEX observations_topic_key_active_uq ON observations(tenant_id, project_key, topic_key) WHERE topic_key IS NOT NULL AND deleted_at IS NULL`); err != nil {
			t.Fatal(err)
		}
		if err := migrations[5].Apply(ctx, abortDB); err != nil {
			t.Fatalf("re-apply 105 after dedup: %v", err)
		}
	})

	t.Run("orphan observation aborts 105 without partial state", func(t *testing.T) {
		abortDSN, cleanupAbort, err := isolatedPostgresDatabase(dsn, "migration105orphan")
		if err != nil {
			t.Fatalf("create orphan-abort database: %v", err)
		}
		defer cleanupAbort()
		abortDB, err := sql.Open("pgx", abortDSN)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if closeErr := abortDB.Close(); closeErr != nil {
				t.Errorf("close abort database: %v", closeErr)
			}
		}()
		for _, migration := range migrations[:5] {
			if err := migration.Apply(ctx, abortDB); err != nil {
				t.Fatalf("apply historical migration %d: %v", migration.Version(), err)
			}
		}
		// Simulate a drifted head-104 database whose observation references a
		// session that does not exist in its tenant. The composite session
		// foreign key must be dropped first, and the 102 sync trigger must be
		// disabled because it resolves sync_changes.workspace_id (NOT NULL)
		// from the session and would fail on the unresolved reference.
		if _, err := abortDB.ExecContext(ctx, `DO $$
DECLARE r record;
BEGIN
	FOR r IN
		SELECT c.conname
		  FROM pg_constraint c
		  JOIN pg_class t ON t.oid = c.conrelid
		  JOIN pg_class rt ON rt.oid = c.confrelid
		  JOIN pg_namespace n ON n.oid = t.relnamespace
		 WHERE c.contype = 'f' AND n.nspname = 'public'
		   AND t.relname = 'observations' AND rt.relname = 'sessions'
	LOOP
		EXECUTE format('ALTER TABLE observations DROP CONSTRAINT %I', r.conname);
	END LOOP;
END $$`); err != nil {
			t.Fatalf("drop observations to sessions foreign key: %v", err)
		}
		if _, err := abortDB.ExecContext(ctx, `ALTER TABLE observations DISABLE TRIGGER observations_sync_change`); err != nil {
			t.Fatalf("disable observations sync trigger: %v", err)
		}
		const orphanSession int64 = 999999999
		if _, err := abortDB.ExecContext(ctx, `
			INSERT INTO observations(tenant_id,session_id,type,title,content)
			VALUES($1,$2,'manual','orphan','no session')`, tenant, orphanSession); err != nil {
			t.Fatalf("seed orphan observation: %v", err)
		}
		if err := migrations[5].Apply(ctx, abortDB); err == nil || !strings.Contains(err.Error(), "reference no session") {
			t.Fatalf("apply 105 over orphan observation err=%v; want fail-closed abort", err)
		}
		assertNoPartial105(t, ctx, abortDB)
		// The abort rolled back cleanly: the orphan row survived untouched.
		var orphans int
		if err := abortDB.QueryRowContext(ctx, `SELECT count(*) FROM observations WHERE tenant_id=$1 AND session_id=$2`, tenant, orphanSession).Scan(&orphans); err != nil || orphans != 1 {
			t.Fatalf("orphan preserved after abort count=%d err=%v; want 1", orphans, err)
		}
		// Reviewed fixture recovery only: remove the synthetic orphan, put
		// the tampered catalog back exactly as migration 100 built it, and
		// the line continues forward (never a destructive Down).
		if _, err := abortDB.ExecContext(ctx, `DELETE FROM observations WHERE tenant_id=$1 AND session_id=$2`, tenant, orphanSession); err != nil {
			t.Fatal(err)
		}
		if _, err := abortDB.ExecContext(ctx, `ALTER TABLE observations ENABLE TRIGGER observations_sync_change`); err != nil {
			t.Fatal(err)
		}
		if _, err := abortDB.ExecContext(ctx, `
			ALTER TABLE observations ADD CONSTRAINT observations_tenant_id_session_id_fkey
			FOREIGN KEY (tenant_id, session_id) REFERENCES sessions(tenant_id, id)`); err != nil {
			t.Fatalf("restore session foreign key: %v", err)
		}
		if err := migrations[5].Apply(ctx, abortDB); err != nil {
			t.Fatalf("re-apply 105 after orphan repair: %v", err)
		}
	})

	t.Run("composite tenant/workspace foreign keys are catalogued and enforced", func(t *testing.T) {
		// Catalog: both follow-up foreign keys are composite (tenant_id,
		// workspace_id) referencing workspaces(tenant_id, id).
		for _, table := range []string{"observations", "handoff_receipts"} {
			var def string
			if err := db.QueryRowContext(ctx, `
				SELECT pg_get_constraintdef(c.oid)
				  FROM pg_constraint c
				  JOIN pg_class t ON t.oid = c.conrelid
				  JOIN pg_namespace n ON n.oid = t.relnamespace
				 WHERE c.contype = 'f' AND n.nspname = 'public'
				   AND t.relname = $1 AND c.conname = $2`,
				table, table+"_tenant_workspace_fkey").Scan(&def); err != nil {
				t.Fatalf("%s composite workspace foreign key missing from the catalog: %v", table, err)
			}
			if want := "FOREIGN KEY (tenant_id, workspace_id) REFERENCES workspaces(tenant_id, id)"; def != want {
				t.Fatalf("%s tenant/workspace foreign key def=%q; want %q", table, def, want)
			}
		}
		// Enforcement: a workspace of ANOTHER tenant must be rejected even
		// though the workspace exists, proving the key is composite and not a
		// bare workspace reference.
		foreignTenant := "00000000-0000-0000-0000-000000000205"
		var foreignWorkspace int64
		if err := db.QueryRowContext(ctx, `
			WITH organization AS (
				INSERT INTO organizations(tenant_id,name) VALUES($1,'foreign tenant') RETURNING id
			)
			INSERT INTO workspaces(tenant_id,organization_id,name)
			SELECT $1,id,'foreign workspace' FROM organization RETURNING id`, foreignTenant).Scan(&foreignWorkspace); err != nil {
			t.Fatalf("seed foreign tenant workspace: %v", err)
		}
		var fkObservation int64
		if err := db.QueryRowContext(ctx, `INSERT INTO observations(tenant_id,session_id,type,title,content) VALUES($1,$2,'manual','fk fixture','fk') RETURNING id`, tenant, session1).Scan(&fkObservation); err != nil {
			t.Fatal(err)
		}
		// handoff_receipts derives nothing: the cross-tenant pair fails
		// directly on the composite foreign key.
		_, err := db.ExecContext(ctx, `
			INSERT INTO handoff_receipts(tenant_id,workspace_id,scope,key,payload_hash,canonical_payload,state,observation_id,initial_status,committed_at)
			VALUES($1,$2,'save','cross-tenant',gen_random_bytes(32),gen_random_bytes(16),'committed',$3,'created',now())`, tenant, foreignWorkspace, fkObservation)
		assertForeignKeyViolation(t, err, "handoff_receipts_tenant_workspace_fkey")
		// observations derives its workspace through the binding trigger, so
		// the bogus pair is injected with that trigger bypassed; the sync
		// trigger is disabled too so only the composite foreign key can
		// reject the row.
		for _, trigger := range []string{"observations_bind_workspace", "observations_sync_change"} {
			if _, err := db.ExecContext(ctx, `ALTER TABLE observations DISABLE TRIGGER `+trigger); err != nil {
				t.Fatalf("disable %s: %v", trigger, err)
			}
		}
		_, err = db.ExecContext(ctx, `
			INSERT INTO observations(tenant_id,session_id,workspace_id,type,title,content)
			VALUES($1,$2,$3,'manual','cross tenant workspace','rejected')`, tenant, session1, foreignWorkspace)
		assertForeignKeyViolation(t, err, "observations_tenant_workspace_fkey")
		for _, trigger := range []string{"observations_bind_workspace", "observations_sync_change"} {
			if _, err := db.ExecContext(ctx, `ALTER TABLE observations ENABLE TRIGGER `+trigger); err != nil {
				t.Fatalf("re-enable %s: %v", trigger, err)
			}
		}
		var leaked int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM observations WHERE tenant_id=$1 AND workspace_id=$2`, tenant, foreignWorkspace).Scan(&leaked); err != nil || leaked != 0 {
			t.Fatalf("cross-tenant observation leaked count=%d err=%v", leaked, err)
		}
	})
}

// assertNoPartial105 proves a failed 105 application left the database at
// head 104: no ledger row, no added column, no swapped constraint.
func assertNoPartial105(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var ledgered bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cortex_server_migrations WHERE version=105)`).Scan(&ledgered); err != nil {
		t.Fatal(err)
	}
	if ledgered {
		t.Fatal("aborted 105 leaked a ledger row")
	}
	for _, table := range []string{"observations", "handoff_receipts"} {
		var columns int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 AND column_name='workspace_id'`, table).Scan(&columns); err != nil {
			t.Fatal(err)
		}
		if columns != 0 {
			t.Fatalf("aborted 105 leaked %s.workspace_id", table)
		}
	}
}

// indexPredicateConjuncts normalizes a catalog-rendered partial-index
// predicate (pg_get_expr over pg_index.indpred) into its semantic
// conjunction set. Parenthesization and whitespace are formatting only:
// "a IS NOT NULL AND b IS NULL" and "((a IS NOT NULL) AND (b IS NULL))"
// normalize to exactly the same conjuncts.
func indexPredicateConjuncts(predicate string) []string {
	var b strings.Builder
	for _, r := range strings.Join(strings.Fields(predicate), " ") {
		if r == '(' || r == ')' {
			continue
		}
		b.WriteRune(r)
	}
	collapsed := strings.Join(strings.Fields(b.String()), " ")
	if collapsed == "" {
		return nil
	}
	var conjuncts []string
	for _, part := range strings.Split(collapsed, " AND ") {
		if part = strings.TrimSpace(part); part != "" {
			conjuncts = append(conjuncts, part)
		}
	}
	return conjuncts
}

// equalStringSets reports whether a and b hold exactly the same unordered
// multiset of strings.
func equalStringSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	remaining := make(map[string]int, len(b))
	for _, s := range b {
		remaining[s]++
	}
	for _, s := range a {
		if remaining[s] == 0 {
			return false
		}
		remaining[s]--
	}
	return true
}

// assertForeignKeyViolation asserts err is a PostgreSQL 23503 foreign key
// violation raised by the named composite constraint.
func assertForeignKeyViolation(t *testing.T, err error, constraint string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s violation, got success", constraint)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Fatalf("err=%v; want SQLSTATE 23503 foreign key violation", err)
	}
	if pgErr.ConstraintName != constraint {
		t.Fatalf("violated constraint=%q; want %q", pgErr.ConstraintName, constraint)
	}
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
	if err := columns.Close(); err != nil {
		t.Fatalf("close columns snapshot rows: %v", err)
	}
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
	if err := ledger.Close(); err != nil {
		t.Fatalf("close ledger snapshot rows: %v", err)
	}
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

// assertPgErrorCode asserts err is a PostgreSQL error with the given SQLSTATE.
func assertPgErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected SQLSTATE %s violation, got success", code)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != code {
		t.Fatalf("err=%v; want SQLSTATE %s", err, code)
	}
}

// scopePrincipalQueryer is the exec/query surface *sql.DB and *sql.Tx
// share, so provenance-minted binding works inside and outside
// transactions.
type scopePrincipalQueryer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// bindVerifiedPrincipal implements provenance-only binding for the scope
// fixtures (REQ-IDP binding contract): it presents the actor's live bearer
// to cortex_verify_token_principal, which mints the one-time token-bound
// v1 proof, then binds the principal with that proof and the
// verification-observed grant version. The binder accepts no literal
// digest string, so fixtures must never authenticate with one.
func bindVerifiedPrincipal(t *testing.T, ctx context.Context, q scopePrincipalQueryer, tenant, actorPub, secret string) {
	t.Helper()
	var version int64
	var provenance string
	if err := q.QueryRowContext(ctx, `
		SELECT grant_version, binding_provenance
		  FROM cortex_verify_token_principal(left($1, 12), hmac(convert_to($1, 'UTF8'), convert_to($2::text, 'UTF8'), 'sha256'), '')`,
		secret, tenant).Scan(&version, &provenance); err != nil {
		t.Fatalf("verify bearer for principal %s: %v", actorPub, err)
	}
	if !strings.HasPrefix(provenance, "v1:") {
		t.Fatalf("verification minted no token-bound provenance for %s: %q", actorPub, provenance)
	}
	if _, err := q.ExecContext(ctx, `SELECT cortex_bind_principal($1, $2, $3)`, actorPub, provenance, version); err != nil {
		t.Fatalf("bind principal %s: %v", actorPub, err)
	}
}

// TestPostgresMigration106ProjectArtifacts exercises migration 106 against
// real PostgreSQL 16: fresh apply with the six artifact tables, forced RLS
// with trusted principal-derived tenant/workspace/project policies and
// least-privilege grants, the fail-closed scope-validation trigger
// (including absent-project workspace defaults), the activation CAS token,
// exact idempotency replay, immutable history and monotonic usage guards,
// RESTRICT hard-delete behavior, soft-delete provenance retention,
// behavioral same-tenant workspace/project denial under RLS as cortex_app,
// and idempotent reapply. The migration DSN suite is the authoritative
// executor; without the DSNs it fails rather than skips.
func TestPostgresMigration106ProjectArtifacts(t *testing.T) {
	dsn := os.Getenv("CORTEX_TEST_POSTGRES_MIGRATION_DSN")
	if dsn == "" {
		t.Fatal("CORTEX_TEST_POSTGRES_MIGRATION_DSN is required for postgres_integration tests")
	}
	ctx := context.Background()

	freshDSN, cleanupFresh, err := isolatedPostgresDatabase(dsn, "migration106")
	if err != nil {
		t.Fatalf("create isolated 106 database: %v", err)
	}
	defer cleanupFresh()
	db, err := sql.Open("pgx", freshDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	migrations := mustPostgresMigrations(t)
	if err := ApplyPostgresServerMigrations(ctx, db); err != nil {
		t.Fatalf("fresh apply 100-106: %v", err)
	}
	var subject *PostgresServerMigration
	for _, migration := range migrations {
		if migration.Version() == 106 {
			subject = migration
			break
		}
	}
	var ledgerChecksum string
	if err := db.QueryRowContext(ctx, `SELECT checksum FROM cortex_server_migrations WHERE version=106`).Scan(&ledgerChecksum); err != nil || ledgerChecksum != subject.Checksum() {
		t.Fatalf("ledger 106 checksum=%q err=%v; want %q", ledgerChecksum, err, subject.Checksum())
	}

	tables := []string{
		"project_artifacts", "project_artifact_revisions", "project_artifact_events",
		"project_artifact_activations", "project_artifact_idempotency", "project_storage_usage",
	}

	t.Run("tables exist with enabled and forced RLS policies", func(t *testing.T) {
		for _, table := range tables {
			var exists bool
			if err := db.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&exists); err != nil || !exists {
				t.Fatalf("table %s missing: err=%v", table, err)
			}
			var rls, forced bool
			if err := db.QueryRowContext(ctx, `
				SELECT c.relrowsecurity, c.relforcerowsecurity
				  FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
				 WHERE n.nspname='public' AND c.relname=$1`, table).Scan(&rls, &forced); err != nil {
				t.Fatalf("rls metadata for %s: %v", table, err)
			}
			if !rls || !forced {
				t.Errorf("%s rls=%v forced=%v; want true/true", table, rls, forced)
			}
			var policies int
			if err := db.QueryRowContext(ctx, `
				SELECT count(*) FROM pg_policies
				 WHERE schemaname='public' AND tablename=$1
				   AND policyname='cortex_project_isolation'
				   AND qual LIKE '%cortex_current_tenant()%'`, table).Scan(&policies); err != nil || policies != 1 {
				t.Errorf("%s cortex_project_isolation policy count=%d err=%v; want 1", table, policies, err)
			}
		}
	})

	t.Run("least privilege grants", func(t *testing.T) {
		// Least privilege (REQ-RET-001, review fix): cortex_app may read and
		// append evidence and mutate only live state. It holds NO DELETE on
		// any artifact table, NO UPDATE on immutable history, and no TRUNCATE;
		// quota cannot be reset because counter rows cannot be removed and
		// the monotonic guard aborts decreases.
		artifactTables := []string{
			"project_artifacts", "project_artifact_revisions", "project_artifact_events",
			"project_artifact_activations", "project_artifact_idempotency", "project_storage_usage",
		}
		wantUpdate := map[string]bool{
			"project_artifacts": true, "project_artifact_revisions": false, "project_artifact_events": false,
			"project_artifact_activations": true, "project_artifact_idempotency": true, "project_storage_usage": true,
		}
		for _, table := range artifactTables {
			var selectPriv, insertPriv, deletePriv, truncatePriv bool
			if err := db.QueryRowContext(ctx, `SELECT has_table_privilege('cortex_app', 'public.`+table+`', 'SELECT')`).Scan(&selectPriv); err != nil || !selectPriv {
				t.Errorf("cortex_app SELECT on %s=%v err=%v; want true", table, selectPriv, err)
			}
			if err := db.QueryRowContext(ctx, `SELECT has_table_privilege('cortex_app', 'public.`+table+`', 'INSERT')`).Scan(&insertPriv); err != nil || !insertPriv {
				t.Errorf("cortex_app INSERT on %s=%v err=%v; want true", table, insertPriv, err)
			}
			if err := db.QueryRowContext(ctx, `SELECT has_table_privilege('cortex_app', 'public.`+table+`', 'DELETE')`).Scan(&deletePriv); err != nil || deletePriv {
				t.Errorf("cortex_app DELETE on %s=%v; want false (indefinite retention)", table, deletePriv)
			}
			if err := db.QueryRowContext(ctx, `SELECT has_table_privilege('cortex_app', 'public.`+table+`', 'TRUNCATE')`).Scan(&truncatePriv); err != nil || truncatePriv {
				t.Errorf("cortex_app TRUNCATE on %s=%v; want false", table, truncatePriv)
			}
			var updatePriv bool
			if err := db.QueryRowContext(ctx, `SELECT has_table_privilege('cortex_app', 'public.`+table+`', 'UPDATE')`).Scan(&updatePriv); err != nil || updatePriv != wantUpdate[table] {
				t.Errorf("cortex_app UPDATE on %s=%v; want %v", table, updatePriv, wantUpdate[table])
			}
			var adminPriv bool
			if err := db.QueryRowContext(ctx, `SELECT has_table_privilege('cortex_admin', 'public.`+table+`', 'SELECT')`).Scan(&adminPriv); err != nil || !adminPriv {
				t.Errorf("cortex_admin SELECT on %s=%v err=%v; want true", table, adminPriv, err)
			}
			if err := db.QueryRowContext(ctx, `SELECT has_table_privilege('cortex_admin', 'public.`+table+`', 'INSERT')`).Scan(&adminPriv); err == nil && adminPriv {
				t.Errorf("cortex_admin must not write %s", table)
			}
			var publicGrants int
			if err := db.QueryRowContext(ctx, `
				SELECT count(*) FROM information_schema.table_privileges
				 WHERE table_schema='public' AND table_name=$1 AND grantee='PUBLIC'`, table).Scan(&publicGrants); err != nil || publicGrants != 0 {
				t.Errorf("PUBLIC grants on %s=%d err=%v; want 0", table, publicGrants, err)
			}
		}
	})

	t.Run("scope validation, immutability, restrict, and soft delete", func(t *testing.T) {
		tenant := "00000000-0000-0000-0000-000000000106"
		actor := "00000000-0000-0000-0000-00000000010a"
		var workspaceID, projectID, otherWorkspaceID int64
		if err := db.QueryRowContext(ctx, `
			WITH organization AS (
				INSERT INTO organizations(tenant_id,name) VALUES($1,'artifacts fixture') RETURNING id
			), w AS (
				INSERT INTO workspaces(tenant_id,organization_id,name)
				SELECT $1,id,'artifacts workspace' FROM organization RETURNING id
			), w2 AS (
				INSERT INTO workspaces(tenant_id,organization_id,name)
				SELECT $1,id,'other workspace' FROM organization RETURNING id
			), p AS (
				INSERT INTO projects(tenant_id,workspace_id,name)
				SELECT $1,id,'artifacts project' FROM w RETURNING id
			)
			SELECT (SELECT id FROM w), (SELECT id FROM p), (SELECT id FROM w2)`, tenant).Scan(&workspaceID, &projectID, &otherWorkspaceID); err != nil {
			t.Fatalf("seed hierarchy: %v", err)
		}

		digest := "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
		var artifactID int64
		if err := db.QueryRowContext(ctx, `
			INSERT INTO project_artifacts
				(tenant_id, workspace_id, project_id, kind, key, source_scope, current_revision, content_bytes, metadata_bytes, digest)
			VALUES ($1, $2, $3, 'skill', 'build', 'project', 1, 3, 2, $4) RETURNING id`,
			tenant, workspaceID, projectID, digest).Scan(&artifactID); err != nil {
			t.Fatalf("seed artifact: %v", err)
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO project_artifact_revisions
				(tenant_id, artifact_id, revision, content, content_bytes, metadata, metadata_bytes, digest, created_by)
			VALUES ($1, $2, 1, 'abc', 3, '{}', 2, $3, $4)`, tenant, artifactID, digest, actor); err != nil {
			t.Fatalf("seed revision: %v", err)
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO project_artifact_activations (tenant_id, artifact_id, revision, activation_revision, activated_by)
			VALUES ($1, $2, 1, 1, $3)`, tenant, artifactID, actor); err != nil {
			t.Fatalf("seed activation: %v", err)
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO project_artifact_events (tenant_id, artifact_id, event_type, revision, actor, payload)
			VALUES ($1, $2, 'created', 1, $3, '{}')`, tenant, artifactID, actor); err != nil {
			t.Fatalf("seed event: %v", err)
		}

		// Scope validation: a workspace that disagrees with the project's own
		// workspace fails closed (23514); an unknown project fails (23503).
		_, err := db.ExecContext(ctx, `
			INSERT INTO project_artifacts
				(tenant_id, workspace_id, project_id, kind, key, source_scope, current_revision, content_bytes, metadata_bytes, digest)
			VALUES ($1, $2, $3, 'rule', 'build', 'project', 1, 3, 2, $4)`, tenant, otherWorkspaceID, projectID, digest)
		assertPgErrorCode(t, err, "23514")
		_, err = db.ExecContext(ctx, `
			INSERT INTO project_artifacts
				(tenant_id, workspace_id, project_id, kind, key, source_scope, current_revision, content_bytes, metadata_bytes, digest)
			VALUES ($1, $2, 999999, 'rule', 'build2', 'project', 1, 3, 2, $3)`, tenant, workspaceID, digest)
		assertPgErrorCode(t, err, "23503")

		// Workspace defaults are first-class rows with an absent project:
		// representable, unique per workspace, and scope-checked both ways.
		if _, err := db.ExecContext(ctx, `
			INSERT INTO project_artifacts
				(tenant_id, workspace_id, project_id, kind, key, source_scope, current_revision, content_bytes, metadata_bytes, digest)
			VALUES ($1, $2, NULL, 'skill', 'build', 'workspace_default', 1, 3, 2, $3)`, tenant, workspaceID, digest); err != nil {
			t.Fatalf("seed workspace default rejected: %v", err)
		}
		_, err = db.ExecContext(ctx, `
			INSERT INTO project_artifacts
				(tenant_id, workspace_id, project_id, kind, key, source_scope, current_revision, content_bytes, metadata_bytes, digest)
			VALUES ($1, $2, NULL, 'skill', 'build', 'workspace_default', 1, 3, 2, $3)`, tenant, workspaceID, digest)
		assertPgErrorCode(t, err, "23505")
		_, err = db.ExecContext(ctx, `
			INSERT INTO project_artifacts
				(tenant_id, workspace_id, project_id, kind, key, source_scope, current_revision, content_bytes, metadata_bytes, digest)
			VALUES ($1, $2, $3, 'skill', 'mixed', 'workspace_default', 1, 3, 2, $4)`, tenant, workspaceID, projectID, digest)
		assertPgErrorCode(t, err, "23514")
		_, err = db.ExecContext(ctx, `
			INSERT INTO project_artifacts
				(tenant_id, workspace_id, project_id, kind, key, source_scope, current_revision, content_bytes, metadata_bytes, digest)
			VALUES ($1, $2, NULL, 'skill', 'mixed2', 'project', 1, 3, 2, $3)`, tenant, workspaceID, digest)
		assertPgErrorCode(t, err, "23514")
		_, err = db.ExecContext(ctx, `
			INSERT INTO project_artifacts
				(tenant_id, workspace_id, project_id, kind, key, source_scope, current_revision, content_bytes, metadata_bytes, digest)
			VALUES ($1, 999999, NULL, 'skill', 'mixed3', 'workspace_default', 1, 3, 2, $2)`, tenant, digest)
		assertPgErrorCode(t, err, "23503")

		// Activation CAS: the pointer is the authoritative token. Direct
		// mirror writes that do not match the pointer's current token abort,
		// pointer moves require a strictly greater token and auto-sync the
		// mirror, and the pointer is retained.
		_, err = db.ExecContext(ctx, `
			UPDATE project_artifacts SET activation_revision = activation_revision + 1
			 WHERE id=$1 AND tenant_id=$2`, artifactID, tenant)
		assertPgErrorCode(t, err, "P0001")
		_, err = db.ExecContext(ctx, `
			UPDATE project_artifacts SET activation_revision = activation_revision - 1
			 WHERE id=$1 AND tenant_id=$2`, artifactID, tenant)
		assertPgErrorCode(t, err, "P0001")
		if _, err := db.ExecContext(ctx, `
			INSERT INTO project_artifact_revisions
				(tenant_id, artifact_id, revision, content, content_bytes, metadata, metadata_bytes, digest, created_by)
			VALUES ($1, $2, 2, 'defg', 4, '{}', 2, $3, $4)`, tenant, artifactID, digest, actor); err != nil {
			t.Fatalf("seed revision 2 rejected: %v", err)
		}
		_, err = db.ExecContext(ctx, `
			UPDATE project_artifact_activations SET revision = 2
			 WHERE tenant_id=$1 AND artifact_id=$2`, tenant, artifactID)
		assertPgErrorCode(t, err, "P0001")
		if _, err := db.ExecContext(ctx, `
			UPDATE project_artifact_activations SET revision = 2, activation_revision = 2
			 WHERE tenant_id=$1 AND artifact_id=$2`, tenant, artifactID); err != nil {
			t.Fatalf("activation pointer move with token increment rejected: %v", err)
		}
		// The pointer move synced the artifact mirror to the new token, and
		// the mirror still cannot drift past the pointer.
		var mirror int
		if err := db.QueryRowContext(ctx, `SELECT activation_revision FROM project_artifacts WHERE id=$1 AND tenant_id=$2`, artifactID, tenant).Scan(&mirror); err != nil || mirror != 2 {
			t.Fatalf("artifact mirror after pointer move=%d err=%v; want synced token 2", mirror, err)
		}
		_, err = db.ExecContext(ctx, `
			UPDATE project_artifacts SET activation_revision = 3
			 WHERE id=$1 AND tenant_id=$2`, artifactID, tenant)
		assertPgErrorCode(t, err, "P0001")
		_, err = db.ExecContext(ctx, `
			UPDATE project_artifact_activations SET revision = 2, activation_revision = 1
			 WHERE tenant_id=$1 AND artifact_id=$2`, tenant, artifactID)
		assertPgErrorCode(t, err, "P0001")
		_, err = db.ExecContext(ctx, `
			DELETE FROM project_artifact_activations WHERE tenant_id=$1 AND artifact_id=$2`, tenant, artifactID)
		assertPgErrorCode(t, err, "P0001")

		// Exact idempotency replay: pending reserves the namespace, commit
		// requires the exact result revision, and the committed receipt is
		// immutable and retained.
		if _, err := db.ExecContext(ctx, `
			INSERT INTO project_artifact_idempotency
				(tenant_id, workspace_id, project_id, scope, idem_key, payload_hash, state)
			VALUES ($1, $2, $3, 'artifact:save', 'k1', decode('0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20','hex'), 'pending')`,
			tenant, workspaceID, projectID); err != nil {
			t.Fatalf("seed pending receipt rejected: %v", err)
		}
		_, err = db.ExecContext(ctx, `
			INSERT INTO project_artifact_idempotency
				(tenant_id, workspace_id, project_id, scope, idem_key, payload_hash, state)
			VALUES ($1, $2, $3, 'artifact:save', 'k1', decode('0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20','hex'), 'pending')`,
			tenant, workspaceID, projectID)
		assertPgErrorCode(t, err, "23505")
		// Committing without the exact result revision fails closed. On
		// PostgreSQL 16 the BEFORE UPDATE commit-guard trigger fires ahead
		// of the table CHECK constraint, so the abort surfaces as the
		// guard's P0001 (not 23514); either way no partial commit lands.
		_, err = db.ExecContext(ctx, `
			UPDATE project_artifact_idempotency
			   SET state='committed', artifact_id=$3, initial_status='created', committed_at=now()
			 WHERE tenant_id=$1 AND workspace_id=$2 AND project_id=$4 AND scope='artifact:save' AND idem_key='k1'`, tenant, workspaceID, artifactID, projectID)
		assertPgErrorCode(t, err, "P0001")
		if _, err := db.ExecContext(ctx, `
			UPDATE project_artifact_idempotency
			   SET state='committed', artifact_id=$3, initial_status='created', result_revision=1, committed_at=now()
			 WHERE tenant_id=$1 AND workspace_id=$2 AND project_id=$4 AND scope='artifact:save' AND idem_key='k1'`,
			tenant, workspaceID, artifactID, projectID); err != nil {
			t.Fatalf("receipt commit rejected: %v", err)
		}
		_, err = db.ExecContext(ctx, `
			UPDATE project_artifact_idempotency SET result_revision = 9
			 WHERE tenant_id=$1 AND scope='artifact:save' AND idem_key='k1'`, tenant)
		assertPgErrorCode(t, err, "P0001")
		_, err = db.ExecContext(ctx, `
			DELETE FROM project_artifact_idempotency WHERE tenant_id=$1 AND scope='artifact:save' AND idem_key='k1'`, tenant)
		assertPgErrorCode(t, err, "P0001")
		// Committed receipts cannot be fabricated by direct INSERT: the
		// result must reference an existing revision of an artifact of the
		// SAME coordinate. First a phantom revision, then a real revision of
		// another coordinate's artifact.
		_, err = db.ExecContext(ctx, `
			INSERT INTO project_artifact_idempotency
				(tenant_id, workspace_id, project_id, scope, idem_key, payload_hash, state, artifact_id, initial_status, result_revision, committed_at)
			VALUES ($1, $2, $3, 'artifact:save', 'fabricated', decode('0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20','hex'), 'committed', $4, 'created', 42, now())`,
			tenant, workspaceID, projectID, artifactID)
		assertPgErrorCode(t, err, "P0001")
		if _, err := db.ExecContext(ctx, `
			INSERT INTO project_artifact_revisions
				(tenant_id, artifact_id, revision, content, content_bytes, metadata, metadata_bytes, digest, created_by)
			SELECT $1, a.id, 1, 'default-rev', 11, '{}', 2, $2, $3
			  FROM project_artifacts a
			 WHERE a.tenant_id=$1 AND a.workspace_id=$4 AND a.project_id IS NULL AND a.source_scope='workspace_default'`,
			tenant, digest, actor, workspaceID); err != nil {
			t.Fatalf("seed workspace-default revision rejected: %v", err)
		}
		_, err = db.ExecContext(ctx, `
			INSERT INTO project_artifact_idempotency
				(tenant_id, workspace_id, project_id, scope, idem_key, payload_hash, state, artifact_id, initial_status, result_revision, committed_at)
			VALUES ($1, $2, $3, 'artifact:save', 'fabricated-wsd', decode('0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20','hex'), 'committed',
			        (SELECT a.id FROM project_artifacts a WHERE a.tenant_id=$1 AND a.workspace_id=$2 AND a.project_id IS NULL AND a.source_scope='workspace_default'), 'created', 1, now())`,
			tenant, workspaceID, projectID)
		assertPgErrorCode(t, err, "P0001")

		// Usage counters fold monotonically and can never be reset or
		// removed; workspace-default counters have their own row.
		if _, err := db.ExecContext(ctx, `
			INSERT INTO project_storage_usage (tenant_id, workspace_id, project_id, content_bytes, metadata_bytes, event_bytes)
			VALUES ($1, $2, $3, 100, 40, 2)`, tenant, workspaceID, projectID); err != nil {
			t.Fatalf("seed usage rejected: %v", err)
		}
		if _, err := db.ExecContext(ctx, `
			UPDATE project_storage_usage SET content_bytes = content_bytes + 1
			 WHERE tenant_id=$1 AND workspace_id=$2 AND project_id=$3`, tenant, workspaceID, projectID); err != nil {
			t.Fatalf("monotonic usage fold rejected: %v", err)
		}
		_, err = db.ExecContext(ctx, `
			UPDATE project_storage_usage SET content_bytes = 0
			 WHERE tenant_id=$1 AND workspace_id=$2 AND project_id=$3`, tenant, workspaceID, projectID)
		assertPgErrorCode(t, err, "P0001")
		_, err = db.ExecContext(ctx, `
			DELETE FROM project_storage_usage WHERE tenant_id=$1 AND workspace_id=$2 AND project_id=$3`, tenant, workspaceID, projectID)
		assertPgErrorCode(t, err, "P0001")
		_, err = db.ExecContext(ctx, `
			INSERT INTO project_storage_usage (tenant_id, workspace_id, project_id, content_bytes)
			VALUES ($1, $2, $3, 1)`, tenant, workspaceID, projectID)
		assertPgErrorCode(t, err, "23505")
		if _, err := db.ExecContext(ctx, `
			INSERT INTO project_storage_usage (tenant_id, workspace_id, project_id, content_bytes)
			VALUES ($1, $2, NULL, 7)`, tenant, workspaceID); err != nil {
			t.Fatalf("seed workspace-default usage rejected: %v", err)
		}
		_, err = db.ExecContext(ctx, `
			INSERT INTO project_storage_usage (tenant_id, workspace_id, project_id, content_bytes)
			VALUES ($1, $2, NULL, 7)`, tenant, workspaceID)
		assertPgErrorCode(t, err, "23505")

		// Immutable history: UPDATE/DELETE on revisions and events abort.
		_, err = db.ExecContext(ctx, `UPDATE project_artifact_revisions SET content='rewritten' WHERE artifact_id=$1 AND tenant_id=$2`, artifactID, tenant)
		assertPgErrorCode(t, err, "P0001")
		_, err = db.ExecContext(ctx, `DELETE FROM project_artifact_events WHERE artifact_id=$1 AND tenant_id=$2`, artifactID, tenant)
		assertPgErrorCode(t, err, "P0001")

		// Hard delete is restricted while history references the artifact,
		// and the owning project cannot be hard-deleted either.
		_, err = db.ExecContext(ctx, `DELETE FROM project_artifacts WHERE id=$1 AND tenant_id=$2`, artifactID, tenant)
		assertPgErrorCode(t, err, "23503")
		_, err = db.ExecContext(ctx, `DELETE FROM projects WHERE id=$1 AND tenant_id=$2`, projectID, tenant)
		assertPgErrorCode(t, err, "23503")

		// Soft delete is a provenance-preserving state transition.
		if _, err := db.ExecContext(ctx, `
			UPDATE project_artifacts
			   SET status='deleted', deleted_at=now(), deleted_by=$3, delete_reason='superseded', updated_at=now()
			 WHERE id=$1 AND tenant_id=$2`, artifactID, tenant, actor); err != nil {
			t.Fatalf("soft delete rejected: %v", err)
		}
		for _, probe := range []struct {
			sql  string
			want int
		}{
			{`SELECT count(*) FROM project_artifact_revisions WHERE tenant_id=$1 AND artifact_id=$2`, 2},
			{`SELECT count(*) FROM project_artifact_events WHERE tenant_id=$1 AND artifact_id=$2`, 1},
			{`SELECT count(*) FROM project_artifact_activations WHERE tenant_id=$1 AND artifact_id=$2`, 1},
		} {
			var count int
			if err := db.QueryRowContext(ctx, probe.sql, tenant, artifactID).Scan(&count); err != nil || count != probe.want {
				t.Errorf("soft delete lost history (%s): count=%d err=%v; want %d", probe.sql, count, err, probe.want)
			}
		}
	})

	// Scope fixture for the trusted principal-derived RLS behavioral matrix:
	// one tenant with workspaces W1/W2, projects P1/P2 in W1 and P3 in W2,
	// verified principal actor subjects with explicit durable grants
	// (wildcard-project, exact-project, and no-grant actors), boundary
	// artifacts, a committed receipt, and a usage counter inside (W1, P1).
	scopeTenant := "00000000-0000-0000-0000-00000000020b"
	scopeActorPub := "00000000-0000-0000-0000-00000000020c"        // grants: workspace W1 + project '*'
	scopeActorNoGrantPub := "00000000-0000-0000-0000-00000000020d" // no grants at all
	scopeActorExactPub := "00000000-0000-0000-0000-00000000020e"   // grants: workspace W1 + project P2 exact
	scopeActorProvPub := "00000000-0000-0000-0000-00000000020f"    // granted later via provisioning INSERT
	digestScope := "b1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
	var ws1, ws2, p1, p2, p3 int64
	var ws1Pub, ws2Pub, p1Pub, p2Pub, p3Pub string
	if err := db.QueryRowContext(ctx, `
		WITH organization AS (
			INSERT INTO organizations(tenant_id,name) VALUES($1,'scope fixture') RETURNING id
		), w1 AS (
			INSERT INTO workspaces(tenant_id,organization_id,name)
			SELECT $1,id,'scope w1' FROM organization RETURNING id, public_id::text
		), w2 AS (
			INSERT INTO workspaces(tenant_id,organization_id,name)
			SELECT $1,id,'scope w2' FROM organization RETURNING id, public_id::text
		), pr1 AS (
			INSERT INTO projects(tenant_id,workspace_id,name) SELECT $1,id,'scope p1' FROM w1 RETURNING id, public_id::text
		), pr2 AS (
			INSERT INTO projects(tenant_id,workspace_id,name) SELECT $1,id,'scope p2' FROM w1 RETURNING id, public_id::text
		), pr3 AS (
			INSERT INTO projects(tenant_id,workspace_id,name) SELECT $1,id,'scope p3' FROM w2 RETURNING id, public_id::text
		)
		SELECT (SELECT id FROM w1), (SELECT public_id FROM w1),
		       (SELECT id FROM pr1), (SELECT public_id FROM pr1),
		       (SELECT id FROM pr2), (SELECT public_id FROM pr2),
		       (SELECT id FROM w2), (SELECT public_id FROM w2),
		       (SELECT id FROM pr3), (SELECT public_id FROM pr3)`, scopeTenant).
		Scan(&ws1, &ws1Pub, &p1, &p1Pub, &p2, &p2Pub, &ws2, &ws2Pub, &p3, &p3Pub); err != nil {
		t.Fatalf("seed scope hierarchy: %v", err)
	}
	for _, actor := range []struct {
		pub     string
		subject string
	}{{scopeActorPub, "scope-principal"}, {scopeActorNoGrantPub, "scope-principal-nogrant"}, {scopeActorExactPub, "scope-principal-exact"}, {scopeActorProvPub, "scope-principal-provisioned"}} {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO actor_subjects (tenant_id, subject, actor_type, public_id)
			VALUES ($1, $2, 'user', $3)`, scopeTenant, actor.subject, actor.pub); err != nil {
			t.Fatalf("seed actor %s: %v", actor.subject, err)
		}
	}
	// Durable grants: W1 + every project of W1 for the wildcard actor; W1 +
	// exactly P2 for the exact actor.
	for _, grant := range []struct {
		actor string
		kind  string
		value string
	}{{scopeActorPub, "workspace", ws1Pub}, {scopeActorPub, "project", "*"}, {scopeActorExactPub, "workspace", ws1Pub}, {scopeActorExactPub, "project", p2Pub}} {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO principal_grants (tenant_id, actor_public_id, grant_type, grant_value)
			VALUES ($1, $2, $3, $4)`, scopeTenant, grant.actor, grant.kind, grant.value); err != nil {
			t.Fatalf("seed grant %s/%s: %v", grant.kind, grant.value, err)
		}
	}
	// Provenance-only binding fixtures: every cortex_bind_principal call
	// below must present the verification-minted token-bound proof, so each
	// scope actor holds a real live service bearer. The service row gives
	// the token a live subject, and the stored prefix/digest use the
	// platform's own derivation (textual head plus tenant-keyed HMAC of
	// the bearer).
	scopeSecrets := map[string]string{
		scopeActorPub:        "scope-wildcard-bearer-a111",
		scopeActorNoGrantPub: "scope-nogrant-bearer-b222",
		scopeActorExactPub:   "scope-exact-bearer-c3333",
		scopeActorProvPub:    "scope-prov-bearer-d44444",
	}
	for pub, secret := range scopeSecrets {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO service_accounts (tenant_id, public_id, name)
			VALUES ($1, $2, 'scope bearer subject')`, scopeTenant, pub); err != nil {
			t.Fatalf("seed bearer service account %s: %v", pub, err)
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO api_tokens (tenant_id, token_prefix, token_digest, subject_service_account_id, created_by)
			SELECT $1::uuid, left($2, 12), hmac(convert_to($2, 'UTF8'), convert_to($1::text, 'UTF8'), 'sha256'), s.id, $3::uuid
			  FROM service_accounts s
			 WHERE s.tenant_id = $1::uuid AND s.public_id = $3::uuid`, scopeTenant, secret, pub); err != nil {
			t.Fatalf("seed bearer token for %s: %v", pub, err)
		}
	}
	for _, seed := range []struct {
		name string
		ws   int64
		proj any
	}{{"p1", ws1, p1}, {"p2", ws1, p2}, {"p3", ws2, p3}, {"wsd", ws1, nil}} {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO project_artifacts
				(tenant_id, workspace_id, project_id, kind, key, source_scope, current_revision, content_bytes, metadata_bytes, digest)
			VALUES ($1, $2, $3, 'skill', 'shared', $4, 1, 1, 1, $5)`,
			scopeTenant, seed.ws, seed.proj, map[bool]string{true: "project", false: "workspace_default"}[seed.proj != nil], digestScope); err != nil {
			t.Fatalf("seed scope artifact %s: %v", seed.name, err)
		}
	}
	// The committed receipt's exact result must reference a real revision of
	// the same coordinate's artifact.
	if _, err := db.ExecContext(ctx, `
		INSERT INTO project_artifact_revisions
			(tenant_id, artifact_id, revision, content, content_bytes, metadata, metadata_bytes, digest, created_by)
		SELECT $1, a.id, 1, 'shared', 5, '{}', 2, $2, $3
		  FROM project_artifacts a
		 WHERE a.tenant_id=$1 AND a.workspace_id=$4 AND a.project_id=$5 AND a.source_scope='project'`,
		scopeTenant, digestScope, scopeActorPub, ws1, p1); err != nil {
		t.Fatalf("seed scope revision: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO project_artifact_idempotency
			(tenant_id, workspace_id, project_id, scope, idem_key, payload_hash, state, artifact_id, initial_status, result_revision, committed_at)
		VALUES ($1, $2, $3, 'artifact:save', 'scope-app', decode('0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20','hex'), 'committed',
		        (SELECT id FROM project_artifacts WHERE tenant_id=$1 AND workspace_id=$2 AND project_id=$3 AND source_scope='project'), 'created', 1, now())`,
		scopeTenant, ws1, p1); err != nil {
		t.Fatalf("seed scope receipt: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO project_storage_usage (tenant_id, workspace_id, project_id, content_bytes)
		VALUES ($1, $2, $3, 50)`, scopeTenant, ws1, p1); err != nil {
		t.Fatalf("seed scope usage: %v", err)
	}

	// withAppScope runs fn inside one transaction in which the verified
	// principal is bound through its real bearer (verification-minted
	// provenance, never a literal digest) and a trusted principal-derived
	// workspace/project scope is installed (migration 106), then executes
	// as cortex_app so FORCE RLS policies apply exactly as in production.
	// The transaction is ALWAYS rolled back: fn's intentional
	// privilege/RLS aborts poison the transaction (later statements would
	// only see 25P02), and the rollback also undoes SET ROLE, which is
	// transactional — so no RESET ROLE is needed.
	withAppScope := func(t *testing.T, actorPub string, wsPub, projPub any, fn func(tx *sql.Tx) error) {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("begin scoped tx: %v", err)
		}
		defer func() { _ = tx.Rollback() }()
		bindVerifiedPrincipal(t, ctx, tx, scopeTenant, actorPub, scopeSecrets[actorPub])
		if wsPub != nil {
			if _, err := tx.ExecContext(ctx, `SELECT cortex_bind_project_scope($1, $2)`, wsPub, projPub); err != nil {
				t.Fatalf("bind project scope: %v", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `SET ROLE cortex_app`); err != nil {
			t.Fatalf("set role cortex_app: %v", err)
		}
		if fnErr := fn(tx); fnErr != nil {
			t.Fatal(fnErr)
		}
	}

	t.Run("scope binding is grants-derived with explicit wildcards", func(t *testing.T) {
		// Second tenant with its own workspace proves cross-tenant denial of
		// the scope binder itself.
		otherTenant := "00000000-0000-0000-0000-00000000030f"
		var otherWsPub string
		if err := db.QueryRowContext(ctx, `
			WITH organization AS (
				INSERT INTO organizations(tenant_id,name) VALUES($1,'other tenant') RETURNING id
			), w AS (
				INSERT INTO workspaces(tenant_id,organization_id,name)
				SELECT $1,id,'other tenant workspace' FROM organization RETURNING public_id::text
			)
			SELECT public_id FROM w`, otherTenant).Scan(&otherWsPub); err != nil {
			t.Fatalf("seed other tenant: %v", err)
		}
		bindScope := func(actorPub string, wsPub, projPub any) error {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatalf("begin tx: %v", err)
			}
			defer func() { _ = tx.Rollback() }()
			bindVerifiedPrincipal(t, ctx, tx, scopeTenant, actorPub, scopeSecrets[actorPub])
			_, err = tx.ExecContext(ctx, `SELECT cortex_bind_project_scope($1, $2)`, wsPub, projPub)
			return err
		}
		// The scope binder resolves strictly inside the principal-derived
		// tenant: another tenant's workspace public ID is refused.
		assertPgErrorCode(t, bindScope(scopeActorPub, otherWsPub, nil), "42501")
		// Same-tenant membership is NOT authorization: without a workspace
		// grant, no binding is possible at all — not even workspace-wide.
		assertPgErrorCode(t, bindScope(scopeActorNoGrantPub, ws1Pub, nil), "42501")
		assertPgErrorCode(t, bindScope(scopeActorNoGrantPub, ws1Pub, p1Pub), "42501")
		// The wildcard actor holds workspace W1 plus project '*': every
		// project of W1 binds, another same-tenant workspace does not, and a
		// project of another workspace does not.
		if err := bindScope(scopeActorPub, ws1Pub, p1Pub); err != nil {
			t.Fatalf("wildcard actor bind (W1,P1) rejected: %v", err)
		}
		if err := bindScope(scopeActorPub, ws1Pub, p2Pub); err != nil {
			t.Fatalf("wildcard actor bind (W1,P2) rejected: %v", err)
		}
		assertPgErrorCode(t, bindScope(scopeActorPub, ws2Pub, nil), "42501")
		assertPgErrorCode(t, bindScope(scopeActorPub, ws1Pub, p3Pub), "42501")
		// The exact actor holds workspace W1 plus exactly P2: P2 binds, P1
		// (same workspace, not granted) does not.
		if err := bindScope(scopeActorExactPub, ws1Pub, p2Pub); err != nil {
			t.Fatalf("exact actor bind (W1,P2) rejected: %v", err)
		}
		assertPgErrorCode(t, bindScope(scopeActorExactPub, ws1Pub, p1Pub), "42501")
	})

	t.Run("principal rebind clears stale scope and persists actor identity", func(t *testing.T) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("begin tx: %v", err)
		}
		defer func() { _ = tx.Rollback() }()
		bindVerifiedPrincipal(t, ctx, tx, scopeTenant, scopeActorPub, scopeSecrets[scopeActorPub])
		if _, err := tx.ExecContext(ctx, `SELECT cortex_bind_project_scope($1, $2)`, ws1Pub, p1Pub); err != nil {
			t.Fatalf("bind scope: %v", err)
		}
		var boundWs, boundProj int64
		if err := tx.QueryRowContext(ctx, `SELECT cortex_current_workspace(), cortex_current_project()`).Scan(&boundWs, &boundProj); err != nil {
			t.Fatalf("read bound scope: %v", err)
		}
		if boundWs != ws1 || boundProj != p1 {
			t.Fatalf("bound scope=(%d,%d); want (%d,%d)", boundWs, boundProj, ws1, p1)
		}
		// Rebinding ANY principal clears the stale scope: the new principal
		// starts unscoped no matter what the previous one had bound.
		bindVerifiedPrincipal(t, ctx, tx, scopeTenant, scopeActorNoGrantPub, scopeSecrets[scopeActorNoGrantPub])
		var staleWs, staleProj sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT cortex_current_workspace(), cortex_current_project()`).Scan(&staleWs, &staleProj); err != nil {
			t.Fatalf("read scope after rebind: %v", err)
		}
		if staleWs.Valid || staleProj.Valid {
			t.Fatalf("scope survived principal rebind: workspace=%v project=%v; want NULL/NULL", staleWs, staleProj)
		}
		// The persisted actor identity switched to the rebound principal:
		// it holds no grants, so the same workspace it just "saw" is now
		// refused.
		_, err = tx.ExecContext(ctx, `SELECT cortex_bind_project_scope($1, NULL)`, ws1Pub)
		assertPgErrorCode(t, err, "42501")
	})

	t.Run("cortex_app cannot mutate principal grants directly", func(t *testing.T) {
		// Privilege denial first, outside any RLS subtleties.
		var canUpdate, canDelete bool
		if err := db.QueryRowContext(ctx, `SELECT has_table_privilege('cortex_app', 'public.principal_grants', 'UPDATE')`).Scan(&canUpdate); err != nil || canUpdate {
			t.Errorf("cortex_app UPDATE on principal_grants=%v err=%v; want false", canUpdate, err)
		}
		if err := db.QueryRowContext(ctx, `SELECT has_table_privilege('cortex_app', 'public.principal_grants', 'DELETE')`).Scan(&canDelete); err != nil || canDelete {
			t.Errorf("cortex_app DELETE on principal_grants=%v err=%v; want false", canDelete, err)
		}
		// The authorized provisioning path still works: INSERTing a new
		// grant for a not-yet-granted actor is exactly what
		// UserRepository.Create does, and it must commit durably. Each
		// denial probe runs in its OWN transaction because an intentional
		// privilege denial aborts the current transaction block.
		asAppDenial := func(t *testing.T, probe string, query string, args ...any) {
			t.Helper()
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatalf("begin %s tx: %v", probe, err)
			}
			defer func() { _ = tx.Rollback() }()
			bindVerifiedPrincipal(t, ctx, tx, scopeTenant, scopeActorPub, scopeSecrets[scopeActorPub])
			if _, err := tx.ExecContext(ctx, `SELECT cortex_bind_project_scope($1, $2)`, ws1Pub, p1Pub); err != nil {
				t.Fatalf("%s: bind project scope: %v", probe, err)
			}
			if _, err := tx.ExecContext(ctx, `SET ROLE cortex_app`); err != nil {
				t.Fatalf("%s: set role cortex_app: %v", probe, err)
			}
			_, err = tx.ExecContext(ctx, query, args...)
			assertPgErrorCode(t, err, "42501")
		}
		asAppDenial(t, "grant UPDATE", `
			UPDATE principal_grants SET grant_value='*' WHERE tenant_id=$1 AND actor_public_id=$2`,
			scopeTenant, scopeActorNoGrantPub)
		asAppDenial(t, "grant DELETE", `
			DELETE FROM principal_grants WHERE tenant_id=$1 AND actor_public_id=$2`,
			scopeTenant, scopeActorNoGrantPub)
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("begin provisioning tx: %v", err)
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO principal_grants (tenant_id, actor_public_id, grant_type, grant_value)
			VALUES ($1, $2, 'workspace', $3)`, scopeTenant, scopeActorProvPub, ws1Pub); err != nil {
			t.Fatalf("provisioning INSERT rejected: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit provisioning: %v", err)
		}
		// The provisioned actor can now bind the granted workspace.
		tx2, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("begin tx: %v", err)
		}
		defer func() { _ = tx2.Rollback() }()
		bindVerifiedPrincipal(t, ctx, tx2, scopeTenant, scopeActorProvPub, scopeSecrets[scopeActorProvPub])
		if _, err := tx2.ExecContext(ctx, `SELECT cortex_bind_project_scope($1, NULL)`, ws1Pub); err != nil {
			t.Fatalf("provisioned actor cannot bind its granted workspace: %v", err)
		}
	})

	t.Run("principal scope RLS denies same-tenant unauthorized workspace and project", func(t *testing.T) {
		// Bound to (W1, P1): the principal sees exactly the P1 project
		// artifact plus the W1 workspace default; the same-tenant W2/P3
		// artifact and the same-workspace P2 artifact are invisible.
		withAppScope(t, scopeActorPub, ws1Pub, p1Pub, func(tx *sql.Tx) error {
			var total, defaults int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM project_artifacts`).Scan(&total); err != nil || total != 2 {
				return fmt.Errorf("bound (W1,P1) visible artifacts=%d err=%v; want 2 (project row + workspace default)", total, err)
			}
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM project_artifacts WHERE source_scope='workspace_default'`).Scan(&defaults); err != nil || defaults != 1 {
				return fmt.Errorf("bound (W1,P1) visible defaults=%d err=%v; want 1", defaults, err)
			}
			// Writing outside the bound scope is impossible even with valid
			// hierarchy coordinates and an underlying wildcard grant: RLS
			// WITH CHECK refuses the row.
			_, err := tx.ExecContext(ctx, `
				INSERT INTO project_artifacts
					(tenant_id, workspace_id, project_id, kind, key, source_scope, current_revision, content_bytes, metadata_bytes, digest)
				VALUES ($1, $2, $3, 'skill', 'escape', 'project', 1, 1, 1, $4)`,
				scopeTenant, ws2, p3, digestScope)
			assertPgErrorCode(t, err, "42501")
			return nil
		})

		// Bound to the workspace only (absent project): only the workspace
		// default is visible, and project-scoped writes are refused.
		withAppScope(t, scopeActorPub, ws1Pub, nil, func(tx *sql.Tx) error {
			var total int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM project_artifacts`).Scan(&total); err != nil || total != 1 {
				return fmt.Errorf("workspace-only binding visible artifacts=%d err=%v; want 1 (the default)", total, err)
			}
			_, err := tx.ExecContext(ctx, `
				INSERT INTO project_artifacts
					(tenant_id, workspace_id, project_id, kind, key, source_scope, current_revision, content_bytes, metadata_bytes, digest)
				VALUES ($1, $2, $3, 'skill', 'escape2', 'project', 1, 1, 1, $4)`,
				scopeTenant, ws1, p1, digestScope)
			assertPgErrorCode(t, err, "42501")
			return nil
		})

		// The exact actor holds a grant for P2 only: bound to (W1, P2) it
		// sees the P2 artifact plus the default, and the same-workspace P1
		// artifact stays invisible and untouchable even though P1 belongs to
		// a workspace the actor is granted.
		withAppScope(t, scopeActorExactPub, ws1Pub, p2Pub, func(tx *sql.Tx) error {
			var total int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM project_artifacts`).Scan(&total); err != nil || total != 2 {
				return fmt.Errorf("bound (W1,P2) visible artifacts=%d err=%v; want 2", total, err)
			}
			res, err := tx.ExecContext(ctx, `
				UPDATE project_artifacts SET updated_at = now()
				 WHERE tenant_id=$1 AND workspace_id=$2 AND project_id=$3`,
				scopeTenant, ws1, p1)
			if err != nil {
				return fmt.Errorf("update ungranted same-workspace project: %v", err)
			}
			if rows, _ := res.RowsAffected(); rows != 0 {
				return fmt.Errorf("ungranted same-workspace project UPDATE affected %d rows; want 0", rows)
			}
			return nil
		})

		// No project scope binding at all: nothing is visible or writable.
		withAppScope(t, scopeActorPub, nil, nil, func(tx *sql.Tx) error {
			var total int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM project_artifacts`).Scan(&total); err != nil || total != 0 {
				return fmt.Errorf("unbound visible artifacts=%d err=%v; want 0", total, err)
			}
			_, err := tx.ExecContext(ctx, `
				INSERT INTO project_artifacts
					(tenant_id, workspace_id, project_id, kind, key, source_scope, current_revision, content_bytes, metadata_bytes, digest)
				VALUES ($1, $2, NULL, 'skill', 'escape3', 'workspace_default', 1, 1, 1, $3)`,
				scopeTenant, ws1, digestScope)
			assertPgErrorCode(t, err, "42501")
			return nil
		})
	})

	t.Run("cortex_app cannot destroy evidence or reset quota", func(t *testing.T) {
		// Each denial runs in its own scoped transaction: an intentional
		// abort poisons the current transaction block, so sibling probes
		// would only observe 25P02 instead of the real denial.
		probes := []struct {
			name  string
			query string
			args  []any
			code  string
		}{
			// No DELETE privilege on immutable history and ledger tables.
			{"revisions delete", `DELETE FROM project_artifact_revisions WHERE tenant_id=$1`, []any{scopeTenant}, "42501"},
			{"events delete", `DELETE FROM project_artifact_events WHERE tenant_id=$1`, []any{scopeTenant}, "42501"},
			{"idempotency delete", `DELETE FROM project_artifact_idempotency WHERE tenant_id=$1`, []any{scopeTenant}, "42501"},
			// Quota counters cannot be reset: UPDATE is granted, but the
			// monotonic guard aborts any decrease.
			{"usage reset", `
				UPDATE project_storage_usage SET content_bytes = 0
				 WHERE tenant_id=$1 AND workspace_id=$2 AND project_id=$3`, []any{scopeTenant, ws1, p1}, "P0001"},
			{"usage delete", `DELETE FROM project_storage_usage WHERE tenant_id=$1`, []any{scopeTenant}, "42501"},
			// Committed receipts are immutable replay evidence.
			{"receipt rewrite", `
				UPDATE project_artifact_idempotency SET result_revision = 99
				 WHERE tenant_id=$1 AND scope='artifact:save' AND idem_key='scope-app'`, []any{scopeTenant}, "P0001"},
		}
		for _, probe := range probes {
			probe := probe
			t.Run(probe.name, func(t *testing.T) {
				withAppScope(t, scopeActorPub, ws1Pub, p1Pub, func(tx *sql.Tx) error {
					_, err := tx.ExecContext(ctx, probe.query, probe.args...)
					assertPgErrorCode(t, err, probe.code)
					return nil
				})
			})
		}
	})

	t.Run("reapply is idempotent", func(t *testing.T) {
		if err := ApplyPostgresServerMigrations(ctx, db); err != nil {
			t.Fatalf("reapply: %v", err)
		}
		var ledgerCount int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM cortex_server_migrations WHERE version BETWEEN 100 AND 106`).Scan(&ledgerCount); err != nil || ledgerCount != 7 {
			t.Fatalf("ledger count after reapply=%d err=%v; want 7", ledgerCount, err)
		}
	})
}

// bootstrapResult captures the three-column reconciler return.
type bootstrapResult struct {
	tokenID string
	version int64
	action  string
}

// callBootstrap executes the migration-role bootstrap reconciler and returns
// its (token_public_id, grant_version, bootstrap_action) row. Scanning
// exactly three columns also proves the pinned return shape.
func callBootstrap(ctx context.Context, db *sql.DB, tenant, wsPub, actor, subject, serviceName, grantsJSON, tokenName, secret, reason string) (bootstrapResult, error) {
	var r bootstrapResult
	err := db.QueryRowContext(ctx,
		`SELECT token_public_id, grant_version, bootstrap_action
		   FROM public.cortex_bootstrap_service_principal($1,$2,$3,$4,$5,$6::jsonb,$7,$8,$9)`,
		tenant, wsPub, actor, subject, serviceName, grantsJSON, tokenName, secret, reason).
		Scan(&r.tokenID, &r.version, &r.action)
	return r, err
}

// mustBootstrap fails the test on any reconciler error.
func mustBootstrap(t *testing.T, ctx context.Context, db *sql.DB, tenant, wsPub, actor, subject, serviceName, grantsJSON, tokenName, secret, reason string) bootstrapResult {
	t.Helper()
	r, err := callBootstrap(ctx, db, tenant, wsPub, actor, subject, serviceName, grantsJSON, tokenName, secret, reason)
	if err != nil {
		t.Fatalf("cortex_bootstrap_service_principal(%s): %v", actor, err)
	}
	return r
}

// bootstrapCount is a one-integer scan helper for the matrix assertions.
func bootstrapCount(t *testing.T, ctx context.Context, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	return count
}

// assertBootstrapClean proves a failed call for a fresh actor left zero
// partial effects: no service account, actor, grants, token, or audit rows.
func assertBootstrapClean(t *testing.T, ctx context.Context, db *sql.DB, tenant, actor string) {
	t.Helper()
	for _, probe := range []struct {
		name  string
		query string
	}{
		{"service_accounts", `SELECT count(*) FROM service_accounts WHERE tenant_id=$1 AND public_id=$2`},
		{"actor_subjects", `SELECT count(*) FROM actor_subjects WHERE tenant_id=$1 AND public_id=$2`},
		{"principal_grants", `SELECT count(*) FROM principal_grants WHERE tenant_id=$1 AND actor_public_id=$2`},
		{"api_tokens", `SELECT count(*) FROM api_tokens WHERE tenant_id=$1 AND created_by=$2`},
		{"audit_events", `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND (actor_public_id=$2 OR resource_public_id=$2)`},
	} {
		if got := bootstrapCount(t, ctx, db, probe.query, tenant, actor); got != 0 {
			t.Errorf("failed bootstrap leaked %s rows for %s: %d", probe.name, actor, got)
		}
	}
}

// TestPostgresMigration106BootstrapReconciler exercises the migration-role
// bootstrap reconciler against real PostgreSQL 16 (REQ-BPR-003/004/008):
// fresh atomic provisioning of the service actor, canonical grants, the
// reserved-name bootstrap token, and non-secret audit; identical sequential
// and concurrent restarts that mutate nothing; grant reconcile with exactly
// one grant_version bump; bearer rotation and fail-closed revoked-bearer
// behavior with revoked history preserved; recovery through an explicitly
// different bearer; conflict/validation/audit failures with zero partial
// effects; secret-free outputs and audit metadata; and the EXECUTE matrix
// that leaves the routine callable only by cortex_migration. The migration
// DSN suite is the authoritative executor; without the DSN it fails rather
// than skips.
func TestPostgresMigration106BootstrapReconciler(t *testing.T) {
	dsn := os.Getenv("CORTEX_TEST_POSTGRES_MIGRATION_DSN")
	if dsn == "" {
		t.Fatal("CORTEX_TEST_POSTGRES_MIGRATION_DSN is required for postgres_integration tests")
	}
	ctx := context.Background()

	freshDSN, cleanupFresh, err := isolatedPostgresDatabase(dsn, "migration106boot")
	if err != nil {
		t.Fatalf("create isolated 106 bootstrap database: %v", err)
	}
	defer cleanupFresh()
	db, err := sql.Open("pgx", freshDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	if err := ApplyPostgresServerMigrations(ctx, db); err != nil {
		t.Fatalf("fresh apply 100-106: %v", err)
	}

	// Sentinel configured bearers; their digests and every derived value
	// must never appear in any reconciler output or audit row.
	const (
		secretAlpha = "bootstrap-bearer-sentinel-alpha"
		secretBeta  = "bootstrap-bearer-sentinel-beta"
		secretGamma = "bootstrap-bearer-sentinel-gamma"
		secretDelta = "bootstrap-bearer-sentinel-delta"
		// The concurrent identity uses a fifth distinct bearer: token
		// digests are unique per tenant, and every sentinel digest above
		// is already held by an actorA token row (active or revoked).
		secretEpsilon = "bootstrap-bearer-sentinel-epsilon"
		// The audit-outage rollback probe uses a sixth: it must reach the
		// injected audit trigger on its first provisioning audit insert,
		// so its bearer digest must be untouched by any earlier row.
		secretZeta = "bootstrap-bearer-sentinel-zeta"
		tokenName  = "bootstrap/server-bearer"
		// The second bootstrap identity uses its own deterministic reserved
		// name: one reserved name is bound to exactly one subject.
		tokenNameB = "bootstrap/server-bearer-b"
	)
	tenant := "00000000-0000-0000-0000-0000000003b0"
	actorA := "00000000-0000-0000-0000-0000000003b1"
	actorB := "00000000-0000-0000-0000-0000000003b2"
	var wsPub string
	if err := db.QueryRowContext(ctx, `
		WITH organization AS (
			INSERT INTO organizations(tenant_id,name) VALUES($1,'bootstrap fixture') RETURNING id
		)
		INSERT INTO workspaces(tenant_id,organization_id,name)
		SELECT $1,id,'bootstrap workspace' FROM organization RETURNING public_id::text`, tenant).Scan(&wsPub); err != nil {
		t.Fatalf("seed bootstrap tenant/workspace: %v", err)
	}
	grantsJSON := func(pairs ...[2]string) string {
		parts := make([]string, 0, len(pairs))
		for _, p := range pairs {
			parts = append(parts, fmt.Sprintf(`{"type":%q,"value":%q}`, p[0], p[1]))
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	baseGrants := grantsJSON(
		[2]string{"role", "owner"},
		[2]string{"workspace", wsPub},
		[2]string{"scope", "workspaces:read"},
	)
	// Expected digests computed with the platform's own derivations so the
	// assertions never embed secret material.
	grantDigestFor := func(grants string) string {
		var digest string
		if err := db.QueryRowContext(ctx, `
			SELECT encode(digest(convert_to(string_agg(q.g, E'\n' ORDER BY q.g), 'UTF8'), 'sha256'), 'hex')
			  FROM (SELECT DISTINCT (obj->>'type') || ':' || (obj->>'value') AS g
			          FROM jsonb_array_elements($1::jsonb) AS obj) q`, grants).Scan(&digest); err != nil {
			t.Fatalf("compute canonical grant digest: %v", err)
		}
		return digest
	}
	hmacHexFor := func(secret string) string {
		var hex string
		if err := db.QueryRowContext(ctx,
			`SELECT encode(hmac(convert_to($1,'UTF8'), convert_to($2::text,'UTF8'),'sha256'),'hex')`, secret, tenant).Scan(&hex); err != nil {
			t.Fatalf("compute bearer hmac: %v", err)
		}
		return hex
	}
	// bearerPrefixFor derives the reconciler's deterministic stored prefix:
	// the textual 12-character head plus the first 16 hex characters of the
	// tenant-keyed digest, so same-head bearers stay unique under
	// UNIQUE (tenant_id, token_prefix) while verification keeps matching
	// the head plus exact digest equality.
	bearerPrefixFor := func(secret string) string {
		var prefix string
		if err := db.QueryRowContext(ctx,
			`SELECT left($1,12) || ':' || substring(encode(hmac(convert_to($1,'UTF8'), convert_to($2::text,'UTF8'),'sha256'),'hex') FROM 1 FOR 16)`,
			secret, tenant).Scan(&prefix); err != nil {
			t.Fatalf("compute deterministic bearer prefix: %v", err)
		}
		return prefix
	}

	t.Run("fresh provisioning is atomic and complete", func(t *testing.T) {
		r := mustBootstrap(t, ctx, db, tenant, wsPub, actorA, "cortex-server-bootstrap", "cortex-server", baseGrants, tokenName, secretAlpha, "initial startup")
		if r.action != "provisioned" || r.version != 1 {
			t.Fatalf("fresh bootstrap action=%q version=%d; want provisioned/1", r.action, r.version)
		}
		var svcName string
		var svcActive bool
		if err := db.QueryRowContext(ctx, `SELECT name, active FROM service_accounts WHERE tenant_id=$1 AND public_id=$2`, tenant, actorA).Scan(&svcName, &svcActive); err != nil || svcName != "cortex-server" || !svcActive {
			t.Fatalf("service account row name=%q active=%v err=%v", svcName, svcActive, err)
		}
		var subject string
		var actorType string
		var active bool
		var revoked sql.NullTime
		var version int64
		var digest string
		if err := db.QueryRowContext(ctx, `SELECT subject, actor_type, active, revoked_at, grant_version, grant_digest FROM actor_subjects WHERE tenant_id=$1 AND public_id=$2`, tenant, actorA).Scan(&subject, &actorType, &active, &revoked, &version, &digest); err != nil {
			t.Fatalf("actor row: %v", err)
		}
		if subject != "cortex-server-bootstrap" || actorType != "service_account" || !active || revoked.Valid || version != 1 || digest != grantDigestFor(baseGrants) {
			t.Fatalf("actor row subject=%q type=%q active=%v revoked=%v version=%d digest=%s", subject, actorType, active, revoked.Valid, version, digest)
		}
		if got := bootstrapCount(t, ctx, db, `SELECT count(*) FROM principal_grants WHERE tenant_id=$1 AND actor_public_id=$2`, tenant, actorA); got != 3 {
			t.Fatalf("grant rows=%d; want exact canonical 3", got)
		}
		if got := bootstrapCount(t, ctx, db, `SELECT count(*) FROM principal_grants WHERE tenant_id=$1 AND actor_public_id=$2 AND grant_type||':'||grant_value IN ('role:owner','workspace:'||$3,'scope:workspaces:read')`, tenant, actorA, wsPub); got != 3 {
			t.Fatalf("canonical grant values matched=%d; want 3", got)
		}
		var prefix, name, tokenDigestHex string
		var expires, tokenRevoked sql.NullTime
		var createdBy string
		if err := db.QueryRowContext(ctx, `
			SELECT token_prefix, name, encode(token_digest,'hex'), expires_at, revoked_at, created_by::text
			  FROM api_tokens WHERE tenant_id=$1 AND public_id=$2`, tenant, r.tokenID).Scan(&prefix, &name, &tokenDigestHex, &expires, &tokenRevoked, &createdBy); err != nil {
			t.Fatalf("bootstrap token row: %v", err)
		}
		if prefix != bearerPrefixFor(secretAlpha) || name != tokenName || tokenDigestHex != hmacHexFor(secretAlpha) || expires.Valid || tokenRevoked.Valid || createdBy != actorA {
			t.Fatalf("token row prefix=%q name=%q digest=%s expires=%v revoked=%v created_by=%s", prefix, name, tokenDigestHex, expires.Valid, tokenRevoked.Valid, createdBy)
		}
		if !strings.HasPrefix(prefix, secretAlpha[:12]) || len(prefix) != 12+1+16 {
			t.Fatalf("deterministic prefix %q must be the textual head plus a 16-hex digest suffix", prefix)
		}
		if got := bootstrapCount(t, ctx, db, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND action='identity.bootstrap.provisioned' AND actor_public_id=$2 AND resource_public_id=$2 AND allowed`, tenant, actorA); got != 1 {
			t.Fatalf("provisioned audit rows=%d; want 1", got)
		}
		var metadata string
		if err := db.QueryRowContext(ctx, `SELECT metadata::text FROM audit_events WHERE tenant_id=$1 AND actor_public_id=$2 AND action='identity.bootstrap.provisioned'`, tenant, actorA).Scan(&metadata); err != nil {
			t.Fatalf("provisioned audit metadata: %v", err)
		}
		// jsonb renders canonical text with a space after each colon; the
		// pinned non-secret fields are exactly action and grant count.
		if !strings.Contains(metadata, `"grant_count": 3`) || !strings.Contains(metadata, `"action": "provisioned"`) {
			t.Fatalf("provisioned audit metadata=%s; want action and grant_count", metadata)
		}
	})

	t.Run("identical restart is unchanged and audit stable", func(t *testing.T) {
		var createdBefore time.Time
		if err := db.QueryRowContext(ctx, `SELECT created_at FROM api_tokens WHERE tenant_id=$1 AND name=$2 AND subject_service_account_id=(SELECT id FROM service_accounts WHERE tenant_id=$1 AND public_id=$3) AND revoked_at IS NULL`, tenant, tokenName, actorA).Scan(&createdBefore); err != nil {
			t.Fatalf("read bootstrap token before restart: %v", err)
		}
		r := mustBootstrap(t, ctx, db, tenant, wsPub, actorA, "cortex-server-bootstrap", "cortex-server", baseGrants, tokenName, secretAlpha, "restart")
		if r.action != "unchanged" || r.version != 1 {
			t.Fatalf("identical restart action=%q version=%d; want unchanged/1", r.action, r.version)
		}
		if got := bootstrapCount(t, ctx, db, `SELECT count(*) FROM api_tokens WHERE tenant_id=$1 AND created_by=$2`, tenant, actorA); got != 1 {
			t.Fatalf("restart minted tokens=%d; want stable 1", got)
		}
		if got := bootstrapCount(t, ctx, db, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND actor_public_id=$2`, tenant, actorA); got != 1 {
			t.Fatalf("restart audit cardinality=%d; want stable 1", got)
		}
		var createdAfter time.Time
		if err := db.QueryRowContext(ctx, `SELECT created_at FROM api_tokens WHERE tenant_id=$1 AND public_id=$2`, tenant, r.tokenID).Scan(&createdAfter); err != nil {
			t.Fatalf("read bootstrap token after restart: %v", err)
		}
		if !createdBefore.Equal(createdAfter) {
			t.Fatalf("restart rewrote token creation time: before=%v after=%v", createdBefore, createdAfter)
		}
		var version int64
		if err := db.QueryRowContext(ctx, `SELECT grant_version FROM actor_subjects WHERE tenant_id=$1 AND public_id=$2`, tenant, actorA).Scan(&version); err != nil || version != 1 {
			t.Fatalf("restart grant_version=%d err=%v; want stable 1", version, err)
		}
	})

	t.Run("grant change reconciles exactly once", func(t *testing.T) {
		widened := grantsJSON(
			[2]string{"role", "owner"},
			[2]string{"workspace", wsPub},
			[2]string{"scope", "workspaces:read"},
			[2]string{"project", "*"},
		)
		r := mustBootstrap(t, ctx, db, tenant, wsPub, actorA, "cortex-server-bootstrap", "cortex-server", widened, tokenName, secretAlpha, "grant widen")
		if r.action != "reconciled" || r.version != 2 {
			t.Fatalf("grant reconcile action=%q version=%d; want reconciled/2", r.action, r.version)
		}
		if got := bootstrapCount(t, ctx, db, `SELECT count(*) FROM principal_grants WHERE tenant_id=$1 AND actor_public_id=$2`, tenant, actorA); got != 4 {
			t.Fatalf("reconciled grants=%d; want exact canonical 4", got)
		}
		var digest string
		var version int64
		if err := db.QueryRowContext(ctx, `SELECT grant_digest, grant_version FROM actor_subjects WHERE tenant_id=$1 AND public_id=$2`, tenant, actorA).Scan(&digest, &version); err != nil || digest != grantDigestFor(widened) || version != 2 {
			t.Fatalf("reconciled digest/version mismatch: %d %s", version, digest)
		}
		if got := bootstrapCount(t, ctx, db, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND actor_public_id=$2 AND action='identity.bootstrap.reconciled'`, tenant, actorA); got != 1 {
			t.Fatalf("reconciled audit rows=%d; want 1", got)
		}
		again := mustBootstrap(t, ctx, db, tenant, wsPub, actorA, "cortex-server-bootstrap", "cortex-server", widened, tokenName, secretAlpha, "restart")
		if again.action != "unchanged" || again.version != 2 {
			t.Fatalf("post-reconcile restart action=%q version=%d; want unchanged/2", again.action, again.version)
		}
		if got := bootstrapCount(t, ctx, db, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND actor_public_id=$2 AND action='identity.bootstrap.reconciled'`, tenant, actorA); got != 1 {
			t.Fatalf("reconcile repeated: audit rows=%d; want stable 1", got)
		}
	})

	t.Run("bearer rotation revokes once and verification follows the live token", func(t *testing.T) {
		var oldTokenID string
		if err := db.QueryRowContext(ctx, `SELECT public_id::text FROM api_tokens WHERE tenant_id=$1 AND name=$2 AND subject_service_account_id=(SELECT id FROM service_accounts WHERE tenant_id=$1 AND public_id=$3) AND revoked_at IS NULL`, tenant, tokenName, actorA).Scan(&oldTokenID); err != nil {
			t.Fatalf("resolve active bootstrap token: %v", err)
		}
		widened := grantsJSON(
			[2]string{"role", "owner"},
			[2]string{"workspace", wsPub},
			[2]string{"scope", "workspaces:read"},
			[2]string{"project", "*"},
		)
		r := mustBootstrap(t, ctx, db, tenant, wsPub, actorA, "cortex-server-bootstrap", "cortex-server", widened, tokenName, secretBeta, "bearer rotation")
		if r.action != "token_rotated" || r.version != 2 || r.tokenID == oldTokenID {
			t.Fatalf("rotation action=%q version=%d token=%s; want token_rotated/2/new id", r.action, r.version, r.tokenID)
		}
		var revokedAt sql.NullTime
		if err := db.QueryRowContext(ctx, `SELECT revoked_at FROM api_tokens WHERE tenant_id=$1 AND public_id=$2`, tenant, oldTokenID).Scan(&revokedAt); err != nil || !revokedAt.Valid {
			t.Fatalf("prior token not revoked: %v", err)
		}
		if got := bootstrapCount(t, ctx, db, `SELECT count(*) FROM api_tokens WHERE tenant_id=$1 AND created_by=$2 AND revoked_at IS NULL`, tenant, actorA); got != 1 {
			t.Fatalf("active tokens after rotation=%d; want exactly 1", got)
		}
		if got := bootstrapCount(t, ctx, db, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND actor_public_id=$2 AND action='identity.bootstrap.token_rotated'`, tenant, actorA); got != 1 {
			t.Fatalf("rotation audit rows=%d; want 1", got)
		}
		var rotatedFrom string
		if err := db.QueryRowContext(ctx, `SELECT metadata->>'rotated_from' FROM audit_events WHERE tenant_id=$1 AND actor_public_id=$2 AND action='identity.bootstrap.token_rotated'`, tenant, actorA).Scan(&rotatedFrom); err != nil || rotatedFrom != oldTokenID {
			t.Fatalf("rotation audit rotated_from=%q err=%v; want prior token id", rotatedFrom, err)
		}
		// The new bearer verifies into the service principal; the old one is
		// revoked and fails closed through the same verification path.
		var subject string
		if err := db.QueryRowContext(ctx, `
			SELECT subject_public_id::text FROM cortex_verify_token_principal(left($1,12), hmac(convert_to($1,'UTF8'), convert_to($2::text,'UTF8'),'sha256'), '')`, secretBeta, tenant).Scan(&subject); err != nil {
			t.Fatalf("verify rotated bearer: %v", err)
		}
		if subject != actorA {
			t.Fatalf("rotated bearer subject=%s; want %s", subject, actorA)
		}
		_, err := db.ExecContext(ctx, `
			SELECT cortex_verify_token_principal(left($1,12), hmac(convert_to($1,'UTF8'), convert_to($2::text,'UTF8'),'sha256'), '')`, secretAlpha, tenant)
		assertPgErrorCode(t, err, "28000")
		// Rotating again with the same bearer is a no-op.
		again := mustBootstrap(t, ctx, db, tenant, wsPub, actorA, "cortex-server-bootstrap", "cortex-server", widened, tokenName, secretBeta, "restart")
		if again.action != "unchanged" || again.tokenID != r.tokenID {
			t.Fatalf("post-rotation restart action=%q; want unchanged with stable token", again.action)
		}
	})

	t.Run("revoked same bearer fails closed without resurrection", func(t *testing.T) {
		if _, err := db.ExecContext(ctx, `
			UPDATE api_tokens SET revoked_at=clock_timestamp()
			 WHERE tenant_id=$1 AND name=$2 AND subject_service_account_id=(SELECT id FROM service_accounts WHERE tenant_id=$1 AND public_id=$3) AND revoked_at IS NULL`, tenant, tokenName, actorA); err != nil {
			t.Fatalf("revoke active bootstrap token: %v", err)
		}
		widened := grantsJSON(
			[2]string{"role", "owner"},
			[2]string{"workspace", wsPub},
			[2]string{"scope", "workspaces:read"},
			[2]string{"project", "*"},
		)
		_, err := callBootstrap(ctx, db, tenant, wsPub, actorA, "cortex-server-bootstrap", "cortex-server", widened, tokenName, secretBeta, "revoked bearer")
		assertPgErrorCode(t, err, "28000")
		var stillRevoked int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM api_tokens WHERE tenant_id=$1 AND created_by=$2 AND revoked_at IS NULL`, tenant, actorA).Scan(&stillRevoked); err != nil || stillRevoked != 0 {
			t.Fatalf("revoked bearer resurrected a token: active=%d err=%v", stillRevoked, err)
		}
		if got := bootstrapCount(t, ctx, db, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND actor_public_id=$2 AND action='identity.bootstrap.token_rotated'`, tenant, actorA); got != 1 {
			t.Fatalf("revoked-bearer failure wrote audit rows=%d; want stable 1", got)
		}
	})

	t.Run("different bearer recovers preserving revoked history", func(t *testing.T) {
		widened := grantsJSON(
			[2]string{"role", "owner"},
			[2]string{"workspace", wsPub},
			[2]string{"scope", "workspaces:read"},
			[2]string{"project", "*"},
		)
		r := mustBootstrap(t, ctx, db, tenant, wsPub, actorA, "cortex-server-bootstrap", "cortex-server", widened, tokenName, secretGamma, "recovery bearer")
		if r.action != "token_rotated" {
			t.Fatalf("recovery action=%q; want token_rotated", r.action)
		}
		if got := bootstrapCount(t, ctx, db, `SELECT count(*) FROM api_tokens WHERE tenant_id=$1 AND created_by=$2 AND revoked_at IS NOT NULL`, tenant, actorA); got != 2 {
			t.Fatalf("revoked history after recovery=%d; want preserved 2", got)
		}
		if got := bootstrapCount(t, ctx, db, `SELECT count(*) FROM api_tokens WHERE tenant_id=$1 AND created_by=$2 AND revoked_at IS NULL`, tenant, actorA); got != 1 {
			t.Fatalf("active tokens after recovery=%d; want 1", got)
		}
	})

	t.Run("combined grant and bearer change audits both transitions once", func(t *testing.T) {
		final := grantsJSON(
			[2]string{"role", "admin"},
			[2]string{"workspace", wsPub},
			[2]string{"scope", "workspaces:read"},
			[2]string{"project", "*"},
		)
		var auditsBefore, rotatedBefore int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND actor_public_id=$2`, tenant, actorA).Scan(&auditsBefore); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND actor_public_id=$2 AND action='identity.bootstrap.token_rotated'`, tenant, actorA).Scan(&rotatedBefore); err != nil {
			t.Fatal(err)
		}
		r := mustBootstrap(t, ctx, db, tenant, wsPub, actorA, "cortex-server-bootstrap", "cortex-server", final, tokenName, secretDelta, "combined change")
		if r.action != "token_rotated" || r.version != 3 {
			t.Fatalf("combined change action=%q version=%d; want token_rotated/3", r.action, r.version)
		}
		if got := bootstrapCount(t, ctx, db, `SELECT count(*) FROM principal_grants WHERE tenant_id=$1 AND actor_public_id=$2 AND grant_type='role' AND grant_value='admin'`, tenant, actorA); got != 1 {
			t.Fatalf("combined change did not reconcile grants to admin role")
		}
		if got := bootstrapCount(t, ctx, db, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND actor_public_id=$2 AND action='identity.bootstrap.reconciled'`, tenant, actorA); got != 2 {
			t.Fatalf("reconciled audits=%d; want 2 (one per real grant change)", got)
		}
		if got := bootstrapCount(t, ctx, db, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND actor_public_id=$2 AND action='identity.bootstrap.token_rotated'`, tenant, actorA); got != rotatedBefore+1 {
			t.Fatalf("rotated audits=%d; want %d+1", got, rotatedBefore)
		}
		if got := bootstrapCount(t, ctx, db, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND actor_public_id=$2`, tenant, actorA); got != auditsBefore+2 {
			t.Fatalf("total audits=%d; want %d+2", got, auditsBefore)
		}
	})

	t.Run("reusing a previously revoked bearer digest fails closed", func(t *testing.T) {
		// secretAlpha's digest still exists on a revoked row; rotating back
		// to it must fail on the tenant-scoped digest uniqueness instead of
		// resurrecting history, with zero partial effects.
		var activeID string
		if err := db.QueryRowContext(ctx, `SELECT public_id::text FROM api_tokens WHERE tenant_id=$1 AND created_by=$2 AND revoked_at IS NULL`, tenant, actorA).Scan(&activeID); err != nil {
			t.Fatal(err)
		}
		final := grantsJSON(
			[2]string{"role", "admin"},
			[2]string{"workspace", wsPub},
			[2]string{"scope", "workspaces:read"},
			[2]string{"project", "*"},
		)
		_, err := callBootstrap(ctx, db, tenant, wsPub, actorA, "cortex-server-bootstrap", "cortex-server", final, tokenName, secretAlpha, "rotate back")
		assertPgErrorCode(t, err, "23505")
		var active int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM api_tokens WHERE tenant_id=$1 AND created_by=$2 AND revoked_at IS NULL`, tenant, actorA).Scan(&active); err != nil || active != 1 {
			t.Fatalf("rotate-back left active tokens=%d err=%v; want 1", active, err)
		}
		var stillActive string
		if err := db.QueryRowContext(ctx, `SELECT public_id::text FROM api_tokens WHERE tenant_id=$1 AND created_by=$2 AND revoked_at IS NULL`, tenant, actorA).Scan(&stillActive); err != nil || stillActive != activeID {
			t.Fatalf("rotate-back changed the active token: %s vs %s", stillActive, activeID)
		}
	})

	t.Run("same textual prefix bearers stay unique across rotation and recovery", func(t *testing.T) {
		// All four sentinel bearers deliberately share the same textual
		// 12-character head; the collision-prone plain-head prefix would
		// have violated UNIQUE (tenant_id, token_prefix) on the very first
		// rotation. The deterministic digest-suffixed prefix keeps every
		// row — active and revoked — distinct while verification keeps
		// matching the head.
		for _, pair := range [][2]string{{secretAlpha, secretBeta}, {secretAlpha, secretGamma}, {secretAlpha, secretDelta}} {
			if pair[0][:12] != pair[1][:12] {
				t.Fatalf("sentinel bearers must share a textual head: %q vs %q", pair[0][:12], pair[1][:12])
			}
		}
		rows, err := db.QueryContext(ctx, `SELECT token_prefix FROM api_tokens WHERE tenant_id=$1 AND created_by=$2 ORDER BY id`, tenant, actorA)
		if err != nil {
			t.Fatalf("read actor tokens: %v", err)
		}
		defer func() {
			if closeErr := rows.Close(); closeErr != nil {
				t.Errorf("close token prefix rows: %v", closeErr)
			}
		}()
		var prefixes []string
		for rows.Next() {
			var prefix string
			if err := rows.Scan(&prefix); err != nil {
				t.Fatalf("scan prefix: %v", err)
			}
			prefixes = append(prefixes, prefix)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate prefixes: %v", err)
		}
		if len(prefixes) != 4 {
			t.Fatalf("actor tokens=%d; want preserved history of 4 (alpha, beta, gamma, delta)", len(prefixes))
		}
		expected := map[string]bool{
			bearerPrefixFor(secretAlpha): true,
			bearerPrefixFor(secretBeta):  true,
			bearerPrefixFor(secretGamma): true,
			bearerPrefixFor(secretDelta): true,
		}
		seen := make(map[string]bool)
		for _, prefix := range prefixes {
			if !expected[prefix] {
				t.Fatalf("unexpected stored prefix %q; want one of the deterministic derivations", prefix)
			}
			if seen[prefix] {
				t.Fatalf("duplicate stored prefix %q across same-head bearers", prefix)
			}
			seen[prefix] = true
			if !strings.HasPrefix(prefix, secretAlpha[:12]) {
				t.Fatalf("prefix %q lost the shared textual head", prefix)
			}
		}
		if len(seen) != 4 {
			t.Fatalf("distinct stored prefixes=%d; want 4", len(seen))
		}
		// Verification compatibility under the shared head: the active
		// delta bearer verifies through the plain textual head exactly as
		// the repository caller passes it, and no other bearer's digest
		// resolves under that same head.
		var subject string
		if err := db.QueryRowContext(ctx, `
			SELECT subject_public_id::text FROM cortex_verify_token_principal(left($1,12), hmac(convert_to($1,'UTF8'), convert_to($2::text,'UTF8'),'sha256'), '')`, secretDelta, tenant).Scan(&subject); err != nil {
			t.Fatalf("verify same-head active bearer: %v", err)
		}
		if subject != actorA {
			t.Fatalf("same-head verification subject=%s; want %s", subject, actorA)
		}
		_, err = db.ExecContext(ctx, `
			SELECT cortex_verify_token_principal(left($1,12), hmac(convert_to($2,'UTF8'), convert_to($3::text,'UTF8'),'sha256'), '')`, secretAlpha, secretGamma, tenant)
		assertPgErrorCode(t, err, "28000")
	})

	t.Run("existing service account with a foreign name fails closed", func(t *testing.T) {
		actor := "00000000-0000-0000-0000-0000000003d1"
		if _, err := db.ExecContext(ctx, `INSERT INTO service_accounts (tenant_id, public_id, name) VALUES ($1, $2, 'foreign-service-name')`, tenant, actor); err != nil {
			t.Fatalf("seed foreign-named service account: %v", err)
		}
		_, err := callBootstrap(ctx, db, tenant, wsPub, actor, "cortex-name-probe-subject", "cortex-server", baseGrants, tokenName, secretAlpha, "name conflict probe")
		assertPgErrorCode(t, err, "22023")
		if !strings.Contains(err.Error(), "name conflicts with the configured service") {
			t.Fatalf("error=%v; want service name conflict message", err)
		}
		// The whole call rolled back: no actor, grants, tokens, or audit for
		// the actor, and exactly the one pre-seeded service row remains.
		if got := bootstrapCount(t, ctx, db, `SELECT count(*) FROM service_accounts WHERE tenant_id=$1 AND public_id=$2`, tenant, actor); got != 1 {
			t.Fatalf("service rows=%d; want the single pre-seeded row", got)
		}
		for _, probe := range []struct {
			name  string
			query string
		}{
			{"actor_subjects", `SELECT count(*) FROM actor_subjects WHERE tenant_id=$1 AND public_id=$2`},
			{"principal_grants", `SELECT count(*) FROM principal_grants WHERE tenant_id=$1 AND actor_public_id=$2`},
			{"api_tokens", `SELECT count(*) FROM api_tokens WHERE tenant_id=$1 AND created_by=$2`},
			{"audit_events", `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND (actor_public_id=$2 OR resource_public_id=$2)`},
		} {
			if got := bootstrapCount(t, ctx, db, probe.query, tenant, actor); got != 0 {
				t.Errorf("name-conflict probe leaked %s rows: %d", probe.name, got)
			}
		}
	})

	t.Run("reserved bootstrap token name is bound to one identity", func(t *testing.T) {
		// actorA already holds the reserved name; a second bootstrap
		// identity requesting the SAME name fails closed instead of
		// adopting or sharing it, with zero partial effects.
		actor := "00000000-0000-0000-0000-0000000003d2"
		_, err := callBootstrap(ctx, db, tenant, wsPub, actor, "cortex-reserved-probe-subject", "cortex-server", baseGrants, tokenName, secretAlpha, "reserved name probe")
		assertPgErrorCode(t, err, "28000")
		if !strings.Contains(err.Error(), "reserved to another subject") {
			t.Fatalf("error=%v; want reserved-name conflict message", err)
		}
		assertBootstrapClean(t, ctx, db, tenant, actor)
	})

	t.Run("validation and identity conflicts fail closed with zero partial effects", func(t *testing.T) {
		probes := []struct {
			name    string
			actor   string
			grants  string
			wsPub   string
			secret  string
			setup   func(t *testing.T)
			code    string
			message string
		}{
			{
				name:    "short bearer",
				actor:   "00000000-0000-0000-0000-0000000003c1",
				grants:  baseGrants,
				secret:  "short",
				code:    "22023",
				message: "bootstrap arguments are invalid",
			},
			{
				name:    "missing configured workspace grant",
				actor:   "00000000-0000-0000-0000-0000000003c2",
				grants:  grantsJSON([2]string{"role", "owner"}, [2]string{"scope", "workspaces:read"}),
				secret:  secretAlpha,
				code:    "22023",
				message: "configured workspace grant",
			},
			{
				name:    "missing owner or admin role",
				actor:   "00000000-0000-0000-0000-0000000003c3",
				grants:  grantsJSON([2]string{"role", "member"}, [2]string{"workspace", wsPub}),
				secret:  secretAlpha,
				code:    "22023",
				message: "owner or admin role",
			},
			{
				name:    "duplicate grants",
				actor:   "00000000-0000-0000-0000-0000000003c4",
				grants:  `[{"type":"role","value":"owner"},{"type":"role","value":"owner"},{"type":"workspace","value":"` + wsPub + `"}]`,
				secret:  secretAlpha,
				code:    "22023",
				message: "bootstrap grants must be unique",
			},
			{
				name:    "unknown grant type",
				actor:   "00000000-0000-0000-0000-0000000003c5",
				grants:  `[{"type":"tenant","value":"*"},{"type":"role","value":"owner"},{"type":"workspace","value":"` + wsPub + `"}]`,
				secret:  secretAlpha,
				code:    "22023",
				message: "allowlisted",
			},
			{
				name:    "unknown workspace",
				actor:   "00000000-0000-0000-0000-0000000003c6",
				grants:  grantsJSON([2]string{"role", "owner"}, [2]string{"workspace", "00000000-0000-0000-0000-0000000003ff"}, [2]string{"scope", "workspaces:read"}),
				wsPub:   "00000000-0000-0000-0000-0000000003ff",
				secret:  secretAlpha,
				code:    "23503",
				message: "bootstrap workspace does not exist",
			},
			{
				name:    "subject belongs to another actor",
				actor:   "00000000-0000-0000-0000-0000000003c7",
				grants:  baseGrants,
				code:    "23505",
				message: "actor_subjects_tenant_id_subject_key",
				setup: func(t *testing.T) {
					if _, err := db.ExecContext(ctx, `INSERT INTO actor_subjects (tenant_id, subject, actor_type, public_id) VALUES ($1, 'cortex-conflict-subject', 'user', $2)`, tenant, "00000000-0000-0000-0000-0000000003c8"); err != nil {
						t.Fatalf("seed conflicting subject: %v", err)
					}
				},
				// The probe uses the taken subject to force the conflict.
				secret: secretAlpha,
			},
		}
		for _, probe := range probes {
			probe := probe
			t.Run(probe.name, func(t *testing.T) {
				if probe.setup != nil {
					probe.setup(t)
				}
				targetWS := probe.wsPub
				if targetWS == "" {
					targetWS = wsPub
				}
				subject := "cortex-server-bootstrap"
				if probe.name == "subject belongs to another actor" {
					subject = "cortex-conflict-subject"
				}
				_, err := callBootstrap(ctx, db, tenant, targetWS, probe.actor, subject, "cortex-server", probe.grants, tokenName, probe.secret, "validation probe")
				assertPgErrorCode(t, err, probe.code)
				if !strings.Contains(err.Error(), probe.message) {
					t.Fatalf("error=%v; want message containing %q", err, probe.message)
				}
				assertBootstrapClean(t, ctx, db, tenant, probe.actor)
			})
		}
		// Unknown tenant (23503) on its own fresh actor.
		_, err := callBootstrap(ctx, db, "00000000-0000-0000-0000-0000000003ee", wsPub, "00000000-0000-0000-0000-0000000003c9", "cortex-server-bootstrap", "cortex-server", baseGrants, tokenName, secretAlpha, "unknown tenant")
		assertPgErrorCode(t, err, "23503")
		// Actor-type conflict: an existing user actor with the same subject
		// and public id as the requested service actor. The seeded service
		// row carries the configured service name so the probe reaches the
		// actor-type conflict branch instead of the name-conflict branch.
		if _, err := db.ExecContext(ctx, `
			WITH s AS (
				INSERT INTO service_accounts (tenant_id, public_id, name) VALUES ($1, $2, 'cortex-server') RETURNING id
			)
			INSERT INTO actor_subjects (tenant_id, subject, actor_type, public_id) VALUES ($1, 'typed-actor-subject', 'user', $2)`, tenant, "00000000-0000-0000-0000-0000000003ca"); err != nil {
			t.Fatalf("seed typed actor: %v", err)
		}
		_, err = callBootstrap(ctx, db, tenant, wsPub, "00000000-0000-0000-0000-0000000003ca", "typed-actor-subject", "cortex-server", baseGrants, tokenName, secretAlpha, "type conflict")
		assertPgErrorCode(t, err, "22023")
		// Inactive actor: an existing service actor that was disabled. The
		// seeded service row carries the configured service name so the
		// probe reaches the inactive-actor branch instead of the
		// name-conflict branch.
		if _, err := db.ExecContext(ctx, `
			WITH s AS (
				INSERT INTO service_accounts (tenant_id, public_id, name, active) VALUES ($1, $2, 'cortex-server', false) RETURNING id
			)
			INSERT INTO actor_subjects (tenant_id, subject, actor_type, public_id, active, revoked_at) VALUES ($1, 'inactive-actor-subject', 'service_account', $2, false, clock_timestamp())`, tenant, "00000000-0000-0000-0000-0000000003cb"); err != nil {
			t.Fatalf("seed inactive actor: %v", err)
		}
		_, err = callBootstrap(ctx, db, tenant, wsPub, "00000000-0000-0000-0000-0000000003cb", "inactive-actor-subject", "cortex-server", baseGrants, tokenName, secretAlpha, "inactive actor")
		assertPgErrorCode(t, err, "28000")
		if got := bootstrapCount(t, ctx, db, `SELECT count(*) FROM principal_grants WHERE tenant_id=$1 AND actor_public_id IN ($2,$3)`, tenant, "00000000-0000-0000-0000-0000000003ca", "00000000-0000-0000-0000-0000000003cb"); got != 0 {
			t.Fatalf("conflict probes leaked grant rows=%d", got)
		}
		if got := bootstrapCount(t, ctx, db, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND actor_public_id IN ($2,$3)`, tenant, "00000000-0000-0000-0000-0000000003ca", "00000000-0000-0000-0000-0000000003cb"); got != 0 {
			t.Fatalf("conflict probes leaked audit rows=%d", got)
		}
	})

	t.Run("audit failure rolls back every provisioning effect", func(t *testing.T) {
		if _, err := db.ExecContext(ctx, `CREATE FUNCTION cortex_test_block_bootstrap_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.action LIKE 'identity.bootstrap.%' THEN
        RAISE EXCEPTION 'injected audit outage' USING ERRCODE = 'P0001';
    END IF;
    RETURN NEW;
END $$`); err != nil {
			t.Fatalf("create audit block trigger function: %v", err)
		}
		t.Cleanup(func() {
			_, _ = db.ExecContext(ctx, `DROP TRIGGER IF EXISTS cortex_test_block_bootstrap_audit ON audit_events`)
			_, _ = db.ExecContext(ctx, `DROP FUNCTION IF EXISTS cortex_test_block_bootstrap_audit()`)
		})
		if _, err := db.ExecContext(ctx, `CREATE TRIGGER cortex_test_block_bootstrap_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION cortex_test_block_bootstrap_audit()`); err != nil {
			t.Fatalf("create audit block trigger: %v", err)
		}
		// The outage probe uses its own reserved token name and a sixth
		// distinct bearer: the primary name is already bound to actorA's
		// service row (a reserved-to-another-subject denial would fire
		// before the first provisioning INSERT), and every earlier
		// sentinel digest is already stored (the token INSERT would fail
		// tenant-scoped digest uniqueness before audit is ever reached).
		_, err := callBootstrap(ctx, db, tenant, wsPub, "00000000-0000-0000-0000-0000000003cc", "rollback-subject", "cortex-server", baseGrants, "bootstrap/rollback-probe", secretZeta, "audit outage probe")
		assertPgErrorCode(t, err, "P0001")
		if _, err := db.ExecContext(ctx, `DROP TRIGGER cortex_test_block_bootstrap_audit ON audit_events`); err != nil {
			t.Fatalf("drop audit block trigger: %v", err)
		}
		assertBootstrapClean(t, ctx, db, tenant, "00000000-0000-0000-0000-0000000003cc")
	})

	t.Run("outputs and audit stay secret free", func(t *testing.T) {
		for _, secret := range []string{secretAlpha, secretBeta, secretGamma, secretDelta} {
			for _, forbidden := range []string{secret, hmacHexFor(secret)} {
				var hits int
				if err := db.QueryRowContext(ctx, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND (metadata::text LIKE '%' || $2 || '%' OR reason LIKE '%' || $2 || '%')`, tenant, forbidden).Scan(&hits); err != nil || hits != 0 {
					t.Fatalf("secret material %q leaked into audit: rows=%d err=%v", forbidden[:12]+"...", hits, err)
				}
			}
		}
		var digestHits int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM audit_events e JOIN actor_subjects a ON a.tenant_id=e.tenant_id AND a.public_id=$2 WHERE e.tenant_id=$1 AND e.metadata::text LIKE '%' || a.grant_digest || '%'`, tenant, actorA).Scan(&digestHits); err != nil || digestHits != 0 {
			t.Fatalf("grant digest leaked into audit rows=%d err=%v", digestHits, err)
		}
	})

	t.Run("concurrent identical bootstrap calls serialize to one transition", func(t *testing.T) {
		const callers = 8
		results := make([]bootstrapResult, callers)
		errs := make([]error, callers)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < callers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				results[i], errs[i] = callBootstrap(ctx, db, tenant, wsPub, actorB, "cortex-server-bootstrap-b", "cortex-server", baseGrants, tokenNameB, secretEpsilon, "concurrent caller")
			}(i)
		}
		close(start)
		wg.Wait()
		provisioned := 0
		for i := 0; i < callers; i++ {
			if errs[i] != nil {
				t.Fatalf("concurrent caller %d failed: %v", i, errs[i])
			}
			switch results[i].action {
			case "provisioned":
				provisioned++
			case "unchanged":
			default:
				t.Fatalf("concurrent caller %d action=%q; want provisioned or unchanged", i, results[i].action)
			}
			if results[i].tokenID != results[0].tokenID || results[i].version != 1 {
				t.Fatalf("concurrent caller %d diverged: token=%s version=%d", i, results[i].tokenID, results[i].version)
			}
		}
		if provisioned != 1 {
			t.Fatalf("concurrent provisioning transitions=%d; want exactly 1", provisioned)
		}
		if got := bootstrapCount(t, ctx, db, `SELECT count(*) FROM service_accounts WHERE tenant_id=$1 AND public_id=$2`, tenant, actorB); got != 1 {
			t.Fatalf("concurrent service rows=%d; want 1", got)
		}
		if got := bootstrapCount(t, ctx, db, `SELECT count(*) FROM principal_grants WHERE tenant_id=$1 AND actor_public_id=$2`, tenant, actorB); got != 3 {
			t.Fatalf("concurrent grant rows=%d; want 3", got)
		}
		if got := bootstrapCount(t, ctx, db, `SELECT count(*) FROM api_tokens WHERE tenant_id=$1 AND created_by=$2`, tenant, actorB); got != 1 {
			t.Fatalf("concurrent token rows=%d; want 1", got)
		}
		if got := bootstrapCount(t, ctx, db, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND actor_public_id=$2`, tenant, actorB); got != 1 {
			t.Fatalf("concurrent audit rows=%d; want 1", got)
		}
	})

	t.Run("EXECUTE is limited to the migration role", func(t *testing.T) {
		const sig = "public.cortex_bootstrap_service_principal(uuid,uuid,uuid,text,text,jsonb,text,text,text)"
		// Catalog ownership contract: the routine lives in public, is owned
		// by cortex_migration regardless of which privileged role applied
		// the file, and pins its search_path in proconfig.
		var schemaName, ownerName, proconfig string
		if err := db.QueryRowContext(ctx, `
			SELECT p.pronamespace::regnamespace::text, pg_get_userbyid(p.proowner), COALESCE(array_to_string(p.proconfig, '|'), '')
			  FROM pg_proc p
			 WHERE p.oid = $1::regprocedure`, sig).Scan(&schemaName, &ownerName, &proconfig); err != nil {
			t.Fatalf("pg_proc catalog row: %v", err)
		}
		if schemaName != "public" {
			t.Fatalf("bootstrap routine schema=%q; want public", schemaName)
		}
		if ownerName != "cortex_migration" {
			t.Fatalf("bootstrap routine owner=%q; want cortex_migration", ownerName)
		}
		if !strings.Contains(proconfig, "search_path=pg_catalog,") || !strings.Contains(proconfig, "public") {
			t.Fatalf("bootstrap routine proconfig=%q; want pinned search_path pg_catalog, public", proconfig)
		}
		for role, want := range map[string]bool{
			"cortex_migration": true,
			"cortex_app":       false,
			"cortex_admin":     false,
			"public":           false,
		} {
			var allowed bool
			if err := db.QueryRowContext(ctx, `SELECT has_function_privilege($1, $2, 'EXECUTE')`, role, sig).Scan(&allowed); err != nil || allowed != want {
				t.Fatalf("has_function_privilege(%s)=%v err=%v; want %v", role, allowed, err, want)
			}
		}
		// The complete EXECUTE ACL grants exactly one grantee: the
		// migration role. This covers EVERY role on the cluster — runtime
		// roles, any hypothetical cortex_bootstrap role, and membership
		// inheritors alike — so nothing else can invoke the reconciler.
		var grantees string
		if err := db.QueryRowContext(ctx, `
			SELECT COALESCE(string_agg(DISTINCT r.rolname, ',' ORDER BY r.rolname), '')
			  FROM pg_proc p
			  JOIN LATERAL aclexplode(p.proacl) a ON a.privilege_type = 'EXECUTE'
			  JOIN pg_roles r ON r.oid = a.grantee
			 WHERE p.oid = $1::regprocedure`, sig).Scan(&grantees); err != nil {
			t.Fatalf("explode EXECUTE ACL: %v", err)
		}
		if grantees != "cortex_migration" {
			t.Fatalf("EXECUTE ACL grantees=%q; want exactly cortex_migration", grantees)
		}
		// Least-privilege definer prerequisites: cortex_migration owns the
		// definer and holds exactly the actor_subjects table privileges the
		// body exercises; runtime roles keep no direct DML on identity
		// tables, and sequence usage for the identity columns stays with
		// the migration role only as already granted by the baselines.
		for _, probe := range []struct {
			role  string
			table string
			priv  string
			want  bool
		}{
			{"cortex_migration", "public.actor_subjects", "SELECT", true},
			{"cortex_migration", "public.actor_subjects", "INSERT", true},
			{"cortex_migration", "public.actor_subjects", "UPDATE", true},
			{"cortex_migration", "public.actor_subjects", "DELETE", false},
			{"cortex_migration", "public.actor_subjects", "TRUNCATE", false},
			{"cortex_app", "public.actor_subjects", "SELECT", false},
			{"cortex_app", "public.actor_subjects", "INSERT", false},
			{"cortex_app", "public.actor_subjects", "UPDATE", false},
			{"cortex_app", "public.actor_subjects", "DELETE", false},
			{"cortex_admin", "public.actor_subjects", "SELECT", false},
			{"cortex_admin", "public.actor_subjects", "INSERT", false},
			{"cortex_admin", "public.actor_subjects", "DELETE", false},
		} {
			var allowed bool
			if err := db.QueryRowContext(ctx, `SELECT has_table_privilege($1, $2, $3)`, probe.role, probe.table, probe.priv).Scan(&allowed); err != nil || allowed != probe.want {
				t.Fatalf("has_table_privilege(%s, %s, %s)=%v err=%v; want %v", probe.role, probe.table, probe.priv, allowed, err, probe.want)
			}
		}
		// The application role keeps only the pinned label-triple column
		// read on actor_subjects; the migration role's identity-column
		// sequence usage is present for the definer's inserts.
		var anyColumn bool
		if err := db.QueryRowContext(ctx, `SELECT has_any_column_privilege('cortex_app', 'public.actor_subjects', 'SELECT')`).Scan(&anyColumn); err != nil || !anyColumn {
			t.Fatalf("cortex_app any-column actor_subjects SELECT=%v err=%v; want true (label triple only)", anyColumn, err)
		}
		for _, seq := range []string{"public.actor_subjects_id_seq", "public.service_accounts_id_seq"} {
			var usage bool
			if err := db.QueryRowContext(ctx, `SELECT has_sequence_privilege('cortex_migration', $1, 'USAGE')`, seq).Scan(&usage); err != nil || !usage {
				t.Fatalf("cortex_migration sequence USAGE on %s=%v err=%v; want true", seq, usage, err)
			}
		}
		// Behavioral denials: neither runtime role may invoke the reconciler.
		// The denied statement aborts its transaction, so the role reset is
		// the rollback itself: SET ROLE is transactional and every probe
		// runs in its own transaction.
		for _, role := range []string{"cortex_app", "cortex_admin"} {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatalf("begin %s tx: %v", role, err)
			}
			if _, err := tx.ExecContext(ctx, `SET ROLE `+role); err != nil {
				t.Fatalf("set role %s: %v", role, err)
			}
			_, err = tx.ExecContext(ctx, `SELECT public.cortex_bootstrap_service_principal($1,$2,$3,$4,$5,$6::jsonb,$7,$8,$9)`,
				tenant, wsPub, actorA, "cortex-server-bootstrap", "cortex-server", baseGrants, tokenName, secretAlpha, "denied probe")
			assertPgErrorCode(t, err, "42501")
			if err := tx.Rollback(); err != nil {
				t.Fatalf("rollback %s denial tx: %v", role, err)
			}
		}
		// The migration role itself can reconcile (unchanged restart).
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.ExecContext(ctx, `SET ROLE cortex_migration`); err != nil {
			t.Fatalf("set role cortex_migration: %v", err)
		}
		var action string
		if err := tx.QueryRowContext(ctx, `SELECT bootstrap_action FROM public.cortex_bootstrap_service_principal($1,$2,$3,$4,$5,$6::jsonb,$7,$8,$9)`,
			tenant, wsPub, actorA, "cortex-server-bootstrap", "cortex-server", grantsJSON([2]string{"role", "admin"}, [2]string{"workspace", wsPub}, [2]string{"scope", "workspaces:read"}, [2]string{"project", "*"}), tokenName, secretDelta, "migration role probe").Scan(&action); err != nil {
			t.Fatalf("cortex_migration reconcile: %v", err)
		}
		if _, err := tx.ExecContext(ctx, `RESET ROLE`); err != nil {
			t.Fatal(err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		if action != "unchanged" {
			t.Fatalf("cortex_migration reconcile action=%q; want unchanged (probe rolled back)", action)
		}
	})
}

// preflightLedgerSnapshot digests every public table and every migration
// ledger row of the target database. It works with OR WITHOUT the ledger
// table, so it can prove zero mutation from a completely empty database.
func preflightLedgerSnapshot(t *testing.T, ctx context.Context, db *sql.DB) string {
	t.Helper()
	var b strings.Builder
	tables, err := db.QueryContext(ctx, `
		SELECT tablename FROM pg_tables WHERE schemaname = 'public' ORDER BY tablename`)
	if err != nil {
		t.Fatalf("snapshot tables: %v", err)
	}
	for tables.Next() {
		var name string
		if err := tables.Scan(&name); err != nil {
			t.Fatalf("scan table snapshot: %v", err)
		}
		fmt.Fprintf(&b, "table|%s\n", name)
	}
	if err := tables.Close(); err != nil {
		t.Fatalf("close table snapshot rows: %v", err)
	}
	if err := tables.Err(); err != nil {
		t.Fatalf("iterate table snapshot: %v", err)
	}
	var ledger sql.NullString
	if err := db.QueryRowContext(ctx,
		`SELECT to_regclass('public.cortex_server_migrations')::text`).Scan(&ledger); err != nil {
		t.Fatalf("snapshot ledger presence: %v", err)
	}
	if !ledger.Valid {
		b.WriteString("ledger|absent\n")
		return b.String()
	}
	rows, err := db.QueryContext(ctx,
		`SELECT version, name, checksum FROM cortex_server_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("snapshot ledger rows: %v", err)
	}
	for rows.Next() {
		var version int
		var name, checksum string
		if err := rows.Scan(&version, &name, &checksum); err != nil {
			t.Fatalf("scan ledger snapshot: %v", err)
		}
		fmt.Fprintf(&b, "ledger|%d|%s|%s\n", version, name, checksum)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close ledger snapshot rows: %v", err)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate ledger snapshot: %v", err)
	}
	return b.String()
}

// TestPostgresMigrationPreflightAndVerifyApplied106 drives the read-only
// rollout preflight (Preflight) and the post-apply check (VerifyApplied) for
// migration 106 against a real, disposable PostgreSQL 16 database across the
// complete ledger state matrix (IDP-T05):
//
//   - empty database (no ledger table)            → unledgered, zero mutation
//   - ledger 100-105 without the 106 row          → unledgered, zero mutation
//   - 106 applied with the current checksum       → already-applied stop,
//     VerifyApplied passes, zero mutation
//   - 106 row carrying a prior prerelease checksum→ tamper-class stop,
//     VerifyApplied fails, zero mutation
//   - ledger row 107 (newer runtime)              → future-version stop,
//     zero mutation
//
// The UPDATE/INSERT statements below deliberately fabricate the stop states
// on THIS test's private isolated database only (dropped at cleanup); they
// never touch any operator database or shared fixture. (The fabricated
// newer-runtime row uses version 199 because 107 became a real registered
// migration of this runtime's line; 199 stays beyond the head.)
func TestPostgresMigrationPreflightAndVerifyApplied106(t *testing.T) {
	dsn := os.Getenv("CORTEX_TEST_POSTGRES_MIGRATION_DSN")
	if dsn == "" {
		t.Fatal("CORTEX_TEST_POSTGRES_MIGRATION_DSN is required for postgres_integration tests")
	}
	ctx := context.Background()

	freshDSN, cleanupFresh, err := isolatedPostgresDatabase(dsn, "migrationpf")
	if err != nil {
		t.Fatalf("create isolated preflight database: %v", err)
	}
	defer cleanupFresh()
	db, err := sql.Open("pgx", freshDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	migrations := mustPostgresMigrations(t)
	var subject *PostgresServerMigration
	for _, migration := range migrations {
		if migration.Version() == 106 {
			subject = migration
			break
		}
	}
	if subject == nil {
		t.Fatal("migration 106 not registered")
	}

	t.Run("empty database: unledgered without a ledger table", func(t *testing.T) {
		before := preflightLedgerSnapshot(t, ctx, db)

		state, err := subject.Preflight(ctx, db)
		if err != nil {
			t.Fatalf("preflight on empty DB err=%v; want nil (expected unledgered state)", err)
		}
		if state.LedgerTable || state.Ledgered || state.FutureLedgerVersion != 0 {
			t.Fatalf("preflight state on empty DB = %+v; want no ledger, no row, no future", state)
		}
		if state.ExpectedChecksum != subject.Checksum() {
			t.Errorf("preflight expected checksum=%q; want embedded %q", state.ExpectedChecksum, subject.Checksum())
		}
		// The post-apply check cannot pass on a database with no ledger.
		if err := subject.VerifyApplied(ctx, db); err == nil {
			t.Error("VerifyApplied succeeded on a database without a ledger table")
		}

		if after := preflightLedgerSnapshot(t, ctx, db); after != before {
			t.Fatalf("preflight/verify mutated an empty database:\nbefore:\n%s\nafter:\n%s", before, after)
		}
	})

	t.Run("ledger 100-105 without 106: target unledgered", func(t *testing.T) {
		for _, migration := range migrations[:6] {
			if err := migration.Apply(ctx, db); err != nil {
				t.Fatalf("apply historical migration %d: %v", migration.Version(), err)
			}
		}
		before := preflightLedgerSnapshot(t, ctx, db)

		state, err := subject.Preflight(ctx, db)
		if err != nil {
			t.Fatalf("preflight with ledger-but-no-106 err=%v; want nil (expected unledgered state)", err)
		}
		if !state.LedgerTable || state.Ledgered || state.FutureLedgerVersion != 0 {
			t.Fatalf("preflight state = %+v; want ledger present, 106 unledgered, no future", state)
		}
		if err := subject.VerifyApplied(ctx, db); err == nil {
			t.Error("VerifyApplied succeeded with no 106 ledger row; want not-applied error")
		}

		if after := preflightLedgerSnapshot(t, ctx, db); after != before {
			t.Fatalf("preflight/verify mutated the ledger:\nbefore:\n%s\nafter:\n%s", before, after)
		}
	})

	t.Run("applied 106: already-applied stop and exact post-apply check", func(t *testing.T) {
		if err := subject.Apply(ctx, db); err != nil {
			t.Fatalf("apply 106: %v", err)
		}
		before := preflightLedgerSnapshot(t, ctx, db)

		state, err := subject.Preflight(ctx, db)
		if err == nil || !errors.Is(err, ErrPreflightStop) {
			t.Fatalf("preflight on applied 106 err=%v; want errors.Is ErrPreflightStop", err)
		}
		if errors.Is(err, ErrSchemaTampered) {
			t.Errorf("already-applied stop must not be tamper-class: %v", err)
		}
		if !state.Ledgered || state.RecordedChecksum != subject.Checksum() {
			t.Fatalf("preflight state = %+v; want 106 ledgered with the embedded checksum", state)
		}
		if err := subject.VerifyApplied(ctx, db); err != nil {
			t.Fatalf("post-apply check on freshly applied 106 err=%v; want nil", err)
		}

		if after := preflightLedgerSnapshot(t, ctx, db); after != before {
			t.Fatalf("preflight/verify mutated the applied database:\nbefore:\n%s\nafter:\n%s", before, after)
		}
	})

	t.Run("prior prerelease checksum: tamper-class stop, VerifyApplied fails", func(t *testing.T) {
		// Fixture-only fabrication on this disposable database: simulate a
		// pre-release build having ledgered an older 106 checksum.
		if _, err := db.ExecContext(ctx,
			`UPDATE cortex_server_migrations SET checksum = 'old-prerelease-checksum' WHERE version = 106`); err != nil {
			t.Fatalf("fabricate prior 106 checksum: %v", err)
		}
		before := preflightLedgerSnapshot(t, ctx, db)

		state, err := subject.Preflight(ctx, db)
		if err == nil || !errors.Is(err, ErrPreflightStop) || !errors.Is(err, ErrSchemaTampered) {
			t.Fatalf("preflight on prior checksum err=%v; want ErrPreflightStop AND ErrSchemaTampered", err)
		}
		if !state.Ledgered || state.RecordedChecksum != "old-prerelease-checksum" {
			t.Fatalf("preflight state = %+v; want the prior checksum reported verbatim", state)
		}
		if err := subject.VerifyApplied(ctx, db); err == nil {
			t.Error("VerifyApplied accepted a drifted 106 checksum; want mismatch error")
		}

		if after := preflightLedgerSnapshot(t, ctx, db); after != before {
			t.Fatalf("preflight/verify mutated the tampered ledger:\nbefore:\n%s\nafter:\n%s", before, after)
		}
	})

	t.Run("future 199 ledger row: newer-runtime stop", func(t *testing.T) {
		// Fixture-only fabrication on this disposable database: simulate a
		// newer runtime having ledgered a migration beyond this runtime's
		// head (107), so the preflight must stop as a future version.
		if _, err := db.ExecContext(ctx,
			`INSERT INTO cortex_server_migrations (version, name, checksum) VALUES (199, 'future_runtime', 'future')`); err != nil {
			t.Fatalf("fabricate future 199 row: %v", err)
		}
		before := preflightLedgerSnapshot(t, ctx, db)

		state, err := subject.Preflight(ctx, db)
		if err == nil || !errors.Is(err, ErrPreflightStop) || !errors.Is(err, ErrFutureMigration) {
			t.Fatalf("preflight with a 199 row err=%v; want ErrPreflightStop AND ErrFutureMigration", err)
		}
		if state.FutureLedgerVersion != 199 {
			t.Fatalf("preflight FutureLedgerVersion = %d; want 199", state.FutureLedgerVersion)
		}

		if after := preflightLedgerSnapshot(t, ctx, db); after != before {
			t.Fatalf("preflight mutated the future ledger:\nbefore:\n%s\nafter:\n%s", before, after)
		}
	})
}

// assertNoPartial107 proves a failed 107 application left the database at
// head 106: no ledger row, no added column on prompts or edges, and the
// tenant-wide client_id indexes still in place.
func assertNoPartial107(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var ledgered bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cortex_server_migrations WHERE version=107)`).Scan(&ledgered); err != nil {
		t.Fatal(err)
	}
	if ledgered {
		t.Fatal("aborted 107 leaked a ledger row")
	}
	for _, table := range []string{"prompts", "edges"} {
		var columns int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 AND column_name='workspace_id'`, table).Scan(&columns); err != nil {
			t.Fatal(err)
		}
		if columns != 0 {
			t.Fatalf("aborted 107 leaked %s.workspace_id", table)
		}
	}
	for _, index := range []string{"observations_client_id_uq", "prompts_client_id_uq", "edges_client_id_uq"} {
		var present int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname='public' AND indexname=$1`, index).Scan(&present); err != nil {
			t.Fatal(err)
		}
		if present != 1 {
			t.Fatalf("aborted 107 lost the tenant-wide index %s", index)
		}
	}
}

// TestPostgresMigration107WorkspaceSync exercises migration 107 against real
// PostgreSQL 16: fresh apply of the full line with the workspace-hardened
// sync schema, an upgrade with existing prompt/edge data backfilled through
// the durable chains, sibling-workspace client-id coexistence for
// observations, prompts, and edges with same-workspace duplicates still
// failing closed, legacy DML compatibility through the binding triggers,
// cross-workspace edge endpoint rejection, sync feed workspace agreement,
// the fail-closed abort paths (orphan prompt, cross-workspace edge, drifted
// duplicate) with zero partial state, the read-only ledger preflight matrix,
// idempotent reapply, and the forward-only Down policy with zero mutation
// (SEC-03 schema closure oracle).
func TestPostgresMigration107WorkspaceSync(t *testing.T) {
	dsn := os.Getenv("CORTEX_TEST_POSTGRES_MIGRATION_DSN")
	if dsn == "" {
		t.Fatal("CORTEX_TEST_POSTGRES_MIGRATION_DSN is required for postgres_integration tests")
	}
	ctx := context.Background()

	freshDSN, cleanupFresh, err := isolatedPostgresDatabase(dsn, "migration107")
	if err != nil {
		t.Fatalf("create isolated 107 database: %v", err)
	}
	defer cleanupFresh()
	db, err := sql.Open("pgx", freshDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	migrations := mustPostgresMigrations(t)
	var subject *PostgresServerMigration
	for _, migration := range migrations {
		if migration.Version() == 107 {
			subject = migration
			break
		}
	}
	if subject == nil {
		t.Fatal("migration 107 not registered")
	}

	// Read-only ledger preflight before any apply: the expected rollout
	// state of 107 on a fresh database is unledgered.
	preflightState, err := subject.Preflight(ctx, db)
	if err != nil {
		t.Fatalf("preflight on empty DB err=%v; want nil (expected unledgered state)", err)
	}
	if preflightState.LedgerTable || preflightState.Ledgered || preflightState.FutureLedgerVersion != 0 {
		t.Fatalf("preflight state on empty DB = %+v; want no ledger, no row, no future", preflightState)
	}

	if err := ApplyPostgresServerMigrations(ctx, db); err != nil {
		t.Fatalf("fresh apply 100-107: %v", err)
	}
	var ledgerCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM cortex_server_migrations WHERE version BETWEEN 100 AND 107`).Scan(&ledgerCount); err != nil || ledgerCount != 8 {
		t.Fatalf("ledger count=%d err=%v; want 8", ledgerCount, err)
	}
	var ledgerChecksum string
	if err := db.QueryRowContext(ctx, `SELECT checksum FROM cortex_server_migrations WHERE version=107`).Scan(&ledgerChecksum); err != nil || ledgerChecksum != subject.Checksum() {
		t.Fatalf("ledger 107 checksum=%q err=%v; want %q", ledgerChecksum, err, subject.Checksum())
	}
	if err := subject.VerifyApplied(ctx, db); err != nil {
		t.Fatalf("post-apply check for 107: %v", err)
	}
	// Reapply is idempotent.
	if err := ApplyPostgresServerMigrations(ctx, db); err != nil {
		t.Fatalf("reapply 100-107: %v", err)
	}

	t.Run("schema is workspace hardened", func(t *testing.T) {
		for _, table := range []string{"prompts", "edges"} {
			var nullable string
			if err := db.QueryRowContext(ctx, `SELECT is_nullable FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 AND column_name='workspace_id'`, table).Scan(&nullable); err != nil {
				t.Fatalf("%s.workspace_id missing: %v", table, err)
			}
			if nullable != "NO" {
				t.Errorf("%s.workspace_id is_nullable=%s; want NO", table, nullable)
			}
		}
		// The client_id uniqueness oracle is catalog-based: key columns
		// from pg_index.indkey, the partial predicate from pg_get_expr,
		// and indisunique for the swap.
		for _, table := range []string{"observations", "prompts", "edges"} {
			index := table + "_client_id_uq"
			var keyColumns string
			var unique bool
			var predicate sql.NullString
			if err := db.QueryRowContext(ctx, `
				SELECT COALESCE(string_agg(a.attname, ',' ORDER BY k.ord), ''), i.indisunique
				  FROM pg_index i
				  JOIN pg_class ci ON ci.oid = i.indexrelid
				  JOIN unnest(i.indkey) WITH ORDINALITY AS k(attnum, ord) ON true
				  JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
				 WHERE i.indrelid = $1::regclass AND ci.relname = $2
				 GROUP BY i.indisunique`, "public."+table, index).Scan(&keyColumns, &unique); err != nil {
				t.Fatalf("%s missing from the catalog: %v", index, err)
			}
			if keyColumns != "tenant_id,workspace_id,client_id" {
				t.Fatalf("%s key columns=%q; want tenant_id,workspace_id,client_id", index, keyColumns)
			}
			if !unique {
				t.Fatalf("%s is not unique", index)
			}
			if err := db.QueryRowContext(ctx, `
				SELECT pg_get_expr(i.indpred, i.indrelid)
				  FROM pg_index i
				  JOIN pg_class ci ON ci.oid = i.indexrelid
				 WHERE i.indrelid = $1::regclass AND ci.relname = $2`, "public."+table, index).Scan(&predicate); err != nil {
				t.Fatalf("%s predicate probe: %v", index, err)
			}
			if !predicate.Valid || !equalStringSets(indexPredicateConjuncts(predicate.String), []string{"client_id IS NOT NULL"}) {
				t.Fatalf("%s predicate=%q; want the partial client_id IS NOT NULL predicate", index, predicate.String)
			}
		}
		// The temporary swap names are gone.
		for _, stale := range []string{"observations_client_id_ws_uq", "prompts_client_id_ws_uq", "edges_client_id_ws_uq"} {
			var present int
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname='public' AND indexname=$1`, stale).Scan(&present); err != nil || present != 0 {
				t.Fatalf("temporary index %s still present count=%d err=%v", stale, present, err)
			}
		}
		// Composite tenant/workspace foreign keys are catalogued.
		for _, table := range []string{"prompts", "edges"} {
			var def string
			if err := db.QueryRowContext(ctx, `
				SELECT pg_get_constraintdef(c.oid)
				  FROM pg_constraint c
				  JOIN pg_class t ON t.oid = c.conrelid
				  JOIN pg_namespace n ON n.oid = t.relnamespace
				 WHERE c.contype = 'f' AND n.nspname = 'public'
				   AND t.relname = $1 AND c.conname = $2`, table, table+"_tenant_workspace_fkey").Scan(&def); err != nil {
				t.Fatalf("%s composite workspace foreign key missing: %v", table, err)
			}
			if want := "FOREIGN KEY (tenant_id, workspace_id) REFERENCES workspaces(tenant_id, id)"; def != want {
				t.Fatalf("%s tenant/workspace foreign key def=%q; want %q", table, def, want)
			}
		}
		// Binding triggers exist, RLS stays enabled and forced, and the
		// application role keeps its baseline table privileges.
		for _, trigger := range []string{"prompts_bind_workspace", "edges_bind_workspace"} {
			var count int
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.triggers WHERE event_object_table = split_part($1, '_bind_workspace', 1) AND trigger_name=$1`, trigger).Scan(&count); err != nil || count == 0 {
				t.Fatalf("binding trigger %s missing count=%d err=%v", trigger, count, err)
			}
		}
		for _, table := range []string{"sessions", "observations", "prompts", "edges"} {
			var rls, forced bool
			if err := db.QueryRowContext(ctx, `
				SELECT c.relrowsecurity, c.relforcerowsecurity
				  FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
				 WHERE n.nspname='public' AND c.relname=$1`, table).Scan(&rls, &forced); err != nil {
				t.Fatalf("rls metadata for %s: %v", table, err)
			}
			if !rls || !forced {
				t.Errorf("%s rls=%v forced=%v; want true/true", table, rls, forced)
			}
			var insertPriv bool
			if err := db.QueryRowContext(ctx, `SELECT has_table_privilege('cortex_app', 'public.`+table+`', 'INSERT')`).Scan(&insertPriv); err != nil || !insertPriv {
				t.Errorf("cortex_app INSERT on %s=%v err=%v; want true", table, insertPriv, err)
			}
		}
		for _, index := range []string{"observations_tenant_workspace_idx", "prompts_tenant_workspace_idx", "edges_tenant_workspace_idx"} {
			var present int
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname='public' AND indexname=$1`, index).Scan(&present); err != nil || present != 1 {
				t.Fatalf("feed index %s missing count=%d err=%v", index, present, err)
			}
		}
	})

	tenant := "00000000-0000-0000-0000-000000000107"
	var ws1, ws2, session1, session2 int64
	if err := db.QueryRowContext(ctx, `
		WITH organization AS (
			INSERT INTO organizations(tenant_id,name) VALUES($1,'107 fixture') RETURNING id
		), w1 AS (
			INSERT INTO workspaces(tenant_id,organization_id,name)
			SELECT $1,id,'workspace one' FROM organization RETURNING id
		)
		INSERT INTO workspaces(tenant_id,organization_id,name)
		SELECT $1,id,'workspace two' FROM organization RETURNING id`, tenant).Scan(&ws2); err != nil {
		t.Fatalf("seed workspaces: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM workspaces WHERE tenant_id=$1 AND name='workspace one'`, tenant).Scan(&ws1); err != nil {
		t.Fatalf("resolve workspace one: %v", err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO sessions(tenant_id,workspace_id) VALUES($1,$2) RETURNING id`, tenant, ws1).Scan(&session1); err != nil {
		t.Fatalf("seed session one: %v", err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO sessions(tenant_id,workspace_id) VALUES($1,$2) RETURNING id`, tenant, ws2).Scan(&session2); err != nil {
		t.Fatalf("seed session two: %v", err)
	}

	t.Run("sibling client ids coexist per workspace", func(t *testing.T) {
		// Identical observation client IDs in two workspaces of one tenant.
		for _, session := range []int64{session1, session2} {
			if _, err := db.ExecContext(ctx, `
				INSERT INTO observations(tenant_id,session_id,client_id,type,title,content)
				VALUES($1,$2,'shared-client','manual','sibling','coexists')`, tenant, session); err != nil {
				t.Fatalf("sibling observation insert (session %d): %v", session, err)
			}
		}
		// Identical prompt client IDs in the two workspaces.
		for _, session := range []int64{session1, session2} {
			if _, err := db.ExecContext(ctx, `
				INSERT INTO prompts(tenant_id,session_id,client_id,content)
				VALUES($1,$2,'shared-client','sibling prompt')`, tenant, session); err != nil {
				t.Fatalf("sibling prompt insert (session %d): %v", session, err)
			}
		}
		// Identical edge client IDs in the two workspaces, each edge
		// spanning observations of its own workspace only.
		var obsA1, obsA2, obsB1, obsB2 int64
		for _, row := range []struct {
			session int64
			target  *int64
			title   string
		}{{session1, &obsA1, "edge a1"}, {session1, &obsA2, "edge a2"}, {session2, &obsB1, "edge b1"}, {session2, &obsB2, "edge b2"}} {
			if err := db.QueryRowContext(ctx, `
				INSERT INTO observations(tenant_id,session_id,type,title,content)
				VALUES($1,$2,'manual',$3,'edge endpoint') RETURNING id`, tenant, row.session, row.title).Scan(row.target); err != nil {
				t.Fatalf("seed %s: %v", row.title, err)
			}
		}
		for _, edge := range []struct{ from, to int64 }{{obsA1, obsA2}, {obsB1, obsB2}} {
			if _, err := db.ExecContext(ctx, `
				INSERT INTO edges(tenant_id,from_observation_id,to_observation_id,relation_type,client_id)
				VALUES($1,$2,$3,'relates','shared-client')`, tenant, edge.from, edge.to); err != nil {
				t.Fatalf("sibling edge insert: %v", err)
			}
		}
		// Duplicates INSIDE one workspace still fail closed on the renamed
		// workspace-scoped indexes.
		if _, err := db.ExecContext(ctx, `
			INSERT INTO observations(tenant_id,session_id,client_id,type,title,content)
			VALUES($1,$2,'shared-client','manual','duplicate','rejected')`, tenant, session1); err == nil || !strings.Contains(err.Error(), "observations_client_id_uq") {
			t.Fatalf("same-workspace duplicate observation err=%v; want unique violation on observations_client_id_uq", err)
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO prompts(tenant_id,session_id,client_id,content)
			VALUES($1,$2,'shared-client','duplicate')`, tenant, session1); err == nil || !strings.Contains(err.Error(), "prompts_client_id_uq") {
			t.Fatalf("same-workspace duplicate prompt err=%v; want unique violation on prompts_client_id_uq", err)
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO edges(tenant_id,from_observation_id,to_observation_id,relation_type,client_id)
			VALUES($1,$2,$3,'relates','shared-client')`, tenant, obsA1, obsA2); err == nil || !strings.Contains(err.Error(), "edges_client_id_uq") {
			t.Fatalf("same-workspace duplicate edge err=%v; want unique violation on edges_client_id_uq", err)
		}
		// The sync feed recorded both sibling observations under their own
		// workspaces with the shared client id.
		var feedWorkspaces int
		if err := db.QueryRowContext(ctx, `
			SELECT count(DISTINCT workspace_id) FROM sync_changes
			 WHERE tenant_id=$1 AND entity_type='observations' AND sync_id='shared-client'`, tenant).Scan(&feedWorkspaces); err != nil || feedWorkspaces != 2 {
			t.Fatalf("sync feed workspaces=%d err=%v; want 2 (each sibling under its own workspace)", feedWorkspaces, err)
		}
	})

	t.Run("legacy prompt DML derives the workspace from the session", func(t *testing.T) {
		var derived int64
		if err := db.QueryRowContext(ctx, `
			INSERT INTO prompts(tenant_id,session_id,content) VALUES($1,$2,'legacy prompt') RETURNING workspace_id`, tenant, session1).Scan(&derived); err != nil {
			t.Fatalf("legacy prompt insert: %v", err)
		}
		if derived != ws1 {
			t.Fatalf("legacy prompt derived workspace=%d; want %d", derived, ws1)
		}
		var explicit int64
		if err := db.QueryRowContext(ctx, `
			INSERT INTO prompts(tenant_id,session_id,workspace_id,content) VALUES($1,$2,$3,'explicit prompt') RETURNING workspace_id`, tenant, session2, ws2).Scan(&explicit); err != nil || explicit != ws2 {
			t.Fatalf("explicit prompt insert ws=%d err=%v; want %d", explicit, err, ws2)
		}
		_, err := db.ExecContext(ctx, `
			INSERT INTO prompts(tenant_id,session_id,workspace_id,content) VALUES($1,$2,$3,'mismatch')`, tenant, session1, ws2)
		assertPgErrorCode(t, err, "23514")
		if !strings.Contains(err.Error(), "conflicts with session workspace") {
			t.Fatalf("prompt mismatch err=%v; want session conflict", err)
		}
	})

	t.Run("edge endpoints must share the workspace", func(t *testing.T) {
		// The sibling subtest seeded observations in both workspaces
		// ('edge a*' in ws1, 'edge b*' in ws2); an edge from ws1 to ws2 is
		// rejected by the binding trigger before any row is written.
		var a1, b2, derived int64
		if err := db.QueryRowContext(ctx, `SELECT id FROM observations WHERE tenant_id=$1 AND title='edge a1'`, tenant).Scan(&a1); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(ctx, `SELECT id FROM observations WHERE tenant_id=$1 AND title='edge b2'`, tenant).Scan(&b2); err != nil {
			t.Fatal(err)
		}
		_, err := db.ExecContext(ctx, `
			INSERT INTO edges(tenant_id,from_observation_id,to_observation_id,relation_type)
			VALUES($1,$2,$3,'relates')`, tenant, a1, b2)
		assertPgErrorCode(t, err, "23514")
		if !strings.Contains(err.Error(), "edge endpoints must share one workspace") {
			t.Fatalf("cross-workspace edge err=%v; want endpoint workspace conflict", err)
		}
		// Legacy edge DML derives the workspace from the from-observation.
		var a2 int64
		if err := db.QueryRowContext(ctx, `SELECT id FROM observations WHERE tenant_id=$1 AND title='edge a2'`, tenant).Scan(&a2); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(ctx, `
			INSERT INTO edges(tenant_id,from_observation_id,to_observation_id,relation_type)
			VALUES($1,$2,$3,'relates') RETURNING workspace_id`, tenant, a1, a2).Scan(&derived); err != nil {
			t.Fatalf("legacy edge insert: %v", err)
		}
		if derived != ws1 {
			t.Fatalf("legacy edge derived workspace=%d; want %d", derived, ws1)
		}
	})

	t.Run("upgrade with existing prompts and edges backfills the workspace", func(t *testing.T) {
		upgradeDSN, cleanupUpgrade, err := isolatedPostgresDatabase(dsn, "migration107up")
		if err != nil {
			t.Fatalf("create upgrade database: %v", err)
		}
		defer cleanupUpgrade()
		upgradeDB, err := sql.Open("pgx", upgradeDSN)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if closeErr := upgradeDB.Close(); closeErr != nil {
				t.Errorf("close upgrade database: %v", closeErr)
			}
		}()
		for _, migration := range migrations[:7] {
			if err := migration.Apply(ctx, upgradeDB); err != nil {
				t.Fatalf("apply historical migration %d: %v", migration.Version(), err)
			}
		}
		// 106-era DML: prompts and edges carry no workspace column yet.
		var upSession1, upSession2 int64
		if err := upgradeDB.QueryRowContext(ctx, `
			WITH organization AS (
				INSERT INTO organizations(tenant_id,name) VALUES($1,'107 upgrade') RETURNING id
			), w1 AS (
				INSERT INTO workspaces(tenant_id,organization_id,name)
				SELECT $1,id,'upgrade one' FROM organization RETURNING id
			)
			INSERT INTO sessions(tenant_id,workspace_id) SELECT $1,id FROM w1 RETURNING id`, tenant).Scan(&upSession1); err != nil {
			t.Fatalf("seed upgrade session one: %v", err)
		}
		if err := upgradeDB.QueryRowContext(ctx, `
			WITH w2 AS (
				INSERT INTO workspaces(tenant_id,organization_id,name)
				SELECT $1,id,'upgrade two' FROM organizations WHERE tenant_id=$1 RETURNING id
			)
			INSERT INTO sessions(tenant_id,workspace_id) SELECT $1,id FROM w2 RETURNING id`, tenant).Scan(&upSession2); err != nil {
			t.Fatalf("seed upgrade session two: %v", err)
		}
		var upObsA1, upObsA2, upObsB int64
		for _, row := range []struct {
			session int64
			target  *int64
		}{{upSession1, &upObsA1}, {upSession1, &upObsA2}, {upSession2, &upObsB}} {
			if err := upgradeDB.QueryRowContext(ctx, `
				INSERT INTO observations(tenant_id,session_id,type,title,content)
				VALUES($1,$2,'manual','upgrade obs','kept') RETURNING id`, tenant, row.session).Scan(row.target); err != nil {
				t.Fatalf("seed upgrade observation: %v", err)
			}
		}
		if _, err := upgradeDB.ExecContext(ctx, `INSERT INTO prompts(tenant_id,session_id,content) VALUES($1,$2,'upgrade prompt one')`, tenant, upSession1); err != nil {
			t.Fatalf("seed upgrade prompt one: %v", err)
		}
		if _, err := upgradeDB.ExecContext(ctx, `INSERT INTO prompts(tenant_id,session_id,content) VALUES($1,$2,'upgrade prompt two')`, tenant, upSession2); err != nil {
			t.Fatalf("seed upgrade prompt two: %v", err)
		}
		if _, err := upgradeDB.ExecContext(ctx, `INSERT INTO edges(tenant_id,from_observation_id,to_observation_id,relation_type) VALUES($1,$2,$3,'relates')`, tenant, upObsA1, upObsA2); err != nil {
			t.Fatalf("seed upgrade edge: %v", err)
		}
		if err := subject.Apply(ctx, upgradeDB); err != nil {
			t.Fatalf("upgrade to 107: %v", err)
		}
		if err := subject.VerifyApplied(ctx, upgradeDB); err != nil {
			t.Fatalf("post-apply check on upgraded 107: %v", err)
		}
		var prompt1WS, prompt2WS, edgeWS int64
		if err := upgradeDB.QueryRowContext(ctx, `
			SELECT (SELECT p.workspace_id FROM prompts p JOIN sessions s ON s.tenant_id=p.tenant_id AND s.id=p.session_id WHERE p.tenant_id=$1 AND p.content='upgrade prompt one'),
			       (SELECT p.workspace_id FROM prompts p JOIN sessions s ON s.tenant_id=p.tenant_id AND s.id=p.session_id WHERE p.tenant_id=$1 AND p.content='upgrade prompt two'),
			       (SELECT e.workspace_id FROM edges e WHERE e.tenant_id=$1)`, tenant).Scan(&prompt1WS, &prompt2WS, &edgeWS); err != nil {
			t.Fatalf("verify backfill: %v", err)
		}
		var wsOne, wsTwo int64
		if err := upgradeDB.QueryRowContext(ctx, `SELECT workspace_id FROM sessions WHERE tenant_id=$1 AND id=$2`, tenant, upSession1).Scan(&wsOne); err != nil {
			t.Fatal(err)
		}
		if err := upgradeDB.QueryRowContext(ctx, `SELECT workspace_id FROM sessions WHERE tenant_id=$1 AND id=$2`, tenant, upSession2).Scan(&wsTwo); err != nil {
			t.Fatal(err)
		}
		if prompt1WS != wsOne || prompt2WS != wsTwo || edgeWS != wsOne {
			t.Fatalf("backfill workspaces prompt1=%d prompt2=%d edge=%d; want %d/%d/%d", prompt1WS, prompt2WS, edgeWS, wsOne, wsTwo, wsOne)
		}
		var preserved int
		if err := upgradeDB.QueryRowContext(ctx, `SELECT count(*) FROM observations WHERE tenant_id=$1 AND content='kept'`, tenant).Scan(&preserved); err != nil || preserved != 3 {
			t.Fatalf("upgrade preserved observations=%d err=%v; want 3", preserved, err)
		}
	})

	t.Run("orphan prompt aborts 107 without partial state", func(t *testing.T) {
		abortDSN, cleanupAbort, err := isolatedPostgresDatabase(dsn, "migration107orphan")
		if err != nil {
			t.Fatalf("create orphan-abort database: %v", err)
		}
		defer cleanupAbort()
		abortDB, err := sql.Open("pgx", abortDSN)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if closeErr := abortDB.Close(); closeErr != nil {
				t.Errorf("close abort database: %v", closeErr)
			}
		}()
		for _, migration := range migrations[:7] {
			if err := migration.Apply(ctx, abortDB); err != nil {
				t.Fatalf("apply historical migration %d: %v", migration.Version(), err)
			}
		}
		// Simulate a drifted head-106 database whose prompt references a
		// session that does not exist in its tenant: drop the composite
		// session foreign key and disable the sync trigger that would also
		// fail to resolve the workspace.
		if _, err := abortDB.ExecContext(ctx, `DO $$
DECLARE r record;
BEGIN
	FOR r IN
		SELECT c.conname
		  FROM pg_constraint c
		  JOIN pg_class t ON t.oid = c.conrelid
		  JOIN pg_class rt ON rt.oid = c.confrelid
		  JOIN pg_namespace n ON n.oid = t.relnamespace
		 WHERE c.contype = 'f' AND n.nspname = 'public'
		   AND t.relname = 'prompts' AND rt.relname = 'sessions'
	LOOP
		EXECUTE format('ALTER TABLE prompts DROP CONSTRAINT %I', r.conname);
	END LOOP;
END $$`); err != nil {
			t.Fatalf("drop prompts to sessions foreign key: %v", err)
		}
		if _, err := abortDB.ExecContext(ctx, `ALTER TABLE prompts DISABLE TRIGGER prompts_sync_change`); err != nil {
			t.Fatalf("disable prompts sync trigger: %v", err)
		}
		const orphanSession int64 = 999999999
		if _, err := abortDB.ExecContext(ctx, `
			INSERT INTO prompts(tenant_id,session_id,content) VALUES($1,$2,'orphan')`, tenant, orphanSession); err != nil {
			t.Fatalf("seed orphan prompt: %v", err)
		}
		if err := subject.Apply(ctx, abortDB); err == nil || !strings.Contains(err.Error(), "reference no session") {
			t.Fatalf("apply 107 over orphan prompt err=%v; want fail-closed abort", err)
		}
		assertNoPartial107(t, ctx, abortDB)
		var orphans int
		if err := abortDB.QueryRowContext(ctx, `SELECT count(*) FROM prompts WHERE tenant_id=$1 AND session_id=$2`, tenant, orphanSession).Scan(&orphans); err != nil || orphans != 1 {
			t.Fatalf("orphan preserved after abort count=%d err=%v; want 1", orphans, err)
		}
		// Reviewed fixture recovery only: remove the synthetic orphan and
		// restore the catalog exactly as migration 100 built it.
		if _, err := abortDB.ExecContext(ctx, `DELETE FROM prompts WHERE tenant_id=$1 AND session_id=$2`, tenant, orphanSession); err != nil {
			t.Fatal(err)
		}
		if _, err := abortDB.ExecContext(ctx, `ALTER TABLE prompts ENABLE TRIGGER prompts_sync_change`); err != nil {
			t.Fatal(err)
		}
		if _, err := abortDB.ExecContext(ctx, `
			ALTER TABLE prompts ADD CONSTRAINT prompts_tenant_id_session_id_fkey
			FOREIGN KEY (tenant_id, session_id) REFERENCES sessions(tenant_id, id)`); err != nil {
			t.Fatalf("restore session foreign key: %v", err)
		}
		if err := subject.Apply(ctx, abortDB); err != nil {
			t.Fatalf("re-apply 107 after orphan repair: %v", err)
		}
	})

	t.Run("cross-workspace edge aborts 107 without partial state", func(t *testing.T) {
		abortDSN, cleanupAbort, err := isolatedPostgresDatabase(dsn, "migration107cross")
		if err != nil {
			t.Fatalf("create cross-abort database: %v", err)
		}
		defer cleanupAbort()
		abortDB, err := sql.Open("pgx", abortDSN)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if closeErr := abortDB.Close(); closeErr != nil {
				t.Errorf("close abort database: %v", closeErr)
			}
		}()
		for _, migration := range migrations[:7] {
			if err := migration.Apply(ctx, abortDB); err != nil {
				t.Fatalf("apply historical migration %d: %v", migration.Version(), err)
			}
		}
		var crossSession1, crossSession2 int64
		if err := abortDB.QueryRowContext(ctx, `
			WITH organization AS (
				INSERT INTO organizations(tenant_id,name) VALUES($1,'107 cross') RETURNING id
			), w1 AS (
				INSERT INTO workspaces(tenant_id,organization_id,name)
				SELECT $1,id,'cross one' FROM organization RETURNING id
			)
			INSERT INTO sessions(tenant_id,workspace_id) SELECT $1,id FROM w1 RETURNING id`, tenant).Scan(&crossSession1); err != nil {
			t.Fatalf("seed cross session one: %v", err)
		}
		if err := abortDB.QueryRowContext(ctx, `
			WITH w2 AS (
				INSERT INTO workspaces(tenant_id,organization_id,name)
				SELECT $1,id,'cross two' FROM organizations WHERE tenant_id=$1 RETURNING id
			)
			INSERT INTO sessions(tenant_id,workspace_id) SELECT $1,id FROM w2 RETURNING id`, tenant).Scan(&crossSession2); err != nil {
			t.Fatalf("seed cross session two: %v", err)
		}
		var crossObsA, crossObsB int64
		if err := abortDB.QueryRowContext(ctx, `INSERT INTO observations(tenant_id,session_id,type,title,content) VALUES($1,$2,'manual','cross a','a') RETURNING id`, tenant, crossSession1).Scan(&crossObsA); err != nil {
			t.Fatal(err)
		}
		if err := abortDB.QueryRowContext(ctx, `INSERT INTO observations(tenant_id,session_id,type,title,content) VALUES($1,$2,'manual','cross b','b') RETURNING id`, tenant, crossSession2).Scan(&crossObsB); err != nil {
			t.Fatal(err)
		}
		// Legal at head 106: the edge endpoints are tenant-scoped only.
		if _, err := abortDB.ExecContext(ctx, `INSERT INTO edges(tenant_id,from_observation_id,to_observation_id,relation_type) VALUES($1,$2,$3,'relates')`, tenant, crossObsA, crossObsB); err != nil {
			t.Fatalf("seed cross-workspace edge: %v", err)
		}
		if err := subject.Apply(ctx, abortDB); err == nil || !strings.Contains(err.Error(), "cross workspaces") {
			t.Fatalf("apply 107 over cross-workspace edge err=%v; want fail-closed abort", err)
		}
		assertNoPartial107(t, ctx, abortDB)
		var crossed int
		if err := abortDB.QueryRowContext(ctx, `SELECT count(*) FROM edges WHERE tenant_id=$1`, tenant).Scan(&crossed); err != nil || crossed != 1 {
			t.Fatalf("cross-workspace edge preserved after abort count=%d err=%v; want 1", crossed, err)
		}
		// Reviewed fixture recovery only: remove the synthetic edge and the
		// line continues forward.
		if _, err := abortDB.ExecContext(ctx, `DELETE FROM edges WHERE tenant_id=$1`, tenant); err != nil {
			t.Fatal(err)
		}
		if err := subject.Apply(ctx, abortDB); err != nil {
			t.Fatalf("re-apply 107 after cross repair: %v", err)
		}
	})

	t.Run("drifted duplicate client ids abort 107 without partial state", func(t *testing.T) {
		abortDSN, cleanupAbort, err := isolatedPostgresDatabase(dsn, "migration107dup")
		if err != nil {
			t.Fatalf("create duplicate-abort database: %v", err)
		}
		defer cleanupAbort()
		abortDB, err := sql.Open("pgx", abortDSN)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if closeErr := abortDB.Close(); closeErr != nil {
				t.Errorf("close abort database: %v", closeErr)
			}
		}()
		for _, migration := range migrations[:7] {
			if err := migration.Apply(ctx, abortDB); err != nil {
				t.Fatalf("apply historical migration %d: %v", migration.Version(), err)
			}
		}
		var dupSession int64
		if err := abortDB.QueryRowContext(ctx, `
			WITH organization AS (
				INSERT INTO organizations(tenant_id,name) VALUES($1,'107 dup') RETURNING id
			), w AS (
				INSERT INTO workspaces(tenant_id,organization_id,name)
				SELECT $1,id,'dup workspace' FROM organization RETURNING id
			)
			INSERT INTO sessions(tenant_id,workspace_id) SELECT $1,id FROM w RETURNING id`, tenant).Scan(&dupSession); err != nil {
			t.Fatalf("seed duplicate fixture session: %v", err)
		}
		// Simulate a drifted database: the tenant-wide guard is gone so
		// two rows with the same client_id land in the SAME workspace, and
		// 107 must abort instead of silently keeping an ambiguous
		// duplicate.
		if _, err := abortDB.ExecContext(ctx, `DROP INDEX observations_client_id_uq`); err != nil {
			t.Fatal(err)
		}
		duplicate := `INSERT INTO observations(tenant_id,session_id,client_id,type,title,content)
			VALUES($1,$2,'dup-client','manual',$3,'x')`
		if _, err := abortDB.ExecContext(ctx, duplicate, tenant, dupSession, "dup one"); err != nil {
			t.Fatalf("seed first duplicate: %v", err)
		}
		if _, err := abortDB.ExecContext(ctx, duplicate, tenant, dupSession, "dup two"); err != nil {
			t.Fatalf("seed second duplicate: %v", err)
		}
		if err := subject.Apply(ctx, abortDB); err == nil || !strings.Contains(err.Error(), "collide inside a workspace") {
			t.Fatalf("apply 107 over duplicate client ids err=%v; want fail-closed abort", err)
		}
		// The abort left the drifted head-106 state exactly as the fixture
		// built it: no ledger row, no workspace columns on prompts/edges,
		// and no partial index swap (the tenant-wide index stays dropped by
		// the fixture; the workspace-scoped replacement must not exist).
		var ledgered bool
		if err := abortDB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cortex_server_migrations WHERE version=107)`).Scan(&ledgered); err != nil || ledgered {
			t.Fatalf("aborted 107 leaked a ledger row ledgered=%v err=%v", ledgered, err)
		}
		for _, table := range []string{"prompts", "edges"} {
			var columns int
			if err := abortDB.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 AND column_name='workspace_id'`, table).Scan(&columns); err != nil {
				t.Fatal(err)
			}
			if columns != 0 {
				t.Fatalf("aborted 107 leaked %s.workspace_id", table)
			}
		}
		for _, swapped := range []string{"observations_client_id_ws_uq", "prompts_client_id_ws_uq", "edges_client_id_ws_uq"} {
			var present int
			if err := abortDB.QueryRowContext(ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname='public' AND indexname=$1`, swapped).Scan(&present); err != nil || present != 0 {
				t.Fatalf("aborted 107 leaked the replacement index %s count=%d err=%v", swapped, present, err)
			}
		}
		// Reviewed fixture recovery only: remove one synthetic duplicate
		// and restore the retired guard so the line can continue forward.
		if _, err := abortDB.ExecContext(ctx, `DELETE FROM observations WHERE tenant_id=$1 AND title='dup two'`, tenant); err != nil {
			t.Fatal(err)
		}
		if _, err := abortDB.ExecContext(ctx, `CREATE UNIQUE INDEX observations_client_id_uq ON observations(tenant_id, client_id) WHERE client_id IS NOT NULL`); err != nil {
			t.Fatal(err)
		}
		if err := subject.Apply(ctx, abortDB); err != nil {
			t.Fatalf("re-apply 107 after dedup: %v", err)
		}
	})

	t.Run("applied 107 preflight stops as already applied", func(t *testing.T) {
		state, err := subject.Preflight(ctx, db)
		if err == nil || !errors.Is(err, ErrPreflightStop) {
			t.Fatalf("preflight on applied 107 err=%v; want errors.Is ErrPreflightStop", err)
		}
		if errors.Is(err, ErrSchemaTampered) {
			t.Errorf("already-applied stop must not be tamper-class: %v", err)
		}
		if !state.Ledgered || state.RecordedChecksum != subject.Checksum() {
			t.Fatalf("preflight state = %+v; want 107 ledgered with the embedded checksum", state)
		}
	})

	t.Run("prior 107 checksum stops and escalates as tamper", func(t *testing.T) {
		if _, err := db.ExecContext(ctx,
			`UPDATE cortex_server_migrations SET checksum='old-prerelease-checksum' WHERE version=107`); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = db.ExecContext(ctx,
				`UPDATE cortex_server_migrations SET checksum=$1 WHERE version=107`, subject.Checksum())
		})
		state, err := subject.Preflight(ctx, db)
		if err == nil || !errors.Is(err, ErrPreflightStop) || !errors.Is(err, ErrSchemaTampered) {
			t.Fatalf("prior-checksum verdict = %v; want errors.Is ErrPreflightStop AND ErrSchemaTampered", err)
		}
		if !state.Ledgered || state.RecordedChecksum != "old-prerelease-checksum" {
			t.Fatalf("preflight state = %+v; want the prior checksum reported verbatim", state)
		}
		if err := subject.VerifyApplied(ctx, db); err == nil {
			t.Error("VerifyApplied accepted a drifted 107 checksum; want mismatch error")
		}
	})

	t.Run("down is forward-only with zero mutation", func(t *testing.T) {
		before := postgresMigrationSnapshot(t, ctx, db)
		if err := subject.Down(ctx, db); err == nil || !errors.Is(err, ErrForwardOnly) {
			t.Fatalf("Down(107) err=%v; want errors.Is ErrForwardOnly", err)
		}
		if after := postgresMigrationSnapshot(t, ctx, db); after != before {
			t.Fatal("Down executed DDL/DML: schema, ledger, or data snapshot changed")
		}
	})
}

// pgFunctionDef returns pg_get_functiondef for a regprocedure signature.
func pgFunctionDef(t *testing.T, ctx context.Context, db *sql.DB, sig string) string {
	t.Helper()
	var def string
	if err := db.QueryRowContext(ctx, `SELECT pg_get_functiondef($1::regprocedure)`, sig).Scan(&def); err != nil {
		t.Fatalf("pg_get_functiondef(%s): %v", sig, err)
	}
	return def
}

// assertAdvisoryBeforeRowLocks proves the advisory gate inside a live
// routine definition precedes every lock-taking identity statement.
func assertAdvisoryBeforeRowLocks(t *testing.T, name, def, gate string) {
	t.Helper()
	gateIdx := strings.Index(def, gate)
	if gateIdx < 0 {
		t.Fatalf("%s live definition does not acquire the gate %q", name, gate)
	}
	for _, keyword := range []string{
		"FOR UPDATE", "FOR SHARE",
		"INSERT INTO public.actor_subjects", "INSERT INTO public.principal_grants",
		"UPDATE public.actor_subjects", "UPDATE public.app_users", "UPDATE public.service_accounts",
		"INSERT INTO public.api_tokens", "UPDATE public.api_tokens",
		"DELETE FROM public.principal_grants",
	} {
		if idx := strings.Index(def, keyword); idx >= 0 && idx < gateIdx {
			t.Fatalf("%s locks identity rows (%q at %d) before the advisory gate (%d)", name, keyword, idx, gateIdx)
		}
	}
}

// TestPostgresMigration108PrincipalRWGating exercises migration 108 against
// real PostgreSQL 16 (PG-00/PG-01/PG-02, MIG-01): the canonical
// cortex_principal_key advisory namespace installed as runtime head with an
// exact ledger checksum; the live routine matrix proving shared-gated
// verify/bind FOR SHARE revalidation, exclusive-gated identity invalidators
// with lock-free key resolves and locked revalidation, and zero
// session-scope advisory usage; xact-scoped lock lifetimes with rollback
// release; the read-only preflight stop matrix with zero mutation; failed
// apply atomicity; a 107-to-108 upgrade; idempotent reapply; the
// forward-only Down policy; same-token verify overlap replacing the old FOR
// UPDATE serialization; throttled monotonic telemetry that never fails
// authentication; and the reader-drain/late-reader-refusal race through the
// real revoke routine.
func TestPostgresMigration108PrincipalRWGating(t *testing.T) {
	dsn := os.Getenv("CORTEX_TEST_POSTGRES_MIGRATION_DSN")
	if dsn == "" {
		t.Fatal("CORTEX_TEST_POSTGRES_MIGRATION_DSN is required for postgres_integration tests")
	}
	ctx := context.Background()

	freshDSN, cleanupFresh, err := isolatedPostgresDatabase(dsn, "migration108")
	if err != nil {
		t.Fatalf("create isolated 108 database: %v", err)
	}
	defer cleanupFresh()
	db, err := sql.Open("pgx", freshDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	migrations := mustPostgresMigrations(t)
	var subject *PostgresServerMigration
	for _, migration := range migrations {
		if migration.Version() == 108 {
			subject = migration
			break
		}
	}
	if subject == nil {
		t.Fatal("migration 108 not registered")
	}

	// Read-only ledger preflight before any apply: the expected rollout
	// state of 108 on a fresh database is unledgered, with zero mutation.
	{
		before := preflightLedgerSnapshot(t, ctx, db)
		state, err := subject.Preflight(ctx, db)
		if err != nil {
			t.Fatalf("preflight on empty DB err=%v; want nil (expected unledgered state)", err)
		}
		if state.LedgerTable || state.Ledgered || state.FutureLedgerVersion != 0 {
			t.Fatalf("preflight state on empty DB = %+v; want no ledger, no row, no future", state)
		}
		if after := preflightLedgerSnapshot(t, ctx, db); after != before {
			t.Fatalf("preflight mutated an empty database:\nbefore:\n%s\nafter:\n%s", before, after)
		}
	}

	// Fresh apply of the full line: 108 is the runtime head with its exact
	// checksum; reapply is idempotent.
	if err := ApplyPostgresServerMigrations(ctx, db); err != nil {
		t.Fatalf("fresh apply 100-108: %v", err)
	}
	{
		var ledgerCount int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM cortex_server_migrations WHERE version BETWEEN 100 AND 108`).Scan(&ledgerCount); err != nil || ledgerCount != 9 {
			t.Fatalf("ledger count=%d err=%v; want 9", ledgerCount, err)
		}
		var ledgerChecksum string
		if err := db.QueryRowContext(ctx, `SELECT checksum FROM cortex_server_migrations WHERE version=108`).Scan(&ledgerChecksum); err != nil || ledgerChecksum != subject.Checksum() {
			t.Fatalf("ledger 108 checksum=%q err=%v; want %q", ledgerChecksum, err, subject.Checksum())
		}
		if err := subject.VerifyApplied(ctx, db); err != nil {
			t.Fatalf("post-apply check for 108: %v", err)
		}
		if err := ApplyPostgresServerMigrations(ctx, db); err != nil {
			t.Fatalf("reapply 100-108: %v", err)
		}
	}

	// The preflight stop matrix on the applied database, zero mutation.
	t.Run("preflight stop matrix", func(t *testing.T) {
		before := preflightLedgerSnapshot(t, ctx, db)
		state, err := subject.Preflight(ctx, db)
		if err == nil || !errors.Is(err, ErrPreflightStop) {
			t.Fatalf("preflight on applied 108 err=%v; want errors.Is ErrPreflightStop", err)
		}
		if errors.Is(err, ErrSchemaTampered) {
			t.Errorf("already-applied stop must not be tamper-class: %v", err)
		}
		if !state.Ledgered || state.RecordedChecksum != subject.Checksum() {
			t.Fatalf("preflight state = %+v; want 108 ledgered with the embedded checksum", state)
		}
		if err := subject.VerifyApplied(ctx, db); err != nil {
			t.Fatalf("post-apply check err=%v; want nil", err)
		}

		if _, err := db.ExecContext(ctx,
			`UPDATE cortex_server_migrations SET checksum='old-prerelease-checksum' WHERE version=108`); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = db.ExecContext(ctx,
				`UPDATE cortex_server_migrations SET checksum=$1 WHERE version=108`, subject.Checksum())
		})
		state, err = subject.Preflight(ctx, db)
		if err == nil || !errors.Is(err, ErrPreflightStop) || !errors.Is(err, ErrSchemaTampered) {
			t.Fatalf("prior-checksum verdict = %v; want errors.Is ErrPreflightStop AND ErrSchemaTampered", err)
		}
		if !state.Ledgered || state.RecordedChecksum != "old-prerelease-checksum" {
			t.Fatalf("preflight state = %+v; want the prior checksum reported verbatim", state)
		}
		if err := subject.VerifyApplied(ctx, db); err == nil {
			t.Error("VerifyApplied accepted a drifted 108 checksum; want mismatch error")
		}
		if err := subject.Apply(ctx, db); err == nil || !strings.Contains(err.Error(), "108 checksum mismatch") {
			t.Fatalf("apply on drifted checksum err=%v; want 108 checksum mismatch", err)
		}
		if _, err := db.ExecContext(ctx,
			`UPDATE cortex_server_migrations SET checksum=$1 WHERE version=108`, subject.Checksum()); err != nil {
			t.Fatal(err)
		}

		if _, err := db.ExecContext(ctx,
			`INSERT INTO cortex_server_migrations (version, name, checksum) VALUES (199, 'future_runtime', 'future')`); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = db.ExecContext(ctx, `DELETE FROM cortex_server_migrations WHERE version=199`)
		})
		state, err = subject.Preflight(ctx, db)
		if err == nil || !errors.Is(err, ErrPreflightStop) || !errors.Is(err, ErrFutureMigration) {
			t.Fatalf("future verdict = %v; want errors.Is ErrPreflightStop AND ErrFutureMigration", err)
		}
		if state.FutureLedgerVersion != 199 {
			t.Fatalf("preflight FutureLedgerVersion=%d; want 199", state.FutureLedgerVersion)
		}
		// Remove the fabricated future row INSIDE the subtest so the
		// zero-mutation snapshot comparison below sees the original state
		// (t.Cleanup alone would run only after the subtest body).
		if _, err := db.ExecContext(ctx, `DELETE FROM cortex_server_migrations WHERE version=199`); err != nil {
			t.Fatal(err)
		}
		if after := preflightLedgerSnapshot(t, ctx, db); after != before {
			t.Fatalf("preflight matrix mutated the database:\nbefore:\n%s\nafter:\n%s", before, after)
		}
	})

	t.Run("live routine matrix is canonically gated", func(t *testing.T) {
		keyDef := pgFunctionDef(t, ctx, db, "public.cortex_principal_key(uuid,uuid)")
		if !strings.Contains(keyDef, "hashtextextended('cortex:principal:' || p_tenant::text || ':' || p_actor::text, 0)") {
			t.Fatalf("canonical key helper body drifted:\n%s", keyDef)
		}

		verifyDef := pgFunctionDef(t, ctx, db, "public.cortex_verify_token_principal(text,bytea,text)")
		assertAdvisoryBeforeRowLocks(t, "verify", verifyDef, "pg_advisory_xact_lock_shared(public.cortex_principal_key(")
		if got := strings.Count(verifyDef, "FOR UPDATE"); got != 0 {
			t.Fatalf("verify must take no token-row lock at all; found %d FOR UPDATE", got)
		}
		if strings.Contains(verifyDef, "FOR SHARE OF t") {
			t.Fatal("verify still locks the token row FOR SHARE; the token re-read must stay lock-free under the shared gate")
		}
		if !strings.Contains(verifyDef, "FOR SHARE OF a") {
			t.Fatal("verify does not revalidate the actor identity row FOR SHARE under the shared gate")
		}
		if !strings.Contains(verifyDef, "pg_try_advisory_xact_lock(hashtextextended('cortex:principal-usage:'") {
			t.Fatal("verify telemetry does not use the dedicated token-usage advisory")
		}
		if !strings.Contains(verifyDef, "interval '30 seconds'") {
			t.Fatal("verify telemetry is not throttled")
		}

		bindDef := pgFunctionDef(t, ctx, db, "public.cortex_bind_principal(uuid,text,bigint)")
		assertAdvisoryBeforeRowLocks(t, "bind", bindDef, "pg_advisory_xact_lock_shared(public.cortex_principal_key(")
		if strings.Contains(bindDef, "FOR UPDATE") {
			t.Fatal("bind still takes exclusive row locks; readers use FOR SHARE under the shared gate")
		}

		for _, sig := range []string{
			"public.cortex_provision_actor(uuid,text,text,jsonb,text)",
			"public.cortex_set_actor_active(uuid,boolean,text)",
			"public.cortex_issue_api_token(uuid,text,text,text[],uuid[],timestamptz,text)",
			"public.cortex_rotate_api_token(uuid,text,text)",
			"public.cortex_revoke_api_token(uuid,text)",
			"public.cortex_bootstrap_service_principal(uuid,uuid,uuid,text,text,jsonb,text,text,text)",
		} {
			assertAdvisoryBeforeRowLocks(t, sig, pgFunctionDef(t, ctx, db, sig), "pg_advisory_xact_lock(public.cortex_principal_key(")
		}
		bootstrapDef := pgFunctionDef(t, ctx, db, "public.cortex_bootstrap_service_principal(uuid,uuid,uuid,text,text,jsonb,text,text,text)")
		if strings.Contains(bootstrapDef, "hashtextextended(p_tenant_id::text || ':' || p_actor_public_id::text, 0)") {
			t.Fatal("bootstrap still uses the legacy non-canonical advisory key")
		}

		// No session-scope advisory usage survived into the catalog.
		for _, sig := range []string{
			"public.cortex_principal_key(uuid,uuid)",
			"public.cortex_verify_token_principal(text,bytea,text)",
			"public.cortex_bind_principal(uuid,text,bigint)",
			"public.cortex_provision_actor(uuid,text,text,jsonb,text)",
			"public.cortex_set_actor_active(uuid,boolean,text)",
			"public.cortex_issue_api_token(uuid,text,text,text[],uuid[],timestamptz,text)",
			"public.cortex_rotate_api_token(uuid,text,text)",
			"public.cortex_revoke_api_token(uuid,text)",
			"public.cortex_bootstrap_service_principal(uuid,uuid,uuid,text,text,jsonb,text,text,text)",
		} {
			def := pgFunctionDef(t, ctx, db, sig)
			for _, banned := range []string{"pg_advisory_lock(", "pg_try_advisory_lock(", "pg_advisory_unlock", "SET LOCAL"} {
				if strings.Contains(def, banned) {
					t.Fatalf("%s live definition contains session-scope usage %q", sig, banned)
				}
			}
		}

		// Owners and EXECUTE matrices: the helper and the bootstrap
		// reconciler are owned by cortex_migration; the six application
		// routines stay executable only by cortex_app.
		for sig, owner := range map[string]string{
			"public.cortex_principal_key(uuid,uuid)":                                                   "cortex_migration",
			"public.cortex_bootstrap_service_principal(uuid,uuid,uuid,text,text,jsonb,text,text,text)": "cortex_migration",
		} {
			var ownerName string
			if err := db.QueryRowContext(ctx, `
				SELECT pg_get_userbyid(p.proowner) FROM pg_proc p WHERE p.oid = $1::regprocedure`, sig).Scan(&ownerName); err != nil || ownerName != owner {
				t.Fatalf("%s owner=%q err=%v; want %s", sig, ownerName, err, owner)
			}
		}
		for sig, want := range map[string]map[string]bool{
			"public.cortex_principal_key(uuid,uuid)":                                                   {"cortex_migration": true, "cortex_app": false, "cortex_admin": false, "public": false},
			"public.cortex_verify_token_principal(text,bytea,text)":                                    {"cortex_app": true, "cortex_admin": false, "public": false},
			"public.cortex_bind_principal(uuid,text,bigint)":                                           {"cortex_app": true, "cortex_admin": false, "public": false},
			"public.cortex_provision_actor(uuid,text,text,jsonb,text)":                                 {"cortex_app": true, "cortex_admin": false, "public": false},
			"public.cortex_set_actor_active(uuid,boolean,text)":                                        {"cortex_app": true, "cortex_admin": false, "public": false},
			"public.cortex_issue_api_token(uuid,text,text,text[],uuid[],timestamptz,text)":             {"cortex_app": true, "cortex_admin": false, "public": false},
			"public.cortex_rotate_api_token(uuid,text,text)":                                           {"cortex_app": true, "cortex_admin": false, "public": false},
			"public.cortex_revoke_api_token(uuid,text)":                                                {"cortex_app": true, "cortex_admin": false, "public": false},
			"public.cortex_bootstrap_service_principal(uuid,uuid,uuid,text,text,jsonb,text,text,text)": {"cortex_migration": true, "cortex_app": false, "cortex_admin": false, "public": false},
		} {
			for role, allowed := range want {
				var got bool
				if err := db.QueryRowContext(ctx, `SELECT has_function_privilege($1, $2, 'EXECUTE')`, role, sig).Scan(&got); err != nil || got != allowed {
					t.Fatalf("has_function_privilege(%s, %s)=%v err=%v; want %v", role, sig, got, err, allowed)
				}
			}
		}
	})

	t.Run("advisory locks are transaction-scoped and released on rollback", func(t *testing.T) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		tenant := "00000000-0000-0000-0000-0000000004b0"
		actor := "00000000-0000-0000-0000-0000000004b1"
		if _, err := tx.ExecContext(ctx,
			`SELECT pg_advisory_xact_lock_shared(public.cortex_principal_key($1, $2))`, tenant, actor); err != nil {
			t.Fatalf("shared gate through the canonical helper: %v", err)
		}
		var held int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid()`).Scan(&held); err != nil || held < 1 {
			t.Fatalf("advisory locks held in tx=%d err=%v; want >=1", held, err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		var residue int
		if err := db.QueryRowContext(ctx,
			`SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid()`).Scan(&residue); err != nil || residue != 0 {
			t.Fatalf("advisory residue after rollback=%d err=%v; want 0", residue, err)
		}
	})

	t.Run("verify telemetry is throttled, monotonic, and non-authoritative", func(t *testing.T) {
		tenant := "00000000-0000-0000-0000-0000000004c0"
		wsPub := seed108TenantWorkspace(t, ctx, db, tenant)
		actor := "00000000-0000-0000-0000-0000000004c1"
		grants := grantsJSON108(
			[2]string{"role", "owner"},
			[2]string{"workspace", wsPub},
			[2]string{"scope", "workspaces:read"},
		)
		const secret = "migration108-bearer-sentinel"
		const tokenName = "bootstrap/108-telemetry"
		mustBootstrap(t, ctx, db, tenant, wsPub, actor, "cortex-108-telemetry", "cortex-108", grants, tokenName, secret, "telemetry fixture")

		// Age the telemetry timestamp past the throttle window.
		if _, err := db.ExecContext(ctx, `
			UPDATE api_tokens SET last_used_at = now() - interval '2 hours'
			 WHERE tenant_id=$1 AND revoked_at IS NULL AND subject_service_account_id=(
			   SELECT id FROM service_accounts WHERE tenant_id=$1 AND public_id=$2)`, tenant, actor); err != nil {
			t.Fatal(err)
		}
		var advanced sql.NullTime
		if err := db.QueryRowContext(ctx, `
			SELECT last_used_at FROM api_tokens
			 WHERE tenant_id=$1 AND revoked_at IS NULL AND subject_service_account_id=(
			   SELECT id FROM service_accounts WHERE tenant_id=$1 AND public_id=$2)`, tenant, actor).Scan(&advanced); err != nil {
			t.Fatal(err)
		}
		if err := verify108Principal(t, ctx, db, secret, tenant); err != nil {
			t.Fatalf("verify after aging telemetry: %v", err)
		}
		var refreshed sql.NullTime
		if err := db.QueryRowContext(ctx, `
			SELECT last_used_at FROM api_tokens
			 WHERE tenant_id=$1 AND revoked_at IS NULL AND subject_service_account_id=(
			   SELECT id FROM service_accounts WHERE tenant_id=$1 AND public_id=$2)`, tenant, actor).Scan(&refreshed); err != nil {
			t.Fatal(err)
		}
		if !refreshed.Valid || !advanced.Valid || !refreshed.Time.After(advanced.Time) {
			t.Fatalf("stale telemetry not advanced by the single winner: before=%v after=%v", advanced.Time, refreshed.Time)
		}
		// Immediate re-verification inside the throttle window keeps the
		// timestamp stable (approximate, throttled) and still succeeds.
		if err := verify108Principal(t, ctx, db, secret, tenant); err != nil {
			t.Fatalf("verify inside throttle window: %v", err)
		}
		var stable sql.NullTime
		if err := db.QueryRowContext(ctx, `
			SELECT last_used_at FROM api_tokens
			 WHERE tenant_id=$1 AND revoked_at IS NULL AND subject_service_account_id=(
			   SELECT id FROM service_accounts WHERE tenant_id=$1 AND public_id=$2)`, tenant, actor).Scan(&stable); err != nil {
			t.Fatal(err)
		}
		if !stable.Valid || stable.Time != refreshed.Time {
			t.Fatalf("throttled telemetry moved inside the window: %v vs %v", stable.Time, refreshed.Time)
		}
	})

	t.Run("same-token verifiers overlap instead of serializing", func(t *testing.T) {
		tenant := "00000000-0000-0000-0000-0000000004d0"
		wsPub := seed108TenantWorkspace(t, ctx, db, tenant)
		actor := "00000000-0000-0000-0000-0000000004d1"
		grants := grantsJSON108(
			[2]string{"role", "owner"},
			[2]string{"workspace", wsPub},
		)
		const secret = "migration108-bearer-overlap"
		mustBootstrap(t, ctx, db, tenant, wsPub, actor, "cortex-108-overlap", "cortex-108", grants, "bootstrap/108-overlap", secret, "overlap fixture")

		connA, err := sql.Open("pgx", freshDSN)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = connA.Close() })
		connA.SetMaxOpenConns(1)
		connB, err := sql.Open("pgx", freshDSN)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = connB.Close() })
		connB.SetMaxOpenConns(1)

		// Reader A holds its transaction open with the shared gate and the
		// FOR SHARE token revalidation. Under the 106-era FOR UPDATE this
		// is exactly the point where a same-token peer would block.
		txA, err := connA.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = txA.Rollback() }()
		if err := verify108PrincipalTx(t, ctx, txA, secret, tenant); err != nil {
			t.Fatalf("reader A verify: %v", err)
		}

		done := make(chan error, 1)
		go func() {
			txB, err := connB.BeginTx(ctx, nil)
			if err != nil {
				done <- err
				return
			}
			defer func() { _ = txB.Rollback() }()
			if err := verify108PrincipalTx(t, ctx, txB, secret, tenant); err != nil {
				done <- err
				return
			}
			done <- nil
		}()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("same-token peer verify while reader A holds the gate: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("same-token peer verify blocked behind an open reader; the FOR UPDATE serialization is still present")
		}

		// The same-token race must not deadlock on telemetry: reader A
		// (winner or skipper) still commits cleanly.
		if err := txA.Commit(); err != nil {
			t.Fatalf("reader A commit after peer verify: %v", err)
		}
	})

	t.Run("revoke drains the in-flight reader and refuses the late reader", func(t *testing.T) {
		tenant := "00000000-0000-0000-0000-0000000004e0"
		wsPub := seed108TenantWorkspace(t, ctx, db, tenant)
		actor := "00000000-0000-0000-0000-0000000004e1"
		grants := grantsJSON108(
			[2]string{"role", "owner"},
			[2]string{"workspace", wsPub},
		)
		const secretA = "migration108-bearer-race-a"
		mustBootstrap(t, ctx, db, tenant, wsPub, actor, "cortex-108-race", "cortex-108", grants, "bootstrap/108-race", secretA, "race fixture")
		// A second live token of the same actor is the revocation victim.
		// Its bearer deliberately uses a DIFFERENT textual head from the
		// bootstrap bearer: token prefixes are unique per tenant.
		const secretB = "race-victim-bearer-108-sentinel"
		if _, err := db.ExecContext(ctx, `
			INSERT INTO api_tokens (tenant_id, name, token_prefix, token_digest, subject_service_account_id, scopes, workspace_ids, expires_at, created_by)
			SELECT $1::uuid, 'race/victim', left($2::text,12), hmac(convert_to($2::text,'UTF8'), convert_to($1::text,'UTF8'),'sha256'),
			       (SELECT id FROM service_accounts WHERE tenant_id=$1::uuid AND public_id=$3::uuid), '{}', '{}', NULL, $3::uuid`,
			tenant, secretB, actor); err != nil {
			t.Fatalf("seed victim token: %v", err)
		}
		var victimID string
		if err := db.QueryRowContext(ctx, `
			SELECT t.public_id::text FROM api_tokens t
			 WHERE t.tenant_id=$1 AND t.name='race/victim' AND t.revoked_at IS NULL`, tenant).Scan(&victimID); err != nil {
			t.Fatal(err)
		}

		connA, err := sql.Open("pgx", freshDSN)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = connA.Close() })
		connA.SetMaxOpenConns(1)
		connB, err := sql.Open("pgx", freshDSN)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = connB.Close() })
		connB.SetMaxOpenConns(1)

		// Reader A verifies the victim token and holds its transaction
		// open: the shared actor gate and FOR SHARE revalidation are live.
		txA, err := connA.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = txA.Rollback() }()
		if err := verify108PrincipalTx(t, ctx, txA, secretB, tenant); err != nil {
			t.Fatalf("in-flight reader verify: %v", err)
		}

		// Writer B binds the owner principal, then revokes the victim
		// token: the exclusive canonical gate must queue behind reader A.
		revokeDone := make(chan error, 1)
		go func() {
			txB, err := connB.BeginTx(ctx, nil)
			if err != nil {
				revokeDone <- err
				return
			}
			defer func() { _ = txB.Rollback() }()
			provenance, version, err := verify108Proof(t, ctx, txB, secretA, tenant, actor)
			if err != nil {
				revokeDone <- err
				return
			}
			if _, err := txB.ExecContext(ctx, `SELECT cortex_bind_principal($1, $2, $3)`, actor, provenance, version); err != nil {
				revokeDone <- err
				return
			}
			var revoked bool
			if err := txB.QueryRowContext(ctx, `SELECT cortex_revoke_api_token($1, 'race probe')`, victimID).Scan(&revoked); err != nil {
				revokeDone <- err
				return
			}
			if !revoked {
				revokeDone <- errors.New("revoke reported a no-op transition")
				return
			}
			if err := txB.Commit(); err != nil {
				revokeDone <- err
				return
			}
			revokeDone <- nil
		}()

		// The writer must still be parked behind the open reader.
		select {
		case err := <-revokeDone:
			t.Fatalf("revoke completed while the in-flight reader still held the shared gate: %v", err)
		case <-time.After(700 * time.Millisecond):
			// Expected: the exclusive gate waits for the reader to drain.
		}

		// The reader completes; the writer must then finish within budget.
		if err := txA.Commit(); err != nil {
			t.Fatalf("in-flight reader commit: %v", err)
		}
		select {
		case err := <-revokeDone:
			if err != nil {
				t.Fatalf("gated revoke after reader drain: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("gated revoke did not complete after the reader drained")
		}

		// The late reader is refused: zero stale post-commit accepts.
		_, err = db.ExecContext(ctx, `
			SELECT cortex_verify_token_principal(left($1,12), hmac(convert_to($1,'UTF8'), convert_to($2::text,'UTF8'),'sha256'), '')`, secretB, tenant)
		assertPgErrorCode(t, err, "28000")
		var stillActive int
		if err := db.QueryRowContext(ctx,
			`SELECT count(*) FROM api_tokens WHERE tenant_id=$1 AND public_id=$2 AND revoked_at IS NULL`, tenant, victimID).Scan(&stillActive); err != nil || stillActive != 0 {
			t.Fatalf("victim token still active=%d err=%v; want 0", stillActive, err)
		}
	})

	t.Run("issue and rotate revalidate subjects without nullable-side row locks", func(t *testing.T) {
		tenant := "00000000-0000-0000-0000-0000000004f0"
		wsPub := seed108TenantWorkspace(t, ctx, db, tenant)
		actor := "00000000-0000-0000-0000-0000000004f1"
		grants := grantsJSON108(
			[2]string{"role", "owner"},
			[2]string{"workspace", wsPub},
		)
		const secret = "migration108-bearer-writer"
		const tokenName = "bootstrap/108-writer"
		boot := mustBootstrap(t, ctx, db, tenant, wsPub, actor, "cortex-108-writer", "cortex-108", grants, tokenName, secret, "writer fixture")

		conn, err := sql.Open("pgx", freshDSN)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		conn.SetMaxOpenConns(1)
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		provenance, version, err := verify108Proof(t, ctx, tx, secret, tenant, actor)
		if err != nil {
			t.Fatalf("writer verify: %v", err)
		}
		if _, err := tx.ExecContext(ctx, `SELECT cortex_bind_principal($1, $2, $3)`, actor, provenance, version); err != nil {
			t.Fatalf("writer bind: %v", err)
		}
		// Direct SQL issue: the split single-table FOR SHARE subject
		// lookups replaced the former nullable-side locks, so the call
		// must succeed instead of raising SQLSTATE 0A000.
		const issuedSecret = "issue-writer-108"
		var issuedID, issuedPrefix string
		if err := tx.QueryRowContext(ctx,
			`SELECT token_public_id::text, token_prefix
			   FROM cortex_issue_api_token($1, 'direct/108-issue', $2, ARRAY['workspaces:read'], '{}', NULL, 'direct issue probe')`,
			actor, issuedSecret).Scan(&issuedID, &issuedPrefix); err != nil {
			t.Fatalf("direct issue through migration 108: %v", err)
		}
		if issuedPrefix != issuedSecret[:12] {
			t.Fatalf("issued prefix=%q; want the bearer head %q", issuedPrefix, issuedSecret[:12])
		}
		// Direct SQL rotate of the bootstrap token: the token row locks
		// FOR UPDATE as the only preserved side and the subject
		// revalidates through the split lockable lookups.
		const rotatedSecret = "rotate-writer-108"
		var rotatedID, rotatedPrefix, rotatedName, rotatedSubject, rotatedType string
		if err := tx.QueryRowContext(ctx, `
			SELECT token_public_id::text, token_prefix, token_name, subject_public_id::text, principal_type
			  FROM cortex_rotate_api_token($1, $2, 'direct rotate probe')`,
			boot.tokenID, rotatedSecret).Scan(&rotatedID, &rotatedPrefix, &rotatedName, &rotatedSubject, &rotatedType); err != nil {
			t.Fatalf("direct rotate through migration 108: %v", err)
		}
		if rotatedSubject != actor || rotatedType != "service_account" || rotatedName != tokenName {
			t.Fatalf("rotate lost subject/name: subject=%s type=%s name=%s", rotatedSubject, rotatedType, rotatedName)
		}
		if rotatedPrefix != rotatedSecret[:12] {
			t.Fatalf("rotated prefix=%q; want the bearer head %q", rotatedPrefix, rotatedSecret[:12])
		}
		// Fail-closed taxonomy survives the split: an unknown issue
		// subject and a rotate of the now-revoked token both reject with
		// the pinned 23503 error. Each probe runs under its own
		// savepoint because the raised error aborts the surrounding
		// transaction otherwise.
		for _, probe := range []struct {
			name  string
			query string
			args  []any
		}{
			{
				"issue_unknown_subject",
				`SELECT cortex_issue_api_token($1, 'no-subject', 'unmapped-writer-bearer', '{}', '{}', NULL, 'probe')`,
				[]any{"00000000-0000-0000-0000-00000000dead"},
			},
			{
				"rotate_revoked_token",
				`SELECT cortex_rotate_api_token($1, 'revoked-writer-bearer', 'probe')`,
				[]any{boot.tokenID},
			},
		} {
			if _, err := tx.ExecContext(ctx, `SAVEPOINT negprobe`); err != nil {
				t.Fatalf("%s savepoint: %v", probe.name, err)
			}
			_, err = tx.ExecContext(ctx, probe.query, probe.args...)
			assertPgErrorCode(t, err, "23503")
			if _, err := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT negprobe`); err != nil {
				t.Fatalf("%s rollback to savepoint: %v", probe.name, err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		var oldRevoked int
		if err := db.QueryRowContext(ctx,
			`SELECT count(*) FROM api_tokens WHERE tenant_id=$1 AND public_id=$2 AND revoked_at IS NOT NULL`,
			tenant, boot.tokenID).Scan(&oldRevoked); err != nil || oldRevoked != 1 {
			t.Fatalf("rotated-from token not revoked after commit: count=%d err=%v", oldRevoked, err)
		}
		if err := verify108Principal(t, ctx, db, rotatedSecret, tenant); err != nil {
			t.Fatalf("verify after direct rotate: %v", err)
		}
		if _, err := db.ExecContext(ctx,
			`SELECT cortex_verify_token_principal(left($1,12), hmac(convert_to($1,'UTF8'), convert_to($2::text,'UTF8'),'sha256'), '')`,
			issuedSecret, tenant); err != nil {
			t.Fatalf("verify after direct issue: %v", err)
		}
	})

	t.Run("down is forward-only with zero mutation", func(t *testing.T) {
		before := postgresMigrationSnapshot(t, ctx, db)
		if err := subject.Down(ctx, db); err == nil || !errors.Is(err, ErrForwardOnly) {
			t.Fatalf("Down(108) err=%v; want errors.Is ErrForwardOnly", err)
		}
		if after := postgresMigrationSnapshot(t, ctx, db); after != before {
			t.Fatal("Down executed DDL/DML: schema, ledger, or data snapshot changed")
		}
	})

	t.Run("upgrade from 107 and failed-apply atomicity", func(t *testing.T) {
		upgradeDSN, cleanupUpgrade, err := isolatedPostgresDatabase(dsn, "migration108up")
		if err != nil {
			t.Fatalf("create isolated 108 upgrade database: %v", err)
		}
		defer cleanupUpgrade()
		up, err := sql.Open("pgx", upgradeDSN)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := up.Close(); err != nil {
				t.Errorf("close upgrade database: %v", err)
			}
		})

		for _, migration := range migrations[:8] {
			if err := migration.Apply(ctx, up); err != nil {
				t.Fatalf("apply historical migration %d: %v", migration.Version(), err)
			}
		}
		// The 107-era verify still carries the FOR UPDATE serialization.
		oldVerify := pgFunctionDef(t, ctx, up, "public.cortex_verify_token_principal(text,bytea,text)")
		if !strings.Contains(oldVerify, "FOR UPDATE OF t") {
			t.Fatal("107-era verify unexpectedly lacks the legacy FOR UPDATE token row lock")
		}
		state, err := subject.Preflight(ctx, up)
		if err != nil || state.Ledgered || state.FutureLedgerVersion != 0 {
			t.Fatalf("preflight at head 107 state=%+v err=%v; want 108 unledgered", state, err)
		}

		// A failing apply of the exact 108 SQL rolls back atomically: the
		// 106-era definitions survive, the helper is absent, no ledger row.
		injected := &PostgresServerMigration{
			version:  198,
			name:     "injected_failure_108",
			sql:      subject.SQL() + "\nSELECT 1/0;",
			checksum: "injected",
		}
		if err := injected.Apply(ctx, up); err == nil {
			t.Fatal("injected failing 108 apply unexpectedly succeeded")
		}
		var helperPresent bool
		if err := up.QueryRowContext(ctx,
			`SELECT to_regprocedure('public.cortex_principal_key(uuid,uuid)') IS NOT NULL`).Scan(&helperPresent); err != nil || helperPresent {
			t.Fatalf("failed apply leaked the canonical helper (present=%v err=%v)", helperPresent, err)
		}
		var failedLedger int
		if err := up.QueryRowContext(ctx,
			`SELECT count(*) FROM cortex_server_migrations WHERE version=198`).Scan(&failedLedger); err != nil || failedLedger != 0 {
			t.Fatalf("failed apply leaked a ledger row (%d)", failedLedger)
		}
		survivor := pgFunctionDef(t, ctx, up, "public.cortex_verify_token_principal(text,bytea,text)")
		if !strings.Contains(survivor, "FOR UPDATE OF t;") {
			t.Fatal("failed 108 apply replaced the verify routine despite rolling back")
		}

		// The real upgrade then applies cleanly and swaps the protocol in.
		if err := subject.Apply(ctx, up); err != nil {
			t.Fatalf("upgrade 107 -> 108: %v", err)
		}
		if err := subject.VerifyApplied(ctx, up); err != nil {
			t.Fatalf("post-apply check after upgrade: %v", err)
		}
		upgraded := pgFunctionDef(t, ctx, up, "public.cortex_verify_token_principal(text,bytea,text)")
		if !strings.Contains(upgraded, "pg_advisory_xact_lock_shared(public.cortex_principal_key(") {
			t.Fatal("upgraded verify does not use the shared canonical gate")
		}
		if err := ApplyPostgresServerMigrations(ctx, up); err != nil {
			t.Fatalf("reapply after upgrade: %v", err)
		}
	})
}

// seed108TenantWorkspace seeds one organization and workspace and returns
// the workspace public id.
func seed108TenantWorkspace(t *testing.T, ctx context.Context, db *sql.DB, tenant string) string {
	t.Helper()
	var wsPub string
	if err := db.QueryRowContext(ctx, `
		WITH organization AS (
			INSERT INTO organizations(tenant_id,name) VALUES($1,'migration 108 fixture') RETURNING id
		)
		INSERT INTO workspaces(tenant_id,organization_id,name)
		SELECT $1,id,'migration 108 workspace' FROM organization RETURNING public_id::text`, tenant).Scan(&wsPub); err != nil {
		t.Fatalf("seed tenant/workspace: %v", err)
	}
	return wsPub
}

// grantsJSON108 builds the reconciler grants JSON from type/value pairs.
func grantsJSON108(pairs ...[2]string) string {
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts = append(parts, fmt.Sprintf(`{"type":%q,"value":%q}`, p[0], p[1]))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// verify108Principal runs a committed verification of one bearer.
func verify108Principal(t *testing.T, ctx context.Context, db *sql.DB, secret, tenant string) error {
	t.Helper()
	var subject string
	return db.QueryRowContext(ctx, `
		SELECT subject_public_id::text FROM cortex_verify_token_principal(
			left($1,12), hmac(convert_to($1,'UTF8'), convert_to($2::text,'UTF8'),'sha256'), '')`, secret, tenant).Scan(&subject)
}

// verify108PrincipalTx runs the verification inside an open transaction.
func verify108PrincipalTx(t *testing.T, ctx context.Context, tx *sql.Tx, secret, tenant string) error {
	t.Helper()
	var subject string
	return tx.QueryRowContext(ctx, `
		SELECT subject_public_id::text FROM cortex_verify_token_principal(
			left($1,12), hmac(convert_to($1,'UTF8'), convert_to($2::text,'UTF8'),'sha256'), '')`, secret, tenant).Scan(&subject)
}

// verify108Proof runs the verification and returns the minted binding
// provenance with the observed grant version.
func verify108Proof(t *testing.T, ctx context.Context, tx *sql.Tx, secret, tenant, actor string) (string, int64, error) {
	t.Helper()
	var subjectID, provenance string
	var version int64
	err := tx.QueryRowContext(ctx, `
		SELECT subject_public_id::text, grant_version, binding_provenance FROM cortex_verify_token_principal(
			left($1,12), hmac(convert_to($1,'UTF8'), convert_to($2::text,'UTF8'),'sha256'), '')`, secret, tenant).
		Scan(&subjectID, &version, &provenance)
	if err != nil {
		return "", 0, err
	}
	if subjectID != actor {
		return "", 0, fmt.Errorf("verified subject %s; want %s", subjectID, actor)
	}
	return provenance, version, nil
}

// TestPostgresMigration109ScopedCodeIndex proves the forward-only lifecycle
// and post-apply security shape against PostgreSQL. Cross-scope row behavior
// is covered with authenticated principals by the scoped-store gate.
func TestPostgresMigration109ScopedCodeIndex(t *testing.T) {
	dsn := os.Getenv("CORTEX_TEST_POSTGRES_MIGRATION_DSN")
	if dsn == "" {
		t.Fatal("CORTEX_TEST_POSTGRES_MIGRATION_DSN is required for postgres_integration tests")
	}
	isolatedDSN, cleanup, err := isolatedPostgresDatabase(dsn, "migration109")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	db, err := sql.Open("pgx", isolatedDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	migrations := mustPostgresMigrations(t)
	var subject *PostgresServerMigration
	for _, migration := range migrations {
		if migration.Version() == 109 {
			subject = migration
			break
		}
	}
	if subject == nil {
		t.Fatal("migration 109 not registered")
	}
	for _, migration := range migrations {
		if migration.Version() >= 109 {
			continue
		}
		if err := migration.Apply(context.Background(), db); err != nil {
			t.Fatalf("apply prerequisite %d: %v", migration.Version(), err)
		}
	}
	state, err := subject.Preflight(context.Background(), db)
	if err != nil || state.Ledgered {
		t.Fatalf("109 preflight state=%+v err=%v; want unledgered", state, err)
	}
	if err := subject.Apply(context.Background(), db); err != nil {
		t.Fatalf("apply 109: %v", err)
	}
	if err := subject.VerifyApplied(context.Background(), db); err != nil {
		t.Fatalf("verify 109: %v", err)
	}
	if err := subject.Apply(context.Background(), db); err != nil {
		t.Fatalf("idempotent reapply 109: %v", err)
	}

	for _, table := range []string{"scoped_code_symbols", "scoped_code_relations", "scoped_code_index_state"} {
		var enabled, forced bool
		if err := db.QueryRowContext(context.Background(),
			`SELECT relrowsecurity, relforcerowsecurity FROM pg_class WHERE oid=to_regclass($1)`,
			"public."+table).Scan(&enabled, &forced); err != nil || !enabled || !forced {
			t.Errorf("%s RLS enabled=%v forced=%v err=%v", table, enabled, forced, err)
		}
		var appSelect bool
		if err := db.QueryRowContext(context.Background(),
			`SELECT has_table_privilege('cortex_app',$1,'SELECT')`, "public."+table).Scan(&appSelect); err != nil || !appSelect {
			t.Errorf("%s cortex_app SELECT=%v err=%v", table, appSelect, err)
		}
	}
	if err := subject.Down(context.Background(), db); err == nil || !errors.Is(err, ErrForwardOnly) {
		t.Fatalf("Down(109) err=%v; want ErrForwardOnly", err)
	}
}
