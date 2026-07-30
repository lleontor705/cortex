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
	"github.com/lleontor705/cortex/internal/migration"
	sqlitestore "github.com/lleontor705/cortex/internal/store/sqlite"

	"go.uber.org/goleak"
	_ "modernc.org/sqlite"
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

	baseline, err := migration.NewV2Baseline()
	if err != nil {
		t.Fatalf("new v2 baseline: %v", err)
	}
	if err := baseline.Apply(context.Background(), db); err != nil {
		t.Fatalf("apply v2 baseline: %v", err)
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

// fakeVectorWriter records upserts and can be configured to fail. Implements
// the worker's vectorWriter interface (domain.VectorIndex subset: Upsert).
type fakeVectorWriter struct {
	mu      sync.Mutex
	upserts map[int64][]float32
	failErr error
}

func newFakeVectorWriter() *fakeVectorWriter {
	return &fakeVectorWriter{upserts: make(map[int64][]float32)}
}

func (f *fakeVectorWriter) Upsert(_ context.Context, points []domain.VectorPoint) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failErr != nil {
		return f.failErr
	}
	for _, p := range points {
		cp := make([]float32, len(p.Vector))
		copy(cp, p.Vector)
		f.upserts[p.ID] = cp
	}
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

// ---------------------------------------------------------------------------
// Drain lifecycle: bounded contexts, no write-after-close (W4 advisory)
// ---------------------------------------------------------------------------

// blockingFinalizeOutbox is a fake outboxQueue whose MarkComplete blocks until
// either its context is cancelled (drain aborted finalize) or proceed is closed
// (finalize would write). It records which branch fired, proving whether a DB
// write could occur. Used to assert the drain's finalize context cancellation
// prevents write-after-close.
type blockingFinalizeOutbox struct {
	seeded []sqlitestore.OutboxIntent

	markCompleteCalled  chan struct{}
	markCompleteOnce    sync.Once
	markCompleteDone    chan struct{}
	markCompleteDoneCLO sync.Once
	proceed             chan struct{}

	mu                sync.Mutex
	wroteMarkComplete bool
	cancelled         bool
}

func newBlockingFinalizeOutbox(seeded []sqlitestore.OutboxIntent) *blockingFinalizeOutbox {
	return &blockingFinalizeOutbox{
		seeded:             seeded,
		markCompleteCalled: make(chan struct{}),
		markCompleteDone:   make(chan struct{}),
		proceed:            make(chan struct{}),
	}
}

func (o *blockingFinalizeOutbox) Lease(_ context.Context, _ int) ([]sqlitestore.OutboxIntent, error) {
	if len(o.seeded) == 0 {
		return nil, nil
	}
	out := o.seeded
	o.seeded = nil
	return out, nil
}
func (o *blockingFinalizeOutbox) RecoverPending(context.Context) error { return nil }
func (o *blockingFinalizeOutbox) PendingCount(context.Context) (int, error) {
	return 0, nil
}
func (o *blockingFinalizeOutbox) UpdateIndexState(context.Context, string, float64, int) error {
	return nil
}
func (o *blockingFinalizeOutbox) MarkFailed(context.Context, int64, error, time.Time) error {
	return nil
}
func (o *blockingFinalizeOutbox) DeadLetter(context.Context, int64, error) error { return nil }

func (o *blockingFinalizeOutbox) MarkComplete(ctx context.Context, _ int64) error {
	o.markCompleteOnce.Do(func() { close(o.markCompleteCalled) })
	defer o.markCompleteDoneCLO.Do(func() { close(o.markCompleteDone) })
	select {
	case <-ctx.Done():
		o.mu.Lock()
		o.cancelled = true
		o.mu.Unlock()
		return ctx.Err()
	case <-o.proceed:
		o.mu.Lock()
		o.wroteMarkComplete = true
		o.mu.Unlock()
		return nil
	}
}

// TestWorker_DrainTimeout_CancelsFinalize_NoWriteAfterClose is the defect pin
// for the write-after-close advisory. It proves that when the drain exceeds
// DrainTimeout, the worker's stop func cancels the finalize context so that any
// in-flight finalize DB write (MarkComplete) aborts BEFORE the stop func
// returns — making it impossible for a finalize write to land after the caller
// closes the database.
//
// Proof shape: MarkComplete blocks on either ctx.Done (drain cancelled finalize)
// or proceed (would write). We start the worker, drive it to an in-flight
// MarkComplete, invoke stop(), then close proceed. If the stop func cancelled
// finalize first (correct), MarkComplete already returned via ctx.Done and the
// write never happens (wroteMarkComplete == false). If finalize used an
// unbounded context (the defect), MarkComplete would proceed to write after
// stop() returned (wroteMarkComplete == true) → test fails.
func TestWorker_DrainTimeout_CancelsFinalize_NoWriteAfterClose(t *testing.T) {
	defer goleak.VerifyNone(t)

	const obsID int64 = 1
	seeded := []sqlitestore.OutboxIntent{{ID: 7, ObservationID: obsID, Intent: "embed_upsert", ModelInfo: "m:8", Status: sqlitestore.OutboxStatusLeased, Attempts: 1, MaxAttempts: 5}}
	outbox := newBlockingFinalizeOutbox(seeded)

	obsReader := &stubObsReader{obs: &domain.Observation{ID: obsID, Title: "t", Content: "c"}}
	embSvc := &fakeEmbeddingService{dims: 8, model: "m"}
	vec := newFakeVectorWriter()

	cfg := WorkerConfig{
		Concurrency:  1,
		PollInterval: 5 * time.Millisecond,
		LeaseBatch:   1,
		MaxBacklog:   1000,
		DrainTimeout: 80 * time.Millisecond, // short: goroutine is blocked in MarkComplete → join times out
	}
	w := NewWorker(outbox, obsReader, embSvc, vec, cfg)
	stop := w.Start(context.Background())

	// Wait until MarkComplete is in flight (blocked on proceed/ctx).
	select {
	case <-outbox.markCompleteCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("MarkComplete was never reached within timeout")
	}

	stop() // drain: cancel run ctx, join (times out), cancel finalize ctx, return

	// After stop() returned, allow the write path. If finalize was cancelled by
	// the drain (correct), MarkComplete already returned via ctx.Done and this is
	// a no-op; if not (defect), the write lands here — caught by the assertion.
	close(outbox.proceed)

	// Wait for MarkComplete to have returned.
	select {
	case <-outbox.markCompleteDone:
	case <-time.After(2 * time.Second):
		t.Fatal("MarkComplete never returned after drain")
	}

	outbox.mu.Lock()
	wrote := outbox.wroteMarkComplete
	cancelled := outbox.cancelled
	outbox.mu.Unlock()

	if wrote {
		t.Fatal("finalize write occurred after drain returned — write-after-close NOT prevented")
	}
	if !cancelled {
		t.Fatal("finalize context was not cancelled by drain; expected ctx.Done() in MarkComplete")
	}
}

// TestWorker_GracefulDrain_FinalizeCompletes proves the bounded finalize context
// stays alive during a graceful drain (goroutines join within DrainTimeout), so
// finalize DB writes complete normally — outcomes are recorded, never silently
// dropped. This is the complement to the timeout test above.
func TestWorker_GracefulDrain_FinalizeCompletes(t *testing.T) {
	defer goleak.VerifyNone(t)
	db, outbox, obsStore, closeDB := setupWorkerDB(t)
	defer closeDB()

	obsID := insertTestObservation(t, obsStore, "graceful", "content")
	intentID := enqueueIntent(t, db, obsID, "m:768")

	embSvc := &fakeEmbeddingService{dims: 768, model: "m"}
	vec := newFakeVectorWriter()

	cfg := fastWorkerConfig()
	cfg.DrainTimeout = 5 * time.Second // ample: graceful join, finalize completes
	w := NewWorker(outbox, obsStore, embSvc, vec, cfg)
	stop := w.Start(context.Background())

	// Wait for the intent to be fully processed (MarkComplete landed).
	waitForStatus(t, db, intentID, sqlitestore.OutboxStatusComplete, 3*time.Second)

	stop() // graceful drain — all goroutines already idle, joins immediately

	// Outcome recorded: intent is complete, not abandoned to 'leased'.
	if got := outboxStatus(t, db, intentID); got != sqlitestore.OutboxStatusComplete {
		t.Fatalf("intent status = %q, want complete (finalize must complete on graceful drain)", got)
	}
}

// stubObsReader is a minimal observationReader for lifecycle unit tests.
type stubObsReader struct {
	obs *domain.Observation
	err error
}

func (s *stubObsReader) GetByID(_ context.Context, _ int64) (*domain.Observation, error) {
	return s.obs, s.err
}

// ---------------------------------------------------------------------------
// IsSaturated — authoritative saturation source (W4 advisory, single threshold)
// ---------------------------------------------------------------------------

// countOutbox is a fake outboxQueue with a controllable PendingCount, used to
// unit-test Worker.IsSaturated at the threshold boundary without a DB.
type countOutbox struct {
	count int
	err   error
}

func (c *countOutbox) Lease(context.Context, int) ([]sqlitestore.OutboxIntent, error) {
	return nil, nil
}
func (c *countOutbox) MarkComplete(context.Context, int64) error { return nil }
func (c *countOutbox) MarkFailed(context.Context, int64, error, time.Time) error {
	return nil
}
func (c *countOutbox) DeadLetter(context.Context, int64, error) error { return nil }
func (c *countOutbox) RecoverPending(context.Context) error           { return nil }
func (c *countOutbox) UpdateIndexState(context.Context, string, float64, int) error {
	return nil
}
func (c *countOutbox) PendingCount(context.Context) (int, error) { return c.count, c.err }

// TestWorker_IsSaturated_ThresholdBoundary proves Worker.IsSaturated is the
// single authoritative saturation check (WorkerConfig.MaxBacklog), consulted by
// the save path. Saturation is strictly greater-than: count == MaxBacklog is
// NOT saturated; count == MaxBacklog+1 IS saturated.
func TestWorker_IsSaturated_ThresholdBoundary(t *testing.T) {
	tests := []struct {
		name       string
		maxBacklog int
		count      int
		want       bool
	}{
		{"empty not saturated", 5, 0, false},
		{"at threshold not saturated", 5, 5, false},
		{"over threshold saturated", 5, 6, true},
		{"default threshold via withDefaults", 0, 1001, true}, // default 1000
		{"default threshold at boundary", 0, 1000, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := NewWorker(&countOutbox{count: tc.count}, &stubObsReader{},
				&fakeEmbeddingService{dims: 8, model: "m"}, newFakeVectorWriter(),
				WorkerConfig{MaxBacklog: tc.maxBacklog})
			got, err := w.IsSaturated(context.Background())
			if err != nil {
				t.Fatalf("IsSaturated error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("IsSaturated(count=%d, max=%d) = %v, want %v", tc.count, tc.maxBacklog, got, tc.want)
			}
		})
	}
}

// TestWorker_IsSaturated_PropagatesPendingCountError proves a backlog probe
// error surfaces to the caller (fail-closed save path propagates, never masks).
func TestWorker_IsSaturated_PropagatesPendingCountError(t *testing.T) {
	probeErr := fmt.Errorf("db unavailable")
	w := NewWorker(&countOutbox{err: probeErr}, &stubObsReader{},
		&fakeEmbeddingService{dims: 8, model: "m"}, newFakeVectorWriter(),
		WorkerConfig{MaxBacklog: 5})
	saturated, err := w.IsSaturated(context.Background())
	if err == nil {
		t.Fatal("expected PendingCount error to propagate, got nil")
	}
	if saturated {
		t.Fatal("saturated must be false when the probe errors (caller checks err first)")
	}
}
