//go:build postgres_integration

package postgres

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lleontor705/cortex/internal/authz"
	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/domain/lifecycle"
	"github.com/lleontor705/cortex/internal/migration"
	servermigrations "github.com/lleontor705/cortex/migrations/v2"
)

// postgresHarness deliberately uses a real PostgreSQL connection and applies
// the same embedded W11 schema used by the migration runner. The dedicated
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

func newAuthorizedTestStore(t *testing.T, h *postgresHarness, tenant, workspace, subject uuid.UUID) *AuthorizedStore {
	t.Helper()
	ctx := context.Background()
	if _, err := h.admin.Exec(ctx, `INSERT INTO actor_subjects(tenant_id,subject,actor_type,public_id,grant_digest,grant_version) VALUES($1,$2,'test',$3,'test-digest',1) ON CONFLICT (tenant_id,subject) DO UPDATE SET public_id=EXCLUDED.public_id,active=true,revoked_at=NULL,grant_digest=EXCLUDED.grant_digest,grant_version=1`, tenant, subject.String(), subject); err != nil {
		t.Fatal(err)
	}
	p := domain.Principal{Subject: subject.String(), Type: "user", OrgID: tenant.String(), GrantDigest: "test-digest", GrantVersion: 1}
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

func newPostgresHarness(t *testing.T) *postgresHarness {
	t.Helper()
	dsn := os.Getenv("CORTEX_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("CORTEX_TEST_POSTGRES_DSN is required for postgres_integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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
	if _, err := adminPool.Exec(ctx, `CREATE TABLE IF NOT EXISTS cortex_server_migrations (version integer PRIMARY KEY, name text NOT NULL, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		t.Fatalf("create migration ledger: %v", err)
	}
	if _, err := adminPool.Exec(ctx, servermigrations.ServerSQL); err != nil {
		t.Fatalf("apply W11 server schema: %v", err)
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
	defer db.Close()
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
	defer db.Close()
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
	if err := m.Down(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := db.QueryRow(`SELECT to_regclass('public.observations') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("Down left server tables behind")
	}
	if err := m.Apply(context.Background(), db); err != nil {
		t.Fatal(err)
	}
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
	defer tx.Rollback(ctx)
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
