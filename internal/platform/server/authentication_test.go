package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

// TestRequestAuthenticatorVerifiesConfiguredBearerExactlyOnce proves the
// configured bootstrap bearer has no static bypass: it travels through the
// narrow verifier exactly once per request, and the factory only ever sees
// the verified principal.
func TestRequestAuthenticatorVerifiesConfiguredBearerExactlyOnce(t *testing.T) {
	const configuredBearer = "configured-bootstrap-bearer"
	want := domain.Principal{Subject: "00000000-0000-0000-0000-000000000003", OrgID: "tenant-1"}
	verifications := 0
	factoryCalls := 0
	wantOps := newFakeOperations()
	auth := requestAuthenticator{
		verifier: verifierFunc(func(_ context.Context, secret, scope string) (domain.Principal, error) {
			verifications++
			if secret != configuredBearer {
				return domain.Principal{}, errors.New("unknown credential")
			}
			if scope != "" {
				t.Fatalf("verification scope = %q, want empty", scope)
			}
			return want, nil
		}),
		factory: operationsFactoryFunc(func(_ context.Context, principal domain.Principal) (Operations, error) {
			factoryCalls++
			if principal.Subject != want.Subject || principal.GrantDigest != want.GrantDigest {
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
		w.WriteHeader(http.StatusNoContent)
	}))

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
		req.Header.Set("Authorization", "Bearer "+configuredBearer)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("request %d status = %d, want %d", i, rec.Code, http.StatusNoContent)
		}
	}
	if verifications != 2 {
		t.Fatalf("verifier calls = %d, want exactly one per request (2)", verifications)
	}
	if factoryCalls != 2 {
		t.Fatalf("factory calls = %d, want 2", factoryCalls)
	}
}

func TestRequestAuthenticatorRejectsUnknownBearerThroughVerifier(t *testing.T) {
	const configuredBearer = "configured-bootstrap-bearer"
	verifications := 0
	auth := requestAuthenticator{
		verifier: verifierFunc(func(_ context.Context, secret, _ string) (domain.Principal, error) {
			verifications++
			if secret == configuredBearer {
				t.Fatal("stale configured bearer was accepted by the verifier stub")
			}
			return domain.Principal{}, errors.New("invalid token")
		}),
		factory: operationsFactoryFunc(func(context.Context, domain.Principal) (Operations, error) {
			t.Fatal("operation factory called for unverified bearer")
			return nil, nil
		}),
	}
	handler := auth.middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("protected handler called for unverified bearer")
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+configuredBearer+"-revoked")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("response = %d, WWW-Authenticate = %q", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
	if verifications != 1 {
		t.Fatalf("verifier calls = %d, want exactly 1", verifications)
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

// TestRequestAuthenticatorDoesNotTrimPresentedSecret pins the byte-exact
// verification contract (IDP-T03B review blocker): the presented secret must
// reach the verifier exactly as transmitted. Configured bearers are validated
// to be canonical (no surrounding/control whitespace), so trimming request
// bytes could only let a non-canonical presentation authenticate and would
// make an accepted-but-padded startup credential unverifiable.
func TestRequestAuthenticatorDoesNotTrimPresentedSecret(t *testing.T) {
	const exactSecret = "\tconfigured-secret-with-padding "
	var verified []string
	auth := requestAuthenticator{
		verifier: verifierFunc(func(_ context.Context, secret, _ string) (domain.Principal, error) {
			verified = append(verified, secret)
			if secret != exactSecret {
				return domain.Principal{}, errors.New("unknown credential")
			}
			return domain.Principal{Subject: "00000000-0000-0000-0000-0000000000ff"}, nil
		}),
		factory: operationsFactoryFunc(func(context.Context, domain.Principal) (Operations, error) {
			return newFakeOperations(), nil
		}),
	}
	handler := auth.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+exactSecret)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("byte-exact presentation status = %d, want %d (verified = %q)", rec.Code, http.StatusNoContent, verified)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(exactSecret))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("trimmed presentation status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if len(verified) != 2 || verified[0] != exactSecret || verified[1] != strings.TrimSpace(exactSecret) {
		t.Fatalf("verifier secrets = %q, want byte-exact [%q %q]", verified, exactSecret, strings.TrimSpace(exactSecret))
	}
}

func TestRequestAuthenticatorRejectsMalformedAuthorizationWithoutVerification(t *testing.T) {
	auth := requestAuthenticator{
		verifier: verifierFunc(func(context.Context, string, string) (domain.Principal, error) {
			t.Fatal("verifier called for malformed authorization header")
			return domain.Principal{}, nil
		}),
		factory: operationsFactoryFunc(func(context.Context, domain.Principal) (Operations, error) {
			t.Fatal("operation factory called for malformed authorization header")
			return nil, nil
		}),
	}
	handler := auth.middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("protected handler called for malformed authorization header")
	}))
	for _, header := range []string{"", "Basic dXNlcjpwYXNz", "Bearer", "Bearer  "} {
		req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("authorization %q status = %d, want %d", header, rec.Code, http.StatusUnauthorized)
		}
	}
}
