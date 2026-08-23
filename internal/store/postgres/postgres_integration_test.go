//go:build postgres_integration

package postgres

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lleontor705/cortex/v2/internal/authz"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/lifecycle"
	"github.com/lleontor705/cortex/v2/internal/identity"
	"github.com/lleontor705/cortex/v2/internal/migration"
)

// postgresHarness deliberately uses a real PostgreSQL connection and applies
// the complete embedded server migration line (100 through the current head)
// through the checksummed migration runner. The dedicated
// postgres_integration tag makes the database dependency explicit.
type postgresHarness struct {
	t      *testing.T
	pool   *pgxpool.Pool
	admin  *pgxpool.Pool
	tenant string
}

func appRoleGrantSQL(role string) string {
	return `GRANT cortex_app TO ` + pgx.Identifier{role}.Sanitize()
}

// mintBindingProvenance provisions — through the privileged migration
// handle only — the app_users, actor_subjects and api_tokens fixtures a
// mediated bind requires, and returns the harness secret plus the
// verify-minted binding provenance v1:<token uuid>:<hexmac> that
// cortex_bind_principal accepts for the actor at the given grant version.
// It mirrors the 106 HMAC construction exactly: token digest =
// HMAC-SHA256(keyed by the tenant UUID, over the secret); provenance MAC =
// HMAC-SHA256(keyed by the token digest, over
// tenant:actor:grant_version:token). Verifying the returned secret through
// the repository must reproduce the returned provenance byte for byte,
// proving the Go and SQL HMAC constructions agree.
func mintBindingProvenance(t *testing.T, h *postgresHarness, tenant, subject uuid.UUID, grantVersion int64, storedGrantDigest string) (string, string) {
	t.Helper()
	ctx := context.Background()
	secret := "ctx_harness_" + subject.String()
	digestMac := hmac.New(sha256.New, []byte(tenant.String()))
	digestMac.Write([]byte(secret))
	digest := digestMac.Sum(nil)
	if _, err := h.admin.Exec(ctx, `INSERT INTO app_users(tenant_id,public_id,email,display_name) VALUES($1,$2,$3,$4) ON CONFLICT (tenant_id,public_id) DO UPDATE SET active=true,disabled_at=NULL`, tenant, subject, "harness-"+subject.String()+"@cortex.test", subject.String()); err != nil {
		t.Fatalf("seed app user: %v", err)
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO actor_subjects(tenant_id,subject,actor_type,public_id,grant_digest,grant_version) VALUES($1,$2,'user',$3,$4,$5) ON CONFLICT (tenant_id,subject) DO UPDATE SET public_id=EXCLUDED.public_id,active=true,revoked_at=NULL,grant_version=EXCLUDED.grant_version,grant_digest=EXCLUDED.grant_digest`, tenant, subject.String(), subject, storedGrantDigest, grantVersion); err != nil {
		t.Fatalf("seed actor: %v", err)
	}
	// The durable owner grant authorizes the harness actor for the
	// mediated definer routines that require a bound owner/admin caller.
	if _, err := h.admin.Exec(ctx, `INSERT INTO principal_grants(tenant_id,actor_public_id,grant_type,grant_value) VALUES($1,$2,'role','owner') ON CONFLICT (tenant_id,actor_public_id,grant_type,grant_value) DO NOTHING`, tenant, subject); err != nil {
		t.Fatalf("seed owner grant: %v", err)
	}
	var tokenID uuid.UUID
	// api_tokens enforces UNIQUE (tenant_id, token_prefix), so the stored
	// prefix cannot be the bare textual head (constant per secret scheme,
	// colliding on the second mint in one tenant). Mirror the 106 bootstrap
	// derivation instead: the 12-character head plus a colon and the first
	// 16 hex characters of the digest. Verification matches on
	// left(token_prefix, 12) plus exact digest equality, so the suffixed
	// form resolves exactly the same row while staying unique per subject.
	prefix := secret[:12] + ":" + hex.EncodeToString(digest)[:16]
	if err := h.admin.QueryRow(ctx, `INSERT INTO api_tokens(tenant_id,public_id,name,token_prefix,token_digest,subject_user_id,scopes,workspace_ids) SELECT $1,$2,'harness',$3,$4,u.id,'{}','{}' FROM app_users u WHERE u.tenant_id=$1 AND u.public_id=$5 ON CONFLICT (tenant_id,token_digest) DO UPDATE SET revoked_at=NULL,updated_at=now() RETURNING public_id`, tenant, uuid.New(), prefix, digest, subject).Scan(&tokenID); err != nil {
		t.Fatalf("seed harness token: %v", err)
	}
	provenanceMac := hmac.New(sha256.New, digest)
	provenanceMac.Write([]byte(tenant.String() + ":" + subject.String() + ":" + strconv.FormatInt(grantVersion, 10) + ":" + tokenID.String()))
	return secret, "v1:" + tokenID.String() + ":" + hex.EncodeToString(provenanceMac.Sum(nil))
}

// newAuthorizedTestStore builds an AuthorizedStore whose grant digest is
// verify-minted binding provenance, matching the mediated
// cortex_bind_principal contract installed by 106: a configured or
// arbitrary digest string can no longer authenticate a bind.
func newAuthorizedTestStore(t *testing.T, h *postgresHarness, tenant, workspace, subject uuid.UUID) *AuthorizedStore {
	t.Helper()
	_, provenance := mintBindingProvenance(t, h, tenant, subject, 1, "test-digest")
	p := domain.Principal{Subject: subject.String(), Type: "user", OrgID: tenant.String(), GrantDigest: provenance, GrantVersion: 1}
	if workspace != uuid.Nil {
		p.WorkspaceIDs = []string{workspace.String()}
	}
	ac := authz.AuthorizedContext{Principal: p, Tenant: domain.TenantContext{TenantID: tenant.String(), WorkspaceID: workspace.String()}, GrantDigest: p.GrantDigest}
	if workspace == uuid.Nil {
		ac.Tenant.WorkspaceID = ""
	}
	store, err := NewAuthorizedStore(h.pool, ac)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// listedUser locates one user by public id in a mediated listing. Listings
// can contain the harness binder alongside the users under test, and
// created_at ties make positional indexing non-deterministic, so assertions
// resolve their subject by identity instead of by order.
func listedUser(t *testing.T, users []identity.UserRecord, id string) identity.UserRecord {
	t.Helper()
	for i := range users {
		if users[i].ID == id {
			return users[i]
		}
	}
	t.Fatalf("user %s missing from listing", id)
	return identity.UserRecord{}
}

func newPostgresHarness(t *testing.T) *postgresHarness {
	t.Helper()
	dsn := os.Getenv("CORTEX_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("CORTEX_TEST_POSTGRES_DSN is required for postgres_integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	adminDSN := os.Getenv("CORTEX_TEST_POSTGRES_MIGRATION_DSN")
	if adminDSN == "" {
		t.Fatal("CORTEX_TEST_POSTGRES_MIGRATION_DSN is required for privileged PostgreSQL fixture setup")
	}
	adminConfig, err := pgxpool.ParseConfig(adminDSN)
	if err != nil {
		t.Fatalf("invalid migration DSN: %v", err)
	}
	adminPool, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := adminPool.Ping(ctx); err != nil {
		adminPool.Close()
		t.Fatal(err)
	}
	// Apply the COMPLETE embedded server migration line (100..head) through
	// the checksummed migration runner in deterministic version order —
	// never a single raw baseline exec. A raw 100-only exec leaves 101-106
	// unapplied and, because migration 100 CREATE OR REPLACEs
	// cortex_bind_principal, re-running it on an already-migrated database
	// silently reverts the 106-mediated binder to the baseline definition,
	// making mediated identity/token results depend on which test ran first.
	adminDB, err := sql.Open("pgx", adminDSN)
	if err != nil {
		t.Fatalf("open migration runner database: %v", err)
	}
	if err := adminDB.PingContext(ctx); err != nil {
		_ = adminDB.Close()
		t.Fatalf("ping migration runner database: %v", err)
	}
	if err := migration.ApplyPostgresServerMigrations(ctx, adminDB); err != nil {
		_ = adminDB.Close()
		t.Fatalf("apply embedded server migration sequence: %v", err)
	}
	assertServerMigrationHead(t, adminDB)
	if err := adminDB.Close(); err != nil {
		t.Fatalf("close migration runner database: %v", err)
	}
	// The application DSN is a real login, never a superuser session that is
	// narrowed with SET ROLE. Grant only the schema role to that login. Table,
	// sequence, and function privileges remain defined by the migration.
	appConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("invalid CORTEX_TEST_POSTGRES_DSN: %v", err)
	}
	if _, err := adminPool.Exec(ctx, appRoleGrantSQL(appConfig.ConnConfig.User)); err != nil {
		t.Fatalf("grant application role to login %q: %v", appConfig.ConnConfig.User, err)
	}
	config := appConfig
	p, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Ping(ctx); err != nil {
		p.Close()
		t.Fatal(err)
	}
	h := &postgresHarness{t: t, pool: p, tenant: ""}
	var superuser bool
	if err := p.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname=current_user`).Scan(&superuser); err != nil {
		p.Close()
		t.Fatalf("inspect application login: %v", err)
	}
	if superuser {
		p.Close()
		t.Fatalf("application DSN login %q must be NOSUPERUSER", config.ConnConfig.User)
	}
	t.Cleanup(func() { p.Close() })
	// Keep the privileged pool separate. It is never passed to a repository or
	// authorization probe.
	h.admin = adminPool
	t.Cleanup(func() { h.admin.Close() })
	return h
}

