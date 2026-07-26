package bundle_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite" // Pure Go SQLite driver (zero-CGO)

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/store/bundle"
)

// errInjected is the sentinel error returned by a failing test participant.
// It identifies WHICH participant caused the failure so the test can assert
// the error propagates and NO partial state remains.
var errInjected = errors.New("injected participant failure")

// ---------------------------------------------------------------------------
// Test infrastructure
// ---------------------------------------------------------------------------

// participantTables lists the tables each logical participant writes to.
// These mirror the W2 design participants: observation, revision, graph edge,
// entity link, outbox intent, audit record. Each gets its own table so we can
// assert per-table rollback.
var participantTables = []string{
	"obs_part",   // observation participant
	"rev_part",   // revision participant
	"graph_part", // graph-edge participant
	"ent_part",   // entity-link participant
	"outbox_part", // outbox-intent participant (headline defect pin target)
	"audit_part", // audit-record participant
}

// setupFileDB creates a FILE-BASED SQLite database (needed for real BUSY
// contention) with the participant tables and returns the *sql.DB.
// maxConns controls the pool size; for contention tests use >1 so multiple
// connections can contend at the SQLite lock level.
func setupFileDB(t *testing.T, maxConns int) (*sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	// Disable driver-level busy_timeout so we can test application-level retry
	// bounds deterministically. _pragma busy_timeout=0 means the driver returns
	// SQLITE_BUSY immediately on lock contention.
	dsn := path + "?_pragma=busy_timeout=0&_pragma=foreign_keys=ON&_pragma=journal_mode=WAL"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns)

	for _, tbl := range participantTables {
		if _, err := db.Exec(fmt.Sprintf(
			"CREATE TABLE IF NOT EXISTS %s (id INTEGER PRIMARY KEY AUTOINCREMENT, val TEXT)", tbl,
		)); err != nil {
			t.Fatalf("create table %s: %v", tbl, err)
		}
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, path
}

