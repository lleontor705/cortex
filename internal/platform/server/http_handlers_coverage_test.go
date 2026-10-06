package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/authz"
	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/privacy"
)

func newCoverageHandler(ops Operations) http.Handler {
	cfg := config.Config{
		HTTP:   config.HTTPConfig{Token: "cov-token"},
		Search: config.SearchConfig{DefaultLimit: 10, MaxLimit: 20},
		Server: config.ServerConfig{WorkspaceID: "00000000-0000-0000-0000-000000000001"},
	}
	auth := requestAuthenticator{
		verifier: verifierFunc(func(_ context.Context, secret, _ string) (domain.Principal, error) {
			if secret != cfg.HTTP.Token {
				return domain.Principal{}, errors.New("unknown")
			}
			return domain.Principal{Subject: "user-1", OrgID: "org-1"}, nil
		}),
		factory: operationsFactoryFunc(func(context.Context, domain.Principal) (Operations, error) {
			return ops, nil
		}),
	}
	h, _ := newHTTPHandlerWithAuth(cfg, requestOperations{}, func(context.Context) error { return nil }, auth.middleware)
	return h
}

func authed(method, path, body string) (*httptest.ResponseRecorder, *http.Request) {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.Header.Set("Authorization", "Bearer cov-token")
	return httptest.NewRecorder(), req
}

func TestCoverageMeEndpoint(t *testing.T) {
	ops := newFakeOperations()
	h := newCoverageHandler(ops)
	w, r := authed(http.MethodGet, "/api/me", "")
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "user-1") {
		t.Fatalf("missing subject: %s", w.Body.String())
	}
}

func TestCoverageMeNoPrincipal(t *testing.T) {
	auth := requestAuthenticator{
		verifier: verifierFunc(func(context.Context, string, string) (domain.Principal, error) {
			return domain.Principal{}, errors.New("nope")
		}),
		factory: operationsFactoryFunc(func(context.Context, domain.Principal) (Operations, error) {
			return newFakeOperations(), nil
		}),
	}
	h, _ := newHTTPHandlerWithAuth(config.Config{HTTP: config.HTTPConfig{Token: "x"}}, requestOperations{}, func(context.Context) error { return nil }, auth.middleware)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no principal status=%d", w.Code)
	}
}

func TestCoverageMeWithWorkspace(t *testing.T) {
	ops := newFakeOperations()
	ops.agentProjects = map[string]string{"p1": "proj1"}
	cfg := config.Config{
		HTTP:   config.HTTPConfig{Token: "cov-token"},
		Search: config.SearchConfig{DefaultLimit: 10, MaxLimit: 20},
		Server: config.ServerConfig{WorkspaceID: "00000000-0000-0000-0000-000000000001"},
	}
	auth := requestAuthenticator{
		verifier: verifierFunc(func(_ context.Context, secret, _ string) (domain.Principal, error) {
			return domain.Principal{Subject: "u", OrgID: "o", WorkspaceIDs: []string{"00000000-0000-0000-0000-000000000001"}}, nil
		}),
		factory:   operationsFactoryFunc(func(context.Context, domain.Principal) (Operations, error) { return ops, nil }),
		workspace: workspaceSelector{defaultWorkspace: "00000000-0000-0000-0000-000000000001"},
	}
	h, _ := newHTTPHandlerWithAuth(cfg, requestOperations{}, func(context.Context) error { return nil }, auth.middleware)
	w, r := authed(http.MethodGet, "/api/me", "")
	r.Header.Set("X-Cortex-Workspace", "00000000-0000-0000-0000-000000000001")
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "workspace_id") {
		t.Fatalf("workspace me: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestCoverageCreateAndListUsers(t *testing.T) {
	ops := newFakeOperations()
	h := newCoverageHandler(ops)
	body := `{"email":"a@b.com","display_name":"A","roles":["owner"],"scopes":["admin"]}`
	w, r := authed(http.MethodPost, "/api/admin/users", body)
	h.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create user status=%d body=%s", w.Code, w.Body.String())
	}
	w2, r2 := authed(http.MethodGet, "/api/admin/users", "")
	h.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("list users status=%d", w2.Code)
	}
}

func TestCoverageSetUserActive(t *testing.T) {
	ops := newFakeOperations()
	h := newCoverageHandler(ops)
	for _, path := range []string{"/api/admin/users/00000000-0000-0000-0000-000000000001/enable", "/api/admin/users/00000000-0000-0000-0000-000000000001/disable"} {
		w, r := authed(http.MethodPost, path, "")
		h.ServeHTTP(w, r)
		if w.Code != http.StatusNoContent {
			t.Fatalf("%s status=%d", path, w.Code)
		}
	}
}

