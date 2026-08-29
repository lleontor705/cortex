package server

import (
	"context"
	"crypto/x509"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/domain"
)

type closeCountingVector struct {
	mu    sync.Mutex
	calls int
}

func (v *closeCountingVector) ID() string                                         { return "test" }
func (v *closeCountingVector) Upsert(context.Context, []domain.VectorPoint) error { return nil }
func (v *closeCountingVector) Search(context.Context, domain.VectorQuery) ([]domain.VectorCandidate, error) {
	return nil, nil
}
func (v *closeCountingVector) Delete(context.Context, []int64) error { return nil }
func (v *closeCountingVector) Health(context.Context) domain.Health  { return domain.Health{} }
func (v *closeCountingVector) Capabilities(context.Context) (domain.Capabilities, error) {
	return domain.Capabilities{}, nil
}

func (v *closeCountingVector) Close() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.calls++
	return errors.New("vector close")
}

type closeCountingEmbedding struct {
	mu    sync.Mutex
	calls int
}

func (e *closeCountingEmbedding) Embed(context.Context, string) ([]float32, error) { return nil, nil }
func (e *closeCountingEmbedding) Dimensions() int                                  { return 0 }
func (e *closeCountingEmbedding) Model() string                                    { return "test" }

func (e *closeCountingEmbedding) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	return errors.New("embedding close")
}

func TestOpenFailsFastWithoutPostgresDSN(t *testing.T) {
	cfg := config.Config{Server: config.ServerConfig{Storage: config.ServerStorageConfig{Driver: "postgres"}}}
	_, err := Open(context.Background(), cfg)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "dsn") {
		t.Fatalf("Open() error = %v, want fail-fast DSN error", err)
	}
}

func TestServerConfigStringRedactsSecrets(t *testing.T) {
	cfg := config.Config{Server: config.ServerConfig{
		Storage: config.ServerStorageConfig{DSN: "postgres://user:secret@db/cortex"},
		Secrets: config.ServerSecretsConfig{SigningKey: "secret-key"},
	}}
	text := cfg.String()
	if strings.Contains(text, "secret") {
		t.Fatalf("Config.String leaked server secret: %q", text)
	}
}

func TestRuntimeCloseIsIdempotentAndConcurrencySafe(t *testing.T) {
	vector := &closeCountingVector{}
	embedding := &closeCountingEmbedding{}
	rt := &Runtime{Vectors: vector, Embeddings: embedding}

	const callers = 32
	var wg sync.WaitGroup
	results := make(chan error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- rt.Close()
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err == nil || !strings.Contains(err.Error(), "vector close") {
			t.Fatalf("Close() error = %v, want stored first cleanup error", err)
		}
	}
	if vector.calls != 1 {
		t.Fatalf("vector Close calls = %d, want 1", vector.calls)
	}
	if embedding.calls != 1 {
		t.Fatalf("embedding Close calls = %d, want 1", embedding.calls)
	}
}

// validBootstrapConfig is a minimal production server configuration that
// satisfies every durable-bootstrap validation rule. The configured bearer is
// a sentinel, never a real credential.
func validBootstrapConfig() config.Config {
	return config.Config{
		Server: config.ServerConfig{
			Storage:                 config.ServerStorageConfig{Driver: "postgres", DSN: "postgres://cortex_app@db/cortex", MigrationDSN: "postgres://cortex_migration@db/cortex"},
			TenantID:                "00000000-0000-0000-0000-000000000001",
			WorkspaceID:             "00000000-0000-0000-0000-000000000002",
			PrincipalSubject:        "00000000-0000-0000-0000-000000000003",
			Roles:                   []string{"owner"},
			Scopes:                  []string{"workspaces:read"},
			ProjectIDs:              []string{"*"},
			ClassificationClearance: []string{"*"},
		},
		HTTP: config.HTTPConfig{Enabled: true, Host: "127.0.0.1", Port: 7438, Token: "configured-bootstrap-bearer"},
	}
}

