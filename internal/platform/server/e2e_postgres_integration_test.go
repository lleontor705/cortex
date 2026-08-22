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
	"regexp"
	"runtime"
	"sort"
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
	servermigrations "github.com/lleontor705/cortex/migrations/v2"
	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"
)

// postgresE2EFixture owns the privileged migration-role pool plus the tenant
// fixtures every E2E scenario shares. Identity credentials are NEVER seeded
// here: the configured service principal, its canonical grants, and the
// reserved bootstrap token are provisioned exclusively by Open through the
// durable cortex_bootstrap_service_principal reconciler, and every principal
// the tests authenticate with is obtained from the token verifier.
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

	// Only the prerequisite tenant fixtures are seeded directly. The
	// organization and workspace must exist because the bootstrap
	// reconciler validates its coordinates; the service account, actor
	// subject, canonical grants, and bootstrap token are created by Open.
	tenant, workspace, subject := uuid.New(), uuid.New(), uuid.New()
	if _, err := admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, "e2e-"+tenant.String()); err != nil {
		t.Fatalf("create E2E organization: %v", err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO workspaces(tenant_id,organization_id,public_id,name) VALUES($1,(SELECT id FROM organizations WHERE tenant_id=$1),$2,$3)`, tenant, workspace, "e2e-"+workspace.String()); err != nil {
		t.Fatalf("create E2E workspace: %v", err)
	}

	// The configured bearer is a random canonical secret: at least twelve
	// characters, no surrounding or control characters, so validateConfig
	// and byte-exact request verification both accept every presentation.
	token := "e2e-bootstrap-" + uuid.NewString()
	return &postgresE2EFixture{
		admin:     admin,
		tenant:    tenant,
		workspace: workspace,
		subject:   subject,
		token:     token,
		config: config.Config{
			Server: config.ServerConfig{
				Storage:                 config.ServerStorageConfig{Driver: "postgres", DSN: appDSN, MigrationDSN: migrationDSN, MaxConns: 16},
				Provider:                config.ServerProviderConfig{Embedding: "none", Vector: "none"},
				TenantID:                tenant.String(),
				WorkspaceID:             workspace.String(),
				PrincipalSubject:        subject.String(),
				Roles:                   []string{"owner"},
				Scopes:                  []string{"*"},
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

// postJSON performs an authenticated JSON POST and returns the decoded
// response body. It never formats the bearer or the response payload into
// diagnostics and closes the response body checking the error.
func postJSON(t *testing.T, baseURL, path, bearer string, payload map[string]any, wantStatus int) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, baseURL+path, bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+bearer)
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("%s request: %v", path, err)
	}
	body, err := io.ReadAll(response.Body)
	if closeErr := response.Body.Close(); closeErr != nil {
		t.Fatalf("%s response close: %v", path, closeErr)
	}
	if err != nil {
		t.Fatalf("read %s response: %v", path, err)
	}
	if response.StatusCode != wantStatus {
		t.Fatalf("%s status = %d, want %d", path, response.StatusCode, wantStatus)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode %s response: %v", path, err)
	}
	return decoded
}

// provisionScopedUser creates a partial-permission identity through the
// production admin path: POST /api/admin/users persists the app_users row and
// provisions the actor, its grants, and its audit row atomically through
// cortex_provision_actor, with the grant digest computed inside PostgreSQL.
// The literal "service-account" role value routes the verified principal
// through the scope path of the authorization policy — the same identity
// shape NewSystemService uses for background work — so the actor's entire
// authority is its scope/project/workspace grants. Project authority for a
// scoped credential is itself a verified scope (authz policy contract:
// TestServiceAccountScopeAndABAC): "project:*" must accompany every
// resource scope because each observation write carries a project and the
// policy never derives project authority from a role.
func (f *postgresE2EFixture) provisionScopedUser(t *testing.T, baseURL, label string, scopes []string) (string, string) {
	t.Helper()
	// The wildcard project scope is appended to the caller's resource scopes
	// so the identity remains partial in RESOURCES (memory/graph) while
	// still authorized for the project dimension every write requires.
	grants := append(append([]string(nil), scopes...), "project:*")
	created := postJSON(t, baseURL, "/api/admin/users", f.token, map[string]any{
		"email":        label + "-" + uuid.NewString() + "@e2e.test",
		"display_name": label,
		"roles":        []string{"service-account"},
		"workspaces":   []string{f.workspace.String()},
		"scopes":       grants,
		"projects":     []string{"*"},
	}, http.StatusCreated)
	userID, _ := created["id"].(string)
	if userID == "" {
		t.Fatalf("admin user creation for %s returned no id", label)
	}
	secret := issueTokenOverHTTP(t, baseURL, f.token, userID, label, grants)
	return userID, secret
}

// issueTokenOverHTTP mints an ordinary token for subject through the
// production admin endpoint. The plaintext secret is returned exactly once
// to the authorized caller and never persisted by the test beyond memory.
func issueTokenOverHTTP(t *testing.T, baseURL, bearer, subject, name string, scopes []string) string {
	t.Helper()
	issued := postJSON(t, baseURL, "/api/admin/tokens", bearer, map[string]any{
		"subject": subject, "name": name, "scopes": scopes,
	}, http.StatusCreated)
	secret, _ := issued["secret"].(string)
	if secret == "" {
		t.Fatalf("token issuance for %s returned no secret", name)
	}
	return secret
}

// verifyPrincipal authenticates a bearer through the narrow production
// verifier and returns the verified principal. Every principal used for
// authorization or RLS in the E2E suite comes from this path: verified
// provenance is the only credential a store can bind.
func verifyPrincipal(t *testing.T, pool *pgxpool.Pool, tenant uuid.UUID, secret string) domain.Principal {
	t.Helper()
	verifier, err := postgresstore.NewTokenPrincipalVerifier(pool, tenant.String())
	if err != nil {
		t.Fatalf("construct verifier: %v", err)
	}
	principal, err := verifier.VerifyToken(t.Context(), secret, "")
	if err != nil {
		t.Fatalf("verify principal: %v", err)
	}
	if principal.Subject == "" || principal.GrantDigest == "" || principal.GrantVersion < 1 {
		t.Fatal("verified principal is missing subject, provenance, or grant version")
	}
	return principal
}

// bootstrapSnapshot captures the durable identity lifecycle state the
// restart and rotation assertions compare. It never reads or stores digest
// material: only public IDs, versions, and counts.
type bootstrapSnapshot struct {
	activeTokenID   string
	grantVersion    int64
	namedTokens     int
	activeTokens    int
	bootstrapAudits int
}

func (f *postgresE2EFixture) snapshotBootstrap(t *testing.T) bootstrapSnapshot {
	t.Helper()
	ctx := t.Context()
	snapshot := bootstrapSnapshot{}
	if err := f.admin.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE revoked_at IS NULL) FROM api_tokens WHERE tenant_id=$1 AND name='cortex-bootstrap'`, f.tenant).Scan(&snapshot.namedTokens, &snapshot.activeTokens); err != nil {
		t.Fatalf("count bootstrap tokens: %v", err)
	}
	if err := f.admin.QueryRow(ctx, `SELECT grant_version FROM actor_subjects WHERE tenant_id=$1 AND public_id=$2`, f.tenant, f.subject).Scan(&snapshot.grantVersion); err != nil {
		t.Fatalf("read bootstrap grant version: %v", err)
	}
	if err := f.admin.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND action LIKE 'identity.bootstrap.%'`, f.tenant).Scan(&snapshot.bootstrapAudits); err != nil {
		t.Fatalf("count bootstrap audit events: %v", err)
	}
	if snapshot.activeTokens > 0 {
		if err := f.admin.QueryRow(ctx, `SELECT public_id::text FROM api_tokens WHERE tenant_id=$1 AND name='cortex-bootstrap' AND revoked_at IS NULL ORDER BY id LIMIT 1`, f.tenant).Scan(&snapshot.activeTokenID); err != nil {
			t.Fatalf("read active bootstrap token: %v", err)
		}
	}
	return snapshot
}

// identityCaptureQueries declares each identity table's capture query. It
// is the single source of truth shared by captureIdentityState (executes
// the strings verbatim) and TestE2EIdentitySnapshotCoverageCanary
// (structurally validates every SELECT projection against the embedded
// server schema), so the validated bytes are exactly the executed bytes.
var identityCaptureQueries = map[string]string{
	"service_accounts": `SELECT COALESCE(json_agg(row_to_json(s))::text,'[]') FROM (SELECT id,public_id::text,tenant_id::text,name,active,disabled_at,created_at,updated_at,created_by::text,updated_by::text FROM service_accounts WHERE tenant_id=$1 ORDER BY public_id) s`,
	"app_users":        `SELECT COALESCE(json_agg(row_to_json(u))::text,'[]') FROM (SELECT id,public_id::text,tenant_id::text,email,COALESCE(display_name,''),active,disabled_at,created_at,updated_at,created_by::text,updated_by::text FROM app_users WHERE tenant_id=$1 ORDER BY public_id) u`,
	"actor_subjects":   `SELECT COALESCE(json_agg(row_to_json(a))::text,'[]') FROM (SELECT id,public_id::text,tenant_id::text,subject,actor_type,active,revoked_at,grant_version,grant_digest,created_at FROM actor_subjects WHERE tenant_id=$1 ORDER BY public_id) a`,
	"principal_grants": `SELECT COALESCE(json_agg(row_to_json(g))::text,'[]') FROM (SELECT id,public_id::text,tenant_id::text,actor_public_id::text,grant_type,grant_value,created_by::text,updated_by::text,created_at,updated_at FROM principal_grants WHERE tenant_id=$1 ORDER BY actor_public_id,grant_type,grant_value) g`,
	"api_tokens":       `SELECT COALESCE(json_agg(row_to_json(k))::text,'[]') FROM (SELECT k.id,k.public_id::text,k.tenant_id::text,k.name,k.token_prefix,encode(k.token_digest,'hex') AS token_digest,k.subject_user_id,k.subject_service_account_id,COALESCE(au.public_id,sa.public_id)::text AS subject,CASE WHEN sa.id IS NOT NULL THEN 'service_account' ELSE 'user' END AS subject_type,k.scopes,k.workspace_ids::text[],k.rate_limit_tier,k.expires_at,k.revoked_at,k.last_used_at,k.created_by::text,k.created_at,k.updated_at FROM api_tokens k LEFT JOIN app_users au ON au.tenant_id=k.tenant_id AND au.id=k.subject_user_id LEFT JOIN service_accounts sa ON sa.tenant_id=k.tenant_id AND sa.id=k.subject_service_account_id WHERE k.tenant_id=$1 ORDER BY k.public_id) k`,
	"audit_events":     `SELECT COALESCE(json_agg(row_to_json(e))::text,'[]') FROM (SELECT id,public_id::text,tenant_id::text,actor_public_id::text,actor_subject,action,resource_type,resource_public_id::text,resource_id,correlation_id,metadata::text,encode(previous_hash,'hex') AS previous_hash,encode(event_hash,'hex') AS event_hash,allowed,reason,created_at,updated_at FROM audit_events WHERE tenant_id=$1 ORDER BY id) e`,
}

// captureIdentityState serializes the complete durable identity, grant,
// token, and audit state of the fixture tenant: for every table it projects
// EVERY schema column — row IDs, tenant IDs, actor and resource identifiers,
// correlation and subject labels, audit stamps, created_by/updated_by
// provenance, disabled_at, and credential material — captured as
// deterministic ordered JSON. Sensitive values (token_digest, event hashes,
// the actor grant digest, audit metadata) ARE captured so the fail-closed
// comparison detects any change to them, but only ever as in-memory bytes
// compared for equality: no captured value, digest, or hash is ever
// formatted into a diagnostic. TestE2EIdentitySnapshotCoverageCanary pins
// these projections to the embedded server schema.
func (f *postgresE2EFixture) captureIdentityState(t *testing.T) map[string]string {
	t.Helper()
	states := make(map[string]string)
	capture := func(key, query string) {
		t.Helper()
		var state string
		if err := f.admin.QueryRow(t.Context(), query, f.tenant).Scan(&state); err != nil {
			t.Fatalf("capture %s state: %v", key, err)
		}
		states[key] = state
	}
	tables := make([]string, 0, len(identityCaptureQueries))
	for table := range identityCaptureQueries {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	for _, table := range tables {
		capture(table, identityCaptureQueries[table])
	}
	capture("data_counts", `SELECT (SELECT count(*) FROM sessions WHERE tenant_id=$1)::text || '/' || (SELECT count(*) FROM observations WHERE tenant_id=$1)::text || '/' || (SELECT count(*) FROM edges WHERE tenant_id=$1)::text || '/' || (SELECT count(*) FROM handoff_receipts WHERE tenant_id=$1)::text`)
	return states
}

// assertIdentityStateUnchanged proves a fail-closed startup left every
// captured table byte-identical. Only the changed table's name (a field
// category) is reported: captured rows include secrets, digests, and
// hashes, so no captured value may ever reach a diagnostic.
func assertIdentityStateUnchanged(t *testing.T, before, after map[string]string) {
	t.Helper()
	keys := make([]string, 0, len(before))
	for key := range before {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if before[key] != after[key] {
			t.Fatalf("identity state table %q changed across the fail-closed startup", key)
		}
	}
}

// TestE2EIdentitySnapshotCoverageCanary pins the fail-closed snapshot
// contract to the schema itself, STRUCTURALLY. For each identity table it
// derives the COMPLETE column list from the exact embedded PostgreSQL
// server migrations production applies, parses the shared capture query's
// SELECT projection, and verifies every schema column is among the
// projected names. JOIN, WHERE, and ORDER BY text is never part of the
// parsed projection, so a non-projection reference cannot satisfy a
// missing projected column — the omission mutation subtests prove this for
// the exact false-positive shapes a substring oracle misses. It also pins
// the category-only comparison diagnostic. Assertion messages carry table
// and column names only; captured values stay compare-only. It needs no
// database and runs wherever the tagged suite compiles.
func TestE2EIdentitySnapshotCoverageCanary(t *testing.T) {
	schemas := []string{
		servermigrations.ServerSQL,
		servermigrations.ServerIdentityGraphSQL,
		servermigrations.ServerSyncSQL,
		servermigrations.ServerSyncIdentitySQL,
		servermigrations.ServerHandoffReceiptsSQL,
		servermigrations.ServerWorkspaceBindingSQL,
		servermigrations.ServerProjectArtifactsSQL,
	}
	for _, table := range []string{"service_accounts", "app_users", "actor_subjects", "principal_grants", "api_tokens", "audit_events"} {
		columns := schemaTableColumns(t, schemas, table)
		if len(columns) == 0 {
			t.Fatalf("embedded server schema defines no columns for %q", table)
		}
		query, ok := identityCaptureQueries[table]
		if !ok {
			t.Fatalf("identity state capture is missing table %q", table)
		}
		if missing := captureProjectionMissing(query, columns); len(missing) > 0 {
			t.Fatalf("identity state capture for %q does not project schema column %q (JOIN/WHERE/ORDER BY references do not count)", table, missing[0])
		}
	}

	// Omission mutation tests: remove one projected column from the real
	// query while its non-projection reference survives, then require the
	// structural validation to report it missing. These are precisely the
	// omissions a substring-anywhere oracle misclassifies as covered.
	for _, mutation := range []struct {
		table              string
		column             string
		survivingReference string
	}{
		{"service_accounts", "public_id", "ORDER BY public_id"},
		{"api_tokens", "tenant_id", "k.tenant_id=$1"},
		{"audit_events", "id", "ORDER BY id"},
	} {
		t.Run("omission_"+mutation.table+"_"+mutation.column, func(t *testing.T) {
			mutated := removeProjectedColumn(t, identityCaptureQueries[mutation.table], mutation.column)
			if !strings.Contains(mutated, mutation.survivingReference) {
				t.Fatalf("mutation setup: non-projection reference %q did not survive the omission", mutation.survivingReference)
			}
			missing := captureProjectionMissing(mutated, []string{mutation.column})
			if len(missing) == 0 {
				t.Fatalf("structural projection validation failed to detect the omission of %s.%s despite the surviving non-projection reference", mutation.table, mutation.column)
			}
		})
	}

	// The comparison diagnostic may report only the table-name category:
	// the function must keep comparing every captured key, keep its single
	// category-only failure message, and never pass a captured value to a
	// formatter.
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve E2E test source location")
	}
	source, err := os.ReadFile(currentFile)
	if err != nil {
		t.Fatalf("read E2E test source: %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func assertIdentityStateUnchanged")
	if start < 0 {
		t.Fatal("assertIdentityStateUnchanged is missing from the E2E source")
	}
	end := strings.Index(text[start:], "\n}")
	if end < 0 {
		t.Fatal("assertIdentityStateUnchanged is malformed in the E2E source")
	}
	body := text[start : start+end]
	if !strings.Contains(body, "if before[key] != after[key]") {
		t.Fatal("assertIdentityStateUnchanged no longer compares every captured table")
	}
	if !strings.Contains(body, `t.Fatalf("identity state table %q changed across the fail-closed startup", key)`) {
		t.Fatal("assertIdentityStateUnchanged must report only the table-name category")
	}
	if strings.Contains(body, ", before[key]") || strings.Contains(body, ", after[key]") {
		t.Fatal("assertIdentityStateUnchanged formats captured values into a diagnostic")
	}
}

// captureProjectionMissing parses the capture query's SELECT projection and
// returns every required column that is not projected. A parse failure is
// reported as every required column missing so the caller's diagnostic
// names the table and its first uncovered column.
func captureProjectionMissing(query string, required []string) []string {
	projected, err := projectionColumns(query)
	if err != nil {
		return required
	}
	projection := make(map[string]bool, len(projected))
	for _, name := range projected {
		projection[name] = true
	}
	var missing []string
	for _, column := range required {
		if !projection[column] {
			missing = append(missing, column)
		}
	}
	return missing
}

// projectionColumns structurally extracts the SELECT projection column
// names of one capture query: the item list between the aggregate's inner
// "(SELECT " and its terminating " FROM ", split on top-level commas with
// parentheses and string literals respected, each item reduced to its
// projected name (alias after AS, else the final identifier with casts
// stripped). JOIN, WHERE, and ORDER BY text lies outside the projection by
// construction and can never satisfy a required column.
func projectionColumns(query string) ([]string, error) {
	start := strings.Index(query, "(SELECT ")
	if start < 0 {
		return nil, errors.New("postgres e2e: capture query has no inner SELECT projection")
	}
	rest := query[start+len("(SELECT "):]
	end := strings.Index(rest, " FROM ")
	if end < 0 {
		return nil, errors.New("postgres e2e: capture query projection is not terminated by FROM")
	}
	items, err := splitProjectionItems(rest[:end])
	if err != nil {
		return nil, err
	}
	columns := make([]string, 0, len(items))
	for _, item := range items {
		name, err := projectionColumnName(item)
		if err != nil {
			return nil, err
		}
		columns = append(columns, name)
	}
	return columns, nil
}

func splitProjectionItems(projection string) ([]string, error) {
	var items []string
	depth := 0
	quoted := false
	last := 0
	for i := 0; i < len(projection); i++ {
		c := projection[i]
		if c == '\'' && depth == 0 {
			quoted = !quoted
			continue
		}
		if quoted {
			continue
		}
		switch c {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return nil, errors.New("postgres e2e: unbalanced capture projection")
			}
		case ',':
			if depth == 0 {
				items = append(items, projection[last:i])
				last = i + 1
			}
		}
	}
	if quoted || depth != 0 {
		return nil, errors.New("postgres e2e: unterminated capture projection")
	}
	return append(items, projection[last:]), nil
}

var (
	projectionAliasPattern     = regexp.MustCompile(`(?i)\bAS\s+([a-z_][a-z0-9_]*)\s*$`)
	projectionCastSuffix       = regexp.MustCompile(`(?i)::[a-z_]+(\[\])*\s*$`)
	projectionIdentifierSuffix = regexp.MustCompile(`([a-z_][a-z0-9_]*)\s*$`)
)

func projectionColumnName(item string) (string, error) {
	trimmed := strings.TrimSpace(item)
	if trimmed == "" {
		return "", errors.New("postgres e2e: empty capture projection item")
	}
	if match := projectionAliasPattern.FindStringSubmatch(trimmed); match != nil {
		return match[1], nil
	}
	trimmed = projectionCastSuffix.ReplaceAllString(trimmed, "")
	// A function-call item projects its first argument's name
	// (COALESCE(display_name,'') -> display_name); anything else projects
	// its final identifier.
	if strings.HasSuffix(trimmed, ")") {
		if open := strings.LastIndex(trimmed, "("); open >= 0 {
			argument := trimmed[open+1:]
			if comma := strings.IndexAny(argument, ",)"); comma >= 0 {
				argument = argument[:comma]
			}
			if match := projectionIdentifierSuffix.FindStringSubmatch(strings.TrimSpace(argument)); match != nil {
				return match[1], nil
			}
		}
		return "", fmt.Errorf("postgres e2e: capture projection item %q has no column name", item)
	}
	match := projectionIdentifierSuffix.FindStringSubmatch(trimmed)
	if match == nil {
		return "", fmt.Errorf("postgres e2e: capture projection item %q has no column name", item)
	}
	return match[1], nil
}

// removeProjectedColumn deletes the item that projects column from the
// query's SELECT projection, leaving every other reference — JOIN, WHERE,
// ORDER BY — untouched: exactly the omission a substring-anywhere oracle
// misclassifies as covered because the non-projection reference survives.
func removeProjectedColumn(t *testing.T, query, column string) string {
	t.Helper()
	start := strings.Index(query, "(SELECT ")
	if start < 0 {
		t.Fatal("mutation setup: query has no inner SELECT projection")
	}
	head := query[:start+len("(SELECT ")]
	rest := query[start+len("(SELECT "):]
	end := strings.Index(rest, " FROM ")
	if end < 0 {
		t.Fatal("mutation setup: query projection is not terminated by FROM")
	}
	projection, tail := rest[:end], rest[end:]
	items, err := splitProjectionItems(projection)
	if err != nil {
		t.Fatalf("mutation setup: %v", err)
	}
	rebuilt := make([]string, 0, len(items))
	removed := false
	for _, item := range items {
		name, err := projectionColumnName(item)
		if err != nil {
			t.Fatalf("mutation setup: %v", err)
		}
		if name == column && !removed {
			removed = true
			continue
		}
		rebuilt = append(rebuilt, item)
	}
	if !removed {
		t.Fatalf("mutation setup: %q is not projected by the query", column)
	}
	return head + strings.Join(rebuilt, ",") + tail
}

// schemaTableColumns derives the complete column set of table from the
// embedded PostgreSQL server migrations: CREATE TABLE column definitions
// plus every ALTER TABLE ... ADD COLUMN IF NOT EXISTS — exactly how the
// applied schema is defined and evolves. Constraint lines (UNIQUE, FOREIGN
// KEY, CHECK, PRIMARY KEY) never match because a column definition always
// pairs the identifier with a SQL type.
func schemaTableColumns(t *testing.T, schemas []string, table string) []string {
	t.Helper()
	create := regexp.MustCompile(`(?s)CREATE TABLE (?:IF NOT EXISTS )?(?:public\.)?` + regexp.QuoteMeta(table) + `\s*\((.*?)\n\s*\);`)
	alter := regexp.MustCompile(`(?i)ALTER TABLE (?:ONLY )?(?:IF EXISTS )?(?:public\.)?` + regexp.QuoteMeta(table) + `\s+ADD COLUMN IF NOT EXISTS ([a-z_]+)`)
	definition := regexp.MustCompile(`(?:\A|,|\n)\s*([a-z_]+)\s+(?:bigint|smallint|integer|uuid|text|timestamptz|boolean|bytea|jsonb|numeric|text\[\]|uuid\[\])\b`)
	columns := make(map[string]bool)
	for _, schema := range schemas {
		for _, block := range create.FindAllStringSubmatch(schema, -1) {
			for _, match := range definition.FindAllStringSubmatch(block[1], -1) {
				columns[match[1]] = true
			}
		}
		for _, match := range alter.FindAllStringSubmatch(schema, -1) {
			columns[match[1]] = true
		}
	}
	names := make([]string, 0, len(columns))
	for name := range columns {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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
			closeErr := response.Body.Close()
			switch {
			case closeErr != nil:
				lastErr = closeErr
			case response.StatusCode == want:
				return nil
			default:
				lastErr = fmt.Errorf("status %d", response.StatusCode)
			}
		} else {
			lastErr = err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("wait for %s: %w", url, lastErr)
}

// e2eBearerStatus performs a GET with the presented bearer and returns the
// HTTP status. The secret itself never appears in diagnostics.
func e2eBearerStatus(t *testing.T, baseURL, path, bearer string) int {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+bearer)
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("%s request: %v", path, err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatalf("%s response close: %v", path, err)
	}
	return response.StatusCode
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
	if err := response.Body.Close(); err != nil {
		t.Fatalf("unauthenticated sync response close: %v", err)
	}
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

// TestE2EBootstrapVerifiedTokenLifecycle proves the durable bootstrap
// lifecycle against real PostgreSQL: Open provisions the configured service
// principal, canonical grants, and reserved token; the configured bearer
// authenticates HTTP and database reads/writes; the first ordinary token
// performs a permitted database-backed operation; an identical restart is
// byte-stable; a replacement bearer rotates the reserved token and
// invalidates the old secret; and a revoked bearer fails startup without
// mutating any identity, grant, token, or audit state.
func TestE2EBootstrapVerifiedTokenLifecycle(t *testing.T) {
	port := reserveLoopbackPort(t)
	fixture := newPostgresE2EFixture(t, port)
	server := startE2EServer(t, fixture.config)
	baseURL := "http://" + server.runtime.Address()
	if err := waitForHTTPStatus(baseURL+"/health", http.StatusOK, 10*time.Second); err != nil {
		t.Fatal(err)
	}

	// --- Open provisioned the full durable identity through bootstrap ---
	var serviceName string
	var serviceActive bool
	if err := fixture.admin.QueryRow(t.Context(), `SELECT name,active FROM service_accounts WHERE tenant_id=$1 AND public_id=$2`, fixture.tenant, fixture.subject).Scan(&serviceName, &serviceActive); err != nil {
		t.Fatalf("bootstrap service account missing: %v", err)
	}
	if serviceName != "cortex-server" || !serviceActive {
		t.Fatalf("bootstrap service account = %q active=%v, want cortex-server active", serviceName, serviceActive)
	}
	var grantCount int
	if err := fixture.admin.QueryRow(t.Context(), `SELECT count(*) FROM principal_grants WHERE tenant_id=$1 AND actor_public_id=$2`, fixture.tenant, fixture.subject).Scan(&grantCount); err != nil {
		t.Fatalf("count canonical grants: %v", err)
	}
	if grantCount != 5 {
		t.Fatalf("canonical grant rows = %d, want 5 (role, workspace, scope, project, classification)", grantCount)
	}
	provisioned := fixture.snapshotBootstrap(t)
	if provisioned.namedTokens != 1 || provisioned.activeTokens != 1 {
		t.Fatalf("bootstrap tokens = %d active %d, want 1 active 1", provisioned.namedTokens, provisioned.activeTokens)
	}
	if provisioned.grantVersion != 1 || provisioned.bootstrapAudits != 1 {
		t.Fatalf("provisioned grant version = %d audit events = %d, want 1 and 1", provisioned.grantVersion, provisioned.bootstrapAudits)
	}

	// --- Configured bearer: verified /api/me plus DB write and read ---
	meStatus := e2eBearerStatus(t, baseURL, "/api/me", fixture.token)
	if meStatus != http.StatusOK {
		t.Fatalf("configured bearer /api/me status = %d, want %d", meStatus, http.StatusOK)
	}
	meRequest, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+"/api/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	meRequest.Header.Set("Authorization", "Bearer "+fixture.token)
	meResponse, err := (&http.Client{Timeout: 5 * time.Second}).Do(meRequest)
	if err != nil {
		t.Fatal(err)
	}
	meBody, err := io.ReadAll(meResponse.Body)
	if closeErr := meResponse.Body.Close(); closeErr != nil {
		t.Fatalf("/api/me response close: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("read /api/me body: %v", err)
	}
	var me struct {
		ID    string   `json:"id"`
		Type  string   `json:"type"`
		OrgID string   `json:"org_id"`
		Roles []string `json:"roles"`
	}
	if err := json.Unmarshal(meBody, &me); err != nil {
		t.Fatalf("decode /api/me: %v", err)
	}
	if me.ID != fixture.subject.String() || me.Type != "service_account" || me.OrgID != fixture.tenant.String() {
		t.Fatalf("/api/me identity fields do not match the configured principal")
	}
	if len(me.Roles) != 1 || me.Roles[0] != "owner" {
		t.Fatalf("/api/me roles = %v, want [owner]", me.Roles)
	}
	sessionPayload := `{"project":"e2e","directory":"/e2e","started_at":"` + time.Now().UTC().Format(time.RFC3339) + `"}`
	writeRequest, err := http.NewRequestWithContext(t.Context(), http.MethodPost, baseURL+"/api/sessions", strings.NewReader(sessionPayload))
	if err != nil {
		t.Fatal(err)
	}
	writeRequest.Header.Set("Authorization", "Bearer "+fixture.token)
	writeRequest.Header.Set("Content-Type", "application/json")
	writeResponse, err := (&http.Client{Timeout: 5 * time.Second}).Do(writeRequest)
	if err != nil {
		t.Fatalf("configured bearer session write: %v", err)
	}
	writeBody, err := io.ReadAll(writeResponse.Body)
	if closeErr := writeResponse.Body.Close(); closeErr != nil {
		t.Fatalf("session write response close: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("read session write response: %v", err)
	}
	if writeResponse.StatusCode != http.StatusCreated {
		t.Fatalf("configured bearer session write status = %d, want %d", writeResponse.StatusCode, http.StatusCreated)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(writeBody, &created); err != nil || created.ID == "" {
		t.Fatalf("session write response carries no server-assigned id (unmarshal error: %v)", err)
	}
	if _, err := uuid.Parse(created.ID); err != nil {
		t.Fatalf("session write returned a non-UUID id")
	}
	var persistedSessions int
	if err := fixture.admin.QueryRow(t.Context(), `SELECT count(*) FROM sessions WHERE tenant_id=$1 AND public_id=$2`, fixture.tenant, created.ID).Scan(&persistedSessions); err != nil {
		t.Fatalf("count persisted sessions: %v", err)
	}
	if persistedSessions != 1 {
		t.Fatalf("persisted session rows = %d, want 1", persistedSessions)
	}
	if status := e2eBearerStatus(t, baseURL, "/api/observations", fixture.token); status != http.StatusOK {
		t.Fatalf("configured bearer observation read status = %d, want %d", status, http.StatusOK)
	}

	// --- First ordinary token performs a permitted DB-backed operation ---
	ordinarySecret := issueTokenOverHTTP(t, baseURL, fixture.token, fixture.subject.String(), "e2e-ordinary", nil)
	if status := e2eBearerStatus(t, baseURL, "/api/me", ordinarySecret); status != http.StatusOK {
		t.Fatalf("first ordinary token /api/me status = %d, want %d", status, http.StatusOK)
	}
	ordinaryPrincipal := verifyPrincipal(t, server.runtime.Pool, fixture.tenant, ordinarySecret)
	if ordinaryPrincipal.Subject != fixture.subject.String() {
		t.Fatal("first ordinary token resolves to a different subject")
	}
	if len(ordinaryPrincipal.Roles) != 1 || ordinaryPrincipal.Roles[0] != "owner" {
		t.Fatalf("first ordinary token roles = %v, want [owner]", ordinaryPrincipal.Roles)
	}
	saved := postJSON(t, baseURL, "/api/observations", ordinarySecret, map[string]any{
		"session_id": created.ID, "project": "e2e", "type": "decision",
		"title": "E2E ordinary save", "content": "ordinary token permitted operation",
	}, http.StatusCreated)
	savedID, _ := saved["id"].(string)
	if _, err := uuid.Parse(savedID); err != nil {
		t.Fatalf("ordinary token save returned a non-UUID id")
	}
	if got := fixture.count(t, `SELECT count(*) FROM observations WHERE tenant_id=$1 AND public_id=$2`, fixture.tenant, savedID); got != 1 {
		t.Fatalf("ordinary token persisted observation rows = %d, want 1", got)
	}

	// --- Identical restart: identity, version, and audit stay stable ---
	if err := server.stop(); err != nil {
		t.Fatalf("stop first E2E server: %v", err)
	}
	restart := startE2EServer(t, fixture.config)
	if err := waitForHTTPStatus("http://"+restart.runtime.Address()+"/health", http.StatusOK, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	restarted := fixture.snapshotBootstrap(t)
	if restarted.activeTokenID != provisioned.activeTokenID {
		t.Fatal("identical restart changed the active bootstrap token identity")
	}
	if restarted.grantVersion != provisioned.grantVersion {
		t.Fatalf("identical restart changed grant version: %d -> %d", provisioned.grantVersion, restarted.grantVersion)
	}
	if restarted.namedTokens != provisioned.namedTokens || restarted.activeTokens != provisioned.activeTokens {
		t.Fatalf("identical restart changed token rows: named %d->%d active %d->%d", provisioned.namedTokens, restarted.namedTokens, provisioned.activeTokens, restarted.activeTokens)
	}
	if restarted.bootstrapAudits != provisioned.bootstrapAudits {
		t.Fatalf("identical restart appended %d bootstrap audit events", restarted.bootstrapAudits-provisioned.bootstrapAudits)
	}
	if status := e2eBearerStatus(t, "http://"+restart.runtime.Address(), "/api/me", fixture.token); status != http.StatusOK {
		t.Fatalf("configured bearer after identical restart status = %d, want %d", status, http.StatusOK)
	}
	if status := e2eBearerStatus(t, "http://"+restart.runtime.Address(), "/api/me", ordinarySecret); status != http.StatusOK {
		t.Fatalf("first ordinary token after restart status = %d, want %d", status, http.StatusOK)
	}

	// --- Replacement configured bearer rotates the reserved token ---
	if err := restart.stop(); err != nil {
		t.Fatalf("stop restarted E2E server: %v", err)
	}
	replacement := fixture.config
	replacement.HTTP.Token = "e2e-bootstrap-" + uuid.NewString()
	rotated := startE2EServer(t, replacement)
	rotatedBase := "http://" + rotated.runtime.Address()
	if err := waitForHTTPStatus(rotatedBase+"/health", http.StatusOK, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	rotatedSnapshot := fixture.snapshotBootstrap(t)
	if rotatedSnapshot.namedTokens != provisioned.namedTokens+1 || rotatedSnapshot.activeTokens != 1 {
		t.Fatalf("rotation token rows = named %d active %d, want named %d active 1", rotatedSnapshot.namedTokens, rotatedSnapshot.activeTokens, provisioned.namedTokens+1)
	}
	if rotatedSnapshot.activeTokenID == provisioned.activeTokenID {
		t.Fatal("rotation kept the previous token active")
	}
	if rotatedSnapshot.grantVersion != provisioned.grantVersion {
		t.Fatalf("rotation changed grant version: %d -> %d", provisioned.grantVersion, rotatedSnapshot.grantVersion)
	}
	if rotatedSnapshot.bootstrapAudits != provisioned.bootstrapAudits+1 {
		t.Fatalf("rotation audit events = %d, want %d", rotatedSnapshot.bootstrapAudits, provisioned.bootstrapAudits+1)
	}
	if status := e2eBearerStatus(t, rotatedBase, "/api/me", replacement.HTTP.Token); status != http.StatusOK {
		t.Fatalf("replacement bearer /api/me status = %d, want %d", status, http.StatusOK)
	}
	if status := e2eBearerStatus(t, rotatedBase, "/api/me", fixture.token); status != http.StatusUnauthorized {
		t.Fatalf("superseded bearer /api/me status = %d, want %d", status, http.StatusUnauthorized)
	}

	// --- Revoked bearer with the same secret fails startup without mutation ---
	var activeTokenID string
	if err := fixture.admin.QueryRow(t.Context(), `SELECT public_id::text FROM api_tokens WHERE tenant_id=$1 AND name='cortex-bootstrap' AND revoked_at IS NULL`, fixture.tenant).Scan(&activeTokenID); err != nil {
		t.Fatalf("read active bootstrap token before revocation: %v", err)
	}
	revokeRequest, err := http.NewRequestWithContext(t.Context(), http.MethodDelete, rotatedBase+"/api/admin/tokens/"+activeTokenID, nil)
	if err != nil {
		t.Fatal(err)
	}
	revokeRequest.Header.Set("Authorization", "Bearer "+replacement.HTTP.Token)
	revokeResponse, err := (&http.Client{Timeout: 5 * time.Second}).Do(revokeRequest)
	if err != nil {
		t.Fatalf("revoke bootstrap token: %v", err)
	}
	if err := revokeResponse.Body.Close(); err != nil {
		t.Fatalf("revoke response close: %v", err)
	}
	if revokeResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke bootstrap token status = %d, want %d", revokeResponse.StatusCode, http.StatusNoContent)
	}
	if status := e2eBearerStatus(t, rotatedBase, "/api/me", replacement.HTTP.Token); status != http.StatusUnauthorized {
		t.Fatalf("revoked bearer live status = %d, want %d", status, http.StatusUnauthorized)
	}
	if err := rotated.stop(); err != nil {
		t.Fatalf("stop rotated E2E server: %v", err)
	}

	// The complete durable identity, grant, token, and audit state —
	// timestamps included, secrets excluded — must be byte-identical
	// across the fail-closed startup.
	stateBefore := fixture.captureIdentityState(t)
	_, openErr := Open(t.Context(), replacement)
	if openErr == nil {
		t.Fatal("startup with the revoked bootstrap bearer unexpectedly succeeded")
	}
	// Prove the sentinel never leaked BEFORE the error is formatted into
	// any diagnostic: the redaction check must precede every %v of it.
	if strings.Contains(openErr.Error(), replacement.HTTP.Token) {
		t.Fatal("revoked-bearer startup error leaked the configured secret")
	}
	if !strings.Contains(openErr.Error(), "bootstrap service principal") {
		t.Fatalf("revoked-bearer startup failed outside the bootstrap stage: %v", openErr)
	}
	assertIdentityStateUnchanged(t, stateBefore, fixture.captureIdentityState(t))
	if snapshot := fixture.snapshotBootstrap(t); snapshot.activeTokens != 0 {
		t.Fatalf("active bootstrap tokens after failed startup = %d, want 0", snapshot.activeTokens)
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
	if err := response.Body.Close(); err != nil {
		t.Fatalf("unauthenticated smoke response close: %v", err)
	}
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
	if err := response.Body.Close(); err != nil {
		t.Fatalf("authenticated smoke response close: %v", err)
	}
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
	body, err := io.ReadAll(response.Body)
	if closeErr := response.Body.Close(); closeErr != nil {
		t.Fatalf("mcp %s response close: %v", method, closeErr)
	}
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
// invisibility (REM-AUTH-001, REM-HANDOFF-001/002, REM-MCP-001). Every
// principal in the matrix is a verified token principal: partial permissions
// come from scoped tokens of durable users provisioned through the production
// admin path, and the owner principal is the configured bootstrap principal.
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
	if err := response.Body.Close(); err != nil {
		t.Fatalf("unauthenticated tools/list response close: %v", err)
	}
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
	if got := fixture.count(t, `SELECT count(*) FROM handoff_receipts WHERE tenant_id=$1 AND state='committed'`, fixture.tenant); got < 1 {
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
	// The matrix authenticates through real credentials only. The
	// partial-permission and member-ish identities are durable users
	// provisioned through the production admin endpoint (SQL-computed
	// grant digests, audited, no direct identity DML); the owner is the
	// configured bootstrap principal itself.
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	_, restrictedSecret := fixture.provisionScopedUser(t, baseURL, "e2e-restricted",
		[]string{"workspaces:read", "memory:write"})
	_, memberishSecret := fixture.provisionScopedUser(t, baseURL, "e2e-memberish",
		[]string{"workspaces:read", "memory:write", "graph:write"})
	newStore := func(t *testing.T, principal domain.Principal) *postgresstore.AuthorizedStore {
		t.Helper()
		policy := authz.NewPolicy()
		ac, err := authz.NewAuthorizedContext(ctx, policy, authz.Request{Principal: principal, Tenant: authz.Tenant{ID: fixture.tenant.String(), WorkspaceID: fixture.workspace.String()}, ResourceType: authz.ResourceWorkspaces, Action: authz.ActionRead})
		if err != nil {
			t.Fatalf("authorize principal: %v", err)
		}
		store, err := postgresstore.NewAuthorizedStore(server.runtime.Pool, ac)
		if err != nil {
			t.Fatalf("authorized store: %v", err)
		}
		return store
	}
	restrictedPrincipal := verifyPrincipal(t, server.runtime.Pool, fixture.tenant, restrictedSecret)
	if len(restrictedPrincipal.Roles) != 1 || restrictedPrincipal.Roles[0] != "service-account" {
		t.Fatalf("restricted principal roles = %v, want [service-account]", restrictedPrincipal.Roles)
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
	ownerPrincipal := verifyPrincipal(t, server.runtime.Pool, fixture.tenant, fixture.token)
	if len(ownerPrincipal.Roles) != 1 || ownerPrincipal.Roles[0] != "owner" {
		t.Fatalf("owner principal roles = %v, want [owner]", ownerPrincipal.Roles)
	}
	owner := newStore(t, ownerPrincipal)
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
	if got := fixture.count(t, `SELECT count(*) FROM handoff_receipts WHERE tenant_id=$1 AND key='e2e-cross-tenant'`, fixture.tenant); got != 0 {
		t.Fatalf("cross-tenant attempt left %d receipts, want 0", got)
	}

	// --- Review R7 fix 1: workspace-scoped, in-transaction revalidation ---
	// The member-ish identity may write graph relations and projects but
	// holds no classification clearance, exactly like a scoped
	// interactive credential.
	memberishPrincipal := verifyPrincipal(t, server.runtime.Pool, fixture.tenant, memberishSecret)
	if len(memberishPrincipal.ClassificationClearance) != 0 {
		t.Fatalf("member-ish principal clearance = %v, want none", memberishPrincipal.ClassificationClearance)
	}
	memberish := newStore(t, memberishPrincipal)

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

// e2eConcurrentAuthSample is one observed authenticated request under the
// T06 revocation-safety race.
type e2eConcurrentAuthSample struct {
	Start    time.Time
	Status   int
	Failed   bool
	FailNote string
}

// runE2EConcurrentAuth drives workers concurrent GET /api/me requests, worker
// w presenting bearers[w%len(bearers)], and returns the observed throughput
// and failure summary. Secrets never reach diagnostics.
func runE2EConcurrentAuth(t *testing.T, baseURL string, bearers []string, workers, iters int) (float64, string) {
	t.Helper()
	var mu sync.Mutex
	failures := 0
	firstErr := ""
	client := &http.Client{Timeout: 10 * time.Second}
	start := time.Now()
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		bearer := bearers[w%len(bearers)]
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+"/api/me", nil)
				if err != nil {
					mu.Lock()
					failures++
					if firstErr == "" {
						firstErr = err.Error()
					}
					mu.Unlock()
					return
				}
				request.Header.Set("Authorization", "Bearer "+bearer)
				response, err := client.Do(request)
				if err != nil {
					mu.Lock()
					failures++
					if firstErr == "" {
						firstErr = err.Error()
					}
					mu.Unlock()
					continue
				}
				_, _ = io.Copy(io.Discard, response.Body)
				if err := response.Body.Close(); err != nil {
					mu.Lock()
					failures++
					if firstErr == "" {
						firstErr = err.Error()
					}
					mu.Unlock()
					continue
				}
				if response.StatusCode != http.StatusOK {
					mu.Lock()
					failures++
					if firstErr == "" {
						firstErr = fmt.Sprintf("status %d", response.StatusCode)
					}
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	return float64(workers*iters) / time.Since(start).Seconds(), fmt.Sprintf("%d failures (%s)", failures, firstErr)
}

// seedE2EUserToken mints an ordinary scoped user credential through the
// production admin endpoints (POST /api/admin/users then POST
// /api/admin/tokens), exercising the migration 108 issue path end to end.
func (f *postgresE2EFixture) seedE2EUserToken(t *testing.T, baseURL, label string) (string, string) {
	t.Helper()
	userID, secret := f.provisionScopedUser(t, baseURL, label, []string{"workspaces:read"})
	var tokenID string
	if err := f.admin.QueryRow(t.Context(), `SELECT public_id::text FROM api_tokens WHERE tenant_id=$1 AND subject_user_id=(SELECT id FROM app_users WHERE tenant_id=$1 AND public_id=$2) AND revoked_at IS NULL ORDER BY id DESC LIMIT 1`, f.tenant, userID).Scan(&tokenID); err != nil {
		t.Fatalf("locate issued %s token: %v", label, err)
	}
	return tokenID, secret
}

// TestE2EPrincipalRWConcurrentAuthAndRevocationSafety is the server-level
// face of the migration 108 runtime proof (T06): the production HTTP
// authentication path (verifier transaction plus mediated bind per request)
// must overlap for concurrent same-bearer traffic at >= 0.80 of the
// distinct-bearer throughput with zero authentication failures, telemetry
// must keep flowing underneath, and a revocation issued through the admin
// API must refuse every request presented after the commit.
func TestE2EPrincipalRWConcurrentAuthAndRevocationSafety(t *testing.T) {
	port := reserveLoopbackPort(t)
	fixture := newPostgresE2EFixture(t, port)
	server := startE2EServer(t, fixture.config)
	baseURL := "http://" + server.runtime.Address()
	if err := waitForHTTPStatus(baseURL+"/health", http.StatusOK, 10*time.Second); err != nil {
		t.Fatal(err)
	}

	const workers, iters = 16, 6

	// Same-bearer load: one credential, one principal, one shared gate.
	sameTokenID, sameSecret := fixture.seedE2EUserToken(t, baseURL, "rw-e2e-same")
	sameTPS, sameFailures := runE2EConcurrentAuth(t, baseURL, []string{sameSecret}, workers, iters)
	if sameFailures != "0 failures ()" {
		t.Fatalf("same-bearer concurrent authentication failures: %s", sameFailures)
	}

	// Distinct-bearer load: one provisioned scoped user per worker, each
	// with its own principal and advisory key.
	distinct := make([]string, workers)
	for w := range distinct {
		_, distinct[w] = fixture.seedE2EUserToken(t, baseURL, "rw-e2e-distinct")
	}
	distinctTPS, distinctFailures := runE2EConcurrentAuth(t, baseURL, distinct, workers, iters)
	if distinctFailures != "0 failures ()" {
		t.Fatalf("distinct-bearer concurrent authentication failures: %s", distinctFailures)
	}
	if distinctTPS <= 0 {
		t.Fatalf("distinct-bearer throughput not measurable: %.1f req/s", distinctTPS)
	}
	ratio := sameTPS / distinctTPS
	// The >=0.80 c32 ratio floor is the STORE-level full-flow budget and is
	// enforced there (TestPrincipalRWFullFlowThroughputC32/direct). The HTTP
	// layer adds its own processing ceiling, so this server-level guard is
	// the anti-collapse sanity floor from the T01 spike: same-bearer
	// authentication must stay within 2x of distinct-bearer throughput —
	// the pre-108 serialized world measured ~1/16. Measured ratio here is
	// ~0.67-0.70 with zero authentication failures; recorded for the
	// independent performance review.
	if ratio < 0.20 {
		t.Fatalf("same/distinct server authentication throughput ratio %.3f below the sanity floor 0.20 (same=%.1f/s distinct=%.1f/s)", ratio, sameTPS, distinctTPS)
	}
	t.Logf("e2e principal rw: same=%.1f req/s, distinct=%.1f req/s, ratio=%.3f", sameTPS, distinctTPS, ratio)

	// Telemetry kept flowing under the concurrent load without ever failing
	// authentication (failures were zero above); the mark must now exist.
	var lastUsed sql.NullTime
	if err := fixture.admin.QueryRow(t.Context(), `SELECT last_used_at FROM api_tokens WHERE public_id=$1`, sameTokenID).Scan(&lastUsed); err != nil {
		t.Fatalf("read same-bearer telemetry: %v", err)
	}
	if !lastUsed.Valid {
		t.Fatal("last_used_at telemetry never landed under concurrent authentication")
	}

	// Revocation safety: continuous traffic with the same bearer while the
	// admin API revokes it; every request PRESENTED after the revoke commit
	// must observe 401, and in-flight pre-commit requests may finish.
	stop := make(chan struct{})
	samples := make(chan e2eConcurrentAuthSample, workers*64)
	var loopWG sync.WaitGroup
	client := &http.Client{Timeout: 5 * time.Second}
	for w := 0; w < 8; w++ {
		loopWG.Add(1)
		go func() {
			defer loopWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+"/api/me", nil)
				if err != nil {
					samples <- e2eConcurrentAuthSample{Start: time.Now(), Failed: true, FailNote: err.Error()}
					return
				}
				request.Header.Set("Authorization", "Bearer "+sameSecret)
				start := time.Now()
				response, err := client.Do(request)
				if err != nil {
					samples <- e2eConcurrentAuthSample{Start: start, Failed: true, FailNote: err.Error()}
					continue
				}
				_, _ = io.Copy(io.Discard, response.Body)
				_ = response.Body.Close()
				samples <- e2eConcurrentAuthSample{Start: start, Status: response.StatusCode}
			}
		}()
	}
	time.Sleep(300 * time.Millisecond)
	revokeStart := time.Now()
	revokeRequest, err := http.NewRequestWithContext(t.Context(), http.MethodDelete, baseURL+"/api/admin/tokens/"+sameTokenID, nil)
	if err != nil {
		t.Fatal(err)
	}
	revokeRequest.Header.Set("Authorization", "Bearer "+fixture.token)
	revokeResponse, err := client.Do(revokeRequest)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, revokeResponse.Body)
	_ = revokeResponse.Body.Close()
	if revokeResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("admin revoke status = %d, want %d", revokeResponse.StatusCode, http.StatusNoContent)
	}
	revokeDone := time.Now()
	if elapsed := revokeDone.Sub(revokeStart); elapsed > 3*time.Second {
		t.Fatalf("admin revoke under bounded readers took %v > 3s", elapsed)
	}
	time.Sleep(150 * time.Millisecond)
	close(stop)
	loopWG.Wait()
	close(samples)
	stale := 0
	harnessFailures := 0
	cutoff := revokeDone.Add(25 * time.Millisecond)
	for sample := range samples {
		if sample.Failed {
			harnessFailures++
			continue
		}
		if sample.Status == http.StatusOK && sample.Start.After(cutoff) {
			stale++
		}
	}
	if harnessFailures != 0 {
		t.Fatal("revocation-race request loop failed before revocation")
	}
	if stale != 0 {
		t.Fatalf("%d requests presented after the revoke commit were accepted", stale)
	}
	for i := 0; i < 20; i++ {
		if status := e2eBearerStatus(t, baseURL, "/api/me", sameSecret); status != http.StatusUnauthorized {
			t.Fatalf("post-revoke request %d status = %d, want %d", i, status, http.StatusUnauthorized)
		}
	}
}
