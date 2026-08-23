package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/lleontor705/cortex/v2/internal/domain"
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
	verifier principalVerifier
	factory  principalOperationsFactory
}

type principalContextKey struct{}

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
		ops, err := a.factory.ForPrincipal(r.Context(), principal)
		if err != nil {
			writeUnauthorized(w)
			return
		}
		ctx := context.WithValue(r.Context(), principalContextKey{}, principal)
		next.ServeHTTP(w, r.WithContext(withOperations(ctx, ops)))
	})
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="cortex"`)
	writeError(w, http.StatusUnauthorized, "unauthorized", "valid bearer token required")
}