func TestValidateConfigEnforcesProductionDatabaseRoleBoundary(t *testing.T) {
	tests := []struct {
		name         string
		runtimeDSN   string
		migrationDSN string
		want         string
	}{
		{name: "missing migration DSN", runtimeDSN: "postgres://cortex_app:runtime-secret@db/cortex", want: "migration DSN is required"},
		{name: "same URL role", runtimeDSN: "postgres://shared:runtime-secret@db/cortex", migrationDSN: "postgres://shared:migration-secret@db/cortex", want: "distinct PostgreSQL roles"},
		{name: "same keyword role", runtimeDSN: "host=db dbname=cortex user=shared password=runtime-secret", migrationDSN: "host=db dbname=cortex user=shared password=migration-secret", want: "distinct PostgreSQL roles"},
		{name: "invalid runtime DSN", runtimeDSN: "postgres://cortex_app:runtime-secret@%zz/cortex", migrationDSN: "postgres://cortex_migration:migration-secret@db/cortex", want: "runtime DSN is invalid"},
		{name: "invalid migration DSN", runtimeDSN: "postgres://cortex_app:runtime-secret@db/cortex", migrationDSN: "postgres://cortex_migration:migration-secret@%zz/cortex", want: "migration DSN is invalid"},
		{name: "distinct roles", runtimeDSN: "postgres://cortex_app:runtime-secret@db/cortex", migrationDSN: "postgres://cortex_migration:migration-secret@db/cortex"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validBootstrapConfig()
			cfg.Server.Storage.DSN = tt.runtimeDSN
			cfg.Server.Storage.MigrationDSN = tt.migrationDSN
			err := validateConfig(cfg)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("validateConfig() = %v, want accepted role boundary", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validateConfig() = %v, want %q", err, tt.want)
			}
			for _, secret := range []string{"runtime-secret", "migration-secret"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("validateConfig() leaked %q: %v", secret, err)
				}
			}
		})
	}
}

