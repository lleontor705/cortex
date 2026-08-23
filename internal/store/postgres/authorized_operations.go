package postgres

// This file is the server capability boundary. Callers get operations, never
// repository objects.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lleontor705/cortex/v2/internal/authz"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/identity"
)

func (s *AuthorizedStore) authorize(ctx context.Context, typ authz.Resource, action authz.Action, project, owner, classification string) error {
	if s == nil || s.store == nil || s.store.authorizer == nil {
		return errors.New(authz.DenyRole)
	}
	t := authz.Tenant{ID: s.store.tenant.TenantID, WorkspaceID: s.store.tenant.WorkspaceID, ProjectID: project}
	return authz.Enforce(ctx, s.store.authorizer, authz.Request{Principal: s.store.principal, Tenant: t, Resource: authz.ResourceRef{TenantID: t.ID, WorkspaceID: t.WorkspaceID, ProjectID: project, OwnerSubject: owner, Classification: classification}, ResourceType: typ, Action: action})
}

func (s *AuthorizedStore) observationResource(ctx context.Context, id int64) (authz.ResourceRef, error) {
	if s == nil || s.store == nil {
		return authz.ResourceRef{}, errors.New(authz.DenyRole)
	}
	var r authz.ResourceRef
	err := s.store.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT COALESCE(o.project_key,''),COALESCE(o.owner_subject,''),COALESCE(o.classification,''),COALESCE(w.public_id::text,'') FROM observations o JOIN sessions se ON se.tenant_id=o.tenant_id AND se.id=o.session_id LEFT JOIN workspaces w ON w.tenant_id=se.tenant_id AND w.id=se.workspace_id WHERE o.tenant_id=public.cortex_current_tenant() AND o.id=$1 AND o.deleted_at IS NULL`, id).Scan(&r.ProjectID, &r.OwnerSubject, &r.Classification, &r.WorkspaceID)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return r, authz.ErrResourceNotFound
	}
	r.TenantID = s.store.tenant.TenantID
	return r, err
}

func (s *AuthorizedStore) authorizeObservation(ctx context.Context, action authz.Action, id int64) error {
	r, err := s.observationResource(ctx, id)
	if err != nil {
		return err
	}
	return s.authorize(ctx, authz.ResourceMemory, action, r.ProjectID, r.OwnerSubject, r.Classification)
}

func (s *AuthorizedStore) SaveObservation(ctx context.Context, o *domain.Observation) error {
	if o == nil {
		return domain.ErrInvalidInput
	}
	if err := s.authorize(ctx, authz.ResourceMemory, authz.ActionWrite, o.Project, s.store.principal.Subject, o.Scope); err != nil {
		return err
	}
	return s.store.observations().Save(ctx, o)
}

// SaveObservationWithEffect authorizes the write exactly like SaveObservation
// and persists through the transactional effect primitive, so the durable
// created/updated classification is decided inside the transaction (RD2,
// REM-SAVE-001). No repository escapes the boundary.
func (s *AuthorizedStore) SaveObservationWithEffect(ctx context.Context, o *domain.Observation) (domain.SaveEffect, error) {
	if o == nil {
		return domain.SaveEffect{}, domain.ErrInvalidInput
	}
	if err := s.authorize(ctx, authz.ResourceMemory, authz.ActionWrite, o.Project, s.store.principal.Subject, o.Scope); err != nil {
		return domain.SaveEffect{}, err
	}
	return s.store.observations().SaveWithEffect(ctx, o)
}

// handoffAuthorizationError converts authorization outcomes into the stable
// handoff taxonomy. Denials of an authenticated principal are forbidden; a
// missing principal binding is unauthorized; anything else (e.g. a failed
// audit dependency) fails closed as retryable-unavailable and never mutates.
func handoffAuthorizationError(err error) error {
	if err == nil {
		return nil
	}
	switch err.Error() {
	case authz.DenyUnauthenticated:
		return &domain.HandoffError{Code: domain.HandoffErrorUnauthorized, Message: domain.ErrHandoffUnauthorized.Message, Operation: "authorize", Context: "authorization required"}
	case authz.DenyRole, authz.DenyScope, authz.DenyTenantMismatch, authz.DenyWorkspace, authz.DenyProject, authz.DenyOwnership, authz.DenyClassification, authz.DenyRevoked:
		return &domain.HandoffError{Code: domain.HandoffErrorForbidden, Message: domain.ErrHandoffForbidden.Message, Operation: "authorize", Context: "principal is not authorized for the complete handoff"}
	}
	return &domain.HandoffError{Code: domain.HandoffErrorUnavailable, Message: domain.ErrHandoffUnavailable.Message, Retryable: true, Operation: "authorize", Context: "authorization dependency failed"}
}

// authorizeHandoffRelationInTx is the in-transaction relation revalidation
// (REM-AUTH-001, review R7 fix 1). It runs INSIDE the executor transaction,
// strictly before the receipt claim and any effect:
//
//  1. the target is resolved by public UUID scoped to the transaction-resolved
//     workspace bigint (bindWorkspace) — sibling workspaces of the same tenant
//     and rows of other tenants are invisible and fail as validation;
//  2. the resolved row is locked FOR SHARE, so its authorizable attributes
//     cannot change between this authorization and the relation write in the
//     same transaction — the resolve-authorize-mutate TOCTOU window of a
//     pre-transaction preauth is closed;
//  3. the graph write is authorized against the LOCKED project, owner, and
//     classification values.
//
// The callback receives the pgx.Tx only because the type is unexported: no
// raw transaction or repository escapes the package boundary.
func (s *AuthorizedStore) authorizeHandoffRelationInTx(ctx context.Context, tx pgx.Tx, relation *domain.HandoffRelationInput) error {
	if s == nil || s.store == nil {
		return domain.ErrHandoffUnauthorized
	}
	if relation == nil || relation.Target.PublicID == nil || relation.Target.LocalID != nil {
		return &domain.HandoffError{Code: domain.HandoffErrorValidation, Message: domain.ErrHandoffValidation.Message, Operation: "relation", Context: "relation target must use the public namespace"}
	}
	ws, ok := workspaceFromContext(ctx)
	if !ok {
		return &domain.HandoffError{Code: domain.HandoffErrorPersistence, Message: domain.ErrHandoffPersistence.Message, Retryable: true, Operation: "relation", Context: "workspace binding is not resolved in this transaction"}
	}
	if err := setHandoffLockTimeout(ctx, tx); err != nil {
		return err
	}
	var project, owner, classification string
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(o.project_key,''),COALESCE(o.owner_subject,''),COALESCE(o.classification,'')
		FROM observations o
		WHERE o.tenant_id=public.cortex_current_tenant() AND o.public_id=$1::uuid AND o.deleted_at IS NULL AND o.workspace_id=$2
		FOR SHARE OF o`, relation.Target.PublicID.String(), ws).Scan(&project, &owner, &classification)
	if errors.Is(err, pgx.ErrNoRows) {
		return &domain.HandoffError{Code: domain.HandoffErrorValidation, Message: domain.ErrHandoffValidation.Message, Operation: "relation", Context: "relation target was not found"}
	}
	if err != nil {
		return handoffPgError(err, "relation")
	}
	if err := s.authorize(ctx, authz.ResourceGraph, authz.ActionWrite, project, owner, classification); err != nil {
		return handoffAuthorizationError(err)
	}
	return nil
}

