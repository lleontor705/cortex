package authz

import (
	"context"
	"testing"

	"github.com/lleontor705/cortex/internal/domain"
)

func principal(role Role, scopes ...string) domain.Principal {
	return domain.Principal{Subject: "u1", Type: "user", OrgID: "tenant-a", Roles: []string{string(role)}, Scopes: scopes, WorkspaceIDs: []string{"ws-a"}}
}

func TestPolicyRoleMatrix(t *testing.T) {
	p := NewPolicy()
	tests := []struct {
		role     Role
		resource Resource
		action   Action
		want     bool
	}{
		{RoleOwner, ResourceMemory, ActionWrite, true}, {RoleAdmin, ResourceUsers, ActionWrite, true},
		{RoleMember, ResourceMemory, ActionWrite, true}, {RoleMember, ResourceTokens, ActionWrite, false},
		{RoleViewer, ResourceMemory, ActionRead, true}, {RoleViewer, ResourceMemory, ActionWrite, false},
		{RoleServiceAccount, ResourceMemory, ActionRead, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.role)+string(tt.resource)+string(tt.action), func(t *testing.T) {
			d := p.Authorize(context.Background(), Request{Principal: principal(tt.role), Tenant: Tenant{ID: "tenant-a", WorkspaceID: "ws-a"}, Resource: ResourceRef{TenantID: "tenant-a", WorkspaceID: "ws-a"}, ResourceType: tt.resource, Action: tt.action})
			if d.Allowed != tt.want {
				t.Fatalf("allowed=%v reason=%s want %v", d.Allowed, d.Reason, tt.want)
			}
		})
	}
}

func TestPolicyDefaultDenyAndTenantSpoof(t *testing.T) {
	p := NewPolicy()
	d := p.Authorize(context.Background(), Request{Principal: principal(RoleOwner), Tenant: Tenant{ID: "tenant-a"}, Resource: ResourceRef{TenantID: "tenant-b"}, ResourceType: ResourceMemory, Action: ActionRead})
	if d.Allowed || d.Reason != DenyTenantMismatch {
		t.Fatalf("decision=%+v", d)
	}
	d = p.Authorize(context.Background(), Request{Principal: domain.Principal{}, ResourceType: ResourceMemory, Action: ActionRead})
	if d.Allowed || d.Reason != DenyUnauthenticated {
		t.Fatalf("decision=%+v", d)
	}
}

func TestServiceAccountScopeAndABAC(t *testing.T) {
	p := NewPolicy()
	base := Request{Principal: principal(RoleServiceAccount, "memory:read", "project:proj-a"), Tenant: Tenant{ID: "tenant-a"}, Resource: ResourceRef{TenantID: "tenant-a", ProjectID: "proj-a"}, ResourceType: ResourceMemory, Action: ActionRead}
	if !p.Authorize(context.Background(), base).Allowed {
		t.Fatal("scoped service account should read")
	}
	base.Resource.ProjectID = "proj-b"
	if p.Authorize(context.Background(), base).Allowed {
		t.Fatal("project scope escaped")
	}
}

func TestDeriveContextIgnoresClientTenant(t *testing.T) {
	c, err := DeriveTenantContext(principal(RoleMember), Tenant{ID: "client-spoof"})
	if err != nil || c.TenantID != "tenant-a" {
		t.Fatalf("context=%+v err=%v", c, err)
	}
}

func TestOpaqueResolverIsTenantScoped(t *testing.T) {
	r := NewOpaqueResolver()
	r.Put("tenant-a", "memory", "mem-a", "internal-a")
	if _, err := r.Resolve("tenant-b", "memory", "mem-a"); err != ErrResourceNotFound {
		t.Fatalf("err=%v", err)
	}
	if got, err := r.Resolve("tenant-a", "memory", "mem-a"); err != nil || got != "internal-a" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}