func TestResolveServerDSNsAllowsDevelopmentFallback(t *testing.T) {
	cfg := validBootstrapConfig()
	cfg.Server.BootstrapDevelopment = true
	cfg.Server.Storage.MigrationDSN = ""

	runtimeDSN, migrationDSN, err := resolveServerDSNs(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if runtimeDSN != cfg.Server.Storage.DSN || migrationDSN != runtimeDSN {
		t.Fatalf("resolved DSNs do not preserve development fallback")
	}
}

func TestNewServerEmbeddingFailsClosedForUnsafeDestination(t *testing.T) {
	cfg := validBootstrapConfig()
	cfg.Search.EmbeddingProvider = "ollama"
	cfg.Search.EmbeddingBaseURL = "http://169.254.169.254"
	if _, err := newServerEmbedding(cfg); err == nil {
		t.Fatal("unsafe embedding destination accepted")
	}
}

func TestResolveServerDSNsPreservesExplicitDevelopmentMigrationDSN(t *testing.T) {
	cfg := validBootstrapConfig()
	cfg.Server.BootstrapDevelopment = true
	want := "postgres://development_migration:migration-secret@db/cortex"
	cfg.Server.Storage.MigrationDSN = want

	_, migrationDSN, err := resolveServerDSNs(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if migrationDSN != want {
		t.Fatal("explicit development migration DSN was not preserved")
	}
}

func TestValidateConfigRejectsInvalidServerInputs(t *testing.T) {
	base := validBootstrapConfig()
	cases := []struct {
		name   string
		mutate func(*config.Config)
		want   string
	}{
		{"driver", func(c *config.Config) { c.Server.Storage.Driver = "sqlite" }, "driver"},
		{"dsn", func(c *config.Config) { c.Server.Storage.DSN = "" }, "DSN"},
		{"tenant", func(c *config.Config) { c.Server.TenantID = "" }, "tenant_id"},
		{"tenant uuid", func(c *config.Config) { c.Server.TenantID = "bad" }, "tenant_id"},
		{"workspace uuid", func(c *config.Config) { c.Server.WorkspaceID = "bad" }, "workspace_id"},
		{"principal subject uuid", func(c *config.Config) { c.Server.PrincipalSubject = "service" }, "principal_subject"},
		{"roles without owner or admin", func(c *config.Config) { c.Server.Roles = []string{"viewer"} }, "owner or admin"},
		{"empty roles", func(c *config.Config) { c.Server.Roles = nil }, "owner or admin"},
		{"bearer too short", func(c *config.Config) { c.HTTP.Token = "short" }, "token"},
		{"bearer missing", func(c *config.Config) { c.HTTP.Token = "" }, "token"},
		{"cors wildcard", func(c *config.Config) { c.HTTP.AllowedOrigins = []string{"*"} }, "allowed_origins"},
		{"cors path", func(c *config.Config) { c.HTTP.AllowedOrigins = []string{"https://console.example/path"} }, "allowed_origins"},
		{"cors query", func(c *config.Config) { c.HTTP.AllowedOrigins = []string{"https://console.example?x=1"} }, "allowed_origins"},
		{"cors fragment", func(c *config.Config) { c.HTTP.AllowedOrigins = []string{"https://console.example#x"} }, "allowed_origins"},
		{"cors userinfo", func(c *config.Config) { c.HTTP.AllowedOrigins = []string{"https://user@console.example"} }, "allowed_origins"},
		{"cors scheme", func(c *config.Config) { c.HTTP.AllowedOrigins = []string{"file://console.example"} }, "allowed_origins"},
		{"uppercase workspace uuid accepted", func(c *config.Config) {
			c.Server.WorkspaceID = strings.ToUpper("10000000-a000-0000-0000-000000000002")
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base
			tc.mutate(&c)
			err := validateConfig(c)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("error=%v, want accepted config", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v want %q", err, tc.want)
			}
			if tc.want == "token" && c.HTTP.Token != "" && strings.Contains(err.Error(), c.HTTP.Token) {
				t.Fatalf("token requirement error echoes bearer %q", err)
			}
		})
	}
}

// TestValidateConfigNoLongerRequiresConfiguredGrantDigest pins the
// deprecation of the configured grant_digest/grant_version compatibility
// inputs: durable bootstrap replaces them as the authority for composition.
func TestValidateConfigNoLongerRequiresConfiguredGrantDigest(t *testing.T) {
	cfg := validBootstrapConfig()
	cfg.Server.GrantDigest = ""
	cfg.Server.GrantVersion = 0
	if err := validateConfig(cfg); err != nil {
		t.Fatalf("validateConfig() = %v, want configured grant digest/version ignored", err)
	}
}

func TestCanonicalBootstrapGrants(t *testing.T) {
	dev := validBootstrapConfig()
	dev.Server.BootstrapDevelopment = true
	dev.Server.Roles = []string{"viewer"}
	dev.Server.Scopes = nil
	dev.Server.ProjectIDs = nil
	dev.Server.ClassificationClearance = nil
	got, err := canonicalBootstrapGrants(dev)
	if err != nil {
		t.Fatalf("development grants error = %v", err)
	}
	want := []bootstrapGrant{
		{Type: "role", Value: "owner"},
		{Type: "workspace", Value: "00000000-0000-0000-0000-000000000002"},
		{Type: "scope", Value: "workspaces:read"},
		{Type: "project", Value: "*"},
		{Type: "classification", Value: "*"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("development grants = %+v, want %+v", got, want)
	}

	prod := validBootstrapConfig()
	prod.Server.Roles = []string{"owner", "admin", "owner", " admin ", ""}
	prod.Server.Scopes = []string{"workspaces:read", "workspaces:read", ""}
	prod.Server.ProjectIDs = []string{"*", "*"}
	prod.Server.WorkspaceID = "00000000-0000-0000-0000-000000000002"
	got, err = canonicalBootstrapGrants(prod)
	if err != nil {
		t.Fatalf("production grants error = %v", err)
	}
	want = []bootstrapGrant{
		{Type: "role", Value: "owner"},
		{Type: "role", Value: "admin"},
		{Type: "workspace", Value: "00000000-0000-0000-0000-000000000002"},
		{Type: "scope", Value: "workspaces:read"},
		{Type: "project", Value: "*"},
		{Type: "classification", Value: "*"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("production grants = %+v, want %+v", got, want)
	}

	noOwner := validBootstrapConfig()
	noOwner.Server.Roles = []string{"viewer"}
	if _, err := canonicalBootstrapGrants(noOwner); err == nil || !strings.Contains(err.Error(), "owner or admin") {
		t.Fatalf("missing owner/admin error = %v", err)
	}

	uppercase := validBootstrapConfig()
	uppercase.Server.WorkspaceID = strings.ToUpper("10000000-a000-0000-0000-000000000002")
	got, err = canonicalBootstrapGrants(uppercase)
	if err != nil {
		t.Fatalf("uppercase workspace error = %v", err)
	}
	var workspaceGrant string
	for _, grant := range got {
		if grant.Type == "workspace" {
			workspaceGrant = grant.Value
		}
	}
	if workspaceGrant != "10000000-a000-0000-0000-000000000002" {
		t.Fatalf("workspace grant = %q, want canonical lowercase uuid spelling", workspaceGrant)
	}
}

// --- migration-role bootstrap reconciler stub ---------------------------------
//
// The stub records the single SQL statement bootstrapServicePrincipal must
// issue through the still-privileged migration handle and returns one canned
// reconciler row, so the nine-argument contract is pinned without PostgreSQL.

type bootstrapStubCall struct {
	query string
	args  []driver.NamedValue
}

type bootstrapStubDB struct {
	mu    sync.Mutex
	calls []bootstrapStubCall
	err   error
	row   []driver.Value
}

func (s *bootstrapStubDB) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare unsupported")
}
func (s *bootstrapStubDB) Close() error { return nil }
func (s *bootstrapStubDB) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions unsupported")
}

func (s *bootstrapStubDB) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, bootstrapStubCall{query: query, args: append([]driver.NamedValue(nil), args...)})
	if s.err != nil {
		return nil, s.err
	}
	return &bootstrapStubRows{row: s.row}, nil
}

