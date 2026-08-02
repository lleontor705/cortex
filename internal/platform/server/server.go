// Package server is the PostgreSQL composition root. It is deliberately kept
// below internal/platform so the local composition never imports this package
// or any network-backed adapter.
package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lleontor705/cortex/internal/authz"
	"github.com/lleontor705/cortex/internal/config"
	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/domain/lifecycle"
	"github.com/lleontor705/cortex/internal/embedding"
	"github.com/lleontor705/cortex/internal/migration"
	"github.com/lleontor705/cortex/internal/server/external"
	postgresstore "github.com/lleontor705/cortex/internal/store/postgres"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// Runtime owns every resource created by Open. Close is idempotent and follows
// the dependency order: HTTP transport, background lifecycle, vector client,
// SQL pool, then migration handle.
type Runtime struct {
	Config        *config.Config
	Pool          *pgxpool.Pool
	Vectors       domain.VectorIndex
	Embeddings    embedding.Service
	Lifecycle     *lifecycle.ArchivalService
	httpServer    *http.Server
	mcpTransport  *mcpserver.StreamableHTTPServer
	stopLifecycle context.CancelFunc
	transportOnce sync.Once
	transportErr  error
	closeOnce     sync.Once
	closeErr      error
}

// Open validates server-only configuration, applies the PostgreSQL migration,
// and constructs tenant-scoped repositories. It performs no work for local
// mode; callers select this root explicitly.
func Open(ctx context.Context, cfg config.Config) (*Runtime, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	storage := cfg.Server.Storage
	migrationDSN := storage.MigrationDSN
	if migrationDSN == "" {
		migrationDSN = storage.DSN
	}
	migrationDB, err := sql.Open("pgx", migrationDSN)
	if err != nil {
		return nil, fmt.Errorf("server: open PostgreSQL migration handle: %w", err)
	}
	cleanupSQL := true
	defer func() {
		if cleanupSQL {
			_ = migrationDB.Close()
		}
	}()

	m, err := migration.NewPostgresServerMigration()
	if err != nil {
		return nil, err
	}
	if err := m.Apply(ctx, migrationDB); err != nil {
		return nil, fmt.Errorf("server: apply migration: %w", err)
	}
	if cfg.Server.BootstrapDevelopment {
		if err := bootstrapDevelopmentData(ctx, migrationDB, cfg); err != nil {
			return nil, fmt.Errorf("server: bootstrap development data: %w", err)
		}
	}
	if err := migrationDB.Close(); err != nil {
		return nil, fmt.Errorf("server: close PostgreSQL migration handle: %w", err)
	}
	cleanupSQL = false

	poolCfg, err := pgxpool.ParseConfig(storage.DSN)
	if err != nil {
		return nil, fmt.Errorf("server: parse PostgreSQL DSN: %w", err)
	}
	if storage.MaxConns > 0 {
		poolCfg.MaxConns = storage.MaxConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("server: create PostgreSQL pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("server: PostgreSQL health check: %w", err)
	}

	roles := append([]string(nil), cfg.Server.Roles...)
	scopes := append([]string(nil), cfg.Server.Scopes...)
	projectIDs := append([]string(nil), cfg.Server.ProjectIDs...)
	clearance := append([]string(nil), cfg.Server.ClassificationClearance...)
	if cfg.Server.BootstrapDevelopment {
		roles = []string{string(authz.RoleOwner)}
		scopes = []string{"workspaces:read"}
		projectIDs = []string{"*"}
		clearance = []string{"*"}
	}
	principal := domain.Principal{Subject: cfg.Server.PrincipalSubject, Type: "service_account", OrgID: cfg.Server.TenantID, WorkspaceIDs: []string{cfg.Server.WorkspaceID}, ProjectIDs: projectIDs, Roles: roles, Scopes: scopes, ClassificationClearance: clearance, GrantDigest: cfg.Server.GrantDigest, GrantVersion: cfg.Server.GrantVersion}
	audit, err := postgresstore.NewAuditSink(pool, cfg.Server.PrincipalSubject, cfg.Server.GrantDigest, cfg.Server.GrantVersion)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("server: construct audit sink: %w", err)
	}
	policy := authz.NewPolicy()
	policy.Audit = audit
	ac, err := authz.NewAuthorizedContext(ctx, policy, authz.Request{Principal: principal, Tenant: authz.Tenant{ID: cfg.Server.TenantID, WorkspaceID: cfg.Server.WorkspaceID}, ResourceType: authz.ResourceWorkspaces, Action: authz.ActionRead})
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("server: authorize composition: %w", err)
	}
	store, err := postgresstore.NewAuthorizedStore(pool, ac)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("server: construct storage: %w", err)
	}

	model := domain.ModelInfo{Name: cfg.Search.EmbeddingModel, Dimension: embeddingDimensions(cfg.Search.EmbeddingProvider)}
	emb := embedding.New(embedding.Config{Provider: cfg.Search.EmbeddingProvider, Model: cfg.Search.EmbeddingModel, BaseURL: cfg.Search.EmbeddingBaseURL})
	if emb != nil {
		model.Name, model.Dimension = emb.Model(), emb.Dimensions()
	}
	vectorCfg := cfg.Vector
	if vectorCfg.Provider == "" {
		vectorCfg.Provider = cfg.Server.Provider.Vector
	}
	if vectorCfg.Provider == "" {
		vectorCfg.Provider = "none"
	}
	vec, err := external.NewVectorIndex(ctx, vectorCfg, external.FactoryInput{ModelInfo: model})
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("server: construct vector provider: %w", err)
	}

	system, err := postgresstore.NewSystemService(store)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("server: construct system service: %w", err)
	}
	handler, transport := newHTTPHandler(cfg, store, pool.Ping)
	rt := &Runtime{
		Config:     &cfg,
		Pool:       pool,
		Vectors:    vec,
		Embeddings: emb,
		httpServer: &http.Server{
			Addr:              listenAddress(cfg.HTTP),
			Handler:           handler,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      0, // Streamable HTTP MCP may keep SSE responses open.
			IdleTimeout:       90 * time.Second,
		},
		mcpTransport: transport,
	}
	interval, parseErr := time.ParseDuration(cfg.Lifecycle.ArchiveCheckInterval)
	if parseErr != nil || interval <= 0 {
		interval = time.Hour
	}
	rt.Lifecycle = lifecycle.NewArchivalService(system, lifecycle.ArchivalConfig{MaxAgeDays: cfg.Memory.AutoArchiveDays, MinArchiveScore: cfg.Memory.MinArchiveScore, CheckInterval: interval})
	if cfg.Lifecycle.EnableAutoArchive {
		rt.stopLifecycle = rt.Lifecycle.Start(ctx)
	}
	return rt, nil
}

