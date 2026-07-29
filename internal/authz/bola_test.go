package authz

import (
	"context"
	"testing"

	"github.com/lleontor705/cortex/internal/domain"
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
