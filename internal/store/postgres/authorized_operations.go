package postgres

// This file is the server capability boundary. Callers get operations, never
// repository objects. The compatibility repository accessors below are kept on
// AuthorizedStore only for old in-package integrations and are not used by the
// server composition root.

import (
	"context"
	"errors"

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

func (s *AuthorizedStore) SaveObservation(ctx context.Context, o *domain.Observation) error {
	if o == nil {
		return domain.ErrInvalidInput
	}
	if err := s.authorize(ctx, authz.ResourceMemory, authz.ActionWrite, o.Project, s.store.principal.Subject, o.Scope); err != nil {
		return err
	}
	return s.store.Observations().Save(ctx, o)
}
func (s *AuthorizedStore) GetObservationByID(ctx context.Context, id int64) (*domain.Observation, error) {
	var project string
	if err := s.store.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT COALESCE(project_key,'') FROM observations WHERE tenant_id=public.cortex_current_tenant() AND id=$1 AND deleted_at IS NULL`, id).Scan(&project)
	}); err != nil {
		return nil, err
	}
	if err := s.authorize(ctx, authz.ResourceMemory, authz.ActionRead, project, "", ""); err != nil {
		return nil, err
	}
	return s.store.Observations().GetByID(ctx, id)
}
func (s *AuthorizedStore) ListObservations(ctx context.Context, f domain.ObservationFilter) ([]*domain.Observation, error) {
	if err := s.authorize(ctx, authz.ResourceMemory, authz.ActionRead, f.Project, "", ""); err != nil {
		return nil, err
	}
	return s.store.Observations().List(ctx, f)
}
func (s *AuthorizedStore) SearchObservations(ctx context.Context, query string, opts domain.SearchOptions) ([]*domain.SearchResult, error) {
	if err := s.authorize(ctx, authz.ResourceSearch, authz.ActionSearch, opts.Project, "", ""); err != nil {
		return nil, err
	}
	return s.store.Search().Search(ctx, query, opts)
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
	return s.store.Observations().SaveBulk(ctx, observations)
}

func (s *AuthorizedStore) CreateGraphEdge(ctx context.Context, e *domain.Edge) error {
	if e == nil {
		return domain.ErrInvalidInput
	}
	var fromProject, toProject string
	if err := s.store.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT COALESCE(a.project_key,''),COALESCE(b.project_key,'') FROM observations a JOIN observations b ON b.id=$2 WHERE a.id=$1 AND a.deleted_at IS NULL AND b.deleted_at IS NULL`, e.FromObsID, e.ToObsID).Scan(&fromProject, &toProject)
	}); err != nil {
		return err
	}
	if fromProject == "" || fromProject != toProject {
		return errors.New(authz.DenyProject)
	}
	if err := s.authorize(ctx, authz.ResourceGraph, authz.ActionWrite, fromProject, "", ""); err != nil {
		return err
	}
	return s.store.Graph().CreateEdge(ctx, e)
}
func (s *AuthorizedStore) GetGraphEdge(ctx context.Context, id int64) (*domain.Edge, error) {
	if err := s.authorize(ctx, authz.ResourceGraph, authz.ActionRead, "", "", ""); err != nil {
		return nil, err
	}
	return s.store.Graph().GetEdge(ctx, id)
}
func (s *AuthorizedStore) GetRelatedObservations(ctx context.Context, id int64, depth int) ([]*domain.Observation, error) {
	if err := s.authorize(ctx, authz.ResourceGraph, authz.ActionRead, "", "", ""); err != nil {
		return nil, err
	}
	return s.store.Graph().GetRelated(ctx, id, depth)
}
func (s *AuthorizedStore) DeleteGraphEdge(ctx context.Context, id int64) error {
	if err := s.authorize(ctx, authz.ResourceGraph, authz.ActionDelete, "", "", ""); err != nil {
		return err
	}
	return s.store.Graph().DeleteEdge(ctx, id)
}
func (s *AuthorizedStore) GetImportanceScore(ctx context.Context, id int64) (*domain.ImportanceScore, error) {
	if err := s.authorize(ctx, authz.ResourceMemory, authz.ActionRead, "", "", ""); err != nil {
		return nil, err
	}
	return s.store.GetScore(ctx, id)
}
func (s *AuthorizedStore) UpdateImportanceScore(ctx context.Context, id int64, increment float64) error {
	if err := s.authorize(ctx, authz.ResourceMemory, authz.ActionWrite, "", "", ""); err != nil {
		return err
	}
	return s.store.UpdateScore(ctx, id, increment)
}
func (s *AuthorizedStore) IssueToken(ctx context.Context, in identity.TokenIssue) (identity.IssuedToken, error) {
	if err := s.authorize(ctx, authz.ResourceTokens, authz.ActionManage, "", in.Subject, ""); err != nil {
		return identity.IssuedToken{}, err
	}
	return s.store.Tokens().Issue(ctx, in)
}
func (s *AuthorizedStore) VerifyToken(ctx context.Context, secret, scope string) (identity.Principal, error) {
	if err := s.authorize(ctx, authz.ResourceTokens, authz.ActionRead, "", "", ""); err != nil {
		return identity.Principal{}, err
	}
	return s.store.Tokens().Verify(ctx, secret, scope)
}
func (s *AuthorizedStore) RevokeToken(ctx context.Context, id string) error {
	if err := s.authorize(ctx, authz.ResourceTokens, authz.ActionManage, "", "", ""); err != nil {
		return err
	}
	return s.store.Tokens().Revoke(ctx, id)
}
func (s *AuthorizedStore) RotateToken(ctx context.Context, id string) (identity.IssuedToken, error) {
	if err := s.authorize(ctx, authz.ResourceTokens, authz.ActionManage, "", "", ""); err != nil {
		return identity.IssuedToken{}, err
	}
	return s.store.Tokens().Rotate(ctx, id)
}
func (s *AuthorizedStore) ListTokens(ctx context.Context) ([]identity.TokenRecord, error) {
	if err := s.authorize(ctx, authz.ResourceTokens, authz.ActionManage, "", "", ""); err != nil {
		return nil, err
	}
	return s.store.Tokens().List(ctx)
}
