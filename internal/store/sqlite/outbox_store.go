// Package sqlite implements the SQLite memory store for Cortex.
//
// This file provides the transactional outbox store (ADR-04, W4, REQ-EMB-002).
// The outbox commits embed+upsert intents in the SAME transaction as the
// observation write; the embedding worker leases and processes them
// asynchronously with retry, capped backoff, and dead-letter for terminal
// failures.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// Outbox status constants. Stored in index_outbox.status.
const (
	OutboxStatusPending    = "pending"
	OutboxStatusLeased     = "leased"
	OutboxStatusComplete   = "complete"
	OutboxStatusDeadLetter = "dead_letter"
)

// OutboxDefaultMaxAttempts is the default retry cap for embed intents.
const OutboxDefaultMaxAttempts = 5

// OutboxIntent is a single durable embed+upsert intent in the outbox.
type OutboxIntent struct {
	ID            int64
	ObservationID int64
	Intent        string
	ModelInfo     string
	Status        string
	Attempts      int
	MaxAttempts   int
	NextRetryAt   sql.NullString
	LeasedAt      sql.NullString
	CompletedAt   sql.NullString
	Error         sql.NullString
	CreatedAt     string
}

// OutboxStore reads and writes the index_outbox table. It implements
// domain.TxParticipant so the enqueue can be enlisted in the shared UnitOfWork
// transaction alongside the observation write (REQ-EMB-002 atomicity).
type OutboxStore struct {
	db *sql.DB
}

// NewOutboxStore creates an outbox store backed by the given database. The
// database must have the index_outbox table (created by migrations/v2/001_init.sql).
func NewOutboxStore(db *sql.DB) *OutboxStore {
	return &OutboxStore{db: db}
}

// Ensure OutboxStore implements domain.TxParticipant (W4 adoption).
var _ domain.TxParticipant = (*OutboxStore)(nil)

// WithinTx implements domain.TxParticipant. It type-asserts the handle to
// *sql.Tx, stashes it into the context under the same txKey used by Store, and
// invokes fn within that context. The fn closure can then call EnqueueInTx,
// which reads the shared tx via txFromContext.
//
// WithinTx does NOT begin, commit, or roll back the transaction — the
// UnitOfWork that owns the shared tx is responsible for its lifecycle.
func (s *OutboxStore) WithinTx(ctx context.Context, handle any, fn func(context.Context) error) error {
	tx, ok := handle.(*sql.Tx)
	if !ok {
		return fmt.Errorf("outbox store: WithinTx expected *sql.Tx handle, got %T", handle)
	}
	return fn(context.WithValue(ctx, txKey{}, tx))
}

// EnqueueInTx inserts a pending embed+upsert intent into index_outbox using the
// shared transaction previously stashed in the context by WithinTx. It MUST be
// called from within a WithinTx closure (or any context that carries a txKey).
// The intent is committed atomically with the observation write (REQ-EMB-002).
func (s *OutboxStore) EnqueueInTx(ctx context.Context, observationID int64, intent, modelInfo string) error {
	tx := txFromContext(ctx)
	if tx == nil {
		return fmt.Errorf("outbox store: EnqueueInTx requires an active shared transaction (call within WithinTx)")
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO index_outbox (observation_id, intent, model_info, status, attempts, max_attempts)
		VALUES (?, ?, ?, ?, 0, ?)
	`, observationID, intent, nullableString(modelInfo), OutboxStatusPending, OutboxDefaultMaxAttempts)
	if err != nil {
		return fmt.Errorf("outbox store: enqueue intent: %w", err)
	}
	return nil
}

// Lease atomically claims up to limit pending intents whose retry delay has
// elapsed. Claimed intents transition to 'leased', have leased_at set to now,
// and attempts incremented (each lease counts as one processing attempt).
// Returns the claimed intents in id order.
func (s *OutboxStore) Lease(ctx context.Context, limit int) ([]OutboxIntent, error) {
	if limit <= 0 {
		limit = 1
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("outbox store: lease begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// 1. Select eligible IDs (pending, retry-eligible).
	rows, err := tx.QueryContext(ctx, `
		SELECT id FROM index_outbox
		WHERE status = ?
		  AND (next_retry_at IS NULL OR datetime(next_retry_at) <= datetime('now'))
		ORDER BY id
		LIMIT ?
	`, OutboxStatusPending, limit)
	if err != nil {
		return nil, fmt.Errorf("outbox store: lease select: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("outbox store: lease scan id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("outbox store: lease rows: %w", err)
	}
	_ = rows.Close()

	if len(ids) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("outbox store: lease commit (empty): %w", err)
		}
		return nil, nil
	}

	// 2. Atomically claim: status=leased, leased_at=now, attempts+1.
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+1)
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	query := fmt.Sprintf(`
		UPDATE index_outbox
		SET status = ?, leased_at = datetime('now'), attempts = attempts + 1
		WHERE id IN (%s)
	`, strings.Join(placeholders, ","))
	updateArgs := append([]any{OutboxStatusLeased}, args...)
	if _, err := tx.ExecContext(ctx, query, updateArgs...); err != nil {
		return nil, fmt.Errorf("outbox store: lease update: %w", err)
	}

	// 3. Read back the full leased rows.
	selectQuery := fmt.Sprintf(`
		SELECT id, observation_id, intent, model_info, status, attempts, max_attempts,
		       next_retry_at, leased_at, completed_at, error, created_at
		FROM index_outbox
		WHERE id IN (%s)
		ORDER BY id
	`, strings.Join(placeholders, ","))
	intentRows, err := tx.QueryContext(ctx, selectQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("outbox store: lease readback: %w", err)
	}
	intents, err := scanOutboxIntents(intentRows)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("outbox store: lease commit: %w", err)
	}
	return intents, nil
}

// MarkComplete transitions an intent to 'complete' with completed_at set.
func (s *OutboxStore) MarkComplete(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE index_outbox
		SET status = ?, completed_at = datetime('now')
		WHERE id = ?
	`, OutboxStatusComplete, id)
	if err != nil {
		return fmt.Errorf("outbox store: mark complete: %w", err)
	}
	return nil
}

