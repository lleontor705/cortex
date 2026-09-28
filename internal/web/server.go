package web

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/lleontor705/cortex/v2/internal/webkey"
)

const (
	// HealthPath is the unauthenticated readiness probe the web shell calls.
	HealthPath = "/health"
	// ConfigScriptPath serves the runtime endpoint configuration script.
	ConfigScriptPath = "/config.js"
	// APIPrefix is reserved for the API mux; the web handler never answers it.
	APIPrefix = "/api/"

	healthStatusOK        = "ok"
	healthServiceName     = "cortex-web"
	immutableCacheControl = "public, max-age=31536000, immutable"
	noCacheControl        = "no-cache, must-revalidate"

	// maxPathDecodeDepth bounds repeated percent-decoding so a hostile path can
	// never hide a traversal behind unbounded layered encoding.
	maxPathDecodeDepth = 4

	bearerScheme = "Bearer "
)

// Config controls the embedded web handler.
type Config struct {
	// ServerURL is the API origin injected as
	// window.__CORTEX_WEB_CONFIG__.serverUrl. When empty the generated script
	// resolves the page origin at load time, keeping the default same-origin.
	ServerURL string

	// Assets overrides the embedded export filesystem. Nil selects the
	// compiled-in dist tree; tests and alternate compositions may substitute one.
	Assets fs.FS
}

// NewHandler builds the embedded web surface. verifier gates the surface via the
// web-key credential; nil disables the guard (web surface mounted without a key).
func NewHandler(cfg Config, verifier webkey.Verifier) http.Handler {
	assetsFS := cfg.Assets
	if assetsFS == nil {
		assetsFS = Assets()
	}
	return &handler{
		assets:    assetsFS,
		verifier:  verifier,
		serverURL: strings.TrimRight(strings.TrimSpace(cfg.ServerURL), "/"),
	}
}

type handler struct {
	assets    fs.FS
	verifier  webkey.Verifier
	serverURL string
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
	case http.MethodOptions:
		w.WriteHeader(http.StatusNoContent)
		return
	default:
		w.Header().Set("Allow", "GET, HEAD, OPTIONS")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clean, ok := sanitizePath(r.URL.EscapedPath())
	if !ok {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	switch clean {
	case HealthPath:
		h.serveHealth(w, r)
		return
	case ConfigScriptPath:
		h.serveConfig(w, r)
		return
	}

	if strings.HasPrefix(clean, APIPrefix) || clean == strings.TrimSuffix(APIPrefix, "/") {
		// The API mux owns this namespace; answering with the SPA shell would
		// mask real API 404s as HTML.
		http.NotFound(w, r)
		return
	}

	if !h.authorize(w, r) {
		return
	}
	if h.serveAsset(w, r, clean) {
		return
	}
	if isRouteLike(clean) {
		h.serveShell(w, r)
		return
	}
	http.NotFound(w, r)
}

func (h *handler) serveHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, r, http.StatusOK, healthDocument{Status: healthStatusOK, Service: healthServiceName})
}

func (h *handler) serveConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", noCacheControl)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.WriteString(w, h.configScript())
}

// configScript emits the shape consumed by web/src/lib/server-endpoint.ts
// (window.__CORTEX_WEB_CONFIG__.serverUrl). With no configured origin it
// resolves the page origin so the default deployment stays same-origin.
func (h *handler) configScript() string {
	if h.serverURL == "" {
		return "window.__CORTEX_WEB_CONFIG__ = { \"serverUrl\": window.location.origin };\n"
	}
	payload, err := json.Marshal(struct {
		ServerURL string `json:"serverUrl"`
	}{ServerURL: h.serverURL})
	if err != nil {
		return "window.__CORTEX_WEB_CONFIG__ = {};\n"
	}
	return "window.__CORTEX_WEB_CONFIG__ = " + string(payload) + ";\n"
}

// authorize validates a presented web key. An absent credential is allowed so
// the shell can render the paste-once gate; once a credential is presented it
// must verify. API authorization is independent and unchanged by this guard.
func (h *handler) authorize(w http.ResponseWriter, r *http.Request) bool {
	if h.verifier == nil {
		return true
	}
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if header == "" {
		return true
	}
	token, ok := bearerToken(header)
	if !ok {
		h.rejectUnauthorized(w, r)
		return false
	}
	if err := h.verifier.Verify(token); err != nil {
		h.rejectUnauthorized(w, r)
		return false
	}
	return true
}

func (h *handler) rejectUnauthorized(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, http.StatusUnauthorized, errorDocument{
		Status: "unauthorized",
		Error:  "invalid web access key",
	})
}

