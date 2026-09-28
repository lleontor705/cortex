package http

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lleontor705/cortex/v2/internal/web"
	"github.com/lleontor705/cortex/v2/internal/webkey"
)

// webAssets mirrors the exported static tree shape used by internal/web tests:
// a root index plus a nested route document.
func webAssets() fstest.MapFS {
	return fstest.MapFS{
		"index.html":       {Data: []byte("<!doctype html><title>cortex web</title>")},
		"graph/index.html": {Data: []byte("<!doctype html><title>graph</title>")},
	}
}

func serveComposed(t *testing.T, srv *Server, method, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	return rec
}

// TestWebMountServesIndexWithAPIAndHealthIntact proves the composed handler
// delegates the root surface to the embedded web UI while /api/* and /health
// keep their exact pre-existing contracts.
func TestWebMountServesIndexWithAPIAndHealthIntact(t *testing.T) {
	srv := setupTestServerWithOptions(t, Options{
		AuthToken:  "secret-token",
		WebHandler: web.NewHandler(web.Config{Assets: webAssets()}, nil),
	})

	root := serveComposed(t, srv, http.MethodGet, "/", nil)
	if root.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200 (body=%q)", root.Code, root.Body.String())
	}
	if ct := root.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("GET / content-type = %q, want text/html", ct)
	}
	if !strings.Contains(root.Body.String(), "<!doctype html") {
		t.Fatalf("GET / did not serve the embedded index: %q", root.Body.String())
	}

	unauthorized := serveComposed(t, srv, http.MethodGet, "/api/observations", nil)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("GET /api/observations without API token = %d, want 401", unauthorized.Code)
	}

	health := serveComposed(t, srv, http.MethodGet, "/health", nil)
	if health.Code != http.StatusOK {
		t.Fatalf("GET /health = %d, want 200 (unauthenticated)", health.Code)
	}

	authorized := serveComposed(t, srv, http.MethodGet, "/api/observations", map[string]string{"X-API-Key": "secret-token"})
	if authorized.Code != http.StatusOK {
		t.Fatalf("GET /api/observations with API token = %d, want 200 (body=%q)", authorized.Code, authorized.Body.String())
	}
}

// TestWebMountAppliesWebKeyGuard proves the mounted web surface is gated by the
// web-key verifier, not by http.token: absent credential renders the gate,
// invalid credential is rejected, and a valid key passes.
func TestWebMountAppliesWebKeyGuard(t *testing.T) {
	store, err := webkey.NewStore(filepath.Join(t.TempDir(), "web.key"))
	if err != nil {
		t.Fatalf("new web key store: %v", err)
	}
	secret, err := store.Generate()
	if err != nil {
		t.Fatalf("generate web key: %v", err)
	}

	srv := setupTestServerWithOptions(t, Options{
		WebHandler: web.NewHandler(web.Config{Assets: webAssets()}, store),
	})

	gate := serveComposed(t, srv, http.MethodGet, "/", nil)
	if gate.Code != http.StatusOK {
		t.Fatalf("GET / with no credential = %d, want 200 (paste-once gate)", gate.Code)
	}

	rejected := serveComposed(t, srv, http.MethodGet, "/", map[string]string{"Authorization": "Bearer ctx_wrong-key"})
	if rejected.Code != http.StatusUnauthorized {
		t.Fatalf("GET / with invalid web key = %d, want 401", rejected.Code)
	}

	accepted := serveComposed(t, srv, http.MethodGet, "/", map[string]string{"Authorization": "Bearer " + secret})
	if accepted.Code != http.StatusOK {
		t.Fatalf("GET / with valid web key = %d, want 200", accepted.Code)
	}
}

// TestWebMountAbsentLeavesAPIOnlyHandler is the regression guard: without an
// explicit web handler the composed server stays API-only (root is 404).
func TestWebMountAbsentLeavesAPIOnlyHandler(t *testing.T) {
	srv := setupTestServer(t)

	if got := serveComposed(t, srv, http.MethodGet, "/", nil).Code; got != http.StatusNotFound {
		t.Fatalf("GET / without web mount = %d, want 404", got)
	}
}