func migrationDSN() string {
	if dsn := os.Getenv("CORTEX_TEST_POSTGRES_MIGRATION_DSN"); dsn != "" {
		return dsn
	}
	return os.Getenv("CORTEX_TEST_POSTGRES_DSN")
}

// assertServerMigrationHead proves the harness database carries exactly the
// complete embedded server migration line before any test logic runs: every
// migration 100..head is ledgered with a checksum-matching row, the ledger
// records no extra or missing versions, and the mediated identity path the
// store package depends on (the 106 verify-minted provenance machinery) is
// actually installed in the schema. It fails closed on any drift so a stale,
// partially migrated, or reverted database can never produce order-dependent
// results.
func assertServerMigrationHead(t *testing.T, db *sql.DB) {
	t.Helper()
	migrations, err := migration.NewPostgresServerMigrations()
	if err != nil {
		t.Fatalf("load embedded server migrations: %v", err)
	}
	head := 0
	for _, m := range migrations {
		if err := m.VerifyApplied(context.Background(), db); err != nil {
			t.Fatalf("server migration sequence incomplete: %v", err)
		}
		if m.Version() > head {
			head = m.Version()
		}
	}
	var ledgerHead, ledgerRows int
	if err := db.QueryRow(`SELECT max(version), count(*) FROM cortex_server_migrations`).Scan(&ledgerHead, &ledgerRows); err != nil {
		t.Fatalf("read migration ledger head: %v", err)
	}
	if ledgerHead != head || ledgerRows != len(migrations) {
		t.Fatalf("migration ledger head=%d rows=%d; want head=%d rows=%d (exactly versions 100..%d)", ledgerHead, ledgerRows, head, len(migrations), head)
	}
	// The mediated verify/binder contract from the head migration must be
	// present: every mediated identity/token test binds through
	// verify-minted provenance rather than a stored grant digest.
	var mediated bool
	if err := db.QueryRow(`SELECT to_regprocedure('public.cortex_verify_token_principal(text,bytea,text)') IS NOT NULL`).Scan(&mediated); err != nil {
		t.Fatalf("probe mediated verifier: %v", err)
	}
	if !mediated {
		t.Fatal("public.cortex_verify_token_principal(text,bytea,text) is absent; the mediated identity path from the migration head is not installed")
	}
}

