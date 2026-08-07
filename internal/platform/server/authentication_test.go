package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lleontor705/cortex/internal/domain"
)

type verifierFunc func(context.Context, string, string) (domain.Principal, error)

func (f verifierFunc) VerifyToken(ctx context.Context, secret, scope string) (domain.Principal, error) {
	return f(ctx, secret, scope)
}

type operationsFactoryFunc func(context.Context, domain.Principal) (Operations, error)

func (f operationsFactoryFunc) ForPrincipal(ctx context.Context, principal domain.Principal) (Operations, error) {
	return f(ctx, principal)
}

func TestRequestAuthenticatorAttributesPersistedTokenPrincipal(t *testing.T) {
	want := domain.Principal{Subject: "user-1", OrgID: "tenant-1"}
	wantOps := newFakeOperations()
	auth := requestAuthenticator{
		verifier: verifierFunc(func(_ context.Context, secret, scope string) (domain.Principal, error) {
			if secret != "ctx_user_token" || scope != "" {
				t.Fatalf("verification input = %q, %q", secret, scope)
			}
			return want, nil
		}),
		factory: operationsFactoryFunc(func(_ context.Context, principal domain.Principal) (Operations, error) {
			if principal.Subject != want.Subject {
				t.Fatalf("factory principal = %+v", principal)
			}
			return wantOps, nil
		}),
	}
	handler := auth.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := principalFromContext(r.Context())
		if !ok || principal.Subject != want.Subject {
			t.Fatalf("request principal = %+v, present = %v", principal, ok)
		}
		ops, err := operationsFromContext(r.Context())
		if err != nil || ops != wantOps {
			t.Fatalf("request operations = %T, error = %v", ops, err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Authorization", "Bearer ctx_user_token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestRequestAuthenticatorUsesBootstrapPrincipalWithoutVerifier(t *testing.T) {
	want := domain.Principal{Subject: "bootstrap-owner", OrgID: "tenant-1"}
	auth := requestAuthenticator{
		bootstrapToken:     "bootstrap-secret",
		bootstrapPrincipal: want,
		verifier: verifierFunc(func(context.Context, string, string) (domain.Principal, error) {
			t.Fatal("verifier called for bootstrap token")
			return domain.Principal{}, nil
		}),
		factory: operationsFactoryFunc(func(_ context.Context, principal domain.Principal) (Operations, error) {
			if principal.Subject != want.Subject {
				t.Fatalf("factory principal = %+v", principal)
			}
			return newFakeOperations(), nil
		}),
	}
	handler := auth.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Authorization", "Bearer bootstrap-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestRequestAuthenticatorRejectsInvalidTokenBeforeOperationFactory(t *testing.T) {
	auth := requestAuthenticator{
		verifier: verifierFunc(func(context.Context, string, string) (domain.Principal, error) {
			return domain.Principal{}, errors.New("invalid token")
		}),
		factory: operationsFactoryFunc(func(context.Context, domain.Principal) (Operations, error) {
			t.Fatal("operation factory called for invalid token")
			return nil, nil
		}),
	}
	handler := auth.middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("protected handler called for invalid token")
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Authorization", "Bearer invalid")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("response = %d, WWW-Authenticate = %q", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
}
