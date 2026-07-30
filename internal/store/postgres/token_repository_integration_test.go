//go:build postgres_integration

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/lleontor705/cortex/internal/identity"
)

func TestPostgresTokenRepositoryLifecycleAndRLS(t *testing.T) {
	h := newPostgresHarness(t)
	ctx := context.Background()
	tenant, sa := uuid.New(), uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, "token-org"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO service_accounts(tenant_id,public_id,name) VALUES($1,$2,$3)`, tenant, sa, "token-service"); err != nil {
		t.Fatal(err)
	}
	store := newAuthorizedTestStore(t, h, tenant, uuid.Nil, uuid.New())
	issued, err := store.tokens().Issue(ctx, identity.TokenIssue{Subject: sa.String(), PrincipalType: "service_account", OrgID: tenant.String(), Scopes: []string{"read"}, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.tokens().Verify(ctx, issued.Secret, "read"); err != nil {
		t.Fatal(err)
	}
	verifyTx, err := store.store.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var lastUsed time.Time
	if err := verifyTx.Handle().(pgx.Tx).QueryRow(ctx, `SELECT last_used_at FROM api_tokens WHERE public_id=$1::uuid`, issued.Record.ID).Scan(&lastUsed); err != nil || lastUsed.IsZero() {
		t.Fatalf("last_used_at not persisted: %v %v", lastUsed, err)
	}
	if err := verifyTx.Rollback(); err != nil {
		t.Fatal(err)
	}
	rotated, err := store.tokens().Rotate(ctx, issued.Record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.tokens().Verify(ctx, issued.Secret, "read"); err == nil {
		t.Fatal("rotated token remained valid")
	}
	if _, err := store.tokens().Verify(ctx, rotated.Secret, "read"); err != nil {
		t.Fatal(err)
	}
	if err := store.tokens().Revoke(ctx, rotated.Record.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.tokens().Verify(ctx, rotated.Secret, "read"); err == nil {
		t.Fatal("revoked token remained valid")
	}
	other := uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, other, "other"); err != nil {
		t.Fatal(err)
	}
	otherStore := newAuthorizedTestStore(t, h, other, uuid.Nil, uuid.New())
	if _, err := otherStore.tokens().Verify(ctx, rotated.Secret, "read"); err == nil {
		t.Fatal("cross-tenant token accepted")
	}
}
