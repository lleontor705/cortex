package cli

// Offline tests for `cortex doctor --deep` (issue #158). Every external
// dependency is faked: providers via httptest endpoints, PostgreSQL via an
// in-process database/sql/driver implementation, and the web-key state dir
// via a redirected HOME. No test touches the network beyond loopback or a
// live database, keeping the suite hermetic and deterministic.

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/migration"
)

// --- failure classification ---------------------------------------------------

func TestClassifyHTTPFailure(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		err        error
		wantClass  string
		wantSubstr string // required substring of the hint
	}{
		{
			name:       "plain 401 is auth",
			status:     http.StatusUnauthorized,
			body:       `{"error":{"message":"invalid api key","type":"auth_error"}}`,
			wantClass:  classAuth,
			wantSubstr: "API_KEY",
		},
		{
			name:       "nan quirk 401 unknown model is model-not-found",
			status:     http.StatusUnauthorized,
			body:       `{"error":{"message":"model 'qwen3-embedding-8B' not found","type":"auth_error"}}`,
			wantClass:  classModelNotFound,
			wantSubstr: "model",
		},
		{
			name:       "403 maps to auth",
			status:     http.StatusForbidden,
			body:       `denied`,
			wantClass:  classAuth,
			wantSubstr: "credentials",
		},
		{
			name:       "404 endpoint not found",
			status:     http.StatusNotFound,
			body:       `not found`,
			wantClass:  classNotFound,
			wantSubstr: "version segment",
		},
		{
			name:       "404 model unknown mentions model",
			status:     http.StatusNotFound,
			body:       `{"error":{"message":"model unknown-model does not exist"}}`,
			wantClass:  classModelNotFound,
			wantSubstr: "model id",
		},
		{
			name:       "400 with unknown model",
			status:     http.StatusBadRequest,
			body:       `{"error":{"message":"invalid model id"}}`,
			wantClass:  classModelNotFound,
			wantSubstr: "model id",
		},
		{
			name:       "plain 400 is bad request",
			status:     http.StatusBadRequest,
			body:       `{"error":{"message":"missing field"}}`,
			wantClass:  classBadRequest,
			wantSubstr: "payload",
		},
		{
			name:       "429 rate limited",
			status:     http.StatusTooManyRequests,
			body:       `slow down`,
			wantClass:  classRateLimited,
			wantSubstr: "budget",
		},
		{
			name:       "5xx provider failure",
			status:     http.StatusInternalServerError,
			body:       `boom`,
			wantClass:  classServer,
			wantSubstr: "provider-side",
		},
		{
			name:       "timeout error",
			err:        &fakeTimeoutError{},
			wantClass:  classTimeout,
			wantSubstr: "timeout",
		},
		{
			name:       "context deadline",
			err:        context.DeadlineExceeded,
			wantClass:  classTimeout,
			wantSubstr: "timeout",
		},
		{
			name:       "connection refused",
			err:        errors.New("dial tcp: connection refused"),
			wantClass:  classNetwork,
			wantSubstr: "base_url",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			class, hint := classifyHTTPFailure(tt.status, tt.body, tt.err)
			if class != tt.wantClass {
				t.Fatalf("class = %q, want %q", class, tt.wantClass)
			}
			if !strings.Contains(hint, tt.wantSubstr) {
				t.Fatalf("hint %q missing %q", hint, tt.wantSubstr)
			}
		})
	}
}

type fakeTimeoutError struct{}

func (fakeTimeoutError) Error() string     { return "context deadline exceeded" }
func (fakeTimeoutError) Timeout() bool     { return true }
func (fakeTimeoutError) Temporary() bool   { return true }

// --- embedding probe ------------------------------------------------------------

