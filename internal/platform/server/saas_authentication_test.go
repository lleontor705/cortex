package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// TestRequestAuthenticatorAcceptsConfiguredBearerWorkspace pins the
// single-tenant request plane: the static bearer verifier yields the synthetic
// constant principal, whose sole workspace grant is the configured default.
// The X-Cortex-Workspace header may echo that workspace but can never redirect
// scope to an ungranted one.
func TestRequestAuthenticatorAcceptsConfiguredBearerWorkspace(t *testing.T) {
	cfg := validBootstrapConfig()
	configuredWorkspace := cfg.Server.WorkspaceID
	const foreignWorkspace = "10000000-a000-0000-0000-000000000002"

	verifier, err := newStaticBearerVerifier(cfg)
	if err != nil {
		t.Fatalf("newStaticBearerVerifier() = %v", err)
	}
	operations := newFakeOperations()
	factoryCalls := 0
	var selected string
	auth := requestAuthenticator{
		verifier: verifier,
		factory: operationsFactoryFunc(func(ctx context.Context, got domain.Principal) (Operations, error) {
			factoryCalls++
			if got.Subject != cfg.Server.PrincipalSubject || got.OrgID != cfg.Server.TenantID {
				t.Fatalf("factory principal = %+v", got)
			}
			workspaceID, ok := workspaceFromContext(ctx)
			if !ok {
				t.Fatal("factory context has no selected workspace")
			}
			selected = workspaceID
			return operations, nil
		}),
		workspace: workspaceSelector{defaultWorkspace: configuredWorkspace},
	}
	handler := auth.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workspaceID, ok := workspaceFromContext(r.Context())
		if !ok || workspaceID != configuredWorkspace {
			t.Fatalf("request workspace = %q, present = %v", workspaceID, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+cfg.HTTP.Token)
	req.Header.Set(workspaceRequestHeader, configuredWorkspace)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || selected != configuredWorkspace {
		t.Fatalf("configured workspace status=%d selected=%q", rec.Code, selected)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+cfg.HTTP.Token)
	req.Header.Set(workspaceRequestHeader, foreignWorkspace)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign workspace status=%d, want %d", rec.Code, http.StatusForbidden)
	}
	if factoryCalls != 1 {
		t.Fatalf("factory calls = %d, want 1; a foreign workspace must not reach the operations factory", factoryCalls)
	}
}

// TestRequestAuthenticatorDefaultsToConfiguredWorkspace pins the fixed
// workspace behavior: without a workspace header the request runs against the
// configured default workspace carried by the synthetic principal.
func TestRequestAuthenticatorDefaultsToConfiguredWorkspace(t *testing.T) {
	cfg := validBootstrapConfig()
	configuredWorkspace := cfg.Server.WorkspaceID

	verifier, err := newStaticBearerVerifier(cfg)
	if err != nil {
		t.Fatalf("newStaticBearerVerifier() = %v", err)
	}
	auth := requestAuthenticator{
		verifier: verifier,
		factory: operationsFactoryFunc(func(ctx context.Context, _ domain.Principal) (Operations, error) {
			workspaceID, ok := workspaceFromContext(ctx)
			if !ok || workspaceID != configuredWorkspace {
				t.Fatalf("factory workspace = %q, present = %v", workspaceID, ok)
			}
			return newFakeOperations(), nil
		}),
		workspace: workspaceSelector{defaultWorkspace: configuredWorkspace},
	}
	handler := auth.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workspaceID, ok := workspaceFromContext(r.Context())
		if !ok || workspaceID != configuredWorkspace {
			t.Fatalf("handler workspace = %q, present = %v", workspaceID, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+cfg.HTTP.Token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("default workspace status=%d, want %d", rec.Code, http.StatusNoContent)
	}
}
