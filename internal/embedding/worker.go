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

	"github.com/lleontor705/cortex/internal/domain"
	sqlitestore "github.com/lleontor705/cortex/internal/store/sqlite"
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

// vectorWriter stores an embedding vector for an observation.
type vectorWriter interface {
	StoreEmbedding(ctx context.Context, observationID int64, embedding []float32, model string) error
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

// NewWorker creates an embedding worker. The concrete *sqlitestore.OutboxStore,
// *sqlitestore.Store, and *sqlitestore.VectorStore satisfy the interface
// parameters structurally (ADR-04: depend on existing concrete stores, NOT the
// domain ports — that's W8 scope).
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
// REQ-EMB-001). The returned cancel func stops accepting new work, drains
// in-flight tasks (bounded by DrainTimeout), and then returns — DB.Close MUST
// only be called after this returns (no goroutine touching the DB remains).
func (w *Worker) Start(ctx context.Context) context.CancelFunc {
	ctx, cancel := context.WithCancel(ctx)

	// Crash recovery: reset 'leased' intents to 'pending' (REQ-EMB-001 startup recovery).
	if err := w.outbox.RecoverPending(ctx); err != nil {
		log.Printf("embedding worker: recover pending intents: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < w.config.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.runLoop(ctx)
		}()
	}

	return func() {
		cancel() // stop accepting new leases
		done := make(chan struct{})
		go func() {
			wg.Wait() // wait for all worker goroutines to exit
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(w.config.DrainTimeout):
			log.Printf("embedding worker: drain timeout exceeded (%v), in-flight tasks may be incomplete", w.config.DrainTimeout)
		}
	}
}

// runLoop is the per-goroutine processing loop. It leases and processes intents
// until the context is cancelled. When no work is available it sleeps for
// PollInterval; when work is available it loops immediately.
func (w *Worker) runLoop(ctx context.Context) {
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		processed := w.processOne(ctx)

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
func (w *Worker) processOne(ctx context.Context) bool {
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
		w.processIntent(ctx, in)
	}
	return true
}

// processIntent processes a single leased intent: hydrate → embed (versioned) →
// validate dims → upsert → track namespace → mark complete. On failure it
// classifies retryable vs non-retryable and records the outcome.
//
// Finalize operations (MarkComplete/MarkFailed/DeadLetter) use a background
// context so they succeed even during shutdown drain — the outcome MUST be
// recorded, never silently dropped (REQ-EMB-002 no silent loss).
func (w *Worker) processIntent(ctx context.Context, in sqlitestore.OutboxIntent) {
	bgCtx := context.Background()

	// 1. Hydrate observation text.
	obs, err := w.obs.GetByID(ctx, in.ObservationID)
	if err != nil {
		w.finalizeFailure(bgCtx, in, fmt.Errorf("hydrate observation %d: %w", in.ObservationID, err))
		return
	}

	// 2. Embed with model-version namespace.
	text := obs.Title + "\n" + obs.Content
	vec, err := w.embeddings.Embed(ctx, text)
	if err != nil {
		if ctx.Err() != nil {
			// Cancellation during embed: leave leased; recovery handles restart.
			return
		}
		w.finalizeFailure(bgCtx, in, fmt.Errorf("embed: %w", err))
		return
	}

	// 3. Dimension validation (model-version namespace prevents dim-mismatch
	// corruption, REQ-EMB-002).
	expectedDims := w.embeddings.Dimensions()
	if len(vec) == 0 {
		w.finalizeFailure(bgCtx, in, fmt.Errorf("embed: model %q returned empty vector", w.embeddings.Model()))
		return
	}
	if len(vec) != expectedDims {
		cause := &domain.ValidationError{
			Field:   "embedding",
			Message: fmt.Sprintf("dimension mismatch: model %q declares %d dims but produced %d", w.embeddings.Model(), expectedDims, len(vec)),
		}
		w.finalizeFailure(bgCtx, in, cause)
		return
	}

	// 4. Upsert vector.
	if err := w.vectors.StoreEmbedding(ctx, in.ObservationID, vec, w.embeddings.Model()); err != nil {
		if ctx.Err() != nil {
			return // leave leased; recovery handles on restart
		}
		w.finalizeFailure(bgCtx, in, fmt.Errorf("store embedding: %w", err))
		return
	}

	// 5. Update index_state namespace tracking (coverage/parity).
	namespace := w.embeddings.Model() + ":" + strconv.Itoa(expectedDims)
	if err := w.outbox.UpdateIndexState(bgCtx, namespace, 1.0, 1); err != nil {
		log.Printf("embedding worker: update index_state for namespace %q: %v", namespace, err)
	}

	// 6. Mark complete.
	if err := w.outbox.MarkComplete(bgCtx, in.ID); err != nil {
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
