//go:build postgres_integration

package server

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	"github.com/lleontor705/cortex/internal/config"
	"github.com/lleontor705/cortex/internal/migration"
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
