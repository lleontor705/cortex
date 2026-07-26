// Package bundle provides the Stores struct that bundles all store dependencies.
// This avoids circular imports between app and mcp packages.
package bundle

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/embedding"
	entitystore "github.com/lleontor705/cortex/internal/store/entity"
	graphstore "github.com/lleontor705/cortex/internal/store/graph"
	"github.com/lleontor705/cortex/internal/store/prompt"
	scoringstore "github.com/lleontor705/cortex/internal/store/scoring"
	"github.com/lleontor705/cortex/internal/store/search"
	"github.com/lleontor705/cortex/internal/store/session"
	sqlitestore "github.com/lleontor705/cortex/internal/store/sqlite"
)

// Stores bundles all store dependencies needed by MCP, HTTP, and CLI.
type Stores struct {
	Observations      *sqlitestore.Store
	Sessions          *session.Store
	Search            *search.Store
	Prompts           *prompt.Store
	Graph             *graphstore.Store
	Scoring           *scoringstore.Store
	Vectors           *sqlitestore.VectorStore
	TemporalSnapshots *sqlitestore.TemporalSnapshotRepository
	Entities          *entitystore.Store
	Metrics           *sqlitestore.MetricsRepository
	QualityMetrics    *sqlitestore.QualityMetricsRepository

	// Embeddings is the optional embedding service for vector search.
	Embeddings embedding.Service

	// LastSearchQuery tracks the most recent search query for implicit feedback.
	// When mem_get_observation is called after mem_search, we log the
	// query-to-observation mapping for future Learning-to-Rank training.
	LastSearchQuery string

	// UnitOfWork coordinates atomic cross-store saves (W2.1, REQ-TX-001).
	// It is nil until wired by the composition root (app.go); tests construct
	// it directly via NewSQLiteUnitOfWork. When non-nil, callers that need
	// multi-participant atomicity SHOULD use Do() instead of per-store Save().
	UnitOfWork domain.UnitOfWork
}

// ---------------------------------------------------------------------------
// SQLiteUnitOfWork — atomic cross-store save (W2.1, REQ-TX-001 + REQ-TX-002)
//
// SQLiteUnitOfWork opens ONE *sql.Tx on the shared *sql.DB and threads the SAME
// *sql.Tx handle into every TxParticipant. Because all participants share one
// transaction, a failure at any point rolls back ALL prior participant work
// atomically — no partial committed state is possible (REQ-TX-001).
//
// On SQLITE_BUSY (contention on the shared write lock), Do retries up to
// BusyRetryConfig.MaxRetries with capped exponential backoff and jitter before
// returning a stable, retryable error (REQ-TX-002). A save never blocks
// unbounded.
// ---------------------------------------------------------------------------

// uowTxKey is the context key under which Do stashes the shared *sql.Tx so
// that the caller's fn can retrieve it via TxHandle and enlist participants.
type uowTxKey struct{}

// TxHandle retrieves the shared transaction handle stashed by
// SQLiteUnitOfWork.Do in the context. Returns nil if no UnitOfWork
// transaction is active. The caller passes this handle to each participant's
// WithinTx.
func TxHandle(ctx context.Context) any {
	return ctx.Value(uowTxKey{})
}

// SQLiteUnitOfWork implements domain.UnitOfWork for the SQLite backend. It
// coordinates multiple TxParticipants within a single shared *sql.Tx.
type SQLiteUnitOfWork struct {
	db  *sql.DB
	cfg domain.BusyRetryConfig
}

// NewSQLiteUnitOfWork creates a UnitOfWork for the given shared *sql.DB. If
// cfg is the zero value, DefaultBusyRetryConfig is used.
func NewSQLiteUnitOfWork(db *sql.DB, cfg domain.BusyRetryConfig) *SQLiteUnitOfWork {
	if cfg.MaxRetries == 0 && cfg.BaseBackoff == 0 && cfg.MaxBackoff == 0 {
		cfg = domain.DefaultBusyRetryConfig()
	}
	return &SQLiteUnitOfWork{db: db, cfg: cfg}
}

