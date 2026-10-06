package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/identity"
	"github.com/lleontor705/cortex/v2/internal/server/external"
	postgresstore "github.com/lleontor705/cortex/v2/internal/store/postgres"
)

type principalVerifier interface {
	VerifyToken(context.Context, string, string) (domain.Principal, error)
}

type principalOperationsFactory interface {
	ForPrincipal(context.Context, domain.Principal) (Operations, error)
}

// requestAuthenticator admits only verified principals to the operations
// factory. A secret that resolves to a durable credential is proven by the
// durable bearer gate upstream through cortex_verify_token_principal
// (revocation and expiry included); every other secret is decided by the
// narrow static verifier below, so a secret without a durable credential
// authenticates only when it is byte-exact the configured bearer.
type requestAuthenticator struct {
	verifier  principalVerifier
	factory   principalOperationsFactory
	workspace workspaceSelector
}

type principalContextKey struct{}
type workspaceContextKey struct{}

const workspaceRequestHeader = "X-Cortex-Workspace"

var errWorkspaceNotGranted = errors.New("server: workspace is not granted")

// workspaceSelector always resolves to the configured default workspace. The
// X-Cortex-Workspace header may only echo that default; any other value is
// rejected, so the header is never an authorization input.
type workspaceSelector struct {
	defaultWorkspace string
}

func (s workspaceSelector) selectWorkspace(r *http.Request, _ domain.Principal) (string, error) {
	raw := r.Header.Get(workspaceRequestHeader)
	requested := strings.TrimSpace(raw)
	if raw != requested {
		return "", errWorkspaceNotGranted
	}
	if requested == "" {
		return s.defaultWorkspace, nil
	}
	workspaceID, err := canonicalWorkspaceID(requested)
	if err != nil {
		return "", errWorkspaceNotGranted
	}
	if workspaceID != s.defaultWorkspace {
		return "", errWorkspaceNotGranted
	}
	return workspaceID, nil
}

func canonicalWorkspaceID(value string) (string, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil || parsed == uuid.Nil {
		return "", errWorkspaceNotGranted
	}
	return parsed.String(), nil
}

func withWorkspace(ctx context.Context, workspaceID string) context.Context {
	return context.WithValue(ctx, workspaceContextKey{}, workspaceID)
}

func workspaceFromContext(ctx context.Context) (string, bool) {
	workspaceID, ok := ctx.Value(workspaceContextKey{}).(string)
	return workspaceID, ok && workspaceID != ""
}

func principalFromContext(ctx context.Context) (domain.Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(domain.Principal)
	return principal, ok
}

type durablePrincipalContextKey struct{}

// withDurablePrincipal records the principal the durable bearer gate already
// proved through cortex_verify_token_principal. The key is package-private
// and written only by that gate, so no request-controlled value can reach it.
func withDurablePrincipal(ctx context.Context, principal domain.Principal) context.Context {
	return context.WithValue(ctx, durablePrincipalContextKey{}, principal)
}

func durablePrincipalFromContext(ctx context.Context) (domain.Principal, bool) {
	principal, ok := ctx.Value(durablePrincipalContextKey{}).(domain.Principal)
	return principal, ok
}

// providedBearer extracts the secret byte-exact: the configured bearer is
// validated canonical at startup (validateBearerToken rejects surrounding and
// control whitespace), so the presented secret is never trimmed — only the
// exact stored credential can verify, and padded presentations fail closed.
// ok is false whenever the header is absent, mis-schemed, or whitespace-only;
// the caller then produces the standard 401 envelope.
func providedBearer(header string) (string, bool) {
	const bearerScheme = "Bearer "
	if !strings.HasPrefix(header, bearerScheme) {
		return "", false
	}
	secret := header[len(bearerScheme):]
	if secret == "" || strings.TrimSpace(secret) == "" {
		return "", false
	}
	return secret, true
}

