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

func TestTokenAdminBoundary(t *testing.T) {
	admin := principal(RoleAdmin)
	for _, action := range []Action{ActionRead, ActionWrite, ActionManage} {
		d := NewPolicy().Authorize(context.Background(), Request{Principal: admin, Tenant: Tenant{ID: "tenant-a", WorkspaceID: "ws-a"}, Resource: ResourceRef{TenantID: "tenant-a", WorkspaceID: "ws-a"}, ResourceType: ResourceTokens, Action: action})
		if !d.Allowed {
			t.Fatalf("admin token %s denied: %+v", action, d)
		}
	}
	member := principal(RoleMember)
	if d := NewPolicy().Authorize(context.Background(), Request{Principal: member, Tenant: Tenant{ID: "tenant-a", WorkspaceID: "ws-a"}, Resource: ResourceRef{TenantID: "tenant-a", WorkspaceID: "ws-a"}, ResourceType: ResourceTokens, Action: ActionManage}); d.Allowed {
		t.Fatal("member escalated to token administration")
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

func TestPolicyABACBoundaries(t *testing.T) {
	p := NewPolicy()
	base := Request{Principal: principal(RoleMember, "project:p"), Tenant: Tenant{ID: "tenant-a", WorkspaceID: "ws-a"}, Resource: ResourceRef{TenantID: "tenant-a", WorkspaceID: "ws-a", ProjectID: "p"}, ResourceType: ResourceMemory, Action: ActionRead}
	for name, mutate := range map[string]func(*Request){
		"workspace":      func(r *Request) { r.Resource.WorkspaceID = "ws-b" },
		"ownership":      func(r *Request) { r.Resource.OwnerSubject = "other"; r.Resource.Classification = "personal" },
		"classification": func(r *Request) { r.Resource.Classification = "restricted" },
	} {
		t.Run(name, func(t *testing.T) {
			r := base
			mutate(&r)
			if d := p.Authorize(context.Background(), r); d.Allowed {
				t.Fatalf("boundary %s unexpectedly allowed", name)
			}
		})
	}
}

func TestExplicitProjectAndClassificationGrants(t *testing.T) {
	p := NewPolicy()
	principal := domain.Principal{Subject: "u", Type: "user", OrgID: "t", Roles: []string{string(RoleMember)}, WorkspaceIDs: []string{"w"}, ProjectIDs: []string{"p1"}, ClassificationClearance: []string{"restricted"}}
	base := Request{Principal: principal, Tenant: Tenant{ID: "t", WorkspaceID: "w"}, Resource: ResourceRef{TenantID: "t", WorkspaceID: "w", ProjectID: "p1", Classification: "restricted"}, ResourceType: ResourceMemory, Action: ActionRead}
	if d := p.Authorize(context.Background(), base); !d.Allowed {
		t.Fatalf("granted resource denied: %+v", d)
	}
	base.Resource.ProjectID = "p2"
	if d := p.Authorize(context.Background(), base); d.Allowed || d.Reason != DenyProject {
		t.Fatalf("ungranted project decision=%+v", d)
	}
	base.Resource.ProjectID, base.Resource.Classification = "p1", "confidential"
	if d := p.Authorize(context.Background(), base); d.Allowed || d.Reason != DenyClassification {
		t.Fatalf("uncleared classification decision=%+v", d)
	}
}