func (h *postgresHarness) begin(t *testing.T) (pgx.Tx, context.Context) {
	t.Helper()
	ctx := context.Background()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	return tx, ctx
}

func TestPostgresW11SchemaConformance(t *testing.T) {
	h := newPostgresHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, table := range []string{"organizations", "workspaces", "projects", "sessions", "observations", "importance_scores", "prompts", "edges", "entities", "observation_entities", "index_outbox", "actor_subjects", "cortex_server_migrations"} {
		var exists bool
		if err := h.pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("missing W11 table %s", table)
		}
	}
	var rls bool
	if err := h.pool.QueryRow(ctx, `SELECT relforcerowsecurity FROM pg_class WHERE oid='public.observations'::regclass`).Scan(&rls); err != nil {
		t.Fatal(err)
	}
	if !rls {
		t.Fatal("observations must force RLS")
	}
}

func TestPostgresImportanceScoresAreTenantScoped(t *testing.T) {
	h := newPostgresHarness(t)
	var rls bool
	if err := h.pool.QueryRow(context.Background(), `SELECT relforcerowsecurity FROM pg_class WHERE oid='public.importance_scores'::regclass`).Scan(&rls); err != nil {
		t.Fatal(err)
	}
	if !rls {
		t.Fatal("importance_scores must force RLS")
	}
	var hasObservationFK bool
	if err := h.pool.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='public.importance_scores'::regclass AND confrelid='public.observations'::regclass)`).Scan(&hasObservationFK); err != nil {
		t.Fatal(err)
	}
	if !hasObservationFK {
		t.Fatal("importance_scores must reference observations")
	}
}

func TestPostgresScoringAndManualArchivalAreScoped(t *testing.T) {
	h := newPostgresHarness(t)
	ctx := context.Background()
	createStore := func(t *testing.T) (*AuthorizedStore, *domain.Observation) {
		tenant, workspace := uuid.New(), uuid.New()
		if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, tenant.String()); err != nil {
			t.Fatal(err)
		}
		if _, err := h.admin.Exec(ctx, `INSERT INTO workspaces(tenant_id,organization_id,public_id,name) VALUES($1,(SELECT id FROM organizations WHERE tenant_id=$1),$2,$3)`, tenant, workspace, workspace.String()); err != nil {
			t.Fatal(err)
		}
		store := newAuthorizedTestStore(t, h, tenant, workspace, uuid.New())
		session := &domain.Session{Project: "archive-project", StartedAt: time.Now().UTC()}
		if err := store.sessions().Create(ctx, session); err != nil {
			t.Fatal(err)
		}
		obs := &domain.Observation{SessionID: session.ID, Project: "archive-project", Type: domain.TypeManual, Title: "old", Content: "old"}
		if err := store.observations().Save(ctx, obs); err != nil {
			t.Fatal(err)
		}
		if _, err := h.admin.Exec(ctx, `UPDATE observations SET created_at=$1 WHERE id=$2`, time.Now().Add(-48*time.Hour), obs.ID); err != nil {
			t.Fatal(err)
		}
		return store, obs
	}
	storeA, obsA := createStore(t)
	storeB, obsB := createStore(t)
	if err := storeA.store.SetScore(ctx, obsA.ID, 0.25); err != nil {
		t.Fatal(err)
	}
	if err := storeB.store.SetScore(ctx, obsB.ID, 4.5); err != nil {
		t.Fatal(err)
	}
	if scoreB, err := storeB.store.GetScore(ctx, obsB.ID); err != nil || scoreB.Score != 4.5 {
		t.Fatalf("tenant B score=%+v err=%v", scoreB, err)
	}
	score, err := storeA.store.GetScore(ctx, obsA.ID)
	if err != nil || score.Score != 0.25 {
		t.Fatalf("score=%+v err=%v", score, err)
	}
	archivable, err := storeA.observations().ListArchivable(ctx, time.Now().Add(-24*time.Hour), 1, 10)
	if err != nil || len(archivable) != 1 || archivable[0].ID != obsA.ID {
		t.Fatalf("tenant A archivable=%v err=%v", archivable, err)
	}
	if got, err := storeB.observations().ListArchivable(ctx, time.Now().Add(-24*time.Hour), 1, 10); err != nil || len(got) != 0 {
		id := int64(0)
		if len(got) > 0 {
			id = got[0].ID
		}
		t.Fatalf("tenant B archivable=%v first_id=%d expected_obs=%d err=%v", got, id, obsB.ID, err)
	}
	service := lifecycle.NewArchivalService(storeA.observations(), lifecycle.ArchivalConfig{MaxAgeDays: 1, MinArchiveScore: 1, CheckInterval: time.Hour})
	service.SetNowFunc(time.Now)
	archived, err := service.RunArchivalCheck(ctx)
	if err != nil || archived != 1 {
		t.Fatalf("manual archival count=%d err=%v", archived, err)
	}
	if _, err := storeA.observations().GetByID(ctx, obsA.ID); err == nil {
		t.Fatal("archived observation remained visible")
	}
}

func TestPostgresW11MigrationLifecycle(t *testing.T) {
	newPostgresHarness(t)
	dsn := migrationDSN()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("close migration database: %v", closeErr)
		}
	}()
	m, err := migration.NewPostgresServerMigration()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(context.Background(), db); err != nil {
		t.Fatalf("idempotent apply: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM cortex_server_migrations WHERE version=$1`, m.Version()).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("ledger rows=%d", n)
	}
}

