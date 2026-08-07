package server

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/lleontor705/cortex/internal/domain"
)

type principalVerifier interface {
	VerifyToken(context.Context, string, string) (domain.Principal, error)
}

type principalOperationsFactory interface {
	ForPrincipal(context.Context, domain.Principal) (Operations, error)
}

type requestAuthenticator struct {
	bootstrapToken     string
	bootstrapPrincipal domain.Principal
	verifier           principalVerifier
	factory            principalOperationsFactory
}

type principalContextKey struct{}

func principalFromContext(ctx context.Context) (domain.Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(domain.Principal)
	return principal, ok
}

func (a requestAuthenticator) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(raw, "Bearer ") {
			writeUnauthorized(w)
			return
		}
		secret := strings.TrimSpace(strings.TrimPrefix(raw, "Bearer "))
		principal, ok := a.bootstrap(secret)
		if !ok {
			var err error
			principal, err = a.verifier.VerifyToken(r.Context(), secret, "")
			if err != nil {
				writeUnauthorized(w)
				return
			}
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

func (a requestAuthenticator) bootstrap(secret string) (domain.Principal, bool) {
	if a.bootstrapToken == "" || len(secret) != len(a.bootstrapToken) || subtle.ConstantTimeCompare([]byte(secret), []byte(a.bootstrapToken)) != 1 {
		return domain.Principal{}, false
	}
	return a.bootstrapPrincipal, true
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="cortex"`)
	writeError(w, http.StatusUnauthorized, "unauthorized", "valid bearer token required")
}
