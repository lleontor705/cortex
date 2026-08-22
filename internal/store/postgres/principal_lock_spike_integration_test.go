//go:build postgres_integration

// T01 pg-lock-spike: canonical SRW principal-lock protocol spike.
//
// This file is TEST-ONLY by contract (work unit pg-lock-spike). It codifies
// the shared-reader/exclusive-writer identity races that migration 108 must
// win before any migration file is written:
//
//   - Readers (verify/bind) overlap on one canonical transaction-scoped
//     shared advisory key; distinct actors never contend.
//   - Direct actor revoke, token revoke, token rotate and grant bootstrap
//     take the EXCLUSIVE advisory key BEFORE any row lock, drain in-flight
//     readers, queue new arrivals behind the writer, and finish bounded.
//     Zero stale post-commit accepts and zero deadlocks are tolerated.
//   - Rollback and context cancellation release transaction-scoped locks.
//   - A real transaction-mode PgBouncer proves no session lock/state
//     leakage and no backend affinity requirement.
//   - c32 same-principal vs distinct-principal full verify+bind throughput
//     ratio and gated-writer latency meet the contract budgets.
//
// The canonical key derivation and the advisory-before-row order are pinned
// statically and cryptographically against
// testdata/principal_lock_spike/canonical_protocol.sql. A PASS of these
// tests selects that key and order for migration 108; any failure blocks
// T05 (pg-migration-108).
//
// Required environment (fail-closed, never skip):
//
//	CORTEX_SPIKE_PG_ADMIN_DSN     superuser DSN on the spike PostgreSQL 16
//	                              cluster (fresh isolated databases are
//	                              created per run and left in place).
//	CORTEX_SPIKE_PGBOUNCER_DSN    DSN through a real transaction-mode
//	                              PgBouncer fronting the same cluster.
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
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/lleontor705/cortex/internal/migration"
	"github.com/lleontor705/cortex/testutil/postgrestest"
)

// canonicalProtocolFixtureChecksum pins the exact bytes (LF-normalized) of
// the canonical protocol fixture. Editing the fixture is a deliberate act
// that must re-pin this constant and re-justify the canonical key/order
// decision recorded in it.
const canonicalProtocolFixtureChecksum = "40d2b76f54562c62a44c2fb38bdbf823babc760b10c6b89be6d3dd87f87518a2"

// Spike budgets from the joined R1 contract. The c32 same/distinct floor is
// the contractual >= 0.5; the writer budgets bound drain under load.
const (
	srwWriterDrainBudget     = 5 * time.Second
	srwRollbackReleaseBudget = 750 * time.Millisecond
	srwPoolLeakBudget        = 2 * time.Second
	srwC32RatioFloor         = 0.5
	srwStaleAcceptEpsilon    = 25 * time.Millisecond
	srwC32Workers            = 32
	srwC32Iters              = 15
)

type srwHarness struct {
	t          *testing.T
	dbName     string
	direct     *pgxpool.Pool
	poolerDSN  string
	pooler     *pgxpool.Pool
	fixtureSQL string
}

var (
	srwOnce   sync.Once
	srwShared *srwHarness
)

func newSRWHarness(t *testing.T) *srwHarness {
	t.Helper()
	adminDSN := os.Getenv("CORTEX_SPIKE_PG_ADMIN_DSN")
	if adminDSN == "" {
		adminDSN = os.Getenv("CORTEX_TEST_POSTGRES_MIGRATION_DSN")
	}
	if adminDSN == "" {
		t.Fatal("CORTEX_SPIKE_PG_ADMIN_DSN or CORTEX_TEST_POSTGRES_MIGRATION_DSN is required for the principal-lock spike (spike PostgreSQL 16 superuser DSN)")
	}
	poolerDSN := os.Getenv("CORTEX_SPIKE_PGBOUNCER_DSN")
	if poolerDSN == "" {
		poolerDSN = os.Getenv("CORTEX_TEST_POSTGRES_DSN")
	}
	if poolerDSN == "" {
		t.Fatal("CORTEX_SPIKE_PGBOUNCER_DSN or CORTEX_TEST_POSTGRES_DSN is required for the principal-lock spike (transaction-mode PgBouncer DSN)")
	}
	srwOnce.Do(func() {
		h := &srwHarness{t: t, poolerDSN: poolerDSN}
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		admin, err := pgxpool.New(ctx, adminDSN)
		if err != nil {
			t.Fatalf("spike admin pool: %v", err)
		}
		defer admin.Close()
		if err := admin.Ping(ctx); err != nil {
			t.Fatalf("spike admin ping: %v (is cortex-spike-pg16 running?)", err)
		}
		dbName := fmt.Sprintf("cortex_spikelock_%d_%d", time.Now().UnixNano()%1_000_000_000_000, os.Getpid())
		if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{dbName}.Sanitize()); err != nil {
			t.Fatalf("create fresh spike database: %v", err)
		}
		h.dbName = dbName
		if err := postgrestest.EnsureMigrationRoles(ctx, adminDSN); err != nil {
			t.Fatalf("ensure migration roles: %v", err)
		}
		// The migration handle targets the fresh database through a parsed
		// config (never a rebuilt DSN string: pgx ConnString returns the
		// original URL and would silently target the maintenance database).
		adminConnCfg, err := pgx.ParseConfig(adminDSN)
		if err != nil {
			t.Fatalf("parse admin DSN: %v", err)
		}
		adminConnCfg.Database = dbName
		sqlDB := sql.OpenDB(stdlib.GetConnector(*adminConnCfg))
		if err := migration.ApplyPostgresServerMigrations(ctx, sqlDB); err != nil {
			_ = sqlDB.Close()
			t.Fatalf("apply server migrations to %s: %v", dbName, err)
		}
		if err := sqlDB.Close(); err != nil {
			t.Fatalf("close migration handle: %v", err)
		}
		directCfg, err := pgxpool.ParseConfig(adminDSN)
		if err != nil {
			t.Fatalf("parse direct DSN: %v", err)
		}
		directCfg.ConnConfig.Database = dbName
		directCfg.MaxConns = 48
		h.direct, err = pgxpool.NewWithConfig(ctx, directCfg)
		if err != nil {
			t.Fatalf("spike direct pool: %v", err)
		}
		fixture, err := os.ReadFile("testdata/principal_lock_spike/canonical_protocol.sql")
		if err != nil {
			t.Fatalf("read canonical protocol fixture: %v", err)
		}
		h.fixtureSQL = string(fixture)
		for i, statement := range strings.Split(h.fixtureSQL, "-- @statement") {
			statement = strings.TrimSpace(statement)
			if statement == "" {
				continue
			}
			if _, err := h.direct.Exec(ctx, statement); err != nil {
				t.Fatalf("install fixture statement %d: %v", i, err)
			}
		}
		poolerCfg, err := pgxpool.ParseConfig(poolerDSN)
		if err != nil {
			t.Fatalf("parse pooler DSN: %v", err)
		}
		// The spike PgBouncer is configured with a wildcard database entry,
		// so the pooler is re-pointed at the fresh isolated database the
		// same way the direct pool is.
		poolerCfg.ConnConfig.Database = dbName
		poolerCfg.MaxConns = 48
		h.pooler, err = pgxpool.NewWithConfig(ctx, poolerCfg)
		if err != nil {
			t.Fatalf("spike pooler pool: %v", err)
		}
		srwShared = h
	})
	if srwShared == nil {
		t.Fatal("spike harness initialization failed")
	}
	return srwShared
}

