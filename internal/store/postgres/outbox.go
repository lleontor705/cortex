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
	LeaseOwner    string
	LeasedUntil   *time.Time
	Error         string
	CompletedAt   *time.Time
	AffectedRows  int
}
type OutboxStore struct{ *Store }

func (r *OutboxStore) EnqueueInTx(ctx context.Context, id int64, intent, model string) error {
	tx, ok := txFromContext(ctx)
	if !ok {
		return fmt.Errorf("postgres outbox: active transaction required")
	}
	_, err := tx.Exec(ctx, `INSERT INTO index_outbox(tenant_id,observation_id,intent,status,created_by,updated_by) VALUES(public.cortex_current_tenant(),$1,$2,'pending',$3,$3)`, id, intent, actorFromContext(ctx))
	return err
}

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
		owner := r.principal.Subject
		rows, e := tx.Query(ctx, `WITH claimed AS (SELECT id FROM index_outbox WHERE status='pending' AND available_at<=now() ORDER BY id FOR UPDATE SKIP LOCKED LIMIT $1) UPDATE index_outbox o SET status='leased',attempts=o.attempts+1,lease_owner=$2,leased_until=now()+interval '5 minutes',updated_at=now() FROM claimed c WHERE o.id=c.id RETURNING o.id,o.observation_id,o.intent,o.status,o.attempts,o.available_at,COALESCE(o.lease_owner,''),o.leased_until,COALESCE(o.error,''),o.completed_at,COALESCE(o.affected_rows,0)`, limit, owner)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var x OutboxIntent
			if e := rows.Scan(&x.ID, &x.ObservationID, &x.Intent, &x.Status, &x.Attempts, &x.AvailableAt, &x.LeaseOwner, &x.LeasedUntil, &x.Error, &x.CompletedAt, &x.AffectedRows); e != nil {
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
		_, e := tx.Exec(ctx, `UPDATE index_outbox SET status='pending',available_at=$1,error=$3,lease_owner=NULL,leased_until=NULL,updated_at=now() WHERE id=$2`, next, id, causeString(cause))
		return e
	})
}
func (r *OutboxStore) DeadLetter(ctx context.Context, id int64, cause error) error {
	return r.updateStatus(ctx, id, OutboxStatusDeadLetter)
}
func (r *OutboxStore) updateStatus(ctx context.Context, id int64, status string) error {
	return r.transaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `UPDATE index_outbox SET status=$1,completed_at=CASE WHEN $1='complete' THEN now() ELSE completed_at END,lease_owner=NULL,leased_until=NULL,updated_at=now() WHERE id=$2`, status, id)
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
		_, e := tx.Exec(ctx, `UPDATE index_outbox SET status='pending',lease_owner=NULL,leased_until=NULL,updated_at=now() WHERE status='leased' AND leased_until < now()`)
		return e
	})
}

func causeString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
