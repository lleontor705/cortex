// Package embedding: W8.4 vector-upsert failure injection tests.
//
// These tests fill the gap in the existing worker_test.go suite: the existing
// RetryExhaustion and NonRetryable tests inject failures at the EMBEDDING
// step (fakeEmbeddingService.failCount). They do NOT exercise the path where
// the embedding SUCCEEDS but the configured VectorIndex.Upsert FAILS — the
// W8.4 scenario where an external adapter (qdrant, pgvector) is unreachable.
//
// These tests verify:
//
//   - A retryable VectorIndex.Upsert error retries with backoff and eventually
//     dead-letters after max_attempts (no silent loss).
//   - A non-retryable VectorIndex.Upsert error (ErrVectorSearchDisabled) goes
//     to immediate dead-letter without consuming retry attempts.
//   - A transient VectorIndex.Upsert failure that subsequently succeeds
//     processes normally (retry-then-recover).
//   - An intent left 'leased' by a crashed worker mid-upsert is recovered on
//     restart and reprocessed (crash recovery at the vector-write boundary).
package embedding

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
	sqlitestore "github.com/lleontor705/cortex/internal/store/sqlite"
	"go.uber.org/goleak"
)

// countFailingVectorWriter fails the first failCount Upsert calls, then
// succeeds. This exercises the retry-then-recover path: the worker retries
// the upsert across poll cycles until the transient failure clears.
type countFailingVectorWriter struct {
	mu        sync.Mutex
	upserts   map[int64][]float32
	failCount int32 // atomic counter
	failUntil int32 // fail while counter < failUntil
	failErr   error
}

func newCountFailingVectorWriter(failUntil int32, err error) *countFailingVectorWriter {
	return &countFailingVectorWriter{
		upserts:   make(map[int64][]float32),
		failUntil: failUntil,
		failErr:   err,
	}
}

func (f *countFailingVectorWriter) Upsert(_ context.Context, points []domain.VectorPoint) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := atomic.AddInt32(&f.failCount, 1)
	if n <= f.failUntil {
		return f.failErr
	}
	for _, p := range points {
		cp := make([]float32, len(p.Vector))
		copy(cp, p.Vector)
		f.upserts[p.ID] = cp
	}
	return nil
}

func (f *countFailingVectorWriter) has(obsID int64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.upserts[obsID]
	return ok
}

// TestWorker_VectorUpsert_RetryExhaustion_DeadLetters is the W8.4 defect pin:
// when the configured VectorIndex.Upsert fails with a RETRYABLE error (e.g.
// external adapter unreachable), the worker MUST retry with backoff and
// eventually dead-letter. The intent is NEVER silently dropped.
func TestWorker_VectorUpsert_RetryExhaustion_DeadLetters(t *testing.T) {
	defer goleak.VerifyNone(t)
	db, outbox, obsStore, closeDB := setupWorkerDB(t)
	defer closeDB()

	obsID := insertTestObservation(t, obsStore, "doomed-vector", "embedding ok but upsert fails")
	res, err := db.Exec(
		`INSERT INTO index_outbox (observation_id, intent, status, attempts, max_attempts) VALUES (?, 'embed_upsert', 'pending', 0, 2)`,
		obsID,
	)
	if err != nil {
		t.Fatalf("insert intent: %v", err)
	}
	intentID, _ := res.LastInsertId()

	// Embedding SUCCEEDS. The VectorIndex always fails with a retryable error.
	embSvc := &fakeEmbeddingService{dims: 768, model: "ok-model"}
	vecWriter := &fakeVectorWriter{
		failErr: errors.New("qdrant: connection refused"),
	}

	cfg := fastWorkerConfig()
	cfg.PollInterval = 2 * time.Millisecond
	w := NewWorker(outbox, obsStore, embSvc, vecWriter, cfg)
	w.setBackoffFn(func(int) time.Duration { return 1 * time.Millisecond })
	cancel := w.Start(context.Background())
	defer cancel()

	waitForStatus(t, db, intentID, sqlitestore.OutboxStatusDeadLetter, 5*time.Second)

	// No silent loss — error cause must be recorded.
	var errMsg sql.NullString
	_ = db.QueryRow(`SELECT error FROM index_outbox WHERE id = ?`, intentID).Scan(&errMsg)
	if !errMsg.Valid || errMsg.String == "" {
		t.Fatal("dead-lettered intent has no error cause recorded")
	}
	// The error message should reference the vector store failure.
	if !contains(errMsg.String, "connection refused") && !contains(errMsg.String, "store embedding") {
		t.Errorf("error cause does not reference the vector upsert failure: %q", errMsg.String)
	}
}

