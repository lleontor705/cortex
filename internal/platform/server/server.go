// Package server is the PostgreSQL composition root. It is deliberately kept
// below internal/platform so the local composition never imports this package
// or any network-backed adapter.
package server

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lleontor705/cortex/v2/internal/authz"
	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/extraction"
	"github.com/lleontor705/cortex/v2/internal/domain/lifecycle"
	"github.com/lleontor705/cortex/v2/internal/embedding"
	"github.com/lleontor705/cortex/v2/internal/migration"
	"github.com/lleontor705/cortex/v2/internal/server/external"
	postgresstore "github.com/lleontor705/cortex/v2/internal/store/postgres"
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

type principalOperationsFactoryFunc func(context.Context, domain.Principal) (Operations, error)

func (f principalOperationsFactoryFunc) ForPrincipal(ctx context.Context, principal domain.Principal) (Operations, error) {
	return f(ctx, principal)
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

	if err := migration.ApplyPostgresServerMigrations(ctx, migrationDB); err != nil {
		return nil, fmt.Errorf("server: apply migration: %w", err)
	}
	if cfg.Server.BootstrapDevelopment {
		if err := bootstrapDevelopmentData(ctx, migrationDB, cfg); err != nil {
			return nil, fmt.Errorf("server: bootstrap development data: %w", err)
		}
	}
	// Durable bootstrap reconciliation runs on EVERY startup while the
	// migration handle is still open: cortex_migration is the only role
	// allowed to provision or reconcile the configured service principal,
	// its canonical grants, and the reserved bootstrap token. Development
	// startups take this same path; only the prerequisite fixtures differ.
	if err := bootstrapServicePrincipal(ctx, migrationDB, cfg); err != nil {
		return nil, err
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

	// The configured bearer is a secret, never a principal. It is verified
	// through the narrow tenant-scoped verifier before any store exists,
	// and startup fails closed when it does not verify. The verified
	// principal's provenance and grant version feed audit/store
	// composition transiently; they never enter Runtime.Config.
	verifier, err := postgresstore.NewTokenPrincipalVerifier(pool, cfg.Server.TenantID)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("server: construct token verifier: %w", err)
	}
	principal, err := verifier.VerifyToken(ctx, cfg.HTTP.Token, "")
	if err != nil {
		pool.Close()
		return nil, redactStageError(fmt.Errorf("server: verify bootstrap credentials: %w", err), cfg.HTTP.Token)
	}
	audit, err := postgresstore.NewAuditSink(pool, principal.Subject, principal.GrantDigest, principal.GrantVersion)
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
	if model.Dimension == 0 {
		if cfg.Vector.Pgvector.Dimension > 0 {
			model.Dimension = cfg.Vector.Pgvector.Dimension
		} else if cfg.Vector.Qdrant.Dimension > 0 {
			model.Dimension = cfg.Vector.Qdrant.Dimension
		}
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
	factory := principalOperationsFactoryFunc(func(ctx context.Context, requestPrincipal domain.Principal) (Operations, error) {
		audit, err := postgresstore.NewAuditSink(pool, requestPrincipal.Subject, requestPrincipal.GrantDigest, requestPrincipal.GrantVersion)
		if err != nil {
			return nil, err
		}
		policy := authz.NewPolicy()
		policy.Audit = audit
		requestContext, err := authz.NewAuthorizedContext(ctx, policy, authz.Request{Principal: requestPrincipal, Tenant: authz.Tenant{ID: requestPrincipal.OrgID, WorkspaceID: cfg.Server.WorkspaceID}, ResourceType: authz.ResourceWorkspaces, Action: authz.ActionRead})
		if err != nil {
			return nil, err
		}
		return postgresstore.NewAuthorizedStore(pool, requestContext)
	})
	authenticator := requestAuthenticator{verifier: verifier, factory: factory}
	// SEC-02: the outbound provider is composed exclusively from trusted
	// administrator configuration. Invalid provider configuration fails
	// startup (fail closed); an absent configuration keeps extract and
	// synthesize heuristic-only with no approved outbound destination. A nil
	// extractor preserves the strict default handler wiring.
	llm, err := config.ServerLLMFromEnv()
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("server: outbound llm configuration: %w", err)
	}
	var extractor *extraction.Service
	if llm.Configured() {
		extractor = newConfiguredExtractor(llm)
	}
	handler, transport := newHTTPHandlerWithAuth(cfg, requestOperations{}, pool.Ping, authenticator.middleware, extractor)
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

// bootstrapDevelopmentData creates only the prerequisite tenant fixtures
// needed by the Docker smoke stack: role membership, the organization, and
// the workspace the durable bootstrap reconciler will bind its grants to.
// The service principal, its grants, and its bootstrap token are provisioned
// exclusively by cortex_bootstrap_service_principal through the same durable
// path production uses.
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
	return tx.Commit()
}