// derivedHandoffScope builds the receipt idempotency scope EXCLUSIVELY from
// the verified principal binding (tenant, workspace, subject). No client
// input participates: idempotency keys are isolated per principal and
// workspace (REM-HANDOFF-002, RD5).
func (s *AuthorizedStore) derivedHandoffScope() domain.HandoffScope {
	workspace := ""
	if s.store.tenant != nil {
		workspace = s.store.tenant.WorkspaceID
	}
	return domain.HandoffScope("tenant/" + s.store.tenant.TenantID + "/workspace/" + workspace + "/principal/" + s.store.principal.Subject)
}

// ExecuteHandoff performs the compound preauthorization — observation write
// ahead of the transaction, plus the optional relation INSIDE it — and then
// delegates to the RLS-bound executor. tenant/workspace/scope authority
// comes only from the verified principal; partial permission, an unavailable
// authorization dependency, or a cross-tenant target fails closed with zero
// effects (REM-AUTH-001, RD5).
func (s *AuthorizedStore) ExecuteHandoff(ctx context.Context, req domain.HandoffRequest) (domain.ObservationWriteResult, error) {
	if s == nil || s.store == nil {
		return domain.ObservationWriteResult{}, domain.ErrHandoffUnauthorized
	}
	canonical, _, hash, err := domain.CanonicalizeHandoff(req)
	if err != nil {
		return domain.ObservationWriteResult{}, err
	}
	if err := s.authorize(ctx, authz.ResourceMemory, authz.ActionWrite, req.Observation.Project, s.store.principal.Subject, req.Observation.Scope); err != nil {
		return domain.ObservationWriteResult{}, handoffAuthorizationError(err)
	}
	return s.store.executeHandoff(ctx, s.derivedHandoffScope(), req.IdempotencyKey, canonical, hash, s.authorizeHandoffRelationInTx)
}

