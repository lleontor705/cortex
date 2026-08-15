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
	"github.com/jackc/pgx/v5/pgconn"
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

	failing := &PostgresServerMigration{version: 106, name: "injected_failure", sql: `CREATE TABLE migration_106_rolled_back(id integer); SELECT 1/0`, checksum: "injected"}
	if err := failing.Apply(ctx, db); err == nil {
		t.Fatal("failed DDL unexpectedly succeeded")
	}
	var tableExists, ledgerExists bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('public.migration_106_rolled_back') IS NOT NULL`).Scan(&tableExists); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cortex_server_migrations WHERE version=106)`).Scan(&ledgerExists); err != nil {
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
		if _, err := db.ExecContext(ctx, `INSERT INTO cortex_server_migrations(version,name,checksum) VALUES(106,'future','future')`); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = db.ExecContext(ctx, `DELETE FROM cortex_server_migrations WHERE version=106`) })
		if err := migrations[0].Apply(ctx, db); err == nil || !errors.Is(err, ErrFutureMigration) {
			t.Fatalf("Apply(100) with future ledger version 106 err=%v; want errors.Is ErrFutureMigration", err)
		}
		if err := ApplyPostgresServerMigrations(ctx, db); err == nil || !errors.Is(err, ErrFutureMigration) {
			t.Fatalf("ApplyPostgresServerMigrations with future ledger version 106 err=%v; want errors.Is ErrFutureMigration", err)
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
		t.Fatalf("fresh apply 100-105: %v", err)
	}
	var ledgerCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM cortex_server_migrations WHERE version BETWEEN 100 AND 105`).Scan(&ledgerCount); err != nil || ledgerCount != 6 {
		t.Fatalf("ledger count=%d err=%v; want 6", ledgerCount, err)
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
		var indexDef string
		if err := db.QueryRowContext(ctx, `SELECT indexdef FROM pg_indexes WHERE schemaname='public' AND indexname='observations_topic_key_active_uq'`).Scan(&indexDef); err != nil {
			t.Fatalf("topic index missing: %v", err)
		}
		if !strings.Contains(indexDef, "tenant_id, workspace_id, project_key, topic_key") || !strings.Contains(indexDef, "WHERE topic_key IS NOT NULL AND deleted_at IS NULL") {
			t.Fatalf("topic index not workspace scoped: %s", indexDef)
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
		defer abortDB.Close()
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
		defer abortDB.Close()
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
		defer abortDB.Close()
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