// spikeActor is the seeded identity fixture: one tenant, one app user, one
// actor_subjects row at grant version gv and one live api token, with the
// verify-minted provenance the SRW bind validates (same HMAC construction
// as migration 106 and mintBindingProvenance).
type spikeActor struct {
	tenant     uuid.UUID
	actor      uuid.UUID
	secret     string
	digest     []byte
	tokenID    uuid.UUID
	provenance string
	gv         int64
}

func (h *srwHarness) seedActor(t *testing.T, gv int64) spikeActor {
	t.Helper()
	a := spikeActor{tenant: uuid.New(), actor: uuid.New(), gv: gv}
	a.secret = "ctx_spike_" + a.actor.String()
	mac := hmac.New(sha256.New, []byte(a.tenant.String()))
	mac.Write([]byte(a.secret))
	a.digest = mac.Sum(nil)
	prefix := a.secret[:12] + ":" + hex.EncodeToString(a.digest)[:16]
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := h.direct.Exec(ctx, `INSERT INTO app_users(tenant_id,public_id,email,display_name) VALUES($1,$2,$3,$4)`,
		a.tenant, a.actor, "spike-"+a.actor.String()+"@cortex.test", a.actor.String()); err != nil {
		t.Fatalf("seed app user: %v", err)
	}
	if _, err := h.direct.Exec(ctx, `INSERT INTO actor_subjects(tenant_id,subject,actor_type,public_id,grant_digest,grant_version) VALUES($1,$2,'user',$3,$4,$5)`,
		a.tenant, a.actor.String(), a.actor, "spike-digest", gv); err != nil {
		t.Fatalf("seed actor subject: %v", err)
	}
	if err := h.direct.QueryRow(ctx, `INSERT INTO api_tokens(tenant_id,public_id,name,token_prefix,token_digest,subject_user_id,scopes,workspace_ids) VALUES($1,$2,'spike',$3,$4,(SELECT id FROM app_users WHERE tenant_id=$1 AND public_id=$5),'{}','{}') RETURNING public_id`,
		a.tenant, uuid.New(), prefix, a.digest, a.actor).Scan(&a.tokenID); err != nil {
		t.Fatalf("seed api token: %v", err)
	}
	a.provenance = spikeProvenance(a.tenant, a.actor, gv, a.tokenID, a.digest)
	return a
}

// spikeProvenance mirrors the 106 HMAC contract: MAC keyed by the token
// digest over tenant:actor:grant_version:token.
func spikeProvenance(tenant, actor uuid.UUID, gv int64, token uuid.UUID, digest []byte) string {
	mac := hmac.New(sha256.New, digest)
	mac.Write([]byte(tenant.String() + ":" + actor.String() + ":" + fmt.Sprintf("%d", gv) + ":" + token.String()))
	return "v1:" + token.String() + ":" + hex.EncodeToString(mac.Sum(nil))
}

type bindOutcome int

const (
	bindOK bindOutcome = iota
	bindRefused
	bindDeadlock
	bindOther
)

func classifyBindError(err error) bindOutcome {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return bindOther
	}
	switch pgErr.Code {
	case "40P01":
		return bindDeadlock
	case "28000":
		return bindRefused
	default:
		return bindOther
	}
}

