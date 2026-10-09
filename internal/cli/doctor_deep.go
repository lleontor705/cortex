package cli

// Deep diagnostics for `cortex doctor --deep` (issue #158): ACTIVE checks
// that exercise the configured providers and storage with the credentials
// already available to the runtime, classifying every failure class from the
// Railway deployment saga that manual debugging caught and shallow doctor
// missed:
//
//   - web access key unreadable on a root-owned volume (writability probe)
//   - vector schema USAGE missing for the runtime role (has_schema_privilege)
//   - dimension config vs live provider mismatch (embedding round-trip)
//   - rerank 401-on-unknown-model misread as bad API key (Nan quirk)
//   - migration ledger health (head vs embedded head, tamper class)
//   - storage DSN role-distinct validation (runtime vs migration role)
//
// SECURITY CONTRACT: deep checks are fail-closed and never print secrets.
// API keys are never echoed; DSNs are never printed (connection failures
// report the ROLE label only, never the connection string); provider response
// bodies are reduced to a failure class + actionable hint and are never
// echoed verbatim. The web key file contents are never read or displayed —
// only existence and directory writability.
//
// ARCHITECTURE BOUNDARY (REQ-FOUND-001): internal/cli is local composition
// and must not import pgx. PostgreSQL probes go through the DeepDBOpener
// seam, injected at process start by cmd/cortex (the sole bridge to server
// composition, which links the pgx stdlib driver). Without injection the
// checks report BLOCKED with remediation — the correct, honest outcome.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/migration"
	"github.com/lleontor705/cortex/v2/internal/retrieval"
	"github.com/lleontor705/cortex/v2/internal/webkey"
)

// Deep check statuses. Only FAIL affects the doctor exit code.
const (
	deepPass    = "PASS"
	deepFail    = "FAIL"
	deepWarn    = "WARN"
	deepInfo    = "INFO"
	deepBlocked = "BLOCKED"
)

// deepCheck is one diagnostic outcome with optional actionable remediation.
type deepCheck struct {
	Name        string
	Status      string
	Detail      string
	Remediation string
}

// DeepDBOpener opens a database/sql connection pool over a registered
// PostgreSQL driver and verifies connectivity with a ping before returning.
// The driver name is deliberately absent from the signature: the injector
// owns the driver registration (pgx stdlib in the server composition).
type DeepDBOpener func(ctx context.Context, dsn string) (*sql.DB, error)

// deepDBOpener is the seam cmd/cortex wires at startup. nil in compositions
// that do not link a PostgreSQL driver; deep storage checks then report
// BLOCKED instead of pretending to verify.
var deepDBOpener DeepDBOpener

// SetDeepDBOpener wires the PostgreSQL probe seam. Called by cmd/cortex (the
// sole server-composition bridge) before command dispatch; harmless if
// called repeatedly with the same opener.
func SetDeepDBOpener(opener DeepDBOpener) {
	deepDBOpener = opener
}

// deepProbeTimeouts bound every active check so doctor never hangs on an
// unreachable provider (the deployment saga wasted minutes on 30s defaults).
const (
	deepEmbeddingTimeout = 10 * time.Second
	deepRerankTimeout    = 8 * time.Second
	deepLLMTimeout       = 8 * time.Second
	deepDBTimeout        = 5 * time.Second
	// deepMaxBodyBytes bounds probe response reads; bodies are used only for
	// classification and are never echoed.
	deepMaxBodyBytes = 64 << 10
)

// runDoctorDeep executes every deep check against the loaded configuration
// and prints a per-check verdict. Exit code 1 iff at least one check FAILed;
// BLOCKED/WARN/INFO do not fail the run (they describe missing capability or
// degraded-but-functional state).
func runDoctorDeep(stdout, stderr io.Writer) int {
	cfg, err := config.Load("")
	if err != nil {
		writef(stderr, "cortex: deep check config load failed, using defaults: %v\n", err)
		cfg = config.DefaultConfig()
	}

	writeln(stdout, "Cortex Doctor — Deep Diagnostics (active probes; credentials used, never printed)\n")

	ctx := context.Background()
	var checks []deepCheck
	checks = append(checks, probeDeepEmbedding(ctx, cfg))
	checks = append(checks, probeDeepVectorSchema(ctx, cfg)...)
	checks = append(checks, probeDeepStorageRoles(ctx, cfg)...)
	checks = append(checks, probeDeepRerank(ctx, cfg))
	checks = append(checks, probeDeepWebKey())
	checks = append(checks, probeDeepLLM(ctx, cfg))

	failures := 0
	for _, c := range checks {
		writef(stdout, "  [%s] %s: %s\n", c.Status, c.Name, c.Detail)
		if c.Remediation != "" {
			writef(stdout, "         ↳ %s\n", c.Remediation)
		}
		if c.Status == deepFail {
			failures++
		}
	}

	writeln(stdout, "")
	if failures > 0 {
		writef(stdout, "%d deep check(s) FAILED.\n", failures)
		return 1
	}
	writeln(stdout, "No deep check failed (BLOCKED = not configured here, WARN = degraded but functional).")
	return 0
}

