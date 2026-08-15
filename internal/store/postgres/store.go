// Package postgres contains the server-mode repositories. Every operation is
// transaction scoped and binds the authenticated tenant through the
// SECURITY DEFINER function installed by the server migration.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lleontor705/cortex/internal/authz"
	"github.com/lleontor705/cortex/internal/domain"
)

var (
	ErrTenantContextRequired   = errors.New("postgres store: tenant context is required")
	ErrPrincipalRequired       = errors.New("postgres store: principal is required")
	ErrGrantDigestRequired     = errors.New("postgres store: grant digest is required")
	ErrGrantVersionRequired    = errors.New("postgres store: grant version is required")
	ErrAuthorizedStoreRequired = errors.New("postgres store: authorized store is required")
)

type tenantKey struct{}
type principalKey struct{}
type actorKey struct{}

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
	if _, err := uuid.Parse(p.OrgID); err != nil {
		return fmt.Errorf("%w: org id: %v", ErrPrincipalRequired, err)
	}
	return nil
}

// Store is a tenant-scoped PostgreSQL repository bundle.
type Store struct {
	pool         *pgxpool.Pool
	tenant       *domain.TenantContext
	principal    domain.Principal
	authorized   bool
	grantDigest  string
	grantVersion int64
	authorizer   authz.Authorizer
}

// AuthorizedStore is the only server-facing storage capability. The raw Store
// remains package-private in composition; transports receive repository ports
// and cannot select a tenant or bypass the authorization binding.
type AuthorizedStore struct {
	store *Store // composition-only compatibility; never exposed to transports
	caps  *internalCapabilities
}

// rawStore and internalCapabilities are deliberately unexported. They are
// retained only by operation implementations and SystemService; transports
// receive AuthorizedStore's operation-specific facade and cannot obtain a
// transaction, repository, or scoring primitive.
type rawStore struct{ store *Store }

type internalCapabilities struct{ raw *rawStore }

func newCapabilities(s *Store) *internalCapabilities {
	return &internalCapabilities{raw: &rawStore{store: s}}
}

func validateAuthorizedContext(ac authz.AuthorizedContext) error {
	if ac.Principal.Subject == "" || ac.Principal.OrgID == "" {
		return ErrPrincipalRequired
	}
	if ac.GrantDigest == "" {
		return ErrGrantDigestRequired
	}
	if ac.Principal.GrantVersion <= 0 {
		return ErrGrantVersionRequired
	}
	if ac.Tenant.TenantID == "" || ac.Tenant.TenantID != ac.Principal.OrgID {
		return ErrTenantContextRequired
	}
	return nil
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
	if tenant.TenantID != principal.OrgID {
		return nil, fmt.Errorf("postgres store: tenant %q does not match principal org %q", tenant.TenantID, principal.OrgID)
	}
	if tenant.WorkspaceID != "" {
		granted := false
		for _, id := range principal.WorkspaceIDs {
			if id == tenant.WorkspaceID {
				granted = true
				break
			}
		}
		if !granted {
			return nil, fmt.Errorf("postgres store: principal is not granted workspace %q", tenant.WorkspaceID)
		}
	}
	return &Store{pool: pool, tenant: tenant, principal: principal}, nil
}

// NewAuthorizedStore is the server-safe constructor. The tenant and grants
// are taken from a prior authorization decision; callers cannot supply a
// client-owned tenant independently of the verified principal.
func NewAuthorizedStore(pool *pgxpool.Pool, ac authz.AuthorizedContext) (*AuthorizedStore, error) {
	if err := validateAuthorizedContext(ac); err != nil {
		return nil, err
	}
	t := ac.Tenant
	s, err := NewStore(pool, &t, ac.Principal)
	if err != nil {
		return nil, err
	}
	s.authorized = true
	s.grantDigest = ac.GrantDigest
	s.grantVersion = ac.Principal.GrantVersion
	s.authorizer = authz.NewPolicy()
	return &AuthorizedStore{store: s, caps: newCapabilities(s)}, nil
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
	if s.authorized {
		if s.grantDigest == "" {
			return ErrGrantDigestRequired
		}
		if _, err := uuid.Parse(s.principal.Subject); err != nil {
			return fmt.Errorf("%w: principal public id: %v", ErrPrincipalRequired, err)
		}
		_, err := tx.Exec(ctx, `SELECT public.cortex_bind_principal($1::uuid,$2::text,$3::bigint)`, s.principal.Subject, s.grantDigest, s.grantVersion)
		if err != nil {
			return fmt.Errorf("postgres store: bind principal: %w", err)
		}
		return nil
	}
	// Compatibility stores cannot safely establish server authorization.
	// Refuse before any tenant value can reach PostgreSQL.
	return ErrAuthorizedStoreRequired
}
func (s *Store) context(ctx context.Context) context.Context {
	return withPrincipal(withTenant(ctx, s.tenant), s.principal)
}

func (s *Store) projectGrantFilter() (projects []string, wildcard bool) {
	for _, p := range s.principal.ProjectIDs {
		if p == "*" {
			return nil, true
		}
		projects = append(projects, p)
	}
	for _, scope := range s.principal.Scopes {
		if scope == "project:*" {
			return nil, true
		}
		if len(scope) > len("project:") && scope[:len("project:")] == "project:" {
			projects = append(projects, scope[len("project:"):])
		}
	}
	return projects, false
}

func (s *Store) classificationGrantFilter() (classes []string, wildcard bool) {
	if s.principal.Type == "service_account" || len(s.principal.ClassificationClearance) == 0 {
		return nil, false
	}
	for _, c := range s.principal.ClassificationClearance {
		if c == "*" {
			return nil, true
		}
		classes = append(classes, c)
	}
	return classes, false
}
func (s *Store) transaction(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	ctx = s.context(ctx)
	if tx, ok := txFromContext(ctx); ok {
		return fn(ctx, tx)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("postgres store: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := s.bind(ctx, tx); err != nil {
		return err
	}
	ctx, err = s.bindActor(ctx, tx)
	if err != nil {
		return err
	}
	// The fresh-transaction path must expose the tx to the closure through
	// the context: receipt primitives (claim/read/finalize) resolve it via
	// txFromContext, exactly like the WithinTx path does.
	ctx = context.WithValue(ctx, txKey{}, tx)
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
	ctx = context.WithValue(s.context(ctx), txKey{}, tx)
	ctx, err := s.bindActor(ctx, tx)
	if err != nil {
		return err
	}
	return fn(ctx)
}

var _ domain.TxParticipant = (*Store)(nil)

func notFound(kind string, id any) error { return &domain.NotFoundError{Type: kind, ID: id} }

func (s *Store) bindActor(ctx context.Context, tx pgx.Tx) (context.Context, error) {
	if id, err := uuid.Parse(s.principal.Subject); err == nil {
		return context.WithValue(ctx, actorKey{}, id), nil
	}
	var id uuid.UUID
	err := tx.QueryRow(ctx, `INSERT INTO actor_subjects(tenant_id,subject,actor_type) VALUES(public.cortex_current_tenant(),$1,$2) ON CONFLICT(tenant_id,subject) DO UPDATE SET subject=EXCLUDED.subject RETURNING public_id`, s.principal.Subject, s.principal.Type).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("postgres store: resolve actor: %w", err)
	}
	return context.WithValue(ctx, actorKey{}, id), nil
}

func actorFromContext(ctx context.Context) any {
	if id, ok := ctx.Value(actorKey{}).(uuid.UUID); ok {
		return id
	}
	return nil
}

type txKey struct{}

func txFromContext(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey{}).(pgx.Tx)
	return tx, ok
}