func (s *AuthorizedStore) GetObservationByID(ctx context.Context, id int64) (*domain.Observation, error) {
	if err := s.authorizeObservation(ctx, authz.ActionRead, id); err != nil {
		return nil, err
	}
	return s.store.observations().GetByID(ctx, id)
}
func (s *AuthorizedStore) GetObservationByPublicID(ctx context.Context, publicID string) (*domain.Observation, error) {
	if s == nil || s.store == nil {
		return nil, errors.New(authz.DenyRole)
	}
	var id int64
	if err := s.store.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id FROM observations WHERE tenant_id=public.cortex_current_tenant() AND public_id=$1::uuid AND deleted_at IS NULL`, publicID).Scan(&id)
	}); err != nil {
		return nil, err
	}
	return s.GetObservationByID(ctx, id)
}
func (s *AuthorizedStore) UpdateObservation(ctx context.Context, o *domain.Observation) error {
	if o == nil {
		return domain.ErrInvalidInput
	}
	if err := s.authorizeObservation(ctx, authz.ActionWrite, o.ID); err != nil {
		return err
	}
	if err := s.authorize(ctx, authz.ResourceMemory, authz.ActionWrite, o.Project, s.store.principal.Subject, o.Scope); err != nil {
		return err
	}
	return s.store.observations().Update(ctx, o)
}
func (s *AuthorizedStore) DeleteObservation(ctx context.Context, id int64) error {
	if err := s.authorizeObservation(ctx, authz.ActionDelete, id); err != nil {
		return err
	}
	return s.store.observations().Delete(ctx, id)
}
func (s *AuthorizedStore) ListObservations(ctx context.Context, f domain.ObservationFilter) ([]*domain.Observation, error) {
	if err := s.authorize(ctx, authz.ResourceMemory, authz.ActionRead, f.Project, "", ""); err != nil {
		return nil, err
	}
	return s.store.observations().List(ctx, f)
}
func (s *AuthorizedStore) SearchObservations(ctx context.Context, query string, opts domain.SearchOptions) ([]*domain.SearchResult, error) {
	if err := s.authorize(ctx, authz.ResourceSearch, authz.ActionSearch, opts.Project, "", ""); err != nil {
		return nil, err
	}
	return s.store.search().Search(ctx, query, opts)
}
func (s *AuthorizedStore) BulkSaveObservations(ctx context.Context, observations []*domain.Observation) error {
	if len(observations) == 0 {
		return domain.ErrInvalidInput
	}
	for _, o := range observations {
		if o == nil {
			return domain.ErrInvalidInput
		}
		if err := s.authorize(ctx, authz.ResourceMemory, authz.ActionWrite, o.Project, s.store.principal.Subject, o.Scope); err != nil {
			return err
		}
	}
	return s.store.observations().SaveBulk(ctx, observations)
}

// CreateSession creates a session in the authorized workspace context.
func (s *AuthorizedStore) CreateSession(ctx context.Context, session *domain.Session) error {
	if session == nil {
		return domain.ErrInvalidInput
	}
	if err := s.authorize(ctx, authz.ResourceMemory, authz.ActionWrite, session.Project, s.store.principal.Subject, ""); err != nil {
		return err
	}
	if session.StartedAt.IsZero() {
		session.StartedAt = time.Now().UTC()
	}
	return (&SessionRepository{Store: s.store}).Create(ctx, session)
}

// ListSessions returns sessions visible in the authorized workspace.
func (s *AuthorizedStore) ListSessions(ctx context.Context, project string) ([]*domain.Session, error) {
	if err := s.authorize(ctx, authz.ResourceMemory, authz.ActionRead, project, "", ""); err != nil {
		return nil, err
	}
	return (&SessionRepository{Store: s.store}).List(ctx, project)
}

// GetServerStats returns counters scoped by the configured tenant and workspace.
func (s *AuthorizedStore) GetServerStats(ctx context.Context) (*domain.ServerStats, error) {
	if err := s.authorize(ctx, authz.ResourceMemory, authz.ActionRead, "", "", ""); err != nil {
		return nil, err
	}
	if err := s.authorize(ctx, authz.ResourceGraph, authz.ActionRead, "", "", ""); err != nil {
		return nil, err
	}
	stats := new(domain.ServerStats)
	if !s.store.isAdmin() {
		err := s.store.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, `
				SELECT
					(SELECT count(*) FROM observations o JOIN sessions se ON se.tenant_id=o.tenant_id AND se.id=o.session_id WHERE o.tenant_id=public.cortex_current_tenant() AND o.deleted_at IS NULL AND se.workspace_id=w.id AND o.owner_subject=$2),
					(SELECT count(*) FROM sessions WHERE tenant_id=public.cortex_current_tenant() AND workspace_id=w.id AND (created_by::text=$2 OR updated_by::text=$2)),
					(SELECT count(*) FROM sessions WHERE tenant_id=public.cortex_current_tenant() AND workspace_id=w.id AND ended_at IS NULL AND (created_by::text=$2 OR updated_by::text=$2)),
					(SELECT count(*) FROM edges e JOIN observations o ON o.tenant_id=e.tenant_id AND o.id=e.from_observation_id JOIN sessions se ON se.tenant_id=o.tenant_id AND se.id=o.session_id WHERE e.tenant_id=public.cortex_current_tenant() AND se.workspace_id=w.id AND o.owner_subject=$2),
					(SELECT count(DISTINCT project_key) FROM sessions WHERE tenant_id=public.cortex_current_tenant() AND workspace_id=w.id AND project_key <> '' AND (created_by::text=$2 OR updated_by::text=$2))
				FROM workspaces w WHERE w.tenant_id=public.cortex_current_tenant() AND w.public_id=$1::uuid`, s.store.tenant.WorkspaceID, s.store.principal.Subject).
				Scan(&stats.Observations, &stats.Sessions, &stats.ActiveSessions, &stats.Edges, &stats.Projects)
		})
		return stats, err
	}
	err := s.store.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT
				(SELECT count(*) FROM observations o JOIN sessions se ON se.tenant_id=o.tenant_id AND se.id=o.session_id WHERE o.tenant_id=public.cortex_current_tenant() AND o.deleted_at IS NULL AND se.workspace_id=w.id),
				(SELECT count(*) FROM sessions WHERE tenant_id=public.cortex_current_tenant() AND workspace_id=w.id),
				(SELECT count(*) FROM sessions WHERE tenant_id=public.cortex_current_tenant() AND workspace_id=w.id AND ended_at IS NULL),
				(SELECT count(*) FROM edges e JOIN observations o ON o.tenant_id=e.tenant_id AND o.id=e.from_observation_id JOIN sessions se ON se.tenant_id=o.tenant_id AND se.id=o.session_id WHERE e.tenant_id=public.cortex_current_tenant() AND se.workspace_id=w.id),
				(SELECT count(DISTINCT project_key) FROM sessions WHERE tenant_id=public.cortex_current_tenant() AND workspace_id=w.id AND project_key <> '')
			FROM workspaces w WHERE w.tenant_id=public.cortex_current_tenant() AND w.public_id=$1::uuid`, s.store.tenant.WorkspaceID).
			Scan(&stats.Observations, &stats.Sessions, &stats.ActiveSessions, &stats.Edges, &stats.Projects)
	})
	return stats, err
}

