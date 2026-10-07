// Package server is the PostgreSQL composition root. It is deliberately kept
// below internal/platform so the local composition never imports this package
// or any network-backed adapter.
package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lleontor705/cortex/v2/internal/authz"
	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/domain"
	agentdomain "github.com/lleontor705/cortex/v2/internal/domain/agent"
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
	agent         *serverAgentService
	reindex       reindexCommandDeps
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
	return openRuntime(ctx, cfg, true)
}

// openRuntime composes shared authenticated storage and providers. Transport
// and background lifecycle surfaces are optional for bounded administrative jobs.
func openRuntime(ctx context.Context, cfg config.Config, withServerSurfaces bool) (*Runtime, error) {
	if err := validateRuntimeConfig(cfg, withServerSurfaces); err != nil {
		return nil, err
	}
	storage := cfg.Server.Storage
	runtimeDSN, migrationDSN, err := resolveServerDSNs(cfg)
	if err != nil {
		return nil, err
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
	if cfg.Server.BootstrapDevelopment || dockerAutoBootstrapRequested() {
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
	// The synthetic request principal has no live token, so its binder proof
	// is keyed by the actor's PERSISTED grant digest. That value is only known
	// after the durable reconciler above runs, and only the privileged
	// migration role can read actor_subjects.grant_digest, so it is resolved
	// here while that handle is still open.
	staticProvenance, grantVersion, err := resolveStaticBindProvenance(ctx, migrationDB, cfg)
	if err != nil {
		return nil, err
	}
	if err := migrationDB.Close(); err != nil {
		return nil, fmt.Errorf("server: close PostgreSQL migration handle: %w", err)
	}
	cleanupSQL = false

	poolCfg, err := pgxpool.ParseConfig(runtimeDSN)
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
	if !cfg.Server.BootstrapDevelopment {
		startupAudit := newAgentAuditor(audit)
		if err := startupAudit.RecordAuthorization(ctx, agentdomain.AuditEvent{
			CorrelationID: uuid.NewString(), ActorID: principal.Subject, TenantID: cfg.Server.TenantID,
			WorkspaceID: cfg.Server.WorkspaceID, Project: "server-startup", Transport: agentdomain.TransportJSON,
			ResultClass: "startup_probe",
		}); err != nil {
			pool.Close()
			return nil, fmt.Errorf("server: agent audit unavailable: %w", err)
		}
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

	// Dimension comes only from the live service below (or from the operator's
	// vector configuration): a static provider-to-dimension mirror desyncs as
	// soon as a preset ships or a model is overridden (REQ-EMB-002).
	model := domain.ModelInfo{Name: cfg.Search.EmbeddingModel}
	emb, err := newServerEmbedding(cfg)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("server: outbound embedding configuration: %w", err)
	}
	if emb != nil {
		model.Name = emb.Model()
	}
	model.Dimension = liveEmbeddingDimensions(cfg, emb)
	vectorCfg := cfg.Vector
	vectorCfg.Provider = resolvedVectorProvider(cfg)
	if vectorCfg.Provider == "pgvector" && strings.TrimSpace(vectorCfg.Pgvector.MigrationDSN) == "" {
		vectorCfg.Pgvector.MigrationDSN = cfg.Server.Storage.MigrationDSN
	}
	vec, err := external.NewVectorIndex(ctx, vectorCfg, external.FactoryInput{ModelInfo: model})
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("server: construct vector provider: %w", err)
	}
	if vec != nil {
		vec, err = external.NewServerScopedVectorIndex(vec, cfg.Server.TenantID, cfg.Server.WorkspaceID)
		if err != nil {
			pool.Close()
			return nil, fmt.Errorf("server: scope vector provider: %w", err)
		}
	}

	reindexDeps := reindexCommandDeps{
		target: vec,
		audit:  reindexPostgresAudit{sink: audit},
		source: func(authority reindexAuthority) (external.ReindexSource, error) {
			if authority.source == nil {
				return nil, errors.New("server: reindex authority has no bound source")
			}
			return authority.source, nil
		},
	}
	if emb != nil {
		reindexDeps.provider = reindexEmbeddingProvider{service: emb}
	}
	reindexDeps.authorize = func(ctx context.Context, projectID, correlationID string) (reindexAuthority, error) {
		decision, err := policy.AuthorizeWithAudit(ctx, authz.Request{
			Principal:    principal,
			Tenant:       authz.Tenant{ID: principal.OrgID, WorkspaceID: cfg.Server.WorkspaceID, ProjectID: projectID},
			Resource:     authz.ResourceRef{TenantID: principal.OrgID, WorkspaceID: cfg.Server.WorkspaceID, ProjectID: projectID, OpaqueID: projectID},
			ResourceType: authz.ResourceAdmin, Action: authz.ActionManage, CorrelationID: correlationID,
		})
		if err != nil {
			return reindexAuthority{}, err
		}
		if !decision.Allowed {
			return reindexAuthority{}, errors.New(decision.Reason)
		}
		source, err := postgresstore.NewPostgresReindexSource(ctx, store, projectID)
		if err != nil {
			return reindexAuthority{}, err
		}
		scope := source.ReindexScope()
		return reindexAuthority{
			ActorID: principal.Subject, TenantID: scope.TenantID, WorkspaceID: scope.WorkspaceID,
			ProjectID: scope.ProjectID, ProjectLabel: source.CanonicalProjectLabel(), source: source,
		}, nil
	}
	if !withServerSurfaces {
		return &Runtime{
			Config: &cfg, Pool: pool, Vectors: vec, Embeddings: emb,
			reindex: reindexDeps,
		}, nil
	}

	system, err := postgresstore.NewSystemService(store)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("server: construct system service: %w", err)
	}
	factory := principalOperationsFactoryFunc(func(ctx context.Context, requestPrincipal domain.Principal) (Operations, error) {
		workspaceID := cfg.Server.WorkspaceID
		if selectedWorkspace, ok := workspaceFromContext(ctx); ok {
			workspaceID = selectedWorkspace
		}
		audit, err := postgresstore.NewAuditSink(pool, requestPrincipal.Subject, requestPrincipal.GrantDigest, requestPrincipal.GrantVersion)
		if err != nil {
			return nil, err
		}
		policy := authz.NewPolicy()
		policy.Audit = audit
		requestContext, err := authz.NewAuthorizedContext(ctx, policy, authz.Request{Principal: requestPrincipal, Tenant: authz.Tenant{ID: requestPrincipal.OrgID, WorkspaceID: workspaceID}, ResourceType: authz.ResourceWorkspaces, Action: authz.ActionRead})
		if err != nil {
			return nil, err
		}
		return postgresstore.NewAuthorizedStore(pool, requestContext)
	})
	// The static verifier is the ONLY request identity source: it derives one
	// synthetic constant principal from configuration, carrying the
	// migration-112 static binder proof AND the grant version read from the
	// bootstrapped actor row. A hardcoded version would desynchronize from the
	// sticky grant_version after any canonical-grant reconcile, so every
	// authenticated request would bind a stale version and PostgreSQL would
	// fail closed with SQLSTATE 28000 in a non-recoverable outage.
	requestConfig := cfg
	requestConfig.Server.GrantDigest = staticProvenance
	requestConfig.Server.GrantVersion = grantVersion
	requestVerifier, err := newStaticBearerVerifier(requestConfig)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("server: construct static bearer verifier: %w", err)
	}
	// Equality assertion: the assembled principal must carry the exact version
	// the actor row reported. Divergence means the verifier stopped consuming
	// the composed value, which would reintroduce the HIGH-1 outage.
	if requestVerifier.principal.GrantVersion != grantVersion {
		pool.Close()
		return nil, fmt.Errorf("server: static bearer principal grant version %d does not match the bootstrapped row version %d", requestVerifier.principal.GrantVersion, grantVersion)
	}
	authenticator := requestAuthenticator{
		verifier: requestVerifier,
		factory:  factory,
		workspace: workspaceSelector{
			defaultWorkspace: cfg.Server.WorkspaceID,
		},
	}
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
	chatProvider, err := newConfiguredChatProvider(llm)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("server: outbound agent configuration: %w", err)
	}
	agentService := newServerAgentService(requestOperations{}, vec, emb, chatProvider)
	agentAuditor := agentAuditorFactory(func(_ context.Context, requestPrincipal domain.Principal) (agentdomain.Auditor, error) {
		sink, err := postgresstore.NewAuditSink(pool, requestPrincipal.Subject, requestPrincipal.GrantDigest, requestPrincipal.GrantVersion)
		if err != nil {
			return agentdomain.Auditor{}, err
		}
		return newAgentAuditor(sink), nil
	})
	adminProbes := composedAdminAIProbes{
		llmStatus:       adminAIStatus{Provider: llm.Provider, Model: llm.Model, BaseURL: llm.BaseURL, TimeoutSeconds: int(llm.Timeout / time.Second), Configured: llm.Configured()},
		embeddingStatus: adminAIStatus{Provider: cfg.Search.EmbeddingProvider, Model: model.Name, BaseURL: cfg.Search.EmbeddingBaseURL, Configured: emb != nil, Dimensions: model.Dimension},
		extractor:       extractor,
		embeddings:      emb,
	}
	handler, transport := newHTTPHandlerWithHybridSearch(cfg, requestOperations{}, pool.Ping, authenticator.middleware, hybridSearchDependencies{
		vectors: vec, embeddings: emb, adminAI: adminProbes, agent: agentService, agentAuditor: agentAuditor,
	}, extractor)
	rt := &Runtime{
		Config:     &cfg,
		Pool:       pool,
		Vectors:    vec,
		Embeddings: emb,
		agent:      agentService,
		reindex:    reindexDeps,
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
	startBackgroundEmbeddingWorker(ctx, pool, emb, vec)
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
	runtimeRole, err := postgresRole(cfg.Server.Storage.DSN)
	if err != nil {
		return fmt.Errorf("application role: %w", err)
	}
	// GRANT does not accept bind parameters: the role name is parsed from the
	// operator-configured runtime DSN and quoted defensively before use.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`GRANT cortex_app TO %s`, quotePostgresIdentifier(runtimeRole))); err != nil {
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

// dockerAutoBootstrapRequested reports whether the Docker auto-bootstrap
// entrypoint enabled fresh-volume provisioning (CORTEX_SERVER_AUTO_BOOTSTRAP=true).
// The entrypoint generates the tenant identity set and the tenant owner bearer
// on first start; the server must then provision the prerequisite tenant
// fixtures the durable bootstrap reconciler binds its grants to. This trigger
// relaxes no authorization boundary: runtime and migration DSNs stay distinct,
// loopback allowances stay off, and the service principal is still provisioned
// exclusively by cortex_bootstrap_service_principal.
func dockerAutoBootstrapRequested() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("CORTEX_SERVER_AUTO_BOOTSTRAP")), "true")
}

