package web_test

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lleontor705/cortex/v2/internal/web"
	"github.com/lleontor705/cortex/v2/internal/webkey"
)

func testAssets() fstest.MapFS {
	return fstest.MapFS{
		"index.html":                         {Data: []byte("<!doctype html><title>shell</title>")},
		"graph/index.html":                   {Data: []byte("<!doctype html><title>graph</title>")},
		"robots.txt":                         {Data: []byte("User-agent: *\n")},
		"_next/static/chunks/main-abc123.js": {Data: []byte("console.log(1)")},
		"_next/static/css/app-abc123.css":    {Data: []byte("body{}")},
	}
}

type stubVerifier map[string]bool

func (s stubVerifier) Verify(secret string) error {
	if s[secret] {
		return nil
	}
	return webkey.ErrInvalidKey
}

func newHandler(cfg web.Config, v webkey.Verifier) http.Handler {
	if cfg.Assets == nil {
		cfg.Assets = testAssets()
	}
	return web.NewHandler(cfg, v)
}

func request(t *testing.T, h http.Handler, method, target string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status=%d want %d (body=%q)", rec.Code, want, rec.Body.String())
	}
}

func TestEmbeddedPlaceholderDocumentIsServed(t *testing.T) {
	rec := request(t, web.NewHandler(web.Config{}, nil), http.MethodGet, "/", nil)
	assertStatus(t, rec, http.StatusOK)
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type=%q want text/html", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-cache") {
		t.Fatalf("cache-control=%q want no-cache", cc)
	}
	if body := strings.ToLower(rec.Body.String()); !strings.Contains(body, "<!doctype html") {
		t.Fatalf("embedded index.html not served (body=%q)", rec.Body.String())
	}
}

// The all: prefix on //go:embed is what keeps underscore trees like _next in
// the binary; plain go:embed silently drops them.
func TestEmbeddedDistKeepsUnderscoreTree(t *testing.T) {
	if _, err := os.Stat(filepath.Join("dist", "_next")); err != nil {
		t.Skip("synced export absent (placeholder-only checkout)")
	}
	if _, err := fs.Stat(web.Assets(), "_next"); err != nil {
		t.Fatalf("embedded FS dropped the _next tree; //go:embed must use all:dist: %v", err)
	}
}

func TestStaticAssetContentTypesAndCacheHeaders(t *testing.T) {
	h := newHandler(web.Config{}, nil)
	cases := []struct{ target, contentType, cache string }{
		{"/_next/static/chunks/main-abc123.js", "text/javascript", "immutable"},
		{"/_next/static/css/app-abc123.css", "text/css", "immutable"},
		{"/robots.txt", "text/plain", "no-cache"},
		{"/index.html", "text/html", "no-cache"},
		{"/graph/", "text/html", "no-cache"},
	}
	for _, tc := range cases {
		rec := request(t, h, http.MethodGet, tc.target, nil)
		assertStatus(t, rec, http.StatusOK)
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, tc.contentType) {
			t.Errorf("%s content-type=%q want %q", tc.target, ct, tc.contentType)
		}
		if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, tc.cache) {
			t.Errorf("%s cache-control=%q want %q", tc.target, cc, tc.cache)
		}
	}
}

func TestPathTraversalIsRejected(t *testing.T) {
	h := newHandler(web.Config{}, nil)
	targets := []string{
		"/../server.go",
		"/%2e%2e/server.go",
		"/%252e%252e%252fserver.go",
		"/graph/..%2f..%2fmain.go",
		"/..%5c..%5cconfig",
	}
	for _, target := range targets {
		rec := request(t, h, http.MethodGet, target, nil)
		if rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
			t.Errorf("%s status=%d want 400/404", target, rec.Code)
		}
	}
}

