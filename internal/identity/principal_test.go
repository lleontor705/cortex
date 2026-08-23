package identity_test

import (
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/identity"
)

// Compile-time proof that identity types are aliases for domain types.
var (
	_ domain.Principal     = identity.Principal{}
	_ domain.TenantContext = identity.TenantContext{}
)

// TestPrincipalAlias verifies identity.Principal is a type alias for domain.Principal.
func TestPrincipalAlias(t *testing.T) {
	p := identity.Principal{
		Subject: "user-1",
		Type:    "user",
		OrgID:   "org-1",
	}
	if p.Subject != "user-1" {
		t.Errorf("Subject = %q, want %q", p.Subject, "user-1")
	}
}

// TestTenantContextAlias verifies identity.TenantContext is a type alias for domain.TenantContext.
func TestTenantContextAlias(t *testing.T) {
	tc := identity.TenantContext{
		TenantID:     "t1",
		WorkspaceID:  "w1",
		OwnerSubject: "u1",
	}
	if tc.TenantID != "t1" {
		t.Errorf("TenantID = %q, want %q", tc.TenantID, "t1")
	}
}

// TestNewPrincipal verifies the constructor produces a valid Principal.
func TestNewPrincipal(t *testing.T) {
	p := identity.NewPrincipal(
		"user-1", "user", "org-1",
		[]string{"ws-1"}, []string{"member"}, []string{"cortex.memory.read"},
		"oidc", "grant-digest-abc",
	)
	if p.Subject != "user-1" {
		t.Errorf("Subject = %q, want %q", p.Subject, "user-1")
	}
	if p.OrgID != "org-1" {
		t.Errorf("OrgID = %q, want %q", p.OrgID, "org-1")
	}
	if len(p.WorkspaceIDs) != 1 || p.WorkspaceIDs[0] != "ws-1" {
		t.Errorf("WorkspaceIDs = %v, want [ws-1]", p.WorkspaceIDs)
	}
	if len(p.Roles) != 1 || p.Roles[0] != "member" {
		t.Errorf("Roles = %v, want [member]", p.Roles)
	}
	if p.AuthMethod != "oidc" {
		t.Errorf("AuthMethod = %q, want %q", p.AuthMethod, "oidc")
	}
}

// TestNewPrincipalNilSafe verifies the constructor does not panic with nil slices.
func TestNewPrincipalNilSafe(t *testing.T) {
	// Must not panic even with nil slice arguments.
	p := identity.NewPrincipal("svc-1", "service_account", "org-1", nil, nil, nil, "client_credentials", "")
	if p.Subject != "svc-1" {
		t.Errorf("Subject = %q", p.Subject)
	}
	// Nil slices should normalize to empty, not nil.
	if p.WorkspaceIDs == nil {
		t.Error("WorkspaceIDs should be non-nil (empty)")
	}
}

func TestNewPrincipalDeepCopiesAuthorizationSlices(t *testing.T) {
	workspaces := []string{"w1"}
	roles := []string{"reader"}
	scopes := []string{"memory:read"}
	p := identity.NewPrincipal("u", "user", "o", workspaces, roles, scopes, "oidc", "g")
	workspaces[0], roles[0], scopes[0] = "evil", "admin", "memory:write"
	if p.WorkspaceIDs[0] != "w1" || p.Roles[0] != "reader" || p.Scopes[0] != "memory:read" {
		t.Fatal("principal aliases caller authorization slices")
	}
	copyScopes := p.ScopesCopy()
	copyScopes[0] = "memory:write"
	if p.Scopes[0] != "memory:read" {
		t.Fatal("scope getter exposed mutable storage")
	}
}

// TestNewTenantContext verifies the constructor.
func TestNewTenantContext(t *testing.T) {
	tc := identity.NewTenantContext("t1", "w1", "u1")
	if tc.TenantID != "t1" {
		t.Errorf("TenantID = %q, want %q", tc.TenantID, "t1")
	}
	if tc.WorkspaceID != "w1" {
		t.Errorf("WorkspaceID = %q, want %q", tc.WorkspaceID, "w1")
	}
	if tc.OwnerSubject != "u1" {
		t.Errorf("OwnerSubject = %q, want %q", tc.OwnerSubject, "u1")
	}
}

// TestNewTenantContextNilSafe verifies the constructor handles empty strings
// without panicking (local mode scenario where tenant context is empty).
func TestNewTenantContextNilSafe(t *testing.T) {
	tc := identity.NewTenantContext("", "", "")
	if tc.TenantID != "" {
		t.Errorf("TenantID = %q, want empty", tc.TenantID)
	}
}