func TestPostgresW11ChecksumLockAndDown(t *testing.T) {
	newPostgresHarness(t)
	dsn := migrationDSN()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	m, err := migration.NewPostgresServerMigration()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE cortex_server_migrations SET checksum='tampered' WHERE version=$1`, m.Version()); err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(context.Background(), db); err == nil {
		t.Fatal("checksum mismatch was accepted")
	}
	if _, err := db.Exec(`UPDATE cortex_server_migrations SET checksum=$1 WHERE version=$2`, m.Checksum(), m.Version()); err != nil {
		t.Fatal(err)
	}
	lockTx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockTx.Exec(`SELECT pg_advisory_xact_lock(hashtext('cortex:v2:server-migrations'))`); err != nil {
		t.Fatal(err)
	}
	lockCtx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	lockDone := make(chan error, 1)
	go func() { lockDone <- m.Apply(lockCtx, db) }()
	if err := <-lockDone; err == nil {
		t.Fatal("advisory lock contention unexpectedly succeeded")
	}
	_ = lockTx.Rollback()
	// Down is forward-only (REM-MIG-001): the ledgered migration must be
	// rejected with errors.Is ErrForwardOnly and the database must remain
	// unchanged — no DDL, no DML, no ledger rewrite, no schema restore.
	before := w11ForwardOnlySnapshot(t, db)
	if err := m.Down(context.Background(), db); !errors.Is(err, migration.ErrForwardOnly) {
		t.Fatalf("Down err=%v; want errors.Is ErrForwardOnly", err)
	}
	if after := w11ForwardOnlySnapshot(t, db); after != before {
		t.Fatal("Down executed DDL/DML: schema, ledger, or data snapshot changed")
	}
}

// w11ForwardOnlySnapshot builds a deterministic digest of the W11 schema
// (public columns), the migration ledger, and tenant row counts for the
// zero-mutation proof of the Down forward-only policy.
func w11ForwardOnlySnapshot(t *testing.T, db *sql.DB) string {
	t.Helper()
	ctx := context.Background()
	var b strings.Builder

	columns, err := db.QueryContext(ctx, `
		SELECT table_name, column_name, data_type
		FROM information_schema.columns
		WHERE table_schema = 'public'
		ORDER BY table_name, column_name`)
	if err != nil {
		t.Fatalf("snapshot columns: %v", err)
	}
	for columns.Next() {
		var table, column, dataType string
		if err := columns.Scan(&table, &column, &dataType); err != nil {
			t.Fatalf("scan columns: %v", err)
		}
		fmt.Fprintf(&b, "col|%s|%s|%s\n", table, column, dataType)
	}
	if err := columns.Close(); err != nil {
		t.Fatalf("close columns snapshot rows: %v", err)
	}
	if err := columns.Err(); err != nil {
		t.Fatalf("iterate columns: %v", err)
	}

	ledger, err := db.QueryContext(ctx, `SELECT version, name, checksum FROM cortex_server_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("snapshot ledger: %v", err)
	}
	for ledger.Next() {
		var version int
		var name, checksum string
		if err := ledger.Scan(&version, &name, &checksum); err != nil {
			t.Fatalf("scan ledger: %v", err)
		}
		fmt.Fprintf(&b, "ledger|%d|%s|%s\n", version, name, checksum)
	}
	if err := ledger.Close(); err != nil {
		t.Fatalf("close ledger snapshot rows: %v", err)
	}
	if err := ledger.Err(); err != nil {
		t.Fatalf("iterate ledger: %v", err)
	}

	for _, table := range []string{"organizations", "workspaces", "projects", "sessions", "observations", "importance_scores", "prompts", "edges", "entities", "observation_entities", "index_outbox", "actor_subjects"} {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil {
			t.Fatalf("snapshot count(%s): %v", table, err)
		}
		fmt.Fprintf(&b, "count|%s|%d\n", table, count)
	}
	return b.String()
}