type srwBindResult struct {
	Outcome bindOutcome
	Err     error
	Bound   time.Time // after the bind call returned inside the transaction
	Done    time.Time // after commit (or failure)
}

// bindFlow executes one full verify+bind read transaction: begin, canonical
// shared-gate bind, optional hold at the pause point, commit. onHold fires
// while the shared advisory lock is held, before the transaction ends.
func (h *srwHarness) bindFlow(ctx context.Context, pool *pgxpool.Pool, a spikeActor, hold time.Duration, onHold func()) srwBindResult {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return srwBindResult{Outcome: bindOther, Err: err, Done: time.Now()}
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `SELECT public.spike_srw_bind($1,$2,$3)`, a.actor, a.provenance, a.gv); err != nil {
		return srwBindResult{Outcome: classifyBindError(err), Err: err, Done: time.Now()}
	}
	res := srwBindResult{Bound: time.Now()}
	if onHold != nil {
		onHold()
	}
	if hold > 0 {
		select {
		case <-time.After(hold):
		case <-ctx.Done():
			return srwBindResult{Outcome: bindOther, Err: ctx.Err(), Done: time.Now()}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return srwBindResult{Outcome: classifyBindError(err), Err: err, Done: time.Now()}
	}
	res.Outcome = bindOK
	res.Done = time.Now()
	return res
}

// writerResult captures the gated-writer transaction timings.
type writerResult struct {
	Err    error
	Start  time.Time
	Commit time.Time
}

func (h *srwHarness) runWriter(ctx context.Context, pool *pgxpool.Pool, call func(ctx context.Context, tx pgx.Tx) error) writerResult {
	start := time.Now()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return writerResult{Err: err, Start: start, Commit: time.Now()}
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := call(ctx, tx); err != nil {
		return writerResult{Err: err, Start: start, Commit: time.Now()}
	}
	if err := tx.Commit(ctx); err != nil {
		return writerResult{Err: err, Start: start, Commit: time.Now()}
	}
	return writerResult{Start: start, Commit: time.Now()}
}

func (h *srwHarness) revokeActorCall(ctx context.Context, tx pgx.Tx, actor uuid.UUID) error {
	_, err := tx.Exec(ctx, `SELECT public.spike_srw_revoke_actor($1)`, actor)
	return err
}

func (h *srwHarness) revokeTokenCall(ctx context.Context, tx pgx.Tx, token uuid.UUID) error {
	_, err := tx.Exec(ctx, `SELECT public.spike_srw_revoke_token($1)`, token)
	return err
}

func (h *srwHarness) rotateTokenCall(ctx context.Context, tx pgx.Tx, token uuid.UUID, prefix string, digest []byte) (uuid.UUID, error) {
	var next uuid.UUID
	err := tx.QueryRow(ctx, `SELECT public.spike_srw_rotate_token($1,$2,$3)`, token, prefix, digest).Scan(&next)
	return next, err
}

func (h *srwHarness) bootstrapActorCall(ctx context.Context, tx pgx.Tx, actor uuid.UUID, grantDigest string) (int64, error) {
	var gv int64
	err := tx.QueryRow(ctx, `SELECT public.spike_srw_bootstrap_actor($1,$2)`, actor, grantDigest).Scan(&gv)
	return gv, err
}

func (h *srwHarness) deadlocks(t *testing.T) int64 {
	t.Helper()
	var n int64
	if err := h.direct.QueryRow(context.Background(), `SELECT deadlocks FROM pg_stat_database WHERE datname = current_database()`).Scan(&n); err != nil {
		t.Fatalf("read deadlock counter: %v", err)
	}
	return n
}

