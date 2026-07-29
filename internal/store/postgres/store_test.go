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
