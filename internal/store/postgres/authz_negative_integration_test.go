//go:build postgres_integration

package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresNonSuperuserPrincipalBindingAndIsolation(t *testing.T) {
	h := newPostgresHarness(t)
	ctx := context.Background()
	tenantA, tenantB, actor := uuid.New(), uuid.New(), uuid.New()
	for _, tenant := range []uuid.UUID{tenantA, tenantB} {
		if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, tenant.String()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO actor_subjects(tenant_id,subject,actor_type,public_id,grant_digest,grant_version) VALUES($1,$2,'user',$3,'digest-a',7)`, tenantA, actor.String(), actor); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO observations(tenant_id,session_id,type,title,content) VALUES($1,0,'manual','a','a'),($2,0,'manual','b','b')`, tenantA, tenantB); err == nil {
		// The FK intentionally rejects this fixture; isolation is checked below
		// using catalog-visible tenant predicates instead of bypassing the schema.
	}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT public.cortex_bind_principal($1,$2,$3)`, actor, "digest-a", 7); err != nil {
		t.Fatal(err)
	}
	var current uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT public.cortex_current_tenant()`).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if current != tenantA {
		t.Fatalf("bound tenant=%s, want %s", current, tenantA)
	}
	expectRejected := func(sql string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(ctx, `SAVEPOINT authz_negative`); err != nil {
			t.Fatal(err)
		}
		_, err := tx.Exec(ctx, sql, args...)
		if err == nil {
			t.Fatal("negative authorization probe unexpectedly succeeded")
		}
		if _, rollbackErr := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT authz_negative`); rollbackErr != nil {
			t.Fatal(rollbackErr)
		}
	}
	expectRejected(`SELECT public.cortex_set_tenant($1)`, tenantB)
	expectRejected(`SELECT public.cortex_bind_principal($1,$2,$3)`, actor, "stale", 7)
	expectRejected(`SELECT public.cortex_bind_principal($1,$2,$3)`, actor, "digest-a", 8)
	var visible int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM observations WHERE tenant_id=$1`, tenantB).Scan(&visible); err != nil {
		t.Fatal(err)
	}
	if visible != 0 {
		t.Fatalf("cross-tenant rows visible to app role: %d", visible)
	}
	adminDSN := os.Getenv("CORTEX_TEST_POSTGRES_AUTHZ_ADMIN_DSN")
	if adminDSN == "" {
		t.Fatal("CORTEX_TEST_POSTGRES_AUTHZ_ADMIN_DSN is required for the non-superuser admin authorization probe")
	}
	adminConfig, err := pgxpool.ParseConfig(adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	adminProbe, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { adminProbe.Close() })
	if err := adminProbe.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	var superuser bool
	if err := adminProbe.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname=current_user`).Scan(&superuser); err != nil {
		t.Fatal(err)
	}
	if superuser {
		t.Fatalf("authorization admin probe login %q must be NOSUPERUSER", adminConfig.ConnConfig.User)
	}
	adminTx, err := adminProbe.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adminTx.Rollback(ctx) })
	if _, err := adminTx.Exec(ctx, `SELECT public.cortex_bind_principal($1,$2,$3)`, actor, "digest-a", 7); err == nil {
		t.Fatal("cortex_admin login unexpectedly executed app-only principal binder")
	}
}