type bootstrapStubRows struct {
	row   []driver.Value
	drain bool
}

func (r *bootstrapStubRows) Columns() []string {
	return []string{"token_public_id", "grant_version", "bootstrap_action"}
}
func (r *bootstrapStubRows) Close() error { return nil }
func (r *bootstrapStubRows) Next(dest []driver.Value) error {
	if r.drain {
		return io.EOF
	}
	r.drain = true
	copy(dest, r.row)
	return nil
}

type bootstrapStubConnector struct{ db *bootstrapStubDB }

func (c *bootstrapStubConnector) Connect(context.Context) (driver.Conn, error) { return c.db, nil }
func (c *bootstrapStubConnector) Driver() driver.Driver                        { return nil }

func TestBootstrapServicePrincipalInvokesMigrationReconciler(t *testing.T) {
	stub := &bootstrapStubDB{row: []driver.Value{"00000000-0000-0000-0000-00000000000a", int64(1), "provisioned"}}
	db := sql.OpenDB(&bootstrapStubConnector{db: stub})
	cfg := validBootstrapConfig()

	if err := bootstrapServicePrincipal(context.Background(), db, cfg); err != nil {
		t.Fatalf("bootstrapServicePrincipal() = %v", err)
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.calls) != 1 {
		t.Fatalf("reconciler invocations = %d, want exactly 1", len(stub.calls))
	}
	call := stub.calls[0]
	if !strings.Contains(call.query, "public.cortex_bootstrap_service_principal") {
		t.Fatalf("query does not target the reconciler: %q", call.query)
	}
	if len(call.args) != 9 {
		t.Fatalf("argument count = %d, want 9", len(call.args))
	}
	wantArgs := []string{
		"00000000-0000-0000-0000-000000000001",
		"00000000-0000-0000-0000-000000000002",
		"00000000-0000-0000-0000-000000000003",
		bootstrapActorSubject,
		bootstrapServiceName,
		"", // grants JSON, asserted below
		bootstrapTokenName,
		"configured-bootstrap-bearer",
		bootstrapAuditReason,
	}
	for i, want := range wantArgs {
		if i == 5 {
			continue
		}
		if got, ok := call.args[i].Value.(string); !ok || got != want {
			t.Fatalf("argument %d = %v, want %q", i, call.args[i].Value, want)
		}
	}
	payload, ok := call.args[5].Value.(string)
	if !ok {
		t.Fatalf("grants argument = %T, want JSON string", call.args[5].Value)
	}
	var grants []bootstrapGrant
	if err := json.Unmarshal([]byte(payload), &grants); err != nil {
		t.Fatalf("grants payload is not JSON: %v", err)
	}
	wantGrants := []bootstrapGrant{
		{Type: "role", Value: "owner"},
		{Type: "workspace", Value: "00000000-0000-0000-0000-000000000002"},
		{Type: "scope", Value: "workspaces:read"},
		{Type: "project", Value: "*"},
		{Type: "classification", Value: "*"},
	}
	if !reflect.DeepEqual(grants, wantGrants) {
		t.Fatalf("grants payload = %+v, want %+v", grants, wantGrants)
	}
	if strings.Contains(payload, cfg.HTTP.Token) {
		t.Fatalf("grants payload exposes the configured bearer")
	}
}

