// Parity route registration coverage (REQ-SH-012, REQ-SH-014).
//
// These tests exercise the composed local serve handler end to end: the five
// web-critical endpoints must be served by internal/api over the bundle-backed
// local port, behind the withAuth token gate, while the embedded web fallback
// keeps hard-404ing every /api/* path it still catches.
package http

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/web"
)

const parityRouteToken = "parity-secret"

// setupParityServer composes the local serve handler with a token gate and the
// embedded web fallback, so auth ordering and route precedence are exercised
// together rather than in isolation.
func setupParityServer(t *testing.T) *Server {
	t.Helper()
	return setupTestServerWithOptions(t, Options{
		AuthToken:  parityRouteToken,
		WebHandler: web.NewHandler(web.Config{Assets: webAssets()}, nil),
	})
}

func parityRouteTargets() []string {
	return []string{
		"/api/me",
		"/api/stats",
		"/api/projects",
		"/api/agent/projects",
		"/api/graph/project-graph?project=demo",
	}
}

func parityAuth() map[string]string { return map[string]string{"X-API-Key": parityRouteToken} }

// TestParityRoutesRequireToken proves every parity endpoint sits behind the
// existing withAuth gate: without a credential the request is rejected before
// any handler or web fallback can answer.
func TestParityRoutesRequireToken(t *testing.T) {
	srv := setupParityServer(t)
	handler := srv.httpServer.Handler

	for _, target := range parityRouteTargets() {
		rec := doRaw(t, handler, http.MethodGet, target, nil, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("GET %s without token = %d, want 401 (body=%q)", target, rec.Code, rec.Body.String())
		}
	}
}

// TestParityRoutesServeAuthenticated proves the five routes are registered on
// the explicit mux (not swallowed by the web shell) and emit JSON.
func TestParityRoutesServeAuthenticated(t *testing.T) {
	srv := setupParityServer(t)
	handler := srv.httpServer.Handler

	for _, target := range parityRouteTargets() {
		rec := doRaw(t, handler, http.MethodGet, target, nil, parityAuth())
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s with token = %d, want 200 (body=%q)", target, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Fatalf("GET %s content-type = %q, want application/json", target, ct)
		}
	}
}

// TestParityRoutesReflectLocalStores proves the routes are backed by the local
// bundle rather than static stubs: seeded rows appear in every envelope.
func TestParityRoutesReflectLocalStores(t *testing.T) {
	srv := setupParityServer(t)
	handler := srv.httpServer.Handler
	ctx := context.Background()

	if err := srv.deps.Sessions.Create(ctx, &domain.Session{ID: "s1", Project: "demo", Directory: "."}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := srv.deps.Observations.Save(ctx, &domain.Observation{
		SessionID: "s1", Type: "manual", Title: "note", Content: "body", Project: "demo", Scope: "project",
	}); err != nil {
		t.Fatalf("save observation: %v", err)
	}

	var stats domain.ServerStats
	decodeJSON(t, doRaw(t, handler, http.MethodGet, "/api/stats", nil, parityAuth()), &stats)
	want := domain.ServerStats{Observations: 1, Sessions: 1, ActiveSessions: 1, Projects: 1}
	if stats != want {
		t.Fatalf("stats = %+v, want %+v", stats, want)
	}

	var projects []string
	decodeJSON(t, doRaw(t, handler, http.MethodGet, "/api/projects", nil, parityAuth()), &projects)
	if len(projects) != 1 || projects[0] != "demo" {
		t.Fatalf("projects = %v, want [demo]", projects)
	}

	var agent struct {
		Projects []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		} `json:"projects"`
	}
	decodeJSON(t, doRaw(t, handler, http.MethodGet, "/api/agent/projects", nil, parityAuth()), &agent)
	if len(agent.Projects) != 1 || agent.Projects[0].ID != "demo" || agent.Projects[0].Label != "demo" {
		t.Fatalf("agent projects = %+v, want [{demo demo}]", agent.Projects)
	}

	var subgraph domain.GraphSubgraph
	decodeJSON(t, doRaw(t, handler, http.MethodGet, "/api/graph/project-graph?project=demo", nil, parityAuth()), &subgraph)
	if subgraph.Root != "demo" || len(subgraph.Nodes) != 1 {
		t.Fatalf("subgraph root/nodes = %q/%d, want demo/1", subgraph.Root, len(subgraph.Nodes))
	}
}

// TestParityMeReturnsLocalOwnerPrincipal proves /api/me renders the synthetic
// single-user local identity instead of a null principal (REQ-SH-012).
func TestParityMeReturnsLocalOwnerPrincipal(t *testing.T) {
	srv := setupParityServer(t)
	rec := doRaw(t, srv.httpServer.Handler, http.MethodGet, "/api/me", nil, parityAuth())
	assertStatus(t, rec, http.StatusOK)

	var envelope struct {
		ID          string   `json:"id"`
		Type        string   `json:"type"`
		OrgID       string   `json:"org_id"`
		WorkspaceID string   `json:"workspace_id"`
		Roles       []string `json:"roles"`
	}
	decodeJSON(t, rec, &envelope)
	if envelope.ID == "" || envelope.Type == "" || envelope.OrgID == "" || envelope.WorkspaceID == "" {
		t.Fatalf("local principal envelope = %+v, want stable identity fields", envelope)
	}
	if len(envelope.Roles) != 1 || envelope.Roles[0] != "owner" {
		t.Fatalf("local principal roles = %v, want [owner]", envelope.Roles)
	}
}

// TestParityDeferredRoutesStayUnregistered pins REQ-SH-014: the deferred
// server-only surface stays absent from the local mux and is never masked by
// the SPA shell.
func TestParityDeferredRoutesStayUnregistered(t *testing.T) {
	srv := setupParityServer(t)
	handler := srv.httpServer.Handler

	for _, target := range []string{"/api/audit", "/api/system/metrics", "/api/rag/stats"} {
		rec := doRaw(t, handler, http.MethodGet, target, nil, parityAuth())
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404 (deferred surface)", target, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "<!doctype") {
			t.Fatalf("GET %s served the SPA shell: %q", target, rec.Body.String())
		}
	}
}

// TestUnknownAPIRouteFallsThroughToHard404 proves an unregistered /api/* path
// still 404s rather than rendering the web document.
func TestUnknownAPIRouteFallsThroughToHard404(t *testing.T) {
	srv := setupParityServer(t)
	rec := doRaw(t, srv.httpServer.Handler, http.MethodGet, "/api/not-a-route", nil, parityAuth())
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/not-a-route = %d, want 404 (body=%q)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "<!doctype") {
		t.Fatalf("GET /api/not-a-route served the SPA shell: %q", rec.Body.String())
	}
}