// Reserved identity anchors for the durable bootstrap reconciler. They are
// stable deployment constants: the reconciler fails closed if an existing
// service account, actor subject, or reserved token name disagrees with
// them, so they must never vary between restarts.
const (
	bootstrapActorSubject = "cortex-server"
	bootstrapServiceName  = "cortex-server"
	bootstrapTokenName    = "cortex-bootstrap"
	bootstrapAuditReason  = "startup"
	minBearerLength       = 12
)

// bootstrapGrant is the canonical type/value grant object contract of
// cortex_bootstrap_service_principal. The SQL routine re-validates the
// allowlist and recomputes the grant digest inside the database.
type bootstrapGrant struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

var bootstrapGrantKinds = []string{"role", "workspace", "scope", "project", "classification"}

// canonicalBootstrapGrants assembles the unique, allowlisted grant payload
// for the reconciler from configuration. Production requires the configured
// roles to include owner or admin; development supplies its historical
// owner/wildcard defaults. The workspace grant always uses the canonical
// uuid spelling so it matches p_workspace_public_id::text exactly.
func canonicalBootstrapGrants(cfg config.Config) ([]bootstrapGrant, error) {
	roles := append([]string(nil), cfg.Server.Roles...)
	scopes := append([]string(nil), cfg.Server.Scopes...)
	projects := append([]string(nil), cfg.Server.ProjectIDs...)
	clearance := append([]string(nil), cfg.Server.ClassificationClearance...)
	if cfg.Server.BootstrapDevelopment {
		roles = []string{string(authz.RoleOwner)}
		scopes = []string{"workspaces:read"}
		projects = []string{"*"}
		clearance = []string{"*"}
	}
	parsedWorkspace, err := uuid.Parse(cfg.Server.WorkspaceID)
	if err != nil {
		return nil, errors.New("server: workspace_id is invalid")
	}
	byKind := map[string][]string{
		"role":           roles,
		"workspace":      {parsedWorkspace.String()},
		"scope":          scopes,
		"project":        projects,
		"classification": clearance,
	}
	grants := make([]bootstrapGrant, 0, len(roles)+len(scopes)+len(projects)+len(clearance)+1)
	seen := make(map[bootstrapGrant]struct{}, cap(grants))
	hasOwnerOrAdmin := false
	for _, kind := range bootstrapGrantKinds {
		for _, raw := range byKind[kind] {
			value := strings.TrimSpace(raw)
			if value == "" {
				continue
			}
			grant := bootstrapGrant{Type: kind, Value: value}
			if _, duplicate := seen[grant]; duplicate {
				continue
			}
			seen[grant] = struct{}{}
			grants = append(grants, grant)
			if kind == "role" && (value == string(authz.RoleOwner) || value == string(authz.RoleAdmin)) {
				hasOwnerOrAdmin = true
			}
		}
	}
	if !hasOwnerOrAdmin {
		return nil, errors.New("server: bootstrap grants require the owner or admin role")
	}
	return grants, nil
}

// bootstrapServicePrincipal reconciles the configured service principal,
// its canonical grants, and the reserved bootstrap token through the
// migration-owned cortex_bootstrap_service_principal routine. It runs once
// per startup on the still-privileged migration handle, before the
// application pool, the verifier, and any AuthorizedStore exist. The
// presented bearer reaches PostgreSQL only as a bound parameter; errors are
// redacted defensively so no stage label can carry it back out.
func bootstrapServicePrincipal(ctx context.Context, db *sql.DB, cfg config.Config) error {
	grants, err := canonicalBootstrapGrants(cfg)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(grants)
	if err != nil {
		return fmt.Errorf("server: encode bootstrap grants: %w", err)
	}

	var tokenID, action string
	var grantVersion int64
	query := `SELECT token_public_id::text, grant_version, bootstrap_action
		FROM public.cortex_bootstrap_service_principal($1::uuid,$2::uuid,$3::uuid,$4::text,$5::text,$6::jsonb,$7::text,$8::text,$9::text)`
	err = db.QueryRowContext(ctx, query,
		cfg.Server.TenantID,
		cfg.Server.WorkspaceID,
		cfg.Server.PrincipalSubject,
		bootstrapActorSubject,
		bootstrapServiceName,
		string(payload),
		bootstrapTokenName,
		cfg.HTTP.Token,
		bootstrapAuditReason,
	).Scan(&tokenID, &grantVersion, &action)
	if err != nil {
		return redactStageError(fmt.Errorf("server: bootstrap service principal: %w", err), cfg.HTTP.Token)
	}
	if tokenID == "" || grantVersion < 1 {
		return errors.New("server: bootstrap service principal returned an invalid reconciliation result")
	}
	switch action {
	case "provisioned", "reconciled", "token_rotated", "unchanged":
	default:
		// The database-returned action is untrusted text: interpolating it
		// into a startup error could echo a sentinel bearer or provenance
		// payload straight past stage redaction. Report the contract
		// violation without the raw value.
		return redactStageError(errors.New("server: bootstrap service principal returned an unknown bootstrap action"), cfg.HTTP.Token)
	}
	return nil
}