func TestUnknownRouteFallsBackToShellButAPIAndAssetsDoNot(t *testing.T) {
	h := newHandler(web.Config{}, nil)
	shell := request(t, h, http.MethodGet, "/projects/42/details", nil)
	assertStatus(t, shell, http.StatusOK)
	if !strings.Contains(shell.Body.String(), "shell") {
		t.Fatalf("SPA fallback did not serve index.html: %q", shell.Body.String())
	}

	assertStatus(t, request(t, h, http.MethodGet, "/missing/chunk.js", nil), http.StatusNotFound)

	for _, target := range []string{"/api/observations", "/api"} {
		rec := request(t, h, http.MethodGet, target, nil)
		assertStatus(t, rec, http.StatusNotFound)
		if strings.Contains(rec.Body.String(), "shell") {
			t.Errorf("%s was swallowed by the SPA fallback", target)
		}
	}
}

func TestHealthIsUnauthenticatedJSON(t *testing.T) {
	h := newHandler(web.Config{}, stubVerifier{"good": true})
	rec := request(t, h, http.MethodGet, "/health", map[string]string{"Authorization": "Bearer wrong"})
	assertStatus(t, rec, http.StatusOK)
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type=%q want application/json", ct)
	}
	var doc struct {
		Status  string `json:"status"`
		Service string `json:"service"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("health body is not JSON: %v", err)
	}
	if doc.Status != "ok" || doc.Service != "cortex-web" {
		t.Fatalf("health=%+v want {ok cortex-web}", doc)
	}

	head := request(t, h, http.MethodHead, "/health", nil)
	assertStatus(t, head, http.StatusOK)
	if head.Body.Len() != 0 {
		t.Fatalf("HEAD /health wrote a body: %q", head.Body.String())
	}
}

func TestConfigScriptEmitsRuntimeEndpoint(t *testing.T) {
	h := newHandler(web.Config{ServerURL: "https://api.example.test/"}, nil)
	rec := request(t, h, http.MethodGet, "/config.js", map[string]string{"Authorization": "Bearer wrong"})
	assertStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	if !strings.Contains(body, `"serverUrl":"https://api.example.test"`) {
		t.Fatalf("config.js missing trimmed runtime serverUrl: %q", body)
	}
	if !strings.Contains(body, "window.__CORTEX_WEB_CONFIG__") {
		t.Fatalf("config.js missing global assignment: %q", body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Fatalf("content-type=%q want javascript", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-cache") {
		t.Fatalf("cache-control=%q want no-cache", cc)
	}

	sameOrigin := request(t, newHandler(web.Config{}, nil), http.MethodGet, "/config.js", nil)
	if !strings.Contains(sameOrigin.Body.String(), "window.location.origin") {
		t.Fatalf("config.js missing same-origin default: %q", sameOrigin.Body.String())
	}

	head := request(t, h, http.MethodHead, "/config.js", nil)
	assertStatus(t, head, http.StatusOK)
	if head.Body.Len() != 0 {
		t.Fatalf("HEAD /config.js wrote a body: %q", head.Body.String())
	}
}

func TestWebKeyGuardAcceptsValidKeyAndRejectsOnlyPresentedInvalidKeys(t *testing.T) {
	h := newHandler(web.Config{}, stubVerifier{"good": true})
	cases := []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{"valid bearer", map[string]string{"Authorization": "Bearer good"}, http.StatusOK},
		{"invalid bearer", map[string]string{"Authorization": "Bearer bad"}, http.StatusUnauthorized},
		{"absent credential keeps gate reachable", nil, http.StatusOK},
		{"malformed scheme", map[string]string{"Authorization": "Basic good"}, http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := request(t, h, http.MethodGet, "/", tc.headers)
			assertStatus(t, rec, tc.want)
			if tc.want == http.StatusUnauthorized && !strings.Contains(rec.Body.String(), `"status":"unauthorized"`) {
				t.Fatalf("unauthorized body=%q", rec.Body.String())
			}
		})
	}

	unguarded := request(t, newHandler(web.Config{}, nil), http.MethodGet, "/", map[string]string{"Authorization": "Bearer bad"})
	assertStatus(t, unguarded, http.StatusOK)

	deepLink := request(t, h, http.MethodGet, "/graph/", map[string]string{"Authorization": "Bearer good"})
	assertStatus(t, deepLink, http.StatusOK)
}

// precompressedFS pairs each compressible asset with a gzip sibling the way the
// web-build sync step emits them, leaving robots.txt identity-only.
func precompressedFS(t *testing.T) fstest.MapFS {
	t.Helper()
	shell := []byte("<!doctype html><title>shell</title>")
	script := []byte("console.log('precompressed')")
	return fstest.MapFS{
		"index.html":    {Data: shell},
		"index.html.gz": {Data: gzipBytes(t, shell)},
		"app.js":        {Data: script},
		"app.js.gz":     {Data: gzipBytes(t, script)},
		"robots.txt":    {Data: []byte("User-agent: *\n")},
	}
}

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func gunzip(t *testing.T, data []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer func() { _ = zr.Close() }()
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	return out
}

func TestStaticAssetsEmitStrongContentETag(t *testing.T) {
	h := newHandler(web.Config{}, nil)
	for _, target := range []string{"/", "/index.html", "/robots.txt", "/_next/static/chunks/main-abc123.js", "/_next/static/css/app-abc123.css"} {
		rec := request(t, h, http.MethodGet, target, nil)
		assertStatus(t, rec, http.StatusOK)
		etag := rec.Header().Get("ETag")
		if strings.HasPrefix(etag, "W/") || !strings.HasPrefix(etag, `"`) || !strings.HasSuffix(etag, `"`) || len(etag) < 3 {
			t.Fatalf("%s etag=%q want quoted strong validator", target, etag)
		}
	}

	sameBytes := request(t, h, http.MethodGet, "/index.html", nil).Header().Get("ETag")
	viaShell := request(t, h, http.MethodGet, "/", nil).Header().Get("ETag")
	if sameBytes != viaShell {
		t.Fatalf("index.html etag=%q shell etag=%q want identical", sameBytes, viaShell)
	}
	distinct := request(t, h, http.MethodGet, "/robots.txt", nil).Header().Get("ETag")
	if distinct == sameBytes {
		t.Fatalf("distinct assets share etag=%q", sameBytes)
	}
}

func TestConditionalRequestReturns304WithoutBody(t *testing.T) {
	h := newHandler(web.Config{}, nil)
	for _, target := range []string{"/index.html", "/_next/static/chunks/main-abc123.js", "/robots.txt"} {
		t.Run(target, func(t *testing.T) {
			initial := request(t, h, http.MethodGet, target, nil)
			assertStatus(t, initial, http.StatusOK)
			etag := initial.Header().Get("ETag")
			if etag == "" {
				t.Fatalf("%s emitted no ETag", target)
			}

			conditional := request(t, h, http.MethodGet, target, map[string]string{"If-None-Match": etag})
			assertStatus(t, conditional, http.StatusNotModified)
			if conditional.Body.Len() != 0 {
				t.Fatalf("%s 304 carried a body %q", target, conditional.Body.String())
			}
			if got := conditional.Header().Get("ETag"); got != etag {
				t.Fatalf("%s 304 etag=%q want %q", target, got, etag)
			}

			stale := request(t, h, http.MethodGet, target, map[string]string{"If-None-Match": `"stale-validator"`})
			assertStatus(t, stale, http.StatusOK)
			if stale.Body.Len() == 0 {
				t.Fatalf("%s stale validator suppressed the body", target)
			}
		})
	}
}

func TestShellFallbackHonorsConditionalRequests(t *testing.T) {
	h := newHandler(web.Config{}, nil)
	etag := request(t, h, http.MethodGet, "/", nil).Header().Get("ETag")
	if etag == "" {
		t.Fatal("SPA shell emitted no ETag")
	}
	rec := request(t, h, http.MethodGet, "/projects/42/details", map[string]string{"If-None-Match": etag})
	assertStatus(t, rec, http.StatusNotModified)
	if rec.Body.Len() != 0 {
		t.Fatalf("304 shell carried a body %q", rec.Body.String())
	}
}

func TestPrecompressedAssetServedWhenClientAcceptsGzip(t *testing.T) {
	h := web.NewHandler(web.Config{Assets: precompressedFS(t)}, nil)
	rec := request(t, h, http.MethodGet, "/app.js", map[string]string{"Accept-Encoding": "gzip"})
	assertStatus(t, rec, http.StatusOK)
	if enc := rec.Header().Get("Content-Encoding"); enc != "gzip" {
		t.Fatalf("content-encoding=%q want gzip", enc)
	}
	if vary := rec.Header().Get("Vary"); !strings.Contains(vary, "Accept-Encoding") {
		t.Fatalf("vary=%q want Accept-Encoding", vary)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Fatalf("content-type=%q want javascript", ct)
	}
	if body := string(gunzip(t, rec.Body.Bytes())); body != "console.log('precompressed')" {
		t.Fatalf("decompressed body=%q", body)
	}

	head := request(t, h, http.MethodHead, "/app.js", map[string]string{"Accept-Encoding": "gzip"})
	assertStatus(t, head, http.StatusOK)
	if head.Body.Len() != 0 {
		t.Fatalf("HEAD served a body: %q", head.Body.String())
	}
}

func TestIdentityServedWhenGzipAbsentOrRefused(t *testing.T) {
	h := web.NewHandler(web.Config{Assets: precompressedFS(t)}, nil)

	plain := request(t, h, http.MethodGet, "/app.js", nil)
	assertStatus(t, plain, http.StatusOK)
	if enc := plain.Header().Get("Content-Encoding"); enc != "" {
		t.Fatalf("no Accept-Encoding content-encoding=%q want none", enc)
	}
	if plain.Body.String() != "console.log('precompressed')" {
		t.Fatalf("identity body=%q", plain.Body.String())
	}

	refused := request(t, h, http.MethodGet, "/app.js", map[string]string{"Accept-Encoding": "gzip;q=0"})
	assertStatus(t, refused, http.StatusOK)
	if enc := refused.Header().Get("Content-Encoding"); enc != "" {
		t.Fatalf("q=0 refusal content-encoding=%q want none", enc)
	}

	absent := request(t, h, http.MethodGet, "/robots.txt", map[string]string{"Accept-Encoding": "gzip"})
	assertStatus(t, absent, http.StatusOK)
	if enc := absent.Header().Get("Content-Encoding"); enc != "" {
		t.Fatalf("unsupported-asset content-encoding=%q want none", enc)
	}
	if absent.Body.String() != "User-agent: *\n" {
		t.Fatalf("unsupported-asset body=%q", absent.Body.String())
	}
}

func TestShellPrefersPrecompressedVariant(t *testing.T) {
	h := web.NewHandler(web.Config{Assets: precompressedFS(t)}, nil)
	rec := request(t, h, http.MethodGet, "/projects/42", map[string]string{"Accept-Encoding": "gzip"})
	assertStatus(t, rec, http.StatusOK)
	if enc := rec.Header().Get("Content-Encoding"); enc != "gzip" {
		t.Fatalf("shell content-encoding=%q want gzip", enc)
	}
	if body := string(gunzip(t, rec.Body.Bytes())); !strings.Contains(body, "shell") {
		t.Fatalf("shell variant decompressed to %q", body)
	}
}

func TestPrecompressedVariantHonorsConditionalRequests(t *testing.T) {
	h := web.NewHandler(web.Config{Assets: precompressedFS(t)}, nil)
	etag := request(t, h, http.MethodGet, "/app.js", map[string]string{"Accept-Encoding": "gzip"}).Header().Get("ETag")
	if etag == "" {
		t.Fatal("precompressed asset emitted no ETag")
	}
	rec := request(t, h, http.MethodGet, "/app.js", map[string]string{"Accept-Encoding": "gzip", "If-None-Match": etag})
	assertStatus(t, rec, http.StatusNotModified)
	if rec.Body.Len() != 0 {
		t.Fatalf("304 precompressed carried a body %q", rec.Body.String())
	}
}
