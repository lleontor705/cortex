// Package authz contains the server authorization port. It is deliberately
// independent of transports and repositories so every use-case can enforce
// BOLA/BFLA before touching storage.
package authz

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/lleontor705/cortex/internal/domain"
)

type Role string

const (
	RoleOwner          Role = "owner"
	RoleAdmin          Role = "admin"
	RoleMember         Role = "member"
	RoleDeveloper      Role = "developer"
	RoleAgent          Role = "agent"
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
type AuditedAuthorizer interface {
	AuthorizeWithAudit(context.Context, Request) (Decision, error)
}

type AuditEvent struct {
	CorrelationID, Actor, Action, Resource, ResourceID, Reason string
	Allowed                                                    bool
}
type AuditSink interface {
	Record(context.Context, AuditEvent) error
}
type Policy struct{ Audit AuditSink }

func NewPolicy() *Policy { return &Policy{} }

var rolePermissions = map[Role]map[Resource]map[Action]bool{
	RoleOwner:     allPermissions(),
	RoleAdmin:     allPermissions(),
	RoleMember:    {ResourceWorkspaces: {ActionRead: true}, ResourceMemory: {ActionRead: true, ActionWrite: true, ActionDelete: true}, ResourceSearch: {ActionRead: true, ActionSearch: true}, ResourceGraph: {ActionRead: true, ActionWrite: true, ActionDelete: true}, ResourceTokens: {ActionRead: true}},
	RoleDeveloper: {ResourceWorkspaces: {ActionRead: true}, ResourceMemory: {ActionRead: true, ActionWrite: true, ActionDelete: true}, ResourceSearch: {ActionRead: true, ActionSearch: true}, ResourceGraph: {ActionRead: true, ActionWrite: true, ActionDelete: true}, ResourceTokens: {ActionRead: true}},
	RoleAgent:     {ResourceWorkspaces: {ActionRead: true}, ResourceMemory: {ActionRead: true, ActionWrite: true, ActionDelete: true}, ResourceSearch: {ActionRead: true, ActionSearch: true}, ResourceGraph: {ActionRead: true, ActionWrite: true, ActionDelete: true}},
	RoleViewer:    {ResourceWorkspaces: {ActionRead: true}, ResourceMemory: {ActionRead: true}, ResourceSearch: {ActionRead: true, ActionSearch: true}, ResourceGraph: {ActionRead: true}},
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
		_ = p.Audit.Record(ctx, AuditEvent{CorrelationID: req.CorrelationID, Actor: req.Principal.Subject, Action: string(req.Action), Resource: string(req.ResourceType), ResourceID: req.Resource.OpaqueID, Reason: d.Reason, Allowed: d.Allowed})
	}
	return d
}

// AuthorizeWithAudit makes audit persistence part of privileged authorization.
// A failed audit denies writes, deletes, and management operations.
func (p *Policy) AuthorizeWithAudit(ctx context.Context, req Request) (Decision, error) {
	d := p.decide(req)
	if p.Audit == nil {
		return d, nil
	}
	err := p.Audit.Record(ctx, AuditEvent{CorrelationID: req.CorrelationID, Actor: req.Principal.Subject, Action: string(req.Action), Resource: string(req.ResourceType), ResourceID: req.Resource.OpaqueID, Reason: d.Reason, Allowed: d.Allowed})
	if err != nil && (req.Action == ActionWrite || req.Action == ActionDelete || req.Action == ActionManage) {
		return d, fmt.Errorf("audit authorization: %w", err)
	}
	return d, nil
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
	if req.Tenant.WorkspaceID != "" && !contains(req.Principal.WorkspaceIDs, req.Tenant.WorkspaceID) {
		return Decision{Reason: DenyWorkspace}
	}
	if req.Resource.WorkspaceID != "" && !contains(req.Principal.WorkspaceIDs, req.Resource.WorkspaceID) {
		return Decision{Reason: DenyWorkspace}
	}
	if req.Tenant.ProjectID != "" && req.Resource.ProjectID != "" && req.Tenant.ProjectID != req.Resource.ProjectID {
		return Decision{Reason: DenyProject}
	}
	// Project authority is never implied by a tenant role. This prevents a
	// caller from elevating itself by selecting an arbitrary project ID.
	if req.Resource.ProjectID != "" && !hasProjectGrant(req.Principal, req.Resource.ProjectID) {
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
	if req.Action == ActionDelete && req.Resource.OwnerSubject != "" && req.Resource.OwnerSubject != req.Principal.Subject && !hasRole(req.Principal, RoleOwner) && !hasRole(req.Principal, RoleAdmin) {
		return Decision{Reason: DenyOwnership}
	}
	if req.Resource.OwnerSubject != "" && req.Resource.OwnerSubject != req.Principal.Subject && !hasRole(req.Principal, RoleOwner) && req.Resource.Classification == "personal" {
		return Decision{Reason: DenyOwnership}
	}
	if req.Resource.Classification != "" && !hasClassificationClearance(req.Principal, req.Resource.Classification) {
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
func hasProjectGrant(p domain.Principal, project string) bool {
	if project == "" {
		return true
	}
	for _, id := range p.ProjectIDs {
		if id == "*" || id == project {
			return true
		}
	}
	// Scopes are also verified grants (never copied from the request). The
	// explicit ProjectIDs field is preferred, while scoped credentials remain
	// compatible with existing issuers.
	return hasProjectScope(p.Scopes, project)
}
func hasClassificationClearance(p domain.Principal, classification string) bool {
	if hasRole(p, RoleOwner) || hasRole(p, RoleAdmin) {
		return true
	}
	for _, c := range p.ClassificationClearance {
		if c == "*" || c == classification {
			return true
		}
	}
	return classification != "restricted" && classification != "confidential"
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
	// requested.ID is intentionally ignored; tenant authority comes only from
	// the verified principal organization.
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
