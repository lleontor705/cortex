package authz

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

func TestPolicyRejectsTenantWorkspaceProjectOwnershipAndClassificationViolations(t *testing.T) {
	base := Request{
		Principal:    domain.Principal{Subject: "actor", OrgID: "tenant", Type: "user", Roles: []string{"member"}, WorkspaceIDs: []string{"workspace"}, ProjectIDs: []string{"project"}},
		Tenant:       Tenant{ID: "tenant", WorkspaceID: "workspace", ProjectID: "project"},
		Resource:     ResourceRef{TenantID: "tenant", WorkspaceID: "workspace", ProjectID: "project"},
		ResourceType: ResourceMemory, Action: ActionRead,
	}
	for name, mutate := range map[string]func(*Request){
		"tenant":         func(r *Request) { r.Resource.TenantID = "other" },
		"workspace":      func(r *Request) { r.Resource.WorkspaceID = "other" },
		"project":        func(r *Request) { r.Resource.ProjectID = "other" },
		"owner":          func(r *Request) { r.Resource.OwnerSubject = "other"; r.Resource.Classification = "personal" },
		"classification": func(r *Request) { r.Resource.Classification = "confidential" },
	} {
		t.Run(name, func(t *testing.T) {
			req := base
			mutate(&req)
			d := NewPolicy().Authorize(context.Background(), req)
			if d.Allowed {
				t.Fatalf("boundary %s unexpectedly allowed", name)
			}
		})
	}
}

func TestPolicyAuditModesAndMemoryAuditRedaction(t *testing.T) {
	failed := &Policy{Audit: failingAudit{}}
	readReq := Request{Principal: domain.Principal{Subject: "actor", OrgID: "tenant", Roles: []string{"owner"}}, Tenant: Tenant{ID: "tenant"}, Resource: ResourceRef{TenantID: "tenant"}, ResourceType: ResourceMemory, Action: ActionRead}
	if _, err := failed.AuthorizeWithAudit(context.Background(), readReq); err != nil {
		t.Fatalf("read audit failure should be best effort: %v", err)
	}
	if _, err := failed.AuthorizeWithAudit(context.Background(), Request{Principal: readReq.Principal, Tenant: readReq.Tenant, Resource: readReq.Resource, ResourceType: ResourceMemory, Action: ActionDelete}); err == nil {
		t.Fatal("delete audit failure was not fail-closed")
	}
	audit := &MemoryAudit{}
	if err := audit.Record(context.Background(), AuditEvent{Actor: strings.Repeat("a", 300), Resource: strings.Repeat("b", 300)}); err != nil {
		t.Fatal(err)
	}
	if err := audit.Record(context.Background(), AuditEvent{Actor: "short", Resource: "resource"}); err != nil {
		t.Fatal(err)
	}
	if len(audit.Events) != 2 || len(audit.Events[0].Actor) != 256 || len(audit.Events[0].Resource) != 256 || audit.Events[1].Actor != "short" {
		t.Fatalf("audit redaction=%+v", audit.Events)
	}
}

func TestAuthorizedContextRoundTripAndInvalidPrincipal(t *testing.T) {
	ctx := context.Background()
	req := Request{Principal: domain.Principal{Subject: "actor", OrgID: "tenant", GrantDigest: "digest", Roles: []string{"viewer"}}, Tenant: Tenant{ID: "tenant"}, Resource: ResourceRef{TenantID: "tenant"}, ResourceType: ResourceMemory, Action: ActionRead}
	ac, err := NewAuthorizedContext(ctx, NewPolicy(), req)
	if err != nil || ac.Tenant.TenantID != "tenant" {
		t.Fatalf("authorized context=%+v err=%v", ac, err)
	}
	stored := WithAuthorizedContext(ctx, ac)
	if got, ok := AuthorizedFromContext(stored); !ok || got.GrantDigest != "digest" {
		t.Fatalf("context value=%+v ok=%v", got, ok)
	}
	if _, ok := AuthorizedFromContext(ctx); ok {
		t.Fatal("empty context unexpectedly contained authorization")
	}
	if _, err := NewAuthorizedContext(ctx, NewPolicy(), Request{ResourceType: ResourceMemory, Action: ActionRead}); !errors.Is(err, errors.New(DenyUnauthenticated)) && (err == nil || err.Error() != DenyUnauthenticated) {
		t.Fatalf("invalid principal error=%v", err)
	}
}

