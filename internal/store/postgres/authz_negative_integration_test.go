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
	// The privileged handle provisions the actor with a configured stored
	// digest at version 7 plus a live harness token; every application-role
	// bind must present the token-bound provenance minted from it.
	_, provenance := mintBindingProvenance(t, h, tenantA, actor, 7, "digest-a")
	if _, err := h.admin.Exec(ctx, `INSERT INTO observations(tenant_id,session_id,type,title,content) VALUES($1,0,'manual','a','a'),($2,0,'manual','b','b')`, tenantA, tenantB); err == nil {
		// The composite session FK must reject this unscoped fixture;
		// isolation is checked below using catalog-visible tenant
		// predicates instead of bypassing the schema.
		t.Fatal("unscoped observations insert unexpectedly succeeded; want session FK rejection")
	}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
			t.Errorf("rollback binding transaction: %v", rollbackErr)
		}
	}()
	if _, err := tx.Exec(ctx, `SELECT public.cortex_bind_principal($1,$2,$3)`, actor, provenance, 7); err != nil {
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
	// The configured stored digest is integrity metadata only and never
	// authenticates a bind under the mediated contract.
	expectRejected(`SELECT public.cortex_bind_principal($1,$2,$3)`, actor, "digest-a", 7)
	// A well-formed proof at a stale grant version fails closed.
	expectRejected(`SELECT public.cortex_bind_principal($1,$2,$3)`, actor, provenance, 8)
	// A tampered MAC fails closed.
	tampered := provenance[:len(provenance)-1]
	if last := provenance[len(provenance)-1]; last == '0' {
		tampered += "1"
	} else {
		tampered += "0"
	}
	expectRejected(`SELECT public.cortex_bind_principal($1,$2,$3)`, actor, tampered, 7)
	// A proof minted for a different actor of the same tenant is foreign.
	foreignActor := uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO app_users(tenant_id,public_id,email,display_name) VALUES($1,$2,$3,$4)`, tenantA, foreignActor, "foreign@authz.test", "foreign"); err != nil {
		t.Fatal(err)
	}
	_, foreignProvenance := mintBindingProvenance(t, h, tenantA, foreignActor, 7, "digest-a")
	expectRejected(`SELECT public.cortex_bind_principal($1,$2,$3)`, actor, foreignProvenance, 7)
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
	if _, err := adminTx.Exec(ctx, `SELECT public.cortex_bind_principal($1,$2,$3)`, actor, provenance, 7); err == nil {
		t.Fatal("cortex_admin login unexpectedly executed app-only principal binder")
	}
}