func TestCoverageTokens(t *testing.T) {
	ops := newFakeOperations()
	h := newCoverageHandler(ops)
	issue := `{"subject":"00000000-0000-0000-0000-000000000042","name":"tok","scopes":["read"],"workspaces":["00000000-0000-0000-0000-000000000001"]}`
	w, r := authed(http.MethodPost, "/api/admin/tokens", issue)
	h.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("issue token status=%d body=%s", w.Code, w.Body.String())
	}
	w2, r2 := authed(http.MethodGet, "/api/admin/tokens", "")
	h.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("list tokens status=%d", w2.Code)
	}
	rotate := httptest.NewRequest(http.MethodPost, "/api/admin/tokens/00000000-0000-0000-0000-000000000099/rotate", nil)
	rotate.Header.Set("Authorization", "Bearer cov-token")
	wr := httptest.NewRecorder()
	h.ServeHTTP(wr, rotate)
	if wr.Code != http.StatusCreated {
		t.Fatalf("rotate token status=%d", wr.Code)
	}
	rev := httptest.NewRequest(http.MethodDelete, "/api/admin/tokens/00000000-0000-0000-0000-000000000099", nil)
	rev.Header.Set("Authorization", "Bearer cov-token")
	wrev := httptest.NewRecorder()
	h.ServeHTTP(wrev, rev)
	if wrev.Code != http.StatusNoContent {
		t.Fatalf("revoke token status=%d", wrev.Code)
	}
}

func TestCoverageSessionsAndStats(t *testing.T) {
	ops := newFakeOperations()
	h := newCoverageHandler(ops)
	cs := `{"project":"demo","summary":"test"}`
	w, r := authed(http.MethodPost, "/api/sessions", cs)
	h.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create session status=%d", w.Code)
	}
	w2, r2 := authed(http.MethodGet, "/api/sessions?project=demo", "")
	h.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("list sessions status=%d", w2.Code)
	}
	w3, r3 := authed(http.MethodGet, "/api/stats", "")
	h.ServeHTTP(w3, r3)
	if w3.Code != http.StatusOK {
		t.Fatalf("stats status=%d", w3.Code)
	}
}

func TestCoverageAuditAndProjects(t *testing.T) {
	ops := newFakeOperations()
	h := newCoverageHandler(ops)
	w, r := authed(http.MethodGet, "/api/audit", "")
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("audit status=%d", w.Code)
	}
	w2, r2 := authed(http.MethodGet, "/api/projects", "")
	h.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("projects status=%d", w2.Code)
	}
}

func TestCoverageObservationsCRUD(t *testing.T) {
	ops := newFakeOperations()
	obs := &domain.Observation{ID: 42, PublicID: "00000000-0000-0000-0000-000000000042", Title: "obs1", Content: "content", Project: "demo", Scope: "project"}
	ops.observations[42] = obs
	h := newCoverageHandler(ops)
	w, r := authed(http.MethodGet, "/api/observations?project=demo", "")
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("list obs status=%d", w.Code)
	}
	w2, r2 := authed(http.MethodGet, "/api/observations?owner=me", "")
	h.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("list obs owner=me status=%d", w2.Code)
	}
	updated := `{"title":"updated","content":"c","session_id":"00000000-0000-0000-0000-000000000099"}`
	w3, r3 := authed(http.MethodPut, "/api/observations/00000000-0000-0000-0000-000000000042", updated)
	h.ServeHTTP(w3, r3)
	if w3.Code != http.StatusOK {
		t.Fatalf("update obs status=%d body=%s", w3.Code, w3.Body.String())
	}
	w4, r4 := authed(http.MethodDelete, "/api/observations/00000000-0000-0000-0000-000000000042", "")
	h.ServeHTTP(w4, r4)
	if w4.Code != http.StatusNoContent {
		t.Fatalf("delete obs status=%d", w4.Code)
	}
}

func TestCoverageSaveObservationSessionRequired(t *testing.T) {
	ops := newFakeOperations()
	h := newCoverageHandler(ops)
	w, r := authed(http.MethodPost, "/api/observations", `{"title":"t","content":"c"}`)
	h.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "session_id is required") {
		t.Fatalf("missing session_id: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestCoverageSearch(t *testing.T) {
	ops := newFakeOperations()
	h := newCoverageHandler(ops)
	w, r := authed(http.MethodGet, "/api/search?q=test", "")
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("search status=%d", w.Code)
	}
}