func TestBootstrapServicePrincipalRejectsUnknownReconcilerAction(t *testing.T) {
	cfg := validBootstrapConfig()
	// A hostile or corrupted database value must never be interpolated into a
	// startup error: that column is untrusted text and could carry a sentinel
	// bearer or provenance payload straight past stage redaction.
	hostileAction := "suspicious-" + cfg.HTTP.Token
	stub := &bootstrapStubDB{row: []driver.Value{"00000000-0000-0000-0000-00000000000a", int64(1), hostileAction}}
	db := sql.OpenDB(&bootstrapStubConnector{db: stub})
	err := bootstrapServicePrincipal(context.Background(), db, cfg)
	if err == nil || !strings.Contains(err.Error(), "unknown bootstrap action") {
		t.Fatalf("unknown action error = %v", err)
	}
	if strings.Contains(err.Error(), hostileAction) {
		t.Fatalf("unknown action error echoes the raw database value: %v", err)
	}
	if strings.Contains(err.Error(), cfg.HTTP.Token) {
		t.Fatalf("unknown action error echoes the configured bearer: %v", err)
	}
}

// TestValidateConfigRejectsNonCanonicalBearer pins the shared canonical
// bearer contract (IDP-T03B review blocker): request secrets are verified
// byte-exact, so a configured bearer with surrounding or control whitespace
// could never authenticate an HTTP request. Such startup credentials must be
// rejected before bootstrap persists them.
func TestValidateConfigRejectsNonCanonicalBearer(t *testing.T) {
	cases := []struct {
		name  string
		token string
	}{
		{"leading whitespace", "   configured-bootstrap-bearer"},
		{"trailing whitespace", "configured-bootstrap-bearer   "},
		{"surrounding tab and newline", "\tconfigured-bootstrap-bearer\n"},
		{"embedded control character", "configured\x00-bootstrap-bearer"},
		{"embedded newline", "configured\nbootstrap-bearer"},
		{"whitespace padding around short core", "   short-token   "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validBootstrapConfig()
			cfg.HTTP.Token = tc.token
			err := validateConfig(cfg)
			if err == nil || !strings.Contains(err.Error(), "token") {
				t.Fatalf("validateConfig(%s) = %v, want token rejection", tc.name, err)
			}
			if strings.Contains(err.Error(), strings.TrimSpace(tc.token)) {
				t.Fatalf("rejection error echoes the bearer: %v", err)
			}
		})
	}
}