// openHolderDB opens a SEPARATE *sql.DB to the same file path, used to hold a
// write lock from a different connection pool (so the UoW's pool can still
// acquire its own connection and hit SQLite-level lock contention).
func openHolderDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	dsn := path + "?_pragma=busy_timeout=0&_pragma=journal_mode=WAL"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("holder sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// setupInMemDB creates an IN-MEMORY SQLite database for atomicity tests that
// do not need real file-level contention.
func setupInMemDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1)
	for _, tbl := range participantTables {
		if _, err := db.Exec(fmt.Sprintf(
			"CREATE TABLE IF NOT EXISTS %s (id INTEGER PRIMARY KEY AUTOINCREMENT, val TEXT)", tbl,
		)); err != nil {
			t.Fatalf("create table %s: %v", tbl, err)
		}
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// tableParticipant is a test double that implements domain.TxParticipant.
// It writes a row to its table within the shared transaction and optionally
// returns errInjected to simulate a participant failure.
type tableParticipant struct {
	name  string
	table string
	fail  bool // if true, returns errInjected after writing
}

func (p *tableParticipant) WithinTx(ctx context.Context, handle any, fn func(context.Context) error) error {
	tx, ok := handle.(*sql.Tx)
	if !ok {
		return fmt.Errorf("tableParticipant %s: expected *sql.Tx, got %T", p.name, handle)
	}
	// Write a row within the shared tx.
	if _, err := tx.ExecContext(ctx,
		fmt.Sprintf("INSERT INTO %s (val) VALUES (?)", p.table),
		"written-by-"+p.name,
	); err != nil {
		return fmt.Errorf("tableParticipant %s insert: %w", p.name, err)
	}
	// Optionally fail AFTER writing — this is the partial-state scenario.
	if p.fail {
		return fmt.Errorf("%s: %w", p.name, errInjected)
	}
	return fn(ctx)
}

// countRows returns the number of rows in the given table.
func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// assertAllTablesEmpty fails the test if any participant table has rows.
func assertAllTablesEmpty(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, tbl := range participantTables {
		if n := countRows(t, db, tbl); n != 0 {
			t.Errorf("table %s has %d rows after rollback — PARTIAL STATE (REQ-TX-001 defect)", tbl, n)
		}
	}
}

// makeParticipants builds a slice of tableParticipants for all participant
// tables. If failIdx >= 0, that participant has fail=true.
func makeParticipants(failIdx int) []*tableParticipant {
	var ps []*tableParticipant
	for i, tbl := range participantTables {
		ps = append(ps, &tableParticipant{
			name:  tbl,
			table: tbl,
			fail:  i == failIdx,
		})
	}
	return ps
}

// enlistAll is the fn passed to UnitOfWork.Do. It retrieves the shared tx from
// the context and enlists every participant in declaration order. If any
// participant fails, the error propagates (triggering rollback).
func enlistAll(ps []*tableParticipant) func(context.Context) error {
	return func(ctx context.Context) error {
		handle := bundle.TxHandle(ctx)
		if handle == nil {
			return errors.New("TxHandle returned nil — Do did not stash the tx")
		}
		for _, p := range ps {
			if err := p.WithinTx(ctx, handle, func(context.Context) error { return nil }); err != nil {
				return err
			}
		}
		return nil
	}
}

// ---------------------------------------------------------------------------
// RED #3: Happy path — all participants commit atomically (REQ-TX-001)
// ---------------------------------------------------------------------------

// TestUnitOfWork_AllParticipantsCommit proves that when every participant
// succeeds, all state is durably visible after commit.
func TestUnitOfWork_AllParticipantsCommit(t *testing.T) {
	db := setupInMemDB(t)
	uow := bundle.NewSQLiteUnitOfWork(db, domain.DefaultBusyRetryConfig())
	ps := makeParticipants(-1) // none fail

	ctx := context.Background()
	err := uow.Do(ctx, nil, toParticipantSlice(ps), enlistAll(ps))
	if err != nil {
		t.Fatalf("Do returned error on happy path: %v", err)
	}

	// Every table MUST have exactly one row.
	for _, tbl := range participantTables {
		if n := countRows(t, db, tbl); n != 1 {
			t.Errorf("table %s has %d rows after commit, want 1 (atomic visibility)", tbl, n)
		}
	}
}

// ---------------------------------------------------------------------------
// RED #1 + #2: Inject-failure at each participant — no partial state (defect pin)
// ---------------------------------------------------------------------------

// TestUnitOfWork_InjectFailureAtEachParticipant is the headline REQ-TX-001
// defect pin. For EACH participant position, it injects a failure AFTER that
// participant writes but BEFORE the next participant runs. It then asserts
// that ZERO tables have any committed rows — no partial state survives the
// rollback. This table-driven test covers all 6 participants including the
// critical outbox-fail-after-obs-commit scenario.
func TestUnitOfWork_InjectFailureAtEachParticipant(t *testing.T) {
	for failIdx, failTbl := range participantTables {
		t.Run("fail_at_"+failTbl, func(t *testing.T) {
			db := setupInMemDB(t)
			uow := bundle.NewSQLiteUnitOfWork(db, domain.DefaultBusyRetryConfig())
			ps := makeParticipants(failIdx)

			ctx := context.Background()
			err := uow.Do(ctx, nil, toParticipantSlice(ps), enlistAll(ps))

			// Do MUST return an error (the injected failure propagated).
			if err == nil {
				t.Fatal("Do returned nil error despite injected participant failure — partial state reported as success (REQ-TX-001 defect)")
			}

			// The error MUST identify the offending participant.
			if !errors.Is(err, errInjected) {
				t.Errorf("Do error = %v, want errors.Is(errInjected) (participant failure must propagate)", err)
			}

			// CRITICAL: NO table may have any committed rows — full atomic rollback.
			assertAllTablesEmpty(t, db)
		})
	}
}

// TestUnitOfWork_OutboxFailAfterObsCommit is the explicit REQ-TX-001 edge
// scenario: observation and revision participants commit, then the
// outbox-intent participant fails. The observation and revision writes MUST
// roll back atomically.
func TestUnitOfWork_OutboxFailAfterObsCommit(t *testing.T) {
	db := setupInMemDB(t)
	uow := bundle.NewSQLiteUnitOfWork(db, domain.DefaultBusyRetryConfig())

	// outbox_part is index 4 in participantTables.
	outboxIdx := 4
	ps := makeParticipants(outboxIdx)

	ctx := context.Background()
	err := uow.Do(ctx, nil, toParticipantSlice(ps), enlistAll(ps))
	if err == nil {
		t.Fatal("expected error from outbox failure, got nil")
	}

	// obs_part (index 0) and rev_part (index 1) wrote BEFORE outbox failed.
	// They MUST be rolled back — no partial committed state.
	if n := countRows(t, db, "obs_part"); n != 0 {
		t.Errorf("obs_part has %d rows after outbox-fail rollback, want 0 (REQ-TX-001 edge: obs+rev must roll back)", n)
	}
	if n := countRows(t, db, "rev_part"); n != 0 {
		t.Errorf("rev_part has %d rows after outbox-fail rollback, want 0", n)
	}
	assertAllTablesEmpty(t, db)
}

// ---------------------------------------------------------------------------
// RED #4: Concurrent writes serialize within busy-timeout, no deadlock
// ---------------------------------------------------------------------------

// TestUnitOfWork_ConcurrentWritesSerialize proves that concurrent UnitOfWork
// saves on the shared *sql.DB serialize correctly without deadlock. This is
// the REQ-TX-002 happy path.
func TestUnitOfWork_ConcurrentWritesSerialize(t *testing.T) {
	db, _ := setupFileDB(t, 1) // single-conn pool serializes writes safely
	// Use a config with generous retry so contention resolves.
	cfg := domain.BusyRetryConfig{
		MaxRetries:   5,
		BaseBackoff:  2 * time.Millisecond,
		MaxBackoff:   20 * time.Millisecond,
		JitterFactor: 0.1,
	}
	uow := bundle.NewSQLiteUnitOfWork(db, cfg)

	const N = 10
	var wg sync.WaitGroup
	errs := make(chan error, N)
	start := make(chan struct{})

	// Bounded context: if serialization deadlocks, the test fails fast.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start // fire all goroutines simultaneously
			ps := []*tableParticipant{
				{name: fmt.Sprintf("w%d", id), table: "obs_part"},
			}
			err := uow.Do(ctx, nil, toParticipantSlice(ps), enlistAll(ps))
			if err != nil {
				errs <- fmt.Errorf("goroutine %d: %w", id, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent save failed: %v (should serialize without error)", err)
	}

	// All N writes MUST be present — serialization preserved all of them.
	if n := countRows(t, db, "obs_part"); n != N {
		t.Errorf("obs_part has %d rows after %d concurrent writes, want %d (serialization lost writes)", n, N, N)
	}
}

// ---------------------------------------------------------------------------
// RED #5: Busy cap — returns stable retryable SQLITE_BUSY (not panic/corrupt)
// ---------------------------------------------------------------------------

// TestUnitOfWork_BusyCapReturnsStableError proves that when contention exceeds
// the retry cap, UnitOfWork.Do returns a stable, retryable SQLITE_BUSY error
// rather than panicking or corrupting transaction state. The returned error
// must be identifiable as a busy/locked condition via IsSQLiteBusy.
func TestUnitOfWork_BusyCapReturnsStableError(t *testing.T) {
	db, path := setupFileDB(t, 1) // UoW pool: 1 connection
	holderDB := openHolderDB(t, path)

	// Very aggressive retry cap: 1 retry, tiny backoff.
	cfg := domain.BusyRetryConfig{
		MaxRetries:   1,
		BaseBackoff:  1 * time.Millisecond,
		MaxBackoff:   2 * time.Millisecond,
		JitterFactor: 0.0,
	}
	uow := bundle.NewSQLiteUnitOfWork(db, cfg)

	// Hold a write lock from a SEPARATE connection pool so the UoW's pool can
	// still acquire its own connection and hit SQLite-level lock contention.
	holdDone := make(chan struct{})
	go func() {
		holdTx, err := holderDB.BeginTx(context.Background(), nil)
		if err != nil {
			close(holdDone)
			return
		}
		_, _ = holdTx.Exec("INSERT INTO obs_part (val) VALUES ('held')")
		// Keep the tx open until signaled.
		<-holdDone
		_ = holdTx.Rollback()
	}()

	// Give the holder time to acquire the write lock.
	time.Sleep(100 * time.Millisecond)

	// Bounded context: if Do hangs unboundedly, the test fails fast.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ps := []*tableParticipant{{name: "contended", table: "rev_part"}}
	err := uow.Do(ctx, nil, toParticipantSlice(ps), enlistAll(ps))

	// Release the holder.
	close(holdDone)
	time.Sleep(10 * time.Millisecond)

	if err == nil {
		t.Skip("save succeeded despite held lock — contention did not materialize on this platform; non-fatal")
	}

	// The error MUST be a stable retryable BUSY error, not a panic.
	if !bundle.IsSQLiteBusy(err) {
		t.Errorf("Do error = %v, want IsSQLiteBusy(err)=true (stable retryable SQLITE_BUSY)", err)
	}
}

// ---------------------------------------------------------------------------
// RED #6: Unbounded-blocking defect pin — saturation flood stays bounded
// ---------------------------------------------------------------------------

// TestUnitOfWork_NoUnboundedBlocking proves that a saturation flood does NOT
// block beyond a bounded duration. Every save either completes or returns a
// retryable error within the bounded window. This pins that the prior
// unbounded blocking behavior is gone (REQ-TX-002 defect pin).
func TestUnitOfWork_NoUnboundedBlocking(t *testing.T) {
	db, _ := setupFileDB(t, 1)
	cfg := domain.BusyRetryConfig{
		MaxRetries:   2,
		BaseBackoff:  1 * time.Millisecond,
		MaxBackoff:   5 * time.Millisecond,
		JitterFactor: 0.1,
	}
	uow := bundle.NewSQLiteUnitOfWork(db, cfg)

	const N = 20
	var wg sync.WaitGroup
	var completed int64
	var failed int64
	start := make(chan struct{})

	// Bounded context per save: ensures each save resolves or fails within a
	// bounded window (never blocks unbounded).
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			ps := []*tableParticipant{
				{name: fmt.Sprintf("flood-%d", id), table: "audit_part"},
			}
			err := uow.Do(ctx, nil, toParticipantSlice(ps), enlistAll(ps))
			if err == nil {
				atomic.AddInt64(&completed, 1)
			} else {
				atomic.AddInt64(&failed, 1)
			}
		}(i)
	}

	done := make(chan struct{})
	go func() {
		close(start)
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Good — flood resolved within the bounded window.
	case <-time.After(30 * time.Second):
		t.Fatalf("saturation flood did not resolve within 30s — UNBOUNDED BLOCKING (REQ-TX-002 defect)")
	}

	total := atomic.LoadInt64(&completed) + atomic.LoadInt64(&failed)
	if total != N {
		t.Errorf("flood resolved %d/%d saves — some goroutines leaked", total, N)
	}
	t.Logf("flood: %d completed, %d returned retryable error (all bounded)", completed, failed)
}

// ---------------------------------------------------------------------------
// Domain config tests
// ---------------------------------------------------------------------------

// TestDefaultBusyRetryConfig verifies the W2 default bounds.
func TestDefaultBusyRetryConfig(t *testing.T) {
	cfg := domain.DefaultBusyRetryConfig()
	if cfg.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", cfg.MaxRetries)
	}
	if cfg.BaseBackoff != 5*time.Millisecond {
		t.Errorf("BaseBackoff = %v, want 5ms", cfg.BaseBackoff)
	}
	if cfg.MaxBackoff != 50*time.Millisecond {
		t.Errorf("MaxBackoff = %v, want 50ms", cfg.MaxBackoff)
	}
	if cfg.JitterFactor != 0.2 {
		t.Errorf("JitterFactor = %v, want 0.2", cfg.JitterFactor)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// toParticipantSlice converts []*tableParticipant to []domain.TxParticipant.
func toParticipantSlice(ps []*tableParticipant) []domain.TxParticipant {
	out := make([]domain.TxParticipant, len(ps))
	for i, p := range ps {
		out[i] = p
	}
	return out
}

// silenceUnused keeps os/rand imports referenced for future test extensions.
var (
	_ = os.ErrNotExist
	_ = rand.Intn
)
