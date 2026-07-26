// Package identity provides Principal and TenantContext types and nil-safe
// constructors for threading authenticated identity through the Cortex platform.
//
// This is a thin helper package that re-exports the domain port types via type
// aliases and provides constructors. The full identity domain model
// (Organization, Workspace, Project, User, ServiceAccount, Agent, OAuth
// verifier, API key, claims mapper) is introduced in W11/W12.
//
// REQ-FOUND-001: these types compile and unit-test in isolation. No caller
// adopts them in W1.
package identity

import "github.com/lleontor705/cortex/internal/domain"

// Principal is a type alias for domain.Principal, re-exported so callers in
// the identity/authz layers reference identity.Principal without importing
// domain directly in every file.
type Principal = domain.Principal

// TenantContext is a type alias for domain.TenantContext.
type TenantContext = domain.TenantContext

// NewPrincipal constructs a fully-populated Principal.
//
// Nil slice arguments are normalized to empty (non-nil) slices so downstream
// range loops and len() calls are always safe without nil checks.
func NewPrincipal(subject, principalType, orgID string, workspaceIDs, roles, scopes []string, authMethod, grantDigest string) Principal {
	return Principal{
		Subject:      subject,
		Type:         principalType,
		OrgID:        orgID,
		WorkspaceIDs: normalizeSlice(workspaceIDs),
		Roles:        normalizeSlice(roles),
		Scopes:       normalizeSlice(scopes),
		AuthMethod:   authMethod,
		GrantDigest:  grantDigest,
	}
}

// NewTenantContext constructs a TenantContext value.
//
// It never panics on empty strings — in local mode all fields are empty,
// representing the synthetic/nil-tenant invariant (REQ-FOUND-001).
func NewTenantContext(tenantID, workspaceID, ownerSubject string) TenantContext {
	return TenantContext{
		TenantID:     tenantID,
		WorkspaceID:  workspaceID,
		OwnerSubject: ownerSubject,
	}
}

// normalizeSlice returns s if non-nil, or an empty slice if nil.
// This prevents nil-slice panics in downstream range/len consumers.
func normalizeSlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