func TestPolicyDefaultDenyReasonsAndWildcardGrants(t *testing.T) {
	p := NewPolicy()
	base := Request{Principal: domain.Principal{Subject: "actor", OrgID: "tenant", Roles: []string{"member"}}, Tenant: Tenant{ID: "tenant"}, Resource: ResourceRef{TenantID: "tenant"}, ResourceType: ResourceMemory, Action: ActionRead}
	cases := []struct {
		name   string
		mutate func(*Request)
		want   string
	}{
		{"unknown action", func(r *Request) { r.Action = "" }, DenyUnknownAction},
		{"missing tenant", func(r *Request) { r.Tenant.ID = "" }, DenyTenantMismatch},
		{"workspace grant", func(r *Request) { r.Tenant.WorkspaceID = "workspace" }, DenyWorkspace},
		{"project mismatch", func(r *Request) { r.Tenant.ProjectID = "a"; r.Resource.ProjectID = "b" }, DenyProject},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			tc.mutate(&r)
			if got := p.Authorize(context.Background(), r); got.Reason != tc.want {
				t.Fatalf("reason=%q want %q", got.Reason, tc.want)
			}
		})
	}
	wildcard := base
	wildcard.Principal.ProjectIDs = []string{"*"}
	wildcard.Principal.ClassificationClearance = []string{"*"}
	wildcard.Resource.ProjectID = "new-project"
	wildcard.Resource.Classification = "restricted"
	if got := p.Authorize(context.Background(), wildcard); !got.Allowed {
		t.Fatalf("wildcard grants denied: %+v", got)
	}
	service := wildcard
	service.Principal = domain.Principal{Subject: "svc", OrgID: "tenant", Type: "service_account", Scopes: []string{"memory:read", "project:new-project"}}
	service.Resource.Classification = ""
	if got := p.Authorize(context.Background(), service); !got.Allowed {
		t.Fatalf("scoped service account denied: %+v", got)
	}
}

func TestTenantDerivationUsesOnlyGrantedWorkspace(t *testing.T) {
	p := domain.Principal{Subject: "actor", OrgID: "tenant", WorkspaceIDs: []string{"workspace-a", "workspace-b"}}
	if got, err := DeriveTenantContext(p, Tenant{ID: "spoof", WorkspaceID: "workspace-b"}); err != nil || got.TenantID != "tenant" || got.WorkspaceID != "workspace-b" {
		t.Fatalf("granted workspace context=%+v err=%v", got, err)
	}
	if got, err := DeriveTenantContext(p, Tenant{WorkspaceID: "ungranted"}); err != nil || got.WorkspaceID != "workspace-a" {
		t.Fatalf("fallback workspace context=%+v err=%v", got, err)
	}
	if _, err := DeriveTenantContext(domain.Principal{}, Tenant{}); err == nil {
		t.Fatal("invalid principal derived a tenant")
	}
}

func TestServiceAccountAndClassificationDefaults(t *testing.T) {
	base := Request{Principal: domain.Principal{Subject: "svc", OrgID: "tenant", Type: "service_account", Scopes: []string{"*"}}, Tenant: Tenant{ID: "tenant"}, Resource: ResourceRef{TenantID: "tenant"}, ResourceType: ResourceMemory, Action: ActionRead}
	if got := NewPolicy().Authorize(context.Background(), base); !got.Allowed {
		t.Fatalf("wildcard service account denied: %+v", got)
	}
	base.Principal = domain.Principal{Subject: "member", OrgID: "tenant", Roles: []string{"member"}}
	base.Resource.Classification = "public"
	if got := NewPolicy().Authorize(context.Background(), base); !got.Allowed {
		t.Fatalf("public classification denied: %+v", got)
	}
	base.Resource.OwnerSubject = "other"
	base.Resource.Classification = "personal"
	if got := NewPolicy().Authorize(context.Background(), base); got.Allowed || got.Reason != DenyOwnership {
		t.Fatalf("ownership boundary decision=%+v", got)
	}
}

type auditedStub struct {
	decision Decision
	err      error
}

func (a auditedStub) Authorize(context.Context, Request) Decision { return a.decision }
func (a auditedStub) AuthorizeWithAudit(context.Context, Request) (Decision, error) {
	return a.decision, a.err
}

func TestEnforceAuditedAuthorizerBranches(t *testing.T) {
	req := Request{ResourceType: ResourceMemory, Action: ActionRead}
	if err := Enforce(context.Background(), auditedStub{decision: Decision{Allowed: true}}, req); err != nil {
		t.Fatalf("allowed audited request rejected: %v", err)
	}
	if err := Enforce(context.Background(), auditedStub{decision: Decision{Reason: DenyRole}}, req); err == nil || err.Error() != DenyRole {
		t.Fatalf("denied audited request error=%v", err)
	}
	if err := Enforce(context.Background(), auditedStub{err: errors.New("audit failure")}, req); err == nil {
		t.Fatal("audited authorizer error was ignored")
	}
}

func TestPolicyAuthorizeEmitsAuditEvent(t *testing.T) {
	audit := &MemoryAudit{}
	p := &Policy{Audit: audit}
	req := Request{Principal: domain.Principal{Subject: "actor", OrgID: "tenant", Roles: []string{"viewer"}}, Tenant: Tenant{ID: "tenant"}, Resource: ResourceRef{TenantID: "tenant"}, ResourceType: ResourceMemory, Action: ActionRead}
	if decision := p.Authorize(context.Background(), req); !decision.Allowed || len(audit.Events) != 1 {
		t.Fatalf("decision=%+v audit=%+v", decision, audit.Events)
	}
}