func TestBootstrapServicePrincipalErrorsNeverExposeBearer(t *testing.T) {
	const bearer = "secret-configured-bearer-value"
	cfg := validBootstrapConfig()
	cfg.HTTP.Token = bearer
	db := sql.OpenDB(&bootstrapStubConnector{db: &bootstrapStubDB{err: fmt.Errorf("reconciler failed for %s", bearer)}})
	err := bootstrapServicePrincipal(context.Background(), db, cfg)
	if err == nil {
		t.Fatal("reconciler failure must abort startup")
	}
	if strings.Contains(err.Error(), bearer) {
		t.Fatalf("stage error leaked bearer: %v", err)
	}
	if !strings.Contains(err.Error(), "bootstrap") {
		t.Fatalf("stage error lost its stage label: %v", err)
	}
}

func TestRedactStageError(t *testing.T) {
	const secret = "ctx-secret-bearer-1234"
	plain := errors.New("server: stage: ordinary failure")
	if got := redactStageError(plain, secret); got != plain {
		t.Fatalf("non-matching error rewritten: %v", got)
	}
	leaking := fmt.Errorf("server: stage: rejected %s at reconcile", secret)
	got := redactStageError(leaking, secret, "")
	if got == nil || got.Error() == leaking.Error() {
		t.Fatalf("secret-bearing error not redacted: %v", got)
	}
	if strings.Contains(got.Error(), secret) || !strings.Contains(got.Error(), "[REDACTED]") {
		t.Fatalf("redacted error = %v", got)
	}
	if redactStageError(nil, secret) != nil {
		t.Fatal("nil error must stay nil")
	}
}

func TestEmbeddingDimensions(t *testing.T) {
	for provider, want := range map[string]int{"openai": 1536, "ollama": 768, "none": 0} {
		if got := embeddingDimensions(provider); got != want {
			t.Errorf("%s=%d want %d", provider, got, want)
		}
	}
}

func TestServeStopsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	rt := &Runtime{httpServer: &http.Server{Addr: "127.0.0.1:0", Handler: http.NewServeMux()}}
	done := make(chan error, 1)
	go func() { done <- rt.Serve(ctx) }()
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("Close() after Serve = %v", err)
	}
}