func TestPostgresW11ParameterizedInputIsData(t *testing.T) {
	h := newPostgresHarness(t)
	tx, ctx := h.begin(t)
	// This remains a data value; it must never alter the query structure.
	var got string
	err := tx.QueryRow(ctx, `SELECT $1::text`, `'; DROP TABLE observations; --`).Scan(&got)
	if err != nil || got == "" {
		t.Fatalf("parameterized value failed: %v", err)
	}
}

func TestPostgresW11SearchParameterCorpus(t *testing.T) {
	h := newPostgresHarness(t)
	tx, ctx := h.begin(t)
	queries := []string{`' OR 1=1 --`, `"quoted phrase"`, `comment /* */`, `unicode café 東京`, `emoji 🔐`, `backslash\\`, `topic:alpha`, `NEAR(foo bar)`}
	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			var parsed string
			if err := tx.QueryRow(ctx, `SELECT websearch_to_tsquery('simple',$1)::text`, query).Scan(&parsed); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPostgresW11AppRoleRequiresBoundTenant(t *testing.T) {
	h := newPostgresHarness(t)
	ctx := context.Background()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
			t.Errorf("rollback unbound-role transaction: %v", rollbackErr)
		}
	}()
	var tenant any
	if err := tx.QueryRow(ctx, `SELECT public.cortex_current_tenant()`).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	if tenant != nil {
		t.Fatalf("tenant context unexpectedly inherited: %v", tenant)
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM public.observations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("unbound app role saw %d rows", count)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('cortex.tenant_id',$1,false)`, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SELECT public.cortex_current_tenant()`).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	if tenant != nil {
		t.Fatalf("GUC spoof changed tenant context: %v", tenant)
	}
}

