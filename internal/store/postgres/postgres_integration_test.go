//go:build integration

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
	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/migration"
	servermigrations "github.com/lleontor705/cortex/migrations/v2"
)

// postgresHarness deliberately uses a real PostgreSQL connection and applies
// the same embedded W11 schema used by the migration runner. Tests are skipped
// when CORTEX_TEST_POSTGRES_DSN is absent, keeping the default zero-CGO suite
// deterministic.
type postgresHarness struct {
	t      *testing.T
	pool   *pgxpool.Pool
	tenant string
}

func newPostgresHarness(t *testing.T) *postgresHarness {
	t.Helper()
	dsn := os.Getenv("CORTEX_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("CORTEX_TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	p, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Ping(ctx); err != nil {
		p.Close()
		t.Fatal(err)
	}
	h := &postgresHarness{t: t, pool: p, tenant: ""}
	t.Cleanup(func() { p.Close() })
	if _, err := p.Exec(ctx, `CREATE TABLE IF NOT EXISTS cortex_server_migrations (version integer PRIMARY KEY, name text NOT NULL, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		t.Fatalf("create migration ledger: %v", err)
	}
	if _, err := p.Exec(ctx, servermigrations.ServerSQL); err != nil {
		t.Fatalf("apply W11 server schema: %v", err)
	}
	return h
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
	for _, table := range []string{"organizations", "workspaces", "projects", "sessions", "observations", "prompts", "edges", "entities", "observation_entities", "index_outbox", "actor_subjects", "cortex_server_migrations"} {
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

func TestPostgresW11MigrationLifecycle(t *testing.T) {
	newPostgresHarness(t)
	dsn := os.Getenv("CORTEX_TEST_POSTGRES_DSN")
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
	dsn := os.Getenv("CORTEX_TEST_POSTGRES_DSN")
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
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE cortex_app`); err != nil {
		t.Fatal(err)
	}
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
	if _, err := h.pool.Exec(ctx, `INSERT INTO organizations(tenant_id,name) VALUES($1,'test-org')`, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := h.pool.Exec(ctx, `INSERT INTO workspaces(tenant_id,organization_id,public_id,name) VALUES($1,(SELECT id FROM organizations WHERE tenant_id=$1),$2,'test-workspace')`, tenant, workspace); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(h.pool, &domain.TenantContext{TenantID: tenant.String(), WorkspaceID: workspace.String()}, domain.Principal{Subject: "opaque-user", Type: "user", OrgID: tenant.String(), WorkspaceIDs: []string{workspace.String()}})
	if err != nil {
		t.Fatal(err)
	}
	s := &domain.Session{Project: "repo", StartedAt: time.Now().UTC(), Summary: "integration"}
	if err := store.Sessions().Create(ctx, s); err != nil {
		t.Fatal(err)
	}
	o := &domain.Observation{SessionID: s.ID, Project: "repo", Scope: domain.ScopeProject, Source: domain.SourceManual, Type: domain.TypeManual, Title: "title", Content: "content", TopicKey: "topic"}
	if err := store.Observations().Save(ctx, o); err != nil {
		t.Fatal(err)
	}
	if o.PublicID == "" || o.ID == 0 {
		t.Fatalf("observation IDs not populated: %#v", o)
	}
	got, err := store.Observations().GetByPublicID(ctx, o.PublicID)
	if err != nil || got.PublicID != o.PublicID {
		t.Fatalf("opaque observation lookup: %v", err)
	}
	if err := store.Prompts().Save(ctx, &domain.Prompt{SessionID: s.ID, Project: "repo", Content: "prompt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Search().Search(ctx, "content", domain.SearchOptions{Project: "repo", Scope: domain.ScopeProject, Type: domain.TypeManual}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Observations().List(ctx, domain.ObservationFilter{Project: "repo", Scope: domain.ScopeProject, Source: domain.SourceManual, Type: domain.TypeManual}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Prompts().List(ctx, "repo", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Sessions().GetByID(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Sessions().End(ctx, s.ID, "done"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Sessions().List(ctx, "repo"); err != nil {
		t.Fatal(err)
	}
	otherSession := &domain.Session{Project: "other", StartedAt: time.Now().UTC()}
	if err := store.Sessions().Create(ctx, otherSession); err != nil {
		t.Fatal(err)
	}
	if err := store.Prompts().Save(ctx, &domain.Prompt{SessionID: otherSession.ID, Project: "other", Content: "other prompt"}); err != nil {
		t.Fatal(err)
	}
	if sessions, err := store.Sessions().List(ctx, "repo"); err != nil || len(sessions) != 1 {
		t.Fatalf("session project filter: len=%d err=%v", len(sessions), err)
	}
	if prompts, err := store.Prompts().List(ctx, "repo", 10); err != nil || len(prompts) != 1 {
		t.Fatalf("prompt project filter: len=%d err=%v", len(prompts), err)
	}
	failedObs := &domain.Observation{SessionID: s.ID, Project: "repo", Title: "uow-failure", Content: "failure", Type: domain.TypeBugfix}
	failedTx, err := store.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = store.WithinTx(ctx, failedTx.Handle(), func(txctx context.Context) error {
		if err := store.Observations().Save(txctx, failedObs); err != nil {
			return err
		}
		if err := store.Entities().SaveLinks(txctx, []*domain.EntityLink{{ObservationID: failedObs.ID, EntityType: domain.EntityConcept, EntityValue: "rollback-entity"}}); err != nil {
			return err
		}
		if err := store.Outbox().WithinTx(txctx, failedTx.Handle(), func(c context.Context) error { return store.Outbox().EnqueueInTx(c, failedObs.ID, "index", "model") }); err != nil {
			return err
		}
		return errors.New("final injected failure")
	})
	if err == nil {
		t.Fatal("UoW failure was not propagated")
	}
	_ = failedTx.Rollback()
	var n int
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM observations WHERE title='uow-failure'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rollback observation rows=%d err=%v", n, err)
	}
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM entities WHERE entity_key='rollback-entity'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rollback entity rows=%d err=%v", n, err)
	}
	successObs := &domain.Observation{SessionID: s.ID, Project: "repo", Title: "uow-success", Content: "success", Type: domain.TypeBugfix}
	successTx, err := store.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WithinTx(ctx, successTx.Handle(), func(txctx context.Context) error {
		if err := store.Observations().Save(txctx, successObs); err != nil {
			return err
		}
		if err := store.Entities().SaveLinks(txctx, []*domain.EntityLink{{ObservationID: successObs.ID, EntityType: domain.EntityConcept, EntityValue: "success-entity"}}); err != nil {
			return err
		}
		return store.Outbox().WithinTx(txctx, successTx.Handle(), func(c context.Context) error { return store.Outbox().EnqueueInTx(c, successObs.ID, "index", "model") })
	}); err != nil {
		_ = successTx.Rollback()
		t.Fatal(err)
	}
	if err := successTx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM observations WHERE title='uow-success'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("success observation rows=%d err=%v", n, err)
	}
	second := &domain.Observation{SessionID: s.ID, Project: "repo", Scope: domain.ScopeProject, Source: domain.SourceAI, Type: domain.TypeDecision, Title: "second", Content: "second content"}
	if err := store.Observations().Save(ctx, second); err != nil {
		t.Fatal(err)
	}
	edge := &domain.Edge{FromObsID: o.ID, ToObsID: second.ID, RelationType: domain.RelationReferences}
	if err := store.Graph().CreateEdge(ctx, edge); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Graph().GetEdge(ctx, edge.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Graph().GetEdgeByPublicID(ctx, edge.PublicID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Graph().GetEdgesForObservation(ctx, o.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Graph().GetRelated(ctx, o.ID, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Graph().GetEvolutionChain(ctx, o.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Graph().GetContradictions(ctx, time.Now().Add(-time.Hour), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Graph().CountEdgesByObservation(ctx, o.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Graph().CountAllEdges(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Observations().CountByRoot(ctx, o.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Entities().SaveLinks(ctx, []*domain.EntityLink{{ObservationID: o.ID, EntityType: domain.EntityConcept, EntityValue: "concept"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Entities().GetByObservation(ctx, o.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Entities().FindByEntity(ctx, domain.EntityConcept, "concept"); err != nil {
		t.Fatal(err)
	}
	if err := store.Entities().DeleteByObservation(ctx, o.ID); err != nil {
		t.Fatal(err)
	}
	unit, err := store.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = store.WithinTx(ctx, unit.Handle(), func(txctx context.Context) error {
		if err := store.Outbox().WithinTx(txctx, unit.Handle(), func(c context.Context) error { return store.Outbox().EnqueueInTx(c, o.ID, "index", "model") }); err != nil {
			return err
		}
		return errors.New("rollback")
	})
	if err == nil {
		t.Fatal("failure injection unexpectedly committed")
	}
	_ = unit.Rollback()
	committed, err := store.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WithinTx(ctx, committed.Handle(), func(txctx context.Context) error {
		return store.Outbox().WithinTx(txctx, committed.Handle(), func(c context.Context) error { return store.Outbox().EnqueueInTx(c, second.ID, "index", "model") })
	}); err != nil {
		_ = committed.Rollback()
		t.Fatal(err)
	}
	if err := committed.Commit(); err != nil {
		t.Fatal(err)
	}
	leased, err := store.Outbox().Lease(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(leased) > 0 {
		if err := store.Outbox().MarkFailed(ctx, leased[0].ID, errors.New("injected"), time.Now()); err != nil {
			t.Fatal(err)
		}
		if err := store.Outbox().DeadLetter(ctx, leased[0].ID, errors.New("dead")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Outbox().PendingCount(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Outbox().RecoverPending(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Search().Search(ctx, "", domain.SearchOptions{}); err == nil {
		t.Fatal("empty search accepted")
	}
	if _, err := store.Observations().GetByTopicKey(ctx, "repo", "topic"); err != nil {
		t.Fatal(err)
	}
	if err := store.Graph().UpdateEdge(ctx, edge); err != nil {
		t.Fatal(err)
	}
	if err := store.Graph().DeleteEdge(ctx, edge.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Observations().Delete(ctx, o.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Observations().GetByPublicID(ctx, o.PublicID); err == nil {
		t.Fatal("soft-deleted observation remained visible")
	}
}