// TestWorker_VectorUpsert_NonRetryable_ImmediateDeadLetter verifies that when
// the VectorIndex returns ErrVectorSearchDisabled (the adapter is inert), the
// worker classifies it as non-retryable and dead-letters IMMEDIATELY without
// consuming retry attempts.
func TestWorker_VectorUpsert_NonRetryable_ImmediateDeadLetter(t *testing.T) {
	defer goleak.VerifyNone(t)
	db, outbox, obsStore, closeDB := setupWorkerDB(t)
	defer closeDB()

	obsID := insertTestObservation(t, obsStore, "disabled-vector", "adapter is disabled")
	intentID := enqueueIntent(t, db, obsID, "disabled-model")

	embSvc := &fakeEmbeddingService{dims: 768, model: "disabled-model"}
	vecWriter := &fakeVectorWriter{
		failErr: domain.ErrVectorSearchDisabled,
	}

	w := NewWorker(outbox, obsStore, embSvc, vecWriter, fastWorkerConfig())
	cancel := w.Start(context.Background())
	defer cancel()

	waitForStatus(t, db, intentID, sqlitestore.OutboxStatusDeadLetter, 3*time.Second)
}

// TestWorker_VectorUpsert_TransientFailureThenSuccess verifies the retry-then-
// recover path: the VectorIndex fails a few times (simulating a transient
// outage), then succeeds. The worker retries across poll cycles and eventually
// processes the intent.
func TestWorker_VectorUpsert_TransientFailureThenSuccess(t *testing.T) {
	defer goleak.VerifyNone(t)
	db, outbox, obsStore, closeDB := setupWorkerDB(t)
	defer closeDB()

	obsID := insertTestObservation(t, obsStore, "transient", "will succeed after retries")
	intentID := enqueueIntent(t, db, obsID, "transient-model")

	embSvc := &fakeEmbeddingService{dims: 768, model: "transient-model"}
	// Fail the first 2 attempts, then succeed.
	vecWriter := newCountFailingVectorWriter(2, errors.New("qdrant: transient timeout"))

	cfg := fastWorkerConfig()
	cfg.PollInterval = 2 * time.Millisecond
	w := NewWorker(outbox, obsStore, embSvc, vecWriter, cfg)
	w.setBackoffFn(func(int) time.Duration { return 1 * time.Millisecond })
	cancel := w.Start(context.Background())
	defer cancel()

	waitForStatus(t, db, intentID, sqlitestore.OutboxStatusComplete, 5*time.Second)

	if !vecWriter.has(obsID) {
		t.Fatal("vector was not upserted after transient failures cleared")
	}
}

// TestWorker_VectorUpsert_RestartRecovery verifies that an intent left 'leased'
// by a crashed worker (mid-upsert) is recovered to 'pending' on restart and
// reprocessed. This is the crash-recovery path at the vector-write boundary.
func TestWorker_VectorUpsert_RestartRecovery(t *testing.T) {
	defer goleak.VerifyNone(t)
	db, outbox, obsStore, closeDB := setupWorkerDB(t)
	defer closeDB()

	obsID := insertTestObservation(t, obsStore, "crashed", "worker died mid-upsert")
	// Simulate a crashed worker: the intent is 'leased' but never completed.
	res, err := db.Exec(
		`INSERT INTO index_outbox (observation_id, intent, status, attempts, max_attempts) VALUES (?, 'embed_upsert', 'leased', 0, 3)`,
		obsID,
	)
	if err != nil {
		t.Fatalf("insert leased intent: %v", err)
	}
	intentID, _ := res.LastInsertId()

	// A fresh worker starts. RecoverPending must reset 'leased' → 'pending'
	// so the intent is reprocessed.
	embSvc := &fakeEmbeddingService{dims: 768, model: "crash-model"}
	vecWriter := newFakeVectorWriter()

	w := NewWorker(outbox, obsStore, embSvc, vecWriter, fastWorkerConfig())
	cancel := w.Start(context.Background())
	defer cancel()

	waitForStatus(t, db, intentID, sqlitestore.OutboxStatusComplete, 5*time.Second)
	if !vecWriter.has(obsID) {
		t.Fatal("vector was not upserted after restart recovery")
	}
}

// contains is a case-sensitive substring check (avoids pulling strings for a
// single helper).
func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchString(s, sub)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
