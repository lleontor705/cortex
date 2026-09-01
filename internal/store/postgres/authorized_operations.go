package postgres

// This file is the server capability boundary. Callers get operations, never
// repository objects.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/lleontor705/cortex/v2/internal/authz"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/code"
	"github.com/lleontor705/cortex/v2/internal/identity"
)

func (s *AuthorizedStore) authorize(ctx context.Context, typ authz.Resource, action authz.Action, project, owner, classification string) error {
	if s == nil || s.store == nil || s.store.authorizer == nil {
		return errors.New(authz.DenyRole)
	}
	t := authz.Tenant{ID: s.store.tenant.TenantID, WorkspaceID: s.store.tenant.WorkspaceID, ProjectID: project}
	return authz.Enforce(ctx, s.store.authorizer, authz.Request{Principal: s.store.principal, Tenant: t, Resource: authz.ResourceRef{TenantID: t.ID, WorkspaceID: t.WorkspaceID, ProjectID: project, OwnerSubject: owner, Classification: classification}, ResourceType: typ, Action: action})
}

// AuthorizeAdminManage exposes a narrow, audited capability gate for server
// administration endpoints without leaking repositories or policy internals.
func (s *AuthorizedStore) AuthorizeAdminManage(ctx context.Context) error {
	return s.authorize(ctx, authz.ResourceAdmin, authz.ActionManage, "", "", "")
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

// GetAgentObservationByID hydrates a vector candidate only when the candidate
// still belongs to the exact public project UUID selected and authorized for
// the request. The label is checked solely as canonical display metadata.
func (s *AuthorizedStore) GetAgentObservationByID(ctx context.Context, projectPublicID, projectLabel string, id int64) (*domain.Observation, error) {
	if s == nil || s.store == nil || id <= 0 || strings.TrimSpace(projectLabel) == "" {
		return nil, domain.ErrInvalidInput
	}
	projectID, err := uuid.Parse(strings.TrimSpace(projectPublicID))
	if err != nil || projectID == uuid.Nil {
		return nil, domain.ErrInvalidInput
	}
	projectPublicID = projectID.String()
	if err := s.authorize(ctx, authz.ResourceSearch, authz.ActionSearch, projectPublicID, "", ""); err != nil {
		return nil, err
	}
	if err := s.authorize(ctx, authz.ResourceMemory, authz.ActionRead, projectPublicID, "", ""); err != nil {
		return nil, err
	}
	var observation domain.Observation
	err = s.store.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		workspace, err := requireWorkspaceScope(ctx)
		if err != nil {
			return err
		}
		statement := `SELECT o.public_id::text,o.id,o.session_id::text,COALESCE(o.project_key,''),COALESCE(o.scope,''),COALESCE(o.source,''),o.type,o.title,o.content,COALESCE(o.topic_key,''),o.created_at,o.updated_at,COALESCE(o.owner_subject,'')
			 FROM observations o
			 JOIN projects p ON p.tenant_id=o.tenant_id AND p.workspace_id=o.workspace_id AND p.id=o.project_id
			 WHERE o.tenant_id=public.cortex_current_tenant()
			   AND o.workspace_id=$1 AND o.id=$2
			   AND o.deleted_at IS NULL AND p.public_id=$3::uuid AND p.name=$4`
		args := []any{workspace, id, projectPublicID, projectLabel}
		statement, args = s.store.appendObservationVisibilityPredicate(statement, args, true)
		return tx.QueryRow(ctx, statement, args...).Scan(&observation.PublicID, &observation.ID, &observation.SessionID, &observation.Project, &observation.Scope, &observation.Source, &observation.Type, &observation.Title, &observation.Content, &observation.TopicKey, &observation.CreatedAt, &observation.UpdatedAt, &observation.OwnerSubject)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, authz.ErrResourceNotFound
	}
	if err != nil {
		return nil, err
	}
	return &observation, nil
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

// SearchAgentObservations separates authority identity from corpus labeling.
// Grants and authorization use the immutable projects.public_id; only after
// resolving and matching that ID inside the bound tenant/workspace transaction
// is its internal projects.id used to filter observations.project_id. The
// label remains display metadata and can never select the corpus.
func (s *AuthorizedStore) SearchAgentObservations(ctx context.Context, projectPublicID, projectLabel, query string, opts domain.SearchOptions) ([]*domain.SearchResult, error) {
	if s == nil || s.store == nil || strings.TrimSpace(projectLabel) == "" || strings.TrimSpace(query) == "" {
		return nil, domain.ErrInvalidInput
	}
	projectID, err := uuid.Parse(strings.TrimSpace(projectPublicID))
	if err != nil || projectID == uuid.Nil {
		return nil, domain.ErrInvalidInput
	}
	projectPublicID = projectID.String()
	if err := s.authorize(ctx, authz.ResourceSearch, authz.ActionSearch, projectPublicID, "", ""); err != nil {
		return nil, err
	}
	if err := s.authorize(ctx, authz.ResourceMemory, authz.ActionRead, projectPublicID, "", ""); err != nil {
		return nil, err
	}
	if opts.Limit <= 0 {
		opts.Limit = 20
	} else if opts.Limit > 100 {
		opts.Limit = 100
	}
	opts.Query = query

	results := make([]*domain.SearchResult, 0)
	err = s.store.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		workspace, err := requireWorkspaceScope(ctx)
		if err != nil {
			return err
		}
		var internalProjectID int64
		var canonicalLabel string
		err = tx.QueryRow(ctx, `SELECT id,name FROM projects WHERE tenant_id=public.cortex_current_tenant() AND workspace_id=$1 AND public_id=$2::uuid`, workspace, projectPublicID).Scan(&internalProjectID, &canonicalLabel)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errors.New(authz.DenyProject)
			}
			return err
		}
		if canonicalLabel != projectLabel {
			return errors.New(authz.DenyProject)
		}

		statement := `SELECT o.public_id::text,o.id,o.session_id::text,COALESCE(o.project_key,''),COALESCE(o.scope,''),COALESCE(o.source,''),o.type,o.title,o.content,COALESCE(o.topic_key,''),o.created_at,o.updated_at,ts_rank_cd(o.search_vector,websearch_to_tsquery('simple',$1)) FROM observations o WHERE o.tenant_id=public.cortex_current_tenant() AND o.deleted_at IS NULL AND o.workspace_id=$2 AND (o.project_id=$3 OR (o.project_id IS NULL AND o.project_key=$4)) AND o.search_vector @@ websearch_to_tsquery('simple',$1)`
		args := []any{opts.Query, workspace, internalProjectID, canonicalLabel}
		statement, args = s.store.appendObservationVisibilityPredicate(statement, args, true)
		if opts.Scope != "" {
			statement += fmt.Sprintf(" AND o.scope=$%d", len(args)+1)
			args = append(args, opts.Scope)
		}
		if opts.Type != "" {
			statement += fmt.Sprintf(" AND o.type=$%d", len(args)+1)
			args = append(args, opts.Type)
		}
		statement += fmt.Sprintf(" ORDER BY 13 DESC,o.created_at DESC LIMIT $%d", len(args)+1)
		args = append(args, opts.Limit)
		rows, err := tx.Query(ctx, statement, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			item := new(domain.SearchResult)
			if err := rows.Scan(&item.PublicID, &item.ID, &item.SessionID, &item.Project, &item.Scope, &item.Source, &item.Type, &item.Title, &item.Content, &item.TopicKey, &item.CreatedAt, &item.UpdatedAt, &item.Rank); err != nil {
				return err
			}
			results = append(results, item)
		}
		return rows.Err()
	})
	return results, err
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