func TestDeepEmbeddingProbeRoundTrip(t *testing.T) {
	const dims = 8
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer probe-key" {
			t.Errorf("missing bearer key")
		}
		vec := make([]float32, dims)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"embedding": vec}}})
	}))
	defer srv.Close()

	cfg := config.DefaultConfig()
	cfg.Search.EmbeddingProvider = "openai-compatible"
	cfg.Search.EmbeddingBaseURL = srv.URL
	cfg.Search.EmbeddingModel = "probe-model"
	t.Setenv("CORTEX_EMBEDDING_API_KEY", "probe-key")

	check := probeDeepEmbedding(context.Background(), cfg)
	if check.Status != deepPass {
		t.Fatalf("status = %s (%s)", check.Status, check.Detail)
	}
	if !strings.Contains(check.Detail, fmt.Sprintf("dims=%d", dims)) {
		t.Fatalf("detail missing dims: %q", check.Detail)
	}
	if !strings.Contains(check.Detail, "latency=") {
		t.Fatalf("detail missing latency: %q", check.Detail)
	}
}

func TestDeepEmbeddingProbeDimensionMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vec := make([]float32, 4)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"embedding": vec}}})
	}))
	defer srv.Close()

	cfg := config.DefaultConfig()
	cfg.Search.EmbeddingProvider = "openai-compatible"
	cfg.Search.EmbeddingBaseURL = srv.URL
	cfg.Search.EmbeddingModel = "probe-model"
	cfg.Vector.Pgvector.Dimension = 1536 // saga class: configured dims diverge from live output
	t.Setenv("CORTEX_EMBEDDING_API_KEY", "probe-key")

	check := probeDeepEmbedding(context.Background(), cfg)
	if check.Status != deepFail {
		t.Fatalf("status = %s, want FAIL", check.Status)
	}
	if !strings.Contains(check.Detail, "MISMATCH") {
		t.Fatalf("detail missing MISMATCH: %q", check.Detail)
	}
	if !strings.Contains(check.Remediation, "dimension") {
		t.Fatalf("remediation missing dimension guidance: %q", check.Remediation)
	}
}

