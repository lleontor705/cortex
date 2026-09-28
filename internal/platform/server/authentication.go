package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/server/external"
)

type principalVerifier interface {
	VerifyToken(context.Context, string, string) (domain.Principal, error)
}

type principalOperationsFactory interface {
	ForPrincipal(context.Context, domain.Principal) (Operations, error)
}

// requestAuthenticator verifies every presented bearer through the narrow
// token verifier. There is deliberately no configured-token bypass: the
// startup bearer and every request bearer take the same single
// cortex_verify_token_principal path, and only a verified principal reaches
// the operations factory.
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

// middleware extracts the bearer secret byte-exact: the configured bearer is
// validated canonical at startup (validateBearerToken rejects surrounding and
// control whitespace), so the presented secret is never trimmed — only the
// exact stored credential can verify, and padded presentations fail closed.
func (a requestAuthenticator) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provided := r.Header.Get("Authorization")
		const bearerScheme = "Bearer "
		if !strings.HasPrefix(provided, bearerScheme) {
			writeUnauthorized(w)
			return
		}
		secret := provided[len(bearerScheme):]
		if secret == "" || strings.TrimSpace(secret) == "" {
			writeUnauthorized(w)
			return
		}
		principal, err := a.verifier.VerifyToken(r.Context(), secret, "")
		if err != nil {
			writeUnauthorized(w)
			return
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