func (h *handler) serveAsset(w http.ResponseWriter, r *http.Request, cleanPath string) bool {
	rel := strings.TrimPrefix(cleanPath, "/")
	if rel == "" {
		rel = "index.html"
	}
	name, ok := h.resolveAsset(rel)
	if !ok {
		return false
	}
	h.writeFile(w, r, name)
	return true
}

// resolveAsset maps a request path to a concrete export file, preferring the
// directory index of extensionless routes (trailingSlash export layout).
func (h *handler) resolveAsset(rel string) (string, bool) {
	candidates := []string{rel}
	switch {
	case strings.HasSuffix(rel, "/"):
		candidates = []string{rel + "index.html"}
	case !strings.Contains(path.Base(rel), "."):
		candidates = []string{rel + "/index.html", rel}
	}
	for _, candidate := range candidates {
		info, err := fs.Stat(h.assets, candidate)
		if err != nil {
			continue
		}
		if info.IsDir() {
			dirIndex := path.Join(candidate, "index.html")
			if _, err := fs.Stat(h.assets, dirIndex); err == nil {
				return dirIndex, true
			}
			continue
		}
		return candidate, true
	}
	return "", false
}

func (h *handler) serveShell(w http.ResponseWriter, r *http.Request) {
	data, err := fs.ReadFile(h.assets, "index.html")
	if err != nil {
		http.Error(w, "web UI assets unavailable", http.StatusInternalServerError)
		return
	}
	h.setStaticHeaders(w, "index.html")
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(data))
}

func (h *handler) writeFile(w http.ResponseWriter, r *http.Request, name string) {
	data, err := fs.ReadFile(h.assets, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h.setStaticHeaders(w, name)
	http.ServeContent(w, r, path.Base(name), time.Time{}, bytes.NewReader(data))
}

func (h *handler) setStaticHeaders(w http.ResponseWriter, name string) {
	w.Header().Set("Content-Type", contentTypeFor(name))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if strings.HasPrefix(name, "_next/") {
		w.Header().Set("Cache-Control", immutableCacheControl)
		return
	}
	w.Header().Set("Cache-Control", noCacheControl)
}

// isRouteLike reports whether a missing path should fall back to the SPA shell.
// Asset-looking paths (an extension in the final segment) and the hashed
// _next/ tree must 404 instead of silently returning HTML.
func isRouteLike(cleanPath string) bool {
	if strings.HasPrefix(cleanPath, "/_next/") {
		return false
	}
	return !strings.Contains(path.Base(cleanPath), ".")
}

// sanitizePath normalizes a raw request path and rejects traversal. Percent
// decoding is repeated (bounded) because net/http decodes once before the
// handler sees the URL, so %252e%252e still hides a literal dot-dot.
func sanitizePath(escapedPath string) (string, bool) {
	if escapedPath == "" {
		escapedPath = "/"
	}
	decoded := escapedPath
	for depth := 0; depth < maxPathDecodeDepth; depth++ {
		next, err := url.PathUnescape(decoded)
		if err != nil {
			return "", false
		}
		if next == decoded {
			break
		}
		decoded = next
	}
	if strings.ContainsRune(decoded, '\x00') {
		return "", false
	}
	decoded = strings.ReplaceAll(decoded, "\\", "/")
	for _, segment := range strings.Split(decoded, "/") {
		if segment == ".." {
			return "", false
		}
	}
	clean := path.Clean(decoded)
	if clean == "." || clean == "" {
		clean = "/"
	}
	if !strings.HasPrefix(clean, "/") {
		clean = "/" + clean
	}
	return clean, true
}

func bearerToken(header string) (string, bool) {
	if len(header) < len(bearerScheme) || !strings.EqualFold(header[:len(bearerScheme)], bearerScheme) {
		return "", false
	}
	token := strings.TrimSpace(header[len(bearerScheme):])
	return token, token != ""
}

func contentTypeFor(name string) string {
	switch path.Ext(name) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json", ".map":
		return "application/json; charset=utf-8"
	case ".txt":
		return "text/plain; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".ico":
		return "image/x-icon"
	case ".png":
		return "image/png"
	case ".webmanifest":
		return "application/manifest+json"
	case ".woff2":
		return "font/woff2"
	}
	if detected := mime.TypeByExtension(path.Ext(name)); detected != "" {
		return detected
	}
	return "application/octet-stream"
}

type healthDocument struct {
	Status  string `json:"status"`
	Service string `json:"service"`
}

type errorDocument struct {
	Status string `json:"status"`
	Error  string `json:"error"`
}

func writeJSON(w http.ResponseWriter, r *http.Request, status int, body any) {
	payload, err := json.Marshal(body)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if r != nil && r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(payload)
}