// ListAuditEvents returns recent administrative audit entries for the workspace tenant.
func (s *AuthorizedStore) ListAuditEvents(ctx context.Context, limit int) ([]*domain.AuditEntry, error) {
	if err := s.authorize(ctx, authz.ResourceAdmin, authz.ActionRead, "", "", ""); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	entries := make([]*domain.AuditEntry, 0)
	err := s.store.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT public_id::text,COALESCE(actor_subject,''),action,resource_type,COALESCE(resource_id,''),COALESCE(reason,''),allowed,created_at FROM audit_events WHERE tenant_id=public.cortex_current_tenant() ORDER BY created_at DESC LIMIT $1`, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			entry := new(domain.AuditEntry)
			if err := rows.Scan(&entry.ID, &entry.ActorSubject, &entry.Action, &entry.ResourceType, &entry.ResourceID, &entry.Reason, &entry.Allowed, &entry.CreatedAt); err != nil {
				return err
			}
			entries = append(entries, entry)
		}
		return rows.Err()
	})
	return entries, err
}

// ListProjects returns project keys visible to the configured principal.
func (s *AuthorizedStore) ListProjects(ctx context.Context) ([]string, error) {
	if err := s.authorize(ctx, authz.ResourceMemory, authz.ActionRead, "", "", ""); err != nil {
		return nil, err
	}
	projects := make([]string, 0)
	err := s.store.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT DISTINCT project_key FROM sessions WHERE tenant_id=public.cortex_current_tenant() AND project_key <> '' ORDER BY project_key`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var project string
			if err := rows.Scan(&project); err != nil {
				return err
			}
			visible := false
			for _, granted := range s.store.principal.ProjectIDs {
				if granted == "*" || granted == project {
					visible = true
					break
				}
			}
			if visible {
				projects = append(projects, project)
			}
		}
		return rows.Err()
	})
	return projects, err
}

