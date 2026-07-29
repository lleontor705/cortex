// Package authz contains the server authorization port. It is deliberately
// independent of transports and repositories so every use-case can enforce
// BOLA/BFLA before touching storage.
package authz

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/lleontor705/cortex/internal/domain"
)

type Role string

const (
	RoleOwner          Role = "owner"
	RoleAdmin          Role = "admin"
	RoleMember         Role = "member"
	RoleViewer         Role = "viewer"
	RoleServiceAccount Role = "service-account"
)

type Resource string

const (
	ResourceMemory     Resource = "memory"
	ResourceSearch     Resource = "search"
	ResourceGraph      Resource = "graph"
	ResourceTokens     Resource = "tokens"
	ResourceUsers      Resource = "users"
	ResourceWorkspaces Resource = "workspaces"
	ResourceAdmin      Resource = "admin"
)

type Action string

const (
	ActionRead   Action = "read"
	ActionWrite  Action = "write"
	ActionDelete Action = "delete"
	ActionManage Action = "manage"
	ActionSearch Action = "search"
)

const (
	DenyUnauthenticated  = "unauthenticated"
	DenyUnknownAction    = "unknown_action"
	DenyRole             = "role_not_permitted"
	DenyScope            = "scope_not_granted"
	DenyTenantMismatch   = "tenant_mismatch"
	DenyWorkspace        = "workspace_not_granted"
	DenyProject          = "project_not_granted"
	DenyOwnership        = "ownership_required"
	DenyClassification   = "classification_not_allowed"
	DenyResourceNotFound = "resource_not_found"
	DenyRevoked          = "credential_revoked"
)

type Tenant struct{ ID, WorkspaceID, ProjectID string }
type ResourceRef struct{ TenantID, WorkspaceID, ProjectID, OwnerSubject, Classification, OpaqueID string }
type Request struct {
	Principal     domain.Principal
	Tenant        Tenant
	Resource      ResourceRef
	ResourceType  Resource
	Action        Action
	CorrelationID string
}
type Decision struct {
	Allowed bool
	Reason  string
}
type Authorizer interface {
	Authorize(context.Context, Request) Decision
}

type AuditEvent struct {
	CorrelationID, Actor, Action, Resource, Reason string
	Allowed                                        bool
}
type AuditSink interface {
	Record(context.Context, AuditEvent) error
}
type Policy struct{ Audit AuditSink }

func NewPolicy() *Policy { return &Policy{} }

var rolePermissions = map[Role]map[Resource]map[Action]bool{
	RoleOwner:  allPermissions(),
	RoleAdmin:  allPermissions(),
	RoleMember: {ResourceMemory: {ActionRead: true, ActionWrite: true}, ResourceSearch: {ActionRead: true, ActionSearch: true}, ResourceGraph: {ActionRead: true, ActionWrite: true}},
	RoleViewer: {ResourceMemory: {ActionRead: true}, ResourceSearch: {ActionRead: true, ActionSearch: true}, ResourceGraph: {ActionRead: true}},
}

func allPermissions() map[Resource]map[Action]bool {
	m := map[Resource]map[Action]bool{}
	for _, r := range []Resource{ResourceMemory, ResourceSearch, ResourceGraph, ResourceTokens, ResourceUsers, ResourceWorkspaces, ResourceAdmin} {
		m[r] = map[Action]bool{ActionRead: true, ActionWrite: true, ActionDelete: true, ActionManage: true, ActionSearch: true}
	}
	return m
}

