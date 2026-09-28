package web_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lleontor705/cortex/v2/internal/web"
)

// assetClassFS drives every content-type branch plus the directory-index and
// _next immutable-cache classes without depending on a synced export tree.
func assetClassFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":              {Data: []byte("<!doctype html><title>shell</title>")},
		"app.js":                  {Data: []byte("export default 1")},
		"app.mjs":                 {Data: []byte("export default 2")},
		"app.css":                 {Data: []byte("body{}")},
		"data.json":               {Data: []byte("{}")},
		"bundle.js.map":           {Data: []byte("{}")},
		"robots.txt":              {Data: []byte("User-agent: *\n")},
		"logo.svg":                {Data: []byte("<svg/>")},
		"favicon.ico":             {Data: []byte("icon")},
		"hero.png":                {Data: []byte("png")},
		"site.webmanifest":        {Data: []byte("{}")},
		"font.woff2":              {Data: []byte("font")},
		"blob.bin":                {Data: []byte("binary")},
		"flat":                    {Data: []byte("extensionless")},
		"graph/index.html":        {Data: []byte("<!doctype html><title>graph</title>")},
		"docs/index.html":         {Data: []byte("<title>docs</title>")},
		"dir/child.txt":           {Data: []byte("child")},
		"_next/static/chunk.js":   {Data: []byte("console.log(1)")},
		"_next/data/payload.json": {Data: []byte("{}")},
	}
}

func richHandler() http.Handler {
	return web.NewHandler(web.Config{Assets: assetClassFS()}, nil)
}

func TestAssetContentTypesAndCacheClasses(t *testing.T) {
	h := richHandler()
	cases := []struct{ target, contentType, cache string }{
		{"/", "text/html", "no-cache"},
		{"/index.html", "text/html", "no-cache"},
		{"/app.js", "text/javascript", "no-cache"},
		{"/app.mjs", "text/javascript", "no-cache"},
		{"/app.css", "text/css", "no-cache"},
		{"/data.json", "application/json", "no-cache"},
		{"/bundle.js.map", "application/json", "no-cache"},
		{"/robots.txt", "text/plain", "no-cache"},
		{"/logo.svg", "image/svg+xml", "no-cache"},
		{"/favicon.ico", "image/x-icon", "no-cache"},
		{"/hero.png", "image/png", "no-cache"},
		{"/site.webmanifest", "application/manifest+json", "no-cache"},
		{"/font.woff2", "font/woff2", "no-cache"},
		{"/blob.bin", "octet-stream", "no-cache"},
		{"/flat", "octet-stream", "no-cache"},
		{"/docs", "text/html", "no-cache"},
		{"/_next/static/chunk.js", "text/javascript", "immutable"},
		{"/_next/data/payload.json", "application/json", "immutable"},
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
		if nosniff := rec.Header().Get("X-Content-Type-Options"); nosniff != "nosniff" {
			t.Errorf("%s x-content-type-options=%q want nosniff", tc.target, nosniff)
		}
	}
}

func TestHeadRequestsCarryHeadersWithoutBody(t *testing.T) {
	h := richHandler()
	for _, target := range []string{"/app.js", "/_next/static/chunk.js", "/projects/42"} {
		rec := request(t, h, http.MethodHead, target, nil)
		assertStatus(t, rec, http.StatusOK)
		if rec.Body.Len() != 0 {
			t.Errorf("%s HEAD wrote a body: %q", target, rec.Body.String())
		}
	}
}

func TestMethodMatrixEnforcesGetHeadOptions(t *testing.T) {
	h := richHandler()
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		rec := request(t, h, method, "/", nil)
		assertStatus(t, rec, http.StatusMethodNotAllowed)
		allow := rec.Header().Get("Allow")
		if !strings.Contains(allow, "GET") || !strings.Contains(allow, "HEAD") || !strings.Contains(allow, "OPTIONS") {
			t.Errorf("%s Allow=%q want GET, HEAD, OPTIONS", method, allow)
		}
	}
	assertStatus(t, request(t, h, http.MethodOptions, "/", nil), http.StatusNoContent)
}

func TestTraversalVectorsRejectWithBadRequest(t *testing.T) {
	h := richHandler()
	targets := []string{
		"/../server.go",
		"/%2e%2e/etc/passwd",
		"/.%2e/",
		"/%2E%2E/",
		"/a/b/%2e%2e/%2e%2e/c",
		"/..%5c..%5cconfig",
		"/..\\..\\secret",
		"/%252e%252e%252fetc",
		"/%2525252e%2525252e/",
		"/safe/%00/name",
	}
	for _, target := range targets {
		rec := request(t, h, http.MethodGet, target, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s status=%d want 400 (body=%q)", target, rec.Code, rec.Body.String())
		}
	}
}