func TestDeepEmbeddingProbeNanUnknownModelQuirk(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"model 'ghost' not found","type":"auth_error"}}`))
	}))
	defer srv.Close()

	cfg := config.DefaultConfig()
	cfg.Search.EmbeddingProvider = "openai-compatible"
	cfg.Search.EmbeddingBaseURL = srv.URL
	cfg.Search.EmbeddingModel = "ghost"

	check := probeDeepEmbedding(context.Background(), cfg)
	if check.Status != deepFail {
		t.Fatalf("status = %s, want FAIL", check.Status)
	}
	if !strings.Contains(check.Detail, classModelNotFound) {
		t.Fatalf("detail must classify as model-not-found (not auth): %q", check.Detail)
	}
	if !strings.Contains(check.Remediation, "MODEL") {
		t.Fatalf("remediation must point at the model id, not the key: %q", check.Remediation)
	}
}

func TestDeepEmbeddingProbeNotConfigured(t *testing.T) {
	cfg := config.DefaultConfig() // provider "none"
	check := probeDeepEmbedding(context.Background(), cfg)
	if check.Status != deepInfo {
		t.Fatalf("status = %s, want INFO", check.Status)
	}
}

func TestDeepEmbeddingProbeOpenAICompatibleRequiresBaseAndModel(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Search.EmbeddingProvider = "openai-compatible"
	check := probeDeepEmbedding(context.Background(), cfg)
	if check.Status != deepFail || !strings.Contains(check.Detail, "base_url") {
		t.Fatalf("missing base_url fail-closed: %+v", check)
	}
	cfg.Search.EmbeddingBaseURL = "http://localhost:1"
	check = probeDeepEmbedding(context.Background(), cfg)
	if check.Status != deepFail || !strings.Contains(check.Detail, "model") {
		t.Fatalf("missing model fail-closed: %+v", check)
	}
}

// --- rerank probe ----------------------------------------------------------------

func TestDeepRerankProbe(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		var gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{
				{"index": 0, "relevance_score": 0.9}, {"index": 1, "relevance_score": 0.1},
			}})
		}))
		defer srv.Close()
		cfg := config.DefaultConfig()
		cfg.Search.RerankProvider = "openai-compatible"
		cfg.Search.RerankBaseURL = srv.URL // version-inclusive base: must NOT double /v1 (#123)
		t.Setenv("CORTEX_RERANK_API_KEY", "rerank-key")
		check := probeDeepRerank(context.Background(), cfg)
		if check.Status != deepPass {
			t.Fatalf("status = %s (%s)", check.Status, check.Detail)
		}
		if gotPath != "/v1/rerank" {
			t.Fatalf("probe hit %q, want /v1/rerank", gotPath)
		}
	})

	t.Run("404 misconfigured path", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		}))
		defer srv.Close()
		cfg := config.DefaultConfig()
		cfg.Search.RerankProvider = "openai-compatible"
		cfg.Search.RerankBaseURL = srv.URL
		t.Setenv("CORTEX_RERANK_API_KEY", "rerank-key")
		check := probeDeepRerank(context.Background(), cfg)
		if check.Status != deepFail || !strings.Contains(check.Detail, classNotFound) {
			t.Fatalf("want endpoint-not-found FAIL: %+v", check)
		}
	})

	t.Run("401 plain auth stays auth", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
		}))
		defer srv.Close()
		cfg := config.DefaultConfig()
		cfg.Search.RerankProvider = "openai-compatible"
		cfg.Search.RerankBaseURL = srv.URL
		t.Setenv("CORTEX_RERANK_API_KEY", "wrong-key")
		check := probeDeepRerank(context.Background(), cfg)
		if check.Status != deepFail || !strings.Contains(check.Detail, classAuth) {
			t.Fatalf("want auth-failure FAIL: %+v", check)
		}
	})
}

func TestDeepRerankProbeNotConfigured(t *testing.T) {
	cfg := config.DefaultConfig()
	check := probeDeepRerank(context.Background(), cfg)
	if check.Status != deepInfo {
		t.Fatalf("status = %s, want INFO", check.Status)
	}
}

// --- web key probe ----------------------------------------------------------------

const testPinnedKey = "ctx_" + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" // ctx_ + 43 base64url chars

func TestDeepWebKeyPinned(t *testing.T) {
	t.Setenv("CORTEX_WEB_KEY", testPinnedKey)
	check := probeDeepWebKey()
	if check.Status != deepPass {
		t.Fatalf("status = %s (%s)", check.Status, check.Detail)
	}
	if !strings.Contains(check.Detail, "env-pinned") {
		t.Fatalf("detail must report env-pinned identity: %q", check.Detail)
	}
}

func TestDeepWebKeyInvalidPin(t *testing.T) {
	t.Setenv("CORTEX_WEB_KEY", "not-a-valid-format")
	check := probeDeepWebKey()
	if check.Status != deepFail {
		t.Fatalf("status = %s, want FAIL", check.Status)
	}
}

func TestDeepWebKeyFileBackedWritable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CORTEX_WEB_KEY", "")
	check := probeDeepWebKey()
	if check.Status != deepInfo && check.Status != deepPass {
		t.Fatalf("status = %s (%s)", check.Status, check.Detail)
	}
	if !strings.Contains(check.Detail, "dir writable") {
		t.Fatalf("detail missing writability verdict: %q", check.Detail)
	}
}

func TestDeepWebKeyUnwritableDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission denial not observable")
	}
	home := t.TempDir()
	stateDir := filepath.Join(home, ".cortex")
	if err := os.MkdirAll(stateDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(stateDir, 0o700) })
	t.Setenv("HOME", home)
	t.Setenv("CORTEX_WEB_KEY", "")
	check := probeDeepWebKey()
	if check.Status != deepFail {
		t.Fatalf("status = %s, want FAIL (%s)", check.Status, check.Detail)
	}
	if !strings.Contains(check.Remediation, "CORTEX_WEB_KEY") {
		t.Fatalf("remediation must offer the env-pin escape hatch: %q", check.Remediation)
	}
}

// --- fake PostgreSQL driver ---------------------------------------------------------

// fakeScenario describes one canned PostgreSQL world served by the fake
// driver. Queries are matched by stable substring of the (lowercased) SQL.
type fakeScenario struct {
	extInstalled bool
	schemaUsage  bool
	tablePriv    bool
	columnType   string  // "" → no embedding column
	indexes      []string
	ledgerHead   int64   // 0 → no ledger rows
	ledgerSum    string  // checksum at head
	connectErr   error   // simulated at connect/ping time
}

var (
	fakeScenarioMu sync.Mutex
	fakeScenarios  = map[string]*fakeScenario{}
	fakeRegistered sync.Once
)

const fakeDriverName = "doctordeep-fakepg"

func registerFakePG() {
	fakeRegistered.Do(func() {
		sql.Register(fakeDriverName, fakeDriver{})
	})
}

func setFakeScenario(name string, s *fakeScenario) {
	registerFakePG()
	fakeScenarioMu.Lock()
	defer fakeScenarioMu.Unlock()
	fakeScenarios[name] = s
}

func clearFakeScenarios() {
	fakeScenarioMu.Lock()
	defer fakeScenarioMu.Unlock()
	fakeScenarios = map[string]*fakeScenario{}
}

// fakeDSN builds a DSN that carries BOTH a parseable role (userinfo, for
// dsnRole) and a scenario selector: postgres://<role>@fakepg/<scenario>.
func fakeDSN(role, scenario string) string {
	return "postgres://" + role + "@fakepg/" + scenario
}

func fakeOpenerFor(t *testing.T) DeepDBOpener {
	registerFakePG()
	t.Cleanup(clearFakeScenarios)
	return func(ctx context.Context, dsn string) (*sql.DB, error) {
		db, err := sql.Open(fakeDriverName, dsn)
		if err != nil {
			return nil, err
		}
		if err := db.PingContext(ctx); err != nil {
			_ = db.Close()
			return nil, err
		}
		return db, nil
	}
}

type fakeDriver struct{}

func (fakeDriver) OpenConnector(dsn string) (driver.Connector, error) {
	return fakeConnector{dsn: dsn}, nil
}

func (fakeDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("fake driver requires Connector (OpenConnector path)")
}

func fakeScenarioFromDSN(dsn string) (string, error) {
	u, err := url.Parse(dsn)
	if err == nil && u.Host == "fakepg" {
		return strings.TrimPrefix(u.Path, "/"), nil
	}
	return strings.TrimPrefix(dsn, "fakepg://"), nil
}

type fakeConnector struct{ dsn string }

func (c fakeConnector) Connect(context.Context) (driver.Conn, error) {
	name, err := fakeScenarioFromDSN(c.dsn)
	if err != nil {
		return nil, err
	}
	fakeScenarioMu.Lock()
	sc := fakeScenarios[name]
	fakeScenarioMu.Unlock()
	if sc == nil {
		return nil, fmt.Errorf("fake pg: unknown scenario %q", name)
	}
	return &fakeConn{sc: sc}, nil
}

func (c fakeConnector) Driver() driver.Driver { return fakeDriver{} }

type fakeConn struct{ sc *fakeScenario }

func (c *fakeConn) Prepare(query string) (driver.Stmt, error) {
	return nil, errors.New("fake pg: prepared statements unsupported")
}
func (c *fakeConn) Close() error              { return nil }
func (c *fakeConn) Begin() (driver.Tx, error) { return nil, errors.New("fake pg: tx unsupported") }
func (c *fakeConn) Ping(context.Context) error {
	if c.sc.connectErr != nil {
		return c.sc.connectErr
	}
	return nil
}

func (c *fakeConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if c.sc.connectErr != nil {
		return nil, c.sc.connectErr
	}
	lower := strings.ToLower(query)
	sc := c.sc
	// ORDER MATTERS: the head-checksum query contains BOTH "select checksum"
	// and "max(version)"; match the more specific pattern first.
	switch {
	case strings.Contains(lower, "select checksum"):
		if sc.ledgerHead == 0 || sc.ledgerSum == "" {
			return &fakeRows{cols: []string{"checksum"}}, nil
		}
		return &fakeRows{cols: []string{"checksum"}, rows: [][]driver.Value{{sc.ledgerSum}}}, nil
	case strings.Contains(lower, "max(version)"):
		if sc.ledgerHead == 0 {
			return &fakeRows{cols: []string{"max"}}, nil
		}
		return &fakeRows{cols: []string{"max"}, rows: [][]driver.Value{{sc.ledgerHead}}}, nil
	case strings.Contains(lower, "pg_extension"):
		n := int64(0)
		if sc.extInstalled {
			n = 1
		}
		return &fakeRows{cols: []string{"count"}, rows: [][]driver.Value{{n}}}, nil
	case strings.Contains(lower, "has_schema_privilege"):
		return &fakeRows{cols: []string{"ok"}, rows: [][]driver.Value{{sc.schemaUsage}}}, nil
	case strings.Contains(lower, "has_table_privilege"):
		return &fakeRows{cols: []string{"ok"}, rows: [][]driver.Value{{sc.tablePriv}}}, nil
	case strings.Contains(lower, "format_type"):
		if sc.columnType == "" {
			return &fakeRows{cols: []string{"t"}}, nil
		}
		return &fakeRows{cols: []string{"t"}, rows: [][]driver.Value{{sc.columnType}}}, nil
	case strings.Contains(lower, "pg_indexes"):
		rows := make([][]driver.Value, 0, len(sc.indexes))
		for _, idx := range sc.indexes {
			rows = append(rows, []driver.Value{idx})
		}
		return &fakeRows{cols: []string{"indexdef"}, rows: rows}, nil
	default:
		return nil, fmt.Errorf("fake pg: unexpected query %q", query)
	}
}

func (c *fakeConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return nil, errors.New("fake pg: exec unsupported")
}

type fakeRows struct {
	cols []string
	rows [][]driver.Value
	i    int
}

func (r *fakeRows) Columns() []string { return r.cols }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.i])
	r.i++
	return nil
}

// --- embedded migration head helper ------------------------------------------------

func embeddedHead(t *testing.T) (int64, string) {
	t.Helper()
	pgMigrations, err := migration.NewPostgresServerMigrations()
	if err != nil {
		t.Fatal(err)
	}
	var head int64
	var headSum string
	for _, m := range pgMigrations {
		if int64(m.Version()) > head {
			head = int64(m.Version())
			headSum = m.Checksum()
		}
	}
	return head, headSum
}

// --- vector schema probe scenarios ----------------------------------------------

func healthyScenario() *fakeScenario {
	return &fakeScenario{
		extInstalled: true,
		schemaUsage:  true,
		tablePriv:    true,
		columnType:   "vector(1536)",
		indexes:      []string{"CREATE INDEX idx ON cortex_vector.embeddings USING hnsw (embedding vector_cosine_ops)"},
	}
}

func TestDeepVectorSchemaHealthy(t *testing.T) {
	sc := healthyScenario()
	setFakeScenario("healthy", sc)

	deepDBOpener = fakeOpenerFor(t)
	t.Cleanup(func() { deepDBOpener = nil })

	cfg := config.DefaultConfig()
	cfg.Vector.Pgvector.DSN = fakeDSN("runner", "healthy")
	cfg.Vector.Pgvector.Dimension = 1536

	var sawUsage, sawDML, sawANN bool
	for _, c := range probeDeepVectorSchema(context.Background(), cfg) {
		switch {
		case strings.Contains(c.Detail, "USAGE on schema"):
			sawUsage = c.Status == deepPass
		case strings.Contains(c.Detail, "DML on"):
			sawDML = c.Status == deepPass
		case strings.Contains(c.Detail, "ANN index"):
			sawANN = c.Status == deepPass
		}
		if c.Status == deepFail {
			t.Fatalf("healthy scenario produced FAIL: %s — %s", c.Detail, c.Remediation)
		}
	}
	if !sawUsage || !sawDML || !sawANN {
		t.Fatalf("missing pass verdicts (usage=%v dml=%v ann=%v)", sawUsage, sawDML, sawANN)
	}
}

func TestDeepVectorSchemaMissingUsageSagaClass(t *testing.T) {
	sc := healthyScenario()
	sc.schemaUsage = false // the exact Railway failure class
	setFakeScenario("no-usage", sc)

	deepDBOpener = fakeOpenerFor(t)
	t.Cleanup(func() { deepDBOpener = nil })

	cfg := config.DefaultConfig()
	cfg.Vector.Pgvector.DSN = fakeDSN("runner", "no-usage")
	cfg.Vector.Pgvector.Dimension = 1536

	var found bool
	for _, c := range probeDeepVectorSchema(context.Background(), cfg) {
		if strings.Contains(c.Detail, "LACKS USAGE") {
			found = true
			if c.Status != deepFail {
				t.Fatalf("missing USAGE must FAIL, got %s", c.Status)
			}
			if !strings.Contains(c.Remediation, "GRANT USAGE") {
				t.Fatalf("remediation must name the grant: %q", c.Remediation)
			}
		}
	}
	if !found {
		t.Fatal("no USAGE verdict emitted")
	}
}

func TestDeepVectorSchemaDimensionMismatch(t *testing.T) {
	sc := healthyScenario()
	sc.columnType = "vector(1024)"
	setFakeScenario("dim-mismatch", sc)

	deepDBOpener = fakeOpenerFor(t)
	t.Cleanup(func() { deepDBOpener = nil })

	cfg := config.DefaultConfig()
	cfg.Vector.Pgvector.DSN = fakeDSN("runner", "dim-mismatch")
	cfg.Vector.Pgvector.Dimension = 1536

	var found bool
	for _, c := range probeDeepVectorSchema(context.Background(), cfg) {
		if strings.Contains(c.Detail, "dimension mismatch") {
			found = true
			if c.Status != deepFail {
				t.Fatalf("dimension mismatch must FAIL, got %s", c.Status)
			}
		}
	}
	if !found {
		t.Fatal("no dimension mismatch verdict emitted")
	}
}

func TestDeepVectorSchemaMissingANNIndexWarns(t *testing.T) {
	sc := healthyScenario()
	sc.indexes = nil
	setFakeScenario("no-ann", sc)

	deepDBOpener = fakeOpenerFor(t)
	t.Cleanup(func() { deepDBOpener = nil })

	cfg := config.DefaultConfig()
	cfg.Vector.Pgvector.DSN = fakeDSN("runner", "no-ann")

	var found bool
	for _, c := range probeDeepVectorSchema(context.Background(), cfg) {
		if strings.Contains(c.Detail, "no HNSW/IVFFlat index") {
			found = c.Status == deepWarn
		}
	}
	if !found {
		t.Fatal("missing ANN index must WARN")
	}
}

func TestDeepVectorSchemaBlockedAndInfo(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Vector.Pgvector.DSN = ""

	// No DSN → INFO (adapter not in use).
	for _, c := range probeDeepVectorSchema(context.Background(), cfg) {
		if c.Status != deepInfo {
			t.Fatalf("no-DSN must be INFO, got %s", c.Status)
		}
	}

	// DSN set but no opener injected (stripped local build) → BLOCKED.
	cfg.Vector.Pgvector.DSN = fakeDSN("runner", "anything")
	saved := deepDBOpener
	deepDBOpener = nil
	t.Cleanup(func() { deepDBOpener = saved })
	for _, c := range probeDeepVectorSchema(context.Background(), cfg) {
		if c.Status != deepBlocked {
			t.Fatalf("no-opener must be BLOCKED, got %s (%s)", c.Status, c.Detail)
		}
		if !strings.Contains(c.Remediation, "make build") {
			t.Fatalf("BLOCKED remediation must point at the build: %q", c.Remediation)
		}
	}
}

func TestDeepVectorSchemaConnectFailure(t *testing.T) {
	sc := healthyScenario()
	sc.connectErr = errors.New("pq: password authentication failed")
	setFakeScenario("bad-conn", sc)

	deepDBOpener = fakeOpenerFor(t)
	t.Cleanup(func() { deepDBOpener = nil })

	cfg := config.DefaultConfig()
	cfg.Vector.Pgvector.DSN = fakeDSN("runner", "bad-conn")

	checks := probeDeepVectorSchema(context.Background(), cfg)
	if len(checks) != 1 || checks[0].Status != deepFail {
		t.Fatalf("connect failure must produce a single FAIL, got %+v", checks)
	}
	// DSN must never surface.
	if strings.Contains(checks[0].Detail+checks[0].Remediation, "postgres://") {
		t.Fatalf("DSN leaked into output: %+v", checks[0])
	}
}

// --- storage roles probe -----------------------------------------------------------

func TestDeepStorageRolesDistinctAndHealthy(t *testing.T) {
	head, headSum := embeddedHead(t)
	setFakeScenario("roles-rt", healthyScenario())
	mg := healthyScenario()
	mg.ledgerHead = head
	mg.ledgerSum = headSum
	setFakeScenario("roles-mg", mg)

	deepDBOpener = fakeOpenerFor(t)
	t.Cleanup(func() { deepDBOpener = nil })

	cfg := config.DefaultConfig()
	cfg.Vector.Pgvector.DSN = fakeDSN("runner", "roles-rt")
	cfg.Vector.Pgvector.MigrationDSN = fakeDSN("migrator", "roles-mg")

	var sawDistinct, sawLedger bool
	for _, c := range probeDeepStorageRoles(context.Background(), cfg) {
		switch {
		case strings.Contains(c.Detail, "roles are distinct"):
			sawDistinct = c.Status == deepPass
		case strings.Contains(c.Detail, "ledger head"):
			sawLedger = c.Status == deepPass
		}
		if c.Status == deepFail {
			t.Fatalf("healthy distinct-role scenario must not FAIL: %s — %s", c.Detail, c.Remediation)
		}
	}
	if !sawDistinct {
		t.Fatal("missing role-distinctness PASS")
	}
	if !sawLedger {
		t.Fatal("missing ledger-head PASS")
	}
}

func TestDeepStorageRolesIdenticalRoleFails(t *testing.T) {
	head, headSum := embeddedHead(t)
	sc := healthyScenario()
	sc.ledgerHead = head
	sc.ledgerSum = headSum
	setFakeScenario("same", sc)

	deepDBOpener = fakeOpenerFor(t)
	t.Cleanup(func() { deepDBOpener = nil })

	cfg := config.DefaultConfig()
	cfg.Vector.Pgvector.DSN = fakeDSN("shared", "same")
	cfg.Vector.Pgvector.MigrationDSN = fakeDSN("shared", "same")

	var found bool
	for _, c := range probeDeepStorageRoles(context.Background(), cfg) {
		if strings.Contains(c.Detail, "IDENTICAL") {
			found = true
			if c.Status != deepFail {
				t.Fatalf("identical roles must FAIL, got %s", c.Status)
			}
			if !strings.Contains(c.Remediation, "role") {
				t.Fatalf("remediation must reference role separation: %q", c.Remediation)
			}
		}
	}
	if !found {
		t.Fatal("no identical-role verdict emitted")
	}
}

func TestDSNRoleParsing(t *testing.T) {
	tests := []struct {
		dsn  string
		want string
	}{
		{"postgres://runner:secret@db.internal:5432/cortex", "runner"},
		{"postgresql://migrator@db/cortex?sslmode=require", "migrator"},
		{"host=db user=runtime_role password=x dbname=cortex", "runtime_role"},
		{"host=db dbname=cortex", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := dsnRole(tt.dsn); got != tt.want {
			t.Errorf("dsnRole(%q) = %q, want %q", tt.dsn, got, tt.want)
		}
	}
}

func TestParseVectorTypmod(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"vector(1536)", 1536},
		{"vector(1024)", 1024},
		{"vector", 0},
		{"halfvec(2048)", 2048},
		{"vector()", 0},
	}
	for _, tt := range tests {
		if got := parseVectorTypmod(tt.in); got != tt.want {
			t.Errorf("parseVectorTypmod(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestDeepStorageRolesNoMigrationDSNIsInfo(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Vector.Pgvector.DSN = "postgres://runner@h/db"
	cfg.Vector.Pgvector.MigrationDSN = ""
	for _, c := range probeDeepStorageRoles(context.Background(), cfg) {
		if c.Status != deepInfo {
			t.Fatalf("single-role setup must be INFO, got %s", c.Status)
		}
	}
}

func TestDeepStorageRolesLedgerAheadIsTamperClass(t *testing.T) {
	sc := healthyScenario()
	sc.ledgerHead = 99999 // beyond any plausible embedded head
	sc.ledgerSum = "x"
	setFakeScenario("ahead", sc)

	deepDBOpener = fakeOpenerFor(t)
	t.Cleanup(func() { deepDBOpener = nil })

	cfg := config.DefaultConfig()
	cfg.Vector.Pgvector.DSN = fakeDSN("migrator", "ahead")
	cfg.Vector.Pgvector.MigrationDSN = fakeDSN("migrator", "ahead")

	var found bool
	for _, c := range probeDeepStorageRoles(context.Background(), cfg) {
		if strings.Contains(c.Detail, "BEYOND") {
			found = true
			if c.Status != deepFail {
				t.Fatalf("ledger ahead must FAIL (tamper class), got %s", c.Status)
			}
		}
	}
	if !found {
		t.Fatal("no ledger-ahead verdict emitted")
	}
}

// --- end-to-end CLI behavior ---------------------------------------------------------

func TestDoctorDeepOfflineExitZero(t *testing.T) {
	// Fresh HOME, no providers, no DSNs, no opener: every check reports
	// INFO/BLOCKED and the run must exit 0 — the documented "correct
	// behavior" for a client without prod DSNs.
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, env := range []string{
		"CORTEX_EMBEDDING_PROVIDER", "CORTEX_RERANK_PROVIDER", "CORTEX_LLM_PROVIDER",
		"CORTEX_AI_PROVIDER", "CORTEX_WEB_KEY",
		"CORTEX_VECTOR_PGVECTOR_DSN", "CORTEX_VECTOR_PGVECTOR_MIGRATION_DSN",
	} {
		t.Setenv(env, "")
	}
	saved := deepDBOpener
	deepDBOpener = nil
	t.Cleanup(func() { deepDBOpener = saved })

	var stdout, stderr strings.Builder
	code := Run([]string{"cortex", "doctor", "--deep"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q\nstdout:\n%s", code, stderr.String(), stdout.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"Cortex Doctor — Deep Diagnostics",
		"[INFO] Embedding provider round-trip",
		"[INFO] Vector schema health (pgvector)",
		"[INFO] Rerank contract probe",
		"[INFO] LLM provider probe",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestDoctorShallowUnchangedByDeepFlag(t *testing.T) {
	// Shallow doctor must not print deep sections when --deep is absent.
	home := t.TempDir()
	t.Setenv("HOME", home)
	var stdout, stderr strings.Builder
	code := Run([]string{"cortex", "doctor"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("shallow doctor exit = %d, stderr = %q", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "Deep Diagnostics") {
		t.Fatalf("shallow doctor leaked deep output:\n%s", stdout.String())
	}
}