// TestPrincipalLockSpikeCanonicalKeyFixture pins the canonical protocol
// artifact: exact fixture bytes, one canonical key namespace, xact-only
// advisory calls, shared-gate-before-row order for readers, and
// exclusive-gate-before-any-row-lock order for every invalidator.
func TestPrincipalLockSpikeCanonicalKeyFixture(t *testing.T) {
	h := newSRWHarness(t)
	fixture := strings.ReplaceAll(h.fixtureSQL, "\r\n", "\n")
	sum := sha256.Sum256([]byte(fixture))
	if got := hex.EncodeToString(sum[:]); got != canonicalProtocolFixtureChecksum {
		t.Fatalf("canonical protocol fixture checksum drift: got %s want %s (re-pin only after re-justifying the canonical key/order)", got, canonicalProtocolFixtureChecksum)
	}
	// Only transaction-scoped advisory calls exist anywhere in the fixture.
	callRe := regexp.MustCompile(`pg_advisory[a-z_]*\s*\(`)
	shared, exclusive := 0, 0
	for _, call := range callRe.FindAllString(fixture, -1) {
		switch strings.TrimSpace(call) {
		case "pg_advisory_xact_lock_shared(":
			shared++
		case "pg_advisory_xact_lock(":
			exclusive++
		default:
			t.Fatalf("non-transaction-scoped advisory call in canonical fixture: %q", call)
		}
	}
	if shared != 1 || exclusive != 4 {
		t.Fatalf("canonical fixture advisory call counts drifted: shared=%d (want 1 reader), exclusive=%d (want 4 invalidators)", shared, exclusive)
	}
	statements := strings.Split(fixture, "-- @statement")
	if len(statements) != 7 { // leading prologue + six statements
		t.Fatalf("canonical fixture statement count drifted: %d sections", len(statements))
	}
	fnBody := func(name string) string {
		marker := "CREATE OR REPLACE FUNCTION " + name + "("
		start := strings.Index(fixture, marker)
		if start < 0 {
			t.Fatalf("canonical fixture is missing function %s", name)
		}
		end := len(fixture)
		if next := strings.Index(fixture[start+len(marker):], "-- @statement"); next >= 0 {
			end = start + len(marker) + next
		}
		return fixture[start:end]
	}
	// Canonical key: exactly one helper, one domain-prefixed namespace over
	// tenant+actor.
	key := fnBody("spike_principal_key")
	if !strings.Contains(key, "hashtextextended('cortex:principal:'") {
		t.Fatal("canonical key derivation must be hashtextextended over the 'cortex:principal:' domain prefix plus tenant and actor")
	}
	// Reader: shared gate strictly before the first FOR SHARE row lock, and
	// never the exclusive call.
	reader := fnBody("spike_srw_bind")
	if i := strings.Index(reader, "pg_advisory_xact_lock("); i >= 0 {
		t.Fatal("reader routine must not take the exclusive advisory lock")
	}
	sharedIdx := strings.Index(reader, "pg_advisory_xact_lock_shared(")
	shareIdx := strings.Index(reader, "FOR SHARE")
	if sharedIdx < 0 || shareIdx < 0 || sharedIdx > shareIdx {
		t.Fatal("reader routine must take the shared advisory gate before any FOR SHARE row lock")
	}
	// Writers: exclusive gate strictly before any row lock, never shared.
	rowLockRe := regexp.MustCompile(`\b(FOR UPDATE|UPDATE|DELETE|INSERT)\b`)
	for _, writer := range []string{"spike_srw_revoke_actor", "spike_srw_revoke_token", "spike_srw_rotate_token", "spike_srw_bootstrap_actor"} {
		body := fnBody(writer)
		exclIdx := strings.Index(body, "pg_advisory_xact_lock(")
		if exclIdx < 0 {
			t.Fatalf("writer %s must take the exclusive advisory lock", writer)
		}
		if strings.Contains(body, "pg_advisory_xact_lock_shared(") {
			t.Fatalf("writer %s must not take the shared advisory lock", writer)
		}
		firstRow := rowLockRe.FindStringIndex(body)
		if firstRow == nil {
			t.Fatalf("writer %s has no row-locking statement to order against", writer)
		}
		if exclIdx > firstRow[0] {
			t.Fatalf("writer %s must acquire the exclusive advisory lock BEFORE any row lock (advisory at %d, row lock at %d)", writer, exclIdx, firstRow[0])
		}
	}
	// Behavioral key properties on the live database.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tenant, actorA, actorB := uuid.New(), uuid.New(), uuid.New()
	keyOf := func(tenant, actor uuid.UUID) int64 {
		var k int64
		if err := h.direct.QueryRow(ctx, `SELECT public.spike_principal_key($1,$2)`, tenant, actor).Scan(&k); err != nil {
			t.Fatalf("derive canonical key: %v", err)
		}
		return k
	}
	keyA1 := keyOf(tenant, actorA)
	keyA2 := keyOf(tenant, actorA)
	if keyA1 != keyA2 {
		t.Fatal("canonical key is not deterministic")
	}
	if keyA1 == keyOf(tenant, actorB) {
		t.Fatal("distinct actors must derive distinct canonical keys")
	}
	if keyA1 == keyOf(uuid.New(), actorA) {
		t.Fatal("distinct tenants must derive distinct canonical keys")
	}
}

// TestPrincipalLockSpikeSRWSharedReadersOverlap proves same-actor readers
// hold the shared gate concurrently while distinct actors never contend.
func TestPrincipalLockSpikeSRWSharedReadersOverlap(t *testing.T) {
	h := newSRWHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	const readers = 8
	run := func(actors []spikeActor) {
		arrived := make(chan struct{}, readers)
		done := make(chan srwBindResult, readers)
		for i := 0; i < readers; i++ {
			go func(a spikeActor) {
				done <- h.bindFlow(ctx, h.direct, a, 800*time.Millisecond, func() { arrived <- struct{}{} })
			}(actors[i])
		}
		for i := 0; i < readers; i++ {
			select {
			case <-arrived:
			case <-time.After(5 * time.Second):
				t.Fatalf("only %d/%d readers reached the shared gate simultaneously (readers must overlap)", i, readers)
			}
		}
		for i := 0; i < readers; i++ {
			res := <-done
			if res.Outcome != bindOK {
				t.Fatalf("shared reader failed: outcome=%d err=%v", res.Outcome, res.Err)
			}
		}
	}
	same := h.seedActor(t, 1)
	run([]spikeActor{same, same, same, same, same, same, same, same})
	distinct := make([]spikeActor, readers)
	for i := range distinct {
		distinct[i] = h.seedActor(t, 1)
	}
	run(distinct)
	// A writer on one actor completes while an unrelated actor's reader
	// holds its own shared gate: distinct actors do not contend.
	unrelated := h.seedActor(t, 1)
	blocker := h.bindFlowAsync(ctx, h.direct, unrelated, 1200*time.Millisecond)
	<-blocker.ready
	start := time.Now()
	w := h.runWriter(ctx, h.direct, func(ctx context.Context, tx pgx.Tx) error {
		return h.revokeActorCall(ctx, tx, same.actor)
	})
	if w.Err != nil {
		t.Fatalf("uncontended writer failed: %v", w.Err)
	}
	if elapsed := w.Commit.Sub(start); elapsed > srwRollbackReleaseBudget {
		t.Fatalf("writer on actor A contended with reader on actor B: %v", elapsed)
	}
	res := <-blocker.done
	if res.Outcome != bindOK {
		t.Fatalf("unrelated reader disturbed by distinct-actor writer: outcome=%d err=%v", res.Outcome, res.Err)
	}
}