func (s *AuthorizedStore) CreateGraphEdge(ctx context.Context, e *domain.Edge) error {
	if e == nil {
		return domain.ErrInvalidInput
	}
	var fromProject, toProject string
	if err := s.store.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT COALESCE(a.project_key,''),COALESCE(b.project_key,'') FROM observations a JOIN observations b ON b.tenant_id=a.tenant_id AND b.id=$2 WHERE a.tenant_id=public.cortex_current_tenant() AND b.tenant_id=public.cortex_current_tenant() AND a.id=$1 AND a.deleted_at IS NULL AND b.deleted_at IS NULL`, e.FromObsID, e.ToObsID).Scan(&fromProject, &toProject)
	}); err != nil {
		return err
	}
	if fromProject == "" || fromProject != toProject {
		return errors.New(authz.DenyProject)
	}
	if err := s.authorize(ctx, authz.ResourceGraph, authz.ActionWrite, fromProject, "", ""); err != nil {
		return err
	}
	return s.store.graph().CreateEdge(ctx, e)
}
func (s *AuthorizedStore) GetGraphEdge(ctx context.Context, id int64) (*domain.Edge, error) {
	if s == nil || s.store == nil {
		return nil, errors.New(authz.DenyRole)
	}
	var project string
	if err := s.store.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT COALESCE(a.project_key,'' ) FROM edges e JOIN observations a ON a.tenant_id=e.tenant_id AND a.id=e.from_observation_id JOIN observations b ON b.tenant_id=e.tenant_id AND b.id=e.to_observation_id WHERE e.tenant_id=public.cortex_current_tenant() AND e.id=$1 AND a.deleted_at IS NULL AND b.deleted_at IS NULL AND a.project_key=b.project_key`, id).Scan(&project)
	}); err != nil {
		return nil, err
	}
	if err := s.authorize(ctx, authz.ResourceGraph, authz.ActionRead, project, "", ""); err != nil {
		return nil, err
	}
	return s.store.graph().GetEdge(ctx, id)
}

