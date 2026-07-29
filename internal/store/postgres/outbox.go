package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	OutboxStatusPending    = "pending"
	OutboxStatusLeased     = "leased"
	OutboxStatusComplete   = "complete"
	OutboxStatusDeadLetter = "dead_letter"
)

type OutboxIntent struct {
	ID            int64
	ObservationID int64
	Intent        string
	Status        string
	Attempts      int
	AvailableAt   time.Time
}
type OutboxStore struct{ *Store }

func (s *Store) Outbox() *OutboxStore { return &OutboxStore{s} }
func (r *OutboxStore) EnqueueInTx(ctx context.Context, id int64, intent, model string) error {
	tx, ok := txFromContext(ctx)
	if !ok {
		return fmt.Errorf("postgres outbox: active transaction required")
	}
	_, err := tx.Exec(ctx, `INSERT INTO index_outbox(tenant_id,observation_id,intent,status,created_by,updated_by) VALUES(public.cortex_current_tenant(),$1,$2,'pending',NULLIF($3,'')::uuid,NULLIF($3,'')::uuid)`, id, intent, r.principal.Subject)
	return err
}
func txFromContext(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey{}).(pgx.Tx)
	return tx, ok
}

type txKey struct{}

func (r *OutboxStore) WithinTx(ctx context.Context, h any, fn func(context.Context) error) error {
	tx, ok := h.(pgx.Tx)
	if !ok {
		return fmt.Errorf("postgres outbox: expected pgx.Tx, got %T", h)
	}
	return fn(context.WithValue(ctx, txKey{}, tx))
}
func (r *OutboxStore) Lease(ctx context.Context, limit int) (out []OutboxIntent, err error) {
	if limit <= 0 {
		limit = 20
	}
	err = r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `WITH claimed AS (SELECT id FROM index_outbox WHERE status='pending' AND available_at<=now() ORDER BY id FOR UPDATE SKIP LOCKED LIMIT $1) UPDATE index_outbox o SET status='leased',attempts=o.attempts+1,updated_at=now() FROM claimed c WHERE o.id=c.id RETURNING o.id,o.observation_id,o.intent,o.status,o.attempts,o.available_at`, limit)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var x OutboxIntent
			if e := rows.Scan(&x.ID, &x.ObservationID, &x.Intent, &x.Status, &x.Attempts, &x.AvailableAt); e != nil {
				return e
			}
			out = append(out, x)
		}
		return rows.Err()
	})
	return
}
func (r *OutboxStore) MarkComplete(ctx context.Context, id int64) error {
	return r.updateStatus(ctx, id, OutboxStatusComplete)
}
func (r *OutboxStore) MarkFailed(ctx context.Context, id int64, cause error, next time.Time) error {
	return r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `UPDATE index_outbox SET status='pending',available_at=$1,updated_at=now() WHERE id=$2`, next, id)
		return e
	})
}
func (r *OutboxStore) DeadLetter(ctx context.Context, id int64, cause error) error {
	return r.updateStatus(ctx, id, OutboxStatusDeadLetter)
}
func (r *OutboxStore) updateStatus(ctx context.Context, id int64, status string) error {
	return r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `UPDATE index_outbox SET status=$1,updated_at=now() WHERE id=$2`, status, id)
		return e
	})
}
func (r *OutboxStore) PendingCount(ctx context.Context) (n int, err error) {
	err = r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM index_outbox WHERE status IN ('pending','leased')`).Scan(&n)
	})
	return
}
func (r *OutboxStore) RecoverPending(ctx context.Context) error {
	return r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `UPDATE index_outbox SET status='pending',updated_at=now() WHERE status='leased'`)
		return e
	})
}