// --- failure classification --------------------------------------------------

// failureClass buckets provider failures into actionable categories. The
// model-not-found class exists because Nan (and other OpenAI-compatible
// gateways) answer unknown model ids with HTTP 401 auth_error — naive
// classification sends operators rotating a perfectly good API key.
const (
	classAuth          = "auth-failure"
	classModelNotFound = "model-not-found"
	classNotFound      = "endpoint-not-found"
	classBadRequest    = "bad-request"
	classRateLimited   = "rate-limited"
	classServer        = "provider-server-error"
	classTimeout       = "timeout"
	classNetwork       = "network-error"
)

// classifyHTTPFailure maps an HTTP status + bounded response body (or a
// transport error) to a failure class and an operator hint. The body is
// inspected only for classification keywords and is NEVER echoed verbatim
// (provider bodies can echo request metadata; keep secrets out of output).
func classifyHTTPFailure(status int, body string, err error) (class, hint string) {
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return classTimeout, "provider did not answer within the probe timeout — check network egress/firewall or raise the provider timeout"
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return classTimeout, "provider did not answer within the probe timeout — check network egress/firewall or raise the provider timeout"
		}
		return classNetwork, "provider unreachable — verify base_url, DNS, and egress rules"
	}
	lowered := strings.ToLower(body)
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		if bodyMentionsUnknownModel(lowered) {
			return classModelNotFound, "the provider answered 401/auth_error for an unknown model id (Nan quirk): verify the MODEL id first before rotating the API key"
		}
		return classAuth, "credentials rejected — verify the CORTEX_*_API_KEY env var for this provider (keys are env-only by design)"
	case status == http.StatusNotFound:
		if bodyMentionsUnknownModel(lowered) {
			return classModelNotFound, "provider reports the model id does not exist — verify the model id"
		}
		return classNotFound, "endpoint path not found — check the base_url version segment (e.g. /v1); a version-inclusive base must not be double-prefixed (issue #123 rule)"
	case status == http.StatusBadRequest:
		if bodyMentionsUnknownModel(lowered) {
			return classModelNotFound, "provider rejected the model id with a 400 — verify the model id"
		}
		return classBadRequest, "provider rejected the probe payload — check model name and API version"
	case status == http.StatusTooManyRequests:
		return classRateLimited, "rate limited — the shared provider budget may be exhausted or the provider quota is lower than configured"
	case status >= 500:
		return classServer, "provider-side failure — retry later or check provider status"
	default:
		return classNetwork, fmt.Sprintf("unexpected provider status %d", status)
	}
}

func bodyMentionsUnknownModel(loweredBody string) bool {
	mentionsModel := strings.Contains(loweredBody, "model")
	notFound := strings.Contains(loweredBody, "not found") ||
		strings.Contains(loweredBody, "unknown") ||
		strings.Contains(loweredBody, "does not exist") ||
		strings.Contains(loweredBody, "invalid model") ||
		strings.Contains(loweredBody, "no such model")
	return mentionsModel && notFound
}

// --- shared probe helpers ------------------------------------------------------

// deepHTTPClient builds a fresh client per probe: doctor is a one-shot
// process, and per-probe clients keep timeout policy independent per check.
func deepHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}

