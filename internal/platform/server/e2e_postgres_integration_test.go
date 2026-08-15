//go:build postgres_integration

package server

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
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lleontor705/cortex/internal/authz"
	"github.com/lleontor705/cortex/internal/config"
	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/migration"
	postgresstore "github.com/lleontor705/cortex/internal/store/postgres"
	cortsync "github.com/lleontor705/cortex/internal/sync"
	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"
)

type postgresE2EFixture struct {
	admin     *pgxpool.Pool
	config    config.Config
	tenant    uuid.UUID
	workspace uuid.UUID
	subject   uuid.UUID
	token     string
}

func newPostgresE2EFixture(t *testing.T, port int) *postgresE2EFixture {
	t.Helper()
	appDSN := os.Getenv("CORTEX_TEST_POSTGRES_DSN")
	if appDSN == "" {
		t.Fatal("CORTEX_TEST_POSTGRES_DSN is required for postgres_integration tests")
	}
	migrationDSN := os.Getenv("CORTEX_TEST_POSTGRES_MIGRATION_DSN")
	if migrationDSN == "" {
		t.Fatal("CORTEX_TEST_POSTGRES_MIGRATION_DSN is required for postgres_integration tests")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	migrationDB, err := sql.Open("pgx", migrationDSN)
	if err != nil {
		t.Fatalf("open migration database: %v", err)
	}
	if err := migration.ApplyPostgresServerMigrations(ctx, migrationDB); err != nil {
		_ = migrationDB.Close()
		t.Fatalf("apply server migrations: %v", err)
	}
	if err := migrationDB.Close(); err != nil {
		t.Fatalf("close migration database: %v", err)
	}

	adminConfig, err := pgxpool.ParseConfig(migrationDSN)
	if err != nil {
		t.Fatalf("parse migration DSN: %v", err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatalf("open fixture pool: %v", err)
	}
	t.Cleanup(admin.Close)
	if err := admin.Ping(ctx); err != nil {
		t.Fatalf("ping fixture database: %v", err)
	}
	appConfig, err := pgxpool.ParseConfig(appDSN)
	if err != nil {
		t.Fatalf("parse application DSN: %v", err)
	}
	if _, err := admin.Exec(ctx, `GRANT cortex_app TO `+pgx.Identifier{appConfig.ConnConfig.User}.Sanitize()); err != nil {
		t.Fatalf("grant cortex_app to application login: %v", err)
	}

	tenant, workspace, subject := uuid.New(), uuid.New(), uuid.New()
	if _, err := admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, "e2e-"+tenant.String()); err != nil {
		t.Fatalf("create E2E organization: %v", err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO workspaces(tenant_id,organization_id,public_id,name) VALUES($1,(SELECT id FROM organizations WHERE tenant_id=$1),$2,$3)`, tenant, workspace, "e2e-"+workspace.String()); err != nil {
		t.Fatalf("create E2E workspace: %v", err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO actor_subjects(tenant_id,subject,actor_type,public_id,grant_digest,grant_version) VALUES($1,$2,'service_account',$3,'e2e-grant',1)`, tenant, subject.String(), subject); err != nil {
		t.Fatalf("create E2E actor: %v", err)
	}

	token := "e2e-" + uuid.NewString()
	return &postgresE2EFixture{
		admin:     admin,
		tenant:    tenant,
		workspace: workspace,
		subject:   subject,
		token:     token,
		config: config.Config{
			Server: config.ServerConfig{
				Storage:                 config.ServerStorageConfig{Driver: "postgres", DSN: appDSN, MigrationDSN: migrationDSN},
				Provider:                config.ServerProviderConfig{Embedding: "none", Vector: "none"},
				TenantID:                tenant.String(),
				WorkspaceID:             workspace.String(),
				PrincipalSubject:        subject.String(),
				GrantDigest:             "e2e-grant",
				GrantVersion:            1,
				Roles:                   []string{"owner"},
				Scopes:                  []string{"workspaces:read"},
				ProjectIDs:              []string{"*"},
				ClassificationClearance: []string{"*"},
			},
			HTTP:      config.HTTPConfig{Enabled: true, Host: "127.0.0.1", Port: port, Token: token},
			Search:    config.SearchConfig{EmbeddingProvider: "none", DefaultLimit: 20, MaxLimit: 100, FusionK: 60},
			Vector:    config.VectorConfig{Provider: "none"},
			Lifecycle: config.LifecycleConfig{EnableAutoArchive: false},
			Memory:    config.MemoryConfig{MaxObservationLength: 50000, AutoArchiveDays: 90, DecayHalfLifeDays: 30, MinArchiveScore: 0.1},
		},
	}
}

type runningE2EServer struct {
	cancel   context.CancelFunc
	runtime  *Runtime
	serveErr chan error
	stopOnce sync.Once
	stopErr  error
}

func startE2EServer(t *testing.T, cfg config.Config) *runningE2EServer {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	rt, err := Open(ctx, cfg)
	if err != nil {
		cancel()
		t.Fatalf("open E2E server: %v", err)
	}
	server := &runningE2EServer{cancel: cancel, runtime: rt, serveErr: make(chan error, 1)}
	go func() { server.serveErr <- rt.Serve(ctx) }()
	t.Cleanup(func() {
		if err := server.stop(); err != nil {
			t.Errorf("stop E2E server: %v", err)
		}
	})
	return server
}

func (s *runningE2EServer) stop() error {
	s.stopOnce.Do(func() {
		s.cancel()
		select {
		case err := <-s.serveErr:
			if err != nil {
				s.stopErr = err
			}
		case <-time.After(10 * time.Second):
			s.stopErr = errors.New("timed out waiting for E2E server shutdown")
		}
		if err := s.runtime.Close(); err != nil {
			s.stopErr = errors.Join(s.stopErr, err)
		}
	})
	return s.stopErr
}

func reserveLoopbackPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve loopback port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release loopback port: %v", err)
	}
	return port
}