// bootstrapDevelopmentData creates the minimum tenant fixtures needed by the
// Docker smoke stack. It is opt-in and uses the privileged migration handle;
// production deployments must provision these records through their control
// plane instead of enabling this flag.
func bootstrapDevelopmentData(ctx context.Context, db *sql.DB, cfg config.Config) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `GRANT cortex_app TO cortex_test`); err != nil {
		return fmt.Errorf("application role: %w", err)
	}

	var organizationID int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO organizations (public_id, tenant_id, name)
		VALUES ($1::uuid, $2::uuid, 'Cortex Docker Development')
		ON CONFLICT (tenant_id) DO UPDATE SET public_id = EXCLUDED.public_id
		RETURNING id`, cfg.Server.TenantID, cfg.Server.TenantID).Scan(&organizationID); err != nil {
		return fmt.Errorf("organization: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workspaces (public_id, tenant_id, organization_id, name)
		VALUES ($1::uuid, $2::uuid, $3, 'Cortex Docker Workspace')
		ON CONFLICT (tenant_id, public_id) DO UPDATE SET organization_id = EXCLUDED.organization_id`, cfg.Server.WorkspaceID, cfg.Server.TenantID, organizationID); err != nil {
		return fmt.Errorf("workspace: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO actor_subjects (public_id, tenant_id, subject, actor_type, grant_version, grant_digest)
		VALUES ($1::uuid, $2::uuid, 'cortex-docker-service', 'service_account', $3, $4)
		ON CONFLICT (tenant_id, subject) DO UPDATE SET public_id = EXCLUDED.public_id, active = true, revoked_at = NULL, grant_version = EXCLUDED.grant_version, grant_digest = EXCLUDED.grant_digest`, cfg.Server.PrincipalSubject, cfg.Server.TenantID, cfg.Server.GrantVersion, cfg.Server.GrantDigest); err != nil {
		return fmt.Errorf("actor subject: %w", err)
	}
	return tx.Commit()
}

func validateConfig(cfg config.Config) error {
	if cfg.Server.Storage.Driver != "postgres" {
		return fmt.Errorf("server: storage.driver must be postgres")
	}
	if cfg.Server.Storage.DSN == "" {
		return errors.New("server: storage DSN is required")
	}
	for name, value := range map[string]string{"tenant_id": cfg.Server.TenantID, "workspace_id": cfg.Server.WorkspaceID, "principal_subject": cfg.Server.PrincipalSubject} {
		if value == "" {
			return fmt.Errorf("server: %s is required", name)
		}
	}
	if _, err := uuid.Parse(cfg.Server.TenantID); err != nil {
		return fmt.Errorf("server: tenant_id is invalid")
	}
	if _, err := uuid.Parse(cfg.Server.WorkspaceID); err != nil {
		return fmt.Errorf("server: workspace_id is invalid")
	}
	if cfg.Server.GrantDigest == "" {
		return errors.New("server: grant_digest is required")
	}
	if cfg.Server.GrantVersion <= 0 {
		return errors.New("server: grant_version is required")
	}
	if !cfg.Server.BootstrapDevelopment && len(cfg.Server.Roles) == 0 && len(cfg.Server.Scopes) == 0 {
		return errors.New("server: at least one configured role or scope is required")
	}
	if cfg.HTTP.Port < 1 || cfg.HTTP.Port > 65535 {
		return errors.New("server: http.port must be between 1 and 65535")
	}
	if !cfg.HTTP.Enabled {
		return errors.New("server: http.enabled must be true for HTTP and MCP transports")
	}
	if cfg.HTTP.Token == "" {
		return errors.New("server: http.token is required")
	}
	return nil
}

func embeddingDimensions(provider string) int {
	switch provider {
	case "openai":
		return 1536
	case "ollama":
		return 768
	default:
		return 0
	}
}

// Close gracefully shuts down background work before closing database handles.
func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		var errs []error
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := r.shutdownTransport(shutdownCtx); err != nil {
			errs = append(errs, err)
		}
		if r.httpServer != nil {
			if err := r.httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errs = append(errs, err)
			}
		}
		if r.stopLifecycle != nil {
			r.stopLifecycle()
			if r.Lifecycle != nil {
				r.Lifecycle.Stop()
			}
		}
		if r.Vectors != nil {
			if err := r.Vectors.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if r.Embeddings != nil {
			if c, ok := r.Embeddings.(io.Closer); ok {
				if err := c.Close(); err != nil {
					errs = append(errs, err)
				}
			}
		}
		if r.Pool != nil {
			r.Pool.Close()
		}
		r.closeErr = errors.Join(errs...)
	})
	return r.closeErr
}

func (r *Runtime) shutdownTransport(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.transportOnce.Do(func() {
		if r.mcpTransport != nil {
			r.transportErr = r.mcpTransport.Shutdown(ctx)
		}
	})
	return r.transportErr
}
