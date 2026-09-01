package authz

import (
	"context"
	"errors"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

type failingAudit struct{}

func (failingAudit) Record(context.Context, AuditEvent) error { return errors.New("audit unavailable") }

func TestPrivilegedAuthorizationFailsClosedWhenAuditFails(t *testing.T) {
	p := &Policy{Audit: failingAudit{}}
	req := Request{Principal: domain.Principal{Subject: "actor", OrgID: "tenant", Roles: []string{"owner"}}, Tenant: Tenant{ID: "tenant"}, ResourceType: ResourceMemory, Action: ActionWrite}
	if err := Enforce(context.Background(), p, req); err == nil {
		t.Fatal("privileged operation allowed after audit failure")
	}
}

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

func TestCodeCapabilityRoleMatrix(t *testing.T) {
	p := NewPolicy()
	tests := []struct {
		name   string
		actor  domain.Principal
		action Action
		want   bool
	}{
		{name: "owner reads", actor: principal(RoleOwner), action: ActionRead, want: true},
		{name: "admin reads", actor: principal(RoleAdmin), action: ActionRead, want: true},
		{name: "member reads", actor: principal(RoleMember), action: ActionRead, want: true},
		{name: "developer reads", actor: principal(RoleDeveloper), action: ActionRead, want: true},
		{name: "agent reads", actor: principal(RoleAgent), action: ActionRead, want: true},
		{name: "viewer reads", actor: principal(RoleViewer), action: ActionRead, want: true},
		{name: "owner writes", actor: principal(RoleOwner), action: ActionWrite, want: true},
		{name: "owner manages", actor: principal(RoleOwner), action: ActionManage, want: true},
		{name: "admin writes", actor: principal(RoleAdmin), action: ActionWrite, want: true},
		{name: "admin manages", actor: principal(RoleAdmin), action: ActionManage, want: true},
		{name: "member cannot write", actor: principal(RoleMember), action: ActionWrite, want: false},
		{name: "member cannot manage", actor: principal(RoleMember), action: ActionManage, want: false},
		{name: "developer cannot write", actor: principal(RoleDeveloper), action: ActionWrite, want: false},
		{name: "developer cannot manage", actor: principal(RoleDeveloper), action: ActionManage, want: false},
		{name: "agent cannot write", actor: principal(RoleAgent), action: ActionWrite, want: false},
		{name: "agent cannot manage", actor: principal(RoleAgent), action: ActionManage, want: false},
		{name: "viewer cannot write", actor: principal(RoleViewer), action: ActionWrite, want: false},
		{name: "viewer cannot manage", actor: principal(RoleViewer), action: ActionManage, want: false},
		{name: "unscoped service account cannot read", actor: principal(RoleServiceAccount), action: ActionRead, want: false},
		{name: "scoped service account reads", actor: principal(RoleServiceAccount, "code:read"), action: ActionRead, want: true},
		{name: "read scoped service account cannot write", actor: principal(RoleServiceAccount, "code:read"), action: ActionWrite, want: false},
		{name: "write scoped service account writes", actor: principal(RoleServiceAccount, "code:write"), action: ActionWrite, want: true},
		{name: "write scoped service account cannot manage", actor: principal(RoleServiceAccount, "code:write"), action: ActionManage, want: false},
		{name: "manage scoped service account manages", actor: principal(RoleServiceAccount, "code:manage"), action: ActionManage, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := p.Authorize(context.Background(), Request{
				Principal:    tt.actor,
				Tenant:       Tenant{ID: "tenant-a", WorkspaceID: "ws-a"},
				Resource:     ResourceRef{TenantID: "tenant-a", WorkspaceID: "ws-a"},
				ResourceType: ResourceCode,
				Action:       tt.action,
			})
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
