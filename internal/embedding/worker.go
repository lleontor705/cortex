// Package embedding also provides the durable embedding worker (ADR-04, W4,
// REQ-EMB-001).
//
// The worker drains the transactional outbox asynchronously: it leases pending
// embed+upsert intents, hydrates the observation text, embeds it with the
// configured model (versioned namespace), upserts the vector, records index
// namespace coverage, and marks the intent complete. Failures retry with capped
// exponential backoff up to max_attempts; terminal (non-retryable) failures go
// to dead-letter. The worker uses a bounded pool, drains on shutdown BEFORE
// DB.Close, and propagates cancellation — no detached fire-and-forget goroutines.
package embedding

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
	sqlitestore "github.com/lleontor705/cortex/v2/internal/store/sqlite"
)

// ---------------------------------------------------------------------------
// Dependency interfaces (satisfied by concrete store types; accepted as
// interfaces so the worker is testable with fakes without mocking the DB layer).
// ---------------------------------------------------------------------------

// outboxQueue is the subset of OutboxStore operations the worker needs.
type outboxQueue interface {
	Lease(ctx context.Context, limit int) ([]sqlitestore.OutboxIntent, error)
	MarkComplete(ctx context.Context, id int64) error
	MarkFailed(ctx context.Context, id int64, cause error, nextRetryAt time.Time) error
	DeadLetter(ctx context.Context, id int64, cause error) error
	RecoverPending(ctx context.Context) error
	PendingCount(ctx context.Context) (int, error)
	UpdateIndexState(ctx context.Context, namespace string, coverage float64, parity int) error
}

// observationReader hydrates observation text (title + content) by ID.
type observationReader interface {
	GetByID(ctx context.Context, id int64) (*domain.Observation, error)
}

// vectorWriter stores an embedding vector for an observation via the
// domain.VectorIndex port (W8 adoption of ADR-05). The worker upserts a
// single-point batch per intent; the model-version namespace on the
// VectorPoint lets the adapter enforce dimension consistency (REQ-VEC-001).
type vectorWriter interface {
	Upsert(ctx context.Context, points []domain.VectorPoint) error
}

// ---------------------------------------------------------------------------
// Defaults
// ---------------------------------------------------------------------------

const (
	defaultConcurrency  = 2
	defaultPollInterval = 100 * time.Millisecond
	defaultLeaseBatch   = 1
	defaultMaxBacklog   = 1000
	defaultDrainTimeout = 30 * time.Second

	// Capped exponential backoff for embed retries.
	defaultBaseBackoff = 500 * time.Millisecond
	defaultMaxBackoff  = 30 * time.Second
)

// WorkerConfig configures the embedding worker.
type WorkerConfig struct {
	// Concurrency is the bounded worker pool size (goroutines). Default 2.
	Concurrency int
	// PollInterval is how long to wait before re-checking for new work when the
	// outbox is empty. Default 100ms.
	PollInterval time.Duration
	// LeaseBatch is how many intents a single worker leases per poll. Default 1.
	LeaseBatch int
	// MaxBacklog is the saturation threshold: when PendingCount exceeds this,
	// the save path fails-closed (REQ-EMB-001 saturation). Default 1000.
	MaxBacklog int
	// DrainTimeout bounds how long Start's cancel func waits for in-flight work
	// on shutdown. Default 30s.
	DrainTimeout time.Duration
}

// withDefaults applies default values for zero fields.
func (c WorkerConfig) withDefaults() WorkerConfig {
	if c.Concurrency <= 0 {
		c.Concurrency = defaultConcurrency
	}
	if c.PollInterval <= 0 {
		c.PollInterval = defaultPollInterval
	}
	if c.LeaseBatch <= 0 {
		c.LeaseBatch = defaultLeaseBatch
	}
	if c.MaxBacklog <= 0 {
		c.MaxBacklog = defaultMaxBacklog
	}
	if c.DrainTimeout <= 0 {
		c.DrainTimeout = defaultDrainTimeout
	}
	return c
}

// ---------------------------------------------------------------------------
// Worker
// ---------------------------------------------------------------------------