func TestPostgresW11RepositoryConformance(t *testing.T) {
	h := newPostgresHarness(t)
	tenant, workspace := uuid.New(), uuid.New()
	ctx := context.Background()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,'test-org')`, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.Exec(ctx, `INSERT INTO workspaces(tenant_id,organization_id,public_id,name) VALUES($1,(SELECT id FROM organizations WHERE tenant_id=$1),$2,'test-workspace')`, tenant, workspace); err != nil {
		t.Fatal(err)
	}
	store := newAuthorizedTestStore(t, h, tenant, workspace, uuid.New())
	s := &domain.Session{Project: "repo", StartedAt: time.Now().UTC(), Summary: "integration"}
	if err := store.sessions().Create(ctx, s); err != nil {
		t.Fatal(err)
	}
	o := &domain.Observation{SessionID: s.ID, Project: "repo", Scope: domain.ScopeProject, Source: domain.SourceManual, Type: domain.TypeManual, Title: "title", Content: "content", TopicKey: "topic"}
	if err := store.observations().Save(ctx, o); err != nil {
		t.Fatal(err)
	}
	if o.PublicID == "" || o.ID == 0 {
		t.Fatalf("observation IDs not populated: %#v", o)
	}
	got, err := store.observations().GetByPublicID(ctx, o.PublicID)
	if err != nil || got.PublicID != o.PublicID {
		t.Fatalf("opaque observation lookup: %v", err)
	}
	if err := store.prompts().Save(ctx, &domain.Prompt{SessionID: s.ID, Project: "repo", Content: "prompt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.search().Search(ctx, "content", domain.SearchOptions{Project: "repo", Scope: domain.ScopeProject, Type: domain.TypeManual}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.observations().List(ctx, domain.ObservationFilter{Project: "repo", Scope: domain.ScopeProject, Source: domain.SourceManual, Type: domain.TypeManual}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.prompts().List(ctx, "repo", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := store.sessions().GetByID(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.sessions().End(ctx, s.ID, "done"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.sessions().List(ctx, "repo"); err != nil {
		t.Fatal(err)
	}
	otherSession := &domain.Session{Project: "other", StartedAt: time.Now().UTC()}
	if err := store.sessions().Create(ctx, otherSession); err != nil {
		t.Fatal(err)
	}
	if err := store.prompts().Save(ctx, &domain.Prompt{SessionID: otherSession.ID, Project: "other", Content: "other prompt"}); err != nil {
		t.Fatal(err)
	}
	if sessions, err := store.sessions().List(ctx, "repo"); err != nil || len(sessions) != 1 {
		t.Fatalf("session project filter: len=%d err=%v", len(sessions), err)
	}
	if prompts, err := store.prompts().List(ctx, "repo", 10); err != nil || len(prompts) != 1 {
		t.Fatalf("prompt project filter: len=%d err=%v", len(prompts), err)
	}
	failedObs := &domain.Observation{SessionID: s.ID, Project: "repo", Title: "uow-failure", Content: "failure", Type: domain.TypeBugfix}
	failedTx, err := store.store.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = store.store.WithinTx(ctx, failedTx.Handle(), func(txctx context.Context) error {
		if err := store.observations().Save(txctx, failedObs); err != nil {
			return err
		}
		if err := store.entities().SaveLinks(txctx, []*domain.EntityLink{{ObservationID: failedObs.ID, EntityType: domain.EntityConcept, EntityValue: "rollback-entity"}}); err != nil {
			return err
		}
		if err := store.outbox().WithinTx(txctx, failedTx.Handle(), func(c context.Context) error { return store.outbox().EnqueueInTx(c, failedObs.ID, "index", "model") }); err != nil {
			return err
		}
		return errors.New("final injected failure")
	})
	if err == nil {
		t.Fatal("UoW failure was not propagated")
	}
	_ = failedTx.Rollback()
	verifyTx, err := store.store.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := verifyTx.Handle().(pgx.Tx).QueryRow(ctx, `SELECT count(*) FROM observations WHERE title='uow-failure'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rollback observation rows=%d err=%v", n, err)
	}
	if err := verifyTx.Handle().(pgx.Tx).QueryRow(ctx, `SELECT count(*) FROM entities WHERE entity_key='rollback-entity'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rollback entity rows=%d err=%v", n, err)
	}
	if err := verifyTx.Rollback(); err != nil {
		t.Fatal(err)
	}
	successObs := &domain.Observation{SessionID: s.ID, Project: "repo", Title: "uow-success", Content: "success", Type: domain.TypeBugfix}
	successTx, err := store.store.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.store.WithinTx(ctx, successTx.Handle(), func(txctx context.Context) error {
		if err := store.observations().Save(txctx, successObs); err != nil {
			return err
		}
		if err := store.entities().SaveLinks(txctx, []*domain.EntityLink{{ObservationID: successObs.ID, EntityType: domain.EntityConcept, EntityValue: "success-entity"}}); err != nil {
			return err
		}
		return store.outbox().WithinTx(txctx, successTx.Handle(), func(c context.Context) error { return store.outbox().EnqueueInTx(c, successObs.ID, "index", "model") })
	}); err != nil {
		_ = successTx.Rollback()
		t.Fatal(err)
	}
	if err := successTx.Commit(); err != nil {
		t.Fatal(err)
	}
	verifySuccessTx, err := store.store.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifySuccessTx.Handle().(pgx.Tx).QueryRow(ctx, `SELECT count(*) FROM observations WHERE title='uow-success'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("success observation rows=%d err=%v", n, err)
	}
	if err := verifySuccessTx.Rollback(); err != nil {
		t.Fatal(err)
	}
	second := &domain.Observation{SessionID: s.ID, Project: "repo", Scope: domain.ScopeProject, Source: domain.SourceAI, Type: domain.TypeDecision, Title: "second", Content: "second content"}
	if err := store.observations().Save(ctx, second); err != nil {
		t.Fatal(err)
	}
	edge := &domain.Edge{FromObsID: o.ID, ToObsID: second.ID, RelationType: domain.RelationReferences}
	if err := store.graph().CreateEdge(ctx, edge); err != nil {
		t.Fatal(err)
	}
	if _, err := store.graph().GetEdge(ctx, edge.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.graph().GetEdgeByPublicID(ctx, edge.PublicID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.graph().GetEdgesForObservation(ctx, o.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.graph().GetRelated(ctx, o.ID, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := store.graph().GetEvolutionChain(ctx, o.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.graph().GetContradictions(ctx, time.Now().Add(-time.Hour), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.graph().CountEdgesByObservation(ctx, o.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.graph().CountAllEdges(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.observations().CountByRoot(ctx, o.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.entities().SaveLinks(ctx, []*domain.EntityLink{{ObservationID: o.ID, EntityType: domain.EntityConcept, EntityValue: "concept"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.entities().GetByObservation(ctx, o.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.entities().FindByEntity(ctx, domain.EntityConcept, "concept"); err != nil {
		t.Fatal(err)
	}
	if err := store.entities().DeleteByObservation(ctx, o.ID); err != nil {
		t.Fatal(err)
	}
	unit, err := store.store.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = store.store.WithinTx(ctx, unit.Handle(), func(txctx context.Context) error {
		if err := store.outbox().WithinTx(txctx, unit.Handle(), func(c context.Context) error { return store.outbox().EnqueueInTx(c, o.ID, "index", "model") }); err != nil {
			return err
		}
		return errors.New("rollback")
	})
	if err == nil {
		t.Fatal("failure injection unexpectedly committed")
	}
	_ = unit.Rollback()
	committed, err := store.store.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.store.WithinTx(ctx, committed.Handle(), func(txctx context.Context) error {
		return store.outbox().WithinTx(txctx, committed.Handle(), func(c context.Context) error { return store.outbox().EnqueueInTx(c, second.ID, "index", "model") })
	}); err != nil {
		_ = committed.Rollback()
		t.Fatal(err)
	}
	if err := committed.Commit(); err != nil {
		t.Fatal(err)
	}
	leased, err := store.outbox().Lease(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(leased) > 0 {
		if err := store.outbox().MarkFailed(ctx, leased[0].ID, errors.New("injected"), time.Now()); err != nil {
			t.Fatal(err)
		}
		if err := store.outbox().DeadLetter(ctx, leased[0].ID, errors.New("dead")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.outbox().PendingCount(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.outbox().RecoverPending(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.search().Search(ctx, "", domain.SearchOptions{}); err == nil {
		t.Fatal("empty search accepted")
	}
	if _, err := store.observations().GetByTopicKey(ctx, "repo", "topic"); err != nil {
		t.Fatal(err)
	}
	if err := store.graph().UpdateEdge(ctx, edge); err != nil {
		t.Fatal(err)
	}
	if err := store.graph().DeleteEdge(ctx, edge.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.observations().Delete(ctx, o.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.observations().GetByPublicID(ctx, o.PublicID); err == nil {
		t.Fatal("soft-deleted observation remained visible")
	}
}

// TestPostgresUserRepositoryMediatedLifecycle pins REQ-IDP-009/010: user
// creation, listing, and activation execute through the mediated definer
// routines, stay transaction-atomic, and surface the historical public
// behavior (records, grant versions, not-found taxonomy).
func TestPostgresUserRepositoryMediatedLifecycle(t *testing.T) {
	h := newPostgresHarness(t)
	ctx := context.Background()
	tenant := uuid.New()
	if _, err := h.admin.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,$2)`, tenant, "user-org"); err != nil {
		t.Fatal(err)
	}
	// The admin caller carries the durable owner role so the ActionManage
	// gates on the public operations still front the mediated repositories.
	adminSubject := uuid.New()
	_, adminProvenance := mintBindingProvenance(t, h, tenant, adminSubject, 1, "test-digest")
	adminPrincipal := domain.Principal{Subject: adminSubject.String(), Type: "user", OrgID: tenant.String(), Roles: []string{"owner"}, GrantDigest: adminProvenance, GrantVersion: 1}
	admin, err := NewAuthorizedStore(h.pool, authz.AuthorizedContext{Principal: adminPrincipal, Tenant: domain.TenantContext{TenantID: tenant.String()}, GrantDigest: adminProvenance})
	if err != nil {
		t.Fatal(err)
	}

	created, err := admin.CreateUser(ctx, identity.UserCreate{
		Email: "Mediated@Cortex.Test", DisplayName: " Mediated User ",
		Roles: []string{"admin", "admin"}, Workspaces: []string{uuid.NewString()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.GrantVersion != 1 || !created.Active || created.Email != "mediated@cortex.test" || created.DisplayName != "Mediated User" {
		t.Fatalf("created record=%+v", created)
	}
	if created.CreatedAt.IsZero() {
		t.Fatal("created_at missing")
	}
	// Duplicate grants are canonicalized; provisioning is mediated and
	// audit-atomic, and the persisted actor digest is recomputed in SQL.
	var storedDigest string
	var storedVersion int64
	if err := h.admin.QueryRow(ctx, `SELECT grant_digest,grant_version FROM actor_subjects WHERE tenant_id=$1 AND public_id=$2`, tenant, created.ID).Scan(&storedDigest, &storedVersion); err != nil {
		t.Fatalf("mediated provisioning left no actor row: %v", err)
	}
	if storedVersion != 1 || len(storedDigest) != 64 {
		// The digest value itself is never printable in a diagnostic; its
		// length carries the same assertion strength without the secret.
		t.Fatalf("stored actor digest length=%d version=%d", len(storedDigest), storedVersion)
	}
	var grantCount int
	if err := h.admin.QueryRow(ctx, `SELECT count(*) FROM principal_grants WHERE tenant_id=$1 AND actor_public_id=$2`, tenant, created.ID).Scan(&grantCount); err != nil {
		t.Fatal(err)
	}
	if grantCount != 2 {
		t.Fatalf("persisted grants=%d, want 2 (role+workspace after dedup)", grantCount)
	}

	// The mediated listing is app_users-driven and keeps every user whose
	// provisioned actor reads back, so the harness binder itself (a seeded
	// app_user with an owner grant) lists alongside the created user:
	// exactly two, located by public id since created_at ties make the
	// ordering non-deterministic.
	users, err := admin.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 {
		t.Fatalf("listed users=%d, want 2 (harness binder + created)", len(users))
	}
	got := listedUser(t, users, created.ID)
	if got.Email != created.Email || got.GrantVersion != 1 {
		t.Fatalf("listed user=%+v", got)
	}
	if len(got.Roles) != 1 || got.Roles[0] != "admin" || len(got.Workspaces) != 1 {
		t.Fatalf("listed grants roles=%v workspaces=%v", got.Roles, got.Workspaces)
	}

	if err := admin.SetUserActive(ctx, created.ID, false); err != nil {
		t.Fatal(err)
	}
	afterDisable, err := admin.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	disabled := listedUser(t, afterDisable, created.ID)
	if disabled.Active {
		t.Fatal("disabled user still listed active")
	}
	if disabled.GrantVersion != 2 {
		t.Fatalf("disable grant version=%d, want 2", disabled.GrantVersion)
	}
	if err := admin.SetUserActive(ctx, created.ID, true); err != nil {
		t.Fatal(err)
	}
	afterEnable, err := admin.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	enabled := listedUser(t, afterEnable, created.ID)
	if !enabled.Active || enabled.GrantVersion != 3 {
		t.Fatalf("enable record=%+v", enabled)
	}
	// Disabling again revokes live tokens of the actor in the same mediated
	// transaction; reactivation never revives them.
	issued, err := admin.tokens().Issue(ctx, identity.TokenIssue{Subject: created.ID, OrgID: tenant.String(), Scopes: []string{"read"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.SetUserActive(ctx, created.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.tokens().Verify(ctx, issued.Secret, "read"); !errors.Is(err, identity.ErrInvalidToken) {
		t.Fatalf("disabled actor token error=%v, want ErrInvalidToken", err)
	}

	if err := admin.SetUserActive(ctx, uuid.NewString(), false); err == nil || !strings.Contains(err.Error(), "user") {
		t.Fatalf("unknown user error=%v, want user not found", err)
	}
	if _, err := admin.CreateUser(ctx, identity.UserCreate{Email: "mediated@cortex.test", DisplayName: "Dup", Roles: []string{"admin"}}); err == nil {
		t.Fatal("duplicate email accepted")
	}
	if _, err := admin.CreateUser(ctx, identity.UserCreate{Email: "", DisplayName: "No Email", Roles: []string{"admin"}}); err == nil {
		t.Fatal("invalid user accepted")
	}
}