func (p *Policy) Authorize(ctx context.Context, req Request) Decision {
	d := p.decide(req)
	if p.Audit != nil {
		_ = p.Audit.Record(ctx, AuditEvent{CorrelationID: req.CorrelationID, Actor: req.Principal.Subject, Action: string(req.Action), Resource: string(req.ResourceType), Reason: d.Reason, Allowed: d.Allowed})
	}
	return d
}
func (p *Policy) decide(req Request) Decision {
	if req.Principal.Subject == "" || req.Principal.OrgID == "" {
		return Decision{Reason: DenyUnauthenticated}
	}
	if req.ResourceType == "" || req.Action == "" {
		return Decision{Reason: DenyUnknownAction}
	}
	if req.Tenant.ID == "" || req.Resource.TenantID != "" && req.Resource.TenantID != req.Tenant.ID || req.Principal.OrgID != req.Tenant.ID {
		return Decision{Reason: DenyTenantMismatch}
	}
	if req.Tenant.WorkspaceID != "" && req.Resource.WorkspaceID != "" && req.Tenant.WorkspaceID != req.Resource.WorkspaceID {
		return Decision{Reason: DenyWorkspace}
	}
	if req.Resource.WorkspaceID != "" && !contains(req.Principal.WorkspaceIDs, req.Resource.WorkspaceID) {
		return Decision{Reason: DenyWorkspace}
	}
	if req.Tenant.ProjectID != "" && req.Resource.ProjectID != "" && req.Tenant.ProjectID != req.Resource.ProjectID {
		return Decision{Reason: DenyProject}
	}
	if req.Resource.ProjectID != "" && req.Tenant.ProjectID == "" && !hasRole(req.Principal, RoleOwner) && !hasRole(req.Principal, RoleAdmin) && !hasProjectScope(req.Principal.Scopes, req.Resource.ProjectID) {
		return Decision{Reason: DenyProject}
	}
	allowed := false
	for _, raw := range req.Principal.Roles {
		role := Role(strings.ToLower(raw))
		if role == RoleServiceAccount {
			allowed = scopeAllows(req.Principal.Scopes, req)
			continue
		}
		if rolePermissions[role][req.ResourceType][req.Action] {
			allowed = true
		}
	}
	if req.Principal.Type == "service_account" && len(req.Principal.Roles) == 0 {
		allowed = scopeAllows(req.Principal.Scopes, req)
	}
	if !allowed {
		return Decision{Reason: DenyRole}
	}
	if req.Resource.OwnerSubject != "" && req.Resource.OwnerSubject != req.Principal.Subject && !hasRole(req.Principal, RoleOwner) && req.Resource.Classification == "personal" {
		return Decision{Reason: DenyOwnership}
	}
	if req.Resource.Classification == "restricted" && !hasRole(req.Principal, RoleOwner) && !hasRole(req.Principal, RoleAdmin) {
		return Decision{Reason: DenyClassification}
	}
	return Decision{Allowed: true}
}
func scopeAllows(scopes []string, req Request) bool {
	for _, s := range scopes {
		if s == "*" || s == string(req.ResourceType)+":"+string(req.Action) || (req.Action == ActionRead && s == string(req.ResourceType)+":read") {
			if req.Resource.ProjectID == "" {
				return true
			}
			if hasProjectScope(scopes, req.Resource.ProjectID) {
				return true
			}
		}
	}
	return false
}
func hasProjectScope(scopes []string, project string) bool {
	for _, s := range scopes {
		if s == "project:*" || s == "project:"+project {
			return true
		}
	}
	return false
}
func hasRole(p domain.Principal, r Role) bool {
	for _, x := range p.Roles {
		if Role(strings.ToLower(x)) == r {
			return true
		}
	}
	return false
}
func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// DeriveTenantContext ignores all client-provided tenant fields. requested is
// retained only to detect an attempted spoof; the authenticated org wins.
func DeriveTenantContext(p domain.Principal, requested Tenant) (domain.TenantContext, error) {
	if p.Subject == "" || p.OrgID == "" {
		return domain.TenantContext{}, errors.New(DenyUnauthenticated)
	}
	if requested.ID != "" && requested.ID != p.OrgID { /* ignore, never trust */
	}
	return domain.TenantContext{TenantID: p.OrgID, WorkspaceID: firstWorkspace(p, requested.WorkspaceID), OwnerSubject: p.Subject}, nil
}
func firstWorkspace(p domain.Principal, requested string) string {
	if requested != "" && contains(p.WorkspaceIDs, requested) {
		return requested
	}
	if len(p.WorkspaceIDs) > 0 {
		return p.WorkspaceIDs[0]
	}
	return ""
}

var ErrResourceNotFound = errors.New(DenyResourceNotFound)

type OpaqueResolver struct {
	mu     sync.RWMutex
	values map[string]string
}

func NewOpaqueResolver() *OpaqueResolver { return &OpaqueResolver{values: map[string]string{}} }
func (r *OpaqueResolver) Put(tenant, kind, opaque, internal string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[tenant+"\x00"+kind+"\x00"+opaque] = internal
}
func (r *OpaqueResolver) Resolve(tenant, kind, opaque string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.values[tenant+"\x00"+kind+"\x00"+opaque]
	if !ok {
		return "", ErrResourceNotFound
	}
	return v, nil
}

type MemoryAudit struct {
	mu     sync.Mutex
	Events []AuditEvent
}

func (a *MemoryAudit) Record(_ context.Context, e AuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	e.Actor = redact(e.Actor)
	e.Resource = redact(e.Resource)
	a.Events = append(a.Events, e)
	return nil
}
func redact(s string) string {
	if len(s) > 256 {
		return s[:256]
	}
	return s
}