func waitForHTTPStatus(url string, want int, timeout time.Duration) error {
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		response, err := client.Get(url)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == want {
				return nil
			}
			lastErr = fmt.Errorf("status %d", response.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("wait for %s: %w", url, lastErr)
}

func openE2ESQLite(t *testing.T, name string) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open E2E SQLite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	baseline, err := migration.NewV2Baseline()
	if err != nil {
		t.Fatalf("load SQLite baseline: %v", err)
	}
	if err := baseline.Apply(t.Context(), db); err != nil {
		t.Fatalf("apply SQLite baseline: %v", err)
	}
	return db
}

func TestE2ESQLiteAuthenticatedSyncRoundTrip(t *testing.T) {
	port := reserveLoopbackPort(t)
	fixture := newPostgresE2EFixture(t, port)
	server := startE2EServer(t, fixture.config)
	baseURL := "http://" + server.runtime.Address()
	if err := waitForHTTPStatus(baseURL+"/health", http.StatusOK, 10*time.Second); err != nil {
		t.Fatal(err)
	}

	httpClient := &http.Client{Timeout: 5 * time.Second}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, baseURL+"/api/sync/push", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := httpClient.Do(request)
	if err != nil {
		t.Fatalf("unauthenticated sync request: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated sync status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}

	source := openE2ESQLite(t, "source")
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := source.Exec(`INSERT INTO sessions(id,project,directory,started_at) VALUES('e2e-session','e2e-project','/e2e',?)`, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("seed source session: %v", err)
	}
	if _, err := source.Exec(`INSERT INTO observations(session_id,type,title,content,project,scope,confidence,source,created_at,updated_at) VALUES('e2e-session','decision','E2E sync','authenticated round trip','e2e-project','project',1,'test',?,?)`, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("seed source observation: %v", err)
	}

	sourceSyncer, err := cortsync.NewRemoteSyncer(source, baseURL, fixture.token, 10*time.Second)
	if err != nil {
		t.Fatalf("create source syncer: %v", err)
	}
	sourceResult, err := sourceSyncer.Sync(t.Context())
	if err != nil {
		t.Fatalf("sync source: %v", err)
	}
	if sourceResult.Pushed != 2 {
		t.Fatalf("source pushed = %d, want 2", sourceResult.Pushed)
	}

	var sourceSyncID string
	if err := source.QueryRow(`SELECT sync_id FROM observations WHERE title='E2E sync'`).Scan(&sourceSyncID); err != nil {
		t.Fatalf("read source sync ID: %v", err)
	}
	destination := openE2ESQLite(t, "destination")
	destinationSyncer, err := cortsync.NewRemoteSyncer(destination, baseURL, fixture.token, 10*time.Second)
	if err != nil {
		t.Fatalf("create destination syncer: %v", err)
	}
	destinationResult, err := destinationSyncer.Sync(t.Context())
	if err != nil {
		t.Fatalf("sync destination: %v", err)
	}
	if destinationResult.Pulled != 2 {
		t.Fatalf("destination pulled = %d, want 2", destinationResult.Pulled)
	}

	var destinationSyncID, sessionID, title, content, project string
	if err := destination.QueryRow(`SELECT sync_id,session_id,title,content,project FROM observations WHERE title='E2E sync'`).Scan(&destinationSyncID, &sessionID, &title, &content, &project); err != nil {
		t.Fatalf("read destination observation: %v", err)
	}
	if destinationSyncID != sourceSyncID || sessionID != "e2e-session" || title != "E2E sync" || content != "authenticated round trip" || project != "e2e-project" {
		t.Fatalf("destination observation = sync:%q session:%q title:%q content:%q project:%q", destinationSyncID, sessionID, title, content, project)
	}

	var sessionsBefore, observationsBefore, changesBefore int
	if err := fixture.admin.QueryRow(t.Context(), `SELECT count(*) FROM sessions WHERE tenant_id=$1`, fixture.tenant).Scan(&sessionsBefore); err != nil {
		t.Fatalf("count synchronized sessions before retry: %v", err)
	}
	if err := fixture.admin.QueryRow(t.Context(), `SELECT count(*) FROM observations WHERE tenant_id=$1`, fixture.tenant).Scan(&observationsBefore); err != nil {
		t.Fatalf("count synchronized observations before retry: %v", err)
	}
	if err := fixture.admin.QueryRow(t.Context(), `SELECT count(*) FROM sync_changes WHERE tenant_id=$1`, fixture.tenant).Scan(&changesBefore); err != nil {
		t.Fatalf("count sync changes before retry: %v", err)
	}
	retryResult, err := sourceSyncer.Sync(t.Context())
	if err != nil {
		t.Fatalf("repeat source sync: %v", err)
	}
	if retryResult.Cursor <= 0 {
		t.Fatalf("retry cursor = %d, want a persisted server cursor", retryResult.Cursor)
	}
	var sessionsAfter, observationsAfter, changesAfter, matchingObservations int
	if err := fixture.admin.QueryRow(t.Context(), `SELECT count(*) FROM sessions WHERE tenant_id=$1`, fixture.tenant).Scan(&sessionsAfter); err != nil {
		t.Fatalf("count synchronized sessions after retry: %v", err)
	}
	if err := fixture.admin.QueryRow(t.Context(), `SELECT count(*) FROM observations WHERE tenant_id=$1`, fixture.tenant).Scan(&observationsAfter); err != nil {
		t.Fatalf("count synchronized observations after retry: %v", err)
	}
	if err := fixture.admin.QueryRow(t.Context(), `SELECT count(*) FROM sync_changes WHERE tenant_id=$1`, fixture.tenant).Scan(&changesAfter); err != nil {
		t.Fatalf("count sync changes after retry: %v", err)
	}
	if err := fixture.admin.QueryRow(t.Context(), `SELECT count(*) FROM observations WHERE tenant_id=$1 AND client_id=$2`, fixture.tenant, sourceSyncID).Scan(&matchingObservations); err != nil {
		t.Fatalf("count synchronized observation identity: %v", err)
	}
	if sessionsAfter != sessionsBefore || observationsAfter != observationsBefore || changesAfter != changesBefore || matchingObservations != 1 {
		t.Fatalf("retry changed persisted state: sessions %d->%d observations %d->%d changes %d->%d matching=%d", sessionsBefore, sessionsAfter, observationsBefore, observationsAfter, changesBefore, changesAfter, matchingObservations)
	}
	if err := server.stop(); err != nil {
		t.Fatalf("stop E2E server: %v", err)
	}
}

func TestE2EServerProcessSmoke(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("server process smoke runs on the Linux PostgreSQL integration gate")
	}

	root := repositoryRoot(t)
	binaryPath := filepath.Join(t.TempDir(), "cortex-smoke")
	build := exec.Command("go", "build", "-tags", "cortex_vectors", "-o", binaryPath, "./cmd/cortex")
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build release-variant smoke binary: %v\n%s", err, output)
	}

	port := reserveLoopbackPort(t)
	fixture := newPostgresE2EFixture(t, port)
	configData, err := yaml.Marshal(struct {
		Server    config.ServerConfig    `yaml:"server"`
		HTTP      config.HTTPConfig      `yaml:"http"`
		Search    config.SearchConfig    `yaml:"search"`
		Memory    config.MemoryConfig    `yaml:"memory"`
		Lifecycle config.LifecycleConfig `yaml:"lifecycle"`
		Vector    config.VectorConfig    `yaml:"vector"`
	}{
		Server:    fixture.config.Server,
		HTTP:      fixture.config.HTTP,
		Search:    fixture.config.Search,
		Memory:    fixture.config.Memory,
		Lifecycle: fixture.config.Lifecycle,
		Vector:    fixture.config.Vector,
	})
	if err != nil {
		t.Fatalf("marshal smoke config: %v", err)
	}
	configPath := filepath.Join(t.TempDir(), "cortex.yaml")
	if err := os.WriteFile(configPath, configData, 0o600); err != nil {
		t.Fatalf("write smoke config: %v", err)
	}

	var stdout, stderr bytes.Buffer
	command := exec.Command(binaryPath, "--mode", "server", "--config", configPath)
	command.Dir = root
	command.Env = isolatedCortexEnvironment(t.TempDir())
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatalf("start smoke server: %v", err)
	}
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	if err := waitForHTTPStatus(baseURL+"/health", http.StatusOK, 15*time.Second); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		stopped = true
		t.Fatalf("smoke server did not become healthy: %v; stderr=%q", err, stderr.String())
	}

	client := &http.Client{Timeout: 5 * time.Second}
	request, err := http.NewRequest(http.MethodGet, baseURL+"/api/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("unauthenticated smoke request: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated smoke status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}

	request, err = http.NewRequest(http.MethodGet, baseURL+"/api/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+fixture.token)
	response, err = client.Do(request)
	if err != nil {
		t.Fatalf("authenticated smoke request: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("authenticated smoke status = %d, want %d", response.StatusCode, http.StatusOK)
	}

	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal smoke server: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		stopped = true
		if err != nil {
			t.Fatalf("smoke server exit: %v; stdout=%q stderr=%q", err, stdout.String(), stderr.String())
		}
	case <-time.After(10 * time.Second):
		_ = command.Process.Kill()
		<-done
		stopped = true
		t.Fatal("smoke server did not stop after SIGTERM")
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve E2E test location")
	}
	root := filepath.Dir(currentFile)
	for range 3 {
		root = filepath.Dir(root)
	}
	return root
}