func TestCoverageResolveConflict(t *testing.T) {
	ops := newFakeOperations()
	obs1 := &domain.Observation{ID: 1, PublicID: "00000000-0000-0000-0000-000000000001", Title: "new"}
	obs2 := &domain.Observation{ID: 2, PublicID: "00000000-0000-0000-0000-000000000002", Title: "old"}
	ops.observations[1] = obs1
	ops.observations[2] = obs2
	h := newCoverageHandler(ops)
	body := `{"new_observation_id":"00000000-0000-0000-0000-000000000001","obsolete_observation_id":"00000000-0000-0000-0000-000000000002","reason":"supersedes"}`
	w, r := authed(http.MethodPost, "/api/graph/resolve", body)
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("resolve status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestCoveragePullSync(t *testing.T) {
	ops := newFakeOperations()
	h := newCoverageHandler(ops)
	w, r := authed(http.MethodGet, "/api/sync/changes?limit=5", "")
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("pull sync status=%d", w.Code)
	}
	w2, r2 := authed(http.MethodGet, "/api/sync/changes?cursor=abc", "")
	h.ServeHTTP(w2, r2)
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("invalid cursor status=%d", w2.Code)
	}
}

func TestCoverageRagStats(t *testing.T) {
	ops := newFakeOperations()
	h := newCoverageHandler(ops)
	w, r := authed(http.MethodGet, "/api/rag/stats?project=demo", "")
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("rag stats status=%d", w.Code)
	}
}

func TestCoverageRelated(t *testing.T) {
	ops := newFakeOperations()
	obs := &domain.Observation{ID: 10, PublicID: "00000000-0000-0000-0000-000000000010"}
	ops.observations[10] = obs
	h := newCoverageHandler(ops)
	w, r := authed(http.MethodGet, "/api/graph/00000000-0000-0000-0000-000000000010/related", "")
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("related status=%d", w.Code)
	}
}

func TestCoverageSearchHybridFallback(t *testing.T) {
	ops := newFakeOperations()
	cfg := config.Config{
		HTTP:   config.HTTPConfig{Token: "cov-token"},
		Search: config.SearchConfig{DefaultLimit: 10, MaxLimit: 20},
	}
	auth := requestAuthenticator{
		verifier: verifierFunc(func(_ context.Context, s, _ string) (domain.Principal, error) {
			return domain.Principal{Subject: "u", OrgID: "o"}, nil
		}),
		factory: operationsFactoryFunc(func(context.Context, domain.Principal) (Operations, error) { return ops, nil }),
	}
	h, _ := newHTTPHandlerWithHybridSearch(cfg, requestOperations{}, func(context.Context) error { return nil }, auth.middleware, hybridSearchDependencies{})
	w, r := authed(http.MethodGet, "/api/search/hybrid?q=test", "")
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("hybrid fallback status=%d", w.Code)
	}
}

type denyError string

func (e denyError) Error() string { return string(e) }

func TestCoverageIsAuthorizationDenial(t *testing.T) {
	if !isAuthorizationDenial(authz.ErrForbidden) {
		t.Fatal("ErrForbidden should be denial")
	}
	for _, deny := range []denyError{authz.DenyRole, authz.DenyScope, authz.DenyTenantMismatch, authz.DenyWorkspace, authz.DenyProject, authz.DenyOwnership, authz.DenyClassification} {
		if !isAuthorizationDenial(deny) {
			t.Fatalf("expected denial for %q", deny)
		}
	}
	if isAuthorizationDenial(errors.New("normal error")) {
		t.Fatal("non-denial should return false")
	}
	if isAuthorizationDenial(nil) {
		t.Fatal("nil should return false")
	}
}

func TestCoverageRespondOperationErrorPaths(t *testing.T) {
	ops := newFakeOperations()
	ops.authorizeAdminErr = &privacy.Error{Code: privacy.ErrCodeValidation, Message: "privacy issue"}
	h := newCoverageHandler(ops)
	w, r := authed(http.MethodGet, "/api/admin/ai/status", "")
	h.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), string(privacy.ErrCodeValidation)) {
		t.Fatalf("privacy error: status=%d body=%s", w.Code, w.Body.String())
	}
}