func (s *AuthorizedStore) GetGraphEdgeByPublicID(ctx context.Context, publicID string) (*domain.Edge, error) {
	edge, err := (&GraphRepository{Store: s.store}).GetEdgeByPublicID(ctx, publicID)
	if err != nil {
		return nil, err
	}
	return s.GetGraphEdge(ctx, edge.ID)
}
func (s *AuthorizedStore) GetRelatedObservations(ctx context.Context, id int64, depth int) ([]*domain.Observation, error) {
	if err := s.authorizeObservation(ctx, authz.ActionRead, id); err != nil {
		return nil, err
	}
	if err := s.authorize(ctx, authz.ResourceGraph, authz.ActionRead, "", "", ""); err != nil {
		return nil, err
	}
	related, err := s.store.graph().GetRelated(ctx, id, depth)
	if err != nil {
		return nil, err
	}
	visible := make([]*domain.Observation, 0, len(related))
	for _, observation := range related {
		if err := s.authorizeObservation(ctx, authz.ActionRead, observation.ID); err == nil {
			visible = append(visible, observation)
		}
	}
	return visible, nil
}
func (s *AuthorizedStore) DeleteGraphEdge(ctx context.Context, id int64) error {
	e, err := s.GetGraphEdge(ctx, id)
	if err != nil {
		return err
	}
	if err := s.authorizeObservation(ctx, authz.ActionDelete, e.FromObsID); err != nil {
		return err
	}
	if err := s.authorizeObservation(ctx, authz.ActionDelete, e.ToObsID); err != nil {
		return err
	}
	if err := s.authorize(ctx, authz.ResourceGraph, authz.ActionDelete, "", "", ""); err != nil {
		return err
	}
	return s.store.graph().DeleteEdge(ctx, id)
}
func (s *AuthorizedStore) GetImportanceScore(ctx context.Context, id int64) (*domain.ImportanceScore, error) {
	if err := s.authorizeObservation(ctx, authz.ActionRead, id); err != nil {
		return nil, err
	}
	return s.store.GetScore(ctx, id)
}
func (s *AuthorizedStore) UpdateImportanceScore(ctx context.Context, id int64, increment float64) error {
	if err := s.authorizeObservation(ctx, authz.ActionWrite, id); err != nil {
		return err
	}
	return s.store.UpdateScore(ctx, id, increment)
}

// RecordImportanceAccess records a read against an authorized observation.
// The resource is resolved before authorization so an id from another tenant
// cannot be used as an oracle or mutate its score metadata.
func (s *AuthorizedStore) RecordImportanceAccess(ctx context.Context, id int64) error {
	if err := s.authorizeObservation(ctx, authz.ActionRead, id); err != nil {
		return err
	}
	return s.store.RecordAccess(ctx, id)
}