// deepPostJSON POSTs a JSON payload with a bearer key and returns the status,
// a bounded body (classification only), and latency. The key is never logged
// and the body is never echoed verbatim by callers.
func deepPostJSON(ctx context.Context, timeout time.Duration, endpoint string, payload any, apiKey string) (status int, body string, latency time.Duration, err error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return 0, "", 0, fmt.Errorf("marshal probe payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return 0, "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	start := time.Now()
	resp, err := deepHTTPClient(timeout).Do(req)
	latency = time.Since(start)
	if err != nil {
		return 0, "", latency, err
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode, deepReadBounded(resp.Body), latency, nil
}

func deepReadBounded(r io.Reader) string {
	b, err := io.ReadAll(io.LimitReader(r, deepMaxBodyBytes))
	if err != nil {
		return ""
	}
	return string(b)
}

// --- provider probes ------------------------------------------------------------

// probeDeepEmbedding performs a live embedding round-trip and validates the
// returned dimension against the configured pgvector dimension when set.
func probeDeepEmbedding(ctx context.Context, cfg *config.Config) deepCheck {
	const name = "Embedding provider round-trip"
	provider := strings.TrimSpace(cfg.Search.EmbeddingProvider)
	switch provider {
	case "", "none":
		return deepCheck{Name: name, Status: deepInfo,
			Detail:      "not configured (search.embedding_provider is none)",
			Remediation: "set search.embedding_provider / CORTEX_EMBEDDING_PROVIDER to enable dense retrieval"}
	}
	switch provider {
	case "openai", "openai-compatible", "ollama":
	default:
		return deepCheck{Name: name, Status: deepFail,
			Detail:      fmt.Sprintf("unknown provider %q", provider),
			Remediation: "valid providers: ollama, openai, openai-compatible"}
	}

	baseURL := strings.TrimRight(strings.TrimSpace(cfg.Search.EmbeddingBaseURL), "/")
	model := strings.TrimSpace(cfg.Search.EmbeddingModel)
	var endpoint string
	switch provider {
	case "ollama":
		if baseURL == "" {
			baseURL = "http://localhost:11434"
		}
		if model == "" {
			model = "nomic-embed-text"
		}
		endpoint = baseURL + "/api/embed"
	case "openai":
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		if model == "" {
			model = "text-embedding-3-small"
		}
		endpoint = baseURL + "/embeddings"
	default: // openai-compatible
		if baseURL == "" {
			return deepCheck{Name: name, Status: deepFail,
				Detail:      "openai-compatible requires an explicit base_url",
				Remediation: "set search.embedding_base_url / CORTEX_EMBEDDING_BASE_URL"}
		}
		if model == "" {
			return deepCheck{Name: name, Status: deepFail,
				Detail:      "openai-compatible requires an explicit model id (dimension cannot be guessed for arbitrary endpoints)",
				Remediation: "set search.embedding_model / CORTEX_EMBEDDING_MODEL"}
		}
		endpoint = baseURL + "/embeddings"
	}

	status, body, latency, err := deepPostJSON(ctx, deepEmbeddingTimeout, endpoint,
		map[string]any{"model": model, "input": "cortex doctor deep probe"},
		config.ResolveEmbeddingAPIKey(provider))
	if err != nil {
		class, hint := classifyHTTPFailure(0, "", err)
		return deepCheck{Name: name, Status: deepFail,
			Detail:      fmt.Sprintf("%s: probe to %s failed: %v", class, endpoint, err),
			Remediation: hint}
	}
	if status != http.StatusOK {
		class, hint := classifyHTTPFailure(status, body, nil)
		return deepCheck{Name: name, Status: deepFail,
			Detail:      fmt.Sprintf("%s: provider returned HTTP %d from %s", class, status, endpoint),
			Remediation: hint}
	}

	dims := embeddingDimsFromBody(body)
	if dims <= 0 {
		return deepCheck{Name: name, Status: deepFail,
			Detail:      "provider returned 200 but no parsable embedding vector",
			Remediation: "verify the endpoint actually implements the embeddings contract"}
	}
	detail := fmt.Sprintf("model=%q dims=%d latency=%dms", model, dims, latency.Milliseconds())

	if want := cfg.Vector.Pgvector.Dimension; want > 0 {
		if dims != want {
			return deepCheck{Name: name, Status: deepFail, Detail: detail + fmt.Sprintf("; MISMATCH: configured vector dimension is %d", want),
				Remediation: "align vector.pgvector.dimension (CORTEX_VECTOR_PGVECTOR_DIMENSION) with the live provider output, or switch the embedding model — a live mismatch corrupts the vector replica (REQ-VEC-001)"}
		}
		detail += fmt.Sprintf(" (matches configured %d)", want)
	}
	return deepCheck{Name: name, Status: deepPass, Detail: detail}
}

// embeddingDimsFromBody parses the first embedding length out of an OpenAI
// (/embeddings) or Ollama (/api/embed) response without materializing the
// whole vector list.
func embeddingDimsFromBody(body string) int {
	var parsed struct {
		Data       []json.RawMessage `json:"data"`
		Embeddings [][]float32       `json:"embeddings"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return 0
	}
	if len(parsed.Embeddings) > 0 {
		return len(parsed.Embeddings[0])
	}
	for _, raw := range parsed.Data {
		var item struct {
			Embedding []float32 `json:"embedding"`
		}
		if err := json.Unmarshal(raw, &item); err == nil && len(item.Embedding) > 0 {
			return len(item.Embedding)
		}
	}
	return 0
}

// probeDeepRerank exercises the rerank contract with a 2-document request
// and classifies 401/404/400 into actionable messages.
func probeDeepRerank(ctx context.Context, cfg *config.Config) deepCheck {
	const name = "Rerank contract probe"
	provider := strings.TrimSpace(cfg.Search.RerankProvider)
	switch provider {
	case "", "none", "late-interaction":
		return deepCheck{Name: name, Status: deepInfo,
			Detail: "not configured (local late-interaction rerank needs no remote contract)"}
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.Search.RerankBaseURL), "/")
	if base == "" {
		return deepCheck{Name: name, Status: deepFail,
			Detail:      "openai-compatible rerank requires an explicit base_url",
			Remediation: "set search.rerank_base_url / CORTEX_RERANK_BASE_URL"}
	}
	key := config.ResolveRerankAPIKey()
	if key == "" {
		return deepCheck{Name: name, Status: deepFail,
			Detail:      "CORTEX_RERANK_API_KEY is not set",
			Remediation: "rerank credentials are env-only: export CORTEX_RERANK_API_KEY"}
	}
	model := strings.TrimSpace(cfg.Search.RerankModel)
	endpoint := retrieval.RerankEndpoint(base)
	status, body, latency, err := deepPostJSON(ctx, deepRerankTimeout, endpoint,
		map[string]any{
			"model":     model,
			"query":     "cortex doctor deep probe",
			"documents": []string{"first probe document", "second probe document"},
			"top_n":     2,
		}, key)
	if err != nil {
		class, hint := classifyHTTPFailure(0, "", err)
		return deepCheck{Name: name, Status: deepFail,
			Detail:      fmt.Sprintf("%s: probe to %s failed: %v", class, endpoint, err),
			Remediation: hint}
	}
	if status != http.StatusOK {
		class, hint := classifyHTTPFailure(status, body, nil)
		return deepCheck{Name: name, Status: deepFail,
			Detail:      fmt.Sprintf("%s: provider returned HTTP %d from %s", class, status, endpoint),
			Remediation: hint}
	}
	return deepCheck{Name: name, Status: deepPass,
		Detail: fmt.Sprintf("2-document rerank accepted (model=%q, %dms)", model, latency.Milliseconds())}
}

// probeDeepLLM exercises the configured LLM provider with a minimal request.
func probeDeepLLM(ctx context.Context, cfg *config.Config) deepCheck {
	const name = "LLM provider probe"
	provider := strings.TrimSpace(cfg.AI.Provider)
	switch provider {
	case "", "none":
		return deepCheck{Name: name, Status: deepInfo,
			Detail: "not configured (ai.provider is empty; local heuristic agents only)"}
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.AI.BaseURL), "/")
	model := strings.TrimSpace(cfg.AI.Model)
	var status int
	var body string
	var latency time.Duration
	var err error
	switch provider {
	case "ollama":
		if baseURL == "" {
			baseURL = "http://localhost:11434"
		}
		start := time.Now()
		var resp *http.Response
		resp, err = deepHTTPClient(deepLLMTimeout).Get(baseURL + "/api/tags")
		latency = time.Since(start)
		if err == nil {
			defer func() { _ = resp.Body.Close() }()
			status = resp.StatusCode
			body = deepReadBounded(resp.Body)
		}
	default: // openai / openai-compatible chat contract
		if baseURL == "" {
			return deepCheck{Name: name, Status: deepFail,
				Detail:      fmt.Sprintf("%s requires an explicit base_url", provider),
				Remediation: "set ai.base_url / CORTEX_LLM_BASE_URL"}
		}
		if model == "" {
			return deepCheck{Name: name, Status: deepFail,
				Detail:      fmt.Sprintf("%s requires an explicit model id", provider),
				Remediation: "set ai.model / CORTEX_LLM_MODEL"}
		}
		status, body, latency, err = deepPostJSON(ctx, deepLLMTimeout, baseURL+"/chat/completions",
			map[string]any{
				"model":      model,
				"messages":   []map[string]string{{"role": "user", "content": "ping"}},
				"max_tokens": 1,
			}, config.ResolveLLMAPIKey(provider))
	}
	if err != nil {
		class, hint := classifyHTTPFailure(0, "", err)
		return deepCheck{Name: name, Status: deepFail,
			Detail:      fmt.Sprintf("%s: probe to %s failed: %v", class, baseURL, err),
			Remediation: hint}
	}
	if status != http.StatusOK {
		class, hint := classifyHTTPFailure(status, body, nil)
		return deepCheck{Name: name, Status: deepFail,
			Detail:      fmt.Sprintf("%s: provider returned HTTP %d from %s", class, status, baseURL),
			Remediation: hint}
	}
	return deepCheck{Name: name, Status: deepPass,
		Detail: fmt.Sprintf("provider reachable (model=%q, %dms)", model, latency.Milliseconds())}
}

// --- web key probe ---------------------------------------------------------------

// probeDeepWebKey verifies the web access key identity is usable by the
// CURRENT process identity: env-pinned keys cannot go unreadable on a
// root-owned volume; file-backed keys can, so the state dir is probed with a
// real create/remove. The key material itself is never read or printed.
func probeDeepWebKey() deepCheck {
	const name = "Web access key"
	pinned, pinErr := webkey.ResolveEnvOverride()
	switch {
	case pinErr != nil:
		return deepCheck{Name: name, Status: deepFail,
			Detail:      "CORTEX_WEB_KEY is set but does not match the minted key format",
			Remediation: "the value must be ctx_ + 43 base64url characters — regenerate with 'cortex web key regenerate' and re-pin"}
	case pinned != "":
		return deepCheck{Name: name, Status: deepPass,
			Detail: "identity is env-pinned via CORTEX_WEB_KEY (key file unused; cannot be broken by volume permissions)"}
	}

	path := config.DefaultWebKeyFile()
	dir := filepath.Dir(path)
	// First boot creates the state dir; mirror that before probing so a
	// missing-but-creatable dir is not misreported as unwritable.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return deepCheck{Name: name, Status: deepFail,
			Detail:      fmt.Sprintf("state dir %s cannot be created by the current process identity: %v", dir, err),
			Remediation: "fix volume ownership/permissions so the cortex process can create its state dir (the Railway saga root-owned volume failure class); or pin the key via CORTEX_WEB_KEY"}
	}
	probe, err := os.CreateTemp(dir, ".doctor-writability-*")
	if err != nil {
		return deepCheck{Name: name, Status: deepFail,
			Detail:      fmt.Sprintf("state dir %s is NOT writable by the current process identity: %v", dir, err),
			Remediation: "fix volume ownership/permissions so the cortex process can create files (the Railway saga root-owned volume failure class); or pin the key via CORTEX_WEB_KEY"}
	}
	probeName := probe.Name()
	_ = probe.Close()
	_ = os.Remove(probeName)

	store, storeErr := webkey.NewStore(path)
	if storeErr != nil {
		return deepCheck{Name: name, Status: deepWarn,
			Detail:      fmt.Sprintf("dir writable, but key store %s unavailable: %v", path, storeErr),
			Remediation: "inspect filesystem permissions on the state directory"}
	}
	exists, existsErr := store.Exists()
	if existsErr != nil {
		return deepCheck{Name: name, Status: deepWarn,
			Detail:      fmt.Sprintf("dir writable, but key file %s status unreadable: %v", path, existsErr),
			Remediation: "inspect file permissions on the key file"}
	}
	if exists {
		return deepCheck{Name: name, Status: deepPass, Detail: fmt.Sprintf("file-backed at %s; dir writable", path)}
	}
	return deepCheck{Name: name, Status: deepInfo,
		Detail: fmt.Sprintf("file-backed at %s; dir writable; key not minted yet (first serve boot mints it)", path)}
}

// --- PostgreSQL probes -------------------------------------------------------------

// deepDefaultVectorSchema and deepDefaultVectorTable mirror the pgvector
// adapter's safe defaults so the probe inspects the same objects the runtime
// would touch (config zero values resolve to these).
const (
	deepDefaultVectorSchema = "cortex_vector"
	deepDefaultVectorTable  = "embeddings"
)

// probeDeepVectorSchema checks the pgvector extension, the runtime role's
// schema/table privileges, the embedding column dimension, and ANN index
// existence — each a distinct failure class from the deployment saga.
func probeDeepVectorSchema(ctx context.Context, cfg *config.Config) []deepCheck {
	const name = "Vector schema health (pgvector)"
	dsn := strings.TrimSpace(cfg.Vector.Pgvector.DSN)
	if dsn == "" {
		return []deepCheck{{Name: name, Status: deepInfo,
			Detail: "no vector DSN configured (vector.pgvector.dsn empty) — pgvector adapter not in use"}}
	}
	if deepDBOpener == nil {
		return []deepCheck{{Name: name, Status: deepBlocked,
			Detail:      "this build cannot probe PostgreSQL (no driver linked)",
			Remediation: "build via cmd/cortex (make build) which links the server composition; a stripped local build reports BLOCKED — that is correct behavior"}}
	}
	schema := strings.TrimSpace(cfg.Vector.Pgvector.Schema)
	if schema == "" {
		schema = deepDefaultVectorSchema
	}
	table := strings.TrimSpace(cfg.Vector.Pgvector.Table)
	if table == "" {
		table = deepDefaultVectorTable
	}

	ctx, cancel := context.WithTimeout(ctx, deepDBTimeout)
	defer cancel()
	db, err := deepDBOpener(ctx, dsn)
	if err != nil {
		return []deepCheck{{Name: name, Status: deepFail,
			Detail:      "runtime role cannot connect (DSN not shown by design)",
			Remediation: "verify vector.pgvector.dsn credentials, host reachability, and TLS requirements: " + err.Error()}}
	}
	defer func() { _ = db.Close() }()

	var checks []deepCheck

	// 1. pgvector extension installed.
	var extCount int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM pg_extension WHERE extname = 'vector'`).Scan(&extCount); err != nil {
		return append(checks, deepCheck{Name: name, Status: deepFail,
			Detail:      "cannot query pg_extension: " + err.Error(),
			Remediation: "the runtime role must be able to read the extension catalog"})
	}
	if extCount == 0 {
		checks = append(checks, deepCheck{Name: name, Status: deepFail,
			Detail:      "pgvector extension is NOT installed in this database",
			Remediation: "CREATE EXTENSION vector (run with the migration/admin role)"})
	} else {
		checks = append(checks, deepCheck{Name: name, Status: deepPass, Detail: "pgvector extension installed"})
	}

	// 2. Runtime role schema USAGE (the saga failure class).
	var schemaUsage bool
	if err := db.QueryRowContext(ctx,
		`SELECT has_schema_privilege(current_user, $1, 'USAGE')`, schema).Scan(&schemaUsage); err != nil {
		checks = append(checks, deepCheck{Name: name, Status: deepFail,
			Detail:      fmt.Sprintf("cannot evaluate schema privilege for %q: %v", schema, err),
			Remediation: "verify the schema exists and the role can resolve it"})
	} else if !schemaUsage {
		checks = append(checks, deepCheck{Name: name, Status: deepFail,
			Detail:      fmt.Sprintf("runtime role LACKS USAGE on schema %q — every vector query will fail at runtime", schema),
			Remediation: fmt.Sprintf("GRANT USAGE ON SCHEMA %s TO the runtime role (run with the migration/admin role)", schema)})
	} else {
		checks = append(checks, deepCheck{Name: name, Status: deepPass,
			Detail: fmt.Sprintf("runtime role has USAGE on schema %q", schema)})
	}

	// 3. Runtime role table DML privileges.
	qualified := schema + "." + table
	var tablePriv bool
	if err := db.QueryRowContext(ctx,
		`SELECT has_table_privilege(current_user, $1, 'SELECT,INSERT,UPDATE,DELETE')`, qualified).Scan(&tablePriv); err != nil {
		checks = append(checks, deepCheck{Name: name, Status: deepWarn,
			Detail:      fmt.Sprintf("table %s not resolvable: %v", qualified, err),
			Remediation: "the embeddings table does not exist yet or the role cannot resolve it — apply the vector schema migration"})
	} else if !tablePriv {
		checks = append(checks, deepCheck{Name: name, Status: deepFail,
			Detail:      fmt.Sprintf("runtime role LACKS full DML on %s", qualified),
			Remediation: fmt.Sprintf("GRANT SELECT,INSERT,UPDATE,DELETE ON %s TO the runtime role", qualified)})
	} else {
		checks = append(checks, deepCheck{Name: name, Status: deepPass, Detail: fmt.Sprintf("runtime role has DML on %s", qualified)})
	}

	// 4. Embedding column dimension vs configured dimension.
	typeDims := "SELECT format_type(a.atttypid, a.atttypmod) FROM pg_attribute a " +
		"JOIN pg_class c ON c.oid = a.attrelid " +
		"JOIN pg_namespace n ON n.oid = c.relnamespace " +
		"WHERE n.nspname = $1 AND c.relname = $2 AND a.attname = 'embedding'"
	var fmtType string
	err = db.QueryRowContext(ctx, typeDims, schema, table).Scan(&fmtType)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		checks = append(checks, deepCheck{Name: name, Status: deepWarn,
			Detail:      fmt.Sprintf("no 'embedding' column on %s — table not migrated", qualified),
			Remediation: "apply the pgvector adapter schema"})
	case err != nil:
		checks = append(checks, deepCheck{Name: name, Status: deepWarn,
			Detail: "cannot read embedding column type: " + err.Error()})
	default:
		liveDims := parseVectorTypmod(fmtType)
		want := cfg.Vector.Pgvector.Dimension
		switch {
		case liveDims <= 0:
			checks = append(checks, deepCheck{Name: name, Status: deepWarn,
				Detail: fmt.Sprintf("embedding column type %q is unconstrained (no fixed dimension)", fmtType)})
		case want > 0 && liveDims != want:
			checks = append(checks, deepCheck{Name: name, Status: deepFail,
				Detail:      fmt.Sprintf("embedding column is vector(%d) but configuration declares %d — live/config dimension mismatch", liveDims, want),
				Remediation: "align vector.pgvector.dimension with the actual table DDL (or re-migrate the table); mismatched writes are rejected fail-closed"})
		default:
			checks = append(checks, deepCheck{Name: name, Status: deepPass,
				Detail: fmt.Sprintf("embedding column %s matches the configured dimension", fmtType)})
		}
	}

	// 5. ANN index existence (HNSW or IVFFlat).
	rows, err := db.QueryContext(ctx,
		`SELECT indexdef FROM pg_indexes WHERE schemaname = $1 AND tablename = $2`, schema, table)
	if err != nil {
		checks = append(checks, deepCheck{Name: name, Status: deepWarn,
			Detail: "cannot list indexes: " + err.Error()})
	} else {
		hasANN := false
		for rows.Next() {
			var def string
			if err := rows.Scan(&def); err == nil {
				lowered := strings.ToLower(def)
				if strings.Contains(lowered, "using hnsw") || strings.Contains(lowered, "using ivfflat") {
					hasANN = true
					break
				}
			}
		}
		_ = rows.Err()
		_ = rows.Close()
		if hasANN {
			checks = append(checks, deepCheck{Name: name, Status: deepPass, Detail: "ANN index (HNSW/IVFFlat) present"})
		} else {
			checks = append(checks, deepCheck{Name: name, Status: deepWarn,
				Detail:      fmt.Sprintf("no HNSW/IVFFlat index on %s — vector search degrades to sequential scan", qualified),
				Remediation: "create the HNSW index per the pgvector adapter tuning defaults"})
		}
	}
	return checks
}

// parseVectorTypmod extracts the dimension from a pgvector format_type value
// like "vector(1536)"; 0 means unconstrained or unparsable.
func parseVectorTypmod(formatType string) int {
	open := strings.IndexByte(formatType, '(')
	close := strings.LastIndexByte(formatType, ')')
	if open < 0 || close <= open {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(formatType[open+1 : close]))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// dsnRole extracts the PostgreSQL role name from a DSN (URL form or key=value
// form) WITHOUT exposing the DSN or its password. Empty when absent.
func dsnRole(dsn string) string {
	s := strings.TrimSpace(dsn)
	if u, err := url.Parse(s); err == nil && u != nil && u.User != nil {
		return u.User.Username()
	}
	for _, kv := range strings.Fields(s) {
		if strings.HasPrefix(strings.ToLower(kv), "user=") {
			return kv[5:]
		}
	}
	return ""
}

// probeDeepStorageRoles validates the storage DSN pair: connectivity for both
// roles, role distinctness, and migration ledger head vs embedded head.
func probeDeepStorageRoles(ctx context.Context, cfg *config.Config) []deepCheck {
	const name = "Storage roles & migration ledger"
	runtimeDSN := strings.TrimSpace(cfg.Vector.Pgvector.DSN)
	migrationDSN := strings.TrimSpace(cfg.Vector.Pgvector.MigrationDSN)
	if migrationDSN == "" {
		return []deepCheck{{Name: name, Status: deepInfo,
			Detail: "no migration DSN configured (vector.pgvector.migration_dsn empty) — single-role setup, role-distinct validation not applicable"}}
	}
	if deepDBOpener == nil {
		return []deepCheck{{Name: name, Status: deepBlocked,
			Detail:      "this build cannot probe PostgreSQL (no driver linked)",
			Remediation: "build via cmd/cortex (make build); BLOCKED in a stripped local build is correct behavior"}}
	}

	var checks []deepCheck
	ctx, cancel := context.WithTimeout(ctx, deepDBTimeout)
	defer cancel()

	runtimeRole := ""
	if runtimeDSN != "" {
		runtimeRole = dsnRole(runtimeDSN)
		if db, err := deepDBOpener(ctx, runtimeDSN); err != nil {
			checks = append(checks, deepCheck{Name: name, Status: deepFail,
				Detail:      "runtime role cannot connect (DSN not shown by design)",
				Remediation: "verify vector.pgvector.dsn: " + err.Error()})
		} else {
			_ = db.Close()
			checks = append(checks, deepCheck{Name: name, Status: deepPass, Detail: "runtime role connects"})
		}
	}

	migDB, err := deepDBOpener(ctx, migrationDSN)
	if err != nil {
		return append(checks, deepCheck{Name: name, Status: deepFail,
			Detail:      "migration role cannot connect (DSN not shown by design)",
			Remediation: "verify vector.pgvector.migration_dsn: " + err.Error()})
	}
	defer func() { _ = migDB.Close() }()
	checks = append(checks, deepCheck{Name: name, Status: deepPass, Detail: "migration role connects"})

	// Role-distinct validation (runbook: docs/project-context-protocol-identity-privilege.md).
	if runtimeRole != "" {
		migRole := dsnRole(migrationDSN)
		if migRole != "" && strings.EqualFold(migRole, runtimeRole) {
			checks = append(checks, deepCheck{Name: name, Status: deepFail,
				Detail:      fmt.Sprintf("runtime and migration roles are IDENTICAL (%q) — violates the role-distinct privilege separation runbook", migRole),
				Remediation: "provision a distinct least-privilege runtime role; the migration role must never serve runtime traffic"})
		} else {
			checks = append(checks, deepCheck{Name: name, Status: deepPass, Detail: "roles are distinct (runtime ≠ migration)"})
		}
	}

	// Ledger head vs embedded head + head checksum tamper check.
	baseline, err := migration.NewV2Baseline()
	if err != nil {
		return append(checks, deepCheck{Name: name, Status: deepWarn,
			Detail: "cannot load embedded migration baseline: " + err.Error()})
	}
	_ = baseline

	var ledgerHead sql.NullInt64
	var ledgerChecksum sql.NullString
	err = migDB.QueryRowContext(ctx,
		`SELECT max(version) FROM cortex_server_migrations`).Scan(&ledgerHead)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !ledgerHead.Valid) {
		return append(checks, deepCheck{Name: name, Status: deepWarn,
			Detail:      "no server migration ledger rows — database never migrated",
			Remediation: "run the server migration apply at deploy time"})
	}
	if err != nil {
		return append(checks, deepCheck{Name: name, Status: deepWarn,
			Detail:      "migration ledger table unreadable: " + err.Error(),
			Remediation: "if the table is genuinely missing the database was never migrated; otherwise the role lacks SELECT on cortex_server_migrations"})
	}
	err = migDB.QueryRowContext(ctx,
		`SELECT checksum FROM cortex_server_migrations WHERE version = (SELECT max(version) FROM cortex_server_migrations)`).Scan(&ledgerChecksum)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return append(checks, deepCheck{Name: name, Status: deepWarn,
			Detail: "cannot read head checksum: " + err.Error()})
	}

	embeddedHead, embeddedChecksum := deepEmbeddedPostgresHead()
	switch {
	case int(ledgerHead.Int64) > embeddedHead:
		checks = append(checks, deepCheck{Name: name, Status: deepFail,
			Detail:      fmt.Sprintf("ledger head %d is BEYOND this runtime's embedded head %d — database created by a NEWER runtime (tamper-class)", ledgerHead.Int64, embeddedHead),
			Remediation: "deploy a runtime at least as new as the ledger head; do NOT roll the ledger back"})
	case int(ledgerHead.Int64) < embeddedHead:
		checks = append(checks, deepCheck{Name: name, Status: deepWarn,
			Detail:      fmt.Sprintf("ledger head %d is behind embedded head %d — migrations pending", ledgerHead.Int64, embeddedHead),
			Remediation: "run the migration rollout per docs/project-context-protocol-identity-privilege.md"})
	case ledgerChecksum.Valid && embeddedChecksum != "" && ledgerChecksum.String != embeddedChecksum:
		checks = append(checks, deepCheck{Name: name, Status: deepFail,
			Detail:      fmt.Sprintf("head migration %d checksum MISMATCH — ledger tamper-class divergence", ledgerHead.Int64),
			Remediation: "investigate before any further rollout; ledgers are append-only and prior checksums escalate as tamper"})
	default:
		checks = append(checks, deepCheck{Name: name, Status: deepPass,
			Detail: fmt.Sprintf("migration ledger head %d matches the embedded head and checksum", ledgerHead.Int64)})
	}
	return checks
}

// deepEmbeddedPostgresHead returns the runtime's embedded PostgreSQL
// migration head version and its pinned checksum.
func deepEmbeddedPostgresHead() (int, string) {
	pgMigrations, err := migration.NewPostgresServerMigrations()
	if err != nil {
		return 0, ""
	}
	head := 0
	var checksum string
	for _, m := range pgMigrations {
		if m.Version() > head {
			head = m.Version()
			checksum = m.Checksum()
		}
	}
	return head, checksum
}