type asyncBind struct {
	ready chan struct{}
	done  chan srwBindResult
}

func (h *srwHarness) bindFlowAsync(ctx context.Context, pool *pgxpool.Pool, a spikeActor, hold time.Duration) asyncBind {
	out := asyncBind{ready: make(chan struct{}), done: make(chan srwBindResult, 1)}
	go func() {
		out.done <- h.bindFlow(ctx, pool, a, hold, func() { close(out.ready) })
	}()
	return out
}

// srwPausePointRace runs the deterministic race shape: one in-flight reader
// holds the shared gate, the writer queues on the exclusive gate, and a late
// reader arrives while the writer is still waiting.
type srwPausePointObservation struct {
	early  srwBindResult
	late   srwBindResult
	writer writerResult
}

func (h *srwHarness) pausePointRace(t *testing.T, pool *pgxpool.Pool, a spikeActor, writer func(ctx context.Context, tx pgx.Tx) error) srwPausePointObservation {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	early := h.bindFlowAsync(ctx, pool, a, 1500*time.Millisecond)
	select {
	case <-early.ready:
	case <-time.After(10 * time.Second):
		t.Fatal("early reader never acquired the shared gate")
	}
	writerDone := make(chan writerResult, 1)
	go func() { writerDone <- h.runWriter(ctx, pool, writer) }()
	time.Sleep(300 * time.Millisecond) // let the writer queue on the exclusive gate
	lateStart := time.Now()
	late := h.bindFlowAsync(ctx, pool, a, 0)
	w := <-writerDone
	lateRes := <-late.done
	earlyRes := <-early.done
	if lateStart.After(w.Commit) {
		t.Fatal("test harness bug: late reader started after the writer already committed; race window missed")
	}
	return srwPausePointObservation{early: earlyRes, late: lateRes, writer: w}
}

func assertNoSRWDeadlock(t *testing.T, label string, results ...srwBindResult) {
	t.Helper()
	for _, res := range results {
		if res.Outcome == bindDeadlock {
			t.Fatalf("%s deadlocked: %v", label, res.Err)
		}
	}
}

// TestPrincipalLockSpikeDirectActorRevokeRace races a direct actor revoke
// against in-flight and arriving binders.
func TestPrincipalLockSpikeDirectActorRevokeRace(t *testing.T) {
	h := newSRWHarness(t)
	a := h.seedActor(t, 1)
	obs := h.pausePointRace(t, h.direct, a, func(ctx context.Context, tx pgx.Tx) error {
		return h.revokeActorCall(ctx, tx, a.actor)
	})
	assertNoSRWDeadlock(t, "direct actor revoke race", obs.early, obs.late)
	if obs.writer.Err != nil {
		t.Fatalf("direct actor revoke failed: %v", obs.writer.Err)
	}
	if drain := obs.writer.Commit.Sub(obs.writer.Start); drain > srwWriterDrainBudget {
		t.Fatalf("direct actor revoke exceeded bounded-writer budget: %v", drain)
	}
	if obs.early.Outcome != bindOK {
		t.Fatalf("in-flight reader should have drained successfully before the writer committed: outcome=%d err=%v", obs.early.Outcome, obs.early.Err)
	}
	if obs.early.Done.After(obs.writer.Commit) {
		t.Fatal("mutual exclusion violated: reader committed after the exclusive writer committed")
	}
	if obs.late.Outcome != bindRefused {
		t.Fatalf("reader arriving behind the writer must be refused post-revoke, got outcome=%d err=%v", obs.late.Outcome, obs.late.Err)
	}
	for i := 0; i < 8; i++ {
		if res := h.bindFlow(context.Background(), h.direct, a, 0, nil); res.Outcome != bindRefused {
			t.Fatalf("stale post-commit accept after direct actor revoke: attempt %d outcome=%d err=%v", i, res.Outcome, res.Err)
		}
	}
}