// redactStageError guarantees a startup-stage error never carries a
// presented secret back to callers or logs: each occurrence is replaced
// with a stable placeholder while the stage label survives.
func redactStageError(err error, secrets ...string) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	redacted := false
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		if strings.Contains(message, secret) {
			message = strings.ReplaceAll(message, secret, "[REDACTED]")
			redacted = true
		}
	}
	if !redacted {
		return err
	}
	return errors.New(message)
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
	if _, err := uuid.Parse(cfg.Server.PrincipalSubject); err != nil {
		return fmt.Errorf("server: principal_subject is invalid")
	}
	// Owner/admin canonical-grant and bearer validation run before any
	// database handle is opened. The configured grant_digest and
	// grant_version are deprecated compatibility inputs and are no longer
	// required for authentication or composition.
	if _, err := canonicalBootstrapGrants(cfg); err != nil {
		return err
	}
	if cfg.HTTP.Port < 1 || cfg.HTTP.Port > 65535 {
		return errors.New("server: http.port must be between 1 and 65535")
	}
	if !cfg.HTTP.Enabled {
		return errors.New("server: http.enabled must be true for HTTP and MCP transports")
	}
	return validateBearerToken(cfg.HTTP.Token)
}

// validateBearerToken enforces the canonical configured-bearer form shared by
// every verification path. The request middleware verifies presented secrets
// byte-exact without trimming, so a bearer with leading/trailing whitespace
// could never authenticate an HTTP request; control characters are rejected
// as non-canonical text. The bearer itself is never echoed in the rejection.
func validateBearerToken(token string) error {
	if token == "" {
		return errors.New("server: http.token is required")
	}
	if len(token) < minBearerLength {
		return fmt.Errorf("server: http.token must be at least %d characters", minBearerLength)
	}
	if token != strings.TrimSpace(token) {
		return errors.New("server: http.token must not have leading or trailing whitespace")
	}
	for _, r := range token {
		if unicode.IsControl(r) {
			return errors.New("server: http.token must not contain control characters")
		}
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

// newConfiguredExtractor builds the production extraction service from
// validated administrator configuration (SEC-02). The destination, bounds,
// and development switches come only from trusted server configuration; the
// credential comes only from the environment. The configured destination is
// approved explicitly into the outbound allowlist, and the policy still
// revalidates scheme, port, and address class at the URL, DNS/dial, and
// redirect layers on every outbound request.
func newConfiguredExtractor(llm config.ServerLLMConfig) *extraction.Service {
	policy := extraction.OutboundPolicy{
		AllowedHosts:              llm.AllowedHosts,
		AllowedPorts:              llm.AllowedPorts,
		AllowLoopback:             llm.AllowLoopback,
		AllowInsecureLoopbackHTTP: llm.AllowLoopbackHTTP,
		MaxRedirects:              llm.MaxRedirects,
		MaxResponseBodyBytes:      llm.MaxResponseBodyBytes,
		MaxErrorBodyBytes:         llm.MaxErrorBodyBytes,
		MaxConcurrent:             llm.MaxConcurrent,
	}
	if llm.CACertPool != nil {
		policy.TLSConfig = &tls.Config{RootCAs: llm.CACertPool}
	}
	destination := llm.BaseURL
	if destination == "" {
		destination = extraction.ProviderDefaultBaseURL(extraction.LLMProvider(llm.Provider))
	}
	if destination != "" {
		// The configuration was already validated, so a rejection here is
		// not expected; leaving the destination unapproved would fail closed
		// at request time, so the error is intentionally ignored.
		_ = policy.ApproveDestination(destination)
	}
	return extraction.NewServiceWithPolicy(extraction.Config{
		Provider: extraction.LLMProvider(llm.Provider),
		BaseURL:  llm.BaseURL,
		APIKey:   llm.APIKey,
		Model:    llm.Model,
		Timeout:  llm.Timeout,
	}, policy)
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