func isolatedCortexEnvironment(home string) []string {
	environment := make([]string, 0, len(os.Environ())+2)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "CORTEX_") && !strings.HasPrefix(value, "HOME=") && !strings.HasPrefix(value, "USERPROFILE=") {
			environment = append(environment, value)
		}
	}
	return append(environment, "HOME="+home, "USERPROFILE="+home)
}

// --- R7: authorized compound handoff end-to-end (REM-AUTH-001, REM-MCP-001) ---

// mcpStreamClient is a minimal JSON-RPC client for the Streamable HTTP MCP
// transport used by the E2E suite.
type mcpStreamClient struct {
	baseURL string
	token   string
	session string
	client  *http.Client
}

func (c *mcpStreamClient) rpc(t *testing.T, method string, params any) json.RawMessage {
	t.Helper()
	envelope, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, c.baseURL+"/mcp", bytes.NewReader(envelope))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	if c.session != "" {
		request.Header.Set("Mcp-Session-Id", c.session)
	}
	response, err := c.client.Do(request)
	if err != nil {
		t.Fatalf("mcp %s: %v", method, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("mcp %s body: %v", method, err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("mcp %s: status %d body %s", method, response.StatusCode, body)
	}
	if session := response.Header.Get("Mcp-Session-Id"); session != "" {
		c.session = session
	}
	return jsonRPCResult(t, method, body)
}

func jsonRPCResult(t *testing.T, method string, body []byte) json.RawMessage {
	t.Helper()
	probe := struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}{}
	if err := json.Unmarshal(body, &probe); err == nil && (len(probe.Result) > 0 || probe.Error != nil) {
		if probe.Error != nil {
			t.Fatalf("mcp %s error: %s", method, probe.Error.Message)
		}
		return probe.Result
	}
	var last json.RawMessage
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if err := json.Unmarshal([]byte(payload), &probe); err != nil {
			continue
		}
		if probe.Error != nil {
			t.Fatalf("mcp %s error: %s", method, probe.Error.Message)
		}
		if len(probe.Result) > 0 {
			last = probe.Result
		}
	}
	if last == nil {
		t.Fatalf("mcp %s produced no JSON-RPC result: %s", method, body)
	}
	return last
}

