package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lleontor705/cortex/v2/internal/authz"
)

// AuditSink persists authorization decisions without recording request content,
// token material, or other secrets. Each event is bound to the same verified
// principal as the server store and is tenant-isolated by PostgreSQL RLS.
type AuditSink struct {
	pool                          *pgxpool.Pool
	principalSubject, grantDigest string
	grantVersion                  int64
}

func NewAuditSink(pool *pgxpool.Pool, principalSubject, grantDigest string, grantVersion int64) (*AuditSink, error) {
	if pool == nil || principalSubject == "" || grantDigest == "" || grantVersion <= 0 {
		return nil, fmt.Errorf("postgres audit: verified principal is required")
	}
	return &AuditSink{pool: pool, principalSubject: principalSubject, grantDigest: grantDigest, grantVersion: grantVersion}, nil
}

func (a *AuditSink) Record(ctx context.Context, e authz.AuditEvent) error {
	tx, err := a.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SELECT public.cortex_bind_principal($1::uuid,$2::text,$3::bigint)`, a.principalSubject, a.grantDigest, a.grantVersion); err != nil {
		return err
	}
	metadata, _ := json.Marshal(map[string]any{"reason": e.Reason, "allowed": e.Allowed})
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(tenant_id,actor_subject,action,resource_type,resource_id,correlation_id,reason,allowed,metadata,event_hash) VALUES(public.cortex_current_tenant(),$1,$2,$3,$4,$5,$6,$7,$8::jsonb,digest($8::text,'sha256'))`, e.Actor, e.Action, e.Resource, e.ResourceID, e.CorrelationID, e.Reason, e.Allowed, string(metadata)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