// TestPrincipalLockSpikeTokenRevokeRace races a token revocation against
// binders presenting that token's provenance.
func TestPrincipalLockSpikeTokenRevokeRace(t *testing.T) {
	h := newSRWHarness(t)
	a := h.seedActor(t, 1)
	obs := h.pausePointRace(t, h.direct, a, func(ctx context.Context, tx pgx.Tx) error {
		return h.revokeTokenCall(ctx, tx, a.tokenID)
	})
	assertNoSRWDeadlock(t, "token revoke race", obs.early, obs.late)
	if obs.writer.Err != nil {
		t.Fatalf("token revoke failed: %v", obs.writer.Err)
	}
	if drain := obs.writer.Commit.Sub(obs.writer.Start); drain > srwWriterDrainBudget {
		t.Fatalf("token revoke exceeded bounded-writer budget: %v", drain)
	}
	if obs.early.Outcome != bindOK {
		t.Fatalf("in-flight reader should have drained successfully: outcome=%d err=%v", obs.early.Outcome, obs.early.Err)
	}
	if obs.late.Outcome != bindRefused {
		t.Fatalf("late reader must be refused after token revocation, got outcome=%d err=%v", obs.late.Outcome, obs.late.Err)
	}
	if res := h.bindFlow(context.Background(), h.direct, a, 0, nil); res.Outcome != bindRefused {
		t.Fatalf("stale post-commit accept after token revoke: outcome=%d err=%v", res.Outcome, res.Err)
	}
	var revoked bool
	if err := h.direct.QueryRow(context.Background(), `SELECT revoked_at IS NOT NULL FROM api_tokens WHERE public_id=$1`, a.tokenID).Scan(&revoked); err != nil || !revoked {
		t.Fatalf("token row not revoked (err=%v revoked=%v)", err, revoked)
	}
}

// TestPrincipalLockSpikeRotateRace races a token rotation: old-cred binders
// must drain, then be refused; the new credential must bind.
func TestPrincipalLockSpikeRotateRace(t *testing.T) {
	h := newSRWHarness(t)
	a := h.seedActor(t, 1)
	newSecret := "ctx_spike_rot_" + uuid.NewString()
	mac := hmac.New(sha256.New, []byte(a.tenant.String()))
	mac.Write([]byte(newSecret))
	newDigest := mac.Sum(nil)
	newPrefix := newSecret[:12] + ":" + hex.EncodeToString(newDigest)[:16]
	var nextToken uuid.UUID
	obs := h.pausePointRace(t, h.direct, a, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		nextToken, err = h.rotateTokenCall(ctx, tx, a.tokenID, newPrefix, newDigest)
		return err
	})
	assertNoSRWDeadlock(t, "rotate race", obs.early, obs.late)
	if obs.writer.Err != nil {
		t.Fatalf("token rotate failed: %v", obs.writer.Err)
	}
	if drain := obs.writer.Commit.Sub(obs.writer.Start); drain > srwWriterDrainBudget {
		t.Fatalf("token rotate exceeded bounded-writer budget: %v", drain)
	}
	if obs.early.Outcome != bindOK {
		t.Fatalf("in-flight old-cred reader should have drained successfully: outcome=%d err=%v", obs.early.Outcome, obs.early.Err)
	}
	if obs.late.Outcome != bindRefused {
		t.Fatalf("late old-cred reader must be refused after rotation, got outcome=%d err=%v", obs.late.Outcome, obs.late.Err)
	}
	if res := h.bindFlow(context.Background(), h.direct, a, 0, nil); res.Outcome != bindRefused {
		t.Fatalf("stale post-commit accept of the rotated-away credential: outcome=%d err=%v", res.Outcome, res.Err)
	}
	rotated := a
	rotated.secret = newSecret
	rotated.digest = newDigest
	rotated.tokenID = nextToken
	rotated.provenance = spikeProvenance(rotated.tenant, rotated.actor, rotated.gv, nextToken, newDigest)
	if res := h.bindFlow(context.Background(), h.direct, rotated, 0, nil); res.Outcome != bindOK {
		t.Fatalf("rotated credential must bind after rotation: outcome=%d err=%v", res.Outcome, res.Err)
	}
}

// TestPrincipalLockSpikeBootstrapRace races grant re-provisioning
// (bootstrap): stale grant-version binders drain, then are refused; the new
// grant version binds.
func TestPrincipalLockSpikeBootstrapRace(t *testing.T) {
	h := newSRWHarness(t)
	a := h.seedActor(t, 1)
	var nextVersion int64
	obs := h.pausePointRace(t, h.direct, a, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		nextVersion, err = h.bootstrapActorCall(ctx, tx, a.actor, "spike-digest-v2")
		return err
	})
	assertNoSRWDeadlock(t, "bootstrap race", obs.early, obs.late)
	if obs.writer.Err != nil {
		t.Fatalf("grant bootstrap failed: %v", obs.writer.Err)
	}
	if drain := obs.writer.Commit.Sub(obs.writer.Start); drain > srwWriterDrainBudget {
		t.Fatalf("grant bootstrap exceeded bounded-writer budget: %v", drain)
	}
	if obs.early.Outcome != bindOK {
		t.Fatalf("in-flight reader should have drained at the old grant version: outcome=%d err=%v", obs.early.Outcome, obs.early.Err)
	}
	if obs.late.Outcome != bindRefused {
		t.Fatalf("late stale-version reader must be refused after bootstrap, got outcome=%d err=%v", obs.late.Outcome, obs.late.Err)
	}
	if res := h.bindFlow(context.Background(), h.direct, a, 0, nil); res.Outcome != bindRefused {
		t.Fatalf("stale post-commit accept after bootstrap: outcome=%d err=%v", res.Outcome, res.Err)
	}
	if nextVersion != a.gv+1 {
		t.Fatalf("bootstrap must advance the grant version: got %d want %d", nextVersion, a.gv+1)
	}
	rebound := a
	rebound.gv = nextVersion
	rebound.provenance = spikeProvenance(rebound.tenant, rebound.actor, nextVersion, rebound.tokenID, rebound.digest)
	if res := h.bindFlow(context.Background(), h.direct, rebound, 0, nil); res.Outcome != bindOK {
		t.Fatalf("new grant version must bind after bootstrap: outcome=%d err=%v", res.Outcome, res.Err)
	}
}

