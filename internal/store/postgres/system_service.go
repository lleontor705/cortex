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
	caps      *internalCapabilities
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
	caps := store.caps
	if caps == nil {
		caps = newCapabilities(store.store)
	}
	return &SystemService{caps: caps, principal: p}, nil
}

func (s *SystemService) authorize(ctx context.Context, action authz.Action, project string) error {
	raw := s.caps.raw.store
	t := authz.Tenant{ID: raw.tenant.TenantID, WorkspaceID: raw.tenant.WorkspaceID, ProjectID: project}
	return authz.Enforce(ctx, raw.authorizer, authz.Request{Principal: s.principal, Tenant: t, Resource: authz.ResourceRef{TenantID: t.ID, WorkspaceID: t.WorkspaceID, ProjectID: project}, ResourceType: authz.ResourceMemory, Action: action})
}

func (s *SystemService) ListArchivable(ctx context.Context, cutoff time.Time, minScore float64, limit int) ([]*domain.Observation, error) {
	if err := s.authorize(ctx, authz.ActionRead, ""); err != nil {
		return nil, err
	}
	return s.caps.raw.store.observations().ListArchivable(ctx, cutoff, minScore, limit)
}

func (s *SystemService) Delete(ctx context.Context, id int64) error {
	raw := s.caps.raw.store
	ref, err := (&AuthorizedStore{store: raw, caps: s.caps}).observationResource(ctx, id)
	if err != nil {
		return err
	}
	if err := s.authorize(ctx, authz.ActionDelete, ref.ProjectID); err != nil {
		return err
	}
	return raw.observations().Delete(ctx, id)
}
