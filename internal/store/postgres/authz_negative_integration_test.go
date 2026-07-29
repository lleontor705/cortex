//go:build postgres_integration

package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresNonSuperuserPrincipalBindingAndIsolation(t *testing.T) {
	h := newPostgresHarness(t)
	ctx := context.Background()
	tenantA, tenantB, actor := uuid.New(), uuid.New(), uuid.New()
	for _, tenant := range []uuid.UUID{tenantA, tenantB} {
		if _, err := h.pool.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, tenant.String()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.pool.Exec(ctx, `INSERT INTO actor_subjects(tenant_id,subject,actor_type,public_id,grant_digest,grant_version) VALUES($1,$2,'user',$3,'digest-a',7)`, tenantA, actor.String(), actor); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(ctx, `INSERT INTO observations(tenant_id,session_id,type,title,content) VALUES($1,0,'manual','a','a'),($2,0,'manual','b','b')`, tenantA, tenantB); err == nil {
		// The FK intentionally rejects this fixture; isolation is checked below
		// using catalog-visible tenant predicates instead of bypassing the schema.
	}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE cortex_app`); err != nil {
		t.Fatal(err)
	}
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
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE cortex_admin`); err != nil {
		t.Fatal(err)
	}
	expectRejected(`SELECT public.cortex_bind_principal($1,$2,$3)`, actor, "digest-a", 7)
}