// ListAgentProjects resolves public IDs and labels inside the principal-bound
// tenant/workspace transaction. It returns only projects granted for search
// and with at least one corpus the same principal may read.
func (s *AuthorizedStore) ListAgentProjects(ctx context.Context) (map[string]string, error) {
	if s == nil || s.store == nil {
		return nil, errors.New(authz.DenyRole)
	}
	if err := s.authorize(ctx, authz.ResourceSearch, authz.ActionSearch, "", "", ""); err != nil {
		return nil, err
	}
	type candidate struct {
		id, label string
		hasMemory bool
	}
	candidates := make([]candidate, 0)
	err := s.store.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		workspace, err := requireWorkspaceScope(ctx)
		if err != nil {
			return err
		}
		_, _ = tx.Exec(ctx, `
			INSERT INTO projects(tenant_id, workspace_id, name)
			SELECT DISTINCT public.cortex_current_tenant(), $1, sub.pname
			  FROM (
			    SELECT project_key AS pname FROM sessions WHERE tenant_id=public.cortex_current_tenant() AND workspace_id=$1 AND project_key <> ''
			    UNION
			    SELECT project_key AS pname FROM observations WHERE tenant_id=public.cortex_current_tenant() AND project_key <> ''
			  ) sub
			 WHERE NOT EXISTS (
			    SELECT 1 FROM projects p WHERE p.tenant_id=public.cortex_current_tenant() AND p.workspace_id=$1 AND p.name=sub.pname
			 )`, workspace)

		rows, err := tx.Query(ctx, `
			SELECT p.public_id::text, p.name,
			       (EXISTS (SELECT 1 FROM sessions se WHERE se.tenant_id=p.tenant_id AND se.workspace_id=p.workspace_id AND (se.project_id=p.id OR se.project_key=p.name))
			        OR EXISTS (SELECT 1 FROM observations o WHERE o.tenant_id=p.tenant_id AND (o.project_id=p.id OR o.project_key=p.name) AND o.deleted_at IS NULL))
			  FROM projects p
			 WHERE p.tenant_id=public.cortex_current_tenant() AND p.workspace_id=$1
			 ORDER BY p.name, p.public_id`, workspace)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item candidate
			if err := rows.Scan(&item.id, &item.label, &item.hasMemory); err != nil {
				return err
			}
			candidates = append(candidates, item)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}

	projects := make(map[string]string)
	for _, item := range candidates {
		if err := s.authorize(ctx, authz.ResourceSearch, authz.ActionSearch, item.id, "", ""); err != nil {
			continue
		}
		if item.hasMemory && s.authorize(ctx, authz.ResourceMemory, authz.ActionRead, item.id, "", "") == nil {
			projects[item.id] = item.label
			continue
		}
		if err := s.authorize(ctx, authz.ResourceCode, authz.ActionRead, item.id, "", ""); err != nil {
			continue
		}
		ready, err := s.agentCodeProjectReady(ctx, item.id)
		if err != nil {
			return nil, err
		}
		if ready {
			projects[item.id] = item.label
		}
	}
	return projects, nil
}