// Worker is the durable embedding worker. It drains the outbox with a bounded
// pool of goroutines, retrying failures with capped backoff and dead-lettering
// terminal failures. Start returns a cancel func that drains on shutdown.
type Worker struct {
	outbox     outboxQueue
	obs        observationReader
	embeddings Service
	vectors    vectorWriter
	config     WorkerConfig
	backoffFn  func(attempt int) time.Duration
}

// NewWorker creates an embedding worker. The concrete *sqlitestore.OutboxStore
// and *sqlitestore.Store satisfy the interface parameters structurally; the
// vectors parameter accepts any domain.VectorIndex implementation (the
// sqlite_blob adapter is the W8 default, ADR-05). The worker depends on the
// domain port, not the concrete vector store.
func NewWorker(outbox outboxQueue, obs observationReader, embeddings Service, vectors vectorWriter, cfg WorkerConfig) *Worker {
	return &Worker{
		outbox:     outbox,
		obs:        obs,
		embeddings: embeddings,
		vectors:    vectors,
		config:     cfg.withDefaults(),
		backoffFn:  computeEmbedBackoff,
	}
}

// setBackoffFn overrides the backoff function. Intended for tests that need
// fast retries without waiting for the real capped-exponential schedule.
func (w *Worker) setBackoffFn(fn func(attempt int) time.Duration) {
	w.backoffFn = fn
}

// Start begins processing outbox intents in a bounded pool of goroutines. It
// first recovers any intents left 'leased' by a crashed worker (crash recovery,
// REQ-EMB-001). The returned cancel func is the worker's STOP function and
// implements explicit cancel/stop/join semantics with TWO bounded contexts:
//
//   - runCtx      bounds leasing, hydration, embedding, and upsert work.
//   - finalizeCtx bounds outcome-recording operations (MarkComplete/MarkFailed/
//     DeadLetter/UpdateIndexState).
//
// The stop func executes three phases:
//  1. STOP  — cancel runCtx: no new leases; in-flight embed/upsert abort and the
//     intent is left leased for crash-recovery on next startup.
//  2. JOIN  — wait for all worker goroutines to exit, bounded by DrainTimeout.
//     finalizeCtx is still alive, so outcomes of in-flight intents are
//     recorded normally (no silent loss on a graceful drain).
//  3. CANCEL FINALIZE — unconditionally cancel finalizeCtx. After this returns,
//     no goroutine can complete a finalize DB write:
//     • joined goroutines have already exited;
//     • a goroutine still unwinding an in-flight finalize observes
//     ctx.Done and the ExecContext aborts BEFORE touching the DB;
//     • a goroutine stuck in an embed/upsert (runCtx already cancelled)
//     cannot reach finalize; its intent remains leased → recovery.
//
// Therefore DB.Close MUST be called only after this stop func returns — and when
// it does, no goroutine is touching or can touch the DB (no write-after-close,
// no leaked DB-accessing goroutine).
func (w *Worker) Start(ctx context.Context) context.CancelFunc {
	// runCtx: bounds all leasing/hydration/embedding/upsert work.
	runCtx, runCancel := context.WithCancel(ctx)
	// finalizeCtx: bounds outcome-recording writes. Lives for the whole worker
	// lifecycle and is cancelled only after the drain join completes, so a
	// graceful drain records outcomes while a timeout drain prevents any
	// finalize write from landing after the caller closes the DB.
	finalizeCtx, finalizeCancel := context.WithCancel(context.Background())

	// Crash recovery: reset 'leased' intents to 'pending' (REQ-EMB-001 startup recovery).
	if err := w.outbox.RecoverPending(runCtx); err != nil {
		log.Printf("embedding worker: recover pending intents: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < w.config.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.runLoop(runCtx, finalizeCtx)
		}()
	}

	return func() {
		// 1. STOP accepting new work / abort in-flight embed+upsert.
		runCancel()
		// 2. JOIN worker goroutines, bounded by DrainTimeout.
		done := make(chan struct{})
		go func() {
			wg.Wait() // wait for all worker goroutines to exit
			close(done)
		}()
		select {
		case <-done:
			// Graceful drain: all goroutines exited; finalizeCtx was alive for
			// the whole join so outcomes were recorded.
		case <-time.After(w.config.DrainTimeout):
			log.Printf("embedding worker: drain timeout exceeded (%v), in-flight finalize cancelled", w.config.DrainTimeout)
		}
		// 3. CANCEL FINALIZE — guarantee no finalize DB write can land after
		//    this stop func returns (no write-after-close). Idempotent.
		finalizeCancel()
	}
}