// Do runs fn with all participants sharing ONE *sql.Tx. On any error (from fn
// or a participant), the transaction is rolled back atomically — no partial
// state is committed. On SQLITE_BUSY, Do retries up to MaxRetries with capped
// backoff before returning a stable retryable error.
//
// The participants slice is accepted for API completeness and future
// instrumentation; fn is responsible for enlisting each participant via
// participant.WithinTx(ctx, TxHandle(ctx), work).
func (u *SQLiteUnitOfWork) Do(ctx context.Context, _ *domain.TenantContext, _ []domain.TxParticipant, fn func(context.Context) error) error {
	return retryOnBusy(ctx, u.cfg, func() error {
		// Open ONE shared transaction on the shared *sql.DB.
		tx, err := u.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
		if err != nil {
			return fmt.Errorf("unitOfWork: begin tx: %w", err)
		}

		// Stash the tx so fn can retrieve it via TxHandle and enlist participants.
		enlistedCtx := context.WithValue(ctx, uowTxKey{}, tx)

		// Run the caller's work (which enlists participants within the shared tx).
		if err := fn(enlistedCtx); err != nil {
			// Explicit reverse-order rollback: with a single shared *sql.Tx,
			// Rollback undoes ALL participant writes atomically.
			_ = tx.Rollback()
			return err
		}

		// Forward-order commit: makes all participant writes durable atomically.
		if err := tx.Commit(); err != nil {
			// Commit may fail with SQLITE_BUSY if the connection was invalidated.
			_ = tx.Rollback() // safe no-op if already finalized
			return fmt.Errorf("unitOfWork: commit: %w", err)
		}
		return nil
	})
}

// Ensure SQLiteUnitOfWork implements domain.UnitOfWork (W2.1 adoption).
var _ domain.UnitOfWork = (*SQLiteUnitOfWork)(nil)

// ---------------------------------------------------------------------------
// BUSY retry + detection (REQ-TX-002)
// ---------------------------------------------------------------------------

// busyErrorSubstrings are the lowercase substrings that identify a SQLite
// SQLITE_BUSY / "database is locked" condition from the modernc.org/sqlite
// pure-Go driver (which reports these as string-based errors, not typed).
var busyErrorSubstrings = []string{
	"sqlite_busy",
	"sqlcode 6",   // SQLITE_BUSY result code
	"database is locked",
	"unable to open database file", // rare: lock contention on journal
}

// IsSQLiteBusy reports whether err represents a SQLITE_BUSY / "database is
// locked" condition. This is the stable, retryable signal callers check after
// UnitOfWork.Do returns an error (REQ-TX-002 edge scenario).
func IsSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, sub := range busyErrorSubstrings {
		if strings.Contains(msg, sub) {
			return true
		}
	}
	return false
}

// retryOnBusy runs fn up to (1 + cfg.MaxRetries) times. If fn returns an error
// that IsSQLiteBusy, it sleeps for capped exponential backoff with jitter and
// retries. Non-busy errors and success terminate immediately. If the retry cap
// is exhausted, the last busy error is returned (stable, retryable).
//
// A MaxRetries of 0 means fn runs exactly once with no retry (the driver-level
// busy_timeout is the only bound).
func retryOnBusy(ctx context.Context, cfg domain.BusyRetryConfig, fn func() error) error {
	var lastErr error
	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		// Respect context cancellation between retries.
		if err := ctx.Err(); err != nil {
			return err
		}

		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		if !IsSQLiteBusy(lastErr) {
			return lastErr // non-retryable: return immediately
		}

		// Compute backoff for the next attempt (if any remain).
		if attempt < cfg.MaxRetries {
			backoff := computeBackoff(cfg, attempt)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}
	}
	return lastErr
}

// computeBackoff returns the backoff duration for the given attempt index,
// using exponential growth capped at MaxBackoff, plus jitter.
func computeBackoff(cfg domain.BusyRetryConfig, attempt int) time.Duration {
	// Exponential: base * 2^attempt, capped at MaxBackoff.
	backoff := cfg.BaseBackoff << uint(attempt)
	if backoff > cfg.MaxBackoff || backoff < 0 {
		backoff = cfg.MaxBackoff
	}
	// Jitter: ±(JitterFactor * backoff).
	if cfg.JitterFactor > 0 && cfg.JitterFactor <= 1 && backoff > 0 {
		delta := float64(backoff) * cfg.JitterFactor
		jitter := time.Duration(rand.Float64()*2*delta - delta)
		backoff += jitter
		if backoff < 0 {
			backoff = 0
		}
	}
	return backoff
}