// TestServerSSRFProductionCompositionFromAdminConfig pins the production
// wiring path (SEC-02): newConfiguredExtractor � the same builder Open uses
// for administrator-owned CORTEX_LLM_* configuration � composes the injected
// extraction service, an approved provider succeeds end-to-end through the
// authenticated API, a destination port that is not the configured one is
// refused before transmission, and request-supplied llm_config credentials
// stay rejected under a configured provider.
func TestServerSSRFProductionCompositionFromAdminConfig(t *testing.T) {
	const llmBody = `{"choices":[{"message":{"content":"{\"observations\":[{\"title\":\"admin llm decision\",\"content\":\"from production composition\",\"type\":\"decision\",\"project\":\"cortex\",\"scope\":\"project\",\"confidence\":0.9,\"tags\":[]}],\"edges\":[],\"summary\":\"composition marker\"}"}}]}`

	var hits int
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("Authorization") != "Bearer sk-admin-env-canary" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(llmBody))
	}))
	defer provider.Close()
	providerURL, err := url.Parse(provider.URL)
	if err != nil {
		t.Fatalf("parse provider url: %v", err)
	}
	providerPort, err := strconv.Atoi(providerURL.Port())
	if err != nil {
		t.Fatalf("parse provider port: %v", err)
	}
	caPool := x509.NewCertPool()
	caPool.AddCert(provider.Certificate())

	auth := requestAuthenticator{
		verifier: verifierFunc(func(_ context.Context, secret, _ string) (domain.Principal, error) {
			if secret != "test-token" {
				return domain.Principal{}, errors.New("unknown credential")
			}
			return domain.Principal{Subject: "00000000-0000-0000-0000-0000000000f1", OrgID: "00000000-0000-0000-0000-000000000001"}, nil
		}),
		factory: operationsFactoryFunc(func(context.Context, domain.Principal) (Operations, error) {
			return newFakeOperations(), nil
		}),
	}

	newHandler := func(llm config.ServerLLMConfig) http.Handler {
		h, _ := newHTTPHandlerWithAuth(
			config.Config{HTTP: config.HTTPConfig{Token: "test-token"}},
			requestOperations{},
			func(context.Context) error { return nil },
			auth.middleware,
			newConfiguredExtractor(llm),
		)
		return h
	}

	postExtract := func(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/extract", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	t.Run("approved provider succeeds through production seam", func(t *testing.T) {
		llm := config.ServerLLMConfig{
			Provider:      "generic",
			BaseURL:       provider.URL,
			APIKey:        "sk-admin-env-canary",
			Model:         "test-model",
			AllowLoopback: true, // explicit local-only development exception for the loopback test provider
			Timeout:       10 * time.Second,
			CACertPool:    caPool,
		}
		rec := postExtract(t, newHandler(llm), `{"text":"We decided to verify the production composition seam.","project":"cortex"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"source_method":"llm"`) {
			t.Fatalf("configured provider was not used: %s", rec.Body.String())
		}
		if hits == 0 {
			t.Fatal("provider never saw the request")
		}
	})

	t.Run("destination other than the configured one is never contacted", func(t *testing.T) {
		hitsBefore := hits
		llm := config.ServerLLMConfig{
			Provider:      "generic",
			BaseURL:       "https://127.0.0.1:1/v1", // configured destination nobody serves; the approved provider is on another port
			APIKey:        "sk-admin-env-canary",
			AllowLoopback: true,
			Timeout:       2 * time.Second,
			CACertPool:    caPool,
		}
		rec := postExtract(t, newHandler(llm), `{"text":"We decided to verify exact destination approval.","project":"cortex"}`)
		// The configured destination is unreachable: extraction must degrade
		// to the deterministic heuristic path, and the approved provider on
		// its own port must observe zero traffic.
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"source_method":"heuristic"`) {
			t.Fatalf("unreachable configured destination must fall back to heuristics: %s", rec.Body.String())
		}
		if hits != hitsBefore {
			t.Fatal("outbound request reached a destination other than the configured one")
		}
		_ = providerPort
	})

	t.Run("request llm_config stays rejected under configured provider", func(t *testing.T) {
		hitsBefore := hits
		llm := config.ServerLLMConfig{
			Provider:      "generic",
			BaseURL:       provider.URL,
			APIKey:        "sk-admin-env-canary",
			AllowLoopback: true,
			Timeout:       10 * time.Second,
			CACertPool:    caPool,
		}
		body := `{"text":"We decided request credentials stay rejected.","project":"cortex","llm_config":{"base_url":"http://127.0.0.1:9/v1","api_key":"sk-request-canary"}}`
		rec := postExtract(t, newHandler(llm), body)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"invalid_configuration"`) {
			t.Fatalf("status = %d body = %s, want 400 invalid_configuration", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "sk-request-canary") || strings.Contains(rec.Body.String(), "sk-admin-env-canary") {
			t.Fatalf("response leaks credentials: %s", rec.Body.String())
		}
		if hits != hitsBefore {
			t.Fatal("request-controlled destination was contacted")
		}
	})
}