// runLoop is the per-goroutine processing loop. It leases and processes intents
// until the context is cancelled. When no work is available it sleeps for
// PollInterval; when work is available it loops immediately. finalizeCtx is
// threaded into processIntent so outcome-recording writes are bounded by the
// worker lifecycle rather than an unbounded context.
func (w *Worker) runLoop(ctx, finalizeCtx context.Context) {
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		processed := w.processOne(ctx, finalizeCtx)

		if err := ctx.Err(); err != nil {
			return
		}
		if !processed {
			select {
			case <-ctx.Done():
				return
			case <-time.After(w.config.PollInterval):
			}
		}
		// If processed, loop immediately to check for more work.
	}
}

// processOne leases a batch of intents and processes each. Returns true if at
// least one intent was leased (more may be available). Returns false if the
// outbox was empty.
func (w *Worker) processOne(ctx, finalizeCtx context.Context) bool {
	intents, err := w.outbox.Lease(ctx, w.config.LeaseBatch)
	if err != nil {
		if ctx.Err() != nil {
			return false
		}
		log.Printf("embedding worker: lease: %v", err)
		return false
	}
	if len(intents) == 0 {
		return false
	}
	for _, in := range intents {
		if ctx.Err() != nil {
			// During drain: leave the leased intent in place. RecoverPending
			// will requeue it on next startup (REQ-EMB-001 durability).
			return true
		}
		w.processIntent(ctx, finalizeCtx, in)
	}
	return true
}

// processIntent processes a single leased intent: hydrate → embed (versioned) →
// validate dims → upsert → track namespace → mark complete. On failure it
// classifies retryable vs non-retryable and records the outcome.
//
// Context discipline (W4 advisory — no write-after-close):
//   - ctx (runCtx) bounds hydrate/embed/upsert. On drain these abort and the
//     intent is left leased for crash recovery — never silently dropped.
//   - finalizeCtx bounds the outcome-recording writes (MarkComplete/MarkFailed/
//     DeadLetter/UpdateIndexState). It is cancelled by the stop func only AFTER
//     the drain join, so a graceful drain records outcomes while a timeout drain
//     aborts any lingering finalize BEFORE the caller closes the DB. The intent
//     remains durable (leased/pending) for recovery either way (REQ-EMB-002).
func (w *Worker) processIntent(ctx, finalizeCtx context.Context, in sqlitestore.OutboxIntent) {
	// 1. Hydrate observation text (runCtx-bounded).
	obs, err := w.obs.GetByID(ctx, in.ObservationID)
	if err != nil {
		w.finalizeFailure(finalizeCtx, in, fmt.Errorf("hydrate observation %d: %w", in.ObservationID, err))
		return
	}

	// 2. Embed with model-version namespace (runCtx-bounded).
	text := PrepareObservationText(obs)
	chunks := ChunkText(text, MaxSingleEmbeddingLength, ChunkOverlapChars)
	targetText := text
	if len(chunks) > 0 {
		targetText = chunks[0]
	}
	vec, err := w.embeddings.Embed(ctx, targetText)
	if err != nil {
		if ctx.Err() != nil {
			// Cancellation during embed: leave leased; recovery handles restart.
			return
		}
		w.finalizeFailure(finalizeCtx, in, fmt.Errorf("embed: %w", err))
		return
	}

	// 3. Dimension validation (model-version namespace prevents dim-mismatch
	// corruption, REQ-EMB-002).
	expectedDims := w.embeddings.Dimensions()
	if len(vec) == 0 {
		w.finalizeFailure(finalizeCtx, in, fmt.Errorf("embed: model %q returned empty vector", w.embeddings.Model()))
		return
	}
	if len(vec) != expectedDims {
		cause := &domain.ValidationError{
			Field:   "embedding",
			Message: fmt.Sprintf("dimension mismatch: model %q declares %d dims but produced %d", w.embeddings.Model(), expectedDims, len(vec)),
		}
		w.finalizeFailure(finalizeCtx, in, cause)
		return
	}

	// 4. Upsert vector via the VectorIndex port (runCtx-bounded). The
	// model-version namespace on the VectorPoint lets the adapter enforce
	// dimension consistency (REQ-VEC-001 dim-mismatch corruption pin).
	point := domain.VectorPoint{
		ID:     in.ObservationID,
		Vector: vec,
		ModelInfo: domain.ModelInfo{
			Name:      w.embeddings.Model(),
			Dimension: expectedDims,
		},
	}
	if err := w.vectors.Upsert(ctx, []domain.VectorPoint{point}); err != nil {
		if ctx.Err() != nil {
			return // leave leased; recovery handles on restart
		}
		w.finalizeFailure(finalizeCtx, in, fmt.Errorf("store embedding: %w", err))
		return
	}

	// 5. Update index_state namespace tracking (coverage/parity) — finalize-bounded.
	namespace := w.embeddings.Model() + ":" + strconv.Itoa(expectedDims)
	if err := w.outbox.UpdateIndexState(finalizeCtx, namespace, 1.0, 1); err != nil {
		log.Printf("embedding worker: update index_state for namespace %q: %v", namespace, err)
	}

	// 6. Mark complete — finalize-bounded outcome.
	if err := w.outbox.MarkComplete(finalizeCtx, in.ID); err != nil {
		log.Printf("embedding worker: mark complete for intent %d: %v", in.ID, err)
	}
}