// middleware extracts the bearer through presentedBearer, then accepts only a
// principal the durable gate proved or the static verifier matches.
func (a requestAuthenticator) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secret, ok := providedBearer(r.Header.Get("Authorization"))
		if !ok {
			writeUnauthorized(w)
			return
		}
		principal, durable := durablePrincipalFromContext(r.Context())
		if !durable {
			var err error
			principal, err = a.verifier.VerifyToken(r.Context(), secret, "")
			if err != nil {
				writeUnauthorized(w)
				return
			}
		}
		workspaceID, err := a.workspace.selectWorkspace(r, principal)
		if err != nil {
			writeError(w, http.StatusForbidden, "workspace_not_granted", "workspace is not granted")
			return
		}
		ctx := context.WithValue(r.Context(), principalContextKey{}, principal)
		if workspaceID != "" {
			ctx = withWorkspace(ctx, workspaceID)
			ctx = external.WithRequestVectorScope(ctx, principal.OrgID, workspaceID)
		}
		ops, err := a.factory.ForPrincipal(ctx, principal)
		if err != nil {
			writeUnauthorized(w)
			return
		}
		next.ServeHTTP(w, r.WithContext(withOperations(ctx, ops)))
	})
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="cortex"`)
	writeError(w, http.StatusUnauthorized, "unauthorized", "valid bearer token required")
}

// durableVerifierPools shares one bounded verification pool per DSN across
// composed runtimes. The gate has no Runtime handle to close, and restart
// loops would otherwise leak a pool per server start until PostgreSQL
// connection limits trip, so a process-wide cache keyed by DSN bounds the
// footprint to one pool per database.
var durableVerifierPools sync.Map // DSN → *pgxpool.Pool

// durableBearerGate proves secrets that resolve to a durable credential
// through cortex_verify_token_principal before the static compare runs:
//
//   - a live credential installs its verified principal, so the downstream
//     middleware skips the static compare and the request carries the
//     same verifier-transaction-plus-bind provenance as a store-level call;
//   - a revoked or expired credential fails closed with 401 even when it is
//     byte-exact the configured bearer — revocation outranks configuration,
//     which a static compare alone can never observe;
//   - a secret with no durable credential falls through to the static
//     compare, preserving the configured-bearer contract for compositions
//     whose bearer has no token row.
//
// Infrastructure failures fall through for the same reason: without a
// verified durable principal the static path still rejects every
// non-configured secret, and the operations factory re-requires the store
// immediately afterwards, so no unverified principal can act.
func durableBearerGate(cfg config.Config, protect func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	verifier, err := newDurableTokenVerifier(cfg)
	if err != nil {
		return denyAllBearerGate()
	}
	if verifier == nil {
		return protect
	}
	return func(next http.Handler) http.Handler {
		inner := protect(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			secret, ok := providedBearer(r.Header.Get("Authorization"))
			if !ok {
				inner.ServeHTTP(w, r)
				return
			}
			principal, err := verifier.VerifyToken(r.Context(), secret, "")
			switch {
			case err == nil:
				inner.ServeHTTP(w, r.WithContext(withDurablePrincipal(r.Context(), principal)))
			case errors.Is(err, identity.ErrTokenRevoked),
				errors.Is(err, identity.ErrTokenExpired),
				errors.Is(err, identity.ErrInsufficientScope):
				writeUnauthorized(w)
			default:
				inner.ServeHTTP(w, r)
			}
		})
	}
}

// denyAllBearerGate fails closed when a durable store is configured but its
// verifier cannot be composed: authentication must not silently degrade to
// the static compare alone once durable verification was required. A
// composition without a durable store never reaches this gate.
func denyAllBearerGate() func(http.Handler) http.Handler {
	return func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusServiceUnavailable, "auth_unavailable", "durable token verification is unavailable")
		})
	}
}

// newDurableTokenVerifier builds the narrow verification capability over a
// bounded pool, or returns (nil, nil) when the composition declares no
// durable store and must stay on the static request plane.
func newDurableTokenVerifier(cfg config.Config) (principalVerifier, error) {
	dsn := strings.TrimSpace(cfg.Server.Storage.DSN)
	if dsn == "" {
		return nil, nil
	}
	pool, err := durableVerifierPool(dsn, cfg.Server.Storage.MaxConns)
	if err != nil {
		return nil, err
	}
	return postgresstore.NewTokenPrincipalVerifier(pool, cfg.Server.TenantID)
}

func durableVerifierPool(dsn string, maxConns int32) (*pgxpool.Pool, error) {
	if cached, ok := durableVerifierPools.Load(dsn); ok {
		return cached.(*pgxpool.Pool), nil
	}
	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	// The gate verifies only; it must stay well below PostgreSQL's
	// connection ceiling next to the runtime pool that serves the actual
	// request traffic.
	if maxConns <= 0 {
		maxConns = 8
	}
	poolCfg.MaxConns = maxConns
	poolCfg.MinConns = 0
	poolCfg.MaxConnIdleTime = 5 * time.Minute
	pool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
	if err != nil {
		return nil, err
	}
	cached, loaded := durableVerifierPools.LoadOrStore(dsn, pool)
	if loaded {
		pool.Close()
		return cached.(*pgxpool.Pool), nil
	}
	return pool, nil
}