type mcpToolCall struct {
	IsError           bool           `json:"isError"`
	StructuredContent map[string]any `json:"structuredContent"`
	Content           []struct {
		Text string `json:"text"`
	} `json:"content"`
}

func (c *mcpStreamClient) callTool(t *testing.T, name string, arguments map[string]any) mcpToolCall {
	t.Helper()
	raw := c.rpc(t, "tools/call", map[string]any{"name": name, "arguments": arguments})
	var call mcpToolCall
	if err := json.Unmarshal(raw, &call); err != nil {
		t.Fatalf("tools/call %s result: %v (%s)", name, err, raw)
	}
	return call
}

func (c *mcpStreamClient) structuredRef(t *testing.T, call mcpToolCall) (string, string) {
	t.Helper()
	if call.IsError {
		t.Fatalf("tool call failed: %+v", call)
	}
	payload := call.StructuredContent
	ref, ok := payload["observation_ref"].(map[string]any)
	if !ok {
		t.Fatalf("structuredContent = %#v, want observation_ref", payload)
	}
	publicID, _ := ref["public_id"].(string)
	status, _ := payload["status"].(string)
	if publicID == "" || status == "" {
		t.Fatalf("structuredContent = %#v, want public_id and status", payload)
	}
	if _, err := uuid.Parse(publicID); err != nil {
		t.Fatalf("public_id = %q, want UUID", publicID)
	}
	if _, hasLocal := ref["local_id"]; hasLocal {
		t.Fatalf("server namespace leaked local_id: %#v", ref)
	}
	return publicID, status
}

func (c *mcpStreamClient) structuredErrorCode(t *testing.T, call mcpToolCall) string {
	t.Helper()
	if !call.IsError {
		t.Fatalf("tool call unexpectedly succeeded: %+v", call)
	}
	payload := call.StructuredContent
	if _, hasRef := payload["observation_ref"]; hasRef {
		t.Fatalf("error result carries a reference: %#v", payload)
	}
	errBody, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("structuredContent = %#v, want error body", payload)
	}
	code, _ := errBody["code"].(string)
	if code == "" {
		t.Fatalf("error body = %#v, want stable code", errBody)
	}
	return code
}

