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
)

// Runtime owns every resource created by Open. Close is idempotent and follows
// the dependency order: background lifecycle, vector client, SQL pool, then
// migration handle. No transport or identity service is started in W11.
type Runtime struct {
	Config        *config.Config
	MigrationDB   *sql.DB
	Pool          *pgxpool.Pool
	Vectors       domain.VectorIndex
	Embeddings    embedding.Service
	Lifecycle     *lifecycle.ArchivalService
	stopLifecycle context.CancelFunc
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
	migrationDB, err := sql.Open("pgx", storage.DSN)
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

	principal := domain.Principal{Subject: cfg.Server.PrincipalSubject, Type: "service_account", OrgID: cfg.Server.TenantID, WorkspaceIDs: []string{cfg.Server.WorkspaceID}, Scopes: []string{"workspaces:read"}, GrantDigest: cfg.Server.GrantDigest, GrantVersion: cfg.Server.GrantVersion}
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
	rt := &Runtime{Config: &cfg, MigrationDB: migrationDB, Pool: pool, Vectors: vec, Embeddings: emb}
	interval, parseErr := time.ParseDuration(cfg.Lifecycle.ArchiveCheckInterval)
	if parseErr != nil || interval <= 0 {
		interval = time.Hour
	}
	rt.Lifecycle = lifecycle.NewArchivalService(system, lifecycle.ArchivalConfig{MaxAgeDays: cfg.Memory.AutoArchiveDays, MinArchiveScore: cfg.Memory.MinArchiveScore, CheckInterval: interval})
	if cfg.Lifecycle.EnableAutoArchive {
		rt.stopLifecycle = rt.Lifecycle.Start(ctx)
	}
	cleanupSQL = false
	return rt, nil
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
		if r.MigrationDB != nil {
			if err := r.MigrationDB.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		r.closeErr = errors.Join(errs...)
	})
	return r.closeErr
}