// SetImportanceScore sets a lifecycle score only after resolving and enforcing
// the actual observation resource.
func (s *AuthorizedStore) SetImportanceScore(ctx context.Context, id int64, score float64) error {
	if err := s.authorizeObservation(ctx, authz.ActionWrite, id); err != nil {
		return err
	}
	return s.store.SetScore(ctx, id, score)
}
func (s *AuthorizedStore) CreateUser(ctx context.Context, in identity.UserCreate) (identity.UserRecord, error) {
	if err := s.authorize(ctx, authz.ResourceUsers, authz.ActionManage, "", "", ""); err != nil {
		return identity.UserRecord{}, err
	}
	return s.store.users().Create(ctx, in)
}
func (s *AuthorizedStore) ListUsers(ctx context.Context) ([]identity.UserRecord, error) {
	if err := s.authorize(ctx, authz.ResourceUsers, authz.ActionManage, "", "", ""); err != nil {
		return nil, err
	}
	return s.store.users().List(ctx)
}
func (s *AuthorizedStore) GetUserProfile(ctx context.Context, id string) (*identity.UserRecord, error) {
	if s.store == nil {
		return nil, errors.New(authz.DenyRole)
	}
	if id != s.store.principal.Subject {
		if err := s.authorize(ctx, authz.ResourceUsers, authz.ActionRead, "", "", ""); err != nil {
			return nil, err
		}
	}
	return s.store.users().GetByPublicID(ctx, id)
}
func (s *AuthorizedStore) SetUserActive(ctx context.Context, id string, active bool) error {
	if err := s.authorize(ctx, authz.ResourceUsers, authz.ActionManage, "", "", ""); err != nil {
		return err
	}
	return s.store.users().SetActive(ctx, id, active)
}
func (s *AuthorizedStore) IssueToken(ctx context.Context, in identity.TokenIssue) (identity.IssuedToken, error) {
	if err := s.authorize(ctx, authz.ResourceTokens, authz.ActionManage, "", in.Subject, ""); err != nil {
		return identity.IssuedToken{}, err
	}
	return s.store.tokens().Issue(ctx, in)
}
func (s *AuthorizedStore) VerifyToken(ctx context.Context, secret, scope string) (identity.Principal, error) {
	if err := s.authorize(ctx, authz.ResourceTokens, authz.ActionRead, "", "", ""); err != nil {
		return identity.Principal{}, err
	}
	return s.store.tokens().Verify(ctx, secret, scope)
}
func (s *AuthorizedStore) RevokeToken(ctx context.Context, id string) error {
	if err := s.authorize(ctx, authz.ResourceTokens, authz.ActionManage, "", "", ""); err != nil {
		return err
	}
	return s.store.tokens().Revoke(ctx, id)
}
func (s *AuthorizedStore) RotateToken(ctx context.Context, id string) (identity.IssuedToken, error) {
	if err := s.authorize(ctx, authz.ResourceTokens, authz.ActionManage, "", "", ""); err != nil {
		return identity.IssuedToken{}, err
	}
	return s.store.tokens().Rotate(ctx, id)
}
func (s *AuthorizedStore) ListTokens(ctx context.Context) ([]identity.TokenRecord, error) {
	if err := s.authorize(ctx, authz.ResourceTokens, authz.ActionManage, "", "", ""); err != nil {
		return nil, err
	}
	return s.store.tokens().List(ctx)
}

