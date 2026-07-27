// Package bundle provides the Stores struct that bundles all store dependencies.
// This avoids circular imports between app and mcp packages.
package bundle

import (
	"context"
	"database/sql"
	"errors"
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
	sqlite "modernc.org/sqlite"
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

	// Outbox is the transactional embed+upsert outbox (ADR-04, W4). It is nil
	// in zero-embedding mode (Embeddings == nil) or when vector search is not
	// available. When non-nil alongside UnitOfWork, the save path enqueues embed
	// intents atomically with the observation write (REQ-EMB-002).
	Outbox *sqlitestore.OutboxStore

	// Worker is the durable embedding worker handle (ADR-04, W4.2). It is nil in
	// zero-embedding mode or when vector search is unavailable. Exposing it on
	// the bundle lets status, health checks, and future waves access the worker
	// through the composition root rather than reaching into App internals. The
	// worker's lifecycle (Start/Drain) is still owned by App; callers MUST NOT
	// call Start on a worker returned here (it is already running).
	//
	// The worker is ALSO the single source of truth for outbox saturation
	// (Worker.IsSaturated, backed by WorkerConfig.MaxBacklog). SaveWithEmbedIntent
	// consults it directly — there is no duplicated bundle-side threshold.
	Worker *embedding.Worker

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
// The participants slice is validated before fn runs: every entry must be
// non-nil. A nil entry is a programming error (the caller declared a
// participant but passed nil). This gives the parameter meaning without
// changing the enlistment model: fn is still responsible for enlisting each
// participant via participant.WithinTx(ctx, TxHandle(ctx), work). The shared
// *sql.Tx ensures all participant writes commit or roll back atomically.
func (u *SQLiteUnitOfWork) Do(ctx context.Context, _ *domain.TenantContext, participants []domain.TxParticipant, fn func(context.Context) error) error {
	// Validate declared participants: a nil entry is a programming error.
	for i, p := range participants {
		if p == nil {
			return fmt.Errorf("unitOfWork: participant at index %d is nil (programming error)", i)
		}
	}
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
			// Shared transaction rolled back atomically — all participant writes
			// undone in one Rollback() (single *sql.Tx, no per-participant ordering).
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

// SQLite primary result codes (stable C-API constants, never change across
// SQLite versions). Used for typed-error detection via errors.As.
const (
	sqliteResultBusy   = 5 // SQLITE_BUSY
	sqliteResultLocked = 6 // SQLITE_LOCKED
)

// busyErrorSubstrings are the lowercase substrings that identify a SQLite
// SQLITE_BUSY / SQLITE_LOCKED condition. This is the FALLBACK detection path,
// used only when the error is not a typed *sqlite.Error (e.g., wrapped or
// re-formatted by an intermediary layer). Each entry corresponds to a genuine
// BUSY/LOCKED indicator — NEVER to SQLITE_CANTOPEN (code 14, "unable to open
// database file"), which is a non-retryable configuration error.
var busyErrorSubstrings = []string{
	"sqlite_busy",
	"sqlite_locked",
	"sqlcode 5", // SQLITE_BUSY result code
	"sqlcode 6", // SQLITE_LOCKED result code
	"database is locked",
	"database table is locked",
}

// IsSQLiteBusy reports whether err represents a SQLITE_BUSY / SQLITE_LOCKED
// condition. This is the stable, retryable signal callers check after
// UnitOfWork.Do returns an error (REQ-TX-002 edge scenario).
//
// Detection uses two paths:
//  1. PRIMARY: typed detection via errors.As against *sqlite.Error. If the
//     error carries a typed code, the primary code (lower 8 bits, handling
//     extended result codes) is compared against SQLITE_BUSY (5) and
//     SQLITE_LOCKED (6). This is robust against driver message-format changes.
//  2. FALLBACK: case-insensitive substring matching against
//     busyErrorSubstrings, for errors that are not typed *sqlite.Error.
func IsSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	// PRIMARY: typed detection.
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		// Extended result codes carry the primary code in the lower 8 bits.
		primary := sqliteErr.Code() & 0xFF
		return primary == sqliteResultBusy || primary == sqliteResultLocked
	}
	// FALLBACK: string matching.
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

// ---------------------------------------------------------------------------
// SaveWithEmbedIntent — atomic observation + outbox intent (ADR-04, W4,
// REQ-EMB-002)
// ---------------------------------------------------------------------------

// SaveWithEmbedIntent saves an observation. When the outbox and UnitOfWork are
// wired (non-nil), it also enqueues an embed+upsert intent in the SAME
// transaction as the observation write — the intent commits atomically with the
// observation, so the embedding worker can process it asynchronously with full
// durability (REQ-EMB-002 transactional outbox).
//
// When the outbox or UnitOfWork is nil (zero-embedding mode, or vector search
// unavailable), it performs a standalone Save with NO outbox activity — the
// zero-embedding local path is byte-for-byte unchanged (REQ-EMB-001 non-goal).
//
// Saturation: the worker is the single authoritative source of the saturation
// threshold (WorkerConfig.MaxBacklog, consulted via Worker.IsSaturated). When
// the worker reports saturation, the save fails-closed — the transaction never
// begins and the caller sees the error. No embedding work is silently accepted
// under overload (REQ-EMB-001). When the worker is absent (Worker == nil), no
// saturation gate is applied: in production the worker and outbox are always
// paired, so this only affects test wiring and preserves zero-worker behavior.
func SaveWithEmbedIntent(ctx context.Context, stores *Stores, obs *domain.Observation) error {
	// Zero-embedding / unwired path: standalone save, no outbox activity.
	if stores.Outbox == nil || stores.UnitOfWork == nil {
		return stores.Observations.Save(ctx, obs)
	}

	// Saturation check via the worker's authoritative state (single source of
	// truth — no duplicated bundle-side constant). Fail-closed under overload
	// (REQ-EMB-001). Skipped when the worker is absent (zero-worker behavior).
	if stores.Worker != nil {
		saturated, err := stores.Worker.IsSaturated(ctx)
		if err != nil {
			return fmt.Errorf("bundle: check embed backlog: %w", err)
		}
		if saturated {
			return fmt.Errorf("bundle: embedding backlog saturated (worker reports overload)")
		}
	}

	// Transactional outbox: save observation + enqueue intent atomically.
	// When SaveInTx signals a dedup skip (ClassDedupSkipped), the outbox
	// enqueue is SKIPPED (no new observation to embed) and the dedup increment
	// commits within the shared tx (REQ-MCPH-002). The caller receives
	// domain.NewDedupSkipped so it can classify the outcome.
	modelInfo := ""
	if stores.Embeddings != nil {
		modelInfo = stores.Embeddings.Model()
	}
	var wasDedup bool
	err := stores.UnitOfWork.Do(ctx, nil, []domain.TxParticipant{stores.Observations, stores.Outbox}, func(txCtx context.Context) error {
		// Participant 1: save the observation within the shared tx.
		if err := stores.Observations.WithinTx(txCtx, TxHandle(txCtx), func(c context.Context) error {
			return stores.Observations.SaveInTx(c, obs)
		}); err != nil {
			if domain.IsClass(err, domain.ClassDedupSkipped) {
				// Dedup: the duplicate_count increment already happened in
				// the shared tx. Return nil so the tx COMMITS (preserving
				// the increment). Skip the outbox (no new observation).
				wasDedup = true
				return nil
			}
			return err
		}
		// Participant 2: enqueue the embed intent within the SAME shared tx.
		return stores.Outbox.WithinTx(txCtx, TxHandle(txCtx), func(c context.Context) error {
			return stores.Outbox.EnqueueInTx(c, obs.ID, "embed_upsert", modelInfo)
		})
	})
	if err != nil {
		return err
	}
	if wasDedup {
		return domain.NewDedupSkipped("duplicate observation skipped (normalized_hash match)")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Request-scoped feedback wiring (W5.1, REQ-RET-001)
// ---------------------------------------------------------------------------

// WireSearchFeedback connects the search store's request-scoped feedback
// attribution to the observation store's persistence layer. After wiring,
// search.Store.RecordFeedback persists feedback via
// Observations.RecordSearchFeedback, attributed to the originating SearchID's
// query — never a shared global.
//
// When stores, Search, or Observations is nil, this is a safe no-op (feedback
// stays disabled rather than falling back to any shared state). When the sink
// is not wired, RecordFeedback validates the SearchID but performs no
// persistence (REQ-RET-001: record-against-known-SearchID only).
//
// This replaces the removed shared mutable search-query field, which raced
// under concurrent searches and could misattribute feedback to whichever
// search ran last.
func WireSearchFeedback(stores *Stores) {
	if stores == nil || stores.Search == nil || stores.Observations == nil {
		return
	}
	observations := stores.Observations
	stores.Search.SetFeedbackSink(func(ctx context.Context, _ domain.SearchID, query string, observationID int64, rankPosition int) error {
		return observations.RecordSearchFeedback(ctx, query, observationID, rankPosition)
	})
}
