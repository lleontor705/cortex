package postgrestest

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// PGXFactory creates one persistent pgx connection. Observer caches the
// returned connection, so samples share a session and terminal shutdown owns
// its close; callers never need to expose a DSN in observer output.
func PGXFactory(dsn string) ConnFactory {
	return func(ctx context.Context) (QueryConn, error) {
		if dsn == "" {
			return nil, fmt.Errorf("missing PostgreSQL DSN")
		}
		conn, err := pgx.Connect(ctx, dsn)
		if err != nil {
			return nil, fmt.Errorf("connect PostgreSQL observer: %w", err)
		}
		return pgxQueryConn{Conn: conn}, nil
	}
}

// PGXPoolerFactory uses pgx's simple protocol, which is required by the
// PgBouncer administrative database.  Extended-protocol prepared statements
// are not supported by that database.
func PGXPoolerFactory(dsn string) ConnFactory {
	return func(ctx context.Context) (QueryConn, error) {
		if dsn == "" {
			return nil, fmt.Errorf("missing PgBouncer DSN")
		}
		cfg, err := PGXPoolerConfig(dsn)
		if err != nil {
			return nil, fmt.Errorf("parse PgBouncer DSN: %w", err)
		}
		cfg.Database = "pgbouncer"
		cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
		conn, err := pgx.ConnectConfig(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("connect PgBouncer observer: %w", err)
		}
		return pgxQueryConn{Conn: conn}, nil
	}
}

// PGXPoolerConfig derives the administrative connection from the supplied
// pooler DSN. The caller's database is intentionally not used for SHOW
// commands; it remains the observer's row-filter prefix instead.
func PGXPoolerConfig(dsn string) (*pgx.ConnConfig, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse PgBouncer DSN: %w", err)
	}
	cfg.Database = "pgbouncer"
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	return cfg, nil
}

type pgxQueryConn struct{ *pgx.Conn }

func (c pgxQueryConn) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	return c.Conn.Query(ctx, sql, args...)
}