// GetProjectDuplicates scans projects and returns groups with casing or near-duplicate discrepancies.
func (s *AuthorizedStore) GetProjectDuplicates(ctx context.Context) ([]domain.ProjectDuplicateGroup, error) {
	if err := s.authorize(ctx, authz.ResourceWorkspaces, authz.ActionRead, "", "", ""); err != nil {
		return nil, err
	}
	groups := make([]domain.ProjectDuplicateGroup, 0)
	err := s.store.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		ws, err := requireWorkspaceScope(ctx)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT o.project_key, count(*) as count
			FROM observations o
			JOIN sessions s ON s.tenant_id = o.tenant_id AND s.id = o.session_id
			WHERE o.tenant_id = public.cortex_current_tenant()
			  AND s.workspace_id = $1
			  AND o.project_key <> ''
			  AND o.deleted_at IS NULL
			GROUP BY o.project_key
		`, ws)
		if err != nil {
			return err
		}
		defer rows.Close()

		m := make(map[string]map[string]int)
		for rows.Next() {
			var p string
			var c int
			if err := rows.Scan(&p, &c); err != nil {
				return err
			}
			lower := strings.ToLower(p)
			if m[lower] == nil {
				m[lower] = make(map[string]int)
			}
			m[lower][p] += c
		}
		if rows.Err() != nil {
			return rows.Err()
		}

		for _, variants := range m {
			if len(variants) > 1 {
				var best string
				var bestCnt int
				var total int
				var list []string
				for v, cnt := range variants {
					list = append(list, v)
					total += cnt
					if cnt > bestCnt || best == "" {
						best = v
						bestCnt = cnt
					}
				}
				groups = append(groups, domain.ProjectDuplicateGroup{
					CanonicalName: best,
					Variants:      list,
					TotalCount:    total,
				})
			}
		}
		return nil
	})
	return groups, err
}

// MergeProject consolidates observations, sessions, prompts, and importance scores from sourceProject into targetProject.
func (s *AuthorizedStore) MergeProject(ctx context.Context, sourceProject, targetProject string) (*domain.ProjectMergeResult, error) {
	if err := s.authorize(ctx, authz.ResourceAdmin, authz.ActionManage, "", "", ""); err != nil {
		return nil, err
	}
	sourceProject = strings.TrimSpace(sourceProject)
	targetProject = strings.TrimSpace(targetProject)
	if sourceProject == "" || targetProject == "" || sourceProject == targetProject {
		return nil, domain.ErrInvalidInput
	}
	res := &domain.ProjectMergeResult{
		SourceProject: sourceProject,
		TargetProject: targetProject,
	}
	err := s.store.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		ws, err := requireWorkspaceScope(ctx)
		if err != nil {
			return err
		}
		// 1. Resolve potential topic_key collision for active observations
		_, _ = tx.Exec(ctx, `
			UPDATE observations old_obs
			SET deleted_at = now(), updated_at = now()
			FROM sessions s
			WHERE old_obs.tenant_id = public.cortex_current_tenant()
			  AND s.tenant_id = old_obs.tenant_id
			  AND s.id = old_obs.session_id
			  AND s.workspace_id = $1
			  AND old_obs.project_key = $2
			  AND old_obs.topic_key IS NOT NULL
			  AND old_obs.topic_key <> ''
			  AND old_obs.deleted_at IS NULL
			  AND EXISTS (
				SELECT 1 FROM observations target_obs
				JOIN sessions ts ON ts.tenant_id = target_obs.tenant_id AND ts.id = target_obs.session_id
				WHERE target_obs.tenant_id = old_obs.tenant_id
				  AND ts.workspace_id = s.workspace_id
				  AND target_obs.project_key = $3
				  AND target_obs.topic_key = old_obs.topic_key
				  AND target_obs.deleted_at IS NULL
			  )
		`, ws, sourceProject, targetProject)

		// 2. Merge remaining observations
		tagObs, err := tx.Exec(ctx, `
			UPDATE observations o
			SET project_key = $3, updated_at = now()
			FROM sessions s
			WHERE o.tenant_id = public.cortex_current_tenant()
			  AND s.tenant_id = o.tenant_id
			  AND s.id = o.session_id
			  AND s.workspace_id = $1
			  AND o.project_key = $2
			  AND o.deleted_at IS NULL
		`, ws, sourceProject, targetProject)
		if err != nil {
			return fmt.Errorf("merge observations: %w", err)
		}
		res.ObservationsMerged = int(tagObs.RowsAffected())

		// 3. Merge sessions
		tagSess, err := tx.Exec(ctx, `
			UPDATE sessions
			SET project_key = $3, updated_at = now()
			WHERE tenant_id = public.cortex_current_tenant()
			  AND workspace_id = $1
			  AND project_key = $2
		`, ws, sourceProject, targetProject)
		if err != nil {
			return fmt.Errorf("merge sessions: %w", err)
		}
		res.SessionsMerged = int(tagSess.RowsAffected())

		// 4. Merge importance_scores
		_, _ = tx.Exec(ctx, `
			UPDATE importance_scores
			SET project_key = $3, updated_at = now()
			WHERE tenant_id = public.cortex_current_tenant()
			  AND workspace_id = $1
			  AND project_key = $2
		`, ws, sourceProject, targetProject)

		// 5. Merge prompts
		tagPrompts, err := tx.Exec(ctx, `
			UPDATE prompts p
			SET project_key = $3, updated_at = now()
			FROM sessions s
			WHERE p.tenant_id = public.cortex_current_tenant()
			  AND s.tenant_id = p.tenant_id
			  AND s.id = p.session_id
			  AND s.workspace_id = $1
			  AND p.project_key = $2
		`, ws, sourceProject, targetProject)
		if err == nil {
			res.PromptsMerged = int(tagPrompts.RowsAffected())
		}

		return nil
	})
	return res, err
}