// TestPrincipalLockSpikeRollbackReleasesXactLocks proves rollback and
// context cancellation both release the transaction-scoped gate.
func TestPrincipalLockSpikeRollbackReleasesXactLocks(t *testing.T) {
	h := newSRWHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Rollback path.
	a := h.seedActor(t, 1)
	tx, err := h.direct.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT public.spike_srw_bind($1,$2,$3)`, a.actor, a.provenance, a.gv); err != nil {
		t.Fatalf("seed reader bind: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("reader rollback: %v", err)
	}
	w := h.runWriter(ctx, h.direct, func(ctx context.Context, tx pgx.Tx) error {
		return h.revokeActorCall(ctx, tx, a.actor)
	})
	if w.Err != nil {
		t.Fatalf("writer after reader rollback failed: %v", w.Err)
	}
	if elapsed := w.Commit.Sub(w.Start); elapsed > srwRollbackReleaseBudget {
		t.Fatalf("rollback did not release the shared gate promptly: %v", elapsed)
	}
	// Context cancellation path.
	b := h.seedActor(t, 1)
	readerCtx, readerCancel := context.WithCancel(ctx)
	hold := make(chan struct{})
	go func() {
		tx, err := h.direct.Begin(readerCtx)
		if err != nil {
			close(hold)
			return
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		if _, err := tx.Exec(readerCtx, `SELECT public.spike_srw_bind($1,$2,$3)`, b.actor, b.provenance, b.gv); err != nil {
			close(hold)
			return
		}
		close(hold)
		<-readerCtx.Done()
	}()
	<-hold
	time.Sleep(150 * time.Millisecond)
	readerCancel()
	w = h.runWriter(ctx, h.direct, func(ctx context.Context, tx pgx.Tx) error {
		return h.revokeActorCall(ctx, tx, b.actor)
	})
	if w.Err != nil {
		t.Fatalf("writer after reader cancellation failed: %v", w.Err)
	}
	if elapsed := w.Commit.Sub(w.Start); elapsed > srwRollbackReleaseBudget {
		t.Fatalf("cancellation did not release the shared gate promptly: %v", elapsed)
	}
}

// TestTransactionPoolNoLeakageNoAffinity runs the protocol through a real
// transaction-mode PgBouncer and proves backend rebinding leaves no
// advisory/session lock or tenant-context leakage.
func TestTransactionPoolNoLeakageNoAffinity(t *testing.T) {
	h := newSRWHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	a1 := h.seedActor(t, 1)
	a2 := h.seedActor(t, 1)
	// Two concurrently held transactions must observe two different server
	// backends: the protocol cannot require backend affinity.
	var pooledDB string
	if err := h.pooler.QueryRow(ctx, `SELECT current_database()`).Scan(&pooledDB); err != nil {
		t.Fatalf("probe pooled database: %v", err)
	}
	if pooledDB != h.dbName {
		t.Fatalf("pooled connection landed on database %q, want fresh spike database %q", pooledDB, h.dbName)
	}
	tx1, err := h.pooler.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx1.Rollback(context.Background()) }()
	tx2, err := h.pooler.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx2.Rollback(context.Background()) }()
	var pid1, pid2 int
	if err := tx1.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid1); err != nil {
		t.Fatal(err)
	}
	if err := tx2.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid2); err != nil {
		t.Fatal(err)
	}
	if pid1 == pid2 {
		t.Fatalf("transaction pool pinned both concurrent transactions to backend %d; backend affinity suspected", pid1)
	}
	if _, err := tx1.Exec(ctx, `SELECT public.spike_srw_bind($1,$2,$3)`, a1.actor, a1.provenance, a1.gv); err != nil {
		t.Fatalf("bind a1 through pooler: %v", err)
	}
	if _, err := tx2.Exec(ctx, `SELECT public.spike_srw_bind($1,$2,$3)`, a2.actor, a2.provenance, a2.gv); err != nil {
		t.Fatalf("bind a2 through pooler: %v", err)
	}
	if err := tx1.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tx2.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	// After commit the xact locks are gone even though the server
	// connection returns to the pool and may be rebound: an exclusive
	// writer through the same pooler must acquire immediately.
	w := h.runWriter(ctx, h.pooler, func(ctx context.Context, tx pgx.Tx) error {
		return h.revokeActorCall(ctx, tx, a1.actor)
	})
	if w.Err != nil {
		t.Fatalf("gated writer through transaction pooler failed: %v", w.Err)
	}
	if elapsed := w.Commit.Sub(w.Start); elapsed > srwPoolLeakBudget {
		t.Fatalf("transaction-scoped lock leaked across pooler rebinding: writer waited %v", elapsed)
	}
	// A second full race through the pooler (reader holds, writer drains,
	// late reader refused) proves the SRW semantics survive rebinding.
	obs := h.pausePointRace(t, h.pooler, a2, func(ctx context.Context, tx pgx.Tx) error {
		return h.revokeActorCall(ctx, tx, a2.actor)
	})
	assertNoSRWDeadlock(t, "pooled pause-point race", obs.early, obs.late)
	if obs.writer.Err != nil {
		t.Fatalf("pooled writer failed: %v", obs.writer.Err)
	}
	if obs.late.Outcome != bindRefused {
		t.Fatalf("pooled late reader must be refused post-revoke, got outcome=%d err=%v", obs.late.Outcome, obs.late.Err)
	}
	// No advisory locks of any kind remain on this database once every
	// transaction ended (session-scoped locks would survive here).
	time.Sleep(300 * time.Millisecond)
	var residue int
	if err := h.direct.QueryRow(ctx, `SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`).Scan(&residue); err != nil {
		t.Fatal(err)
	}
	if residue != 0 {
		t.Fatalf("%d advisory locks survived transaction end through the pooler (session leakage)", residue)
	}
	// Tenant-context hygiene: backend reuse must never leave two tenant
	// contexts for one backend; the bind's txid-scoped maintenance keeps
	// exactly the last transaction's row per backend.
	var dupes int
	if err := h.direct.QueryRow(ctx, `SELECT count(*) FROM (SELECT backend_pid FROM cortex_tenant_context GROUP BY backend_pid HAVING count(*) > 1) d`).Scan(&dupes); err != nil {
		t.Fatal(err)
	}
	if dupes != 0 {
		t.Fatalf("%d backends retain more than one tenant context row after pooled binds", dupes)
	}
}

// TestPrincipalLockSpikeC32ThroughputAndWriterLatency proves the c32
// same-principal vs distinct-principal full verify+bind throughput budget,
// bounded gated-writer latency under load, zero stale post-commit accepts
// and zero deadlocks.
func TestPrincipalLockSpikeC32ThroughputAndWriterLatency(t *testing.T) {
	h := newSRWHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	run := func(actors []spikeActor, label string) float64 {
		start := time.Now()
		var wg sync.WaitGroup
		for w := 0; w < srwC32Workers; w++ {
			wg.Add(1)
			go func(a spikeActor) {
				defer wg.Done()
				for i := 0; i < srwC32Iters; i++ {
					if res := h.bindFlow(ctx, h.direct, a, 2*time.Millisecond, nil); res.Outcome != bindOK {
						t.Errorf("%s worker bind failed: outcome=%d err=%v", label, res.Outcome, res.Err)
						return
					}
				}
			}(actors[w%len(actors)])
		}
		wg.Wait()
		elapsed := time.Since(start)
		tps := float64(srwC32Workers*srwC32Iters) / elapsed.Seconds()
		t.Logf("%s: %d txns in %v (%.0f txn/s)", label, srwC32Workers*srwC32Iters, elapsed, tps)
		return tps
	}
	distinct := make([]spikeActor, srwC32Workers)
	for i := range distinct {
		distinct[i] = h.seedActor(t, 1)
	}
	tpsDistinct := run(distinct, "c32 distinct-principal full flow")
	same := h.seedActor(t, 1)
	tpsSame := run([]spikeActor{same}, "c32 same-principal full flow")
	ratio := tpsSame / tpsDistinct
	t.Logf("c32 same/distinct throughput ratio: %.3f (floor %.2f)", ratio, srwC32RatioFloor)
	if ratio < srwC32RatioFloor {
		t.Fatalf("same-principal throughput collapsed: ratio %.3f < floor %.2f (distinct %.0f txn/s, same %.0f txn/s)", ratio, srwC32RatioFloor, tpsDistinct, tpsSame)
	}
	// Gated writer under sustained c32 same-principal load.
	deadlocksBefore := h.deadlocks(t)
	type record struct {
		ts      time.Time
		outcome bindOutcome
	}
	var mu sync.Mutex
	records := make([]record, 0, 4096)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < srwC32Workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				res := h.bindFlow(ctx, h.direct, same, time.Millisecond, nil)
				mu.Lock()
				records = append(records, record{ts: res.Done, outcome: res.Outcome})
				mu.Unlock()
			}
		}()
	}
	time.Sleep(600 * time.Millisecond)
	w := h.runWriter(ctx, h.direct, func(ctx context.Context, tx pgx.Tx) error {
		return h.revokeActorCall(ctx, tx, same.actor)
	})
	close(stop)
	wg.Wait()
	if w.Err != nil {
		t.Fatalf("gated writer under c32 load failed: %v", w.Err)
	}
	if drain := w.Commit.Sub(w.Start); drain > srwWriterDrainBudget {
		t.Fatalf("gated writer under c32 load exceeded budget: %v", drain)
	}
	t.Logf("gated writer under c32 same-principal load drained in %v", w.Commit.Sub(w.Start))
	stale, deadlocked, served := 0, 0, 0
	for _, r := range records {
		switch r.outcome {
		case bindDeadlock:
			deadlocked++
		case bindOK:
			if r.ts.Before(w.Start) || r.ts.Before(w.Commit.Add(srwStaleAcceptEpsilon)) {
				served++
			} else {
				stale++
			}
		}
	}
	if deadlocked != 0 {
		t.Fatalf("%d deadlocked reader transactions under c32 load", deadlocked)
	}
	if stale != 0 {
		t.Fatalf("%d stale post-commit accepts after the gated writer committed", stale)
	}
	t.Logf("c32 load served %d successful binds before the writer committed; 0 stale accepts after", served)
	if delta := h.deadlocks(t) - deadlocksBefore; delta != 0 {
		t.Fatalf("server deadlock counter advanced by %d during the spike", delta)
	}
}
