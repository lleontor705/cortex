// Package postgres contains the server-mode repositories. Every operation is
// transaction scoped and binds the authenticated tenant through the
// SECURITY DEFINER function installed by the server migration.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lleontor705/cortex/internal/domain"
)

var (
	ErrTenantContextRequired = errors.New("postgres store: tenant context is required")
	ErrPrincipalRequired     = errors.New("postgres store: principal is required")
)

type tenantKey struct{}
type principalKey struct{}

func withTenant(ctx context.Context, t *domain.TenantContext) context.Context {
	return context.WithValue(ctx, tenantKey{}, t)
}
func tenantFromContext(ctx context.Context) (*domain.TenantContext, bool) {
	t, ok := ctx.Value(tenantKey{}).(*domain.TenantContext)
	return t, ok
}
func withPrincipal(ctx context.Context, p domain.Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}
func principalFromContext(ctx context.Context) (domain.Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(domain.Principal)
	return p, ok
}

func validateTenantContext(t *domain.TenantContext) error {
	if t == nil || t.TenantID == "" {
		return ErrTenantContextRequired
	}
	if _, err := uuid.Parse(t.TenantID); err != nil {
		return fmt.Errorf("%w: tenant id: %v", ErrTenantContextRequired, err)
	}
	if t.WorkspaceID != "" {
		if _, err := uuid.Parse(t.WorkspaceID); err != nil {
			return fmt.Errorf("%w: workspace id: %v", ErrTenantContextRequired, err)
		}
	}
	return nil
}

func validatePrincipal(p domain.Principal) error {
	if p.Subject == "" || p.OrgID == "" {
		return ErrPrincipalRequired
	}
	return nil
}

// Store is a tenant-scoped PostgreSQL repository bundle.
type Store struct {
	pool      *pgxpool.Pool
	tenant    *domain.TenantContext
	principal domain.Principal
}

func NewStore(pool *pgxpool.Pool, tenant *domain.TenantContext, principal domain.Principal) (*Store, error) {
	if pool == nil {
		return nil, errors.New("postgres store: nil pool")
	}
	if err := validateTenantContext(tenant); err != nil {
		return nil, err
	}
	if err := validatePrincipal(principal); err != nil {
		return nil, err
	}
	return &Store{pool: pool, tenant: tenant, principal: principal}, nil
}

func (s *Store) Backend() string { return "postgres" }
func (s *Store) Health(ctx context.Context) domain.Health {
	if err := s.pool.Ping(ctx); err != nil {
		return domain.Health{Status: domain.StatusUnhealthy, Message: err.Error()}
	}
	return domain.Health{Status: domain.StatusHealthy}
}
func (s *Store) BeginTx(ctx context.Context) (domain.Tx, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("postgres store: begin: %w", err)
	}
	if err := s.bind(ctx, tx); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return &txHandle{tx: tx, ctx: ctx}, nil
}
func (s *Store) bind(ctx context.Context, tx pgx.Tx) error {
	if err := validateTenantContext(s.tenant); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `SELECT public.cortex_set_tenant($1::uuid)`, s.tenant.TenantID)
	if err != nil {
		return fmt.Errorf("postgres store: bind tenant: %w", err)
	}
	return nil
}
func (s *Store) context(ctx context.Context) context.Context {
	return withPrincipal(withTenant(ctx, s.tenant), s.principal)
}
func (s *Store) transaction(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	ctx = s.context(ctx)
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("postgres store: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := s.bind(ctx, tx); err != nil {
		return err
	}
	if err := fn(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres store: commit: %w", err)
	}
	return nil
}

type txHandle struct {
	tx  pgx.Tx
	ctx context.Context
}

func (t *txHandle) Commit() error   { return t.tx.Commit(t.ctx) }
func (t *txHandle) Rollback() error { return t.tx.Rollback(t.ctx) }
func (t *txHandle) Handle() any     { return t.tx }

func (s *Store) WithinTx(ctx context.Context, handle any, fn func(context.Context) error) error {
	tx, ok := handle.(pgx.Tx)
	if !ok || tx == nil {
		return fmt.Errorf("postgres store: WithinTx expected pgx.Tx, got %T", handle)
	}
	if err := s.bind(ctx, tx); err != nil {
		return err
	}
	return fn(s.context(ctx))
}

var _ domain.TxParticipant = (*Store)(nil)

func notFound(kind string, id any) error { return &domain.NotFoundError{Type: kind, ID: id} }
func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}
