package authz

import (
	"context"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

func TestEnforceBOLA(t *testing.T) {
	p := NewPolicy()
	ctx := context.Background()
	owner := domain.Principal{Subject: "u1", OrgID: "t1", Roles: []string{string(RoleMember)}, WorkspaceIDs: []string{"w1"}}
	if err := Enforce(ctx, p, Request{Principal: owner, Tenant: Tenant{ID: "t1", WorkspaceID: "w1"}, Resource: ResourceRef{TenantID: "t1", WorkspaceID: "w1", OwnerSubject: "u1"}, ResourceType: ResourceMemory, Action: ActionWrite}); err != nil {
		t.Fatal(err)
	}
	if err := Enforce(ctx, p, Request{Principal: owner, Tenant: Tenant{ID: "t1", WorkspaceID: "w1"}, Resource: ResourceRef{TenantID: "t2", WorkspaceID: "w1"}, ResourceType: ResourceMemory, Action: ActionRead}); err == nil || err.Error() != DenyTenantMismatch {
		t.Fatalf("err=%v", err)
	}
}

func TestAuthorizedContextRequiresDecision(t *testing.T) {
	p := NewPolicy()
	c, err := NewAuthorizedContext(context.Background(), p, Request{Principal: domain.Principal{Subject: "u", OrgID: "t", Roles: []string{string(RoleViewer)}}, Tenant: Tenant{ID: "t"}, ResourceType: ResourceMemory, Action: ActionRead})
	if err != nil || c.Principal.Subject != "u" {
		t.Fatalf("ctx=%+v err=%v", c, err)
	}
}

func TestProjectGrantIsRequiredEvenForTenantAdmin(t *testing.T) {
	p := domain.Principal{Subject: "u", OrgID: "t", Roles: []string{string(RoleAdmin)}, WorkspaceIDs: []string{"w"}}
	req := Request{Principal: p, Tenant: Tenant{ID: "t", WorkspaceID: "w"}, Resource: ResourceRef{TenantID: "t", WorkspaceID: "w", ProjectID: "p"}, ResourceType: ResourceMemory, Action: ActionRead}
	if got := NewPolicy().Authorize(context.Background(), req); got.Allowed || got.Reason != DenyProject {
		t.Fatalf("decision=%+v, want project denial without a principal grant", got)
	}
	p.Scopes = []string{"project:p"}
	req.Principal = p
	if got := NewPolicy().Authorize(context.Background(), req); !got.Allowed {
		t.Fatalf("decision=%+v, want explicit project grant", got)
	}
}

func TestCodeReadRequiresExplicitProjectGrant(t *testing.T) {
	p := domain.Principal{Subject: "u", OrgID: "t", Roles: []string{string(RoleDeveloper)}, WorkspaceIDs: []string{"w"}}
	req := Request{Principal: p, Tenant: Tenant{ID: "t", WorkspaceID: "w"}, Resource: ResourceRef{TenantID: "t", WorkspaceID: "w", ProjectID: "p"}, ResourceType: ResourceCode, Action: ActionRead}
	if got := NewPolicy().Authorize(context.Background(), req); got.Allowed || got.Reason != DenyProject {
		t.Fatalf("decision=%+v, want project denial without a principal grant", got)
	}
	p.ProjectIDs = []string{"p"}
	req.Principal = p
	if got := NewPolicy().Authorize(context.Background(), req); !got.Allowed {
		t.Fatalf("decision=%+v, want explicit project grant", got)
	}
}

func TestScopedCodeWriterCannotEscapeProjectGrant(t *testing.T) {
	p := domain.Principal{Subject: "svc", Type: "service_account", OrgID: "t", Roles: []string{string(RoleServiceAccount)}, WorkspaceIDs: []string{"w"}, Scopes: []string{"code:write", "project:p1"}}
	req := Request{Principal: p, Tenant: Tenant{ID: "t", WorkspaceID: "w"}, Resource: ResourceRef{TenantID: "t", WorkspaceID: "w", ProjectID: "p2"}, ResourceType: ResourceCode, Action: ActionWrite}
	if got := NewPolicy().Authorize(context.Background(), req); got.Allowed || got.Reason != DenyProject {
		t.Fatalf("decision=%+v, want project denial outside explicit service-account scope", got)
	}
	req.Resource.ProjectID = "p1"
	if got := NewPolicy().Authorize(context.Background(), req); !got.Allowed {
		t.Fatalf("decision=%+v, want write inside explicit service-account scope", got)
	}
}

func TestEnforceFailsClosedForNilAuthorizer(t *testing.T) {
	if err := Enforce(context.Background(), nil, Request{}); err == nil || err.Error() != DenyRole {
		t.Fatalf("error=%v, want fail-closed role denial", err)
	}
}
