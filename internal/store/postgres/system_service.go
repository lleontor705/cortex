package postgres

import (
	"context"
	"time"

	"github.com/lleontor705/cortex/internal/authz"
	"github.com/lleontor705/cortex/internal/domain"
)

// SystemService is the only capability granted to background lifecycle work.
// It intentionally exposes no repository and derives its identity from the
// already verified server principal; clients cannot construct or supply it.
type SystemService struct {
	store     *AuthorizedStore
	principal domain.Principal
}

func NewSystemService(store *AuthorizedStore) (*SystemService, error) {
	if store == nil || store.store == nil {
		return nil, ErrAuthorizedStoreRequired
	}
	p := store.store.principal
	p.Type = "service_account"
	p.Roles = []string{"service-account"}
	p.Scopes = []string{"memory:read", "memory:delete", "project:*"}
	return &SystemService{store: store, principal: p}, nil
}

func (s *SystemService) authorize(ctx context.Context, action authz.Action, project string) error {
	t := authz.Tenant{ID: s.store.store.tenant.TenantID, WorkspaceID: s.store.store.tenant.WorkspaceID, ProjectID: project}
	return authz.Enforce(ctx, s.store.store.authorizer, authz.Request{Principal: s.principal, Tenant: t, Resource: authz.ResourceRef{TenantID: t.ID, WorkspaceID: t.WorkspaceID, ProjectID: project}, ResourceType: authz.ResourceMemory, Action: action})
}

func (s *SystemService) ListArchivable(ctx context.Context, cutoff time.Time, minScore float64, limit int) ([]*domain.Observation, error) {
	if err := s.authorize(ctx, authz.ActionRead, ""); err != nil {
		return nil, err
	}
	return s.store.store.observations().ListArchivable(ctx, cutoff, minScore, limit)
}

func (s *SystemService) Delete(ctx context.Context, id int64) error {
	ref, err := s.store.observationResource(ctx, id)
	if err != nil {
		return err
	}
	if err := s.authorize(ctx, authz.ActionDelete, ref.ProjectID); err != nil {
		return err
	}
	return s.store.store.observations().Delete(ctx, id)
}
