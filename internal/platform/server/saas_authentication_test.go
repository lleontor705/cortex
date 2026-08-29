package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

func TestRequestAuthenticatorSelectsOnlyGrantedWorkspaceForSaaSRequest(t *testing.T) {
	const allowedWorkspace = "10000000-a000-0000-0000-000000000001"
	const otherWorkspace = "10000000-a000-0000-0000-000000000002"
	principal := domain.Principal{
		Subject:      "00000000-0000-0000-0000-000000000003",
		OrgID:        "00000000-0000-0000-0000-000000000004",
		WorkspaceIDs: []string{allowedWorkspace},
	}
	operations := newFakeOperations()
	var selected string
	auth := requestAuthenticator{
		verifier: verifierFunc(func(context.Context, string, string) (domain.Principal, error) { return principal, nil }),
		factory: operationsFactoryFunc(func(ctx context.Context, got domain.Principal) (Operations, error) {
			if got.Subject != principal.Subject {
				t.Fatalf("factory principal = %+v", got)
			}
			workspaceID, ok := workspaceFromContext(ctx)
			if !ok {
				t.Fatal("factory context has no selected workspace")
			}
			selected = workspaceID
			return operations, nil
		}),
		workspace: workspaceSelector{allowRequestSelection: true},
	}
	handler := auth.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workspaceID, ok := workspaceFromContext(r.Context())
		if !ok || workspaceID != allowedWorkspace {
			t.Fatalf("request workspace = %q, present = %v", workspaceID, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Authorization", "Bearer ctx_user_token")
	req.Header.Set(workspaceRequestHeader, allowedWorkspace)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || selected != allowedWorkspace {
		t.Fatalf("allowed selection status=%d selected=%q", rec.Code, selected)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Authorization", "Bearer ctx_user_token")
	req.Header.Set(workspaceRequestHeader, otherWorkspace)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign workspace status=%d, want %d", rec.Code, http.StatusForbidden)
	}
}
func TestRequestAuthenticatorDefaultsSaaSRequestToFirstGrantedWorkspace(t *testing.T) {
	const firstWorkspace = "10000000-a000-0000-0000-000000000011"
	const secondWorkspace = "10000000-a000-0000-0000-000000000012"
	principal := domain.Principal{
		Subject:      "00000000-0000-0000-0000-000000000013",
		OrgID:        "00000000-0000-0000-0000-000000000014",
		WorkspaceIDs: []string{firstWorkspace, secondWorkspace},
	}
	auth := requestAuthenticator{
		verifier: verifierFunc(func(context.Context, string, string) (domain.Principal, error) { return principal, nil }),
		factory: operationsFactoryFunc(func(ctx context.Context, _ domain.Principal) (Operations, error) {
			workspaceID, ok := workspaceFromContext(ctx)
			if !ok || workspaceID != firstWorkspace {
				t.Fatalf("factory workspace = %q, present = %v", workspaceID, ok)
			}
			return newFakeOperations(), nil
		}),
		workspace: workspaceSelector{allowRequestSelection: true},
	}
	handler := auth.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workspaceID, ok := workspaceFromContext(r.Context())
		if !ok || workspaceID != firstWorkspace {
			t.Fatalf("handler workspace = %q, present = %v", workspaceID, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Authorization", "Bearer ctx_user_token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("default selection status=%d, want %d", rec.Code, http.StatusNoContent)
	}
}