// Beyond the bounded decode depth or spelled with non-ASCII lookalikes, a path
// must stay inert route text: never a 400 traversal verdict, never a leak.
func TestInertLookalikesAreNotTraversal(t *testing.T) {
	h := richHandler()
	targets := []string{
		"/%252525252e%252525252e/",
		"/\uFF0E\uFF0E/",
		"/\u2025/",
	}
	for _, target := range targets {
		rec := request(t, h, http.MethodGet, target, nil)
		assertStatus(t, rec, http.StatusOK)
		if body := strings.ToLower(rec.Body.String()); !strings.Contains(body, "<!doctype html") {
			t.Errorf("%s not contained as shell: %q", target, rec.Body.String())
		}
	}
}

func TestSPAFallbackEdges(t *testing.T) {
	h := richHandler()
	shellTargets := []string{
		"/projects/42/details/extra",
		"/trailing/",
		"/graph/extra",
		"/a/b/c?tab=one",
		"/graph/?panel=two",
		"/api-not-really",
	}
	for _, target := range shellTargets {
		rec := request(t, h, http.MethodGet, target, nil)
		assertStatus(t, rec, http.StatusOK)
		if body := strings.ToLower(rec.Body.String()); !strings.Contains(body, "<!doctype html") {
			t.Errorf("%s not served as shell: %q", target, rec.Body.String())
		}
	}
	missing := []string{"/missing.js", "/missing.css", "/missing.woff2", "/_next/static/missing.js", "/_next/gone", "/.well-known/security.txt"}
	for _, target := range missing {
		assertStatus(t, request(t, h, http.MethodGet, target, nil), http.StatusNotFound)
	}
}

func TestMissingShellAssetFailsClosed(t *testing.T) {
	h := web.NewHandler(web.Config{Assets: fstest.MapFS{"robots.txt": {Data: []byte("x")}}}, nil)
	assertStatus(t, request(t, h, http.MethodGet, "/unknown/route", nil), http.StatusInternalServerError)
}

func TestGuardBearerAndExemptionEdges(t *testing.T) {
	h := web.NewHandler(web.Config{Assets: assetClassFS()}, stubVerifier{"good": true})
	cases := []struct {
		name, target, header string
		want                 int
	}{
		{"lowercase scheme", "/", "bearer good", http.StatusOK},
		{"extra inner spaces", "/", "Bearer    good", http.StatusOK},
		{"surrounding spaces", "/", "  Bearer good  ", http.StatusOK},
		{"scheme without token", "/", "Bearer", http.StatusUnauthorized},
		{"scheme with blank token", "/", "Bearer    ", http.StatusUnauthorized},
		{"token with suffix", "/", "Bearer good extra", http.StatusUnauthorized},
		{"wrong scheme", "/", "Token good", http.StatusUnauthorized},
		{"absent credential", "/", "", http.StatusOK},
		{"invalid key on asset", "/app.js", "Bearer bad", http.StatusUnauthorized},
		{"absent key on asset", "/app.js", "", http.StatusOK},
		{"config.js exempt", "/config.js", "Bearer bad", http.StatusOK},
		{"health exempt", "/health", "Bearer bad", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var headers map[string]string
			if tc.header != "" {
				headers = map[string]string{"Authorization": tc.header}
			}
			assertStatus(t, request(t, h, http.MethodGet, tc.target, headers), tc.want)
		})
	}
}

func TestConfigScriptEscapesServerURLInjection(t *testing.T) {
	injected := "  https://api.example.test/\"}\"><script>alert(1)</script>///  "
	h := web.NewHandler(web.Config{Assets: assetClassFS(), ServerURL: injected}, nil)
	rec := request(t, h, http.MethodGet, "/config.js", nil)
	assertStatus(t, rec, http.StatusOK)
	if body := strings.ToLower(rec.Body.String()); strings.Contains(body, "<script") {
		t.Fatalf("config.js emitted raw markup: %q", rec.Body.String())
	}
	if nosniff := rec.Header().Get("X-Content-Type-Options"); nosniff != "nosniff" {
		t.Fatalf("config.js x-content-type-options=%q want nosniff", nosniff)
	}
	if got, want := decodeConfigServerURL(t, rec.Body.String()), strings.TrimRight(strings.TrimSpace(injected), "/"); got != want {
		t.Fatalf("serverUrl=%q want %q", got, want)
	}

	slashOnly := web.NewHandler(web.Config{Assets: assetClassFS(), ServerURL: "  /  "}, nil)
	rec = request(t, slashOnly, http.MethodGet, "/config.js", nil)
	if !strings.Contains(rec.Body.String(), "window.location.origin") {
		t.Fatalf("slash-only origin must fall back to same-origin: %q", rec.Body.String())
	}
}

func decodeConfigServerURL(t *testing.T, body string) string {
	t.Helper()
	start := strings.Index(body, "= ")
	end := strings.LastIndex(body, ";")
	if start < 0 || end <= start {
		t.Fatalf("unexpected config.js shape: %q", body)
	}
	var doc struct {
		ServerURL string `json:"serverUrl"`
	}
	if err := json.Unmarshal([]byte(body[start+2:end]), &doc); err != nil {
		t.Fatalf("config.js payload is not valid JSON: %v (%q)", err, body)
	}
	return doc.ServerURL
}