func (f *postgresE2EFixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var count int
	if err := f.admin.QueryRow(t.Context(), query, args...).Scan(&count); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return count
}

// TestE2EServerAuthorizedHandoffEndToEnd drives cortex_save and cortex_handoff
// over the authenticated Streamable HTTP MCP transport and the AuthorizedStore
// matrix directly: structured public-id results, idempotent replay, conflict
// with zero effects, partial-permission fail-closed, and cross-tenant RLS
// invisibility (REM-AUTH-001, REM-HANDOFF-001/002, REM-MCP-001).
func TestE2EServerAuthorizedHandoffEndToEnd(t *testing.T) {
	port := reserveLoopbackPort(t)
	fixture := newPostgresE2EFixture(t, port)
	server := startE2EServer(t, fixture.config)
	baseURL := "http://" + server.runtime.Address()
	if err := waitForHTTPStatus(baseURL+"/health", http.StatusOK, 10*time.Second); err != nil {
		t.Fatal(err)
	}

	// Unauthenticated MCP calls are rejected before any operation runs.
	unauthenticated := &http.Client{Timeout: 5 * time.Second}
	request, _ := http.NewRequest(http.MethodPost, baseURL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	request.Header.Set("Accept", "application/json, text/event-stream")
	response, err := unauthenticated.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated tools/list status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}

	client := &mcpStreamClient{baseURL: baseURL, token: fixture.token, client: &http.Client{Timeout: 10 * time.Second}}
	client.rpc(t, "initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "e2e", "version": "1"}})
	list := client.rpc(t, "tools/list", map[string]any{})
	for _, name := range []string{"cortex_save", "cortex_handoff"} {
		if !strings.Contains(string(list), `"name":"`+name+`"`) {
			t.Fatalf("tools/list missing %s: %s", name, list)
		}
	}

	// Seed a session for the interactive save inside the fixture workspace.
	sessionPublic := uuid.NewString()
	if _, err := fixture.admin.Exec(t.Context(), `INSERT INTO sessions(tenant_id, workspace_id, public_id, project_key) VALUES($1,(SELECT id FROM workspaces WHERE tenant_id=$1 AND public_id=$2),$3,'e2e')`, fixture.tenant, fixture.workspace, sessionPublic); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	// cortex_save returns the exclusive public-namespace structured result.
	save := client.callTool(t, "cortex_save", map[string]any{
		"title": "E2E save", "content": "authorized save", "type": "decision",
		"session_id": sessionPublic, "project": "e2e",
	})
	saveID, saveStatus := client.structuredRef(t, save)
	if saveStatus != string(domain.WriteStatusCreated) {
		t.Fatalf("save status = %q, want created", saveStatus)
	}
	if got := fixture.count(t, `SELECT count(*) FROM observations WHERE tenant_id=$1 AND public_id=$2`, fixture.tenant, saveID); got != 1 {
		t.Fatalf("saved observation rows = %d, want 1", got)
	}

	handoffArgs := func(key, content string, relation map[string]any) map[string]any {
		args := map[string]any{
			"idempotency_key": key,
			"observation": map[string]any{
				"title": "E2E handoff", "content": content, "project": "e2e", "type": "decision",
			},
		}
		if relation != nil {
			args["relation"] = relation
		}
		return args
	}
	relation := map[string]any{
		"target":    map[string]any{"public_id": saveID},
		"type":      "references",
		"reasoning": "e2e compound authorization",
	}

	handoff := client.callTool(t, "cortex_handoff", handoffArgs("e2e-key-1", "durable handoff", relation))
	handoffID, handoffStatus := client.structuredRef(t, handoff)
	if handoffStatus != string(domain.WriteStatusCreated) {
		t.Fatalf("handoff status = %q, want created", handoffStatus)
	}
	if handoffID == saveID {
		t.Fatal("handoff reused the save observation instead of materializing its own")
	}
	if got := fixture.count(t, `SELECT count(*) FROM edges WHERE tenant_id=$1`, fixture.tenant); got != 1 {
		t.Fatalf("edges after handoff = %d, want 1", got)
	}
	if got := fixture.count(t, `SELECT count(*) FROM handoff_receipts WHERE tenant_id=$1 AND state='committed'`); got < 1 {
		t.Fatalf("committed receipts = %d, want at least 1", got)
	}
	var receiptScope string
	if err := fixture.admin.QueryRow(t.Context(), `SELECT scope FROM handoff_receipts WHERE tenant_id=$1 AND key='e2e-key-1'`, fixture.tenant).Scan(&receiptScope); err != nil {
		t.Fatalf("read receipt: %v", err)
	}
	if !strings.HasPrefix(receiptScope, "tenant/"+fixture.tenant.String()+"/workspace/"+fixture.workspace.String()) {
		t.Fatalf("receipt scope = %q, want principal-derived tenant/workspace scope", receiptScope)
	}

	// Identical replay returns the same reference with status replayed.
	replay := client.callTool(t, "cortex_handoff", handoffArgs("e2e-key-1", "durable handoff", relation))
	replayID, replayStatus := client.structuredRef(t, replay)
	if replayID != handoffID || replayStatus != string(domain.WriteStatusReplayed) {
		t.Fatalf("replay = %q/%q, want %q/replayed", replayID, replayStatus, handoffID)
	}

	observationsBefore := fixture.count(t, `SELECT count(*) FROM observations WHERE tenant_id=$1`, fixture.tenant)
	edgesBefore := fixture.count(t, `SELECT count(*) FROM edges WHERE tenant_id=$1`, fixture.tenant)
	receiptsBefore := fixture.count(t, `SELECT count(*) FROM handoff_receipts WHERE tenant_id=$1`, fixture.tenant)

	// Same key with a different payload conflicts without any effect.
	conflict := client.callTool(t, "cortex_handoff", handoffArgs("e2e-key-1", "mutated handoff", relation))
	if code := client.structuredErrorCode(t, conflict); code != string(domain.HandoffErrorConflict) {
		t.Fatalf("conflict code = %q, want %q", code, domain.HandoffErrorConflict)
	}
	if got := fixture.count(t, `SELECT count(*) FROM observations WHERE tenant_id=$1`, fixture.tenant); got != observationsBefore {
		t.Fatalf("conflict mutated observations: %d -> %d", observationsBefore, got)
	}

	// --- AuthorizedStore matrix (partial permission, cross-tenant RLS) ---
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	newStore := func(t *testing.T, principal domain.Principal) *postgresstore.AuthorizedStore {
		t.Helper()
		policy := authz.NewPolicy()
		ac, err := authz.NewAuthorizedContext(ctx, policy, authz.Request{Principal: principal, Tenant: authz.Tenant{ID: fixture.tenant.String(), WorkspaceID: fixture.workspace.String()}, ResourceType: authz.ResourceWorkspaces, Action: authz.ActionRead})
		if err != nil {
			t.Fatalf("authorize %s: %v", principal.Subject, err)
		}
		store, err := postgresstore.NewAuthorizedStore(server.runtime.Pool, ac)
		if err != nil {
			t.Fatalf("authorized store: %v", err)
		}
		return store
	}
	restrictedPrincipal := domain.Principal{
		Subject:      fixture.subject.String(),
		Type:         "service_account",
		OrgID:        fixture.tenant.String(),
		WorkspaceIDs: []string{fixture.workspace.String()},
		Scopes:       []string{"workspaces:read", "memory:write"},
		GrantDigest:  "e2e-grant", GrantVersion: 1,
	}
	restricted := newStore(t, restrictedPrincipal)

	// Save allowed, relation denied: the compound handoff fails closed with
	// zero effects (REM-AUTH-001 edge).
	_, err = restricted.ExecuteHandoff(ctx, domain.HandoffRequest{
		IdempotencyKey: "e2e-restricted-relation",
		Observation:    domain.SaveObservationInput{Title: "restricted", Content: "relation denied", Project: "e2e"},
		Relation:       &domain.HandoffRelationInput{Target: mustPublicRef(t, saveID), Type: domain.RelationReferences},
	})
	var denial *domain.HandoffError
	if !errors.As(err, &denial) || denial.Code != domain.HandoffErrorForbidden {
		t.Fatalf("partial permission error = %v, want forbidden handoff error", err)
	}
	if got := fixture.count(t, `SELECT count(*) FROM observations WHERE tenant_id=$1`, fixture.tenant); got != observationsBefore {
		t.Fatalf("denied handoff mutated observations: %d -> %d", observationsBefore, got)
	}
	if got := fixture.count(t, `SELECT count(*) FROM edges WHERE tenant_id=$1`, fixture.tenant); got != edgesBefore {
		t.Fatalf("denied handoff mutated edges: %d -> %d", edgesBefore, got)
	}
	if got := fixture.count(t, `SELECT count(*) FROM handoff_receipts WHERE tenant_id=$1`, fixture.tenant); got != receiptsBefore {
		t.Fatalf("denied handoff mutated receipts: %d -> %d", receiptsBefore, got)
	}

	// The same restricted principal succeeds without the relation.
	restrictedResult, err := restricted.ExecuteHandoff(ctx, domain.HandoffRequest{
		IdempotencyKey: "e2e-restricted-plain",
		Observation:    domain.SaveObservationInput{Title: "restricted", Content: "plain handoff", Project: "e2e"},
	})
	if err != nil {
		t.Fatalf("restricted plain handoff: %v", err)
	}
	if restrictedResult.Status != domain.WriteStatusCreated || restrictedResult.Ref.PublicID == nil {
		t.Fatalf("restricted result = %+v, want created public ref", restrictedResult)
	}

	// Cross-tenant relation targets are invisible under RLS: validation, no
	// oracle, zero effects.
	foreignTenant, foreignWorkspace, foreignSession, foreignObservation := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := fixture.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,'foreign')`, foreignTenant); err != nil {
		t.Fatalf("seed foreign org: %v", err)
	}
	if _, err := fixture.admin.Exec(ctx, `INSERT INTO workspaces(tenant_id,organization_id,public_id,name) VALUES($1,(SELECT id FROM organizations WHERE tenant_id=$1),$2,'foreign')`, foreignTenant, foreignWorkspace); err != nil {
		t.Fatalf("seed foreign workspace: %v", err)
	}
	if _, err := fixture.admin.Exec(ctx, `INSERT INTO sessions(tenant_id,workspace_id,public_id,project_key) VALUES($1,(SELECT id FROM workspaces WHERE tenant_id=$1 AND public_id=$2),$3,'foreign')`, foreignTenant, foreignWorkspace, foreignSession); err != nil {
		t.Fatalf("seed foreign session: %v", err)
	}
	if _, err := fixture.admin.Exec(ctx, `INSERT INTO observations(tenant_id,session_id,public_id,type,title,content,project_key) VALUES($1,(SELECT id FROM sessions WHERE tenant_id=$1 AND public_id=$3),$2,'decision','foreign','foreign content','foreign')`, foreignTenant, foreignObservation, foreignSession); err != nil {
		t.Fatalf("seed foreign observation: %v", err)
	}
	owner := newStore(t, domain.Principal{
		Subject: fixture.subject.String(), Type: "service_account", OrgID: fixture.tenant.String(),
		WorkspaceIDs: []string{fixture.workspace.String()}, Roles: []string{"owner"},
		ProjectIDs: []string{"*"}, ClassificationClearance: []string{"*"},
		GrantDigest: "e2e-grant", GrantVersion: 1,
	})
	observationsAfterRestricted := fixture.count(t, `SELECT count(*) FROM observations WHERE tenant_id=$1`, fixture.tenant)
	_, err = owner.ExecuteHandoff(ctx, domain.HandoffRequest{
		IdempotencyKey: "e2e-cross-tenant",
		Observation:    domain.SaveObservationInput{Title: "cross", Content: "tenant isolation", Project: "e2e"},
		Relation:       &domain.HandoffRelationInput{Target: mustPublicRef(t, foreignObservation.String()), Type: domain.RelationReferences},
	})
	if !errors.As(err, &denial) || denial.Code != domain.HandoffErrorValidation {
		t.Fatalf("cross-tenant error = %v, want validation handoff error", err)
	}
	if got := fixture.count(t, `SELECT count(*) FROM observations WHERE tenant_id=$1`, fixture.tenant); got != observationsAfterRestricted {
		t.Fatalf("cross-tenant attempt mutated observations: %d -> %d", observationsAfterRestricted, got)
	}
	if got := fixture.count(t, `SELECT count(*) FROM handoff_receipts WHERE tenant_id=$1 AND key='e2e-cross-tenant'`); got != 0 {
		t.Fatalf("cross-tenant attempt left %d receipts, want 0", got)
	}

	// --- Review R7 fix 1: workspace-scoped, in-transaction revalidation ---
	memberish := newStore(t, domain.Principal{
		Subject:      uuid.NewString(),
		Type:         "service_account",
		OrgID:        fixture.tenant.String(),
		WorkspaceIDs: []string{fixture.workspace.String()},
		Scopes:       []string{"workspaces:read", "memory:write", "graph:write"},
		ProjectIDs:   []string{"*"},
		GrantDigest:  "e2e-grant", GrantVersion: 1,
	})

	// A sibling workspace of the SAME tenant is invisible to the
	// workspace-scoped relation resolution even for an owner: validation,
	// zero effects.
	siblingWorkspace, siblingSession, siblingObservation := uuid.New(), uuid.New(), uuid.New()
	if _, err := fixture.admin.Exec(ctx, `INSERT INTO workspaces(tenant_id,organization_id,public_id,name) VALUES($1,(SELECT id FROM organizations WHERE tenant_id=$1),$2,'e2e sibling')`, fixture.tenant, siblingWorkspace); err != nil {
		t.Fatalf("seed sibling workspace: %v", err)
	}
	if _, err := fixture.admin.Exec(ctx, `INSERT INTO sessions(tenant_id,workspace_id,public_id,project_key) VALUES($1,(SELECT id FROM workspaces WHERE tenant_id=$1 AND public_id=$2),$3,'e2e-sibling')`, fixture.tenant, siblingWorkspace, siblingSession); err != nil {
		t.Fatalf("seed sibling session: %v", err)
	}
	if _, err := fixture.admin.Exec(ctx, `INSERT INTO observations(tenant_id,session_id,public_id,type,title,content,project_key) VALUES($1,(SELECT id FROM sessions WHERE tenant_id=$1 AND public_id=$3),$2,'decision','sibling','sibling content','e2e-sibling')`, fixture.tenant, siblingObservation, siblingSession); err != nil {
		t.Fatalf("seed sibling observation: %v", err)
	}
	beforeSibling := fixture.count(t, `SELECT count(*) FROM handoff_receipts WHERE tenant_id=$1`, fixture.tenant)
	_, err = owner.ExecuteHandoff(ctx, domain.HandoffRequest{
		IdempotencyKey: "e2e-cross-workspace",
		Observation:    domain.SaveObservationInput{Title: "sibling", Content: "workspace isolation", Project: "e2e"},
		Relation:       &domain.HandoffRelationInput{Target: mustPublicRef(t, siblingObservation.String()), Type: domain.RelationReferences},
	})
	if !errors.As(err, &denial) || denial.Code != domain.HandoffErrorValidation {
		t.Fatalf("cross-workspace error = %v, want validation handoff error", err)
	}
	if got := fixture.count(t, `SELECT count(*) FROM handoff_receipts WHERE tenant_id=$1`, fixture.tenant); got != beforeSibling {
		t.Fatalf("cross-workspace attempt mutated receipts: %d -> %d", beforeSibling, got)
	}

	// A committed concurrent attribute change is revalidated at execution
	// time against the locked row: restricted classification denies the
	// member-ish principal with zero effects.
	if _, err := fixture.admin.Exec(ctx, `UPDATE observations SET classification='restricted' WHERE tenant_id=$1 AND public_id=$2`, fixture.tenant, saveID); err != nil {
		t.Fatalf("restrict target classification: %v", err)
	}
	beforeRestricted := fixture.count(t, `SELECT count(*) FROM handoff_receipts WHERE tenant_id=$1`, fixture.tenant)
	_, err = memberish.ExecuteHandoff(ctx, domain.HandoffRequest{
		IdempotencyKey: "e2e-attribute-drift",
		Observation:    domain.SaveObservationInput{Title: "drift", Content: "attribute revalidation", Project: "e2e"},
		Relation:       &domain.HandoffRelationInput{Target: mustPublicRef(t, saveID), Type: domain.RelationReferences},
	})
	if !errors.As(err, &denial) || denial.Code != domain.HandoffErrorForbidden {
		t.Fatalf("attribute-drift error = %v, want forbidden revalidation of locked attributes", err)
	}
	if got := fixture.count(t, `SELECT count(*) FROM handoff_receipts WHERE tenant_id=$1`, fixture.tenant); got != beforeRestricted {
		t.Fatalf("attribute-drift attempt mutated receipts: %d -> %d", beforeRestricted, got)
	}

	// A live uncommitted attribute change holds the target row: the FOR
	// SHARE revalidation must block on it and fail closed as bounded
	// contention (retryable unavailable) rather than authorize stale
	// attributes — then zero effects after the blocker rolls back.
	conn, err := fixture.admin.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire admin connection: %v", err)
	}
	if _, err := conn.Exec(ctx, `BEGIN`); err != nil {
		t.Fatalf("begin blocker: %v", err)
	}
	if _, err := conn.Exec(ctx, `UPDATE observations SET classification='confidential' WHERE tenant_id=$1 AND public_id=$2`, fixture.tenant, saveID); err != nil {
		t.Fatalf("hold target row lock: %v", err)
	}
	beforeLock := fixture.count(t, `SELECT count(*) FROM handoff_receipts WHERE tenant_id=$1`, fixture.tenant)
	_, err = memberish.ExecuteHandoff(ctx, domain.HandoffRequest{
		IdempotencyKey: "e2e-live-lock",
		Observation:    domain.SaveObservationInput{Title: "live", Content: "lock contention", Project: "e2e"},
		Relation:       &domain.HandoffRelationInput{Target: mustPublicRef(t, saveID), Type: domain.RelationReferences},
	})
	var contention *domain.HandoffError
	if !errors.As(err, &contention) || contention.Code != domain.HandoffErrorUnavailable || !contention.Retryable {
		t.Fatalf("live-lock error = %v, want retryable unavailable contention", err)
	}
	if _, err := conn.Exec(ctx, `ROLLBACK`); err != nil {
		t.Fatalf("rollback blocker: %v", err)
	}
	conn.Release()
	if got := fixture.count(t, `SELECT count(*) FROM handoff_receipts WHERE tenant_id=$1`, fixture.tenant); got != beforeLock {
		t.Fatalf("live-lock attempt mutated receipts: %d -> %d", beforeLock, got)
	}
}

func mustPublicRef(t *testing.T, id string) domain.ObservationRef {
	t.Helper()
	parsed, err := uuid.Parse(id)
	if err != nil {
		t.Fatalf("public id %q: %v", id, err)
	}
	return domain.ObservationRef{PublicID: &parsed}
}
