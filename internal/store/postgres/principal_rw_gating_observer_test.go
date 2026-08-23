//go:build postgres_integration

package postgres

// This file is deliberately diagnostic-only.  The observer is never consulted
// by the C32 oracle and is entirely absent unless explicitly opted in.
import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lleontor705/cortex/v2/testutil/postgrestest"
)

var rwC32ObserverPrefixOnce sync.Once
var rwC32ObserverPrefixValue string

func rwC32ObserverRunPrefix() string {
	rwC32ObserverPrefixOnce.Do(func() {
		rwC32ObserverPrefixValue = fmt.Sprintf("cortex_rwproof_obs_%d_%d_", time.Now().UnixNano(), os.Getpid())
	})
	return rwC32ObserverPrefixValue
}

type rwC32ObserverRun struct{ diagnostic *postgrestest.Diagnostic }

type rwC32PGXConn struct{ *pgx.Conn }

type rwC32PGXRows struct{ pgx.Rows }

func (r rwC32PGXRows) FieldDescriptions() []pgconn.FieldDescription {
	return r.Rows.FieldDescriptions()
}

func (c rwC32PGXConn) Query(ctx context.Context, sql string, args ...any) (postgrestest.Rows, error) {
	rows, err := c.Conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return rwC32PGXRows{rows}, nil
}

func (c rwC32PGXConn) Close(ctx context.Context) error { return c.Conn.Close(ctx) }

func rwC32PostgresObserverFactory(dsn string) postgrestest.ConnFactory {
	return func(ctx context.Context) (postgrestest.QueryConn, error) {
		cfg, err := pgx.ParseConfig(dsn)
		if err != nil {
			return nil, err
		}
		conn, err := pgx.ConnectConfig(ctx, cfg)
		if err != nil {
			return nil, err
		}
		return rwC32PGXConn{conn}, nil
	}
}

func rwC32PoolerObserverFactory(dsn string) postgrestest.ConnFactory {
	return func(ctx context.Context) (postgrestest.QueryConn, error) {
		cfg, err := postgrestest.PGXPoolerConfig(dsn)
		if err != nil {
			return nil, err
		}
		conn, err := pgx.ConnectConfig(ctx, cfg)
		if err != nil {
			return nil, err
		}
		return rwC32PGXConn{conn}, nil
	}
}

func TestRWC32ObserverFactoriesUseDistinctTargets(t *testing.T) {
	const postgresDSN = "postgres://admin:secret@localhost:5432/cortex_rwproof?sslmode=disable"
	const poolerDSN = "postgres://admin:secret@localhost:6432/cortex_rwproof?sslmode=disable"
	postgresCfg, err := pgx.ParseConfig(postgresDSN)
	if err != nil {
		t.Fatal(err)
	}
	poolerCfg, err := postgrestest.PGXPoolerConfig(poolerDSN)
	if err != nil {
		t.Fatal(err)
	}
	if postgresCfg.Database != "cortex_rwproof" || postgresCfg.Host != "localhost" || postgresCfg.Port != 5432 {
		t.Fatalf("postgres observer target changed: host=%q port=%d database=%q", postgresCfg.Host, postgresCfg.Port, postgresCfg.Database)
	}
	if postgresCfg.DefaultQueryExecMode == pgx.QueryExecModeSimpleProtocol {
		t.Fatal("postgres observer must retain the normal extended protocol")
	}
	if poolerCfg.Database != "pgbouncer" || poolerCfg.Port != 6432 {
		t.Fatalf("pooler observer target=%s:%d/%q", poolerCfg.Host, poolerCfg.Port, poolerCfg.Database)
	}
	if poolerCfg.DefaultQueryExecMode != pgx.QueryExecModeSimpleProtocol {
		t.Fatal("pooler observer must use simple protocol")
	}
}

func newRWC32Observer(t *testing.T, dbName string) *rwC32ObserverRun {
	t.Helper()
	if !postgrestest.DiagnosticEnabled() {
		return nil
	}
	prefix := rwC32ObserverRunPrefix()
	d := postgrestest.NewDiagnostic(postgrestest.Config{
		RunPrefix: prefix,
		Source:    "principal-rw-gating-observer",
		Interval:  postgrestest.DiagnosticInterval,
		// These are the already-authorized administrative/data DSNs.  The
		// observer never uses the rw application password or report credentials.
		Postgres: rwC32PostgresObserverFactory(os.Getenv("CORTEX_SPIKE_PG_ADMIN_DSN")),
		Pooler:   rwC32PoolerObserverFactory(os.Getenv("CORTEX_SPIKE_PGBOUNCER_DSN")),
	})
	return &rwC32ObserverRun{diagnostic: d}
}

func (r *rwC32ObserverRun) Start(ctx context.Context, pool *pgxpool.Pool) {
	if r == nil || r.diagnostic == nil {
		return
	}
	off := rwC32ObserverRTT(ctx, pool)
	_ = r.diagnostic.Start(ctx, off, nil)
	firstCtx, cancel := context.WithTimeout(ctx, postgrestest.DiagnosticFirstSampleTimeout)
	defer cancel()
	if !r.diagnostic.WaitFirstSample(firstCtx) {
		return
	}
	on := rwC32ObserverRTT(ctx, pool)
	r.diagnostic.SetMeasuredOverhead(off, on)
}

func (r *rwC32ObserverRun) Phase(p postgrestest.Phase) {
	if r != nil && r.diagnostic != nil {
		r.diagnostic.RecordPhase(p)
	}
}

func (r *rwC32ObserverRun) Stop() {
	if r != nil && r.diagnostic != nil {
		_ = r.diagnostic.Stop()
	}
}

func (r *rwC32ObserverRun) Emit(emit func(string)) error {
	if r == nil || r.diagnostic == nil {
		return nil
	}
	return r.diagnostic.EmitReport(emit)
}

// rwC32ObserverRTTPair measures paired no-op RTTs before sampling starts.  A
// failure produces an incomplete gate; it is diagnostic BLOCKED/INCONCLUSIVE,
// never a protocol verdict.
func rwC32ObserverRTT(ctx context.Context, pool *pgxpool.Pool) []time.Duration {
	if pool == nil {
		return nil
	}
	out := make([]time.Duration, 0, 32)
	for i := 0; i < 32; i++ {
		started := time.Now()
		var value int
		if err := pool.QueryRow(ctx, "SELECT 1").Scan(&value); err != nil {
			return nil
		}
		out = append(out, time.Since(started))
	}
	return out
}
