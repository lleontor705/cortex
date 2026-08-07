package postgres

// This file is the server capability boundary. Callers get operations, never
// repository objects.

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lleontor705/cortex/internal/authz"
	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/identity"
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