// quotePostgresIdentifier renders a role name as a safe SQL identifier for
// statements (like GRANT) that cannot take bind parameters.
func quotePostgresIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
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
	// The Docker auto-bootstrap path provisions the same tenant owner bearer
	// the synthetic request principal historically represented, so its grants
	// must carry the canonical wildcard project and classification grants too.
	// Without them the durable principal verified through
	// cortex_verify_token_principal loses the project authority the synthetic
	// contract guarantees (NewSyntheticPrincipal), and every project-scoped
	// operation is denied with DenyProject.
	devDefaults := cfg.Server.BootstrapDevelopment || dockerAutoBootstrapRequested()
	roles := append([]string(nil), cfg.Server.Roles...)
	scopes := append([]string(nil), cfg.Server.Scopes...)
	projects := append([]string(nil), cfg.Server.ProjectIDs...)
	clearance := append([]string(nil), cfg.Server.ClassificationClearance...)
	if devDefaults {
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

// resolveStaticBindProvenance reads the bootstrapped service principal's
// persisted grant digest and grant version, then renders the migration-112
// static binder contract for the synthetic request principal. The digest
// cannot be a configured constant: cortex_bind_principal recomputes the static
// MAC from the actor row and fails closed on any value it cannot reproduce.
// The returned version must also reach the request principal, because the
// binder revalidates the presented version against the row before the MAC
// check; returning it here lets the composition carry the real value instead
// of a stale literal.
func resolveStaticBindProvenance(ctx context.Context, db *sql.DB, cfg config.Config) (string, int64, error) {
	var storedDigest string
	var grantVersion int64
	err := db.QueryRowContext(ctx, `
		SELECT grant_digest, grant_version
		  FROM public.actor_subjects
		 WHERE tenant_id = $1::uuid
		   AND public_id = $2::uuid
		   AND active
		   AND revoked_at IS NULL`,
		cfg.Server.TenantID, cfg.Server.PrincipalSubject,
	).Scan(&storedDigest, &grantVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, errors.New("server: bootstrapped service principal is missing or revoked")
	}
	if err != nil {
		return "", 0, fmt.Errorf("server: read bootstrapped service principal: %w", err)
	}
	provenance, err := staticBindProvenance(cfg.Server.TenantID, cfg.Server.PrincipalSubject, storedDigest, grantVersion)
	if err != nil {
		return "", 0, err
	}
	return provenance, grantVersion, nil
}

// staticBindProvenance derives the `static:<hex>` argument migration 112
// recomputes in PostgreSQL: HMAC-SHA256 over the canonical
// `tenant:actor:grant version` message keyed by the actor's persisted grant
// digest. It is rendered here, rather than left as the raw digest, because
// both the authorized store and the audit sink hand a principal's configured
// digest straight to cortex_bind_principal.
func staticBindProvenance(tenantID, actorID, storedDigest string, grantVersion int64) (string, error) {
	if storedDigest == "" {
		return "", errors.New("server: bootstrapped service principal has no grant digest")
	}
	if grantVersion < 1 {
		return "", errors.New("server: bootstrapped service principal has no grant version")
	}
	tenant, err := uuid.Parse(tenantID)
	if err != nil {
		return "", errors.New("server: tenant_id is invalid")
	}
	actor, err := uuid.Parse(actorID)
	if err != nil {
		return "", errors.New("server: principal_subject is invalid")
	}
	mac := hmac.New(sha256.New, []byte(storedDigest))
	_, _ = mac.Write([]byte(tenant.String() + ":" + actor.String() + ":" + strconv.FormatInt(grantVersion, 10)))
	return "static:" + hex.EncodeToString(mac.Sum(nil)), nil
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
	return validateRuntimeConfig(cfg, true)
}

func validateRuntimeConfig(cfg config.Config, withServerSurfaces bool) error {
	if cfg.Server.Storage.Driver != "postgres" {
		return fmt.Errorf("server: storage.driver must be postgres")
	}
	if strings.TrimSpace(cfg.Server.Storage.DSN) == "" {
		return errors.New("server: storage DSN is required")
	}
	// The single-tenant static verifier is the composition's only request
	// identity source; it is assembled here so startup fails closed, naming
	// the missing key, before any pool or handler exists. Its binder proof is
	// not a configured constant: the composition later replaces the digest
	// with the migration-112 static provenance derived from the bootstrapped
	// actor's persisted grant digest.
	if _, err := newStaticBearerVerifier(cfg); err != nil {
		return err
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
	if withServerSurfaces {
		if cfg.HTTP.Port < 1 || cfg.HTTP.Port > 65535 {
			return errors.New("server: http.port must be between 1 and 65535")
		}
		if !cfg.HTTP.Enabled {
			return errors.New("server: http.enabled must be true for HTTP and MCP transports")
		}
		if err := validateAllowedOrigins(cfg.HTTP.AllowedOrigins); err != nil {
			return err
		}
	}
	if err := validateBearerToken(cfg.HTTP.Token); err != nil {
		return err
	}
	_, _, err := resolveServerDSNs(cfg)
	return err
}

func validateAllowedOrigins(origins []string) error {
	for _, configured := range origins {
		origin := strings.TrimSpace(configured)
		if origin == "*" {
			return errors.New("server: http.allowed_origins must not contain wildcard")
		}
		u, err := url.Parse(origin)
		if origin == "" || err != nil || u.Opaque != "" || u.Host == "" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("server: http.allowed_origins must contain only HTTP(S) origins")
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return errors.New("server: http.allowed_origins must contain only HTTP(S) origins")
		}
	}
	return nil
}

// resolveServerDSNs validates the database authority boundary before sql.Open
// or pgxpool.ParseConfig can create a handle. Development bootstrap retains
// the single-DSN convenience; every other deployment must name a distinct
// PostgreSQL role for migrations. Errors deliberately omit DSN contents.
func resolveServerDSNs(cfg config.Config) (string, string, error) {
	runtimeDSN := cfg.Server.Storage.DSN
	if strings.TrimSpace(runtimeDSN) == "" {
		return "", "", errors.New("server: storage DSN is required")
	}
	migrationDSN := cfg.Server.Storage.MigrationDSN
	if strings.TrimSpace(migrationDSN) == "" {
		if !cfg.Server.BootstrapDevelopment {
			return "", "", errors.New("server: migration DSN is required outside development bootstrap")
		}
		migrationDSN = runtimeDSN
	}
	if cfg.Server.BootstrapDevelopment {
		return runtimeDSN, migrationDSN, nil
	}

	runtimeRole, err := postgresRole(runtimeDSN)
	if err != nil {
		return "", "", errors.New("server: runtime DSN is invalid")
	}
	migrationRole, err := postgresRole(migrationDSN)
	if err != nil {
		return "", "", errors.New("server: migration DSN is invalid")
	}
	if runtimeRole == migrationRole {
		return "", "", errors.New("server: runtime and migration DSNs must use distinct PostgreSQL roles")
	}
	return runtimeDSN, migrationDSN, nil
}

func postgresRole(dsn string) (string, error) {
	parsed, err := pgx.ParseConfig(dsn)
	if err != nil || strings.TrimSpace(parsed.User) == "" {
		return "", errors.New("invalid PostgreSQL role")
	}
	return parsed.User, nil
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

// liveEmbeddingDimensions resolves the embedding dimension from the only
// trusted sources: the composed embedder's live service state, then the
// operator's vector configuration. A static provider-to-dimension mirror
// desyncs from shipped presets and silently mismatches 4096-dim endpoints
// (REQ-EMB-002), so no provider-name lookup may answer this question.
func liveEmbeddingDimensions(cfg config.Config, service embedding.Service) int {
	if service != nil {
		if dimensions := service.Dimensions(); dimensions > 0 {
			return dimensions
		}
	}
	if dimensions := cfg.Vector.Pgvector.Dimension; dimensions > 0 {
		return dimensions
	}
	return cfg.Vector.Qdrant.Dimension
}

func newServerEmbedding(cfg config.Config) (embedding.Service, error) {
	provider := strings.TrimSpace(cfg.Search.EmbeddingProvider)
	if provider == "" || provider == "none" {
		return nil, nil
	}
	baseURL := strings.TrimSpace(cfg.Search.EmbeddingBaseURL)
	if baseURL == "" {
		switch provider {
		case "openai":
			baseURL = "https://api.openai.com/v1"
		case "ollama":
			baseURL = "http://localhost:11434"
		case "openai-compatible":
			// A generic compatible endpoint has no safe default host; the
			// operator must name it so destination approval has a real URL
			// to evaluate instead of a generic empty-destination rejection.
			return nil, errors.New("embedding: openai-compatible requires an explicit base URL")
		}
	}
	policy := embedding.OutboundPolicy{
		AllowLoopback:                cfg.Server.BootstrapDevelopment,
		AllowInsecureLoopbackHTTP:    cfg.Server.BootstrapDevelopment,
		RailwayInternalEmbeddingHost: cfg.Server.RailwayInternalEmbeddingHost,
		MaxRedirects:                 3,
		MaxResponseBodyBytes:         4 << 20,
		MaxConcurrent:                4,
		Timeout:                      30 * time.Second,
	}
	if err := policy.ApproveDestination(baseURL); err != nil {
		return nil, err
	}
	return embedding.NewSecure(embedding.Config{
		Provider: provider,
		APIKey:   config.ResolveEmbeddingAPIKey(provider),
		Model:    cfg.Search.EmbeddingModel,
		BaseURL:  baseURL,
	}, policy)
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
		// 15s grace (was 5s): see http.go Serve teardown — StateNew connections can hold
		// graceful Shutdown past a 5s deadline (issue #102).
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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