// MarkFailed records a processing failure. If the intent's current attempts
// count has reached its max_attempts, the intent is dead-lettered (terminal);
// otherwise it transitions back to 'pending' with next_retry_at set for capped
// backoff and the failure cause stored in the error column.
func (s *OutboxStore) MarkFailed(ctx context.Context, id int64, cause error, nextRetryAt time.Time) error {
	retryStr := nextRetryAt.UTC().Format(time.RFC3339Nano)
	errStr := ""
	if cause != nil {
		errStr = cause.Error()
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE index_outbox
		SET status = CASE WHEN attempts >= max_attempts THEN ? ELSE ? END,
		    next_retry_at = CASE WHEN attempts >= max_attempts THEN next_retry_at ELSE ? END,
		    error = ?
		WHERE id = ?
	`, OutboxStatusDeadLetter, OutboxStatusPending, retryStr, errStr, id)
	if err != nil {
		return fmt.Errorf("outbox store: mark failed: %w", err)
	}
	return nil
}

// DeadLetter explicitly transitions an intent to 'dead_letter' (terminal). Used
// for non-retryable failures (e.g. model not found, dimension mismatch).
func (s *OutboxStore) DeadLetter(ctx context.Context, id int64, cause error) error {
	errStr := ""
	if cause != nil {
		errStr = cause.Error()
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE index_outbox
		SET status = ?, error = ?
		WHERE id = ?
	`, OutboxStatusDeadLetter, errStr, id)
	if err != nil {
		return fmt.Errorf("outbox store: dead letter: %w", err)
	}
	return nil
}

// PendingCount returns the number of non-terminal intents (pending + leased).
// Used for saturation/overload detection in the save path (REQ-EMB-001).
func (s *OutboxStore) PendingCount(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM index_outbox
		WHERE status IN (?, ?)
	`, OutboxStatusPending, OutboxStatusLeased).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("outbox store: pending count: %w", err)
	}
	return count, nil
}

// RecoverPending resets all 'leased' intents back to 'pending'. Called on
// startup to recover intents claimed by a worker that died before completing
// (crash recovery, REQ-EMB-001).
func (s *OutboxStore) RecoverPending(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE index_outbox
		SET status = ?
		WHERE status = ?
	`, OutboxStatusPending, OutboxStatusLeased)
	if err != nil {
		return fmt.Errorf("outbox store: recover pending: %w", err)
	}
	return nil
}

// UpdateIndexState upserts a row into index_state for the given namespace,
// recording vector coverage and parity (namespace tracking, ADR-04 §F).
func (s *OutboxStore) UpdateIndexState(ctx context.Context, namespace string, coverage float64, parity int) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO index_state (namespace, coverage, parity, updated_at)
		VALUES (?, ?, ?, datetime('now'))
		ON CONFLICT(namespace) DO UPDATE SET
			coverage = excluded.coverage,
			parity = excluded.parity,
			updated_at = datetime('now')
	`, namespace, coverage, parity)
	if err != nil {
		return fmt.Errorf("outbox store: update index_state: %w", err)
	}
	return nil
}

// scanOutboxIntents scans a *sql.Rows into a slice of OutboxIntent and closes
// the rows.
func scanOutboxIntents(rows *sql.Rows) ([]OutboxIntent, error) {
	defer func() { _ = rows.Close() }()
	var intents []OutboxIntent
	for rows.Next() {
		var in OutboxIntent
		var modelInfo sql.NullString
		if err := rows.Scan(
			&in.ID, &in.ObservationID, &in.Intent, &modelInfo, &in.Status,
			&in.Attempts, &in.MaxAttempts, &in.NextRetryAt, &in.LeasedAt,
			&in.CompletedAt, &in.Error, &in.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("outbox store: scan intent: %w", err)
		}
		in.ModelInfo = modelInfo.String
		intents = append(intents, in)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("outbox store: iterate intents: %w", err)
	}
	return intents, nil
}