// agentCodeProjectReady checks readiness only after the transaction has been
// bound to the authorized project. An unbound RLS query would make every
// code-only project appear absent and must never be used as an availability
// signal.
func (s *AuthorizedStore) agentCodeProjectReady(ctx context.Context, project string) (bool, error) {
	ready := false
	err := s.store.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		codeStore, err := newPostgresCodeStore(ctx, tx, s.store.tenant.WorkspaceID, project)
		if err != nil {
			return err
		}
		if err := codeStore.requireReady(ctx); err != nil {
			if errors.Is(err, ErrCodeIndexUnavailable) {
				return nil
			}
			return err
		}
		ready = true
		return nil
	})
	return ready, err
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

func (s *AuthorizedStore) ListCodeSymbols(ctx context.Context, filter code.SymbolFilter) ([]code.Symbol, error) {
	if s == nil || s.store == nil {
		return nil, errors.New(authz.DenyRole)
	}
	project, err := normalizeCodeProject(filter.Project)
	if err != nil {
		return nil, err
	}
	filter.Project = project.String()
	if err := s.authorize(ctx, authz.ResourceCode, authz.ActionRead, filter.Project, "", ""); err != nil {
		return nil, err
	}
	var symbols []code.Symbol
	err = s.store.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		codeStore, err := newPostgresCodeStore(ctx, tx, s.store.tenant.WorkspaceID, filter.Project)
		if err != nil {
			return err
		}
		symbols, err = codeStore.ListSymbols(ctx, filter)
		return err
	})
	return symbols, err
}

func (s *AuthorizedStore) GetCodeGraph(ctx context.Context, project string) (*code.CodeGraph, error) {
	if s == nil || s.store == nil {
		return nil, errors.New(authz.DenyRole)
	}
	projectID, err := normalizeCodeProject(project)
	if err != nil {
		return nil, err
	}
	project = projectID.String()
	if err := s.authorize(ctx, authz.ResourceCode, authz.ActionRead, project, "", ""); err != nil {
		return nil, err
	}
	var graph *code.CodeGraph
	err = s.store.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		codeStore, err := newPostgresCodeStore(ctx, tx, s.store.tenant.WorkspaceID, project)
		if err != nil {
			return err
		}
		graph, err = codeStore.GetGraph(ctx, project)
		return err
	})
	return graph, err
}

func (s *AuthorizedStore) SaveCodeGraph(ctx context.Context, graph *code.CodeGraph) error {
	if s == nil || s.store == nil {
		return errors.New(authz.DenyRole)
	}
	if graph == nil {
		return domain.ErrInvalidInput
	}
	projectID, err := normalizeCodeProject(graph.Project)
	if err != nil {
		return err
	}
	graph.Project = projectID.String()
	if err := s.authorize(ctx, authz.ResourceCode, authz.ActionWrite, graph.Project, "", ""); err != nil {
		return err
	}
	return s.store.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		codeStore, err := newPostgresCodeStore(ctx, tx, s.store.tenant.WorkspaceID, graph.Project)
		if err != nil {
			return err
		}
		return codeStore.replaceGraph(ctx, graph)
	})
}

func (s *AuthorizedStore) GetRAGStats(ctx context.Context, project string) (*domain.RAGStats, error) {
	if s == nil || s.store == nil {
		return nil, errors.New(authz.DenyRole)
	}
	obsList, err := s.ListObservations(ctx, domain.ObservationFilter{Project: project, Limit: 10000})
	if err != nil {
		return nil, err
	}
	total := len(obsList)
	indexed := 0
	pending := 0
	failed := 0

	for _, o := range obsList {
		if o.HasEmbedding || o.RAGStatus == "indexed" || o.RAGStatus == "" {
			indexed++
		} else if o.RAGStatus == "pending" {
			pending++
		} else if o.RAGStatus == "failed" {
			failed++
		}
	}
	coverage := 100.0
	if total > 0 {
		coverage = float64(indexed) / float64(total) * 100.0
	}
	return &domain.RAGStats{
		Project:             project,
		TotalObservations:   total,
		IndexedObservations: indexed,
		PendingObservations: pending,
		FailedObservations:  failed,
		CoveragePct:         coverage,
		EmbeddingModel:      "text-embedding-3-small",
		EmbeddingDim:        1536,
		VectorProvider:      "pgvector/hnsw",
	}, nil
}
