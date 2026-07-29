package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/lleontor705/cortex/internal/domain"
)

func TestValidateTenantContext(t *testing.T) {
	id := uuid.NewString()
	if err := validateTenantContext(&domain.TenantContext{TenantID: id, WorkspaceID: uuid.NewString()}); err != nil {
		t.Fatalf("valid context rejected: %v", err)
	}
	for _, tc := range []*domain.TenantContext{nil, {}, {TenantID: "not-a-uuid"}} {
		if err := validateTenantContext(tc); err == nil {
			t.Fatalf("invalid context %v accepted", tc)
		}
	}
}

func TestTenantContextRoundTrip(t *testing.T) {
	ctx := withTenant(context.Background(), &domain.TenantContext{TenantID: uuid.NewString()})
	got, ok := tenantFromContext(ctx)
	if !ok || got.TenantID == "" {
		t.Fatal("tenant context was not retained")
	}
}

func TestPrincipalMustOwnTenantAndWorkspace(t *testing.T) {
	p := domain.Principal{Subject: "opaque-subject", OrgID: uuid.NewString(), WorkspaceIDs: []string{uuid.NewString()}}
	tenant := &domain.TenantContext{TenantID: uuid.NewString(), WorkspaceID: p.WorkspaceIDs[0]}
	if _, err := NewStore(nil, tenant, p); err == nil {
		t.Fatal("nil pool must be rejected before tenant checks")
	}
	// Validation is deliberately independent of subject representation.
	if err := validatePrincipal(p); err != nil {
		t.Fatalf("opaque subject rejected: %v", err)
	}
	if err := validateTenantContext(&domain.TenantContext{TenantID: p.OrgID, WorkspaceID: p.WorkspaceIDs[0]}); err != nil {
		t.Fatal(err)
	}
}

func TestActorMappingDoesNotCastOpaqueSubject(t *testing.T) {
	ctx := context.WithValue(context.Background(), actorKey{}, uuid.New())
	if actorFromContext(ctx) == nil {
		t.Fatal("resolved actor was lost from transaction context")
	}
	if actorFromContext(context.Background()) != nil {
		t.Fatal("unresolved actor must not become a SQL value")
	}
}
