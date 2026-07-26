package embedding

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
	sqlitestore "github.com/lleontor705/cortex/internal/store/sqlite"

	_ "modernc.org/sqlite"
	"go.uber.org/goleak"
)

// ---------------------------------------------------------------------------
// Test infrastructure
// ---------------------------------------------------------------------------

// setupWorkerDB creates an in-memory DB with the v2 baseline schema and returns
// real outbox + observation stores for integration-level worker tests. The
// returned cleanup function closes the DB — callers MUST defer it (not
// t.Cleanup) so that in goleak tests db.Close runs BEFORE goleak.VerifyNone
// (LIFO defer ordering kills the database/sql.connectionOpener goroutine first).
func setupWorkerDB(t *testing.T) (*sql.DB, *sqlitestore.OutboxStore, *sqlitestore.Store, func()) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1)

	for _, stmt := range []string{
		`CREATE TABLE observations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT, type TEXT NOT NULL, title TEXT NOT NULL, content TEXT NOT NULL,
			project TEXT NOT NULL DEFAULT 'default', scope TEXT NOT NULL DEFAULT 'project',
			topic_key TEXT, normalized_hash TEXT,
			confidence REAL DEFAULT 1.0, source TEXT DEFAULT 'manual',
			tags TEXT, revision_count INTEGER DEFAULT 1, duplicate_count INTEGER DEFAULT 1,
			last_seen_at TEXT, created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')), deleted_at TEXT
		)`,
		`CREATE TABLE index_outbox (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			observation_id INTEGER NOT NULL,
			intent TEXT NOT NULL, model_info TEXT,
			status TEXT NOT NULL DEFAULT 'pending',
			attempts INTEGER NOT NULL DEFAULT 0, max_attempts INTEGER NOT NULL DEFAULT 5,
			next_retry_at TEXT, leased_at TEXT, completed_at TEXT, error TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE index_state (namespace TEXT PRIMARY KEY, coverage REAL NOT NULL, parity INTEGER DEFAULT 0, authority_digest TEXT, updated_at TEXT NOT NULL)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}
	cleanup := func() { _ = db.Close() }
	return db, sqlitestore.NewOutboxStore(db), sqlitestore.NewStore(db), cleanup
}

// insertTestObservation inserts an observation and returns its ID.
func insertTestObservation(t *testing.T, obsStore *sqlitestore.Store, title, content string) int64 {
	t.Helper()
	obs := &domain.Observation{Title: title, Content: content, Type: domain.TypeManual, Project: "test", Scope: "project"}
	if err := obsStore.Save(context.Background(), obs); err != nil {
		t.Fatalf("save obs: %v", err)
	}
	return obs.ID
}

// enqueueIntent enqueues a pending outbox intent for the given observation.
func enqueueIntent(t *testing.T, db *sql.DB, obsID int64, modelInfo string) int64 {
	t.Helper()
	res, err := db.Exec(
		`INSERT INTO index_outbox (observation_id, intent, model_info, status) VALUES (?, 'embed_upsert', ?, 'pending')`,
		obsID, modelInfo,
	)
	if err != nil {
		t.Fatalf("enqueue intent: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func outboxStatus(t *testing.T, db *sql.DB, id int64) string {
	t.Helper()
	var status string
	if err := db.QueryRow(`SELECT status FROM index_outbox WHERE id = ?`, id).Scan(&status); err != nil {
		t.Fatalf("query status %d: %v", id, err)
	}
	return status
}

// fakeEmbeddingService is a controllable embedding.Service fake.
type fakeEmbeddingService struct {
	dims      int
	model     string
	failCount int32 // atomic; number of Embed calls that fail
	callCount int32 // atomic
}

func (f *fakeEmbeddingService) Embed(_ context.Context, text string) ([]float32, error) {
	n := atomic.AddInt32(&f.callCount, 1)
	if n <= atomic.LoadInt32(&f.failCount) {
		return nil, fmt.Errorf("simulated embed failure (call %d)", n)
	}
	vec := make([]float32, f.dims)
	for i := range vec {
		vec[i] = float32(len(text) + i%100)
	}
	return vec, nil
}

func (f *fakeEmbeddingService) Dimensions() int { return f.dims }
func (f *fakeEmbeddingService) Model() string   { return f.model }

// fakeVectorWriter records upserts and can be configured to fail.
type fakeVectorWriter struct {
	mu      sync.Mutex
	upserts map[int64][]float32
	failErr error
}

func newFakeVectorWriter() *fakeVectorWriter {
	return &fakeVectorWriter{upserts: make(map[int64][]float32)}
}

func (f *fakeVectorWriter) StoreEmbedding(_ context.Context, observationID int64, embedding []float32, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failErr != nil {
		return f.failErr
	}
	cp := make([]float32, len(embedding))
	copy(cp, embedding)
	f.upserts[observationID] = cp
	return nil
}

func (f *fakeVectorWriter) has(obsID int64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.upserts[obsID]
	return ok
}

// fastWorkerConfig returns a WorkerConfig with short intervals for fast tests.
func fastWorkerConfig() WorkerConfig {
	return WorkerConfig{
		Concurrency:  1,
		PollInterval: 5 * time.Millisecond,
		LeaseBatch:   1,
		MaxBacklog:   1000,
		DrainTimeout: 5 * time.Second,
	}
}

// waitForStatus polls the outbox until the intent reaches the target status or
// the deadline expires.
func waitForStatus(t *testing.T, db *sql.DB, id int64, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if outboxStatus(t, db, id) == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := outboxStatus(t, db, id); got != want {
		t.Fatalf("intent %d status = %q, want %q (timed out after %v)", id, got, want, timeout)
	}
}

// ---------------------------------------------------------------------------
// Worker lifecycle + processing tests
// ---------------------------------------------------------------------------

func TestWorker_ProcessesIntent_EndToEnd(t *testing.T) {
	defer goleak.VerifyNone(t)
	db, outbox, obsStore, closeDB := setupWorkerDB(t)
	defer closeDB() // LIFO: runs before goleak

	obsID := insertTestObservation(t, obsStore, "test title", "test content")
	intentID := enqueueIntent(t, db, obsID, "test-model:768")

	embSvc := &fakeEmbeddingService{dims: 768, model: "test-model"}
	vecWriter := newFakeVectorWriter()

	w := NewWorker(outbox, obsStore, embSvc, vecWriter, fastWorkerConfig())
	cancel := w.Start(context.Background())
	defer cancel() // LIFO: runs before closeDB

	waitForStatus(t, db, intentID, sqlitestore.OutboxStatusComplete, 3*time.Second)
	if !vecWriter.has(obsID) {
		t.Fatal("vector was not upserted for the observation")
	}
}

func TestWorker_RestartRecovery_LeasedBecomesPendingAndProcesses(t *testing.T) {
	defer goleak.VerifyNone(t)
	db, outbox, obsStore, closeDB := setupWorkerDB(t)
	defer closeDB()

	obsID := insertTestObservation(t, obsStore, "crash recovery", "content after restart")
	res, err := db.Exec(
		`INSERT INTO index_outbox (observation_id, intent, status, attempts, max_attempts) VALUES (?, 'embed_upsert', 'leased', 1, 5)`,
		obsID,
	)
	if err != nil {
		t.Fatalf("insert leased intent: %v", err)
	}
	intentID, _ := res.LastInsertId()

	embSvc := &fakeEmbeddingService{dims: 768, model: "rec-model"}
	vecWriter := newFakeVectorWriter()

	w := NewWorker(outbox, obsStore, embSvc, vecWriter, fastWorkerConfig())
	cancel := w.Start(context.Background()) // Start triggers RecoverPending
	defer cancel()

	waitForStatus(t, db, intentID, sqlitestore.OutboxStatusComplete, 3*time.Second)
	if !vecWriter.has(obsID) {
		t.Fatal("vector not upserted after restart recovery")
	}
}

func TestWorker_ShutdownDrain_NoGoroutineLeak(t *testing.T) {
	// NOTE: goleak.VerifyNone is the defect pin — the legacy detached go func()
	// leaked a goroutine on shutdown. This test asserts ZERO goroutines remain
	// after the worker's cancel func returns (drain complete).
	defer goleak.VerifyNone(t)
	db, outbox, obsStore, closeDB := setupWorkerDB(t)
	defer closeDB()

	obsID := insertTestObservation(t, obsStore, "drain test", "content")
	enqueueIntent(t, db, obsID, "drain-model:768")

	embSvc := &fakeEmbeddingService{dims: 768, model: "drain-model"}
	vecWriter := newFakeVectorWriter()

	w := NewWorker(outbox, obsStore, embSvc, vecWriter, fastWorkerConfig())
	cancel := w.Start(context.Background())

	time.Sleep(50 * time.Millisecond) // let it do some work

	cancel() // MUST block until drain; NOT deferred here so we assert explicitly
	// If any goroutine leaked, goleak.VerifyNone (deferred above) will fail.
}

func TestWorker_RetryExhaustion_DeadLetters(t *testing.T) {
	defer goleak.VerifyNone(t)
	db, outbox, obsStore, closeDB := setupWorkerDB(t)
	defer closeDB()

	obsID := insertTestObservation(t, obsStore, "doomed", "will always fail")
	res, err := db.Exec(
		`INSERT INTO index_outbox (observation_id, intent, status, attempts, max_attempts) VALUES (?, 'embed_upsert', 'pending', 0, 2)`,
		obsID,
	)
	if err != nil {
		t.Fatalf("insert intent: %v", err)
	}
	intentID, _ := res.LastInsertId()

	embSvc := &fakeEmbeddingService{dims: 768, model: "fail-model", failCount: 100}
	vecWriter := newFakeVectorWriter()

	cfg := fastWorkerConfig()
	cfg.PollInterval = 2 * time.Millisecond
	w := NewWorker(outbox, obsStore, embSvc, vecWriter, cfg)
	w.setBackoffFn(func(int) time.Duration { return 1 * time.Millisecond }) // fast retry
	cancel := w.Start(context.Background())
	defer cancel()

	waitForStatus(t, db, intentID, sqlitestore.OutboxStatusDeadLetter, 5*time.Second)

	// No silent loss — error cause must be recorded.
	var errMsg sql.NullString
	_ = db.QueryRow(`SELECT error FROM index_outbox WHERE id = ?`, intentID).Scan(&errMsg)
	if !errMsg.Valid || errMsg.String == "" {
		t.Fatal("dead-lettered intent has no error cause recorded")
	}
}

func TestWorker_NonRetryableFailure_ImmediateDeadLetter(t *testing.T) {
	defer goleak.VerifyNone(t)
	db, outbox, obsStore, closeDB := setupWorkerDB(t)
	defer closeDB()

	obsID := insertTestObservation(t, obsStore, "dim mismatch", "bad vector dims")
	intentID := enqueueIntent(t, db, obsID, "wrong-model:999")

	// Embedding declares 999 dims but Embed returns 64 — dimension mismatch.
	embSvc := &dimMismatchService{model: "wrong-model", declaredDims: 999}
	vecWriter := newFakeVectorWriter()

	w := NewWorker(outbox, obsStore, embSvc, vecWriter, fastWorkerConfig())
	cancel := w.Start(context.Background())
	defer cancel()

	waitForStatus(t, db, intentID, sqlitestore.OutboxStatusDeadLetter, 3*time.Second)
}

func TestWorker_EmptyOutbox_NoLeak(t *testing.T) {
	defer goleak.VerifyNone(t)
	_, outbox, obsStore, closeDB := setupWorkerDB(t)
	defer closeDB()

	embSvc := &fakeEmbeddingService{dims: 768, model: "idle"}
	vecWriter := newFakeVectorWriter()

	w := NewWorker(outbox, obsStore, embSvc, vecWriter, fastWorkerConfig())
	cancel := w.Start(context.Background())
	time.Sleep(100 * time.Millisecond) // poll empty outbox
	cancel()
	// goleak verifies no goroutines remain.
}

func TestWorker_Cancellation_StopsAcceptingWork(t *testing.T) {
	defer goleak.VerifyNone(t)
	db, outbox, obsStore, closeDB := setupWorkerDB(t)
	defer closeDB()

	embSvc := &fakeEmbeddingService{dims: 768, model: "cancel-test"}
	vecWriter := newFakeVectorWriter()

	cfg := fastWorkerConfig()
	cfg.PollInterval = 50 * time.Millisecond // slow poll
	w := NewWorker(outbox, obsStore, embSvc, vecWriter, cfg)
	cancel := w.Start(context.Background())

	cancel() // cancel BEFORE any work is enqueued

	// Enqueue work AFTER cancellation — it must NOT be processed.
	obsID := insertTestObservation(t, obsStore, "post-cancel", "should not process")
	intentID := enqueueIntent(t, db, obsID, "post:768")

	time.Sleep(100 * time.Millisecond)
	status := outboxStatus(t, db, intentID)
	if status != sqlitestore.OutboxStatusPending && status != sqlitestore.OutboxStatusLeased {
		t.Fatalf("post-cancel intent status = %q, want pending/leased (not processed after shutdown)", status)
	}
}

// dimMismatchService returns an embedding whose dimension does NOT match the
// declared Dimensions(), producing a non-retryable terminal error.
type dimMismatchService struct {
	model        string
	declaredDims int
}

func (s *dimMismatchService) Embed(_ context.Context, _ string) ([]float32, error) {
	return make([]float32, 64), nil // 64 != declaredDims
}
func (s *dimMismatchService) Dimensions() int { return s.declaredDims }
func (s *dimMismatchService) Model() string   { return s.model }

// ---------------------------------------------------------------------------
// Backoff calculation test
// ---------------------------------------------------------------------------

func TestBackoff_CappedExponential(t *testing.T) {
	tests := []struct {
		attempt int
		minDur  time.Duration
		maxDur  time.Duration
	}{
		{0, 0, defaultBaseBackoff},
		{1, defaultBaseBackoff, defaultBaseBackoff * 2},
		{5, defaultMaxBackoff / 2, defaultMaxBackoff},
		{20, defaultMaxBackoff, defaultMaxBackoff}, // capped
	}
	for _, tc := range tests {
		d := computeEmbedBackoff(tc.attempt)
		if d < tc.minDur || d > tc.maxDur {
			t.Fatalf("backoff(%d) = %v, want in [%v, %v]", tc.attempt, d, tc.minDur, tc.maxDur)
		}
	}
	if d := computeEmbedBackoff(100); d > defaultMaxBackoff {
		t.Fatalf("backoff(100) = %v exceeds cap %v", d, defaultMaxBackoff)
	}
}

// Ensure the worker's dependencies are wired correctly — compile-time check.
func TestWorker_TypeAssertions(t *testing.T) {
	var _ Service = (*fakeEmbeddingService)(nil)
	_ = errors.New("test")
}