// finalizeFailure records the outcome of a failed processing attempt. Non-
// retryable errors (validation/dimension/config) go straight to dead-letter;
// retryable errors schedule a retry with capped exponential backoff, or dead-
// letter if max_attempts is exhausted.
func (w *Worker) finalizeFailure(ctx context.Context, in sqlitestore.OutboxIntent, cause error) {
	if isNonRetryable(cause) {
		if err := w.outbox.DeadLetter(ctx, in.ID, cause); err != nil {
			log.Printf("embedding worker: dead-letter intent %d: %v", in.ID, err)
		}
		return
	}
	retryAt := time.Now().Add(w.backoffFn(in.Attempts))
	if err := w.outbox.MarkFailed(ctx, in.ID, cause, retryAt); err != nil {
		log.Printf("embedding worker: mark failed for intent %d: %v", in.ID, err)
	}
}

// isNonRetryable reports whether err represents a terminal, non-retryable
// failure (dimension mismatch, validation error, disabled backend). These go
// straight to dead-letter without consuming retry attempts.
func isNonRetryable(err error) bool {
	if err == nil {
		return false
	}
	var ve *domain.ValidationError
	if errors.As(err, &ve) {
		return true
	}
	if errors.Is(err, domain.ErrVectorSearchDisabled) {
		return true
	}
	return false
}

// IsSaturated reports whether the outbox backlog exceeds the configured
// MaxBacklog threshold. The save path calls this before enqueuing to fail-closed
// under overload (REQ-EMB-001 saturation/overload behavior).
func (w *Worker) IsSaturated(ctx context.Context) (bool, error) {
	count, err := w.outbox.PendingCount(ctx)
	if err != nil {
		return false, err
	}
	return count > w.config.MaxBacklog, nil
}

// computeEmbedBackoff returns the retry delay for the given attempt index using
// capped exponential growth: base * 2^attempt, capped at defaultMaxBackoff.
func computeEmbedBackoff(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	d := defaultBaseBackoff << uint(attempt)
	if d > defaultMaxBackoff || d <= 0 {
		return defaultMaxBackoff
	}
	return d
}
